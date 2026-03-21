package vt_test

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/vito/tuist"
	"github.com/vito/tuist/vt"
	"gotest.tools/v3/golden"
)

// ── test components ────────────────────────────────────────────────────────

// text renders fixed lines.
type text struct {
	tuist.Compo
	Lines  []string
	Cursor *tuist.CursorPos
}

func (s *text) Render(ctx tuist.Context) {
	ctx.Lines(s.Lines...)
	if s.Cursor != nil {
		ctx.SetCursor(s.Cursor.Row, s.Cursor.Col)
	}
}

// borderedBox renders a lipgloss-bordered box that respects ctx.Height.
type borderedBox struct {
	tuist.Compo
	Title string
	Lines []string
}

func (b *borderedBox) Render(ctx tuist.Context) {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63"))

	innerW := max(10, ctx.Width-2)

	inner := append([]string{b.Title}, b.Lines...)

	if ctx.Height > 0 && len(inner) > ctx.Height-2 {
		maxInner := ctx.Height - 2
		if maxInner > 1 {
			inner = inner[:maxInner-1]
			inner = append(inner, "...")
		} else if maxInner > 0 {
			inner = inner[:maxInner]
		}
	}

	box := borderStyle.Width(innerW).Render(strings.Join(inner, "\n"))
	ctx.Lines(strings.Split(box, "\n")...)
}

// progressBar renders a text-based progress bar with lipgloss styling.
type progressBar struct {
	tuist.Compo
	Label string
	Total int
	Done  int
}

func (p *progressBar) Render(ctx tuist.Context) {
	barWidth := ctx.Width - len(p.Label) - 5
	if barWidth < 5 {
		barWidth = 5
	}
	filled := barWidth * p.Done / p.Total
	empty := barWidth - filled

	filledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	emptyStyle := lipgloss.NewStyle().Faint(true)

	bar := "[" +
		filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty)) +
		"]"

	pct := fmt.Sprintf("%3d%%", 100*p.Done/p.Total)
	line := p.Label + " " + bar + " " + pct
	ctx.Line(line)
}

// callbackComponent renders via a function.
type callbackComponent struct {
	tuist.Compo
	fn func(tuist.Context)
}

func (c *callbackComponent) Render(ctx tuist.Context) {
	c.fn(ctx)
}

// ── basic rendering ────────────────────────────────────────────────────────

func TestGolden_SimpleText(t *testing.T) {
	term := vt.New(40, 5)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{
		"Hello, world!",
		"This is tuist.",
	}})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/simple_text.golden")
}

func TestGolden_StyledText(t *testing.T) {
	term := vt.New(50, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	bold := lipgloss.NewStyle().Bold(true)
	italic := lipgloss.NewStyle().Italic(true)
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	greenBg := lipgloss.NewStyle().Background(lipgloss.Color("2"))

	tui.AddChild(&text{Lines: []string{
		bold.Render("Bold heading"),
		italic.Render("Italic subtext"),
		red.Render("Red alert") + " normal " + greenBg.Render("green bg"),
		"plain text line",
	}})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/styled_text.golden")
}

func TestGolden_Container(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{
		lipgloss.NewStyle().Bold(true).Render("=== Status ==="),
	}})
	tui.AddChild(&text{Lines: []string{
		"  Engine: running",
		"  Tasks:  3/5",
		"  Errors: 0",
	}})
	tui.AddChild(&text{Lines: []string{
		lipgloss.NewStyle().Faint(true).Render("Press q to quit"),
	}})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/container.golden")
}

func TestGolden_ProgressBar(t *testing.T) {
	term := vt.New(40, 4)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&progressBar{Label: "Building", Total: 20, Done: 5})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/progress_bar.golden")
}

// ── differential rendering ─────────────────────────────────────────────────

