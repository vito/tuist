package tuist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// InputListener is called with each decoded event before it reaches the
// focused component. Return true to consume the event and stop propagation.
type InputListener func(ctx Context, ev uv.Event) bool

type inputListenerEntry struct {
	fn  InputListener
	tok any // unique token for removal
}

// RenderStats captures performance metrics for a single render cycle.
type RenderStats struct {
	// RenderTime is how long it took to generate the rendered content
	// (calling Component.Render on the tree, excluding overlay compositing).
	RenderTime time.Duration

	// CompositeTime is how long overlay compositing took (column-level
	// string surgery). Zero when there are no overlays.
	CompositeTime time.Duration

	// DiffTime is how long the differential update computation took.
	DiffTime time.Duration

	// WriteTime is how long it took to write the escape sequences to
	// the terminal.
	WriteTime time.Duration

	// TotalTime is the wall-clock duration of the entire doRender call.
	TotalTime time.Duration

	// TotalLines is the total number of lines in the rendered output.
	TotalLines int

	// LinesRepainted is the number of lines that were actually written
	// to the terminal (changed lines).
	LinesRepainted int

	// CacheHits is the number of lines that matched the previous frame
	// and were skipped.
	CacheHits int

	// FullRedraw is true when the entire screen was repainted (no diff).
	FullRedraw bool

	// FullRedrawReason describes why a full redraw was triggered.
	FullRedrawReason string

	// OverlayCount is the number of active overlays composited.
	OverlayCount int

	// BytesWritten is the number of bytes sent to the terminal (escape
	// sequences + content). Large values indicate potential slowness over
	// SSH or on slow terminals.
	BytesWritten int

	// FirstChangedLine is the first line index that differed from the
	// previous frame, or -1 if nothing changed.
	FirstChangedLine int

	// LastChangedLine is the last line index that differed from the
	// previous frame, or -1 if nothing changed.
	LastChangedLine int

	// ScrollLines is how many lines the viewport scrolled this frame.
	ScrollLines int

	// ── Runtime / pprof-level metrics ──

	// HeapAlloc is the current heap allocation in bytes (live objects).
	HeapAlloc uint64

	// HeapObjects is the number of allocated heap objects.
	HeapObjects uint64

	// TotalAlloc is the cumulative bytes allocated (monotonically increasing).
	TotalAlloc uint64

	// Sys is the total memory obtained from the OS.
	Sys uint64

	// Mallocs is the number of heap allocations during this render frame.
	Mallocs uint64

	// Frees is the number of heap frees during this render frame.
	Frees uint64

	// HeapAllocDelta is the net bytes allocated during this render frame
	// (TotalAlloc delta).
	HeapAllocDelta uint64

	// NumGC is the total number of completed GC cycles.
	NumGC uint32

	// GCPauseNs is the most recent GC pause duration in nanoseconds.
	GCPauseNs uint64

	// GCCPUFraction is the fraction of CPU time used by the GC.
	GCCPUFraction float64

	// Goroutines is the number of goroutines at the time of the render.
	Goroutines int

	// StackInuse is bytes used by goroutine stacks.
	StackInuse uint64

	// HeapInuse is bytes in in-use heap spans.
	HeapInuse uint64

	// HeapIdle is bytes in idle (unused) heap spans.
	HeapIdle uint64
}

// renderStatsJSON is the JSONL record written by the debug writer.
type renderStatsJSON struct {
	Ts             int64           `json:"ts"`
	TotalUs        int64           `json:"total_us"`
	RenderUs       int64           `json:"render_us"`
	CompositeUs    int64           `json:"composite_us"`
	DiffUs         int64           `json:"diff_us"`
	WriteUs        int64           `json:"write_us"`
	TotalLines     int             `json:"total_lines"`
	LinesRepainted int             `json:"lines_repainted"`
	CacheHits      int             `json:"cache_hits"`
	FullRedraw     bool            `json:"full_redraw"`
	FullRedrawWhy  string          `json:"full_redraw_why,omitempty"`
	OverlayCount   int             `json:"overlay_count"`
	BytesWritten   int             `json:"bytes_written"`
	FirstChanged   int             `json:"first_changed"`
	LastChanged    int             `json:"last_changed"`
	ScrollLines    int             `json:"scroll_lines"`
	Components     []ComponentStat `json:"components,omitempty"`

	// Runtime / memory metrics
	HeapAlloc      uint64  `json:"heap_alloc"`
	HeapObjects    uint64  `json:"heap_objects"`
	TotalAlloc     uint64  `json:"total_alloc"`
	Sys            uint64  `json:"sys"`
	Mallocs        uint64  `json:"mallocs"`
	Frees          uint64  `json:"frees"`
	HeapAllocDelta uint64  `json:"heap_alloc_delta"`
	NumGC          uint32  `json:"num_gc"`
	GCPauseNs      uint64  `json:"gc_pause_ns"`
	GCCPUFraction  float64 `json:"gc_cpu_fraction"`
	Goroutines     int     `json:"goroutines"`
	StackInuse     uint64  `json:"stack_inuse"`
	HeapInuse      uint64  `json:"heap_inuse"`
	HeapIdle       uint64  `json:"heap_idle"`
}

// TUI is the main renderer. It extends Container with differential rendering
// on the normal scrollback buffer.
//
// All component state — including Render(), HandleKeyPress(), and any
// Dispatch callbacks — runs on a single UI goroutine. Components never
// need locks for their own fields.
type TUI struct {
	Container

	terminal Terminal

	// mu protects fields shared between the main goroutine and the UI
	// goroutine: fullRedrawCount, kittyKeyboard.
	// All rendering and component state is owned by the UI goroutine.
	mu sync.Mutex

	// ── mu-protected state (shared between goroutines) ──

	fullRedrawCount      int
	kittyKeyboard        bool
	syncOutputSupported  bool  // true until DECRPM says otherwise
	syncOutputQueryState uint8 // 0=unsent, 1=sent, 2=answered
	altScreen            bool  // true when using alt screen (no sync output)

	// ── UI-goroutine-only state (no lock needed) ──

	previousLines    []string
	previousWidth    int
	focusedComponent Component
	inputListeners   []inputListenerEntry

	frameDir            string // TUIST_FRAMES: dump each frame to this dir
	frameNum            int
	cursorRow           int
	hardwareCursorRow   int
	hardwareCursorCol   int // last written cursor column (1-indexed terminal column)
	showHardwareCursor  bool
	cursorHidden        bool // tracks whether we've sent HideCursor to the terminal
	clearOnShrink       bool
	maxLinesRendered    int
	previousViewportTop int
	screenHeight        int // snapshotted once per frame from terminal.Rows()

	overlayStack []*overlayEntry

	mouseRefCount int // number of mounted MouseEnabled components
	pasteRefCount int // number of mounted Pasteable components

	// Positional mouse dispatch: zones rebuilt after each render by scanning markers.
	mouseZones      []mouseZone
	markerIDs       map[int64]Component  // marker ID → component, maintained on mount/dismount
	trackedZones    map[int64]*mouseZone // reused open-marker tracker
	ansiParser      *ansi.Parser         // reused ANSI parser for zone scanning
	lastMouseTarget Component            // for hover enter/leave tracking

	debugWriter  io.Writer    // if non-nil, render stats are logged here
	envDebugFile *os.File     // opened by Start from TUIST_LOG; closed by Stop
	writeBuf     bytes.Buffer // reused across frames for terminal output

	// inputPipe feeds raw bytes from the terminal reader goroutine
	// into the TerminalReader, which handles escape sequence
	// decoding, bracketed paste buffering, and escape timeouts.
	inputPipe *io.PipeWriter

	// ── Event loop channels ──

	eventCh    chan uv.Event // decoded input events from terminal
	dispatchMu sync.Mutex    // protects dispatchQ
	dispatchQ  []func()      // closures to run on UI goroutine
	dispatchCh chan struct{} // capacity-1 signal: "dispatchQ is non-empty"
	renderCh   chan struct{} // coalesced render requests
	loopDone   chan struct{} // closed when runLoop exits

	// ── Lifecycle ──

	stopCtx    context.Context    // cancelled by Stop() to signal shutdown
	stopCancel context.CancelFunc // cancels stopCtx
}

// New creates a TUI backed by the given terminal. No goroutines are
// started; call [TUI.Start] to begin the interactive event loop, or
// use [TUI.RenderOnce] for synchronous single-frame rendering.
func New(term Terminal) *TUI {
	t := &TUI{
		terminal:     term,
		cursorHidden: true, // assume hidden until explicitly shown
		eventCh:      make(chan uv.Event, 64),
		dispatchCh:   make(chan struct{}, 1),
		renderCh:     make(chan struct{}, 1),
	}
	// Wire upward propagation: when any child calls Update(), the root
	// Compo's requestRender triggers TUI.RequestRender.
	t.requestRender = func() {
		t.RequestRender(false)
	}
	// Mount the TUI's own Container so children added via AddChild
	// are automatically mounted (receiving lifecycle hooks).
	t.tui = t
	t.self = &t.Container
	t.mountCtx = context.Background()
	// mountCancel is nil for the root — it's never dismounted.
	return t
}

// runLoop is the unified event loop. It processes input events, dispatched
// closures, and render requests — all on a single goroutine. This is the
// "UI goroutine": all component state (Render, HandleKeyPress, Dispatch
// callbacks) executes here, so components never need locks.
func (t *TUI) runLoop() {
	defer close(t.loopDone)
	for {
		// Wait for any signal.
		select {
		case <-t.stopCtx.Done():
			return
		case ev := <-t.eventCh:
			t.dispatchEvent(ev)
		case <-t.dispatchCh:
			t.drainDispatchQ()
		case <-t.renderCh:
			// fall through to drain + render
		}

		t.drainAll()
		t.doRender()
	}
}

