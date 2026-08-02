# MCSM helper link protocol — v1

The wire contract between the helper mod (inside a Minecraft server) and the MCSM
agent. Both sides hand-write their types; the fixtures in `fixtures/` are the
shared source of truth and are parsed by **both** the Go and the Java test suites.

If you change anything here, change a fixture — otherwise the change is untested
by construction.

---

## 1. Connection

The mod dials the agent. The agent never dials the mod.

```
GET ws://127.0.0.1:8090/agent/v1/link/{server_id}
Authorization: Bearer {launch_token}
```

`{server_id}` and `{launch_token}` come from the environment the agent injected
when it spawned the server process:

| Variable | Meaning |
|---|---|
| `MCSM_AGENT_URL` | Base URL, e.g. `http://127.0.0.1:8090`. Mod rewrites the scheme to `ws`/`wss`. |
| `MCSM_SERVER_ID` | Which server this process is. |
| `MCSM_TOKEN` | Per-launch bearer token. Never logged, never written to disk by the mod. |

If any variable is absent the mod **disables itself silently** and the server runs
exactly as if it were not installed. That is the correct behaviour for a server
started outside the panel, and it must never be an error or a log warning louder
than a single debug line.

Authentication happens at the HTTP upgrade, not in a protocol frame, so a bad
token is rejected before a WebSocket session exists.

### Close codes

| Code | Meaning | Mod's reaction |
|---|---|---|
| `1000` / `1001` | Normal / agent going away | Reconnect with backoff |
| `1002` | Handshake failed for a transient reason (timeout, malformed first frame) | Reconnect with backoff |
| `4400` | Unsupported protocol version | **Stop permanently.** Do not retry; log once. |
| `4401` | Bad or expired token | **Stop permanently.** Retrying cannot help. |
| `4409` | Another live session already exists for this server id | Reconnect with backoff; log a warning |
| anything else | Unexpected | Reconnect with backoff |

`4400` is reserved for an actual version mismatch — it is the one close code that
tells the mod to give up for the rest of the server's life, so the agent must
never use it for a handshake that merely timed out or arrived garbled.

`4409` is retryable because the usual cause is the mod's own previous connection
not yet torn down on the agent side. The agent evicts any session that sends
nothing for **4× the heartbeat interval** (a healthy mod sends a snapshot every
heartbeat, so silence that long means the connection is dead), which bounds how
long a reconnect can keep colliding with its own ghost.

Reconnect backoff: 1s, doubling to a 60s ceiling, with jitter. The mod must never
reconnect in a tight loop and must never log more than once per backoff step.

---

## 2. Framing

One JSON object per WebSocket **text** frame. UTF-8. No binary frames, no
fragmentation of a logical message across frames.

Every frame:

```jsonc
{
  "v": 1,              // protocol version, integer
  "type": "snapshot",  // discriminator
  "ts": 1753800000000, // mod's wall clock, epoch millis (agent uses its own clock for storage)
  "id": "…",           // present only on rpc_request / rpc_response
  "data": { }          // type-specific payload
}
```

Unknown fields are **ignored, not rejected**, on both sides. Unknown `type`
values are ignored with a rate-limited debug log. This is what lets one side ship
ahead of the other without breaking the link.

`ts` is informational. The agent timestamps what it stores with its own clock,
because a server host with a skewed clock must not corrupt the metric series.

---

## 3. Message types

### 3.1 `hello` — mod → agent, first frame

```json
{
  "v": 1,
  "type": "hello",
  "ts": 1753800000000,
  "data": {
    "mod_version": "1.0.0",
    "mc_version": "26.2",
    "loader": "fabric",
    "loader_version": "0.19.3",
    "server_brand": "fabric",
    "capabilities": ["vitals", "player_events", "rpc", "command_exec"]
  }
}
```

`capabilities` is how the agent knows what this build can do without inspecting
versions. A future Paper adapter that cannot report MSPT simply omits `vitals`.

### 3.2 `welcome` — agent → mod, reply to `hello`

