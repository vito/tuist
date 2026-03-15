package tuist

import (
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
)

// TextInput is a text editor component with cursor, history, and
// kill-line support. It supports multiline editing via Shift+Enter.
// Long lines are word-wrapped to fit the available width.
type TextInput struct {
	Compo

	// Prompt is rendered before the first line. May contain ANSI codes.
	Prompt string

	// ContinuationPrompt is rendered before continuation lines (lines
	// after the first in multiline input). Defaults to aligned spacing
	// if empty.
	ContinuationPrompt string

	// Value is the current input text.
	value []rune
	// cursor is the position within value (0 = before first rune).
	cursor int

	focused bool

	// lastRenderWidth stores the width from the most recent Render call,
	// used by CursorScreenCol and moveCursorVertically.
	lastRenderWidth int

	// OnSubmit is called when Enter is pressed. The string is the trimmed
	// input value. Return true to clear the input after submission.
	OnSubmit func(ctx Context, value string) bool

	// Suggestion is a ghost completion hint shown after the cursor. It is
	// cleared on every keystroke and must be re-set by the caller (e.g. in
	// OnSubmit or OnChange). Cleared automatically on each keystroke.
	Suggestion string

	// SuggestionStyle wraps the suggestion text (e.g. dim style).
	// If nil, the suggestion is rendered as-is.
	SuggestionStyle func(string) string

	// OnChange is called after the input value has been modified (character
	// inserted, deleted, etc.). It is NOT called for cursor-only movements.
	OnChange func(ctx Context)

	// KeyInterceptor, if set, is called before TextInput processes a key.
	// Return true to consume the event (TextInput won't handle it).
	// Return false to let TextInput handle it normally.
	KeyInterceptor func(ctx Context, ev uv.KeyPressEvent) bool
}

// NewTextInput creates a TextInput with the given prompt.
func NewTextInput(prompt string) *TextInput {
	return &TextInput{Prompt: prompt}
}

func (t *TextInput) SetFocused(_ Context, focused bool) { t.focused = focused }

// Value returns the current input string.
func (t *TextInput) Value() string { return string(t.value) }

// SetValue replaces the input and moves the cursor to the end.
func (t *TextInput) SetValue(s string) {
	t.value = []rune(s)
	t.cursor = len(t.value)
	t.Update()
}

// CursorEnd moves the cursor to the end of the input.
func (t *TextInput) CursorEnd() { t.cursor = len(t.value) }

// CursorScreenCol returns the screen column of the cursor, including
// the prompt width. This is useful for callers that need to position
// overlays (e.g. completion menus) relative to the cursor.
func (t *TextInput) CursorScreenCol() int {
	_, col := t.wrappedCursorRowCol()
	return col
}

