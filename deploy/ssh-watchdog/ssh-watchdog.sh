#!/bin/sh

set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

log() {
    printf '%s\n' "ssh-watchdog: $*"
}

unit_is_loaded() {
    [ "$(systemctl show "$1" --property=LoadState --value 2>/dev/null || true)" = "loaded" ]
}

find_service_unit() {
    for unit in ssh.service sshd.service; do
        if unit_is_loaded "$unit"; then
            printf '%s\n' "$unit"
            return 0
        fi
    done

    return 1
}

service_unit=$(find_service_unit) || {
    log "neither ssh.service nor sshd.service is installed"
    exit 1
}

socket_unit=ssh.socket
socket_managed=false
if unit_is_loaded "$socket_unit"; then
    if systemctl is-enabled --quiet "$socket_unit" 2>/dev/null || \
       systemctl is-active --quiet "$socket_unit" 2>/dev/null; then
        socket_managed=true
    fi
fi

repair_socket_mode() {
    reason=$1
    log "$reason; repairing $socket_unit and $service_unit"

    service_was_failed=false
    if systemctl is-failed --quiet "$service_unit" 2>/dev/null; then
        service_was_failed=true
    fi

    systemctl reset-failed "$socket_unit" "$service_unit" 2>/dev/null || true
    systemctl restart "$socket_unit"

    # A socket-activated SSH service may legitimately be inactive until the
    # next connection. Restart it immediately only if it was actually failed.
    if [ "$service_was_failed" = true ]; then
        systemctl restart "$service_unit"
    fi
}

repair_service_mode() {
    reason=$1
    log "$reason; repairing $service_unit"

    systemctl reset-failed "$service_unit" 2>/dev/null || true
    systemctl restart "$service_unit"
}

if [ "$socket_managed" = true ]; then
    socket_state=$(systemctl show "$socket_unit" --property=SubState --value 2>/dev/null || true)

    if ! systemctl is-active --quiet "$socket_unit" 2>/dev/null; then
        repair_socket_mode "$socket_unit is not active"
    elif [ "$socket_state" != "listening" ] && [ "$socket_state" != "running" ]; then
        repair_socket_mode "$socket_unit is active with an unhealthy state: ${socket_state:-unknown}"
    elif systemctl is-failed --quiet "$service_unit" 2>/dev/null; then
        repair_socket_mode "$service_unit is failed"
    fi

    final_socket_state=$(systemctl show "$socket_unit" --property=SubState --value 2>/dev/null || true)
    if ! systemctl is-active --quiet "$socket_unit" 2>/dev/null || \
       { [ "$final_socket_state" != "listening" ] && [ "$final_socket_state" != "running" ]; }; then
        log "$socket_unit is still unhealthy after repair"
        exit 1
    fi
else
    service_state=$(systemctl show "$service_unit" --property=SubState --value 2>/dev/null || true)

    if ! systemctl is-active --quiet "$service_unit" 2>/dev/null; then
        repair_service_mode "$service_unit is not active"
    elif [ "$service_state" != "running" ]; then
        repair_service_mode "$service_unit is active but not running (state: ${service_state:-unknown})"
    fi

    if ! systemctl is-active --quiet "$service_unit" 2>/dev/null; then
        log "$service_unit is still unhealthy after repair"
        exit 1
    fi
fi

