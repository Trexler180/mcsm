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
    echo 200 > "$T/state/health"
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
    # curl answers what state/health says. A line "N CODE" answers 000 for
    # the first N calls and CODE from then on: an API still starting up.
    cat > "$T/shims/curl" <<EOF
#!/usr/bin/env bash
read -r a b < "$T/state/health"
if [ -n "\$b" ]; then
    if [ "\$a" -gt 0 ]; then echo "\$((a - 1)) \$b" > "$T/state/health"; echo 000; else echo "\$b"; fi
else
    echo "\$a"
fi
EOF
    cat > "$T/shims/chown" <<EOF
#!/usr/bin/env bash
echo "chown \$*" >> "$T/calls"
exit 0
EOF
    # install without owners (the test isn't root): -d makes directories, else copies.
    cat > "$T/shims/install" <<EOF
#!/usr/bin/env bash
echo "install \$*" >> "$T/calls"
EOF
    cat >> "$T/shims/install" <<'EOF'
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
        DEPLOY_HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-2}" \
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

stage_web() { # content
    rm -rf "$T/webbuild" && mkdir -p "$T/webbuild" && echo "$1" > "$T/webbuild/index.html"
    tar -czf "$T/stage/web.tgz" -C "$T/webbuild" .
}

# Two bad deploys in a row must not cost the build that worked: rollback
# returns to it, binary, web bundle and database alike.
test_rollback_reaches_the_last_healthy_build() {
    setup
    echo "web-good" > "$T/good.html" && mkdir -p "$ROOT/web" && cp "$T/good.html" "$ROOT/web/index.html"

    echo 503 > "$T/state/health"
    stage_web web-bad-1
    apply api web sha:bad1
    check "a failed deploy is reported" grep -q "health\] FAILED" "$T/out"
    check "the failed build is marked unverified" test -f "$ROOT/bin/mcsm-api.unverified"
    echo "db-migrated-by-bad1" > "$ROOT/data/mcsm.db"
    sleep 1 # the copies are named by the second

    echo "api-bad-2" > "$T/stage/upload-mcsm-api"
    stage_web web-bad-2
    apply api web sha:bad2
    check "the second attempt is installed" grep -qx "api-bad-2" "$ROOT/bin/mcsm-api"
    check "the rollback binary is still the good one" grep -qx "api-old" "$ROOT/bin/mcsm-api.prev"
    check "the rollback web bundle is still the good one" grep -qx "web-good" "$ROOT/web_prev/index.html"
    check "the rollback database is still the good one" test -n "$(grep -- '-bad1$' "$ROOT/db-prev/LATEST")"
    check "the failed build's database was copied too" test "$(snapshots)" = 2

    echo 200 > "$T/state/health"
    apply rollback sha:rollback
    check "rollback succeeds" grep -q "health\] OK" "$T/out"
    check "rollback returns the good binary" grep -qx "api-old" "$ROOT/bin/mcsm-api"
    check "rollback returns the good web bundle" grep -qx "web-good" "$ROOT/web/index.html"
    check "rollback returns the good database" grep -qx "db-v1" "$ROOT/data/mcsm.db"
    check "a healthy rollback is verified" test ! -f "$ROOT/bin/mcsm-api.unverified"
}

# A deploy that passes is what the next rollback returns to.
test_healthy_deploy_becomes_the_rollback_target() {
    setup
    apply api sha:good1
    check "a healthy build is verified" test ! -f "$ROOT/bin/mcsm-api.unverified"
    echo "api-newer" > "$T/stage/upload-mcsm-api"
    apply api sha:good2
    check "the previous healthy build is kept" grep -qx "api-new" "$ROOT/bin/mcsm-api.prev"
}

# A database copy a rollback would restore outlives the pruning of old copies.
test_rollback_copy_survives_pruning() {
    setup
    apply api sha:good
    echo 503 > "$T/state/health"
    local i
    for i in 1 2 3 4; do
        sleep 1
        stage_new_binaries
        apply api "sha:bad$i"
    done
    check "the copy from before the failures is kept" test -d "$ROOT/db-prev/$(cat "$ROOT/db-prev/LATEST")"
    check "it is the healthy build's" test -n "$(grep -- '-bad1$' "$ROOT/db-prev/LATEST")"
}

# The services can read and run what is deployed but not rewrite it.
test_artifacts_are_installed_for_root() {
    setup
    stage_web web-new
    apply api agent web sha:own
    check "api binary installed as root" grep -q "install -o root -g root -m 0755 .*upload-mcsm-api" "$T/calls"
    check "agent binary installed as root" grep -q "install -o root -g root -m 0755 .*upload-mcsm-agent" "$T/calls"
    check "web bundle handed to root" grep -q "chown -R root:root web_new" "$T/calls"
    check "nothing is handed to the service user" bash -c "! grep -E '^(install|chown) .*mcsm[: ]' '$T/calls' | grep -v 'root' | grep -q ."
}

# An API that takes a while to come back is waited for, not failed.
test_waits_for_a_slow_api() {
    setup
    echo "3 200" > "$T/state/health"
    HEALTH_TIMEOUT=10 apply api sha:slow
    check "the deploy succeeds once the API answers" grep -q "health\] OK" "$T/out"
    check "the wait is reported" grep -q "waited 3s for the API" "$T/out"
}

for t in test_api_deploy_copies_database test_relative_database_path test_web_deploy_takes_no_copy \
         test_keeps_three_copies test_rollback_restores_database test_rollback_keepdb \
         test_rollback_reaches_the_last_healthy_build test_healthy_deploy_becomes_the_rollback_target \
         test_rollback_copy_survives_pruning test_artifacts_are_installed_for_root test_waits_for_a_slow_api; do
    $t
    rm -rf "$T"
done

echo "deploy-apply: $pass passed, $fail failed"
[ "$fail" = 0 ] && echo "all deploy-apply tests passed"
[ "$fail" = 0 ]
