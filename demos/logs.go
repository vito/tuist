// logs is an interactive stress test for tuist rendering. It creates a
// TUI with a large scrollable log and hotkeys to exercise every rendering
// code path. Render debug is automatically enabled.
//
// Usage:
//
//	go run ./pkg/tuist/demos logs
//	go run ./pkg/tuist/demos logs -lines 500
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/tuist"
)

func logsMain() {
	lines := flag.Int("lines", 200, "initial number of log lines")
	flag.Parse()

	if err := runLogs(*lines); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runLogs(initialLines int) error {
	tui := tuist.New(sharedTerm)

	// Auto-enable render debug.
	logPath := "/tmp/tuist.log"
	debugFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open debug log: %w", err)
	}
	defer debugFile.Close() //nolint:errcheck // best-effort close of debug log
	tui.SetDebugWriter(debugFile)

	log := newStressLog(initialLines)
	statusBar := &statusBarComponent{}

	tui.AddChild(log)
	tui.AddChild(statusBar)

	if err := tui.Start(); err != nil {
		return fmt.Errorf("TUI start: %w", err)
	}

	// State.
	var (
		overlayHandle    *tuist.OverlayHandle
		keymapHandle     *tuist.OverlayHandle
		continuousTicker *time.Ticker
		continuousDone   chan struct{}
	)

	quit := make(chan struct{})

	stopContinuous := func() {
		if continuousTicker != nil {
			continuousTicker.Stop()
			close(continuousDone)
			continuousTicker = nil
		}
	}

	doQuit := func() {
		select {
		case <-quit:
		default:
			close(quit)
		}
	}

	// Keymap — declarative bindings that drive both input handling and the help overlay.
	var km keymap
	km = keymap{
		{key: "q", desc: "quit", action: func(ctx tuist.Context, _ uv.Key) {
			doQuit()
		}},
		{key: "Ctrl+C", desc: "quit", action: func(ctx tuist.Context, _ uv.Key) {
			doQuit()
		}},
		{key: "?", desc: "toggle keymap", action: func(ctx tuist.Context, _ uv.Key) {
			if keymapHandle != nil {
				keymapHandle.Remove()
				keymapHandle = nil
				statusBar.set(km.statusLine())
			} else {
				overlay := km.overlay()
				keymapHandle = ctx.ShowOverlay(overlay, &tuist.OverlayOptions{
					Width:  tuist.SizeAbs(overlay.width + 2),
					Anchor: tuist.AnchorCenter,
				})
				statusBar.set("\x1b[7m ? to close keymap \x1b[0m")
			}
			ctx.RequestRender(false)
		}},
		{key: "v", desc: "toggle verbose", action: func(ctx tuist.Context, _ uv.Key) {
			log.mu.Lock()
			log.verbose = !log.verbose
			v := log.verbose
			log.mu.Unlock()
			log.Update()
			if v {
				statusBar.set("\x1b[7m VERBOSE ON — all lines expanded (off-screen repaint!) \x1b[0m")
			} else {
				statusBar.set("\x1b[7m VERBOSE OFF — compact view \x1b[0m")
			}
			ctx.RequestRender(false)
		}},
		{key: "c", desc: "toggle color", action: func(ctx tuist.Context, _ uv.Key) {
			log.mu.Lock()
			log.colorize = !log.colorize
			c := log.colorize
			log.mu.Unlock()
			log.Update()
			if c {
				statusBar.set("\x1b[7m COLOR ON — ANSI styles changed on every line \x1b[0m")
			} else {
				statusBar.set("\x1b[7m COLOR OFF — plain text \x1b[0m")
			}
			ctx.RequestRender(false)
		}},
		{key: "a", desc: "append 10 lines", action: func(ctx tuist.Context, _ uv.Key) {
			appendLines(log, 10)
			statusBar.set("\x1b[7m +10 lines appended \x1b[0m")
			ctx.RequestRender(false)
		}},
		{key: "A", desc: "append 100 lines", action: func(ctx tuist.Context, _ uv.Key) {
			appendLines(log, 100)
			statusBar.set("\x1b[7m +100 lines appended \x1b[0m")
			ctx.RequestRender(false)
		}},
		{key: "d", desc: "delete 10 lines", action: func(ctx tuist.Context, _ uv.Key) {
			log.mu.Lock()
			if len(log.lines) > 10 {
				log.lines = log.lines[:len(log.lines)-10]
			} else {
				log.lines = nil
			}
			n := len(log.lines)
			log.mu.Unlock()
			log.Update()
			statusBar.set(fmt.Sprintf("\x1b[7m deleted 10 lines (now %d) \x1b[0m", n))
			ctx.RequestRender(false)
		}},
		{key: "o", desc: "toggle overlay", action: func(ctx tuist.Context, _ uv.Key) {
			if overlayHandle != nil {
				overlayHandle.Remove()
				overlayHandle = nil
				statusBar.set("\x1b[7m overlay hidden \x1b[0m")
			} else {
				overlay := &staticLines{lines: []string{
					"╭──────────────────╮",
					"│ Completions      │",
					"│  container       │",
					"│  directory       │",
					"│  withExec        │",
					"│  withMountedDir  │",
					"│  stdout          │",
					"│  stderr          │",
					"│  file            │",
					"╰──────────────────╯",
				}}
				overlayHandle = ctx.ShowOverlay(overlay, &tuist.OverlayOptions{
					Width:   tuist.SizeAbs(22),
					Anchor:  tuist.AnchorBottomLeft,
					OffsetX: 2,
					OffsetY: -1,
				})
				statusBar.set("\x1b[7m overlay shown (press o to hide) \x1b[0m")
			}
			ctx.RequestRender(false)
		}},
		{key: "r", desc: "force redraw", action: func(ctx tuist.Context, _ uv.Key) {
			statusBar.set("\x1b[7m forced full redraw \x1b[0m")
			ctx.RequestRender(true)
		}},
		{key: "1-9", desc: "continuous repaint line N×10", action: func(ctx tuist.Context, key uv.Key) {
			stopContinuous()
			target := int(key.Text[0]-'0') * 10
			continuousDone = make(chan struct{})
			continuousTicker = time.NewTicker(50 * time.Millisecond)
			statusBar.set(fmt.Sprintf("\x1b[7m continuous repaint on line %d every 50ms (0 to stop) \x1b[0m", target))
			ctx.RequestRender(false)
			go func() {
				tick := continuousTicker
				done := continuousDone
				for {
					select {
					case <-done:
						return
					case <-tick.C:
						log.mu.Lock()
						if target < len(log.lines) {
							log.lines[target].entry.message = fmt.Sprintf("[continuous] tick %d latency=%dµs",
								time.Now().UnixMicro()%100000, rand.Intn(5000))
							log.lines[target].Update()
						}
						log.mu.Unlock()
						log.Update()
					}
				}
			}()
		}},
		{key: "0", desc: "stop continuous", action: func(ctx tuist.Context, _ uv.Key) {
			stopContinuous()
			statusBar.set("\x1b[7m continuous repaint stopped \x1b[0m")
			ctx.RequestRender(false)
		}},
	}

	statusBar.set(km.statusLine())

	// Input handler — delegates to the keymap.
	tui.AddInputListener(func(ctx tuist.Context, ev uv.Event) bool {
		kp, ok := ev.(uv.KeyPressEvent)
		if !ok {
			return false
		}
		return km.handle(ctx, uv.Key(kp))
	})

	fmt.Fprintf(os.Stderr, "Render debug → %s\n", logPath)
	fmt.Fprintf(os.Stderr, "Run 'go run ./cmd/render-debug' in another terminal for live charts.\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
	case <-sigCh:
	}

	stopContinuous()
	signal.Stop(sigCh)
	tui.Stop()
	fmt.Println("Done.")
	return nil
}

