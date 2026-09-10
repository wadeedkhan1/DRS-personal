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
| **Connection ownership** | Module-level singleton, started with the bundle | `src/runtime.ts`, `index.js` |
| Control channel | Browser `WebSocket` (RN polyfill) | `src/net/connection.ts` |
| Screen capture | Android `MediaProjection` via `getDisplayMedia()` | `src/webrtc/session.ts` |
| WebRTC + VP8 encode | `react-native-webrtc` ^124 (native, hardware-assisted) | `src/webrtc/session.ts` |
| Persisted identity + machine id | `@react-native-async-storage/async-storage` | `src/config/storage.ts` |
| Zero-touch enrollment | `drs://` deep link + RN `Linking` | `AndroidManifest.xml`, `src/runtime.ts` |
| Persisted connected/disconnected flag | `SharedPreferences` (native must read it with no JS) | `android/.../AgentPrefs.kt` |
| Telemetry, keep-alive, heartbeat ticks, notifications | Kotlin native module `DRSScreenCapture` | `android/.../ScreenCaptureModule.kt` |
| Process keep-alive | `dataSync` foreground service + partial wake lock | `android/.../ConnectionService.kt` |
| Notification actions → JS | `BroadcastReceiver` | `android/.../AgentCommandReceiver.kt` |

There is **no hand-rolled VP8 here** — unlike the Windows agent, `react-native-webrtc`
negotiates VP8 in SDP and encodes the capture track natively.

---

## How it works

### 1. Enrollment (once)

**By deep link (the normal route).** The invite page offers a **Set up the agent** button
firing `drs://enroll?server=…&token=…`; `MainActivity` has a matching `VIEW`/`BROWSABLE`
intent-filter, and `handleEnrollLink` in `src/runtime.ts` enrolls from it with nothing
typed.

This is the phone's equivalent of the config trailer appended to the Windows `.exe`, and it
exists because an APK **cannot** carry one: the system renames an installed package to
`base.apk` and an app cannot read its own installer. A private `drs://` scheme rather than
an https App Link, because App Links need a verified domain hosting `assetlinks.json` and
this deployment is routinely an IP address on a LAN.

Two details that are load-bearing:

- Both `Linking.getInitialURL()` (a link that launched a cold app) and the `url` listener
  (one arriving while it already runs — `launchMode="singleTask"` delivers that as a new
  intent) are wired, because either alone misses half the cases.
- **An already-enrolled device ignores the link.** Re-enrolling would rotate its secret and
  silently move it to whichever team the new link points at, so a link forwarded into a
  group chat must not be able to reassign phones that are already managed.

**By hand.** The user types the server URL and the enrollment token into the app and taps
**Enroll Device**.

Either way `src/net/enroll.ts` POSTs `/api/devices/enroll` with `type: "android"` and the
device model as the name; the returned `deviceId` / `agentSecret` / `wsUrl` are saved to
AsyncStorage.

The request also carries a `machine_id` — a random value stored under its own AsyncStorage
key, separate from the identity so it survives re-enrollment. Without it the server would
de-duplicate on the device *name*, and every "Samsung SM-G991B" enrolled from one invite
link reports the same one: the second phone would take over the first's device row and
invalidate its secret.

> Note the casing split: this REST body is **snake_case**; everything on the WebSocket is
> **camelCase**.

The Android enroll request does **not** yet send `allow_screen` / `allow_terminal`, so the
server applies its defaults: screen on, terminal off. There is no terminal runner on Android.

### 2. Staying connected
`src/net/connection.ts` mirrors `agents/windows/internal/conn`: dial → `hello` → `welcome`
(within a 10s deadline) → heartbeat. Reconnects with 1s → 30s backoff; `error{fatal:true}`
stops for good.

#### Who owns the connection

`src/runtime.ts`, at module scope — **not** a React component. `index.js` calls
`bootRuntime()` beside `AppRegistry.registerComponent`, and `App.tsx` only subscribes to
what it reports.

This is the arrangement's whole point, and it replaced one that quietly did the opposite.
The connection used to be created by a `useEffect` in `App.tsx`, so its cleanup ran when
that component unmounted — and swiping the app out of recents destroys the activity, which
makes React Native unmount the root component. The agent therefore closed its own socket
and stopped its own keep-alive service the moment the user dismissed the app. That read as
"Android killed it"; it was not. Dismissing an app is not switching it off, any more than
it is for a VPN client, and the two are now separate:

| Action | Effect |
|---|---|
| Close / swipe out of recents | Nothing. The service and socket stay up. |
| **Disconnect** in the notification | Socket closed, service stopped, flag cleared. |
| **Disconnect** in the app | The same, via the same `stopRuntime()`. |
| **Reset / Re-enroll** | Disconnects *and* forgets the identity. |

Because `bootRuntime()` runs whenever the JS bundle loads — including headlessly, when
`ConnectionService.onTaskRemoved` recreates the React context after the activity is gone —
a process reclaimed for memory reconnects on its own. It consults `AgentPrefs` first, so a
deliberate Disconnect is not helpfully undone by a restart. That flag lives in
`SharedPreferences` rather than AsyncStorage precisely because native code has to read it
at moments when there is no JS context to ask.

