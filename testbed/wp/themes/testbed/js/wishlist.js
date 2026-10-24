// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// "Add to wishlist" buttons: remember the product and confirm.
(function () {
	document.querySelectorAll('.tb-wishlist').forEach(function (button) {
		button.addEventListener('click', function () {
			var saved = [];
			try { saved = JSON.parse(localStorage.getItem('tb-wishlist') || '[]'); } catch (e) {}
			saved.push(button.dataset.product);
			try { localStorage.setItem('tb-wishlist', JSON.stringify(saved)); } catch (e) {}
			var status = document.querySelector('.tb-wishlist-status');
			if (status) status.textContent = 'Added to your wishlist.';
			button.textContent = 'On your wishlist';
		});
	});
})();
