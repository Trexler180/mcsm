# Helper mod — build status

Last updated 2026-07-30. Design rationale is in `DECISIONS.md`; the wire contract
is in `PROTOCOL.md`.

---

## Current state: builds, links, and is proven end to end

| Check | Result |
|---|---|
| `cd apps/mod && ./gradlew build` | **passes** — jar 53,504 bytes (mod v1.0.2) |
| `cd apps/mod && ./gradlew test` | **12 pass across 2 classes** |
| Jar reproducibility | **byte-identical across clean rebuilds** (verified, sha256 `e78addc5…`) |
| `cd apps/agent && go build ./... && go vet ./...` | **clean** |
| `cd apps/agent && go test ./...` | **220 pass across 12 packages** |
| `cd apps/api && go build/vet/test` | **169 pass across 23 packages** |
| `cd apps/web && pnpm typecheck && pnpm lint && pnpm test` | **clean; 48 tests pass** |

The reproducibility result matters: it was flagged as the main risk to the
commit-the-jar + CI-hash scheme, and it is now measured rather than assumed. The
CI gate is therefore real.

---

## What exists

### Mod (`apps/mod`)

Platform-agnostic core with no Minecraft imports — `LinkConfig`,
`OutboundQueue`, `Messages`, `Frames`, `ServerFacade`, `RpcDispatcher`,
`LinkClient` — plus `fabric/FabricAdapter` (the only class that imports
Minecraft) and `fabric/TickStats`. Zero mixins; every hook is a public Fabric API
event. No bundled dependencies: the WebSocket client is `java.net.http.WebSocket`
from the JDK and JSON is Minecraft's own Gson.

### Agent (`apps/agent`)

- `internal/link` — wire types, session (RPC correlation, timeouts, single-writer
  discipline), registry (auth before upgrade, handshake, duplicate rejection),
  `MemorySink`.
- `internal/process/linktoken.go` — per-launch token minting and constant-time
  validation.
- `internal/helperjar` — `go:embed`s the built jar, exposes bytes + sha256.
- Spawn path injects `MCSM_AGENT_URL` / `MCSM_SERVER_ID` / `MCSM_TOKEN`; the
  token is persisted in run state so a **reattached** server can re-authenticate.
- `runstate.go` mode tightened **0644 → 0600** now that it holds a credential.
- Link endpoint mounted at `/agent/v1/link/{id}`, deliberately outside the
  agent-token middleware group.

### Tests

- Golden-fixture round-trip on **both** sides now, reading the same
  `fixtures/*.json` — including the empty-collection boundary case. The Java half
  was verified by falsification: adding a field to a payload class makes it fail.
- `TickStatsTest` pins the single-pass `rates()` against the per-window `tps()`
  they replaced, including after the 900-bucket ring wraps, and asserts a long
  stall stays cheap to catch up from.
- Nine end-to-end tests driving the real registry with a fake mod over a real
  loopback WebSocket: handshake, bad token, missing token, duplicate session,
  RPC round trip, call-on-closed-session, event delivery, malformed-frame
  tolerance, and disconnect-ordering.
- Six more over the same harness for `Registry.ExecCommand`, one per row of the
  retryability table below, plus `linkHostForTLS` over generated certificates
  with varying SANs. The two that encode the actual bugs — a timeout must not
  look retryable, a loopback literal must not be advertised under a cert that
  does not cover it — were each verified by falsification.

### CI

A `mod` job builds with JDK 25 and fails if the committed jar no longer matches
a fresh build.

---

## Integration: it is baked in and toggleable

No manual jar handling. The mod ships inside `mcsm-agent` and the panel turns it
on and off.

- **Agent** (`internal/process/helpermod.go`) reconciles the jar in
  `<server>/mods/` immediately before the JVM starts: installs when enabled,
  replaces a stale build (compared by sha256), and **deletes it when disabled**.
  Writes are atomic (temp + rename) so a crash cannot leave a half-written jar
  that Fabric would refuse to load. Fabric-only; other platforms are skipped
  silently. An install failure never blocks a server from starting.
- **Config** flows as `StartConfig.HelperMod`, and the launch token is only
  minted when the mod is actually enabled.
