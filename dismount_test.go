package tuist

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/stretchr/testify/require"
)

// mountProbe is a leaf that counts its lifecycle and keeps its mount context.
// It is MouseEnabled so the tests can see mouse registration follow mounts.
type mountProbe struct {
	Compo
	text      string
	mounts    int
	dismounts int
	mountCtx  context.Context
}

func (p *mountProbe) OnMount(ctx Context) {
	p.mounts++
	p.mountCtx = ctx.Context
}

func (p *mountProbe) OnDismount() { p.dismounts++ }

func (p *mountProbe) HandleMouse(Context, MouseEvent) bool { return false }

func (p *mountProbe) Render(ctx Context) { ctx.Line(p.text) }

// showHost renders its child only while show is set: a render-driven
// dismount, like an app pruning a subtree it no longer displays.
type showHost struct {
	Compo
	child Component
	show  bool
}

func (h *showHost) Render(ctx Context) {
	if h.show && h.child != nil {
		h.RenderChild(ctx, h.child)
	}
}

func (h *showHost) toggle(show bool) {
	h.show = show
	h.Update()
}

// A component the app keeps after dismount must not keep its former
// ancestors alive. Apps cache per-item views (log panes, test rows) long after
// the tree that rendered them is pruned; a strong back-link let each of them
// pin that whole tree.
func TestDismountedChildDoesNotPinFormerParent(t *testing.T) {
	for _, tc := range []struct {
		name string
		// wrap builds the former parent of leaf, rendering it.
		wrap func(leaf Component) Component
	}{
		{"RenderChild", func(leaf Component) Component {
			return &plainWrapper{child: leaf}
		}},
		{"Container", func(leaf Component) Component {
			c := &Container{}
			c.AddChild(leaf) // structural link, not just a render-time one
			return c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui := newFocusTUI()
			leaf := &mountProbe{text: "leaf"}
			host := &showHost{show: true}
			tui.AddChild(host)

			// Build the parent in a helper so no stack slot of this test
			// keeps it alive.
			parentRef := func() weak.Pointer[Compo] {
				parent := tc.wrap(leaf)
				host.child = parent
				return weak.Make(parent.compo())
			}()
			tui.Step()
			require.Equal(t, 1, leaf.mounts)

			// Prune the parent: dismounted, and the app drops it — but
			// keeps the leaf.
			host.child = nil
			host.toggle(false)
			tui.Step()
			require.Equal(t, 1, leaf.dismounts)

			runtime.GC()
			runtime.GC()
			require.Nil(t, parentRef.Value(), "retained child kept its former parent reachable")
			runtime.KeepAlive(leaf)
		})
	}
}

// The TUI holds a dismounted focus target's former ancestors only while it
// is a focus target. Once focus moves on, they are released like any other.
func TestDismountedFocusTargetReleasesAncestorsWhenBlurred(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	host := &showHost{show: true}
	other := &mountProbe{text: "other"}
	tui.AddChild(host)
	tui.AddChild(other)
	parentRef := func() weak.Pointer[Compo] {
		w := &plainWrapper{child: p}
		host.child = w
		return weak.Make(w.compo())
	}()
	tui.SetFocus(p)
	tui.Step()

	host.child = nil
	host.toggle(false)
	tui.Step()
	runtime.GC()
	runtime.GC()
	require.NotNil(t, parentRef.Value(), "a dismounted focus target's ancestors must stay alive for focus checks")

	tui.SetFocus(other)
	runtime.GC()
	runtime.GC()
	require.Nil(t, parentRef.Value(), "former focus target still pins its former parent")
}

// countingLeaf counts its renders.
type countingLeaf struct {
	Compo
	renders int
}

func (c *countingLeaf) Render(ctx Context) {
	c.renders++
	ctx.Line("leaf")
}

// A leaf has no descendants to remount, so a leaf that is hidden and shown
// again unchanged is served from its cache rather than re-rendered.
func TestRemountedLeafKeepsCache(t *testing.T) {
	tui := newFocusTUI()
	leaf := &countingLeaf{}
	host := &showHost{child: leaf, show: true}
	tui.AddChild(host)
	tui.Step()

	host.toggle(false)
	tui.Step()
	host.toggle(true)
	require.Contains(t, strings.Join(tui.Step(), "\n"), "leaf")
	require.Equal(t, 1, leaf.renders)
}