// ── stress log component ───────────────────────────────────────────────────

// stressLog renders a list of logLine components, each with its own
// spinner. When lines scroll above the viewport, their spinners
// freeze via the Volatile interface — a good stress test for the
// offscreen optimisation.
type stressLog struct {
	tuist.Compo
	mu       sync.Mutex
	lines    []*logLine
	verbose  bool
	colorize bool
}

type stressEntry struct {
	ts      time.Time
	level   string
	message string
}

func newStressLog(n int) *stressLog {
	s := &stressLog{}
	s.lines = makeLogLines(n)
	s.Update()
	return s
}

func makeLogLines(n int) []*logLine {
	levels := []string{"INFO", "DEBUG", "WARN", "ERROR", "TRACE"}
	modules := []string{"tuist.render", "tuist.diff", "tuist.overlay", "tuist.input", "tuist.cursor",
		"dang.eval", "dang.parse", "dang.infer", "dang.graphql", "dang.repl"}
	messages := []string{
		"processing request",
		"cache miss for key",
		"connection established",
		"rendering frame",
		"overlay composited",
		"differential update applied",
		"component tree walked",
		"escape sequence generated",
		"viewport scrolled",
		"cursor repositioned",
		"input dispatched to handler",
		"focus changed",
		"style computation completed",
		"width calculation for line",
		"ANSI truncation applied",
	}

	lines := make([]*logLine, n)
	base := time.Now().Add(-time.Duration(n) * 100 * time.Millisecond)
	for i := range lines {
		lines[i] = newLogLine(stressEntry{
			ts:      base.Add(time.Duration(i) * 100 * time.Millisecond),
			level:   levels[rand.Intn(len(levels))],
			message: fmt.Sprintf("[%s] %s id=%d latency=%dµs", modules[rand.Intn(len(modules))], messages[rand.Intn(len(messages))], rand.Intn(10000), rand.Intn(5000)),
		})
	}
	return lines
}

