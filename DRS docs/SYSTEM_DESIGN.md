# DRS — System Design (as built)

**Status:** describes the system **as it currently exists in this repo**, not the plan.
For the original intent see `DRS_Phase1_SRS_FR_NFR.md` and `DRS_Phase1_SDS.md` — where they
disagree with this document, this document is what the code does.

Per-agent detail lives in [`AGENT_WINDOWS.md`](AGENT_WINDOWS.md) and
[`AGENT_ANDROID.md`](AGENT_ANDROID.md).

---

## 1. What it is

A remote screen-monitoring platform. Operators log into a web portal, see their enrolled
devices' live online/offline state, and open a live view of one device's screen. Video travels
**peer-to-peer from the device to the operator's browser** over WebRTC; the server only
relays signaling. On devices whose `allow_terminal` capability is set — every Windows agent,
unless an admin revokes it — the operator can also run shell commands.

---

## 2. Components

| Component | Path | Tech | Role |
|---|---|---|---|
| **Backend** | `backend/cmd/server` | Go, `net/http` + `gorilla/websocket` | REST API + WebRTC signaling relay. One process. |
| **TURN relay** | `backend/cmd/turnserver` | Go, `pion/turn/v4` | Media relay for networks with no direct path. Separate binary. |
| **Database** | `backend/migrations` | PostgreSQL 16 | Users, devices, teams and their membership, sessions, audit, enrollment tokens. |
| **Portal** | `frontend/` | React 18, TypeScript, Vite, Tailwind, lucide-react, react-router-dom v6 | Operator SPA. Real paths, not tab state — see §3, *Portal routes*. |
| **Windows agent** | `agents/windows/` | Go + CGO (libvpx), Fyne GUI | Desktop endpoint: capture, stream, run commands. |
| **Android agent** | `agents/android/` | React Native + Kotlin module | Mobile endpoint: capture and stream only. |
| **Wire protocol** | `backend/pkg/protocol` | Go (source of truth) | Mirrored by `agents/windows/internal/protocol`, `agents/android/src/protocol.ts`, `frontend/src/api/protocol.ts`. |

**Media never passes through the backend.** Its load is signaling frames and API calls, which
is why one modest instance carries many concurrent sessions. When no direct path exists,
media goes through the TURN relay — which keeps DTLS-SRTP, congestion control and loss
recovery — rather than through a hand-rolled relay over the signaling socket. There is
deliberately **no video-over-WebSocket frame type** in the protocol.

---

## 3. Endpoints

### REST (`backend/cmd/server/main.go`)

Public:
| Method | Path | Notes |
|---|---|---|
| GET | `/health` | Returns 503 if the DB is unreachable. |
| POST | `/api/auth/login` | Email + password → 24h JWT. One error message for unknown user and wrong password alike. |
| POST | `/api/devices/enroll` | Public by necessity — the agent has no credential yet; the token *is* the credential. |
| GET | `/api/enroll/agent?token=…&platform=…` | The agent binary, with this invite's server address and token appended for Windows. Public for the same reason. An unknown or revoked token gets a **404**, not a generic binary. |
| GET | `/api/enroll/availability` | Which agents this deployment has staged. Says nothing about tokens. |

Authenticated (`Authorization: Bearer`, both roles, each scoped by RBAC):
`GET /api/auth/me`, `/api/devices`, `/api/devices/{id}`, `/api/groups`,
`/api/groups/{id}/members`, `/api/sessions`, `/api/audit-logs`, `/api/reports/usage`,
`/api/session/ice`, `POST /api/devices/enrollment-token`,
`GET /api/devices/enrollment-tokens`, `DELETE /api/devices/enrollment-token/{id}`

The three invite-link routes are scoped inside their handlers rather than by role: an
Admin's link is pinned to them and restricted to a team they belong to, and they can list
and revoke only links they created.

Super Admin only:
`PUT /api/devices/{id}/assign`, `DELETE /api/devices/{id}`, `POST|PATCH /api/groups`,
`DELETE /api/groups/{id}`, `POST /api/groups/{id}/members`,
`DELETE /api/groups/{id}/members/{userId}`, `GET|POST /api/users`, `DELETE /api/users/{id}`

