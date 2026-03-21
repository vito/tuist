package vt_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vito/tuist/vt"
	"gotest.tools/v3/golden"
)

// loadFrames reads numbered frame files (0.txt, 1.txt, ...) from dir.
func loadFrames(t *testing.T, dir string) []vt.Frame {
	t.Helper()
	var frames []vt.Frame
	for i := 0; ; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%d.txt", i))
		f, err := vt.ParseFrameFile(path)
		if err != nil {
			if i == 0 {
				t.Fatalf("no frames in %s: %v", dir, err)
			}
			break
		}
		frames = append(frames, f)
	}
	return frames
}

func TestFrames_StatusBarFocus(t *testing.T) {
	frames := loadFrames(t, "testdata/frames/status_bar_focus")
	term := vt.Replay(frames, 0)
	golden.Assert(t, term.Render(), "golden/frames_status_bar_focus.golden")
}
