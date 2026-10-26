// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// A custom modal dialog done the way the WAI-ARIA dialog pattern asks:
// opening moves focus into it, Tab and Shift+Tab stay inside, Escape and
// the close button close it and focus goes back to the opener. It is the
// plain-PHP site's a11y/modal.js plus removeClose (the two sites share no
// files). Options switch off one part at a time for the broken states:
//
//   focusOnOpen: false   focus stays on the opener
//   escape: false        Escape does nothing
//   contain: false       Tab may leave the dialog (s52)
//   removeClose: true    closing (Escape or the button) removes the
//                        focused control, so focus is left on nothing (s51)
//
// Elements are found with document.getElementById, never through id
// globals: an id such as "open" or "close" names window.open there.
function testbedDialog(openerId, dialogId, closeId, options) {
	var o = Object.assign({focusOnOpen: true, escape: true, contain: true, removeClose: false}, options || {});
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
		if (o.removeClose) {
			// The 2.0 close routine, behind Escape and the close button
			// alike, replaces the dialog's content, the focused control
			// included, and forgets to put focus anywhere.
			if (dialog.contains(document.activeElement)) document.activeElement.remove();
			dialog.hidden = true;
			opener.setAttribute('aria-expanded', 'false');
			return;
		}
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
