# Development & Style Guide

Practical guide for making changes to the ReportPortal MCP Server: environment
setup, coding conventions, and the established approaches for configuration,
validation, analytics, testing, and extending the server with new tools or
prompts.

See also: [Architecture & Limitations](ARCHITECTURE.md) · [Release Workflow](RELEASE.md)

## 1. Prerequisites & setup

* Go — version pinned in `go.mod` (currently `1.25.0`; the CI build matrix in
  `.github/workflows/build.yml` is the source of truth if it ever drifts from
  `go.mod`).
* [Go Task](https://taskfile.dev/) v3: `go install github.com/go-task/task/v3/cmd/task@latest`
* Docker (for `task docker:*`, integration image builds, MCP Inspector runs).
* A ReportPortal instance to test against (25.1+ for base tools, 26.1+ with
  TMS enabled for TMS tools).

```bash
git clone https://github.com/reportportal/reportportal-mcp-server.git
cd reportportal-mcp-server
task deps      # go mod download/tidy
task build     # go build ./cmd/reportportal-mcp-server -> bin/reportportal-mcp-server
task test      # go test ./...
```

All `task` commands are defined in `Taskfile.yaml`; read it before writing a
raw `go`/`docker` command; there is very likely already a task for it
(`fmt`, `lint`, `checks`, `test`, `test:integration`, `debug`, `docker:build`,
`docker:run`, `inspector`, `inspector-debug`, `verify:testdata`).

On Windows, `task debug`/`task debug:stop` shell out to PowerShell
(`Get-NetTCPConnection`, `dlv`) — that's expected, this codebase is primarily
developed on Windows.

## 2. Repository layout (where to put things)

```
cmd/reportportal-mcp-server/   entrypoint (main.go) — keep this thin
cmd/verify-testdata/           fixture-replay CLI used against a live server
internal/config/               CLI flags, env vars, TLS config, version vars
internal/reportportal/         HTTP-mode server (Chi router + middleware)
internal/reportportal/mcp_handlers/   stdio bootstrap + ALL tool handlers
internal/reportportal/mcp_handlers/prompts/   YAML prompt definitions
internal/reportportal/analytics/      GA4 usage tracking
internal/reportportal/middleware/     token/project extraction + injection
internal/reportportal/utils/          shared helpers (pagination, schema, ctx, TMS scenario building)
internal/promptreader/         YAML → mcp.Prompt/Handler parsing
internal/integration/          black-box integration tests (mock RP + mock LLM client)
helm-charts/reportportal-mcp-server/  Helm chart for HTTP-mode k8s deployment
```

New MCP tools that operate on **launches**/**test items** go in
`launches.go`/`items.go`; anything **TMS** (milestones, folders, test cases,
test plans, manual launches) goes in `tms.go`. Cross-domain helpers belong in
`internal/reportportal/utils`, not duplicated per file.

**`launches.go`, `items.go`, and `tms.go` must contain only `mcp.Tool`
definitions/handlers (the `tool*` constructors) and their `Register*Tools`
registration function — nothing else.** Any helper that isn't itself a tool
constructor (request/response shaping, cross-field validation, schema-builder
functions, upload/multipart plumbing, etc.) belongs in
`internal/reportportal/utils/mcp_utils.go` as an **exported** function
(`utils.XxxYyy`), even if today it's only called from one domain file. This
keeps the handler files scannable as "here's the list of tools" and makes the
helper independently unit-testable and reusable if another domain needs it
later. See `utils.ResolveTestCaseAttributes`, `utils.UploadTMSAttachment`,
`utils.ResolveExecutionCommentAttachments`, `utils.ValidatePlanAndTestCaseIDs`,
and `utils.TestPlanAndCaseIDsProperties` for examples — all were moved out of
`tms.go` for exactly this reason. If you add a new non-tool helper to
`launches.go`/`items.go`/`tms.go`, move it to `mcp_utils.go` as part of the
same change rather than leaving it in place "for now."

## 3. Code style

* **Formatting/linting is enforced by `golangci-lint` v2 (`.golangci.yml`)**
  with the `standard` linter set plus `gosec`, and formatters `gci`, `gofmt`,
  `gofumpt`, `goimports`, `golines`. Local import group order (enforced by
  `gci`/`goimports`): stdlib → third-party → this module
  (`github.com/reportportal/reportportal-mcp-server/...`), each group
  separated by a blank line.
* Run before every commit/PR:
  ```bash
  task fmt     # auto-formats (gci/gofmt/gofumpt/goimports/golines)
  task lint    # golangci-lint run ./...
  task checks  # go mod tidy && fmt && lint, the combined pre-PR gate
  ```
  CI (`.github/workflows/build.yml`) runs `golangci-lint` (v2.11.4, pinned —
  keep `Taskfile.yaml`'s `GOLANGCI_LINT_VERSION` and the workflow version in
  sync) and `task test` + `task app:build` on every push/PR to `main`/`develop`.
* **`gosec` is enabled** — expect it to flag things like `math/rand`,
  unchecked file permissions, etc. Suppress a specific, reviewed false
  positive inline with `//nolint:gosec // <reason>`, never with a blanket
  file/package-level disable. See existing examples in `analytics.go` and
  `mcp_utils.go` for the expected comment style (always explain *why* it's
  safe, not just that it's suppressed).
* **Comments**: godoc-style on exported identifiers, explaining *why*/*contract*,
  not restating the code. Several functions in this codebase document tricky
  invariants directly above the func (e.g. `ReadResponseBody`'s "IMPORTANT
  CONTRACT" comment, `buildHTTPClient`'s note on why stdio doesn't need
  connection-pool tuning) — follow that pattern for anything non-obvious,
  especially around concurrency, context propagation, or protocol quirks.
* **Errors**: wrap with `fmt.Errorf("...: %w", err)`; give the caller (an AI
  assistant relaying the message to a human) enough detail to self-correct,
  e.g. include which field was invalid and what values are legal.
* **Generics**: the codebase leans on Go generics for shared plumbing
  (`ToolHandler[In, Out]`, `registerTool[In, Out]`, `PaginatedRequest[T]`,
  `ApplyPaginationOptions[T PaginatedRequest[T]]`, `WithAnalytics[In]`). Prefer
  extending these generic helpers over copy-pasting a monomorphic variant.
* **Concurrency**: anything reachable from HTTP mode must be safe for
  concurrent use (one `*mcp.Server`/`*gorp.Client` shared across requests).
  Per-request state (token, project, query params) travels via
  `context.Context` (`internal/reportportal/utils/ctx_utils.go`), never via
  mutable struct fields on the long-lived resource types (`TMSResources`,
  etc.).

## 4. Configuration approach

All runtime configuration is CLI-flag based via `urfave/cli/v3`, with every
flag bound to an environment variable through `Sources: cli.EnvVars(...)` —
there is no separate config-file/env-parsing layer. This is the single source
of truth for what's configurable; when adding a new setting:

1. Add a `cli.Flag` in `internal/config/cli.go`:
   - Common to both modes → `GetCommonFlags()`.
   - HTTP-only → `GetHTTPFlags()` (name it `[HTTP-ONLY]` in `Usage` for
     discoverability in `--help`).
   - stdio-only → `GetStdioFlags()`.
2. Document the corresponding env var in the `ServerDescription` const at the
   top of `cli.go` if it's non-obvious (mutual exclusions, mode-specific
   behavior, etc.) — this string **is** the `--help` output.
