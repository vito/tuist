package tuist

import (
	"context"
	"io"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testContext returns a Context suitable for unit tests, with a valid
// background context.Context and the given dimensions.
func testContext(width int, height ...int) Context {
	ctx := Context{Context: context.Background(), Width: width}
	if len(height) > 0 {
		ctx.Height = height[0]
	}
	return ctx
}

// mockTerminal records writes and simulates a fixed-size terminal.
type mockTerminal struct {
	cols, rows int
	written    strings.Builder
	onInput    func([]byte)
	onResize   func()
}

func newMockTerminal(cols, rows int) *mockTerminal {
	return &mockTerminal{cols: cols, rows: rows}
}

func (m *mockTerminal) Start(onInput func([]byte), onResize func()) error {
	m.onInput = onInput
	m.onResize = onResize
	return nil
}
func (m *mockTerminal) Stop()                    {}
func (m *mockTerminal) SetInputPassthrough(io.Writer) {}
func (m *mockTerminal) Write(p []byte)               { m.written.Write(p) }
func (m *mockTerminal) WriteString(s string)         { m.written.WriteString(s) }
func (m *mockTerminal) Columns() int         { return m.cols }
func (m *mockTerminal) Rows() int            { return m.rows }
func (m *mockTerminal) HideCursor()          { m.written.WriteString("\x1b[?25l") }
func (m *mockTerminal) ShowCursor()          { m.written.WriteString("\x1b[?25h") }

func (m *mockTerminal) reset() { m.written.Reset() }

// staticComponent renders fixed lines. Always dirty (re-renders every frame).
type staticComponent struct {
	Compo
	lines  []string
	cursor *CursorPos
}

func (s *staticComponent) Render(ctx Context) RenderResult {
	return RenderResult{
		Lines:  s.lines,
		Cursor: s.cursor,
	}
}


func TestFirstRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"hello", "world"}})

	// Simulate start without goroutines.
	term.reset()

	tui.RenderOnce()

	out := term.written.String()
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "world")
	// Should use synchronized output.
	assert.Contains(t, out, "\x1b[?2026h")
	assert.Contains(t, out, "\x1b[?2026l")
}

func TestWidthChangeUsesDiffUpdate(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"hello"}})

	tui.RenderOnce()
	assert.Equal(t, 1, tui.FullRedraws())

	// Simulate resize — should use diff update, not full redraw.
	// This avoids clear+home which causes ghost output when a
	// scrollbar narrows the terminal by a few characters.
	term.cols = 60
	term.reset()
	tui.RenderOnce()
	assert.Equal(t, 1, tui.FullRedraws()) // no additional full redraw
}

func TestOffscreenChangeTriggersFullRedraw(t *testing.T) {
	term := newMockTerminal(40, 5) // only 5 rows visible
	tui := New(term)

	// Create enough content to scroll.
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	comp := &staticComponent{lines: lines}
	tui.AddChild(comp)

	tui.RenderOnce()
	assert.Equal(t, 1, tui.FullRedraws())

	// Change a line that is above the viewport (line 0 is off-screen when
	// we have 20 lines and 5 rows).
	comp.lines[0] = "CHANGED"
	comp.Update()
	term.reset()
	tui.RenderOnce()

	// Should trigger a full redraw because the change is above viewport.
	assert.Equal(t, 2, tui.FullRedraws())
	out := term.written.String()
	assert.Contains(t, out, "\x1b[3J") // scrollback cleared
}

func TestNoChangeNoOutput(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"stable"}})

	tui.RenderOnce()
	term.reset()

	// Render again with no changes.
	tui.RenderOnce()

	out := term.written.String()
	// Should only have cursor positioning (hide cursor), no content writes.
	assert.NotContains(t, out, "stable")
	assert.NotContains(t, out, "\x1b[2K") // no line clears
}