// cursorRowCol computes the (row, col) of the cursor within the value,
// treating '\n' as line separators (without wrapping).
func (t *TextInput) cursorRowCol() (row, col int) {
	for i := 0; i < t.cursor && i < len(t.value); i++ {
		if t.value[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return
}

// wrappedCursorRowCol computes the (row, col) of the cursor in the
// wrapped output, accounting for word wrapping at lastRenderWidth.
// col includes the prompt width.
func (t *TextInput) wrappedCursorRowCol() (row, col int) {
	prompt := t.Prompt
	contPrompt := t.ContinuationPrompt
	if contPrompt == "" {
		w := VisibleWidth(prompt)
		contPrompt = strings.Repeat(" ", w)
	}
	promptW := VisibleWidth(prompt)
	contPromptW := VisibleWidth(contPrompt)
	width := t.lastRenderWidth

	inputLines := strings.Split(string(t.value), "\n")
	runeOffset := 0
	visualRow := 0
	for i, inputLine := range inputLines {
		lineRunes := []rune(inputLine)
		firstW := width - promptW
		nextW := width - contPromptW
		if i > 0 {
			firstW = nextW
		}
		segments := wordWrapRunes(lineRunes, firstW, nextW)
		for j, seg := range segments {
			segStart := runeOffset + seg.runeStart
			segEnd := runeOffset + seg.runeEnd
			if t.cursor >= segStart && (t.cursor < segEnd || (t.cursor == segEnd && (j == len(segments)-1 && i == len(inputLines)-1))) {
				// Cursor is in this segment.
				pw := contPromptW
				if i == 0 && j == 0 {
					pw = promptW
				}
				cursorInSeg := t.cursor - segStart
				col = pw + VisibleWidth(string(lineRunes[seg.runeStart:seg.runeStart+cursorInSeg]))
				return visualRow, col
			}
			visualRow++
		}
		runeOffset += len(lineRunes) + 1 // +1 for '\n'
	}
	// Fallback: cursor at end.
	pw := contPromptW
	if len(inputLines) <= 1 {
		// Check if we're still on the first visual line.
		if visualRow <= 1 {
			pw = promptW
		}
	}
	return visualRow - 1, pw
}

// Render returns one or more lines: prompt + input, with cursor position.
// Long lines are word-wrapped to fit ctx.Width.
func (t *TextInput) Render(ctx Context) RenderResult {
	t.lastRenderWidth = ctx.Width

	val := string(t.value)
	inputLines := strings.Split(val, "\n")

	prompt := t.Prompt
	contPrompt := t.ContinuationPrompt
	if contPrompt == "" {
		// Default: align with the main prompt using spaces.
		w := VisibleWidth(prompt)
		contPrompt = strings.Repeat(" ", w)
	}
	promptW := VisibleWidth(prompt)
	contPromptW := VisibleWidth(contPrompt)

	var lines []string
	var cursorRow, cursorCol int
	runeOffset := 0

	for i, inputLine := range inputLines {
		lineRunes := []rune(inputLine)

		// Compute available content widths.
		firstW := ctx.Width - promptW
		nextW := ctx.Width - contPromptW
		if i > 0 {
			firstW = nextW
		}

		segments := wordWrapRunes(lineRunes, firstW, nextW)
		isLastInputLine := i == len(inputLines)-1

		for j, seg := range segments {
			isLastSeg := j == len(segments)-1

			// Choose prompt for this visual line.
			p := contPrompt
			pw := contPromptW
			if i == 0 && j == 0 {
				p = prompt
				pw = promptW
			}

			var buf strings.Builder
			buf.WriteString(p)
			buf.WriteString(seg.text)

			// Ghost suggestion on the very last visual line.
			if isLastInputLine && isLastSeg && t.Suggestion != "" && t.cursor == len(t.value) {
				hint := strings.TrimPrefix(t.Suggestion, val)
				if hint != "" {
					if t.SuggestionStyle != nil {
						buf.WriteString(t.SuggestionStyle(hint))
					} else {
						buf.WriteString(hint)
					}
				}
			}

			lines = append(lines, buf.String())

			// Track cursor position.
			segStart := runeOffset + seg.runeStart
			segEnd := runeOffset + seg.runeEnd
			if t.cursor >= segStart && (t.cursor < segEnd || (t.cursor == segEnd && isLastInputLine && isLastSeg)) {
				cursorInSeg := t.cursor - segStart
				cursorRow = len(lines) - 1
				cursorCol = pw + VisibleWidth(string(lineRunes[seg.runeStart:seg.runeStart+cursorInSeg]))
			}
		}
		runeOffset += len(lineRunes) + 1 // +1 for '\n'
	}

	var cursor *CursorPos
	if t.focused {
		cursor = &CursorPos{Row: cursorRow, Col: cursorCol}
	}

	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
	}
}

// HandleKeyPress implements [Interactive].
func (t *TextInput) HandleKeyPress(ctx Context, ev uv.KeyPressEvent) bool {
	if t.KeyInterceptor != nil && t.KeyInterceptor(ctx, ev) {
		return true
	}
	return t.handleKeyPress(ctx, ev)
}

// HandlePaste implements [Pasteable].
func (t *TextInput) HandlePaste(ctx Context, ev uv.PasteEvent) bool {
	t.handlePaste(ctx, ev.Content)
	return true
}

func (t *TextInput) handlePaste(ctx Context, content string) {
	oldValue := string(t.value)
	runes := []rune(content)
	newVal := make([]rune, 0, len(t.value)+len(runes))
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, runes...)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor += len(runes)
	t.Update()
	if t.OnChange != nil && string(t.value) != oldValue {
		t.OnChange(ctx)
	}
}

