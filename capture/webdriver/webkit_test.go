// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake WebKit driver: with fakeDriverEnv
// set it serves a classic WebDriver on the --port it was given. With
// fakeFailEnv naming a file that doesn't exist yet it creates the file
// and exits instead, the way a driver does when its port was taken.
// With fakeFailEnv "always" it always exits.
const (
	fakeDriverEnv = "SOUNDCHECK_FAKE_WEBDRIVER"
	fakeFailEnv   = "SOUNDCHECK_FAKE_WEBDRIVER_FAIL_ONCE"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeDriverEnv) != "" {
		serveFakeDriver()
		return
	}
	os.Exit(m.Run())
}

func serveFakeDriver() {
	if f := os.Getenv(fakeFailEnv); f == "always" {
		os.Exit(1)
	} else if f != "" {
		if _, err := os.Stat(f); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(f, nil, 0o600)
			fmt.Fprintln(os.Stderr, "address already in use")
			os.Exit(1)
		}
	}
	port := ""
	for _, a := range os.Args[1:] {
		if p, ok := strings.CutPrefix(a, "--port="); ok {
			port = p
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"value":{"ready":true}}`)
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"value":{"sessionId":"s1","capabilities":{}}}`)
	})
	mux.HandleFunc("/session/s1", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"value":null}`)
	})
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = srv.ListenAndServe()
	os.Exit(1)
}

// A driver that exits before it answers (its port was taken in the
// meantime) is started once more on another port.
func TestLaunchWebKitRetriesOnce(t *testing.T) {
	t.Setenv(fakeDriverEnv, "1")
	t.Setenv(fakeFailEnv, t.TempDir()+"/failed")
	var logs []string
	b, err := LaunchWebKit(context.Background(), Options{Bin: os.Args[0], Log: func(s string) { logs = append(logs, s) }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if b.BiDi() || len(logs) != 1 || !strings.Contains(logs[0], "trying another port") {
		t.Errorf("bidi %v, logs %q", b.BiDi(), logs)
	}
}

// A driver that keeps exiting is an error once its second start fails
// too, without waiting out the readiness timeout.
func TestLaunchWebKitGivesUp(t *testing.T) {
	t.Setenv(fakeDriverEnv, "1")
	t.Setenv(fakeFailEnv, "always")
	start := time.Now()
	var logs []string
	_, err := LaunchWebKit(context.Background(), Options{Bin: os.Args[0], Log: func(s string) { logs = append(logs, s) }})
	if err == nil || !strings.Contains(err.Error(), "exited before its WebDriver server answered") || len(logs) != 1 {
		t.Errorf("err = %v, logs %q", err, logs)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("gave up after %s", d)
	}
}
