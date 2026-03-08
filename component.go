package tuist

import (
	"context"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// ── EventContext ───────────────────────────────────────────────────────────

// EventContext provides access to framework operations. It is passed
// to event handlers, lifecycle hooks, and dispatch callbacks — the places
// where components perform side effects. It is NOT available during Render,
// which should be a pure function of component state.
//
// EventContext embeds [context.Context]. The Done() channel is closed when
// the source component is dismounted, so background goroutines spawned from
// OnMount can use it as a cancellation signal.
type EventContext struct {
	context.Context
	tui    *TUI
	source Component
}

// SetFocus gives keyboard focus to the given component (or nil to blur).
func (ctx EventContext) SetFocus(comp Component) {
	ctx.tui.SetFocus(comp)
}

// ShowOverlay displays a component as an overlay and returns a handle.
func (ctx EventContext) ShowOverlay(comp Component, opts *OverlayOptions) *OverlayHandle {
	return ctx.tui.ShowOverlay(comp, opts)
}

// AddInputListener registers a listener that intercepts input before it
// reaches the focused component. Returns a removal function.
func (ctx EventContext) AddInputListener(l InputListener) func() {
	return ctx.tui.AddInputListener(l)
}

// RequestRender schedules a render. If repaint is true, forces full redraw.
func (ctx EventContext) RequestRender(repaint bool) {
	ctx.tui.RequestRender(repaint)
}

// HasKittyKeyboard reports terminal keyboard protocol support.
func (ctx EventContext) HasKittyKeyboard() bool {
	return ctx.tui.HasKittyKeyboard()
}

// HasOverlay reports whether any overlay is currently visible.
func (ctx EventContext) HasOverlay() bool {
	return ctx.tui.HasOverlay()
}

// EnableMouse increments the mouse reference count, enabling terminal mouse
// reporting if it wasn't already enabled. Call DisableMouse to decrement.
func (ctx EventContext) EnableMouse() {
	ctx.tui.EnableMouse()
}

// DisableMouse decrements the mouse reference count, disabling terminal
// mouse reporting when no components need it.
func (ctx EventContext) DisableMouse() {
	ctx.tui.DisableMouse()
}

// Dispatch schedules a function to run on the UI goroutine.
//
// Safe to call from any goroutine. This is the primary way for
// background goroutines (spawned from OnMount, commands, etc.) to
// mutate component state and call [Compo.Update]. The caller already
// has the EventContext in closure scope, so the callback doesn't
// receive one.
func (ctx EventContext) Dispatch(fn func()) {
	ctx.tui.Dispatch(fn)
}

// SetDebugWriter enables render performance logging. Must be called on
// the UI goroutine (from an event handler or Dispatch callback).
func (ctx EventContext) SetDebugWriter(w io.Writer) {
	ctx.tui.debugWriter = w
}



// ── Render ─────────────────────────────────────────────────────────────────

// RenderContext carries everything a component needs to render.
type RenderContext struct {
	// Width is the available width in terminal columns.
	Width int
	// Height is the allocated height in lines. 0 means unconstrained
	// (the component may return as many lines as it wants).
	Height int
	// ScreenHeight is the actual terminal height in rows. It is always
	// set regardless of whether Height constrains the component. Components
	// that render inline but want to fill the viewport can use this.
	ScreenHeight int

	// Recycle is a pre-allocated []string from the previous render,
	// resliced to zero length. Components may append into it to avoid
	// allocating a new lines slice each frame. It is nil on the first
	// render. Components that ignore it get no behavior change.
	//
	// The slice is safe to reuse because parent containers copy child
	// lines into their own buffer via append.
	Recycle []string

	// componentStats, when non-nil, collects per-component render
	// metrics. Set by the TUI when debug logging is enabled.
	componentStats *[]ComponentStat
}

// ComponentStat captures render metrics for a single component within
// a frame.
type ComponentStat struct {
	Name     string `json:"name"`
	RenderUs int64  `json:"render_us"`
	Lines    int    `json:"lines"`
	Cached   bool   `json:"cached"`
}

// componentName returns a short human-readable name for a component.
func componentName(c Component) string {
	if n, ok := c.(interface{ Name() string }); ok {
		return n.Name()
	}
	t := reflect.TypeOf(c)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() + "." + t.Name()
}

// CursorPos represents a cursor position within a component's rendered output.
type CursorPos struct {
	Row, Col int
}

// RenderResult is the output of a Component.Render call.
type RenderResult struct {
	// Lines is the rendered content.
	Lines []string

	// Cursor, if non-nil, is where the hardware cursor should be placed,
	// relative to this component's output (Row 0 = first line of Lines).
	Cursor *CursorPos
}

// ── Compo ──────────────────────────────────────────────────────────────────

// ── Mouse zone markers ─────────────────────────────────────────────────────

// markerCounter allocates unique marker IDs. IDs start at 1000 to avoid
// collision with typical ANSI sequences.
var markerCounter atomic.Int64

func init() { markerCounter.Store(1000) }

// markerOf returns the CSI marker string for a component, allocating an
// ID on first use. The marker is a private CSI sequence (ESC[<id>z) that
// terminals ignore and lipgloss.Width treats as zero-width.
func markerOf(comp Component) string {
	cp := comp.compo()
	id := cp.markerID.Load()
	if id == 0 {
		id = markerCounter.Add(1)
		cp.markerID.Store(id)
	}
	return "\x1b[" + strconv.FormatInt(id, 10) + "z"
}

func markLines(comp Component, lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	m := markerOf(comp)
	marked := make([]string, len(lines))
	copy(marked, lines)
	marked[0] = m + marked[0]
	marked[len(marked)-1] = marked[len(marked)-1] + m
	return marked
}

// Compo provides automatic render caching and dirty propagation for
// components. Embed it in your component struct:
//
//	type MyWidget struct {
//	    tuist.Compo
//	    // ... your fields ...
//	}
//
// Call Update() when your component's state changes. The framework will
// re-render the component on the next frame. Between Update() calls,
// Render() is skipped entirely and the cached result is reused.
//
// Update() propagates upward through the component tree, so parent
// Containers automatically know a child changed. If the tree is rooted
// in a TUI, Update() also schedules a render automatically.
//
// Dirty tracking uses a monotonic generation counter rather than a
// boolean flag. Update() increments the counter; renderComponent
// snapshots it before calling Render and records the snapshot
// afterwards. Any concurrent Update() during Render increments the
// counter past the snapshot, guaranteeing a re-render on the next
// frame — no store-ordering subtleties required.
type Compo struct {
	generation    atomic.Int64
	renderedGen   int64        // generation when last rendered; render-goroutine only
	cache         *renderCache // only accessed from the render goroutine
	parent        *Compo
	self          Component    // the Component that embeds this Compo; set by RenderChild
	requestRender func()       // set on the root by TUI
	markerID      atomic.Int64 // unique zone marker ID, lazy-allocated

	// Double-buffered line slices for zero-allocation rendering.
	// renderComponent alternates between lineBufs[0] and lineBufs[1]
	// so the previous render's slice (which may be referenced by
	// TUI.previousLines or a parent's buffer) is never overwritten.
	// The alternate buffer is offered to Render via ctx.Recycle.
	lineBufs [2][]string
	bufIdx   int

	// Inline children rendered via RenderChild. Populated during Render,
	// cleared at the start of each cache-miss re-render. Used by
	// collectMarkers to discover zone markers for positional mouse
	// dispatch, and by dismountTree for lifecycle cleanup.
	//
	// MouseEnabled inline children are auto-mounted on first encounter
	// so they participate in mouse tracking and zone dispatch.
	renderChildren []Component

	// componentStats is propagated from parent to child during rendering
	// so that stats collection works across package boundaries (where
	// the unexported RenderContext field would be lost). Set by
	// renderComponent before calling Render, inherited by children
	// via RenderChild.
	componentStats *[]ComponentStat

	// Lifecycle — managed by the framework during mount/dismount.
	// Components never access these directly; they receive EventContext
	// through handlers and lifecycle hooks.
	tui         *TUI
	mountCtx    context.Context
	mountCancel context.CancelFunc
}

type renderCache struct {
	result RenderResult
	width  int
}

// Update marks the component as needing re-render on the next frame.
// Propagates upward so parent containers are also marked dirty.
// If the component tree is rooted in a TUI, a render is scheduled
// automatically.
//
// Must be called from the UI goroutine (event handlers, lifecycle hooks,
// or Dispatch callbacks). Background goroutines should use
// [EventContext.Dispatch] to schedule state changes and Update calls.
func (c *Compo) Update() {
	c.generation.Add(1)
	if c.parent != nil {
		c.parent.Update()
	} else if c.requestRender != nil {
		c.requestRender()
	}
}

// compo returns the embedded Compo. The unexported method ensures that
// only types embedding Compo can satisfy the Component interface.
func (c *Compo) compo() *Compo { return c }

// RenderChild renders a child component through this Compo, using the
// framework's render cache. It also wires the child's parent pointer so
// that Update() on the child propagates upward through this component.
//
// Children are automatically mounted into the TUI when the parent is
// mounted. This fires [Mounter.OnMount] lifecycle hooks and wires the
// component into the tree so [Compo.Update] propagation reaches
// [TUI.RequestRender]. When the parent is re-rendered without calling
// RenderChild for a previously rendered child, that child is
// dismounted automatically.
//
// MouseEnabled children additionally have their output wrapped with
// zone markers for positional mouse dispatch.
//
// Use this instead of calling child.Render(ctx) directly when your
// component wraps another component without using Container or Slot:
//
//	func (w *MyWrapper) Render(ctx tuist.RenderContext) tuist.RenderResult {
//	    return w.RenderChild(w.inner, ctx)
//	}
func (c *Compo) RenderChild(child Component, ctx RenderContext) RenderResult {
	child.compo().parent = c
	child.compo().componentStats = c.componentStats
	c.renderChildren = append(c.renderChildren, child)

	// Auto-mount inline children so they get lifecycle hooks and
	// proper Update() propagation through the TUI.
	if c.tui != nil {
		cp := child.compo()
		if cp.tui == nil {
			cp.self = child
			cp.tui = c.tui
			if c.mountCtx != nil {
				cp.mountCtx, cp.mountCancel = context.WithCancel(c.mountCtx)
			}
			if _, ok := child.(MouseEnabled); ok {
				c.tui.EnableMouse()
			}
			if m, ok := child.(Mounter); ok {
				ectx := EventContext{
					Context: cp.mountCtx,
					tui:     c.tui,
					source:  child,
				}
				m.OnMount(ectx)
			}
		}
	}

	r := renderComponent(child, ctx)
	if _, ok := child.(MouseEnabled); ok {
		r.Lines = markLines(child, r.Lines)
	}
	return r
}

// RenderChildInline renders a child component and returns the result as
// a single string suitable for inline embedding within a parent's line.
// For MouseEnabled children, the string is automatically wrapped with
// zone markers for positional mouse dispatch.
//
// This is a convenience wrapper around [RenderChild] for components that
// produce content meant to be composed horizontally within a parent's
// rendered line:
//
//	func (c *Chrome) Render(ctx tuist.RenderContext) tuist.RenderResult {
//	    re := c.RenderChildInline(c.reInput, ctx)
//	    im := c.RenderChildInline(c.imInput, ctx)
//	    top := title + " re " + re + "  im " + im
//	    return tuist.RenderResult{Lines: []string{top}}
//	}
func (c *Compo) RenderChildInline(child Component, ctx RenderContext) string {
	r := c.RenderChild(child, ctx)
	return strings.Join(r.Lines, "")
}

// renderComponent renders a child component, using its Compo cache when
// the component is clean and the width hasn't changed. This is the core
// function that makes finalized components O(1).
//
// Dirty tracking is race-free: the generation counter is snapshotted
// before Render and recorded after. Any concurrent Update() increments
// the counter past the snapshot, so the next renderComponent call sees
// a mismatch and re-renders. No boolean store-ordering issues.
func renderComponent(ch Component, ctx RenderContext) RenderResult {
	cp := ch.compo()

	// Resolve stats collector: prefer ctx (fresh each frame from the TUI),
	// fall back to the Compo's (propagated from parent via RenderChild,
	// for cross-package calls where ctx loses the unexported field).
	stats := ctx.componentStats
	if stats == nil {
		stats = cp.componentStats
	}
	// Store it so this component's RenderChild calls can propagate it.
	cp.componentStats = stats

	gen := cp.generation.Load()
	if cp.cache != nil && gen == cp.renderedGen && cp.cache.width == ctx.Width {
		// Cache hit — skip Render entirely.
		if stats != nil {
			*stats = append(*stats, ComponentStat{
				Name:   componentName(ch),
				Lines:  len(cp.cache.result.Lines),
				Cached: true,
			})
		}
		return cp.cache.result
	}

	// Cache miss — render and store. Record the generation we checked,
	// not the current one, so any Update() during Render() is visible
	// as a generation mismatch on the next frame.
	//
	// Flip to the alternate line buffer and offer it via ctx.Recycle.
	// The previous render's buffer (lineBufs[bufIdx]) may still be
	// referenced by TUI.previousLines or a parent container, so we
	// use the OTHER buffer. Components that append into ctx.Recycle
	// avoid allocating a fresh slice each frame.
	cp.bufIdx ^= 1
	ctx.Recycle = cp.lineBufs[cp.bufIdx][:0]

	// Save previous render children for orphan cleanup after Render.
	// Nil out (rather than [:0]) so Render's appends don't alias.
	prevRenderChildren := cp.renderChildren
	cp.renderChildren = nil
	var r RenderResult
	if stats != nil {
		start := time.Now()
		r = ch.Render(ctx)
		elapsed := time.Since(start)
		*stats = append(*stats, ComponentStat{
			Name:     componentName(ch),
			RenderUs: elapsed.Microseconds(),
			Lines:    len(r.Lines),
		})
	} else {
		r = ch.Render(ctx)
	}
	// Save back in case append grew the slice.
	cp.lineBufs[cp.bufIdx] = r.Lines
	cp.cache = &renderCache{result: r, width: ctx.Width}
	cp.renderedGen = gen

	// Dismount render children that were present last frame but not this
	// frame (the parent's Render stopped calling RenderChild for them).
	for _, prev := range prevRenderChildren {
		if prev.compo().tui == nil {
			continue // was never auto-mounted
		}
		found := slices.Contains(cp.renderChildren, prev)
		if !found {
			dismountTree(prev)
		}
	}

	return r
}

// ── Component interfaces ───────────────────────────────────────────────────

// Component is the interface all UI components must implement.
// All components must embed Compo to get automatic render caching
// and dirty propagation.
type Component interface {
	// compo returns the embedded Compo. Unexported to keep it out of
	// the public API; satisfied automatically by embedding Compo.
	compo() *Compo

	// Render produces the visual output within the given constraints.
	Render(ctx RenderContext) RenderResult
}

// Interactive is an optional interface for components that accept keyboard
// input when focused. The TUI decodes raw terminal bytes and dispatches
// typed events; components never see raw bytes.
//
// Key events are delivered to the focused component first. If
// HandleKeyPress returns false, the event bubbles up through parent
// components in the tree (any parent implementing Interactive gets a
// chance to handle it). If the focused component does not implement
// Interactive at all, the event bubbles immediately.
type Interactive interface {
	Component

	// HandleKeyPress is called with a decoded key press event.
	// Return true if the event was consumed; return false to let it
	// bubble to the parent component.
	HandleKeyPress(ctx EventContext, ev uv.KeyPressEvent) bool
}

// Pasteable is an optional interface for components that accept pasted
// text (via bracketed paste). Paste events bubble like key events: if
// HandlePaste returns false, the event propagates to the parent.
type Pasteable interface {
	HandlePaste(ctx EventContext, ev uv.PasteEvent) bool
}

// MouseEvent wraps an ultraviolet mouse event with component-relative
// coordinates for hit-testing within the component's rendered output.
//
// Use Row and Col for position checks. Use the embedded [uv.MouseEvent]
// for button/modifier info and to distinguish event subtypes:
//
//	switch ev.MouseEvent.(type) {
//	case uv.MouseClickEvent:
//	case uv.MouseMotionEvent:
//	case uv.MouseWheelEvent:
//	}
type MouseEvent struct {
	uv.MouseEvent

	// Row is the mouse Y position relative to this component's first
	// rendered line (0-indexed).
	Row int

	// Col is the mouse X position (terminal column, 0-indexed).
	Col int
}

// MouseEnabled is an optional interface for components that need mouse
// event capture. When a component implementing MouseEnabled is mounted
// into a TUI-rooted tree, the TUI enables terminal mouse reporting
// (SGR extended mode with all-motion tracking). When the last such
// component is dismounted, mouse reporting is disabled and normal
// terminal scrollback behavior is restored.
//
// Mouse events are dispatched positionally: the framework finds the
// deepest MouseEnabled component whose rendered region contains the
// mouse cursor and delivers the event there. If HandleMouse returns
// false, the event bubbles up through parent components in the tree
// (like key events). When MouseEnabled overlays are active, dispatch
// falls back to focus-based delivery.
type MouseEnabled interface {
	Component

	// HandleMouse is called with a decoded mouse event. Use ev.Row and
	// ev.Col for component-relative hit testing. Switch on
	// ev.MouseEvent.(type) to distinguish clicks, motion, and wheel
	// events. Return true if the event was consumed; return false to
	// let it bubble to the parent component.
	HandleMouse(ctx EventContext, ev MouseEvent) bool
}

// Hoverable is an optional interface for MouseEnabled components that want
// to know when the mouse enters or leaves their rendered region. This is
// useful for clearing hover highlights when the cursor moves away.
//
// SetHovered(true) is called when the mouse first enters the component's
// region. SetHovered(false) is called when the mouse leaves (moves to a
// different component or to a non-interactive area).
type Hoverable interface {
	SetHovered(ctx EventContext, hovered bool)
}

// Focusable is an optional interface for components that want to know when
// they gain or lose focus (e.g. to show/hide a cursor).
type Focusable interface {
	SetFocused(ctx EventContext, focused bool)
}

// Mounter is an optional interface for components that need to perform
// setup when they enter a TUI-rooted tree. The EventContext embeds
// context.Context whose Done() channel is closed when the component is
// dismounted — use it to bound background goroutine lifetimes.
//
// OnMount is called lazily during the first render after a component
// is added to a Container, Slot, or rendered via [RenderChild]. This
// means OnMount fires on the UI goroutine during the render pass, not
// immediately when AddChild or Set is called.
type Mounter interface {
	OnMount(ctx EventContext)
}

// Dismounter is an optional interface for components that need to perform
// cleanup when they leave a TUI-rooted tree. The mount context's Done()
// channel is already closed when OnDismount is called.
//
// Dismount fires children-first (leaves before parents).
type Dismounter interface {
	OnDismount()
}

// ── Lifecycle propagation ──────────────────────────────────────────────────

// dismountTree dismounts a component and all its descendants, firing
// OnDismount hooks children-first (leaves before parents) and cancelling
// mount contexts. If a component implements MouseEnabled, the TUI's
// mouse reference count is decremented (disabling terminal mouse
// reporting when the last such component is dismounted).
//
// Children are discovered via renderChildren, which is populated by
// RenderChild during rendering. Container and Slot use RenderChild,
// so their children appear in renderChildren automatically.
func dismountTree(comp Component) {
	cp := comp.compo()

	// Dismount children rendered via RenderChild.
	for _, child := range cp.renderChildren {
		if child.compo().tui != nil {
			dismountTree(child)
		}
	}
	cp.renderChildren = nil

	if cp.mountCancel != nil {
		cp.mountCancel()
	}
	if d, ok := comp.(Dismounter); ok {
		d.OnDismount()
	}

	// Decrement mouse ref count before clearing tui pointer.
	if _, ok := comp.(MouseEnabled); ok && cp.tui != nil {
		cp.tui.DisableMouse()
	}

	cp.tui = nil
	cp.mountCtx = nil
	cp.mountCancel = nil
}

// ── Container ──────────────────────────────────────────────────────────────

// Container is a Component that holds child components and renders them
// sequentially (vertical stack). It embeds Compo, so parent containers
// can cache entire subtrees when nothing changes.
//
// Children are mounted lazily via [RenderChild] on the first render
// after being added. Removed children are dismounted when the parent
// re-renders and they are no longer in the child list (orphan cleanup).
type Container struct {
	Compo
	Children []Component
}

// AddChild appends a component to the container. The child will be
// mounted on the next render via [RenderChild].
func (c *Container) AddChild(comp Component) {
	c.Children = append(c.Children, comp)
	c.Update()
}

// RemoveChild removes a component from the container. The child will
// be dismounted when the container re-renders (orphan cleanup).
func (c *Container) RemoveChild(comp Component) {
	for i, ch := range c.Children {
		if ch == comp {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			c.Update()
			return
		}
	}
}

// Clear removes all children. They will be dismounted when the
// container re-renders (orphan cleanup).
func (c *Container) Clear() {
	c.Children = nil
	c.Update()
}

func (c *Container) Render(ctx RenderContext) RenderResult {
	lines := ctx.Recycle
	var cursor *CursorPos
	for _, ch := range c.Children {
		r := c.RenderChild(ch, ctx)
		if r.Cursor != nil {
			cursor = &CursorPos{
				Row: len(lines) + r.Cursor.Row,
				Col: r.Cursor.Col,
			}
		}
		lines = append(lines, r.Lines...)
	}
	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
	}
}

// ── Slot ───────────────────────────────────────────────────────────────────

// Slot is a component that delegates to a single replaceable child.
// Use it to swap between components (e.g. text input vs spinner)
// without modifying the parent container's child list.
//
// The child is mounted lazily via [RenderChild] on the first render
// after being set. The previous child is dismounted when the Slot
// re-renders and it is no longer the current child (orphan cleanup).
type Slot struct {
	Compo
	child Component
}

// NewSlot creates a Slot with the given initial child.
func NewSlot(child Component) *Slot {
	return &Slot{child: child}
}

// Set replaces the current child. The old child will be dismounted
// and the new child mounted on the next render.
func (s *Slot) Set(c Component) {
	s.child = c
	s.Update()
}

// Get returns the current child.
func (s *Slot) Get() Component {
	return s.child
}

func (s *Slot) Render(ctx RenderContext) RenderResult {
	if s.child == nil {
		return RenderResult{}
	}
	return s.RenderChild(s.child, ctx)
}
