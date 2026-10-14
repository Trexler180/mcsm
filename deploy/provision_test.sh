#!/usr/bin/env bash
# Tests for provision.sh. Runs the real script against a throwaway directory
# standing in for the host, with stand-ins for systemctl, nginx, curl, runuser,
# pkaction, and the user and capability tools that record what they were asked
# to do. Needs only bash and
# coreutils, so it runs on Linux and in Git Bash.
#
#   bash deploy/provision_test.sh

set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
PROVISION=$HERE/provision.sh
failures=0
current=""

fail() { printf '  FAIL [%s] %s\n' "$current" "$*"; failures=$((failures + 1)); }
assert_eq() { [ "$1" = "$2" ] || fail "$3: got '$1', want '$2'"; }
assert_contains() { grep -qF -- "$2" <<<"$1" || fail "$3: output lacks '$2'"; }
assert_not_contains() { ! grep -qF -- "$2" <<<"$1" || fail "$3: output unexpectedly has '$2'"; }
assert_same() { cmp -s "$1" "$2" || fail "$3: $2 differs from $1"; }

# ── Fake host ────────────────────────────────────────────────────

new_host() {
    T=$(mktemp -d)
    R=$T/root
    mkdir -p "$T/bin" "$T/state" "$T/proc/101" "$T/proc/102" \
        "$R/etc/nginx/sites-enabled" "$R/etc/nginx/conf.d" "$R/etc/nginx/snippets" \
        "$R/etc/systemd/system" "$R/etc/polkit-1/rules.d"
    : > "$T/calls"
    echo "panel.example.test" > "$T/domain"

    # The site includes the dashboard snippet, as on the real host.
    cat > "$R/etc/nginx/sites-enabled/site.conf" <<'EOF'
server {
    listen 443 ssl;
    server_name panel.example.test;
    include /etc/nginx/snippets/mcsm-dashboard.conf;
}
EOF
    # The pre-provisioning snippet: the repo's, minus the managed additions.
    grep -v 'well-known' "$HERE/nginx/mcsm-dashboard.conf" > "$R/etc/nginx/snippets/mcsm-dashboard.conf"
    cp "$HERE/nginx/mcsm-ws-upgrade.conf" "$R/etc/nginx/conf.d/mcsm-ws-upgrade.conf"

    # Running processes start with only what secrets.env gives them.
    printf 'JWT_SECRET=x\nDATABASE_PATH=/srv/dashboard/data/mcsm.db\n' > "$T/state/base-mcsm-api.service"
    printf 'AGENT_TOKEN=x\nAGENT_SERVER_ROOT=/srv/dashboard/servers\n' > "$T/state/base-mcsm-agent.service"
    tr '\n' '\0' < "$T/state/base-mcsm-api.service" > "$T/proc/101/environ"
    tr '\n' '\0' < "$T/state/base-mcsm-agent.service" > "$T/proc/102/environ"
    mkdir -p "$R/srv/dashboard/data" "$R/srv/dashboard/servers"

    # The host's users: the agent's, without the server user yet.
    echo mcsm > "$T/state/users"
    : > "$T/state/groups-mcsm"
    # The launcher Deploy-Dashboard.ps1 would have built into the bundle.
    printf 'launcher-binary' > "$T/mcsm-launch"

    make_shims
}

# A restart re-reads the unit's drop-ins into its process environment, the way
# systemd would; secrets.env overrides are simulated with state/override-*.
restart_env() { # unit pid
    local f lines="$T/state/env-$1"
    : > "$lines"
    [ -f "$T/state/base-$1" ] && cat "$T/state/base-$1" >> "$lines"
    for f in "$R/etc/systemd/system/$1.d/"*.conf; do
        [ -f "$f" ] || continue
        sed -n 's/^Environment=//p' "$f" >> "$lines"
    done
    [ -f "$T/state/override-$1" ] && cat "$T/state/override-$1" >> "$lines"
    # A later assignment of a name replaces an earlier one, as in systemd.
    awk -F= '{ if (!($1 in v)) o[++n] = $1; v[$1] = $0 } END { for (i = 1; i <= n; i++) print v[o[i]] }' "$lines" |
        tr '\n' '\0' > "$T/proc/$2/environ"
}

