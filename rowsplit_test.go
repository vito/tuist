package tuist

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The framework's core invariant is that one element of the output buffer is
// exactly one physical terminal row: cursor translation, overlay anchoring,
// viewport math and the differential renderer's relative cursor moves are all
// derived from slice indices. A single element spanning two rows desynchronizes
// the renderer's model of the hardware cursor permanently, so the tests below
// cover both guards: the split in [Context.Line]/[Context.Lines] and the
// position-preserving sanitization in renderFrame.

func TestContextLineSplitsRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"single row", "a", []string{"a"}},
		{"empty", "", []string{""}},
		{"newline", "a\nb", []string{"a", "b"}},
		{"crlf", "a\r\nb", []string{"a", "b"}},
		{"three rows", "a\nb\nc", []string{"a", "b", "c"}},
		{"trailing newline", "a\n", []string{"a", ""}},
		{"leading newline", "\na", []string{"", "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &renderOutput{}
			testContext(40).withOutput(out).Line(tc.in)
			assert.Equal(t, tc.want, out.lines)
		})
	}
}

func TestContextLinesSplitsRows(t *testing.T) {
	// Mixed clean and multi-row strings: the total is the physical row count.
	out := &renderOutput{}
	testContext(40).withOutput(out).Lines("clean", "two\nrows", "also clean")
	assert.Equal(t, []string{"clean", "two", "rows", "also clean"}, out.lines)

	// The all-clean case is the common one and must behave exactly as before.
	clean := &renderOutput{}
	testContext(40).withOutput(clean).Lines("a", "b", "c")
	assert.Equal(t, []string{"a", "b", "c"}, clean.lines)
}

// wrappedComponent emits its content through lipgloss's Width style, which
// WRAPS rather than truncates — the most common real-world way a component
// hands a multi-row string to ctx.Line.
type wrappedComponent struct {
	Compo
	text  string
	width int
}

func (w *wrappedComponent) Render(ctx Context) {
	ctx.Line(lipgloss.NewStyle().Width(w.width).Render(w.text))
}

func TestWrappedLineBecomesOneEntryPerRow(t *testing.T) {
	const (
		colWidth = 10
		text     = "wrapping-is-not-truncation-and-this-token-is-long"
	)
	wantRows := strings.Count(lipgloss.NewStyle().Width(colWidth).Render(text), "\n") + 1
	require.Greater(t, wantRows, 1, "lipgloss should have wrapped the content")

	term := NewHeadlessTerminal(40, 10)
	tui := New(term)
	tui.AddChild(&wrappedComponent{text: text, width: colWidth})

	frame := tui.Step()
	assert.Len(t, frame, wantRows, "frame length must equal the physical row count")
	for i, line := range frame {
		assert.NotContainsf(t, line, "\n", "frame line %d spans rows: %q", i, line)
	}
}

// multiRowOverlay emits its whole body as a single string, the way a
// lipgloss-bordered or vertically joined overlay does.
type multiRowOverlay struct {
	Compo
	body string
}

func (m *multiRowOverlay) Render(ctx Context) { ctx.Line(m.body) }

func TestOverlayAnchoredWithPostSplitHeight(t *testing.T) {
	// compositeOverlays sizes and anchors an overlay from len(oLines), so a
	// two-row overlay that measured as one line used to be anchored one row
	// too low — a visible symptom of the unsplit string.
	const termW, termH = 20, 6
	base := make([]string, termH)
	for i := range base {
		base[i] = strings.Repeat(".", termW)
	}

	term := NewHeadlessTerminal(termW, termH)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: base})
	tui.ShowOverlay(&multiRowOverlay{body: "TOP\nBOT"}, &OverlayOptions{
		Width:  SizeAbs(3),
		Anchor: AnchorBottomLeft,
	})

	frame := tui.Step()
	require.Len(t, frame, termH)
	// Bottom-anchored and two rows tall: the last two rows, in order.
	assert.True(t, strings.HasPrefix(stripANSI(frame[termH-2]), "TOP"),
		"overlay's first row should be second-to-last, got %q", stripANSI(frame[termH-2]))
	assert.True(t, strings.HasPrefix(stripANSI(frame[termH-1]), "BOT"),
		"overlay's second row should be last, got %q", stripANSI(frame[termH-1]))
}

