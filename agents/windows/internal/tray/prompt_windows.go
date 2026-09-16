//go:build windows

package tray

import (
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"drs/agent/windows/internal/config"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procSetFocus            = user32.NewProc("SetFocus")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procIsDialogMessageW    = user32.NewProc("IsDialogMessageW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")

	procGetStockObject = gdi32.NewProc("GetStockObject")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wsOverlapped   = 0x00000000
	wsPopup        = 0x80000000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsBorder       = 0x00800000
	wsTabStop      = 0x00010000
	wsExTopMost    = 0x00000008
	wsExDlgModal   = 0x00000001
	wsExClientEdge = 0x00000200

	esAutoHScroll = 0x0080

	bsDefPushButton = 0x00000001
	bsPushButton    = 0x00000000

	wmCommand = 0x0111
	wmDestroy = 0x0002
	wmSetFont = 0x0030
	wmClose   = 0x0010

	smCxScreen = 0
	smCyScreen = 1

	defaultGUIFont = 17 // DEFAULT_GUI_FONT

	swShow = 5

	idBtnConnect = 1
	idBtnCancel  = 2
	idEditServer = 101
	idEditToken  = 102
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type point struct {
	x, y int32
}

type msg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type dialogState struct {
	serverEdit windows.Handle
	tokenEdit  windows.Handle
	serverVal  string
	tokenVal   string
	submitted  bool
}

var activeDialogState *dialogState

func dialogWndProc(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCommand:
		cmdID := uint16(wParam & 0xFFFF)
		if cmdID == idBtnConnect {
			if activeDialogState != nil {
				activeDialogState.serverVal = getControlText(activeDialogState.serverEdit)
				activeDialogState.tokenVal = getControlText(activeDialogState.tokenEdit)
				activeDialogState.submitted = true
			}
			procDestroyWindow.Call(uintptr(hwnd))
			return 0
		} else if cmdID == idBtnCancel {
			procDestroyWindow.Call(uintptr(hwnd))
			return 0
		}
	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return ret
}

func getControlText(h windows.Handle) string {
	lenRes, _, _ := procGetWindowTextLength.Call(uintptr(h))
	length := int(lenRes)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return windows.UTF16ToString(buf)
}

// PromptCredentials displays a native Win32 modal dialog to configure server and token.
// Pops up instantly in the foreground with zero external process or script dependencies.
func PromptCredentials(defaultServer, defaultToken string) (server, token string, ok bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInstRes, _, _ := procGetModuleHandleW.Call(0)
	hInst := windows.Handle(hInstRes)

	className, _ := windows.UTF16PtrFromString("DRSConfigDialogClass")
	windowTitle, _ := windows.UTF16PtrFromString("DRS Agent — Server & Token")

	// Get system background brush (COLOR_BTNFACE + 1)
	colorBtnFace := windows.Handle(16) // COLOR_BTNFACE + 1 = 16

	wndProcCallback := syscall.NewCallback(dialogWndProc)

	var wc wndClassExW
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.lpfnWndProc = wndProcCallback
	wc.hInstance = hInst
	wc.hbrBackground = colorBtnFace
	wc.lpszClassName = className

	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	// Window dimensions
	const dlgWidth = 460
	const dlgHeight = 220

	screenW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	screenH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	posX := (int32(screenW) - dlgWidth) / 2
	posY := (int32(screenH) - dlgHeight) / 2
	if posX < 0 {
		posX = 100
	}
	if posY < 0 {
		posY = 100
	}

	hwndRes, _, _ := procCreateWindowExW.Call(
		wsExTopMost|wsExDlgModal,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowTitle)),
		wsPopup|wsCaption|wsSysMenu|wsVisible,
		uintptr(posX), uintptr(posY),
		uintptr(dlgWidth), uintptr(dlgHeight),
		0, 0, uintptr(hInst), 0,
	)
	hwnd := windows.Handle(hwndRes)
	if hwnd == 0 {
		return "", "", false
	}

	fontRes, _, _ := procGetStockObject.Call(defaultGUIFont)
	hFont := fontRes

	// Controls
	staticClass, _ := windows.UTF16PtrFromString("STATIC")
	editClass, _ := windows.UTF16PtrFromString("EDIT")
	buttonClass, _ := windows.UTF16PtrFromString("BUTTON")

	// Label 1
	lbl1Text, _ := windows.UTF16PtrFromString("Server URL or Invite Link:")
	lbl1Res, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(lbl1Text)),
		wsChild|wsVisible,
		24, 18, 400, 18,
		uintptr(hwnd), 0, uintptr(hInst), 0,
	)
	procSendMessageW.Call(lbl1Res, wmSetFont, hFont, 1)

	// Edit 1 (Server URL)
	val1Text, _ := windows.UTF16PtrFromString(defaultServer)
	edit1Res, _, _ := procCreateWindowExW.Call(
		wsExClientEdge, uintptr(unsafe.Pointer(editClass)), uintptr(unsafe.Pointer(val1Text)),
		wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll,
		24, 38, 400, 24,
		uintptr(hwnd), uintptr(idEditServer), uintptr(hInst), 0,
	)
	procSendMessageW.Call(edit1Res, wmSetFont, hFont, 1)

	// Label 2
	lbl2Text, _ := windows.UTF16PtrFromString("Enrollment Token (DRS-…):")
	lbl2Res, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(lbl2Text)),
		wsChild|wsVisible,
		24, 72, 400, 18,
		uintptr(hwnd), 0, uintptr(hInst), 0,
	)
	procSendMessageW.Call(lbl2Res, wmSetFont, hFont, 1)

	// Edit 2 (Token)
	val2Text, _ := windows.UTF16PtrFromString(defaultToken)
	edit2Res, _, _ := procCreateWindowExW.Call(
		wsExClientEdge, uintptr(unsafe.Pointer(editClass)), uintptr(unsafe.Pointer(val2Text)),
		wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll,
		24, 92, 400, 24,
		uintptr(hwnd), uintptr(idEditToken), uintptr(hInst), 0,
	)
	procSendMessageW.Call(edit2Res, wmSetFont, hFont, 1)

	// Connect Button (default push button)
	btn1Text, _ := windows.UTF16PtrFromString("Connect")
	btn1Res, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(buttonClass)), uintptr(unsafe.Pointer(btn1Text)),
		wsChild|wsVisible|wsTabStop|bsDefPushButton,
		220, 132, 95, 28,
		uintptr(hwnd), uintptr(idBtnConnect), uintptr(hInst), 0,
	)
	procSendMessageW.Call(btn1Res, wmSetFont, hFont, 1)

	// Cancel Button
	btn2Text, _ := windows.UTF16PtrFromString("Cancel")
	btn2Res, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(buttonClass)), uintptr(unsafe.Pointer(btn2Text)),
		wsChild|wsVisible|wsTabStop|bsPushButton,
		325, 132, 95, 28,
		uintptr(hwnd), uintptr(idBtnCancel), uintptr(hInst), 0,
	)
	procSendMessageW.Call(btn2Res, wmSetFont, hFont, 1)

	state := &dialogState{
		serverEdit: windows.Handle(edit1Res),
		tokenEdit:  windows.Handle(edit2Res),
	}
	activeDialogState = state

	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
	procBringWindowToTop.Call(uintptr(hwnd))
	procSetForegroundWindow.Call(uintptr(hwnd))
	if defaultServer == "" {
		procSetFocus.Call(edit1Res)
	} else {
		procSetFocus.Call(edit2Res)
	}

	// Modal message loop with IsDialogMessage handling for Tab/Enter/Esc
	var m msg
	for {
		res, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(res) <= 0 {
			break
		}
		dlgRes, _, _ := procIsDialogMessageW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&m)))
		if dlgRes == 0 {
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}

	activeDialogState = nil

	if !state.submitted {
		return "", "", false
	}

	rawServer := strings.TrimSpace(state.serverVal)
	rawToken := strings.TrimSpace(state.tokenVal)

	// Use SplitInvite to see if the user pasted an invite link with embedded token
	parsedServer, parsedToken, splitOK := config.SplitInvite(rawServer)
	if splitOK && parsedServer != "" {
		if parsedToken != "" {
			return parsedServer, parsedToken, true
		}
		return parsedServer, rawToken, true
	}

	return rawServer, rawToken, rawServer != ""
}
