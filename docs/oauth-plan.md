# OAuth support — implementation plan (HTTP mode)

## Goal

Let users connect to the remote MCP server by logging in through their corporate IdP from the
IDE, instead of pasting a static ReportPortal API key. The customer's IdP is **Microsoft Entra ID**.

The MCP server acts as an **OAuth 2.1 Resource Server only**. The whole login flow happens
between the MCP client (VS Code / Copilot) and Entra; the server advertises where to
authenticate, validates the resulting token, and forwards it unchanged to the ReportPortal API.

## Already confirmed (no work needed)

- `service-api` validates JWTs from external IdPs via `rp.oauth2.providers.<name>.*`
  (issuer routing + JWKS). Configuration only, done on the RP deployment side —
  see `oauth-rp-backend.md`.
- RP ignores OAuth scopes and does not validate `audience`; permissions come from the RP
  database. Users must already exist (SCIM handles this) — there is no JIT provisioning.
  Audience validation in RP is a known limitation and is out of scope for now.
- goRP needs no changes: `WithApiKeyAuth` wraps any bearer string as a static token source.
- The MCP Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.4.1, already in `go.mod`) provides
  the building blocks — see "Implementation decisions".

## Entra ID specifics that shape the design

- **No Dynamic Client Registration.** Clients must use a pre-registered `client_id`.
  VS Code detects `login.microsoftonline.com` in `authorization_servers` and uses its built-in
  Microsoft auth provider with its own first-party client ID
  (`aebc6443-996d-45c2-90f0-388ff96faa56`), which must be pre-authorized on our API scope.
- **Scope decides the token audience.** Without an explicit scope of our API, Entra issues a
  Microsoft Graph token, which third parties cannot validate (Graph tokens carry a `nonce` in the
  header). The scope (e.g. `api://<app-id>/access_as_user`) must be advertised to clients.
- **Requested scope ≠ `scp` claim.** The client requests `api://<app-id>/access_as_user`, but the
  token's `scp` claim contains only the short name `access_as_user` (space-separated if several).
- **Token version.** The app registration must set `requestedAccessTokenVersion: 2`; otherwise
  Entra issues v1 tokens with `iss=https://sts.windows.net/{tenant-id}/`, which won't match the
  v2 issuer configured in RP and in our metadata. For v2 tokens `aud` is the app's client ID (GUID).
- **The tenant is part of `iss`**, so checking `iss` is enough; no separate `tid` check.
- **One app registration** ("ReportPortal MCP") is the audience of the token. The same token is
  forwarded to RP, which accepts it because it only checks issuer and signature.

## Implementation decisions

- **Middleware:** use `auth.RequireBearerToken` from the SDK, not a custom one. The SDK's
  streamable transport binds sessions to `auth.TokenInfo.UserID` read via
  `auth.TokenInfoFromContext`, and only `RequireBearerToken` can put `TokenInfo` into the context
  (the context key is unexported). Do **not** pass `RequireBearerTokenOptions.Scopes` — it
  compares strings literally against `TokenInfo.Scopes`; the scope check lives in our verifier.
- **Metadata endpoint:** use `auth.ProtectedResourceMetadataHandler` with
  `oauthex.ProtectedResourceMetadata` (fields `Resource`, `AuthorizationServers`,
  `ScopesSupported`).
- **JWT library:** `github.com/coreos/go-oidc/v3`. Build the verifier with
  `oidc.NewRemoteKeySet(ctx, jwksURL)` + `oidc.NewVerifier(issuer, keySet, &oidc.Config{ClientID: audience})`
  — no discovery call at startup, keys are fetched lazily and cached/rotated by the library.
  It checks signature, `iss`, `aud`, `exp`; v1 tokens fail on `iss`. The `ctx` passed to
  `NewRemoteKeySet` is used for all later JWKS fetches, so it must be long-lived (server lifetime,
  not a request context); pass an HTTP client with a timeout via `oidc.ClientContext`.
- **Tests sign tokens** with `github.com/go-jose/go-jose/v4` (already a dependency of go-oidc).

## Why tokens are validated on the MCP server, not only in RP

- **Token refresh depends on it.** MCP clients refresh or re-authenticate only on a transport-level
  HTTP `401`. If an expired token is passed through, `initialize` / `tools/list` still succeed
  (they never reach RP), and RP's `401` comes back as a tool error inside an HTTP `200` JSON-RPC
  response — the client never re-authenticates and tools stay broken until restart.
- **No unauthenticated access to the MCP server itself.** Today any string ≥16 chars opens a
  session and lists tools.
- **Session binding.** The SDK binds sessions to `TokenInfo.UserID`; it must come from a verified token.
- **MCP spec compliance.** Servers must accept only tokens issued for them (audience check).

