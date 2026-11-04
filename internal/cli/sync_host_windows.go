package cli

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runHostProbe(ctx context.Context, cmd *exec.Cmd) error {
	// An unnamed, non-inherited job contains the process before its entrypoint
	// runs. Do not allow breakaway or fall back to root-only cancellation.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	limits := new(windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION)
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	var pinned runtime.Pinner
	pinned.Pin(limits)
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(limits)), uint32(unsafe.Sizeof(*limits)))
	pinned.Unpin()
	if err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error {
		_ = windows.TerminateJobObject(job, 1)
		// Context cancellation can race the assignment while the root is still
		// suspended. Kill it too, even if the job was not assigned yet.
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer windows.TerminateJobObject(job, 1)
	abort := func(err error) error {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return abort(err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return abort(err)
	}
	if err := resumeHostProbeThread(ctx, uint32(cmd.Process.Pid)); err != nil {
		return abort(err)
	}
	return cmd.Wait()
}

func resumeHostProbeThread(ctx context.Context, pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		count, err := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("unexpected probe thread suspension state")
		}
		return nil
	}
	return errors.New("suspended probe thread unavailable")
}
