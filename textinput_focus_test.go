package tuist

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frameCursor renders a frame at the terminal's size and returns the
// cursor position the component tree reported, if any.
func frameCursor(tui *TUI) *CursorPos {
	var stats RenderStats
	_, cursor, _ := tui.renderFrame(tui.terminal.Columns(), tui.terminal.Rows(), &stats)
	return cursor
}

// A TextInput whose render is cached must drop its cursor when it loses
// focus and regain it when refocused, even if nothing else about it
// changed.
func TestTextInputFocusChangeInvalidatesCache(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	ti := NewTextInput("> ")
	ti.SetValue("hi")
	other := &focusProbe{log: &log}
	tui.AddChild(ti)
	tui.AddChild(other)
	tui.SetFocus(ti)
	tui.Step()

	cursor := frameCursor(tui)
	require.NotNil(t, cursor, "focused input should report a cursor")
	assert.Equal(t, 0, cursor.Row)
	assert.Equal(t, 4, cursor.Col) // "> " (2) + "hi" (2)

	tui.SetFocus(other)
	assert.Nil(t, frameCursor(tui), "blurred input kept a stale cursor")

	tui.SetFocus(ti)
	cursor = frameCursor(tui)
	require.NotNil(t, cursor, "refocused input should report a cursor")
	assert.Equal(t, 4, cursor.Col)
}

func TestTextInputSetFocusedUpdatesOnlyOnChange(t *testing.T) {
	ti := NewTextInput("> ")
	gen := ti.generation.Load()

	ti.SetFocused(Context{}, false)
	assert.Equal(t, gen, ti.generation.Load(), "no-op blur requested a render")

	ti.SetFocused(Context{}, true)
	assert.Greater(t, ti.generation.Load(), gen)

	gen = ti.generation.Load()
	ti.SetFocused(Context{}, true)
	assert.Equal(t, gen, ti.generation.Load(), "no-op focus requested a render")
}

// focusFrame decorates a TextInput child based on whether it owns the
// keyboard, the way a prompt frame might show a focus cue.
type focusFrame struct {
	Compo
	tui     *TUI
	input   *TextInput
	renders int
}

func (f *focusFrame) Render(ctx Context) {
	f.renders++
	if f.tui.IsFocused(f.input) {
		ctx.Line("[focused]")
	} else {
		ctx.Line("[idle]")
	}
	f.RenderChild(ctx, f.input)
}

func TestTextInputFocusRerendersWrapper(t *testing.T) {
	var log []string
	tui := newFocusTUI()
	ti := NewTextInput("> ")
	frame := &focusFrame{tui: tui, input: ti}
	other := &focusProbe{log: &log}
	tui.AddChild(frame)
	tui.AddChild(other)

	lines := tui.Step()
	require.NotEmpty(t, lines)
	assert.Equal(t, "[idle]", lines[0])

	tui.SetFocus(ti)
	lines = tui.Step()
	assert.Equal(t, "[focused]", lines[0])
	cursor := frameCursor(tui)
	require.NotNil(t, cursor)
	assert.Equal(t, 1, cursor.Row) // below the frame's header line

	tui.SetFocus(other)
	lines = tui.Step()
	assert.Equal(t, "[idle]", lines[0])
	assert.Nil(t, frameCursor(tui))

	// Nothing changed: the wrapper stays cached.
	renders := frame.renders
	tui.Step()
	assert.Equal(t, renders, frame.renders, "wrapper re-rendered without a change")
}