- **API** stores the choice in the existing server `settings` JSON — no
  migration. `GET`/`POST /servers/{id}/helper-mod`, gated at view/settings
  access respectively. Default is on for Fabric servers the panel created, off
  for imported ones (an imported directory is someone's existing instance).
- **Reconcile** skips the jar, so the panel never adopts it as a user mod,
  never offers a bogus update for it, and never lets it be deleted out from
  under the feature.
- **UI**: a Helper Mod panel on the server Options tab, disabled with an
  explanation on non-Fabric servers.

## Link-lifecycle hardening (v1.0.1)

A review pass fixed a cluster of failure modes that only bite against a live
server:

- **Hello ordering is structural.** The mod sends `hello` synchronously on the
  IO thread before draining the queue, so a player event fired between
  connect and handshake can no longer beat `hello` onto the wire and get the
  session closed as a bad handshake.
- **`4400` is reserved for a real version mismatch.** The agent closes a
  merely-garbled or timed-out handshake with `1002`, which the mod retries.
  Previously any handshake hiccup produced `4400` and permanently disabled the
  mod for the rest of the server's life.
- **`4409` (duplicate) is retryable.** The agent evicts sessions silent for 4×
  the heartbeat, so a reconnect colliding with its own half-dead predecessor
  resolves via ordinary backoff instead of giving up forever.
- **Disconnect clears derived state immediately.** `MemorySink` gained an
  `OnDisconnectFunc` seam wired to `Manager.ClearLinkRoster`, and
  `RefreshPlayers` requires a running instance before serving the link roster —
  a stopped server can no longer report players for up to 45s.
- **`server_stopping` is actually delivered.** `LinkClient.stop()` gives the IO
  thread a bounded grace period to flush the queue before closing the socket.
- **`accepted_capabilities` is a real intersection**, not an echo of whatever
  the mod announced.
- **Restart applies config overrides.** `Manager.RestartWith` now uses the
  configuration the API sends, so launch-time settings changed since the last
  start (the helper-mod toggle) genuinely take effect on a plain restart.

## Audit pass (v1.0.2)

A read of the whole path — mod, link, agent consumption — against a live-server
mental model rather than the test harness. Every item below was a real defect,
not a style note.

- **`server_ready` was never once delivered.** The IO thread cleared the outbound
  queue on connect, and that event is queued microseconds after `start()` —
  always before the socket finishes opening. The clear now happens when a
  session *ends*, which is where it was actually wanted: it discards frames
  belonging to the dead session while letting anything produced before or between
  sessions through. That also closes a correlation hazard, since the agent
  restarts RPC ids per session and a surviving stale `rpc_response` could have
  been matched against a live call.
- **Every server stop paid 1.5 s for nothing.** `stop()` joined the IO thread
  unconditionally to flush `server_stopping`. With no agent reachable — the
  common case for a server started outside the panel — that thread is asleep in
  backoff with nothing to send, so the join always ran to timeout, on the server
  thread. Now it waits only when a session is live *and* something is queued.
- **A thread leaked per reconnect attempt.** `HttpClient` was built fresh inside
  the connect loop and never closed. Each one eagerly starts a selector thread
  and an executor released only on close or GC, so a day-long agent outage at the
  60 s backoff ceiling churned ~1,440 of them. One client is now built on first
  use and shut down when the loop exits.
- **`max_frame_bytes` was negotiated and then ignored.** Inbound, fragmented text
  was appended to an unbounded buffer — the peer chose how much heap to take.
  Outbound, nothing measured a snapshot, and exceeding the agent's read limit
  does not truncate the frame, it closes the session; the next heartbeat would
  rebuild the same oversized frame, so a large enough server would have sat in a
  permanent reconnect loop with only a debug line to show for it. Both directions
  now honour the value, and an oversized snapshot degrades by trimming
  `players.list` (see PROTOCOL.md §3.2).
- **One thrown exception could stop all snapshots.** `scheduleAtFixedRate`
  cancels a repeating task the first time it throws, silently. The body called
  `facade.submit` outside its own try.
- **The agent leaked the connection on the ordinary exit path.** Every early
  return in `Registry.Handle` closed the socket; the path where `serve` returns
  did not. Now `defer conn.CloseNow()`.
- **Disconnect was announced before the registry slot was freed.** Go defers run
  last-registered-first, which put `sink.OnDisconnect` ahead of `unregister` — so
  a consumer reacting to "the mod is gone" could look at the registry, still find
  a live session, and re-derive what it had just been told to drop. Pinned by
  `TestDisconnectFiresAfterTheRegistrySlotIsFreed`.
- **`TickStats` did three passes for three nested windows.** m1 ⊂ m5 ⊂ m15, so
  one backward walk answers all three: 1,260 bucket reads and as many `floorMod`
  divisions per heartbeat became 900 reads and none. Separately, the gap-fill
  loop after a stall ran once per skipped second without bound — an hour-long
  freeze meant 3,600 iterations on the tick thread of a server that had just
  proven it has no headroom. It is clamped to one window, past which every bucket
  is already zeroed.

## Consumption pass (agent-side; mod unchanged at v1.0.2)

Closing the gap between "the mod reports it" and "the manager uses it". No Java
changed in this pass, so the jar and its CI hash gate are untouched.

- **Vitals are persisted and charted.** Migration `022_server_metrics_vitals`
  adds `tps` / `mspt_avg` / `mspt_p95` to `server_metrics` and their rollup
  counterparts (`tps_min`, `mspt_p95_max`, `vitals_samples`) to
  `server_metrics_hourly`. The agent exposes `/vitals`, the poller reads it once
  a minute as a bounded extra call, and the panel renders a vitals panel and a
  tick-health chart. Every vitals column is NULLable on purpose: NULL is "no mod
  data covered this sample", which `AVG`/`COUNT` skip — a genuine TPS of 0
  during a freeze is a different fact and must not be averaged together with it.
- **Tick rate is on the live metrics stream.** The 2s WebSocket that feeds the
  CPU and RAM sparklines now also carries `tps` and `tick_seq` when the mod has
  reported inside the last four heartbeats (`handlers.tickFields`), so the
  dashboard draws a live TPS sparkline beside them. `ProxyMetrics` forwards
  frames verbatim, so the API is untouched.

  `tick_seq` counts snapshots received rather than stamping a time, and the
  browser appends a point only when it moves. The stream ticks every 2s while
  the mod reports every 15s: plotting every frame would draw a staircase
  claiming a resolution the data does not have, and keying on `SnapshotAt`
  instead would silently drop one of two snapshots landing in the same
  millisecond. Absent fields mean no card at all, so a vanilla server's
  dashboard is byte-for-byte what it was.
- **`command.exec` has a production caller.** `Manager.ExecCommand` prefers a
  linked mod and falls back to stdin, wired at `cmd/agent/main.go`. See below —
  the fallback rule is the whole design.
- **`MemorySink.Forget` evicts a purged server.** Hung off a new
  `Manager.OnUnregisterFunc`, which fires from `Unregister` — the single point a
  purge funnels through.
- **TLS advertises a host the certificate covers** (`cmd/agent/linkhost.go`).

### The command path, and why the fallback is narrow

`ExecCommand` is deliberately *not* layered underneath `SendCommand`.
`ApplyPlayerAction` issues its kick/ban/op commands through `SendCommand`, and
routing those over the link was explicitly out of scope; folding the two together
would reroute moderation through the RPC dispatcher as a side effect of touching
the console. `TestPlayerActionsDoNotUseTheLink` pins the separation.

The fallback rule matters more than the happy path, because the fallback is
"run the same command again over stdin":

| Failure | Retryable? | Why |
|---|---|---|
| No session | yes | The mod never existed |
| Session closed before write | yes | `ErrNotDelivered` — nothing reached the wire |
| Write error | yes | A partial frame cannot parse as an envelope |
| Mod refused the method | yes | An older build without `command.exec` executed nothing |
| **Timeout** | **no** | The command may already have run |
| Session died mid-call | no | Same — the reply was lost, not the command |
| Undecodable result | no | The command ran; only our reading of the reply failed |

`link.ErrNoSession` and `link.ErrNotDelivered` are the only errors that become
`process.ErrLinkUnavailable`, which is the sole licence to retry. Getting this
wrong in the permissive direction executes an operator's command twice, so each
row above is pinned by its own test rather than covered by one happy path; the
timeout row was verified by falsification (making it wrap `ErrNotDelivered`
fails `TestExecCommandTimeoutIsNotRetryable`).

A command the server ran and *rejected* is not an error here at all: it comes
back as `success: false`, a fact about the command rather than about the link.
The agent's `POST /command` answers 200 with `via_mod` / `success` / `output`
rather than a 4xx, and `via_mod` is what says whether the other two mean
anything — stdin is fire-and-forget and can only report that the write landed.
The API client discards the body, so this is additive.

### TLS host selection

`linkHostForTLS` picks the advertised host from the certificate's SANs, in order:
the preferred host if covered (an IP SAN of `127.0.0.1` — the properly
provisioned case), else `localhost` if covered (still loopback, token still off
the network), else the first covered DNS name (verifies, but the dial may now
leave the machine, which is logged), else nothing — which leaves
`LinkAgentURL` empty and keeps servers dormant on the scrape/stdin paths rather
than having every one of them retry a handshake that cannot succeed.

## First live run, and the bug it found (2026-07-31)

Enabled on a real Fabric 26.2 server. It linked
(`mc=26.2 loader=fabric/0.19.3 mod=1.0.2`) and delivered vitals on every
one-minute sample for 2h37m with no reconnects — TPS 20.0, MSPT avg ~0.7 ms,
p95 ~0.9–1.05 ms. That retires "compile-checked only" for the telemetry half of
`FabricAdapter`: the tick sampling, heap and player readings, heartbeat, frame
sizing and the whole ingest → poller → rollup chain now have live evidence.

Then it stopped dead at 04:00, and the cause was not in the mod at all.

A **"Update Mods" scheduled task** (`0 4 * * *`) restarts the server through the
auto-update engine, and that engine built its own agent start payload by hand —
`agent.StartConfig(...)` with no helper-mod flag. A *missing* `helper_mod` is
not "unspecified", it is "disabled", so the agent did what disabling means: it
deleted the jar from `mods/` and injected no link env vars. The panel went on
reporting the toggle as on. The same call also dropped `no_install`, which on an
imported server invites the agent to re-provision a runtime over the user's own
files — the more destructive half of the same omission, unhit only by luck.
`internal/migrate/engine.go` had it too.

Fixed by making one builder, `agent.StartConfigForServer(srv *store.Server)`,
the only way to construct that payload, and routing all four start paths through
it. The helper-mod predicates moved next to it so the answer the UI shows and
the answer the start payload carries cannot drift.

The unit tests matter less here than the structural one. A missing field is
indistinguishable from a deliberate "off", and the broken path never called the
builder at all — so no test *of* the builder could have caught it.
`TestEveryStartPathUsesTheSharedBuilder` asserts the property that actually
holds the line: outside the `agent` package, nobody calls the low-level
`StartConfig` directly. Verified by falsification — restoring the old call in
the auto-update engine fails the test and names the file.

## What is still open

1. **Player actions still take the console/file path.** `player.kick`,
   `player.ban`, `whitelist.*`, `op.*` and the rest of `RpcDispatcher` remain
   reachable only from tests. This was a deliberate scoping decision, not an
   oversight: until the adapter has run against a real server (item 2), putting
   moderation on it trades a working path for an unproven one. The wiring point
   is `ApplyPlayerAction`, and it would want the same delivered/not-delivered
   discipline `ExecCommand` now has.
2. **Real-server e2e.** Never run against an actual Fabric 26.2 server. The
   handshake, RPC and command paths are proven against a fake peer; the
   *adapter* — every Minecraft call in `FabricAdapter` — is compile-checked only.
   This is now the gating item for everything else: it is the one risk that
   cannot be retired by more tests against a fake mod.

---

## Minecraft 26.2 API notes

Obtained by `javap` against the jar Loom downloaded, not from memory. 26.x
renamed a lot; do not write against 1.21-era tutorials.

| Thing | 26.2 |
|---|---|
| Obfuscation | **None.** No yarn, no mappings, plain `implementation` |
| `ResourceLocation` | renamed **`Identifier`**; `ResourceKey.identifier()`, not `.location()` |
| Op/whitelist identity | **`NameAndId`** (record of `UUID`/`String`), not `GameProfile` |
| Permissions | `net.minecraft.server.permissions.LevelBasedPermissionSet` |
| Tick timing | `getTickTimesNanos()`, `getAverageTickTimeNanos()` |
| Player ping | `player.connection.latency()` on `ServerCommonPacketListenerImpl` |
| Entities | `ServerLevel.getAllEntities()` is public; there is **no** O(1) counter |
| Commands | `Commands.getDispatcher()` + `parse`/`execute` gives real success/failure; `performPrefixedCommand` swallows it |
| Gradle | `dirMode`/`fileMode` removed in 9 → `dirPermissions { unix(…) }` |

## Known deviations from the spec

`PROTOCOL.md` §5 says nothing in the snapshot path may iterate all entities on
the tick thread. `FabricAdapter.countEntities` **does** iterate, once per
heartbeat (15s), not per tick.

Re-examined in the v1.0.2 audit rather than carried forward on the old note, and
the conclusion is to keep it:

- There is still no reachable counter. `EntityLookup.count()` and
  `PersistentEntitySectionManager.count()` both exist and are `public` in 26.2,
  but `ServerLevel.entityManager` is `private` and `Level.getEntities()` is
  `protected`, so neither is callable from outside without a mixin or reflection
  — and zero mixins is a deliberate property of this build, not an accident.
  (Verified by `javap` against the Loom-resolved jar, not from memory.)
- `ServerEntityEvents` in Fabric API 0.156.0+26.2 exposes `ENTITY_LOAD` and
  **no** `ENTITY_UNLOAD`, so an incrementally-maintained counter cannot be kept
  accurate from public events either.
- The cost does not justify reaching for either. `getAllEntities()` walks an
  `Int2ObjectLinkedOpenHashMap`'s values — O(entities) in pointer chases, no
  allocation — so a 5,000-entity server spends on the order of a hundred
  microseconds once every 15 seconds. Sampling it on a slower cycle, the fix the
  previous note proposed, would trade snapshot freshness for a saving that does
  not register against a 50 ms tick budget.

The effort went to the things in the same path that were actually measurable
instead: see the `TickStats` entry in the audit pass above.
