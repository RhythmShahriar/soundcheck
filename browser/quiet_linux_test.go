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
	script := fmt.Sprintf("#!/bin/sh\nexec %q -f -qq -y -e trace=socket,connect,sendto,sendmsg,sendmmsg -o %q %q \"$@\"\n", strace, trace, bin)
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
	socketCall  = regexp.MustCompile(`^(\d+)\s+socket\(([A-Z_0-9]+), ([A-Z_0-9|]+).*\)\s+=\s+(\d+)(<[^>]*>)?`)
	connectCall = regexp.MustCompile(`^(\d+)\s+connect\((\d+)(<[^>]*>)?, \{sa_family=(AF_INET6?),.*?(?:inet_addr\("([^"]+)"\)|inet_pton\(AF_INET6, "([^"]+)")`)
	sendCall    = regexp.MustCompile(`^(\d+)\s+send(?:to|msg|mmsg)\((\d+)(<[^>]*>)?,`)
	unfinished  = regexp.MustCompile(`^(\d+)\s+(.*) <unfinished \.\.\.>$`)
	resumed     = regexp.MustCompile(`^(\d+)\s+<\.\.\. \w+ resumed>(.*)$`)
	sockInode   = regexp.MustCompile(`^<socket:\[(\d+)\]>$`)
)

// ipv6Probe is the address Chrome's IPv6 reachability check connects a
// UDP socket to.
const ipv6Probe = "2001:4860:4860::8888"

// sockKey names the socket behind a descriptor. strace -y prints each
// descriptor with the socket's inode, which is the same in every
// process and thread that holds the socket and differs as soon as the
// number is reused, so the inode is the key. Chrome runs many
// processes under one trace, each with its own descriptor table, so a
// bare number would mix them up; it only stands in, per process, when
// strace couldn't name the socket.
func sockKey(pid, fd, decoded string) string {
	if m := sockInode.FindStringSubmatch(decoded); m != nil {
		return "inode:" + m[1]
	}
	return pid + ":" + fd
}

// outsideLoopback reads an strace -f -y log. It lists every connection
// to an address that isn't loopback plus every send on the socket
// Chrome's IPv6 check connected. A call strace split across lines
// ("<unfinished ...>" then "<... resumed>") is joined first.
func outsideLoopback(t *testing.T, trace string) []string {
	t.Helper()
	f, err := os.Open(trace) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	udp := map[string]bool{}       // socket: a datagram socket
	probe := map[string]bool{}     // socket: connected to ipv6Probe
	pending := map[string]string{} // pid: the start of an unfinished call
	var bad []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := unfinished.FindStringSubmatch(line); m != nil {
			pending[m[1]] = m[2]
			continue
		}
		if m := resumed.FindStringSubmatch(line); m != nil {
			start, ok := pending[m[1]]
			if !ok {
				continue
			}
			delete(pending, m[1])
			line = m[1] + " " + start + m[2]
		}
		if m := socketCall.FindStringSubmatch(line); m != nil {
			k := sockKey(m[1], m[4], m[5])
			udp[k] = strings.Contains(m[3], "SOCK_DGRAM")
			delete(probe, k)
			continue
		}
		if m := sendCall.FindStringSubmatch(line); m != nil {
			if probe[sockKey(m[1], m[2], m[3])] {
				bad = append(bad, "sent on the IPv6 check's socket: "+line)
			}
			continue
		}
		m := connectCall.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		k := sockKey(m[1], m[2], m[3])
		addr := m[5] + m[6]
		switch {
		case strings.HasPrefix(addr, "127.") || addr == "::1" || addr == "::ffff:127.0.0.1":
		case addr == ipv6Probe && udp[k]:
			probe[k] = true
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

// TestOutsideLoopbackPerSocket covers the false alarm the race gate hit:
// one Chrome process connects fd 27 to the IPv6 check while another
// sends Mojo messages on its own fd 27, a socketpair. Sockets are told
// apart by inode, so only a send on the probe socket itself counts,
// and a split call is joined before it is read.
func TestOutsideLoopbackPerSocket(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	log := `10 socket(AF_INET6, SOCK_DGRAM|SOCK_CLOEXEC, IPPROTO_IP) = 27<socket:[100]>
10 connect(27<socket:[100]>, {sa_family=AF_INET6, sin6_port=htons(443), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "2001:4860:4860::8888", &sin6_addr), sin6_scope_id=0}, 28) = 0
20 sendto(27<socket:[200]>, "\20\0\0\0@\0", 64, MSG_NOSIGNAL, NULL, 0) = 64
11 sendmsg(27<socket:[300]>, {msg_name=NULL}, MSG_NOSIGNAL) = 8
30 socket(AF_INET, SOCK_STREAM|SOCK_CLOEXEC, IPPROTO_IP <unfinished ...>
31 socket(AF_UNIX, SOCK_STREAM, 0) = 5<socket:[401]>
30 <... socket resumed>)         = 6<socket:[400]>
30 connect(6<socket:[400]>, {sa_family=AF_INET, sin_port=htons(443), <unfinished ...>
30 <... connect resumed>sin_addr=inet_addr("142.251.43.14")}, 16) = -1 EINPROGRESS (Operation now in progress)
12 sendto(27<socket:[100]>, "x", 1, 0, NULL, 0) = 1
`
	if err := os.WriteFile(trace, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := outsideLoopback(t, trace)
	if len(bad) != 2 || !strings.Contains(bad[0], "142.251.43.14") || !strings.Contains(bad[1], "12 sendto(27<socket:[100]>") {
		t.Errorf("bad = %q", bad)
	}
}
