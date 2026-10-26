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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/internal/pii"
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
//
// When focus is in a frame from another site (IsFrame), the stop is the
// frame element of the page itself: its selector, node, tag, box and
// the frame's host. Nothing is read inside such a frame, not even its
// name.
type Stop struct {
	Selector      string     `json:"selector"`        // stable selector (a11y.Selector), " >>> " into shadow roots and same-origin frames
	BackendNodeID int        `json:"backend_node_id"` // the element's DOM node, for telling stops apart in one walk
	Tag           string     `json:"tag"`             // lowercase tag name
	Role          string     `json:"role"`            // computed ARIA role
	NameLen       int        `json:"name_len"`        // length of the accessible name, in runes
	Name          string     `json:"name"`            // the accessible name, redacted, at most 120 runes
	Box           [4]float64 `json:"box"`             // x, y, width and height in CSS pixels of the top viewport
	InViewport    bool       `json:"in_viewport"`     // the box is at least partly on screen
	InDialog      bool       `json:"in_dialog"`       // inside an open dialog or a visible aria-modal element
	Connected     bool       `json:"connected"`       // still in the document
	IsBody        bool       `json:"is_body"`         // the document itself has focus: the walk wrapped round
	IsFrame       bool       `json:"is_frame"`        // focus is in a frame from another origin, which isn't read
	FrameHost     string     `json:"frame_host"`      // that frame's host: nothing else is read there
}

// Caps on what a Stop keeps.
const (
	maxName     = 120 // runes of Name
	maxSelector = 300 // runes of Selector
)

// deepActive finds the focused element through open shadow roots and
// frames of the same origin. A frame of another origin has no readable
// document (contentDocument is null), so focus inside it ends at the
// frame element.
const deepActive = `(() => {
  let el = document.activeElement;
  for (;;) {
    if (el && el.shadowRoot && el.shadowRoot.activeElement) { el = el.shadowRoot.activeElement; continue; }
    if (el && (el.localName === 'iframe' || el.localName === 'frame')) {
      let doc = null;
      try { doc = el.contentDocument; } catch (e) { doc = null; }
      if (doc && doc.activeElement && doc.activeElement !== doc.body && doc.activeElement !== doc.documentElement) {
        el = doc.activeElement;
        continue;
      }
    }
    return el;
  }
})()`

// describe reads a Stop from the focused element (this). The path is
// built the way the a11y module builds it, so a11y.Selector gives both
// the same selector for the same element; a same-origin frame starts a
// new tree like a shadow root does. Names and values are never read
// here: the name comes from Chrome's accessibility tree (see Reader).
const describe = `function () {
  const el = this;
  const up = n => {
    if (n.parentElement) return n.parentElement;
    const root = n.parentNode;
    if (root && root.nodeType === 11 && root.host) return root.host;
    const view = n.ownerDocument && n.ownerDocument.defaultView;
    try { return view && view.frameElement; } catch (e) { return null; }
  };
  const trees = [];
  let tree = [];
  for (let n = el; n && n.nodeType === 1;) {
    const p = n.parentNode;
    const same = p ? Array.from(p.children || []).filter(c => c.localName === n.localName) : [];
    tree.unshift({tag: n.localName, id: n.id || '', name: n.getAttribute('name') || '', nth: same.length > 1 ? same.indexOf(n) + 1 : 0});
    const next = up(n);
    if (next && next !== n.parentElement) {
      trees.unshift(tree);
      tree = [];
    }
    n = next;
  }
  trees.unshift(tree);
  const visible = n => {
    const s = n.ownerDocument.defaultView.getComputedStyle(n);
    const r = n.getBoundingClientRect();
    return s.display !== 'none' && s.visibility !== 'hidden' && r.width > 0 && r.height > 0;
  };
  let dialog = false;
  for (let n = el; n && !dialog; n = up(n)) {
    if (n.localName === 'dialog' && n.open) dialog = true;
    else if (n.getAttribute && n.getAttribute('aria-modal') === 'true' && visible(n)) dialog = true;
  }
  const r = el.getBoundingClientRect();
  let x = r.x, y = r.y;
  for (let w = el.ownerDocument.defaultView; w && w !== w.parent;) {
    let f = null;
    try { f = w.frameElement; } catch (e) { f = null; }
    if (!f) break;
    const fr = f.getBoundingClientRect();
    x += fr.x + f.clientLeft;
    y += fr.y + f.clientTop;
    w = w.parent;
  }
  const tag = el.localName;
  const doc = el.ownerDocument;
  let cross = false;
  if (tag === 'iframe' || tag === 'frame') {
    try { cross = !el.contentDocument; } catch (e) { cross = true; }
  }
  return JSON.stringify({
    path: trees, tag,
    box: [x, y, r.width, r.height],
    in_viewport: y + r.height > 0 && x + r.width > 0 && y < innerHeight && x < innerWidth,
    in_dialog: dialog,
    connected: el.isConnected,
    is_body: el === doc.body || el === doc.documentElement,
    is_frame: cross,
    src: cross ? (el.src || '') : '',
  });
}`