func TestOverlayDoesNotStealFocus(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	// Create a main component and give it focus.
	main := &staticComponent{lines: []string{"main"}}
	tui.AddChild(main)
	tui.SetFocus(main)

	// Show overlay — focus should remain on main.
	overlay := &staticComponent{lines: []string{"popup"}}
	tui.ShowOverlay(overlay, &OverlayOptions{
		Width:  SizeAbs(10),
		Anchor: AnchorCenter,
	})

	focused := tui.focusedComponent
	assert.Equal(t, main, focused)
}

func TestSlotComponent(t *testing.T) {
	a := &staticComponent{lines: []string{"child-a"}}
	b := &staticComponent{lines: []string{"child-b-1", "child-b-2"}}

	slot := NewSlot(a)

	// Initial render — child has no cache yet, so it renders.
	r := renderComponent(slot, testContext(40))
	assert.Equal(t, []string{"child-a"}, r.Lines)

	// Second render — child is clean (nobody called Update), cached.
	r = renderComponent(slot, testContext(40))
	assert.Equal(t, []string{"child-a"}, r.Lines)

	// Swap child — Slot.Set marks dirty.
	slot.Set(b)
	r = renderComponent(slot, testContext(40))
	assert.Equal(t, []string{"child-b-1", "child-b-2"}, r.Lines)
}

func TestVisibleWidth(t *testing.T) {
	assert.Equal(t, 5, VisibleWidth("hello"))
	assert.Equal(t, 5, VisibleWidth("\x1b[31mhello\x1b[0m"))
	assert.Equal(t, 0, VisibleWidth(""))
}

func TestSliceByColumn(t *testing.T) {
	// Plain text.
	assert.Equal(t, "ell", SliceByColumn("hello", 1, 3))
	assert.Equal(t, "hel", SliceByColumn("hello", 0, 3))

	// With ANSI codes.
	colored := "\x1b[31mhello\x1b[0m"
	slice := SliceByColumn(colored, 1, 3)
	stripped := strings.ReplaceAll(slice, "\x1b[31m", "")
	stripped = strings.ReplaceAll(stripped, "\x1b[0m", "")
	assert.Equal(t, "ell", stripped)
}

func TestCompositeLineAt(t *testing.T) {
	base := strings.Repeat(".", 20)
	result := CompositeLineAt(base, "HI", 5, 4, 20)
	w := VisibleWidth(result)
	assert.Equal(t, 20, w, "composited line should be exactly terminal width")
	stripped := stripANSI(result)
	assert.Equal(t, ".....HI  ...........", stripped)
}

