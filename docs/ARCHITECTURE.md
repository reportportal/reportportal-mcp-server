# Architecture

This document describes how the ReportPortal MCP Server is put together: process
layout, request flow, key packages, and the constraints/limitations you need to
know before changing it. It is aimed at engineers picking up the project with
little or no prior context.

See also: [Development & Style Guide](DEVELOPMENT.md) · [Release Workflow](RELEASE.md)

## 1. What this project is

A [Model Context Protocol](https://modelcontextprotocol.io/overview) (MCP)
server that exposes ReportPortal (test reporting + TMS) capabilities as MCP
**tools** and **prompts**, so any MCP-compatible AI assistant (Claude, Cursor,
Copilot, ...) can query/mutate ReportPortal data through natural language.

It is a single Go binary (`cmd/reportportal-mcp-server`) that can run in one of
two transport modes, selected at startup by the `MCP_MODE` environment
variable:

| Mode | Transport | Typical use |
|------|-----------|-------------|
| `stdio` (default) | stdin/stdout, one process per client | Local AI tool integrations (Claude Desktop, Cursor, VS Code) |
| `http` | Streamable HTTP / SSE via `github.com/modelcontextprotocol/go-sdk` | Shared/remote deployments serving many clients concurrently |

Both modes share the same tool/prompt registration code — only the transport,
authentication source, and HTTP client tuning differ.

## 2. High-level component diagram

```
                         ┌───────────────────────────────┐
                         │        cmd/.../main.go         │
                         │  builds *cli.Command via       │
                         │  config.InitAppConfig(...)      │
                         └───────────────┬────────────────┘
                                         │ MCP_MODE env var
                     ┌───────────────────┴───────────────────┐
                     │                                       │
              MCP_MODE=stdio                          MCP_MODE=http
                     │                                       │
                     ▼                                       ▼
   internal/reportportal/mcp_handlers/server.go   internal/reportportal/http_server.go
     RunStdioServer(ctx, cmd)                        RunStreamingServer(ctx, cmd)
     - reads RP_API_TOKEN/RP_HOST/RP_PROJECT          - Chi router + middleware
       from CLI flags (env-bound)                     - per-request token/project
     - builds *mcp.Server via NewServer(...)            via HTTP headers
     - runs over mcp.StdioTransport                   - builds *mcp.Server via
                                                         NewHTTPServer(...)
                     │                                       │
                     └───────────────────┬───────────────────┘
                                         ▼
                     mcp_handlers.RegisterLaunchTools / RegisterTestItemTools /
                     RegisterTMSTools  — register mcp.Tool + typed handler pairs
                                         │
                                         ▼
                         gorp.Client (github.com/reportportal/goRP/v5)
                         generated OpenAPI client for ReportPortal's REST API
                                         │
                                         ▼
                              ReportPortal instance (RP_HOST)
```

Cross-cutting concerns hang off the same wiring:

* **Analytics** (`internal/reportportal/analytics`) — optional, batches
  anonymous usage metrics to Google Analytics 4.
* **Middleware** (`internal/reportportal/middleware`) — injects the bearer
  token/project into outgoing ReportPortal requests (`QueryParamsMiddleware`)
  and extracts them from incoming HTTP requests (`HTTPTokenMiddleware`).
* **Prompts** (`internal/reportportal/mcp_handlers/prompts/*.yaml`) — declarative
  MCP prompts loaded at startup via `internal/promptreader`.
* **Context propagation** (`internal/reportportal/utils/ctx_utils.go`) — the
  token and project key travel through `context.Context`, not global state.

## 3. Package map

| Package | Responsibility |
|---|---|
| `cmd/reportportal-mcp-server` | Process entrypoint; wires signal handling + `config.InitAppConfig`. |
| `cmd/verify-testdata` | Standalone CLI that replays `internal/integration/testdata/*.json` fixtures against a **live** running server (see [Development guide](DEVELOPMENT.md#testing)). |
| `internal/config` | CLI flag/env-var definitions (`cli.go`), TLS config construction (`tls.go`), build-time version vars (`Version`/`Commit`/`Date`, injected via `-ldflags`). |
| `internal/reportportal` | HTTP-mode server: Chi router, middleware stack, health/info/metrics endpoints, graceful shutdown (`http_server.go`). |
| `internal/reportportal/mcp_handlers` | stdio-mode server bootstrap (`server.go`) **and** all MCP tool implementations, split by domain: `launches.go`, `items.go`, `tms.go`. These three files hold *only* `mcp.Tool` definitions/handlers and their `Register*Tools` function — non-tool helpers live in `internal/reportportal/utils/mcp_utils.go` instead (see [Development guide §2](DEVELOPMENT.md#2-repository-layout-where-to-put-things)). Also prompt loading glue. |
| `internal/reportportal/mcp_handlers/prompts` | YAML prompt definitions (embedded via `//go:embed`). |
| `internal/reportportal/analytics` | Google Analytics 4 usage tracking (opt-out), instance-ID discovery, batching. |
| `internal/reportportal/middleware` | HTTP middleware: token/project extraction (HTTP mode) and injection (outgoing RP client). |
| `internal/reportportal/utils` | Shared helpers used across all tool handlers: pagination, project-key resolution, JSON-schema builders, response helpers, context helpers, TMS manual-scenario building/validation. |
| `internal/promptreader` | Parses prompt YAML into `mcp.Prompt` + `mcp.PromptHandler` pairs, with Go `text/template` rendering of arguments. |
| `internal/integration` | Black-box integration test harness (mock ReportPortal + mock LLM client + real MCP server) driven by JSON fixtures. |
| `helm-charts/reportportal-mcp-server` | Kubernetes Helm chart for HTTP-mode deployments. |

## 4. Request lifecycle

### stdio mode

1. `main.go` builds a `*cli.Command` and runs it; `MCP_MODE` (unset ⇒ `stdio`)
   selects `mcphandlers.RunStdioServer`.
2. `RunStdioServer` requires `RP_API_TOKEN` (flag/env), optionally stores
   `RP_PROJECT` in the root `context.Context` via `utils.WithProjectInContext`.
3. `mcphandlers.NewServer(...)` builds one `gorp.Client` for the whole process,
   configured with the token via `gorp.WithApiKeyAuth` and a `QueryParamsMiddleware`
   that also merges any query params carried on the request context.
4. All tools are registered once; the server then blocks on
   `mcpServer.Run(ctx, &mcp.StdioTransport{})`, i.e. **one client per process,
   one token for the whole process lifetime**.

### http mode

1. `RunStreamingServer` builds an `HTTPServerConfig` from CLI flags (no token —
   HTTP mode never reads `RP_API_TOKEN`/`--token`).
2. `NewHTTPServer` builds **one** shared `gorp.Client` with an **empty**
   fallback token; the real token is injected per-request.
3. A Chi router is configured with (in order): CORS → RequestID → RealIP →
   Logger → Recoverer → conditional timeout (skipped for SSE) → Throttle
   (`--max-workers`, default `NumCPU()*2`) → route dispatch.
4. On the `/mcp`, `/api/mcp` group: `HTTPTokenMiddleware` reads
   `Authorization: Bearer <token>` and `X-Project` headers and stores them in
   the **request** context; `mcpMiddleware` rejects anything that is not a
   JSON POST, an SSE GET, or a session-terminating DELETE.
5. Requests are dispatched to `mcp.NewStreamableHTTPHandler`, which calls the
   shared `*mcp.Server`. Because token/project live in the *request* context
   (not a struct field), the same `gorp.Client` and tool handlers are safely
   reused across concurrent requests from different users —
   `QueryParamsMiddleware` reads the per-request context at the moment the
   outgoing RP HTTP request is built.

### Inside a tool handler (both modes)

1. `utils.ExtractProject(ctx, args.ProjectKey)` resolves the effective project:
   context value (env var in stdio / `X-Project` header in HTTP) **always
   wins**; the `projectKey` tool argument is only a fallback.
2. Handler builds/executes a `gorp`-generated OpenAPI request, or (for some TMS
   endpoints not yet in the generated client) a raw `net/http` request built
   from `rpClient.GetConfig()` — see `toolGetMilestonesByFilter` in
   `internal/reportportal/mcp_handlers/tms.go` for the pattern.
3. `utils.ReadAPIResponse` / `utils.ReadResponseBody` convert the HTTP
   response into an `*mcp.CallToolResult`, passing the raw JSON body straight
   through instead of the typed decoded struct (see limitation below).
4. If analytics is enabled, `utils.WithAnalytics` wraps the handler and fires
   `TrackMCPEvent` before execution.

## 5. External dependencies worth knowing

* **`github.com/modelcontextprotocol/go-sdk/mcp`** — official MCP Go SDK;
  provides `mcp.Server`, `mcp.AddTool` (generic, schema-inferring), stdio and
  Streamable HTTP transports.
* **`github.com/reportportal/goRP/v5`** — generated OpenAPI client for
  ReportPortal's REST API (`pkg/gorp`, `pkg/openapi`). Since it's generated,
  not every TMS endpoint may be present yet; see §6.
* **`github.com/urfave/cli/v3`** — CLI flags/env-var binding
  (`internal/config/cli.go`).
* **`github.com/go-chi/chi/v5`** — HTTP router used only in HTTP mode.
* **`github.com/google/jsonschema-go/jsonschema`** — builds the `InputSchema`
  for every tool (see [Development guide](DEVELOPMENT.md#json-schema--input-validation)).

## 6. Known limitations / gotchas

These are structural properties of the current design — read them before
"fixing" something that is actually intentional, and update this list when you
change the underlying behavior.

1. **One RP client per process, not per user (stdio mode).** stdio mode is
   single-tenant by design: one token, one project, one OS process per MCP
   client connection. It cannot serve multiple ReportPortal users
   concurrently. HTTP mode is multi-tenant instead (token/project come from
   headers).
2. **`RP_PROJECT` / `RP_API_TOKEN` are silently ignored in HTTP mode.** By
   design — HTTP mode is meant to be shared, so per-user secrets must travel
   per-request (`Authorization` header, `X-Project` header). This is a common
   source of "why isn't my env var working" support questions; see the
   README's Troubleshooting section.
3. **`--insecure` and `--tls-ca-cert` are mutually exclusive.** Enforced in
   both `config.BuildTLSConfig` and the CLI `Action`. Setting both is a
   startup error, not a silent override.
4. **Never overwrite `rpClient.APIClient.GetConfig().HTTPClient` after client
   creation** without re-adding the OAuth2 Bearer-token transport — doing so
   silently drops authentication. This exact regression is covered by
   `TestNewServer_BearerTokenSentWithTLSConfig` /
   `..._BearerTokenSentWithoutTLS` in
   `internal/reportportal/mcp_handlers/server_test.go`. If you need a custom
   `*http.Client` (e.g. custom TLS), build it via `buildHTTPClient`/
   `createHTTPClient` and thread it through `oauth2.HTTPClient` in the auth
   context — don't replace the client used by the OAuth2-wrapped transport
   directly.
5. **The generated ReportPortal OpenAPI client is incomplete for some TMS
   endpoints** (e.g. milestones). Handlers work around this by issuing raw
   `net/http` requests built from `rpClient.GetConfig()` (scheme/host/default
   headers/middleware) instead of a generated method. When the upstream
   `goRP` client adds the missing operation, prefer migrating the handler to
   the typed client.
6. **Polymorphic `oneOf` responses can fail client-side decoding even on a
   successful HTTP call** (e.g. `TmsTestCaseRS.ManualScenario`, which is a
   discriminated union of `TEXT`/`STEPS` scenario shapes). `utils.ReadAPIResponse`
   deliberately ignores decode errors when the HTTP status is 2xx and returns
   the **raw JSON body** instead — do not "fix" this by surfacing the decode
   error, and do not rely on the typed decoded struct downstream of these
   calls.
7. **Import Launch from File has a hard upload cap.** Default 50 MiB decoded
   size, further capped by the selected plugin's advertised
   `details.maxFileSize`. Base64 payloads are measured **after** decoding.
   There is no streaming/chunked upload path — large files must be
   pre-split/compressed by the caller.
8. **Analytics `GetAnalyticArg()` deliberately obfuscates the GA4 API secret**
   with XOR/base64 tricks so it isn't trivially greppable in the binary or
   source. It is **not** a security boundary (the value ends up in every
   built binary) — it only stops the secret from being copy-pasted from a
   `git blame`/`strings` at a glance. Do not add a "cleaner" plain-string
   constant; do not treat it as a secret that needs rotation-on-leak handling.
9. **Analytics is best-effort and fails open.** If `NewAnalytics` fails (e.g.
   missing GA4 secret at build time) the server logs a warning and continues
   with `analyticsInstance == nil`; every analytics call is a nil-safe no-op
   (`TrackMCPEvent`, `incrementMetric`, etc. all check `a == nil`). Never make
   analytics failures fatal or block a tool call on analytics I/O — events are
   fire-and-forget, batched every `BatchSendInterval` (10s) in a background
   goroutine.
10. **No persistent state / no database.** The server is stateless between
    requests beyond the in-memory analytics counters; all "state" lives in
    ReportPortal itself. Restarting the process loses only unsent analytics
    counters (≤ `BatchSendInterval` of data).
11. **HTTP mode has no built-in authentication of *its own* endpoints** beyond
    passing through whatever bearer token the caller supplies to ReportPortal
    — `/health`, `/info`, `/metrics` are unauthenticated by design (they leak
    no RP data). MCP endpoints rely entirely on ReportPortal validating the
    forwarded token; the server does not cache/verify tokens itself beyond the
    lightweight `utils.ValidateRPToken` format check (UUID or ≥16 chars) used
    to decide whether to even forward a header.
12. **TMS tools require ReportPortal 26.1+ with TMS enabled**; non-TMS tools
    require 25.1+ / service-api ≥ 5.14.0. Calling TMS tools against an older
    instance will surface as ReportPortal API errors (404/400), not a clean
    "unsupported version" message from this server.
13. **Windows path handling in `//go:embed`.** Prompt files are read with
    `filepath.Clean(dir)+"/"+entry.Name()` — forward slash is hard-coded
    because `embed.FS` always uses `/`, even when the host OS (e.g. Windows)
    uses `\`. Don't switch this to `filepath.Join`.
14. **Go version drift risk.** `go.mod` currently pins `go 1.25.0`; the root
    `README.md` "Prerequisites" section may quote a different minimum (e.g.
    1.24.4). Treat `go.mod` and `.golangci.yml`'s `GOLANGCI_LINT_VERSION`
    comment as the source of truth and keep the README in sync when bumping.
15. **No request-level rate limiting against ReportPortal itself** — only
    local concurrency is throttled (`--max-workers` / Chi `Throttle`). A
    misbehaving client can still overwhelm the upstream ReportPortal instance
    if `--max-workers` is set too high for that instance's capacity.

## 7. Deployment shapes

* **Local binary / Docker, stdio** — the common case for IDE/desktop AI tool
  integrations (see README "Installation").
* **Local binary / Docker, HTTP** — for local testing of the HTTP transport
  (`task docker:run MCP_MODE=http`), or `docker-compose.yaml` for a minimal
  standalone HTTP deployment.
* **Kubernetes via Helm** (`helm-charts/reportportal-mcp-server`) — HTTP mode
  only, fronted by an Ingress; see the chart's own
  [README](../helm-charts/reportportal-mcp-server/README.md) for values.
* **GitHub Container images** — `develop-*` (from `develop`), `rc/*`/`hotfix/*`
  branch images, PR "feature" images, and tagged releases on Docker Hub — see
  [Release Workflow](RELEASE.md).
