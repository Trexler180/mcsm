# Security

## Auth Model

The supported model is:

- Admins manage nodes, users, audit, and every server.
- Server owners manage their own servers.
- Granular collaborator permissions are intentionally not exposed yet.

The `server_permissions` and `api_keys` tables are reserved for future work.

## Tokens

- JWT access tokens authenticate ordinary API requests.
- Refresh tokens are stored only as hashed database rows and rotated on use.
- Browser refresh tokens are delivered as `HttpOnly`, `SameSite=Strict`
  cookies scoped to `/api/v1/auth`; frontend code stores only the short-lived
  access token.
- Query-string tokens are accepted only for websocket/download endpoints where
  browsers cannot reliably send an Authorization header.
- Agent calls use bearer tokens stored in the API database and never emitted in
  node JSON.

## Production Startup Guards

Outside `MCSM_DEV_MODE=1` or a development `APP_ENV`, the API requires a
configured `JWT_SECRET`. The API and agent reject the default `dev-agent-token`
outside development mode.

## Future Work

- Encrypt stored agent tokens at rest.
- Add API keys for automation only when there is a concrete need.
- Add granular permissions and collaborator UI as a deliberate feature, not a
  placeholder.