// drainAll drains all pending events and dispatches before rendering.
// This coalesces rapid input and multiple dispatches into one frame.
// Called only on the UI goroutine.
func (t *TUI) drainAll() {
	for {
		select {
		case <-t.stopCtx.Done():
			return
		case ev := <-t.eventCh:
			t.dispatchEvent(ev)
		case <-t.dispatchCh:
			t.drainDispatchQ()
		default:
			return
		}
	}
}

// drainDispatchQ runs all queued dispatch functions in order.
// Called only on the UI goroutine.
func (t *TUI) drainDispatchQ() {
	t.dispatchMu.Lock()
	q := t.dispatchQ
	t.dispatchQ = nil
	t.dispatchMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

// HasKittyKeyboard reports whether the terminal confirmed support for the
// Kitty keyboard protocol (disambiguate escape codes). This is determined
// by the response to the RequestKittyKeyboard query sent during Start().
// Returns false until the response is received.
//
// Safe to call from any goroutine.
func (t *TUI) HasKittyKeyboard() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kittyKeyboard
}

// SetDebugWriter enables render performance logging. Each render cycle
// writes a single stats line to w. Pass nil to disable.
//
// Safe to call from any goroutine; takes effect on next render.
func (t *TUI) SetDebugWriter(w io.Writer) {
	t.Dispatch(func() {
		t.debugWriter = w
	})
}

// FullRedraws returns the number of full (non-differential) redraws performed.
//
// Safe to call from any goroutine.
func (t *TUI) FullRedraws() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fullRedrawCount
}

// SetSyncOutput overrides synchronized output detection. Use this to
// force sync output on or off, e.g. in tests or when the terminal is
// known to support it but doesn't respond to DECRQM.
//
// Safe to call from any goroutine.
func (t *TUI) SetSyncOutput(supported bool) {
	t.mu.Lock()
	t.syncOutputSupported = supported
	t.syncOutputQueryState = 2 // treat as answered
	t.mu.Unlock()
}

// enterAltScreen switches to the alternate screen buffer. Used when
// synchronized output is not available, since alt screen gives us
// absolute cursor positioning and eliminates stale-line artifacts.
func (t *TUI) enterAltScreen() {
	t.terminal.WriteString(escAltScreenEnter)
	t.terminal.WriteString(escCursorHome)
	t.altScreen = true
	// Reset rendering state — alt screen is a blank slate.
	t.previousLines = nil
	t.previousWidth = -1
	t.cursorRow = 0
	t.hardwareCursorRow = 0
	t.hardwareCursorCol = 0
	t.maxLinesRendered = 0
	t.previousViewportTop = 0
}

// leaveAltScreen switches back to the normal screen buffer.
func (t *TUI) leaveAltScreen() {
	if !t.altScreen {
		return
	}
	t.terminal.WriteString(escAltScreenLeave)
	t.altScreen = false
}

// HasSyncOutput reports whether the terminal supports synchronized output
// (DEC private mode 2026). This is queried via DECRQM at startup; until
// the response arrives, it returns false (safe default for terminals
// that don't implement DECRQM and never reply).
//
// When the terminal does not support synchronized output, full redraws
// are avoided (they would flicker) and only the visible region is
// repainted.
//
// Safe to call from any goroutine.
func (t *TUI) HasSyncOutput() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.syncOutputSupported
}

// SetShowHardwareCursor enables or disables the hardware cursor.
// When enabled, the terminal cursor is positioned at the component's
// reported cursor location and made visible; this is used for IME
// input and cursor-position tests.
func (t *TUI) SetShowHardwareCursor(enabled bool) {
	if t.showHardwareCursor == enabled {
		return
	}
	t.showHardwareCursor = enabled
	if !enabled && !t.cursorHidden {
		t.terminal.HideCursor()
		t.cursorHidden = true
	}
}

// enableMouse registers a MouseEnabled component, incrementing the mouse
// reference count and enabling terminal mouse reporting if this is the
// first such component. The component's marker ID is eagerly allocated
// and added to the marker map so that scanMouseZones can skip the
// per-frame tree walk.
func (t *TUI) enableMouse(comp Component) {
	t.mouseRefCount++
	if t.mouseRefCount == 1 {
		t.terminal.WriteString(escMouseAllMotionEnable)
		t.terminal.WriteString(escMouseSGREnable)
	}
	// Eagerly allocate marker ID and register in the map.
	markerOf(comp) // ensures markerID is set
	mid := comp.compo().markerID.Load()
	if t.markerIDs == nil {
		t.markerIDs = make(map[int64]Component)
	}
	t.markerIDs[mid] = comp
}

// disableMouse unregisters a MouseEnabled component, decrementing the
// mouse reference count and disabling terminal mouse reporting when the
// last such component is dismounted.
func (t *TUI) disableMouse(comp Component) {
	t.mouseRefCount--
	if t.mouseRefCount <= 0 {
		t.mouseRefCount = 0
		t.terminal.WriteString(escMouseSGRDisable)
		t.terminal.WriteString(escMouseAllMotionDisable)
	}
	// Remove from marker map.
	if mid := comp.compo().markerID.Load(); mid != 0 {
		delete(t.markerIDs, mid)
	}
}

// enablePaste registers a Pasteable component, incrementing the paste
// reference count and enabling bracketed paste mode if this is the first.
func (t *TUI) enablePaste() {
	t.pasteRefCount++
	if t.pasteRefCount == 1 {
		t.terminal.WriteString("\x1b[?2004h")
	}
}

// disablePaste unregisters a Pasteable component, decrementing the paste
// reference count and disabling bracketed paste when the last such
// component is dismounted.
func (t *TUI) disablePaste() {
	t.pasteRefCount--
	if t.pasteRefCount <= 0 {
		t.pasteRefCount = 0
		t.terminal.WriteString("\x1b[?2004l")
	}
}

// SetFocus gives keyboard focus to the given component (or nil).
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) SetFocus(comp Component) {
	if f, ok := t.focusedComponent.(Focusable); ok {
		f.SetFocused(t.contextFor(t.focusedComponent), false)
	}
	t.focusedComponent = comp
	if f, ok := comp.(Focusable); ok {
		f.SetFocused(t.contextFor(comp), true)
	}
}

// contextFor constructs a Context for the given component, using its
// mount context if available.
func (t *TUI) contextFor(comp Component) Context {
	ctx := context.Background()
	if comp != nil {
		cp := comp.compo()
		if cp.mountCtx != nil {
			ctx = cp.mountCtx
		}
	}
	return Context{
		Context: ctx,
		tui:     t,
		source:  comp,
	}
}

// AddInputListener registers a listener that intercepts input before it
// reaches the focused component. Returns a function that removes it.
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) AddInputListener(l InputListener) func() {
	type token struct{}
	tok := &token{}
	t.inputListeners = append(t.inputListeners, inputListenerEntry{fn: l, tok: tok})
	return func() {
		t.Dispatch(func() {
			for i, entry := range t.inputListeners {
				if entry.tok == tok {
					t.inputListeners = append(t.inputListeners[:i], t.inputListeners[i+1:]...)
					return
				}
			}
		})
	}
}

// ShowOverlay displays a component as an overlay on top of the base content.
// Focus is not changed; use [TUI.SetFocus] to direct input to the overlay's
// component when needed.
// Must be called on the UI goroutine (from an event handler or Dispatch).
func (t *TUI) ShowOverlay(comp Component, opts *OverlayOptions) *OverlayHandle {
	entry := &overlayEntry{
		component: comp,
		options:   opts,
	}
	t.overlayStack = append(t.overlayStack, entry)
	t.RequestRender(false)
	return &OverlayHandle{tui: t, entry: entry}
}

// Dispatch schedules a function to run on the UI goroutine. Use this to
// mutate component state from background goroutines (e.g. after async I/O).
// The function runs before the next render, serialized with all input
// handling and other dispatched functions.
//
// Safe to call from any goroutine.
func (t *TUI) Dispatch(fn func()) {
	t.dispatchMu.Lock()
	t.dispatchQ = append(t.dispatchQ, fn)
	t.dispatchMu.Unlock()
	// Signal the event loop that work is available.
	select {
	case t.dispatchCh <- struct{}{}:
	default: // already signaled
	}
	// Also signal a render so the event loop wakes up.
	t.RequestRender(false)
}

