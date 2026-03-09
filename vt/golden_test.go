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

func (s *text) Render(ctx tuist.Context) tuist.RenderResult {
	return tuist.RenderResult{Lines: s.Lines, Cursor: s.Cursor}
}

// borderedBox renders a lipgloss-bordered box that respects ctx.Height.
type borderedBox struct {
	tuist.Compo
	Title string
	Lines []string
}

func (b *borderedBox) Render(ctx tuist.Context) tuist.RenderResult {
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
	return tuist.RenderResult{Lines: strings.Split(box, "\n")}
}

// progressBar renders a text-based progress bar with lipgloss styling.
type progressBar struct {
	tuist.Compo
	Label string
	Total int
	Done  int
}

func (p *progressBar) Render(ctx tuist.Context) tuist.RenderResult {
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
	return tuist.RenderResult{Lines: []string{line}}
}

// callbackComponent renders via a function.
type callbackComponent struct {
	tuist.Compo
	fn func(tuist.Context) tuist.RenderResult
}

func (c *callbackComponent) Render(ctx tuist.Context) tuist.RenderResult {
	return c.fn(ctx)
}

// ── basic rendering ────────────────────────────────────────────────────────

func TestGolden_SimpleText(t *testing.T) {
	term := vt.New(40, 5)
	tui := tuist.New(term)
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
	tui.AddChild(&progressBar{Label: "Building", Total: 20, Done: 5})
	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/progress_bar.golden")
}

// ── differential rendering ─────────────────────────────────────────────────

func TestGolden_DiffUpdate(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)

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
	tui.AddChild(&text{Lines: []string{"stable line"}})

	tui.RenderOnce()
	tui.RenderOnce() // re-render with no changes

	golden.Assert(t, term.Render(), "golden/no_change_stable.golden")
}

func TestGolden_WidthChange(t *testing.T) {
	term := vt.New(40, 5)
	tui := tuist.New(term)
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

	var bgLines []string
	for i := range 15 {
		bgLines = append(bgLines, fmt.Sprintf("content line %d", i))
	}
	tui.AddChild(&text{Lines: bgLines})

	overlay := &callbackComponent{fn: func(ctx tuist.Context) tuist.RenderResult {
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
		return tuist.RenderResult{Lines: strings.Split(box, "\n")}
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
