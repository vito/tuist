// Package teav2 adapts bubbletea v2 models for use as tuist components.
package teav2

import (
	"reflect"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/tuist"
)

// bubbletea's Batch and Sequence produce runtime-internal messages that the
// real event loop unrolls itself — a model never sees them, so handing them
// to Update silently drops them. BatchMsg is exported, but sequenceMsg is
// not, so it can't be matched with a type assertion. It can, however, be
// learned from the public API: tea.Sequence with two non-nil commands must
// return one (compactCmds returns a single command directly, and a slice
// otherwise). Matching on the learned reflect.Type is exact and survives the
// unexported name changing. Same trick for tea.RequestWindowSize's internal
// size request.
var (
	teaNoop           tea.Cmd = func() tea.Msg { return nil }
	sequenceMsgType           = reflect.TypeOf(tea.Sequence(teaNoop, teaNoop)())
	windowSizeReqType         = reflect.TypeOf(tea.RequestWindowSize())
	cmdSliceType              = reflect.TypeOf([]tea.Cmd(nil))
)

// asSequence unwraps bubbletea's internal sequenceMsg. Its underlying type
// is []Cmd, so a reflect conversion recovers the commands without access to
// the unexported type itself.
func asSequence(msg tea.Msg) ([]tea.Cmd, bool) {
	if reflect.TypeOf(msg) != sequenceMsgType {
		return nil, false
	}
	return reflect.ValueOf(msg).Convert(cmdSliceType).Interface().([]tea.Cmd), true
}

// Model is the interface for bubbletea v2 models that can be wrapped
// as tuist components. It matches the common pattern used by bubbles
// (list, table, viewport, etc.) where Update returns the concrete
// type and View returns a string.
type Model[T any] interface {
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}

// Wrap wraps a bubbletea v2 model as a tuist Component. It bridges
// the two frameworks:
//
//   - Render calls the model's View() and splits into lines
//   - HandleKeyPress forwards decoded key events as tea.KeyPressMsg
//   - Width changes are delivered as tea.WindowSizeMsg
//   - Commands returned by Update are executed asynchronously and
//     their resulting messages are fed back through Update
//
// Usage:
//
//	items := []list.Item{...}
//	delegate := list.NewDefaultDelegate()
//	m := list.New(items, delegate, 80, 20)
//	comp := teav2.Wrap(m)
//	tui.AddChild(comp)
type Wrap[T Model[T]] struct {
	tuist.Compo
	model    T
	width    int
	height   int
	onQuit   func()
	dispatch func(func()) // schedules work on the UI goroutine; see ready
	pending  []tea.Cmd    // commands issued before a dispatcher was available
}

// New wraps a bubbletea v2 model as a tuist Component.
func New[T Model[T]](model T) *Wrap[T] {
	b := &Wrap[T]{model: model}
	b.Update()
	return b
}

// OnQuit sets a callback invoked when the bubbletea model returns a
// tea.QuitMsg. This lets the host application handle quit requests
// (e.g. close an overlay).
func (b *Wrap[T]) OnQuit(fn func()) {
	b.onQuit = fn
}

// ready captures the dispatch function, exactly once. OnMount and the input
// callbacks all call it first.
//
// It cannot live in OnMount alone. Components mount lazily, on the first
// render after being added, but [TUI.SetFocus] can hand a component focus the
// moment it is added — so a key press buffered by the terminal is delivered
// before the mount. Capturing here means a dispatcher is always in place
// before a command runs, so no type-ahead is silently dropped.
//
// Always runs on the UI goroutine.
func (b *Wrap[T]) ready(ctx tuist.Context) {
	if b.dispatch != nil {
		return
	}
	b.dispatch = ctx.Dispatch
	b.flushPending()
}

// OnMount readies the component when it enters the tree, for the common case
// where nothing reached it before its first render.
func (b *Wrap[T]) OnMount(ctx tuist.Context) {
	b.ready(ctx)
}

// Model returns the underlying bubbletea model.
func (b *Wrap[T]) Model() T {
	return b.model
}

// SendMsg sends a message to the bubbletea model's Update function,
// as if it came from a command. Useful for programmatic control.
// Must be called from the UI goroutine.
func (b *Wrap[T]) SendMsg(msg tea.Msg) {
	b.updateModel(msg)
}

