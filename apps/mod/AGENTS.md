# Adding support for a new Minecraft version

Instructions for an agent (or person) asked to "make the helper mod support
X" — e.g. 26.3. Read `DECISIONS.md` for why the mod is built this way before
changing anything structural.

The goal is that this repo is the only thing you touch. Publishing a build makes
every deployed manager pick it up automatically; **no manager release is
involved, and you must never need to modify the manager to add a version.**

---

## Do not guess API facts

Minecraft is post-training-cutoff for most models and **26.x renamed a great
deal**. Names from 1.21-era tutorials will not compile. Verified examples:

| 1.21.x | 26.x |
|---|---|
| `ResourceLocation`, `.location()` | `Identifier`, `.identifier()` |
| `GameProfile` for op/whitelist | `NameAndId` |
| — | `LevelBasedPermissionSet` for permissions |

Get facts from the artifact, not from memory:

```bash
# Versions, loader, Fabric API, Loom — all authoritative
curl -s https://meta.fabricmc.net/v2/versions/game | head -40
curl -s https://meta.fabricmc.net/v2/versions/loader | head -20
curl -s 'https://api.modrinth.com/v2/project/fabric-api/version?game_versions=%5B%2226.3%22%5D'
curl -s https://raw.githubusercontent.com/FabricMC/fabric-example-mod/<VERSION>/gradle.properties

# Required Java version for a game version
curl -s https://piston-meta.mojang.com/mc/game/version_manifest_v2.json

# Real method signatures, once Loom has downloaded the jar
javap -cp ~/.gradle/caches/fabric-loom/<VERSION>/minecraft-common.jar \
  net.minecraft.server.MinecraftServer | grep -i tick
```

If a symbol cannot be confirmed, **verify it with `javap` rather than writing
code you hope compiles.**

## Steps

1. **Check whether the current source already builds.** Because Minecraft ships
   deobfuscated from 26.x, a new release in the same series often needs only a
   version bump in `gradle.properties`. Try that first.

2. **Bump `gradle.properties`** — `minecraft_version`, `fabric_api_version`,
   `loader_version`, `loom_version`. Take every value from the sources above.
   Note the official `fabric-example-mod` has a branch per Minecraft version.

3. **Build.** `./gradlew build`. Fix compile errors using `javap`, not guesses.

4. **Only if the API diverges** (as it does between 1.21.x and 26.x) add a
   per-version source variant. `com.mcsm.helper.core` is deliberately free of
   Minecraft imports and must be shared unchanged — **only
   `fabric/FabricAdapter` may fork.** If you find yourself editing `core/` to
   support a game version, stop: something is wrong.

5. **Do not change the wire protocol** to add a game version. `PROTOCOL.md` and
   `fixtures/` are a contract with agents already deployed. If a change is truly
   required, it is a new `protocol_version` and a new index entry — never an
   edit to an existing one.

6. **Verify reproducibility.** Build twice from clean and confirm identical
   sha256. Non-reproducible jars break the manager's verification.

7. **Publish.** Tag, let CI build and attach the jar to a GitHub release, and
   regenerate `versions.json`. See `INDEX-FORMAT.md` — especially the rules
   about never mutating a released entry.

## Definition of done

- `./gradlew build` passes.
- Two clean builds produce identical sha256.
- `versions.json` has a **new** entry; no existing entry was modified or removed.
- `minecraft_versions` lists only versions actually built and tested against.
- `protocol_version` is unchanged unless the wire protocol genuinely changed.
- Fixtures in `fixtures/` are untouched, or changed deliberately with the
  protocol version bumped.

## What breaks users if you get it wrong

- **Wrong `minecraft_versions`** → Fabric treats the unsatisfied dependency as
  fatal and **servers will not boot**. This is the worst failure mode here; the
  list must be exact.
- **Wrong `sha256`** → every agent rejects the download and silently keeps its
  old build.
- **Mutated release entry** → agents that cached the old digest see what looks
  like tampering.
- **Bumped `protocol_version` unnecessarily** → older agents stop installing
  your build entirely.
