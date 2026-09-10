# DRS — Deployment Runbook

**Status:** the commands that actually deploy this repo to a single VPS, in order.
For what the stack *is*, see [`SYSTEM_DESIGN.md`](SYSTEM_DESIGN.md) §8. This document is the
procedure; that one is the design.

Worked example throughout is the current VPS:

| | |
|---|---|
| Public IP | `2.25.114.216` (bound directly to `eth0` — no NAT, nothing to forward) |
| Hostname | `2.25.114.216.nip.io` |
| Portal | `https://2.25.114.216.nip.io` |
| Code lives at | `/opt/drs` |

Substitute your own IP if it differs. Most commands derive it from `curl -4 -s ifconfig.me` so
they stay correct without editing.

**The `-4` is not optional.** This VPS is dual-stacked (`2a02:4780:95:3d14::1`), and a plain
`curl ifconfig.me` answers over IPv6 and returns that address instead. Everything downstream
then breaks in a way that is not obvious: a hostname cannot contain colons, so
`2a02:…::1.nip.io` never resolves, and `TURN_PUBLIC_IP` would be set to an address the relay
cannot advertise — which fails *only for remote peers*.

> **`deploy/scripts/setup_vps.sh` is not used by this runbook.** It opens only 22/80/443 —
> omitting the relay ports, so every cross-NAT session fails; it runs `docker compose up`
> without `--profile turn`, so the relay never starts; and it starts the stack *before*
> certificates exist, which nginx cannot survive. The steps below do those three things in
> the right order. Fix the script or delete it; do not run it as-is.

---

## 1. Why so little needs changing

The application is already origin-agnostic. Worth knowing, because it means there is no
per-deployment rebuild and no hostname baked into a config file:

- `frontend/src/api/client.ts` sets `API_BASE = '/api'` — relative, so the portal works under
  any hostname it happens to be served from.
- `handlers.go`'s `agentWSURL` builds the agent's socket URL from the request's `Host` and
  `X-Forwarded-Proto`, and `deploy/nginx/conf.d/drs.conf` sets that header. Agents are handed
  `wss://<whatever-host-they-enrolled-against>/ws/agent` automatically.
- Migrations are embedded via a `*.up.sql` glob and applied at boot, so a new migration file
  needs no deployment step of its own.
