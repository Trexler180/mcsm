#!/usr/bin/env bash
# Tests for deploy-apply.sh, run against a fake host: a temp /srv/dashboard,
# a fake /proc for the running API, and shims for systemctl, curl, install and
# chown that record what they were asked to do. Run: bash scripts/deploy_apply_test.sh
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
APPLY="$HERE/deploy-apply.sh"
pass=0
fail=0

# setup builds a fresh fake host in $T: the API running (pid 101) with its
# database at data/mcsm.db, current binaries, and a staging dir with new ones.
setup() {
    T=$(mktemp -d)
    ROOT="$T/srv/dashboard"
    mkdir -p "$ROOT/data" "$ROOT/bin" "$T/proc/101" "$T/stage" "$T/shims" "$T/state"
    echo "db-v1" > "$ROOT/data/mcsm.db"
    echo "wal-v1" > "$ROOT/data/mcsm.db-wal"
    echo "api-old" > "$ROOT/bin/mcsm-api"
    echo "agent-old" > "$ROOT/bin/mcsm-agent"
    printf 'JWT_SECRET=x\nDATABASE_PATH=%s\n' "$ROOT/data/mcsm.db" | tr '\n' '\0' > "$T/proc/101/environ"
    echo 101 > "$T/state/api-pid"
    echo "panel.example.test" > "$T/domain"
    : > "$T/calls"
    cp "$APPLY" "$T/stage/deploy-apply.sh"
    stage_new_binaries

    cat > "$T/shims/systemctl" <<EOF
#!/usr/bin/env bash
echo "systemctl \$*" >> "$T/calls"
case "\$1" in
    show) cat "$T/state/api-pid" ;;
    stop) [ "\$2" = mcsm-api ] && echo 0 > "$T/state/api-pid" ;;
    restart) case " \$* " in *" mcsm-api "*) echo 101 > "$T/state/api-pid" ;; esac ;;
    is-active) echo active ;;
esac
exit 0
EOF
    cat > "$T/shims/curl" <<'EOF'
#!/usr/bin/env bash
echo 200
EOF
    cat > "$T/shims/chown" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    # install without owners (the test isn't root): -d makes directories, else copies.
    cat > "$T/shims/install" <<'EOF'
#!/usr/bin/env bash
dir=0; args=()
while [ $# -gt 0 ]; do
    case "$1" in
        -d) dir=1 ;;
        -o|-g|-m) shift ;;
        *) args+=("$1") ;;
    esac
    shift
done
if [ "$dir" = 1 ]; then mkdir -p "${args[@]}"; else cp "${args[0]}" "${args[1]}"; fi
EOF
    chmod +x "$T/shims/"*
}

stage_new_binaries() {
    echo "api-new" > "$T/stage/upload-mcsm-api"
    echo "agent-new" > "$T/stage/upload-mcsm-agent"
}

apply() {
    PATH="$T/shims:$PATH" DEPLOY_ROOT="$ROOT" DEPLOY_PROC="$T/proc" DEPLOY_DOMAIN_FILE="$T/domain" DEPLOY_HEALTH_WAIT=0 \
        bash "$T/stage/deploy-apply.sh" "$@" > "$T/out" 2>&1 || echo "[exit $?]" >> "$T/out"
}

check() { # name condition...
    local name=$1
    shift
    if "$@"; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        echo "FAIL: $name"
        sed 's/^/    /' "$T/out" 2>/dev/null || true
    fi
}

snapshots() { ls -1d "$ROOT"/db-prev/2*/ 2>/dev/null | wc -l | tr -d ' '; }

# An api deploy copies the database, with the API stopped first, before the new
# binary runs.
test_api_deploy_copies_database() {
    setup
    apply api sha:abc123
    local snap
    snap="$ROOT/db-prev/$(cat "$ROOT/db-prev/LATEST")"
    check "deploy succeeds" grep -q "health\] OK" "$T/out"
    check "copy holds the database" grep -qx "db-v1" "$snap/mcsm.db"
    check "copy holds the WAL" grep -qx "wal-v1" "$snap/mcsm.db-wal"
    check "copy is named for the deploy" test -n "$(echo "$snap" | grep -- '-abc123$')"
    check "copy remembers where the database lives" grep -qx "$ROOT/data/mcsm.db" "$snap/PATH"
    check "API stopped before the copy" grep -q "systemctl stop mcsm-api" "$T/calls"
    check "new binary installed" grep -qx "api-new" "$ROOT/bin/mcsm-api"
    check "old binary kept" grep -qx "api-old" "$ROOT/bin/mcsm-api.prev"
}