`ServeMux` takes one handler per method per prefix, so the two `DELETE`s under
`/api/groups/` share one entry point (`DeleteGroupOrMember`) that dispatches on the path.
The invite-link routes sit *underneath* `/api/devices/` and rely on Go 1.22 specificity to
win over `GetDevice` and `DeleteDevice`; `cmd/server/routes_test.go` pins that, because a
conflict there is a panic at registration — the server would fail to boot, not fail to
build.

Deleting a team leaves its devices enrolled but ungrouped (`devices.group_id` is
`ON DELETE SET NULL`); its memberships cascade, because a team that no longer exists must
not keep granting access.

### Portal routes (`frontend/src/App.tsx`)

| Path | Page | Access |
|---|---|---|
| `/login` | sign-in | public; redirects to the attempted path after auth |
| `/enroll?token=…` | offers the configured agent download and the Android deep link | public |
| `/invites` | outstanding invite links, with revoke | both roles, scoped to links you created |
| `/devices` | endpoint list | both roles |
| `/devices/{id}/live` | one session | both roles |
| `/viewer` | device picker | both roles |
| `/monitor` | monitoring wall over every visible device | both roles |
| `/teams`, `/teams/{id}` | teams; detail has devices + members | both roles, read-only for Admin |
| `/teams/{id}/monitor` | monitoring wall scoped to one team | both roles |
| `/sessions`, `/audit`, `/reports` | history and reporting | both roles |
| `/users` | admin accounts | Super Admin |

This was a single `currentTab` string until recently, which meant the URL never changed:
no deep links, no back button, and a refresh always landed on the device list even
mid-session. `/devices/{id}/live` resolves its device from the loaded list, falling back to
`GET /api/devices/{id}` — that fallback is what makes the URL survive a paste or a refresh,
when there is no device list yet. Route guards (`RequireAuth`, `RequireSuperAdmin`) only
decide what to render; `middleware.RequireRole` on the server remains the actual boundary.

### WebSocket (`backend/internal/ws/`)

| Path | Client | Auth |
|---|---|---|
| `/ws/agent` | device | `hello` frame with device id + agent secret. Origin unchecked — a native process has no ambient cookie authority. |
| `/ws/session?deviceId=…[&fps=…&maxWidth=…]` | one operator watching one device | JWT as the second `Sec-WebSocket-Protocol` value (`bearer, <token>`); browsers cannot set an `Authorization` header on a WS. Origin **is** checked. |
| `/ws/presence` | dashboard | same bearer subprotocol. |

Two invariants govern the relay:

1. **It never interprets media or SDP** — signaling frames are forwarded verbatim. The one
   exception is `terminal_command`, which is parsed so it can be audited before forwarding.
2. **Routing is by connection identity, never by an id in the payload.** A frame is delivered
   to whoever the hub's own maps say is on the other end. A client cannot reach a session it
   does not own by naming it.

The viewer's inbound allowlist is the security boundary: a browser may only send `answer`,
`ice_candidate` and `terminal_command`. Anything else is dropped — notably an `offer`, since
accepting one would invert the negotiation.

`fps` and `maxWidth` on `/ws/session` are optional, and let one viewer ask for a cheaper
session than the deployment default — the monitoring wall runs its tiles at 4fps/480px.
Both are clamped server-side to 1–60 and 320–3840 (`clampQuery` in `ws/session.go`), the
same bounds the Windows agent applies to what it receives, so the two clamps cannot
disagree about what a request means. Absent or unparseable falls back to `SESSION_FPS` /
`SESSION_MAX_WIDTH`, so a caller that sends neither behaves exactly as before they existed.

---

## 4. Key flows

### Enrollment

Enrollment is **zero-touch**: one reusable link, and the recipient types nothing.

```
Admin → POST /api/devices/enrollment-token → "DRS-XXXXXX"  (row: team, assigned admin, label)
   ↓ portal renders one invite link: https://server/enroll?token=DRS-…
     the same link works for any number of devices, and for both platforms

Windows                                  Android
  GET /api/enroll/agent?token=…            GET /api/enroll/agent?platform=android&token=…
  ← drs-agent.exe + config trailer         ← drs-agent.apk (unmodified)
  run it                                   install, tap "Set up the agent"
  ↓ reads its own trailer                  ↓ drs://enroll?server=…&token=…
  
Agent → POST /api/devices/enroll {token, name, type, os, machine_id, allow_*}
   ↓ server: hash-lookup token (rejecting revoked/expired) → find-or-create the device
     by machine_id → mint a fresh 32-byte agent secret
   ← {deviceId, agentSecret, wsUrl, heartbeatIntervalSeconds}
  ↓ Windows also registers itself to start at login
```