make_shims() {
    cat > "$T/bin/systemctl" <<EOF
#!/usr/bin/env bash
T='$T'; R='$R'
echo "systemctl \$*" >> "\$T/calls"
case "\$1" in
  show)
    unit=\$2; prop=\$4
    case "\$prop" in
      LoadState) [ "\$unit" = ssh.service ] && echo loaded || echo not-found ;;
      MainPID) case "\$unit" in mcsm-api.service) echo 101 ;; mcsm-agent.service) echo 102 ;; *) echo 0 ;; esac ;;
    esac ;;
  is-enabled|is-active)
    unit=\${@: -1}
    [ ! -e "\$T/state/disabled-\$unit" ] ;;
  restart)
    case "\$2" in
      mcsm-api.service) bash "\$T/restart-env" mcsm-api.service 101 ;;
      mcsm-agent.service) bash "\$T/restart-env" mcsm-agent.service 102 ;;
    esac ;;
  *) exit 0 ;;
esac
EOF
    { echo "#!/usr/bin/env bash"; echo "T='$T'; R='$R'"; declare -f restart_env; echo 'restart_env "$@"'; } > "$T/restart-env"

    cat > "$T/bin/nginx" <<EOF
#!/usr/bin/env bash
echo "nginx \$*" >> '$T/calls'
if [ -e '$T/state/nginx-bad' ]; then
  echo "nginx: [emerg] unexpected \"}\" in /etc/nginx/snippets/mcsm-dashboard.conf:9" >&2
  exit 1
fi
EOF

    cat > "$T/bin/curl" <<EOF
#!/usr/bin/env bash
url=\${@: -1}
case "\$url" in
  */api/v1/health) echo 200 ;;
  */.well-known/oauth-protected-resource/*)
    grep -q oauth-protected-resource '$R/etc/nginx/snippets/mcsm-dashboard.conf' && echo 200 || echo 404 ;;
  */dashboard/) [ -e '$T/state/dashboard-down' ] && echo 502 || echo 200 ;;
  *) echo 404 ;;
esac
EOF

    cat > "$T/bin/runuser" <<EOF
#!/usr/bin/env bash
echo "runuser \$*" >> '$T/calls'
# "runuser -u mcsm-mc -- test -x DIR": can the server user reach DIR?
if [ "\$4" = test ]; then
  case "\$6" in
    */srv/dashboard/data) [ ! -e '$T/state/data-closed' ] ;;
    *) [ ! -e '$T/state/servers-unreachable' ] ;;
  esac
  exit
fi
if [ -f '$R/etc/polkit-1/rules.d/50-mcsm-reboot.rules' ] ||
   [ -f '$R/etc/polkit-1/localauthority/50-local.d/50-mcsm-reboot.pkla' ]; then
  echo 's "yes"'
else
  echo 's "challenge"'
fi
EOF

    # Users and groups, from state/users and state/groups-<user>.
    cat > "$T/bin/getent" <<EOF
#!/usr/bin/env bash
grep -qx "\$2" '$T/state/users'
EOF
    cat > "$T/bin/id" <<EOF
#!/usr/bin/env bash
echo "\${@: -1} \$(tr '\\n' ' ' < '$T/state/groups-'\${@: -1} 2>/dev/null)"
EOF
    cat > "$T/bin/useradd" <<EOF
#!/usr/bin/env bash
echo "useradd \$*" >> '$T/calls'
echo "\${@: -1}" >> '$T/state/users'
EOF
    cat > "$T/bin/usermod" <<EOF
#!/usr/bin/env bash
echo "usermod \$*" >> '$T/calls'
echo "\$2" >> '$T/state/groups-'"\$3"
EOF
    # File capabilities, kept in state/caps-<file name>.
    cat > "$T/bin/setcap" <<EOF