3. Read it back via `cmd.String("flag-name")` / `cmd.Bool(...)` / `cmd.Int(...)`
   in `newMCPServer` (stdio) or `buildHTTPServerConfig` (HTTP) — don't call
   `os.Getenv` directly in handler code; `GetMCPMode()` in `cli.go` is the one
   documented exception because the mode must be known *before* the flag set
   is even built.
4. Update the README's "Configuration" and "For developers" tables, and the
   Helm chart's `values.yaml`/README if the setting is relevant to HTTP-mode
   deployments.
5. TLS-related settings go through `config.BuildTLSConfig` (returns `nil` for
   "use Go defaults" — don't special-case nil TLS config at call sites beyond
   what's already there); `--insecure` and `--tls-ca-cert` must stay mutually
   exclusive (enforced in both the CLI `Action` and `BuildTLSConfig` itself —
   keep both checks if you touch this).

Precedence rule to preserve if you add project/token-like settings: **context
value (env var in stdio / HTTP header in HTTP mode) always wins over a
per-call tool argument**, which is only a fallback. See
`utils.ExtractProject` — replicate this pattern rather than inventing a new
precedence order.

## 5. Validation approach

Validation happens at two layers; keep both in sync when adding/changing a
tool parameter.

### 5.1 JSON Schema (client-facing contract)

Every tool's `InputSchema` is a `*jsonschema.Schema`
(`github.com/google/jsonschema-go/jsonschema`) built by hand alongside the
`mcp.Tool` definition. Use the shared builders in
`internal/reportportal/utils` instead of inlining ad-hoc schemas:

* `utils.ProjectKeySchema(defaultProjectKey)` — the `projectKey` field.
* `utils.LimitSchema(defaultLimit)` / `utils.OffsetSchema()` — limit/offset
  pagination.
* `utils.SetPaginationProperties(sortingParams)` — page/page-size/page-sort
  pagination (older, offset-based tools use `limit`/`offset` instead — match
  whichever pagination style the underlying RP endpoint uses).
* `utils.AttributesSchema(isUpdate)`, `utils.RequirementsSchema(isUpdate)`,
  `utils.TestCaseTypeSchema(isUpdate)`, `utils.StepsSchema()` — TMS
  test-case/manual-scenario fields.

Conventions to follow for any new schema field:

* Set `MinLength`/`Pattern: `\S`` on free-text fields that must be
  non-blank (see `attributesItemSchema`, `StepsSchema`) instead of validating
  blankness only in Go.
* Set `Minimum`/`Maximum` on numeric IDs (`openapi.PtrFloat64(1)` for
  "ID, must be ≥ 1").
  Reuse `github.com/reportportal/goRP/v5/pkg/openapi`'s `PtrFloat64`/`PtrInt`
  helpers for schema pointer fields — don't hand-roll `func ptr[T](v T) *T`.
* Set `Enum` for closed value sets (priorities, statuses, milestone types) and
  keep the Go-side switch/validation in sync with the same literal values.
* Reject unknown properties on structured object fields with
  `AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}` (see
  `attributesItemSchema`, `StepsSchema`) so typos in AI-generated tool calls
  fail fast with a schema error instead of being silently ignored.
* Write the field `Description` for the AI assistant, not just a human
  reader: state defaults, units, valid ranges/enums, and cross-field
  constraints (e.g. `TestCaseTypeSchema`'s description explains when the
  field becomes required). The model consuming this schema only has the
  description text to go on.

### 5.2 Go-side validation (business rules the schema can't express)

Structural/cross-field rules that JSON Schema can't (or shouldn't) encode
belong in the handler or in a shared helper such as
`utils.BuildManualScenario`:

