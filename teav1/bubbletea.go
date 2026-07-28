// Package teav1 adapts bubbletea v1 models for use as tuist components.
package teav1

import (
	"reflect"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
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
// unexported name changing. Same trick for tea.WindowSize's internal size
// request. TestSequenceTypeLearned re-verifies both against whatever
// bubbletea version is actually linked.
var (
	teaNoop           tea.Cmd = func() tea.Msg { return nil }
	sequenceMsgType           = reflect.TypeOf(tea.Sequence(teaNoop, teaNoop)())
	windowSizeReqType         = reflect.TypeOf(tea.WindowSize()())
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

// Wrap wraps a bubbletea v1 model as a tuist Component. It bridges
// the two frameworks:
//
//   - Render calls the model's View() and splits into lines
//   - HandleKeyPress forwards decoded key events as tea.KeyMsg
//   - Width changes are delivered as tea.WindowSizeMsg
//   - Commands returned by Init/Update are executed asynchronously
//     and their resulting messages are fed back through Update
//
// Usage:
//
//	m := myModel{...}
//	comp := teav1.New(m)
//	tui.AddChild(comp)
type Wrap struct {
	tuist.Compo
	model    tea.Model
	width    int
	height   int
	onQuit   func()
	dispatch func(func()) // schedules work on the UI goroutine; see ready
	inited   bool         // model.Init() has run
	pending  []tea.Cmd    // commands issued before a dispatcher was available
}

// New wraps a bubbletea v1 model as a tuist Component.
// The model's Init() is called before the model sees anything else — a
// render, a key, or a paste — whichever comes first.
func New(model tea.Model) *Wrap {
	b := &Wrap{model: model}
	b.Update()
	return b
}

// OnQuit sets a callback invoked when the bubbletea model returns a
// tea.Quit command.
func (b *Wrap) OnQuit(fn func()) {
	b.onQuit = fn
}

// ready captures the dispatch function and runs the model's Init(), each
// exactly once. OnMount and the input callbacks all call it first.
//
// It cannot live in OnMount alone. Components mount lazily, on the first
// render after being added, but [TUI.SetFocus] can hand a component focus the
// moment it is added — so a key press buffered by the terminal is delivered
// before the mount. Initializing here means the model is always initialized
// before it sees a message, and a dispatcher is always in place before a
// command runs, so no type-ahead is silently dropped.
//
// Always runs on the UI goroutine.
func (b *Wrap) ready(ctx tuist.Context) {
	if b.dispatch != nil && b.inited {
		return
	}
	if b.dispatch == nil {
		b.dispatch = ctx.Dispatch
	}
	if !b.inited {
		b.inited = true
		if cmd := b.model.Init(); cmd != nil {
			b.execCmd(cmd)
		}
	}
	b.flushPending()
}

// OnMount readies the component when it enters the tree, for the common case
// where nothing reached it before its first render.
func (b *Wrap) OnMount(ctx tuist.Context) {
	b.ready(ctx)
}

// Model returns the underlying bubbletea v1 model.
func (b *Wrap) Model() tea.Model {
	return b.model
}

// SendMsg sends a message to the bubbletea model's Update function.
// Must be called from the UI goroutine.
func (b *Wrap) SendMsg(msg tea.Msg) {
	b.updateModel(msg)
}

func (b *Wrap) updateModel(msg tea.Msg) {
	var cmd tea.Cmd
	b.model, cmd = b.model.Update(msg)
	b.Update()
	if cmd != nil {
		b.execCmd(cmd)
	}
}

func (b *Wrap) execCmd(cmd tea.Cmd) {
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
//   - a size request (tea.WindowSize) is answered with the last rendered
//     size; before the first render it's dropped, since that render sends
//     its own tea.WindowSizeMsg anyway
//   - anything else goes to the model's Update on the UI goroutine
//
// One deviation: bubbletea stops delivering once quit is processed, whereas
// commands after a QuitMsg here still run and deliver — updates to a
// dismounted component are harmless no-ops.
//
// Runs off the UI goroutine.
func (b *Wrap) deliver(dispatch func(func()), msg tea.Msg) {
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
func (b *Wrap) flushPending() {
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
func (b *Wrap) Render(ctx tuist.Context) {
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
func (b *Wrap) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	b.ready(ctx)
	b.updateModel(uvKeyToV1(uv.Key(ev)))
	return true // bubbletea models consume all key events
}

// HandlePaste implements tuist.Pasteable. Pasted text is delivered as
// individual rune key events to the bubbletea model, matching the
// behavior bubbletea v1 would exhibit without bracketed paste mode.
func (b *Wrap) HandlePaste(ctx tuist.Context, ev uv.PasteEvent) bool {
	b.ready(ctx)
	for _, r := range ev.Content {
		b.updateModel(tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{r},
		})
	}
	return true
}

// uvKeyToV1 converts an ultraviolet Key to a bubbletea v1 KeyMsg.
func uvKeyToV1(k uv.Key) tea.KeyMsg {
	alt := k.Mod.Contains(uv.ModAlt)
	ctrl := k.Mod.Contains(uv.ModCtrl)
	shift := k.Mod.Contains(uv.ModShift)

	// Printable text (no ctrl modifier).
	if k.Text != "" && !ctrl {
		return tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune(k.Text),
			Alt:   alt,
		}
	}

	// Map special keys.
	keyType, ok := uvToV1Key[k.Code]
	if ok {
		// Apply modifier variants for arrow keys and nav keys.
		if shifted, ok := shiftedKey(keyType, ctrl, shift); ok {
			return tea.KeyMsg{Type: shifted, Alt: alt}
		}
		return tea.KeyMsg{Type: keyType, Alt: alt}
	}

	// Ctrl+letter: bubbletea v1 maps ctrl+a to KeyCtrlA (0x01), etc.
	if ctrl && k.Code >= 'a' && k.Code <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(k.Code - 'a' + 1), Alt: alt}
	}

	// Printable rune fallback.
	if k.Code >= 0x20 {
		return tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{k.Code},
			Alt:   alt,
		}
	}

	return tea.KeyMsg{Type: tea.KeyRunes}
}