The thing that makes this work is that **the download is personalised per invite**. The
backend appends a small config trailer — this server's address and this token — to the
`.exe` as it streams it (`pkg/agentcfg`), and the agent reads it back out of its own file on
first run. A PE's headers describe where its sections end, so bytes after them are never
mapped and cannot corrupt the executable. Two alternatives were rejected: encoding the
config in the *filename* breaks the moment anyone renames the file or a browser appends
" (1)", and building a per-invite binary server-side would need a Go toolchain and a CGO
libvpx build inside the container. The trade-off worth naming is that appending invalidates
an Authenticode signature — the agent is unsigned today, and if it is ever signed the config
has to move to a signed resource or a sidecar.

**Android cannot be personalised the same way.** The system renames an installed APK to
`base.apk` and an app cannot read its own installer, so the phone is configured by a
`drs://enroll?server=…&token=…` deep link from the same page instead. A private scheme
rather than an https App Link, because App Links need a verified domain hosting
`assetlinks.json` and this deployment is routinely an IP address on a LAN.

Behaviours worth knowing, all deliberate:

- **Tokens are reusable.** One invite link enrolls as many machines as you point it at, each
  getting its own device row and its own secret. Redemption does not mark a token used.
- **Revocation and expiry ARE enforced**, in exactly two places: `redeemToken` and the
  download endpoint, both filtering `revoked_at IS NULL AND expires_at > NOW()`. The revoke
  endpoint would be decorative without those clauses. Revoking kills any installer already
  downloaded from that link, and deliberately does **not** touch devices it already enrolled
  — those hold their own secrets, and removing one is a separate decision.
- Only the token **hash** is stored, so DB read access is not enough to use one.
- **Devices are de-duplicated by `machine_id`**, a UUID the agent generates once and keeps
  beside its identity file. It used to be `(org_id, name, type)`, which breaks under fleet
  rollout: cloned VMs and imaged PCs share a hostname, and the second to enroll would match
  the first's row and rotate its secret — leaving the first agent reconnecting forever with
  a credential the server had discarded. An agent that sends no machine id still falls back
  to the name lookup, and a machine id that matches nothing falls back to a name lookup
  **restricted to rows that have claimed no machine id**, so a device enrolled before this
  existed adopts its own row rather than duplicating it.
- **Any admin can generate an invite link.** A Super Admin may target any admin and any team;
  an Admin's link is pinned to themselves and `canSeeGroup` restricts it to a team they
  belong to. That check is load-bearing — without it, opening the route would let any Admin
  mint a link into any team, which is escalation rather than convenience.
- No device row is created when a token is *generated* — only on redemption. (An earlier
  version pre-created one, leaving a permanent "Pending Device" per unused token.)
- **Agent binaries are uploaded, not built into the image**, because the Windows agent links
  libvpx through CGO and uses Fyne and so cannot be produced by the Linux build. They live in
  one directory served two ways: nginx publishes it at `/downloads/` for the plain
  unconfigured binary and the CLI route, and the backend reads it (`AGENT_BINARY_DIR`, a
  read-only bind mount) to personalise a download. A fresh deployment legitimately has
  neither, so the portal asks `GET /api/enroll/availability` and says which situation it is
  in rather than offering a download that 404s.
- The download endpoint lives under `/api/` because nginx routes only `/api/`, `/ws/` and
  `/health` to the backend. That also fixed a dev/prod split: `/downloads` is not in the Vite
  proxy, so the old `HEAD` probe hit the SPA fallback, got a 200, and reported every binary
  as present in development whether or not one existed.

### Device presence

`hello` → `welcome` → heartbeat every `HEARTBEAT_SECONDS` (server-chosen, so the read
deadline and the beat rate cannot disagree). Each beat carries CPU %, RAM %, hostname and OS,
folded into `devices.metadata` and `last_seen_at`.

Liveness is inferred from heartbeats, not from close frames: **miss three intervals and the
socket is considered dead.** A machine that loses power never says goodbye, and without this
it would read as online until TCP eventually gave up.