```json
{
  "v": 1,
  "type": "welcome",
  "ts": 1753800000123,
  "data": {
    "heartbeat_ms": 15000,
    "accepted_capabilities": ["vitals", "player_events", "rpc", "command_exec"],
    "max_frame_bytes": 262144
  }
}
```

The agent dictates `heartbeat_ms` so snapshots align with the existing metric
sample interval. The mod must honour it, not its own default.

`accepted_capabilities` is the intersection of what the mod announced and what
the agent understands — not an echo. A mod that announces a capability the agent
does not list back must not expect the agent to act on it.

`max_frame_bytes` is binding, not advisory. The agent enforces it as a read
limit, and a frame over it **closes the session** rather than arriving
truncated — so a mod that ignores the value and periodically exceeds it does not
lose one frame, it loses the link, and reconnects only to lose it again on the
next heartbeat. The mod therefore measures each snapshot before queueing it and
degrades to fit: `players.list` is trimmed first (`players.online` still carries
the true count), then `dimensions` is dropped. It also uses the value to bound
inbound frame reassembly, so a peer cannot decide how much of the server's heap
to consume.

If the agent will not accept the session it closes with a code from §1 instead of
sending `welcome`.

### 3.3 `snapshot` — mod → agent

Full authoritative state. Sent **immediately after `welcome`** and then every
`heartbeat_ms`. This is the reconcile anchor that makes dropped events harmless.

```json
{
  "v": 1,
  "type": "snapshot",
  "ts": 1753800015000,
  "data": {
    "uptime_ms": 3600000,
    "tps": { "m1": 19.98, "m5": 19.99, "m15": 20.0 },
    "mspt": { "avg": 8.4, "p50": 7.9, "p95": 14.2, "p99": 21.7, "max": 40.1 },
    "heap": { "used_mb": 2048, "committed_mb": 4096, "max_mb": 8192 },
    "gc": { "collections": 142, "time_ms": 3820 },
    "chunks": { "loaded": 1024 },
    "entities": { "total": 3150 },
    "dimensions": [
      { "id": "minecraft:overworld", "chunks": 812, "entities": 2400 }
    ],
    "players": {
      "online": 2,
      "max": 20,
      "list": [
        { "uuid": "…", "name": "…", "ping_ms": 42, "dimension": "minecraft:overworld" }
      ]
    }
  }
}
```

**The agent treats `players.list` as truth.** Any divergence from its
event-derived view is resolved in favour of the snapshot — that is the mechanism
by which a dropped `player_join` self-heals within one heartbeat.

### 3.4 `event` — mod → agent

Low-latency notifications. Best-effort: **may be dropped under backpressure.**
Never rely on an event for durable state; that is the snapshot's job.

```json
{ "v": 1, "type": "event", "ts": 1753800001000,
  "data": { "kind": "player_join", "uuid": "…", "name": "…" } }
```

| `kind` | Extra fields |
|---|---|
| `player_join` | `uuid`, `name` |
| `player_leave` | `uuid`, `name` |
| `player_death` | `uuid`, `name`, `message` (rendered death message) |
| `server_ready` | — (fired when the server finishes starting) |
| `server_stopping` | — |

Events produced while the link is down are held in the outbound queue and flushed
after the next `hello`, so `server_ready` — which is necessarily queued before the
first connection exists — is delivered rather than discarded. Frames belonging to
a session are dropped when *that* session ends, which also stops an
`rpc_response` outliving its request: the agent restarts correlation ids per
session, so a stale id could otherwise be matched against a live call.

### 3.5 `rpc_request` — agent → mod

```json
{ "v": 1, "type": "rpc_request", "id": "01J8…", "ts": 1753800002000,
  "data": { "method": "player.kick", "params": { "player": "…", "reason": "afk" } } }
```

`id` is opaque to the mod and echoed verbatim in the response.

### 3.6 `rpc_response` — mod → agent

Success:

```json
{ "v": 1, "type": "rpc_response", "id": "01J8…", "ts": 1753800002050,
  "data": { "ok": true, "result": { } } }
```

Failure:

