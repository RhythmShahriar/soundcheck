// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A custom modal dialog done the way the WAI-ARIA dialog pattern asks:
// opening moves focus into it, Tab and Shift+Tab stay inside, Escape and
// the close button close it and focus goes back to the opener. Options
// switch off one part at a time for the broken states:
//
//   focusOnOpen: false   focus stays on the opener (s53)
//   escape: false        Escape does nothing (s75)
//   contain: false       Tab may leave the dialog (a non-modal dialog)
//
// Elements are found with document.getElementById, never through id
// globals: an id such as "open" or "close" names window.open there.
function testbedDialog(openerId, dialogId, closeId, options) {
	var o = Object.assign({focusOnOpen: true, escape: true, contain: true}, options || {});
	var opener = document.getElementById(openerId);
	var dialog = document.getElementById(dialogId);
	var close = document.getElementById(closeId);
	var focusable = function () {
		return Array.prototype.filter.call(
			dialog.querySelectorAll('a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])'),
			function (el) { return !el.disabled; }
		);
	};
	var hide = function () {
		dialog.hidden = true;
		opener.setAttribute('aria-expanded', 'false');
		opener.focus();
	};
	opener.addEventListener('click', function () {
		dialog.hidden = false;
		opener.setAttribute('aria-expanded', 'true');
		if (o.focusOnOpen) focusable()[0].focus();
	});
	close.addEventListener('click', hide);
	dialog.addEventListener('keydown', function (e) {
		if (e.key === 'Escape' && o.escape) {
			e.preventDefault();
			hide();
			return;
		}
		if (e.key !== 'Tab' || !o.contain) return;
		var els = focusable();
		var first = els[0];
		var last = els[els.length - 1];
		if (e.shiftKey && document.activeElement === first) {
			e.preventDefault();
			last.focus();
		} else if (!e.shiftKey && document.activeElement === last) {
			e.preventDefault();
			first.focus();
		}
	});
	return {opener: opener, dialog: dialog, close: close, hide: hide};
}
