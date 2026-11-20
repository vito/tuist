package tuist

import (
	"context"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// mountProbe is a leaf that counts its lifecycle and keeps its mount context.
// It is MouseEnabled and Pasteable so the tests can see mouse and paste
// registration follow mounts.
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

func (p *mountProbe) HandlePaste(Context, uv.PasteEvent) bool { return false }

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

// hideAndShow mounts host's subtree, omits it for a frame, then re-includes
// it unchanged.
func hideAndShow(t *testing.T, tui *TUI, host *showHost) {
	t.Helper()
	tui.Step()
	host.toggle(false)
	tui.Step()
	host.toggle(true)
	tui.Step()
}

// A subtree omitted from one frame and re-included unchanged must remount all
// of it, not just its top. Its top's render cache would otherwise still be
// valid (same generation, same width), so Render -- and with it the
// RenderChild calls that mount the descendants -- would never run.
func TestRemountedSubtreeRemountsDescendants(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "leaf"}
	mid := &plainWrapper{child: leaf}
	host := &showHost{child: mid, show: true}
	tui.AddChild(host)

	tui.Step()
	require.Equal(t, 1, leaf.mounts)
	firstCtx := leaf.mountCtx

	host.toggle(false)
	tui.Step()
	require.Equal(t, 1, leaf.dismounts)
	require.Error(t, firstCtx.Err(), "dismount should cancel the mount context")

	host.toggle(true)
	frame := strings.Join(tui.Step(), "\n")
	require.Contains(t, frame, "leaf")
	require.Equal(t, 2, leaf.mounts, "descendant of a remounted subtree was not remounted")
	require.Same(t, tui, leaf.compo().tui)
	require.NotNil(t, leaf.mountCtx)
	require.NoError(t, leaf.mountCtx.Err(), "remounted descendant has a dead mount context")
}

// The same holds through a Container, several levels down.
func TestRemountedContainerRemountsDescendants(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "leaf"}
	inner := &Container{}
	inner.AddChild(leaf)
	outer := &Container{}
	outer.AddChild(inner)
	host := &showHost{child: outer, show: true}
	tui.AddChild(host)

	hideAndShow(t, tui, host)
	require.Equal(t, 2, leaf.mounts)
	require.Same(t, tui, leaf.compo().tui)
}

// A remounted descendant registers for mouse and paste again: dismount
// unregistered it, so without a remount the terminal would stop reporting
// them.
func TestRemountedDescendantRestoresMouseAndPaste(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "leaf"}
	host := &showHost{child: &plainWrapper{child: leaf}, show: true}
	tui.AddChild(host)

	tui.Step()
	require.Equal(t, 1, tui.mouseRefCount)
	require.Equal(t, 1, tui.pasteRefCount)

	host.toggle(false)
	tui.Step()
	require.Equal(t, 0, tui.mouseRefCount)
	require.Equal(t, 0, tui.pasteRefCount)

	host.toggle(true)
	tui.Step()
	require.Equal(t, 1, tui.mouseRefCount, "remounted descendant did not re-enable mouse")
	require.Equal(t, 1, tui.pasteRefCount, "remounted descendant did not re-enable paste")
}

// Update from a remounted descendant still reaches the root and re-renders.
func TestRemountedDescendantUpdateReachesRoot(t *testing.T) {
	tui := newFocusTUI()
	leaf := &mountProbe{text: "leaf"}
	host := &showHost{child: &plainWrapper{child: leaf}, show: true}
	tui.AddChild(host)
	hideAndShow(t, tui, host)

	leaf.text = "changed"
	leaf.Update()
	require.Contains(t, strings.Join(tui.Step(), "\n"), "changed",
		"Update from a remounted descendant did not reach the root")
}

// A focused descendant of a remounted subtree gets SetFocused afresh with its
// new mount context.
func TestRemountedDescendantIsRenotifiedOfFocus(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	p := &focusProbe{log: &log}
	host := &showHost{child: &plainWrapper{child: p}, show: true}
	tui.AddChild(host)
	tui.SetFocus(p)
	hideAndShow(t, tui, host)

	requireLog(t, log, []string{
		"mount(ctx=true)", "focused(true,ctx=true)", "render",
		"mount(ctx=true)", "focused(true,ctx=true)",
	})
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
// again unchanged is still served from its cache.
func TestRemountedLeafKeepsCache(t *testing.T) {
	tui := newFocusTUI()
	leaf := &countingLeaf{}
	host := &showHost{child: leaf, show: true}
	tui.AddChild(host)
	hideAndShow(t, tui, host)
	require.Equal(t, 1, leaf.renders)
}