Presence is tracked **in-memory** (`internal/presence`) behind `PresenceStore` /
`SessionRegistry` interfaces, and mirrored to Postgres so the dashboard is right after a
backend restart. `GET /api/devices` overlays live presence over the stored row. Presence
events are RBAC-filtered **at delivery**, so an Admin is never told about another Admin's
devices, and are published non-blocking — a slow dashboard loses events rather than stalling
the hub.

Reconnecting agents evict the previous socket **explicitly**. Overwriting the map entry alone
would let the stale connection's deferred cleanup delete the *new* entry moments later,
making a live device read as offline.

### A monitoring session

```
Browser opens /ws/session?deviceId=…
  ├─ JWT validated, RBAC checked (canViewDevice), device must be online
  ├─ refused if the device consented to neither screen nor terminal
  ├─ registry.RegisterSession claims the device        ← one viewer per device
  ├─ session row written, session_start audited
  ├─ → browser: session_capabilities {allowScreen, allowTerminal}
  └─ → agent:   start_session {sessionId, fps, maxWidth, iceServers, iceTransportPolicy}
                (only if allowScreen — a terminal-only device never starts capture)

Agent → session_ready → offer      ┐
Browser → answer                   ├─ relayed verbatim by the hub
ICE candidates ↔ both ways         ┘

Media: agent ⇄ browser, direct or via TURN. Never through the backend.
```

**The agent is always the offerer** — it owns the media. `session_ready` is announced before
the offer so the browser can build its `RTCPeerConnection` rather than racing it.

Teardown has exactly one path (`viewerConn.endSession`, idempotent) reached from every exit:
normal disconnect, browser crash, network drop, panic, or the agent dropping. Closing the
socket *is* ending the session — the backend then sends the agent `stop_session`
unconditionally. There is no separate "end session" message to forget.

### The monitoring wall

`/monitor` and `/teams/{id}/monitor` (`frontend/src/pages/GroupMonitorPage.tsx`) show many
devices at once. There is no multiplexing and no new server concept behind it: **each tile
is an ordinary session**, one `/ws/session` socket and one `RTCPeerConnection` per device,
opened by `useDeviceSession`. Everything above therefore applies to each tile unchanged,
including the one-viewer-per-device claim — a tile whose device another operator already
holds is refused with close code 1008 and says so in place of video.

Three consequences worth stating plainly, because they follow from that choice:

- **A live tile locks its device.** Twelve tiles lock twelve machines. `MAX_LIVE_TILES = 12`
  caps how many connect on their own for that reason as much as for bandwidth; devices past
  the cap render a Start button instead.
- **Tiles are cheap on purpose.** Each asks for `fps=4&maxWidth=480`. At the default
  24fps/1280px a dozen panes would be a dozen devices' encoders and roughly 30 Mbps into one
  browser.
- **Enlarging re-opens the session rather than renegotiating it.** Clicking a tile parks its
  grid session and opens a new one at the server defaults. The alternative — a message
  telling a live agent to change rate mid-stream — would need new protocol on both agents,
  and on Android would mean re-acquiring the capture track anyway. The cost is a ~1s black
  frame on enlarge. The grid tile *must* park first: two sessions on one device would have
  the wall refuse itself.

The second of those needs more than ordering the two React effects correctly. The server
frees a device's viewer slot when it *sees* the socket close, which is a round trip after
the browser asks — so reopening immediately races the session on its way out and is
refused as "already being viewed" by itself. `useDeviceSession` therefore keeps a
**per-device handoff gate**: `SessionConnection.close()` returns a promise that resolves on
the socket's `close` event (with a 3s ceiling, since a socket that never acknowledges must
not block the device forever), the teardown parks that promise in a map keyed by device id,
and the next session on that device awaits it before calling `start()`. Resolved already in
the ordinary case, so it costs a microtask.

Access control adds nothing new. The device list comes from `GET /api/devices`, already
scoped by `adminVisibleDevices`; each tile's socket re-checks `canViewDevice` against live
team membership; and a team-scoped wall resolves its team from `ListGroups`, which for an
Admin returns only teams they are a member of. An unresolvable team id renders the same
"not found" card whether it does not exist or the operator is simply not on it.

### Remote terminal

```
Browser → terminal_command {commandId, command, shell}
   ↓ hub: parse → consent gate (allowTerminal) → AUDIT → forward
Agent → run (PowerShell/cmd, 60s cap, 256 KiB per stream) → terminal_result
   ↓ hub forwards to the one operator whose command produced it
```

