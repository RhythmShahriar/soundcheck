// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/importer"
)

// maxImport is the largest file import reads: configs are a few KB.
const maxImport = 4 << 20

// cmdImport writes .soundcheck/config.yaml from a BackstopJS or Lost
// Pixel config. The file is only read: a Lost Pixel config is code, and
// it's scanned as text, never run. Unlike init it doesn't ask git to
// ignore the snapshot folders, so it starts no process at all; the
// first snapshot does that.
func cmdImport(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("import", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	force := fset.Bool("force", false, "")
	pagesOnly := fset.Bool("pages-only", false, "")
	site := fset.String("site", "", "")
	pos, err := cli.ParseInterspersed(fset, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, importUsage())
		return exitOK
	}
	if err == nil && len(pos) != 1 {
		err = cli.Usagef("%s", importUsage())
	}
	if err != nil {
		fmt.Fprintln(stderr, prefix(), flagError(fset, err))
		return exitUsage
	}
	src := pos[0]
	data, err := readImport(src)
	if err != nil {
		fmt.Fprintln(stderr, prefix(), err)
		return exitUsage
	}

	var res *importer.Result
	switch ext := strings.ToLower(filepath.Ext(src)); {
	case ext == ".ts" || ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".mts" || ext == ".cts":
		if !*pagesOnly {
			fmt.Fprintf(stderr, "%s %s is code, which Soundcheck never runs; add --pages-only to read just its Lost Pixel page paths\n", prefix(), src)
			if strings.HasSuffix(ext, "js") {
				fmt.Fprintf(stderr, "  for a backstop.js, save it as JSON first: node -p \"JSON.stringify(require('./%s'))\" > backstop.json\n", filepath.Base(src))
			}
			return exitUsage
		}
		res, err = importer.LostPixelPages(data)
		if err == nil && *site != "" {
			res.Site = *site
		}
	case *pagesOnly:
		fmt.Fprintf(stderr, "%s --pages-only is for a Lost Pixel config (lostpixel.config.ts); a backstop.json is read in full\n", prefix())
		return exitUsage
	default:
		res, err = importer.Backstop(data, *site)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", prefix(), src, err)
		return exitUsage
	}

	path := filepath.Join(config.Dir, "config.yaml")
	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(stderr, "%s %s already exists (--force replaces it)\n", prefix(), path)
		return exitUsage
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stderr, prefix(), err)
		return exitError
	}
	out, err := res.YAML(filepath.Base(src))
	if err == nil {
		err = saveConfig(path, out)
	}
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	fmt.Fprintf(stdout, "Wrote %s from %s:\n", path, src)
	for _, l := range res.Mapped {
		fmt.Fprintln(stdout, "  "+l)
	}
	if len(res.Mapped) == 0 {
		fmt.Fprintln(stdout, "  nothing: every setting keeps its default")
	}
	if len(res.Unmapped) > 0 {
		fmt.Fprintln(stdout, "Not imported:")
		for _, l := range res.Unmapped {
			fmt.Fprintln(stdout, "  - "+named(l))
		}
	}
	next := cmdName() + " snapshot <url>"
	if res.Site != "" {
		next = cmdName() + " snapshot" // site: is set
	}
	fmt.Fprintf(stdout, "Next: %s, then %s compare after your change\n", next, cmdName())
	return exitOK
}

func importUsage() string {
	return "usage: " + cmdName() + " import <backstop.json> [--site <url>] [--force]\n" +
		"       " + cmdName() + " import <lostpixel.config.ts> --pages-only [--site <url>] [--force]"
}

// readImport reads the file to import, refusing anything too big to be
// a config.
func readImport(path string) ([]byte, error) {
	fi, err := os.Stat(path) //nolint:gosec // the file the user asked to import
	switch {
	case err != nil:
		return nil, err
	case fi.IsDir():
		return nil, fmt.Errorf("%s is a folder; name the config file in it", path)
	case fi.Size() > maxImport:
		return nil, fmt.Errorf("%s is %d MB, too big for a config", path, fi.Size()>>20)
	}
	return os.ReadFile(path) //nolint:gosec // the file the user asked to import
}

// saveConfig checks data loads as a Soundcheck config, then puts it at
// path. It's checked in a temporary file beside path, so a config that
// wouldn't load is never left in place.
func saveConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".import-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Load runs config.Validate over the file.
	if _, err := config.Load(tmp.Name()); err != nil {
		return fmt.Errorf("the imported settings don't load, so nothing was written: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		return err
	}
	return os.Rename(tmp.Name(), path)
}
