//go:build unix

package tuist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// StdTerminal is a Terminal backed by the standard file descriptors.
// It detects which of stdin, stdout, or stderr is a TTY and uses that
// for raw mode, output, and size queries. Input is read from whichever
// fd is a TTY for input (typically stdin).
type StdTerminal struct {
	origTermios *unix.Termios
	ttyFd       int      // fd of the TTY (for termios and size queries)
	ttyOut      *os.File // file to write output to (the TTY)
	ttyIn       io.Reader // reader for input (the TTY input fd)
	ttyInFd     int       // fd for input (for termios restore)
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

// findTTYs checks stdin, stdout, and stderr for TTY fds.
// Returns an input reader (stdin if it's a TTY) and an output writer
// (preferring stderr, then stdout). Either may be nil if no TTY is found.
func findTTYs() (in io.Reader, out io.Writer) {
	if term.IsTerminal(os.Stdin.Fd()) {
		in = os.Stdin
	}
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		if term.IsTerminal(f.Fd()) {
			out = f
			break
		}
	}
	return
}

// openInputTTY opens /dev/tty as a fallback input source when stdin is
// not a TTY (e.g. when stdin is piped). Returns nil, nil if /dev/tty is
// not available.
func openInputTTY() (*os.File, error) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not open /dev/tty: %w", err)
	}
	return f, nil
}

func NewStdTerminal() *StdTerminal {
	return &StdTerminal{}
}

func (t *StdTerminal) Start(onInput func([]byte), onResize func()) error {
	t.onInput = onInput
	t.onResize = onResize
	t.stopCtx, t.stopCancel = context.WithCancel(context.Background())

	// Find TTY fds among stdin, stdout, stderr.
	in, out := findTTYs()
	if out == nil {
		return fmt.Errorf("no TTY found on stdin, stdout, or stderr")
	}
	ttyOut, ok := out.(*os.File)
	if !ok {
		return fmt.Errorf("TTY output is not an *os.File")
	}
	t.ttyFd = int(ttyOut.Fd())
	t.ttyOut = ttyOut

	// If stdin is not a TTY (e.g. piped), try /dev/tty as a fallback.
	if in == nil {
		tty, err := openInputTTY()
		if err != nil {
			return err
		}
		if tty != nil {
			in = tty
		}
	}
	if in != nil {
		t.ttyIn = in
	} else {
		t.ttyIn = os.Stdin
	}
	// Determine the input fd for raw mode. If the input reader is an
	// *os.File we use its fd; otherwise fall back to the output TTY fd
	// (which works when input and output share the same TTY).
	if f, ok := t.ttyIn.(*os.File); ok {
		t.ttyInFd = int(f.Fd())
	} else {
		t.ttyInFd = t.ttyFd
	}

	// Save and set raw mode on the input fd.
	orig, err := unix.IoctlGetTermios(t.ttyInFd, ioctlReadTermios)
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
	if err := unix.IoctlSetTermios(t.ttyInFd, ioctlWriteTermios, &raw); err != nil {
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

// readStdin reads from the TTY input forever and writes to the current inputSink.
// This goroutine lives for the process lifetime.
func (t *StdTerminal) readStdin() {
	buf := make([]byte, 4096)
	for {
		n, err := t.ttyIn.Read(buf)
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
		_ = unix.IoctlSetTermios(t.ttyInFd, ioctlWriteTermios, t.origTermios)
	}
}

func (t *StdTerminal) Write(p []byte) {
	_, _ = t.ttyOut.Write(p)
}

func (t *StdTerminal) WriteString(s string) {
	_, _ = t.ttyOut.WriteString(s)
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
	ws, err := unix.IoctlGetWinsize(t.ttyFd, unix.TIOCGWINSZ)
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
