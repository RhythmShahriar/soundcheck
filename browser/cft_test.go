// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeArchive builds a Chrome for Testing style zip for platform with a
// "chrome" that is executable, plus any extra entries.
func fakeArchive(t *testing.T, platform string, extra func(*zip.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	root := t.TempDir()
	rel, err := filepath.Rel(root, binPath(root, platform))
	if err != nil {
		t.Fatal(err)
	}
	h := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate}
	h.SetMode(0o755)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("#!/bin/sh\necho fake chrome\n"))
	l := &zip.FileHeader{Name: "chrome-" + platform + "/current"}
	l.SetMode(os.ModeSymlink | 0o777)
	w, _ = zw.CreateHeader(l)
	_, _ = w.Write([]byte("."))
	if extra != nil {
		extra(zw)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// serve publishes body as version's archive for platform on a local
// server and points cftBase at it.
func serve(t *testing.T, version, platform string, body []byte) *int {
	t.Helper()
	hits := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+version+"/"+platform+"/chrome-"+platform+".zip" {
			http.NotFound(w, r)
			return
		}
		*hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	old := cftBase
	cftBase = srv.URL
	t.Cleanup(func() { cftBase = old })
	return hits
}

func TestInstallRefusesATamperedArchive(t *testing.T) {
	good := fakeArchive(t, "linux64", nil)
	tampered := append([]byte(nil), good...)
	tampered[len(tampered)/2] ^= 0xff
	serve(t, "153.0.1.2", "linux64", tampered)
	rel := Release{Version: "153.0.1.2", Archives: map[string]Archive{"linux64": {SHA256: sum(good), Size: int64(len(good))}}}
	dir := t.TempDir()
	_, err := install(t.Context(), rel, "linux64", dir, nil)
	if err == nil || !strings.Contains(err.Error(), "doesn't match the checksum") || !strings.Contains(err.Error(), "nothing was installed") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("left %d entries behind, e.g. %s", len(entries), entries[0].Name())
	}
}

func TestInstallRefusesAWrongSize(t *testing.T) {
	good := fakeArchive(t, "linux64", nil)
	serve(t, "153.0.1.2", "linux64", good)
	rel := Release{Version: "153.0.1.2", Archives: map[string]Archive{"linux64": {SHA256: sum(good), Size: int64(len(good)) + 1}}}
	if _, err := install(t.Context(), rel, "linux64", t.TempDir(), nil); err == nil {
		t.Fatal("installed an archive of the wrong size")
	}
}

func TestFindDownloadsAndVerifies(t *testing.T) {
	platform := Platform()
	if platform == "" {
		t.Skip("no Chrome for Testing build for this platform")
	}
	good := fakeArchive(t, platform, nil)
	hits := serve(t, "153.0.1.2", platform, good)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	t.Setenv("LocalAppData", cache)
	oldPin, oldLook := pinned, lookPath
	pinned = Release{Version: "153.0.1.2", Archives: map[string]Archive{platform: {SHA256: sum(good), Size: int64(len(good))}}}
	lookPath = func() (string, bool) { return "", false }
	t.Cleanup(func() { pinned, lookPath = oldPin, oldLook })

	if _, _, err := Find(Options{NoDownload: true}); err == nil {
		t.Fatal("NoDownload downloaded")
	}
	var logs []string
	bin, downloaded, err := Find(Options{Log: func(m string) { logs = append(logs, m) }})
	if err != nil {
		t.Fatal(err)
	}
	if !downloaded || !strings.Contains(bin, "cft-153.0.1.2") {
		t.Errorf("bin = %s, downloaded = %v", bin, downloaded)
	}
	if PinnedVersion() != "153.0.1.2" {
		t.Errorf("PinnedVersion = %q", PinnedVersion())
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "Downloading Chrome for Testing 153.0.1.2") {
		t.Errorf("logs = %q", logs)
	}
	if fi, err := os.Stat(bin); err != nil || (runtime.GOOS != "windows" && fi.Mode()&0o100 == 0) {
		t.Errorf("chrome not installed executable: %v %v", fi, err)
	}
	// The second call uses the install without downloading again, even
	// offline.
	if again, _, err := Find(Options{NoDownload: true}); err != nil || again != bin || *hits != 1 {
		t.Errorf("second Find = %s, %v after %d downloads", again, err, *hits)
	}

	// Prune keeps the pinned version and removes an older rod download.
	dir, _ := DownloadDir()
	old := filepath.Join(dir, "chromium-1321438")
	if err := os.MkdirAll(old, 0o750); err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(false)
	if err != nil || len(removed) != 1 || removed[0] != old {
		t.Errorf("Prune = %q, %v", removed, err)
	}
	if !fileExists(bin) {
		t.Error("Prune removed the pinned browser")
	}
}

