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
| System tray | `fyne.io/systray` (Win32 `Shell_NotifyIcon`) | `internal/tray` |
| Telemetry | `shirou/gopsutil/v4` (CPU, RAM, OS) | `internal/sysinfo` |
| Autostart / single-instance | `golang.org/x/sys/windows` (HKCU Run key, named mutex) | `cmd/agent/platform_windows.go` |
| Command runner | `os/exec` → `powershell.exe` / `cmd.exe` | `internal/terminal` |

**libvpx is mandatory.** `CGO_ENABLED=0` still compiles but produces a binary that cannot
encode video. `build.ps1` verifies after linking that `vpx_codec` symbols are present, that
the PE subsystem is GUI (no console window), and that libvpx was linked statically.

---

## How it works

### 1. Enrollment (once)

There are three routes in, and all of them end in the same `internal/enroll` call, so they
cannot behave differently:

**Zero-touch (the normal one).** A binary downloaded from an invite link carries its own
configuration, appended by the backend as it streamed the file:

```
<base drs-agent.exe bytes>
<config JSON>              N bytes   {"serverUrl":"…","token":"DRS-…","autostart":true}
<uint32 little-endian N>   4 bytes
"DRSCFG\x00\x01"           8 bytes
```

`internal/config/embedded.go` reads it back from `os.Executable()` — the file, not the
loaded image, because the trailer sits past the last PE section and is never mapped.
Reading our own executable while it runs is fine: the loader opens the image with
`FILE_SHARE_READ`. The magic is *last* so a reader seeks to a fixed offset from the end and
gives up after 12 bytes on a binary that has no trailer, which is the common case for a
developer build and must never be misread as configured.

On launch, the agent starts silently with zero popups or notifications and docks directly into the system tray. If the device is already enrolled, it connects immediately. If not enrolled, it reads the embedded configuration and self-enrolls with up to 3 automatic retries. If configured for autostart, it registers the login entry.

**Headless CLI.** `drs-agent enroll -server … -token … [-name …]`.

**Manual Tray.** Right-clicking the tray icon and choosing `Change Server / Token…` allows entering or updating the server URL and token.

All three POST `/api/devices/enroll` and get back `deviceId`, `agentSecret`, `wsUrl`, and
the heartbeat interval. That identity is written **0600** to `%AppData%\drs\agent.json`
(`internal/config`). Logs go next to it, `agent.log`.

**Machine identity.** The request also carries a `machine_id`: a random UUID generated once
and kept in `%AppData%\drs\machine-id`, *separate* from the identity file because it must
survive re-enrollment — it is what says "this is the same physical machine", which a new
identity explicitly is not. The server de-duplicates on it instead of the hostname, because
one invite link rolled across a fleet hits cloned VMs and imaged PCs that share a name, and
matching on the name meant the second machine took over the first's row and invalidated its
secret. A random value rather than a hardware serial or `MachineGuid`: those need WMI or the
registry and a clone copies them too, whereas a value generated *after* the clone is made is
exactly the property wanted.

The enroll request always sends `allow_screen: true` and `allow_terminal: true` — there is no
per-machine capability picker in the GUI and no `-screen` / `-terminal` flags on the CLI, so
every device enrolled by this build lands fully capable and the two paths cannot disagree. The
enroll screen states what that allows instead of asking. The flags are still sent rather than
left to the server's defaults, because the **server** is the only place they are enforced —
it stores them per device and checks them on every session. Changing them for a device that is
already enrolled currently means re-enrolling it (or editing the row); there is no portal
control for it yet.


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

Those two numbers used to be the same for every session (`SESSION_FPS` / `SESSION_MAX_WIDTH`).
They are now **per session**: a viewer may ask for less on `/ws/session`, which is how the
portal's monitoring wall runs its tiles at 4fps/480px. Nothing changed on this side — the
server clamps to the same bounds before sending, and the clamp here is unchanged and still
the last word.

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

### 5. Lifecycle and System Tray
`internal/tray` owns the main goroutine (Windows requires the tray message pump there).
There is no permanent GUI window: double-clicking the executable runs it silently into
the system tray with no popups or notifications.

The tray icon displays the current state:
- ⚪ Grey: Reconnecting / offline / unenrolled.
- 🟢 Green: Connected and available.
- 🔴 Red: Screen is being viewed by an operator.

Right-clicking the tray icon provides:
- Live connection status display
- Connected server URL display
- `Reconnect`: immediately interrupts backoff and dials the server
- `Change Server / Token…`: prompts to enter an invite link or server URL and token
- `Quit DRS Agent`: cleanly shuts down the connection and exits

`drs-agent install` writes an HKCU `…\CurrentVersion\Run` entry. **HKCU login entry, not a Windows service** — a service runs in session 0,
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
- The executable is trimmed down to ~15 MB by eliminating Fyne and all OpenGL/graphics dependencies.
