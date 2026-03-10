package tuist

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Completion represents a single completion suggestion.
type Completion struct {
	// Label is the text inserted on accept.
	Label string

	// DisplayLabel overrides Label for rendering in the menu. If empty,
	// Label is shown. May contain ANSI escape codes for styling.
	DisplayLabel string

	// Detail is a short type signature or description shown alongside.
	// May contain ANSI escape codes for styling.
	Detail string

	// Documentation is a longer doc string for the detail panel.
	Documentation string

	// Kind is a free-form string describing the completion type (e.g.
	// "function", "keyword", "field"). May contain ANSI escape codes
	// for styling. The framework does not interpret this value — it is
	// purely for the consumer's use in DisplayLabel or DetailRenderer.
	Kind string

	// InsertText overrides Label for the actual text inserted into the
	// input. For example, an argument completion might insert "name: "
	// while showing "name" as the label. If empty, Label is used.
	InsertText string
}

// displayLabel returns the text to show in the menu.
func (c Completion) displayLabel() string {
	if c.DisplayLabel != "" {
		return c.DisplayLabel
	}
	return c.Label
}

// insertText returns the text to insert into the input.
func (c Completion) insertText() string {
	if c.InsertText != "" {
		return c.InsertText
	}
	return c.Label
}

// CompletionResult is returned by a CompletionProvider.
type CompletionResult struct {
	// Items is the list of completion candidates.
	Items []Completion

	// ReplaceFrom is the byte offset in the input where the completed
	// token starts. The text from ReplaceFrom to the cursor is replaced
	// by the chosen completion's InsertText/Label.
	ReplaceFrom int
}

// CompletionProvider is called by CompletionMenu when the input changes.
// It receives the full input text and cursor byte position, and returns
// completion candidates. Return an empty/nil Items slice for no completions.
type CompletionProvider func(input string, cursorPos int) CompletionResult

// DetailRenderer renders the detail panel for a highlighted completion.
// It receives the completion and available width, and returns lines to
// display in the detail bubble. If nil, a default renderer shows
// Detail and Documentation.
type DetailRenderer func(c Completion, width int) []string

// ── CompletionMenu ─────────────────────────────────────────────────────────

// CompletionMenu manages an autocomplete dropdown and detail panel for a
// TextInput. It handles:
//   - Querying a CompletionProvider on input changes
//   - Showing/hiding the dropdown overlay
//   - Keyboard navigation (Up/Down/Escape)
//   - Setting the ghost suggestion on the TextInput
//   - Showing a detail panel for the highlighted item
//
// Usage:
//
//	ti := tuist.NewTextInput("prompt> ")
//	menu := tuist.NewCompletionMenu(ti, provider)
//	// Add menu as a child so it receives bubbled keys.
//	container.AddChild(ti)
//	container.AddChild(menu) // invisible; just handles events
//
// The CompletionMenu does not render any content itself — it manages
// overlays attached to the TextInput's cursor position.
type CompletionMenu struct {
	Compo

	// Provider generates completions. Must be set before use.
	Provider CompletionProvider

	// DetailRenderer renders the detail panel. If nil, a default
	// renderer is used that shows Detail and Documentation.
	DetailRenderer DetailRenderer

	// MaxVisible is the maximum number of items shown in the dropdown
	// before scrolling. Defaults to 8.
	MaxVisible int

	// Styles (all have sensible defaults).
	MenuStyle         lipgloss.Style
	MenuSelectedStyle lipgloss.Style
	MenuBorderStyle   lipgloss.Style
	DetailBorderStyle lipgloss.Style
	DetailTitleStyle  lipgloss.Style
	DimStyle          lipgloss.Style

	// input is the TextInput this menu is attached to.
	input *TextInput

	// state
	visible     bool
	items       []Completion   // current completion list
	replaceFrom int            // byte offset where the token starts
	index       int            // selected index within matches
	overlay     *completionMenuOverlay
	handle      *OverlayHandle
	detailComp  *completionDetailOverlay
	detailH     *OverlayHandle
	cursorGroup *CursorGroup
}