func (s *stressLog) Render(ctx tuist.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, l := range s.lines {
		l.colorize = s.colorize
		l.verbose = s.verbose
		s.RenderChild(ctx, l)
	}
}

// ── per-line component with spinner ────────────────────────────────────────

type logLine struct {
	tuist.Compo
	entry    stressEntry
	spinner  *tuist.Spinner
	colorize bool
	verbose  bool
}

func newLogLine(e stressEntry) *logLine {
	sp := tuist.NewSpinner()
	sp.Style = func(s string) string { return "\x1b[35m" + s + "\x1b[0m" }
	return &logLine{
		entry:   e,
		spinner: sp,
	}
}

func (l *logLine) Render(ctx tuist.Context) {
	e := l.entry
	ts := e.ts.Format("15:04:05.000")
	var levelStyled string
	if l.colorize {
		switch e.level {
		case "ERROR":
			levelStyled = "\x1b[31m" + e.level + "\x1b[0m"
		case "WARN":
			levelStyled = "\x1b[33m" + e.level + "\x1b[0m"
		case "DEBUG":
			levelStyled = "\x1b[36m" + e.level + "\x1b[0m"
		case "TRACE":
			levelStyled = "\x1b[90m" + e.level + "\x1b[0m"
		default:
			levelStyled = "\x1b[32m" + e.level + "\x1b[0m"
		}
	} else {
		levelStyled = e.level
	}
	spin := l.RenderChildInline(ctx, l.spinner)
	ctx.Line(fmt.Sprintf("%s %s %-5s %s", spin, ts, levelStyled, e.message))

	if l.verbose {
		detail := fmt.Sprintf("           → stack: %s | goroutine: %d | alloc: %dKB",
			randomStack(), rand.Intn(500), rand.Intn(8192))
		if l.colorize {
			detail = "\x1b[90m" + detail + "\x1b[0m"
		}
		ctx.Line(detail)
	}
}

func randomStack() string {
	frames := []string{
		"main.run", "tuist.doRender", "tuist.compositeOverlays",
		"tuist.CompositeLineAt", "ansi.Truncate", "dang.Eval",
		"runtime.goexit", "net/http.serve", "tuist.handleInput",
	}
	n := 2 + rand.Intn(3)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = frames[rand.Intn(len(frames))]
	}
	return strings.Join(parts, " → ")
}

func appendLines(log *stressLog, n int) {
	levels := []string{"INFO", "DEBUG", "WARN"}
	log.mu.Lock()
	for range n {
		log.lines = append(log.lines, newLogLine(stressEntry{
			ts:      time.Now(),
			level:   levels[rand.Intn(len(levels))],
			message: fmt.Sprintf("[append] new line %d val=%d", len(log.lines), rand.Intn(99999)),
		}))
	}
	log.mu.Unlock()
	log.Update()
}

