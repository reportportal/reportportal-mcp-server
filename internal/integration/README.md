# Integration Tests

This package provides integration tests for the ReportPortal MCP Server using a three-service architecture:

1. **MCP Server** - The actual ReportPortal MCP Server being tested
2. **LLM Client Mock** - Simulates an LLM client making requests to the MCP Server
3. **ReportPortal Mock Server** - Mocks the ReportPortal API responses

## Test Structure

Tests are defined using JSON files based on the Postman Collection v2.1.0 schema format.

### Test Case Format

Each test case file (`testdata/*.json`) contains:

```json
{
  "name": "Test Case Name",
  "description": "Description of the test",
  "reportPortalMock": {
    "requestResponsePairs": [
      {
        "request": { /* Postman request format */ },
        "response": { /* Postman response format */ }
      }
    ]
  },
  "llmClientMock": {
    "request": { /* Postman request format - LLM client request to MCP Server */ },
    "expectedResponse": { /* Postman response format - Expected MCP Server response */ }
  }
}
```

### ReportPortal Mock Configuration

The `reportPortalMock` section defines request/response pairs that the ReportPortal Mock Server will handle. When the MCP Server makes a request to ReportPortal, the mock server matches it against these pairs and returns the corresponding response.

**Example:**

```json
"reportPortalMock": {
  "requestResponsePairs": [
    {
      "request": {
        "method": "GET",
        "header": [
          {
            "key": "Authorization",
            "value": "Bearer test-token-123"
          }
        ],
        "url": {
          "path": ["api", "v1", "project", "launch"],
          "query": [
            {
              "key": "page.page",
              "value": "1"
            }
          ]
        }
      },
      "response": {
        "code": 200,
        "header": [
          {
            "key": "Content-Type",
            "value": "application/json"
          }
        ],
        "body": "{ \"content\": [] }"
      }
    }
  ]
}
```

### LLM Client Mock Configuration

The `llmClientMock` section defines:
- `request`: The request that the LLM client mock will send to the MCP Server
- `expectedResponse`: The expected response from the MCP Server

**Example:**

```json
"llmClientMock": {
  "request": {
    "method": "POST",
    "header": [
      {
        "key": "Content-Type",
        "value": "application/json"
      },
      {
        "key": "Authorization",
        "value": "Bearer test-token-123"
      }
    ],
    "body": {
      "mode": "raw",
      "raw": "{\"jsonrpc\": \"2.0\", \"method\": \"tools/call\", \"id\": 1, \"params\": {...}}"
    },
    "url": {
      "path": ["mcp"]
    }
  },
  "expectedResponse": {
    "code": 200,
    "body": "{\"jsonrpc\": \"2.0\", \"id\": 1, \"result\": {...}}"
  }
}
```

## Running Tests

### Run all integration tests:

```bash
go test ./internal/integration/... -v
```

### Run a specific test:

```bash
go test ./internal/integration/... -v -run TestIntegration
```

### Run tests from Postman collections:

```bash
go test ./internal/integration/... -v -run TestIntegrationFromPostmanCollection
```

## Test Flow

1. **Setup Phase:**
   - Start ReportPortal Mock Server with predefined request/response pairs
   - Create and start MCP Server pointing to the ReportPortal Mock Server
   - Create LLM Client Mock pointing to the MCP Server

2. **Execution Phase:**
   - LLM Client Mock sends request to MCP Server
   - MCP Server processes request and makes API calls to ReportPortal Mock Server
   - ReportPortal Mock Server matches requests and returns predefined responses
   - MCP Server processes ReportPortal responses and returns result to LLM Client Mock

3. **Validation Phase:**
   - Validate that MCP Server response matches expected response
   - Verify that ReportPortal Mock Server received expected requests

## Adding New Test Cases

1. Create a new JSON file in `testdata/` (or `collections/` at the project root for Postman collection format).
2. Follow the test case format described above.
3. Provide deterministic mock responses so the MCP server can replay them without hitting the real API.
4. Keep every credential in the fixture fake (e.g., `Bearer test-token-123`) so files are safe to share.
5. Run `go test ./internal/integration/... -v` to validate.

### Recommended authoring workflow

To make sure recorded fixtures stay close to the real MCP behaviour:

1. **Author the JSON** – describe the ReportPortal mock exchange plus the LLM → MCP request and expected response.
2. **Hit the live MCP server** – using the same request body, call the real server at `http://localhost:8080/mcp`:
   - export your real credentials into the terminal session (`$env:RP_API_TOKEN`, `$env:RP_PROJECT` or set the `X-Project` header).
   - call `initialize` first, capture the returned `mcp-session-id`, and reuse it in subsequent `tools/call` requests.
   - compare the live response payload with the JSON file and adjust the fixture when the structure differs (e.g., IDs, fields that changed in ReportPortal).
3. **Never persist secrets** – RP tokens, project names, and any other sensitive data must stay outside the JSON files. When the tests run in Cursor/CI, those values come from environment variables, so the fixtures themselves remain generic.

Following this loop (record → verify against live MCP → commit) keeps the integration tests representative without leaking real credentials.

## Postman Collection Support

You can also define tests using Postman Collection format. Place collection files in the `collections/` directory at the project root. The test runner will automatically discover and run them.

**Note:** Postman collections are stored separately from test fixtures (`testdata/`) to avoid confusion between the two formats.

## Components

### ReportPortalMockServer

- Matches incoming HTTP requests against predefined request/response pairs
- Supports flexible matching of URLs, headers, query parameters, and request bodies
- Logs all incoming requests for debugging

### LLMClientMock

- Sends HTTP requests to the MCP Server
- Supports JSON-RPC MCP protocol requests
- Validates responses against expected format

### Integration Test Orchestrator

- Manages lifecycle of all three services
- Coordinates test execution
- Validates results


