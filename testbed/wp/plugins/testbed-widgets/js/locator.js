// Store locator: lists the shops in #store-locator.
(function () {
	var el = document.getElementById('store-locator');
	if (!el) return;
	var shops = ['Brew & Co Soho, 12 Dean Street', 'Brew & Co Leeds, 4 Briggate', 'Brew & Co Bath, 9 Milsom Street'];
	el.innerHTML = '<ul>' + shops.map(function (s) { return '<li>' + s + '</li>'; }).join('') + '</ul>';
})();
