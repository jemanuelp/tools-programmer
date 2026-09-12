package lab

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestContainerPolicyHasNoHostCapabilitiesOrNetwork(t *testing.T) {
	args := ContainerArgs("sha256:"+strings.Repeat("a", 64), "run", []string{"contracts"}, false, time.Minute)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--network=none", "--user=10001:10001", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only", "--pull=never", "--memory=4g", "--pids-limit=256"} {
		if !strings.Contains(joined, want) {
			t.Fatal("missing", want)
		}
	}
	for _, bad := range []string{"--privileged", "docker.sock", "--publish", "--network=host", "/home/", "--env-file"} {
		if strings.Contains(joined, bad) {
			t.Fatal("unsafe", bad)
		}
	}
}
func TestPromptWriterDropsRawRuntimeOutput(t *testing.T) {
	var out bytes.Buffer
	p := &promptWriter{out: &out}
	p.Write([]byte("secret raw runtime payload\n" + humanPrompt + "\n/host/home\n"))
	if out.String() != humanPrompt+"\n" {
		t.Fatal(out.String())
	}
}
func TestSelectionRejectsUnknownDuplicateAndEmpty(t *testing.T) {
	for _, s := range []string{"", "contracts,contracts", "unknown", "contracts,"} {
		if _, err := Select(s); err == nil {
			t.Fatal(s)
		}
	}
	got, err := Select("contracts,silent")
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
}
func TestBoundedOutputAndCancellation(t *testing.T) {
	b := &boundedBuffer{limit: 3}
	if _, err := b.Write([]byte("four")); err == nil {
		t.Fatal("unbounded output")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, func() (bool, error) { return false, nil }); err == nil {
		t.Fatal("ignored cancellation")
	}
}

type fakeDocker struct {
	calls [][]string
	err   error
}

func (d *fakeDocker) Run(_ context.Context, _ io.Reader, _ io.Writer, args ...string) error {
	d.calls = append(d.calls, append([]string{}, args...))
	return d.err
}
func TestDockerFailureIsSafe(t *testing.T) {
	d := &fakeDocker{err: errors.New("docker_command")}
	_, err := dockerOutput(context.Background(), d, "inspect", "fixture")
	if err == nil || len(d.calls) != 1 {
		t.Fatal(err)
	}
}

type lifecycleDocker struct {
	run, source            string
	calls                  [][]string
	interrupt              bool
	malformed              bool
	copied                 chan struct{}
	private, modePreserved bool
	startupFailure         bool
	selection              []string
	frames                 func(io.Writer, Report)
}