#!/usr/bin/env bash
echo "setcap \$*" >> '$T/calls'
echo "\$2 \$1" > '$T/state/caps-'"\$(basename "\$2")"
EOF
    cat > "$T/bin/getcap" <<EOF
#!/usr/bin/env bash
cat '$T/state/caps-'"\$(basename "\$1")" 2>/dev/null
EOF
    # Closing the data directory is what the server user's probe sees.
    cat > "$T/bin/chmod" <<EOF
#!/usr/bin/env bash
echo "chmod \$*" >> '$T/calls'
case "\${@: -1}" in */srv/dashboard/data) touch '$T/state/data-closed' ;; esac
EOF

    cat > "$T/bin/pkaction" <<EOF
#!/usr/bin/env bash
[ -e '$T/state/polkit-legacy' ] && echo "pkaction version 0.105" || echo "pkaction version 124"
EOF
    chmod +x "$T/bin/"*
}

run() { # mode [args...] -> sets OUT and CODE
    OUT=$(PATH="$T/bin:$PATH" PROVISION_ROOT="$R" PROVISION_PROC="$T/proc" \
        PROVISION_DOMAIN_FILE="$T/domain" PROVISION_WAIT_SECONDS=1 \
        PROVISION_LAUNCHER_SRC="${LAUNCHER_SRC_OVERRIDE:-$T/mcsm-launch}" \
        bash "$PROVISION" "$@" 2>&1)
    CODE=$?
}

calls() { cat "$T/calls"; }
reset_calls() { : > "$T/calls"; }

# ── Tests ────────────────────────────────────────────────────────

test_check_reports_an_unprovisioned_host() {
    new_host
    run check
    assert_eq "$CODE" 2 "exit status"
    assert_contains "$OUT" "mcsm-dashboard.conf differs from the repo" "snippet drift"
    assert_contains "$OUT" "50-mcsm-reboot.rules is missing" "polkit missing"
    assert_contains "$OUT" "MCP discovery answers 404" "discovery"
    assert_contains "$OUT" "does not have APP_BASE_PATH=/dashboard/" "api env"
    assert_not_contains "$(calls)" "restart" "check must not change anything"
    assert_not_contains "$(calls)" "reload" "check must not change anything"
}

test_apply_converges_and_is_idempotent() {
    new_host
    run apply
    assert_eq "$CODE" 0 "first apply"
    assert_contains "$OUT" "ok: host matches the repo" "converged"
    assert_same "$HERE/nginx/mcsm-dashboard.conf" "$R/etc/nginx/snippets/mcsm-dashboard.conf" "snippet"
    assert_same "$HERE/polkit/50-mcsm-reboot.rules" "$R/etc/polkit-1/rules.d/50-mcsm-reboot.rules" "polkit"
    assert_same "$HERE/ssh-watchdog/ssh-restart.conf" "$R/etc/systemd/system/ssh.service.d/10-watchdog-restart.conf" "ssh drop-in"
    assert_contains "$(calls)" "nginx -t" "tests nginx before reload"
    assert_contains "$(calls)" "systemctl reload nginx" "reloads nginx"
    assert_contains "$(calls)" "systemctl daemon-reload" "daemon-reload"
    assert_contains "$(calls)" "systemctl restart mcsm-api.service" "restarts api"
    assert_contains "$(calls)" "systemctl restart mcsm-agent.service" "restarts agent"
    assert_contains "$(calls)" "systemctl enable --now ssh-watchdog.timer" "enables watchdog"
    assert_not_contains "$(calls)" "restart ssh" "must never restart SSH"
    # The unchanged ws map was left alone and nothing backed it up.
    [ ! -e "$(ls -d "$R"/var/backups/mcsm-provision/*/etc/nginx/conf.d 2>/dev/null)" ] ||
        fail "an identical file was backed up as if replaced"

    reset_calls
    run apply
    assert_eq "$CODE" 0 "second apply"
    assert_contains "$OUT" "nothing to change" "idempotent"
    assert_not_contains "$(calls)" "restart" "no restarts when nothing changed"
    assert_not_contains "$(calls)" "reload nginx" "no reload when nothing changed"
}