func TestGolden_DiffUpdate(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	comp := &text{Lines: []string{
		"line 1: stable",
		"line 2: will change",
		"line 3: stable",
	}}
	tui.AddChild(comp)
	tui.RenderOnce()

	comp.Lines[1] = "line 2: CHANGED!"
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/diff_update.golden")
}

func TestGolden_AppendLines(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	comp := &text{Lines: []string{"a"}}
	tui.AddChild(comp)
	tui.RenderOnce()

	comp.Lines = []string{"a", "b", "c"}
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/append_lines.golden")
}

func TestGolden_NoChangeStable(t *testing.T) {
	term := vt.New(40, 5)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"stable line"}})

	tui.RenderOnce()
	tui.RenderOnce() // re-render with no changes

	golden.Assert(t, term.Render(), "golden/no_change_stable.golden")
}

func TestGolden_WidthChange(t *testing.T) {
	term := vt.New(40, 5)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"hello world"}})
	tui.RenderOnce()

	// Resize the virtual terminal.
	term.VT.Resize(5, 60)
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/width_change.golden")
}

func TestGolden_OffscreenChange(t *testing.T) {
	term := vt.New(40, 5)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	comp := &text{Lines: lines}
	tui.AddChild(comp)
	tui.RenderOnce()

	// Change a line above the viewport.
	comp.Lines[0] = "line 00 CHANGED"
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/offscreen_change.golden")
}

