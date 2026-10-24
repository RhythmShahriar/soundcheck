// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"fmt"
	"path"
	"runtime"
	"strings"
)

// Safe calls fn, a module's hook, and turns a panic in it into a warning
// instead of a crashed run: one bug in one module (a nil map, an index
// out of range) mustn't lose every other module's results or leave
// Chrome running. The warning reads
// "<module>: panic in <hook>: <value> (<top 3 frames>)", the frames being
// where the panic happened, so the module's author can find it.
func Safe(module, hook string, warn func(string), fn func()) {
	defer func() {
		if v := recover(); v != nil {
			msg := fmt.Sprintf("%s: panic in %s: %v", module, hook, v)
			if fr := panicFrames(3); fr != "" {
				msg += " (" + fr + ")"
			}
			warn(msg)
		}
	}()
	fn()
}

// panicFrames describes the top n frames of the panicking goroutine's
// stack, from where the panic happened outwards, as "pkg.Func file:line"
// joined with ", ". It must be called from the deferred function that
// recovered.
func panicFrames(n int) string {
	pcs := make([]uintptr, 64)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(1, pcs)])
	var out []string
	panicking := false
	for len(out) < n {
		f, more := frames.Next()
		switch {
		case f.Function == "runtime.gopanic":
			panicking = true
		case panicking && !strings.HasPrefix(f.Function, "runtime."):
			// The function name keeps its package's last path element:
			// ".../checks/health.Module.CheckPage" becomes
			// "health.Module.CheckPage".
			out = append(out, fmt.Sprintf("%s %s:%d", path.Base(f.Function), path.Base(f.File), f.Line))
		}
		if !more {
			break
		}
	}
	return strings.Join(out, ", ")
}
