# Host provisioning

Everything the production host needs **outside `/srv/dashboard`** is code in
this directory, applied by `provision.sh`. The deploy wrapper ships it with
every deploy:

```powershell
.\scripts\Deploy-Dashboard.ps1                # code deploy; prints a [config] drift report
.\scripts\Deploy-Dashboard.ps1 -Part host     # converge host config only
.\scripts\Deploy-Dashboard.ps1 -Provision     # code deploy + converge host config
```

Do not edit these files on the server. A hand edit shows up as drift in the next
deploy's report, and the next `-Part host` replaces it (keeping a backup).

## What is managed

| Component | Repo | Installed to | Takes effect |
|---|---|---|---|
| `nginx` | `nginx/mcsm-dashboard.conf` | `/etc/nginx/snippets/mcsm-dashboard.conf` | `nginx -t`, then reload |
| | `nginx/mcsm-ws-upgrade.conf` | `/etc/nginx/conf.d/mcsm-ws-upgrade.conf` | |
| `api-env` | `systemd/mcsm-api.service.d/20-provisioned.conf` | `/etc/systemd/system/mcsm-api.service.d/` | API restart |
| `reboot` | `systemd/mcsm-agent.service.d/20-allow-reboot.conf` | `/etc/systemd/system/mcsm-agent.service.d/` | agent restart |
| | `polkit/50-mcsm-reboot.rules` (`.pkla` on polkit 0.105) | `/etc/polkit-1/rules.d/` | immediately |
| `ssh-watchdog` | `ssh-watchdog/*` | `/usr/local/sbin/ssh-watchdog`, `/etc/systemd/system/` | timer enabled |

Plus checks with nothing to install:

- **nginx include** — the site's 443 server block must contain
  `include /etc/nginx/snippets/mcsm-dashboard.conf;`. Provisioning reports a
  missing include but never edits the site file, which also holds TLS settings
  it does not own.
- **Effective settings** — read from the running processes, so a pending
  restart or a `secrets.env` override is reported rather than assumed away.
- **Reboot grant** — logind must answer `yes` to `CanReboot` for the `mcsm` user.
- **Boot** — `mcsm-api`, `mcsm-agent`, nginx, and SSH must be enabled, or the
  dashboard (and remote access) will not come back after a reboot.

Not managed, deliberately: `secrets.env` (secrets never leave the host), the
`mcsm-*.service` units themselves and their pre-existing `10-killmode.conf`
drop-in, and the nginx site file.

## Guarantees

- **Idempotent.** Identical files are left alone; nothing restarts or reloads
  unless something it reads changed.
- **Backed up.** Every replaced file is copied to
  `/var/backups/mcsm-provision/<timestamp>/` first; `history.log` there lists
  each run.
- **nginx is rolled back on failure.** A config `nginx -t` rejects, or one that
  stops the dashboard answering after reload, is replaced by the previous files
  and reloaded again.
- **SSH is never restarted.** The watchdog drop-in only changes how systemd
  reacts the next time sshd exits.
- **Root-only path.** The bundle is uploaded to and run from `/root/mcsm-deploy`,
  never from a directory the `mcsm` user can write.
- **Consistent with the deploy.** `apply` refuses to run if the bundle's
  `APP_BASE_PATH` or nginx location disagree with the web base the deploy built.

## Adding something

1. Put the file here and add one line to `manifest()` in `provision.sh`.
2. If it needs a reload or restart, add it next to the existing ones in
   `apply()`; if its effect can be verified at runtime, add that to `check()`.
3. Cover it in `provision_test.sh` (`bash deploy/provision_test.sh`, also run
   by `make test` and `Deploy-Dashboard.ps1 -Test`).

A new non-secret setting goes in a `systemd/` drop-in. A new secret goes in
`secrets.env` by hand.
