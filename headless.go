package tuist

import (
	"bytes"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

// HeadlessTerminal is a [Terminal] for synchronous, headless driving — used
// by tests and other non-interactive consumers that advance the UI by hand
// via [TUI.Step] rather than running the event-loop goroutine ([TUI.Start]).
//
// It has a fixed (resizable) size, never reads real input (events are fed
// through [TUI.Inject] instead), and buffers output writes so a caller can
// inspect the raw escape stream if needed. For golden-style assertions prefer
// [TUI.Frame], which returns the laid-out visible lines directly.
type HeadlessTerminal struct {
	cols, rows int
	onResize   func()
	buf        bytes.Buffer
}

var _ Terminal = (*HeadlessTerminal)(nil)

// NewHeadlessTerminal returns a HeadlessTerminal of the given dimensions.
func NewHeadlessTerminal(cols, rows int) *HeadlessTerminal {
	return &HeadlessTerminal{cols: cols, rows: rows}
}

// Start records the resize callback. onInput is ignored: headless input is
// delivered synchronously via [TUI.Inject], not through the terminal.
func (h *HeadlessTerminal) Start(_ func([]byte), onResize func()) error {
	h.onResize = onResize
	return nil
}

func (h *HeadlessTerminal) Stop()                         {}
func (h *HeadlessTerminal) SetInputPassthrough(io.Writer) {}
func (h *HeadlessTerminal) Write(p []byte)                { h.buf.Write(p) }
func (h *HeadlessTerminal) WriteString(s string)          { h.buf.WriteString(s) }
func (h *HeadlessTerminal) Columns() int                  { return h.cols }
func (h *HeadlessTerminal) Rows() int                     { return h.rows }
func (h *HeadlessTerminal) HideCursor()                   {}
func (h *HeadlessTerminal) ShowCursor()                   {}

// Resize changes the terminal dimensions and notifies the TUI, mirroring a
// real terminal's SIGWINCH so the next [TUI.Step]/[TUI.Frame] reflows.
func (h *HeadlessTerminal) Resize(cols, rows int) {
	h.cols, h.rows = cols, rows
	if h.onResize != nil {
		h.onResize()
	}
}

// Output returns the bytes written to the terminal since the last Reset.
func (h *HeadlessTerminal) Output() string { return h.buf.String() }

// Reset clears the captured output buffer.
func (h *HeadlessTerminal) Reset() { h.buf.Reset() }

// keyNames maps the names ParseKey accepts to ultraviolet key codes. It mirrors
// the (unexported) table ultraviolet uses to *match* keys, so a key scripted
// here round-trips through uv.Key.String()/MatchString the same way real
// decoded input does.
var keyNames = map[string]rune{
	"enter":     uv.KeyEnter,
	"tab":       uv.KeyTab,
	"backspace": uv.KeyBackspace,
	"escape":    uv.KeyEscape,
	"esc":       uv.KeyEscape,
	"space":     uv.KeySpace,
	"up":        uv.KeyUp,
	"down":      uv.KeyDown,
	"left":      uv.KeyLeft,
	"right":     uv.KeyRight,
	"home":      uv.KeyHome,
	"end":       uv.KeyEnd,
	"pgup":      uv.KeyPgUp,
	"pgdown":    uv.KeyPgDown,
	"insert":    uv.KeyInsert,
	"delete":    uv.KeyDelete,
	"begin":     uv.KeyBegin,
	"find":      uv.KeyFind,
	"select":    uv.KeySelect,
}

var keyMods = map[string]uv.KeyMod{
	"ctrl":  uv.ModCtrl,
	"alt":   uv.ModAlt,
	"shift": uv.ModShift,
	"meta":  uv.ModMeta,
	"super": uv.ModSuper,
	"hyper": uv.ModHyper,
}

// ParseKey builds a key press event from a key spec — a named key ("down",
// "enter", "esc", "pgup", "f1"), an optional "+"-joined modifier prefix
// ("ctrl+c", "alt+enter", "shift+tab"), or any printable rune ("+", "/", "a").
// It is the inverse of [uv.Key.String]/[uv.Key.Keystroke] and exists because
// ultraviolet keeps its own string→key parser unexported. Use it to script
// headless input via [TUI.Inject]. An unknown multi-rune name becomes an
// extended key carrying the name as text.
func ParseKey(spec string) uv.KeyPressEvent {
	var (
		mod  uv.KeyMod
		code rune
		text string
	)
	parts := strings.Split(spec, "+")
	for i, part := range parts {
		switch {
		case part == "":
			// An empty part comes from a "+" in the spec (e.g. "+" splits to
			// ["",""], "ctrl++" to ["ctrl","",""]) — i.e. the plus key itself.
			code = '+'
		case i < len(parts)-1 && keyMods[part] != 0:
			mod |= keyMods[part]
		default:
			if c, ok := keyNames[part]; ok {
				code = c
			} else if utf8.RuneCountInString(part) == 1 {
				code, _ = utf8.DecodeRuneInString(part)
			} else {
				code = uv.KeyExtended
				text = part
			}
		}
	}
	// A printable key with no non-shift modifier carries Text, so its
	// Key.String() yields the character — as real decoded input does.
	if text == "" && code != uv.KeyExtended && unicode.IsPrint(code) &&
		mod&^uv.ModShift == 0 {
		text = string(code)
	}
	return uv.KeyPressEvent{Mod: mod, Code: code, Text: text}
}

// Inject queues input events to be processed on the next [TUI.Step]. The
// events flow through the same path as real terminal input — input
// listeners, focus routing, and key bubbling all apply — but synchronously,
// with no event-loop goroutine. Safe only on the driving goroutine (no
// concurrent Start()).
func (t *TUI) Inject(evs ...uv.Event) {
	t.injected = append(t.injected, evs...)
}

// Step advances the TUI one frame without the event-loop goroutine: it
// drains the dispatch queue, processes any [TUI.Inject]ed events (which may
// dispatch further work), drains again, then renders a single frame and
// returns its visible lines. It is the headless equivalent of one runLoop
// iteration, intended for deterministic testing.
//
// Must not be called while [TUI.Start] is active.
func (t *TUI) Step() []string {
	t.drainDispatchQ()
	if len(t.injected) > 0 {
		evs := t.injected
		t.injected = nil
		for _, ev := range evs {
			t.dispatchEvent(ev)
		}
		t.drainDispatchQ()
	}
	return t.Frame()
}

// Frame renders a single frame at the terminal's current size and returns
// the visible lines (mouse-zone markers stripped), without draining the
// dispatch queue or processing input. Use it to read the current output
// between [TUI.Step] calls.
//
// Like Step, it must not be called while [TUI.Start] is active.
func (t *TUI) Frame() []string {
	width := t.terminal.Columns()
	height := t.terminal.Rows()
	t.screenHeight = height
	var stats RenderStats
	stats.OverlayCount = len(t.overlayStack)
	newLines, _, _ := t.renderFrame(width, height, &stats)
	return t.scanMouseZones(newLines)
}
