// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"
	"sync"
	"unsafe"

	"github.com/go-rod/rod/lib/launcher"
	"golang.org/x/sys/windows"
)

// process is a Chrome started through rod's launcher.
//
// leakless stays off on Windows: its guard executable, unpacked into
// the temp folder at run time, is regularly flagged by antivirus as a
// false positive, which quarantines it and breaks every run. Instead
// Soundcheck puts itself in a job object that kills everything in it
// when its last handle closes. The handle is never closed, so the
// kernel closes it when Soundcheck exits, however it exits. Chrome and
// its children inherit the job, so they go with it.
type process struct{ l *launcher.Launcher }

var jobOnce sync.Once

func start(_ context.Context, l *launcher.Launcher, bin string) (string, *process, error) {
	jobOnce.Do(killOnExitJob)
	u, err := l.Bin(bin).Leakless(false).Launch()
	if err != nil {
		return "", nil, err
	}
	return u, &process{l}, nil
}

// killOnExitJob puts this process in a kill-on-close job. It's best
// effort: when the process can't join a job (an older Windows inside
// another job), Chrome is still closed by Close, just not after a crash.
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

func (p *process) stop() {
	p.l.Kill()
	p.l.Cleanup()
}
