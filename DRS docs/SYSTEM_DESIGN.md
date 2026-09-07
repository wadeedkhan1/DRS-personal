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

Authenticated (`Authorization: Bearer`, both roles, each scoped by RBAC):
`GET /api/auth/me`, `/api/devices`, `/api/devices/{id}`, `/api/groups`,
`/api/groups/{id}/members`, `/api/sessions`, `/api/audit-logs`, `/api/reports/usage`,
`/api/session/ice`

Super Admin only:
`POST /api/devices/enrollment-token`, `PUT /api/devices/{id}/assign`,
`DELETE /api/devices/{id}`, `POST|PATCH /api/groups`, `DELETE /api/groups/{id}`,
`POST /api/groups/{id}/members`, `DELETE /api/groups/{id}/members/{userId}`,
`GET|POST /api/users`, `DELETE /api/users/{id}`

`ServeMux` takes one handler per method per prefix, so the two `DELETE`s under
`/api/groups/` share one entry point (`DeleteGroupOrMember`) that dispatches on the path.
Deleting a team leaves its devices enrolled but ungrouped (`devices.group_id` is
`ON DELETE SET NULL`); its memberships cascade, because a team that no longer exists must
not keep granting access.

### Portal routes (`frontend/src/App.tsx`)

| Path | Page | Access |
|---|---|---|
| `/login` | sign-in | public; redirects to the attempted path after auth |
| `/enroll?token=…` | shows the invite code and what to do with it | public |
| `/devices` | endpoint list | both roles |
| `/devices/{id}/live` | one session | both roles |
| `/viewer` | device picker | both roles |
| `/teams`, `/teams/{id}` | teams; detail has devices + members | both roles, read-only for Admin |
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
| `/ws/session?deviceId=…` | one operator watching one device | JWT as the second `Sec-WebSocket-Protocol` value (`bearer, <token>`); browsers cannot set an `Authorization` header on a WS. Origin **is** checked. |
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

---

## 4. Key flows

### Enrollment

```
Super Admin → POST /api/devices/enrollment-token → "DRS-XXXXXX"
   ↓ portal renders an invite link: https://server/enroll?token=DRS-…
     (opening that link in a browser lands on /enroll, which offers the agent
      download and the code, and explains that the agent, not the browser, enrolls)
Agent (paste link) → POST /api/devices/enroll {token, name, type, os, allow_screen, allow_terminal}
                     (Windows agent always sends both capabilities true; Android omits them)
   ↓ server: hash-lookup token → create-or-update device row → mint fresh 32-byte agent secret
   ← {deviceId, agentSecret, wsUrl, heartbeatIntervalSeconds}
```

Enrollment needs **two** things at the endpoint: the agent binary and the token. The invite
link carries both — `/enroll` serves the binary from `/downloads/` alongside the code — so an
admin sends one link rather than a link plus a file through some other channel.

Behaviours worth knowing, all deliberate:

- **Tokens are reusable and effectively non-expiring** (TTL is 100 years and redemption does
  not mark them used). One invite link can enroll many machines. The trade: anyone holding a
  token can enroll devices indefinitely.
- Only the token **hash** is stored, so DB read access is not enough to use one.
- **Devices are de-duplicated by `(org_id, name, type)`.** Re-running the agent on the same
  machine re-enrolls that same row and rotates its secret rather than spawning a duplicate
  that lingers offline forever. A distinct hostname still creates a distinct device.
- A Super Admin may target any admin and any team, or leave devices unassigned. The route is
  registered `superAdminOnly`, so **an Admin cannot generate an invite link at all**;
  `GenerateEnrollmentToken` still contains a branch pinning a non-super-admin's tokens to
  themselves, which is dead code today. Opening it up is a one-word change in `main.go`
  (`superAdminOnly` → `anyAdmin`) — the handler already does the right thing.
- No device row is created when a token is *generated* — only on redemption. (An earlier
  version pre-created one, leaving a permanent "Pending Device" per unused token.)
