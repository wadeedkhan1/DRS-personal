# Android Agent

**Path:** `agents/android/` · **Stack:** React Native 0.74 + TypeScript, native Kotlin module · **minSdk:** 24

The mobile endpoint agent. It is the capture-only counterpart to the Windows agent: same wire
protocol, same session shape, but it streams the **phone's own screen** and has no
device-viewing UI of its own. Every module in `src/` is a deliberate port of a Windows agent
package, and the file headers say which one.

---

## Tech stack

| Concern | Technology | Where |
|---|---|---|
| App shell / UI | React Native 0.74, TypeScript | `App.tsx` |
| Control channel | Browser `WebSocket` (RN polyfill) | `src/net/connection.ts` |
| Screen capture | Android `MediaProjection` via `getDisplayMedia()` | `src/webrtc/session.ts` |
| WebRTC + VP8 encode | `react-native-webrtc` ^124 (native, hardware-assisted) | `src/webrtc/session.ts` |
| Persisted identity | `@react-native-async-storage/async-storage` | `src/config/storage.ts` |
| Telemetry, keep-alive, heartbeat ticks | Kotlin native module `DRSScreenCapture` | `android/.../ScreenCaptureModule.kt` |
| Process keep-alive | `dataSync` foreground service + partial wake lock | `android/.../ConnectionService.kt` |

There is **no hand-rolled VP8 here** — unlike the Windows agent, `react-native-webrtc`
negotiates VP8 in SDP and encodes the capture track natively.

---

## How it works

### 1. Enrollment (once)
The user types the server URL and the enrollment token into the app and taps **Enroll
Device**. `src/net/enroll.ts` POSTs `/api/devices/enroll` with `type: "android"` and the
device model as the name; the returned `deviceId` / `agentSecret` / `wsUrl` are saved to
AsyncStorage.

> Note the casing split: this REST body is **snake_case**; everything on the WebSocket is
> **camelCase**.

The Android enroll request does **not** yet send `allow_screen` / `allow_terminal`, so the
server applies its defaults: screen on, terminal off. There is no terminal runner on Android.

### 2. Staying connected
`src/net/connection.ts` mirrors `agents/windows/internal/conn`: dial → `hello` → `welcome`
(within a 10s deadline) → heartbeat. Reconnects with 1s → 30s backoff; `error{fatal:true}`
stops for good.

Two Android-specific wrinkles:

- **Heartbeats come from native code.** RN's `setInterval` is driven by the display frame
  callback, which Android pauses when the app is backgrounded or the screen is off — so a JS
  timer stops beating and the server drops the agent after ~3× the interval. `startHeartbeat`
  runs a Kotlin `Handler` on its own thread and emits a `DRSHeartbeatTick` event to JS, which
  sends the beat. JS `setInterval` remains as a fallback.
- The agent beats at **half** the server's interval for margin against a missed tick.
- CPU % and RAM % are reported as `0` (Android does not expose them the way gopsutil does);
  battery, model and OS version come from the native module and show in the dashboard card.

`ConnectionService` is a lightweight `dataSync` foreground service holding a
`PARTIAL_WAKE_LOCK` so the process — and the socket — survive backgrounding and screen-off.
It is deliberately **not** the capture service; `react-native-webrtc` runs its own
`mediaProjection` foreground service on demand.

### 3. A screen session
Identical negotiation to Windows — **the phone is the offerer**:

```
start_session → getDisplayMedia() (Android consent dialog) → session_ready → offer
              → (browser) answer → ICE trickles both ways → media
```

The stream is acquired **first**, so consent and capture are proven to work before a peer
connection exists. Candidates that overtake the answer are buffered and flushed after
`setRemoteDescription`.

**Android 14+ (API 34):** the consent dialog defaults to *single app* capture, which produces
zero frames once the app is backgrounded. `createConfigForDefaultDisplay: true` forces
full-screen capture of the default display. Gated on `Platform.Version >= 34` since the
option does not exist below that.

Once ICE connects, `getStats()` is polled every 2s for `media-source` frames and
`outbound-rtp` counters. That line is surfaced in the app's debug panel, and it is the
distinction that matters when diagnosing "no video": `framesEncoded` stuck at 0 means the
capture surface is not feeding the encoder, while `bytesSent` climbing means media is going
out and the problem is on the receiving side.

### 4. Debug panel
The app screen carries a live log view (`src/log.ts`) with **Share** and **Clear**, so a
field failure can be exported off the phone without adb.

---

## Build & run

Requires Android SDK, JDK 17, Node ≥ 18.

```bash
cd agents/android
npm install
npm run android          # builds + installs the debug APK, starts Metro
```

- **Emulator against a local server:** `adb reverse tcp:8080 tcp:8080`, then use
  `http://localhost:8080`.
- **Physical phone on the LAN:** the PC's LAN IP, e.g. `http://192.168.1.50:8080`. Cleartext
  `http`/`ws` is enabled for testing (`usesCleartextTraffic="true"`); production must use
  `https`/`wss`.

---

## Current limits

- **The phone user must approve the capture dialog.** Silent fully-remote capture is not
  possible on stock Android without device-owner provisioning. Keep the app foregrounded when
  an operator starts a session.
- No boot-autostart — the app must be launched manually.
- View-only: no remote input, and **no remote terminal** (the Windows agent's
  `terminal_command` handler has no Android counterpart).
- No consent capability picker in the UI, so enrollment always lands on the server defaults.
