// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestChromeConnectsOnlyToLoopback runs Chrome under strace while it
// shows a page on 127.0.0.1 and fails on any connection elsewhere: the
// release gate that says a run reaches the site and nothing else.
// Chrome checks whether IPv6 is reachable with a UDP socket it connects
// to a public address and never sends on; that one is allowed.
func TestChromeConnectsOnlyToLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("keeps Chrome open for a while")
	}
	needChrome(t)
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("no strace")
	}
	if out, err := exec.Command(strace, "-o", os.DevNull, "true").CombinedOutput(); err != nil { //nolint:gosec // strace from PATH
		t.Skipf("strace can't trace here: %v %s", err, out)
	}
	bin, _, err := Find(Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	wrapper := filepath.Join(dir, "chrome")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -f -qq -e trace=socket,connect,sendto,sendmsg,sendmmsg -o %q %q \"$@\"\n", strace, trace, bin)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<!doctype html><title>Quiet</title><p><a href="/next">Next</a>`)
	}))
	defer srv.Close()

	b, err := Launch(context.Background(), Options{Bin: wrapper, Log: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewTab(b.Browser)
	if err != nil {
		b.Close()
		t.Fatal(err)
	}
	if err := p.Navigate(srv.URL); err != nil {
		b.Close()
		t.Fatal(err)
	}
	// Chrome starts most of its services in the first seconds.
	time.Sleep(12 * time.Second)
	b.Close()

	if bad := outsideLoopback(t, trace); len(bad) > 0 {
		t.Errorf("Chrome connected beyond loopback:\n%s", strings.Join(bad, "\n"))
	}
}

var (
	socketCall  = regexp.MustCompile(`^(\d+)\s+socket\(([A-Z_0-9]+), ([A-Z_0-9|]+).*\) = (\d+)`)
	connectCall = regexp.MustCompile(`^(\d+)\s+connect\((\d+), \{sa_family=(AF_INET6?),.*?(?:inet_addr\("([^"]+)"\)|inet_pton\(AF_INET6, "([^"]+)")`)
	sendCall    = regexp.MustCompile(`^(\d+)\s+send(?:to|msg|mmsg)\((\d+),`)
)

// ipv6Probe is the address Chrome's IPv6 reachability check connects a
// UDP socket to.
const ipv6Probe = "2001:4860:4860::8888"

// outsideLoopback reads an strace log and lists every connection to an
// address that isn't loopback. Threads share their process's sockets,
// so a socket is known by its descriptor alone; a reused descriptor is
// replaced by the next socket() call that returns it.
func outsideLoopback(t *testing.T, trace string) []string {
	t.Helper()
	f, err := os.Open(trace) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	udp := map[string]bool{}   // descriptor: a datagram socket
	probe := map[string]bool{} // descriptor: connected to ipv6Probe
	var bad []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := socketCall.FindStringSubmatch(line); m != nil {
			udp[m[4]] = strings.Contains(m[3], "SOCK_DGRAM")
			delete(probe, m[4])
			continue
		}
		if m := sendCall.FindStringSubmatch(line); m != nil {
			if probe[m[2]] {
				bad = append(bad, "sent on the IPv6 check's socket: "+line)
			}
			continue
		}
		m := connectCall.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		addr := m[4] + m[5]
		switch {
		case strings.HasPrefix(addr, "127.") || addr == "::1" || addr == "::ffff:127.0.0.1":
		case addr == ipv6Probe && udp[m[2]]:
			probe[m[2]] = true
		default:
			bad = append(bad, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return bad
}

func TestOutsideLoopback(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	log := `10 socket(AF_INET6, SOCK_DGRAM|SOCK_CLOEXEC, IPPROTO_IP) = 21
10 connect(21, {sa_family=AF_INET6, sin6_port=htons(443), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "2001:4860:4860::8888", &sin6_addr), sin6_scope_id=0}, 28) = -1 ENETUNREACH (Network is unreachable)
10 socket(AF_INET, SOCK_STREAM|SOCK_CLOEXEC|SOCK_NONBLOCK, IPPROTO_IP) = 22
10 connect(22, {sa_family=AF_INET, sin_port=htons(39200), sin_addr=inet_addr("127.0.0.1")}, 16) = 0
11 socket(AF_INET, SOCK_STREAM|SOCK_CLOEXEC|SOCK_NONBLOCK, IPPROTO_IP) = 23
11 connect(23, {sa_family=AF_INET, sin_port=htons(443), sin_addr=inet_addr("142.251.43.14")}, 16) = -1 EINPROGRESS (Operation now in progress)
11 socket(AF_INET6, SOCK_STREAM|SOCK_CLOEXEC, IPPROTO_IP) = 24
11 connect(24, {sa_family=AF_INET6, sin6_port=htons(443), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "2001:4860:4860::8888", &sin6_addr), sin6_scope_id=0}, 28) = -1 EINPROGRESS
12 connect(5, {sa_family=AF_UNIX, sun_path="/tmp/x"}, 20) = 0
`
	if err := os.WriteFile(trace, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := outsideLoopback(t, trace)
	if len(bad) != 2 || !strings.Contains(bad[0], "142.251.43.14") || !strings.Contains(bad[1], "connect(24") {
		t.Errorf("bad = %q", bad)
	}
}
