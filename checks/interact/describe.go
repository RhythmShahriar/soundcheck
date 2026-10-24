// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(New()) }

// Checks lists the checks the module reports.
func (*Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "interact/dead-click", Level: model.Warning, Title: "Control does nothing", Doc: "A button or link did nothing when clicked."},
		{ID: "interact/click-error", Level: model.Error, Title: "Click causes an error", Doc: "Clicking a control threw a JavaScript error."},
		{ID: "interact/click-request", Level: model.Error, Title: "Click request fails", Doc: "A request a click sent failed."},
		{ID: "interact/form-action", Level: model.Error, Title: "Form posts nowhere", Doc: "A form's action is missing or broken."},
		{ID: "interact/form-error", Level: model.Error, Title: "Form submission fails", Doc: "Submitting a form gave a server error."},
		{ID: "interact/form-submit", Level: model.Error, Title: "Form no longer submits", Doc: "A form with valid test data didn't submit."},
		{ID: "interact/form-validation", Level: model.Warning, Title: "Form shows no validation", Doc: "An empty submit showed no error message."},
		{ID: "interact/no-nonce", Level: model.Warning, Title: "Form without a security token", Doc: "A form that writes data has no nonce."},
		{ID: "interact/unnamed-field", Level: model.Warning, Title: "Field without a name", Doc: "A form field has no name, so its value is never sent."},
		{ID: "interact/unlabeled", Level: model.Notice, Title: "Required field without a label", Doc: "A required field has no label a visitor can read."},
		{ID: "interact/duplicate-id", Level: model.Notice, Title: "Duplicate ids", Doc: "Several elements share one id."},
		{ID: "interact/flow", Level: model.Error, Title: "Flow fails", Doc: "A flow in .soundcheck/flows stopped at a step."},
		{ID: "interact/flow-file", Level: model.Error, Title: "Flow file can't be read", Doc: "A flow file has a mistake."},
		{ID: "interact/flow-production", Level: model.Warning, Title: "Flow skipped on production", Doc: "Flows don't run on production sites."},
	}
}

// Phase puts interact after every other module on each page: its clicks
// and submits change the page, which the others must see as it loaded.
func (*Module) Phase() int { return check.PhaseMutate }
