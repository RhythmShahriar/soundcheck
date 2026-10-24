// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:generate go run ./internal/pingen -o cft_pins.go

// cftBase is where Chrome for Testing archives are downloaded from:
// Google's own storage and nothing else. Tests point it at a local
// server.
var cftBase = "https://storage.googleapis.com/chrome-for-testing-public"

// Archive is one platform's pinned Chrome for Testing download.
type Archive struct {
	SHA256 string // hex SHA-256 of the zip
	Size   int64  // bytes, for the progress message and a size limit
}

// Release is a pinned Chrome for Testing version with one archive per
// platform (linux64, linux-arm64, mac-arm64, mac-x64, win32, win64).
type Release struct {
	Version  string
	Archives map[string]Archive
}

// pinned is the release this build downloads. It's a variable so tests
// can swap in their own; cft_pins.go sets it from the generated pins.
var pinned = Release{Version: cftVersion, Archives: cftArchives}

// PinnedVersion is the Chrome for Testing version this build downloads
// when no Chrome is installed, or "" when the build has no pins.
func PinnedVersion() string { return pinned.Version }

// maxArchive caps a download well above any real Chrome for Testing
// zip (about 150 to 250 MB), so a broken mirror can't fill the disk.
const maxArchive = 1 << 30

// Platform is the Chrome for Testing name for the platform Soundcheck
// runs on, or "" when there's no build for it.
func Platform() string { return platformFor(runtime.GOOS, runtime.GOARCH) }

func platformFor(goos, goarch string) string {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "linux64"
	case "linux/arm64":
		return "linux-arm64"
	case "darwin/arm64":
		return "mac-arm64"
	case "darwin/amd64":
		return "mac-x64"
	case "windows/amd64", "windows/arm64": // arm64 Windows runs x64 Chrome
		return "win64"
	case "windows/386":
		return "win32"
	}
	return ""
}

// ArchiveURL is where the Chrome for Testing zip for version and
// platform is published.
func ArchiveURL(base, version, platform string) string {
	return fmt.Sprintf("%s/%s/%s/chrome-%s.zip", strings.TrimSuffix(base, "/"), version, platform, platform)
}

// installDir is the folder a release is unpacked into, under the
// download dir.
func installDir(dir, version string) string { return filepath.Join(dir, "cft-"+version) }

// binPath is the Chrome executable inside an unpacked archive.
func binPath(root, platform string) string {
	top := filepath.Join(root, "chrome-"+platform)
	switch {
	case strings.HasPrefix(platform, "mac"):
		return filepath.Join(top, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
	case strings.HasPrefix(platform, "win"):
		return filepath.Join(top, "chrome.exe")
	}
	return filepath.Join(top, "chrome")
}

// install downloads rel's archive for platform into dir, checks its
// SHA-256 against the pin before unpacking anything and returns the
// Chrome binary. A mismatch leaves nothing behind.
func install(ctx context.Context, rel Release, platform, dir string, log func(string)) (string, error) {
	pin, ok := rel.Archives[platform]
	if !ok || rel.Version == "" {
		return "", fmt.Errorf("this build of Soundcheck has no pinned Chrome for Testing for %s; install Chrome or pass --browser /path/to/chrome", platformName(platform))
	}
	final := installDir(dir, rel.Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	url := ArchiveURL(cftBase, rel.Version, platform)
	if log != nil {
		log(fmt.Sprintf("Downloading Chrome for Testing %s (%d MB) once, into %s …", rel.Version, (pin.Size+(1<<20)-1)>>20, dir))
	}
	zipPath, err := download(ctx, url, dir, pin)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(zipPath) }()

	tmp, err := os.MkdirTemp(dir, ".unpack-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := unzip(zipPath, tmp); err != nil {
		return "", fmt.Errorf("unpacking Chrome for Testing: %w", err)
	}
	if _, err := os.Stat(binPath(tmp, platform)); err != nil {
		return "", fmt.Errorf("the Chrome for Testing archive has no %s", filepath.Base(binPath(tmp, platform)))
	}
	if err := os.Rename(tmp, final); err != nil {
		// Another run may have finished the same install first.
		if _, statErr := os.Stat(binPath(final, platform)); statErr != nil {
			return "", err
		}
	}
	return binPath(final, platform), nil
}

func platformName(p string) string {
	if p == "" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return p
}

// download fetches url into a temporary file in dir while hashing it,
// and removes the file unless its size and SHA-256 match pin.
func download(ctx context.Context, url, dir string, pin Archive) (path string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL is cftBase plus a pinned version and platform
	if err != nil {
		return "", fmt.Errorf("downloading Chrome for Testing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading Chrome for Testing: %s from %s", resp.Status, url)
	}
	f, err := os.CreateTemp(dir, ".download-*.zip")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return "", fmt.Errorf("downloading Chrome for Testing: %w", err)
	}
	if n > maxArchive {
		return "", errors.New("downloading Chrome for Testing: the archive is larger than 1 GB; refusing it")
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, pin.SHA256) || (pin.Size > 0 && n != pin.Size) {
		return "", fmt.Errorf("the Chrome for Testing download from %s doesn't match the checksum this build pins (got sha256 %s, want %s); nothing was installed", url, got, pin.SHA256)
	}
	return f.Name(), f.Close()
}

// unzip unpacks src into dest, keeping file modes (Chrome must stay
// executable) and the symlinks inside a macOS app bundle. Entries that
// would land outside dest are refused.
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		if err := unzipOne(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func unzipOne(f *zip.File, dest string) error {
	target, err := inside(dest, f.Name)
	if err != nil {
		return err
	}
	mode := f.Mode()
	switch {
	case mode.IsDir():
		return os.MkdirAll(target, 0o750)
	case mode&os.ModeSymlink != 0:
		link, err := readAll(f, 4096)
		if err != nil {
			return err
		}
		// A link may point anywhere inside the archive, never out of it.
		if filepath.IsAbs(link) {
			return fmt.Errorf("%s: absolute symlink %s", f.Name, link)
		}
		if _, err := inside(dest, filepath.Join(filepath.Dir(f.Name), link)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.Symlink(link, target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm()|0o600) //nolint:gosec // target is checked to be inside dest
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, maxArchive)); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// inside joins name onto dest and refuses a result outside dest.
func inside(dest, name string) (string, error) {
	p := filepath.Join(dest, name) //nolint:gosec // checked just below
	if p != dest && !strings.HasPrefix(p, dest+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q points outside the folder", name)
	}
	return p, nil
}

func readAll(f *zip.File, limit int64) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, limit))
	return string(b), err
}
