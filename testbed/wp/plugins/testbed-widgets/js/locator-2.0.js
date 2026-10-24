// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Store locator 2.0 expects a settings object the plugin no longer
// prints, so it throws on every page load (scenario s05).
(function () {
	var settings = window.tbLocatorSettings;
	var map = settings.map.init(document.getElementById('store-locator'));
	map.render();
})();