func (d *lifecycleDocker) Run(ctx context.Context, in io.Reader, out io.Writer, args ...string) error {
	d.calls = append(d.calls, append([]string{}, args...))
	switch args[0] {
	case "image":
		io.WriteString(out, "sha256:"+strings.Repeat("a", 64))
		return nil
	case "create":
		d.copied = make(chan struct{})
		for i, a := range args {
			if a == "--scenarios" {
				d.selection = strings.Split(args[i+1], ",")
			}
		}
		for _, a := range args {
			if strings.HasPrefix(a, "LAB_RUN=") {
				d.run = strings.TrimPrefix(a, "LAB_RUN=")
			}
		}
		io.WriteString(out, strings.Repeat("e", 64))
		return nil
	case "exec":
		defer close(d.copied)
		file, ok := in.(*os.File)
		if !ok {
			return errors.New("expected_private_archive")
		}
		st, err := os.Stat(filepath.Dir(file.Name()))
		if err != nil {
			return err
		}
		d.private = st.Mode().Perm() == 0700
		r := tar.NewReader(in)
		for {
			h, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Name == "file.go" {
				d.modePreserved = h.Mode == 0600
			}
			if h.Name == ".lab-source.json" {
				var s Source
				if json.NewDecoder(r).Decode(&s) != nil {
					return errors.New("manifest")
				}
				d.source = s.Hash
			}
		}
		return nil
	case "start":
		if d.interrupt {
			return errors.New("docker_command")
		}
		io.WriteString(out, sourcePrompt+"\n")
		select {
		case <-d.copied:
		case <-ctx.Done():
			return ctx.Err()
		}
		r := sampleReport()
		r.Run = d.run
		r.SourceHash = d.source
		r.InputHash = inputHash(d.selection, false, time.Minute)
		if d.startupFailure {
			r.Startup = &Startup{Stage: "reexec", Status: "failed", Cause: "exec_denied", ExitCode: 1, ElapsedMS: 35440}
			r.Results = nil
			r.BinaryHash = ""
			r.VersionsVerified = false
		}
		if d.malformed {
			r.Run = "stale"
		}
		b, _ := json.Marshal(r)
		if d.frames != nil {
			d.frames(out, r)
			return nil
		}
		io.WriteString(out, "secret log must be dropped\nLAB_REPORT "+string(b)+"\n")
		if d.startupFailure {
			return &commandFailure{1}
		}
		return nil
	case "rm":
		return nil
	}
	return errors.New("unexpected_docker_command")
}
func TestHostRetainsSafeEvidenceAndCleansExactContainer(t *testing.T) {
	for _, mode := range []string{"success", "interrupted", "stale"} {
		t.Run(mode, func(t *testing.T) {
			root := snapshotFixture(t)
			if err := os.Chmod(filepath.Join(root, "file.go"), 0600); err != nil {
				t.Fatal(err)
			}
			d := &lifecycleDocker{interrupt: mode == "interrupted", malformed: mode == "stale"}
			out := filepath.Join(t.TempDir(), "report")
			r, err := runHost(context.Background(), HostOptions{Source: root, Output: out, Scenarios: []string{"contracts"}, Timeout: time.Minute}, d)
			if mode == "success" && err != nil {
				t.Fatal(err)
			}
			if mode == "success" && (!d.private || !d.modePreserved) {
				t.Fatal("source privacy or mode lost")
			}
			if mode != "success" && err == nil {
				t.Fatal("expected failure")
			}
			last := d.calls[len(d.calls)-1]
			if len(last) != 3 || last[0] != "rm" || last[2] != strings.Repeat("e", 64) {
				t.Fatal(d.calls)
			}
			if !r.Cleaned {
				t.Fatal("cleanup not recorded")
			}
			b, e := os.ReadFile(filepath.Join(out, "report.json"))
			if e != nil || strings.Contains(string(b), "secret log") {
				t.Fatal(string(b), e)
			}
		})
	}
}

