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

func (s *text) Render(ctx tuist.RenderContext) tuist.RenderResult {
	out := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		if tuist.VisibleWidth(l) > ctx.Width {
			out[i] = tuist.Truncate(l, ctx.Width, "")
		} else {
			out[i] = l
		}
	}
	return tuist.RenderResult{Lines: out, Cursor: s.Cursor}
}

// borderedBox renders a lipgloss-bordered box that respects ctx.Height.
type borderedBox struct {
	tuist.Compo
	Title string
	Lines []string
}

func (b *borderedBox) Render(ctx tuist.RenderContext) tuist.RenderResult {
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

func (p *progressBar) Render(ctx tuist.RenderContext) tuist.RenderResult {
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
	fn func(tuist.RenderContext) tuist.RenderResult
}

func (c *callbackComponent) Render(ctx tuist.RenderContext) tuist.RenderResult {
	return c.fn(ctx)
}

// ── golden tests ───────────────────────────────────────────────────────────

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

func TestGolden_DiffUpdate(t *testing.T) {
	term := vt.New(40, 6)
	tui := tuist.New(term)

	comp := &text{Lines: []string{
		"line 1: stable",
		"line 2: will change",
		"line 3: stable",
	}}
	tui.AddChild(comp)

	// First render.
	tui.RenderOnce()

	// Mutate and re-render. The TUI writes differential updates directly
	// to midterm, which applies cursor movement and line clearing just
	// like a real terminal.
	comp.Lines[1] = "line 2: CHANGED!"
	comp.Update()
	tui.RenderOnce()

	golden.Assert(t, term.Render(), "golden/diff_update.golden")
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

func TestGolden_ProgressBar(t *testing.T) {
	term := vt.New(40, 4)
	tui := tuist.New(term)

	tui.AddChild(&progressBar{Label: "Building", Total: 20, Done: 5})

	tui.RenderOnce()
	golden.Assert(t, term.Render(), "golden/progress_bar.golden")
}

// ── overlay regression tests (ported from tui_test.go) ─────────────────────

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
		"TOP-BORDER",
		"content-a",
		"content-b",
		"content-c",
		"BOTTOM-BORDER",
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
		"TOP-BORDER",
		"content-a",
		"content-b",
		"content-c",
		"BOTTOM-BORDER",
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
		"TOP-BORDER",
		"content-a",
		"content-b",
		"BOTTOM-BORDER",
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

	// Mimics the detailBubble.Render pattern: lipgloss Width(n) is
	// TOTAL width including borders, so content must be wrapped to n-2.
	overlay := &callbackComponent{fn: func(ctx tuist.RenderContext) tuist.RenderResult {
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