Authorization is not re-checked per command — the operator already passed `canViewDevice`
when the socket opened. But **every command is audited before it is forwarded**, and a
command aimed at a device that never consented is audited as `terminal_command_denied` and
answered with an error rather than relayed.

### NAT traversal

One `ice.Provider` feeds **both** peers: the browser fetches the list from
`GET /api/session/ice`, the agent receives an identical copy inside `start_session`. Peers
negotiating against different candidate universes fail in a way that is nearly unreadable
from either end, so there is a single source.

TURN uses the **coturn REST scheme**: the username is an expiry timestamp, the password is
`HMAC-SHA1(secret, username)` base64'd. No per-user state anywhere, and credentials age out
on their own. `ice.RESTCredential` is the single definition of that derivation — the relay
validates incoming credentials by calling the same function, so the two cannot drift apart.

`FORCE_TURN_RELAY=true` sets `iceTransportPolicy: "relay"` on both peers, discarding host and
server-reflexive candidates so media can only move through the relay. Connectivity then stops
depending on either endpoint's NAT: both sides make an *outbound* allocation, so neither needs
a public address or an open inbound port. Budget ~7–8 Mbps per session at default encode
settings. `TransportPolicy()` returns `"relay"` only when TURN is actually configured, and
the backend **refuses to start** if `FORCE_TURN_RELAY` is set without it.

---

## 5. Data model (`backend/migrations`)

Migrations are **embedded in the binary** and applied at boot, each in its own transaction
together with the row recording it — so a migration either applies completely and is marked,
or does neither. There is one code path for local and deployed runs.

| Table | Purpose |
|---|---|
| `organizations` | Tenant root. One is seeded on first boot. |
| `users` | Portal accounts: `super_admin` \| `admin`. bcrypt password hashes. |
| `device_groups` | Teams. Name is unique per org (case-insensitively), because a second "Finance" is a way to believe you granted access you did not. |
| `user_group_members` | Which admins are on which team. This is what makes a team an access grant rather than a caption. |
| `devices` | Enrolled endpoints: type, OS, IP, `agent_secret_hash`, assignment, `allow_screen`, `allow_terminal`, status, JSONB metadata, `machine_id`, `enrolled_via_token`. |
| `enrollment_tokens` | Hashed tokens with their assignment, team, label and revocation state, so redemption knows where the device lands and whether the link is still live. |
| `sessions` | Session history (start, end, mode, status) behind the usage report. |
| `audit_logs` | Append-only trail. |

Two hardening details in migration 2:

- `audit_logs.actor_user_id` has **no foreign key**. It used to be `ON DELETE SET NULL`, so
  deleting a user quietly erased who did what across the entire history. The denormalised
  `actor_email` alongside it keeps the record readable after the user is gone.
- A `BEFORE UPDATE OR DELETE` trigger **enforces** append-only. The claim was previously
  aspirational — the application connects as the owner and could rewrite at will.

Migration 3 adds the consent columns with asymmetric defaults: `allow_screen` **TRUE** (so
existing devices and an enroll request that omits the fields keep working) and
`allow_terminal` **FALSE**. Those defaults only cover a caller that sends neither field —
today only the Android agent. The Windows agent always sends both as **true** (see
§6, *Device capabilities*).

Migration 4 adds `user_group_members`, an index on `devices.group_id` (which had none,
correctly, while nothing filtered on it) and the unique team name per org. It folds any
pre-existing duplicate team names into distinct ones first rather than failing on data that
was legal when it was written.

Migration 5 gives an invite link an off switch: `revoked_at`, `revoked_by` and a human
`label`, plus `devices.enrolled_via_token` so the portal can show how many devices a link
has produced — the number that decides whether revoking it is safe. That column is
`ON DELETE SET NULL`, because deleting a spent token must never cascade into deleting real
machines.

Migration 6 adds `devices.machine_id` with a **partial** unique index on
`(org_id, machine_id) WHERE machine_id IS NOT NULL`. Partial because every device enrolled
before it existed has NULL there and several NULLs must stay legal; stating the predicate
also keeps the index off rows that can never match.