test_rejected_nginx_config_is_rolled_back() {
    new_host
    local before
    before=$(cat "$R/etc/nginx/snippets/mcsm-dashboard.conf")
    touch "$T/state/nginx-bad"
    run apply
    assert_eq "$CODE" 1 "apply status"
    assert_contains "$OUT" "nginx: rejected the new config: nginx: [emerg]" "reports nginx error"
    assert_eq "$(cat "$R/etc/nginx/snippets/mcsm-dashboard.conf")" "$before" "snippet restored"
    assert_not_contains "$(calls)" "reload nginx" "never reloads a rejected config"
    # The other components still converge.
    assert_same "$HERE/polkit/50-mcsm-reboot.rules" "$R/etc/polkit-1/rules.d/50-mcsm-reboot.rules" "polkit still installed"
}

test_dashboard_broken_by_reload_is_rolled_back() {
    new_host
    local before
    before=$(cat "$R/etc/nginx/snippets/mcsm-dashboard.conf")
    touch "$T/state/dashboard-down"
    run apply
    assert_eq "$CODE" 1 "apply status"
    assert_contains "$OUT" "dashboard stopped answering" "reports outage"
    assert_eq "$(cat "$R/etc/nginx/snippets/mcsm-dashboard.conf")" "$before" "snippet restored"
    assert_eq "$(grep -c 'reload nginx' "$T/calls")" 2 "reloaded the new config, then the restored one"
}

test_snippet_not_included_is_reported_not_installed() {
    new_host
    sed -i '/include/d' "$R/etc/nginx/sites-enabled/site.conf"
    run apply
    assert_eq "$CODE" 2 "apply status"
    assert_contains "$OUT" "no enabled site includes /etc/nginx/snippets/mcsm-dashboard.conf" "reports missing include"
    assert_not_contains "$(calls)" "nginx -t" "leaves nginx alone"
}

test_base_path_mismatch_refuses_to_apply() {
    new_host
    run apply --base-path /elsewhere/
    assert_eq "$CODE" 1 "apply status"
    assert_contains "$OUT" "does not set APP_BASE_PATH=/elsewhere/" "explains"
    [ ! -e "$R/etc/polkit-1/rules.d/50-mcsm-reboot.rules" ] || fail "installed files despite the mismatch"
}

test_drift_after_apply_is_detected() {
    new_host
    run apply
    echo "# hand edit" >> "$R/etc/nginx/snippets/mcsm-dashboard.conf"
    run check
    assert_eq "$CODE" 2 "check status"
    assert_contains "$OUT" "mcsm-dashboard.conf differs from the repo (edited on the server?)" "drift"
}

test_secrets_override_is_reported() {
    new_host
    echo "APP_BASE_PATH=/" > "$T/state/override-mcsm-api.service"
    run apply
    assert_contains "$OUT" "does not have APP_BASE_PATH=/dashboard/ (restart pending, or secrets.env overrides it)" "override"
}

test_units_disabled_at_boot_are_reported() {
    new_host
    run apply
    touch "$T/state/disabled-mcsm-agent.service"
    run check
    assert_eq "$CODE" 2 "check status"
    assert_contains "$OUT" "boot: mcsm-agent.service is not enabled at boot" "boot"
}

# polkit 0.105 does not read JavaScript rules; it gets the .pkla grant instead.
test_legacy_polkit_gets_the_pkla_grant() {
    new_host
    touch "$T/state/polkit-legacy"
    run apply
    assert_eq "$CODE" 0 "apply status"
    assert_same "$HERE/polkit/50-mcsm-reboot.pkla" "$R/etc/polkit-1/localauthority/50-local.d/50-mcsm-reboot.pkla" "pkla"
    [ ! -e "$R/etc/polkit-1/rules.d/50-mcsm-reboot.rules" ] || fail "installed JS rules that polkit 0.105 cannot read"
}

