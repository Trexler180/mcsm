# SSH watchdog

This watchdog repairs the OpenSSH systemd units on a Linux production host.
Every 10 seconds it checks the configured SSH mode:

- With systemd socket activation, `ssh.socket` must be active and in the
  `listening` or `running` state. A failed daemon is also repaired.
- Without socket activation, `ssh.service` (or `sshd.service`) must be active
  and running.
- A service drop-in sets `Restart=always`, so systemd immediately restarts an
  SSH daemon that exits while the watchdog covers failed listening sockets.

It is installed and kept in sync by host provisioning (`deploy/provision.sh`,
run by `Deploy-Dashboard.ps1 -Part host`), which also attaches the
`Restart=always` drop-in to whichever of `ssh.service`/`sshd.service` is
installed. Provisioning never restarts SSH itself. Verify it with:

```sh
systemctl status ssh-watchdog.timer
systemctl status ssh.socket ssh.service
journalctl -u ssh-watchdog.service --since today
```

The watchdog runs on the production host, so it cannot recover a stopped VPS,
a broken network route/firewall, or a failed systemd/PID 1. Keep the provider's
out-of-band console available for those cases.

