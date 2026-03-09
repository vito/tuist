\use-plugin{chroma}
\use-plugin{tuist}

\styled{tuist-home}
\single-page

# tuist {#index}

A component-based TUI framework for Go with cached rendering, line-level
diffing, and a single UI goroutine.

\header-links{
  [GitHub](https://github.com/vito/tuist)
}{
  [pkg.go.dev](https://pkg.go.dev/github.com/vito/tuist)
}

\install{go get github.com/vito/tuist}

## Minimal example

Embed `Compo`, implement `Render`, call `Update()` when state changes.

```go
type Counter struct {
    tuist.Compo
    Count int
}

func (c *Counter) Render(ctx tuist.Context) tuist.RenderResult {
    return tuist.RenderResult{
        Lines: []string{fmt.Sprintf("Count: %d", c.Count)},
    }
}

func (c *Counter) HandleKeyPress(_ tuist.Context, ev uv.KeyPressEvent) bool {
    c.Count++
    c.Update()
    return true
}

// startup
term := tuist.NewStdTerminal()
tui  := tuist.New(term)
tui.Start()
tui.Dispatch(func() {
    counter := &Counter{}
    tui.AddChild(counter)
    tui.SetFocus(counter)
})
```

## How it works

Components form a tree. Each embeds `Compo` which tracks a generation
counter. `Update()` bumps the counter and propagates upward. On each
frame, the framework skips any subtree whose generation hasn't changed —
cached result returned in O(1).

The rendered output (a `[]string`, one per line) is diffed against the
previous frame. Only changed lines are written to the terminal, wrapped in
synchronized output (DEC 2026) to avoid flicker.

All component state lives on a single goroutine. The event loop drains input
events and `Dispatch()` closures, coalesces them, then renders once:

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

## Interfaces

Only `Render` is required. Everything else is opt-in:

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

## Composition

`RenderChild` is how you compose components. It wires up the parent
pointer, handles mount/dismount lifecycle, and wraps `MouseEnabled`
children in zone markers for positional dispatch. Always use it instead of
calling `child.Render()` directly.

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

Children rendered via `RenderChild` that are no longer rendered on a
subsequent frame are automatically dismounted (orphan cleanup).

## Concurrency

Components don't need locks. All field access happens on the UI goroutine.
Background goroutines push mutations via `Dispatch`:

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

## Overlays

Overlays composite a component on top of the base content at column-level
precision. Positioning can be viewport-anchored, content-relative, or
cursor-relative.

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

Focus is not changed automatically — you call `SetFocus` to direct
input to the overlay component.

## Built-in components

* `Container` — renders children sequentially (vertical stack).
  `AddChild`, `RemoveChild`, `Clear`.

* `Slot` — holds one replaceable child. `Set(child)` swaps it; old
  child is dismounted automatically.

*  `TextInput` — single/multiline text editor with cursor, prompt,
  word/subword movement, kill-line, ghost suggestions, paste support, and a
  `KeyInterceptor` hook.

* `Spinner` — animated braille spinner. Starts on mount, stops on
  dismount. Configurable `Style` and `Label`.

* `CompletionMenu` — dropdown autocomplete wired to a `TextInput`.
  Takes a `CompletionProvider`, manages overlay lifecycle, handles
  keyboard nav, and shows a detail panel. Cursor-group-aware.

## Mouse

Implement `MouseEnabled` on a component and the framework handles the
rest. When a `MouseEnabled` component is mounted, terminal mouse
reporting is enabled (ref-counted). Zone markers (zero-width CSI sequences)
are injected around the component's rendered output; after each frame, the
markers are scanned to build a hit map. Mouse events are dispatched to the
deepest zone under the cursor with component-relative coordinates.

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

Inline children rendered via `RenderChildInline` also get zone
markers — you can have clickable spans within a single line.

## Demos

```bash
# interactive selector
$ go run github.com/vito/tuist/demos@latest

# or run one directly
$ go run github.com/vito/tuist/demos@latest keygen   # mandelbrot fractal, ~165fps, inline editors
$ go run github.com/vito/tuist/demos@latest grid     # mouse hover/click grid, keyboard nav
$ go run github.com/vito/tuist/demos@latest logs     # scrollback stress test, overlays, spinner
```

## Inspiration

* [Go-app](https://go-app.dev) — component system, lifecycle hooks, UI goroutine model
* [pi-tui](https://github.com/badlogic/pi-mono/tree/main/packages/tui) — repaintable scrollback approach; tuist started as a conversion of this
* [BubbleZone](https://github.com/lrstanley/bubblezone) — mouse region markers trick
* [Bubbletea](https://github.com/charmbracelet/bubbletea) — great TUI framework, different model. tuist uses [Lipgloss](https://github.com/charmbracelet/lipgloss) and [Ultraviolet](https://github.com/charmbracelet/ultraviolet) from its ecosystem.
