// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"errors"
	"testing"
)

func TestStubs(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, nil, nil); !errors.Is(err, ErrNotBuilt) {
		t.Errorf("Write = %v", err)
	}
	if err := Verify(dir); err == nil {
		t.Error("Verify of an empty folder succeeded")
	}
	if prev, err := Previous(dir, "http://brew.test/"); prev != nil || err != nil {
		t.Errorf("Previous in an empty store = %v, %v", prev, err)
	}
}
