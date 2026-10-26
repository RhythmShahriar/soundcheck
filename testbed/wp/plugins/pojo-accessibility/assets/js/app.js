// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A stand-in for an accessibility overlay's script, served from the
// path the real plugin uses (s73) so Soundcheck's overlay check can see
// it without the testbed asking anything of another host. It does
// nothing: the scenario is that the script is on the page.
window.testbedOverlayStub = true;