## Behavior

OAuth off (default): **no behavior change** — existing passthrough of any bearer token.

OAuth on — the verifier decides per request:

| `Authorization` header                     | Result                                                       |
|--------------------------------------------|--------------------------------------------------------------|
| missing / not `Bearer`                     | `401` + `WWW-Authenticate` (done by `RequireBearerToken`)    |
| non-JWT passing `utils.ValidateRPToken`    | accepted as RP API key, forwarded to RP as today             |
| non-JWT failing `utils.ValidateRPToken`    | `401`                                                        |
| JWT, valid signature/`iss`/`aud`/`exp`     | accepted, forwarded to RP unchanged                          |
| JWT, any check fails                       | `401`, reason logged at `warn` (never log the token)         |

JWT detection: exactly three dot-separated segments and the first one base64url-decodes to a JSON
object. Everything else is treated as an API key.

The `scp` check is **lenient**: if the claim is present it must contain the expected scope
(otherwise `401`), but a token without `scp` is accepted. A matching `aud` already proves the token
was issued for this server, and app-only tokens carry `roles` instead of `scp`.

`TokenInfo` filled by the verifier:
- JWT: `UserID = oid` claim (fallback `sub`), `Scopes = strings.Fields(scp)`,
  `Expiration = exp`, `Extra["claims"]` = raw claims map.
- API key: `UserID = analytics.HashToken(key)`, `Expiration = time.Now().Add(time.Hour)`
  (the SDK rejects a zero expiration), no scopes.

Verifier errors must wrap `auth.ErrInvalidToken` (`fmt.Errorf("%w: …", auth.ErrInvalidToken)`),
otherwise the SDK answers `500`.

## Work items (in this order)