- **Agent binaries are served from `/downloads/`**, public like `/enroll` itself: whoever
  installs an agent generally has no portal account. Holding the binary grants nothing —
  enrolling still needs a token. They are **uploaded**, not built into the image, because the
  Windows agent links libvpx through CGO and uses Fyne and so cannot be produced by the Linux
  build. Both `/enroll` and the enroll modal probe the file with `HEAD` and adapt, since a
  fresh deployment legitimately has none; `deploy/nginx/conf.d/drs.conf` gives `/downloads/`
  its own `location` so a missing file 404s instead of falling through to the SPA and
  answering `index.html` with a 200, which would make every probe report success.

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
| `devices` | Enrolled endpoints: type, OS, IP, `agent_secret_hash`, assignment, `allow_screen`, `allow_terminal`, status, JSONB metadata. |
| `enrollment_tokens` | Hashed tokens with their assignment and team, so redemption knows where the device lands. |
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
| Enrollment | Hashed single-credential token. Public endpoint by necessity. |
| RBAC | Super Admin sees the whole org; Admin sees a device **assigned to them or in a team they belong to**. Enforced on REST (`adminVisibleDevices`, one SQL fragment used by every list), on `/ws/session` (`canViewDevice`), and per presence event (`visibleToAdmin`) — the three must agree, since a REST list and a session socket disagreeing shows up as a mysterious 404 after someone has been told they have access. Membership is read live on REST calls and on every session open; it is deliberately **not** a JWT claim, since a 24h token with no revocation list would mean removing someone from a team did not remove their access. |
| Presence-feed staleness | The presence socket reads the viewer's team memberships **once at upgrade** and holds them, because `DeviceMeta` carries the assignment fields specifically to keep per-event filtering off the database. The bounded cost: removing an Admin from a team does not cut their presence feed until they reconnect, so they can still see whether an unreachable device is online. Nothing can be viewed or run on that basis — both data paths check live. |
| Assignment freshness | `AssignDevice` and `DeleteGroup` push the new assignment into the presence store (`UpdateDeviceAssignment`). Presence meta is otherwise written only when an agent connects, so reassigning an online device used to leave its presence record pointing at the previous admin until that agent reconnected — cosmetic while a team was a caption, a failure to revoke once membership grants visibility. |
| Enumeration | "Not found" and "not permitted" return the same answer everywhere, so endpoints cannot be used to discover other admins' devices or which emails have accounts. |
| Device capabilities | `allow_screen` / `allow_terminal`, stored per device and enforced server-side on every session — a device without `allow_terminal` cannot have a command run on it *even by a Super Admin*. The Windows agent requests **both** at enrollment and offers the end user no choice, so this is a server-side policy field, not an end-user consent prompt. Nothing exposes it for editing yet: re-enrolling the device is the only way to change it (see §9). |
| Session isolation | One viewer per device (`RegisterSession`). Routing by connection identity. Inbound frame allowlists. |
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
| `SESSION_FPS` / `SESSION_MAX_WIDTH` | `24` / `1280` | Requested of agents, which clamp again. 720p@24 is where the pure-Go pipeline holds a smooth rate on ordinary hardware. |
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

**Working end to end locally:** enrollment (GUI and CLI), presence, live WebRTC video from
the Windows agent, remote terminal, per-device capability enforcement, invite links, hostname
de-dup, audit trail, usage report, forced TURN relay, teams with membership-based access,
device reassignment from the portal, session history, and deep-linkable portal routes.

**Deferred / not built:**

| Gap | Consequence |
|---|---|
| Redis for presence & session state | In-memory, so **single backend instance only**. The `PresenceStore` / `SessionRegistry` interfaces (with compile-time assertions) are the seam that keeps the swap cheap. |
| Per-admin invite links | The endpoint is Super Admin only; see §4. |
| Roles beyond two constants | `super_admin` / `admin` are string constants with a DB `CHECK`. No roles or permissions table, no per-resource grants. |
| Changing a user's role or password | `models.UpdateUserRequest` exists with no handler and no route. An account's role and password are fixed at creation; the only remedy is delete and recreate. |
| Server-side device filtering | `GET /api/devices` takes no query parameters (SRS FR-6.2). The portal holds the full scoped list and filters by search, status, platform and team client-side, which is fine at this fleet size and is the thing to revisit first when it is not. |
| One team per device | `devices.group_id` is a single column, so a device belongs to at most one team. |
| Token redemption tracking | `enrollment_tokens.used_at` and `.device_id` exist and are written by nothing, which is what makes tokens reusable (see §4). |
| Session recording, object storage | No durable place to put recordings. |
| MFA | `users.mfa_secret` exists but nothing uses it. |
| Remote input / control | `sessions.mode` allows `'control'`; nothing implements it. Terminal is the only input path. |
| Interactive PTY | Terminal is a stateless one-shot runner; a PTY goes on the same envelope vocabulary later. |
| Android terminal | No `terminal_command` handler on Android. |
| Android boot autostart | The app must be launched manually. |
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
