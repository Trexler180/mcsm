# Remote MCP hardening evidence context

Analysis target: the ServerManager source tree at revision
`f27626beb3e383f22e4f7b75137701097a916615`, **plus the uncommitted scoped
access-key foundation present in the working tree**. That foundation is the
starting point of this phase, not an accident, so the evidence below is read
from the working tree rather than from the committed revision.

## Source revision and drift

| Item | Value |
| --- | --- |
| Branch | `feature/secure-remote-mcp` |
| Committed revision | `f27626beb3e383f22e4f7b75137701097a916615` |
| Working-tree drift | dirty — see below |
| Evidence manifest digest | `8a71009c033820b467ae6aaa14f9c92bd065b30192a807d04b8f63c059271a70` |

Drift relative to the committed revision, all of it the scoped access-key work
this phase builds on:

- Modified: `apps/api/internal/api/handlers/{auth,helpers,players,server_members,servers,tasks}.go`,
  `apps/api/internal/api/handlers/settings_test.go`,
  `apps/api/internal/api/middleware/ratelimit.go`,
  `apps/api/internal/api/{router,server_access,server_access_test}.go`,
  `apps/api/internal/auth/{middleware,middleware_test}.go`,
  `apps/api/internal/store/{audit,users}.go`,
  `apps/api/migrations/migrations_test.go`,
  `apps/web/src/components/settings/security.tsx`,
  `apps/web/src/lib/{api,types}.ts`, `apps/web/src/routes/audit.page.tsx`,
  `docs/{operations,security}.md`.
- Added: `apps/api/internal/api/access_keys_test.go`,
  `apps/api/internal/api/handlers/{apikeys,apikeys_test,server_scopes_test}.go`,
  `apps/api/internal/api/middleware/ratelimit_test.go`,
  `apps/api/internal/auth/machine.go`,
  `apps/api/internal/store/{apikeys,apikeys_test}.go`,
  `apps/api/migrations/026_agent_access_keys.sql`,
  `apps/web/src/components/settings/{access-keys,access-keys.test}.tsx`,
  `docs/hardening/agent-access/**`.
- Untracked and **out of scope**: `deploy/` (user-owned; not inspected, not
  modified).

## Evidence inventory

| Evidence | Reader-facing title | Path | SHA-256 | What it establishes |
| --- | --- | --- | --- | --- |
| `E01` | Documented credential boundary | `docs/security.md` | `2634e2451c92accec654af01b1fa03e4479fda857be57efa7ae9f540294fbcfa` | The panel's supported credentials are browser JWTs, single-use tickets, and now scoped access keys; no interactive delegation flow exists. |
| `E02` | Scoped access-key store | `apps/api/internal/store/apikeys.go` | `2375a0999aca025c8ee1136e5b88ff0a4b3a6745aa9686518d0f354a83819f71` | Hash-only storage, mandatory bounded expiry, soft revocation, uniform invalid-credential error, and throttled usage metadata already exist and are reusable patterns. |
| `E03` | Bearer authentication boundary | `apps/api/internal/auth/middleware.go` | `dd27483033dd899540db2ab847468e9649f5a64efdf3bab390a72f53e49c4d4e` | A reserved `mcsm_pat_` prefix separates machine bearers from JWTs; machine credentials are header-only and never mint tickets. |
| `E04` | Machine authorization intersection | `apps/api/internal/api/server_access.go` | `74e9d4a412fc455c4564db6fa6f5f812c783b9514a7a37e6a93fba05689c4b83` | `serverAccessGate` evaluates key bounds *before* the global-admin bypass, and `machineBoundary` confines machine principals to `/api/v1/servers`. |
| `E05` | Route/permission map | `apps/api/internal/api/router.go` | `39f440e8cb8de1b6dc0be071f92f8460f8b23570fe5a78493f6f3dd33a14a311` | Every dangerous server operation already has a granular permission gate that a tool facade can reuse instead of inventing a second policy vocabulary. |
| `E06` | Step-up reauthentication pattern | `apps/api/internal/api/handlers/apikeys.go` | `bf9d6024d9baff02f0b49fb7cb5e65f2e9d6974938902b9895acfcc3ce3c3891` | Password + enabled-TOTP re-auth with per-IP and per-account throttling, generic failures, and "caller must currently hold what they delegate" is implemented and testable. |
| `E07` | Permission lattice | `apps/api/internal/store/models.go` | `39ea7f37b955943880eb6677054ab467efda4884f31433919b3677e302afd69b` | Groups and leaves (`power.start`, `power.stop`, `power.restart`, …) give a ready-made scope vocabulary with well-defined subsumption. |
| `E08` | Audit attribution | `apps/api/internal/store/audit.go` | `18dafc371ec48cb6bf2b491780313e21506ec267ab6693d70f83c522db56184e` | `LogActionWithKey` already names a human owner plus the machine credential; it needs one more actor kind, not a new mechanism. |
| `E09` | Per-caller rate identity | `apps/api/internal/api/middleware/ratelimit.go` | `1bd906f6a85473f118d102d23022d2ee2cebd8e7a449d8758b1aeda4f785a98e` | Authenticated traffic is bucketed per key/user/IP, but the limiter is mounted *inside* the authenticated group, so unauthenticated OAuth endpoints would have no budget of their own. |
| `E10` | Diagnostic data sources | `apps/api/internal/store/ops.go` | `c7741d75bfbe1e20e2549f27d1dc3cbc281b9ca7baf05429590e53668d74976d` | Indexed log events carry server-authored text (`message`) straight from Minecraft output — the primary prompt-injection carrier in any diagnosis tool. |
| `E11` | Server lifecycle handlers | `apps/api/internal/api/handlers/servers.go` | `cad85895fc92360bc08ddfe1832e42bee69e1b39920d1c6125bfb14d00025974` | Start/stop/restart already persist intent, audit, and tolerate long agent calls; an approval queue must reuse them rather than re-implement lifecycle semantics. |
| `E12` | Node worker client | `apps/api/internal/agent/client.go` | `380c5bb81b320cc2942a0c48c3813966906ed4bc823b9bd024be6e223b85a532` | Status/stats/vitals are read through the API's decrypted node token; nothing model-facing may ever receive that token or a raw agent URL. |

## Evidence limitations

- This is a source-derived design. No vulnerability has been demonstrated, no
  MCP client has been observed against this codebase, and no measured latency,
  token-volume, or concurrency figure was supplied.
- Claude Code and Codex OAuth behaviour is taken from the MCP specification and
  the official `github.com/modelcontextprotocol/go-sdk` (v1.7.0, which
  negotiates protocol versions `2024-11-05` through `2026-07-28`). Any
  client-specific quirk is a hypothesis until exercised against a real client;
  the implementation plan therefore requires a protocol smoke test and records
  a documented fallback.
- The intended clients are **public** OAuth clients running on the operator's
  own machine (Claude Code, Codex CLI). They cannot keep a client secret, so
  every control here assumes a public client with PKCE.
- The threat model assumes the model itself may be adversarial-by-accident:
  a hostile Minecraft log line, mod name, or MOTD can reach the model's
  context, so returned content is treated as untrusted evidence throughout.