// NewCompletionMenu creates a CompletionMenu attached to the given TextInput.
func NewCompletionMenu(input *TextInput, provider CompletionProvider) *CompletionMenu {
	m := &CompletionMenu{
		Provider:   provider,
		input:      input,
		MaxVisible: 8,
		MenuStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("237")),
		MenuSelectedStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("63")).
			Bold(true),
		MenuBorderStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")),
		DetailBorderStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("241")),
		DetailTitleStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Bold(true),
		DimStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("241")),
		cursorGroup: NewCursorGroup(),
	}

	// Wire into the TextInput's OnChange.
	prevOnChange := input.OnChange
	input.OnChange = func(ctx Context) {
		if prevOnChange != nil {
			prevOnChange(ctx)
		}
		m.Refresh(ctx)
	}

	return m
}

// Refresh re-queries the provider and updates the menu. Call this when
// external state changes (e.g. new bindings become available) but the
// input text hasn't changed.
func (m *CompletionMenu) Refresh(ctx Context) {
	val := m.input.Value()
	if val == "" || m.Provider == nil {
		m.hide()
		m.input.Suggestion = ""
		ctx.RequestRender(false)
		return
	}

	result := m.Provider(val, len(val))
	m.items = result.Items
	m.replaceFrom = result.ReplaceFrom

	if len(m.items) == 0 {
		m.hide()
		m.input.Suggestion = ""
		ctx.RequestRender(false)
		return
	}

	// Build suggestion: replace from ReplaceFrom with first item's insert text.
	if len(m.items) > 0 {
		first := m.items[0]
		suggestion := val[:m.replaceFrom] + first.insertText()
		if suggestion != val {
			m.input.Suggestion = suggestion
		} else {
			m.input.Suggestion = ""
		}
	}

	// Single match with no more to show: just set suggestion, no dropdown.
	if len(m.items) == 1 {
		m.hide()
		if len(m.items) == 1 {
			m.showDetailFor(ctx, m.items[0])
		}
		ctx.RequestRender(false)
		return
	}

	m.visible = true
	if m.index >= len(m.items) {
		m.index = 0
	}
	m.showMenu(ctx)
}

// Visible reports whether the dropdown is currently showing.
func (m *CompletionMenu) Visible() bool { return m.visible }

// Hide dismisses the completion menu and detail panel.
func (m *CompletionMenu) Hide() { m.hide() }

func (m *CompletionMenu) hide() {
	m.visible = false
	m.items = nil
	m.index = 0
	if m.handle != nil {
		m.handle.Remove()
		m.handle = nil
		m.overlay = nil
	}
	m.hideDetail()
}

func (m *CompletionMenu) hideDetail() {
	if m.detailH != nil {
		m.detailH.Remove()
		m.detailH = nil
		m.detailComp = nil
	}
}

// HandleKeyPress intercepts Up/Down/Escape for menu navigation.
// Returns true if the key was consumed. Call this from a parent
// component's HandleKeyPress, or embed CompletionMenu as a child.
func (m *CompletionMenu) HandleKeyPress(ctx Context, ev uv.KeyPressEvent) bool {
	if !m.visible {
		return false
	}

	key := uv.Key(ev)
	switch {
	case key.Code == uv.KeyDown && key.Mod == 0,
		key.Code == 'n' && key.Mod == uv.ModCtrl:
		m.index++
		if m.index >= len(m.items) {
			m.index = 0
		}
		m.syncSelection(ctx)
		return true

	case key.Code == uv.KeyUp && key.Mod == 0,
		key.Code == 'p' && key.Mod == uv.ModCtrl:
		m.index--
		if m.index < 0 {
			m.index = len(m.items) - 1
		}
		m.syncSelection(ctx)
		return true

	case key.Code == uv.KeyEscape:
		m.hide()
		ctx.RequestRender(false)
		return true
	}

	return false
}

