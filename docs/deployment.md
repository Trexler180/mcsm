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

```bash
cd apps/api   && go build -o mcsm-api   ./cmd/server
cd apps/agent && go build -o mcsm-agent ./cmd/agent
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

Two long-running native processes. Adjust `User`, paths, and secrets to match
your host. Use a real secrets mechanism (`EnvironmentFile=` with `chmod 600`, or
your secrets manager) rather than inlining production secrets.

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
Environment=SERVER_ROOT=/var/lib/mcsm/servers
Environment=JWT_SECRET=CHANGE_ME
Environment=ADMIN_PASSWORD=CHANGE_ME
Environment=AUTO_REGISTER_LOCAL_AGENT=1
Environment=LOCAL_AGENT_TOKEN=CHANGE_ME
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
Environment=AGENT_TOKEN=CHANGE_ME
Environment=AGENT_SERVER_ROOT=/var/lib/mcsm/servers
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`AGENT_TOKEN` and the API's `LOCAL_AGENT_TOKEN` must be the same value. Then:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now mcsm-api mcsm-agent
```

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

- **Stored agent tokens are encrypted at rest** using the app encryption key
  (`APP_ENCRYPTION_KEY`, else the persisted key file beside the database). Set
  `APP_ENCRYPTION_KEY` explicitly in production so the key never lives on disk.

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

Adjust `connect-src`/`img-src` if you front the API or mod-thumbnail CDNs from
other origins. Do not put the full `script-src 'self'` policy in the bundled
`index.html` — it would break the Vite dev server; it belongs at the proxy.

## State

- SQLite database at `DATABASE_PATH`.
- Minecraft server directories and local backup archives under `SERVER_ROOT`.

Back up both together so metadata and files stay in sync (see README).

## Upgrades

1. Read release and migration notes.
2. Back up the database and `SERVER_ROOT`.
3. Build the new binaries and web bundle.
4. Restart the services; goose migrations run on API boot.
5. Confirm `/api/v1/health` returns `{"status":"ok"}` and the node is online.

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
