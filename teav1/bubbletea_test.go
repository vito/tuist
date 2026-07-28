package teav1

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/tuist"
)

// settle steps the TUI until cond holds, giving command goroutines a chance to
// dispatch their results back between frames. Commands run off the UI
// goroutine, so a message needs a frame boundary (and a moment) to land.
func settle(t *testing.T, tui *tuist.TUI, cond func() bool) {
	t.Helper()
	for range 200 {
		tui.Step()
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	tui.Step()
}

func count(log []string, entry string) int {
	n := 0
	for _, e := range log {
		if e == entry {
			n++
		}
	}
	return n
}

// probe is a bubbletea model that records the order in which it observes
// things, and answers a key press with an asynchronous command — the shape
// huh (and most bubbles) use: a key produces a Cmd whose message drives the
// next state transition.
type probe struct {
	log     *[]string
	quitOn  string // key that makes the model ask to quit
	initCmd tea.Cmd
}

type ackMsg struct{}

func (p probe) Init() tea.Cmd {
	*p.log = append(*p.log, "init")
	return p.initCmd
}

func (p probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		*p.log = append(*p.log, "key:"+key)
		if key == p.quitOn {
			return p, tea.Quit
		}
		return p, func() tea.Msg { return ackMsg{} }
	case ackMsg:
		*p.log = append(*p.log, "ack")
	}
	return p, nil
}

func (p probe) View() string { return "probe" }

// newProbeTUI builds a headless TUI with a focused, freshly added Wrap. The
// wrap is deliberately not rendered yet, so it is added-and-focused but
// unmounted — the state [TUI.SetFocus] leaves a component in until the next
// frame.
func newProbeTUI(t *testing.T, p probe) (*tuist.TUI, *Wrap) {
	t.Helper()
	tui := tuist.New(tuist.NewHeadlessTerminal(80, 24))
	w := New(p)
	tui.AddChild(w)
	tui.SetFocus(w)
	return tui, w
}

// A key press delivered before the first render must still reach the model,
// and the command it returns must still be delivered. tuist drains input
// before rendering, so type-ahead lands on a component that has been focused
// but not yet mounted.
func TestKeyBeforeMountRunsInitAndDeliversCommand(t *testing.T) {
	var log []string
	tui, _ := newProbeTUI(t, probe{log: &log})

	tui.Inject(uv.KeyPressEvent{Code: 'a', Text: "a"})
	settle(t, tui, func() bool { return count(log, "ack") > 0 })

	want := []string{"init", "key:a", "ack"}
	if len(log) != len(want) {
		t.Fatalf("log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("log = %v, want %v", log, want)
		}
	}
}

// The same for a paste, which HandlePaste turns into a run of key events.
func TestPasteBeforeMountRunsInitFirst(t *testing.T) {
	var log []string
	tui, _ := newProbeTUI(t, probe{log: &log})

	tui.Inject(uv.PasteEvent{Content: "hi"})
	settle(t, tui, func() bool { return count(log, "key:i") > 0 })

	if len(log) == 0 || log[0] != "init" {
		t.Fatalf("Init did not run before the pasted keys: %v", log)
	}
	var keys []string
	for _, e := range log {
		if len(e) > 4 && e[:4] == "key:" {
			keys = append(keys, e[4:])
		}
	}
	if len(keys) != 2 || keys[0] != "h" || keys[1] != "i" {
		t.Fatalf("pasted keys = %v, want [h i]", keys)
	}
}

// A pre-mount key press whose command asks to quit must still fire OnQuit.
// Before the fix this dropped the tea.Quit and the caller waiting on the quit
// callback hung.
func TestQuitFromKeyBeforeMount(t *testing.T) {
	var log []string
	tui, w := newProbeTUI(t, probe{log: &log, quitOn: "q"})

	quit := 0
	w.OnQuit(func() { quit++ })

	tui.Inject(uv.KeyPressEvent{Code: 'q', Text: "q"})
	settle(t, tui, func() bool { return quit > 0 })

	if quit != 1 {
		t.Fatalf("OnQuit fired %d times, want 1 (log: %v)", quit, log)
	}
}

