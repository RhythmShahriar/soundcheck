// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"os"
	"testing"
)

// TestMain clears the proxy settings of the environment before any test
// runs. The run's client follows them (http.ProxyFromEnvironment), which
// would send a request a regressed guard let through to a real proxy
// instead of the tests' 127.0.0.1 traps. Go reads them once a process,
// so they're cleared here and not per test.
func TestMain(m *testing.M) {
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "REQUEST_METHOD",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
