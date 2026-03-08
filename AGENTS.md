# tuist

Go TUI framework (`github.com/vito/tuist`). Not the Swift build tool.

- Language: Go
- Module: `github.com/vito/tuist`
- Rendering: differential terminal rendering on the normal scrollback buffer
- Architecture: single UI goroutine processes input, dispatches, and renders; components never need locks
- Key files:
  - `tui.go` — main TUI renderer, event loop, differential rendering
  - `component.go` — Compo, Container, Slot, lifecycle, render caching
  - `terminal.go` — Terminal interface
  - `overlay.go` — overlay compositing
  - `spinner.go`, `textinput.go` — built-in components
- Demos: `demos/` (consolidated launcher), `teav1/demo/`, `teav2/demo/`
- Debug UI: `internal/debugui/` — web dashboard that tails JSONL render stats
- Debug logging: `TUI.SetDebugWriter(w)` writes per-frame JSONL stats; `TUIST_LOG` env var auto-configures it globally