func (m *CompletionMenu) syncSelection(ctx Context) {
	// Update suggestion to selected item.
	if m.index >= 0 && m.index < len(m.items) {
		val := m.input.Value()
		sel := m.items[m.index]
		suggestion := val[:m.replaceFrom] + sel.insertText()
		if suggestion != val {
			m.input.Suggestion = suggestion
		} else {
			m.input.Suggestion = ""
		}
	}

	if m.overlay != nil {
		m.overlay.index = m.index
		m.overlay.Update()
	}
	m.syncDetail(ctx)
	ctx.RequestRender(false)
}

// ── menu overlay ───────────────────────────────────────────────────────────

func (m *CompletionMenu) menuBoxWidth() int {
	maxW := 0
	for _, item := range m.items {
		if w := lipgloss.Width(item.displayLabel()); w > maxW {
			maxW = w
		}
	}
	if maxW > 60 {
		maxW = 60
	}
	return maxW + 4 // padding + border
}

func (m *CompletionMenu) menuBoxHeight() int {
	n := len(m.items)
	h := min(n, m.MaxVisible) + 2 // items + border
	if n > m.MaxVisible {
		h++ // info line
	}
	return h
}

// tokenLen returns the length of the token being completed, used for
// positioning the overlay at the start of the token.
func (m *CompletionMenu) tokenLen() int {
	val := m.input.Value()
	return len(val) - m.replaceFrom
}

func (m *CompletionMenu) showMenu(ctx Context) {
	opts := &OverlayOptions{
		Width:          SizeAbs(m.menuBoxWidth()),
		MaxHeight:      SizeAbs(m.menuBoxHeight()),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        -m.tokenLen(),
		CursorGroup:    m.cursorGroup,
	}
	if m.handle != nil {
		m.overlay.items = m.items
		m.overlay.index = m.index
		m.overlay.Update()
		m.handle.SetOptions(opts)
	} else {
		m.overlay = &completionMenuOverlay{
			items:         m.items,
			index:         m.index,
			maxVisible:    m.MaxVisible,
			style:         m.MenuStyle,
			selectedStyle: m.MenuSelectedStyle,
			borderStyle:   m.MenuBorderStyle,
			dimStyle:      m.DimStyle,
		}
		m.handle = ctx.ShowOverlay(m.overlay, opts)
	}
	m.syncDetail(ctx)
}

// ── detail panel ───────────────────────────────────────────────────────────

func (m *CompletionMenu) detailOpts() *OverlayOptions {
	detailX := -m.tokenLen()
	if m.handle != nil {
		detailX += m.menuBoxWidth() + 1
	}
	return &OverlayOptions{
		Width:          SizePct(35),
		MaxHeight:      SizePct(80),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        detailX,
		CursorGroup:    m.cursorGroup,
	}
}

func (m *CompletionMenu) showDetailFor(ctx Context, c Completion) {
	lines := m.renderDetail(c, 40) // approximate width; real width comes from Render
	if len(lines) == 0 {
		m.hideDetail()
		return
	}
	opts := m.detailOpts()
	if m.detailComp == nil {
		m.detailComp = &completionDetailOverlay{
			lines:       lines,
			borderStyle: m.DetailBorderStyle,
		}
		m.detailH = ctx.ShowOverlay(m.detailComp, opts)
	} else {
		m.detailComp.lines = lines
		m.detailComp.Update()
		m.detailH.SetOptions(opts)
	}
}

