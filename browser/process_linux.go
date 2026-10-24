// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
)

// process is a Chrome that Soundcheck started itself. On Linux it runs
// in its own process group, so Close can kill the renderers and helpers
// along with it, and with a parent-death signal, so the kernel kills it
// when Soundcheck dies, even by SIGKILL or a panic that skips Close.
//
// Pdeathsig fires when the OS thread that started Chrome exits, not the
// whole process. Go only ends threads that a goroutine left locked with
// runtime.LockOSThread, which nothing on this path does, so in practice
// it fires when Soundcheck exits.
type process struct {
	pid     int
	exited  chan struct{}
	userDir string
}

func start(ctx context.Context, l *launcher.Launcher, bin string) (string, *process, error) {
	cmd := exec.Command(bin, l.FormatArgs()...) //nolint:gosec // bin is the chosen browser, args are rod's fixed flags
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	pctx, cancel := context.WithCancel(ctx)
	defer cancel() // unblocks the parser once nobody waits for the URL
	parser := launcher.NewURLParser().Context(pctx)
	cmd.Stdout, cmd.Stderr = parser, parser
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	p := &process{pid: cmd.Process.Pid, exited: make(chan struct{}), userDir: l.Get(flags.UserDataDir)}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	select {
	case u := <-parser.URL:
		ws, err := launcher.ResolveURL(u)
		if err != nil {
			p.stop()
			return "", nil, err
		}
		return ws, p, nil
	case <-p.exited:
		p.stop()
		return "", nil, parser.Err()
	case <-ctx.Done():
		p.stop()
		return "", nil, ctx.Err()
	}
}

// stop kills Chrome's whole process group and removes its profile. It
// first gives a Chrome that was asked to close a moment to exit on its
// own.
func (p *process) stop() {
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-p.pid, syscall.SIGKILL)
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
	}
	if p.userDir != "" {
		_ = os.RemoveAll(p.userDir)
	}
}

// getPID is Chrome's process id, which is also its process group.
func (p *process) getPID() int { return p.pid }