func TestCompositeLineAtPreservesSpaces(t *testing.T) {
	base := "hello    world      end"
	result := CompositeLineAt(base, "XX", 9, 2, 30)
	stripped := stripANSI(result)
	assert.Equal(t, "hello    XXrld      end       ", stripped)
	assert.Equal(t, 30, VisibleWidth(result))
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			_, n := parseEscape(s[i:])
			if n > 0 {
				i += n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestExpandTabs(t *testing.T) {
	assert.Equal(t, "ok      github.com/foo", ExpandTabs("ok\tgithub.com/foo", 8))
	assert.Equal(t, "a       b", ExpandTabs("a\tb", 8))
	assert.Equal(t, "abcd    e", ExpandTabs("abcd\te", 8))
	assert.Equal(t, "abcdefgh        x", ExpandTabs("abcdefgh\tx", 8))
	assert.Equal(t, "hello world", ExpandTabs("hello world", 8))
}

func TestCompositeWithTabExpandedLine(t *testing.T) {
	base := ExpandTabs("ok\tgithub.com/foo/bar/baz  3.682s", 8)
	result := CompositeLineAt(base, "XY", 10, 4, 40)
	stripped := stripANSI(result)
	assert.Contains(t, stripped, "ok      gi")
	assert.Contains(t, stripped, "XY")
	assert.Equal(t, 40, VisibleWidth(result))
}

// compoComponent embeds Compo for automatic caching. Call Update() to
// mark dirty. Tracks render call count.
type compoComponent struct {
	Compo
	lines       []string
	renderCount int
}

func (c *compoComponent) Render(ctx Context) RenderResult {
	c.renderCount++
	return RenderResult{Lines: c.lines}
}

func TestCompoSkipsRenderWhenClean(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	// Two children with Compo: one finalized, one changing.
	finalized := &compoComponent{lines: []string{"old line 1", "old line 2"}}
	finalized.Update() // dirty for first render
	active := &staticComponent{lines: []string{"input> "}}
	tui.AddChild(finalized)
	tui.AddChild(active)

	// First render — finalized is dirty, renders.
	tui.RenderOnce()
	assert.Equal(t, 1, finalized.renderCount)

	// Second render — finalized is clean (nobody called Update).
	// Render should be SKIPPED entirely (renderCount stays 1).
	term.reset()
	tui.RenderOnce()
	assert.Equal(t, 1, finalized.renderCount, "clean Compo should skip Render")

	// The output should still contain finalized's content (from cache).
	prev := tui.previousLines
	assert.True(t, len(prev) >= 3)
	assert.Contains(t, stripANSI(prev[0]), "old line 1")
	assert.Contains(t, stripANSI(prev[1]), "old line 2")
	assert.Contains(t, stripANSI(prev[2]), "input> ")
}

func TestContainerDirtyPropagation(t *testing.T) {
	c := &Container{}
	c1 := &compoComponent{lines: []string{"a"}}
	c1.Update()
	c2 := &compoComponent{lines: []string{"b"}}
	c2.Update()
	c.AddChild(c1)
	c.AddChild(c2)

	// First render — children are dirty, both render.
	ctx := testContext(40)
	r := renderComponent(c, ctx)
	assert.Equal(t, []string{"a", "b"}, r.Lines)
	assert.Equal(t, 1, c1.renderCount)
	assert.Equal(t, 1, c2.renderCount)

	// Second render — children clean, Container cached at root level.
	r = renderComponent(c, ctx)
	assert.Equal(t, []string{"a", "b"}, r.Lines)
	assert.Equal(t, 1, c1.renderCount, "clean child should not re-render")
	assert.Equal(t, 1, c2.renderCount, "clean child should not re-render")

	// Mark one child dirty — Container re-renders.
	c1.Update()
	r = renderComponent(c, ctx)
	assert.Equal(t, []string{"a", "b"}, r.Lines)
	assert.Equal(t, 2, c1.renderCount, "dirty child should re-render")
	assert.Equal(t, 1, c2.renderCount, "clean child should still be cached")
}

func TestCompoCachedChildNoRepaint(t *testing.T) {
	// Verify that a cached Compo child's line range is not repainted
	// when only other children change.
	term := newMockTerminal(40, 10)
	tui := New(term)

	clean := &compoComponent{lines: []string{"stable-1", "stable-2"}}
	clean.Update()
	changing := &compoComponent{lines: []string{"v1"}}
	changing.Update()
	tui.AddChild(clean)
	tui.AddChild(changing)

	// First render (full).
	tui.RenderOnce()

	// Now only changing child is updated.
	changing.lines = []string{"v2"}
	changing.Update()
	term.reset()
	tui.RenderOnce()

	out := term.written.String()
	// The changing child should be repainted.
	assert.Contains(t, out, "v2")
	// The clean child should NOT be repainted.
	assert.NotContains(t, out, "stable-1")
	assert.NotContains(t, out, "stable-2")
}

func TestUpdatePropagatesAndRequestsRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	child := &compoComponent{lines: []string{"hello"}}
	child.Update()
	tui.AddChild(child)
	tui.RenderOnce()

	// Drain the render channel.
	select {
	case <-tui.renderCh:
	default:
	}

	// Update the child — should propagate and request render.
	child.lines = []string{"world"}
	child.Update()

	// The render channel should have a pending request.
	select {
	case <-tui.renderCh:
		// good
	default:
		t.Fatal("Update() should have triggered RequestRender via propagation")
	}
}

func TestForceRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{"content"}})

	tui.RenderOnce()
	assert.Equal(t, 1, tui.FullRedraws())

	// Force re-render should do a full redraw even with no changes.
	tui.previousLines = nil
	tui.previousWidth = -1
	tui.cursorRow = 0
	tui.hardwareCursorRow = 0
	tui.maxLinesRendered = 0
	tui.previousViewportTop = 0

	term.reset()
	tui.RenderOnce()
	assert.Equal(t, 2, tui.FullRedraws())
}

