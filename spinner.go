package tuist

import (
	"time"
)

// Spinner is a component that shows an animated spinner. It starts
// spinning automatically when mounted (added to a TUI-rooted tree)
// and stops when dismounted.
type Spinner struct {
	Compo

	// Style wraps each frame (e.g. to apply color). May be nil.
	Style func(string) string
	// Label is displayed after the spinner frame.
	Label string
	// Epoch, if non-zero, is used as the animation start time instead
	// of the mount time. Set this to a shared value so multiple
	// spinners animate in sync.
	Epoch time.Time

	frames   []string
	interval time.Duration
	start    time.Time
}

// NewSpinner creates a dot-style spinner.
func NewSpinner() *Spinner {
	return &Spinner{
		frames:   []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		interval: 80 * time.Millisecond,
	}
}

// OnMount starts the spinner animation. The goroutine is bounded by
// ctx.Done(), which fires when the component is dismounted.
func (s *Spinner) OnMount(ctx Context) {
	if !s.Epoch.IsZero() {
		s.start = s.Epoch
	} else {
		s.start = time.Now()
	}
	ticker := time.NewTicker(s.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx.Dispatch(func() {
					s.Update()
				})
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Spinner) Render(ctx Context) {
	elapsed := time.Since(s.start)
	idx := int(elapsed/s.interval) % len(s.frames)
	frame := s.frames[idx]
	if s.Style != nil {
		frame = s.Style(frame)
	}
	if s.Label != "" {
		frame += " " + s.Label
	}
	ctx.Line(frame)
}