**Team membership is additive, never restrictive.** An Admin sees a device assigned to them
**or** in a team they belong to. Restrictive would have silently revoked access to every
individually-assigned device the moment the migration ran, and would make an empty
membership table mean "no Admin can see anything".

---

## 6. Security model

| Layer | Mechanism |
|---|---|
| Operator auth | bcrypt password → HS256 JWT (24h). `JWT_SECRET` required, ≥32 chars, **no default** — the server refuses to start without it. |
| Agent auth | 32-byte agent secret, sent over TLS in `hello`; only its hash is stored, compared constant-time. |
| Enrollment | Hashed token, stored hash-only. Public endpoint by necessity — the machine has no credential yet, so the token is the credential. |
| The installer is a credential | This is the sharpest edge in the system. A download from an invite link is a **self-enrolling payload**: whoever runs it joins the organization and it registers itself to start at login, with no prompt. That is what makes a ten-machine rollout practical, and it means a forwarded `.exe` is a way in. Three things bound it: the download endpoint refuses an unknown or revoked token with a 404, revocation kills already-downloaded installers because `redeemToken` re-checks on every enrollment, and every link is listed at `/invites` with the number of devices it produced. What revocation does **not** do is un-enroll devices already joined — they hold their own secrets, and removing one is a separate act. |
| Invite scope | An Admin's link is pinned to them (`AdminID = claims.UserID`) and `canSeeGroup` restricts its team to one they belong to, so a link can grant no access its creator does not already have. Listing and revoking are scoped by `created_by` the same way, and a link belonging to someone else answers "not found" rather than confirming it exists. |
| RBAC | Super Admin sees the whole org; Admin sees a device **assigned to them or in a team they belong to**. Enforced on REST (`adminVisibleDevices`, one SQL fragment used by every list), on `/ws/session` (`canViewDevice`), and per presence event (`visibleToAdmin`) — the three must agree, since a REST list and a session socket disagreeing shows up as a mysterious 404 after someone has been told they have access. Membership is read live on REST calls and on every session open; it is deliberately **not** a JWT claim, since a 24h token with no revocation list would mean removing someone from a team did not remove their access. |
| Presence-feed staleness | The presence socket reads the viewer's team memberships **once at upgrade** and holds them, because `DeviceMeta` carries the assignment fields specifically to keep per-event filtering off the database. The bounded cost: removing an Admin from a team does not cut their presence feed until they reconnect, so they can still see whether an unreachable device is online. Nothing can be viewed or run on that basis — both data paths check live. |
| Assignment freshness | `AssignDevice` and `DeleteGroup` push the new assignment into the presence store (`UpdateDeviceAssignment`). Presence meta is otherwise written only when an agent connects, so reassigning an online device used to leave its presence record pointing at the previous admin until that agent reconnected — cosmetic while a team was a caption, a failure to revoke once membership grants visibility. |
| Enumeration | "Not found" and "not permitted" return the same answer everywhere, so endpoints cannot be used to discover other admins' devices or which emails have accounts. |
| Device capabilities | `allow_screen` / `allow_terminal`, stored per device and enforced server-side on every session — a device without `allow_terminal` cannot have a command run on it *even by a Super Admin*. The Windows agent requests **both** at enrollment and offers the end user no choice, so this is a server-side policy field, not an end-user consent prompt. Nothing exposes it for editing yet: re-enrolling the device is the only way to change it (see §9). |
| Session isolation | One viewer per device (`RegisterSession`). Routing by connection identity. Inbound frame allowlists. The monitoring wall does not weaken this — it opens N ordinary sessions, each claiming its own device, so watching many screens at once grants no access that watching them one at a time would not. |
| Client-supplied session quality | `fps` / `maxWidth` on `/ws/session` are the only viewer-chosen numbers that reach an agent. Both are clamped server-side before they are put in `start_session`, and both are already clamped again by the agent. The worst a client can ask for is the deployment's own maximum. |
| Media | DTLS-SRTP end-to-end between agent and browser. The TURN relay forwards packets it cannot read. |
| Audit | Login success/failure, enrollment, device assign/delete, user create/delete, team create/update/delete, team member add/remove, session start/end, every terminal command (and every denied one). Team mutations are permission changes, so they belong in the trail alongside `assign_device` (SRS FR-1.8). **Audit logs stay actor-scoped:** an Admin sees entries where they were the actor. Team membership does not widen this, because `actor_user_id` records who *did* something, which a team does not extend. |
| Robustness | Every socket goroutine wraps a `recover` — one malformed connection cannot take the process down. Per-socket write mutexes, since gorilla panics on concurrent writes. Read limits: 1 MiB agent, 64 KiB viewer. |