func TestTabbedLineClampedToTerminalWidth(t *testing.T) {
	// A tab measures as zero columns but advances the terminal to the next tab
	// stop, so this line reads as 12 columns and paints 42. Without expansion
	// the width clamp waves it through and it wraps onto a second row.
	const tabbed = "aa\tbb\tcc\tdd\tee\tff"
	require.Less(t, VisibleWidth(tabbed), 20, "tabs measure as narrow")

	term := NewHeadlessTerminal(20, 5)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{tabbed}})

	frame := tui.Step()
	require.Len(t, frame, 1)
	assert.NotContains(t, frame[0], "\t", "tabs should be expanded before clamping")
	assert.LessOrEqual(t, VisibleWidth(frame[0]), 20, "clamped line must fit one row")
	// Expanded: "aa" to column 8, "bb" to 16, "cc" to 24 — clamped at 20.
	assert.Equal(t, "aa"+strings.Repeat(" ", 6)+"bb"+strings.Repeat(" ", 6)+"cc  ", frame[0])
}

// rawComponent writes straight into the render buffer, bypassing
// [Context.Line]'s split. It stands in for output that reaches the renderer
// some other way — hand-built composites and other embedders of the frame.
type rawComponent struct {
	Compo
	lines []string
}

func (r *rawComponent) Render(ctx Context) {
	ctx.output.lines = append(ctx.output.lines, r.lines...)
}

func TestRenderFrameSanitizesUnsplitLines(t *testing.T) {
	term := NewHeadlessTerminal(20, 5)
	tui := New(term)
	tui.AddChild(&rawComponent{lines: []string{"a\nb", "c\rd", "e\vf\fg"}})

	frame := tui.Step()
	// Position-preserving: three elements in, three elements out, because
	// overlay anchoring and cursor translation already ran against that count.
	require.Len(t, frame, 3)
	assert.Equal(t, []string{"a b", "c d", "e f g"}, frame)

	// The loop mutates the root component's cached buffer in place, so a
	// second frame re-runs it over its own output: it must be a no-op.
	before := append([]string(nil), frame...)
	assert.Equal(t, before, tui.Frame(), "sanitization must be idempotent")
}

func TestFrameLinesAreSingleRows(t *testing.T) {
	const termW = 30
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(10).
		Render("boxed")
	require.Greater(t, strings.Count(box, "\n"), 0, "a bordered box is multi-line")

	term := NewHeadlessTerminal(termW, 12)
	tui := New(term)
	tui.AddChild(&staticComponent{lines: []string{
		"plain",
		box,
		"crlf\r\ntail",
		"tabbed\tcolumns\there",
	}})

	frame := tui.Step()
	wantRows := 1 + (strings.Count(box, "\n") + 1) + 2 + 1
	require.Len(t, frame, wantRows)

	for i, line := range frame {
		assert.Equalf(t, 0, strings.Count(line, "\n"), "frame line %d spans rows: %q", i, line)
		assert.NotContainsf(t, line, "\r", "frame line %d has a carriage return: %q", i, line)
		assert.NotContainsf(t, line, "\t", "frame line %d has an unexpanded tab: %q", i, line)
		assert.LessOrEqualf(t, VisibleWidth(line), termW,
			"frame line %d is wider than the terminal: %q", i, line)
	}

	// RenderLines skips the frame machinery entirely, so it relies solely on
	// the split in Context.Line.
	for i, line := range tui.RenderLines() {
		assert.Equalf(t, 0, strings.Count(line, "\n"), "rendered line %d spans rows: %q", i, line)
	}
}
