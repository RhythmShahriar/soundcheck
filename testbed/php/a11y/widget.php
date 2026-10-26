<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Scenario s67: a web component loses its label. The card number field
// is a custom element, <tb-card-field>, whose input lives in an open
// shadow root. Good: the shadow root has a <label for> for it. Broken:
// the component's 2.0 template dropped the label, so the field has no
// name. a11y/label must report it with " >>> " in its source (the
// selector goes into the shadow root).

require __DIR__ . '/../layout.php';
$broken = testbed_broken( 's67' );
page_start( 'Pay an invoice' );
?>
<h1>Pay invoice 1042</h1>
<p>£420.00 to Ledger Ltd.</p>
<tb-card-field id="card-field"></tb-card-field>
<script>
customElements.define('tb-card-field', class extends HTMLElement {
	connectedCallback() {
		if (this.shadowRoot) return;
		var root = this.attachShadow({mode: 'open'});
		root.innerHTML = '<style>label{display:block;font-weight:600}input{font:inherit;padding:8px;border:1px solid #5b6577;border-radius:4px}input:focus-visible{outline:3px solid #2f6fdb;outline-offset:2px}</style>' +
			<?= $broken ? "''" : "'<label for=\"card\">Card number</label>'" ?> +
			'<input id="card" name="card" type="text" inputmode="numeric" autocomplete="cc-number">';
	}
});
</script>
<?php
testbed_selftest(
	sprintf(
		<<<'JS'
	var root = document.getElementById('card-field').shadowRoot;
	var input = root && root.getElementById('card');
	return input && (input.labels.length === 0) === %s ? true : 'shadow label';
JS,
		$broken ? 'true' : 'false'
	)
);
page_end();
