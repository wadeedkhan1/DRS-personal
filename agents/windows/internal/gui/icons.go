//go:build windows

package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"fyne.io/fyne/v2"
)

// trayIcon renders a filled circle of the given colour as a PNG resource for the system
// tray. Fyne wants a fyne.Resource and renders PNG cleanly, so the icons are generated
// here rather than shipped as files — the three states differ only by colour, exactly as
// the old .ico tray did.
func trayIcon(name string, r, g, b uint8) fyne.Resource {
	const size = 32
	const centre = (size - 1) / 2.0
	const radius = 14.0

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - centre
			dy := float64(y) - centre
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 0xFF})
			} else {
				img.Set(x, y, color.RGBA{})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return fyne.NewStaticResource(name, buf.Bytes())
}

var (
	// Grey: running but not connected to the server.
	iconOffline = trayIcon("drs-offline.png", 0x94, 0xA3, 0xB8)
	// Blue: connected and available to be viewed.
	iconOnline = trayIcon("drs-online.png", 0x38, 0xBD, 0xF8)
	// Red: someone is watching this screen right now.
	iconInSession = trayIcon("drs-insession.png", 0xE1, 0x1D, 0x48)
)
