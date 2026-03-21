// Command tuist-frames is a frame-by-frame viewer for TUIST_FRAMES output.
//
// Usage:
//
//	go run ./cmd/tuist-frames /tmp/frames
//
// Keys: ← previous, → next, Home first, End last, q quit.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/vito/tuist"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: tuist-frames <dir>\n")
		os.Exit(1)
	}
	dir := os.Args[1]

	// Load all frames eagerly — they're small text files.
	var frames []string
	for i := 0; ; i++ {
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.txt", i)))
		if err != nil {
			break
		}
		frames = append(frames, string(data))
	}
	if len(frames) == 0 {
		fmt.Fprintf(os.Stderr, "no frames found in %s\n", dir)
		os.Exit(1)
	}

	term := tuist.NewStdTerminal()
	tui := tuist.New(term)

	viewer := &frameViewer{frames: frames}
	tui.AddChild(viewer)
	tui.SetFocus(viewer)

	if err := tui.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Wait for quit signal or interrupt.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-viewer.quit:
	case <-sig:
	}

	tui.Stop()
}

type frameViewer struct {
	tuist.Compo
	frames []string
	index  int
	quit   chan struct{}
}

func (v *frameViewer) OnMount(ctx tuist.Context) {
	v.quit = make(chan struct{})
}

func (v *frameViewer) HandleKeyPress(ctx tuist.Context, ev uv.KeyPressEvent) bool {
	key := uv.Key(ev)
	switch {
	case key.Text == "q" || (key.Code == 'c' && key.Mod == uv.ModCtrl):
		close(v.quit)
		return true
	case key.Code == uv.KeyLeft, key.Text == "h":
		if v.index > 0 {
			v.index--
			v.Update()
		}
		return true
	case key.Code == uv.KeyRight, key.Text == "l":
		if v.index < len(v.frames)-1 {
			v.index++
			v.Update()
		}
		return true
	case key.Code == uv.KeyHome, key.Text == "g":
		v.index = 0
		v.Update()
		return true
	case key.Code == uv.KeyEnd, key.Text == "G":
		v.index = len(v.frames) - 1
		v.Update()
		return true
	}
	return false
}

func (v *frameViewer) Render(ctx tuist.Context) {
	lines := strings.Split(strings.TrimRight(v.frames[v.index], "\n"), "\n")
	ctx.Lines(lines...)
	footer := fmt.Sprintf("frame %d / %d  [←/→ step  Home/End jump  q quit]", v.index, len(v.frames)-1)
	ctx.Line(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Width(ctx.Width).PaddingChar('─').Render(footer))
}