// ── helper components ──────────────────────────────────────────────────────

type statusBarComponent struct {
	tuist.Compo
	mu   sync.Mutex
	line string
}

func (s *statusBarComponent) set(line string) {
	s.mu.Lock()
	s.line = line
	s.mu.Unlock()
	s.Update()
}
func (s *statusBarComponent) Render(ctx tuist.Context) {
	s.mu.Lock()
	line := s.line
	s.mu.Unlock()
	ctx.Line(line)
}

type staticLines struct {
	tuist.Compo
	lines []string
}

func (s *staticLines) Render(ctx tuist.Context) {
	ctx.Lines(s.lines...)
}

// ── keymap ──────────────────────────────────────────────────────────────────

// binding pairs a key label with a match predicate, description, and action.
type binding struct {
	key    string                              // display label (e.g. "q", "Ctrl+C", "1-9")
	desc   string                              // short description for help
	match  func(uv.Key) bool                   // optional custom matcher (overrides default)
	action func(ctx tuist.Context, key uv.Key) // handler
}

type keymap []binding

// handle dispatches a key event to the first matching binding.
func (km keymap) handle(ctx tuist.Context, key uv.Key) bool {
	for _, b := range km {
		if b.matches(key) {
			b.action(ctx, key)
			return true
		}
	}
	return false
}

// matches returns true if key matches this binding.
func (b *binding) matches(key uv.Key) bool {
	if b.match != nil {
		return b.match(key)
	}
	// Default: parse the key label.
	switch b.key {
	case "Ctrl+C":
		return key.Code == 'c' && key.Mod == uv.ModCtrl
	case "1-9":
		return len(key.Text) == 1 && key.Text[0] >= '1' && key.Text[0] <= '9'
	default:
		return key.Text == b.key
	}
}

// statusLine renders a compact one-line summary: "?=help key=desc key=desc …"
func (km keymap) statusLine() string {
	seen := map[string]bool{}
	var parts []string
	for _, b := range km {
		label := b.key + "=" + b.desc
		if seen[label] {
			continue
		}
		seen[label] = true
		parts = append(parts, label)
	}
	return "\x1b[7m " + strings.Join(parts, " ") + " \x1b[0m"
}

// overlay builds a box-drawn help component for ShowOverlay.
func (km keymap) overlay() *keymapOverlay {
	seen := map[string]bool{}
	type row struct{ key, desc string }
	var rows []row
	maxKey := 0
	for _, b := range km {
		label := b.key + "\t" + b.desc
		if seen[label] {
			continue
		}
		seen[label] = true
		rows = append(rows, row{b.key, b.desc})
		if len(b.key) > maxKey {
			maxKey = len(b.key)
		}
	}

	// Compute the content width from the longest row.
	contentW := 0
	for _, r := range rows {
		w := maxKey + 3 + len(r.desc) // "key · desc"
		if w > contentW {
			contentW = w
		}
	}

	title := "Keymap"
	if contentW < len(title) {
		contentW = len(title)
	}

	boxW := contentW + 4 // "│ " + content + " │"

	var lines []string
	// Top border with centered title.
	topPad := contentW - len(title)
	leftPad := topPad / 2
	rightPad := topPad - leftPad
	lines = append(lines, "╭─"+strings.Repeat("─", leftPad)+title+strings.Repeat("─", rightPad)+"─╮")

	for _, r := range rows {
		padKey := strings.Repeat(" ", maxKey-len(r.key))
		cell := "\x1b[1m" + r.key + "\x1b[0m" + padKey + " · " + r.desc
		visW := maxKey + 3 + len(r.desc)
		trail := strings.Repeat(" ", contentW-visW)
		lines = append(lines, "│ "+cell+trail+" │")
	}

	lines = append(lines, "╰"+strings.Repeat("─", boxW-2)+"╯")

	return &keymapOverlay{staticLines: staticLines{lines: lines}, width: boxW}
}

// keymapOverlay is a staticLines with a precomputed box width so the
// caller can size the overlay correctly.
type keymapOverlay struct {
	staticLines
	width int
}
