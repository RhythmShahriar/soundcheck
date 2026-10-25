// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var jobOnce sync.Once

// KillOnExit puts this process in a job object that kills everything in
// it when its last handle closes. The handle is never closed, so the
// kernel closes it when this process exits, however it exits. The
// browsers it started inherit the job and go with it. It's best
// effort: when the process can't join a job (an older Windows inside
// another job), browsers are still closed by their Close, just not after
// a crash. Calling it again does nothing.
func KillOnExit() { jobOnce.Do(killOnExitJob) }

func killOnExitJob() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil { //nolint:gosec // the API takes a pointer to this struct
		_ = windows.CloseHandle(job)
		return
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
	}
}

// KillTree kills p and every process it started. Killing only p would
// leave its children running: Windows doesn't end them with their
// parent. Firefox's launcher process starts the real browser as a
// child. taskkill is part of every Windows; when it fails, p alone is
// killed.
func KillTree(p *os.Process) error {
	taskkill := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	if os.Getenv("SystemRoot") == "" {
		taskkill = "taskkill.exe"
	}
	//nolint:gosec // taskkill with a process ID
	if err := exec.Command(taskkill, "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run(); err == nil {
		return nil
	}
	return p.Kill()
}
