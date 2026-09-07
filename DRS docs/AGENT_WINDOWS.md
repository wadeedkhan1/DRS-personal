# Windows Agent

**Path:** `agents/windows/` · **Language:** Go 1.26 (CGO required) · **Binary:** `drs-agent.exe`

The endpoint agent for Windows desktops. It enrolls once, then keeps a single outbound
WebSocket open to the backend and waits to be told what to do. When an operator opens the
device in the portal it captures the primary display, encodes VP8, and streams it
**peer-to-peer to the browser** — the video never passes through the server. It can also run
one-shot shell commands on request.

---

## Tech stack

| Concern | Technology | Where |
|---|---|---|
| Control channel | `github.com/coder/websocket` | `internal/conn` |
| Screen capture | `github.com/kbinani/screenshot` (GDI `BitBlt`) | `internal/screen/capture.go` |
| Downscale + colour convert | Hand-written parallel RGBA → I420 box filter | `internal/screen/convert.go` |
| Video encode | `pion/mediadevices` VP8 (libvpx via CGO) | `internal/screen/webrtc.go` |
| WebRTC transport | `pion/webrtc/v4` (DTLS-SRTP, trickle ICE) | `internal/screen/webrtc.go` |
| Desktop GUI + tray | `fyne.io/fyne/v2` + `fyne.io/systray` | `internal/gui` |
| Telemetry | `shirou/gopsutil/v4` (CPU, RAM, OS) | `internal/sysinfo` |
| Autostart / single-instance | `golang.org/x/sys/windows` (HKCU Run key, named mutex) | `cmd/agent/platform_windows.go` |
| Command runner | `os/exec` → `powershell.exe` / `cmd.exe` | `internal/terminal` |

**libvpx is mandatory.** `CGO_ENABLED=0` still compiles but produces a binary that cannot
encode video. `build.ps1` verifies after linking that `vpx_codec` symbols are present, that
the PE subsystem is GUI (no console window), and that libvpx was linked statically.

---

## How it works

### 1. Enrollment (once)
The user pastes the invite link (`https://server/enroll?token=DRS-…`) into the GUI and clicks
Connect. `internal/enroll` POSTs `/api/devices/enroll` and gets back `deviceId`,
`agentSecret`, `wsUrl`, and the heartbeat interval. That identity is written **0600** to
`%AppData%\drs\agent.json` (`internal/config`). Logs go next to it, `agent.log`.

The enroll request always sends `allow_screen: true` and `allow_terminal: true` — there is no
per-machine capability picker in the GUI and no `-screen` / `-terminal` flags on the CLI, so
every device enrolled by this build lands fully capable and the two paths cannot disagree. The
enroll screen states what that allows instead of asking. The flags are still sent rather than
left to the server's defaults, because the **server** is the only place they are enforced —
it stores them per device and checks them on every session. Changing them for a device that is
already enrolled currently means re-enrolling it (or editing the row); there is no portal
control for it yet.

The same thing is available headless: `drs-agent enroll -server … -token … [-name …]`.

### 2. Staying connected
`internal/conn` dials `wsUrl`, sends `hello` (device id + secret), and expects `welcome`.
Then it heartbeats every N seconds (interval is **server-chosen**, so the server's read
deadline and the beat rate can never disagree) carrying CPU %, RAM %, hostname and OS.

- Reconnects with 1s → 30s exponential backoff, so sleep/wake, network drops and server
  restarts all recover unattended.
- An `error{fatal:true}` (revoked secret, deleted device, protocol mismatch) stops the loop
  **for good** instead of hammering the server.
- Every writer to the socket goes through one mutex — the heartbeat loop, the WebRTC session
  and the command runner all share it.

### 3. A screen session
The backend pushes `start_session` (with the ICE server list and the requested fps/width).
**The agent is the WebRTC offerer** — it owns the media, so it offers and the browser answers.
This keeps the trust direction one-way: the browser cannot initiate anything.

```
start_session → session_ready → offer → (browser) answer → ICE trickles both ways → media
```

