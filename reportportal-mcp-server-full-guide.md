# ReportPortal MCP Server

## What is the ReportPortal MCP Server?

The ReportPortal MCP Server is a bridge between your ReportPortal system and AI chat assistants (such as Claude Desktop, GitHub Copilot, Cursor). In simple terms, it lets you ask questions in plain English about your test runs and get answers directly from ReportPortal. 

For example, instead of logging into the ReportPortal UI, you could ask your AI assistant "What tests failed in the last run?" or "List the 5 most recent test runs," and it will fetch that information from ReportPortal for you. This makes it easy for QA testers and managers to query test results using natural language, speeding up analysis and reporting.

## Why Use It?

- **Quick Test Insights**: Instantly retrieve summaries of test runs, failure counts, or error details without writing code or navigating the UI.
- **Chat-Based Queries**: Use your AI assistant (Claude, Cursor, etc.) to converse with ReportPortal data. It's like having a smart test-reporting helper in your chat window.
- **Integration Flexibility**: Works with any MCP-compatible AI tool. You simply point the tool at this server and it can run ReportPortal queries under the hood.
- **No Custom Scripts Needed**: Common queries (listing runs, getting failures, analysis) are built-in as simple "commands" you invoke via chat.


// TODO!!!!!!!!!!!!!!!!!!!!


## Installing and Running the MCP Server

### Prerequisites

You need Go (version 1.24.1 or later) to build the server, and of course access to a ReportPortal instance. Alternatively, you can use a provided Docker image if you prefer containers.

### Build the Server

Clone the GitHub repo and compile the binary. For example:

```bash
git clone https://github.com/reportportal/reportportal-mcp-server.git
cd reportportal-mcp-server
go build -o reportportal-mcp-server ./cmd/reportportal-mcp-server
```

This creates an executable called `reportportal-mcp-server`.

### Configure Connection

The server needs to know where your ReportPortal is and how to authenticate. Set these environment variables in your shell:

