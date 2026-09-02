# DRS Android Agent (React Native)

A capture-only Android endpoint agent for DRS. It enrolls with the server, holds an
outbound WebSocket control channel, and — when an operator starts a session from the portal —
streams its screen to the browser viewer over WebRTC (VP8). It is the mobile counterpart to
`agents/windows`.

There is **no dashboard / device-viewing UI** on the phone; it only captures its own screen.

## How it works

Same wire contract as the Windows agent (`backend/pkg/protocol/protocol.go`):

1. **Enroll** (once): `POST /api/devices/enroll` with the one-time token → server returns
   `deviceId` / `agentSecret` / `wsUrl`, persisted in AsyncStorage.
2. **Connect**: dial `wsUrl` → `hello` → `welcome` → heartbeat every N seconds. Reconnects
   with 1s→30s backoff; a `fatal` error (revoked device) stops for good.
3. **Capture** (operator-initiated): server sends `start_session` → the phone runs
   `getDisplayMedia()` (Android shows the capture-consent dialog) → the agent is the WebRTC
   **offerer**: `session_ready` → `offer` → applies the browser `answer` → trickles ICE. On
   `stop_session` (or viewer disconnect) it tears the session down.

A lightweight foreground service (`ConnectionService`) keeps the process — and the control
socket — alive while the app is backgrounded or the screen is off. Screen-capture's own
`mediaProjection` foreground service is provided by `react-native-webrtc`.

### Source layout

| Path | Role | Ported from |
|---|---|---|
| `src/protocol.ts` | Envelope + message types | `backend/pkg/protocol/protocol.go` |
| `src/config/storage.ts` | Persisted identity (AsyncStorage) | `agents/windows/internal/config` |
| `src/net/enroll.ts` | One-time enrollment | `agents/windows/internal/enroll` |
| `src/net/connection.ts` | Socket state machine, heartbeat, reconnect | `agents/windows/internal/conn` |
| `src/webrtc/session.ts` | WebRTC offerer (getDisplayMedia → PC → offer) | `agents/windows/internal/screen/webrtc.go` |
| `src/telemetry.ts` | Heartbeat sysInfo + keep-alive service control | `agents/windows/internal/sysinfo` |
| `android/.../ScreenCaptureModule.kt` | Native: battery/OS/model + FGS control | — |
| `android/.../ConnectionService.kt` | Keep-alive foreground service | — |

## Build & run

Requires Android SDK, JDK 17, and Node ≥ 18. `minSdkVersion` is 24 (react-native-webrtc).

```bash
cd agents/android
npm install
# device or emulator connected (adb devices)
npm run android          # builds + installs the debug APK, starts Metro
```

- **Emulator / localhost server:** `adb reverse tcp:8080 tcp:8080`, then use
  `http://localhost:8080` as the server URL.
- **Physical phone on the LAN:** use the PC's LAN IP, e.g. `http://192.168.1.50:8080`.
  Cleartext `http`/`ws` is enabled for local testing (`usesCleartextTraffic="true"`);
  production should use `https`/`wss`.

## Test end-to-end

1. Start the backend + portal (`start_local.bat`). Log in and generate an enrollment token
   (device type **android**).
2. Launch the app, enter the server URL + token, tap **Enroll Device**.
3. The phone should appear **online** in the dashboard with a phone icon and its Android
   version. (Enroll + `hello`/`welcome` + heartbeat working.)
4. Open the device's live viewer in the portal → the phone shows Android's capture-consent
   dialog → **Start now**. Live video should render in the browser.
5. Background the app → it stays online (foreground-service notification). Stop the viewer →
   the phone returns to online. Restart the backend → the app reconnects with backoff.

## Notes / limits

- Because of Android's security model the phone user must approve the capture dialog; keep
  the app foregrounded when an operator starts a session. Silent, fully-remote capture is not
  possible on stock Android without device-owner provisioning.
- No boot-autostart in this phase (the app must be launched manually).
- View-only: no remote input/control (matches the current protocol).
