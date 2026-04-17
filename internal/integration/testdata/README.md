## Test Data Overview

This directory stores all JSON fixtures that drive the integration tests in `internal/integration`. Each file follows the Postman-style schema described in `internal/integration/README.md` and is safe to commit because **no real credentials live here**.

### Folder layout

This directory contains **test fixture files only**:

- `*.json` – Test fixture files for `verify-testdata` tool (e.g., `get_test_item_by_id_real.json`, `get_test_item_test.json`)
  - These files are verified against the live MCP server
  - Format: Contains `reportPortalMock` and `llmClientMock` sections

**Note:** Postman collection files are stored in a separate `collections/` directory at the project root level.

### Important: Use Generic Placeholders

All JSON fixtures must use **generic placeholders** instead of real values:

- ✅ **Authorization**: `Bearer test-token-1234567` (not your real token, min 16 chars)
- ✅ **X-Project**: `test-project` (not your real project name)
- ✅ **Test Item IDs**: Use real IDs that exist in your ReportPortal instance (e.g., `5273849853`)
- ✅ **URLs in reportPortalMock**: Use `test-project` as project name

**Why?** The verification tool (`task verify:testdata`) automatically substitutes:
- `test-token-1234567` → `$RP_API_TOKEN` (from environment)
- `test-project` → `$RP_PROJECT` (from environment)

This keeps fixtures portable and safe to commit while allowing validation against real servers.

### Authoring checklist

1. **Create the fixture**
   - Describe the ReportPortal mock exchange in `reportPortalMock`.
   - Describe the LLM→MCP request and the expected MCP response in `llmClientMock`.
   - Use generic placeholders: `Bearer test-token-1234567`, `X-Project: test-project`.
   - Use **real test item IDs** that exist in your ReportPortal project (not dummy IDs like `12345`).

2. **Validate against the real MCP server**
   - Export secrets in your shell:
     ```bash
     export RP_API_TOKEN="your-real-token"
     export RP_PROJECT="your-real-project"
     ```
   - Run the verification task:
     ```bash
     task verify:testdata
     ```
   - The tool will:
     - Initialize an MCP session to get `mcp-session-id`
     - Replay each test fixture against `http://localhost:8080/mcp`
     - Substitute `test-token-1234567` with your real `RP_API_TOKEN`
     - Substitute `test-project` with your real `RP_PROJECT`
     - Report which tests pass/fail

3. **Update fixtures based on real responses**
   - If the verification tool reports a failure, compare the expected vs. actual response.
   - Update the `expectedResponse.body` in your JSON fixture to match the real MCP server output.
   - Re-run `task verify:testdata` to confirm the fix.

4. **Keep secrets out of Git**
   - Never commit real tokens, project names, or other private values.
   - Always use `test-token-1234567` and `test-project` placeholders in JSON files.
   - The verification tool injects real values at runtime from environment variables.

Following this loop (create → verify → update → commit) keeps `testdata/` in sync with real MCP behavior while avoiding credential leaks.

### Automated Verification

The easiest way to validate all fixtures is using the automated verification tool:

```bash
# Set environment variables
export RP_API_TOKEN="your-real-token"
export RP_PROJECT="your-real-project"

# Run verification against MCP server at http://localhost:8080/mcp
task verify:testdata

# Or with custom port
task verify:testdata MCP_SERVER_PORT=9000

# Or run directly with Go
go run cmd/verify-testdata/main.go -url http://localhost:8080/mcp -dir testdata -v
```

The verification tool will:
1. Initialize an MCP session to obtain `mcp-session-id`
2. Discover all `*.json` files in `testdata/`
3. For each fixture:
   - Extract the `llmClientMock.request.body.raw` (the MCP tool call)
   - Send it to the live MCP server with real credentials
   - Check if the response is valid (no errors)
   - Report pass/fail status
4. Print a summary with total/success/failed/skipped counts

### Manual Verification (PowerShell)

For manual testing or debugging a single fixture:

```powershell
# Step 1: Initialize session
$headers = @{
  'Content-Type'  = 'application/json'
  'Authorization' = "Bearer $($env:RP_API_TOKEN)"
  'X-Project'     = $env:RP_PROJECT
}
$initBody = '{"jsonrpc":"2.0","method":"initialize","id":0,"params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"manual-test","version":"1.0"}}}'
$initResp = Invoke-WebRequest -Uri http://localhost:8080/mcp -Method Post -Headers $headers -Body $initBody
$sessionId = $initResp.Headers['mcp-session-id']
Write-Output "Session ID: $sessionId"

# Step 2: Call the tool
$headers['mcp-session-id'] = $sessionId
$body = '{"jsonrpc":"2.0","method":"tools/call","id":1,"params":{"name":"get_test_item_by_id","arguments":{"test_item_id":"5273849853"}}}'

try {
  $response = Invoke-WebRequest -Uri http://localhost:8080/mcp -Method Post -Headers $headers -Body $body
  Write-Output "STATUS: $($response.StatusCode)"
  $response.Content | Tee-Object -FilePath "$env:TEMP\mcp_response.json"
} catch {
  Write-Output "ERROR: $($_.Exception.Message)"
  if ($_.Exception.Response) {
    $reader = [System.IO.StreamReader]::new($_.Exception.Response.GetResponseStream())
    $reader.BaseStream.Position = 0
    $reader.ReadToEnd() | Tee-Object -FilePath "$env:TEMP\mcp_response.json"
  }
}
```

Swap the `name`/`arguments` in the `$body` to match the tool you are validating.

