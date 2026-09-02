// Package tray shows the on-device indicator.
//
// This is a compliance requirement, not decoration: SRS FR-5.5 and assumption A4 both
// call for a visible sign on the monitored machine, and employee-monitoring law in many
// jurisdictions expects the person to be able to tell when their screen is being
// watched. The icon turns red for the entire duration of a session.
package tray

import (
	"log"
	"sync"

	"fyne.io/systray"
)

// Status is what the icon is currently showing.
type Status int

const (
	// Offline means the agent is running but not connected to the server.
	Offline Status = iota
	// Online means connected and available to be viewed.
	Online
	// InSession means someone is watching this screen right now.
	InSession
)

// Controller updates the indicator.
type Controller struct {
	mu       sync.Mutex
	statusMI *systray.MenuItem
	ready    bool
}

// Run starts the tray and blocks until Quit is called or the menu is used to exit.
//
// systray must own the main goroutine on Windows, so this is called from main and the
// real work happens in the callback.
func Run(onReady func(c *Controller), onExit func()) {
	c := &Controller{}
	systray.Run(func() {
		systray.SetIcon(iconOffline)
		systray.SetTitle("DRS Agent")
		systray.SetTooltip("DRS Agent — starting")

		c.mu.Lock()
		c.statusMI = systray.AddMenuItem("Starting…", "Current connection status")
		c.statusMI.Disable()
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit DRS Agent", "Disconnect and exit")
		c.ready = true
		c.mu.Unlock()

		go func() {
			<-quit.ClickedCh
			systray.Quit()
		}()

		onReady(c)
	}, onExit)
}

// Quit tears the tray down, which unblocks Run.
func Quit() { systray.Quit() }

// Set updates the icon, tooltip and menu label.
//
// Safe to call from the socket goroutine, and safe to call before the tray finishes
// initialising: the status item is simply skipped until it exists.
func (c *Controller) Set(s Status) {
	c.mu.Lock()
	ready := c.ready
	item := c.statusMI
	c.mu.Unlock()

	var icon []byte
	var label, tooltip string
	switch s {
	case InSession:
		icon, label = iconInSession, "Screen is being viewed"
		tooltip = "DRS Agent — your screen is being viewed"
	case Online:
		icon, label = iconOnline, "Connected"
		tooltip = "DRS Agent — connected"
	default:
		icon, label = iconOffline, "Reconnecting…"
		tooltip = "DRS Agent — reconnecting"
	}

	if !ready {
		return
	}
	systray.SetIcon(icon)
	systray.SetTooltip(tooltip)
	if item != nil {
		item.SetTitle(label)
	}
}

// SetFatal shows a permanent error state, used when the server has revoked this device.
func (c *Controller) SetFatal(reason string) {
	c.mu.Lock()
	item := c.statusMI
	ready := c.ready
	c.mu.Unlock()

	log.Printf("agent: %s", reason)
	if !ready {
		return
	}
	systray.SetIcon(iconOffline)
	systray.SetTooltip("DRS Agent — " + reason)
	if item != nil {
		item.SetTitle(reason)
	}
}
