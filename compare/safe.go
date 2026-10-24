// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// safe runs fn, one module's hook and turns a panic in it into a
// warning, so a bug in one module costs its own changes, not the whole
// comparison. The warning reads "<module>: panic in <hook>: <value>
// (<top 3 frames>)", the format the runner uses for the other hooks.
func safe(module, hook string, warn func(string), fn func()) {
	defer func() {
		if v := recover(); v != nil {
			warn(fmt.Sprintf("%s: panic in %s: %v (%s)", module, hook, v, frames(3)))
		}
	}()
	fn()
}

// self is this package's import path, where a module's own frames end.
const self = "github.com/RhythmShahriar/soundcheck/compare"

// hookCallers are the functions of this package that call modules'
// hooks, through closures named after them ("Compare.func3").
var hookCallers = []string{".Compare.", ".finish.", ".Stream.", ".feedBefore."}

// frames describes up to n frames of a panicking goroutine's stack as
// "function file:line", from where the panic happened down to the
// module's hook. The runtime's frames (gopanic, a nil map write) are
// skipped. So are hookCallers and safe below the hook: they're the same
// for every panic. Called from the deferred recover, the stack still
// holds the panicking frames.
func frames(n int) string {
	pcs := make([]uintptr, 32)
	it := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	var out []string
	for len(out) < n {
		f, more := it.Next()
		if f.Function == self+".safe" || slices.ContainsFunc(hookCallers, func(c string) bool { return strings.HasPrefix(f.Function, self+c) }) {
			break
		}
		if !strings.HasPrefix(f.Function, "runtime.") && f.Function != self+".safe.func1" {
			name := f.Function[strings.LastIndexByte(f.Function, '/')+1:]
			out = append(out, fmt.Sprintf("%s %s:%d", name, filepath.Base(f.File), f.Line))
		}
		if !more {
			break
		}
	}
	return strings.Join(out, ", ")
}
