# Deployment

The supported production deployment is three native processes on a single host.
No containers or virtualization are used or required.

## Processes

- `mcsm-api`: Go API on `:8081`. Runs goose migrations on boot, seeds the admin
  user, auto-registers the local agent, starts the scheduler and poller.
- `mcsm-agent`: Go agent on `:8090`. Manages Java server processes and files.
- web: the static React bundle (`apps/web/dist`) served by any web server /
  reverse proxy already on the host, proxying `/api` to the API.

The API auto-registers the agent as `Local Agent` when
`AUTO_REGISTER_LOCAL_AGENT=1`, pointing at `LOCAL_AGENT_FQDN:LOCAL_AGENT_PORT`.

## Build

Go 1.26.11 or newer (with the default `GOTOOLCHAIN=auto`, an older Go downloads
it), Node 22 and pnpm 10.

```bash
cd apps/api   && go build -o mcsm-api   ./cmd/server
cd apps/agent && go build -o mcsm-agent ./cmd/agent
# Server isolation helper (Linux); install it as below, never next to the agent.
cd apps/agent && CGO_ENABLED=0 go build -o mcsm-launch ./cmd/mcsm-launch
cd apps/web   && pnpm install --frozen-lockfile && pnpm build
```

### Web build options (subpath / reverse proxy)

The web build honours two optional environment variables. Both default off, so
local dev (`run.ps1` / `run.sh`) and a root-hosted deployment need neither.

- `VITE_BASE` — base path the app is served from, with leading and trailing
  slash. Set to e.g. `/dashboard/` to host the SPA under a subpath behind a
  reverse proxy. This drives the asset base, the router `basepath`, the PWA
  manifest `scope`/`start_url`, and base-path-aware login redirects.
- `VITE_PWA_SELF_DESTROY=1` — emit a self-destroying service worker that
  unregisters any previously-installed SW and clears its caches. Useful for
  ephemeral/test deployments where stale PWA caches cause confusion.

```bash
# Example: build for hosting under https://host/dashboard
VITE_BASE=/dashboard/ VITE_PWA_SELF_DESTROY=1 pnpm build
```

When hosting under a subpath, the API still serves `/api/v1` at the origin root,
so proxy `/api/` to `:8081` (not `/dashboard/api/`).

## Run

Run `mcsm-api` and `mcsm-agent` under whatever service manager the host uses
(systemd on Linux, a service wrapper or scheduled task on Windows). Set at least
`JWT_SECRET`, `AGENT_TOKEN`, `ADMIN_PASSWORD`, `DATABASE_PATH`, and
`SERVER_ROOT`. Point a reverse proxy at `apps/web/dist` for static files and
forward `/api` (including WebSocket upgrades for console/metrics) to `:8081`.

### systemd (Linux)

Two long-running native processes. Adjust `User` and paths to match your host.

Secrets go in a file only root can read, never in the unit: unit files are
world-readable and `systemctl show` prints their `Environment=` lines to any
user, which on a host with server isolation includes the user every Minecraft
mod runs as. A token readable there undoes the isolation.

```bash
sudo install -d -m 0755 /etc/mcsm
sudo install -m 0600 /dev/null /etc/mcsm/api.env
sudo install -m 0600 /dev/null /etc/mcsm/agent.env
```

```ini
# /etc/mcsm/api.env — long random values; `openssl rand -hex 32` makes one
JWT_SECRET=...
ADMIN_PASSWORD=...
LOCAL_AGENT_TOKEN=...

# /etc/mcsm/agent.env — the same value as LOCAL_AGENT_TOKEN
AGENT_TOKEN=...
```

Outside development mode both processes refuse to start on the placeholder
values this repository uses in its examples (`CHANGE_ME`, `changeme`,
`dev-secret`, `dev-agent-token`).

The panel's own data (the database and its encryption key) and the server
root are separate directories. With server isolation the first is closed to
the server user and the second must be reachable by it, so one cannot sit
inside the other.

