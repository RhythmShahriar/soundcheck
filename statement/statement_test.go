// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

// The config file may have a statement: block.
func TestBlockRegistered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("statement:\n  owner: Example Coffee Ltd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("statement: isn't accepted: %v", err)
	}
	if cfg.Block("statement").Kind == 0 {
		t.Error("the statement block is lost")
	}
	if _, err := DecodeOwner(cfg); err != nil {
		t.Error(err)
	}
}

func TestBuildOfNothing(t *testing.T) {
	if _, err := Build(Input{}); !errors.Is(err, ErrMissingOwner) {
		t.Errorf("Build of nothing = %v", err)
	}
}