`ConnectionService` carries `android:stopWithTask="false"`; without it Android tears the
service down with the task and the rest of this is moot.

#### The notification

The ongoing notification is the disconnect control, the way a VPN's is. It carries a
**Disconnect** action and its text follows the connection — "Online — available for
monitoring", "Live — sharing screen", "Reconnecting…" — driven from JS through
`setAgentState`. During a session the subtext names the operator, so the phone's owner can
see who is watching without opening the app.

Disconnect goes through JS (`AgentCommandReceiver` → `DRSAgentCommand` event → `stopRuntime`)
rather than just killing the service, because closing the WebSocket properly is what makes
the portal show the device offline immediately; dropping the process instead leaves the
server waiting out three missed heartbeats. The receiver stops the service directly only as
a backstop, when there is no live JS context to receive the event at all.

Start and stop are **serialised** through a promise chain (`serialise()` in `runtime.ts`).
They both do several awaits before touching `conn`, and they arrive from callers that know
nothing about each other — the boot path, the in-app button, the notification. Interleaved,
a stop landing while a start was awaiting the foreground service would find `conn` still
null, stop nothing, and leave a live socket behind a UI reading "Disconnected", with no
service holding the process up and no way back short of a Reset.

Two Android-specific wrinkles in the heartbeat itself:

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

### 3. Approving capture when the app is not on screen

Android refuses to let a backgrounded process start the MediaProjection consent activity.
Calling `getDisplayMedia()` anyway produces no dialog and no frames, and the operator sees
an unexplained black screen — which was the reason the agent previously had to be kept in
the foreground.

So capture is acquired through `gatedAcquire` in `src/runtime.ts`:

```
start_session → isAppForeground()?
                  yes → getDisplayMedia() straight away
                  no  → post a heads-up notification
                        ("<operator> wants to view this device. Tap to open DRS and allow it.")
                        wait up to 60s for AppState to become 'active'
                        → getDisplayMedia() (the system consent dialog)
```

Two details are load-bearing, and both were got wrong first:

- **The notification grants nothing — it only opens the app.** An "Approve" action that
  resolved the request directly would make the notification the consent gate. That is
  unsafe: on API 24–28 the system may *launch* a full-screen intent with no user
  interaction at all while the device is locked, so an operator could have started capture
  on a locked phone without the owner touching it. Consent stays where it belongs, in
  Android's own MediaProjection dialog. For the same reason there is no full-screen intent
  (which since API 34 also needs `USE_FULL_SCREEN_INTENT`, granted only to calling and
  alarm apps); a heads-up notification, visible on the lock screen, is what it asks for.
- **The wait is on `AppState`, not on the tap.** `startActivity` returns long before the
  activity is resumed, so proceeding on the tap would call `getDisplayMedia` while still
  effectively backgrounded — precisely the failure being avoided. The `AppState`
  subscription is per request, so two overlapping requests cannot cancel each other's wait.

On timeout the promise rejects, `Connection.onStartSession` catches it and sends
`session_error`, and the browser shows a real reason rather than black video.

This does **not** make capture silent — it still needs a tap and then the system consent
dialog. What it removes is the requirement that someone already be looking at the app.

### 4. A screen session
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

**Requested quality.** `start_session` carries `fps` and `maxWidth`, and the wall asks for
far less than the default so a dozen tiles are affordable. Unlike the Windows agent, which
scales inside its own capture pipeline, MediaProjection here captures at the display's own
size whatever `getDisplayMedia` is asked for — so the only lever is the encoder's, applied
in `applyQuality()` as `maxFramerate` and `scaleResolutionDownBy` on the video sender.
The clamps (1–60 fps, 320–3840 px) match `agents/windows/internal/screen/webrtc.go`, so
both agents read the same `start_session` the same way. It is best-effort: an
implementation that ignores `setParameters` still streams, just at full rate.

Once ICE connects, `getStats()` is polled every 2s for `media-source` frames and
`outbound-rtp` counters. That line is surfaced in the app's debug panel, and it is the
distinction that matters when diagnosing "no video": `framesEncoded` stuck at 0 means the
capture surface is not feeding the encoder, while `bytesSent` climbing means media is going
out and the problem is on the receiving side.

### 5. Debug panel
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
  possible on stock Android without device-owner provisioning. The app no longer has to be
  foregrounded — a backgrounded request raises an approval notification first — but somebody
  still has to tap it, and then the system dialog.
- **No boot-autostart.** The agent survives the app being dismissed and reconnects after the
  process is reclaimed, but a reboot needs the app opened once. There is no
  `BOOT_COMPLETED` receiver.
- **No battery-optimisation exemption.** Nothing prompts the user to exempt the app from
  Doze, so aggressive OEM battery managers (Xiaomi, Samsung, Oppo) can still kill the
  foreground service after a long idle period. This is the most likely cause of an agent
  that stays connected on a Pixel and does not on a Redmi.
- View-only: no remote input, and **no remote terminal** (the Windows agent's
  `terminal_command` handler has no Android counterpart).
- No consent capability picker in the UI, so enrollment always lands on the server defaults.