func (t *TextInput) handleKeyPress(ctx Context, e uv.KeyPressEvent) bool {
	key := uv.Key(e)

	oldValue := string(t.value)
	savedSuggestion := t.Suggestion
	t.Suggestion = "" // Clear suggestion on every keystroke

	// handled tracks whether the key was consumed by this component.
	// The deferred Update/OnChange runs regardless (suggestion was cleared).
	var handled bool
	defer func() {
		t.Update() // any input may change cursor or content
		if t.OnChange != nil && string(t.value) != oldValue {
			t.OnChange(ctx)
		}
	}()

	// Shift+Enter, Alt+Enter, or Ctrl+J: insert newline for multiline input.
	// Shift+Enter requires kitty keyboard protocol support in the terminal.
	// Alt+Enter works in most terminals (\x1b \x0d).
	// Ctrl+J (\x0a) works universally as it's always distinct from Enter (\x0d).
	if (key.Code == uv.KeyEnter && (key.Mod.Contains(uv.ModShift) || key.Mod.Contains(uv.ModAlt))) ||
		(key.Code == 'j' && key.Mod == uv.ModCtrl) {
		t.InsertRune('\n')
		return true
	}

	// Enter (unmodified): submit.
	if key.Code == uv.KeyEnter && key.Mod == 0 {
		if t.OnSubmit != nil {
			val := strings.TrimSpace(string(t.value))
			if t.OnSubmit(ctx, val) {
				t.value = nil
				t.cursor = 0
			}
		}
		return true
	}

	// Tab: accept suggestion if available, otherwise bubble.
	if key.Code == uv.KeyTab && key.Mod == 0 {
		if savedSuggestion != "" {
			t.SetValue(savedSuggestion)
			return true
		}
		handled = false
		return handled
	}

	// Backtab (Shift+Tab): bubble.
	if key.Code == uv.KeyTab && key.Mod.Contains(uv.ModShift) {
		handled = false
		return handled
	}

	// Right arrow / Ctrl+F: accept suggestion at end, else move cursor.
	// Exclude Ctrl modifier on Right so that Ctrl+Right falls through to word movement.
	if (key.Code == uv.KeyRight && key.Mod == 0 || key.Code == 'f' && key.Mod == uv.ModCtrl) {
		if key.Code == uv.KeyRight && savedSuggestion != "" && t.cursor == len(t.value) {
			t.SetValue(savedSuggestion)
			return true
		}
		if t.cursor < len(t.value) {
			t.cursor++
		}
		return true
	}

	// Backspace.
	if key.Code == uv.KeyBackspace {
		if t.cursor > 0 {
			t.value = append(t.value[:t.cursor-1], t.value[t.cursor:]...)
			t.cursor--
		}
		return true
	}

	// Delete.
	if key.Code == uv.KeyDelete {
		if t.cursor < len(t.value) {
			t.value = append(t.value[:t.cursor], t.value[t.cursor+1:]...)
		}
		return true
	}

	// Cursor movement.
	if key.Code == uv.KeyLeft && key.Mod == 0 || key.Code == 'b' && key.Mod == uv.ModCtrl {
		if t.cursor > 0 {
			t.cursor--
		}
		return true
	}
	if key.Code == uv.KeyHome || key.Code == 'a' && key.Mod == uv.ModCtrl {
		t.cursor = t.lineStart()
		return true
	}
	if key.Code == uv.KeyEnd || key.Code == 'e' && key.Mod == uv.ModCtrl {
		t.cursor = t.lineEnd()
		return true
	}

	// Up/Down: move between visual lines (including wrapped lines), else bubble.
	if key.Code == uv.KeyUp && key.Mod == 0 {
		if t.hasMultipleVisualLines() {
			t.moveCursorVertically(-1)
			return true
		}
		handled = false
		return handled
	}
	if key.Code == uv.KeyDown && key.Mod == 0 {
		if t.hasMultipleVisualLines() {
			t.moveCursorVertically(1)
			return true
		}
		handled = false
		return handled
	}

	// Word movement.
	if key.MatchString("alt+left") || key.MatchString("ctrl+left") || key.MatchString("alt+b") {
		t.cursor = t.wordLeft()
		return true
	}
	if key.MatchString("alt+right") || key.MatchString("ctrl+right") || key.MatchString("alt+f") {
		if key.Code == uv.KeyRight && savedSuggestion != "" && t.cursor == len(t.value) {
			t.SetValue(savedSuggestion)
			return true
		}
		t.cursor = t.wordRight()
		return true
	}

	// Kill line.
	if key.Code == 'u' && key.Mod == uv.ModCtrl {
		start := t.lineStart()
		t.value = append(t.value[:start], t.value[t.cursor:]...)
		t.cursor = start
		return true
	}
	if key.Code == 'k' && key.Mod == uv.ModCtrl {
		end := t.lineEnd()
		t.value = append(t.value[:t.cursor], t.value[end:]...)
		return true
	}

	// Kill subword backward (Ctrl+W).
	if key.Code == 'w' && key.Mod == uv.ModCtrl {
		start := t.subwordLeft()
		t.value = append(t.value[:start], t.value[t.cursor:]...)
		t.cursor = start
		return true
	}

	// Kill word forward (Alt+D).
	if key.Code == 'd' && key.Mod == uv.ModAlt {
		end := t.wordRight()
		t.value = append(t.value[:t.cursor], t.value[end:]...)
		return true
	}

	// Transpose (Ctrl+T).
	if key.Code == 't' && key.Mod == uv.ModCtrl {
		if t.cursor > 0 && t.cursor < len(t.value) {
			t.value[t.cursor-1], t.value[t.cursor] = t.value[t.cursor], t.value[t.cursor-1]
			t.cursor++
		}
		return true
	}

	// Unhandled non-printable keys: bubble.
	if key.Text == "" {
		handled = false
		return handled
	}

	// Insert printable text.
	runes := []rune(key.Text)
	newVal := make([]rune, 0, len(t.value)+len(runes))
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, runes...)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor += len(runes)
	return true
}

