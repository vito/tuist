package tuist_test

import (
	"fmt"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/tuist"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	countStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	keyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
)

// Label is a static single-line component.
type Label struct {
	tuist.Compo
	Text string
}

func (l *Label) Render(ctx tuist.RenderContext) tuist.RenderResult {
	return tuist.RenderResult{Lines: []string{l.Text}}
}

// Counter increments on each key press, and 'q' quits.
type Counter struct {
	tuist.Compo
	Count   int
	quit    func()
	focused bool
}

func (c *Counter) Render(ctx tuist.RenderContext) tuist.RenderResult {
	return tuist.RenderResult{Lines: []string{countStyle.Render(fmt.Sprintf("%d", c.Count))}}
}

var _ tuist.Interactive = (*Counter)(nil)

func (c *Counter) HandleKeyPress(_ tuist.EventContext, ev uv.KeyPressEvent) bool {
	if ev.Text == "q" {
		c.quit()
		return true
	}
	c.Count++
	c.Update()
	return true
}

var _ tuist.Focusable = (*Counter)(nil)

func (c *Counter) SetFocused(_ tuist.EventContext, focused bool) { c.focused = focused }

func Example() {
	term := tuist.NewProcessTerminal()
	tui := tuist.New(term)

	if err := tui.Start(); err != nil {
		panic(err)
	}
	defer tui.Stop()

	done := make(chan struct{})
	counter := &Counter{quit: func() { close(done) }}

	// All component mutations must happen on the UI goroutine.
	tui.Dispatch(func() {
		tui.AddChild(&Label{Text: titleStyle.Render("● Counter")})
		tui.AddChild(counter)
		tui.AddChild(&Label{
			Text: keyStyle.Render("any key") + hintStyle.Render(" increment  ") +
				keyStyle.Render("q") + hintStyle.Render(" quit"),
		})
		tui.SetFocus(counter)
	})

	<-done
}
