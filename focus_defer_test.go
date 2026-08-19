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

func TestFocusIdentity(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}

	if tui.Focused() != nil {
		t.Fatalf("Focused() = %T, want nil", tui.Focused())
	}
	if tui.IsFocused(p) {
		t.Fatal("unfocused component reported focused")
	}

	tui.AddChild(p)
	tui.SetFocus(p)
	if tui.Focused() != p || !tui.IsFocused(p) {
		t.Fatalf("focus identity does not reflect deferred target: focused=%T", tui.Focused())
	}
	if !tui.contextFor(p).IsFocused() {
		t.Fatal("source context does not report focus")
	}

	tui.SetFocus(nil)
	if tui.Focused() != nil || tui.IsFocused(p) {
		t.Fatalf("focus identity not cleared: focused=%T", tui.Focused())
	}
}

func TestPushFocusRestore(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.Step()
	tui.SetFocus(a)

	h := tui.PushFocus(b)
	if tui.Focused() != b {
		t.Fatalf("PushFocus focused %T, want b", tui.Focused())
	}
	h.Restore()
	if tui.Focused() != a {
		t.Fatalf("Restore focused %T, want a", tui.Focused())
	}
}

func TestPushFocusNested(t *testing.T) {
	var logA, logB, logC []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	c := &focusProbe{log: &logC}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.AddChild(c)
	tui.Step()
	tui.SetFocus(a)

	hb := tui.PushFocus(b)
	hc := tui.PushFocus(c)
	hc.Restore()
	if tui.Focused() != b {
		t.Fatalf("inner Restore focused %T, want b", tui.Focused())
	}
	hb.Restore()
	if tui.Focused() != a {
		t.Fatalf("outer Restore focused %T, want a", tui.Focused())
	}
}

func TestPushFocusRestoreIgnoresFocusChangesWithinScope(t *testing.T) {
	var logA, logB, logC []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	c := &focusProbe{log: &logC}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.AddChild(c)
	tui.Step()
	tui.SetFocus(a)

	h := tui.PushFocus(b)
	tui.SetFocus(c)
	h.Restore()
	if tui.Focused() != a {
		t.Fatalf("Restore focused %T, want captured target a", tui.Focused())
	}
}

func TestPushFocusRejectsComponentFromAnotherTUI(t *testing.T) {
	var logA, logB []string
	tuiA := newFocusTUI()
	a := &focusProbe{log: &logA}
	tuiA.AddChild(a)
	tuiA.Step()
	tuiA.SetFocus(a)

	tuiB := newFocusTUI()
	b := &focusProbe{log: &logB}
	tuiB.AddChild(b)
	tuiB.Step()

	h := tuiA.PushFocus(b)
	if tuiA.Focused() != a {
		t.Fatalf("cross-TUI PushFocus changed focus to %T", tuiA.Focused())
	}
	h.Restore()
	if tuiA.Focused() != a {
		t.Fatalf("cross-TUI no-op handle changed focus to %T", tuiA.Focused())
	}
}

func TestPushFocusOutOfOrderRestore(t *testing.T) {
	var logA, logB, logC []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	c := &focusProbe{log: &logC}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.AddChild(c)
	tui.Step()
	tui.SetFocus(a)

	hb := tui.PushFocus(b)
	hc := tui.PushFocus(c)
	hb.Restore()
	if tui.Focused() != c {
		t.Fatalf("out-of-order Restore focused %T, want c", tui.Focused())
	}
	hc.Restore()
	if tui.Focused() != a {
		t.Fatalf("closing inner scope focused %T, want a", tui.Focused())
	}

	// Both handles have already been restored; duplicate calls are no-ops.
	hc.Restore()
	hb.Restore()
	if tui.Focused() != a {
		t.Fatalf("duplicate Restore focused %T, want a", tui.Focused())
	}
}

func TestPushFocusPreviousTargetRemoved(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.Step()
	tui.SetFocus(a)

	h := tui.PushFocus(b)
	tui.RemoveChild(a)
	h.Restore()
	if tui.Focused() != nil {
		t.Fatalf("restored removed component: %T", tui.Focused())
	}
}

func TestPushFocusCurrentTargetRemoved(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.Step()
	tui.SetFocus(a)

	h := tui.PushFocus(b)
	tui.RemoveChild(b)
	if tui.Focused() != nil {
		t.Fatalf("removed scoped component retained focus: %T", tui.Focused())
	}
	h.Restore()
	if tui.Focused() != a {
		t.Fatalf("Restore after scoped removal focused %T, want a", tui.Focused())
	}
}