* Mutually exclusive fields (e.g. `steps` only valid when
  `test-case-type=steps`; `instructions`/`expected-result` invalid for
  `steps`).
* Conditional requiredness (e.g. `steps` required on create but optional on
  update).
* Range/relationship checks that need parsed values (e.g. milestone
  `end-date` must not precede `start-date`; time-range filters where "from"
  must be earlier than "to" — see `utils.ProcessStartTimeFilter`).
* Anything requiring `strings.TrimSpace` before checking blankness on a
  slice element (schema `Pattern` covers a whole string but not "trim then
  check" semantics precisely).

Return validation failures as a plain `error` from the handler (`fmt.Errorf`)
— the framework surfaces it to the MCP client as a tool error; do **not**
build a manual `*mcp.CallToolResult{IsError: true}` for input-validation
failures (reserve that pattern for the specific "response body describes the
failure" contract documented on `utils.ReadResponseBody`).

## 6. Analytics approach

Analytics (`internal/reportportal/analytics`) is **best-effort, batched, and
privacy-conscious** telemetry of *tool usage counts* — never payloads,
arguments, or ReportPortal data.

Ground rules when touching this package or adding a new tool:

* **Every tool must be wrapped with `utils.WithAnalytics(tracker, toolName, handler)`**
  at registration time (see any `tool*` constructor in `launches.go`/`items.go`/`tms.go`).
  This is how usage shows up in the `mcp_event_triggered` GA4 event with a
  `tool` param — forgetting it just means that tool is invisible in metrics,
  it does not break functionality, but it is still expected for every new
  tool.
* **Never send tool arguments, ReportPortal data, or free-text content** to
  GA4. The only params sent are `custom_user_id` (hashed), `event_name`, `tool`,
  and (once resolved) `instanceID`. If you're tempted to add a new param,
  check it can't leak project names, test data, or credentials.
* **User identification is always a one-way hash, never the raw token**:
  `analytics.HashToken` is SHA-256 over the raw RP API token (stdio) or the
  Bearer token from the request context (HTTP, anonymous-mode fallback).
  There is a fixed `anonymousUserIDHash` pre-computed constant for the fully
  anonymous case — reuse it rather than hashing an empty string ad hoc.
* **Fail open, never fail loud.** `NewAnalytics` returning an error (e.g. no
  GA4 secret compiled in) only logs `slog.Warn` and leaves
  `analyticsInstance == nil`; every method on `*Analytics` is nil-receiver-safe
  (`if a == nil { return }`). Don't add a code path where analytics being
  unavailable/erroring affects a tool's result or the server's health.
* **It's opt-out, not opt-in**: `--analytics-off` / `RP_MCP_ANALYTICS_OFF=true`
  disables it. Preserve this default; don't flip it to opt-in without a
  product decision, since usage metrics are how the team currently gauges
  adoption.
* **Batching, not per-event HTTP calls.** Tool calls only increment an
  in-memory atomic counter (`incrementMetric`); a background goroutine
  (`startMetricsProcessor`) flushes non-zero counters to GA4 every
  `BatchSendInterval` (10s), chunked to `maxPerRequest` (25) events per HTTP
  call, **per user**. If you need a new metric, prefer extending this
  counter map over adding a new unbatched HTTP call path.
* **Shutdown must be graceful and bounded.** `Stop()` cancels the internal
  context (aborting in-flight GA4 HTTP calls), closes `stopChan`, and waits on
  the background goroutine with a 5s hard timeout before giving up — mirror
  this pattern (cancel → close → bounded wait) if you add another background
  worker anywhere in the server.
* **Two separate HTTP clients on purpose**: `httpClient` (GA4, always default
  TLS/cert verification — GA4 is a public Google endpoint and must never trust
  a user-supplied custom CA or skip verification) vs. `rpClient` (ReportPortal
  `/api/info` only, may use the operator's custom `tlsCfg`/insecure setting).
  Do not merge these into one client.
* `GetAnalyticArg()` reconstructs the GA4 API secret from obfuscated
  fragments (see [Architecture limitation #8](ARCHITECTURE.md#6-known-limitations--gotchas)) — this is intentionally not a
  plain string constant; leave the obfuscation in place and don't "clean it
  up" into a literal.

## 7. Adding a new MCP tool

1. Pick the right file (`launches.go`, `items.go`, or `tms.go`) based on
   domain, or create a new one for a genuinely new domain and register it
   from `NewServer` (stdio, `server.go`) **and** `initializeTools` (HTTP,
   `http_server.go`) — both call sites must stay in sync.
2. Define a typed `Args` struct with `json` tags matching your schema
   property names exactly (`mcp.AddTool`/`ToolHandler[In, Out]` decode
   directly into this struct — see any `*Args` type in `tms.go`/`launches.go`).
3. Write a `func (r *XResources) toolYourThing() (*mcp.Tool, ToolHandler[Args, any])`
   returning the `mcp.Tool{Name, Description, InputSchema}` and the handler,
   following §5 for schema + validation conventions.
4. Wrap the handler body with `utils.WithAnalytics(r.analytics, "your_tool_name", func(...) {...})`.
5. Inside the handler: resolve the project via `utils.ExtractProject`, build
   the RP request (prefer the generated `gorp`/`openapi` client; fall back to
   a raw `net/http` request off `rpClient.GetConfig()` only if the operation
   isn't in the generated client yet — see limitation #5 in
   [ARCHITECTURE.md](ARCHITECTURE.md#6-known-limitations--gotchas)), and
   return via `utils.ReadAPIResponse`/`utils.ReadResponseBody`.
6. Register it: call `registerTool(s, r.toolYourThing)` inside the
   `Register*Tools` function for that domain (e.g. `RegisterTMSTools`,
   `RegisterLaunchTools`). Do **not** add it directly in
   `NewServer`/`initializeTools` — those entry points only invoke the
   `Register*Tools` functions; bypassing them skips analytics wrapping and the
   shared resource instance. Only add a new `Register*Tools` call to
   `NewServer`/`initializeTools` when introducing a **brand-new domain file**
   (step 1 above).
7. Update the README tool table (`## Available Tools (commands)`) — the
   number "33 tools" quoted in the Verifying-Your-Setup section will need
   bumping too if you're adding rather than modifying a tool.
8. Add unit tests (see §9) and, for anything with non-trivial request/response
   shaping, an integration fixture (see §9.2).

## 8. Adding a new prompt

No code changes needed — drop a new YAML file into
`internal/reportportal/mcp_handlers/prompts/` following the structure of
`launch.yaml` (`name`, `description`, `arguments[]`, `messages[]` with
`role`/`content.type: text`/`content.text` as a Go `text/template` body). It
is picked up automatically via `//go:embed prompts/*.yaml` +
`ReadPrompts`/`LoadPromptsFromYAML` at server startup. Template execution uses
`missingkey=error`, so every `{{.Argument}}` referenced in the template text
must be declared in `arguments[]` and supplied by the caller.

## 9. Testing

* **Unit tests** live next to the code (`*_test.go` in the same package),
  using `testify` (`assert`/`require`). Table-driven tests are the norm for
  pure functions (see `utils_test.go`); handler tests spin up a real
  in-process MCP client/server pair via `mcp.NewInMemoryTransports()` (see
  `connectInProcess` in `server_test.go`) rather than mocking the SDK.
* Run with `task test` (`go test ./...`); `task test:json-report` /
  `task test:junit-report` for CI-style output (the latter needs `gotestsum`).
* **Integration tests** (`internal/integration`) spin up three fake
  "services" in-process: the real MCP server, a mock ReportPortal HTTP
  server, and a mock LLM client — driven entirely by JSON fixtures in
  Postman Collection v2.1.0 request/response shape. Read
  `internal/integration/README.md` before adding a fixture; the short version:
  - Each fixture has `reportPortalMock.requestResponsePairs` (what the mock RP
    server returns) and `llmClientMock.request`/`expectedResponse` (what the
    mock LLM client sends to the MCP server and expects back).
  - Run with `task test:integration`.
* **`testdata/*.json` fixtures are dual-purpose**: they drive
  `internal/integration` tests AND can be replayed against a **live** running
  MCP server with `cmd/verify-testdata` (`task verify:testdata`) to catch
  drift against a real ReportPortal instance. Read
  `internal/integration/testdata/README.md` for the full authoring workflow —
  the critical rule is **never commit real credentials or project keys**:
  always use the placeholders `Bearer test-token-1234567` and
  `X-Project: test-project`; the verify tool substitutes real
  `RP_API_TOKEN`/`RP_PROJECT` from the environment at replay time. Use
  **real** test item/launch IDs from a test ReportPortal project (not
  fabricated numbers) so replay-verification is meaningful.
* When you touch shared plumbing (auth injection, TLS, pagination, project
  resolution), check whether an existing regression test already covers the
  behavior (e.g. `TestNewServer_BearerTokenSentWithTLSConfig`) before adding a
  new one — extend the existing test if the scenario is a variant of it.

## 10. Local debugging

* `task debug` / `task debug MCP_MODE=http` — builds with debug symbols and
  launches under Delve (`dlv exec --headless`), listening on `DLV_PORT`
  (default `52202`); attach your IDE's Go debugger to that port.
* `task inspector` / `task inspector-debug` — runs the
  [MCP Inspector](https://github.com/modelcontextprotocol/inspector) against
  a Dockerized build of the server (optionally under Delve) for interactive
  tool/prompt exploration without wiring up a full AI assistant.
* `debug.dockerfile` / `IMAGE_NAME_DEBUG` — the Docker-based debug image used
  by `inspector-debug`.

## 11. Dependency updates

`go.mod`/`go.sum` are kept current partly via Dependabot-style bump commits
(see `git log` — e.g. "Bump golang.org/x/net ..."). When bumping a dependency
manually, run `go mod tidy` (also part of `task checks`) and re-run
`task test` + `task lint` before opening a PR — `golangci-lint`'s `gosec`
linter in particular can start flagging new code paths pulled in by a
transitive dependency bump.
