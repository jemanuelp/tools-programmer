//go:build !windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func runHostProbe(_ context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == syscall.ESRCH {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	// WaitDelay may finish after the wrapper exits while children still own its
	// output pipe. Clean the owned group on every outcome, not only cancellation.
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	return cmd.Run()
}
