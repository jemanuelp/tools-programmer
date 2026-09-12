package lab

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const DefaultImage = "tools-programmer-lab:1.18.30"

type HostOptions struct {
	Source, Output, Image string
	Scenarios             []string
	Interactive           bool
	Timeout               time.Duration
}
type Docker interface {
	Run(context.Context, io.Reader, io.Writer, ...string) error
}
type dockerCLI struct{}

type commandFailure struct{ code int }

func (e *commandFailure) Error() string { return "docker_command" }
func commandExit(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	var failure *commandFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return -1
}

func startupContextCause(ctx context.Context, fallback string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	return fallback
}

// BuildFailure is an allowlisted projection, never the original Docker error or
// build log. The Docker exit and failed RUN process exit are separate facts.
type BuildFailure struct {
	Cause           string `json:"cause"`
	Stage           string `json:"stage"`
	ExitCode        int    `json:"exit_code"`
	ProcessExitCode int    `json:"process_exit_code,omitempty"`
	Vertex          int    `json:"vertex,omitempty"`
	Step            int    `json:"step,omitempty"`
	Steps           int    `json:"steps,omitempty"`
	BuildRef        string `json:"build_ref,omitempty"`
	Truncated       bool   `json:"truncated"`
}

func (e *BuildFailure) Error() string {
	s := fmt.Sprintf("lab_prepare_failed cause=%s docker_exit=%d stage=%s", e.Cause, e.ExitCode, e.Stage)
	if e.Step > 0 {
		s += fmt.Sprintf(" vertex=%d step=%d/%d", e.Vertex, e.Step, e.Steps)
	}
	if e.ProcessExitCode > 0 {
		s += fmt.Sprintf(" process_exit=%d", e.ProcessExitCode)
	}
	if e.Truncated {
		s += " diagnostics_truncated=true"
	}
	if e.BuildRef != "" {
		parts := strings.Split(e.BuildRef, "/")
		s += "; build_ref=" + e.BuildRef + "; inspect: docker buildx history inspect --builder " + parts[0] + " " + parts[2]
	}
	switch e.Cause {
	case "ca_read":
		s += "; check CA bundle readability and parent-directory traversal for _apt"
	case "tls_trust":
		s += "; check the pinned CA bootstrap and snapshot TLS trust"
	case "checksum_mismatch", "manifest_verification":
		s += "; verify the exact remote bytes and recorded digest; do not bypass verification"
	case "package_unavailable":
		s += "; check signed snapshot indexes and package availability"
	case "network", "dns":
		s += "; check explicit build-network reachability"
	case "storage_capacity":
		s += "; check available build storage"
	case "deadline":
		s += "; preparation exceeded its deadline"
	}
	return s
}

// PrepareDiagnostic never formats an arbitrary error containing local paths.
func PrepareDiagnostic(err error) string {
	var failure *BuildFailure
	if errors.As(err, &failure) {
		return failure.Error()
	}
	return "lab_prepare_failed cause=source_preparation; check snapshot eligibility and temporary storage"
}

type buildVertex struct {
	stage, cause string
	step, steps  int
}
type buildLog struct {
	mu                        sync.Mutex
	line                      []byte
	dropping, truncated       bool
	seen                      int
	vertices                  map[int]buildVertex
	failed, last, processExit int
	ref                       string
	globalCause               string
}

var buildHeader = regexp.MustCompile(`^#([0-9]{1,5}) \[ *(?:[A-Za-z0-9_.-]+ )?([0-9]{1,4})/([0-9]{1,4})\] (.*)$`)
var buildInternal = regexp.MustCompile(`^#([0-9]{1,5}) \[internal\] (.*)$`)
var buildLine = regexp.MustCompile(`^#([0-9]{1,5}) (.*)$`)
var buildExit = regexp.MustCompile(`exit code: ([0-9]{1,3})(?:[^0-9]|$)`)
var buildReference = regexp.MustCompile(`^View build details: docker-desktop://dashboard/build/([A-Za-z0-9_.-]{1,64}/[A-Za-z0-9_.-]{1,64}/[a-z0-9]{20,40})$`)

