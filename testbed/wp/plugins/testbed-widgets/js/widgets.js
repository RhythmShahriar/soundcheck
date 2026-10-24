// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Stock checker (admin-ajax) and reviews (REST API) widgets.
(function () {
	document.querySelectorAll('.tb-stock').forEach(function (el) {
		fetch(tbWidgets.ajax + '?action=testbed_stock&product=' + encodeURIComponent(el.dataset.product))
			.then(function (r) { return r.json(); })
			.then(function (r) { el.textContent = r.success ? r.data.stock + ' bags in stock' : 'Stock unavailable right now'; })
			.catch(function () { el.textContent = 'Stock unavailable right now'; });
	});
	document.querySelectorAll('.tb-reviews').forEach(function (el) {
		fetch(tbWidgets.rest)
			.then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
			.then(function (list) {
				el.innerHTML = list.map(function (x) { return '<blockquote>' + x.text + ' <cite>' + x.author + '</cite></blockquote>'; }).join('');
			})
			.catch(function () { el.textContent = 'Reviews are unavailable.'; });
	});
})();
