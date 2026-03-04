package tuist

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/windows"
)

// ProcessTerminal is a Terminal backed by os.Stdin / os.Stdout on Windows.
// It uses the Windows Console API to enable virtual terminal processing
// and raw input mode.
type ProcessTerminal struct {
	origInMode  uint32
	origOutMode uint32
	onInput     func([]byte)
	onResize    func()
	stopCancel  context.CancelFunc
	stopCtx     context.Context

	sizeMu sync.RWMutex
	cols   int
	rows   int
}

func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{}
}

func (t *ProcessTerminal) Start(onInput func([]byte), onResize func()) error {
	t.onInput = onInput
	t.onResize = onResize
	t.stopCtx, t.stopCancel = context.WithCancel(context.Background())

	inHandle := windows.Handle(os.Stdin.Fd())
	outHandle := windows.Handle(os.Stdout.Fd())

	// Save original console modes.
	if err := windows.GetConsoleMode(inHandle, &t.origInMode); err != nil {
		return fmt.Errorf("get input console mode: %w", err)
	}
	if err := windows.GetConsoleMode(outHandle, &t.origOutMode); err != nil {
		return fmt.Errorf("get output console mode: %w", err)
	}

	// Enable raw input mode with virtual terminal input.
	rawIn := uint32(windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_WINDOW_INPUT)
	if err := windows.SetConsoleMode(inHandle, rawIn); err != nil {
		return fmt.Errorf("set raw input mode: %w", err)
	}

	// Enable virtual terminal processing on output.
	rawOut := t.origOutMode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.ENABLE_PROCESSED_OUTPUT
	// Disable auto newline so we control cursor positioning.
	rawOut &^= windows.DISABLE_NEWLINE_AUTO_RETURN
	if err := windows.SetConsoleMode(outHandle, rawOut); err != nil {
		return fmt.Errorf("set output mode: %w", err)
	}

	// Cache initial terminal size.
	t.refreshSize()

	// Enable bracketed paste.
	t.WriteString("\x1b[?2004h")

	// Enable Kitty keyboard protocol.
	t.WriteString(ansi.KittyKeyboard(ansi.KittyDisambiguateEscapeCodes, 1))
	t.WriteString(ansi.RequestKittyKeyboard)

	// Read stdin in a goroutine.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				t.onInput(data)
			}
			if err != nil {
				return
			}
		}
	}()

	// Poll for resize events using console input events.
	go func() {
		for {
			select {
			case <-t.stopCtx.Done():
				return
			default:
				oldCols := t.Columns()
				oldRows := t.Rows()
				t.refreshSize()
				if (t.Columns() != oldCols || t.Rows() != oldRows) && t.onResize != nil {
					t.onResize()
				}
				// Use WaitForSingleObject with a timeout to avoid busy-spinning.
				windows.WaitForSingleObject(inHandle, 100)
			}
		}
	}()

	return nil
}

func (t *ProcessTerminal) Stop() {
	// Disable Kitty keyboard protocol.
	t.WriteString(ansi.KittyKeyboard(0, 1))

	// Disable bracketed paste.
	t.WriteString("\x1b[?2004l")

	if t.stopCancel != nil {
		t.stopCancel()
	}

	// Restore original console modes.
	inHandle := windows.Handle(os.Stdin.Fd())
	outHandle := windows.Handle(os.Stdout.Fd())
	_ = windows.SetConsoleMode(inHandle, t.origInMode)
	_ = windows.SetConsoleMode(outHandle, t.origOutMode)
}

func (t *ProcessTerminal) Write(p []byte) {
	_, _ = os.Stdout.Write(p)
}

func (t *ProcessTerminal) WriteString(s string) {
	_, _ = os.Stdout.WriteString(s)
}

func (t *ProcessTerminal) Columns() int {
	t.sizeMu.RLock()
	c := t.cols
	t.sizeMu.RUnlock()
	if c == 0 {
		return 80
	}
	return c
}

func (t *ProcessTerminal) Rows() int {
	t.sizeMu.RLock()
	r := t.rows
	t.sizeMu.RUnlock()
	if r == 0 {
		return 24
	}
	return r
}

func (t *ProcessTerminal) refreshSize() {
	var info windows.ConsoleScreenBufferInfo
	outHandle := windows.Handle(os.Stdout.Fd())
	err := windows.GetConsoleScreenBufferInfo(outHandle, &info)
	if err != nil {
		return
	}
	cols := int(info.Window.Right-info.Window.Left) + 1
	rows := int(info.Window.Bottom-info.Window.Top) + 1
	t.sizeMu.Lock()
	if cols > 0 {
		t.cols = cols
	}
	if rows > 0 {
		t.rows = rows
	}
	t.sizeMu.Unlock()
}

func (t *ProcessTerminal) HideCursor() {
	t.WriteString("\x1b[?25l")
}

func (t *ProcessTerminal) ShowCursor() {
	t.WriteString("\x1b[?25h")
}

