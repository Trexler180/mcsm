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