```json
{ "v": 1, "type": "rpc_response", "id": "01J8…", "ts": 1753800002050,
  "data": { "ok": false, "error": { "code": "player_not_found", "message": "no player named …" } } }
```

Every `rpc_request` gets exactly one `rpc_response`. The mod must respond even on
internal failure — a missing response is a bug, and the agent's own timeout is a
backstop, not the design.

#### Error codes

| Code | Meaning |
|---|---|
| `unknown_method` | Not implemented by this build |
| `invalid_params` | Missing or malformed parameters |
| `player_not_found` | Named player is not online / not known |
| `timeout` | Work did not complete on the server thread in time |
| `unsupported` | Method exists but this platform cannot do it |
| `internal` | Anything else; `message` carries detail |

---

## 4. RPC methods (v1)

| Method | Params | Result |
|---|---|---|
| `player.kick` | `player`, `reason?` | `{}` |
| `player.ban` | `player`, `reason?` | `{}` |
| `player.pardon` | `player` | `{}` |
| `whitelist.add` | `player` | `{}` |
| `whitelist.remove` | `player` | `{}` |
| `whitelist.list` | — | `{ "players": ["…"] }` |
| `op.grant` | `player` | `{}` |
| `op.revoke` | `player` | `{}` |
| `message.broadcast` | `message` | `{}` |
| `message.player` | `player`, `message` | `{}` |
| `server.save` | `flush?` (bool) | `{}` |
| `server.stop` | — | `{}` |
| `command.exec` | `command` | `{ "output": ["…"], "success": true }` |

`command.exec` is the reason this protocol beats stdin: it returns what the
command actually replied. It grants no authority the existing panel console does
not already grant, since that console already writes arbitrary commands to stdin.

---

## 5. Threading and backpressure — normative

These are requirements, not suggestions. A helper mod that degrades the server it
is helping is worse than no mod.

1. **The server tick thread must never block on the link.** No socket I/O, no
   queue `put` that can wait, no lock held across a send.
2. Outbound frames go through a **bounded** queue (default 256). On overflow,
   **drop the oldest `event`** frames first; never drop a `snapshot` or an
   `rpc_response`. If the queue is full of non-droppable frames, drop the new
   frame and count it.
3. Work that must touch game state is scheduled onto the server thread
   (`server.execute(...)`) and its result is delivered back **asynchronously** by
   correlation id. `command.exec` has a server-side timeout (default 10s) after
   which the mod replies `timeout` — it does not wait forever and it does not
   leave the request unanswered.
4. Sampling for `snapshot` reads pre-aggregated counters. Nothing in the snapshot
   path may iterate all entities on the tick thread on demand; entity and chunk
   counts are gathered on the tick that precedes a heartbeat, cheaply.
5. All logging is rate-limited. A disconnected agent must not produce log spam,
   because the log is also what the panel shows the user. A condition that
   recurs every heartbeat — an unknown frame type, an oversized snapshot — is
   logged once per session, not once per occurrence.
6. Shutdown does not wait on the network. The mod gives the IO thread a bounded
   grace period to flush `server_stopping`, but only when a session is actually
   live and something is queued on it. A server whose agent was never reachable
   must stop exactly as fast as it would without the mod installed.

---

## 6. Fixtures

`fixtures/*.json` are canonical frames. Both test suites must, for each fixture:
parse it into the native type, re-serialize, and assert semantic equality with the
original. A field added on one side and not the other fails that side's test.

| File | Covers |
|---|---|
| `hello.json` | Handshake from mod |
| `welcome.json` | Handshake reply from agent |
| `snapshot_full.json` | Every vitals field populated |
| `snapshot_empty_server.json` | Zero players, zero entities — the boundary case that trips naive parsers |
| `event_player_join.json` | Join event |
| `event_player_death.json` | Event carrying a message string |
| `rpc_request_kick.json` | Agent → mod request |
| `rpc_response_ok.json` | Success reply |
| `rpc_response_error.json` | Failure reply with error code |
| `rpc_response_exec.json` | Captured command output |