func (l *buildLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(p) > (256<<10)-l.seen {
		l.truncated = true
		l.seen = 256 << 10
	} else {
		l.seen += len(p)
	}
	for _, c := range p {
		if c == '\n' {
			if !l.dropping {
				l.parse(string(l.line))
			}
			l.line = l.line[:0]
			l.dropping = false
			continue
		}
		if l.dropping {
			continue
		}
		if len(l.line) == 8192 {
			l.truncated = true
			l.dropping = true
			l.line = l.line[:0]
			continue
		}
		l.line = append(l.line, c)
	}
	return len(p), nil
}
func buildStage(line string) string {
	switch {
	case strings.Contains(line, "apt-get"):
		return "apt"
	case strings.Contains(line, "cacert-"):
		return "ca_bootstrap"
	case strings.Contains(line, "npm ci"):
		return "plugin_dependencies"
	case strings.Contains(line, "go mod"):
		return "go_dependencies"
	case strings.Contains(line, "go build"):
		return "go_build"
	case strings.Contains(line, "curl "):
		return "toolchains"
	case strings.Contains(line, "FROM "):
		return "image_resolution"
	default:
		return "unknown"
	}
}
func buildCause(line string) string {
	s := strings.ToLower(line)
	switch {
	case strings.Contains(s, "could not load certificates from") && strings.Contains(s, "(cainfo option)"):
		return "ca_read"
	case strings.Contains(s, "no system certificates"), strings.Contains(s, "certificate verification failed"), strings.Contains(s, "certificate signed by unknown authority"):
		return "tls_trust"
	case strings.Contains(s, "checksum mismatch"), strings.Contains(s, "did not match expected digest"), strings.Contains(s, "checksum failed"), strings.Contains(s, "computed checksum did not match"):
		return "checksum_mismatch"
	case strings.Contains(s, "manifest verification failed"):
		return "manifest_verification"
	case strings.Contains(s, "no installation candidate"), strings.Contains(s, "unable to locate package"):
		return "package_unavailable"
	case strings.Contains(s, "temporary failure resolving"), strings.Contains(s, "could not resolve host"):
		return "dns"
	case strings.Contains(s, "could not connect"), strings.Contains(s, "connection refused"), strings.Contains(s, "i/o timeout"):
		return "network"
	case strings.Contains(s, "no space left on device"):
		return "storage_capacity"
	case strings.Contains(s, "permission denied"):
		return "permission_denied"
	case strings.Contains(s, "read-only file system"):
		return "read_only_filesystem"
	case strings.Contains(s, "module lookup disabled"), strings.Contains(s, "missing go.sum entry"):
		return "dependency_unavailable"
	default:
		return ""
	}
}
func (l *buildLog) parse(line string) {
	if l.vertices == nil {
		l.vertices = map[int]buildVertex{}
	}
	if m := buildReference.FindStringSubmatch(line); m != nil {
		l.ref = m[1]
		return
	}
	if m := buildInternal.FindStringSubmatch(line); m != nil {
		id, _ := strconv.Atoi(m[1])
		stage := "source_input"
		if strings.HasPrefix(m[2], "load metadata for ") {
			stage = "image_resolution"
		}
		if len(l.vertices) < 128 {
			l.vertices[id] = buildVertex{stage: stage}
			l.last = id
		} else {
			l.truncated = true
		}
		return
	}
	if m := buildHeader.FindStringSubmatch(line); m != nil {
		id, _ := strconv.Atoi(m[1])
		step, _ := strconv.Atoi(m[2])
		steps, _ := strconv.Atoi(m[3])
		if len(l.vertices) < 128 {
			l.vertices[id] = buildVertex{stage: buildStage(m[4]), step: step, steps: steps}
			l.last = id
		} else {
			l.truncated = true
		}
		return
	}
	id := l.last
	text := line
	if m := buildLine.FindStringSubmatch(line); m != nil {
		id, _ = strconv.Atoi(m[1])
		text = m[2]
	}
	v, ok := l.vertices[id]
	if !ok {
		if l.globalCause == "" {
			l.globalCause = buildCause(text)
		}
		return
	}
	if v.cause == "" {
		v.cause = buildCause(text)
		l.vertices[id] = v
	}
	if strings.HasPrefix(text, "ERROR:") {
		if l.failed == 0 {
			l.failed = id
		}
		if m := buildExit.FindStringSubmatch(text); m != nil && id == l.failed && l.processExit == 0 {
			l.processExit, _ = strconv.Atoi(m[1])
		}
	}
}
func (l *buildLog) failure(err, errorContext error) *BuildFailure {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.dropping && len(l.line) > 0 {
		l.parse(string(l.line))
	}
	l.line = nil
	id := l.failed
	if id == 0 {
		id = l.last
	}
	v := l.vertices[id]
	f := &BuildFailure{Cause: v.cause, Stage: v.stage, ExitCode: -1, ProcessExitCode: l.processExit, Vertex: id, Step: v.step, Steps: v.steps, BuildRef: l.ref, Truncated: l.truncated}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		f.ExitCode = exit.ExitCode()
	}
	if f.Cause == "" {
		f.Cause = l.globalCause
	}
	if f.Cause == "" {
		switch {
		case errors.Is(errorContext, context.DeadlineExceeded):
			f.Cause = "deadline"
		case errors.Is(errorContext, context.Canceled):
			f.Cause = "cancelled"
		case errors.Is(err, exec.ErrNotFound):
			f.Cause = "docker_unavailable"
		default:
			f.Cause = "build_failed"
		}
	}
	if f.Stage == "" {
		f.Stage = "unknown"
	}
	return f
}

