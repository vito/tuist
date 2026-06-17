package tuist

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markStyle wraps text in zero-width markers so tests can assert which runes
// were styled without affecting visible width.
func markStyle(s string) string { return "<" + s + ">" }

func TestTextInputHighlightStylesSpan(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("foo bar")
	ti.SetFocused(Context{}, true)
	// Highlight "bar" (runes 4..7).
	ti.Highlight = func(value string) []StyleSpan {
		return []StyleSpan{{Start: 4, End: 7, Style: markStyle}}
	}

	ctx := Context{Context: context.Background(), Width: 80}
	result := renderComponent(ti, ctx)

	require.Len(t, result.Lines, 1)
	assert.Equal(t, "> foo <bar>", result.Lines[0])
	// Cursor column is computed from raw runes, unaffected by styling.
	require.NotNil(t, result.Cursor)
	assert.Equal(t, 9, result.Cursor.Col) // "> " (2) + "foo bar" (7)
}

func TestTextInputHighlightSurvivesWrap(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("hello world")
	ti.SetFocused(Context{}, true)
	// Highlight the whole value; it must be styled on every wrapped segment.
	ti.Highlight = func(value string) []StyleSpan {
		return []StyleSpan{{Start: 0, End: len([]rune(value)), Style: markStyle}}
	}

	ctx := Context{Context: context.Background(), Width: 10} // prompt 2, avail 8
	result := renderComponent(ti, ctx)

	require.Equal(t, 2, len(result.Lines), "lines: %v", result.Lines)
	// "hello " on line 0, "world" on line 1 — each segment styled independently.
	assert.Equal(t, "> <hello >", result.Lines[0])
	assert.Equal(t, "  <world>", result.Lines[1])
}

func TestTextInputHighlightMultipleSpans(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("ab cd")
	ti.SetFocused(Context{}, true)
	ti.Highlight = func(value string) []StyleSpan {
		return []StyleSpan{
			{Start: 0, End: 2, Style: func(s string) string { return "[" + s + "]" }},
			{Start: 3, End: 5, Style: markStyle},
		}
	}

	ctx := Context{Context: context.Background(), Width: 80}
	result := renderComponent(ti, ctx)

	require.Len(t, result.Lines, 1)
	assert.Equal(t, "> [ab] <cd>", result.Lines[0])
}

func TestTextInputNoHighlightUnchanged(t *testing.T) {
	ti := NewTextInput("> ")
	ti.SetValue("plain")
	ti.SetFocused(Context{}, true)

	ctx := Context{Context: context.Background(), Width: 80}
	result := renderComponent(ti, ctx)

	require.Len(t, result.Lines, 1)
	assert.Equal(t, "> plain", result.Lines[0])
	assert.False(t, strings.Contains(result.Lines[0], "<"))
}
