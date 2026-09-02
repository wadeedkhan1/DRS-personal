//go:build windows

// Package gui is the agent's window. It replaces the enroll-on-the-command-line, then
// run-again flow with a single application the user double-clicks: paste the invite link,
// choose what to share, and click Connect. Once enrolled it shows connection status and
// lives in the system tray.
//
// The whole app runs on one Fyne event loop that owns the main goroutine (a Windows
// requirement for the tray's message pump). The WebSocket connection runs on its own
// goroutine and reports status back through fyne.Do so widget updates happen on the main
// thread.
package gui

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"drs/agent/windows/internal/config"
	"drs/agent/windows/internal/conn"
	"drs/agent/windows/internal/enroll"
)

// gui holds the widgets and goroutine state the callbacks need to reach.
type gui struct {
	app fyne.App
	win fyne.Window

	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	quitting    bool
	statusLabel *widget.Label

	hostname string
}

// Run launches the GUI and blocks until the user quits. startMinimized hides the window
// at launch and shows only the tray icon; it is passed when the agent starts at login so
// a window does not pop up in the user's face every time they sign in.
func Run(startMinimized bool) {
	a := app.NewWithID("com.drs.agent")

	w := a.NewWindow("DRS Agent")
	w.Resize(fyne.NewSize(480, 460))

	ctx, cancel := context.WithCancel(context.Background())

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "this PC"
	}

	u := &gui{app: a, win: w, ctx: ctx, cancel: cancel, hostname: host}
	u.setupTray()

	// Closing the window hides it to the tray rather than quitting, so the connection
	// keeps running in the background. Quitting is deliberate, via the tray menu.
	w.SetCloseIntercept(func() { w.Hide() })

	cfg, _ := config.Load()
	if cfg.Enrolled() {
		u.showStatus(cfg)
		u.startConnection(cfg)
		if !startMinimized {
			w.Show()
		}
	} else {
		u.showEnroll()
		w.Show()
	}

	a.Run()
}

func (u *gui) setupTray() {
	desk, ok := u.app.(desktop.App)
	if !ok {
		return
	}
	menu := fyne.NewMenu("DRS Agent",
		fyne.NewMenuItem("Open DRS Agent", func() { u.win.Show() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { u.quit() }),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(iconOffline)
}

// showEnroll builds the first-run form: where to connect, and what to expose.
func (u *gui) showEnroll() {
	title := widget.NewLabelWithStyle("Connect this PC to DRS", fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true})
	intro := widget.NewLabel("Paste the invite link your administrator sent you.")
	intro.Wrapping = fyne.TextWrapWord

	linkEntry := widget.NewEntry()
	linkEntry.SetPlaceHolder("https://server:8080/enroll?token=DRS-…  (or just the server URL)")

	tokenEntry := widget.NewEntry()
	tokenEntry.SetPlaceHolder("Enrollment token (DRS-…)")

	// Pasting a full invite link fills the token in automatically and leaves just the
	// server address behind, so the two fields never fight over the same text.
	linkEntry.OnChanged = func(s string) {
		if server, tok, ok := splitInvite(s); ok {
			if tok != "" {
				tokenEntry.SetText(tok)
			}
			if server != s {
				linkEntry.SetText(server)
			}
		}
	}

	hostLabel := widget.NewLabelWithStyle("This PC will appear as:  "+u.hostname,
		fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	screenCheck := widget.NewCheck("Allow screen sharing", nil)
	screenCheck.SetChecked(true)
	termCheck := widget.NewCheck("Allow terminal access (run commands remotely)", nil)
	termCheck.SetChecked(false)

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	status.Hide()

	var connectBtn *widget.Button
	connectBtn = widget.NewButton("Connect", func() {
		server, tok, _ := splitInvite(strings.TrimSpace(linkEntry.Text))
		if strings.TrimSpace(tokenEntry.Text) != "" {
			tok = strings.TrimSpace(tokenEntry.Text)
		}
		allowScreen := screenCheck.Checked
		allowTerminal := termCheck.Checked

		if server == "" {
			u.setInlineStatus(status, "Enter the invite link or server address.", true)
			return
		}
		if tok == "" {
			u.setInlineStatus(status, "Enter the enrollment token.", true)
			return
		}
		if !allowScreen && !allowTerminal {
			u.setInlineStatus(status, "Choose at least one: screen sharing or terminal access.", true)
			return
		}

		connectBtn.Disable()
		u.setInlineStatus(status, "Connecting…", false)

		go func() {
			cfg, err := enroll.Enroll(server, tok, "", allowScreen, allowTerminal)
			if err == nil {
				err = config.Save(cfg)
			}
			fyne.Do(func() {
				if err != nil {
					connectBtn.Enable()
					u.setInlineStatus(status, "Could not connect: "+err.Error(), true)
					return
				}
				u.showStatus(cfg)
				u.startConnection(cfg)
			})
		}()
	})
	connectBtn.Importance = widget.HighImportance

	form := container.NewVBox(
		title,
		intro,
		widget.NewForm(
			widget.NewFormItem("Invite link / server", linkEntry),
			widget.NewFormItem("Token", tokenEntry),
		),
		hostLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("What may operators do on this PC?", fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true}),
		screenCheck,
		termCheck,
		connectBtn,
		status,
	)

	u.win.SetContent(container.NewPadded(form))
}

// showStatus swaps the window to the connected view. The status label is updated live by
// onStatus as the connection changes state.
func (u *gui) showStatus(cfg config.Config) {
	u.mu.Lock()
	u.statusLabel = widget.NewLabelWithStyle("Connecting…", fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true})
	label := u.statusLabel
	u.mu.Unlock()

	shares := shareSummary(cfg)

	info := widget.NewForm(
		widget.NewFormItem("This PC", widget.NewLabel(u.hostname)),
		widget.NewFormItem("Server", widget.NewLabel(cfg.ServerURL)),
		widget.NewFormItem("Sharing", widget.NewLabel(shares)),
	)

	hideBtn := widget.NewButton("Hide to tray", func() { u.win.Hide() })
	reenrollBtn := widget.NewButton("Re-enroll / change sharing", func() { u.showEnroll() })

	note := widget.NewLabel("This agent keeps running in the background. A tray icon turns red " +
		"while your screen is being viewed.")
	note.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		widget.NewLabelWithStyle("DRS Agent", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		label,
		widget.NewSeparator(),
		info,
		note,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, reenrollBtn, hideBtn),
	)

	u.win.SetContent(container.NewPadded(content))
}

