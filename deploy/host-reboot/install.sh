#!/bin/sh
# Enable "Reboot host" from the ServerManager dashboard on this machine.
#
# 1. Grants the agent's user (mcsm) the polkit reboot action, and nothing else.
# 2. Opts the agent in with AGENT_ALLOW_REBOOT=1 via a systemd drop-in.
# 3. Verifies that logind now answers "yes" for that user.
#
# It does not restart the agent; do that yourself afterwards (see the end).
# Run as root from this directory:  sudo sh ./install.sh

set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

AGENT_USER=mcsm
AGENT_UNIT=mcsm-agent.service

if [ "$(id -u)" -ne 0 ]; then
    printf '%s\n' "install.sh must be run as root" >&2
    exit 1
fi
if ! id "$AGENT_USER" >/dev/null 2>&1; then
    printf '%s\n' "user $AGENT_USER does not exist; is the agent installed?" >&2
    exit 1
fi

source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

# polkit 0.105 reads .pkla files; newer versions read JavaScript .rules.
polkit_version=$(pkaction --version 2>/dev/null | awk '{print $NF}')
case "$polkit_version" in
    0.105*|0.10[0-5]*)
        install -d -m 0755 /etc/polkit-1/localauthority/50-local.d
        install -m 0644 "$source_dir/50-mcsm-reboot.pkla" /etc/polkit-1/localauthority/50-local.d/50-mcsm-reboot.pkla
        printf '%s\n' "installed legacy polkit grant (polkit $polkit_version)"
        ;;
    *)
        install -d -m 0755 /etc/polkit-1/rules.d
        install -m 0644 "$source_dir/50-mcsm-reboot.rules" /etc/polkit-1/rules.d/50-mcsm-reboot.rules
        printf '%s\n' "installed polkit rule (polkit ${polkit_version:-unknown})"
        ;;
esac

install -d -m 0755 "/etc/systemd/system/$AGENT_UNIT.d"
cat > "/etc/systemd/system/$AGENT_UNIT.d/20-allow-reboot.conf" <<'EOF'
[Service]
Environment=AGENT_ALLOW_REBOOT=1
EOF
systemctl daemon-reload
printf '%s\n' "enabled AGENT_ALLOW_REBOOT=1 for $AGENT_UNIT"

# Ask logind exactly what the agent will ask, as the agent's user.
answer=$(runuser -u "$AGENT_USER" -- busctl call org.freedesktop.login1 /org/freedesktop/login1 \
    org.freedesktop.login1.Manager CanReboot 2>&1 || true)
printf 'logind CanReboot as %s: %s\n' "$AGENT_USER" "$answer"
case "$answer" in
    *'"yes"'*) ;;
    *)
        printf '%s\n' "WARNING: logind did not answer yes; the dashboard will refuse to reboot." >&2
        printf '%s\n' "Check: journalctl -u polkit --since '10 min ago'" >&2
        ;;
esac

# The services the dashboard needs must come back on their own after a reboot.
for unit in mcsm-api.service "$AGENT_UNIT" nginx.service; do
    printf '%-20s %s\n' "$unit" "$(systemctl is-enabled "$unit" 2>/dev/null || echo missing)"
done

printf '\n%s\n' "Done. Restart the agent to pick up the setting:"
printf '%s\n' "  systemctl restart $AGENT_UNIT"
printf '%s\n' "(Minecraft servers keep running across an agent restart unless AGENT_STOP_SERVERS_ON_EXIT=1.)"