// Reader reads focus on one page. It keeps one isolated world for its
// reads, made again when the page loads a new document, where Active
// would make one per call.
type Reader struct {
	page  *rod.Page
	mu    sync.Mutex
	world proto.RuntimeExecutionContextID
}

// NewReader returns a Reader for page.
func NewReader(page *rod.Page) *Reader { return &Reader{page: page} }

// Active returns the stop that has focus on page: the deep active
// element, through open shadow roots and same-origin frames. With
// nothing focused it is the document's stop (IsBody).
func Active(ctx context.Context, page *rod.Page) (Stop, error) {
	return NewReader(page).Active(ctx)
}

// Active returns the stop that has focus (see the package's Active).
func (r *Reader) Active(ctx context.Context) (Stop, error) {
	s, _, err := r.ActiveWith(ctx, "")
	return s, err
}

// ActiveWith returns the stop that has focus and the result of fn, a
// JavaScript function declaration called on the focused element (this)
// in the isolated world that returns a string, such as JSON, or a
// promise of one. fn ""
// calls nothing. A probe that judges the focused element (is it hidden,
// covered, ignored) uses it, so the element it judges is the one the
// stop describes. It isn't called for the document's own stop.
func (r *Reader) ActiveWith(ctx context.Context, fn string) (Stop, string, error) {
	p := r.page.Context(ctx)
	var res *proto.RuntimeEvaluateResult
	err := r.inWorld(ctx, func(world proto.RuntimeExecutionContextID) error {
		var err error
		res, err = proto.RuntimeEvaluate{Expression: deepActive, ContextID: world}.Call(p)
		return err
	})
	if err != nil {
		return Stop{}, "", err
	}
	if res.ExceptionDetails != nil {
		return Stop{}, "", errors.New(res.ExceptionDetails.Text)
	}
	if res.Result == nil || res.Result.ObjectID == "" {
		return Stop{Selector: "body", Tag: "body", IsBody: true, Connected: true}, "", nil
	}
	obj := res.Result.ObjectID
	defer func() { _ = proto.RuntimeReleaseObject{ObjectID: obj}.Call(p) }()
	call, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: describe, ObjectID: obj, ReturnByValue: true}.Call(p)
	if err != nil {
		return Stop{}, "", err
	}
	if call.ExceptionDetails != nil {
		return Stop{}, "", errors.New(call.ExceptionDetails.Text)
	}
	var raw struct {
		Path [][]a11y.Elem `json:"path"`
		Src  string        `json:"src"`
		Stop
	}
	if err := json.Unmarshal([]byte(call.Result.Value.Str()), &raw); err != nil {
		return Stop{}, "", fmt.Errorf("focused element: %w", err)
	}
	s := raw.Stop
	s.Selector = clip(a11y.Selector(raw.Path), maxSelector)
	if s.IsBody {
		s.Selector = "body"
	}
	node, err := proto.DOMDescribeNode{ObjectID: obj}.Call(p)
	if err != nil {
		return Stop{}, "", err
	}
	s.BackendNodeID = int(node.Node.BackendNodeID)
	switch {
	case s.IsFrame:
		s.FrameHost = frameHost(p, node.Node, raw.Src)
	case !s.IsBody:
		s.Role, s.Name, s.NameLen = axName(p, node.Node.BackendNodeID)
	}
	extra := ""
	if fn != "" {
		out, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: fn, ObjectID: obj, ReturnByValue: true, AwaitPromise: true}.Call(p)
		if err != nil {
			return s, "", err
		}
		if out.ExceptionDetails != nil {
			return s, "", errors.New(out.ExceptionDetails.Text)
		}
		extra = out.Result.Value.Str()
	}
	return s, extra, nil
}