func TestConcurrentUpdateNotLost(t *testing.T) {
	// Regression test: if Update() is called on a component while its
	// Render() is in progress (e.g. from a streaming goroutine),
	// the dirty flag must not be lost. Previously, renderComponent
	// used a boolean flag and called Store(false) AFTER Render(),
	// which could overwrite a concurrent Update()'s Store(true).
	// The generation counter approach eliminates this: renderComponent
	// records the generation it checked, and any concurrent Update()
	// increments past it.
	term := newMockTerminal(40, 10)
	tui := New(term)

	// A component whose Render calls Update() on itself to simulate
	// a concurrent update during rendering. On first render it returns
	// the current (stale) value but marks itself dirty with a new value.
	sneaky := &updateDuringRenderComponent{value: "before"}
	sneaky.Update()
	tui.AddChild(sneaky)

	// First render: Render() returns "before", then sets value="after"
	// and calls Update(). The generation counter advances past what
	// renderComponent recorded, so the component stays dirty.
	tui.RenderOnce()

	// The component should still be dirty after the first render
	// because Update() was called during Render().
	cp := sneaky.compo()
	assert.NotEqual(t, cp.generation.Load(), cp.renderedGen,
		"Update() during Render() must not be lost")

	// Second render should pick up the new value.
	term.reset()
	tui.RenderOnce()
	out := term.written.String()
	assert.Contains(t, out, "after",
		"second render should reflect the update made during first render")
}

// updateDuringRenderComponent calls Update() on itself during Render
// to simulate a concurrent update from another goroutine. On first
// render it snapshots the current value, then mutates and calls Update().
type updateDuringRenderComponent struct {
	Compo
	value    string
	rendered bool
}

func (u *updateDuringRenderComponent) Render(ctx Context) RenderResult {
	snapshot := u.value
	if !u.rendered {
		u.rendered = true
		u.value = "after"
		u.Update() // simulate concurrent update
	}
	return RenderResult{Lines: []string{snapshot}}
}

func TestSubwordLeft(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		cursor int
		want   int
	}{
		{"ident", "container", 9, 0},
		{"dot chain", "container.withExec", 18, 10},
		{"after dot", "container.", 10, 9},
		{"after paren", "container.withExec(", 19, 18},
		{"after brackets", `["echo"]`, 8, 6},
		{"after closing bracket", `["echo"]`, 7, 6},
		{"after space", "foo bar", 7, 4},
		{"multiple spaces", "foo   bar", 9, 6},
		{"symbols run", "foo..", 5, 3},
		{"empty", "", 0, 0},
		{"at start", "hello", 0, 0},
		{"mixed", "a.b(c)", 6, 5},
		{"mixed mid", "a.b(c)", 5, 4},
		{"mixed paren", "a.b(c)", 4, 3},
		{"mixed dot", "a.b(c)", 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ti := NewTextInput("> ")
			ti.value = []rune(tt.input)
			ti.cursor = tt.cursor
			got := ti.subwordLeft()
			assert.Equal(t, tt.want, got, "subwordLeft(%q, cursor=%d)", tt.input, tt.cursor)
		})
	}
}

