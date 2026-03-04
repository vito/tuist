// Package tuist implements a differential terminal renderer that uses the
// normal scrollback buffer (no alternate screen). It can surgically update
// any line via cursor movement, and falls back to a full clear+repaint when
// off-screen content changes. Synchronized output prevents flickering.
//
// This is a Go port of the pi TUI renderer.
package tuist

import "io"

// Terminal abstracts terminal I/O so the renderer can be tested with a
// fake terminal.
type Terminal interface {
	// Start puts the terminal into raw mode and begins listening for input
	// and resize events. onInput receives raw bytes from stdin. onResize is
	// called when the terminal dimensions change.
	Start(onInput func([]byte), onResize func()) error

	// Stop restores the terminal to its original state and discards
	// subsequent input until the next Start call.
	Stop()

	// SetInputPassthrough redirects stdin bytes to w instead of the
	// normal input handler. This is used when a background command
	// needs exclusive stdin access: the command reads from a pipe
	// whose write end is w, while the terminal's single reader
	// goroutine continues to be the sole consumer of os.Stdin.
	// Pass nil to discard input (done automatically by Stop).
	// Start restores normal input handling.
	SetInputPassthrough(w io.Writer)

	// Write sends raw bytes to the terminal.
	Write(p []byte)

	// WriteString sends a string to the terminal.
	WriteString(s string)

	// Columns returns the current terminal width.
	Columns() int

	// Rows returns the current terminal height.
	Rows() int

	// HideCursor hides the hardware cursor.
	HideCursor()

	// ShowCursor shows the hardware cursor.
	ShowCursor()
}

// inputCallbackWriter adapts an onInput callback to io.Writer.
type inputCallbackWriter struct {
	fn func([]byte)
}

func (w inputCallbackWriter) Write(p []byte) (int, error) {
	w.fn(p)
	return len(p), nil
}
