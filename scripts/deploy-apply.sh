#!/usr/bin/env bash
# Server-side applier for ServerManager deploys. Uploaded and invoked by
# Deploy-Dashboard.ps1 — not meant to be run by hand.
#
# Usage: bash deploy-apply.sh [web] [api] [agent] [rollback] [sha:<short>]
#
# - web    : extract data/web.tgz into web/ via a near-atomic dir swap
# - api    : install data/upload-mcsm-api  -> bin/mcsm-api  (keeps .prev)
# - agent  : install data/upload-mcsm-agent-> bin/mcsm-agent (keeps .prev)
# - rollback : restore bin/*.prev and web_prev, restart services
# Restarting mcsm-api also applies any new DB migrations (goose runs on boot).
set -euo pipefail

ROOT=/srv/dashboard
cd "$ROOT"

do_web=0; do_api=0; do_agent=0; rollback=0; SHA="unknown"
for a in "$@"; do
  case "$a" in
    web) do_web=1 ;;
    api) do_api=1 ;;
    agent) do_agent=1 ;;
    rollback) rollback=1 ;;
    sha:*) SHA="${a#sha:}" ;;
  esac
done

install_bin() { # $1 = binary name
  local n="$1"
  install -o mcsm -g mcsm -m 0755 "data/upload-$n" "bin/$n.new"
  [ -f "bin/$n" ] && cp -a "bin/$n" "bin/$n.prev"
  mv "bin/$n.new" "bin/$n"
  rm -f "data/upload-$n"
}

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
    tar -xzf data/web.tgz -C web_new
    chown -R mcsm:mcsm web_new
    find web_new -type d -exec chmod 755 {} +
    find web_new -type f -exec chmod 644 {} +
    rm -rf web_prev; [ -d web ] && mv web web_prev
    mv web_new web
    rm -f data/web.tgz
    echo "[deploy] web bundle updated"
  fi
  svc=""
  [ "$do_agent" = 1 ] && svc="$svc mcsm-agent"
  [ "$do_api"   = 1 ] && svc="$svc mcsm-api"
  if [ -n "$svc" ]; then echo "[deploy] restarting:$svc"; systemctl restart $svc; fi
fi

# Record what was deployed (audit trail).
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) sha=$SHA web=$do_web api=$do_api agent=$do_agent rollback=$rollback" >> data/DEPLOYED.txt

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
if [ "$fail" = 1 ]; then echo "[health] FAILED"; exit 1; fi
echo "[health] OK (sha=$SHA)"
