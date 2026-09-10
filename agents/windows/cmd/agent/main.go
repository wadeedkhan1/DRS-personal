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
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"drs/agent/windows/internal/config"
	"drs/agent/windows/internal/enroll"
	"drs/agent/windows/internal/gui"
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
	// -startup is set by the autostart entry so the agent comes up hidden in the tray at
	// login instead of popping a window open every time the user signs in.
	startup := flag.Bool("startup", false, "start hidden in the system tray (used at login)")
	flag.Parse()

	// One agent per login session. Two copies would authenticate as the same device and
	// evict each other in a reconnect loop.
	release, acquired := acquireSingleInstance()
	if !acquired {
		fatalf("The DRS agent is already running.%s%s", "\n\n",
			"Look for its icon in the system tray. Use the tray menu to quit it if you "+
				"want to start a different build.")
	}
	defer release()

	// A binary handed out through an invite link carries its own configuration, so it can
	// enroll before showing anything. Failures here are not fatal: they fall through to
	// the manual GUI, which is pre-filled from the same configuration.
	//
	// A zero-touch binary that is ready goes **straight to the tray**: there is nothing to
	// tell the user and nothing to ask them, so a window would only be something to close.
	// Self-enrollment failing is the one case that does need the window — gui.Run always
	// shows it when the device is not enrolled, so that path needs no special handling
	// here.
	silent := selfEnroll()

	// The GUI owns the main goroutine: on Windows the tray's message loop has to run on
	// the thread the process started on. It handles enrollment (when the device is not yet
	// enrolled) and the live connection itself, so there is nothing to set up here first.
	gui.Run(*startup || silent)
	log.Println("agent: stopped")
}

// selfEnroll enrolls this machine from configuration appended to the executable by the
// download endpoint, so an agent sent through an invite link needs nothing typed.
//
// Every exit is silent and non-fatal. This runs before the window appears, and a machine
// whose enrollment failed should get the ordinary form — pre-filled — rather than an
// error dialog nobody can act on.
//
// It reports whether this is a zero-touch binary that is ready to run, which is what
// decides between going straight to the tray and opening a window. True means "there is
// nothing to show the user": either it just enrolled, or it is a configured binary being
// run again on a machine that is already enrolled. False means either an ordinary
// hand-configured build, or a self-enrollment that failed and needs the form.
func selfEnroll() (silent bool) {
	// Never re-enroll. Enrolling rotates the agent secret, so doing it on every launch of
	// an already-configured agent would invalidate the identity the previous run was
	// using and churn the device row on every reboot.
	//
	// An already-enrolled machine running a zero-touch binary still starts silently: the
	// recipient double-clicked an installer, and a window they have to close is not what
	// they were promised.
	if existing, err := config.Load(); err == nil && existing.Enrolled() {
		_, embErr := config.ReadEmbedded()
		return embErr == nil
	}

	embedded, err := config.ReadEmbedded()
	if err != nil {
		if !errors.Is(err, config.ErrNoEmbedded) {
			// A damaged trailer is worth a log line: it means the download was truncated,
			// which is otherwise invisible and looks like the feature simply not working.
			log.Printf("agent: embedded configuration unusable: %v", err)
		}
		return false
	}

	log.Printf("agent: self-enrolling against %s", embedded.ServerURL)
	cfg, err := enroll.Enroll(embedded.ServerURL, embedded.Token, "")
	if err != nil {
		// Most often the server is unreachable from this machine, or the invite link has
		// been revoked. Show the window: the GUI says so with the fields already filled in.
		log.Printf("agent: self-enrollment failed: %v", err)
		return false
	}
	if err := config.Save(cfg); err != nil {
		log.Printf("agent: could not save the identity from self-enrollment: %v", err)
		return false
	}
	log.Printf("agent: self-enrolled as device %s", cfg.DeviceID)

	if embedded.Autostart {
		// Registering autostart is what makes "run it once" mean "this machine is
		// managed" rather than "managed until it reboots". Failure is not worth aborting
		// an otherwise successful enrollment over, and not worth showing a window for.
		if exe, exeErr := os.Executable(); exeErr != nil {
			log.Printf("agent: could not determine the executable path for autostart: %v", exeErr)
		} else if err := installAutostart(exe); err != nil {
			log.Printf("agent: could not register autostart: %v", err)
		} else {
			log.Println("agent: registered to start at login")
		}
	}

	return true
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