// Start begins the TUI event loop and puts the terminal into raw mode.
// The event loop runs on a background goroutine; all component state
// mutations (Render, HandleKeyPress, Dispatch callbacks) are serialized
// on that goroutine. Call [TUI.Stop] to end the loop and restore the
// terminal.
//
// For synchronous/headless rendering without an event loop, use
// [TUI.RenderOnce] instead.
//
// If the TUIST_LOG environment variable is set, Start automatically opens
// the specified file path for render debug logging (JSONL format). This
// is equivalent to calling SetDebugWriter with the opened file. The file
// is created/truncated on each Start call and closed on Stop.
func (t *TUI) Start() error {
	// If a previous loop is still running, stop it first.
	if t.loopDone != nil {
		select {
		case <-t.loopDone:
			// Already exited.
		default:
			t.stopCancel()
			<-t.loopDone
		}
	}

	t.stopCtx, t.stopCancel = context.WithCancel(context.Background())
	t.loopDone = make(chan struct{})

	// Auto-configure frame dumping from environment.
	if dir := os.Getenv("TUIST_FRAMES"); dir != "" {
		_ = os.MkdirAll(dir, 0755)
		t.frameDir = dir
		t.frameNum = 0
	}

	// Auto-configure debug logging from environment.
	if logPath := os.Getenv("TUIST_LOG"); logPath != "" && t.debugWriter == nil {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("open TUIST_LOG %q: %w", logPath, err)
		}
		t.debugWriter = f
		t.envDebugFile = f
	}

	t.inputPipe = t.startInputReader(t.stopCtx)

	err := t.terminal.Start(
		func(data []byte) {
			// Feed raw bytes into the TerminalReader via the pipe.
			_, _ = t.inputPipe.Write(data)
		},
		func() { t.RequestRender(false) },
	)
	if err != nil {
		return err
	}

	// Hide cursor and mark state before starting the run loop so
	// the UI goroutine sees consistent cursorHidden state (no race).
	t.terminal.HideCursor()
	t.cursorHidden = true

	// Check for explicit override via environment variable.
	// TUIST_SYNC=0 disables synchronized output (avoids flicker on
	// terminals that silently ignore mode 2026).
	// TUIST_SYNC=1 forces it on (skip the DECRQM query).
	if v := os.Getenv("TUIST_SYNC"); v == "0" {
		t.mu.Lock()
		t.syncOutputSupported = false
		t.syncOutputQueryState = 2 // answered (overridden)
		t.mu.Unlock()
		t.enterAltScreen()
	} else if v == "1" {
		t.mu.Lock()
		t.syncOutputSupported = true
		t.syncOutputQueryState = 2 // answered (overridden)
		t.mu.Unlock()
	} else {
		// Query synchronized output support. The response (DECRPM)
		// is handled in dispatchEvent. Until we hear back, we assume
		// sync output is NOT supported — terminals that don't
		// implement DECRQM won't reply at all.
		t.terminal.WriteString(ansi.RequestSynchronizedOutputMode)
		t.mu.Lock()
		t.syncOutputQueryState = 1 // sent
		t.mu.Unlock()
	}

	// Start the run loop after terminal.Start() so that the terminal's
	// fields (e.g. ttyOut) are fully initialized before the loop goroutine
	// can attempt to write to them.
	go t.runLoop()

	t.RequestRender(false)
	return nil
}

// Stop ends the TUI event loop and restores the terminal.
func (t *TUI) Stop() {
	t.stop(false)
}

func (t *TUI) stop(clear bool) {
	// Signal the event loop to stop and wait for it to finish any
	// in-progress work.
	if t.stopCancel != nil {
		t.stopCancel()
	}
	// Close the input pipe so the TerminalReader's StreamEvents
	// goroutine exits.
	if t.inputPipe != nil {
		_ = t.inputPipe.Close()
		t.inputPipe = nil
	}
	if t.loopDone != nil {
		<-t.loopDone
	}

	prev := t.previousLines
	hcr := t.hardwareCursorRow

	// Disable bracketed paste before restoring terminal.
	if t.pasteRefCount > 0 {
		t.terminal.WriteString("\x1b[?2004l")
		t.pasteRefCount = 0
	}

	// Disable mouse tracking before restoring terminal.
	if t.mouseRefCount > 0 {
		t.terminal.WriteString(escMouseSGRDisable)
		t.terminal.WriteString(escMouseAllMotionDisable)
		t.mouseRefCount = 0
	}

	if t.altScreen {
		// Leave alt screen — the normal screen buffer is restored
		// automatically by the terminal.
		t.leaveAltScreen()
	} else if len(prev) > 0 {
		if clear {
			// Move cursor to the top of the TUI output and erase
			// everything below so the executed command starts clean.
			t.terminal.WriteString(cursorVertical(-hcr))
			t.terminal.WriteString("\r\x1b[J")
		} else {
			// Move cursor past content so the shell prompt appears below.
			target := len(prev)
			t.terminal.WriteString(cursorVertical(target - hcr))
			t.terminal.WriteString("\r\n")
		}
	}

	// Ensure cursor is at column 0 for clean shell handoff.
	t.terminal.WriteString("\r")
	t.terminal.ShowCursor()
	t.terminal.Stop()

	// Close environment-opened debug log file.
	if t.envDebugFile != nil {
		_ = t.envDebugFile.Close()
		t.envDebugFile = nil
		t.debugWriter = nil
	}
}

// Exec suspends the TUI and runs fn with exclusive access to the
// terminal. Input from stdin is piped to in; out and errOut are
// connected to stdout and stderr respectively. When fn returns, the
// TUI is automatically restarted.
//
// This is the equivalent of bubbletea's ExecCommand. The terminal's
// single reader goroutine remains the sole consumer of os.Stdin;
// fn reads from a pipe that receives the forwarded bytes.
func (t *TUI) Exec(fn func(in io.Reader, out io.Writer, errOut io.Writer) error) error {
	t.stop(true)

	pr, pw := io.Pipe()
	t.terminal.SetInputPassthrough(pw)

	err := fn(pr, os.Stdout, os.Stderr)

	_ = pw.Close()
	t.terminal.SetInputPassthrough(nil)
	return errors.Join(err, t.Start())
}

// RequestRender schedules a render on the next iteration. If repaint is
// true, all cached state is discarded and a full repaint occurs.
//
// Safe to call from any goroutine.
func (t *TUI) RequestRender(repaint bool) {
	if repaint {
		// Repaint resets are dispatched to the UI goroutine to avoid races.
		t.Dispatch(func() {
			t.previousLines = nil
			t.previousWidth = -1
			t.cursorRow = 0
			t.hardwareCursorRow = 0
			t.hardwareCursorCol = 0
			t.maxLinesRendered = 0
			t.previousViewportTop = 0
		})
	}

	// Non-blocking send to coalesce multiple rapid requests.
	select {
	case t.renderCh <- struct{}{}:
	default:
	}
}

// ---------- input handling --------------------------------------------------

// startInputReader creates a TerminalReader backed by a pipe and
// streams decoded events (including properly buffered bracketed paste)
// to eventCh. Returns the write end of the pipe for the terminal's
// stdin reader to feed bytes into.
func (t *TUI) startInputReader(ctx context.Context) *io.PipeWriter {
	pr, pw := io.Pipe()
	reader := uv.NewTerminalReader(pr, os.Getenv("TERM"))
	go func() {
		defer pr.Close()
		// StreamEvents blocks until ctx is cancelled or the reader
		// hits an error (e.g. pipe closed). It handles escape
		// timeouts, bracketed paste buffering, terminfo lookups, etc.
		_ = reader.StreamEvents(ctx, t.eventCh)
	}()
	return pw
}

func (t *TUI) dispatchEvent(ev uv.Event) {
	// Construct Context for the focused component.
	comp := t.focusedComponent
	ctx := t.contextFor(comp)

	for _, entry := range t.inputListeners {
		if entry.fn(ctx, ev) {
			return
		}
	}

	switch e := ev.(type) {
	case uv.ModeReportEvent:
		if e.Mode == ansi.SynchronizedOutputMode {
			supported := e.Value.IsSet() || e.Value.IsReset()
			t.mu.Lock()
			t.syncOutputSupported = supported
			t.syncOutputQueryState = 2 // answered
			t.mu.Unlock()
			if !supported && !t.altScreen {
				t.enterAltScreen()
			}
		}
		return
	case uv.KeyboardEnhancementsEvent:
		// Update under lock for HasKittyKeyboard() which may be called
		// from any goroutine.
		t.mu.Lock()
		t.kittyKeyboard = e.SupportsKeyDisambiguation()
		t.mu.Unlock()
		return
	case uv.KeyPressEvent:
		t.bubbleKeyPress(comp, ctx, e)
	case uv.PasteEvent:
		t.bubblePaste(comp, ctx, e)
	case uv.MouseEvent:
		// Positional dispatch: deliver mouse events to the component
		// under the cursor rather than the focused component. Falls
		// back to focus-based dispatch when MouseEnabled overlays are
		// active (their positions aren't in the content-tree hit map).
		if t.hasMouseEnabledOverlay() {
			m := e.Mouse()
			me := MouseEvent{MouseEvent: e, Row: m.Y, Col: m.X}
			t.bubbleMouse(comp, ctx, me)
		} else {
			t.dispatchMousePositional(e)
		}
	}
}

// bubbleKeyPress delivers a key event to the focused component and, if
// not consumed, walks up the parent chain giving each Interactive
// ancestor a chance to handle it.
func (t *TUI) bubbleKeyPress(comp Component, ctx Context, ev uv.KeyPressEvent) {
	if comp == nil {
		return
	}

	// Focused component gets first shot.
	if ic, ok := comp.(Interactive); ok {
		if ic.HandleKeyPress(ctx, ev) {
			return
		}
	}

	// Bubble up through parents.
	cp := comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if ic, ok := cp.self.(Interactive); ok {
				if ic.HandleKeyPress(ctx, ev) {
					return
				}
			}
		}
		cp = cp.parent
	}
}

// bubbleMouse delivers a mouse event to the focused component and, if
// not consumed, walks up the parent chain giving each MouseEnabled
// ancestor a chance to handle it. Used as the fallback when
// MouseEnabled overlays are active.
func (t *TUI) bubbleMouse(comp Component, ctx Context, ev MouseEvent) {
	if comp == nil {
		return
	}

	// Focused component gets first shot.
	if mc, ok := comp.(MouseEnabled); ok {
		if mc.HandleMouse(ctx, ev) {
			return
		}
	}

	// Bubble up through parents.
	cp := comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if mc, ok := cp.self.(MouseEnabled); ok {
				if mc.HandleMouse(ctx, ev) {
					return
				}
			}
		}
		cp = cp.parent
	}
}

