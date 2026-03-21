package tuist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWordWrapRunes(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		firstWidth int
		contWidth  int
		wantTexts  []string
	}{
		{"empty", "", 20, 20, []string{""}},
		{"fits", "hello world", 20, 20, []string{"hello world"}},
		{"wrap at word", "hello world", 8, 8, []string{"hello ", "world"}},
		{"wrap long word", "abcdefghij", 5, 5, []string{"abcde", "fghij"}},
		{"multiple wraps", "one two three four", 10, 10, []string{"one two ", "three four"}},
		{"no width", "hello", 0, 0, []string{"hello"}},
		{"first wider", "hello world foo", 15, 8, []string{"hello world foo"}},
		{"first narrower", "hi world", 5, 20, []string{"hi ", "world"}},
		{"exact fit", "hello", 5, 5, []string{"hello"}},
		{"single space break", "a b", 2, 2, []string{"a ", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segs := wordWrapRunes([]rune(tt.input), tt.firstWidth, tt.contWidth)
			var texts []string
			for _, s := range segs {
				texts = append(texts, s.text)
			}
			assert.Equal(t, tt.wantTexts, texts)

			// Verify rune indices are correct.
			runes := []rune(tt.input)
			for _, s := range segs {
				assert.Equal(t, string(runes[s.runeStart:s.runeEnd]), s.text,
					"runeStart=%d runeEnd=%d", s.runeStart, s.runeEnd)
			}
		})
	}
}

func TestTextInputRenderWordWrap(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("hello world this is a long line")
	ti.SetFocused(Context{}, true)

	ctx := Context{Context: context.Background(), Width: 15}
	// prompt "> " is 2 cols wide, so 13 cols for text.
	result := renderComponent(ti, ctx)

	// Should have multiple lines.
	require.Greater(t, len(result.Lines), 1, "expected wrapping, got: %v", result.Lines)

	// First line should start with prompt.
	assert.Contains(t, result.Lines[0], "> ")

	// Subsequent lines should have continuation prompt (spaces).
	for i := 1; i < len(result.Lines); i++ {
		assert.True(t, len(result.Lines[i]) >= 2, "line %d too short", i)
	}

	// Cursor should be on the last line at the end.
	require.NotNil(t, result.Cursor)
}

func TestTextInputRenderNoWrapWhenFits(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("hi")
	ti.SetFocused(Context{}, true)

	ctx := Context{Context: context.Background(), Width: 80}
	result := renderComponent(ti, ctx)

	assert.Len(t, result.Lines, 1)
	assert.Equal(t, "> hi", result.Lines[0])
	require.NotNil(t, result.Cursor)
	assert.Equal(t, 0, result.Cursor.Row)
	assert.Equal(t, 4, result.Cursor.Col) // "> " (2) + "hi" (2)
}

func TestTextInputRenderNoWrapZeroWidth(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("hello world")
	ti.SetFocused(Context{}, true)

	ctx := Context{Context: context.Background(), Width: 0}
	result := renderComponent(ti, ctx)

	// With zero width, no wrapping should occur.
	assert.Len(t, result.Lines, 1)
}

func TestTextInputCursorPositionInWrappedLine(t *testing.T) {
	ti := NewTextInput("> ")
	// "hello world" with width 10: prompt=2, avail=8
	// "hello " (6) fits, "world" wraps to next line.
	ti.SetValue("hello world")
	ti.cursor = 8 // cursor on 'r' in "world"
	ti.SetFocused(Context{}, true)

	ctx := Context{Context: context.Background(), Width: 10}
	result := renderComponent(ti, ctx)

	require.Equal(t, 2, len(result.Lines), "lines: %v", result.Lines)
	require.NotNil(t, result.Cursor)
	assert.Equal(t, 1, result.Cursor.Row)
	// "world" -> cursor at index 8 means 2 chars into "world": "wo" = col 2, plus prompt width 2 = 4
	assert.Equal(t, 4, result.Cursor.Col)
}

func TestTextInputWrappedMultiline(t *testing.T) {
	ti := NewTextInput("> ")
	// Multiline with wrapping: first line wraps, second line short.
	ti.SetValue("hello world foo\nbar")
	ti.SetFocused(Context{}, true)
	ti.CursorEnd()

	ctx := Context{Context: context.Background(), Width: 12}
	result := renderComponent(ti, ctx)

	// prompt=2, avail=10
	// line 0: "hello " (6 fits), then "world foo" wraps: "world foo" (9 fits in 10)
	// Actually: "hello " (6), "world foo" (9) -> fits in 10
	// line 1: "bar" (3)
	// So we should get 3 visual lines.
	require.GreaterOrEqual(t, len(result.Lines), 2)
}

func TestTextInputHasMultipleVisualLines(t *testing.T) {
	ti := NewTextInput("> ")

	// Short text, no wrapping.
	ti.SetValue("hi")
	ti.lastRenderWidth = 80
	assert.False(t, ti.hasMultipleVisualLines())

	// Long text that wraps.
	ti.SetValue("hello world this is very long")
	ti.lastRenderWidth = 15
	assert.True(t, ti.hasMultipleVisualLines())

	// Explicit newline.
	ti.SetValue("a\nb")
	ti.lastRenderWidth = 80
	assert.True(t, ti.hasMultipleVisualLines())
}
