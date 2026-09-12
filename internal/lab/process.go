package lab

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type boundedBuffer struct {
	data  []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > b.limit {
		return 0, errors.New("output_bound")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func command(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = env
	c.WaitDelay = time.Second
	b := &boundedBuffer{limit: 2 << 20}
	c.Stdout = b
	c.Stderr = io.Discard
	if c.Run() != nil {
		return b.data, errors.New("command_failed")
	}
	return b.data, nil
}

type ownedProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func startProcess(ctx context.Context, dir string, env []string, name string, args ...string) (*ownedProcess, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = env
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = time.Second
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	if c.Start() != nil {
		return nil, errors.New("process_start")
	}
	p := &ownedProcess{cmd: c, done: make(chan struct{})}
	go func() { _ = c.Wait(); close(p.done) }()
	return p, nil
}
func (p *ownedProcess) stop() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
		}
	})
}

func privateEnv(root string) []string {
	return []string{"PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "HOME=" + root + "/home", "XDG_CONFIG_HOME=" + root + "/config", "XDG_CACHE_HOME=" + root + "/cache", "XDG_DATA_HOME=" + root + "/data", "XDG_STATE_HOME=" + root + "/state", "TMPDIR=" + root + "/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0", "GOMODCACHE=/opt/gomod", "GOCACHE=/tmp/gocache", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true", "OPENCODE_DISABLE_PROJECT_CONFIG=true", "OPENCODE_DISABLE_CLAUDE_CODE=true", "OPENCODE_DISABLE_MODELS_FETCH=true", "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_EXTERNAL_SKILLS=true", "OPENCODE_DISABLE_LSP_DOWNLOAD=true", "OPENCODE_ENABLE_QUESTION_TOOL=true", "OPENCODE_ENABLE_PARALLEL=true", "OPENCODE_EXPERIMENTAL_BACKGROUND_SUBAGENTS=true"}
}
func privateDirs(root string) error {
	for _, n := range []string{"home", "config", "cache", "data", "state", "tmp", "repo", "observer"} {
		if os.MkdirAll(root+"/"+n, 0700) != nil {
			return errors.New("fixture_directory")
		}
	}
	return nil
}
