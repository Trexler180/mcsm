#!/usr/bin/env bash
# ServerManager host provisioning: the parts of the production host that live
# outside /srv/dashboard — the nginx snippets, systemd drop-ins, the polkit
# reboot grant, and the SSH watchdog.
#
# Every file it installs is a byte-for-byte copy of a file next to this script,
# so the repo is the source of truth and the host can be checked against it.
#
#   provision.sh check [--base-path /dashboard/]   report drift; changes nothing
#   provision.sh apply [--base-path /dashboard/]   converge the host, then check
#
# Exit status: 0 the host matches the repo, 1 an apply step failed (and was
# rolled back where it could be), 2 check found drift, 64 usage error.
#
# Runs as root. Deploy-Dashboard.ps1 ships it on every deploy and runs `check`;
# `-Provision` (or `-Part host`) runs `apply`. It never restarts SSH and never
# edits a file it does not own: the site's nginx server block, secrets.env, and
# anything not in manifest() are left alone.

set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)

# Test hooks. Production leaves all of these unset.
R=${PROVISION_ROOT:-}                                  # prefix for every host path
PROC=${PROVISION_PROC:-/proc}
DOMAIN_FILE=${PROVISION_DOMAIN_FILE:-/root/.test_domain}
WAIT_SECONDS=${PROVISION_WAIT_SECONDS:-30}

AGENT_USER=mcsm
API_UNIT=mcsm-api.service
AGENT_UNIT=mcsm-agent.service
API_LOCAL=http://127.0.0.1:8081
DASHBOARD_SNIPPET=/etc/nginx/snippets/mcsm-dashboard.conf
API_DROPIN=systemd/mcsm-api.service.d/20-provisioned.conf

usage() { echo "usage: provision.sh apply|check [--base-path PATH]" >&2; exit 64; }

MODE=${1:-}
[ $# -gt 0 ] && shift
BASE_PATH=/dashboard/
while [ $# -gt 0 ]; do
    case "$1" in
        --base-path) [ $# -ge 2 ] || usage; BASE_PATH=$2; shift 2 ;;
        *) usage ;;
    esac
done
case "$MODE" in apply|check) ;; *) usage ;; esac

if [ -z "$R" ] && [ "$(id -u)" -ne 0 ]; then
    echo "provision.sh must run as root" >&2
    exit 1
fi

# Never printed: deploy output is shown to people and tools that should not
# learn the production hostname.
DOMAIN=$(cat "$DOMAIN_FILE" 2>/dev/null || true)

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
# Root-owned and outside anything the mcsm user can write.
STATE_DIR=$R/var/backups/mcsm-provision
BACKUP_DIR=$STATE_DIR/$STAMP

problems=0
say() { printf '[config] %s\n' "$*"; }
bad() { say "$*"; problems=$((problems + 1)); }

# ── What is managed ──────────────────────────────────────────────

ssh_unit() {
    local u
    for u in ssh.service sshd.service; do
        if [ "$(systemctl show "$u" -p LoadState --value 2>/dev/null || true)" = loaded ]; then
            echo "$u"
            return
        fi
    done
}

# polkit 0.105 (Ubuntu <= 22.04) reads .pkla files; newer versions read
# JavaScript rules.
polkit_legacy() {
    case "$(pkaction --version 2>/dev/null | awk '{print $NF}')" in
        0.10[0-5]*) return 0 ;;
    esac
    return 1
}

# One line per managed file: component|source (relative to HERE)|destination|mode
manifest() {
    echo "nginx|nginx/mcsm-ws-upgrade.conf|$R/etc/nginx/conf.d/mcsm-ws-upgrade.conf|0644"
    echo "nginx|nginx/mcsm-dashboard.conf|$R$DASHBOARD_SNIPPET|0644"
    echo "api-env|$API_DROPIN|$R/etc/systemd/system/$API_UNIT.d/20-provisioned.conf|0644"
    echo "reboot|systemd/mcsm-agent.service.d/20-allow-reboot.conf|$R/etc/systemd/system/$AGENT_UNIT.d/20-allow-reboot.conf|0644"
    if polkit_legacy; then
        echo "reboot|polkit/50-mcsm-reboot.pkla|$R/etc/polkit-1/localauthority/50-local.d/50-mcsm-reboot.pkla|0644"
    else
        echo "reboot|polkit/50-mcsm-reboot.rules|$R/etc/polkit-1/rules.d/50-mcsm-reboot.rules|0644"
    fi
    local su
    su=$(ssh_unit)
    echo "ssh-watchdog|ssh-watchdog/ssh-watchdog.sh|$R/usr/local/sbin/ssh-watchdog|0755"
    echo "ssh-watchdog|ssh-watchdog/ssh-watchdog.service|$R/etc/systemd/system/ssh-watchdog.service|0644"
    echo "ssh-watchdog|ssh-watchdog/ssh-watchdog.timer|$R/etc/systemd/system/ssh-watchdog.timer|0644"
    if [ -n "$su" ]; then
        echo "ssh-watchdog|ssh-watchdog/ssh-restart.conf|$R/etc/systemd/system/$su.d/10-watchdog-restart.conf|0644"
    fi
}