func TestGolden_FirstRenderClearsExistingContent(t *testing.T) {
	// Regression: when re-adding shorter content after removing all
	// children, old content must not bleed through.
	term := vt.New(80, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	long := &text{Lines: []string{"Loading Dagger module from /home/user/project..."}}
	tui.AddChild(long)
	tui.RenderOnce()

	tui.RemoveChild(long)
	tui.RenderOnce()

	short := &text{Lines: []string{"Welcome v0.1"}}
	tui.AddChild(short)
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/first_render_clears.golden")
}

func TestGolden_ShrinkToZeroLines(t *testing.T) {
	// Regression: when a component goes from N lines to 0, every
	// previous line must be cleared — including line 0.
	term := vt.New(40, 8)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	comp := &text{Lines: []string{"line A", "line B", "line C", "line D"}}
	tui.AddChild(comp)
	tui.RenderOnce()

	// Shrink to zero.
	comp.Lines = nil
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/shrink_to_zero.golden")
}

// ── cursor positioning ─────────────────────────────────────────────────────

func TestGolden_CursorPosition(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.SetShowHardwareCursor(true)

	tui.AddChild(&text{
		Lines:  []string{"first line", "cursor here", "last line"},
		Cursor: &tuist.CursorPos{Row: 1, Col: 3},
	})
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/cursor_position.golden")
}

func TestGolden_ContainerPropagatesCursor(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.SetShowHardwareCursor(true)

	// First child: 2 lines, no cursor.
	tui.AddChild(&text{Lines: []string{"a", "b"}})
	// Second child: 1 line, cursor at (0, 5).
	// In assembled output, this is row 2, col 5.
	tui.AddChild(&text{
		Lines:  []string{"hello world"},
		Cursor: &tuist.CursorPos{Row: 0, Col: 5},
	})
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/container_cursor.golden")
}

// ── overlay compositing ────────────────────────────────────────────────────

func TestGolden_OverlayCompositing(t *testing.T) {
	term := vt.New(20, 5)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	tui.AddChild(&text{Lines: []string{
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
		strings.Repeat(".", 20),
	}})

	tui.ShowOverlay(&text{Lines: []string{"OVERLAY"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_compositing.golden")
}

func TestGolden_Overlay(t *testing.T) {
	term := vt.New(50, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 8 {
		bgLines = append(bgLines, fmt.Sprintf("background line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	tui.ShowOverlay(&borderedBox{
		Title: "Popup",
		Lines: []string{"Option A", "Option B", "Option C"},
	}, &tuist.OverlayOptions{
		Width:     tuist.SizeAbs(25),
		MaxHeight: tuist.SizeAbs(8),
		Anchor:    tuist.AnchorCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay.golden")
}

func TestGolden_ContentRelativeOverlay(t *testing.T) {
	term := vt.New(30, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	tui.AddChild(&text{Lines: []string{"line-0", "line-1", "line-2"}})

	tui.ShowOverlay(&text{Lines: []string{"MENU-A", "MENU-B"}}, &tuist.OverlayOptions{
		Width:           tuist.SizeAbs(10),
		Anchor:          tuist.AnchorBottomLeft,
		ContentRelative: true,
		OffsetY:         -1,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/content_relative_overlay.golden")
}

func TestGolden_CursorRelativeOverlayPreferAbove(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	tui.AddChild(&text{
		Lines:  []string{"line-0", "line-1", "line-2", "line-3", "line-4", "input>"},
		Cursor: &tuist.CursorPos{Row: 5, Col: 7},
	})

	tui.ShowOverlay(&text{Lines: []string{"MENU-A", "MENU-B", "MENU-C"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_relative_above.golden")
}

func TestGolden_CursorRelativeOverlayFlipToBelow(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	// Cursor at row 1 — not enough room above for 3-line menu.
	tui.AddChild(&text{
		Lines:  []string{"line-0", "input>", "line-2", "line-3"},
		Cursor: &tuist.CursorPos{Row: 1, Col: 7},
	})

	tui.ShowOverlay(&text{Lines: []string{"MENU-A", "MENU-B", "MENU-C"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_relative_flip_below.golden")
}

func TestGolden_CursorRelativeOverlayOffsetX(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	tui.AddChild(&text{
		Lines:  []string{"aaaa", "bbbb", "cccc", "input>"},
		Cursor: &tuist.CursorPos{Row: 3, Col: 10},
	})

	tui.ShowOverlay(&text{Lines: []string{"HI"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(5),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        -3,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_relative_offset_x.golden")
}

func TestGolden_CursorRelativeOverlayMaxHeightNotClampedToContent(t *testing.T) {
	// Regression: cursor-relative overlays should resolve MaxHeight
	// against terminal height, not content height.
	term := vt.New(40, 24)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	tui.AddChild(&text{
		Lines:  []string{"line-0", "line-1", "input>"},
		Cursor: &tuist.CursorPos{Row: 2, Col: 7},
	})

	var menuLines []string
	for i := range 10 {
		menuLines = append(menuLines, fmt.Sprintf("item-%d", i))
	}
	tui.ShowOverlay(&text{Lines: menuLines}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(15),
		MaxHeight:      tuist.SizeAbs(12),
		CursorRelative: true,
		PreferAbove:    false,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_relative_max_height.golden")
}

func TestGolden_CursorRelativeOverlayCursorGroupBothFitAbove(t *testing.T) {
	term := vt.New(60, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 7 {
		bgLines = append(bgLines, fmt.Sprintf("line-%d", i))
	}
	bgLines = append(bgLines, "input>")
	tui.AddChild(&text{
		Lines:  bgLines,
		Cursor: &tuist.CursorPos{Row: 7, Col: 7},
	})

	group := tuist.NewCursorGroup()

	tui.ShowOverlay(&text{Lines: []string{"M-0", "M-1", "M-2", "M-3", "M-4"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		CursorGroup:    group,
	})

	tui.ShowOverlay(&text{Lines: []string{"D-0", "D-1"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        12,
		CursorGroup:    group,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_group_both_above.golden")
}

func TestGolden_CursorRelativeOverlayCursorGroupFlipAll(t *testing.T) {
	term := vt.New(60, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	// Cursor at row 3. Menu (2 lines) fits above but detail (5 lines)
	// doesn't. CursorGroup forces both below.
	tui.AddChild(&text{
		Lines:  []string{"line-0", "line-1", "line-2", "input>"},
		Cursor: &tuist.CursorPos{Row: 3, Col: 7},
	})

	group := tuist.NewCursorGroup()

	tui.ShowOverlay(&text{Lines: []string{"M-0", "M-1"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		CursorGroup:    group,
	})

	tui.ShowOverlay(&text{Lines: []string{"D-0", "D-1", "D-2", "D-3", "D-4"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        12,
		CursorGroup:    group,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_group_flip_all.golden")
}

func TestGolden_CursorRelativeOverlayNoCursor(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	// No cursor — cursor-relative overlay should be hidden.
	tui.AddChild(&text{Lines: []string{"line-0", "line-1"}})

	tui.ShowOverlay(&text{Lines: []string{"MENU-A"}}, &tuist.OverlayOptions{
		Width:          tuist.SizeAbs(10),
		CursorRelative: true,
		PreferAbove:    true,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/cursor_relative_no_cursor.golden")
}

func TestGolden_OverlayMaxHeightPassedToComponent(t *testing.T) {
	term := vt.New(80, 24)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	tui.AddChild(&text{Lines: []string{
		"content 0", "content 1", "content 2", "content 3", "content 4",
		"content 5", "content 6", "content 7", "content 8", "content 9",
	}})

	// The overlay is given MaxHeight=8 and renders 5 lines, verifying
	// the height constraint is passed through and respected.
	tui.ShowOverlay(&text{Lines: []string{
		"line 0", "line 1", "line 2", "line 3", "line 4",
	}}, &tuist.OverlayOptions{
		Width:     tuist.SizeAbs(20),
		MaxHeight: tuist.SizeAbs(8),
		Anchor:    tuist.AnchorTopRight,
		Margin:    tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_max_height.golden")
}

// ── bordered overlay regression tests ──────────────────────────────────────

func TestGolden_OverlayBorderedBoxWithMaxHeight(t *testing.T) {
	term := vt.New(60, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 10 {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	var detailLines []string
	for i := range 20 {
		detailLines = append(detailLines, fmt.Sprintf("detail %d", i))
	}
	tui.ShowOverlay(&borderedBox{
		Title: "MyFunction",
		Lines: detailLines,
	}, &tuist.OverlayOptions{
		Width:     tuist.SizeAbs(30),
		MaxHeight: tuist.SizeAbs(12),
		Anchor:    tuist.AnchorTopRight,
		Margin:    tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_bordered_max_height.golden")
}

func TestGolden_OverlayBorderedBoxFitsNaturally(t *testing.T) {
	term := vt.New(60, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 10 {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	tui.ShowOverlay(&borderedBox{
		Title: "SmallFunc",
		Lines: []string{"returns String!", "", "A short description."},
	}, &tuist.OverlayOptions{
		Width:     tuist.SizeAbs(30),
		MaxHeight: tuist.SizeAbs(12),
		Anchor:    tuist.AnchorTopRight,
		Margin:    tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_bordered_fits.golden")
}

func TestGolden_OverlayLastLineNotDropped(t *testing.T) {
	term := vt.New(60, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"line 0", "line 1", "line 2"}})

	tui.ShowOverlay(&text{Lines: []string{
		"TOP-BORDER", "content-a", "content-b", "content-c", "BOTTOM-BORDER",
	}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(20),
		Anchor: tuist.AnchorTopRight,
		Margin: tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_last_line.golden")
}

func TestGolden_OverlayLastLineWithScrolling(t *testing.T) {
	term := vt.New(60, 12)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 15 {
		bgLines = append(bgLines, fmt.Sprintf("bg line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	tui.ShowOverlay(&text{Lines: []string{
		"TOP-BORDER", "content-a", "content-b", "content-c", "BOTTOM-BORDER",
	}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(20),
		Anchor: tuist.AnchorTopRight,
		Margin: tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_last_line_scrolling.golden")
}

func TestGolden_OverlayTwoOverlaysLastLine(t *testing.T) {
	term := vt.New(60, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{
		"line 0", "line 1", "line 2", "line 3", "line 4",
	}})

	tui.ShowOverlay(&text{Lines: []string{"menu-a", "menu-b", "menu-c"}}, &tuist.OverlayOptions{
		Width:           tuist.SizeAbs(15),
		Anchor:          tuist.AnchorBottomLeft,
		ContentRelative: true,
		OffsetY:         -1,
	})

	tui.ShowOverlay(&text{Lines: []string{
		"TOP-BORDER", "content-a", "content-b", "BOTTOM-BORDER",
	}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(20),
		Anchor: tuist.AnchorTopRight,
		Margin: tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_two_overlays.golden")
}

func TestGolden_OverlayAtBottomOfViewport(t *testing.T) {
	term := vt.New(60, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 8 {
		bgLines = append(bgLines, fmt.Sprintf("bg line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	tui.ShowOverlay(&text{Lines: []string{
		"╭───────────────╮",
		"│ line 1        │",
		"│ line 2        │",
		"│ line 3        │",
		"│ line 4        │",
		"│ line 5        │",
		"│ line 6        │",
		"│ line 7        │",
		"╰───────────────╯",
	}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(18),
		Anchor: tuist.AnchorTopRight,
		Margin: tuist.OverlayMargin{Top: 0, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_at_bottom_viewport.golden")
}

func TestGolden_OverlayTallerThanViewport(t *testing.T) {
	term := vt.New(60, 8)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"bg 0", "bg 1", "bg 2", "bg 3"}})

	tui.ShowOverlay(&text{Lines: []string{
		"╭───────────────╮",
		"│ line 1        │",
		"│ line 2        │",
		"│ line 3        │",
		"│ line 4        │",
		"│ line 5        │",
		"│ line 6        │",
		"│ line 7        │",
		"│ line 8        │",
		"│ line 9        │",
		"│ line 10       │",
		"╰───────────────╯",
	}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(18),
		Anchor: tuist.AnchorTopLeft,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_taller_than_viewport.golden")
}

func TestGolden_OverlayBorderedBoxWidthMismatch(t *testing.T) {
	term := vt.New(80, 24)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	var bgLines []string
	for i := range 15 {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	overlay := &callbackComponent{fn: func(ctx tuist.Context) {
		borderStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63"))

		contentW := max(8, ctx.Width-2)

		var lines []string
		lines = append(lines, "Title")
		for i := range 8 {
			line := fmt.Sprintf("detail line %d with extra text padding here", i)
			if len(line) > contentW {
				line = line[:contentW]
			}
			lines = append(lines, line)
		}

		if ctx.Height > 0 && len(lines) > ctx.Height-2 {
			maxInner := ctx.Height - 2
			if maxInner > 1 {
				lines = lines[:maxInner-1]
				lines = append(lines, "...")
			}
		}

		inner := strings.Join(lines, "\n")
		box := borderStyle.Width(ctx.Width).Render(inner)
		ctx.Lines(strings.Split(box, "\n")...)
	}}
	tui.ShowOverlay(overlay, &tuist.OverlayOptions{
		Width:     tuist.SizeAbs(35),
		MaxHeight: tuist.SizeAbs(14),
		Anchor:    tuist.AnchorTopRight,
		Margin:    tuist.OverlayMargin{Top: 1, Right: 1},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_bordered_width_mismatch.golden")
}

func TestGolden_OverlayAfterContentShrinks(t *testing.T) {
	// Regression: a viewport-relative overlay anchored to the top-right
	// should stay visible when the base content shrinks. Previously the
	// overlay was positioned relative to maxLinesRendered (a high-water
	// mark), so after content shrank the overlay ended up in blank space
	// far below the visible content.
	term := vt.New(40, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	// Start with 20 lines of content.
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	comp := &text{Lines: lines}
	tui.AddChild(comp)

	tui.ShowOverlay(&text{Lines: []string{"NOTIFY"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorTopRight,
	})
	tui.RenderOnce()

	// Grow content to 100 lines.
	lines = make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	comp.Lines = lines
	comp.Update()
	tui.RenderOnce()

	// Shrink content back to 20 lines.
	lines = make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	comp.Lines = lines
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/overlay_after_content_shrinks.golden")
}

// ── overlay anchor positions ───────────────────────────────────────────────

func overlayAnchorBackground(rows int) []string {
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = strings.Repeat(".", 40)
	}
	return lines
}

func TestGolden_OverlayAnchorTopLeft(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"TL1", "TL2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorTopLeft,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_top_left.golden")
}

func TestGolden_OverlayAnchorTopCenter(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"TC1", "TC2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorTopCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_top_center.golden")
}

func TestGolden_OverlayAnchorBottomLeft(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"BL1", "BL2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorBottomLeft,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_bottom_left.golden")
}

func TestGolden_OverlayAnchorBottomRight(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"BR1", "BR2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorBottomRight,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_bottom_right.golden")
}

func TestGolden_OverlayAnchorBottomCenter(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"BC1", "BC2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorBottomCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_bottom_center.golden")
}

func TestGolden_OverlayAnchorLeftCenter(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"LC1", "LC2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorLeftCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_left_center.golden")
}

func TestGolden_OverlayAnchorRightCenter(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"RC1", "RC2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorRightCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_anchor_right_center.golden")
}

// ── overlay margins ────────────────────────────────────────────────────────

func TestGolden_OverlayMarginAllSides(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"M1", "M2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorTopLeft,
		Margin: tuist.OverlayMargin{Top: 2, Right: 3, Bottom: 2, Left: 4},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_margin_all_sides.golden")
}

func TestGolden_OverlayMarginBottomRight(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"BR1", "BR2"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorBottomRight,
		Margin: tuist.OverlayMargin{Bottom: 2, Right: 3},
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_margin_bottom_right.golden")
}

// ── overlay sizing ─────────────────────────────────────────────────────────

func TestGolden_OverlayWidthPct(t *testing.T) {
	term := vt.New(40, 8)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(8)})
	tui.ShowOverlay(&text{Lines: []string{"50% width overlay"}}, &tuist.OverlayOptions{
		Width:  tuist.SizePct(50),
		Anchor: tuist.AnchorCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_width_pct.golden")
}

func TestGolden_OverlayMaxHeightPct(t *testing.T) {
	term := vt.New(40, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(20)})

	var lines []string
	for i := range 15 {
		lines = append(lines, fmt.Sprintf("item %02d", i))
	}
	tui.ShowOverlay(&text{Lines: lines}, &tuist.OverlayOptions{
		Width:     tuist.SizeAbs(15),
		MaxHeight: tuist.SizePct(50), // 50% of 20 = 10 lines
		Anchor:    tuist.AnchorCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_max_height_pct.golden")
}

func TestGolden_OverlayMinWidth(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(6)})
	// Width is 5 but MinWidth is 15, so 15 should win.
	tui.ShowOverlay(&text{Lines: []string{"narrow?"}}, &tuist.OverlayOptions{
		Width:    tuist.SizeAbs(5),
		MinWidth: 15,
		Anchor:   tuist.AnchorCenter,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_min_width.golden")
}

// ── explicit Row/Col positioning ───────────────────────────────────────────

func TestGolden_OverlayExplicitRowCol(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"HERE"}}, &tuist.OverlayOptions{
		Width: tuist.SizeAbs(8),
		Row:   tuist.SizeAbs(3),
		Col:   tuist.SizeAbs(10),
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_explicit_row_col.golden")
}

func TestGolden_OverlayRowColPct(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	// 50% row = vertically centered, 50% col = horizontally centered.
	tui.ShowOverlay(&text{Lines: []string{"CTR"}}, &tuist.OverlayOptions{
		Width: tuist.SizeAbs(8),
		Row:   tuist.SizePct(50),
		Col:   tuist.SizePct(50),
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_row_col_pct.golden")
}

// ── overlay offset (non-cursor-relative) ───────────────────────────────────

func TestGolden_OverlayOffsetXY(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"OFF"}}, &tuist.OverlayOptions{
		Width:   tuist.SizeAbs(8),
		Anchor:  tuist.AnchorTopLeft,
		OffsetX: 5,
		OffsetY: 2,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_offset_xy.golden")
}

// ── overlay handle operations ──────────────────────────────────────────────

func TestGolden_OverlaySetHidden(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"bg line 0", "bg line 1", "bg line 2"}})
	handle := tui.ShowOverlay(&text{Lines: []string{"POPUP"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorCenter,
	})
	tui.RenderOnce()

	// Hide the overlay — background should show through.
	handle.SetHidden(true)
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_set_hidden.golden")
}

func TestGolden_OverlaySetHiddenThenShow(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"bg line 0", "bg line 1", "bg line 2"}})
	handle := tui.ShowOverlay(&text{Lines: []string{"POPUP"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorCenter,
	})
	tui.RenderOnce()

	handle.SetHidden(true)
	tui.RenderOnce()

	// Unhide — overlay should reappear.
	handle.SetHidden(false)
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_set_hidden_then_show.golden")
}

func TestGolden_OverlayRemove(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"bg line 0", "bg line 1", "bg line 2"}})
	handle := tui.ShowOverlay(&text{Lines: []string{"POPUP"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorCenter,
	})
	tui.RenderOnce()

	handle.Remove()
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_remove.golden")
}

func TestGolden_OverlaySetOptions(t *testing.T) {
	term := vt.New(40, 8)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(8)})
	handle := tui.ShowOverlay(&text{Lines: []string{"MOVE"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorTopLeft,
	})
	tui.RenderOnce()

	// Move overlay to bottom-right.
	handle.SetOptions(&tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorBottomRight,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_set_options.golden")
}

// ── content-relative with different anchors ────────────────────────────────

func TestGolden_ContentRelativeTopRight(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	// 4 lines of content, viewport is 10.
	tui.AddChild(&text{Lines: []string{"line-0", "line-1", "line-2", "line-3"}})
	tui.ShowOverlay(&text{Lines: []string{"CR-TR"}}, &tuist.OverlayOptions{
		Width:           tuist.SizeAbs(10),
		Anchor:          tuist.AnchorTopRight,
		ContentRelative: true,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_content_relative_top_right.golden")
}

func TestGolden_ContentRelativeBottomRight(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"line-0", "line-1", "line-2", "line-3"}})
	tui.ShowOverlay(&text{Lines: []string{"CR-BR"}}, &tuist.OverlayOptions{
		Width:           tuist.SizeAbs(10),
		Anchor:          tuist.AnchorBottomRight,
		ContentRelative: true,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_content_relative_bottom_right.golden")
}

func TestGolden_ContentRelativeWithScrolling(t *testing.T) {
	// Content taller than viewport — overlay should be positioned
	// relative to the full content height, not the viewport.
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	tui.AddChild(&text{Lines: lines})
	tui.ShowOverlay(&text{Lines: []string{"BOTTOM"}}, &tuist.OverlayOptions{
		Width:           tuist.SizeAbs(10),
		Anchor:          tuist.AnchorBottomRight,
		ContentRelative: true,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_content_relative_scrolling.golden")
}

// ── overlapping overlays ───────────────────────────────────────────────────

func TestGolden_OverlappingOverlays(t *testing.T) {
	// Two overlays at the same anchor — the second (later) one should
	// paint over the first where they overlap.
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"AAAA", "AAAA", "AAAA"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorTopLeft,
	})
	tui.ShowOverlay(&text{Lines: []string{"BB", "BB"}}, &tuist.OverlayOptions{
		Width:   tuist.SizeAbs(8),
		Anchor:  tuist.AnchorTopLeft,
		OffsetX: 2,
		OffsetY: 1,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_overlapping.golden")
}

// ── content shrink with bottom anchor ──────────────────────────────────────

func TestGolden_OverlayAfterContentShrinksBottomAnchor(t *testing.T) {
	// Same regression scenario as OverlayAfterContentShrinks but with
	// a bottom-anchored overlay to verify it stays pinned to the
	// visible bottom.
	term := vt.New(40, 20)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	comp := &text{Lines: lines}
	tui.AddChild(comp)

	tui.ShowOverlay(&text{Lines: []string{"STATUS"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorBottomRight,
	})
	tui.RenderOnce()

	// Grow content to 100 lines.
	big := make([]string, 100)
	for i := range big {
		big[i] = fmt.Sprintf("line %02d", i)
	}
	comp.Lines = big
	comp.Update()
	tui.RenderOnce()

	// Shrink back to 5 lines.
	small := make([]string, 5)
	for i := range small {
		small[i] = fmt.Sprintf("line %02d", i)
	}
	comp.Lines = small
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/overlay_shrink_bottom_anchor.golden")
}

// ── overlay with no options ────────────────────────────────────────────────

func TestGolden_OverlayNilOptions(t *testing.T) {
	// Passing nil options should use sensible defaults.
	term := vt.New(40, 6)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(6)})
	tui.ShowOverlay(&text{Lines: []string{"DEFAULT"}}, nil)
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_nil_options.golden")
}

// ── multiple overlays with different anchors ───────────────────────────────

func TestGolden_OverlayFourCorners(t *testing.T) {
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: overlayAnchorBackground(10)})
	tui.ShowOverlay(&text{Lines: []string{"TL"}}, &tuist.OverlayOptions{
		Width: tuist.SizeAbs(6), Anchor: tuist.AnchorTopLeft,
	})
	tui.ShowOverlay(&text{Lines: []string{"TR"}}, &tuist.OverlayOptions{
		Width: tuist.SizeAbs(6), Anchor: tuist.AnchorTopRight,
	})
	tui.ShowOverlay(&text{Lines: []string{"BL"}}, &tuist.OverlayOptions{
		Width: tuist.SizeAbs(6), Anchor: tuist.AnchorBottomLeft,
	})
	tui.ShowOverlay(&text{Lines: []string{"BR"}}, &tuist.OverlayOptions{
		Width: tuist.SizeAbs(6), Anchor: tuist.AnchorBottomRight,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_four_corners.golden")
}

// ── overlay with content shorter than viewport ─────────────────────────────

func TestGolden_OverlayContentShorterThanViewport(t *testing.T) {
	// Content is only 3 lines, viewport is 10.
	// Viewport-relative overlay should be placed relative to the viewport,
	// not the content.
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)
	tui.AddChild(&text{Lines: []string{"short-0", "short-1", "short-2"}})
	tui.ShowOverlay(&text{Lines: []string{"BOT"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(8),
		Anchor: tuist.AnchorBottomRight,
	})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/overlay_content_shorter.golden")
}

// ── overlay update after content changes ───────────────────────────────────

func TestGolden_OverlayContentGrows(t *testing.T) {
	// Overlay should stay anchored correctly as content grows past the
	// viewport height.
	term := vt.New(40, 10)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	comp := &text{Lines: []string{"line-0", "line-1"}}
	tui.AddChild(comp)

	tui.ShowOverlay(&text{Lines: []string{"NOTIF"}}, &tuist.OverlayOptions{
		Width:  tuist.SizeAbs(10),
		Anchor: tuist.AnchorTopRight,
	})
	tui.RenderOnce()

	// Grow content past viewport.
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	comp.Lines = lines
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/overlay_content_grows.golden")
}
