# Software Requirements Specification (Draft)
## DRS — Phase 1 MVP: Windows & Android Screen Monitoring Platform

**Prepared for:** SDS planning discussion
**Status:** Draft v0.2 — tech stack confirmed; see companion `DRS_Phase1_SDS.md` for architecture and deployment

---

## 1. Purpose & Scope

The client-provided document ("DRS Full Development Roadmap") describes a large, five-platform
MSP/RMM suite built across three parallel engineering streams (Windows, macOS/Linux, Android/iOS
MDM) with patch management, PSA integrations, and billing. That is the **long-term vision**, not
the Phase 1 build.

Based on your direct brief, the actual Phase 1 deliverable is narrower:

> A remote screen monitoring/control tool (AnyDesk / ScreenConnect–style) supporting **Windows
> and Android only**, with a two-tier access model:
> - **Super Admin** — sees and manages all employees/devices across the organization
> - **Admin** — sees and manages only the employees/devices/clients assigned to them

This document defines the Functional Requirements (FR) and Non-Functional Requirements (NFR)
for that Phase 1 scope only. Section 6 lists roadmap items intentionally deferred.

**Update:** The Phase 1 tech stack is now confirmed — React/TypeScript portal, Go + Pion WebRTC
backend, PostgreSQL, a C++ Windows agent, and a React Native Android agent, deployed on a single
VPS. Full details are in the companion `DRS_Phase1_SDS.md`, including two flagged risks (no TURN
fallback for NAT traversal; native-module requirement inside the React Native Android agent) that
touch several requirements below and are cross-referenced where relevant.

---

## 2. Assumptions (please confirm/correct before we move to SDS)

These assumptions materially affect the design, so it's worth locking them down now:

| # | Assumption | Why it matters |
|---|---|---|
| A1 | This is **employee-owned/company-owned device monitoring** (internal use), not a consumer support tool for external customers. | Changes consent/notification requirements and enrollment flow. |
| A2 | Screen viewing is the core need; the confirmed Windows agent stack (DXGI capture) supports this cleanly. **Full remote control (mouse/keyboard takeover)** is still open for both platforms — see SDS §9. | Changes Android complexity a lot either way; React Native doesn't reduce that either way. |
| A3 | Single organization for now — **flagged as an open question** (SDS §9), since the confirmed tech stack's rationale mentions a "multi-tenant command center," which may mean literal multi-organization SaaS rather than just multiple Admins in one org. `org_id` is included in the schema regardless, as low-cost insurance. | Changes how much tenant-isolation logic the API needs in Phase 1 vs. later. |
| A4 | Some visible notification (banner/tray icon) will appear on the monitored device when a live session starts, for legal/compliance reasons — even if monitoring itself is continuous. | Employee monitoring laws vary by country; silent-only monitoring carries legal risk. |
| A5 | "Admin" and "Super Admin" are portal users; the monitored employee does **not** log into the portal, they just have the agent installed. | Defines the user model — 2 roles, not 3. |

If any of these are wrong, the FR list below will need adjusting — flag it and we'll revise before SDS.

---

## 3. User Roles

| Role | Description |
|---|---|
| **Super Admin** | Full visibility and control over all Admins, all employees, and all devices in the system. Can create/manage Admin accounts and assign employees to them. |
| **Admin** | Can only view/manage the employees and devices explicitly assigned to them. Cannot see other Admins' employees. |
| **Employee / Monitored User** | Not a portal user. Represented in the system only via the device agent installed on their Windows PC or Android phone. |

---

## 4. Functional Requirements

### 4.1 Authentication & Role-Based Access Control (RBAC)

| ID | Requirement |
|---|---|
| FR-1.1 | System shall provide secure login (username/password) for Super Admin and Admin roles. |
| FR-1.2 | System shall support optional MFA (TOTP-based) for portal login. |
| FR-1.3 | Super Admin shall be able to view, monitor, and control **all** registered devices/employees in the system. |
| FR-1.4 | Admin shall be able to view, monitor, and control **only** devices/employees explicitly assigned to them. |
| FR-1.5 | Super Admin shall be able to create, edit, deactivate, and delete Admin accounts. |
| FR-1.6 | Super Admin shall be able to assign/reassign employees (and their devices) to a specific Admin, individually or via group. |
| FR-1.7 | System shall support grouping employees under a Team/Department/Client label to simplify assignment. |
| FR-1.8 | All login attempts, role changes, and permission/assignment changes shall be logged with actor, timestamp, and action. |

### 4.2 Device Enrollment & Agent Management

