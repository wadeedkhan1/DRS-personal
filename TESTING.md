# How to test DRS Phase 1

Everything below has been built and verified to compile; none of it has been run
end-to-end against a live Postgres, so this is the path to prove it actually works.

## 0. One-time setup

```bat
REM Postgres (skip if you already have one on 5432)
docker run -d --name drs-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16-alpine

REM Backend config (the server now refuses to start without secrets)
copy backend\.env.example backend\.env
```

**If you have an existing `drs_db` from before this rework, drop it.** Migration 2
renames `device_secret_hash` to `agent_secret_hash`, and agent secrets moved from bcrypt
to SHA-256, so previously enrolled devices can never authenticate again. Either
`docker rm -f drs-pg` and start fresh, or delete the rows in `devices`.

## 1. Backend + portal

```bat
start_local.bat
```

Expected in the backend window:

- `[DB] Applied migration 000001_init_schema` then `000002_webrtc_protocol`
- `[DB] Seeded initial Super Admin account: admin@drs.local`
- `NAT traversal: STUN only, no TURN relay configured`
- `Signaling: /ws/agent (devices), /ws/session (viewers), /ws/presence (dashboard)`

Log in at http://localhost:3000 with the `DEFAULT_SUPERADMIN_*` values from
`backend\.env`. The header should show **Live updates: Connected** — that is the
`/ws/presence` socket, and it is the first thing to check if presence looks stale.

## 2. Build the agent

```powershell
powershell -File agents\windows\build.ps1
```

Must print all four checks. `libvpx symbols present` is the one that matters: without it
the binary cannot encode video, and the script deletes it rather than let you ship it.

## 3. Enroll and connect

In the portal: **Enroll Device** → Windows → copy the command. Then:

```powershell
agents\windows\build\drs-agent.exe enroll -server http://localhost:8080 -token DRS-XXXXXX
agents\windows\build\drs-agent.exe
```

A tray icon appears. Grey = reconnecting, **blue = connected**, red = being viewed.
Agent logs go to `%AppData%\drs\agent.log` (there is no console — it is a GUI binary).

The device should turn **online** in the dashboard within a second, without a refresh.

## 4. The main test: live video

Dashboard → **Live View** on the online device.

Pass criteria:

- Video appears within a few seconds.
- The badge reads `VP8 · Direct (P2P)`.
- fps, kbps, ms and resolution are all non-zero and *moving*. Every one of those comes
  from `RTCPeerConnection.getStats()`; if they are all zero the media path never opened.
- The tray icon on the agent turns **red** for the whole session.
- `chrome://webrtc-internals` shows one inbound-rtp video stream with `framesDecoded`
  climbing and a `succeeded` candidate pair.

If it stays on "Negotiating peer connection…", the signaling worked but ICE did not.
Check `webrtc-internals` for the candidate pair state — on one machine it should pick a
`host` candidate immediately.

## 5. The regressions worth re-checking

These are the specific bugs this rework fixed, and each is easy to verify:

| What to do | What should happen |
|---|---|
| Kill the browser tab mid-session, then start a new session on the same device | Works immediately. Previously the device was locked in `in_session` forever. |
| Click Live View, go to another tab, come back | One session, no "device is already being viewed" error. |
| Run a second browser (or incognito) and try to view the same device | Told the device is busy; the **first** session keeps streaming undisturbed. |
| Stop the agent with the tray menu, restart it | Device goes offline then online in the dashboard on its own. |
| Kill the backend while the agent runs, then restart it | Agent reconnects on its own with backoff (watch `agent.log`). |
| Pull the network cable / disable Wi-Fi on the agent machine | Device goes offline within ~30s (3 missed heartbeats), not "never". |
| Log in as an Admin with no devices assigned, open Live View | Sees nothing. Hitting `/ws/session?deviceId=<other device>` directly returns 404. |
| `SELECT * FROM sessions` after a session ends | `ended_at` set, `status = 'completed'`. |
| `UPDATE audit_logs SET action='x';` in psql | Rejected: `audit_logs is append-only`. |