// RemoveChild must not leave the removed child in the slice's backing array,
// where the container would keep it alive.
func TestRemoveChildReleasesChild(t *testing.T) {
	c := &Container{}
	ref := func() weak.Pointer[Compo] {
		child := &mountProbe{}
		c.AddChild(child)
		c.RemoveChild(child)
		return weak.Make(child.compo())
	}()
	runtime.GC()
	runtime.GC()
	require.Nil(t, ref.Value(), "removed child still reachable from its container")
	runtime.KeepAlive(c)
}

// selfRemover removes itself from its container when it mounts.
type selfRemover struct {
	Compo
	c *Container
}

func (s *selfRemover) OnMount(Context)    { s.c.RemoveChild(s) }
func (s *selfRemover) Render(ctx Context) { ctx.Line("gone") }

// A child removing itself from the Container rendering it must not disturb
// the Render loop ranging over Children.
func TestRemoveChildDuringContainerRender(t *testing.T) {
	tui := newFocusTUI()
	c := &Container{}
	c.AddChild(&selfRemover{c: c})
	c.AddChild(&mountProbe{text: "row"})
	tui.AddChild(c)

	tui.Step()
	frame := strings.Join(tui.Step(), "\n")
	require.Contains(t, frame, "row")
	require.NotContains(t, frame, "gone")
	require.Len(t, c.Children, 1)
}

// A subtree omitted from one frame and re-included unchanged must remount all
// of it, not just its top. Its top's render cache would otherwise still be
// valid (same generation, same width), so Render — and with it the
// RenderChild calls that mount the descendants — would never run.
func TestRemountedSubtreeRemountsDescendants(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "leaf"}
	mid := &plainWrapper{child: leaf}
	host := &showHost{child: mid, show: true}
	tui.AddChild(host)

	tui.Step()
	require.Equal(t, 1, leaf.mounts)
	require.Equal(t, 1, tui.mouseRefCount)
	firstCtx := leaf.mountCtx

	host.toggle(false)
	tui.Step()
	require.Equal(t, 1, leaf.dismounts)
	require.Error(t, firstCtx.Err(), "dismount should cancel the mount context")
	require.Equal(t, 0, tui.mouseRefCount)

	host.toggle(true)
	frame := strings.Join(tui.Step(), "\n")
	require.Contains(t, frame, "leaf")
	require.Equal(t, 2, leaf.mounts, "descendant of a remounted subtree was not remounted")
	require.Same(t, tui, leaf.compo().tui)
	require.NotNil(t, leaf.compo().mountCtx)
	require.NoError(t, leaf.mountCtx.Err(), "remounted descendant has a dead mount context")
	require.Equal(t, 1, tui.mouseRefCount, "remounted descendant did not re-enable mouse")

	// Update() from the remounted descendant must still reach the root and
	// re-render it.
	leaf.text = "changed"
	leaf.Update()
	frame = strings.Join(tui.Step(), "\n")
	require.Contains(t, frame, "changed", "Update from a remounted descendant did not reach the root")
}

// condHost renders its leaf only while the leaf has text, so the leaf's own
// Update decides whether it is displayed.
type condHost struct {
	Compo
	leaf *mountProbe
}

func (h *condHost) Render(ctx Context) {
	if h.leaf.text != "" {
		h.RenderChild(ctx, h.leaf)
	}
}

// A dismounted component's Update still reaches a former ancestor that is
// alive and mounted, so a host that shows a child depending on the child's
// own state re-renders when that state changes.
func TestDismountedComponentUpdateReachesLiveHost(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "first"}
	tui.AddChild(&condHost{leaf: leaf})
	require.Contains(t, strings.Join(tui.Step(), "\n"), "first")

	leaf.text = ""
	leaf.Update()
	tui.Step()
	require.Equal(t, 1, leaf.dismounts)

	leaf.text = "second"
	leaf.Update()
	require.Contains(t, strings.Join(tui.Step(), "\n"), "second",
		"Update from a dismounted component did not reach its live host")
	require.Equal(t, 2, leaf.mounts)
}

// freshWrapHost renders its leaf, while the leaf has text, inside a wrapper
// it builds anew on every render — so nothing but the leaf's own back-link
// refers to the wrapper once it is dropped.
type freshWrapHost struct {
	Compo
	leaf *mountProbe
}

func (h *freshWrapHost) Render(ctx Context) {
	if h.leaf.text != "" {
		h.RenderChild(ctx, &plainWrapper{child: h.leaf})
	}
}