```ini
# /etc/systemd/system/mcsm-api.service
[Unit]
Description=ServerManager API
After=network-online.target
Wants=network-online.target

[Service]
User=mcsm
WorkingDirectory=/opt/mcsm
ExecStart=/opt/mcsm/mcsm-api
Environment=API_HOST=127.0.0.1
Environment=API_PORT=8081
Environment=DATABASE_PATH=/var/lib/mcsm/mcsm.db
Environment=SERVER_ROOT=/srv/mcsm/servers
Environment=AUTO_REGISTER_LOCAL_AGENT=1
EnvironmentFile=/etc/mcsm/api.env
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/mcsm-agent.service
[Unit]
Description=ServerManager Agent
After=network-online.target
Wants=network-online.target

[Service]
User=mcsm
WorkingDirectory=/opt/mcsm
ExecStart=/opt/mcsm/mcsm-agent
Environment=AGENT_HOST=127.0.0.1
Environment=AGENT_PORT=8090
Environment=AGENT_SERVER_ROOT=/srv/mcsm/servers
EnvironmentFile=/etc/mcsm/agent.env
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`AGENT_TOKEN` and the API's `LOCAL_AGENT_TOKEN` must be the same value. Then:

```bash
sudo install -d -o mcsm -g mcsm -m 0700 /var/lib/mcsm
sudo install -d -o mcsm -g mcsm -m 0755 /srv/mcsm /srv/mcsm/servers
sudo systemctl daemon-reload
sudo systemctl enable --now mcsm-api mcsm-agent
```

#### Server isolation

To run Minecraft servers as their own user (strongly recommended; see
[security.md](security.md#server-isolation)):

```bash
sudo useradd --system --user-group --no-create-home --home-dir /nonexistent \
    --shell /usr/sbin/nologin mcsm-mc
