package tuist

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counterComponent is an Interactive+Mounter test component: it renders a
// count, bumps it on the "+" key, and records that it was mounted. It models
// the three things the headless driver must exercise faithfully — render,
// focus-routed key handling, and OnMount-driven lazy work.
type counterComponent struct {
	Compo
	count   int
	mounted bool
}

func (c *counterComponent) OnMount(Context) { c.mounted = true }

func (c *counterComponent) Render(ctx Context) {
	ctx.Lines("count: " + strings.Repeat("x", c.count))
}

func (c *counterComponent) HandleKeyPress(_ Context, ev uv.KeyPressEvent) bool {
	if uv.Key(ev).String() == "+" {
		c.count++
		c.Update()
		return true
	}
	return false
}

var _ Interactive = (*counterComponent)(nil)
var _ Mounter = (*counterComponent)(nil)

func key(s string) uv.KeyPressEvent {
	return uv.KeyPressEvent{Code: rune(s[0]), Text: s}
}

func TestParseKeyRoundTrips(t *testing.T) {
	// A scripted key must stringify to the same spec the consuming handlers
	// switch on (uv.Key(ev).String()). This is the contract that matters; note
	// uv's own MatchString is unreliable for the literal "+", so String() — not
	// MatchString — is the oracle.
	cases := []string{
		"down", "up", "left", "right",
		"enter", "esc", "space", "tab", "pgup", "pgdown", "home", "end",
		"+", "-", "=", "/", "a", "?",
		"ctrl+c", "alt+enter", "shift+tab",
	}
	for _, spec := range cases {
		ev := ParseKey(spec)
		assert.Equalf(t, spec, uv.Key(ev).String(),
			"ParseKey(%q) -> %+v stringified wrong", spec, ev)
	}
}

func TestHeadlessStepRendersAndMounts(t *testing.T) {
	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	c := &counterComponent{}
	tui.AddChild(c)
	tui.SetFocus(c)

	// First Step renders and fires OnMount (the lazy-work hook).
	frame := strings.Join(tui.Step(), "\n")
	assert.Contains(t, frame, "count: ")
	assert.True(t, c.mounted, "OnMount should fire during the first Step render")
}

func TestHeadlessInjectRoutesKeyToFocus(t *testing.T) {
	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	c := &counterComponent{}
	tui.AddChild(c)
	tui.SetFocus(c)
	tui.Step()

	// Inject "+" twice through the real input path (dispatchEvent ->
	// bubbleKeyPress -> focused component).
	tui.Inject(key("+"), key("+"))
	frame := strings.Join(tui.Step(), "\n")
	assert.Equal(t, 2, c.count)
	assert.Contains(t, frame, "count: xx")
}

func TestHeadlessDispatchAppliesBeforeRender(t *testing.T) {
	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	c := &counterComponent{}
	tui.AddChild(c)
	tui.SetFocus(c)
	tui.Step()

	// A Dispatch from "background I/O" is applied by the next Step, then
	// reflected in that same frame — the re-render-on-arrival contract.
	tui.Dispatch(func() { c.count = 5; c.Update() })
	frame := strings.Join(tui.Step(), "\n")
	assert.Contains(t, frame, "count: xxxxx")
}

func TestHeadlessResizeReflows(t *testing.T) {
	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"hello"}})
	require.Contains(t, strings.Join(tui.Step(), "\n"), "hello")

	term.Resize(20, 5)
	// Frame reads the new dimensions without draining input/dispatch.
	_ = tui.Frame()
	assert.Equal(t, 5, tui.screenHeight)
}