// bubblePaste delivers a paste event with the same bubbling logic as
// key presses.
func (t *TUI) bubblePaste(comp Component, ctx Context, ev uv.PasteEvent) {
	if comp == nil {
		return
	}

	if p, ok := comp.(Pasteable); ok {
		if p.HandlePaste(ctx, ev) {
			return
		}
	}

	// Bubble up through parents.
	cp := comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if p, ok := cp.self.(Pasteable); ok {
				if p.HandlePaste(ctx, ev) {
					return
				}
			}
		}
		cp = cp.parent
	}
}

// ---------- positional mouse dispatch ----------------------------------------

// mouseZone is a rectangular region in the rendered output belonging to
// a MouseEnabled component. Built by scanning zone markers after each
// render. Zones can be full-width (tree components auto-marked by
// Container/Slot) or column-bounded (inline content wrapped with [Mark]).
type mouseZone struct {
	comp     Component
	startRow int
	startCol int
	endRow   int // inclusive
	endCol   int // exclusive
}

// scanMouseZones scans rendered lines for zone markers, building the
// mouse hit map. Returns a new slice with markers stripped (for diffing
// and terminal output). The original lines (with markers) are not
// modified.
//
// Uses [ansi.DecodeSequence] for correct ANSI parsing and Unicode
// grapheme/width handling — no hand-rolled rune or escape parsing.
func (t *TUI) scanMouseZones(lines []string) []string {
	t.mouseZones = t.mouseZones[:0]

	// Fast path: no MouseEnabled components are mounted, so no markers
	// were emitted and there is nothing to scan or strip. This avoids
	// the rebuildMarkerMap tree walk entirely.
	if t.mouseRefCount == 0 {
		return lines
	}

	markerMap := t.markerIDs

	// Fast path: no mouse-enabled components have markers, so there is
	// nothing to scan or strip.
	if len(markerMap) == 0 {
		return lines
	}

	// Reuse open-marker tracker.
	if t.trackedZones == nil {
		t.trackedZones = make(map[int64]*mouseZone)
	} else {
		clear(t.trackedZones)
	}
	tracked := t.trackedZones

	// Reuse ANSI parser.
	if t.ansiParser == nil {
		t.ansiParser = ansi.NewParser()
	}
	p := t.ansiParser

	stripped := make([]string, len(lines))

	for row, line := range lines {
		// Fast path: lines without ESC cannot contain zone markers (or
		// any ANSI sequences), so pass them through without parsing.
		if strings.IndexByte(line, '\x1b') < 0 {
			stripped[row] = line
			continue
		}

		clean := make([]byte, 0, len(line))
		col := 0 // visible column counter
		var state byte
		input := line
		for len(input) > 0 {
			seq, width, n, newState := ansi.DecodeSequence(input, state, p)
			state = newState

			if width == 0 && ansi.Cmd(p.Command()).Final() == 'z' {
				// Zone marker: ESC[<id>z
				params := p.Params()
				if len(params) > 0 {
					id := int64(params[0].Param(0))
					if comp := markerMap[id]; comp != nil {
						if z, ok := tracked[id]; ok {
							z.endRow = row
							z.endCol = col
							t.mouseZones = append(t.mouseZones, *z)
							delete(tracked, id)
						} else {
							tracked[id] = &mouseZone{
								comp:     comp,
								startRow: row,
								startCol: col,
							}
						}
					}
				}
				// Strip marker — don't append to clean.
			} else if width > 0 {
				// Visible content: advance column, keep in output.
				col += width
				clean = append(clean, seq...)
			} else {
				// Non-marker escape sequence: keep verbatim.
				clean = append(clean, seq...)
			}

			input = input[n:]
		}
		stripped[row] = string(clean)
	}

	return stripped
}

// dispatchMousePositional finds the deepest (last-scanned) MouseEnabled
// zone containing the mouse position and dispatches the event with
// zone-relative coordinates.
func (t *TUI) dispatchMousePositional(ev uv.MouseEvent) {
	m := ev.Mouse()
	contentY := t.previousViewportTop + m.Y

	// Find the deepest (innermost) zone containing the cursor.
	// Zones are appended in scan order; inner zones come after outer
	// ones, so the last match is the deepest.
	var best *mouseZone
	for i := range t.mouseZones {
		z := &t.mouseZones[i]
		if !zoneContains(z, contentY, m.X) {
			continue
		}
		best = z
	}

	// Hover enter/leave tracking.
	var target Component
	if best != nil {
		target = best.comp
	}
	t.updateMouseHover(target)

	if best == nil {
		// Nothing under cursor — focus-based fallback (e.g. overlays).
		comp := t.focusedComponent
		if comp != nil {
			me := MouseEvent{MouseEvent: ev, Row: m.Y, Col: m.X}
			t.bubbleMouse(comp, t.contextFor(comp), me)
		}
		return
	}

	me := MouseEvent{
		MouseEvent: ev,
		Row:        contentY - best.startRow,
		Col:        m.X - best.startCol,
	}
	ctx := t.contextFor(best.comp)

	if mc, ok := best.comp.(MouseEnabled); ok {
		if mc.HandleMouse(ctx, me) {
			return
		}
	}

	// Bubble to parent zones (outer zones containing this point).
	cp := best.comp.compo().parent
	for cp != nil {
		if cp.self != nil {
			if mc, ok := cp.self.(MouseEnabled); ok {
				// Find this parent's zone for relative coords.
				pz := t.findZone(cp.self, contentY, m.X)
				var pme MouseEvent
				if pz != nil {
					pme = MouseEvent{MouseEvent: ev, Row: contentY - pz.startRow, Col: m.X - pz.startCol}
				} else {
					pme = MouseEvent{MouseEvent: ev, Row: contentY, Col: m.X}
				}
				if mc.HandleMouse(t.contextFor(cp.self), pme) {
					return
				}
			}
		}
		cp = cp.parent
	}
}

// findZone finds the zone for a specific component containing the given point.
func (t *TUI) findZone(comp Component, row, col int) *mouseZone {
	for i := range t.mouseZones {
		z := &t.mouseZones[i]
		if z.comp == comp && zoneContains(z, row, col) {
			return z
		}
	}
	return nil
}

// zoneContains reports whether (row, col) is inside a zone's bounding
// rectangle. Column bounds are checked on every row, not just the first
// and last — zones are rectangular regions, not flowing text ranges.
func zoneContains(z *mouseZone, row, col int) bool {
	if row < z.startRow || row > z.endRow {
		return false
	}
	if col < z.startCol || col >= z.endCol {
		return false
	}
	return true
}

// updateMouseHover fires Hoverable.SetHovered transitions when the
// mouse moves to a different component (or away from all components).
func (t *TUI) updateMouseHover(target Component) {
	if target == t.lastMouseTarget {
		return
	}
	if t.lastMouseTarget != nil {
		if h, ok := t.lastMouseTarget.(Hoverable); ok {
			h.SetHovered(t.contextFor(t.lastMouseTarget), false)
		}
	}
	t.lastMouseTarget = target
	if target != nil {
		if h, ok := target.(Hoverable); ok {
			h.SetHovered(t.contextFor(target), true)
		}
	}
}

// hasMouseEnabledOverlay reports whether any visible overlay implements
// MouseEnabled. Used to decide between positional and focus-based dispatch.
func (t *TUI) hasMouseEnabledOverlay() bool {
	for _, o := range t.overlayStack {
		if o.hidden {
			continue
		}
		if _, ok := o.component.(MouseEnabled); ok {
			return true
		}
	}
	return false
}

// ---------- escape sequences ------------------------------------------------
//
// Named constants for ANSI/DEC escape sequences used during rendering.
// Using constants makes the intent clear and avoids scattered string literals.

const (
	// Synchronized output (DEC private mode 2026). Tells the terminal to
	// buffer writes and flush atomically, preventing flicker.
	escSyncBegin = "\x1b[?2026h"
	escSyncEnd   = "\x1b[?2026l"

	// Screen / scrollback clearing.
	escClearScrollback = "\x1b[3J" // erase scrollback buffer
	escClearScreen     = "\x1b[2J" // erase visible screen
	escCursorHome      = "\x1b[H"  // move cursor to (1,1)

	// Line-level operations.
	escClearLine = "\x1b[2K" // erase entire current line

	// Mouse tracking (SGR extended mode with all-motion tracking).
	// These enable/disable mouse event reporting from the terminal.
	escMouseAllMotionEnable  = "\x1b[?1003h" // any-event (motion + buttons)
	escMouseAllMotionDisable = "\x1b[?1003l"
	escMouseSGREnable        = "\x1b[?1006h" // SGR extended encoding
	escMouseSGRDisable       = "\x1b[?1006l"

	// Alternate screen buffer.
	escAltScreenEnter = "\x1b[?1049h"
	escAltScreenLeave = "\x1b[?1049l"
)

// hasSyncOutput returns whether the terminal supports synchronized output.
// Must only be called from the UI goroutine.
func (t *TUI) hasSyncOutput() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.syncOutputSupported
}

// syncBegin returns the synchronized output begin sequence, or empty
// string if the terminal does not support it.
func (t *TUI) syncBegin() string {
	if t.hasSyncOutput() {
		return escSyncBegin
	}
	return ""
}

