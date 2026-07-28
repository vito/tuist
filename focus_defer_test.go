package tuist

import (
	"fmt"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// focusProbe records its lifecycle and input in order. Each entry notes
// whether the delivered Context carries a real (cancellable) mount context —
// context.Background()'s Done() is nil, a mount context's is not — so the
// log distinguishes a properly-mounted delivery from the old fallback.
type focusProbe struct {
	Compo
	log       *[]string
	gateInput bool // drop keys unless focused, like focus-gated components do
	focused   bool
	rendered  bool
}

func (f *focusProbe) OnMount(ctx Context) {
	*f.log = append(*f.log, fmt.Sprintf("mount(ctx=%v)", ctx.Done() != nil))
}

func (f *focusProbe) SetFocused(ctx Context, focused bool) {
	f.focused = focused
	*f.log = append(*f.log, fmt.Sprintf("focused(%v,ctx=%v)", focused, ctx.Done() != nil))
	f.Update()
}

func (f *focusProbe) HandleKeyPress(_ Context, ev uv.KeyPressEvent) bool {
	if f.gateInput && !f.focused {
		return false
	}
	*f.log = append(*f.log, "key:"+uv.Key(ev).String())
	return true
}

func (f *focusProbe) Render(ctx Context) {
	if !f.rendered {
		f.rendered = true
		*f.log = append(*f.log, "render")
	}
	ctx.Line("probe")
}

func newFocusTUI() *TUI {
	return New(NewHeadlessTerminal(40, 10))
}

func requireLog(t *testing.T, log, want []string) {
	t.Helper()
	if len(log) != len(want) {
		t.Fatalf("log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("log = %v, want %v", log, want)
		}
	}
}

// Focusing an unmounted component defers SetFocused to its mount: it fires
// after OnMount, before the first Render, with the mount context.
func TestFocusDeferredToMount(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	tui.AddChild(p)
	tui.SetFocus(p)

	if len(log) != 0 {
		t.Fatalf("notification delivered before mount: %v", log)
	}

	tui.Step()
	requireLog(t, log, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render"})
}

// Focusing an already-mounted component notifies immediately, as today.
func TestFocusImmediateWhenMounted(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	tui.AddChild(p)
	tui.Step()

	tui.SetFocus(p)
	requireLog(t, log, []string{"mount(ctx=true)", "render", "focused(true,ctx=true)"})
}

// If focus moves on before the first target ever mounts, the stale target
// gets neither the focus nor a blur notification — it was never observably
// focused.
func TestStaleDeferredFocusSkipped(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.SetFocus(a)
	tui.SetFocus(b)
	tui.Step()

	requireLog(t, logA, []string{"mount(ctx=true)", "render"})
	requireLog(t, logB, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render"})
}

// Blurring only happens after a real focus notification, and carries a
// valid context.
func TestBlurAfterNotifyHasMountContext(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.SetFocus(a)
	tui.Step() // a mounts + notified

	tui.SetFocus(b)
	requireLog(t, logA, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render", "focused(false,ctx=true)"})
}

// Type-ahead at a focused-but-unmounted component waits for the mount and
// then lands — after the focus notification, so even focus-gated components
// accept it. This is the tuist-level fix for the class of bug where a form
// installed and focused in one event batch swallowed the keystroke that
// arrived in the same batch.
func TestPreMountInputQueuedUntilMount(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log, gateInput: true}
	tui.AddChild(p)
	tui.SetFocus(p)
	tui.Inject(ParseKey("enter"))

	tui.Step()
	requireLog(t, log, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render", "key:enter"})
}

// Queued input preserves arrival order.
func TestQueuedInputPreservesOrder(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log, gateInput: true}
	tui.AddChild(p)
	tui.SetFocus(p)
	tui.Inject(ParseKey("a"), ParseKey("b"), ParseKey("c"))

	tui.Step()
	requireLog(t, log, []string{
		"mount(ctx=true)", "focused(true,ctx=true)", "render",
		"key:a", "key:b", "key:c",
	})
}

// A dismount kills the focus notification with the mount: if the component
// is still the focus target when it re-mounts, SetFocused is delivered
// afresh with the new mount context.
func TestDismountRenotifiesOnRemount(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	tui.AddChild(p)
	tui.SetFocus(p)
	tui.Step() // mount + notify

	tui.RemoveChild(p)
	tui.Step() // orphan cleanup dismounts; p is still the focus target
	tui.AddChild(p)
	p.rendered = false
	tui.Step() // remount → re-notify

	requireLog(t, log, []string{
		"mount(ctx=true)", "focused(true,ctx=true)", "render",
		"mount(ctx=true)", "focused(true,ctx=true)", "render",
	})
}

// A component whose OnMount claims focus for itself is notified exactly
// once, synchronously from its own SetFocus call.
type selfFocusProbe struct {
	focusProbe
}

func (s *selfFocusProbe) OnMount(ctx Context) {
	s.focusProbe.OnMount(ctx)
	ctx.SetFocus(s)
}

func TestOnMountSelfFocusNotifiesOnce(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &selfFocusProbe{focusProbe{log: &log}}
	tui.AddChild(p)
	tui.SetFocus(p) // deferred; OnMount will also claim focus

	tui.Step()
	requireLog(t, log, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render"})
}

// Overlay components are composited, never mounted, so their focus
// notification and input delivery stay immediate — the behavior
// ShowOverlay's docs promise. (The context is the Background fallback,
// as before: there is no mount context to carry.)
func TestOverlayFocusStaysImmediate(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log, gateInput: true}
	tui.ShowOverlay(p, nil)
	tui.SetFocus(p)

	requireLog(t, log, []string{"focused(true,ctx=false)"})

	tui.Inject(ParseKey("x"))
	tui.Step()
	if count := len(log); count < 2 || log[1] != "key:x" {
		t.Fatalf("overlay input was deferred: %v", log)
	}
}
