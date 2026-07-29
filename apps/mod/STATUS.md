# Helper mod — build status

Last updated 2026-07-29. Design rationale is in `DECISIONS.md`; the wire contract
is in `PROTOCOL.md`.

---

## Current state: builds, links, and is proven end to end

| Check | Result |
|---|---|
| `cd apps/mod && ./gradlew build` | **passes** — jar 50,306 bytes |
| Jar reproducibility | **byte-identical across clean rebuilds** (verified, sha256 `95e88abf…`) |
| `cd apps/agent && go build ./... && go vet ./...` | **clean** |
| `cd apps/agent && go test ./...` | **158 pass across 11 packages** |
| `cd apps/api && go build/vet/test` | **160 pass across 23 packages** |
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

- Golden-fixture round-trip (Go), including the empty-collection boundary case.
- Eight end-to-end tests driving the real registry with a fake mod over a real
  loopback WebSocket: handshake, bad token, missing token, duplicate session,
  RPC round trip, call-on-closed-session, event delivery, and malformed-frame
  tolerance.

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

## What is still open

1. **Metrics columns.** No migration yet for `tps` / `mspt_p95` /
   `heap_used_mb` / `chunks` / `entities`, and `MemorySink` does not forward to
   the metrics pipeline (the callback hook exists, unused). So vitals arrive at
   the agent and are held in memory, but are not charted or persisted.
2. **Nothing consumes mod data yet.** `Registry.Connected()` is the intended
   branch point for "prefer the mod, fall back to scraping", but the players and
   status handlers still take the scraping path unconditionally. This is the
   next piece that turns collected data into visible value.
3. **Java-side fixture tests.** The Go half of the shared-fixture guard exists;
   the Java half does not, so Java-side drift is currently uncaught.
4. **Real-server e2e.** Never run against an actual Fabric 26.2 server. The
   handshake and RPC paths are proven against a fake peer; the *adapter* — every
   Minecraft call in `FabricAdapter` — is compile-checked only.

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
the tick thread. `FabricAdapter.countEntities` **does** iterate, because 26.2
exposes no counter. It is a plain allocation-free iteration that runs once per
heartbeat (15s), not per tick. Flagged rather than silently accepted — if entity
counts prove costly on a large server, the fix is to sample them on a slower
cycle than the rest of the snapshot.