// syncEnd returns the synchronized output end sequence, or empty
// string if the terminal does not support it.
func (t *TUI) syncEnd() string {
	if t.hasSyncOutput() {
		return escSyncEnd
	}
	return ""
}

// cursorTo returns an escape sequence moving the cursor to row, col (1-indexed).
func cursorTo(row, col int) string {
	return fmt.Sprintf("\x1b[%d;%dH", row, col)
}

// cursorUp returns an escape sequence moving the cursor up n rows.
// Returns "" if n <= 0.
func cursorUp(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dA", n)
}

// cursorDown returns an escape sequence moving the cursor down n rows.
// Returns "" if n <= 0.
func cursorDown(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dB", n)
}

// cursorColumn returns an escape sequence moving the cursor to column col
// (1-indexed, as the terminal expects).
func cursorColumn(col int) string {
	return fmt.Sprintf("\x1b[%dG", col)
}

// cursorVertical writes cursor-up or cursor-down for a signed delta.
func cursorVertical(delta int) string {
	if delta > 0 {
		return cursorDown(delta)
	}
	return cursorUp(-delta)
}

// ---------- differential rendering ------------------------------------------

// diffResult holds the output of diffLines.
type diffResult struct {
	firstChanged int  // first line that differs, or -1
	lastChanged  int  // last line that differs, or -1
	appendStart  bool // true if changes are purely appended lines starting right after prevLines
}

// diffLines compares old and new line slices and returns the range of changed
// lines. Pure function — no side effects.
func diffLines(prev, next []string) diffResult {
	firstChanged := -1
	lastChanged := -1
	n := max(len(next), len(prev))
	for i := range n {
		var oldLine, newLine string
		if i < len(prev) {
			oldLine = prev[i]
		}
		if i < len(next) {
			newLine = next[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}

	appended := len(next) > len(prev)
	if appended {
		if firstChanged == -1 {
			firstChanged = len(prev)
		}
		lastChanged = len(next) - 1
	}
	appendStart := appended && firstChanged == len(prev) && firstChanged > 0

	return diffResult{
		firstChanged: firstChanged,
		lastChanged:  lastChanged,
		appendStart:  appendStart,
	}
}

// RenderOnce performs a single synchronous render cycle, writing the
// output directly to the underlying Terminal. This is useful for
// headless rendering, testing, or any scenario where you want to
// produce exactly one frame without running the event loop.
//
// Must not be called concurrently with the event loop (i.e., don't
// call this while Start() is active).
func (t *TUI) RenderOnce() {
	t.doRender()
}

func (t *TUI) doRender() {
	totalStart := time.Now()

	// Snapshot memory stats at frame start for per-frame delta tracking.
	var memBefore runtime.MemStats
	if t.debugWriter != nil {
		runtime.ReadMemStats(&memBefore)
	}

	// Capture terminal dimensions once per frame. These come from the
	// terminal (protected by its own lock, updated on SIGWINCH) and
	// should not change mid-frame.
	width := t.terminal.Columns()
	height := t.terminal.Rows()
	t.screenHeight = height

	var stats RenderStats
	stats.OverlayCount = len(t.overlayStack)

	// Render all components (output contains zone markers).
	newLines, cursorPos, compStats := t.renderFrame(width, height, &stats)

	// Scan zone markers to build the mouse hit map, then strip markers
	// so the diff engine and terminal see clean output.
	displayLines := t.scanMouseZones(newLines)

	// Dump frame for debugging if TUIST_FRAMES is set.
	if t.frameDir != "" {
		t.dumpFrame(displayLines, height)
	}

	// Choose rendering strategy and write to terminal.
	if t.altScreen {
		t.applyFrameAltScreen(width, height, displayLines, cursorPos, compStats, &stats, totalStart, &memBefore)
	} else {
		t.applyFrame(width, height, displayLines, cursorPos, compStats, &stats, totalStart, &memBefore)
	}
}

// renderFrame renders the component tree and composites overlays, producing
// the new set of output lines with reset sequences appended.
func (t *TUI) renderFrame(width, height int, stats *RenderStats) ([]string, *CursorPos, []ComponentStat) {
	renderStart := time.Now()
	ctx := Context{
		Context:        context.Background(),
		tui:            t,
		Width:          width,
		viewportTop:    max(0, t.maxLinesRendered-height),
		viewportHeight: height,
	}
	var compStats []ComponentStat
	if t.debugWriter != nil {
		t.componentStats = &compStats
	}
	baseResult := renderComponent(&t.Container, ctx)
	cursorPos := baseResult.Cursor
	stats.RenderTime = time.Since(renderStart)

	var newLines []string
	// Composite overlays (needs a mutable copy of the lines slice).
	if len(t.overlayStack) > 0 {
		newLines = make([]string, len(baseResult.Lines))
		copy(newLines, baseResult.Lines)
		compositeStart := time.Now()
		newLines, cursorPos = t.compositeOverlays(
			newLines, cursorPos, t.overlayStack,
			width, height,
		)
		stats.CompositeTime = time.Since(compositeStart)
	} else {
		// No overlays — safe to use the cached slice directly.
		newLines = baseResult.Lines
	}

	stats.TotalLines = len(newLines)

	// Truncate lines that exceed the terminal width so they don't wrap
	// to the next physical row (which would break the diff renderer's
	// line-count assumptions). Use a fast len() pre-check to skip the
	// ANSI-aware truncation for lines that are obviously short enough.
	if width > 0 {
		for i, line := range newLines {
			if len(line) > width {
				newLines[i] = ansi.Truncate(line, width, "")
			}
		}
	}

	return newLines, cursorPos, compStats
}

// applyFrame decides the rendering strategy (full redraw vs differential
// update) and writes the result to the terminal.
func (t *TUI) applyFrame(width, height int, newLines []string, cursorPos *CursorPos, compStats []ComponentStat, stats *RenderStats, totalStart time.Time, memBefore *runtime.MemStats) {
	emitStats := func() {
		t.emitDebugStats(t.debugWriter, stats, compStats, totalStart, memBefore)
	}

	// Full redraw needed?
	if reason, clear := t.needsFullRedraw(newLines); reason != "" {
		if !t.hasSyncOutput() && clear {
			// Without sync output, a clear+repaint would flicker.
			// Reset maxLinesRendered so viewport math is correct,
			// then fall through to differential rendering.
			t.maxLinesRendered = len(newLines)
		} else {
			stats.FullRedrawReason = reason
			t.writeFullRedraw(width, height, newLines, cursorPos, stats, clear)
			emitStats()
			return
		}
	}

	// Compute diff.
	diffStart := time.Now()
	dr := diffLines(t.previousLines, newLines)
	viewportTop := max(0, t.maxLinesRendered-height)

	// No changes — just reposition cursor.
	if dr.firstChanged == -1 {
		stats.DiffTime = time.Since(diffStart)
		stats.CacheHits = len(newLines)
		stats.FirstChangedLine = -1
		stats.LastChangedLine = -1
		t.positionHardwareCursor(cursorPos, len(newLines))
		t.previousViewportTop = max(0, t.maxLinesRendered-height)
		// Always update previousLines so that Container's double-buffered
		// line slices stay in sync. Without this, a no-change frame leaves
		// previousLines pointing at a stale buffer that Container will
		// overwrite on a subsequent frame, corrupting the diff baseline.
		t.previousLines = newLines
		emitStats()
		return
	}

	// All changes in deleted tail.
	if dr.firstChanged >= len(newLines) {
		t.writeTailShrink(width, height, newLines, cursorPos, stats, &dr, diffStart, viewportTop)
		emitStats()
		return
	}

	// First change above previous viewport.
	if dr.firstChanged < t.previousViewportTop {
		if !t.hasSyncOutput() {
			// Without synchronized output a full clear+repaint would
			// flicker. Instead, repaint only the visible region.
			t.writeVisibleRepaint(width, height, newLines, cursorPos, stats, &dr, diffStart)
			emitStats()
			return
		}
		stats.FullRedrawReason = fmt.Sprintf(
			"above_viewport:first=%d,vpTop=%d,prevLines=%d,newLines=%d,height=%d",
			dr.firstChanged, t.previousViewportTop, len(t.previousLines), len(newLines), height,
		)
		t.writeFullRedraw(width, height, newLines, cursorPos, stats, true)
		emitStats()
		return
	}

	// Differential update.
	t.writeDiffUpdate(width, height, newLines, cursorPos, stats, &dr, diffStart, viewportTop)
	emitStats()
}

// needsFullRedraw returns (reason, clearScreen) if a full redraw is required,
// or ("", false) if differential rendering can proceed.
func (t *TUI) needsFullRedraw(newLines []string) (string, bool) {
	if len(t.previousLines) == 0 {
		return "first_render", false
	}
	if t.clearOnShrink && len(newLines) < t.maxLinesRendered && len(t.overlayStack) == 0 {
		return "clear_on_shrink", true
	}
	return "", false
}

// writeFullRedraw writes all lines to the terminal, optionally clearing the
// screen first. Updates TUI state and positions the cursor.
func (t *TUI) writeFullRedraw(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, clear bool) {
	t.mu.Lock()
	t.fullRedrawCount++
	t.mu.Unlock()

	stats.FullRedraw = true
	stats.LinesRepainted = len(newLines)
	stats.CacheHits = 0
	stats.FirstChangedLine = 0
	stats.LastChangedLine = max(0, len(newLines)-1)

	diffStart := time.Now()
	buf := &t.writeBuf
	buf.Reset()
	buf.WriteString(t.syncBegin())
	if clear {
		buf.WriteString(escClearScrollback)
		buf.WriteString(escClearScreen)
		buf.WriteString(escCursorHome)
	}
	for i, line := range newLines {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		if !clear {
			buf.WriteString(escClearLine)
		}
		buf.WriteString(line)
		buf.WriteString(segmentReset)
	}
	buf.WriteString(t.syncEnd())
	stats.DiffTime = time.Since(diffStart)
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.Write(buf.Bytes())
	stats.WriteTime = time.Since(writeStart)

	cr := max(0, len(newLines)-1)
	ml := t.maxLinesRendered
	if clear {
		ml = len(newLines)
	} else {
		ml = max(ml, len(newLines))
	}

	t.cursorRow = cr
	t.hardwareCursorRow = cr
	t.hardwareCursorCol = 0 // unknown after writing content
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-height)

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.previousLines = newLines
	t.previousWidth = width
}

// writeTailShrink handles the case where content was only removed from the
// end (no visible lines changed, just fewer of them).
func (t *TUI) writeTailShrink(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time, viewportTop int) {
	stats.CacheHits = len(newLines)
	stats.LinesRepainted = 0
	stats.FirstChangedLine = dr.firstChanged
	stats.LastChangedLine = dr.lastChanged

	if len(t.previousLines) > len(newLines) {
		targetRow := max(0, len(newLines)-1)
		currentScreen := t.hardwareCursorRow - t.previousViewportTop
		targetScreen := targetRow - viewportTop
		delta := targetScreen - currentScreen
		extra := len(t.previousLines) - len(newLines)

		if extra > height {
			stats.FullRedrawReason = fmt.Sprintf(
				"tail_shrink_too_large:extra=%d,height=%d", extra, height,
			)
			t.writeFullRedraw(width, height, newLines, cursorPos, stats, true)
			return
		}

		buf := &t.writeBuf
		buf.Reset()
		buf.WriteString(t.syncBegin())
		buf.WriteString(cursorVertical(delta))
		buf.WriteString("\r")
		if len(newLines) == 0 {
			// Shrinking to nothing: clear all lines starting from
			// the current position (targetRow = 0).
			for i := range extra {
				buf.WriteString(escClearLine)
				if i < extra-1 {
					buf.WriteString(cursorDown(1))
					buf.WriteString("\r")
				}
			}
			if extra > 1 {
				buf.WriteString(cursorUp(extra - 1))
			}
		} else {
			// Shrinking but keeping some lines: skip past the last
			// remaining line, then clear the extras below.
			buf.WriteString(cursorDown(1))
			for i := range extra {
				buf.WriteString("\r")
				buf.WriteString(escClearLine)
				if i < extra-1 {
					buf.WriteString(cursorDown(1))
				}
			}
			buf.WriteString(cursorUp(extra))
		}
		buf.WriteString(t.syncEnd())
		stats.DiffTime = time.Since(diffStart)
		stats.BytesWritten = buf.Len()

		writeStart := time.Now()
		t.terminal.Write(buf.Bytes())
		stats.WriteTime = time.Since(writeStart)

		t.cursorRow = targetRow
		t.hardwareCursorRow = targetRow
		t.hardwareCursorCol = 0 // unknown after cursor movement
	} else {
		stats.DiffTime = time.Since(diffStart)
	}

	// Shrink maxLinesRendered when trailing lines are cleared so the
	// viewport is computed from the actual content size.
	if len(t.previousLines) > len(newLines) {
		t.maxLinesRendered = len(newLines)
	}

	t.positionHardwareCursor(cursorPos, len(newLines))
	t.previousLines = newLines
	t.previousWidth = width
	t.previousViewportTop = max(0, t.maxLinesRendered-height)
}

// writeDiffUpdate writes only the changed lines to the terminal, scrolling
// the viewport as needed.
func (t *TUI) writeDiffUpdate(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time, viewportTop int) {
	buf := &t.writeBuf
	buf.Reset()
	buf.WriteString(t.syncBegin())

	hardwareCursorRow := t.hardwareCursorRow
	prevViewportTop := t.previousViewportTop
	prevViewportBottom := prevViewportTop + height - 1

	moveTargetRow := dr.firstChanged
	if dr.appendStart {
		moveTargetRow = dr.firstChanged - 1
	}

	// Scroll viewport down if the first change is below the visible area.
	if moveTargetRow > prevViewportBottom {
		currentScreen := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreen
		buf.WriteString(cursorDown(moveToBottom))
		scroll := moveTargetRow - prevViewportBottom
		stats.ScrollLines = scroll
		for range scroll {
			buf.WriteString("\r\n")
		}
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	// Compute cursor delta using local values (may have been adjusted by scrolling above).
	currentScreen := hardwareCursorRow - prevViewportTop
	targetScreen := moveTargetRow - viewportTop
	delta := targetScreen - currentScreen
	buf.WriteString(cursorVertical(delta))

	if dr.appendStart {
		buf.WriteString("\r\n")
	} else {
		buf.WriteString("\r")
	}

	renderEnd := min(dr.lastChanged, len(newLines)-1)
	prevLineCount := len(t.previousLines)
	for i := dr.firstChanged; i <= renderEnd; i++ {
		if i > dr.firstChanged {
			if i < prevLineCount {
				// Line already exists on screen — use cursor movement
				// instead of \r\n to avoid triggering terminal scroll-
				// to-bottom when the user has scrolled up in scrollback.
				buf.WriteString(cursorDown(1))
				buf.WriteString("\r")
			} else {
				// New line that doesn't exist yet — must use \r\n to
				// create it.
				buf.WriteString("\r\n")
			}
		}
		buf.WriteString(escClearLine)
		buf.WriteString(newLines[i])
		buf.WriteString(segmentReset)
	}

	finalCursorRow := renderEnd

	// Clear deleted trailing lines.
	if prevLineCount > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			buf.WriteString(cursorDown(moveDown))
			finalCursorRow = len(newLines) - 1
		}
		extra := prevLineCount - len(newLines)
		// These lines already exist on screen — use cursor movement
		// instead of \r\n to avoid terminal scroll-to-bottom.
		for range extra {
			buf.WriteString(cursorDown(1))
			buf.WriteString("\r")
			buf.WriteString(escClearLine)
		}
		buf.WriteString(cursorUp(extra))
	}

	buf.WriteString(t.syncEnd())
	stats.DiffTime = time.Since(diffStart)

	// Compute repainted/cached stats.
	stats.LinesRepainted = renderEnd - dr.firstChanged + 1
	stats.CacheHits = len(newLines) - stats.LinesRepainted
	stats.FirstChangedLine = dr.firstChanged
	stats.LastChangedLine = dr.lastChanged
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.Write(buf.Bytes())
	stats.WriteTime = time.Since(writeStart)

	cr := max(0, len(newLines)-1)
	// If trailing lines were cleared, shrink maxLinesRendered so the
	// viewport is computed from the actual content size.
	ml := t.maxLinesRendered
	if prevLineCount > len(newLines) {
		ml = len(newLines)
	} else {
		ml = max(ml, len(newLines))
	}

	t.cursorRow = cr
	t.hardwareCursorRow = finalCursorRow
	t.hardwareCursorCol = 0 // unknown after writing content
	t.maxLinesRendered = ml
	t.previousViewportTop = max(0, ml-height)

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.previousLines = newLines
	t.previousWidth = width
}

