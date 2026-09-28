# Agent access hardening evidence context

Analysis target: the ServerManager source tree at revision
`f27626beb3e383f22e4f7b75137701097a916615`.

The working tree contained an unrelated untracked `deploy/` directory. It is
outside this analysis and must remain untouched. No completed security scan or
incident report was supplied; the evidence below is current source and project
documentation inspected for this design.

| Evidence | Reader-facing title | Path | SHA-256 | What it establishes |
| --- | --- | --- | --- | --- |
| `E01` | Documented authentication and node-token boundary | `docs/security.md` | `1e5f39f2d00ee3ae49e777b9991a351fa1eb364242f83fffa24b15edfede9607` | Browser JWTs and encrypted node tokens exist; automation keys are explicitly deferred. |
| `E02` | Reserved API-key schema | `apps/api/migrations/001_initial.sql` | `71a3b3606242d59237692acf8ffb31939ad1ecb8c23506f37516fa30232c832e` | `api_keys` already stores an owner, token hash, name, and expiry, but lacks scopes, server bounds, lifecycle state, and usage metadata. |
| `E03` | JWT-only request authentication | `apps/api/internal/auth/middleware.go` | `5aca63de98dfa5ce85c9c70fb7c2c299a9e3dd4a980842c3eccab62ed8b12de0` | Normal API calls accept JWT bearer credentials; narrow browser-only routes also accept single-use tickets. |
| `E04` | Live server authorization | `apps/api/internal/api/server_access.go` | `71ee20b1a9aba661d2981900dec93499c900ca1329f5638ef5052df852bb18b5` | Server authorization is re-read from the database, but global admins bypass server permission checks. |
| `E05` | Route-level permission map | `apps/api/internal/api/router.go` | `da912abd3fe63cdc55bd98a8bac29db6495b418ff81cd814a51219696ca20a9d` | Dangerous server operations already have granular permission gates that can serve as machine scopes if the key restriction is evaluated first. |
| `E06` | User-only audit attribution | `apps/api/internal/store/audit.go` | `9c4ad18b9c0d73ff990bfd691714d8c2a70fb7ab86cbfe72ae210e365c57a840` | Audit entries identify a user and server, but cannot distinguish a human session from one of that user's automation credentials. |
| `E07` | Reversible control-plane node secret storage | `apps/api/internal/store/nodes.go` | `b3affc1bb75c3036c36ad07a404cede3139483a0b38dff6fedc4a910a57d7e36` | The API decrypts a powerful node bearer token so it can call each host worker. |
| `E08` | Shared node-token enforcement | `apps/agent/internal/api/middleware/auth.go` | `d92368cb0eeb418b6786faaf84d29325c77ce2dd7003a902b4399f6f55bf5b4f` | A node token grants the caller the agent's full HTTP surface and is only compared as an opaque bearer secret. |

Evidence limitations:

- This is a source-derived design, not a claim that a vulnerability has been
  exploited or that the proposed controls are already effective.
- No latency, SQLite write-rate, key-volume, or concurrent automation workload
  was supplied. Resource claims remain source-derived or hypothetical and have
  explicit validation work.
- The intended external clients are assumed to be operator-controlled AI or
  automation agents capable of setting an `Authorization` header and protecting
  a long-lived secret. Browser-only query tickets are not part of this design.

