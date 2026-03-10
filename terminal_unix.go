//go:build unix

package tuist

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"
)

// StdTerminal is a Terminal backed by the standard file descriptors
// Terminal dimensions are cached and refreshed on SIGWINCH to avoid
// repeated ioctl syscalls during rendering.
type StdTerminal struct {
	origTermios *unix.Termios
	onInput     func([]byte)
	onResize    func()
	sigCh       chan os.Signal
	stopCancel  context.CancelFunc
	stopCtx     context.Context

	// inputMu protects inputSink. The stdin reader goroutine holds
	// this lock while writing to the sink, so swapping sinks is safe.
	inputMu   sync.Mutex
	inputSink io.Writer // nil = discard; set to onInput wrapper or passthrough

	// readerOnce ensures only one stdin reader goroutine exists.
	readerOnce sync.Once

	sizeMu sync.RWMutex
	cols   int
	rows   int
}

func NewStdTerminal() *StdTerminal {
	return &StdTerminal{}
}

func (t *StdTerminal) Start(onInput func([]byte), onResize func()) error {
	t.onInput = onInput
	t.onResize = onResize
	t.stopCtx, t.stopCancel = context.WithCancel(context.Background())

	// Save and set raw mode.
	fd := int(os.Stdin.Fd()) // TODO: find TTY on any of stdio, to handle pipes
	orig, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return fmt.Errorf("get termios: %w", err)
	}
	t.origTermios = orig

	raw := *orig
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return fmt.Errorf("set raw: %w", err)
	}

	// Cache initial terminal size.
	t.refreshSize()

	// Enable bracketed paste.
	t.WriteString("\x1b[?2004h")

	// Enable Kitty keyboard protocol (disambiguate escape codes).
	t.WriteString(ansi.KittyKeyboard(ansi.KittyDisambiguateEscapeCodes, 1))
	t.WriteString(ansi.RequestKittyKeyboard)

	// Direct input to the onInput callback.
	t.inputMu.Lock()
	t.inputSink = inputCallbackWriter{t.onInput}
	t.inputMu.Unlock()

	// Start the single stdin reader goroutine (only once per process).
	t.readerOnce.Do(func() {
		go t.readStdin()
	})

	// Listen for SIGWINCH.
	t.sigCh = make(chan os.Signal, 1)
	signal.Notify(t.sigCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-t.sigCh:
				t.refreshSize()
				if t.onResize != nil {
					t.onResize()
				}
			case <-t.stopCtx.Done():
				return
			}
		}
	}()

	return nil
}

// readStdin reads from os.Stdin forever and writes to the current inputSink.
// This goroutine lives for the process lifetime.
func (t *StdTerminal) readStdin() {
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			t.inputMu.Lock()
			sink := t.inputSink
			t.inputMu.Unlock()
			if sink != nil {
				sink.Write(data) //nolint:errcheck
			}
		}
		if err != nil {
			return
		}
	}
}

// SetInputPassthrough redirects stdin to the given writer instead of
// the normal input handler. Pass nil to discard input (e.g. when the
// terminal is stopped). Call with the onInput wrapper to resume normal
// input handling (done automatically by Start).
func (t *StdTerminal) SetInputPassthrough(w io.Writer) {
	t.inputMu.Lock()
	t.inputSink = w
	t.inputMu.Unlock()
}

func (t *StdTerminal) Stop() {
	// Disable Kitty keyboard protocol.
	t.WriteString(ansi.KittyKeyboard(0, 1))

	// Disable bracketed paste.
	t.WriteString("\x1b[?2004l")

	// Stop directing input to the TUI.
	t.inputMu.Lock()
	t.inputSink = nil
	t.inputMu.Unlock()

	if t.stopCancel != nil {
		t.stopCancel()
	}
	if t.sigCh != nil {
		signal.Stop(t.sigCh)
	}
	if t.origTermios != nil {
		fd := int(os.Stdin.Fd())
		_ = unix.IoctlSetTermios(fd, ioctlWriteTermios, t.origTermios)
	}
}

func (t *StdTerminal) Write(p []byte) {
	_, _ = os.Stdout.Write(p)
}

func (t *StdTerminal) WriteString(s string) {
	_, _ = os.Stdout.WriteString(s)
}

func (t *StdTerminal) Columns() int {
	t.sizeMu.RLock()
	c := t.cols
	t.sizeMu.RUnlock()
	if c == 0 {
		return 80
	}
	return c
}

func (t *StdTerminal) Rows() int {
	t.sizeMu.RLock()
	r := t.rows
	t.sizeMu.RUnlock()
	if r == 0 {
		return 24
	}
	return r
}

// refreshSize queries the kernel for current terminal dimensions and caches
// them. Called once at Start and on every SIGWINCH.
func (t *StdTerminal) refreshSize() {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return
	}
	t.sizeMu.Lock()
	if ws.Col > 0 {
		t.cols = int(ws.Col)
	}
	if ws.Row > 0 {
		t.rows = int(ws.Row)
	}
	t.sizeMu.Unlock()
}

func (t *StdTerminal) HideCursor() {
	t.WriteString("\x1b[?25l")
}

func (t *StdTerminal) ShowCursor() {
	t.WriteString("\x1b[?25h")
}