# ── Helpers ──────────────────────────────────────────────────────

# The bundle must agree with the deploy that shipped it: the web is built for
# BASE_PATH, so nginx must serve it there and the API must build links with it.
bundle_consistent() {
    local ok=0
    if ! grep -qx "Environment=APP_BASE_PATH=$BASE_PATH" "$HERE/$API_DROPIN"; then
        bad "repo: $API_DROPIN does not set APP_BASE_PATH=$BASE_PATH (the deploy's web base)"
        ok=1
    fi
    if ! grep -q "^location $BASE_PATH {" "$HERE/nginx/mcsm-dashboard.conf"; then
        bad "repo: nginx/mcsm-dashboard.conf does not serve $BASE_PATH (the deploy's web base)"
        ok=1
    fi
    return $ok
}

snippet_included() {
    grep -RqsE "^[[:space:]]*include[[:space:]]+$DASHBOARD_SNIPPET;" \
        "$R/etc/nginx/sites-enabled/" "$R/etc/nginx/conf.d/"
}

# The value a running unit actually has, read from the process — what the
# service is really using, including anything secrets.env overrides.
unit_env_is() { # unit NAME=VALUE
    local pid
    pid=$(systemctl show "$1" -p MainPID --value 2>/dev/null || echo 0)
    [ -n "$pid" ] && [ "$pid" != 0 ] || return 1
    tr '\0' '\n' < "$PROC/$pid/environ" 2>/dev/null | grep -qx "$2"
}

http_code() {
    curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$1" 2>/dev/null || echo 000
}

wait_for_api() {
    local i
    for ((i = 0; i < WAIT_SECONDS; i++)); do
        [ "$(http_code "$API_LOCAL/api/v1/health")" = 200 ] && return 0
        sleep 1
    done
    return 1
}

# ── Check ────────────────────────────────────────────────────────

check() {
    local comp src dest mode
    while IFS='|' read -r comp src dest mode; do
        if [ ! -f "$dest" ]; then
            bad "$comp: ${dest#"$R"} is missing"
        elif ! cmp -s "$HERE/$src" "$dest"; then
            bad "$comp: ${dest#"$R"} differs from the repo (edited on the server?)"
        fi
    done < <(manifest)

    # nginx: the snippet only matters if the site includes it.
    if ! snippet_included; then
        bad "nginx: no enabled site includes $DASHBOARD_SNIPPET; add 'include $DASHBOARD_SNIPPET;' to the 443 server block"
    fi
    wait_for_api || bad "api: not answering on $API_LOCAL"
    if [ -n "$DOMAIN" ]; then
        local code
        code=$(http_code "https://$DOMAIN/.well-known/oauth-protected-resource/api/v1/mcp")
        [ "$code" = 200 ] || bad "nginx: MCP discovery answers $code, want 200"
    fi

    unit_env_is "$API_UNIT" "APP_BASE_PATH=$BASE_PATH" ||
        bad "api-env: the running API does not have APP_BASE_PATH=$BASE_PATH (restart pending, or secrets.env overrides it)"

    unit_env_is "$AGENT_UNIT" "AGENT_ALLOW_REBOOT=1" ||
        bad "reboot: the running agent does not have AGENT_ALLOW_REBOOT=1 (restart pending?)"
    # polkit notices a new rule file asynchronously, so right after an apply
    # the answer gets a few seconds to settle before it counts.
    local answer i
    for ((i = 0; i < 5; i++)); do
        answer=$(runuser -u "$AGENT_USER" -- busctl call org.freedesktop.login1 /org/freedesktop/login1 \
            org.freedesktop.login1.Manager CanReboot 2>&1 || true)
        case "$answer" in *'"yes"'*) break ;; esac
        [ "$MODE" = apply ] || break
        sleep 1
    done
    case "$answer" in
        *'"yes"'*) ;;
        *) bad "reboot: logind does not let $AGENT_USER reboot (answered: ${answer:-nothing})" ;;
    esac

    if [ -n "$(ssh_unit)" ]; then
        systemctl is-enabled --quiet ssh-watchdog.timer 2>/dev/null &&
            systemctl is-active --quiet ssh-watchdog.timer 2>/dev/null ||
            bad "ssh-watchdog: timer is not enabled and running"
    fi

    # Everything the dashboard needs must come back on its own after a reboot.
    local unit
    for unit in "$API_UNIT" "$AGENT_UNIT" nginx.service; do
        systemctl is-enabled --quiet "$unit" 2>/dev/null || bad "boot: $unit is not enabled at boot"
    done
    if ! systemctl is-enabled --quiet ssh.socket 2>/dev/null &&
        ! systemctl is-enabled --quiet "$(ssh_unit)" 2>/dev/null; then
        bad "boot: SSH is not enabled at boot"
    fi

    if [ "$problems" -eq 0 ]; then
        say "ok: host matches the repo (nginx, api-env, reboot, ssh-watchdog, boot)"
        return 0
    fi
    say "$problems problem(s); deploy with -Provision to converge"
    return 2
}

