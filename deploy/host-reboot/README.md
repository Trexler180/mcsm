# Host reboot from the dashboard

Lets a global admin reboot a node's machine from **Nodes → ⏻ Reboot host**.
Useful when the host is reachable over HTTPS but SSH is not.

What happens when someone clicks it:

1. The API requires a global admin on an interactive sign-in (access keys and
   MCP grants are refused), plus their current password and, when enrolled, a
   TOTP code. Every attempt, refused or not, is written to the audit log
   (`node.reboot`, `node.reboot.denied`, `node.reboot.failed`).
2. The agent refuses unless `AGENT_ALLOW_REBOOT=1` is set, and asks logind
   whether it may reboot **before** touching anything, so a refused reboot
   never leaves the host up with its servers down.
3. The agent stops every Minecraft server gracefully (up to 60 s each), then
   runs `systemctl reboot`.

The agent runs unprivileged as `mcsm`, so the OS has to allow it. This
directory contains that grant: a polkit rule for exactly the reboot action and
that user — not power-off, not suspend, not overriding inhibitor locks.

## Install (once, as root, from this directory)

```sh
sudo sh ./install.sh
sudo systemctl restart mcsm-agent
```

The installer picks the polkit format for the installed version, adds a
`mcsm-agent` drop-in with `AGENT_ALLOW_REBOOT=1`, checks that logind answers
`yes` for the `mcsm` user, and lists whether `mcsm-api`, `mcsm-agent`, and
nginx are enabled at boot. All three must say `enabled` or the dashboard will
not come back after a reboot. Restarting the agent leaves Minecraft servers
running unless `AGENT_STOP_SERVERS_ON_EXIT=1` is set.

## Verify

```sh
runuser -u mcsm -- busctl call org.freedesktop.login1 /org/freedesktop/login1 \
    org.freedesktop.login1.Manager CanReboot          # s "yes"
systemctl show mcsm-agent -p Environment | grep AGENT_ALLOW_REBOOT=1
journalctl -u mcsm-agent | grep AGENT_ALLOW_REBOOT    # logged at agent start
```

If the dashboard says the host "does not allow this agent to reboot it", the
first command is not answering `yes`.

## Remove

```sh
rm -f /etc/polkit-1/rules.d/50-mcsm-reboot.rules \
      /etc/polkit-1/localauthority/50-local.d/50-mcsm-reboot.pkla \
      /etc/systemd/system/mcsm-agent.service.d/20-allow-reboot.conf
systemctl daemon-reload && systemctl restart mcsm-agent
```