// Eval runs expr, a JavaScript expression that gives a string such as
// JSON, in the reader's isolated world and returns the string. Promises
// are awaited.
func (r *Reader) Eval(ctx context.Context, expr string) (string, error) {
	p := r.page.Context(ctx)
	var res *proto.RuntimeEvaluateResult
	err := r.inWorld(ctx, func(world proto.RuntimeExecutionContextID) error {
		var err error
		res, err = proto.RuntimeEvaluate{Expression: expr, ContextID: world, ReturnByValue: true, AwaitPromise: true}.Call(p)
		return err
	})
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", errors.New(res.ExceptionDetails.Text)
	}
	return res.Result.Value.Str(), nil
}

// CallOn calls fn, a JavaScript function declaration that returns a
// string or a promise of one, in the reader's isolated world on DOM
// nodes named by their backend node ids (Stop.BackendNodeID): this is
// the first node and every node is an argument. A probe that needs the
// stops of a walk again, such as the elements of a suspected trap, uses
// it instead of looking them up by selector.
func (r *Reader) CallOn(ctx context.Context, fn string, nodes ...int) (string, error) {
	if len(nodes) == 0 {
		return "", errors.New("CallOn needs a node")
	}
	p := r.page.Context(ctx)
	var out string
	err := r.inWorld(ctx, func(world proto.RuntimeExecutionContextID) error {
		args := make([]*proto.RuntimeCallArgument, 0, len(nodes))
		var objs []proto.RuntimeRemoteObjectID
		defer func() {
			for _, o := range objs {
				_ = proto.RuntimeReleaseObject{ObjectID: o}.Call(p)
			}
		}()
		for _, id := range nodes {
			res, err := proto.DOMResolveNode{BackendNodeID: proto.DOMBackendNodeID(id), ExecutionContextID: world}.Call(p)
			if err != nil {
				return err
			}
			objs = append(objs, res.Object.ObjectID)
			args = append(args, &proto.RuntimeCallArgument{ObjectID: res.Object.ObjectID})
		}
		call, err := proto.RuntimeCallFunctionOn{FunctionDeclaration: fn, ObjectID: objs[0], Arguments: args,
			ReturnByValue: true, AwaitPromise: true}.Call(p)
		if err != nil {
			return err
		}
		if call.ExceptionDetails != nil {
			return errors.New(call.ExceptionDetails.Text)
		}
		out = call.Result.Value.Str()
		return nil
	})
	return out, err
}

// MaxNodes caps the elements Nodes returns.
const MaxNodes = 2000

// Nodes runs expr, a JavaScript expression that gives an array of
// elements (or a promise of one), in the reader's isolated world and
// returns their DOM nodes in the array's order, at most MaxNodes. A
// probe finds the elements a walk acts on this way: the page's own
// scripts never see the query; the nodes go to CallOn and the
// protocol's DOM calls as they are.
func (r *Reader) Nodes(ctx context.Context, expr string) ([]int, error) {
	p := r.page.Context(ctx)
	var out []int
	err := r.inWorld(ctx, func(world proto.RuntimeExecutionContextID) error {
		out = out[:0]
		res, err := proto.RuntimeEvaluate{Expression: expr, ContextID: world, AwaitPromise: true}.Call(p)
		if err != nil {
			return err
		}
		if res.ExceptionDetails != nil {
			return errors.New(res.ExceptionDetails.Text)
		}
		if res.Result == nil || res.Result.ObjectID == "" {
			return nil
		}
		arr := res.Result.ObjectID
		defer func() { _ = proto.RuntimeReleaseObject{ObjectID: arr}.Call(p) }()
		props, err := proto.RuntimeGetProperties{ObjectID: arr, OwnProperties: true}.Call(p)
		if err != nil {
			return err
		}
		// Properties come back in no promised order: they are put in
		// the array's order by index.
		byIndex := map[int]int{}
		for _, prop := range props.Result {
			i, err := strconv.Atoi(prop.Name)
			if err != nil || i < 0 || i >= MaxNodes || prop.Value == nil || prop.Value.ObjectID == "" {
				continue
			}
			node, err := proto.DOMDescribeNode{ObjectID: prop.Value.ObjectID}.Call(p)
			_ = proto.RuntimeReleaseObject{ObjectID: prop.Value.ObjectID}.Call(p)
			if err != nil || node.Node.NodeType != 1 {
				continue
			}
			byIndex[i] = int(node.Node.BackendNodeID)
		}
		for i := range MaxNodes {
			if id, ok := byIndex[i]; ok {
				out = append(out, id)
			}
		}
		return nil
	})
	return out, err
}

