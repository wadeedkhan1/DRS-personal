# System Design Specification (Draft)
## DRS — Phase 1 MVP: Windows & Android Screen Monitoring Platform

**Companion document to:** `DRS_Phase1_SRS_FR_NFR.md`
**Status:** Draft v0.1 — built from the confirmed tech stack decision (see Section 2)

---

## 1. Overview

This SDS translates the FR/NFR list into a concrete system: what runs where, how components
talk to each other, how the database is shaped, and how the whole thing is deployed on a single
VPS. It also documents two deviations from your tech-stack decision that I'm flagging as risks
rather than silently accepting — see Section 3.

An architecture diagram was rendered in the chat alongside this document — refer to it for the
component layout described in Section 4.

---

## 2. Confirmed Tech Stack — Phase 1 MVP

| Component | Technology | Notes |
|---|---|---|
| Frontend Portal | React 18, TypeScript, TailwindCSS | Single-page app, served as static build |
| Backend & Signaling | Go (Golang), Pion WebRTC | Single binary, handles REST API + WebSocket signaling |
| Primary Database | PostgreSQL 16 | Single instance on the same VPS for MVP |
| Windows Agent | C++ (Win32 / Desktop Duplication API) | DXGI-based capture |
| Android Agent | React Native shell + native module for capture | See Section 3.2 — RN wraps UI; capture stays native |
| NAT Traversal | Public STUN only (e.g. Google's `stun.l.google.com`) | Free, zero infrastructure — see Section 3.1 for the risk this creates |

### Deferred to Phase 2 (per your tech table)

| Component | Technology | Deferred impact |
|---|---|---|
| Relay / TURN fallback | Coturn | P2P connection failures behind strict NAT/firewalls have no fallback (Section 3.1) |
| Cache & real-time state | Redis 7 | Presence/session state held in-memory in the Go process instead (Section 6) |
| Object storage | MinIO / S3-compatible | Session recording (SRS FR-7.1) is out of scope for MVP — no durable place to put recordings |

---

## 3. Flagged Risks & Recommendations

### 3.1 No TURN relay — recommend keeping Coturn "on standby"

WebRTC connects two peers directly whenever possible, using STUN just to discover each side's
public address. That works fine on open home networks. It commonly **fails** behind corporate
firewalls and symmetric NAT — which describes a large share of the employee/office networks this
product is meant to monitor. Without TURN, those sessions simply won't connect, with no
retry path.

**Recommendation:** Include Coturn in the Docker Compose stack from day one (Section 7), but leave
it disabled/unused until pilot testing shows how often direct P2P fails. If failures are rare,
defer it for real. If they're common — likely, given the target environment — you flip it on
without a deployment redesign. This costs you a few hundred MB of RAM allocated but idle; it does
not cost you the Phase 2 timeline.

### 3.2 React Native for the Android agent — native module still required

`MediaProjection` (screen capture) and Android's foreground-service persistence are OS-level APIs
with no React Native bridge. Building the Android agent in RN means:

- The capture pipeline, foreground service, and permission handling are still written in
  Kotlin/Java as a native module.
- RN only wraps the enrollment screen, settings, and any on-device UI.
- Since iOS isn't in Phase 1 scope, there's no second platform to share that RN code with yet —
  the typical "write once, run twice" payoff of RN doesn't apply until iOS is actually built.

This isn't a blocker, just worth being deliberate about. If your team already has RN depth and
this is about developer velocity for the UI chrome, the hybrid approach (RN shell + native
capture module) is fine and I've designed FR-4.x around it. If the assumption was that RN avoids
native Android code entirely, it's worth reconsidering pure Kotlin for this one agent — it may
end up being less total code.

---

## 4. High-Level Architecture

Three logical layers:

1. **Clients** — Admin browser (portal), Windows agent, Android agent. All are peers from the
   server's point of view; the browser is just another WebSocket client with a UI.
2. **VPS (single server)** — Nginx (TLS termination + reverse proxy), the Go backend (REST API +
   WebSocket signaling server), PostgreSQL.
3. **Peer-to-peer media path** — once a session is authorized and SDP/ICE candidates are
   exchanged through the backend, the actual screen-share stream flows directly between the agent
   and the admin browser. The VPS is never in the media path for a successful P2P connection.

This matters for sizing: your VPS only needs to handle small JSON/WebSocket signaling messages,
not video bandwidth. A modest VPS supports far more concurrent *sessions* than it would if it were
relaying media (see Section 8.5).

### Data flow — session establishment

1. Admin selects an online device in the portal → `POST /api/sessions`.
2. Backend checks RBAC (does this Admin own this device?), creates a session record, and pushes a
   "start session" message to the target agent over its persistent WebSocket connection.
3. Agent and Admin browser exchange SDP offer/answer and ICE candidates through the backend's
   WebSocket relay (small text messages only — this is signaling, not media).
4. Both sides attempt a direct P2P connection using STUN-discovered addresses.
   - **Success:** media streams directly, VPS uninvolved from here on.
   - **Failure** (see 3.1): with no TURN, the session fails to establish. Phase 2: fall back to Coturn relay.
5. On session end (manual disconnect or timeout), both sides notify the backend, which closes out
   the session record and writes an audit log entry.

---

## 5. Database Schema

Rendered as an entity-relationship diagram in chat. Key design notes:

- Every table that scopes to an organization carries an `org_id` column now, even though Phase 1
  runs as a single organization. This is cheap insurance — see the open question in Section 9 about
  whether "multi-tenant" in your tech-stack notes means literal multi-organization SaaS. Adding
  `org_id` later to a live schema is far more painful than including it unused today.
- `devices.assigned_admin_id` is what enforces the Admin-only-sees-their-devices rule; Super Admin
  bypasses this filter entirely at the query layer, not by having a row per device.
- `sessions` has no `recording_path` column in Phase 1 — that's deliberately left out since there's
  nowhere to durably store a recording yet (Section 2). Add it in the Phase 2 migration alongside
  object storage.

| Table | Key columns | Purpose |
|---|---|---|
| `organizations` | id, name | Present but single-row in Phase 1 |
| `users` | id, org_id, email, password_hash, role (`super_admin`\|`admin`), mfa_secret | Portal accounts only |
| `devices` | id, org_id, name, type (`windows`\|`android`), os_version, assigned_admin_id, enrollment_token, last_seen_at, status | One row per enrolled agent |
| `device_groups` | id, org_id, name | Optional team/client grouping |
| `sessions` | id, device_id, admin_id, started_at, ended_at, mode (`view`\|`control`), connection_type (`p2p`\|`failed`) | One row per monitoring session |
| `audit_logs` | id, actor_user_id, action, target_type, target_id, metadata (jsonb), created_at | Immutable — insert-only |

---

## 6. Real-Time State — In-Memory, Not Redis (For Now)

With Redis deferred, device presence (who's online) and active-session tracking live in the Go
backend's own memory, not a shared store. This is a legitimate simplification **as long as you run
exactly one backend instance**, which matches the single-VPS Phase 1 plan.

The constraint this creates: if you ever run two backend instances (for zero-downtime deploys or
horizontal scaling), in-memory state breaks — Instance A won't know a device connected to Instance
B. Design the backend's presence/session-tracking code behind a small interface now
(`PresenceStore`, `SessionRegistry`) so swapping the in-memory implementation for a Redis-backed
one later is a one-file change, not a rewrite. This is the main thing the "extensibility" NFR in
the SRS is protecting against.

---

## 7. Deployment on a VPS

### 7.1 Stack

Single VPS, orchestrated with Docker Compose:

```
services:
  nginx      → reverse proxy, TLS termination (Let's Encrypt/Certbot)
  backend    → Go binary (API + WebSocket signaling)
  postgres   → PostgreSQL 16, persistent volume
  coturn     → included, disabled by default (see 3.1)
```

Frontend is a static React build served either directly by Nginx or from a tiny static-file
container — no separate app server needed.

### 7.2 Networking & security

- Only ports 80 and 443 are exposed publicly; Nginx is the sole entry point.
- Backend and PostgreSQL are only reachable inside the Docker network — never bind them to a
  public port.
- UFW (or provider firewall) allows 22 (SSH — ideally restricted to known IPs), 80, 443 only.
- TLS via Let's Encrypt, auto-renewed by Certbot.
- Public STUN (no self-hosted infrastructure needed for Phase 1 — it's free).

### 7.3 Process management

- `docker-compose.yml` with `restart: always` on every service so a VPS reboot brings everything
  back without manual intervention.
- A basic systemd unit wrapping `docker compose up` is a reasonable belt-and-suspenders addition,
  but not required if your VPS provider guarantees Docker starts on boot.

### 7.4 Backups

- Nightly `pg_dump` via cron, retained locally for N days.
- Since object storage is deferred, also cron a copy of the dump off the VPS (e.g. `rsync`/`scp`
  to a second small VPS, or even a cheap object storage bucket used *only* for backups — this
  doesn't require standing up the full MinIO/S3 recordings pipeline from your Phase 2 plan, just a
  destination for a nightly `.sql.gz` file).
- Document a restore procedure now, before you need it under pressure.

### 7.5 CI/CD (lightweight, MVP-appropriate)

- GitHub Actions: on push to `main`, build the Go binary and Docker images, run tests.
- Deploy step: SSH into the VPS, `git pull` + `docker compose up -d --build`, or push images to a
  registry and pull on the VPS. Either is fine for a single-instance MVP — don't over-engineer
  blue-green deploys yet.

### 7.6 Sizing

Because media doesn't transit the VPS (Section 4), the server's load is dominated by WebSocket
connection count and REST/API traffic, not bandwidth. A starting point:

- **2–4 vCPU, 4–8 GB RAM** comfortably handles signaling and API traffic for low hundreds of
  concurrent devices plus a normal admin load, with headroom for PostgreSQL.
- Scale up RAM before CPU if you see pressure — Postgres and many idle WebSocket connections are
  more memory-bound than compute-bound at this scale.
- Revisit sizing once Coturn is actually relaying media (Section 3.1) — TURN relay is
  bandwidth-heavy in a way signaling never is, and that's the point at which VPS specs (and
  possibly a dedicated relay box) need to grow.

### 7.7 Monitoring (kept intentionally minimal for MVP)

The original roadmap calls for a full Grafana + Prometheus + CloudWatch stack — that's
appropriately sized for the multi-stream production build, not a single-VPS MVP. For Phase 1:

- Docker's own logs (`docker compose logs`) plus a simple external uptime check (e.g. a free
  uptime-monitoring service pinging `/health`) is enough to know if something's down.
- Add real observability tooling when you have real scale to observe.

---

## 8. Security Design

- TLS 1.3 everywhere (enforced at Nginx).
- Passwords hashed with Argon2id or bcrypt; never stored plain or reversibly encrypted.
- JWT (or signed session cookie) for portal auth, short-lived with refresh; RBAC checked
  server-side on every API call — the frontend role checks are UX only, not the security boundary.
- Each agent authenticates with a unique per-device secret issued at enrollment, not a shared
  credential — this bounds the blast radius if one device is compromised.
- Audit log writes are append-only at the database level (no `UPDATE`/`DELETE` grants on that
  table for the application role).

---

## 9. Open Questions

1. **"Multi-tenant" in your tech-stack notes** — the Frontend Portal row's rationale mentions
   "multi-tenant command center." Does this mean actual multi-organization SaaS (separate client
   companies with data isolation), or just multiple Admins/teams inside one organization? I've
   defaulted to including `org_id` in the schema either way, but the answer changes how much
   tenant-isolation logic needs to exist in the API layer for Phase 1 versus later.
2. **React Native decision** — per Section 3.2, is this a firm choice (and if so, why — team
   skills, future iOS reuse, something else), or open to reconsidering native Kotlin for this
   specific agent?
3. **Coturn on standby** — comfortable including it disabled-by-default in the Compose stack now
   (near-zero cost), or do you want it fully absent from Phase 1 infra?
4. **VPS provider** — any preference (Hetzner, DigitalOcean, Linode/Akamai, AWS Lightsail, etc.)?
   Doesn't change the architecture, but affects exact setup steps I'd hand you next.

---

## 10. Next Step

Once the open questions above are resolved (or you tell me to proceed with the defaults stated),
the next artifacts are:

- Full API endpoint list (REST + WebSocket message schema)
- `docker-compose.yml` and Nginx config, ready to deploy
- Database migration files matching Section 5