// InsertRune inserts a rune at the current cursor position.
func (t *TextInput) InsertRune(r rune) {
	newVal := make([]rune, 0, len(t.value)+1)
	newVal = append(newVal, t.value[:t.cursor]...)
	newVal = append(newVal, r)
	newVal = append(newVal, t.value[t.cursor:]...)
	t.value = newVal
	t.cursor++
}

func (t *TextInput) hasMultipleLines() bool {
	return slices.Contains(t.value, '\n')
}

// lineStart returns the index of the start of the current line.
func (t *TextInput) lineStart() int {
	i := t.cursor - 1
	for i >= 0 && t.value[i] != '\n' {
		i--
	}
	return i + 1
}

// lineEnd returns the index of the end of the current line (before '\n' or at len).
func (t *TextInput) lineEnd() int {
	i := t.cursor
	for i < len(t.value) && t.value[i] != '\n' {
		i++
	}
	return i
}

// moveCursorVertically moves the cursor up (dir=-1) or down (dir=1)
// by one visual (wrapped) line.
func (t *TextInput) moveCursorVertically(dir int) {
	prompt := t.Prompt
	contPrompt := t.ContinuationPrompt
	if contPrompt == "" {
		w := VisibleWidth(prompt)
		contPrompt = strings.Repeat(" ", w)
	}
	promptW := VisibleWidth(prompt)
	contPromptW := VisibleWidth(contPrompt)
	width := t.lastRenderWidth

	// Build the list of visual segments with their rune offsets.
	type visualLine struct {
		runeOffset int // absolute rune offset in t.value
		seg        wrapSegment
		promptW    int
	}
	inputLines := strings.Split(string(t.value), "\n")
	var vlines []visualLine
	runeOffset := 0
	for i, inputLine := range inputLines {
		lineRunes := []rune(inputLine)
		firstW := width - promptW
		nextW := width - contPromptW
		if i > 0 {
			firstW = nextW
		}
		segments := wordWrapRunes(lineRunes, firstW, nextW)
		for j, seg := range segments {
			pw := contPromptW
			if i == 0 && j == 0 {
				pw = promptW
			}
			vlines = append(vlines, visualLine{runeOffset: runeOffset, seg: seg, promptW: pw})
		}
		runeOffset += len(lineRunes) + 1
	}

	// Find which visual line the cursor is on.
	currentVLine := len(vlines) - 1
	for vi, vl := range vlines {
		segStart := vl.runeOffset + vl.seg.runeStart
		segEnd := vl.runeOffset + vl.seg.runeEnd
		if t.cursor >= segStart && t.cursor < segEnd {
			currentVLine = vi
			break
		}
		if t.cursor == segEnd && vi == len(vlines)-1 {
			currentVLine = vi
			break
		}
	}

	targetVLine := currentVLine + dir
	if targetVLine < 0 || targetVLine >= len(vlines) {
		return
	}

	// Compute the visible column of the cursor in the current visual line.
	cur := vlines[currentVLine]
	cursorInSeg := t.cursor - (cur.runeOffset + cur.seg.runeStart)
	segRunes := t.value[cur.runeOffset+cur.seg.runeStart : cur.runeOffset+cur.seg.runeStart+cursorInSeg]
	curCol := cur.promptW + VisibleWidth(string(segRunes))

	// Move to the same visible column in the target visual line.
	tgt := vlines[targetVLine]
	tgtRunes := t.value[tgt.runeOffset+tgt.seg.runeStart : tgt.runeOffset+tgt.seg.runeEnd]
	col := 0
	ri := 0
	for ri < len(tgtRunes) {
		rw := VisibleWidth(string(tgtRunes[ri : ri+1]))
		if tgt.promptW+col+rw > curCol {
			break
		}
		col += rw
		ri++
	}
	t.cursor = tgt.runeOffset + tgt.seg.runeStart + ri
}