func (m *CompletionMenu) syncDetail(ctx Context) {
	if !m.visible || len(m.items) == 0 {
		m.hideDetail()
		return
	}
	if m.index < 0 || m.index >= len(m.items) {
		m.hideDetail()
		return
	}
	c := m.items[m.index]
	lines := m.renderDetail(c, 40)
	if len(lines) == 0 {
		m.hideDetail()
		return
	}
	opts := m.detailOpts()
	if m.detailComp == nil {
		m.detailComp = &completionDetailOverlay{
			lines:       lines,
			borderStyle: m.DetailBorderStyle,
		}
		m.detailH = ctx.ShowOverlay(m.detailComp, opts)
	} else {
		m.detailComp.lines = lines
		m.detailComp.Update()
		m.detailH.SetOptions(opts)
	}
}

func (m *CompletionMenu) renderDetail(c Completion, width int) []string {
	if m.DetailRenderer != nil {
		return m.DetailRenderer(c, width)
	}
	return defaultDetailRenderer(c, width, m.DetailTitleStyle, m.DimStyle)
}

func defaultDetailRenderer(c Completion, _ int, titleStyle, dimStyle lipgloss.Style) []string {
	if c.Detail == "" && c.Documentation == "" {
		return nil
	}
	var lines []string
	lines = append(lines, titleStyle.Render(c.Label))
	if c.Detail != "" {
		lines = append(lines, dimStyle.Render(c.Detail))
	}
	if c.Documentation != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(c.Documentation, "\n")...)
	}
	return lines
}

// ── overlay components ─────────────────────────────────────────────────────

type completionMenuOverlay struct {
	Compo
	items         []Completion
	index         int
	maxVisible    int
	style         lipgloss.Style
	selectedStyle lipgloss.Style
	borderStyle   lipgloss.Style
	dimStyle      lipgloss.Style
}

func (c *completionMenuOverlay) Render(ctx Context) RenderResult {
	if len(c.items) == 0 {
		return RenderResult{}
	}

	visible := min(len(c.items), c.maxVisible)
	start := 0
	if c.index >= visible {
		start = c.index - visible + 1
	}
	end := start + visible

	// Max width from all items for stable sizing.
	maxW := 0
	for _, item := range c.items {
		if w := lipgloss.Width(item.displayLabel()); w > maxW {
			maxW = w
		}
	}
	if maxW > 60 {
		maxW = 60
	}
	if maxW+4 > ctx.Width {
		maxW = ctx.Width - 4
	}
	if maxW < 4 {
		maxW = 4
	}

	var menuLines []string
	for i := start; i < end && i < len(c.items); i++ {
		label := c.items[i].displayLabel()
		if lipgloss.Width(label) > maxW {
			label = label[:maxW-3] + "..."
		}
		padded := fmt.Sprintf(" %-*s ", maxW, label)
		if i == c.index {
			menuLines = append(menuLines, c.selectedStyle.Render(padded))
		} else {
			menuLines = append(menuLines, c.style.Render(padded))
		}
	}

	if len(c.items) > visible {
		info := fmt.Sprintf(" %d/%d ", c.index+1, len(c.items))
		menuLines = append(menuLines, c.dimStyle.Render(info))
	}

	inner := strings.Join(menuLines, "\n")
	box := c.borderStyle.Render(inner)
	return RenderResult{Lines: strings.Split(box, "\n")}
}

type completionDetailOverlay struct {
	Compo
	lines       []string
	borderStyle lipgloss.Style
}

func (d *completionDetailOverlay) Render(ctx Context) RenderResult {
	if len(d.lines) == 0 {
		return RenderResult{}
	}

	// Truncate if needed.
	lines := d.lines
	if ctx.Height > 0 && len(lines) > ctx.Height-2 {
		maxInner := ctx.Height - 2
		if maxInner > 1 {
			lines = lines[:maxInner-1]
			lines = append(lines, "...")
		} else if maxInner > 0 {
			lines = lines[:maxInner]
		}
	}

	inner := strings.Join(lines, "\n")
	box := d.borderStyle.Width(ctx.Width).Render(inner)
	return RenderResult{Lines: strings.Split(box, "\n")}
}
