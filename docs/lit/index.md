\use-plugin{tuist}

\styled{tuist-home}

# tuist {#index}

A component-based TUI framework for Go with cached rendering, line-level
diffing, and a single UI goroutine.

\header-links{
  [GitHub](https://github.com/vito/tuist)
}{
  [pkg.go.dev](https://pkg.go.dev/github.com/vito/tuist)
}

\shell{go get github.com/vito/tuist}

\table-of-contents

\include-section{../../README.md}

## minimal example

```go
type Counter struct {
    // All components embed tea.Compo.
    tuist.Compo
    // Components store state however they like (public or private).
    Count int
}

// All components implement Render.
func (c *Counter) Render(ctx tuist.Context) tuist.RenderResult {
    return tuist.RenderResult{
        Lines: []string{fmt.Sprintf("Count: %d", c.Count)},
    }
}

var _ tuist.Interactive = (*Counter)(nil)

// Interactive components handle keypresses. See [interfaces].
func (c *Counter) HandleKeyPress(_ tuist.Context, ev uv.KeyPressEvent) bool {
    c.Count++
    c.Update()
    return true
}

func main() {
    tui := tuist.New(tuist.NewStdTerminal())
    // Start the rendering + dispatch loop
    tui.Start()
    // Queue some updates for the UI goroutine.
    tui.Dispatch(func() {
        counter := &Counter{}
        tui.AddChild(counter)
        tui.SetFocus(counter)
    })
}
```

## how it works

Embedding `Compo` provides bookkeeping for the component in the UI tree. Each
component has a *generation*, a *parent*, and *children*.

Calling `Update()` increments the generation and propagates upward. On each
frame, Tuist only calls `Render()` for components whose generation is higher
than last render.

`Render()` returns rendered lines (`[]string`) and an optional cursor position.
Tuist only renders lines that changed from the last frame. If the lines are
offscreen, Tuist has to do a full repaint, but does so using synchronized output
(DEC 2026) so it won't flicker.

To avoid a sprawl of mutexes, Tuist provides `Dispatch()` for scheduling updates
in the frame rendering loop, where they will be coalesced, followed by a single
render:

```go
func (t *TUI) runLoop() {
    for {
        select {
        case ev := <-t.eventCh:    // decoded terminal input
            t.dispatchEvent(ev)
        case <-t.dispatchCh:       // closures from Dispatch()
            t.drainDispatchQ()
        case <-t.renderCh:         // render request
        }
        t.drainAll()             // coalesce rapid events
        t.doRender()             // render tree → diff → write deltas
    }
}
```

## interfaces

Components only need to implement `Render`. Everything else is opt-in:

```go
// Every component must embed Compo and implement Render.
type Component interface {
    Render(ctx Context) RenderResult
}

// Keyboard input. Events bubble up the parent chain if handler returns false.
type Interactive interface {
    HandleKeyPress(ctx Context, ev uv.KeyPressEvent) bool
}

// Mouse events with component-relative coordinates. Positional dispatch via zone markers.
type MouseEnabled interface {
    HandleMouse(ctx Context, ev MouseEvent) bool
}

// Lifecycle. Mount context is cancelled on dismount — use it to bound goroutines.
type Mounter    interface { OnMount(ctx Context) }
type Dismounter interface { OnDismount() }

// Focus/hover state notifications.
type Focusable  interface { SetFocused(ctx Context, bool) }
type Hoverable  interface { SetHovered(ctx Context, bool) }

// Bracketed paste.
type Pasteable  interface { HandlePaste(ctx Context, ev uv.PasteEvent) bool }
```

## composition

Compose components together by calling `RenderChild()` in the parent component's
`Render()` function.

```go
// Vertical stack — Container does this internally
func (c *MyLayout) Render(ctx tuist.Context) tuist.RenderResult {
    var lines []string
    for _, child := range c.children {
        r := c.RenderChild(ctx, child)
        lines = append(lines, r.Lines...)
    }
    return tuist.RenderResult{Lines: lines}
}

// With adjusted constraints — ctx.Resize returns a copy with new Width/Height
func (b *Border) Render(ctx tuist.Context) tuist.RenderResult {
    inner := b.RenderChild(ctx.Resize(ctx.Width-2, ctx.Height-2), b.child)
    // ... wrap inner.Lines with border chrome
}

// Inline — for embedding a child within a single line (e.g. a clickable value in a status bar)
func (c *Chrome) Render(ctx tuist.Context) tuist.RenderResult {
    re := c.RenderChildInline(ctx, c.reInput)  // returns string, zones auto-wired
    im := c.RenderChildInline(ctx, c.imInput)
    return tuist.RenderResult{Lines: []string{"re " + re + "  im " + im}}
}
```

`RenderChild` handles all the bookkeeping:

* Wires up parent/child relationships, so `Update()` propagates up the component
  tree.
* If the child component is `MouseEnabled`, its output is wrapped with "zone
  markers" for detecting and dispatching mouse events.
* When the child component is first mounted, `OnMount()` is called.
* When the child component is no longer mounted, `OnDismount()` is called.


## concurrency

Use goroutines like you normally would, and use `Dispatch()` to queue component
tree updates for the frame render loop:

```go
func (w *Widget) OnMount(ctx tuist.Context) {
    go func() {
        data, err := fetchData(ctx)  // ctx.Done() fires on dismount
        if err != nil { return }
        ctx.Dispatch(func() {
            w.data = data
            w.Update()
        })
    }()
}
```

## overlays

Overlays allow components like floating menus and notification bubbles to render
over the base content. They can be positioned relative to the viewport, the full
content, or the cursor.

```go
// Centered modal
handle := ctx.ShowOverlay(dialog, &tuist.OverlayOptions{
    Width:  tuist.SizePct(50),
    Anchor: tuist.AnchorCenter,
})

// Completion menu that follows the cursor, flips above/below as needed
handle := ctx.ShowOverlay(menu, &tuist.OverlayOptions{
    CursorRelative: true,
    PreferAbove:    true,
    CursorGroup:    group, // linked overlays share the above/below decision
})

handle.Hide()              // remove permanently
handle.SetHidden(true)     // toggle visibility
handle.SetOptions(newOpts) // reposition without recreating
```


## built-in components

* `Container` — renders children sequentially in a vertical stack. This is the
  starting point: `TUI` embeds it, and you call `AddChild`/`RemoveChild` to go
  from there.

* `Slot` — holds one replaceable child. `Set(child)` swaps it.

* `Spinner` — animated spinner.

* `TextInput` — (supposed-to-be) full-featured editing prompt, with built-in
  completion and ghost suggestions.

* `CompletionMenu` — fancy dropdown autocomplete wired to a `TextInput`.


## mouse support

Components implement `MouseEnabled` to handle mouse hover/click events. Mouse
support is integrated into the rendering and event pipeline, automatically
adding "zone markers" to a component's output and performing hit detection to
route events to the right component.

Tuist counts how many components implement `MouseEnabled` and emits the proper
terminal sequences to enable/disable mouse support automatically.

```go
func (c *Cell) HandleMouse(ctx tuist.Context, ev tuist.MouseEvent) bool {
    switch ev.MouseEvent.(type) {
    case uv.MouseClickEvent:
        ctx.SetFocus(c)
        return true
    case uv.MouseMotionEvent:
        c.cursorRow, c.cursorCol = ev.Row, ev.Col  // component-relative
        c.Update()
        return true
    }
    return false
}
```


## debug UI

Tuist can emit per-frame JSONL render stats for performance analysis. Set the
`TUIST_LOG` environment variable to a file path, or call `TUI.SetDebugWriter(w)`
to start logging. The `debugui` command provides a web dashboard that tails the
log and streams live charts:

```sh
go run github.com/vito/tuist/debugui@latest -f /tmp/tuist.log
```

\screenshot{img/debugui.png}{debug UI dashboard}
