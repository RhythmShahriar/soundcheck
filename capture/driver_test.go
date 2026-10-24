// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture_test

import (
	"context"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/capture/drivertest"
)

// TestChromeContract runs the driver contract against Chrome, which every
// other engine is held to.
func TestChromeContract(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	drivertest.Run(t, func(t *testing.T) capture.Driver {
		b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
		if err != nil {
			t.Fatal(err)
		}
		sess, err := b.Session()
		if err != nil {
			b.Close()
			t.Fatal(err)
		}
		d := capture.ChromeDriver(sess, b.Close)
		if d.Engine() != capture.Chrome {
			t.Errorf("Engine = %q", d.Engine())
		}
		return d
	}, drivertest.Options{})
}