| ID | Requirement |
|---|---|
| FR-2.1 | System shall provide a Windows agent installer (.msi) supporting silent/unattended install. |
| FR-2.2 | System shall provide an Android agent (APK), installed via direct link, enrollment code, or QR code, requesting required permissions (Screen Capture / MediaProjection, Accessibility if control is in scope, Device Admin). |
| FR-2.3 | Each agent shall register with a unique Device ID and report metadata: device name, OS version, IP address, last-seen timestamp, assigned employee name. |
| FR-2.4 | Portal shall display real-time online/offline status per device. |
| FR-2.5 | Admin/Super Admin (per their permission scope) shall be able to remotely deregister/remove an agent. |
| FR-2.6 | Windows agent shall auto-start on system boot and auto-reconnect after network interruption. |
| FR-2.7 | Android agent shall run as a persistent background/foreground service and auto-reconnect after network interruption or reboot, subject to Android OS battery-optimization constraints. |

### 4.3 Windows Agent — Monitoring & Control

| ID | Requirement |
|---|---|
| FR-3.1 | Admin/Super Admin shall be able to view a live screen stream (view-only) of an assigned online Windows device. |
| FR-3.2 | *(Confirm scope — see A2)* System shall support full remote control (mouse/keyboard input) of the Windows device. |
| FR-3.3 | Streaming shall use adaptive bitrate/quality based on available network bandwidth. |
| FR-3.4 | System shall support capturing periodic screenshots (e.g., every N minutes) for a lightweight monitoring mode, as an alternative to continuous live streaming. |
| FR-3.5 | System shall support secure file transfer (upload/download) between Admin and device, with integrity validation. *(Phase 2 candidate if not core to "monitoring.")* |

### 4.4 Android Agent — Monitoring & Control

| ID | Requirement |
|---|---|
| FR-4.1 | Admin/Super Admin shall be able to view a live screen stream (view-only) of an assigned online Android device via the Android MediaProjection API, implemented as a native module inside the React Native app shell (see SDS §3.2). |
| FR-4.2 | System shall report basic device status: battery level, network type (WiFi/cellular), OS version. |
| FR-4.3 | System shall support capturing periodic screenshots as a lightweight monitoring alternative to continuous streaming. |
| FR-4.4 | *(Confirm scope)* Full remote control of Android devices — flagged as high-complexity; recommend Phase 2 unless business-critical for Phase 1. |

### 4.5 Session Management

| ID | Requirement |
|---|---|
| FR-5.1 | Admin/Super Admin shall initiate a monitoring session by selecting an online device from the portal. |
| FR-5.2 | System shall establish a real-time P2P WebRTC connection between Admin and device, using public STUN servers for NAT traversal. **MVP has no TURN relay fallback** — sessions behind strict corporate NATs/firewalls may fail to connect (see SDS §3.1, which recommends including Coturn in the deployment now, disabled by default, so it can be enabled quickly if pilot testing shows failures). |
| FR-5.3 | System shall support only one active viewing session per device at a time (configurable if concurrent viewing is needed later). |
| FR-5.4 | Sessions shall auto-terminate after a configurable inactivity timeout. |
| FR-5.5 | *(Per Assumption A4)* A visible on-device indicator (banner/tray/notification icon) shall appear while a live session is active. |

### 4.6 Admin Portal / Dashboard

| ID | Requirement |
|---|---|
| FR-6.1 | Web-based dashboard listing all devices/employees visible to the logged-in role, with live status indicators. |
| FR-6.2 | Search and filter by employee name, device name, group, or online/offline status. |
| FR-6.3 | Device detail view showing last-seen screenshot/thumbnail, connection history, and device metadata. |
| FR-6.4 | Support for organizing devices into teams/groups/clients for Admin-level segregation. |

### 4.7 Session Recording & Audit Trail

| ID | Requirement |
|---|---|
| FR-7.1 | *(Deferred to Phase 2 — see Section 6)* Session recording requires object storage, which was cut from the confirmed Phase 1 stack. |
| FR-7.2 | System shall maintain an immutable audit log of all portal actions: logins, session start/stop, file transfers, control actions — each with actor identity and timestamp. |
| FR-7.3 | Super Admin shall be able to view audit logs for all Admins and all devices. Admin shall be able to view audit logs only for their own actions and assigned devices. |

### 4.8 Alerts & Notifications

| ID | Requirement |
|---|---|
| FR-8.1 | System shall notify an Admin when one of their assigned devices comes online/goes offline (optional, configurable). |
| FR-8.2 | System shall notify Super Admin when a new device is enrolled or a new Admin account is created. |

### 4.9 Reporting

