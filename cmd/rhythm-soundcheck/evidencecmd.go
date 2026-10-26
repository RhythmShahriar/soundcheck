// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/evidence"
	"github.com/RhythmShahriar/soundcheck/store"
)

const evidenceUsage = `Usage:
  rhythm soundcheck evidence verify [run folder|snapshot]
  rhythm soundcheck evidence sign [run folder|snapshot] --key <file>
  rhythm soundcheck evidence keygen -o <file>

Every --eaa run writes an evidence pack, eaa/evidence.json: the SHA-256 of
each file the run recorded, chained to the previous --eaa run of the same
site. verify checks the files and the chain (default: the latest --eaa
run or snapshot). sign signs the pack with an ed25519 key from keygen, which
prints the public key to give whoever checks it. A run made with -k can't
be signed: its TLS certificates weren't verified.
Exit codes: 0 verified or signed · 1 verification failed (names the file or the gap) · 2 usage · 3 can't read
`

// The evidence package's commands, as variables so the tests can stand
// in for them.
var (
	verifyEvidence = evidence.Verify
	signEvidence   = evidence.Sign
	keygenEvidence = evidence.Keygen
)

// cmdEvidence verifies or signs a run's evidence pack, or makes a key.
func cmdEvidence(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("evidence", flag.ContinueOnError)
	key := flags.String("key", "", "")
	var out string
	flags.StringVar(&out, "o", "", "")
	flags.StringVar(&out, "output", "", "")
	pos, code, ok := subFlags(flags, args, evidenceUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) == 0 {
		fmt.Fprint(stderr, named(evidenceUsage))
		return exitUsage
	}
	sub, rest := pos[0], pos[1:]
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case sub != "verify" && sub != "sign" && sub != "keygen":
		return failed(stderr, cli.Usagef("evidence %s: want verify, sign or keygen", sub))
	case sub == "keygen" && len(rest) > 0, sub != "keygen" && len(rest) > 1:
		fmt.Fprint(stderr, named(evidenceUsage))
		return exitUsage
	case sub != "sign" && set["key"]:
		return failed(stderr, cli.Usagef("--key goes with evidence sign"))
	case sub != "keygen" && (set["o"] || set["output"]):
		return failed(stderr, cli.Usagef("-o goes with evidence keygen"))
	}
	if sub == "keygen" {
		return keygen(out, stdout, stderr)
	}
	if sub == "sign" && *key == "" {
		return failed(stderr, cli.Usagef("evidence sign needs --key <file>; make one with %s evidence keygen -o <file>", cmdName()))
	}
	dir, err := statementSource(store.Store{Root: config.Dir}, rest)
	if err != nil {
		return failed(stderr, err)
	}
	pack := filepath.Join(dir, eaaDir, evidenceFile)
	data, err := os.ReadFile(pack) //nolint:gosec // the evidence pack of a run folder
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return failed(stderr, fmt.Errorf("%s has no evidence pack (%s/%s); only --eaa runs write one", rel(dir), eaaDir, evidenceFile))
	case err != nil:
		return failed(stderr, err)
	}
	if sub == "verify" {
		if err := verifyEvidence(dir); err != nil {
			if errors.Is(err, evidence.ErrNotBuilt) {
				return failed(stderr, err)
			}
			fmt.Fprintln(stderr, prefix(), err)
			return exitFindings
		}
		fmt.Fprintf(stdout, "Verified %s: every file matches and the chain is whole\n", rel(pack))
		return exitOK
	}
	// The pack says whether the run checked TLS; a signature would vouch
	// for pages that may not have come from the site.
	var head struct {
		Insecure bool `json:"insecure"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return failed(stderr, fmt.Errorf("%s: %w", rel(pack), err))
	}
	if head.Insecure {
		return failed(stderr, cli.Usagef("can't sign a run made with -k: TLS wasn't verified"))
	}
	if _, err := os.Stat(*key); err != nil {
		return failed(stderr, fmt.Errorf("--key: %w", err))
	}
	if err := signEvidence(dir, *key); err != nil {
		return failed(stderr, err)
	}
	fmt.Fprintf(stdout, "Signed %s\n", rel(pack))
	return exitOK
}

// keygen writes a new signing key to path and prints its public key. It
// never replaces a file: a lost key would leave signed packs nobody can
// sign again with the same key.
func keygen(path string, stdout, stderr io.Writer) int {
	if path == "" || path == "-" {
		return failed(stderr, cli.Usagef("evidence keygen needs -o <file> for the private key"))
	}
	if _, err := os.Lstat(path); err == nil {
		return failed(stderr, cli.Usagef("%s already exists; keygen never replaces a key, so name a new file", path))
	}
	pub, err := keygenEvidence(path)
	if err != nil {
		return failed(stderr, err)
	}
	fmt.Fprintf(stdout, "public key: %s\n", pub)
	fmt.Fprintf(stderr, "Private key written to %s; keep it out of git\n", path)
	return exitOK
}