var uvToV1Key = map[rune]tea.KeyType{
	uv.KeyUp:        tea.KeyUp,
	uv.KeyDown:      tea.KeyDown,
	uv.KeyLeft:      tea.KeyLeft,
	uv.KeyRight:     tea.KeyRight,
	uv.KeyHome:      tea.KeyHome,
	uv.KeyEnd:       tea.KeyEnd,
	uv.KeyPgUp:      tea.KeyPgUp,
	uv.KeyPgDown:    tea.KeyPgDown,
	uv.KeyDelete:    tea.KeyDelete,
	uv.KeyInsert:    tea.KeyInsert,
	uv.KeyTab:       tea.KeyTab,
	uv.KeyBackspace: tea.KeyBackspace,
	uv.KeyEnter:     tea.KeyEnter,
	uv.KeyEscape:    tea.KeyEscape,
	uv.KeySpace:     tea.KeySpace,
	uv.KeyF1:        tea.KeyF1,
	uv.KeyF2:        tea.KeyF2,
	uv.KeyF3:        tea.KeyF3,
	uv.KeyF4:        tea.KeyF4,
	uv.KeyF5:        tea.KeyF5,
	uv.KeyF6:        tea.KeyF6,
	uv.KeyF7:        tea.KeyF7,
	uv.KeyF8:        tea.KeyF8,
	uv.KeyF9:        tea.KeyF9,
	uv.KeyF10:       tea.KeyF10,
	uv.KeyF11:       tea.KeyF11,
	uv.KeyF12:       tea.KeyF12,
}

// shiftedKey returns the ctrl/shift variant of a base key type, if one exists
// in bubbletea v1's key model.
func shiftedKey(base tea.KeyType, ctrl, shift bool) (tea.KeyType, bool) {
	switch {
	case ctrl && shift:
		if k, ok := ctrlShiftKeys[base]; ok {
			return k, true
		}
	case ctrl:
		if k, ok := ctrlKeys[base]; ok {
			return k, true
		}
	case shift:
		if k, ok := shiftKeys[base]; ok {
			return k, true
		}
	}
	return 0, false
}

var ctrlKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:     tea.KeyCtrlUp,
	tea.KeyDown:   tea.KeyCtrlDown,
	tea.KeyLeft:   tea.KeyCtrlLeft,
	tea.KeyRight:  tea.KeyCtrlRight,
	tea.KeyHome:   tea.KeyCtrlHome,
	tea.KeyEnd:    tea.KeyCtrlEnd,
	tea.KeyPgUp:   tea.KeyCtrlPgUp,
	tea.KeyPgDown: tea.KeyCtrlPgDown,
}

var shiftKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:    tea.KeyShiftUp,
	tea.KeyDown:  tea.KeyShiftDown,
	tea.KeyLeft:  tea.KeyShiftLeft,
	tea.KeyRight: tea.KeyShiftRight,
	tea.KeyHome:  tea.KeyShiftHome,
	tea.KeyEnd:   tea.KeyShiftEnd,
	tea.KeyTab:   tea.KeyShiftTab,
}

var ctrlShiftKeys = map[tea.KeyType]tea.KeyType{
	tea.KeyUp:    tea.KeyCtrlShiftUp,
	tea.KeyDown:  tea.KeyCtrlShiftDown,
	tea.KeyLeft:  tea.KeyCtrlShiftLeft,
	tea.KeyRight: tea.KeyCtrlShiftRight,
	tea.KeyHome:  tea.KeyCtrlShiftHome,
	tea.KeyEnd:   tea.KeyCtrlShiftEnd,
}
