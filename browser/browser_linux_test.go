// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperEnv = "SOUNDCHECK_TEST_BROWSER_HELPER"

// TestChromeDiesWithSoundcheck kills a process that launched Chrome with
// SIGKILL, so no Close and no deferred code runs. Chrome and
// its children still go.
func TestChromeDiesWithSoundcheck(t *testing.T) {
	if os.Getenv(helperEnv) == "1" {
		helper()
		return
	}
	needChrome(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestChromeDiesWithSoundcheck$") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var pid int
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if n, ok := strings.CutPrefix(sc.Text(), "chrome-pid "); ok {
			pid, _ = strconv.Atoi(n)
			break
		}
	}
	if pid == 0 {
		t.Fatal("the helper never reported Chrome's pid")
	}
	group := groupOf(pid)
	if len(group) < 2 {
		t.Fatalf("Chrome's group has %v; want the browser and its children", group)
	}
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if left := waitGone(group, 5*time.Second); len(left) > 0 {
		for _, p := range left {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
		t.Fatalf("still running 5 s after Soundcheck was killed: %v (Chrome was %d)", left, pid)
	}
}

// helper launches Chrome with a page open, reports its pid and waits to
// be killed.
func helper() {
	b, err := Launch(context.Background(), Options{NoDownload: true, Log: func(string) {}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := NewTab(b.Browser); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	time.Sleep(time.Second) // let Chrome start its renderer
	fmt.Printf("chrome-pid %d\n", b.proc.getPID())
	time.Sleep(time.Minute)
	os.Exit(1)
}

func TestCloseKillsRenderers(t *testing.T) {
	needChrome(t)
	b, err := Launch(context.Background(), Options{NoDownload: true, Log: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTab(b.Browser); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	group := groupOf(b.proc.getPID())
	if len(group) < 2 {
		t.Fatalf("Chrome's group has %v; want the browser and its children", group)
	}
	dir := b.proc.userDir
	b.Close()
	if left := waitGone(group, 5*time.Second); len(left) > 0 {
		t.Errorf("still running after Close: %v", left)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("profile %s left behind", dir)
	}
}

// groupOf lists the live processes in process group pgid.
func groupOf(pgid int) []int {
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	var pids []int
	for _, f := range stats {
		state, group, ok := stat(f)
		if ok && group == pgid && state != "Z" {
			pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(f)))
			pids = append(pids, pid)
		}
	}
	return pids
}

// stat reads a process's state and process group from /proc.
func stat(path string) (state string, pgid int, ok bool) {
	data, err := os.ReadFile(path) //nolint:gosec // a /proc path
	if err != nil {
		return "", 0, false
	}
	// The command name can hold spaces and parentheses; fields after it
	// are "state ppid pgrp ...".
	i := strings.LastIndexByte(string(data), ')')
	if i < 0 {
		return "", 0, false
	}
	f := strings.Fields(string(data)[i+1:])
	if len(f) < 3 {
		return "", 0, false
	}
	pgid, err = strconv.Atoi(f[2])
	return f[0], pgid, err == nil
}

// waitGone waits until none of pids is running (zombies count as gone)
// and returns those still there at the deadline.
func waitGone(pids []int, d time.Duration) []int {
	deadline := time.Now().Add(d)
	for {
		var left []int
		for _, p := range pids {
			if state, _, ok := stat(fmt.Sprintf("/proc/%d/stat", p)); ok && state != "Z" {
				left = append(left, p)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
		time.Sleep(100 * time.Millisecond)
	}
}