func TestCachedLinesNotMutatedBySegmentReset(t *testing.T) {
	// Regression test: doRender appends segmentReset to each line.
	// If it mutates the cached RenderResult's Lines slice, subsequent
	// frames see double-reset strings that never match, causing
	// spurious full redraws.
	term := newMockTerminal(40, 10)
	tui := New(term)

	cached := &compoComponent{lines: []string{"stable"}}
	cached.Update()
	changing := &compoComponent{lines: []string{"v1"}}
	changing.Update()
	tui.AddChild(cached)
	tui.AddChild(changing)

	// First render.
	tui.RenderOnce()
	assert.Equal(t, 1, tui.FullRedraws())

	// Change only the second component. The first is cached.
	changing.lines = []string{"v2"}
	changing.Update()
	term.reset()
	tui.RenderOnce()

	// Should NOT be a full redraw — cached component's line 0
	// should be identical across frames.
	assert.Equal(t, 1, tui.FullRedraws(), "cached line should not accumulate segmentReset")
	out := term.written.String()
	assert.Contains(t, out, "v2")
	assert.NotContains(t, out, "stable") // cached line not repainted

	// Third render — same pattern, still no full redraw.
	changing.lines = []string{"v3"}
	changing.Update()
	term.reset()
	tui.RenderOnce()
	assert.Equal(t, 1, tui.FullRedraws(), "still no full redraw on third frame")
}

func TestHasKittyKeyboard(t *testing.T) {
	term := newMockTerminal(80, 24)
	tui := New(term)

	// Before any response, HasKittyKeyboard is false.
	assert.False(t, tui.HasKittyKeyboard())

	// Simulate a KeyboardEnhancementsEvent with disambiguate flag.
	tui.dispatchEvent(uv.KeyboardEnhancementsEvent{Flags: 1}) // KittyDisambiguateEscapeCodes = 1
	assert.True(t, tui.HasKittyKeyboard())

	// Zero flags means no support.
	tui.dispatchEvent(uv.KeyboardEnhancementsEvent{Flags: 0})
	assert.False(t, tui.HasKittyKeyboard())
}

// ── Lifecycle tests ────────────────────────────────────────────────────────

// lifecycleComponent tracks mount/dismount calls.
type lifecycleComponent struct {
	Compo
	mounted       bool
	mountCount    int
	dismountCount int
	lines         []string
}

func (c *lifecycleComponent) OnMount(ctx Context) {
	c.mounted = true
	c.mountCount++
}

func (c *lifecycleComponent) OnDismount() {
	c.mounted = false
	c.dismountCount++
}

func (c *lifecycleComponent) Render(ctx Context) RenderResult {
	return RenderResult{Lines: c.lines}
}

func TestMountOnFirstRender(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	comp := &lifecycleComponent{lines: []string{"hello"}}
	comp.Update()
	assert.False(t, comp.mounted)
	assert.Equal(t, 0, comp.mountCount)

	tui.AddChild(comp)
	// Not mounted yet — mounting is lazy (happens on first render).
	assert.False(t, comp.mounted)

	tui.RenderOnce()
	assert.True(t, comp.mounted)
	assert.Equal(t, 1, comp.mountCount)
}

func TestDismountOnRemoveChild(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	comp := &lifecycleComponent{lines: []string{"hello"}}
	comp.Update()
	tui.AddChild(comp)
	tui.RenderOnce()
	assert.True(t, comp.mounted)

	tui.RemoveChild(comp)
	// Not dismounted yet — dismount is lazy (orphan cleanup on re-render).
	assert.True(t, comp.mounted)

	tui.RenderOnce()
	assert.False(t, comp.mounted)
	assert.Equal(t, 1, comp.dismountCount)
}

func TestMountPropagatesDownTree(t *testing.T) {
	// Build a subtree, then attach it to the TUI.
	// Children should be mounted on the first render.
	child := &lifecycleComponent{lines: []string{"child"}}
	child.Update()
	container := &Container{}
	container.AddChild(child)
	assert.False(t, child.mounted, "child should not be mounted before rendering")

	term := newMockTerminal(40, 10)
	tui := New(term)
	tui.AddChild(container)
	assert.False(t, child.mounted, "child should not be mounted before rendering")

	tui.RenderOnce()
	assert.True(t, child.mounted, "child should be mounted after first render")
	assert.Equal(t, 1, child.mountCount)
}