// Init's own command must survive too, whichever callback happens to run
// Init — here a pre-mount key press rather than OnMount.
func TestInitCommandDeliveredWhenInitRunsBeforeMount(t *testing.T) {
	var log []string
	tui, _ := newProbeTUI(t, probe{
		log:     &log,
		initCmd: func() tea.Msg { return ackMsg{} },
	})

	tui.Inject(uv.KeyPressEvent{Code: 'a', Text: "a"})
	settle(t, tui, func() bool { return count(log, "ack") >= 2 })

	// one from Init's command, one from the key press's command
	if acks := count(log, "ack"); acks != 2 {
		t.Fatalf("got %d acks, want 2 (log: %v)", acks, log)
	}
}

// SendMsg before the component has ever been mounted or focused must not drop
// the resulting command either; it is held until a dispatcher exists.
func TestSendMsgBeforeMountHoldsCommand(t *testing.T) {
	var log []string
	tui := tuist.New(tuist.NewHeadlessTerminal(80, 24))
	w := New(probe{log: &log})

	// Not added to the tree yet: no dispatcher exists, so the command the key
	// produces must be held rather than run with nowhere to deliver its result.
	w.SendMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(w.pending) != 1 {
		t.Fatalf("pending = %d commands, want 1", len(w.pending))
	}

	tui.AddChild(w)
	settle(t, tui, func() bool { return count(log, "ack") > 0 })

	if len(w.pending) != 0 {
		t.Fatalf("pending = %d commands after mount, want 0", len(w.pending))
	}
	if count(log, "ack") == 0 {
		t.Fatalf("command issued before mount was dropped: %v", log)
	}
}

// Mounting normally (no early input) must still run Init exactly once.
func TestInitRunsOnceOnMount(t *testing.T) {
	var log []string
	tui, _ := newProbeTUI(t, probe{log: &log})

	for range 5 {
		tui.Step()
	}

	if inits := count(log, "init"); inits != 1 {
		t.Fatalf("Init ran %d times, want 1 (log: %v)", inits, log)
	}
}

// ---------- command plumbing: Batch, Sequence, WindowSize -------------------

// logMsg is a plain message the cmdProbe records verbatim.
type logMsg string

func say(s string) tea.Cmd { return func() tea.Msg { return logMsg(s) } }

func sayAfter(s string, d time.Duration) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(d)
		return logMsg(s)
	}
}

// cmdProbe is a bubbletea model that answers each key press with a scripted
// command and records every message it observes. Update only ever runs on
// the UI goroutine, so the log needs no locking.
type cmdProbe struct {
	log  *[]string
	cmds map[string]tea.Cmd
}

func (p cmdProbe) Init() tea.Cmd { return nil }

func (p cmdProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return p, p.cmds[msg.String()]
	case tea.WindowSizeMsg:
		*p.log = append(*p.log, fmt.Sprintf("size:%dx%d", msg.Width, msg.Height))
	case logMsg:
		*p.log = append(*p.log, string(msg))
	}
	return p, nil
}

func (p cmdProbe) View() string { return "cmdProbe" }

// newCmdTUI mounts a focused cmdProbe and renders one frame, so commands run
// through the normal post-mount path.
func newCmdTUI(t *testing.T, log *[]string, cmds map[string]tea.Cmd) (*tuist.TUI, *Wrap) {
	t.Helper()
	tui := tuist.New(tuist.NewHeadlessTerminal(80, 24))
	w := New(cmdProbe{log: log, cmds: cmds})
	tui.AddChild(w)
	tui.SetFocus(w)
	tui.Step()
	return tui, w
}

func indexOf(log []string, entry string) int {
	for i, e := range log {
		if e == entry {
			return i
		}
	}
	return -1
}

// tea.Sequence's commands must be delivered in order, even when the first is
// the slowest. Before sequenceMsg handling, the whole sequence was fed to
// Update as an unknown message and every command in it was dropped.
func TestSequenceRunsInOrder(t *testing.T) {
	var log []string
	tui, _ := newCmdTUI(t, &log, map[string]tea.Cmd{
		"s": tea.Sequence(sayAfter("first", 30*time.Millisecond), say("second"), say("third")),
	})

	tui.Inject(uv.KeyPressEvent{Code: 's', Text: "s"})
	settle(t, tui, func() bool { return count(log, "third") > 0 })

	a, b, c := indexOf(log, "first"), indexOf(log, "second"), indexOf(log, "third")
	if a < 0 || b < 0 || c < 0 || a > b || b > c {
		t.Fatalf("sequence delivered out of order: %v", log)
	}
}