# A database given relative to the API's working directory is found there.
test_relative_database_path() {
    setup
    mkdir -p "$T/apicwd"
    mv "$ROOT/data/mcsm.db" "$ROOT/data/mcsm.db-wal" "$T/apicwd/"
    printf 'DATABASE_PATH=./mcsm.db\n' | tr '\n' '\0' > "$T/proc/101/environ"
    ln -s "$T/apicwd" "$T/proc/101/cwd" 2>/dev/null || cp -r "$T/apicwd" "$T/proc/101/cwd"
    apply api sha:rel
    local snap
    snap="$ROOT/db-prev/$(cat "$ROOT/db-prev/LATEST")"
    check "relative path copied" grep -qx "db-v1" "$snap/mcsm.db"
}

# Only api deploys touch the database.
test_web_deploy_takes_no_copy() {
    setup
    mkdir -p "$T/webbuild" && echo hi > "$T/webbuild/index.html"
    tar -czf "$T/stage/web.tgz" -C "$T/webbuild" .
    apply web sha:web1
    check "no copy for a web deploy" test "$(snapshots)" = 0
    check "API not stopped" bash -c "! grep -q 'systemctl stop' '$T/calls'"
}

# Old copies are pruned to the newest three.
test_keeps_three_copies() {
    setup
    local i
    for i in 1 2 3 4; do
        stage_new_binaries
        apply api "sha:d$i"
        sleep 1 # the copies are named by the second
    done
    check "three copies kept" test "$(snapshots)" = 3
    check "the oldest went" bash -c "! ls -d '$ROOT'/db-prev/*-d1 2>/dev/null | grep -q ."
    check "LATEST is the newest" test -n "$(grep -- '-d4$' "$ROOT/db-prev/LATEST")"
}

# Rollback puts the pre-deploy database back and sets the newer one aside; the
# copy is used once.
test_rollback_restores_database() {
    setup
    apply api sha:new
    echo "db-v2-migrated" > "$ROOT/data/mcsm.db"
    echo "wal-v2" > "$ROOT/data/mcsm.db-wal"
    apply rollback sha:rollback
    check "rollback succeeds" grep -q "health\] OK" "$T/out"
    check "database restored" grep -qx "db-v1" "$ROOT/data/mcsm.db"
    check "WAL restored" grep -qx "wal-v1" "$ROOT/data/mcsm.db-wal"
    check "newer database set aside" bash -c "grep -qx 'db-v2-migrated' '$ROOT'/db-prev/rolled-back-*/mcsm.db"
    check "binary rolled back" grep -qx "api-old" "$ROOT/bin/mcsm-api"
    check "copy marked used" test ! -f "$ROOT/db-prev/LATEST"

    echo "db-v3" > "$ROOT/data/mcsm.db"
    apply rollback sha:rollback
    check "second rollback leaves the database" grep -qx "db-v3" "$ROOT/data/mcsm.db"
}

# keepdb rolls back code only.
test_rollback_keepdb() {
    setup
    apply api sha:new
    echo "db-v2" > "$ROOT/data/mcsm.db"
    apply rollback keepdb sha:rollback
    check "keepdb leaves the database" grep -qx "db-v2" "$ROOT/data/mcsm.db"
    check "keepdb still rolls back the binary" grep -qx "api-old" "$ROOT/bin/mcsm-api"
    check "copy kept for a later rollback" test -f "$ROOT/db-prev/LATEST"
}

for t in test_api_deploy_copies_database test_relative_database_path test_web_deploy_takes_no_copy \
         test_keeps_three_copies test_rollback_restores_database test_rollback_keepdb; do
    $t
    rm -rf "$T"
done

echo "deploy-apply: $pass passed, $fail failed"
[ "$fail" = 0 ] && echo "all deploy-apply tests passed"
[ "$fail" = 0 ]
