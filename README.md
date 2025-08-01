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

## Installation

There are two ways to run the latest version of the ReportPortal MCP Server.
Each of this way is suitable for any LLM provider.

### Via Docker (recommended).

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

### Using pre-built binaries.

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

Choose your favourite AI Tool to connect.

### Claude Desktop

1. Open Claude Desktop, go to **Settings → Developer → Edit Config**.
2. Add a new MCP server entry that runs the ReportPortal MCP Server.
```json
{
  "mcpServers": {
    "reportportal": {
      // choose the Docker or binary configuration from the section above
    }
  }
}
```
3. Save and restart Claude Desktop.

### Claude Code CLI

1. Open your terminal.
2. Run the following comman.
```bash
claude mcp add-json reportportal '{"command": "docker", "args": ["run", "-i", "--rm", "-e", "RP_API_TOKEN", "-e", "RP_HOST", "-e", "RP_PROJECT", "reportportal/mcp-server"], "env": {"RP_API_TOKEN": "your-api-token", "RP_HOST": "https://your-reportportal-instance.com", "RP_PROJECT": "YourProjectInReportPortal"}}'
```

**Configuration Options:**
- Use `-s user` to add the server to your user configuration (available across all projects).
- Use `-s project` to add the server to project-specific configuration (shared via `.mcp.json`).
- Default scope is `local` (available only to you in the current project).

Documentation: [Claude Code guide](https://docs.anthropic.com/en/docs/claude-code/mcp).

### Cursor (AI Code Editor)

1. In Cursor, go to **Settings → Extensions → MCP** and click to add a new global MCP server.
2. Add a new MCP server entry that runs the ReportPortal MCP Server.
```json
{
  "mcpServers": {
    "reportportal": {
      // choose the Docker or binary configuration from the section above
    }
  }
}
```
Documentation: [Cursor MCP]([https://docs.anthropic.com/en/docs/claude-code/mcp](https://docs.cursor.com/en/tools/developers#example)).

### GitHub Copilot (In VS Code and JetBrains IDEs)

#### VS Code

1. Install/update the GitHub Copilot plugin.
2. Type **>mcp** in the search bar and select **MCP: Open User Configuration**.
3. Add configuration:
```json
{
  "servers": {
    "reportportal": {
      // choose the Docker or binary configuration from the section above
    }
  }
}
```

Documentation: [VS Code Copilot Guide](https://code.visualstudio.com/docs/copilot/chat/mcp-servers).

#### JetBrains IDEs

1. Install/update the GitHub Copilot plugin.
2. Click **GitHub Copilot icon in the status bar → Edit Settings → Model Context Protocol → Configure**.
3. Add configuration:
```json
{
  "servers": {
    "reportportal": {
      // choose the Docker or binary configuration from the section above
    }
  }
}
```
4. Press `Ctrl + S` or `Command + S` to save, or close the `mcp.json` file. The configuration should take effect immediately and restart all the MCP servers defined. You can restart the IDE if needed.

Documentation: [JetBrains Copilot Guide](https://plugins.jetbrains.com/plugin/17718-github-copilot).

Once connected, your AI assistant will list ReportPortal-related "tools" it can invoke. You can then ask your questions in chat, and the assistant will call those tools on your behalf.

## Features

The ReportPortal MCP server provides a comprehensive set of capabilities for interacting with ReportPortal:

### Launch Management
- Get and filter launches (test runs) with pagination
- Get launch details by name or ID
- Force-finish running launches
- Delete launches
- Run automated analysis (auto analysis, unique error analysis) on launches

### Test Item Analysis
- Get test items within by filter
- Get detailed information on each test item
- View test execution statistics and failures
- Retrieve test logs and attachments

### Report Generation
- Analyze launches to get detailed test execution insights
- Generate structured reports with statistics and failure analysis

### Available Tools (commands)

| Tool Name                  | Description                                      | Parameters                                                                                                    |
|----------------------------|--------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| Get Launches by filter            | Lists ReportPortal launches with pagination by filter      |  `name`, `description`, `owner`, `number`, `start_time`, `end_time`, `attributes`, `sort`, `page`, `page-size` (all optional)                                                                     |
| Get Last Launch by Name    | Retrieves the most recent launch by name         | `name`                                                                                                      |
| Run Auto Analysis          | Runs auto analysis on a launch                   | `launch_id`, `analyzer_mode`, `analyzer_type`, `analyzer_item_modes`                                          |
| Run Unique Error Analysis  | Runs unique error analysis on a launch           | `launch_id`, `remove_numbers`                                                                                 |
| Force Finish Launch        | Forces a launch to finish                        | `launch_id`                                                                                                   |
| Delete Launch              | Deletes a specific launch                        | `launch_id`                                                                                                   |
| Get Test Items by filter  | Lists test items for a specific launch           | `launch-id` (required), `name`, `description`, `status`, `has_retries`, `start_time`, `end_time`, `attributes`, `parent_id`, `defect_comment`, `auto_analyzed`, `ignored_in_aa`, `pattern_name`, `ticket_id`, `sort`, `page`, `page-size` (all optional)                                                        |
| Get Logs by filter  | Lists logs for a specific test item or nested step          | `parent-id` (required), `log_level`, `log_content`, `logs_with_attachments`, `status`, `sort`, `page`, `page-size` (all optional)                                                        |
| Get Attachment by ID        | Retrieves an attachment binary by id        | `attachment_id`                                                                                                |
| Run Unique Error Analysis  | Runs unique error analysis on a launch           | `launch_id`, `remove_numbers`                                                                                 |
| Get Test Item by ID        | Retrieves details of a specific test item        | `test_item_id`                                                                                                |

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
- **"Show me details of launch with number 1234."** – fetches information (ID, name, description, stats) for that specific launch.
- **"Run an analysis on launch ABC."** – triggers the ReportPortal's auto-analysis to summarize results and failures for launch "ABC".
- **"Finish the running launch with ID 4321."** – forces a currently running test launch to stop.
- **"Show me the top five 500-level errors in the last hour"** - lists the top 5 such errors from the recent test results.

Each query above corresponds to a "tool" provided by the MCP server, but you just phrase it naturally.
The AI will invoke the correct command behind the scenes.
These features let you query and manage your test reports in many ways through simple chat interactions.

## For developers

### Prerequisites
- Go 1.24.1 or later
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

| Variable | Description | Required |
|----------|-------------|----------|
| `RP_HOST` | The URL of your ReportPortal (e.g. https://myreportportal.example.com) | Yes |
| `RP_PROJECT` | Your default project name in ReportPortal | Optional |
| `RP_API_TOKEN` | Your ReportPortal API token (for access) | Yes |
| `MCP_PORT` | Port for the MCP server | `4389` |

For example:

```bash
export RP_HOST="https://your-reportportal-instance.com"
export RP_PROJECT="YourProjectInReportPortal"
export RP_API_TOKEN="your-api-token"
```

### Starting the Server

After configuring the env vars as above, simply run:

```bash
./reportportal-mcp-server
```

This will start the MCP server on the configured port.

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

## License

This project is licensed under the [Apache 2.0 License](LICENSE).
