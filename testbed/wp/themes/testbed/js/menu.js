// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Opens and closes the Browse panel from the header button.
(function () {
	var button = document.querySelector('.menu-toggle');
	var panel = document.getElementById('browse-panel');
	if (!button || !panel) return;
	button.addEventListener('click', function () {
		var open = button.getAttribute('aria-expanded') === 'true';
		button.setAttribute('aria-expanded', String(!open));
		panel.hidden = open;
	});
})();
