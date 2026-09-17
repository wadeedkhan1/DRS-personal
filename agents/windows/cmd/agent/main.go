// Command agent is the DRS Windows endpoint agent.
//
//	drs-agent enroll -server https://drs.example.com -token DRS-ABC123
//	drs-agent            (default: connect and stay connected, with a tray icon)
//	drs-agent install    register to start at login
//	drs-agent uninstall  remove the autostart entry
//
// It captures the primary display, encodes VP8, and streams it peer-to-peer to the
// operator's browser. Signaling goes over one outbound WebSocket; the video does not
// touch the server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"drs/agent/windows/internal/config"
	"drs/agent/windows/internal/conn"
	"drs/agent/windows/internal/enroll"
	"drs/agent/windows/internal/tray"
)

func main() {
	// Built with -H windowsgui, so there is no console to print to. Everything goes to
	// a log file next to the identity file.
	closeLog := setupLogging()
	defer closeLog()

	command := ""
	if len(os.Args) > 1 && !isFlag(os.Args[1]) {
		command = os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	switch command {
	case "enroll":
		runEnroll()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "", "run":
		runAgent()
	default:
		fatalf("unknown command %q (expected: enroll, install, uninstall, run)", command)
	}
}

func isFlag(arg string) bool { return len(arg) > 0 && arg[0] == '-' }

func runEnroll() {
	server := flag.String("server", "", "DRS server base URL, e.g. https://drs.example.com")
	token := flag.String("token", "", "Enrollment token from the portal")
	name := flag.String("name", "", "Device name (defaults to this machine's hostname)")
	// There are deliberately no capability flags: every enrollment requests screen and
	// terminal, so the CLI and GUI paths cannot produce differently-capable devices.
	flag.Parse()

	if *server == "" || *token == "" {
		fatalf("both -server and -token are required\n" +
			"example: drs-agent enroll -server https://drs.example.com -token DRS-ABC123")
	}

	cfg, err := enroll.Enroll(*server, *token, *name)
	if err != nil {
		fatalf("enrollment failed: %v", err)
	}
	if err := config.Save(cfg); err != nil {
		fatalf("could not save agent identity: %v", err)
	}

	path, _ := config.DefaultPath()
	report("Enrolled successfully.\nDevice ID: %s\nIdentity saved to: %s\n\n"+
		"Start the agent with:  drs-agent\nStart it at login with: drs-agent install", cfg.DeviceID, path)
}

func runInstall() {
	flag.Parse()
	exe, err := os.Executable()
	if err != nil {
		fatalf("could not determine this executable's path: %v", err)
	}
	if err := installAutostart(exe); err != nil {
		fatalf("could not register autostart: %v", err)
	}
	report("Registered to start at login: %s", filepath.Base(exe))
}

func runUninstall() {
	flag.Parse()
	if err := uninstallAutostart(); err != nil {
		fatalf("could not remove autostart: %v", err)
	}
	report("Autostart removed.")
}

func runAgent() {
	// Keep -startup flag parsed for backwards compatibility with autostart entries.
	_ = flag.Bool("startup", false, "start hidden in the system tray (used at login)")
	flag.Parse()

	// One agent per login session. Two copies would authenticate as the same device and
	// evict each other in a reconnect loop.
	release, acquired := acquireSingleInstance()
	if !acquired {
		fatalf("The DRS agent is already running.\n\n" +
			"Look for its icon in the system tray. Use the tray menu to quit it if you " +
			"want to start a different build.")
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reconnectCh := make(chan struct{}, 1)
	triggerReconnect := func() {
		select {
		case reconnectCh <- struct{}{}:
		default:
		}
	}

	var trayCtrl *tray.Controller
	var trayMu sync.Mutex

	setTray := func(f func(c *tray.Controller)) {
		trayMu.Lock()
		defer trayMu.Unlock()
		if trayCtrl != nil {
			f(trayCtrl)
		}
	}

	var connCancel context.CancelFunc
	var connMu sync.Mutex

	startConnection := func(cfg config.Config) {
		connMu.Lock()
		if connCancel != nil {
			connCancel()
		}
		var connCtx context.Context
		connCtx, connCancel = context.WithCancel(ctx)
		connMu.Unlock()

		setTray(func(c *tray.Controller) {
			c.SetServer(cfg.ServerURL)
			c.Set(tray.Offline)
		})

		go func() {
			onStatus := func(online, inSession bool) {
				setTray(func(c *tray.Controller) {
					switch {
					case inSession:
						c.Set(tray.InSession)
					case online:
						c.Set(tray.Online)
					default:
						c.Set(tray.Offline)
					}
				})
			}

			err := conn.Run(connCtx, cfg, onStatus, reconnectCh)
			if err != nil {
				setTray(func(c *tray.Controller) {
					c.SetFatal("Disconnected: " + err.Error())
				})
			}
		}()
	}

	callbacks := tray.Callbacks{
		OnReconnect: func() {
			triggerReconnect()
		},
		OnChangeConfig: func() {
			cfg, _ := config.Load()
			// A downloaded agent already carries its server address and invite token in
			// the trailer, so offer both as the dialog's defaults. This used to pass an
			// empty token, which left the field blank on a binary that knew the answer
			// perfectly well — the only way to fill it in was to go and find the invite
			// link again on another machine.
			//
			// The trailer wins over the saved identity when it has one. It records where
			// whoever handed out this executable wants the device to point *now*, while
			// the saved serverUrl records wherever it last happened to enroll — which,
			// on a machine that has been re-pointed, is precisely the stale value the
			// operator opened this dialog to correct.
			server, token := cfg.ServerURL, ""
			if emb, embErr := config.ReadEmbedded(); embErr == nil {
				server = emb.ServerURL
				token = emb.Token
			}
			server, token, ok := tray.PromptCredentials(server, token)
			if !ok {
				return
			}
			newCfg, err := enrollWithRetry(server, token, 3)
			if err != nil {
				showMessage("DRS Agent — Enrollment Failed", err.Error())
				return
			}
			if err := config.Save(newCfg); err != nil {
				showMessage("DRS Agent — Save Failed", err.Error())
				return
			}
			startConnection(newCfg)
		},
		OnQuit: func() {
			cancel()
			connMu.Lock()
			if connCancel != nil {
				connCancel()
			}
			connMu.Unlock()
		},
	}

	onReady := func(c *tray.Controller) {
		trayMu.Lock()
		trayCtrl = c
		trayMu.Unlock()

		// 1. If already enrolled on this machine, connect immediately.
		cfg, err := config.Load()
		if err == nil && cfg.Enrolled() {
			log.Printf("agent: loaded existing identity for device %s", cfg.DeviceID)
			startConnection(cfg)
			return
		}

		// 2. If the executable has baked-in configuration, self-enroll with up to 3 retries.
		embedded, embErr := config.ReadEmbedded()
		if embErr == nil {
			c.Set(tray.Offline)
			newCfg, enrollErr := enrollWithRetry(embedded.ServerURL, embedded.Token, 3)
			if enrollErr == nil {
				if err := config.Save(newCfg); err == nil {
					if embedded.Autostart {
						if exe, exeErr := os.Executable(); exeErr == nil {
							_ = installAutostart(exe)
						}
					}
					startConnection(newCfg)
					return
				}
				log.Printf("agent: failed to save identity: %v", err)
			} else {
				log.Printf("agent: self-enrollment failed after 3 attempts: %v", enrollErr)
			}
		}

		// 3. Neither enrolled nor self-enrolled: sit in tray and await configuration.
		// Name the address self-enrollment was aiming at, when the binary carries one.
		// Blanking it here meant a failed self-enrollment looked identical to a plain
		// developer build, which is the one case where knowing the baked-in server is
		// what tells you the download was configured wrong.
		c.Set(tray.Unenrolled)
		if embErr == nil {
			c.SetServer(embedded.ServerURL)
		} else {
			c.SetServer("")
		}
	}

	// Systray must own the main goroutine on Windows.
	tray.Run(callbacks, onReady, func() {
		cancel()
	})
	log.Println("agent: stopped")
}

func enrollWithRetry(serverURL, token string, maxAttempts int) (config.Config, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		log.Printf("agent: enrolling against %s (attempt %d/%d)", serverURL, attempt, maxAttempts)
		cfg, err := enroll.Enroll(serverURL, token, "")
		if err == nil {
			log.Printf("agent: enrolled successfully as device %s", cfg.DeviceID)
			return cfg, nil
		}
		lastErr = err
		log.Printf("agent: enrollment attempt %d failed: %v", attempt, err)
		if attempt < maxAttempts {
			time.Sleep(2 * time.Second)
		}
	}
	return config.Config{}, lastErr
}

// setupLogging sends output to %AppData%\drs\agent.log.
func setupLogging() func() {
	path, err := config.DefaultPath()
	if err != nil {
		return func() {}
	}
	logPath := filepath.Join(filepath.Dir(path), "agent.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return func() {}
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	return func() { _ = f.Close() }
}

// report shows a message to whoever ran the command. With no console attached, a
// message box is the only thing the user will actually see.
func report(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	showMessage("DRS Agent", msg)
}

func fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	showMessage("DRS Agent", msg)
	os.Exit(1)
}