## 5b. Zero-touch enrollment

The flow this exists for: **one link, ten machines, nothing typed.**

First stage a binary where the backend can read it. In `backend\.env`:

```
AGENT_BINARY_DIR=../deploy/downloads
```

then copy `agents\windows\build\drs-agent.exe` into `deploy\downloads\` and restart the
backend. Without this the enroll page says no agent is published — which is the correct
behaviour, not a failure.

1. Log in as an **Admin** (not the super admin) who is on a team. **Invite devices** →
   the "Assign to Admin" picker should be **absent** (their link is pinned to them
   server-side), and the team list should show only their teams. Name the link and create it.
2. Open the invite link **on a second machine**. Download the Windows agent. The file must
   be named `drs-agent.exe` and be a few hundred bytes larger than the one in
   `deploy\downloads\` — that difference is the config trailer.
3. **Double-click it. Type nothing.** Within a few seconds it should appear in the portal,
   in that admin's team, under its own computer name. Check:
   - `%AppData%\drs\agent.log` — "self-enrolling against …" then "self-enrolled as device …"
   - `reg query HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v DRSAgent`
4. **Run it again.** It must *not* re-enroll: no new log line, no new device, and the
   existing device stays online. Re-enrolling would rotate the secret on every launch.
5. **Rename it to `foo.exe`** and run it on a clean machine — still self-enrolls. This is
   what the trailer buys over encoding the config in the filename.
6. Use the **same link** on a third machine. Two devices, one team, separate secrets.

**Machine-id de-duplication** (the fleet-rollout failure this prevents):

| What to do | What should happen |
|---|---|
| Enroll two machines with the **same hostname** (rename one, or clone a VM) | **Two** devices, both online. Before this they collided: the second took the first's row and the first was authenticated out of its own identity, reconnecting forever with a dead secret. |
| Upgrade an agent that enrolled **before** this change, and let it re-enroll | It adopts its existing device row and fills in `machine_id` — it must **not** create a duplicate and leave the original offline. |
| Delete `%AppData%\drs\machine-id` and re-enroll | A new device appears. Expected: wiping it makes the machine look new, the same way losing the identity file does. |

**Revocation** — the counterweight to a self-enrolling installer:

1. **Invite Links** in the sidebar. The link should be listed with its team and a device count.
2. **Revoke** it.
3. Try the download link again → **404**, not a binary.
4. Run an installer downloaded from it *before* revoking → enrollment refused.
5. The devices it already enrolled must **keep streaming**. Revocation stops new
   enrollments; it does not un-enroll anything.

**Access control worth checking deliberately:**

| What to do | What should happen |
|---|---|
| As an Admin, `POST /api/devices/enrollment-token` naming a team they are **not** on | 403. Without this check, opening the route to Admins would be an escalation. |
| As Admin A, open **Invite Links** | Only A's links. Admin B's are invisible, and `DELETE`ing one of B's by id returns 404. |
| `GET /api/enroll/agent?token=DRS-BOGUS` | 404, not a generic binary. |
| Unset `AGENT_BINARY_DIR` and reload `/enroll?token=…` | Says no agent is published, rather than offering a download that 404s. |

**Android:** open the invite link **on the phone**, install the APK, then tap
**Set up the agent**. It should enroll with nothing typed and land in the same team.
Tapping it again on an already-enrolled phone must do nothing — the log says
"ignoring an enroll link: this device is already enrolled".

Both new devices should then show up together on the wall at `/teams/{id}/monitor`.

## 6. The monitoring wall

Needs **at least two** enrolled devices to be worth anything; the interesting behaviour is
what happens between tiles. Two Windows agents on one machine work fine — enroll them under
different names, since devices de-dup on `(org, name, type)`.

1. Put both devices in a team (**Teams → a team → Add device**), then open
   **Teams → Monitor all**, or the sidebar's **Monitoring Wall** for everything you can see.
2. Both tiles should go live. Check the agent log (`%AppData%\drs\agent.log`) for
   `fps=4` / `maxWidth=480` in the `start_session` — that is the per-session quality
   request, and it is the whole reason a dozen tiles is affordable.
3. **Click a tile.** It should enlarge, and its `start_session` in the log should now read
   `fps=24 maxWidth=1280`. Expect a ~1s black frame: enlarging re-opens the session rather
   than renegotiating it. The other tile must keep streaming throughout.
4. **Click Close (or press Esc).** It drops back into the grid at tile quality.
5. The enlarge/collapse cycle must **never** show "already being viewed by someone else".
   If it does, the per-device handoff gate in `useDeviceSession` is not doing its job — the
   old session's socket had not finished closing before the new one claimed the device.
6. Leave the wall open and, in a second browser, open `/devices/{id}/live` for one of those
   devices. It must be refused as busy, and the wall must be undisturbed. One viewer per
   device still holds; the wall just holds several devices at once.
7. Close the wall tab. Both devices should return to `online` within a second.

**Access control.** Log in as an Admin who is *not* on that team:

| What to do | What should happen |
|---|---|
| Open `/monitor` | None of that team's devices appear |
| Paste `/teams/{id}/monitor` directly | "Team not found" card — the same answer whether it does not exist or they are simply not on it |
| Add them to the team, reload | The devices appear and the wall works |

Server-side, nothing new is being trusted: every tile is an ordinary `/ws/session` that
re-checks `canViewDevice` against live membership. The quick proof is to connect to
`/ws/session?deviceId=…&fps=999&maxWidth=1` by hand — the agent should be asked for
`fps=60 maxWidth=320`, i.e. clamped, not obeyed.

## 7. The Android agent in the background

This is the one that needs a real phone; an emulator will not tell you much about how the
OS treats a dismissed app.

```bash
cd agents/android && npm install && npm run android
```

Enroll it, confirm **Online** in the portal, then:

| What to do | What should happen |
|---|---|
| **Swipe the app out of recents** | Notification stays. Portal keeps showing the device **online for minutes**. This is the regression that defines the feature — before, it disconnected instantly. |
| Reopen the app | Shows the still-running connection, not a fresh connect. The debug log has no new "connecting" line. |
| Tap **Disconnect** in the notification | Notification clears, portal shows offline **within a second** (not after 30s of missed heartbeats). |
| Reopen the app after disconnecting | Shows "Disconnected" with a **Connect** button, and the enrolled identity intact — Disconnect is not Reset. |
| Tap Connect | Back online. |
| Force-stop the app from Settings, then reopen it | Reconnects on its own, because the enabled flag persisted. |
| Disconnect, then force-stop and reopen | Stays disconnected. A deliberate Disconnect must not be undone by a restart. |
| Start a session from the portal while the app is on screen | Consent dialog appears as before; video flows. |
| **Start a session with the app swiped away** | A heads-up notification: "<operator> wants to view this device." Tapping it opens the app, *then* the consent dialog appears. Approving delivers video. |
| Ignore that notification for 60s | The browser shows a real error ("not approved on the device in time"), not a black screen. |
| While a session is live, pull down the shade | The ongoing notification reads "Live — sharing screen" and names the operator. |

Two things that are **not** implemented, so do not test for them: the agent does not start
after a reboot (open the app once), and nothing asks the phone to exempt DRS from battery
optimisation — on Xiaomi/Samsung/Oppo the OS may still kill it after a long idle spell.
Both are listed in `DRS docs/AGENT_ANDROID.md`.

## 8. Deployment

```bash
cd deploy
cp .env.example .env      # fill in every required value
./scripts/init_certs.sh your.domain you@example.com
docker compose up -d
```

`init_certs.sh` writes a self-signed placeholder first so nginx can start, then swaps in
a real certificate. Without the placeholder nginx will not load the 443 block at all.

### Making media go through the server

By default a session connects peers directly and uses the relay only when it must. For
the deployment the SRS describes — devices on networks you do not control, reachable
from anywhere — you want the relay always on:

```bash
# in deploy/.env
TURN_PUBLIC_IP=<the VPS public IP, as an IP and not a hostname>
TURN_SHARED_SECRET=<openssl rand -hex 32>
FORCE_TURN_RELAY=true

