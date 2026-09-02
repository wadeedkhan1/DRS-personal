//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// runKey is the per-user autostart location.
//
// HKCU rather than HKLM, and a login entry rather than a Windows service, for a
// concrete reason: a service runs in session 0, which has no visible desktop, so screen
// capture there returns either nothing or a blank desktop. The agent has to run inside
// the user's interactive session to see what the user sees. It also means no admin
// rights are needed to install it.
const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runKeyName = "DRSAgent"
)

func installAutostart(exePath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	// Quoted so a path containing spaces survives.
	return key.SetStringValue(runKeyName, `"`+exePath+`"`)
}

func uninstallAutostart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	err = key.DeleteValue(runKeyName)
	if err == registry.ErrNotExist {
		return nil // already absent; nothing to undo
	}
	return err
}

// acquireSingleInstance stops a second agent running for this user.
//
// Without it, two copies authenticate as the same device, and because the backend evicts
// the older socket on every connect they knock each other offline in a loop: each
// eviction triggers a reconnect, which evicts the other, forever. The device flickers
// between online and offline and no session can stay up.
//
// The name is Local\ scoped, so it is per-login-session. That is the right granularity:
// the agent has to run inside the user's interactive session to capture their screen, so
// one instance per session, not one per machine.
func acquireSingleInstance() (release func(), acquired bool) {
	noop := func() {}

	name, err := windows.UTF16PtrFromString(`Local\DRSAgentSingleInstance`)
	if err != nil {
		return noop, true // never block startup on the guard itself
	}

	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		// CreateMutex still returns a valid handle when the mutex already exists, so it
		// has to be closed even on this path.
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return noop, false
		}
		return noop, true
	}
	return func() { _ = windows.CloseHandle(handle) }, true
}

// showMessage puts up a dialog, which is the only feedback available in a windowsgui
// build with no console attached.
func showMessage(title, body string) {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	b, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return
	}
	_, _ = windows.MessageBox(0, b, t, windows.MB_OK|windows.MB_ICONINFORMATION)
}
