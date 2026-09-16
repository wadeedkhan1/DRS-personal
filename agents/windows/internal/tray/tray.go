// Package tray shows the on-device indicator and minimal system tray menu.
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
	// Unenrolled means the agent is not yet paired with a server.
	Unenrolled
)

// Callbacks holds the user action handlers for menu items.
type Callbacks struct {
	OnReconnect    func()
	OnChangeConfig func()
	OnQuit         func()
}

// Controller updates the tray indicator and menu labels.
type Controller struct {
	mu        sync.Mutex
	statusMI  *systray.MenuItem
	serverMI  *systray.MenuItem
	ready     bool
	callbacks Callbacks
}

// Run starts the system tray loop and blocks until Quit is called or the menu is used to exit.
// systray must own the main goroutine on Windows.
func Run(callbacks Callbacks, onReady func(c *Controller), onExit func()) {
	c := &Controller{callbacks: callbacks}

	systray.Run(func() {
		systray.SetIcon(iconOffline)
		systray.SetTitle("DRS Agent")
		systray.SetTooltip("DRS Agent — starting")

		c.mu.Lock()
		c.statusMI = systray.AddMenuItem("Status: Starting…", "Current connection status")
		c.statusMI.Disable()

		c.serverMI = systray.AddMenuItem("Server: (connecting…)", "Target DRS server")
		c.serverMI.Disable()

		systray.AddSeparator()

		reconnectMI := systray.AddMenuItem("Reconnect", "Force immediate reconnection to the server")
		configMI := systray.AddMenuItem("Change Server / Token…", "Configure server address or enrollment token")

		systray.AddSeparator()

		quitMI := systray.AddMenuItem("Quit DRS Agent", "Disconnect and exit")
		c.ready = true
		c.mu.Unlock()

		go func() {
			for {
				select {
				case <-reconnectMI.ClickedCh:
					if c.callbacks.OnReconnect != nil {
						go c.callbacks.OnReconnect()
					}
				case <-configMI.ClickedCh:
					if c.callbacks.OnChangeConfig != nil {
						go c.callbacks.OnChangeConfig()
					}
				case <-quitMI.ClickedCh:
					if c.callbacks.OnQuit != nil {
						c.callbacks.OnQuit()
					}
					systray.Quit()
					return
				}
			}
		}()

		onReady(c)
	}, onExit)
}

// Quit tears down the system tray.
func Quit() {
	systray.Quit()
}

// Set updates the tray icon, tooltip, and status menu label.
func (c *Controller) Set(s Status) {
	c.mu.Lock()
	ready := c.ready
	item := c.statusMI
	c.mu.Unlock()

	var icon []byte
	var label, tooltip string
	switch s {
	case InSession:
		icon = iconInSession
		label = "Status: Screen is being viewed (Active)"
		tooltip = "DRS Agent — your screen is being viewed"
	case Online:
		icon = iconOnline
		label = "Status: Connected"
		tooltip = "DRS Agent — connected"
	case Unenrolled:
		icon = iconOffline
		label = "Status: Not enrolled"
		tooltip = "DRS Agent — not enrolled"
	default:
		icon = iconOffline
		label = "Status: Reconnecting…"
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

// SetServer updates the server display item in the tray menu.
func (c *Controller) SetServer(serverURL string) {
	c.mu.Lock()
	ready := c.ready
	item := c.serverMI
	c.mu.Unlock()

	if !ready || item == nil {
		return
	}
	if serverURL == "" {
		item.SetTitle("Server: (not configured)")
	} else {
		item.SetTitle("Server: " + serverURL)
	}
}

// SetFatal shows a permanent error state when the server revokes or rejects the device.
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
		item.SetTitle("Status: " + reason)
	}
}
