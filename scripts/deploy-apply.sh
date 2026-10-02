#!/usr/bin/env bash
# Server-side applier for ServerManager deploys. Uploaded and invoked by
# Deploy-Dashboard.ps1 — not meant to be run by hand.
#
# Usage: bash deploy-apply.sh [web] [api] [agent] [provision] [rollback]
#                             [sha:<short>] [base:<web base path>]
#
# - web       : extract web.tgz into web/ via a near-atomic dir swap
# - api       : install upload-mcsm-api   -> bin/mcsm-api   (keeps .prev)
# - agent     : install upload-mcsm-agent -> bin/mcsm-agent (keeps .prev)
# - provision : converge host config (nginx, systemd drop-ins, polkit, SSH
#               watchdog) to the shipped bundle — see deploy/provision.sh
# - rollback  : restore bin/*.prev and web_prev, restart services
# Restarting mcsm-api also applies any new DB migrations (goose runs on boot).
#
# Every deploy ships the provisioning bundle and reports host-config drift,
# whether or not `provision` was asked for.
#
# Artifacts are read from the directory this script was uploaded to: a
# root-only staging dir, never /srv/dashboard/data. That directory belongs to
# the mcsm user, and so does everything running as mcsm — the API, the agent,
# and every Minecraft server with its mods. A root-run step that read from it
# would let any of them swap a file between upload and apply.
set -euo pipefail

ROOT=/srv/dashboard
STAGE=$(cd "$(dirname "$0")" && pwd)
case "$STAGE" in
  "$ROOT"|"$ROOT"/*)
    echo "refusing to run from $STAGE: the staging dir must not be writable by mcsm" >&2
    exit 1 ;;
esac
cd "$ROOT"

do_web=0; do_api=0; do_agent=0; do_provision=0; rollback=0; SHA="unknown"; BASE=/dashboard/
for a in "$@"; do
  case "$a" in
    web) do_web=1 ;;
    api) do_api=1 ;;
    agent) do_agent=1 ;;
    provision) do_provision=1 ;;
    rollback) rollback=1 ;;
    sha:*) SHA="${a#sha:}" ;;
    base:*) BASE="${a#base:}" ;;
  esac
done

install_bin() { # $1 = binary name
  local n="$1"
  install -o mcsm -g mcsm -m 0755 "$STAGE/upload-$n" "bin/$n.new"
  [ -f "bin/$n" ] && cp -a "bin/$n" "bin/$n.prev"
  mv "bin/$n.new" "bin/$n"
  rm -f "$STAGE/upload-$n"
}

# Deploys before the root-only staging dir uploaded here; retire those copies
# so nothing root-run is ever left in the mcsm-owned data dir.
rm -f data/deploy-apply.sh data/web.tgz data/upload-mcsm-api data/upload-mcsm-agent

# Unpack the provisioning bundle (shipped with every deploy).
PROV=""
if [ -f "$STAGE/provision.tgz" ]; then
  rm -rf "$STAGE/provision"
  mkdir -m 700 "$STAGE/provision"
  tar -xzf "$STAGE/provision.tgz" -C "$STAGE/provision" --no-same-owner
  rm -f "$STAGE/provision.tgz"
  PROV="$STAGE/provision/provision.sh"
fi

provision_status=0
if [ "$rollback" = 1 ]; then
  echo "[rollback] restoring previous artifacts"
  [ -f bin/mcsm-api.prev ]   && cp -a bin/mcsm-api.prev   bin/mcsm-api   && echo "  api binary rolled back"
  [ -f bin/mcsm-agent.prev ] && cp -a bin/mcsm-agent.prev bin/mcsm-agent && echo "  agent binary rolled back"
  if [ -d web_prev ]; then
    rm -rf web_bad; mv web web_bad; cp -a web_prev web; rm -rf web_bad
    echo "  web rolled back"
  fi
  systemctl restart mcsm-agent mcsm-api
else
  [ "$do_api"   = 1 ] && { install_bin mcsm-api;   echo "[deploy] api binary updated"; }
  [ "$do_agent" = 1 ] && { install_bin mcsm-agent; echo "[deploy] agent binary updated"; }
  if [ "$do_web" = 1 ]; then
    rm -rf web_new; mkdir web_new
    tar -xzf "$STAGE/web.tgz" -C web_new
    chown -R mcsm:mcsm web_new
    find web_new -type d -exec chmod 755 {} +
    find web_new -type f -exec chmod 644 {} +
    rm -rf web_prev; [ -d web ] && mv web web_prev
    mv web_new web
    rm -f "$STAGE/web.tgz"
    echo "[deploy] web bundle updated"
  fi
  svc=""
  [ "$do_agent" = 1 ] && svc="$svc mcsm-agent"
  [ "$do_api"   = 1 ] && svc="$svc mcsm-api"
  if [ -n "$svc" ]; then echo "[deploy] restarting:$svc"; systemctl restart $svc; fi

  if [ "$do_provision" = 1 ]; then
    if [ -z "$PROV" ]; then
      echo "[provision] no provisioning bundle was uploaded" >&2
      provision_status=1
    else
      echo "[provision]"
      bash "$PROV" apply --base-path "$BASE" || provision_status=$?
    fi
  fi
fi

# Record what was deployed (audit trail).
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) sha=$SHA web=$do_web api=$do_api agent=$do_agent provision=$do_provision rollback=$rollback" >> data/DEPLOYED.txt

# --- Health checks ---
sleep 2
D=$(cat /root/.test_domain)
fail=0
http_check() { # $1 label  $2 url  $3 expect
  local code
  code=$(curl -sS -o /dev/null -w "%{http_code}" "$2" 2>/dev/null || echo 000)
  printf "  %-18s %s\n" "$1" "$code"
  [ "$code" = "$3" ] || fail=1
}
echo "[health]"
for u in mcsm-api mcsm-agent; do
  st=$(systemctl is-active "$u" || true)
  printf "  %-18s %s\n" "svc:$u" "$st"
  [ "$st" = active ] || fail=1
done
http_check "api /health"      "http://127.0.0.1:8081/api/v1/health" 200
http_check "dashboard/"       "https://$D/dashboard/"               200
http_check "dashboard/login"  "https://$D/dashboard/login"          200
http_check "api via proxy"    "https://$D/api/v1/health"            200

# Host config. A provisioning run already printed its own check and fails the
# deploy if it did not converge; a code-only deploy reports drift as a warning,
# so the person deploying learns about it without being blocked by it.
if [ "$do_provision" = 1 ]; then
  [ "$provision_status" = 0 ] || fail=1
elif [ -n "$PROV" ] && [ "$rollback" = 0 ]; then
  bash "$PROV" check --base-path "$BASE" || echo "[config] WARNING: host config differs from the repo; run Deploy-Dashboard.ps1 -Part host"
fi

if [ "$fail" = 1 ]; then echo "[health] FAILED"; exit 1; fi
echo "[health] OK (sha=$SHA)"