- nginx already forwards `Sec-WebSocket-Protocol` (a browser cannot set an `Authorization`
  header on a WebSocket, so the viewer's JWT travels there) and falls back to `/index.html`
  for the SPA's client-side routes.

So the whole deployment is: secrets, a hostname, a certificate, and the firewall.

---

## 2. Hostname and TLS: why `nip.io`

Let's Encrypt will not issue a certificate for a bare IP, and a self-signed certificate is a
hard blocker rather than a warning you can click through: `enroll.go` uses a default
`http.Client` and `conn.go` calls `websocket.Dial` with nil options, so **both verify TLS**.
The browser would let you proceed; the agents would not connect at all.

`nip.io` resolves `<ip>.nip.io` to that IP. It is a real DNS name, so Let's Encrypt issues for
it normally, at no cost and with nothing to register.

Its three limitations, all of which argue for a real domain before this carries anything you
care about:

1. **Shared issuance quota.** Let's Encrypt allows 50 certificates per registered domain per
   week. Whether each `<ip>.nip.io` gets its own quota depends on the service's Public Suffix
   List status. If Step 6 fails with `too many certificates already issued`, switch
   `.nip.io` → `.sslip.io` (an equivalent service) and retry.
2. **DNS filtering.** Wildcard-DNS services are used in DNS-rebinding attacks, so some
   corporate resolvers and DNS filters block them. That bites precisely where this platform is
   aimed — agents on networks you do not control — and presents as one device that will not
   connect while every other device works.
3. **The hostname is permanent per agent.** See §9.

A `.xyz`/`.top` domain is $1–3/year, needs one A record pointing at the VPS, and removes all
three. Nothing else in the procedure changes: set `DRS_HOST` to it in Step 5.

---

## 3. Step 1 — Commit the working tree (on Windows)

Not optional. `frontend/src/routes/` and `backend/migrations/000004_team_membership.*` are
untracked; without them the frontend build fails on `App.tsx`'s `./routes/AppLayout` import and
the team-membership schema is silently absent.

```powershell
cd c:\Wadeed\repos\DRS-personal
git add -A
git commit -m "<commit message>"
git push
```

No git remote? Skip the push and use the `scp` variant in Step 4.

---

## 4. Step 2 — Connect and confirm the address

```powershell
ssh -o ServerAliveInterval=30 root@2.25.114.216
```

Then work inside tmux, and reattach with `tmux attach -t drs` after any disconnect:

```bash
apt-get install -y tmux
tmux new -s drs
```

Both of these are here because idle SSH sessions to this host get reset, and Step 7 runs a
multi-minute build in the foreground — a drop part-way through kills it and leaves half-built
images behind.

On the VPS:

```bash
curl -4 -s ifconfig.me; echo
ip -4 addr show scope global | grep inet
```

Expected, and what this VPS reports:

```
2.25.114.216
inet 2.25.114.216/24 brd 2.25.114.255 scope global eth0
```

Both lines must show the same IPv4 address. Drop the `-4` and `curl` answers over IPv6 on this
host, returning `2a02:4780:95:3d14::1` — see the note at the top of this document.

The public IP appearing **directly on the interface** is the thing to confirm. It means the
host owns the address, so inbound 443 and the relay ports arrive unaided. Had `ifconfig.me`
disagreed with `ip addr`, the box would be behind NAT: the relay's advertised address and the
inbound port range would both need attention, and a wrong relay address fails *only for remote
peers* — indistinguishable from a firewall problem when tested from the VPS itself.

---

## 5. Step 3 — Packages, swap, firewall

```bash
apt-get update && apt-get install -y ca-certificates curl gnupg ufw gzip git
curl -fsSL https://get.docker.com | sh
docker compose version
```

```bash
fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab
free -h
```

Swap is here because the nginx image builds the SPA in-place (`npm ci` + `vite build`), which
is the peak memory moment of the whole deployment and gets OOM-killed on a 1–2 GB instance.

```bash
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 3478/tcp
ufw allow 3478/udp
ufw allow 49160:49200/udp
ufw --force enable
ufw status numbered
```

| Ports | For |
|---|---|
| 22/tcp | SSH. Allow it *before* `ufw enable` or you lock yourself out. |
| 80/tcp | ACME http-01 challenge, and the redirect to 443. Nothing else is served there. |
| 443/tcp | Portal, API, all three WebSocket routes. |
| 3478 tcp+udp | Relay control channel. |
| 49160-49200/udp | Relay media. One port per allocation, so this range is also the ceiling on concurrent relayed sessions (~40). Widen it here and in `.env` together. |

---

## 6. Step 4 — Get the code onto the box

```bash
git clone YOUR_REPO_URL /opt/drs
```

Or, from PowerShell on your machine if there is no remote:

```powershell
ssh root@2.25.114.216 "mkdir -p /opt/drs"
scp -r backend frontend agents deploy "DRS docs" CLAUDE.md root@2.25.114.216:/opt/drs/
```

`/opt/drs` is not arbitrary — `deploy/scripts/backup_db.sh` is wired into cron at that path in
Step 8.

Verify the checkout before going on:

```bash
ls /opt/drs/frontend/src/routes/
ls /opt/drs/backend/migrations/*.up.sql
```

`routes/` must hold `AppLayout.tsx`, `RequireAuth.tsx` and `RequireSuperAdmin.tsx`, and
`000004_team_membership.up.sql` must be present. Check it here rather than later: a clone
missing them builds happily for several minutes before failing on an unresolved import, and a
missing migration does not fail at all — it just leaves the team tables absent.

---

## 7. Step 5 — Resolve the hostname and write `.env`

```bash
cd /opt/drs/deploy

PUBLIC_IP=$(curl -4 -s ifconfig.me)
DRS_HOST="${PUBLIC_IP}.nip.io"

echo "portal will be: https://${DRS_HOST}"
getent hosts "$DRS_HOST"
```

Both lines must look right before going on. Expected here:

```
portal will be: https://2.25.114.216.nip.io
2.25.114.216    2.25.114.216.nip.io
```

`getent` printing **nothing** is the failure this guard exists to catch, and there are two
causes. If the echoed host contains colons, `curl` answered over IPv6 — add the `-4` and
re-run. If it is a correct dotted IPv4 and still does not resolve, nip.io is unreachable from
this host: use `DRS_HOST="${PUBLIC_IP}.sslip.io"` and carry that through the rest of the steps.

```bash
cat > .env <<EOF
DB_PASSWORD=$(openssl rand -hex 32)
JWT_SECRET=$(openssl rand -hex 32)
CORS_ORIGINS=https://${DRS_HOST}
PUBLIC_BASE_URL=https://${DRS_HOST}
ADMIN_EMAIL=admin@drs.local
ADMIN_PASSWORD=$(openssl rand -base64 24 | tr -d '=+/')

HEARTBEAT_SECONDS=10
STUN_URLS=stun:stun.l.google.com:19302
SESSION_FPS=24
SESSION_MAX_WIDTH=1280

TURN_PUBLIC_IP=${PUBLIC_IP}
TURN_SHARED_SECRET=$(openssl rand -hex 32)
TURN_PORT=3478
TURN_REALM=drs
TURN_MIN_PORT=49160
TURN_MAX_PORT=49200
FORCE_TURN_RELAY=false
EOF

chmod 600 .env
grep -E 'ADMIN_EMAIL|ADMIN_PASSWORD|CORS_ORIGINS|TURN_PUBLIC_IP' .env
```

**Record the admin password from that last line now.** `config.go` seeds the bootstrap super
admin only when no super admin exists, so once Postgres is initialised the password is not
recoverable — resetting it means dropping the `pgdata` volume.

The values that matter, and why:

| Variable | Why it is what it is |
|---|---|
| `CORS_ORIGINS` | Exact-match allowlist, not a wildcard: combined with credentials, a reflected origin would give every site on the internet authenticated API access. It must equal the origin you actually type, which is why it is generated from the same `DRS_HOST` as the certificate. |
| `PUBLIC_BASE_URL` | The origin baked into a zero-touch agent's downloaded `.exe` as its server address. Left unset, the backend derives it from request headers, which is right behind nginx but trusts `X-Forwarded-Host`; pinning it to the same `DRS_HOST` makes the baked URL deterministic and closes that trust. A wrong value produces agents that can never connect, so it is generated from `DRS_HOST` like the rest. |
| `JWT_SECRET` | `openssl rand -hex 32` gives 64 characters; the floor is 32, below which HMAC-SHA256 carries less than 256 bits. The server refuses to start otherwise. |
| `TURN_PUBLIC_IP` | Must be a literal address, not a hostname — it is what the relay puts in the candidates it hands out. |
| `FORCE_TURN_RELAY=false` | STUN attempts a direct path first and the relay stays a fallback. Forcing relay costs ~7–8 Mbps of VPS bandwidth per concurrent session for no gain when a direct path exists. Set it `true` only if direct connections prove unreliable in practice. |
| `ADMIN_PASSWORD` | `tr -d '=+/'` strips characters that complicate `.env` parsing and shell quoting. 24 random bytes leaves ample entropy after the strip. |

---

## 8. Step 6 — Certificate

Use a real address you read — Let's Encrypt sends expiry notices there.

```bash
chmod +x scripts/*.sh
./scripts/init_certs.sh "$(curl -4 -s ifconfig.me).nip.io" you@youremail.com
```

Look for `Successfully received certificate`.

The script resolves a circular dependency: nginx will not start while a `server` block
references a certificate file that does not exist, but certbot's http-01 challenge needs nginx
already serving. So a self-signed placeholder goes in, nginx starts, certbot replaces it, nginx
reloads.

| Failure | Cause and fix |
|---|---|
| `too many certificates already issued` | The shared-quota case from §2. Re-run Step 5 with `.sslip.io`, then this step. |
| Challenge times out / connection refused | Port 80 unreachable from outside. Check `ufw status`. |
| `NXDOMAIN` / cannot resolve | The `getent` check in Step 5 was skipped or failed. |

---

## 9. Step 7 — Start the stack

```bash
docker compose --profile turn up -d --build
docker compose ps
```

**`--profile turn` is required.** The relay is behind a compose profile, so without the flag
its container never starts, and every session lacking a direct peer-to-peer path fails with
nothing informative in any log.

First build takes several minutes: a Go compile, then `npm ci` and `vite build`.

```bash
docker compose logs -f backend
```

Wait for `Listening on http://0.0.0.0:8080`, then Ctrl-C. The embedded migrations run during
boot, so `000004_team_membership` should scroll past on a fresh database.

Five containers should be `Up`: `drs_nginx`, `drs_backend`, `drs_postgres`, `drs_certbot`,
`drs_turn`.

---

## 10. Step 8 — Nightly backups

```bash
(crontab -l 2>/dev/null | grep -v backup_db.sh; \
 echo "0 2 * * * /opt/drs/deploy/scripts/backup_db.sh >> /var/log/drs_backup.log 2>&1") | crontab -
crontab -l
```

`pg_dump | gzip` into `/var/backups/drs`, pruned at 14 days. The `grep -v` makes re-running
this idempotent rather than stacking duplicate cron lines.

Certificate renewal needs no cron: the `certbot` container loops `certbot renew` and nginx
reloads every 12 hours to pick up a new certificate. That lives in compose deliberately, so
renewal cannot be forgotten when the stack moves to another machine.

---

## 11. Step 9 — Verify

```bash
DRS_HOST="$(curl -4 -s ifconfig.me).nip.io"

curl -s "https://${DRS_HOST}/health"; echo
curl -sI "https://${DRS_HOST}/" | head -3
docker compose logs turn | tail -20
ss -ulnp | grep 3478
docker compose ps
```

| Check | Pass |
|---|---|
| `/health` | JSON. A 503 means the backend cannot reach Postgres. |
| `/` | `HTTP/2 200`, no certificate error. |
| `turn` logs | Relay listening on 3478. |
| `ss` | A process bound to `:3478` on the host — `network_mode: host` means it appears in the host's own socket table, not behind a Docker proxy. |

Then open `https://2.25.114.216.nip.io` in a browser — no certificate warning — and sign in
with `admin@drs.local` and the Step 5 password.

---

## 12. Step 10 — Publish the agent, then enroll a device

Enrollment needs two things on the endpoint: the **agent binary** and a **token**. The invite
link carries both once the binary is published, so publish it first — otherwise the link
delivers a code for software the recipient has no way to obtain, and you are back to sending
the exe by hand.

### Build and upload the agent

The binary is built on Windows and uploaded. It cannot be built in the Linux image: the agent
links libvpx through CGO and uses Fyne.

```powershell
cd c:\Wadeed\repos\DRS-personal\agents\windows
.\build.ps1
scp .\build\drs-agent.exe root@2.25.114.216:/opt/drs/deploy/downloads/drs-agent.exe
```

The filename matters — `frontend/src/hooks/useAgentDownload.ts` probes for exactly
`drs-agent.exe` (and `drs-agent.apk` for Android). Confirm it is served:

```bash
curl -sI "https://$(curl -4 -s ifconfig.me).nip.io/downloads/drs-agent.exe" | head -3
```

`200` with `Content-Disposition: attachment` means done. `404` means the filename is wrong or
the file landed elsewhere. No reload is needed — `deploy/downloads/` is a bind mount, read per
request, so a new upload is live immediately.

### Enroll

In the portal: **Devices → generate enrollment token**, then send the invite link. The
recipient opens it and gets the download and their code on one page. The modal tells you which
mode you are in: *"the invite link offers the Windows agent for download"* if the binary is
published, or an amber warning that the link carries the code only if it is not.

On the endpoint, paste the link into the agent GUI. The equivalent from the command line:

```powershell
.\drs-agent.exe enroll -server https://2.25.114.216.nip.io -token DRS-XXXXXX
```

The agent is unsigned, so SmartScreen warns on first run — **More info → Run anyway**. Code
signing is the only real fix (~$100+/yr for a certificate).

The device appears online within one heartbeat (~10 s). Start a session and read the viewer
badge: **"Direct (P2P)"** means STUN found a direct path, **"Relayed (TURN)"** means the
fallback engaged. Both are successes.

**The hostname is written into the agent, permanently.** `config.go` persists `serverUrl` and
`wsUrl` into `%AppData%\drs\agent.json` at enrollment, and `conn.go` dials the stored value
forever after — it is never re-derived. Consequences:

- Move from `nip.io` to a real domain and **every enrolled device must be re-enrolled.**
- Same if the VPS's IP ever changes, since the `nip.io` name embeds it.
- A nip.io outage takes every agent offline until it returns.

So settle the hostname before enrolling anything you would not want to re-enroll.

---

## 13. Does this reach devices over the internet?

Yes. Per leg:

**Agent → server.** The agent dials `wss://<host>/ws/agent`, an ordinary outbound 443
connection. Outbound only: the device needs no public address, no inbound port, no forwarding.
Home NAT, corporate firewalls and CGNAT are all fine.

**Browser → server.** HTTPS and WSS on 443 from anywhere, subject to the exact-match
`CORS_ORIGINS`.

**Media.** Both peers receive an identical ICE list from the server — negotiating against
different candidate sets fails in ways that are nearly undiagnosable from either end. STUN
tries for a direct path; where there is none (symmetric NAT, CGNAT, restrictive corporate
egress) media falls back to the relay on the VPS. That fallback is what 3478 and
49160-49200/udp exist for.

Different cities, different ISPs, mobile tethering: all work. Two standing limits — a relayed
session costs roughly 7–8 Mbps of VPS bandwidth, and the port range caps concurrent relayed
sessions at about 40.

---

## 14. Operations

**Redeploy after a code change**

```bash
cd /opt/drs && git pull && cd deploy && docker compose --profile turn up -d --build
```

**Logs**

```bash
cd /opt/drs/deploy
docker compose logs -f backend
docker compose logs -f turn
docker compose logs --tail 100 nginx
```

**Restart one service**

```bash
docker compose restart backend
```

**Restore a backup**

```bash
gunzip -c /var/backups/drs/drs_db_YYYYMMDD_HHMMSS.sql.gz | \
  docker exec -i drs_postgres psql -U postgres -d drs_db
```

**Change the hostname** (new domain, or new IP): edit `CORS_ORIGINS` in `.env`, re-run
`./scripts/init_certs.sh <new-host> <email>`, then
`docker compose --profile turn up -d --build`. Every already-enrolled agent needs re-enrolling
— see §12.

---

## 15. Troubleshooting

| Symptom | Cause |
|---|---|
| Session hangs, viewer badge never appears | In order of likelihood: `49160-49200/udp` not open (`ufw status`); `TURN_PUBLIC_IP` not matching `curl -4 -s ifconfig.me`, or holding an IPv6 address; relay container not running (`docker compose ps \| grep turn` — usually a missing `--profile turn`). A wrong relay address passes every test runnable from the VPS itself. |
| Portal loads, API calls all fail | `CORS_ORIGINS` does not exactly match the origin in the address bar. `https://` vs `http://` and a stray trailing slash both count as mismatches. |
| Agent sockets connect, browser sockets rejected as unauthenticated | `Sec-WebSocket-Protocol` not reaching the backend. nginx does not forward it by default; `drs.conf` does. Suspect an edited nginx config. |
| Device shows at the proxy's IP | `X-Forwarded-For` / `X-Real-IP` not being set by nginx. |
| Backend exits at boot | Config validation. `JWT_SECRET` missing or under 32 characters, `DB_PASSWORD` empty, or `FORCE_TURN_RELAY=true` without `TURN_PUBLIC_IP` and `TURN_SHARED_SECRET`. Failing to start is deliberate — the alternative is a deployment with publicly-known credentials, or one where every session fails silently. |
| nginx restart-loops | Missing `/etc/letsencrypt/live/drs/fullchain.pem`. Run `./scripts/init_certs.sh` (Step 6). |
| Frontend build killed mid-`vite build` | No swap. Step 3. |
| `/health` returns 503 | Backend is up, Postgres is not. `docker compose logs postgres`. |
