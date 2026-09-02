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
	// The GUI is the normal way to choose these; the flags exist so the CLI path can too.
	// The defaults match the server's: screen on, terminal off.
	screen := flag.Bool("screen", true, "allow screen sharing")
	terminal := flag.Bool("terminal", false, "allow remote terminal access")
	flag.Parse()

	if *server == "" || *token == "" {
		fatalf("both -server and -token are required\n" +
			"example: drs-agent enroll -server https://drs.example.com -token DRS-ABC123")
	}

	cfg, err := enroll.Enroll(*server, *token, *name, *screen, *terminal)
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

	// The GUI owns the main goroutine: on Windows the tray's message loop has to run on
	// the thread the process started on. It handles enrollment (when the device is not yet
	// enrolled) and the live connection itself, so there is nothing to set up here first.
	gui.Run(*startup)
	log.Println("agent: stopped")
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