**No secret has a working default.** `JWT_SECRET`, `DB_PASSWORD`, `DEFAULT_SUPERADMIN_PASSWORD`
and `TURN_SHARED_SECRET` must all be set; failing to start is the safer outcome than coming up
with publicly-known credentials.

---

## 7. Configuration (`backend/.env`)

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` | |
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_NAME` / `DB_SSLMODE` | `127.0.0.1` / `5432` / `postgres` / `drs_db` / `disable` | The DB is created on first run if missing. |
| `DB_PASSWORD` | — | **Required.** |
| `JWT_SECRET` | — | **Required, ≥32 chars.** |
| `CORS_ORIGINS` | `http://localhost:3000` | Explicit list; `*` is dev-only and rejected with credentials. |
| `HEARTBEAT_SECONDS` | `10` | Read deadline is 3×. |
| `SESSION_FPS` / `SESSION_MAX_WIDTH` | `24` / `1280` | Requested of agents, which clamp again. 720p@24 is where the pure-Go pipeline holds a smooth rate on ordinary hardware. A viewer may ask for less per session; see §3. |
| `AGENT_BINARY_DIR` | — (disabled) | Where `drs-agent.exe` / `.apk` are staged. Setting it turns on zero-touch enrollment: the backend serves the Windows agent with the invite's server address and token appended. Empty means the enroll page says no agent is published, rather than offering a download that cannot work. The only filesystem path the backend knows about. |
| `PUBLIC_BASE_URL` | — (derived) | The origin the server is reachable at. Normally derived per request from `X-Forwarded-Proto` / `X-Forwarded-Host` / `Host`. Set it only when that is wrong: this value is **written into downloaded agents**, so a wrong one produces agents that can never connect, on machines nobody is watching. |
| `STUN_URLS` | `stun:stun.l.google.com:19302` | Comma-separated. |
| `TURN_PUBLIC_IP` / `TURN_SHARED_SECRET` / `TURN_PORT` / `TURN_REALM` / `TURN_CRED_TTL_SECONDS` | — / — / `3478` / `drs` / `3600` | Both first two needed to enable TURN. |
| `FORCE_TURN_RELAY` | `false` | Requires TURN configured or the server refuses to start. |
| `TURN_LISTEN_ADDR` / `TURN_MIN_PORT` / `TURN_MAX_PORT` | `0.0.0.0` / `49160` / `49200` | Relay only. `TURN_PUBLIC_IP` must be an IP, not a hostname. |
| `DEFAULT_SUPERADMIN_EMAIL` / `_PASSWORD` | `admin@drs.local` / — | The first account is created only if no super admin exists and the password is set. |

The relay reads **the same `.env`**, so one file keeps the credentials the backend hands out
and the credentials the relay accepts in agreement by construction.

---

## 8. Deployment

The step-by-step procedure — secrets, hostname, certificate, firewall, verification — is in
[`DEPLOYMENT.md`](DEPLOYMENT.md). This section is the shape of the stack, not the runbook.

### Local (`start_local.bat`, `start_relay.bat`)
Checks Postgres on 5432, creates `backend/.env` from the example, starts `go run ./cmd/server`
(applies migrations) and `npm run dev` on port 3000. `start_relay.bat` adds the TURN relay for
testing forced-relay locally.

### VPS (`deploy/docker-compose.yml`)

```
nginx (80/443, TLS + built portal)  →  backend (8080)  →  postgres:16
certbot (renews, reloads nginx every 12h)
turn (profile "turn", network_mode: host)
```

- The frontend image **builds** the SPA, so there is no hand-maintained `dist/`.
- `deploy/downloads/` is bind-mounted to nginx and served at `/downloads/`. A bind mount, not
  an image layer, so publishing a new agent build is an upload rather than a rebuild — and no
  reload is needed, since the directory is read per request.
- No migrations are mounted into Postgres — the backend binary embeds and applies them.
- `network_mode: host` on the relay is not optional: it allocates a fresh port per session,
  and publishing that range through Docker's bridge would start a userspace proxy per port
  and rewrite the source addresses the relay depends on.