// A batch nested in a sequence must complete before the sequence continues,
// mirroring bubbletea's execSequenceMsg.
func TestSequenceWaitsForNestedBatch(t *testing.T) {
	var log []string
	tui, _ := newCmdTUI(t, &log, map[string]tea.Cmd{
		"s": tea.Sequence(
			tea.Batch(sayAfter("b1", 40*time.Millisecond), sayAfter("b2", 10*time.Millisecond)),
			say("after"),
		),
	})

	tui.Inject(uv.KeyPressEvent{Code: 's', Text: "s"})
	settle(t, tui, func() bool { return count(log, "after") > 0 })

	after := indexOf(log, "after")
	if b1, b2 := indexOf(log, "b1"), indexOf(log, "b2"); b1 < 0 || b2 < 0 || b1 > after || b2 > after {
		t.Fatalf("sequence continued before its nested batch finished: %v", log)
	}
}

// A top-level batch delivers every command's message (order unspecified).
func TestBatchDeliversAll(t *testing.T) {
	var log []string
	tui, _ := newCmdTUI(t, &log, map[string]tea.Cmd{
		"b": tea.Batch(say("one"), say("two"), say("three")),
	})

	tui.Inject(uv.KeyPressEvent{Code: 'b', Text: "b"})
	settle(t, tui, func() bool {
		return count(log, "one") > 0 && count(log, "two") > 0 && count(log, "three") > 0
	})

	for _, want := range []string{"one", "two", "three"} {
		if count(log, want) != 1 {
			t.Fatalf("batch delivery wrong: %v", log)
		}
	}
}

// tea.Quit inside a sequence must still fire OnQuit, after the work before it.
func TestQuitInsideSequenceFiresOnQuit(t *testing.T) {
	var log []string
	tui, w := newCmdTUI(t, &log, map[string]tea.Cmd{
		"s": tea.Sequence(say("work"), tea.Quit),
	})
	quit := 0
	w.OnQuit(func() { quit++ })

	tui.Inject(uv.KeyPressEvent{Code: 's', Text: "s"})
	settle(t, tui, func() bool { return quit > 0 })

	if quit != 1 || count(log, "work") != 1 {
		t.Fatalf("quit=%d log=%v, want quit=1 with work delivered", quit, log)
	}
}

// A tea.WindowSize request after the first render is answered with the
// rendered size. (The first render's own WindowSizeMsg accounts for the
// initial entry in the log.)
func TestWindowSizeRequestAnswered(t *testing.T) {
	var log []string
	tui, _ := newCmdTUI(t, &log, map[string]tea.Cmd{
		"w": tea.WindowSize(),
	})

	tui.Inject(uv.KeyPressEvent{Code: 'w', Text: "w"})
	settle(t, tui, func() bool { return count(log, "size:80x24") >= 2 })

	if count(log, "size:80x24") < 2 {
		t.Fatalf("window size request was not answered: %v", log)
	}
}

// The learned internal types must match what the linked bubbletea actually
// produces — this is the canary that fails loudly if a future bubbletea
// changes shape instead of silently regressing to dropped messages.
func TestSequenceTypeLearned(t *testing.T) {
	cmds, ok := asSequence(tea.Sequence(say("a"), say("b"))())
	if !ok || len(cmds) != 2 {
		t.Fatalf("tea.Sequence not recognized: ok=%v len=%d", ok, len(cmds))
	}
	if _, ok := asSequence(tea.Batch(say("a"), say("b"))()); ok {
		t.Fatal("tea.Batch misrecognized as a sequence")
	}
	if reflect.TypeOf(tea.WindowSizeMsg{}) == windowSizeReqType {
		t.Fatal("size request type collides with tea.WindowSizeMsg")
	}
}