// Update from a dismounted component reaches the host that dropped it even
// after the intermediate wrapper it was rendered through has been collected.
func TestDismountedComponentUpdateOutlivesTransientWrapper(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "first"}
	tui.AddChild(&freshWrapHost{leaf: leaf})
	require.Contains(t, strings.Join(tui.Step(), "\n"), "first")

	leaf.text = ""
	leaf.Update()
	tui.Step()
	require.Equal(t, 1, leaf.dismounts)

	runtime.GC()
	runtime.GC()
	require.Nil(t, leaf.compo().detachedParent.Value(), "transient wrapper should have been collected")

	leaf.text = "second"
	leaf.Update()
	require.Contains(t, strings.Join(tui.Step(), "\n"), "second",
		"Update from a dismounted component did not reach the host that dropped it")
	require.Equal(t, 2, leaf.mounts)
}

// Explicitly removing an ancestor of a focus target that is dismounted at the
// time must still invalidate that target, whether it holds focus directly or
// waits in a focus scope to be restored — and must not depend on whether the
// removed ancestor has been garbage-collected by then. The check walks the
// target's former parent links, which therefore stay strong above a focus
// target.
func TestExplicitRemovalOfDismountedFocusAncestor(t *testing.T) {
	// host → wrap → inner → p, where host renders wrap conditionally (a
	// render-driven dismount) and inner is a structural child of wrap. Only
	// wrap holds inner, so once removed, inner is garbage unless p's
	// back-link keeps it.
	setup := func(t *testing.T, focusBeforeDismount bool) (*TUI, *showHost, *Container, *focusProbe) {
		var log []string
		tui := newFocusTUI()
		p := &focusProbe{log: &log}
		wrap := &Container{}
		func() {
			inner := &Container{}
			inner.AddChild(p)
			wrap.AddChild(inner)
		}()
		host := &showHost{child: wrap, show: true}
		tui.AddChild(host)
		if focusBeforeDismount {
			tui.SetFocus(p)
		}
		tui.Step()

		host.toggle(false)
		tui.Step()
		require.Nil(t, p.compo().tui, "p should be dismounted")
		if !focusBeforeDismount {
			// Focusing it now must pin the chain that the dismount left
			// weak.
			tui.SetFocus(p)
		}
		require.Same(t, p, tui.Focused(), "render-driven dismount keeps the focus target")
		return tui, host, wrap, p
	}

	removeInner := func(wrap *Container) {
		wrap.RemoveChild(wrap.Children[0])
		runtime.GC()
		runtime.GC()
	}

	t.Run("focused", func(t *testing.T) {
		tui, _, wrap, _ := setup(t, true)
		removeInner(wrap)
		require.Nil(t, tui.Focused(), "removing a dismounted ancestor kept its descendant focused")
	})

	for _, before := range []bool{true, false} {
		name := "restored after overlay"
		if !before {
			name += ", focused after dismount"
		}
		t.Run(name, func(t *testing.T) {
			tui, _, wrap, _ := setup(t, before)
			var overlayLog []string
			overlay := &focusProbe{log: &overlayLog}
			oh := tui.ShowOverlay(overlay, nil)
			h := tui.PushFocus(overlay)

			removeInner(wrap)
			h.Restore()
			oh.Remove()
			require.Nil(t, tui.Focused(), "restored focus into an explicitly removed subtree")
		})
	}

	// The target's subtree is dropped first and the removed ancestor's in a
	// later frame, so the second dismount sees no focus target below it.
	t.Run("ancestor dismounted in a later frame", func(t *testing.T) {
		var log []string
		tui := newFocusTUI()
		p := &focusProbe{log: &log}
		g := &Container{}
		var host, x *showHost
		func() {
			inner := &Container{}
			inner.AddChild(p)
			wrap := &Container{}
			wrap.AddChild(inner)
			host = &showHost{child: wrap, show: true}
			x = &showHost{child: host, show: true}
			g.AddChild(x)
		}()
		tui.AddChild(g)
		tui.SetFocus(p)
		tui.Step()

		host.toggle(false)
		tui.Step()
		x.toggle(false)
		tui.Step()
		host, x = nil, nil

		var overlayLog []string
		overlay := &focusProbe{log: &overlayLog}
		oh := tui.ShowOverlay(overlay, nil)
		h := tui.PushFocus(overlay)
		g.RemoveChild(g.Children[0])
		tui.Step() // dismount x, so nothing in the tree holds it any more
		runtime.GC()
		runtime.GC()
		h.Restore()
		oh.Remove()
		require.Nil(t, tui.Focused(), "restored focus into an explicitly removed subtree")
	})

	// A target that never mounted, in a container that is dismounted before
	// or after it is focused: its own link is structural and strong, the
	// container's is weak.
	for _, focusFirst := range []bool{true, false} {
		name := "never-mounted target, focused before container dismount"
		if !focusFirst {
			name = "never-mounted target, focused after container dismount"
		}
		t.Run(name, func(t *testing.T) {
			var log []string
			tui := newFocusTUI()
			p := &focusProbe{log: &log}
			g := &Container{}
			inner := &Container{}
			host := &showHost{child: inner, show: true}
			g.AddChild(host)
			tui.AddChild(g)
			tui.Step()

			if focusFirst {
				inner.AddChild(p)
				tui.SetFocus(p)
			}
			host.toggle(false)
			tui.Step()
			if !focusFirst {
				inner.AddChild(p)
				tui.SetFocus(p)
			}
			host = nil

			var overlayLog []string
			overlay := &focusProbe{log: &overlayLog}
			oh := tui.ShowOverlay(overlay, nil)
			h := tui.PushFocus(overlay)
			g.RemoveChild(g.Children[0])
			tui.Step()
			runtime.GC()
			runtime.GC()
			h.Restore()
			oh.Remove()
			require.Nil(t, tui.Focused(), "restored focus into an explicitly removed subtree")
		})
	}

	// Focused while unattached, then added under a dismounted container: no
	// chain existed to retain when focus was set.
	t.Run("target attached under a dismounted container after focusing", func(t *testing.T) {
		var log []string
		tui := newFocusTUI()
		p := &focusProbe{log: &log}
		g := &Container{}
		inner := &Container{}
		host := &showHost{child: inner, show: true}
		g.AddChild(host)
		tui.AddChild(g)
		tui.Step()
		host.toggle(false)
		tui.Step()
		host = nil

		tui.SetFocus(p)
		inner.AddChild(p)

		var overlayLog []string
		overlay := &focusProbe{log: &overlayLog}
		oh := tui.ShowOverlay(overlay, nil)
		h := tui.PushFocus(overlay)
		g.RemoveChild(g.Children[0])
		tui.Step()
		runtime.GC()
		runtime.GC()
		h.Restore()
		oh.Remove()
		require.Nil(t, tui.Focused(), "restored focus into an explicitly removed subtree")
	})

	// A target whose removed ancestor it was moved out from under before
	// Restore gets focus back: the check follows its current links.
	t.Run("target reparented after its ancestor was removed", func(t *testing.T) {
		var log []string
		tui := newFocusTUI()
		p := &focusProbe{log: &log}
		rows := &Container{}
		row := &Container{}
		row.AddChild(p)
		rows.AddChild(row)
		tui.AddChild(rows)
		tui.SetFocus(p)
		tui.Step()

		var overlayLog []string
		overlay := &focusProbe{log: &overlayLog}
		oh := tui.ShowOverlay(overlay, nil)
		h := tui.PushFocus(overlay)
		rows.RemoveChild(row)
		tui.Step()
		newRow := &Container{}
		newRow.AddChild(p)
		rows.AddChild(newRow)
		tui.Step()
		require.Same(t, tui, p.compo().tui, "p should be mounted under its new row")

		h.Restore()
		oh.Remove()
		require.Same(t, p, tui.Focused(), "reparented target should get focus back")
	})

	// Removing a focused child from a detached container clears focus even
	// once the components that linked the container to the tree are gone.
	t.Run("removal from a detached container", func(t *testing.T) {
		tui := newFocusTUI()
		c := &Container{}
		x := &showHost{show: true}
		func() {
			x.child = &showHost{child: c, show: true}
		}()
		tui.AddChild(x)
		tui.Step()

		x.child.(*showHost).toggle(false)
		tui.Step()
		x.child = nil
		x.toggle(false)
		tui.Step()
		runtime.GC()
		runtime.GC()

		var log []string
		n := &focusProbe{log: &log}
		c.AddChild(n)
		tui.SetFocus(n)
		c.RemoveChild(n)
		require.Nil(t, tui.Focused(), "removed child kept focus")
	})

	t.Run("restored without removal", func(t *testing.T) {
		tui, host, _, p := setup(t, true)
		var overlayLog []string
		overlay := &focusProbe{log: &overlayLog}
		oh := tui.ShowOverlay(overlay, nil)
		h := tui.PushFocus(overlay)
		h.Restore()
		oh.Remove()
		require.Same(t, p, tui.Focused(), "a merely omitted target should get focus back")

		host.toggle(true)
		tui.Step()
		require.True(t, p.focused, "remounted target was not re-notified")
	})
}