// startConnection runs the socket loop on its own goroutine, reporting state back to the
// UI and the tray.
func (u *gui) startConnection(cfg config.Config) {
	go func() {
		err := conn.Run(u.ctx, cfg, u.onStatus)
		if err != nil {
			u.onFatal(err)
		}
	}()
}

// onStatus is called from the connection goroutine on every state change. It marshals the
// update onto the UI thread and refreshes both the tray icon and the status line.
func (u *gui) onStatus(online, inSession bool) {
	if u.isQuitting() {
		return
	}

	var icon fyne.Resource
	var text string
	switch {
	case inSession:
		icon, text = iconInSession, "🔴  Your screen is being viewed"
	case online:
		icon, text = iconOnline, "🟢  Connected — ready when an operator views this PC"
	default:
		icon, text = iconOffline, "⚪  Reconnecting to the server…"
	}

	fyne.Do(func() {
		if desk, ok := u.app.(desktop.App); ok {
			desk.SetSystemTrayIcon(icon)
		}
		u.mu.Lock()
		label := u.statusLabel
		u.mu.Unlock()
		if label != nil {
			label.SetText(text)
		}
	})
}

// onFatal is reached when the server permanently rejects this device (deleted or
// re-enrolled elsewhere). The user is shown the reason rather than the agent vanishing.
func (u *gui) onFatal(err error) {
	if u.isQuitting() {
		return
	}
	fyne.Do(func() {
		if desk, ok := u.app.(desktop.App); ok {
			desk.SetSystemTrayIcon(iconOffline)
		}
		u.mu.Lock()
		label := u.statusLabel
		u.mu.Unlock()
		if label != nil {
			label.SetText("Disconnected: " + err.Error())
		}
		u.win.Show()
	})
}

func (u *gui) quit() {
	u.mu.Lock()
	u.quitting = true
	u.mu.Unlock()
	u.cancel()
	u.app.Quit()
}

func (u *gui) isQuitting() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.quitting
}

func (u *gui) setInlineStatus(l *widget.Label, msg string, isError bool) {
	l.SetText(msg)
	l.Show()
	_ = isError // colour is not themed here; the message text carries the meaning
}

// splitInvite pulls a server base URL and (if present) a token out of whatever the user
// pasted. It accepts a full invite link (https://host:port/enroll?token=DRS-…), a bare
// server URL, or a host:port. ok is false when the input is empty.
func splitInvite(s string) (server, token string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	// Give a scheme-less host:port a scheme so url.Parse treats it as a host, not a path.
	toParse := s
	if !strings.Contains(s, "://") {
		toParse = "http://" + s
	}
	if u, err := url.Parse(toParse); err == nil && u.Host != "" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "http"
		}
		server = scheme + "://" + u.Host
		token = u.Query().Get("token")
		return server, token, true
	}
	return strings.TrimRight(s, "/"), "", true
}

// shareSummary describes what the device consented to, for the status screen.
func shareSummary(cfg config.Config) string {
	switch {
	case cfg.AllowScreen && cfg.AllowTerminal:
		return "Screen + Terminal"
	case cfg.AllowScreen:
		return "Screen only"
	case cfg.AllowTerminal:
		return "Terminal only"
	default:
		return "Nothing"
	}
}
