package tuist

import (
	"strconv"
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

// heightLeaf renders the terminal height it read; renders counts how many times
// it actually re-rendered (vs. served from cache).
type heightLeaf struct {
	Compo
	renders int
}

func (c *heightLeaf) Render(ctx Context) {
	c.renders++
	ctx.Lines("h=" + strconv.Itoa(ctx.ScreenHeight()))
}

// plainWrapper renders a child but never reads the height itself, so on a
// height-only resize its own (generation, width) cache key is unchanged. It
// models a cached ancestor that must still re-invoke a height-dependent child.
type plainWrapper struct {
	Compo
	child Component
}

func (w *plainWrapper) Render(ctx Context) { w.RenderChild(ctx, w.child) }

// TestHeadlessResizeRerendersHeightDependentChild guards the framework's
// height-aware caching: a component that reads ScreenHeight must re-render on a
// height-only resize (width unchanged) even when nested under a parent that
// does not read it. Without upward dependency propagation the cached wrapper
// short-circuits and the child keeps its first-paint layout.
func TestHeadlessResizeRerendersHeightDependentChild(t *testing.T) {
	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	leaf := &heightLeaf{}
	tui.AddChild(&plainWrapper{child: leaf})

	require.Contains(t, strings.Join(tui.Step(), "\n"), "h=10")
	rendersAfterFirst := leaf.renders

	// Height-only resize: width stays 40, so only the height key can force the
	// re-render.
	term.Resize(40, 5)
	frame := strings.Join(tui.Step(), "\n")
	assert.Contains(t, frame, "h=5", "height-dependent child should reflow on a height-only resize")
	assert.Greater(t, leaf.renders, rendersAfterFirst, "child should have re-rendered, not served stale cache")

	// A no-op resize (same dimensions) must NOT force a re-render -- the cache
	// should still hold when the height is unchanged.
	rendersBeforeNoop := leaf.renders
	term.Resize(40, 5)
	_ = tui.Step()
	assert.Equal(t, rendersBeforeNoop, leaf.renders, "unchanged height should stay cached")
}

// updatableLeaf is a sibling whose Update() forces the shared parent to
// re-render on a frame where the height-dependent leaf is served from cache.
type updatableLeaf struct {
	Compo
	n int
}

func (u *updatableLeaf) Render(ctx Context) { ctx.Lines("n=" + strconv.Itoa(u.n)) }

// twoChildWrapper renders two children and never reads the height itself.
type twoChildWrapper struct {
	Compo
	a, b Component
}

func (w *twoChildWrapper) Render(ctx Context) {
	w.RenderChild(ctx, w.a)
	w.RenderChild(ctx, w.b)
}

// TestHeadlessResizeReflowsAfterSiblingUpdate guards the subtler half of
// height-aware caching: the parent's dependency on a height-reading child must
// survive a frame where that child is served from cache. A sibling Update()
// re-renders the parent while the height leaf cache-hits -- and a cache hit
// never calls ScreenHeight(), so the parent would record usesScreenHeight=false
// and then cache-hit across the next resize, freezing the leaf at a stale
// height. (Reproduced from a real trace: a ticking keymap bar kept re-rendering
// the root with the body cached, so resizes stopped reflowing.)
func TestHeadlessResizeReflowsAfterSiblingUpdate(t *testing.T) {
	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	leaf := &heightLeaf{}
	sibling := &updatableLeaf{}
	tui.AddChild(&twoChildWrapper{a: leaf, b: sibling})

	require.Contains(t, strings.Join(tui.Step(), "\n"), "h=10")

	// Sibling update: the wrapper re-renders, the height leaf is served from
	// cache. This is the frame that used to reset the wrapper's height
	// dependency.
	sibling.n++
	sibling.Update()
	frame := strings.Join(tui.Step(), "\n")
	require.Contains(t, frame, "n=1")
	require.Contains(t, frame, "h=10")

	// Height-only resize: the wrapper must still know it contains a
	// height-dependent child and re-invoke it.
	rendersBefore := leaf.renders
	term.Resize(40, 5)
	frame = strings.Join(tui.Step(), "\n")
	assert.Contains(t, frame, "h=5", "height child must reflow after a sibling update re-rendered the parent")
	assert.Greater(t, leaf.renders, rendersBefore, "height child should re-render, not stay cached")
}