// Name returns the role and redacted accessible name of a node from
// Chrome's accessibility tree, as a Stop has them: never the element's
// value or text. nameLen counts the runes of the whole name.
func (r *Reader) Name(ctx context.Context, node int) (role, name string, nameLen int) {
	return axName(r.page.Context(ctx), proto.DOMBackendNodeID(node))
}

// inWorld calls f with the reader's isolated world, making one first if
// needed. A world dies with its document, so when f fails because its
// world is gone, a new one is made and f is tried once more.
func (r *Reader) inWorld(ctx context.Context, f func(proto.RuntimeExecutionContextID) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for try := 0; ; try++ {
		if r.world == 0 {
			p := r.page.Context(ctx)
			world, err := proto.PageCreateIsolatedWorld{FrameID: p.FrameID, WorldName: "soundcheck-focus"}.Call(p)
			if err != nil {
				return fmt.Errorf("can't open an isolated world: %w", err)
			}
			r.world = world.ExecutionContextID
		}
		err := f(r.world)
		if err == nil || try > 0 || !worldGone(err) {
			return err
		}
		r.world = 0
	}
}

// worldGone reports whether err is Chrome's answer for an execution
// context that no longer exists, as after a navigation.
func worldGone(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Cannot find context") || strings.Contains(msg, "context with specified id")
}

// axName reads the element's role and accessible name from Chrome's
// accessibility tree, never from the element's value or text: for a
// field the name is its label, not what a visitor typed. The name is
// redacted and cut to maxName runes; nameLen counts the runes of the
// whole name.
func axName(p *rod.Page, id proto.DOMBackendNodeID) (role, name string, nameLen int) {
	tree, err := proto.AccessibilityGetPartialAXTree{BackendNodeID: id, FetchRelatives: false}.Call(p)
	if err != nil {
		return "", "", 0
	}
	for _, n := range tree.Nodes {
		if n.BackendDOMNodeID != id {
			continue
		}
		if n.Role != nil {
			role = clip(n.Role.Value.Str(), 60)
		}
		if n.Name != nil {
			full := strings.Join(strings.Fields(n.Name.Value.Str()), " ")
			nameLen = utf8.RuneCountInString(full)
			name = clip(pii.Redact(full), maxName)
		}
		return role, name, nameLen
	}
	return "", "", 0
}

// frameHost is the host of the frame a frame element shows: from the
// browser's frame tree, which follows the frame wherever it navigated,
// else from its src.
func frameHost(p *rod.Page, n *proto.DOMNode, src string) string {
	if n.FrameID != "" {
		if tree, err := (proto.PageGetFrameTree{}).Call(p); err == nil {
			if raw := frameURL(tree.FrameTree, n.FrameID); raw != "" {
				if u, err := url.Parse(raw); err == nil && u.Host != "" {
					return u.Host
				}
			}
		}
	}
	if u, err := url.Parse(src); err == nil {
		return u.Host
	}
	return ""
}

func frameURL(t *proto.PageFrameTree, id proto.PageFrameID) string {
	if t == nil || t.Frame == nil {
		return ""
	}
	if t.Frame.ID == id {
		return t.Frame.URL
	}
	for _, c := range t.ChildFrames {
		if u := frameURL(c, id); u != "" {
			return u
		}
	}
	return ""
}

// clip cuts s to n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
