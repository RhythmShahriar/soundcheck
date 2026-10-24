// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSnapshotHangup closes the "terminal" in the middle of a snapshot:
// the command must stop the same way as Ctrl-C, save nothing and exit 130.
func TestSnapshotHangup(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGQUIT} {
		t.Run(sig.String(), func(t *testing.T) {
			needBrowser(t)
			hit := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					http.NotFound(w, r)
					return
				}
				select {
				case hit <- struct{}{}:
				default:
				}
				select { // a page that never finishes loading
				case <-r.Context().Done():
				case <-time.After(time.Minute):
				}
			}))
			defer srv.Close()

			dir := t.TempDir()
			cmd, stdout, stderr := cliCommand(t, dir, "snapshot", srv.URL, "--name", "hung", "--plain", "--no-download",
				"--viewport", "desktop", "--modules", "health", "--rate", "50", "--timeout", "50s")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-hit:
			case <-time.After(60 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("the site was never asked for a page\n%s\n%s", stdout, stderr)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			var err error
			select {
			case err = <-done:
			case <-time.After(30 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("still running 30s after %v\n%s\n%s", sig, stdout, stderr)
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != exitCanceled {
				t.Fatalf("exit %v, want %d\n%s\n%s", err, exitCanceled, stdout, stderr)
			}
			if !strings.Contains(stderr.String(), "canceled") {
				t.Errorf("stderr has no 'canceled':\n%s", stderr)
			}
			for _, p := range []string{".soundcheck/snapshots/hung", ".soundcheck/.incoming/hung", ".soundcheck/snapshots/LATEST"} {
				if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
					t.Errorf("%s exists after %v", p, sig)
				}
			}
		})
	}
}