sudo usermod -aG mcsm-mc mcsm
sudo install -D -o root -g mcsm -m 0750 mcsm-launch /usr/local/libexec/mcsm/mcsm-launch
sudo setcap cap_setuid,cap_setgid+ep /usr/local/libexec/mcsm/mcsm-launch
sudo chmod 0700 /var/lib/mcsm      # the API's data directory, not the server root
```

and give the agent unit:

```ini
[Service]
AmbientCapabilities=CAP_KILL
Environment=AGENT_ISOLATE_SERVERS=1
```

The server user must be able to traverse to `AGENT_SERVER_ROOT` (every
directory above it needs `o+x`) and must not be able to reach the API's data
directory, so keep the server root outside it. On the production host this is
all done by `deploy/provision.sh`. Set `AGENT_ISOLATE_SERVERS=0` to opt out and
run servers as the agent's user.

### nginx (static UI + API/WebSocket proxy)

Serve the built web bundle and proxy `/api` (including WebSocket upgrades) to the
API. This is where TLS is terminated (see below).

```nginx
server {
    listen 80;
    server_name mc.example.com;

    root /opt/mcsm/web/dist;   # contents of apps/web/dist
    index index.html;

    # SPA: serve the app shell for any non-file route.
    location / {
        try_files $uri /index.html;
    }

    # API + console/metrics WebSockets.
    location /api/ {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;   # long-lived console/metrics streams
    }
}
```

`$connection_upgrade` comes from the standard map (put it in `http {}`):

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

### Remote agent (MCP) endpoint

Remote agent connections stay **off** until the API can state its own public
address, because an OAuth token is bound to the exact resource URL it was issued
for and a guessed origin would mint tokens bound to the wrong audience. Set one
of these on the API service:

```ini
# The origin clients reach, no path, https unless it is loopback.
Environment=MCP_PUBLIC_ORIGIN=https://mc.example.com
# APP_ORIGIN is used when MCP_PUBLIC_ORIGIN is unset — production usually
# already sets it.
Environment=APP_ORIGIN=https://mc.example.com
# The path the SPA is served under, so the consent screen URL is right.
# Defaults to /; set it when hosting under a subpath.
Environment=APP_BASE_PATH=/dashboard/
# Optional. Self-registered clients may only call back to loopback, because
# registration is open to the internet and a remote callback is how a crafted
# authorize link turns an operator's approval into someone else's access. Set
# this only if you deliberately run a hosted MCP client that cannot use a
# loopback redirect.
# Environment=MCP_ALLOW_REMOTE_REDIRECTS=true
# Optional diagnostic. An agent access key is never accepted at the MCP
# endpoint; this only makes the 401 say so instead of failing anonymously,
# which is worth turning on while someone is debugging a client that keeps
# presenting the wrong credential.
# Environment=MCP_EXPLAIN_ACCESS_KEY_REJECTION=true
# (The pre-release alias MCP_ALLOW_ACCESS_KEYS is no longer read.)
```

`MCP_PUBLIC_ORIGIN` or `APP_ORIGIN` is required for any deployment that is not
purely local. The fallback that lets local development work unconfigured needs
the request to have both arrived from a loopback address and named a loopback
host, and it refuses any request carrying a forwarding header. A proxied
request therefore never satisfies it — the API answers `503` on the OAuth
endpoints and logs the variable to set.

The agent's `GET /files/content` gained an optional `tail_bytes` query parameter
so the panel can read the end of a large log without transferring the whole
file. It is additive: an agent that predates it ignores the parameter and
returns the file entire, which the panel then refuses for exceeding its response
ceiling — so a stale agent degrades to the previous behaviour (raw log reads
fail on big files) rather than returning something wrong. Upgrade agents with
the API to get log and crash-report reads on busy servers.

With neither set, the surface is unmounted and 404s — except for requests
arriving from loopback, which is what keeps `make dev-mcp` working with no
configuration.

OAuth discovery is **origin-rooted by specification**, so the proxy must forward
the `.well-known` documents in addition to `/api/`. Without this a client can
reach the MCP endpoint but never discover where to authorize, and connecting
fails with an unhelpful 401. On the production host this block, and
`APP_BASE_PATH`, are managed by host provisioning (see
[Host provisioning](#host-provisioning)); other deployments add them by hand:

```nginx
    # OAuth discovery for the MCP endpoint. Origin-rooted, so it cannot live
    # under /api/ — without this block the SPA's index.html answers instead.
    location ~ ^/\.well-known/(oauth-protected-resource|oauth-authorization-server|openid-configuration) {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
```

TLS is not optional here. The client sends a bearer token on every call, and the
origin resolver refuses a non-loopback `http` origin outright.

Verify after deploying:

```bash
# Must return JSON naming your origin, not the SPA shell.
curl -s https://mc.example.com/.well-known/oauth-protected-resource/api/v1/mcp
curl -s https://mc.example.com/.well-known/oauth-authorization-server

# Must be 401 with a WWW-Authenticate naming the metadata document.
curl -s -i -X POST https://mc.example.com/api/v1/mcp | head -n 20
```

Operator guidance for connecting and revoking is in
[operations.md](operations.md#remote-agent-connections-mcp); the security model
is in [security.md](security.md#remote-agent-connections-mcp-over-oauth).

### Host reboot from the dashboard (optional)

Global admins can reboot a node's machine from **Nodes → Reboot host**, which
stops every Minecraft server gracefully and then runs `systemctl reboot`. It is
off until the host grants it, because the agent runs unprivileged and needs the
OS to allow exactly one action. Host provisioning installs both halves — the
polkit grant in `deploy/polkit/` and the `AGENT_ALLOW_REBOOT=1` drop-in — and
its check confirms that logind answers `yes` for the agent's user and that
`mcsm-api`, `mcsm-agent`, nginx, and SSH all start at boot, so the dashboard
comes back after a reboot.

### Host provisioning

Everything the production host needs outside `/srv/dashboard` lives in
`deploy/` and is applied by `deploy/provision.sh` — never edited by hand on the
server. See [`deploy/README.md`](../deploy/README.md) for what is managed.

```powershell
.\scripts\Deploy-Dashboard.ps1 -Part host     # converge host config only
.\scripts\Deploy-Dashboard.ps1 -Provision     # code deploy + converge
```

Provisioning also sets up server isolation — the `mcsm-mc` user, the
`mcsm-launch` helper (built by `Deploy-Dashboard.ps1` into the root-only
bundle), the agent's isolation drop-in, and the API's hardening drop-in — and
reports a deliberate opt-out (`AGENT_ISOLATE_SERVERS=0` in `secrets.env`)
instead of treating it as drift.

Every deploy, with or without `-Provision`, ships the bundle and prints a
`[config]` report comparing the host with the repo. Drift on a code-only deploy
is a warning; a provisioning run that does not converge fails the deploy.
Applying is idempotent: identical files are left alone, replaced files are
backed up under `/var/backups/mcsm-provision/`, nginx changes are tested with
`nginx -t` and rolled back if the test or the post-reload dashboard check
fails, and SSH is never restarted. Secrets stay in
`/srv/dashboard/data/secrets.env`, which provisioning never reads or writes;
anything set there overrides the managed drop-ins.

### Public status pages (optional)

Servers can opt into a public, unauthenticated status page (Options → Public
Status Page). The API renders it itself — a small self-contained HTML page plus
a JSON endpoint — showing only the server name, online/offline, sampled player
count, Minecraft version, and uptime history.

- Page: `GET /status/<slug>` (HTML)
- Data: `GET /api/v1/public/status/<slug>` (JSON, CORS-open)

The JSON route is already covered by the `/api/` proxy above. To expose the
HTML page at the panel's own domain, add:

```nginx
    # Public status pages, rendered by the API.
    location /status/ {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
```

#### Per-server subdomains

To give each page its own subdomain (`<slug>.status.example.com`), set the env
var on the API service:

```
PUBLIC_STATUS_DOMAIN=status.example.com
```

and route the wildcard host to the API — the API resolves the slug from the
`Host` header and serves the page at `/`:

```nginx
server {
    listen 443 ssl;
    server_name *.status.example.com;

    # Wildcard cert required (Let's Encrypt: DNS-01 challenge).
    ssl_certificate     /etc/letsencrypt/live/status.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/status.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

DNS needs a wildcard record (`*.status.example.com` → the host). Slugs are
validated to DNS-label rules and common infrastructure labels (`www`, `api`,
`mail`, …) are reserved, so a panel user can't claim a sensitive hostname.

Notes:

- A page is only served while its server has `public_status` enabled; disabled
  or unknown slugs return 404 (indistinguishable from each other).
- Responses are cached in-process for 15 s per slug and carry
  `Cache-Control: public, max-age=30`, so public traffic doesn't multiply into
  database load. An nginx `proxy_cache` in front is optional hardening.

## Hardening

Before exposing the panel to the public internet:

- **Keep the agent off the network.** The agent launches processes and reads and
  writes the server filesystem, guarded only by `AGENT_TOKEN`. Keep
  `AGENT_HOST=127.0.0.1` and firewall `:8090` to loopback. The agent now refuses
  to bind a non-loopback address without TLS unless you set
  `AGENT_ALLOW_INSECURE=1`; if you must run it across hosts, set
  `AGENT_TLS_CERT`/`AGENT_TLS_KEY` instead and treat the token as a TLS-only
  secret. Never port-forward `:8081` or `:8090` directly — only the reverse
  proxy should face the internet.

- **Set `APP_ORIGIN`** on the API to your panel's public URL (e.g.
  `https://mc.example.com`). This pins the console/metrics WebSocket Origin
  allowlist. Leave it unset only for local split-origin development.

- **Set `TRUSTED_PROXIES`** to the reverse proxy's address(es) (IPs or CIDRs,
  comma-separated) when the proxy is on a different host than the API. Forwarded
  headers (`X-Forwarded-For`, `X-Forwarded-Proto`, …) are honored only from these
  peers; everything else has them stripped, so client IPs in the audit log and
  login throttle can't be spoofed. When unset it defaults to loopback only,
  which is correct for a same-host proxy.

- **Set a proxy secret** when anything else runs on the API's host; see
  [Proxy secret](#proxy-secret) below. An address says which machine a
  request came from, not which program.

- **Login throttling, rate limiting, and password policy are automatic.**
  Repeated failed logins lock the source IP (and, briefly, the targeted account);
  authenticated traffic is per-caller rate limited; passwords must be ≥10 chars.
  No configuration needed.

- **Optional MFA + session management** are available per-user under Settings →
  Security (TOTP with recovery codes; review/revoke active sessions). Set
  `APP_NAME` to control the label shown in authenticator apps.

- **The bootstrap admin password is written to a file, not the logs.** On first
  boot with no `ADMIN_PASSWORD`, the generated password is written to
  `.mcsm-initial-admin-password` (mode 0600) beside the database. Log in, change
  it, then delete the file.

- **Stored agent tokens, two-factor secrets and integration secrets are
  encrypted at rest** using the app encryption key (`APP_ENCRYPTION_KEY`, else
  the persisted key file beside the database). Each value is bound to the row
  it belongs to, so it cannot be copied onto another row. Set
  `APP_ENCRYPTION_KEY` explicitly in production so the key never lives on disk,
  and back it up with the database: a database restored without its key locks
  out every two-factor account and every node.

### Proxy secret

On a single host the reverse proxy reaches the API over loopback, and so can
every other process on the machine. Minecraft servers are such processes, and
their mods are code you did not write. Without a secret, one of them can
connect to `127.0.0.1:8081` directly, say it is the proxy, and claim any client
address: each sign-in guess then gets a fresh per-address lockout, and the
audit log records addresses that were never there. (The per-account lockout
still applies, and none of this gets past authentication.)

`TRUSTED_PROXY_SECRET` closes that. When it is set, the API believes forwarding
headers only from a trusted peer that also sends the same value in
`X-MCSM-Proxy-Secret`; everything else is treated as a direct client.

1. Pick a value: `openssl rand -hex 32`.
2. Give it to nginx in a file only root can read. nginx reads its
   configuration as root, so its workers need no access:

   ```bash
   sudo install -m 0600 /dev/null /etc/nginx/mcsm-proxy-secret.conf
   echo 'proxy_set_header X-MCSM-Proxy-Secret "<value>";' | sudo tee /etc/nginx/mcsm-proxy-secret.conf >/dev/null
   ```

   Every `location` that proxies to the API needs
   `include /etc/nginx/mcsm-proxy-secret*.conf;` beside its other
   `proxy_set_header` lines. The managed snippet
   (`deploy/nginx/mcsm-dashboard.conf`) already has it; add it to any location
   of your own, such as a status-page subdomain's.
3. `sudo nginx -t && sudo systemctl reload nginx`. Nothing changes yet: the API
   ignores the header until it has a secret of its own.
4. Add `TRUSTED_PROXY_SECRET=<value>` to the API's secrets file (`secrets.env`
   on the hosted instance) and restart the API.

Do them in that order. With the API's half set and nginx's missing, the panel
still works but sees every visitor as the host itself, so they share one
rate limit and one lockout. Host provisioning reports a half-finished setup,
and notes when none is set.

### Reverse-proxy security headers (recommended)

The API sets a strict CSP and HSTS on its own JSON responses, but the SPA shell
is served by the proxy — set its headers there. A starting point for the nginx
`server` block:

```nginx
add_header X-Frame-Options DENY always;
add_header X-Content-Type-Options nosniff always;
add_header Referrer-Policy strict-origin-when-cross-origin always;
add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
add_header Content-Security-Policy "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' wss:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'" always;
```

On the hosted instance the managed nginx snippet sets the framing, sniffing,
referrer and HSTS headers for the dashboard itself, so provisioning reports them
missing like any other drift; the snippet above is for deployments that write
their own server block.

Adjust `connect-src`/`img-src` if you front the API or mod-thumbnail CDNs from
other origins. The built `index.html` also carries an equivalent policy in a
`<meta>` tag (injected at build time — see `buildCSPPlugin` in
`apps/web/vite.config.ts`), so the bundle is protected even behind a proxy
without these headers. The proxy header remains worth setting: `<meta>` CSP
cannot express `frame-ancestors`, and a header covers every response, not just
the document.

## State

- SQLite database at `DATABASE_PATH`.
- Minecraft server directories and local backup archives under `SERVER_ROOT`.

Back up both together so metadata and files stay in sync (see README).

The API also keeps its own copies of the database: one a day into `db-backups`
beside it, the newest seven kept (`DB_BACKUP_DIR`, `DB_BACKUP_INTERVAL_HOURS`,
`DB_BACKUP_KEEP`). They are taken with `VACUUM INTO` while the panel runs, so
each is a consistent database on its own. They protect against a damaged or
mistakenly changed database, not a lost disk: point `DB_BACKUP_DIR` at other
storage or sync that directory off the host. The encryption key is not copied
with them, so the directory can go somewhere less trusted than the host; a
restore needs both.

## Upgrades

1. Read release and migration notes.
2. Back up the database and `SERVER_ROOT`.
3. Build the new binaries and web bundle.
4. Restart the services; goose migrations run on API boot.
5. Confirm `/api/v1/health` returns `{"status":"ok"}` and the node is online.

Going back to an older release means going back to its database too. On first
boot a release can migrate the schema and re-encrypt stored secrets in a form
the older API cannot read, and that API then refuses to start. Restore the
database copy from step 2 (with the encryption key it was written with) along
with the older binaries.

`scripts/Deploy-Dashboard.ps1` does this for the hosted instance: every api
deploy copies the database, with the API stopped, to `/srv/dashboard/db-prev`
(root-only, the last three kept), and `-Rollback` restores that copy with the
previous binaries, setting the replaced database aside in the same directory.
`-Rollback -KeepDatabase` rolls back code only.

"Previous" means the last build that passed the deploy's health checks. A
deploy that fails them is marked unverified, and deploying over it again
leaves the rollback target, and the database copy that belongs to it, where
they were, so a failed fix for a failed deploy still rolls back to the build
that worked. The health checks wait up to a minute for a restarted API, which
answers only after its migrations have run.

Deployed binaries and the web bundle are installed root-owned: the services
read and run them but cannot rewrite them.

## TLS

Terminate TLS at the reverse proxy in front of the web bundle / API. With the
nginx example above, Let's Encrypt is one command:

```bash
sudo certbot --nginx -d mc.example.com
```

certbot rewrites the `server` block to listen on `:443` with the issued
certificate and installs an auto-renew timer. Because the frontend uses relative
`/api` URLs and protocol-aware `wss://`, no app config changes once the site is
served over HTTPS — secure WebSockets follow automatically.

If the agent is reachable outside the host, set `AGENT_TLS_CERT` and
`AGENT_TLS_KEY`; plain public HTTP agents are not recommended. On a single-host
deployment the agent stays on loopback (`AGENT_HOST=127.0.0.1`) and needs no TLS
of its own.

## Settings reference

Settings added for the hardened backend (the rest are described above):

| Setting | Process | Default | Meaning |
|---|---|---|---|
| `AGENT_ISOLATE_SERVERS` | agent | auto | `1` requires server isolation, `0` turns it off, unset isolates when the host allows |
| `AGENT_SERVER_USER` | agent | `mcsm-mc` | The user isolated servers run as (the launcher reads `/etc/mcsm/server-user`) |
| `AGENT_LAUNCHER` | agent | `/usr/local/libexec/mcsm/mcsm-launch` | The isolation helper |
| `AGENT_CHILD_ENV_PASSTHROUGH` | agent | — | Extra variable names passed to server processes (never `AGENT_*`, `MCSM_*` or secrets) |
| `MCSM_HELPER_INDEX_PUBKEY` | agent | — | Key(s) the remote helper-mod index must be signed with; without one the index is ignored and the embedded helper build is used |
| `MCSM_HELPER_INDEX_INSECURE` | agent | — | `1` accepts an unsigned index (development only) |
| `AUDIT_RETENTION_DAYS` | API | `365` | How long audit rows are kept; `0` keeps them forever |
| `APP_ENCRYPTION_KEY` / `APP_ENCRYPTION_KEY_FILE` | API | `.mcsm-secret-key` beside the database | Encrypts stored secrets; back it up with the database |
| `TRUSTED_PROXIES` | API | loopback | Peers whose `X-Real-IP` / `X-Forwarded-For` are believed |
| `TRUSTED_PROXY_SECRET` | API | — | When set, a trusted peer must also send this value in `X-MCSM-Proxy-Secret` to be believed |
| `DB_BACKUP_DIR` | API | `db-backups` beside the database | Where the API keeps copies of its own database |
| `DB_BACKUP_INTERVAL_HOURS` | API | `24` | How often a copy is taken; `0` turns them off |
| `DB_BACKUP_KEEP` | API | `7` | How many copies are kept |