// applyFrameAltScreen renders on the alternate screen buffer using absolute
// cursor positioning. Only the visible slice of content is written, and only
// lines that differ from the previous frame are updated.
func (t *TUI) applyFrameAltScreen(width, height int, newLines []string, cursorPos *CursorPos, compStats []ComponentStat, stats *RenderStats, totalStart time.Time, memBefore *runtime.MemStats) {
	emitStats := func() {
		t.emitDebugStats(t.debugWriter, stats, compStats, totalStart, memBefore)
	}

	diffStart := time.Now()

	// Compute the visible slice.
	viewportTop := max(0, len(newLines)-height)
	viewportEnd := min(viewportTop+height, len(newLines))
	visible := newLines[viewportTop:viewportEnd]

	// Compute the previous visible slice for diffing.
	prevViewportTop := t.previousViewportTop
	prevViewportEnd := min(prevViewportTop+height, len(t.previousLines))
	var prevVisible []string
	if prevViewportTop < len(t.previousLines) {
		prevVisible = t.previousLines[prevViewportTop:prevViewportEnd]
	}

	buf := &t.writeBuf
	buf.Reset()

	linesRepainted := 0
	firstChanged := -1
	lastChanged := -1

	for i, line := range visible {
		// Check if this line differs from the previous frame.
		var prevLine string
		if i < len(prevVisible) {
			prevLine = prevVisible[i]
		}
		if line == prevLine {
			continue
		}

		if firstChanged == -1 {
			firstChanged = i
		}
		lastChanged = i

		// Absolute position: row i+1 (1-indexed), column 1.
		buf.WriteString(cursorTo(i+1, 1))
		buf.WriteString(escClearLine)
		buf.WriteString(line)
		buf.WriteString(segmentReset)
		linesRepainted++
	}

	// Clear any leftover lines below the content.
	for i := len(visible); i < height; i++ {
		var prevLine string
		if i < len(prevVisible) {
			prevLine = prevVisible[i]
		}
		if prevLine == "" {
			continue
		}
		buf.WriteString(cursorTo(i+1, 1))
		buf.WriteString(escClearLine)
	}

	stats.DiffTime = time.Since(diffStart)
	stats.LinesRepainted = linesRepainted
	stats.CacheHits = len(visible) - linesRepainted
	stats.TotalLines = len(newLines)
	if firstChanged >= 0 {
		stats.FirstChangedLine = viewportTop + firstChanged
		stats.LastChangedLine = viewportTop + lastChanged
	} else {
		stats.FirstChangedLine = -1
		stats.LastChangedLine = -1
	}
	stats.BytesWritten = buf.Len()

	if buf.Len() > 0 {
		writeStart := time.Now()
		t.terminal.Write(buf.Bytes())
		stats.WriteTime = time.Since(writeStart)
	}

	t.maxLinesRendered = len(newLines)
	t.previousViewportTop = viewportTop
	t.previousLines = newLines

	// Position hardware cursor for the component's cursor (if any).
	t.positionHardwareCursorAltScreen(cursorPos, viewportTop, height)

	emitStats()
}

