package vt

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/vito/tuist"
)

// Frame holds the parsed content of a single TUIST_FRAMES dump file.
type Frame struct {
	Number      int
	Lines       []string // all content lines (full output)
	Width       int      // terminal width (0 if not recorded)
	Height      int      // terminal height (viewport)
	ViewportTop int
}

// headerRe parses the === header line. Width is optional for
// backwards compatibility with older frame dumps.
var headerRe = regexp.MustCompile(
	`^=== frame (\d+) \| lines=(\d+)(?: width=(\d+))? height=(\d+) viewportTop=(\d+) ===$`,
)

// lineRe matches a content line in the "full content" section:
//
//	"> [  42] content"  or  "  [  42] content"
var lineRe = regexp.MustCompile(`^[> ] \[\s*\d+\] `)

// ParseFrame parses a single TUIST_FRAMES dump file.
func ParseFrame(data string) (Frame, error) {
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	if len(lines) == 0 {
		return Frame{}, fmt.Errorf("empty frame file")
	}

	m := headerRe.FindStringSubmatch(lines[0])
	if m == nil {
		return Frame{}, fmt.Errorf("bad header: %q", lines[0])
	}

	f := Frame{}
	f.Number, _ = strconv.Atoi(m[1])
	// m[2] is total lines count (for validation)
	if m[3] != "" {
		f.Width, _ = strconv.Atoi(m[3])
	}
	f.Height, _ = strconv.Atoi(m[4])
	f.ViewportTop, _ = strconv.Atoi(m[5])

	// Find the "--- full content ---" section and extract lines.
	inFull := false
	for _, line := range lines {
		if line == "--- full content ---" {
			inFull = true
			continue
		}
		if !inFull {
			continue
		}
		if !lineRe.MatchString(line) {
			continue
		}
		// Strip the "> [NNN] " or "  [NNN] " prefix.
		// Find "] " after the bracket.
		idx := strings.Index(line, "] ")
		if idx < 0 {
			continue
		}
		f.Lines = append(f.Lines, line[idx+2:])
	}

	totalLines, _ := strconv.Atoi(m[2])
	if len(f.Lines) != totalLines {
		return f, fmt.Errorf("expected %d lines, parsed %d", totalLines, len(f.Lines))
	}

	return f, nil
}

// ParseFrameFile reads and parses a TUIST_FRAMES dump file from disk.
func ParseFrameFile(path string) (Frame, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Frame{}, err
	}
	return ParseFrame(string(data))
}

// Replay feeds a sequence of frames through a TUI backed by a virtual
// terminal, returning the terminal after the last frame. Each frame is
// rendered via [tuist.TUI.RenderOnce].
//
// Width must be provided if the frame files don't include it (pre-width
// format). Pass 0 to use the width from the frame header.
func Replay(frames []Frame, width int) *Terminal {
	if len(frames) == 0 {
		panic("Replay: no frames")
	}

	f0 := frames[0]
	w := width
	if w == 0 {
		w = f0.Width
	}
	if w == 0 {
		panic("Replay: width not specified and not in frame header")
	}

	term := New(w, f0.Height)
	tui := tuist.New(term)
	tui.SetSyncOutput(true)

	comp := &replayComponent{}
	tui.AddChild(comp)

	for _, f := range frames {
		comp.lines = f.Lines
		comp.Update()
		tui.RenderOnce()
	}

	return term
}

// replayComponent is a minimal component that emits pre-rendered lines.
type replayComponent struct {
	tuist.Compo
	lines []string
}

func (r *replayComponent) Render(ctx tuist.Context) {
	ctx.Lines(r.lines...)
}
