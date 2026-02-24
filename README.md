![Build Status](https://github.com/reportportal/reportportal-mcp-server/workflows/Build/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/reportportal/reportportal-mcp-server)](https://goreportcard.com/report/github.com/reportportal/goRP)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

# ReportPortal MCP Server

## What is the ReportPortal MCP Server?

The ReportPortal MCP Server is a bridge between your ReportPortal instance and AI chat assistants (such as Claude Desktop, GitHub Copilot, Cursor). In simple terms, it lets you ask questions in plain English about your test runs and get answers directly from ReportPortal. It follows the official [MCP](https://modelcontextprotocol.io/overview) guidelines.

For example, instead of logging into the ReportPortal UI, you could ask your AI assistant "What tests failed in the last run?" or "List the 5 most recent test runs," and it will fetch that information from ReportPortal for you. This makes it easy for QA testers and managers to query test results using natural language, speeding up analysis and reporting.

## Why Use It?

- **Quick Test Insights**: Instantly retrieve summaries of test runs, failure counts, or error details without writing code or navigating the UI.
- **Chat-Based Queries**: Use your favourite AI assistant (Claude, Cursor, etc.) to converse with ReportPortal data. It's like having a smart test-reporting helper in your chat window.
- **Integration Flexibility**: Works with any MCP-compatible AI tool. You simply point the tool at this server and it can run ReportPortal queries under the hood.
- **No Custom Scripts Needed**: Common queries (listing runs, getting failures, analysis) are built-in as simple "commands" you invoke via chat.

## Prerequisites

Before setting up the MCP server, you need the following information from your ReportPortal instance:

### ReportPortal Host URL (`RP_HOST`)

The URL of your ReportPortal instance:
- Example: `https://reportportal.example.com`
- For local: `http://localhost:8080`

### Project Name (`RP_PROJECT`) — Optional

This value is optional. When set, it defines the default project used for all requests; individual tools can still override it per request. To find your project name:
1. Log into ReportPortal
2. Check the URL: `https://your-rp-instance.com/ui/#PROJECT_NAME/...`
3. Or find it in the top-left dropdown menu

### API Token (`RP_API_TOKEN`)

You can get an API token from your ReportPortal Profile or generate a new one.
**Security Note:** Never commit tokens to version control or share them publicly.

### Server Mode (`MCP_MODE`)

`MCP_MODE` is a system environment variable that controls the transport mode of the MCP server:

| Value | Description |
|-------|-------------|
| `stdio` | **(default)** Standard input/output – used for local AI tool integrations (Claude Desktop, Cursor, VS Code Copilot, etc.) |
| `http` | HTTP/SSE streaming – exposes an HTTP endpoint for remote or multi-client access |

This variable is only needed when you want to run the server in HTTP mode. For local AI tool integrations the default `stdio` mode is used and no extra configuration is required.

> **Important:** `http` mode is intended for server deployments. The MCP server must be **deployed and running** with `MCP_MODE=http` before any AI tool can connect to it remotely. See the [For developers](#for-developers) section for deployment instructions.

<a name="installation"></a>
## Installation

There are two ways to connect to the ReportPortal MCP Server:
1. **Locally** - via *Docker* (recommended) or using *pre-built binaries*.
2. **Connecting to a remote MCP server** (when the server is already deployed)

Each of these methods is suitable for any LLM provider.

### Local installation

The configurations below use the default `stdio` mode (`MCP_MODE=stdio`), which is the correct choice for all local AI tool integrations. To run the server in HTTP mode instead, add `MCP_MODE=http` to the `env` block (see the [For developers](#for-developers) section for details).

#### Via Docker (recommended)

The MCP server is available on the official ReportPortal's [DockerHub](https://hub.docker.com/r/reportportal/mcp-server).

Configuration:
```json
{
  "reportportal": {
    "command": "docker",
    "args": [
      "run",
      "-i",
      "--rm",
      "-e",
      "RP_API_TOKEN",
      "-e",
      "RP_HOST",
      "-e",
      "RP_PROJECT",
      "reportportal/mcp-server"
    ],
    "env": {
      "RP_API_TOKEN": "your-api-token",
      "RP_HOST": "https://your-reportportal-instance.com",
      "RP_PROJECT": "YourProjectInReportPortal"
    }
  }
}
```

#### Using pre-built binaries

The OS pre-built binaries can be downloaded from the official releases on [GitHub](https://github.com/reportportal/reportportal-mcp-server/releases).

Configuration:
```json
{
  "reportportal": {
    "command": "/path/to/reportportal-mcp-server-binary",
    "args": ["stdio"],
    "env": {
      "RP_API_TOKEN": "your-api-token",
      "RP_HOST": "https://your-reportportal-instance.com",
      "RP_PROJECT": "YourProjectInReportPortal"
    }
  }
}
```

### Connecting to a Remote MCP Server

If the ReportPortal MCP Server is already **deployed** and accessible via HTTP, you can connect to it remotely without running it locally. This is useful when the server is hosted centrally or in a shared environment.

> **Note:** The remote server must be **deployed and running** in HTTP mode (system environment variable `MCP_MODE=http`) before any AI tool can connect to it. See the [For developers](#for-developers) section for deployment and configuration details.

**Remote Server Configuration:**

```json
{
  "reportportal": {
    "url": "http://your-mcp-server-host:port/mcp",
    "headers": {
      "Authorization": "Bearer ${RP_API_TOKEN}",
      "X-Project": "YourProjectInReportPortal"
    }
  }
}
```

**Configuration Parameters:**
- `url`: The HTTP endpoint URL of the remote MCP server (use `/mcp` or `/api/mcp`)
- `headers.Authorization`: Bearer token for authentication (required)
- `headers.X-Project`: The ReportPortal project name (optional)

## AI Tool Setup

Choose your favourite AI Tool to connect.

### Cursor (AI Code Editor)

Just click

[![Install MCP Server](https://cursor.com/deeplink/mcp-install-dark.svg)](https://cursor.com/en/install-mcp?name=reportportal&config=eyJjb21tYW5kIjoiZG9ja2VyIHJ1biAtaSAtLXJtIC1lIFJQX0FQSV9UT0tFTiAtZSBSUF9IT1NUIC1lIFJQX1BST0pFQ1QgcmVwb3J0cG9ydGFsL21jcC1zZXJ2ZXIiLCJlbnYiOnsiUlBfQVBJX1RPS0VOIjoieW91ci1hcGktdG9rZW4iLCJSUF9IT1NUIjoiaHR0cHM6Ly95b3VyLXJlcG9ydHBvcnRhbC1pbnN0YW5jZS5jb20iLCJSUF9QUk9KRUNUIjoiWW91clByb2plY3RJblJlcG9ydFBvcnRhbCJ9fQ%3D%3D)

Or follow the next steps:

1. In Cursor, go to **Settings** then **Tools & MCP** and click **Add Custom MCP**.
2. That will open file `mcp.json` where you need to add a new MCP server entry that runs the ReportPortal MCP Server:

**For local installation (Docker or binary):**

> Choose your preferred configuration from the [Installation section](#installation) and paste it inside the `reportportal` block.

```json
{
  "mcpServers": {
    "reportportal": {
      // paste your chosen configuration here
    }
  }
}
```

**For remote server:**

> **Note:** The remote server must be **deployed and running** in HTTP mode before connecting.

```json
{
  "mcpServers": {
    "reportportal": {
      "url": "http://your-mcp-server-host:port/mcp/",
      "headers": {
        "Authorization": "Bearer ${RP_API_TOKEN}",
        "X-Project": "YourProjectInReportPortal"
      }
    }
  }
}
```

Documentation: [Cursor MCP](https://cursor.com/en-US/docs/context/mcp).

### GitHub Copilot (In VS Code and JetBrains IDEs)

#### VS Code

1. Install/update the GitHub Copilot plugin.
2. Press `Ctrl+P` and type `>mcp` in the search bar and select **MCP: Open User Configuration**.
3. That will open file `mcp.json` where you need to add a new MCP server entry that runs the ReportPortal MCP Server:

**For local installation (Docker or binary):**

> Choose your preferred configuration from the [Installation section](#installation) and paste it inside the `reportportal` block.

```json
{
  "servers": {
    "reportportal": {
      // paste your chosen configuration here
    }
  }
}
```

**For remote server:**

> **Note:** The remote server must be **deployed and running** in HTTP mode before connecting.

```json
{
  "servers": {
    "reportportal": {
      "url": "http://your-mcp-server-host:port/mcp/",
      "requestInit": {
        "headers": {
          "Authorization": "Bearer ${RP_API_TOKEN}",
          "X-Project": "YourProjectInReportPortal"
        }
      }
    }
  }
}
```

Documentation: [VS Code Copilot Guide](https://code.visualstudio.com/docs/copilot/chat/mcp-servers).

#### JetBrains IDEs

1. Install/update the GitHub Copilot plugin.
2. Click **GitHub Copilot icon in the status bar → Edit Settings → Model Context Protocol → Configure**.
3. Add configuration:

**For local installation (Docker or binary):**

> Choose your preferred configuration from the [Installation section](#installation) and paste it inside the `reportportal` block.

```json
{
  "servers": {
    "reportportal": {
      // paste your chosen configuration here
    }
  }
}
```

**For remote server:**

> **Note:** The remote server must be **deployed and running** in HTTP mode before connecting.

```json
{
  "servers": {
    "reportportal": {
      "url": "http://your-mcp-server-host:port/mcp/",
      "requestInit": {
        "headers": {
          "Authorization": "Bearer ${RP_API_TOKEN}",
          "X-Project": "YourProjectInReportPortal"
        }
      }
    }
  }
}
```

4. Press `Ctrl + S` or `Command + S` to save, or close the `mcp.json` file. The configuration should take effect immediately and restart all the MCP servers defined. You can restart the IDE if needed.

Documentation: [JetBrains Copilot Guide](https://plugins.jetbrains.com/plugin/17718-github-copilot).

### Claude Desktop

1. Open Claude Desktop, go to **Settings → Developer → Edit Config**.
2. Add a new MCP server entry that runs the ReportPortal MCP Server.

**For local installation (Docker or binary):**

> Choose your preferred configuration from the [Installation section](#installation) and paste it inside the `reportportal` block.

```json
{
  "mcpServers": {
    "reportportal": {
      // paste your chosen configuration here
    }
  }
}
```
3. Save and restart Claude Desktop.

**For remote server:**

Claude Desktop does not natively support direct Server-Sent Events (SSE) transport for remote Model Context Protocol (MCP) servers, as it is designed to communicate primarily via local standard I/O (stdio). To connect to a remote SSE server, you must use a local wrapper script or bridging tool (e.g., mcp-remote, npx) in your claude_desktop_config.json to bridge stdio to SSE.

### Claude Code CLI

1. Open your terminal.
2. Run one of the following commands.

**For local installation (Docker):**
```bash
claude mcp add-json reportportal '{"command": "docker", "args": ["run", "-i", "--rm", "-e", "RP_API_TOKEN", "-e", "RP_HOST", "-e", "RP_PROJECT", "reportportal/mcp-server"], "env": {"RP_API_TOKEN": "${RP_API_TOKEN}", "RP_HOST": "https://your-reportportal-instance.com", "RP_PROJECT": "YourProjectInReportPortal"}}'
```

**For remote server:**

> **Note:** The remote server must be **deployed and running** in HTTP mode before connecting.

```bash
claude mcp add-json reportportal '{"url": "http://your-mcp-server-host:port/mcp/", "headers": {"Authorization": "Bearer ${RP_API_TOKEN}", "X-Project": "YourProjectInReportPortal"}}'
```

**Configuration Options:**
- Use `-s user` to add the server to your user configuration (available across all projects).
- Use `-s project` to add the server to project-specific configuration (shared via `.mcp.json`).
- Default scope is `local` (available only to you in the current project).

Documentation: [Claude Code guide](https://docs.anthropic.com/en/docs/claude-code/mcp).

### Other Coding Agents

The ReportPortal MCP Server is compatible with any MCP-compatible coding agent. While the exact configuration format may vary, most agents support either:

**Local installation (stdio mode):**
```json
{
  "reportportal": {
    "command": "docker",
    "args": ["run", "-i", "--rm", "-e", "RP_API_TOKEN", "-e", "RP_HOST", "-e", "RP_PROJECT", "reportportal/mcp-server"],
    "env": {
      "RP_API_TOKEN": "your-api-token",
      "RP_HOST": "https://your-reportportal-instance.com",
      "RP_PROJECT": "YourProjectInReportPortal"
    }
  }
}
```

**Remote server (HTTP mode):**

> **Note:** The remote server must be **deployed and running** in HTTP mode before connecting.

```json
{
  "reportportal": {
    "url": "http://your-mcp-server-host:port/mcp/",
    "headers": {
      "Authorization": "Bearer your-api-token",
      "X-Project": "YourProjectInReportPortal"
    }
  }
}
```

Please refer to your coding agent's documentation for the exact configuration format and where to place the configuration file.

Once connected, your AI assistant will list ReportPortal-related "tools" it can invoke. You can then ask your questions in chat, and the assistant will call those tools on your behalf.

> **Note:** After completing the setup, see [Verifying Your Setup](#verifying-your-setup) section for instructions on how to test your configuration.

## ReportPortal compatibility

It is strongly recommended to use the **latest versions** of ReportPortal.

The version 1.x of this MCP server supports ReportPortal product versions from [25.1](https://github.com/reportportal/reportportal/releases/tag/25.1) (where the API service version not lower than [5.14.0](https://github.com/reportportal/service-api/releases/tag/5.14.0)).\
Compatibility with older versions has not been tested and may result in incorrect work of the MCP server.

## Features

The ReportPortal MCP server provides a comprehensive set of capabilities for interacting with ReportPortal:

### Launch Management
- Get and filter launches (test runs) with pagination
- Get launch details by name or ID
- Force-finish running launches
- Delete launches
- Run automated analysis (auto analysis, unique error analysis, quality gate) on launches

### Test Item Analysis

- Get test items within by filter
- Get detailed information on each test item
- View test execution statistics and failures
- Retrieve test logs and attachments
- Make a decision on test result by updating test item defect types

### Report Generation

- Analyze launches to get detailed test execution insights
- Generate structured reports with statistics and failure analysis

### Available Tools (commands)

| Tool Name                  | Description                                      | Parameters                                                                                                    |
|----------------------------|--------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| Get Launches by filter            | Lists ReportPortal launches with pagination by filter      |  `name`, `description`, `owner`, `number`, `start_time`, `end_time`, `attributes`, `sort`, `page`, `page-size` (all optional)                                                                     |
| Get Last Launch by Name    | Retrieves the most recent launch by name         | `launch` (required)                                                                                                      |
| Get Launch by ID           | Retrieves a specific launch by its ID directly   | `project` (optional, string), `launch_id` (required, string)                                                                 |
| Run Quality Gate          | Runs quality gate analysis on a launch           | `launch_id` (required), `project` (optional)                                          |
| Run Auto Analysis          | Runs auto analysis on a launch                   | `launch_id` (required), `analyzer_mode` (optional), `analyzer_type` (optional), `analyzer_item_modes` (optional)                                          |
| Run Unique Error Analysis  | Runs unique error analysis on a launch           | `launch_id` (required), `remove_numbers` (optional)                                                                                 |
| Force Finish Launch        | Forces a launch to finish                        | `launch_id` (required)                                                                                                   |
| Delete Launch              | Deletes a specific launch                        | `launch_id` (required)                                                                                                   |
| Get Suites by filter  | Lists test suites for a specific launch           | `launch-id` (required), `name`, `description`, `start_time_from`, `start_time_to`, `attributes`, `parent_id`, `sort`, `page`, `page-size` (all optional)                                                        |
| Get Test Items by filter  | Lists test items for a specific launch           | `launch-id` (required), `name`, `description`, `status`, `has_retries`, `start_time_from`, `start_time_to`, `attributes`, `parent_id`, `defect_comment`, `auto_analyzed`, `ignored_in_aa`, `pattern_name`, `ticket_id`, `sort`, `page`, `page-size` (all optional)                                                        |
| Get Logs by filter  | Lists logs for a specific test item or nested step          | `parent-item-id` (required), `log_level`, `log_content`, `logs_with_attachments`, `status`, `sort`, `page`, `page-size` (all optional)                                                        |
| Get Attachment by ID        | Retrieves an attachment binary by id        | `attachment-content-id` (required)                                                                                                |
| Get Test Item by ID        | Retrieves details of a specific test item        | `test_item_id` (required)                                                                                                |
| Get Project Defect Types        | Retrieves available defect types for the specific project        | None                                                                                              |
| Update defect types by item ids        | Updates defect types for multiple test items        |`test_items_ids` (required), `defect_type_id` (required), `defect_type_comment` (optional)                                                                                               |

### Available Prompts

#### Analyze Launch

Analyzes a ReportPortal launch and provides detailed information about test results, failures, and statistics.

Parameters:
- `launch_id`: ID of the launch to analyze

You can follow the [prompt text and structure](https://github.com/reportportal/reportportal-mcp-server/blob/main/internal/reportportal/prompts/launch.yaml) as a reference while working on your own prompts.

### Example Queries (Natural Language)

Here are some real-world examples of what you might ask your AI after setup (the assistant's response will be drawn from ReportPortal data):

- **"List the 5 most recent test launches."** – returns a paginated list of recent test runs with names and statuses.
- **"What tests failed in the latest run?"** – shows failed test items for the most recent launch.
- **"Show me details of launch with ID 119000."** – retrieves a specific launch directly by its ID without pagination.
- **"Show me details of launch with number 1234."** – fetches information (ID, name, description, stats) for that specific launch.
- **"Run quality gate on launch 12345."** – executes quality gate analysis to verify if launch meets defined quality criteria.
- **"Run an analysis on launch ABC."** – triggers the ReportPortal's auto-analysis to summarize results and failures for launch "ABC".
- **"Finish the running launch with ID 4321."** – forces a currently running test launch to stop.
- **"Show me the top five 500-level errors in the last hour"** - lists the top 5 such errors from the recent test results.

Each query above corresponds to a "tool" provided by the MCP server, but you just phrase it naturally.
The AI will invoke the correct command behind the scenes.
These features let you query and manage your test reports in many ways through simple chat interactions.

## For developers

### Prerequisites
- Go 1.24.4 or later
- A ReportPortal instance

### Building from Source

```bash
# Clone the repository
git clone https://github.com/reportportal/reportportal-mcp-server.git
cd reportportal-mcp-server

# Build the binary
go build -o reportportal-mcp-server ./cmd/reportportal-mcp-server
```

This creates an executable called `reportportal-mcp-server`.

### Configuration

The server needs to know where your ReportPortal is and how to authenticate. Set these environment variables in your shell:

**For stdio mode (default):**

| Variable | Description | Required |
|----------|-------------|----------|
| `RP_HOST` | The URL of your ReportPortal (e.g. https://myreportportal.example.com) | Yes |
| `RP_PROJECT` | Your default project name in ReportPortal | Optional |
| `RP_API_TOKEN` | Your ReportPortal API token (for access) | Yes |

**For HTTP mode:**

Set `MCP_MODE=http` and configure the following:
- `RP_HOST`: Required - The URL of your ReportPortal
- `RP_PROJECT`: Optional - Your default project name
- `MCP_SERVER_PORT`: Optional - HTTP server port (default: 8080)
- `MCP_SERVER_HOST`: Optional - HTTP bind host (default: empty)
- Authentication tokens must be passed per-request via `Authorization: Bearer <token>` header
- `RP_API_TOKEN` environment variable is **not used** in HTTP mode

**Example for stdio mode:**

```bash
export RP_HOST="https://your-reportportal-instance.com"
export RP_PROJECT="YourProjectInReportPortal"
export RP_API_TOKEN="your-api-token"
./reportportal-mcp-server
```

**Example for HTTP mode:**

```bash
export MCP_MODE=http
export RP_HOST="https://your-reportportal-instance.com"
export RP_PROJECT="YourProjectInReportPortal"
export MCP_SERVER_PORT=8080
./reportportal-mcp-server
# Tokens are passed per-request via HTTP Authorization header
```

### HTTP API Endpoints

When running in HTTP mode (`MCP_MODE=http`), the server exposes the following endpoints:

#### MCP Endpoints (for tool calls and MCP protocol)

- **`POST /mcp`** - Main MCP endpoint for JSON-RPC requests
- **`POST /api/mcp`** - Alternative MCP endpoint (same functionality)
- **`GET /mcp`** - SSE (Server-Sent Events) stream endpoint for MCP protocol
- **`GET /api/mcp`** - Alternative SSE stream endpoint

**Important:** POST requests must be sent to `/mcp` or `/api/mcp`, not to the root endpoint `/`.

**Request Format:**

All MCP requests must follow the JSON-RPC 2.0 specification:

```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "id": 1,
  "params": {
    "name": "get_launches",
    "arguments": {
      "filter-cnt-name": "test",
      "page": 1,
      "page-size": 10
    }
  }
}
```

**Example Request:**

```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-reportportal-token" \
  -d '{
    "jsonrpc": "2.0",
    "method": "tools/call",
    "id": 1,
    "params": {
      "name": "get_launches",
      "arguments": {
        "page": 1,
        "page-size": 10
      }
    }
  }'
```

#### Information Endpoints (GET only)

- **`GET /`** - Root endpoint, returns server information and available endpoints
- **`GET /health`** - Health check endpoint
- **`GET /info`** - Server information and configuration
- **`GET /api/status`** - Server status (same as `/info`)
- **`GET /metrics`** - Analytics metrics (if analytics enabled)

**Note:** The root endpoint `/` only accepts GET requests. POST requests to `/` will return a 404 error. Use `/mcp` or `/api/mcp` for MCP protocol requests.

### Starting the Server

The server will start in the mode specified by `MCP_MODE` environment variable (default: stdio).

Once running, the MCP server is ready to accept queries from your AI tool.

### Development

To set up a development environment or contribute:

### Task Tool
Install Go Task v3:
```bash
go install github.com/go-task/task/v3/cmd/task@latest
```

### Dependencies
Run task deps to install Go dependencies:
```bash
task deps
```

### Build
```bash
task build
```

### Tests
```bash
task test
```

### Build with Docker
```bash
task docker:build
```

### Debugging with MCP Inspector
The [modelcontextprotocol/inspector](https://github.com/modelcontextprotocol/inspector) tool is useful for testing and debugging MCP servers locally:

```bash
npx @modelcontextprotocol/inspector docker run -i --rm -e "RP_API_TOKEN=$RP_API_TOKEN" -e "RP_PROJECT=$RP_PROJECT" -e "RP_HOST=$RP_HOST" reportportal-mcp-server
```

Alternatively, you can use the Task command:

```bash
# Run inspector against your local server
task inspector
```

### Code Quality

```bash
# Lint
task lint

# Format
task fmt
```

### Extending the Server

#### Adding new Tools

To add a new tool, create a new method in the appropriate resource file and add it to the server in the `NewServer` function:

```go
// In your resource file (e.g., launches.go)
func (lr *LaunchResources) toolNewFeature() (tool mcp.Tool, handler server.ToolHandlerFunc) {
    // Implement your tool
}

// In server.go
func NewServer(...) *server.MCPServer {
    // ...
    s.AddTool(launches.toolNewFeature())
    // ...
}
```

#### Adding new Prompts

To add a new prompt, simply create a YAML file describing your prompt and place it in the `prompts` folder at the root of the project. The server will automatically read and initialize all prompts from this directory on startup—no code changes are required.

**Example:**

1. Use an existing or create a new file, e.g., `my_custom_prompt.yaml`, in the `prompts` folder.
2. Define your prompt logic and parameters in YAML format.
3. Rebuild the server to load the new prompt.

This approach allows you to extend the server's capabilities with custom prompts quickly and without modifying the codebase.

## Verifying Your Setup

### 1. Verify ReportPortal Accessibility

Before testing the MCP server, ensure your ReportPortal instance is accessible:

**Via Browser:**
1. Open your ReportPortal URL (`RP_HOST`) in a web browser
2. You should see the ReportPortal login page or dashboard
3. Verify you can log in with your credentials

**Via Command Line (curl):**

```bash
# Test basic connectivity
curl -I https://your-reportportal-instance.com

# Test API access with your token
curl -H "Authorization: Bearer your-api-token" \
     https://your-reportportal-instance.com/api/v1/YourProject/launch
```

**Via PowerShell:**

```powershell
# Test basic connectivity
Invoke-WebRequest -Uri "https://your-reportportal-instance.com" -Method Head

# Test API access
$headers = @{
    "Authorization" = "Bearer your-api-token"
}
Invoke-RestMethod -Uri "https://your-reportportal-instance.com/api/v1/YourProject/launch" -Headers $headers
```

**Expected Results:**
- HTTP 200 OK response
- Valid JSON response with launch data (if project has launches)
- No authentication errors (401) or forbidden errors (403)

### 2. Verify Remote MCP Server (if using remote deployment)

If connecting to a remote MCP server, verify it's accessible:

**Via Browser:**

```text
http://your-mcp-server-host:port/
```

Should return server information and available endpoints.

**Via curl (GET request):**

```bash
# Check server health
curl http://your-mcp-server-host:port/health

# Check server info
curl http://your-mcp-server-host:port/info
```

**Via curl (MCP protocol test):**

```bash
# Test MCP endpoint with JSON-RPC request
curl -X POST http://your-mcp-server-host:port/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-api-token" \
  -H "X-Project: YourProject" \
  -d '{
    "jsonrpc": "2.0",
    "method": "tools/list",
    "id": 1
  }'
```

**Via PowerShell:**

```powershell
# Check server health
Invoke-RestMethod -Uri "http://your-mcp-server-host:port/health"

# Test MCP endpoint
$headers = @{
    "Content-Type" = "application/json"
    "Authorization" = "Bearer your-api-token"
    "X-Project" = "YourProject"
}
$body = @{
    jsonrpc = "2.0"
    method = "tools/list"
    id = 1
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://your-mcp-server-host:port/mcp" -Method Post -Headers $headers -Body $body
```

**Via ping (network connectivity only):**

```bash
ping your-mcp-server-host
```

**Expected Results:**
- Server responds to health checks
- `/info` returns server configuration
- MCP endpoint returns list of available tools
- No connection refused or timeout errors

### 3. Verify MCP Server Integration

After configuration, verify the AI assistant can communicate with the MCP server:

**Step 1: Check Available Tools**

Ask your AI assistant:

```text
"What ReportPortal tools are available?"
```

Expected response: A list of 15 tools including launches, test items, analysis tools, etc.

**Step 2: Test Basic Query**

Try a simple query:

```text
"List the 5 most recent test launches"
```

Expected response: A formatted list of recent launches with names, statuses, and timestamps.

**Step 3: Check Server Logs**

Monitor logs to verify requests are being processed:

**Docker:**

```bash
# View logs
docker logs <container-name>

# Follow logs in real-time
docker logs -f <container-name>

# View last 50 lines
docker logs --tail 50 <container-name>
```

**Binary (stdio mode):**
Check the terminal output where the server is running. You should see log entries for each request.

**Binary (HTTP mode):**

```bash
# If redirecting to file
./reportportal-mcp-server > server.log 2>&1

# Then view logs
tail -f server.log
```

**Expected Log Output:**

```text
INFO: MCP server started
INFO: Received request: tools/list
INFO: Processing tool call: get_launches
DEBUG: Calling ReportPortal API: /api/v1/project/launch
```

### 4. Common Verification Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| AI shows no ReportPortal tools | MCP server not connected | Check configuration file syntax, restart AI assistant |
| "Connection refused" error | Server not running or wrong port | Verify server is running: `docker ps` or check process |
| "401 Unauthorized" | Invalid API token | Regenerate token in ReportPortal profile |
| "403 Forbidden" | Token valid but no project access | Check `RP_PROJECT` name, verify user has access |
| "404 Not Found" | Wrong endpoint URL | Ensure remote URL ends with `/mcp/` |
| Empty results from queries | No data in ReportPortal | Run some tests first to populate data |
| Timeout errors | Network issues or slow ReportPortal | Check network connectivity, ReportPortal performance |

## Troubleshooting

### Connection Issues

**Problem: Cannot connect to ReportPortal**

Symptoms:
- "Connection refused" errors
- "Could not resolve host" errors
- Timeout errors

Solutions:
1. Verify `RP_HOST` is correct and includes protocol (`https://` or `http://`)
2. Check network connectivity: `ping your-reportportal-host`
3. Verify ReportPortal is running and accessible
4. Check firewall rules allow connection to ReportPortal
5. For HTTPS, verify SSL certificates are valid

**Problem: Cannot connect to remote MCP server**

Symptoms:
- "Connection refused" on MCP server
- "No route to host" errors

Solutions:
1. Verify MCP server is running: Check with `curl http://host:port/health`
2. Check `MCP_MODE=http` is set on the server
3. Verify port is correct (default: 8080)
4. Check firewall/security groups allow access to the port
5. Ensure server is bound to correct network interface (`MCP_SERVER_HOST`)

### Authentication Issues

**Problem: 401 Unauthorized errors**

Solutions:
1. Verify API token is correct and not expired
2. Regenerate token in ReportPortal: Profile → API Keys → Generate New Token
3. Check token is properly set in environment variable or configuration
4. For remote servers, verify `Authorization: Bearer <token>` header is sent
5. Ensure no extra spaces or quotes around the token value

**Problem: 403 Forbidden errors**

Solutions:
1. Verify user has access to the specified project
2. Check project name matches exactly (case-sensitive)
3. Verify user role has sufficient permissions
4. For remote servers, check `X-Project` header is set correctly

### Docker Issues

**Problem: Docker container exits immediately**

Solutions:
1. Check container logs: `docker logs <container-name>`
2. Verify all required environment variables are set
3. Ensure environment variable values don't have syntax errors
4. Check Docker has permission to access the network

**Problem: "docker: command not found"**

Solutions:
1. Install Docker Desktop for your OS
2. Verify Docker is running: `docker --version`
3. For Linux, add user to docker group: `sudo usermod -aG docker $USER`

### AI Assistant Integration Issues

**Problem: AI assistant doesn't recognize MCP server**

Solutions:
1. Verify configuration file syntax is valid JSON
2. Check configuration file is in the correct location
3. Restart the AI assistant after configuration changes
4. For Cursor/VS Code, check MCP extension is installed and enabled
5. Review AI assistant logs for error messages

**Problem: Tools list is empty**

Solutions:
1. Verify MCP server is running and accessible
2. Check server logs for startup errors
3. Ensure server mode matches client configuration (stdio vs HTTP)
4. For remote servers, verify URL ends with `/mcp/`

### Performance Issues

**Problem: Slow responses or timeouts**

Solutions:
1. Check ReportPortal API performance (test with curl)
2. Reduce page size in queries (`page-size` parameter)
3. Use filters to limit data retrieval
4. Check network latency between server and ReportPortal
5. Monitor server resources (CPU, memory)

**Problem: High memory usage**

Solutions:
1. Reduce concurrent requests
2. Limit result set sizes with pagination
3. Restart the MCP server periodically
4. Check for memory leaks in logs

### Configuration Issues

**Problem: "Environment variable not set"**

Solutions:
1. Verify variable names are correct: `RP_HOST`, `RP_PROJECT`, `RP_API_TOKEN`
2. For Docker, check `-e` flags are specified correctly
3. For binary, export variables in the same shell session
4. Use `echo $VAR_NAME` (Linux/Mac) or `$env:VAR_NAME` (PowerShell) to verify

**Problem: "Invalid JSON" configuration errors**

Solutions:
1. Validate JSON syntax using a JSON validator
2. Remove trailing commas in JSON objects
3. Ensure all strings are properly quoted
4. Check for comments (JSON doesn't support comments)
5. Verify escape characters in paths (use `\\` in Windows paths)

### Getting Help

If you're still experiencing issues:

1. **Check Logs**: Enable debug logging and review detailed error messages
2. **Test Components**: Verify each component separately (ReportPortal → MCP Server → AI Assistant)
3. **GitHub Issues**: Search or create an issue at [reportportal-mcp-server](https://github.com/reportportal/reportportal-mcp-server/issues)
4. **Documentation**: Review [MCP specification](https://modelcontextprotocol.io/) and [ReportPortal API docs](https://reportportal.io/docs/)
5. **Community**: Join ReportPortal community for support

## License

This project is licensed under the [Apache 2.0 License](LICENSE).
