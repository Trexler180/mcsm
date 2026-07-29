# Build index format (`versions.json`)

The manager discovers helper mod builds by fetching a single JSON document. This
is the contract between the mod's release process and every deployed agent.

The agent reads it via `MCSM_HELPER_INDEX_URL`, typically a raw GitHub URL such
as `https://raw.githubusercontent.com/<owner>/<repo>/main/versions.json`, or a
release asset. With that variable unset the agent uses only the build embedded in
its binary.

Consumer: `apps/agent/internal/helpermod`.

---

## Document

```json
{
  "schema_version": 1,
  "builds": [
    {
      "mod_version": "1.1.0",
      "loader": "fabric",
      "minecraft_versions": ["26.3", "26.3.1"],
      "protocol_version": 1,
      "url": "https://github.com/<owner>/<repo>/releases/download/v1.1.0/mcsm-helper-26.3.jar",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "size": 51234
    }
  ]
}
```

## Fields

| Field | Meaning |
|---|---|
| `schema_version` | Format version. An agent that does not recognise it **ignores the whole index** and falls back to its embedded build rather than guessing. |
| `mod_version` | The build's own version. Shown to users; not used for matching. |
| `loader` | `fabric` today. Matched case-insensitively. |
| `minecraft_versions` | **Exact** game versions this jar supports. |
| `protocol_version` | The link protocol this build speaks. |
| `url` | **https only.** Plaintext URLs are refused outright. |
| `sha256` | Lowercase hex digest of the jar. Mismatches are discarded and never cached. |
| `size` | Bytes. Downloads exceeding it are rejected. |

## Why exact versions and not ranges

A range expression needs a parser, and a parser is somewhere for a subtle bug to
live. The consequence of getting it wrong is not a missing feature — Fabric
Loader treats an unsatisfied dependency as **fatal**, so a jar installed onto the
wrong version stops that server from booting.

The build that produced the jar knows exactly which versions it compiled and was
tested against, so it states them. Supporting `26.3.1` means adding it to the
list and republishing, which costs nothing now that publishing is decoupled from
the manager's release cycle.

## Why `protocol_version` exists

Embedding the jar in the agent made version skew impossible — one binary, one
jar. Publishing independently brings skew back: a mod release could speak a
protocol an older agent has never heard of.

So each build declares its protocol, and **an agent installs only builds it can
actually talk to**. When several builds match a Minecraft version, the agent
picks the highest protocol it understands.

The practical rule: **a protocol change means a new entry, never an edit to an
existing one.** Agents in the field depend on the old entry continuing to exist.

## Selection

For a server, the agent:

1. Fetches the index (cached for 6h; a stale cached copy is used when the network
   is down).
2. Keeps builds where `loader` matches, `minecraft_versions` contains the
   server's exact version, and `protocol_version` ≤ what the agent speaks.
3. Picks the highest remaining `protocol_version`.
4. Downloads, verifies `sha256`, caches by digest, installs.
5. Falls back to the embedded build if any of that fails and the embedded build
   covers the version.

## Failure semantics — important

The agent distinguishes two outcomes that look similar and must never be
conflated:

- **Index readable, no matching build** → authoritative. Any installed helper jar
  is removed, because leaving it would break the next boot.
- **Index unreachable** → unknown. The installed jar is left **untouched**.

Publishing a malformed index therefore degrades to "keep using what you have",
not "delete everyone's mod".

## Rules for publishing

1. **Never change a released entry's `sha256` or `url`.** Agents cache by digest;
   a changed artifact under an old identity is indistinguishable from tampering
   and will be rejected.
2. **Add, don't mutate.** Superseding a build means a new entry.
3. **Keep old entries.** Removing one strands every agent still on that
   Minecraft version.
4. **Generate this file from the build**, never hand-edit it. The digest and size
   must come from the artifact that was actually uploaded.
