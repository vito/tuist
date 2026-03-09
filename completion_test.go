package tuist

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletionDisplayLabel(t *testing.T) {
	c := Completion{Label: "foo"}
	assert.Equal(t, "foo", c.displayLabel())

	c.DisplayLabel = "foo: String"
	assert.Equal(t, "foo: String", c.displayLabel())
}

func TestCompletionInsertText(t *testing.T) {
	c := Completion{Label: "foo"}
	assert.Equal(t, "foo", c.insertText())

	c.InsertText = "foo: "
	assert.Equal(t, "foo: ", c.insertText())
}

func TestNewCompletionMenu(t *testing.T) {
	ti := NewTextInput("test> ")
	provider := func(input string, cursor int) CompletionResult {
		return CompletionResult{}
	}
	m := NewCompletionMenu(ti, provider)
	assert.NotNil(t, m)
	assert.Equal(t, 8, m.MaxVisible)
	assert.False(t, m.Visible())
	assert.NotNil(t, m.cursorGroup)
}

func TestCompletionMenuHide(t *testing.T) {
	ti := NewTextInput("test> ")
	m := NewCompletionMenu(ti, nil)
	m.visible = true
	m.items = []Completion{{Label: "foo"}}
	m.index = 1

	m.Hide()
	assert.False(t, m.Visible())
	assert.Nil(t, m.items)
	assert.Equal(t, 0, m.index)
}

func TestDefaultDetailRenderer(t *testing.T) {
	title := defaultTitleStyle()
	dim := defaultDimStyle()

	t.Run("empty", func(t *testing.T) {
		c := Completion{Label: "foo"}
		lines := defaultDetailRenderer(c, 40, title, dim)
		assert.Nil(t, lines)
	})

	t.Run("detail only", func(t *testing.T) {
		c := Completion{Label: "foo", Detail: "String"}
		lines := defaultDetailRenderer(c, 40, title, dim)
		require.Len(t, lines, 2)
	})

	t.Run("detail and doc", func(t *testing.T) {
		c := Completion{Label: "foo", Detail: "String", Documentation: "A foo thing."}
		lines := defaultDetailRenderer(c, 40, title, dim)
		require.Len(t, lines, 4) // title, detail, blank, doc
	})

	t.Run("multiline doc", func(t *testing.T) {
		c := Completion{Label: "foo", Documentation: "Line 1\nLine 2"}
		lines := defaultDetailRenderer(c, 40, title, dim)
		require.Len(t, lines, 4) // title, blank, line1, line2
	})
}

func TestCompletionMenuOverlayRender(t *testing.T) {
	items := []Completion{
		{Label: "alpha"},
		{Label: "bravo"},
		{Label: "charlie"},
	}
	overlay := &completionMenuOverlay{
		items:         items,
		index:         1,
		maxVisible:    10,
		style:         defaultMenuStyle(),
		selectedStyle: defaultSelectedStyle(),
		borderStyle:   defaultBorderStyle(),
		dimStyle:      defaultDimStyle(),
	}
	result := overlay.Render(Context{Width: 80, Height: 24})
	assert.NotEmpty(t, result.Lines)
}

func TestCompletionDetailOverlayRender(t *testing.T) {
	overlay := &completionDetailOverlay{
		lines:       []string{"Title", "detail"},
		borderStyle: defaultBorderStyle(),
	}
	result := overlay.Render(Context{Width: 40, Height: 20})
	assert.NotEmpty(t, result.Lines)
}

func TestCompletionDetailOverlayEmpty(t *testing.T) {
	overlay := &completionDetailOverlay{
		borderStyle: defaultBorderStyle(),
	}
	result := overlay.Render(Context{Width: 40, Height: 20})
	assert.Empty(t, result.Lines)
}

// helpers to create default styles for tests
func defaultMenuStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237"))
}
func defaultSelectedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("63")).Bold(true)
}
func defaultBorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63"))
}
func defaultTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
}
func defaultDimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
}