func (dockerCLI) Run(ctx context.Context, in io.Reader, out io.Writer, args ...string) error {
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdin = in
	c.Stdout = out
	c.Stderr = io.Discard
	var log *buildLog
	if len(args) > 0 && args[0] == "build" {
		log = &buildLog{}
		c.Stdout = log
		c.Stderr = log
	}
	c.WaitDelay = time.Second
	// This is the host adapter only. It does not pass this environment to a container.
	if err := c.Run(); err != nil {
		if log != nil {
			return log.failure(err, ctx.Err())
		}
		return &commandFailure{commandExit(err)}
	}
	return nil
}
func dockerOutput(ctx context.Context, d Docker, args ...string) ([]byte, error) {
	b := &boundedBuffer{limit: 2 << 20}
	err := d.Run(ctx, nil, b, args...)
	return b.data, err
}
func newRunID() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

func ContainerArgs(image, run string, scenarios []string, interactive bool, timeout time.Duration) []string {
	args := []string{"create", "--name", "tools-programmer-lab-" + run, "--pull=never", "--platform=linux/amd64", "--network=none", "--user=10001:10001", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only", "--pids-limit=256", "--cpus=2", "--memory=4g", "--memory-swap=4g", "--init", "--log-driver=none", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=3g,mode=1777", "--env", "LAB_RUN=" + run, "--env", "LAB_TIMEOUT=" + timeout.String()}
	args = append(args, "--label", "tools-programmer.lab.run="+run)
	if interactive {
		args = append(args, "--interactive")
	}
	args = append(args, image, "--scenarios", strings.Join(scenarios, ","))
	if interactive {
		args = append(args, "--interactive")
	}
	return args
}

func Prepare(ctx context.Context, root, image string) error {
	tmp, err := os.MkdirTemp("", "tools-programmer-lab-build-")
	if err != nil {
		return errors.New("build_directory")
	}
	defer os.RemoveAll(tmp)
	if _, err := Snapshot(ctx, root, tmp); err != nil {
		return err
	}
	return (dockerCLI{}).Run(ctx, nil, io.Discard, "build", "--progress=plain", "--platform=linux/amd64", "--tag", image, "--file", filepath.Join(tmp, "lab/Dockerfile"), tmp)
}

// Only this exact static prompt may cross the live stdout boundary. Raw runtime
// stdout and stderr never reach the user's terminal or the report directory.
const humanPrompt = "LAB: Allow this fixture session's assignment once? Type allow or deny."
const sourcePrompt = "LAB: Source transfer ready."

type promptWriter struct {
	line        []byte
	out         io.Writer
	report      []byte
	source      func()
	decode      func([]byte) (Report, error)
	protocolErr error
	dropping    bool
}