# ── Apply ────────────────────────────────────────────────────────

CHANGED=()

# Install src over dest when they differ, keeping the old copy first.
# Returns 0 changed, 1 already identical, 2 failed (and reported).
#
# apply() runs in a context where bash ignores `set -e` (it is called from a
# condition), so every step here checks its own result rather than relying on
# errexit — a silent half-install is the failure this script exists to prevent.
sync_file() { # component src dest mode
    local comp=$1 src=$HERE/$2 dest=$3 mode=$4 rel=${3#"$R"}
    if [ -f "$dest" ] && cmp -s "$src" "$dest"; then
        return 1
    fi
    if [ -e "$dest" ]; then
        if ! { mkdir -p "$(dirname "$BACKUP_DIR$rel")" && cp -p "$dest" "$BACKUP_DIR$rel"; }; then
            bad "$comp: could not back up $rel; left it unchanged"
            return 2
        fi
    fi
    if ! install -D -m "$mode" "$src" "$dest"; then
        bad "$comp: could not install $rel"
        return 2
    fi
    CHANGED+=("$comp|$dest")
    say "$comp: updated $rel"
    return 0
}

changed() { # component
    local c
    for c in "${CHANGED[@]+"${CHANGED[@]}"}"; do
        [ "${c%%|*}" = "$1" ] && return 0
    done
    return 1
}

# Put back what sync_file replaced in a component: the saved copy, or nothing
# if the file did not exist before.
restore() { # component
    local c dest
    for c in "${CHANGED[@]+"${CHANGED[@]}"}"; do
        [ "${c%%|*}" = "$1" ] || continue
        dest=${c#*|}
        if [ -f "$BACKUP_DIR${dest#"$R"}" ]; then
            cp -p "$BACKUP_DIR${dest#"$R"}" "$dest"
        else
            rm -f "$dest"
        fi
        say "$1: restored ${dest#"$R"}"
    done
}

apply() {
    local failed=0
    bundle_consistent || return 1

    local comp src dest mode
    while IFS='|' read -r comp src dest mode; do
        if [ "$comp" = nginx ] && ! snippet_included; then
            continue # reported by check; installing an unused snippet helps nobody
        fi
        local rc=0
        sync_file "$comp" "$src" "$dest" "$mode" || rc=$?
        [ "$rc" -ne 2 ] || failed=1
    done < <(manifest)

    # nginx: test before reloading, and verify the dashboard after. Either
    # failing puts the previous files back, so a bad snippet costs a warning
    # rather than the site.
    if changed nginx; then
        local test_out
        if ! test_out=$(nginx -t 2>&1); then
            bad "nginx: rejected the new config: $(printf '%s\n' "$test_out" | grep -m1 -E 'emerg|error' || echo "nginx -t failed")"
            restore nginx
            failed=1
        elif ! systemctl reload nginx; then
            bad "nginx: reload failed; restoring the previous config"
            restore nginx
            systemctl reload nginx || true
            failed=1
        else
            if [ -n "$DOMAIN" ] && [ "$(http_code "https://$DOMAIN$BASE_PATH")" != 200 ]; then
                bad "nginx: the dashboard stopped answering after reload; restoring the previous config"
                restore nginx
                systemctl reload nginx || true
                failed=1
            fi
        fi
    fi

    if changed api-env || changed reboot || changed ssh-watchdog; then
        systemctl daemon-reload || { bad "systemd: daemon-reload failed"; failed=1; }
    fi
    if [ -n "$(ssh_unit)" ]; then
        # Idempotent. Never restarts SSH itself: the drop-in only changes how
        # systemd reacts the next time sshd exits.
        systemctl enable --now ssh-watchdog.timer >/dev/null 2>&1 ||
            { bad "ssh-watchdog: could not enable the timer"; failed=1; }
    fi
    if changed api-env; then
        say "api-env: restarting $API_UNIT"
        systemctl restart "$API_UNIT" || { bad "api-env: $API_UNIT failed to restart"; failed=1; }
    fi
    if changed reboot; then
        # Minecraft servers keep running across an agent restart (KillMode=process).
        say "reboot: restarting $AGENT_UNIT"
        systemctl restart "$AGENT_UNIT" || { bad "reboot: $AGENT_UNIT failed to restart"; failed=1; }
    fi

    mkdir -p "$STATE_DIR" || true
    printf '%s changed=%d failed=%d\n' "$STAMP" "${#CHANGED[@]}" "$failed" >> "$STATE_DIR/history.log" ||
        say "could not write ${STATE_DIR#"$R"}/history.log"
    [ "${#CHANGED[@]}" -gt 0 ] || say "nothing to change"
    [ "${#CHANGED[@]}" -eq 0 ] || say "backups of replaced files: ${BACKUP_DIR#"$R"}"
    return $failed
}

if [ "$MODE" = apply ]; then
    if ! apply; then
        check || true
        exit 1
    fi
fi
check