`internal/screen` clamps the requested fps (1–60, default 24) and width (320–3840, default
1280) so a bad command cannot ask for a firehose, and picks a bitrate from
`w*h*fps/6` clamped to 1.2–8 Mbps.

**The capture pipeline** (`internal/screen/pipeline.go`) is the performance-critical part:

- Capture and RGBA→I420 conversion run on their own goroutine, *ahead* of the encoder, so
  conversion of the next frame overlaps encoding of the current one.
- Three rotating frame buffers. The capturer always **overwrites** the pending frame rather
  than queueing — a queued frame is pure latency, so stale frames are dropped.
- Downscale and colour conversion are fused into one parallel pass writing target-resolution
  Y/Cb/Cr directly. Because the result is already 4:2:0 `image.YCbCr`, mediadevices' `ToI420`
  degrades to a struct copy and the conversion stage effectively disappears.
- A **capture/encode gate** (single-token channel) serialises `BitBlt` against the libvpx
  encode call. This is not a data lock: a GDI blit overlapping a libvpx encode on another
  thread wedges inside win32k and never returns, killing the stream. Reproducible within
  3–4 frames without the gate.

Encoder settings are deliberate: CBR (not VBR — bursts read as latency spikes), zero
lookahead, `RateControlMaxQuantizer=52` so text stays legible instead of smearing, and a
keyframe every 2 seconds so a late joiner has something to resynchronise on.

At most one session runs at a time. A new `start_session` replaces the old one; the socket
dropping tears everything down, so no capture goroutine outlives the connection that
authorised it.

### 4. Remote terminal
`terminal_command` → run → `terminal_result` back up the same socket. Phase 1 is a stateless
**command runner**, not a PTY: each command is one process whose stdout, stderr and exit code
are captured into a single frame.

- PowerShell by default (`-NoProfile -NonInteractive`), `cmd.exe` on request.
- 60s timeout; stdout and stderr each capped at 256 KiB with a truncation marker.
- `CREATE_NO_WINDOW` + `HideWindow` so no console flashes on the monitored user's screen.
- Runs at the agent's own privilege — the logged-in user, **non-elevated**. It cannot make
  machine-wide changes needing Administrator.
- Each command gets its own goroutine so a slow one never stalls the socket reader.

### 5. GUI and lifecycle
`internal/gui` owns the main goroutine (Windows requires the tray message pump there). It
shows the enrollment form when unenrolled, otherwise a live status view. Closing the window
hides to tray; quitting is deliberate via the tray menu. Tray icon: ⚪ reconnecting,
🟢 connected, 🔴 screen being viewed.

`drs-agent install` writes an HKCU `…\CurrentVersion\Run` entry with `-startup` (hidden to
tray at login). **HKCU login entry, not a Windows service** — a service runs in session 0,
which has no visible desktop, so capture there returns nothing. A `Local\`-scoped named mutex
prevents a second instance: two copies would authenticate as the same device and evict each
other in an endless reconnect loop.

---

## Build & run

Requires MSYS2 with `mingw-w64-x86_64-gcc`, `mingw-w64-x86_64-libvpx`, `mingw-w64-x86_64-pkgconf`.

```powershell
powershell -File agents\windows\build.ps1       # → agents\windows\build\drs-agent.exe
```

Then double-click the exe and paste the invite link, or:

```
drs-agent.exe enroll -server http://localhost:8080 -token DRS-XXXXXX
drs-agent.exe            # connect and stay connected
drs-agent.exe install    # start at login
```

---

## Current limits

- Primary display only (`CaptureDisplay(0)`); no monitor picker.
- View-only video — no remote mouse/keyboard input. The terminal is the only
  operator→device input path.
- GDI capture, not the Desktop Duplication API. DXGI is faster but needs per-adapter
  recovery across resolution changes, UAC's secure desktop and session switches. Revisit
  only if capture becomes the measured bottleneck — currently conversion and encoding are,
  and both already run in parallel with capture.
- No JPEG-over-WebSocket fallback **by design**: relaying video through the server is what
  this architecture exists to avoid. A failed session is reported, not downgraded.
- `internal/tray/` is dead code superseded by the Fyne GUI; nothing imports it.
