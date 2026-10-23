// Wishlist 2.0 moved storage into a WishlistStore helper that this
// release forgot to ship, so every click throws (scenario s13).
(function () {
	document.querySelectorAll('.tb-wishlist').forEach(function (button) {
		button.addEventListener('click', function () {
			WishlistStore.add(button.dataset.product);
			var status = document.querySelector('.tb-wishlist-status');
			if (status) status.textContent = 'Added to your wishlist.';
			button.textContent = 'On your wishlist';
		});
	});
})();