func (b *Wrap[T]) updateModel(msg tea.Msg) {
	var cmd tea.Cmd
	b.model, cmd = b.model.Update(msg)
	b.Update()
	if cmd != nil {
		b.execCmd(cmd)
	}
}

func (b *Wrap[T]) execCmd(cmd tea.Cmd) {
	// Read the dispatcher here, on the UI goroutine: reading b.dispatch from
	// the command goroutine would race with ready's write.
	dispatch := b.dispatch
	if dispatch == nil {
		// Nowhere to schedule the result yet (SendMsg before the component was
		// ever mounted or focused). Hold the command until ready supplies a
		// dispatcher rather than running it and dropping its message.
		b.pending = append(b.pending, cmd)
		return
	}
	go func() {
		b.deliver(dispatch, cmd())
	}()
}

// deliver routes a command's resulting message the way bubbletea's own event
// loop (execBatchMsg/execSequenceMsg) would, instead of handing
// runtime-internal messages to the model:
//
//   - tea.QuitMsg fires OnQuit
//   - tea.BatchMsg runs its commands concurrently and waits for them — the
//     wait is what makes a batch nested in a sequence block the sequence,
//     matching bubbletea; at the top level it's unobservable
//   - sequenceMsg (tea.Sequence) runs its commands one at a time, in order,
//     recursing so nested batches and sequences complete before it continues
//   - a size request (tea.RequestWindowSize) is answered with the last
//     rendered size; before the first render it's dropped, since that render
//     sends its own tea.WindowSizeMsg anyway
//   - anything else goes to the model's Update on the UI goroutine
//
// One deviation: bubbletea stops delivering once quit is processed, whereas
// commands after a QuitMsg here still run and deliver — updates to a
// dismounted component are harmless no-ops.
//
// Runs off the UI goroutine.
func (b *Wrap[T]) deliver(dispatch func(func()), msg tea.Msg) {
	if msg == nil {
		return
	}
	if _, ok := msg.(tea.QuitMsg); ok {
		dispatch(func() {
			if b.onQuit != nil {
				b.onQuit()
			}
		})
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var wg sync.WaitGroup
		for _, cmd := range batch {
			if cmd == nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				b.deliver(dispatch, cmd())
			}()
		}
		wg.Wait()
		return
	}
	if cmds, ok := asSequence(msg); ok {
		for _, cmd := range cmds {
			if cmd == nil {
				continue
			}
			b.deliver(dispatch, cmd())
		}
		return
	}
	if reflect.TypeOf(msg) == windowSizeReqType {
		dispatch(func() {
			if b.width > 0 {
				b.updateModel(tea.WindowSizeMsg{Width: b.width, Height: b.height})
			}
		})
		return
	}
	dispatch(func() {
		b.updateModel(msg)
	})
}

// flushPending runs commands that were issued before a dispatcher existed, in
// the order they were issued. Runs on the UI goroutine.
func (b *Wrap[T]) flushPending() {
	if len(b.pending) == 0 {
		return
	}
	cmds := b.pending
	b.pending = nil
	for _, cmd := range cmds {
		b.execCmd(cmd)
	}
}

// Render implements tuist.Component.
//
// It deliberately does not call ready: renderChild mounts before it renders,
// so OnMount has always run by now, and a render Context is the one Context
// that can carry a nil TUI (a child rendered under an unmounted parent) — its
// Dispatch would panic when a command later tried to use it.
func (b *Wrap[T]) Render(ctx tuist.Context) {
	if ctx.Width != b.width || ctx.ScreenHeight() != b.height {
		b.width = ctx.Width
		b.height = ctx.ScreenHeight()
		var cmd tea.Cmd
		b.model, cmd = b.model.Update(tea.WindowSizeMsg{
			Width:  ctx.Width,
			Height: ctx.ScreenHeight(),
		})
		if cmd != nil {
			b.execCmd(cmd)
		}
	}

	view := b.model.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	ctx.Lines(lines...)
}

// HandleKeyPress implements tuist.Interactive.
func (b *Wrap[T]) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	b.ready(ctx)
	b.updateModel(tea.KeyPressMsg(ev))
	return true // bubbletea models consume all key events
}
