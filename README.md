# tuist

A high-performance TUI framework for Go.

## Demos

```sh
go run github.com/vito/tuist/demos@latest
```

## The Idea

* Everything is a component, embedding `tui.Compo`
* Components have lifecycle hooks (`OnMount`, `OnDismount`)
* Components can be hovered and focused (`SetHovered`, `SetFocused`)
* Components are fully interactive (`HandleKeyPress`, `HandlePaste`, `HandleMouse`)
* Components render to text, with an optional cursor position
* Components renders are cached, and only re-render when `Compo.Update` is called
* Output is diffed against previous frame and only changed lines are repainted
* If content changes off-screen, a full (synchronized) repaint is required (trade-off)

## Inspiration

* [Go-app](https://go-app.dev) - component system, lifecycle hooks, UI goroutine model
* [pi-tui][pi-tui] - the approach for repaintable scrollback; this project started as a straight-up conversion.
* [BubbleZone](https://github.com/lrstanley/bubblezone) - for the mouse region markers trick
* [Bubbletea](https://github.com/charmbracelet/bubbletea) - a great TUI framework, I just needed a different model. This project leverages various components from its ecosystem ([Lipgloss](https://github.com/charmbracelet/lipgloss), [Ultraviolet](https://github.com/charmbracelet/ultraviolet)).

## LLM usage

Used it lots. It wrote the commits, but not this README.

[pi-tui]: https://github.com/badlogic/pi-mono/tree/main/packages/tui