func (p *promptWriter) Write(b []byte) (int, error) {
	for _, c := range b {
		if c == '\n' {
			if p.dropping {
				if bytes.HasPrefix(p.line, []byte("LAB_REPORT ")) && p.protocolErr == nil {
					p.protocolErr = errors.New("report_bound")
				}
				p.line = nil
				p.dropping = false
				continue
			}
			if string(p.line) == sourcePrompt && p.source != nil {
				p.source()
			}
			if string(p.line) == humanPrompt {
				fmt.Fprintln(p.out, humanPrompt)
			}
			if bytes.HasPrefix(p.line, []byte("LAB_REPORT ")) {
				frame := p.line[len("LAB_REPORT "):]
				err := errors.New("report_schema")
				if p.decode != nil {
					_, err = p.decode(frame)
				}
				if err == nil {
					p.report = append([]byte{}, frame...)
				} else if p.protocolErr == nil {
					p.protocolErr = err
				}
			}
			p.line = nil
		} else if len(p.line) < 1<<20 {
			p.line = append(p.line, c)
		} else {
			p.dropping = true
		}
	}
	return len(b), nil
}

func RunHost(ctx context.Context, opt HostOptions) (Report, error) {
	return runHost(ctx, opt, dockerCLI{})
}
func runHost(ctx context.Context, opt HostOptions, d Docker) (report Report, err error) {
	if _, err := Select(strings.Join(opt.Scenarios, ",")); err != nil {
		return report, err
	}
	if opt.Timeout < time.Second || opt.Timeout > 30*time.Minute {
		return report, errors.New("invalid_timeout")
	}
	if opt.Image == "" {
		opt.Image = DefaultImage
	}
	// No existing output directory is overwritten or mistaken for this run.
	if err := os.Mkdir(opt.Output, 0700); err != nil {
		return report, errors.New("output_must_be_new")
	}
	var tty *os.File
	if opt.Interactive {
		tty, err = os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return report, errors.New("interactive_tty_required")
		}
		defer tty.Close()
		if _, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS); err != nil {
			return report, errors.New("interactive_tty_required")
		}
	}
	outer, err := os.MkdirTemp("", "tools-programmer-lab-source-")
	if err != nil {
		return report, errors.New("source_directory")
	}
	defer os.RemoveAll(outer)
	tmp := filepath.Join(outer, "snapshot")
	if os.Mkdir(tmp, 0700) != nil {
		return report, errors.New("source_directory")
	}
	s, err := Snapshot(ctx, opt.Source, tmp)
	if err != nil {
		return report, err
	}
	archive, err := os.OpenFile(filepath.Join(outer, "source.tar"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return report, errors.New("source_archive_open")
	}
	defer archive.Close()
	if err := WriteSourceArchive(tmp, s, archive); err != nil {
		return report, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return report, errors.New("source_archive_seek")
	}
	run := newRunID()
	report = Report{Version: 1, Run: run, Commit: s.Commit, SourceHash: s.Hash, Dirty: s.Dirty, OpenCode: "1.18.30", Go: "go1.26.8", Node: "v24.18.0"}
	report.InputHash = inputHash(opt.Scenarios, opt.Interactive, opt.Timeout)
	report.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	ctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	image, err := dockerOutput(ctx, d, "image", "inspect", "--format", "{{.Id}}", opt.Image)
	if err != nil {
		return report, errors.New("image_not_prepared")
	}
	imageID := strings.TrimSpace(string(image))
	if !strings.HasPrefix(imageID, "sha256:") || !hash.MatchString(strings.TrimPrefix(imageID, "sha256:")) {
		return report, errors.New("image_identity")
	}
	report.Image = imageID
	id := ""
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		// A timed-out create can have succeeded. Recover only the exact random
		// name AND matching run label, then remove by the observed immutable ID.
		if id == "" {
			b, e := dockerOutput(cleanup, d, "inspect", "--format", `{{.Id}} {{index .Config.Labels "tools-programmer.lab.run"}}`, "tools-programmer-lab-"+run)
			parts := strings.Fields(string(b))
			if e == nil && len(parts) == 2 && hash.MatchString(parts[0]) && parts[1] == run {
				id = parts[0]
			}
		}
		removeErr := errors.New("container_cleanup_unconfirmed")
		if id != "" {
			removeErr = d.Run(cleanup, nil, io.Discard, "rm", "--force", id)
		}
		report.Cleaned = removeErr == nil
		if removeErr != nil && err == nil {
			err = errors.New("container_cleanup")
		}
		if err != nil && report.Startup == nil && len(report.Results) == 0 {
			report.Startup = &Startup{Stage: "bootstrap", Status: "failed", Cause: "container_exit", ExitCode: -1}
		}
		fillNotRun(&report, opt.Scenarios)
		if writeErr := WriteReport(opt.Output, report); writeErr != nil {
			err = errors.New("report_write")
		}
	}()
	createArgs := ContainerArgs(imageID, run, opt.Scenarios, opt.Interactive, opt.Timeout)
	// Insert only host-observed identity metadata before the image argument.
	for i, arg := range createArgs {
		if arg == imageID {
			metadata := []string{"--env", "LAB_SOURCE_HASH=" + s.Hash, "--env", "LAB_COMMIT=" + s.Commit, "--env", "LAB_INPUT_HASH=" + report.InputHash}
			createArgs = append(append(append([]string{}, createArgs[:i]...), metadata...), createArgs[i:]...)
			break
		}
	}
	created, createErr := dockerOutput(ctx, d, createArgs...)
	if createErr != nil {
		return report, errors.New("container_create")
	}
	id = strings.TrimSpace(string(created))
	if !hash.MatchString(id) {
		id = ""
		return report, errors.New("container_identity")
	}
	args := []string{"start", "--attach"}
	var in io.Reader
	ready := make(chan struct{}, 1)
	wire := &promptWriter{out: io.Discard, source: func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	}}
	wire.decode = func(frame []byte) (Report, error) {
		return decodeSelectedReport(frame, run, s.Hash, report.InputHash, opt.Scenarios)
	}
	out := io.Writer(wire)
	if tty != nil {
		args = append(args, "--interactive")
		in = tty
		wire.out = tty
	}
	args = append(args, id)
	transferCtx, stopTransfer := context.WithCancel(ctx)
	defer stopTransfer()
	transferred := make(chan error, 1)
	go func() {
		select {
		case <-ready:
			err := d.Run(transferCtx, archive, io.Discard, "exec", "--interactive", "--user=10001:10001", id, "/usr/local/bin/lab-bootstrap", "receive-source")
			transferred <- err
			if err != nil {
				stopTransfer()
			}
		case <-transferCtx.Done():
			transferred <- errors.New("source_handshake")
		}
	}()
	startErr := d.Run(transferCtx, in, out, args...)
	stopTransfer()
	transferErr := <-transferred
	// tmpfs disappears on container exit. Receive bounded report frames while
	// attached instead of copying stopped-container state or exporting raw logs.
	if len(wire.report) == 0 {
		report.Startup = &Startup{Stage: "bootstrap", Status: "failed", Cause: startupContextCause(ctx, "container_exit"), ExitCode: commandExit(startErr)}
		if wire.protocolErr != nil {
			report.ProtocolError = "protocol_error"
			report.Startup.Cause = "protocol_error"
			return report, wire.protocolErr
		}
		return report, errors.New("container_report_missing")
	}
	guest, e := wire.decode(wire.report)
	if e != nil {
		report.Startup = &Startup{Stage: "bootstrap", Status: "failed", Cause: "protocol_error", ExitCode: 1}
		return report, e
	}
	guest.Image = imageID
	guest.Commit = s.Commit
	guest.Dirty = s.Dirty
	report = guest
	if wire.protocolErr != nil {
		report.ProtocolError = "protocol_error"
	}
	if transferErr != nil {
		if report.Startup == nil || report.Startup.Status != "failed" {
			report.Startup = &Startup{Stage: "source_wait", Status: "failed", Cause: startupContextCause(ctx, "source_transfer"), ExitCode: 1}
		}
		return report, errors.New("source_transfer")
	}
	if report.Startup != nil && report.Startup.Status == "running" {
		report.Startup.Status = "failed"
		report.Startup.Cause = startupContextCause(ctx, "container_exit")
		report.Startup.ExitCode = commandExit(startErr)
	}
	fillNotRun(&report, opt.Scenarios)
	if wire.protocolErr != nil {
		return report, wire.protocolErr
	}
	if startErr != nil {
		allPass := true
		for _, result := range report.Results {
			allPass = allPass && result.Status == "pass"
		}
		if allPass {
			last := len(report.Results) - 1
			report.Results[last].Status = "fail"
			report.Results[last].Code = "container_exit_mismatch"
			return report, errors.New("container_exit_mismatch")
		}
	}
	return report, nil
}

