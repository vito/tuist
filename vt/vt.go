// Package vt provides a virtual terminal that implements [tuist.Terminal]
// backed by [midterm.Terminal]. It interprets all ANSI escape sequences
// exactly as a real terminal would — cursor movement, colors, line
// clearing, synchronized output, etc. — making it ideal for testing,
// headless rendering, and golden file snapshots.
package vt

import (
	"bytes"
	"io"

	"github.com/vito/midterm"
	"github.com/vito/tuist"
)

// Terminal implements [tuist.Terminal] backed by a [midterm.Terminal].
// All output from the TUI is interpreted by the virtual terminal,
// producing a pixel-perfect representation of what a real terminal
// would display.
type Terminal struct {
	// VT is the underlying virtual terminal. Access it directly to
	// inspect content, cursor position, cell formatting, etc.
	VT *midterm.Terminal
}

var _ tuist.Terminal = (*Terminal)(nil)

// New creates a Terminal with the given dimensions.
func New(cols, rows int) *Terminal {
	return &Terminal{
		VT: midterm.NewTerminal(rows, cols),
	}
}

func (m *Terminal) Start(onInput func([]byte), onResize func()) error { return nil }
func (m *Terminal) Stop()                                             {}
func (m *Terminal) SetInputPassthrough(io.Writer)                     {}
func (m *Terminal) Write(p []byte)                                    { m.VT.Write(p) }
func (m *Terminal) WriteString(s string)                              { m.VT.Write([]byte(s)) }
func (m *Terminal) Columns() int                                      { return m.VT.Width }
func (m *Terminal) Rows() int                                         { return m.VT.Height }
func (m *Terminal) HideCursor()                                       { m.VT.Write([]byte("\x1b[?25l")) }
func (m *Terminal) ShowCursor()                                       { m.VT.Write([]byte("\x1b[?25h")) }

// Render returns the virtual terminal's content as a string including
// ANSI escape sequences for colors and formatting, capturing the full
// styled appearance as a real terminal would display it.
func (m *Terminal) Render() string {
	buf := new(bytes.Buffer)
	m.VT.Render(buf)
	return buf.String()
}