test_usage_errors() {
    new_host
    run frobnicate
    assert_eq "$CODE" 64 "unknown mode"
    run check --base-path
    assert_eq "$CODE" 64 "missing value"
}

# ── Server isolation ─────────────────────────────────────────────

test_apply_sets_up_server_isolation() {
    new_host
    run apply
    assert_eq "$CODE" 0 "apply status"
    assert_contains "$OUT" "created the server user mcsm-mc" "user"
    assert_contains "$OUT" "added mcsm to the mcsm-mc group" "group"
    assert_contains "$OUT" "installed /usr/local/libexec/mcsm/mcsm-launch" "launcher"
    assert_contains "$OUT" "closed the API's data directory" "data dir"
    assert_same "$T/mcsm-launch" "$R/usr/local/libexec/mcsm/mcsm-launch" "launcher copied"
    assert_contains "$(calls)" "setcap cap_setuid,cap_setgid+ep" "capabilities"
    assert_contains "$(calls)" "useradd --system --user-group" "system user"
    assert_same "$HERE/systemd/mcsm-agent.service.d/30-isolation.conf" \
        "$R/etc/systemd/system/mcsm-agent.service.d/30-isolation.conf" "agent drop-in"
    assert_same "$HERE/systemd/mcsm-api.service.d/30-hardening.conf" \
        "$R/etc/systemd/system/mcsm-api.service.d/30-hardening.conf" "api drop-in"
    assert_contains "$(calls)" "systemctl restart mcsm-agent.service" "agent picks it up"
    assert_contains "$OUT" "ok: host matches the repo" "converged"

    reset_calls
    run apply
    assert_eq "$CODE" 0 "second apply"
    assert_contains "$OUT" "nothing to change" "idempotent"
    assert_not_contains "$(calls)" "useradd" "user created once"
    assert_not_contains "$(calls)" "setcap" "capabilities set once"
    assert_not_contains "$(calls)" "restart" "no restart when nothing changed"
}

# The deliberate opt-out is reported, not treated as drift.
test_isolation_opt_out_is_honoured() {
    new_host
    echo "AGENT_ISOLATE_SERVERS=0" > "$T/state/override-mcsm-agent.service"
    run apply
    assert_eq "$CODE" 0 "apply status"
    assert_contains "$OUT" "isolation: turned off on this host (AGENT_ISOLATE_SERVERS=0)" "reported"
    assert_not_contains "$OUT" "does not have AGENT_ISOLATE_SERVERS=1" "not drift"
}

test_missing_launcher_is_reported() {
    new_host
    LAUNCHER_SRC_OVERRIDE=$T/no-such-launcher run apply
    assert_eq "$CODE" 1 "apply status"
    assert_contains "$OUT" "the bundle has no launcher" "explains"
}

test_unreachable_server_root_is_reported() {
    new_host
    touch "$T/state/servers-unreachable"
    run apply
    assert_contains "$OUT" "mcsm-mc cannot reach the server root /srv/dashboard/servers" "explains"
}

test_server_root_inside_data_dir_is_refused() {
    new_host
    printf 'AGENT_TOKEN=x\nAGENT_SERVER_ROOT=/srv/dashboard/data/servers\n' > "$T/state/base-mcsm-agent.service"
    tr '\n' '\0' < "$T/state/base-mcsm-agent.service" > "$T/proc/102/environ"
    run apply
    assert_eq "$CODE" 1 "apply status"
    assert_contains "$OUT" "is inside the API's data directory" "explains"
    assert_not_contains "$(calls)" "chmod 0700" "left the data directory open rather than cut off the servers"
}

tests=$(declare -F | awk '{print $3}' | grep '^test_')
for t in $tests; do
    current=$t
    printf '%s\n' "$t"
    "$t"
    rm -rf "$T"
done
if [ "$failures" -gt 0 ]; then
    printf '\n%d failure(s)\n' "$failures"
    exit 1
fi
printf '\nall provision tests passed\n'