func Bootstrap(ctx context.Context, args []string) error {
	started := time.Now()
	r := startupReport()
	out := io.Writer(os.Stdout)
	startupStage(&r, "source_wait", started, out)
	fail := func(cause string, err error) error { startupFailed(&r, cause, nil, false, started, out); return err }
	if os.Getuid() == 0 {
		return fail("nonroot_required", errors.New("nonroot_required"))
	}
	budget, err := time.ParseDuration(os.Getenv("LAB_TIMEOUT"))
	if err != nil || budget < time.Second || budget > 30*time.Minute {
		return fail("configuration_invalid", errors.New("invalid_timeout"))
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := privateDirs("/tmp"); err != nil {
		return fail("source_invalid", err)
	}
	fmt.Println(sourcePrompt)
	if err := wait(ctx, func() (bool, error) {
		_, err := os.Stat(sourceReady)
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	}); err != nil {
		cause := "source_invalid"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			cause = "deadline"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			cause = "cancelled"
		}
		return fail(cause, err)
	}
	startupStage(&r, "source_verify", started, out)
	if _, err := loadSource(); err != nil {
		return fail("source_invalid", err)
	}
	startupStage(&r, "lab_build", started, out)
	build := exec.CommandContext(ctx, "go", "build", "-p=2", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", "/tmp/lab-current", "./cmd/tools-programmer-lab")
	build.Dir = sourceRoot
	build.Env = append(privateEnv("/tmp"), "GOCACHE=/tmp/gocache")
	if failure := executeStartupBuild(ctx, build); failure != nil {
		startupFailed(&r, startupBuildCause(failure.Cause), &failure.ExitCode, failure.Truncated, started, out)
		return errors.New("offline_lab_build")
	}
	startupStage(&r, "lab_verify", started, out)
	if _, err := loadSource(); err != nil {
		return fail("source_invalid", err)
	}
	// Re-exec the exact requested runner too, not an old scenario implementation
	// baked into a previously prepared dependency image.
	startupStage(&r, "reexec", started, out)
	err = reexecGuest(args, os.Environ())
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return fail("exec_denied", err)
	}
	return fail("exec_failed", err)
}

