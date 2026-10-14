#!/usr/bin/env bash
# End-to-end check of server isolation on a throwaway Linux host (a container):
# it sets the host up the way provision.sh does — the server user, the agent
# user in its group, the launcher installed root-owned with its file
# capabilities — then runs the agent's isolation test as the agent's user
# holding only CAP_KILL, as the systemd drop-in gives it.
#
#   docker run --rm -v "$PWD/apps:/src" -v "$PWD/deploy:/deploy:ro" golang:1.25 bash /deploy/isolation_e2e.sh
#
# Never run this on a real host: it creates users.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run as root in a throwaway container" >&2; exit 1; }
[ -f /.dockerenv ] || [ -n "${MCSM_E2E_FORCE:-}" ] || { echo "refusing to run outside a container" >&2; exit 1; }

apt-get update -qq >/dev/null && apt-get install -y -qq libcap2-bin >/dev/null

groupadd --system mcsm
useradd --system --gid mcsm --home-dir /var/lib/mcsm --create-home --shell /usr/sbin/nologin mcsm
useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin mcsm-mc
usermod -aG mcsm-mc mcsm

cd /src/agent
export GOFLAGS=-buildvcs=false
CGO_ENABLED=0 go build -o /tmp/mcsm-launch ./cmd/mcsm-launch
install -D -o root -g mcsm -m 0750 /tmp/mcsm-launch /usr/local/libexec/mcsm/mcsm-launch
setcap cap_setuid,cap_setgid+ep /usr/local/libexec/mcsm/mcsm-launch

go test -c -o /tmp/isolation.test ./internal/isolation
chmod 0755 /tmp/isolation.test
install -d -o mcsm -g mcsm -m 0755 /srv/servers

cd /tmp
setpriv --reuid mcsm --regid mcsm --init-groups --inh-caps +kill --ambient-caps +kill -- \
    env MCSM_ISOLATION_E2E=1 MCSM_SERVER_ROOT=/srv/servers HOME=/var/lib/mcsm \
    /tmp/isolation.test -test.v -test.run 'EndToEnd|ShareDir|Traversable'