| ID | Requirement |
|---|---|
| FR-9.1 | System shall generate basic usage reports (session count, session duration, device uptime) exportable as CSV/PDF. |

---

## 5. Non-Functional Requirements

| ID | Category | Requirement |
|---|---|---|
| NFR-1 | Performance | Live screen streaming latency target: sub-200ms under normal network conditions. Dashboard pages should load in under 2 seconds. |
| NFR-2 | Scalability | Phase 1 runs as a single Go backend instance with in-memory presence/session state (no Redis yet — SDS §6). This state should sit behind a swappable interface in code so a later move to Redis-backed shared state for horizontal scaling doesn't require a rewrite. |
| NFR-3 | Security | All data in transit encrypted via TLS 1.3; data at rest (recordings, credentials) encrypted with AES-256; passwords hashed with a strong algorithm (bcrypt/Argon2); RBAC enforced server-side on every API call, not just in the UI. |
| NFR-4 | Availability | Target uptime of 99.5%+ for the relay server and portal; agents must auto-reconnect after any outage without manual intervention. |
| NFR-5 | Usability | Admin portal should be usable by non-technical staff without training; clear visual indicators for online/offline/recording states. |
| NFR-6 | Compatibility | Windows 10/11 and Server 2016+ for the Windows agent; Android 8.0+ for the Android agent; portal supported on latest Chrome, Edge, and Firefox. |
| NFR-7 | Maintainability | Modular, documented codebase with CI/CD pipeline; agent and backend versioned independently. |
| NFR-8 | Compliance & Privacy | Employee monitoring must account for applicable local labor/privacy laws (e.g., consent/notice requirements). Data retention period should be configurable. Audit logs must be tamper-evident. |
| NFR-9 | Backup & Recovery | Regular automated database backups; documented recovery point/time objectives for disaster recovery. |
| NFR-10 | Extensibility | Architecture (agent protocol, relay layer, DB schema) should be designed so macOS, Linux, and iOS support can be added later per the original roadmap, without a full rebuild. |
| NFR-11 | Device Footprint | Agents should be lightweight — target under ~50MB RAM usage, minimal CPU overhead when idle, to avoid employee complaints about slowdown. |

---

## 6. Explicitly Out of Scope for Phase 1

Pulled from the original roadmap — these are valid future-phase items, not Phase 1 requirements:

- macOS and Linux agents
- iOS MDM (Apple MDM protocol, DEP/ABM, supervised device management)
- GPS tracking / geofencing
- Mobile policy engine (Wi-Fi/VPN configs, camera/mic/USB restrictions)
- OS + third-party patch management (Windows/macOS)
- Remote PowerShell/CMD shell gateway, Registry Editor, Service Controller
- Script library, automation rule engine, scheduled bulk deployment
- Multi-tenant MSP portal with per-client branding
- PSA integrations (ConnectWise, HaloPSA)
- SSO (SAML/OIDC), Active Directory / Azure AD sync
- SaaS billing (Stripe, per-endpoint subscriptions)
- Advanced reporting/executive dashboards
- App whitelist/blacklist, remote wipe
- Session recording and storage (needs object storage — cut from the confirmed Phase 1 stack)
- TURN relay fallback for NAT traversal (Coturn — see SDS §3.1 on the risk this creates)
- Redis-backed shared presence/session state (single backend instance holds this in memory for Phase 1 — see SDS §6)

Keeping these visibly "parked" (rather than silently dropped) is useful so the client roadmap and
your Phase 1 build stay traceable to each other.

---

## 7. Open Questions

Resolved by the confirmed tech stack: session recording is deferred (Q3 below is answered — not
in Phase 1 at all, not "mandatory vs. admin-triggered"). Still open:

1. Is full remote **control** (not just view) required for Windows in Phase 1? For Android?
2. Continuous live streaming, or periodic screenshot capture, or both (user-selectable)?
3. Any specific country/labor-law compliance requirement your client has flagged (this affects FR-5.5 and NFR-8 heavily)?
4. Expected initial scale (number of employees/devices) — affects VPS sizing, see SDS §7.6.

Plus the four questions raised in `DRS_Phase1_SDS.md` §9 (multi-tenant scope, the React Native
decision, whether to include Coturn disabled-by-default, and VPS provider preference).

---

## 8. Next Step

The System Design Specification (`DRS_Phase1_SDS.md`) is now written, covering architecture,
database schema, and VPS deployment against the confirmed tech stack. Once the open questions in
Section 7 (and SDS §9) are settled, the next artifacts are:

- Full API endpoint list (REST + WebSocket message schema)
- `docker-compose.yml` and Nginx config, ready to deploy
- Database migration files