// stallingServer sends the headers and a few bytes, then nothing until
// the test ends.
func stallingServer(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("PK"))
		w.(http.Flusher).Flush()
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(done); srv.Close() })
	old := cftBase
	cftBase = srv.URL
	t.Cleanup(func() { cftBase = old })
}

func TestInstallGivesUpOnAStalledDownload(t *testing.T) {
	stallingServer(t)
	old := stallTimeout
	stallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { stallTimeout = old })
	rel := Release{Version: "153.0.1.2", Archives: map[string]Archive{"linux64": {SHA256: sum([]byte("x")), Size: 1000}}}
	dir := t.TempDir()
	_, err := install(t.Context(), rel, "linux64", dir, nil)
	if err == nil || !strings.Contains(err.Error(), "no data from") || !strings.Contains(err.Error(), "for 100ms; giving up") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left %d entries behind, e.g. %s", len(entries), entries[0].Name())
	}
}

func TestFindContextStopsTheDownloadWhenCanceled(t *testing.T) {
	platform := Platform()
	if platform == "" {
		t.Skip("no Chrome for Testing build for this platform")
	}
	stallingServer(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	t.Setenv("LocalAppData", cache)
	oldPin, oldLook := pinned, lookPath
	pinned = Release{Version: "153.0.1.2", Archives: map[string]Archive{platform: {SHA256: sum([]byte("x")), Size: 1000}}}
	lookPath = func() (string, bool) { return "", false }
	t.Cleanup(func() { pinned, lookPath = oldPin, oldLook })

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := FindContext(ctx, Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s to stop", d)
	}
}

func TestInstallWithoutPins(t *testing.T) {
	_, err := install(t.Context(), Release{}, "linux64", t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "no pinned Chrome for Testing") {
		t.Errorf("err = %v", err)
	}
}

func TestUnzipRefusesEscapes(t *testing.T) {
	for name, extra := range map[string]func(*zip.Writer){
		"dotdot": func(zw *zip.Writer) {
			w, _ := zw.Create("../evil")
			_, _ = w.Write([]byte("x"))
		},
		"absolute link": func(zw *zip.Writer) {
			h := &zip.FileHeader{Name: "chrome-linux64/passwd"}
			h.SetMode(os.ModeSymlink | 0o777)
			w, _ := zw.CreateHeader(h)
			_, _ = w.Write([]byte("/etc/passwd"))
		},
		"escaping link": func(zw *zip.Writer) {
			h := &zip.FileHeader{Name: "chrome-linux64/up"}
			h.SetMode(os.ModeSymlink | 0o777)
			w, _ := zw.CreateHeader(h)
			_, _ = w.Write([]byte("../../.."))
		},
		// x -> "." then c -> "x/.." passes a lexical check but c is the
		// parent of dest, so c/evil would land outside it.
		"link chain": func(zw *zip.Writer) {
			for _, l := range [][2]string{{"x", "."}, {"c", "x/.."}} {
				h := &zip.FileHeader{Name: l[0]}
				h.SetMode(os.ModeSymlink | 0o777)
				w, _ := zw.CreateHeader(h)
				_, _ = w.Write([]byte(l[1]))
			}
			w, _ := zw.Create("c/evil")
			_, _ = w.Write([]byte("x"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "a.zip")
			if err := os.WriteFile(zipPath, fakeArchive(t, "linux64", extra), 0o600); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "dest")
			if err := os.Mkdir(dest, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := unzip(zipPath, dest); err == nil {
				t.Error("unpacked an entry outside the folder")
			}
			if fileExists(filepath.Join(filepath.Dir(dest), "evil")) {
				t.Error("wrote a file next to dest")
			}
		})
	}
}

// A partial folder from an earlier install (no Chrome in it) is
// replaced instead of failing every later run.
func TestInstallReplacesAPartialFolder(t *testing.T) {
	good := fakeArchive(t, "linux64", nil)
	serve(t, "153.0.1.2", "linux64", good)
	rel := Release{Version: "153.0.1.2", Archives: map[string]Archive{"linux64": {SHA256: sum(good), Size: int64(len(good))}}}
	dir := t.TempDir()
	stale := filepath.Join(installDir(dir, rel.Version), "chrome-linux64", "leftover")
	if err := os.MkdirAll(filepath.Dir(stale), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin, err := install(t.Context(), rel, "linux64", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(bin) || fileExists(stale) {
		t.Errorf("bin %s exists = %v, stale file kept = %v", bin, fileExists(bin), fileExists(stale))
	}
}

func TestPlatformFor(t *testing.T) {
	for in, want := range map[string]string{
		"linux/amd64": "linux64", "linux/arm64": "linux-arm64", "darwin/arm64": "mac-arm64",
		"darwin/amd64": "mac-x64", "windows/amd64": "win64", "windows/386": "win32", "freebsd/amd64": "",
	} {
		goos, goarch, _ := strings.Cut(in, "/")
		if got := platformFor(goos, goarch); got != want {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
	if got := ArchiveURL("https://h/", "1.2", "mac-x64"); got != "https://h/1.2/mac-x64/chrome-mac-x64.zip" {
		t.Errorf("ArchiveURL = %s", got)
	}
}

func TestSandbox(t *testing.T) {
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	for _, c := range []struct {
		name    string
		uid     int
		env     string
		opt     bool
		off     bool
		err     bool
		warning bool
	}{
		{name: "user", uid: 1000},
		{name: "root", uid: 0, err: true},
		{name: "root with env", uid: 0, env: "1", off: true, warning: true},
		{name: "root with option", uid: 0, opt: true, off: true, warning: true},
		{name: "root with env 0", uid: 0, env: "0", err: true},
		{name: "user opting out", uid: 1000, env: "1", off: true, warning: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			geteuid = func() int { return c.uid }
			t.Setenv(NoSandboxEnv, c.env)
			var logs []string
			off, err := sandbox(Options{NoSandbox: c.opt, Log: func(m string) { logs = append(logs, m) }})
			if (err != nil) != c.err || off != c.off {
				t.Fatalf("off = %v, err = %v", off, err)
			}
			if c.err && !strings.Contains(err.Error(), NoSandboxEnv+"=1") {
				t.Errorf("err doesn't say how to opt in: %v", err)
			}
			if got := len(logs) == 1 && strings.HasPrefix(logs[0], "WARNING: Chrome is running without its sandbox"); got != c.warning {
				t.Errorf("logs = %q", logs)
			}
		})
	}
}

func TestSandboxHint(t *testing.T) {
	chrome := errors.New("[launcher] Failed to get the debug url: [1:1:FATAL:zygote_host_impl_linux.cc(127)] No usable sandbox! Update your kernel")
	if got := sandboxHint(chrome, false); !strings.Contains(got, NoSandboxEnv+"=1, if this container can't give Chrome its sandbox") {
		t.Errorf("hint = %q", got)
	}
	if got := sandboxHint(chrome, true); got != "" {
		t.Errorf("hinted with the sandbox already off: %q", got)
	}
	if got := sandboxHint(errors.New("error while loading shared libraries: libnss3.so"), false); got != "" {
		t.Errorf("hinted for an unrelated failure: %q", got)
	}
}

func TestDescribe(t *testing.T) {
	if v := productVersion("HeadlessChrome/153.0.8010.52"); v != "153.0.8010.52" {
		t.Errorf("productVersion = %q", v)
	}
	b := &Browser{Version: "153.0.1.2", Downloaded: true}
	if got := b.Describe(); got != "Chrome for Testing 153.0.1.2 (downloaded)" {
		t.Errorf("Describe = %q", got)
	}
	b = &Browser{Version: "153.0.8010.52", Bin: "/usr/bin/google-chrome"}
	if got := b.Describe(); got != "Chrome 153.0.8010.52 (/usr/bin/google-chrome)" {
		t.Errorf("Describe = %q", got)
	}
}