func TestDismountPropagatesDownTree(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	child := &lifecycleComponent{lines: []string{"child"}}
	child.Update()
	container := &Container{}
	tui.AddChild(container)
	container.AddChild(child)
	tui.RenderOnce()
	assert.True(t, child.mounted)

	tui.RemoveChild(container)
	tui.RenderOnce()
	assert.False(t, child.mounted, "child should be dismounted when parent is removed")
}

func TestSlotMountDismount(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	a := &lifecycleComponent{lines: []string{"a"}}
	a.Update()
	b := &lifecycleComponent{lines: []string{"b"}}
	b.Update()
	slot := NewSlot(a)
	tui.AddChild(slot)

	tui.RenderOnce()
	assert.True(t, a.mounted, "initial child should be mounted after render")
	assert.False(t, b.mounted)

	slot.Set(b)
	tui.RenderOnce()
	assert.False(t, a.mounted, "old child should be dismounted after render")
	assert.True(t, b.mounted, "new child should be mounted after render")
	assert.Equal(t, 1, a.dismountCount)
	assert.Equal(t, 1, b.mountCount)
}

func TestMountContextCancelledOnDismount(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	comp := &lifecycleComponent{lines: []string{"hello"}}
	comp.Update()
	tui.AddChild(comp)
	tui.RenderOnce()

	assert.NotNil(t, comp.compo().mountCtx)
	mountCtx := comp.compo().mountCtx

	select {
	case <-mountCtx.Done():
		t.Fatal("context should not be done while mounted")
	default:
	}

	tui.RemoveChild(comp)
	tui.RenderOnce()

	select {
	case <-mountCtx.Done():
		// good — context was cancelled
	default:
		t.Fatal("context should be done after dismount")
	}
}

func TestContainerClearDismountsAll(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	a := &lifecycleComponent{lines: []string{"a"}}
	a.Update()
	b := &lifecycleComponent{lines: []string{"b"}}
	b.Update()
	tui.AddChild(a)
	tui.AddChild(b)

	tui.RenderOnce()
	assert.True(t, a.mounted)
	assert.True(t, b.mounted)

	tui.Clear()
	tui.RenderOnce()
	assert.False(t, a.mounted)
	assert.False(t, b.mounted)
}

// ── Input bubbling tests ────────────────────────────────────────────────────

// interactiveComponent records key events and optionally consumes them.
type interactiveComponent struct {
	Compo
	lines   []string
	keys    []string // recorded key descriptions
	consume bool     // if true, HandleKeyPress returns true
}

func (c *interactiveComponent) Render(ctx Context) RenderResult {
	return RenderResult{Lines: c.lines}
}

func (c *interactiveComponent) HandleKeyPress(_ Context, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	desc := string(key.Code)
	if key.Text != "" {
		desc = key.Text
	}
	c.keys = append(c.keys, desc)
	return c.consume
}

// interactiveContainer is a Container that also implements Interactive,
// so it can participate in bubbling.
type interactiveContainer struct {
	Container
	keys    []string
	consume bool
}

func (c *interactiveContainer) HandleKeyPress(_ Context, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	desc := string(key.Code)
	if key.Text != "" {
		desc = key.Text
	}
	c.keys = append(c.keys, desc)
	return c.consume
}

func TestBubblingToParent(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	// Build tree: TUI → parent (interactive container) → child.
	parent := &interactiveContainer{consume: false}
	child := &interactiveComponent{lines: []string{"child"}, consume: false}
	child.Update()

	parent.AddChild(child)
	tui.AddChild(parent)
	tui.RenderOnce() // mount + wire parent pointers

	tui.SetFocus(child)

	ev := uv.KeyPressEvent{Code: 'x', Text: "x"}
	tui.dispatchEvent(ev)

	// Child returns false, event bubbles to parent.
	assert.Equal(t, []string{"x"}, child.keys)
	assert.Equal(t, []string{"x"}, parent.keys)
}