func TestPushFocusWithOverlays(t *testing.T) {
	t.Run("temporary", func(t *testing.T) {
		var logA, logOverlay []string
		tui := newFocusTUI()
		a := &focusProbe{log: &logA}
		overlay := &focusProbe{log: &logOverlay}
		tui.AddChild(a)
		tui.Step()
		tui.SetFocus(a)
		overlayHandle := tui.ShowOverlay(overlay, nil)

		h := tui.PushFocus(overlay)
		if tui.Focused() != overlay {
			t.Fatalf("PushFocus focused %T, want overlay", tui.Focused())
		}
		h.Restore()
		if tui.Focused() != a {
			t.Fatalf("Restore focused %T, want a", tui.Focused())
		}
		overlayHandle.Remove()
	})

	t.Run("previous", func(t *testing.T) {
		var logA, logOverlay []string
		tui := newFocusTUI()
		a := &focusProbe{log: &logA}
		overlay := &focusProbe{log: &logOverlay}
		tui.AddChild(a)
		tui.Step()
		overlayHandle := tui.ShowOverlay(overlay, nil)
		tui.SetFocus(overlay)

		h := tui.PushFocus(a)
		h.Restore()
		if tui.Focused() != overlay {
			t.Fatalf("Restore focused %T, want overlay", tui.Focused())
		}
		overlayHandle.Remove()
		if tui.Focused() != nil {
			t.Fatalf("removed overlay retained focus: %T", tui.Focused())
		}
	})
}

func TestPushFocusBeforeTargetMount(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB, gateInput: true}
	tui.AddChild(a)
	tui.Step()
	tui.SetFocus(a)

	tui.AddChild(b)
	h := tui.PushFocus(b)
	if len(logB) != 0 {
		t.Fatalf("focus delivered before mount: %v", logB)
	}
	tui.Inject(ParseKey("x"))
	tui.Step()
	requireLog(t, logB, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render", "key:x"})

	h.Restore()
	if tui.Focused() != a {
		t.Fatalf("Restore focused %T, want a", tui.Focused())
	}
}

func TestPushFocusRestoresDeferredPreviousTarget(t *testing.T) {
	var logA, logB []string
	tui := newFocusTUI()
	a := &focusProbe{log: &logA}
	b := &focusProbe{log: &logB}
	tui.AddChild(a)
	tui.AddChild(b)
	tui.SetFocus(a)

	h := tui.PushFocus(b)
	h.Restore()
	if tui.Focused() != a {
		t.Fatalf("Restore focused %T, want deferred previous target", tui.Focused())
	}
	tui.Step()
	requireLog(t, logA, []string{"mount(ctx=true)", "focused(true,ctx=true)", "render"})
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

// A render-driven dismount kills the focus notification with the mount but
// retains the focus target: if the component re-mounts, SetFocused is
// delivered afresh with the new mount context. Explicit Container removal is
// tested separately and clears focus immediately.
type conditionalFocusHost struct {
	Compo
	child Component
	show  bool
}

func (h *conditionalFocusHost) Render(ctx Context) {
	if h.show {
		h.RenderChild(ctx, h.child)
	}
}

func TestDismountRenotifiesOnRemount(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	host := &conditionalFocusHost{child: p, show: true}
	tui.AddChild(host)
	tui.SetFocus(p)
	tui.Step() // mount + notify

	host.show = false
	host.Update()
	tui.Step() // orphan cleanup dismounts; p is still the focus target
	if tui.Focused() != p {
		t.Fatalf("render-driven dismount cleared focus: %T", tui.Focused())
	}
	host.show = true
	host.Update()
	p.rendered = false
	tui.Step() // remount → re-notify

	requireLog(t, log, []string{
		"mount(ctx=true)", "focused(true,ctx=true)", "render",
		"mount(ctx=true)", "focused(true,ctx=true)", "render",
	})
}

func TestExplicitRemovalClearsDescendantFocus(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	container := &Container{}
	container.AddChild(p)
	tui.AddChild(container)
	tui.SetFocus(p)
	tui.Step()

	tui.RemoveChild(container)
	if tui.Focused() != nil {
		t.Fatalf("removed ancestor retained descendant focus: %T", tui.Focused())
	}
}

func TestNoInputAfterExplicitRemoval(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log, gateInput: true}
	tui.AddChild(p)
	tui.SetFocus(p)
	tui.Step()

	tui.RemoveChild(p)
	tui.Inject(ParseKey("x"))
	tui.Step()
	for _, entry := range log {
		if entry == "key:x" {
			t.Fatalf("removed component received input: %v", log)
		}
	}
}

func TestExplicitRemovalClearsDeferredDescendantFocus(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log, gateInput: true}
	container := &Container{}
	container.AddChild(p)
	tui.AddChild(container)
	tui.SetFocus(p)

	// Neither component has mounted, but the structural parent links are
	// enough to recognize that removing the container dismisses p too.
	tui.RemoveChild(container)
	if tui.Focused() != nil {
		t.Fatalf("removed unmounted ancestor retained descendant focus: %T", tui.Focused())
	}
	tui.Inject(ParseKey("x"))
	tui.Step()
	if len(log) != 0 {
		t.Fatalf("removed unmounted component received lifecycle/input: %v", log)
	}
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