docker compose --profile turn up -d
```

Open on the VPS firewall: **80, 443 TCP; 3478 UDP+TCP; 49160-49200 UDP.**

What that buys, and what it costs:

- **No device needs a public address or an inbound port.** The agent and the browser each
  make an *outbound* allocation to the relay, so a device behind a home router or a
  corporate firewall is reachable without touching either. This is the property that
  makes the system work globally, and it is why forced relay is the right default here
  rather than an optimisation.
- **Connectivity stops depending on NAT type.** `FORCE_TURN_RELAY=true` makes both peers
  discard host and server-reflexive candidates, so there is one path and it either works
  or fails immediately — instead of working on some networks and mysteriously not others.
- **Media is relayed, not decoded.** DTLS-SRTP is negotiated end to end between agent and
  browser; the relay forwards packets it cannot read. It carries the stream without
  becoming a party to it. (An SFU would be different, and would be the thing to build if
  multi-viewer or server-side recording is ever wanted.)
- **Bandwidth is the real cost:** roughly **7-8 Mbps of VPS traffic per concurrent
  session** (inbound from the agent plus outbound to the viewer) at the default encode
  settings. Ten concurrent sessions is ~75 Mbps sustained. Check the host's allowance
  before scaling, and lower `SESSION_MAX_WIDTH` or `SESSION_FPS` if it is tight.

The relay is this project's own binary, `backend/cmd/turnserver`, built into the same
image as the API server. It is a separate process because it needs the host's network
directly — one port per allocation — but sharing the image means both sides derive TURN
credentials from the same function, `ice.RESTCredential`. That matters more than it
sounds: a credential-format mismatch between an external relay and the ICE list is
invisible from both ends, showing up only as "ICE failed" in the browser and
"unauthorised" in the relay log.

`backend/cmd/turnserver` refuses to start without `TURN_SHARED_SECRET` (it would be an
open relay) or without `TURN_PUBLIC_IP` (it would hand out unreachable candidates).

### Testing the relay locally

Worth doing before deploying, because it is the path every deployed session will take
and a local session otherwise connects over loopback and never exercises it.

In `backend\.env`:

```
TURN_PUBLIC_IP=127.0.0.1
TURN_SHARED_SECRET=any-32-plus-random-characters
FORCE_TURN_RELAY=true
```

Then, in a third window alongside the backend and portal:

```bat
start_relay.bat
```

Start a session and check:

- The backend logs `NAT traversal: ALL media forced through the TURN relay at ...`.
- The relay window logs an allocation for each peer — **two per session**, one for the
  agent and one for the browser.
- The viewer badge reads **`VP8 · Relayed (TURN)`**, not `Direct (P2P)`. That badge is
  read from `getStats()`, so it reports what the connection actually negotiated.
- Video still plays, with slightly higher latency than direct. On one machine the extra
  hop is trivial; across the internet expect the relay's own RTT to be added.

If the relay logs nothing at all, the peers never tried it: check that
`GET /api/session/ice` returns a `turn:` entry with a username, which requires both
`TURN_PUBLIC_IP` and `TURN_SHARED_SECRET` to be set on the **backend**.

There is also a test that covers the credential contract directly:

```bash
cd backend && go test ./cmd/turnserver/
```

It mints a credential exactly the way the backend hands it to a peer, opens a real
allocation against a real relay with it, and confirms an expired credential is refused.

## Not implemented

- Remote control (mouse/keyboard), MFA, MSI installer, session recording, multi-viewer.
- Android boot autostart and battery-optimisation exemption (see §7).
