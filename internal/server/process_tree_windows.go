//go:build windows

package server

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct {
	job windows.Handle
}

func newProcessTree(process *os.Process) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create Windows Job Object: %w", err)
	}

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("configure Windows Job Object: %w", err)
	}

	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(process.Pid),
	)
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("open PTY process %d: %w", process.Pid, err)
	}
	defer windows.CloseHandle(processHandle)

	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("assign PTY process %d to Windows Job Object: %w", process.Pid, err)
	}

	return &windowsProcessTree{job: job}, nil
}

func (tree *windowsProcessTree) Close() error {
	if err := windows.CloseHandle(tree.job); err != nil {
		return fmt.Errorf("close Windows Job Object: %w", err)
	}
	return nil
}
