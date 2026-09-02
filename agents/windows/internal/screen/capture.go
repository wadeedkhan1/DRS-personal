package screen

import (
	"errors"
	"fmt"
	"image"

	"github.com/kbinani/screenshot"
)

// capture grabs the primary display.
//
// kbinani/screenshot dispatches to GDI on Windows. That is a deliberate choice over the
// Desktop Duplication API: DXGI is faster, but it needs a per-adapter duplication object
// that breaks on resolution changes, UAC's secure desktop and session switches, each of
// which has to be detected and recovered from.
//
// If capture ever becomes the measured bottleneck this is the first thing to revisit —
// but as of the pipeline rework the expensive stages are conversion and encoding, and
// both now run in parallel with this one.
func capture() (image.Image, error) {
	if screenshot.NumActiveDisplays() == 0 {
		// Almost always means the process has no desktop session: an agent installed as
		// a system service lands in session 0, which has no visible desktop to capture.
		// Hence the per-user autostart in cmd/agent.
		return nil, errors.New("no active display found: the agent is running outside a " +
			"desktop session, so there is nothing to capture")
	}
	img, err := screenshot.CaptureDisplay(0)
	if err != nil {
		return nil, fmt.Errorf("capture display: %w", err)
	}
	return img, nil
}

func clamp(v, lo, hi, def int) int {
	switch {
	case v == 0:
		return def
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