func (t *TextInput) wordLeft() int {
	i := t.cursor
	for i > 0 && isSpace(t.value[i-1]) {
		i--
	}
	for i > 0 && !isSpace(t.value[i-1]) {
		i--
	}
	return i
}

// subwordLeft moves backward to the previous subword boundary, where
// boundaries are transitions between character classes: identifiers
// (letters/digits/_), whitespace, and symbols (everything else).
// This makes Ctrl+W stop at ".", "(", ")" etc. instead of only at
// whitespace.
func (t *TextInput) subwordLeft() int {
	i := t.cursor
	// Skip whitespace.
	for i > 0 && isSpace(t.value[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	// Delete a run of the same character class.
	class := runeClass(t.value[i-1])
	for i > 0 && runeClass(t.value[i-1]) == class {
		i--
	}
	return i
}

func (t *TextInput) wordRight() int {
	i := t.cursor
	for i < len(t.value) && !isSpace(t.value[i]) {
		i++
	}
	for i < len(t.value) && isSpace(t.value[i]) {
		i++
	}
	return i
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// runeClass classifies a rune for subword boundary detection.
// 0 = identifier (letter/digit/_), 1 = symbol/punctuation.
// Whitespace is handled separately before calling this.
func runeClass(r rune) int {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
		return 0
	}
	return 1
}

// hasMultipleVisualLines returns true if the input would render as more than
// one visual line, either from explicit newlines or word wrapping.
func (t *TextInput) hasMultipleVisualLines() bool {
	if slices.Contains(t.value, '\n') {
		return true
	}
	if t.lastRenderWidth <= 0 {
		return false
	}
	prompt := t.Prompt
	promptW := VisibleWidth(prompt)
	availW := t.lastRenderWidth - promptW
	if availW <= 0 {
		return false
	}
	return VisibleWidth(string(t.value)) > availW
}

// wrapSegment describes a slice of a logical line after word wrapping.
type wrapSegment struct {
	text      string // the text content of this segment
	runeStart int    // starting rune index within the logical line
	runeEnd   int    // exclusive ending rune index within the logical line
}

// wordWrapRunes word-wraps runes into segments. The first segment uses
// firstWidth visible columns; subsequent segments use contWidth. If widths
// are <= 0, no wrapping is performed.
func wordWrapRunes(runes []rune, firstWidth, contWidth int) []wrapSegment {
	if len(runes) == 0 {
		return []wrapSegment{{text: "", runeStart: 0, runeEnd: 0}}
	}

	// No wrapping if width is not constraining.
	if firstWidth <= 0 && contWidth <= 0 {
		return []wrapSegment{{text: string(runes), runeStart: 0, runeEnd: len(runes)}}
	}

	var segments []wrapSegment
	pos := 0
	isFirst := true

	for pos < len(runes) {
		maxW := contWidth
		if isFirst {
			maxW = firstWidth
		}
		isFirst = false

		if maxW <= 0 {
			// Can't fit anything; take everything to avoid infinite loop.
			segments = append(segments, wrapSegment{
				text:      string(runes[pos:]),
				runeStart: pos,
				runeEnd:   len(runes),
			})
			break
		}

		// Find how many runes fit within maxW visible columns.
		end := pos
		col := 0
		lastSpace := -1
		for end < len(runes) {
			rw := VisibleWidth(string(runes[end]))
			if col+rw > maxW {
				break
			}
			if runes[end] == ' ' {
				lastSpace = end
			}
			col += rw
			end++
		}

		if end >= len(runes) {
			// Rest fits on this line.
			segments = append(segments, wrapSegment{
				text:      string(runes[pos:]),
				runeStart: pos,
				runeEnd:   len(runes),
			})
			break
		}

		// Need to break. Prefer word boundary.
		if lastSpace > pos {
			// Break after the space: include the space on this line,
			// next line starts with the next word.
			segments = append(segments, wrapSegment{
				text:      string(runes[pos : lastSpace+1]),
				runeStart: pos,
				runeEnd:   lastSpace + 1,
			})
			pos = lastSpace + 1
		} else {
			// No word boundary found; hard break at character boundary.
			segments = append(segments, wrapSegment{
				text:      string(runes[pos:end]),
				runeStart: pos,
				runeEnd:   end,
			})
			pos = end
		}
	}

	return segments
}
