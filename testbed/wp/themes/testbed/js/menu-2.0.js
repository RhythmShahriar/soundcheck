// Menu 2.0 renamed the button's class to .js-menu-toggle, but the header
// template still renders .menu-toggle, so nothing is bound and the button
// is dead (scenario s12).
(function () {
	var button = document.querySelector('.js-menu-toggle');
	var panel = document.getElementById('browse-panel');
	if (!button || !panel) return;
	button.addEventListener('click', function () {
		var open = button.getAttribute('aria-expanded') === 'true';
		button.setAttribute('aria-expanded', String(!open));
		panel.hidden = open;
	});
})();