func diagnosticDocker(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestPrepareFailureKeepsSafeCauseStageAndExit(t *testing.T) {
	diagnosticDocker(t, `printf '%s\n' '#6 [2/11] RUN apt-get update' '#6 0.1 W: No system certificates available. Try installing ca-certificates.' '#6 0.2 Certificate verification failed: issuer unknown' '#6 0.3 Package ca-certificates has no installation candidate' '#6 ERROR: process failed: exit code: 100' 'SENTINEL_CREDENTIAL /private/user/source' >&2
printf '%s\n' 'RAW_STDOUT_SECRET /private/user/build'
exit 1
`)
	var out bytes.Buffer
	err := (dockerCLI{}).Run(context.Background(), nil, &out, "build", "--progress=plain", "fixture")
	if err == nil {
		t.Fatal("failed build passed")
	}
	b, _ := json.Marshal(err)
	var failure struct {
		Cause       string `json:"cause"`
		Stage       string `json:"stage"`
		Exit        int    `json:"exit_code"`
		ProcessExit int    `json:"process_exit_code"`
		Step        int    `json:"step"`
		Steps       int    `json:"steps"`
		Ref         string `json:"build_ref"`
	}
	if json.Unmarshal(b, &failure) != nil || failure.Cause != "tls_trust" || failure.Stage != "apt" || failure.Exit != 1 || failure.ProcessExit != 100 || failure.Step != 2 || failure.Steps != 11 {
		t.Fatalf("safe primary failure not retained: %s", b)
	}
	if failure.Ref != "" {
		t.Fatal("invented an unobserved build reference")
	}
	for _, secret := range []string{"SENTINEL_CREDENTIAL", "RAW_STDOUT_SECRET", "/private/user"} {
		if strings.Contains(string(b)+err.Error()+out.String(), secret) {
			t.Fatal("raw build output escaped")
		}
	}
}

func TestPrepareFailureBoundsLogsAndKeepsObservedReference(t *testing.T) {
	diagnosticDocker(t, `printf '%s\n' '#8 [4/11] RUN go mod download' >&2
i=0; while [ "$i" -lt 4096 ]; do printf '%0512d' 0 >&2; i=$((i+1)); done
printf '\n%s\n' '#8 ERROR: process failed: exit code: 7' 'View build details: docker-desktop://dashboard/build/fixture/fixture0/0123456789abcdefghijklmnop' >&2
exit 1
`)
	err := (dockerCLI{}).Run(context.Background(), nil, io.Discard, "build", "--progress=plain", "fixture")
	if err == nil {
		t.Fatal("failed build passed")
	}
	b, _ := json.Marshal(err)
	var failure struct {
		Truncated   bool   `json:"truncated"`
		Ref         string `json:"build_ref"`
		Step        int    `json:"step"`
		ProcessExit int    `json:"process_exit_code"`
	}
	if json.Unmarshal(b, &failure) != nil || !failure.Truncated || failure.Step != 4 || failure.ProcessExit != 7 || failure.Ref != "fixture/fixture0/0123456789abcdefghijklmnop" || len(b) > 2048 {
		t.Fatalf("bounded diagnostic lost final evidence: %s", b)
	}
}

func TestPrepareDiagnosticKeepsFirstFailedVertex(t *testing.T) {
	log := &buildLog{}
	io.WriteString(log, "#6 [2/11] RUN apt-get update\n#9 [4/11] RUN go mod download\n#6 0.2 Certificate verification failed\n#6 ERROR: exit code: 100\n#9 ERROR: cancelled\nERROR: failed to build\n")
	failure := log.failure(errors.New("unexportable /private/path SENTINEL_CREDENTIAL"), nil)
	if failure.Stage != "apt" || failure.Step != 2 || failure.ProcessExitCode != 100 || failure.Cause != "tls_trust" {
		t.Fatal(failure)
	}
	if strings.Contains(PrepareDiagnostic(failure), "SENTINEL") || strings.Contains(PrepareDiagnostic(errors.New("SENTINEL_CREDENTIAL /private/path")), "SENTINEL") {
		t.Fatal("raw error escaped")
	}
	if strings.Contains(PrepareDiagnostic(failure), "inspect:") {
		t.Fatal("invented inspection target")
	}
}

func TestBuildDiagnosticByteBudgetAndUnnumberedFailures(t *testing.T) {
	log := &buildLog{}
	for i := 0; i < 4; i++ {
		io.WriteString(log, strings.Repeat("x\n", 32<<10))
	}
	io.WriteString(log, "manifest verification failed for digest\n")
	failure := log.failure(errors.New("ignored"), nil)
	if !failure.Truncated || failure.Cause != "manifest_verification" || failure.Stage != "unknown" {
		t.Fatal(failure)
	}
	if len(log.line) != 0 || len(log.vertices) != 0 {
		t.Fatal("raw logs retained")
	}
}

func TestPrepareCAReadFailureWithPaddedStep(t *testing.T) {
	diagnosticDocker(t, `printf '%s\n' '#6 [ 3/12] RUN apt-get update' '#6 0.1 Could not load certificates from /etc/ssl/certs/ca-certificates.crt (CaInfo option): Error while reading file.' '#6 0.2 E: The repository does not have a Release file.' '#6 0.3 SENTINEL_CREDENTIAL /private/user/source' '#6 ERROR: process failed: exit code: 100' >&2
exit 1
`)
	var out bytes.Buffer
	err := (dockerCLI{}).Run(context.Background(), nil, &out, "build", "--progress=plain", "fixture")
	var failure *BuildFailure
	if !errors.As(err, &failure) || failure.Cause != "ca_read" || failure.Stage != "apt" || failure.Vertex != 6 || failure.Step != 3 || failure.Steps != 12 || failure.ExitCode != 1 || failure.ProcessExitCode != 100 {
		t.Fatalf("padded-step CA failure not preserved: %+v", err)
	}
	b, _ := json.Marshal(failure)
	for _, secret := range []string{"SENTINEL_CREDENTIAL", "/private/user", "Release file", "/etc/ssl/certs/ca-certificates.crt"} {
		if strings.Contains(string(b)+PrepareDiagnostic(err)+out.String(), secret) {
			t.Fatal("raw diagnostic escaped")
		}
	}
}

func TestSourceDeliveryDoesNotExposeHostSnapshot(t *testing.T) {
	args := ContainerArgs("fixture", "run", []string{"contracts"}, false, time.Minute)
	for _, arg := range args {
		if arg == "--mount" || strings.Contains(arg, "/private/snapshot") {
			t.Fatal("host-owned source permissions cannot serve a different runtime UID")
		}
	}
}

func TestPrivateTmpfsExplicitlyAllowsBuiltExecutable(t *testing.T) {
	args := ContainerArgs("fixture", "run", []string{"contracts"}, false, time.Minute)
	for i, arg := range args {
		if arg == "--tmpfs" {
			options := strings.Split(args[i+1], ",")
			found := false
			for _, option := range options {
				found = found || option == "exec"
			}
			if !found {
				t.Fatal("offline runner is built on Docker's default noexec tmpfs")
			}
			return
		}
	}
	t.Fatal("missing private tmpfs")
}

func TestHostStartupFailureDoesNotFabricateScenarioExecutions(t *testing.T) {
	root := snapshotFixture(t)
	d := &lifecycleDocker{startupFailure: true}
	selected := []string{"contracts", "silent", "egress"}
	r, err := runHost(context.Background(), HostOptions{Source: root, Output: filepath.Join(t.TempDir(), "report"), Scenarios: selected, Timeout: time.Minute}, d)
	if err != nil || r.ExitCode() == 0 || r.Startup == nil || r.Startup.Cause != "exec_denied" {
		t.Fatal(err, r)
	}
	for _, result := range r.Results {
		if result.Status != "not_run" || len(result.Checks) != 0 || result.ElapsedMS != 0 {
			t.Fatal("fabricated execution", result)
		}
	}
}

func TestStartupBuildDiagnosticsAreBoundedAndScrubbed(t *testing.T) {
	diagnosticDocker(t, "printf '%s\\n' '/private/source: permission denied SENTINEL_SECRET' >&2\nprintf '%s\\n' 'UNTRUSTED_STDOUT'\nexit 7\n")
	failure := executeStartupBuild(context.Background(), exec.Command("docker"))
	if failure == nil || failure.ExitCode != 7 || failure.Cause != "permission_denied" {
		t.Fatal(failure)
	}
	r := sampleReport()
	r.Results = nil
	var out bytes.Buffer
	started := time.Now()
	startupStage(&r, "lab_build", started, &out)
	startupFailed(&r, startupBuildCause(failure.Cause), &failure.ExitCode, failure.Truncated, started, &out)
	for _, bad := range []string{"/private/source", "SENTINEL_SECRET", "UNTRUSTED_STDOUT"} {
		if strings.Contains(out.String(), bad) {
			t.Fatal("startup raw output escaped")
		}
	}
	p := &promptWriter{out: io.Discard, decode: func(frame []byte) (Report, error) {
		return decodeSelectedReport(frame, r.Run, r.SourceHash, r.InputHash, []string{"contracts"})
	}}
	p.Write(out.Bytes())
	if _, err := DecodeReport(bytes.NewReader(p.report), r.Run, r.SourceHash); err != nil {
		t.Fatal(err)
	}
}

func TestReexecForwardsSelectionAndEnvironmentWithoutTouchingStdin(t *testing.T) {
	original := syscallExec
	defer func() { syscallExec = original }()
	stdin := os.Stdin
	args := []string{"--scenarios", "contracts,silent", "--interactive"}
	env := []string{"LAB_TIMEOUT=10m", "LAB_RUN=fixture"}
	syscallExec = func(path string, argv, gotEnv []string) error {
		if path != "/tmp/lab-current" || !reflect.DeepEqual(argv, append([]string{path, "guest"}, args...)) || !reflect.DeepEqual(gotEnv, env) {
			t.Fatal("reexec contract changed")
		}
		return syscall.EACCES
	}
	if !errors.Is(reexecGuest(args, env), syscall.EACCES) || os.Stdin != stdin {
		t.Fatal("reexec boundary not preserved")
	}
}

func TestStartupDeadlineClassificationUsesContextNotElapsedGuess(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if startupContextCause(ctx, "container_exit") != "deadline" {
		t.Fatal("deadline not preserved")
	}
	if startupContextCause(context.Background(), "container_exit") != "container_exit" {
		t.Fatal("invented deadline")
	}
}