func TestBubblingConsumed(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	parent := &interactiveContainer{consume: false}
	child := &interactiveComponent{lines: []string{"child"}, consume: true} // consumes
	child.Update()

	parent.AddChild(child)
	tui.AddChild(parent)
	tui.RenderOnce() // mount + wire parent pointers
	tui.SetFocus(child)

	ev := uv.KeyPressEvent{Code: 'x', Text: "x"}
	tui.dispatchEvent(ev)

	// Child consumed it — parent should NOT see it.
	assert.Equal(t, []string{"x"}, child.keys)
	assert.Empty(t, parent.keys)
}

func TestBubblingNonInteractiveFocused(t *testing.T) {
	// When the focused component doesn't implement Interactive,
	// the event should still bubble to Interactive ancestors.
	term := newMockTerminal(40, 10)
	tui := New(term)

	parent := &interactiveContainer{consume: true}
	// staticComponent doesn't implement Interactive.
	child := &staticComponent{lines: []string{"child"}}
	child.Update()

	parent.AddChild(child)
	tui.AddChild(parent)
	tui.RenderOnce() // mount + wire parent pointers
	tui.SetFocus(child) // non-Interactive component gets focus

	ev := uv.KeyPressEvent{Code: 'z', Text: "z"}
	tui.dispatchEvent(ev)

	// Should bubble to parent.
	assert.Equal(t, []string{"z"}, parent.keys)
}

func TestSpinnerMountDismountLifecycle(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	sp := NewSpinner()
	slot := NewSlot(nil)
	tui.AddChild(slot)

	// Spinner not in tree yet.
	assert.Nil(t, sp.compo().mountCtx)

	// Add spinner to slot — mounted lazily on render.
	slot.Set(sp)
	tui.RenderOnce()
	assert.NotNil(t, sp.compo().mountCtx)

	// Remove spinner — dismounted on next render.
	ctx := sp.compo().mountCtx
	slot.Set(nil)
	tui.RenderOnce()
	assert.Nil(t, sp.compo().mountCtx)
	select {
	case <-ctx.Done():
		// good
	default:
		t.Fatal("spinner's mount context should be cancelled after removal")
	}
}

// ── Zone marker tests ──────────────────────────────────────────────────────

// mouseComponent is a MouseEnabled component for testing zone dispatch.
type mouseComponent struct {
	Compo
	lines        []string
	lastEvent    *MouseEvent
	lastCtx      *Context
	consumeMouse bool
}

func (m *mouseComponent) Render(ctx Context) RenderResult {
	return RenderResult{Lines: m.lines}
}

func (m *mouseComponent) HandleMouse(ctx Context, ev MouseEvent) bool {
	m.lastEvent = &ev
	m.lastCtx = &ctx
	return m.consumeMouse
}

func TestScanMouseZones_MarkersStrippedAndZonesDetected(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	mc := &mouseComponent{lines: []string{"hello"}, consumeMouse: true}
	tui.AddChild(mc)

	tui.RenderOnce()

	// Find the zone for mc.
	var found *mouseZone
	for i := range tui.mouseZones {
		if tui.mouseZones[i].comp == mc {
			found = &tui.mouseZones[i]
			break
		}
	}
	require.NotNil(t, found, "expected a zone for the mouseComponent")
	assert.Equal(t, 0, found.startRow)
	assert.Equal(t, 0, found.startCol)
	assert.Equal(t, 0, found.endRow)
	assert.Equal(t, 5, found.endCol)

	// Terminal output should not contain zone markers.
	out := term.written.String()
	assert.Contains(t, out, "hello")
}

