// Package hardening applies process-level protections at startup.
package hardening

// Apply makes the process non-dumpable where the platform supports it, and
// reports whether it did.
//
// On Linux, /proc/<pid>/environ and /proc/<pid>/mem of a dumpable process are
// readable by any other process running as the same user. Minecraft servers
// (and so every mod and plugin) may run as that user, and this process holds
// secrets in its environment — the agent token, the JWT signing key. A
// non-dumpable process has those /proc entries owned by root and refuses
// ptrace from its own user, which closes that path without changing anything
// else about how it runs.
func Apply() (bool, error) { return apply() }