- Enable the relay with `docker compose --profile turn up -d`.
- The backend prefers `X-Forwarded-For` / `X-Real-IP` for client IPs; behind a proxy
  `RemoteAddr` is the proxy, which would show every device at the same address.
- `http.Server` sets **no** `ReadTimeout`/`WriteTimeout` — either would apply to hijacked
  WebSocket connections and kill every long-lived socket. Header reading is bounded
  separately and the socket layer sets its own per-message deadlines.

---

## 9. State of play

**Working end to end locally:** zero-touch enrollment (a personalised download on Windows,
a `drs://` deep link on Android) alongside the GUI and CLI routes, per-admin invite links
with revocation, machine-id de-duplication, presence, live WebRTC video from the Windows
agent, remote terminal, per-device capability enforcement, audit trail, usage report,
forced TURN relay, teams with membership-based access, device reassignment from the portal,
session history, deep-linkable portal routes, the multi-screen monitoring wall, and an
Android agent that keeps running once the app is dismissed.

**Deferred / not built:**

| Gap | Consequence |
|---|---|
| Redis for presence & session state | In-memory, so **single backend instance only**. The `PresenceStore` / `SessionRegistry` interfaces (with compile-time assertions) are the seam that keeps the swap cheap. |
| Roles beyond two constants | `super_admin` / `admin` are string constants with a DB `CHECK`. No roles or permissions table, no per-resource grants. |
| Changing a user's role or password | `models.UpdateUserRequest` exists with no handler and no route. An account's role and password are fixed at creation; the only remedy is delete and recreate. |
| Server-side device filtering | `GET /api/devices` takes no query parameters (SRS FR-6.2). The portal holds the full scoped list and filters by search, status, platform and team client-side, which is fine at this fleet size and is the thing to revisit first when it is not. |
| One team per device | `devices.group_id` is a single column, so a device belongs to at most one team. |
| Token redemption tracking | `enrollment_tokens.used_at` and `.device_id` are still written by nothing — tokens are reusable by design. Revocation (migration 5) is the off switch instead, and `devices.enrolled_via_token` records what each link produced. |
| Session recording, object storage | No durable place to put recordings. |
| MFA | `users.mfa_secret` exists but nothing uses it. |
| Remote input / control | `sessions.mode` allows `'control'`; nothing implements it. Terminal is the only input path. |
| Interactive PTY | Terminal is a stateless one-shot runner; a PTY goes on the same envelope vocabulary later. |
| Android terminal | No `terminal_command` handler on Android. |
| Android boot autostart | The agent survives the app being dismissed and reconnects after the process is reclaimed, but a reboot needs the app opened once. No `BOOT_COMPLETED` receiver. |
| Android battery-optimisation exemption | Nothing asks the user to exempt the app from Doze. Aggressive OEM battery managers (Xiaomi, Samsung, Oppo) can still kill the foreground service after a long idle period. |
| Multiple viewers per one device | `Hub.viewers` and `MemoryStore.deviceSess` are both single-valued per device, and the agent produces one offer per `start_session`. Two operators cannot watch one screen simultaneously — including two monitoring walls that overlap. |
| Editing a device's capabilities | `allow_screen` / `allow_terminal` are set at enrollment and nothing can change them afterwards — no endpoint, no portal control. Re-enrolling the device is the only route. Windows agents always enroll with both, so today this only bites a device an admin wants to *restrict*. |
| Multi-monitor | Primary display only. |
| Cross-network VPS test | Untested outside the LAN. |

---

## 10. Where to look

```
backend/pkg/protocol/protocol.go      ← the wire contract, and the best single file to read first
backend/internal/ws/{hub,session}.go  ← the relay: routing, RBAC, session lifecycle
backend/internal/handlers/            ← REST, enrollment, RBAC scoping
backend/internal/presence/            ← live state + the Redis seam
backend/internal/ice/provider.go      ← ICE list and TURN credentials
agents/windows/internal/screen/       ← capture → convert → VP8 → WebRTC
agents/android/src/                   ← the RN port, module for module
frontend/src/App.tsx                  ← the route table
frontend/src/api/session.ts           ← the browser side of a session
frontend/src/components/ScreenViewer  ← the viewer UI + terminal panel
frontend/src/pages/Team*.tsx          ← teams: devices in one panel, admins in the other
```