func TestScanMouseZones_FullLineComponent(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	mc := &mouseComponent{lines: []string{"hello", "world"}, consumeMouse: true}
	tui.AddChild(mc)

	tui.RenderOnce()

	// The Container auto-marks MouseEnabled children, so zones should exist.
	require.NotEmpty(t, tui.mouseZones, "expected at least one mouse zone")

	// Find the zone for mc.
	var found *mouseZone
	for i := range tui.mouseZones {
		if tui.mouseZones[i].comp == mc {
			found = &tui.mouseZones[i]
			break
		}
	}
	require.NotNil(t, found, "expected a zone for the mouseComponent")
	assert.Equal(t, 0, found.startRow)
	assert.Equal(t, 0, found.startCol)
	assert.Equal(t, 1, found.endRow)
	// endCol should be the width of "world" (5)
	assert.Equal(t, 5, found.endCol)
}

func TestScanMouseZones_InlineMark(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	// An inline MouseEnabled component rendered via RenderChildInline.
	inline := &mouseComponent{lines: []string{"VALUE"}, consumeMouse: true}

	// A parent that uses RenderChildInline to embed the inline component.
	parent := &markingParent{inline: inline}
	tui.AddChild(parent)

	tui.RenderOnce()

	// Find the inline zone.
	var found *mouseZone
	for i := range tui.mouseZones {
		if tui.mouseZones[i].comp == inline {
			found = &tui.mouseZones[i]
			break
		}
	}
	require.NotNil(t, found, "expected a zone for the inline component")
	// "prefix" is 6 chars, then the marked "VALUE" is 5 chars.
	assert.Equal(t, 0, found.startRow)
	assert.Equal(t, 6, found.startCol)
	assert.Equal(t, 0, found.endRow)
	assert.Equal(t, 11, found.endCol)
}

// markingParent renders a line with an inline RenderChildInline'd component.
type markingParent struct {
	Compo
	inline Component
}

func (p *markingParent) Render(ctx Context) RenderResult {
	inlined := p.RenderChildInline(ctx, p.inline)
	line := "prefix" + inlined + "suffix"
	return RenderResult{Lines: []string{line}}
}

func TestScanMouseZones_StripsMarkers(t *testing.T) {
	term := newMockTerminal(40, 10)
	tui := New(term)

	mc := &mouseComponent{lines: []string{"hello"}, consumeMouse: true}
	tui.AddChild(mc)

	tui.RenderOnce()

	// The terminal output should not contain any zone markers.
	out := term.written.String()
	assert.NotContains(t, out, "z") // no ESC[...z marker in output
	assert.Contains(t, out, "hello")
}

func TestZoneContains(t *testing.T) {
	// Single-line zone at row 2, cols 5-10.
	z := &mouseZone{startRow: 2, startCol: 5, endRow: 2, endCol: 10}

	assert.True(t, zoneContains(z, 2, 5))   // start corner
	assert.True(t, zoneContains(z, 2, 9))   // last col
	assert.False(t, zoneContains(z, 2, 4))  // before start
	assert.False(t, zoneContains(z, 2, 10)) // at endCol (exclusive)
	assert.False(t, zoneContains(z, 1, 7))  // wrong row
	assert.False(t, zoneContains(z, 3, 7))  // wrong row

	// Multi-line zone: rows 1-3, cols 3-7 (rectangular).
	z2 := &mouseZone{startRow: 1, startCol: 3, endRow: 3, endCol: 8}
	assert.False(t, zoneContains(z2, 1, 2)) // before startCol
	assert.True(t, zoneContains(z2, 1, 3))  // start corner
	assert.False(t, zoneContains(z2, 2, 0)) // middle row, before startCol
	assert.True(t, zoneContains(z2, 2, 5))  // middle row, inside rect
	assert.False(t, zoneContains(z2, 3, 0)) // last row, before startCol
	assert.True(t, zoneContains(z2, 3, 7))  // last row, just before endCol
	assert.False(t, zoneContains(z2, 3, 8)) // at endCol (exclusive)
	assert.False(t, zoneContains(z2, 0, 5)) // above zone
	assert.False(t, zoneContains(z2, 4, 5)) // below zone
}