1. **Configuration** (`internal/config/cli.go` → `GetHTTPFlags`; `buildHTTPServerConfig` and
   `HTTPServerConfig` in `internal/reportportal/http_server.go`)
   HTTP-only flags:

   | Flag              | Env var               | Example                                                                 |
   |-------------------|-----------------------|-------------------------------------------------------------------------|
   | `oauth-enabled`   | `MCP_OAUTH_ENABLED`   | `true` (default `false`)                                                |
   | `public-url`      | `MCP_PUBLIC_URL`      | `https://mcp.example.com/mcp` — full public URL of the MCP endpoint     |
   | `oauth-issuer`    | `MCP_OAUTH_ISSUER`    | `https://login.microsoftonline.com/{tenant-id}/v2.0`                    |
   | `oauth-jwks-url`  | `MCP_OAUTH_JWKS_URL`  | `https://login.microsoftonline.com/{tenant-id}/discovery/v2.0/keys`     |
   | `oauth-audience`  | `MCP_OAUTH_AUDIENCE`  | `<GUID>` — Application (client) ID of the MCP app registration          |
   | `oauth-scope`     | `MCP_OAUTH_SCOPE`     | `api://<app-id>/access_as_user`                                         |

   `oauth-audience` is the expected `aud` claim of v2 tokens, which for Entra equals the app
   registration's Application (client) ID. It is **not** the `client_id` of an MCP client
   (e.g. VS Code's) — those are mentioned elsewhere in this document and must not be confused.

   When `oauth-enabled` is true, all other OAuth flags are required and `public-url`,
   `oauth-issuer`, `oauth-jwks-url` must be absolute `https` URLs (allow `http` for loopback hosts,
   for tests); otherwise fail at startup with a clear error. The expected `scp` value is the part of
   `oauth-scope` after the last `/`. Update the AUTHENTICATION section of `ServerDescription`.
   *Done when:* config tests cover valid config, each missing field, and OAuth off.

2. **Token verifier** (new package `internal/reportportal/oauth`, files `verifier.go`,
   `verifier_test.go`; don't name it `auth` to avoid clashing with the SDK package)
   Constructor takes the OAuth config and returns an `auth.TokenVerifier` implementing the
   "Behavior" section above.
   *Done when:* tests using a local `httptest.Server` serving a JWKS (RSA key generated in the test)
   cover: valid token; wrong `aud`; wrong `iss`; v1-style issuer; expired; `scp` with a different
   scope (rejected); `scp` absent (accepted); bad signature; `oid` missing, so `sub` is used;
   valid API key; too-short API key; garbage string.

3. **Routes and middleware** (`setupRoutes` and `corsMiddleware` in `internal/reportportal/http_server.go`)
   Only when OAuth is on:
   - Register `auth.ProtectedResourceMetadataHandler` as public routes next to `/health`:
     `/.well-known/oauth-protected-resource` and
     `/.well-known/oauth-protected-resource` + path of `public-url` (e.g. `…/mcp`).
     Metadata: `Resource = public-url` (from config, never from the `Host` header — the server runs
     behind a reverse proxy), `AuthorizationServers = [issuer]`, `ScopesSupported = [oauth-scope]`.
   - In the MCP route group, add `auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{ResourceMetadataURL: …})`
     **before** `HTTPTokenMiddleware`. `ResourceMetadataURL` = origin of `public-url` +
     `/.well-known/oauth-protected-resource` + path of `public-url`.

   Always: add `WWW-Authenticate` to `Access-Control-Expose-Headers`.
   *Done when:* `http_server_test.go` covers metadata JSON shape on both paths, `401` with
   `WWW-Authenticate` on `/mcp` without a token, request with a valid JWT reaches the MCP handler,
   request with an API key still works, and with OAuth off nothing new is registered.

4. **Analytics** (`getUserIDFromContext` in `internal/reportportal/analytics/analytics.go`)
   Keep the existing first priority (config user ID). Then, if `auth.TokenInfoFromContext(ctx)`
   returns a JWT-based `TokenInfo` (`Extra["claims"]` present), use `HashToken(UserID)`; otherwise
   fall back to the current bearer-token hash, so API-key users keep the same analytics ID as with
   OAuth off. Don't parse the JWT again. The HTTP request context already reaches tool handlers
   (that's how `utils.GetTokenFromContext` works today), so `TokenInfo` will be available there too.
   *Done when:* tests cover JWT TokenInfo, API-key TokenInfo, no TokenInfo, and config user ID set.

5. **Deployment and docs**
   - `helm-charts/reportportal-mcp-server`: `values.yaml`, `templates/deployment.yaml`, and the env
     var table in its `README.md`.
   - `docker-compose.yaml`: commented-out OAuth env vars.
   - Root `README.md`: new section under "Connecting to a Remote MCP Server" covering
     - Entra app registration: App ID URI, exposed scope, `requestedAccessTokenVersion: 2`,
       VS Code client ID in *Authorized client applications*, admin consent;
     - server env vars from item 1;
     - VS Code `mcp.json` example: `"type": "http"`, `url` equal to `MCP_PUBLIC_URL`, only the
       `X-Project` header (no `Authorization`);
     - note that clients must use exactly `MCP_PUBLIC_URL` (metadata `resource` must match);
     - other MCP clients need a pre-registered public client in Entra with loopback redirect URIs.

6. **Final checks** (no Docker; requires a local `golangci-lint` v2.11.4, the version pinned in
   `Taskfile.yaml`) — all must pass:
   ```bash
   go mod tidy
   golangci-lint fmt ./...
   golangci-lint run ./...
   go test ./...
   ```

## Constraints for the implementer

- Do not touch stdio mode, goRP, or anything under `pkg/` of other repos.
- Do not remove or change the API-key path; with OAuth off the server behaves exactly as today.
- Never log tokens or full claims; log only the failure reason and `oid`/`sub`.
- The "Risks" section below is manual verification by the team, not an implementation task.

## Risks to verify early (manual, on real clients)

- **Authorization server discovery.** Entra serves only OIDC discovery
  (`/{tenant-id}/v2.0/.well-known/openid-configuration`), not RFC 8414. Current MCP clients reach
  it via fallback; older ones may not. Entra metadata also appears to lack
  `code_challenge_methods_supported`, which strict clients (MCP spec 2025-11-25) treat as a reason
  to abort.
- **RFC 8707 `resource` parameter.** MCP clients send `resource=<mcp-url>`; Entra v2 may reject it
  with `AADSTS9010010` if it doesn't match the requested scope. Possible mitigation: set the
  Application ID URI to the MCP server's public URL (requires a verified domain).
- **`WWW-Authenticate` format.** The SDK writes `resource_metadata=<url>` unquoted; confirm the
  target clients parse it.
- The first two are bypassed for VS Code by its built-in Microsoft provider, so check them for every
  other client the customer intends to use.

## Out of scope

- Any change to goRP.
- Audience validation in `service-api`.
- MCP server acting as an authorization server, issuing its own tokens, or On-Behalf-Of exchange.
- stdio mode (not usable at this customer by policy).

## Customer input (needed for rollout, not for implementation)

- Tenant ID.
- App registration for the MCP server (or approval to follow our setup guide): App ID URI,
  scope name, `requestedAccessTokenVersion: 2`, VS Code client ID pre-authorized, admin consent.
- Which MCP clients besides VS Code are needed, and a `client_id` for them if so.
- The claim used for user matching — tracked in `oauth-rp-backend.md`.
