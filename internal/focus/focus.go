// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package focus presses keys the way a visitor does and reads where
// keyboard focus is. Key presses go through the Chrome DevTools
// protocol (Input.dispatchKeyEvent), so the page gets trusted events it
// can't tell from a real keyboard, never synthetic ones from
// dispatchEvent. The focused element is read in an isolated world, so a
// page that overrides document.activeElement or the DOM prototypes
// can't misreport it.
package focus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/checks/a11y"
)

// Key is a key the keyboard walk and key flows press.
type Key string

// The keys Press knows, named as key flows write them.
const (
	Tab        Key = "Tab"
	ShiftTab   Key = "Shift+Tab"
	Enter      Key = "Enter"
	Space      Key = "Space"
	Escape     Key = "Escape"
	ArrowUp    Key = "ArrowUp"
	ArrowDown  Key = "ArrowDown"
	ArrowLeft  Key = "ArrowLeft"
	ArrowRight Key = "ArrowRight"
	Home       Key = "Home"
	End        Key = "End"
)

// keys maps each Key to the key it presses; ShiftTab is Tab with Shift
// held.
var keys = map[Key]input.Key{
	Tab: input.Tab, ShiftTab: input.Tab, Enter: input.Enter, Space: input.Space, Escape: input.Escape,
	ArrowUp: input.ArrowUp, ArrowDown: input.ArrowDown, ArrowLeft: input.ArrowLeft, ArrowRight: input.ArrowRight,
	Home: input.Home, End: input.End,
}

// ErrUnknownKey is Press's error for a key it doesn't know.
var ErrUnknownKey = errors.New("unknown key")

// Press presses and releases k on page, as a trusted key event.
//
// The events are rod's own (input.Key.Encode), sent with page bound to
// ctx: rod's Keyboard sends with the page's own context, so a canceled
// walk would go on pressing.
func Press(ctx context.Context, page *rod.Page, k Key) error {
	key, ok := keys[k]
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownKey, k)
	}
	p := page.Context(ctx)
	mods := 0
	if k == ShiftTab {
		mods = input.ShiftLeft.Modifier()
		if err := input.ShiftLeft.Encode(proto.InputDispatchKeyEventTypeKeyDown, mods).Call(p); err != nil {
			return err
		}
		defer func() { _ = input.ShiftLeft.Encode(proto.InputDispatchKeyEventTypeKeyUp, 0).Call(p) }()
	}
	if err := key.Encode(proto.InputDispatchKeyEventTypeKeyDown, mods).Call(p); err != nil {
		return err
	}
	return key.Encode(proto.InputDispatchKeyEventTypeKeyUp, mods).Call(p)
}

// Stop is one keyboard focus stop: the element that has focus, as far
// as it can be told without storing what a visitor typed. Its JSON
// names are fixed: they are the fields of every forward and backward
// entry of a keyboard trace (keyboard/<pageid>.json).
type Stop struct {
	Selector      string     `json:"selector"`        // stable selector (a11y.Selector), " >>> " into shadow roots
	BackendNodeID int        `json:"backend_node_id"` // the element's DOM node, for telling stops apart in one walk
	Tag           string     `json:"tag"`             // lowercase tag name
	Role          string     `json:"role"`            // ARIA role
	NameLen       int        `json:"name_len"`        // length of the accessible name, in runes
	Name          string     `json:"name"`            // the accessible name, redacted, at most 120 runes
	Box           [4]float64 `json:"box"`             // x, y, width and height in CSS pixels
	InViewport    bool       `json:"in_viewport"`     // the box is at least partly on screen
	InDialog      bool       `json:"in_dialog"`       // inside an open dialog or a visible aria-modal element
	Connected     bool       `json:"connected"`       // still in the document
	IsBody        bool       `json:"is_body"`         // the document itself has focus: the walk wrapped round
	IsFrame       bool       `json:"is_frame"`        // focus is in a frame
	FrameHost     string     `json:"frame_host"`      // a cross-origin frame's host: nothing else is read there
}

// deepActive finds the focused element through open shadow roots.
const deepActive = `(() => {
  let el = document.activeElement;
  while (el && el.shadowRoot && el.shadowRoot.activeElement) el = el.shadowRoot.activeElement;
  return el;
})()`

// describe reads a Stop from the focused element (this). The path is
// built the way the a11y module builds it, so a11y.Selector gives both
// the same selector for the same element.
const describe = `function () {
  const el = this;
  const trees = [];
  let tree = [];
  for (let n = el; n && n.nodeType === 1;) {
    const p = n.parentNode;
    const same = p ? Array.from(p.children || []).filter(c => c.localName === n.localName) : [];
    tree.unshift({tag: n.localName, id: n.id || '', name: n.getAttribute('name') || '', nth: same.length > 1 ? same.indexOf(n) + 1 : 0});
    if (p && p.nodeType === 11 && p.host) {
      trees.unshift(tree);
      tree = [];
      n = p.host;
    } else {
      n = n.parentElement;
    }
  }
  trees.unshift(tree);
  const r = el.getBoundingClientRect();
  const tag = el.localName;
  return JSON.stringify({
    path: trees, tag,
    box: [r.x, r.y, r.width, r.height],
    in_viewport: r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth,
    in_dialog: !!el.closest('dialog[open], [aria-modal="true"]'),
    connected: el.isConnected,
    is_body: el === document.body || el === document.documentElement,
    is_frame: tag === 'iframe' || tag === 'frame',
  });
}`

// Active returns the stop that has focus on page: the deep active
// element, through open shadow roots. With nothing focused it is the
// document's stop (IsBody).
func Active(ctx context.Context, page *rod.Page) (Stop, error) {
	p := page.Context(ctx)
	world, err := proto.PageCreateIsolatedWorld{FrameID: p.FrameID, WorldName: "soundcheck-focus"}.Call(p)
	if err != nil {
		return Stop{}, fmt.Errorf("can't open an isolated world: %w", err)
	}
	res, err := proto.RuntimeEvaluate{Expression: deepActive, ContextID: world.ExecutionContextID}.Call(p)
	if err != nil {
		return Stop{}, err
	}
	if res.ExceptionDetails != nil {
		return Stop{}, errors.New(res.ExceptionDetails.Text)
	}
	if res.Result == nil || res.Result.ObjectID == "" {
		return Stop{Tag: "body", IsBody: true, Connected: true}, nil
	}
	obj := res.Result.ObjectID
	defer func() { _ = proto.RuntimeReleaseObject{ObjectID: obj}.Call(p) }()
	call, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: describe, ObjectID: obj, ReturnByValue: true}.Call(p)
	if err != nil {
		return Stop{}, err
	}
	if call.ExceptionDetails != nil {
		return Stop{}, errors.New(call.ExceptionDetails.Text)
	}
	var raw struct {
		Path [][]a11y.Elem `json:"path"`
		Stop
	}
	if err := json.Unmarshal([]byte(call.Result.Value.Str()), &raw); err != nil {
		return Stop{}, fmt.Errorf("focused element: %w", err)
	}
	s := raw.Stop
	s.Selector = a11y.Selector(raw.Path)
	node, err := proto.DOMDescribeNode{ObjectID: obj}.Call(p)
	if err != nil {
		return Stop{}, err
	}
	s.BackendNodeID = int(node.Node.BackendNodeID)
	return s, nil
}