func reexecGuest(args, env []string) error {
	return syscallExec("/tmp/lab-current", append([]string{"/tmp/lab-current", "guest"}, args...), env)
}
func executeStartupBuild(ctx context.Context, c *exec.Cmd) *BuildFailure {
	log := &buildLog{}
	c.Stdout = log
	c.Stderr = log
	c.WaitDelay = time.Second
	if err := c.Run(); err != nil {
		return log.failure(err, ctx.Err())
	}
	return nil
}
func startupBuildCause(cause string) string {
	switch cause {
	case "permission_denied", "read_only_filesystem", "storage_capacity", "dependency_unavailable", "deadline", "cancelled":
		return cause
	default:
		return "build_failed"
	}
}

func humanDecision(ctx context.Context) (bool, error) {
	fmt.Println(humanPrompt)
	return readHumanDecision(ctx, os.Stdin)
}

func readHumanDecision(ctx context.Context, input io.Reader) (bool, error) {
	answer := make(chan bool, 1)
	go func() {
		s := bufio.NewScanner(io.LimitReader(input, 128))
		answer <- s.Scan() && s.Text() == "allow"
	}()
	select {
	case yes := <-answer:
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return yes, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Kept separate for the Linux-only bootstrap process replacement.
var syscallExec = unix.Exec