// positionHardwareCursorAltScreen positions the cursor on the alt screen
// using absolute coordinates.
func (t *TUI) positionHardwareCursorAltScreen(pos *CursorPos, viewportTop, height int) {
	if pos == nil {
		if !t.cursorHidden {
			t.terminal.HideCursor()
			t.cursorHidden = true
		}
		return
	}

	// Convert content row to screen row.
	screenRow := pos.Row - viewportTop
	if screenRow < 0 || screenRow >= height {
		// Cursor is offscreen.
		if !t.cursorHidden {
			t.terminal.HideCursor()
			t.cursorHidden = true
		}
		return
	}

	col := max(0, pos.Col) + 1 // 1-indexed
	row := screenRow + 1       // 1-indexed

	t.terminal.WriteString(cursorTo(row, col))
	t.hardwareCursorRow = pos.Row
	t.hardwareCursorCol = col

	if t.showHardwareCursor {
		if t.cursorHidden {
			t.terminal.ShowCursor()
			t.cursorHidden = false
		}
	} else {
		if !t.cursorHidden {
			t.terminal.HideCursor()
			t.cursorHidden = true
		}
	}
}

// writeVisibleRepaint repaints only the lines within the current viewport.
// Used when content changes above the viewport and synchronized output is
// not available (so a full clear+repaint would flicker).
func (t *TUI) writeVisibleRepaint(width, height int, newLines []string, cursorPos *CursorPos, stats *RenderStats, dr *diffResult, diffStart time.Time) {
	// When content shrinks, accept the new line count so viewport
	// math is based on actual content, not stale history.
	ml := len(newLines)
	if t.maxLinesRendered > ml {
		t.maxLinesRendered = ml
	}
	viewportTop := max(0, ml-height)
	viewportBottom := min(viewportTop+height-1, len(newLines)-1)

	stats.FullRedrawReason = fmt.Sprintf(
		"visible_repaint(no_sync):first=%d,vpTop=%d,vpBot=%d",
		dr.firstChanged, viewportTop, viewportBottom,
	)

	buf := &t.writeBuf
	buf.Reset()

	// Move cursor to the top of the visible region on screen.
	// The hardware cursor is at some absolute row; we need to get
	// to the screen position corresponding to viewportTop.
	hardwareCursorRow := t.hardwareCursorRow
	prevViewportTop := t.previousViewportTop

	// Screen position of hardware cursor relative to old viewport.
	currentScreen := hardwareCursorRow - prevViewportTop

	// We want screen row 0 of the new viewport. Since the terminal
	// only lets us move relative to the cursor, compute the delta
	// in screen coordinates. The new viewport may be at a different
	// absolute position than the old one.
	//
	// Absolute row of the new viewport top on screen:
	//   The terminal's visible area hasn't scrolled (no \r\n emitted),
	//   so screen row 0 is still at absolute row prevViewportTop.
	//   We want to land at absolute row viewportTop.
	newScreen := viewportTop - prevViewportTop
	delta := newScreen - currentScreen
	buf.WriteString(cursorVertical(delta))
	buf.WriteString("\r")

	// Repaint each visible line.
	linesRepainted := 0
	prevLineCount := len(t.previousLines)
	for i := viewportTop; i <= viewportBottom; i++ {
		if i > viewportTop {
			if i < prevLineCount {
				buf.WriteString(cursorDown(1))
				buf.WriteString("\r")
			} else {
				buf.WriteString("\r\n")
			}
		}
		buf.WriteString(escClearLine)
		if i < len(newLines) {
			buf.WriteString(newLines[i])
			buf.WriteString(segmentReset)
		}
		linesRepainted++
	}

	finalCursorRow := viewportBottom

	// Clear any stale lines below the new content that are still
	// on screen (visible within the old viewport).
	prevViewportBottom := prevViewportTop + height - 1
	staleStart := viewportBottom + 1
	if staleStart <= prevViewportBottom && staleStart < prevLineCount {
		staleEnd := min(prevViewportBottom, prevLineCount-1)
		for i := staleStart; i <= staleEnd; i++ {
			buf.WriteString(cursorDown(1))
			buf.WriteString("\r")
			buf.WriteString(escClearLine)
		}
		cleared := staleEnd - staleStart + 1
		buf.WriteString(cursorUp(cleared))
	}

	stats.DiffTime = time.Since(diffStart)
	stats.LinesRepainted = linesRepainted
	stats.CacheHits = len(newLines) - linesRepainted
	stats.FirstChangedLine = viewportTop
	stats.LastChangedLine = viewportBottom
	stats.BytesWritten = buf.Len()

	writeStart := time.Now()
	t.terminal.Write(buf.Bytes())
	stats.WriteTime = time.Since(writeStart)

	t.cursorRow = max(0, len(newLines)-1)
	t.hardwareCursorRow = finalCursorRow
	t.hardwareCursorCol = 0
	t.maxLinesRendered = ml
	t.previousViewportTop = viewportTop

	t.positionHardwareCursor(cursorPos, len(newLines))

	t.previousLines = newLines
	t.previousWidth = width
}

// dumpFrame writes the full content and visible screen to numbered files
// in the TUIST_FRAMES directory for debugging.
func (t *TUI) dumpFrame(displayLines []string, height int) {
	n := t.frameNum
	t.frameNum++

	path := fmt.Sprintf("%s/%d.txt", t.frameDir, n)

	viewportTop := max(0, len(displayLines)-height)
	viewportEnd := min(viewportTop+height, len(displayLines))

	var buf strings.Builder
	fmt.Fprintf(&buf, "=== frame %d | lines=%d height=%d viewportTop=%d ===\n",
		n, len(displayLines), height, viewportTop)

	buf.WriteString("\n--- screen (what alt screen should show) ---\n")
	for i := viewportTop; i < viewportEnd; i++ {
		fmt.Fprintf(&buf, "[%3d] %s\n", i, displayLines[i])
	}

	buf.WriteString("\n--- full content ---\n")
	for i, line := range displayLines {
		marker := "  "
		if i >= viewportTop && i < viewportEnd {
			marker = "> "
		}
		fmt.Fprintf(&buf, "%s[%3d] %s\n", marker, i, line)
	}

	_ = os.WriteFile(path, []byte(buf.String()), 0644)
}

// emitDebugStats writes render stats as a JSONL record if a debug writer
// is configured.
func (t *TUI) emitDebugStats(w io.Writer, stats *RenderStats, compStats []ComponentStat, totalStart time.Time, memBefore *runtime.MemStats) {
	if w == nil {
		return
	}
	stats.TotalTime = time.Since(totalStart)

	// Read memory stats at frame end.
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Populate runtime metrics.
	stats.HeapAlloc = memAfter.HeapAlloc
	stats.HeapObjects = memAfter.HeapObjects
	stats.TotalAlloc = memAfter.TotalAlloc
	stats.Sys = memAfter.Sys
	stats.HeapInuse = memAfter.HeapInuse
	stats.HeapIdle = memAfter.HeapIdle
	stats.StackInuse = memAfter.StackInuse
	stats.NumGC = memAfter.NumGC
	stats.GCCPUFraction = memAfter.GCCPUFraction
	stats.Goroutines = runtime.NumGoroutine()

	// Per-frame deltas: allocations and frees during this render cycle.
	if memBefore != nil {
		stats.Mallocs = memAfter.Mallocs - memBefore.Mallocs
		stats.Frees = memAfter.Frees - memBefore.Frees
		stats.HeapAllocDelta = memAfter.TotalAlloc - memBefore.TotalAlloc
	}

	// Most recent GC pause.
	if memAfter.NumGC > 0 {
		// PauseNs is a circular buffer of recent GC pauses indexed by NumGC.
		stats.GCPauseNs = memAfter.PauseNs[(memAfter.NumGC+255)%256]
	}

	rec := renderStatsJSON{
		Ts:             time.Now().UnixMilli(),
		TotalUs:        stats.TotalTime.Microseconds(),
		RenderUs:       stats.RenderTime.Microseconds(),
		CompositeUs:    stats.CompositeTime.Microseconds(),
		DiffUs:         stats.DiffTime.Microseconds(),
		WriteUs:        stats.WriteTime.Microseconds(),
		TotalLines:     stats.TotalLines,
		LinesRepainted: stats.LinesRepainted,
		CacheHits:      stats.CacheHits,
		FullRedraw:     stats.FullRedraw,
		FullRedrawWhy:  stats.FullRedrawReason,
		OverlayCount:   stats.OverlayCount,
		BytesWritten:   stats.BytesWritten,
		FirstChanged:   stats.FirstChangedLine,
		LastChanged:    stats.LastChangedLine,
		ScrollLines:    stats.ScrollLines,
		Components:     compStats,

		HeapAlloc:      stats.HeapAlloc,
		HeapObjects:    stats.HeapObjects,
		TotalAlloc:     stats.TotalAlloc,
		Sys:            stats.Sys,
		Mallocs:        stats.Mallocs,
		Frees:          stats.Frees,
		HeapAllocDelta: stats.HeapAllocDelta,
		NumGC:          stats.NumGC,
		GCPauseNs:      stats.GCPauseNs,
		GCCPUFraction:  stats.GCCPUFraction,
		Goroutines:     stats.Goroutines,
		StackInuse:     stats.StackInuse,
		HeapInuse:      stats.HeapInuse,
		HeapIdle:       stats.HeapIdle,
	}
	data, _ := json.Marshal(rec)
	data = append(data, '\n')
	w.Write(data) //nolint:errcheck
}