| Variable | Description | Required |
|----------|-------------|----------|
| `RP_HOST` | The URL of your ReportPortal (e.g. https://myreportportal.example.com) | Yes |
| `RP_PROJECT` | Your default project name in ReportPortal | Optional |
| `RP_API_TOKEN` | Your ReportPortal API token (for access) | Yes |

For example:

```bash
export RP_HOST="https://your-reportportal-instance.com"
export RP_PROJECT="ExampleProject"
export RP_API_TOKEN="your-api-token"
```

### Run the Server

Simply run the binary. By default it listens on port 4389 (you can override with `MCP_PORT` if needed).

```bash
./reportportal-mcp-server
```

Once running, the MCP server is ready to accept queries from your AI tool.

## Connecting to AI Chat Tools (Claude, Cursor, etc.)

After the MCP server is running, configure your AI assistant to use it:

### Claude Desktop
Open Claude, go to Settings → Developer → Edit Config. Add a new MCP server entry that runs the `reportportal-mcp-server` command with the same environment variables. (The exact steps mirror how you'd add any local MCP server.) Save and restart Claude.

### Cursor (AI Code Editor)
In Cursor, go to Settings → Extensions → MCP and add a new global MCP server. Enter the command to run `reportportal-mcp-server` and set the `RP_HOST`, `RP_PROJECT`, and `RP_API_TOKEN` environment variables. Cursor's setup is identical to VS Code's (since it's based on VS Code).

// TODO!!!!!!!!!!!!!!!!!!!!


### Other Tools
Any tool supporting the Model Context Protocol (MCP) can connect. You generally just tell it to run `reportportal-mcp-server` (with needed env vars) as a local "tool." The AI will then discover available commands from this server.

Once connected, your AI assistant will list ReportPortal-related "tools" it can invoke. You can then ask your questions in chat, and the assistant will call those tools on your behalf.

## Example Queries (Natural Language)

Here are some real-world examples of what you might ask your AI after setup (the assistant's response will be drawn from ReportPortal data):

- **"List the 5 most recent test launches."** – returns a paginated list of recent test runs with names and statuses.
- **"What tests failed in the latest run?"** – shows failed test items for the most recent launch.
- **"Show me details of launch Build #1234."** – fetches information (ID, name, description, stats) for that specific launch.
- **"Run an analysis on launch ABC."** – triggers the built-in auto-analysis tool to summarize results and failures for launch ABC.
- **"What are the unique error messages in launch XYZ?"** – uses the unique error analysis tool to find recurring failures in that run.
- **"Finish the running launch with ID 4321."** – forces a currently running test launch to stop.

> **Note**: For reference, a similar example query is demonstrated by asking "Show me the top five 500-level errors in the last hour" in a Claude chat example. You would replace "500-level errors" with queries about test failures or launches in ReportPortal.

Each query above corresponds to a "tool" provided by the MCP server, but you just phrase it naturally. The AI will invoke the correct command behind the scenes.

## Features

The ReportPortal MCP server provides a comprehensive set of capabilities for interacting with ReportPortal:

### Launch Management
- List and filter launches (test runs) with pagination
- Get launch details by name or ID
- Force-finish running launches
- Delete launches
- Run automated analyses (auto analysis, unique error analysis) on launches

### Test Item Analysis
- List test items within launches
- Get detailed information on each test item
- View test execution statistics and failures
- Retrieve test logs and attachments

### Report Generation
- Analyze launches to get detailed test execution insights
- Generate structured reports with statistics and failure analysis

### Extensibility
- Add custom tools by writing new code
- Add custom natural-language prompts via YAML files in the `prompts/` folder which are auto-loaded by the server on startup (available while buildind and running the server from sources)

These features let you query and manage your test reports in many ways through simple chat interactions.

## Key Commands (Tools) Explained

The MCP server comes with several built-in commands (tools) you can use. Here are the most important ones, in simple terms:

| Tool | Description |
|------|-------------|
| **List Launches** | Lists test launches (runs) in ReportPortal, with optional paging. Use this to see recent or filtered runs. |
| **Get Launch by Name (Last Launch)** | Finds the most recent launch with a given name, returning its details (ID, stats, etc.). |
| **Force Finish Launch** | Stops a running launch immediately (useful if a test run hangs). |
| **Delete Launch** | Deletes a specific launch (remove old test runs). |
| **List Test Items by Launch** | Lists all test items (individual tests) within a given launch. You can page through the results. |
| **Get Test Item by ID** | Retrieves detailed information about one specific test item by its ID. |
| **Run Auto Analysis** | Performs automated analysis on a launch (gathers stats and failure insights). |
| **Run Unique Error Analysis** | Finds and groups unique error messages in a launch's failures. |

These tools cover the main interactions you'll need (listing runs, getting details, managing runs, and analyzing results). You simply invoke them by writing a natural question; the AI assistant translates it into the appropriate command call. For example, asking "List launches in project Foo" will use the List Launches tool with the right parameters.

## For developers

### Prerequisites
- Go 1.24.1 or later
- A running ReportPortal instance

### Building from Source

```bash
git clone https://github.com/reportportal/reportportal-mcp-server.git
cd reportportal-mcp-server
go build -o reportportal-mcp-server ./cmd/reportportal-mcp-server
```

This compiles the `reportportal-mcp-server` binary.

### Configuration

The server is configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `RP_HOST` | ReportPortal host URL | Required |
| `RP_API_TOKEN` | ReportPortal API token | Required |
| `RP_PROJECT` | Default project name | Optional |
| `MCP_PORT` | Port for the MCP server | `4389` |

Set these before running the server.

### Starting the Server

After configuring the env vars as above, simply run:

```bash
./reportportal-mcp-server
```

This will start the MCP server on the configured port.

Once it's running, connect your AI tool as described above and begin querying.

> **Note**: There is also an **Analyze Launch** prompt available, which summarizes a launch's results in one command.

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

### Docker
```bash
task docker:build
```

### Debugging
Use the MCP Inspector to test the MCP server locally. For example:

```bash
npx @modelcontextprotocol/inspector docker run -i --rm \
  -e "RP_API_TOKEN=$RP_API_TOKEN" -e "RP_PROJECT=$RP_PROJECT" -e "RP_HOST=$RP_HOST" \
  reportportal-mcp-server
```

This simulates an AI client calling your server locally.

### Code Quality

```bash
# Lint
task lint

# Format
task fmt
```

### Extending the Server

#### New Tools
Add Go methods in the internal code for new commands, then register them in `server.NewServer()`.

#### New Prompts
Write a YAML file in the top-level `prompts/` folder; the server will auto-load it on startup.

See the existing code and comments for examples on how to extend functionality.

## License

This project is licensed under the Apache 2.0 License.

---

## References

- [GitHub - reportportal/reportportal-mcp-server: MCP server for ReportPortal](https://github.com/reportportal/reportportal-mcp-server)
- [Connect to Local MCP Servers - Model Context Protocol](https://modelcontextprotocol.io/docs/tools/mcp-inspector)
- [Configure MCP Servers on VSCode, Cursor & Claude Desktop | Knowledge Share](https://www.cursor.com/knowledge-share)
- [How to Use the Observe MCP Server with Claude, Cursor, and Augment](https://observeinc.com/blog/mcp-server/)