// ---------- overlay compositing ---------------------------------------------

func (t *TUI) compositeOverlays(lines []string, baseCursor *CursorPos, overlays []*overlayEntry, termW, termH int) ([]string, *CursorPos) {
	contentH := len(lines)
	result := make([]string, contentH)
	copy(result, lines)
	cursor := baseCursor

	type rendered struct {
		lines           []string
		row             int
		col             int
		w               int
		contentRelative bool
		cursor          *CursorPos
	}
	// Pre-rendered overlay data, used for cursor-relative two-pass layout.
	type preRendered struct {
		entry  *overlayEntry
		opts   *OverlayOptions
		w      int
		lines  []string
		cursor *CursorPos
	}

	// Pass 1: render all overlays and determine CursorGroup directions.
	// Cursor groups need all member heights before deciding above/below.
	var pre []preRendered
	groupAbove := make(map[*CursorGroup]bool) // true = all members fit above
	groupSeen := make(map[*CursorGroup]bool)  // whether we've seen this group

	for _, e := range overlays {
		if e.hidden {
			continue
		}
		opts := e.options

		// Resolve width and maxHeight.
		// ContentRelative overlays use contentH as reference height.
		// CursorRelative overlays use termH because they can extend
		// past content bounds.
		cr := opts != nil && opts.ContentRelative
		refH := termH
		if cr {
			refH = contentH
		}
		w, _, _, maxH, maxHSet := t.resolveOverlayLayout(opts, 0, termW, refH)
		renderH := termH
		if maxHSet {
			renderH = maxH
		}
		oResult := renderComponent(e.component, Context{
			Context: context.Background(),
			tui:     t,
			Width:   w,
			Height:  renderH,
		})
		oLines := oResult.Lines
		if maxHSet && len(oLines) > maxH {
			oLines = oLines[:maxH]
		}

		pre = append(pre, preRendered{
			entry:  e,
			opts:   opts,
			w:      w,
			lines:  oLines,
			cursor: oResult.Cursor,
		})

		// Track CursorGroup: if any member doesn't fit above, the whole
		// group goes below.
		if opts != nil && opts.CursorRelative && cursor != nil && opts.CursorGroup != nil {
			g := opts.CursorGroup
			if !groupSeen[g] {
				groupSeen[g] = true
				groupAbove[g] = true // optimistic: all fit above
			}
			if !cursorFitsAbove(cursor, len(oLines)) {
				groupAbove[g] = false
			}
		}
	}

	// Pass 2: compute positions and build the composite.
	var items []rendered
	minNeeded := len(result)

	for _, p := range pre {
		opts := p.opts
		oLines := p.lines

		var row, col int
		cr := opts != nil && opts.ContentRelative
		if opts != nil && opts.CursorRelative {
			if cursor == nil {
				continue // no cursor — skip this overlay
			}
			// Determine above/below: use group decision if in a group,
			// otherwise decide individually.
			above := opts.PreferAbove && cursorFitsAbove(cursor, len(oLines))
			if opts.CursorGroup != nil {
				if ga, ok := groupAbove[opts.CursorGroup]; ok {
					above = opts.PreferAbove && ga
				}
			}
			row, col = resolveCursorPosition(opts, cursor, len(oLines), above)
			cr = true // composited in content space
		} else {
			refH := termH
			if cr {
				refH = contentH
			}
			var maxH int
			var maxHSet bool
			_, row, col, maxH, maxHSet = t.resolveOverlayLayout(opts, len(oLines), termW, refH)
			if maxHSet && len(oLines) > maxH {
				oLines = oLines[:maxH]
				_, row, col, _, _ = t.resolveOverlayLayout(opts, len(oLines), termW, refH)
			}
		}
		items = append(items, rendered{
			lines:           oLines,
			row:             row,
			col:             col,
			w:               p.w,
			contentRelative: cr,
			cursor:          p.cursor,
		})
		if row+len(oLines) > minNeeded {
			minNeeded = row + len(oLines)
		}
	}

	// Overlays (both viewport-relative and content-relative) can extend the working height.
	// Use contentH (the current content height) rather than maxLinesRendered
	// (high-water mark) so that viewport-relative overlays track the actual
	// content bounds when content shrinks.
	workingH := max(contentH, minNeeded)
	for len(result) < workingH {
		result = append(result, "")
	}

	viewportStart := max(0, workingH-termH)

	for _, item := range items {
		for i, ol := range item.lines {
			var idx int
			if item.contentRelative {
				idx = item.row + i
			} else {
				idx = viewportStart + item.row + i
			}
			if idx >= 0 && idx < len(result) {
				result[idx] = CompositeLineAt(result[idx], ol, item.col, item.w, termW)
			}
		}
		// If the overlay's component has focus and provides a cursor,
		// translate it to the composited coordinate space.
		if item.cursor != nil {
			focused := t.focusedComponent
			// Check if this overlay has focus by comparing its component
			// We check via the overlay stack entries.
			for _, e := range overlays {
				if e.component == focused {
					// This is the focused overlay; use its cursor.
					var baseRow int
					if item.contentRelative {
						baseRow = item.row
					} else {
						baseRow = viewportStart + item.row
					}
					cursor = &CursorPos{
						Row: baseRow + item.cursor.Row,
						Col: item.col + item.cursor.Col,
					}
					break
				}
			}
		}
	}

	return result, cursor
}

// ---------- cursor ----------------------------------------------------------

func (t *TUI) positionHardwareCursor(pos *CursorPos, totalLines int) {
	if pos == nil || totalLines <= 0 {
		if !t.cursorHidden {
			t.terminal.HideCursor()
			t.cursorHidden = true
		}
		return
	}

	targetRow := clamp(pos.Row, 0, totalLines-1)
	targetCol := max(0, pos.Col)
	targetTermCol := targetCol + 1 // 1-indexed for the terminal

	hcr := t.hardwareCursorRow
	show := t.showHardwareCursor

	// Only write cursor movement if position actually changed.
	if targetRow != hcr || targetTermCol != t.hardwareCursorCol {
		seq := cursorVertical(targetRow-hcr) + cursorColumn(targetTermCol)
		if seq != "" {
			t.terminal.WriteString(seq)
		}
		t.hardwareCursorRow = targetRow
		t.hardwareCursorCol = targetTermCol
	}

	if show {
		if t.cursorHidden {
			t.terminal.ShowCursor()
			t.cursorHidden = false
		}
	} else {
		if !t.cursorHidden {
			t.terminal.HideCursor()
			t.cursorHidden = true
		}
	}
}

// ---------- print above -----------------------------------------------------

// PrintAbove writes text into the terminal scrollback buffer above the
// TUI's rendered content. The TUI content is erased and re-rendered below
// the printed text on the next frame. Newlines in the text are translated
// to \r\n for proper terminal output.
//
// This is useful for content that must not be word-wrapped by the TUI
// renderer (e.g. clickable URLs) or for persistent output that should
// remain in scrollback after the TUI exits.
//
// Safe to call from any goroutine.
func (t *TUI) PrintAbove(text string) {
	t.Dispatch(func() {
		t.printAbove(text)
	})
}

func (t *TUI) printAbove(text string) {
	buf := &t.writeBuf
	buf.Reset()

	// Move to the top of our rendered area.
	if t.hardwareCursorRow > 0 {
		buf.WriteString(cursorUp(t.hardwareCursorRow))
	}
	buf.WriteString("\r")

	// Erase all TUI content from here down.
	buf.WriteString("\x1b[J")

	// Write the above text with \r\n line endings.
	lines := strings.Split(text, "\n")
	// Trim trailing empty line from Split if text ends with \n.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		buf.WriteString(line)
	}
	// End with \r\n so TUI content starts on the next line.
	buf.WriteString("\r\n")

	t.terminal.Write(buf.Bytes())

	// Reset rendering state so the TUI re-renders from scratch
	// starting at the current cursor position.
	t.previousLines = nil
	t.previousWidth = -1
	t.cursorRow = 0
	t.hardwareCursorRow = 0
	t.hardwareCursorCol = 0
	t.maxLinesRendered = 0
	t.previousViewportTop = 0
}

// AboveWriter returns an io.Writer that prints each Write call's content
// above the TUI via [PrintAbove]. Each Write is a separate PrintAbove
// call; callers should buffer complete messages before writing.
func (t *TUI) AboveWriter() io.Writer {
	return &aboveWriter{tui: t}
}

type aboveWriter struct {
	tui *TUI
}

func (w *aboveWriter) Write(p []byte) (int, error) {
	w.tui.PrintAbove(string(p))
	return len(p), nil
}

// ---------- helpers ---------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
