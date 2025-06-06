![Build Status](https://github.com/reportportal/reportportal-mcp-server/workflows/Build/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/reportportal/reportportal-mcp-server)](https://goreportcard.com/report/github.com/reportportal/goRP)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

# ReportPortal MCP Server

This repository contains a ReportPortal MCP Server.
It allows users to interact with ReportPortal directly from GitHub Copilot / Claude / etc chat to query and analyze test execution results.

## Features

The ReportPortal MCP server provides a comprehensive set of capabilities for interacting with ReportPortal:

### Launch Management
- List and filter launches with pagination
- Get launch details by name or ID
- Force finish running launches
- Delete launches
- Run automated analysis on launches (auto analysis, unique error analysis)

### Test Item Analysis
- List test items within launches
- Get detailed test item information
- View test execution statistics and failures

### Report Generation
- Analyze launches to get detailed test execution insights
- Generate structured reports with statistics and failure analysis

### Extensibility
- Add custom tools through code extensions
- Define new prompts via YAML files in the `prompts` directory
- Access structured resource data for launches and test items

## Installation

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

## Configuration

The server uses environment variables for configuration:

| Variable       | Description                              | Default |
|----------------|------------------------------------------|---------|
| `RP_HOST`      | ReportPortal host URL                    |         |
| `RP_API_TOKEN` | ReportPortal API token                   |         |
| `RP_PROJECT`   | (optional) ReportPortal project name     |         |
| `MCP_PORT`     | (optional) Port to run the MCP server on | `4389`  |

## Usage

### Starting the Server

```bash
# Set required environment variables
export RP_HOST="https://your-reportportal-instance.com"
export RP_PROJECT="your-project"
export RP_TOKEN="your-api-token"

# Run the server
./reportportal-mcp-server
```

### Available Tools

| Tool Name                  | Description                                      | Parameters                                                                                                    |
|----------------------------|--------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| List Launches              | Lists ReportPortal launches with pagination      | `page` (optional), `page-size` (optional)                                                                     |
| Get Last Launch by Name    | Retrieves the most recent launch by name         | `launch`                                                                                                      |
| Force Finish Launch        | Forces a launch to finish                        | `launch_id`                                                                                                   |
| Delete Launch              | Deletes a specific launch                        | `launch_id`                                                                                                   |
| Get Last Launch by Filter  | Retrieves the most recent launch by filters      | `name`, `description`, `uuid`, `status`, `start_time`, `end_time`, `attributes`, `mode`, `sort` (all optional)|
| List Test Items by Launch  | Lists test items for a specific launch           | `launch-id`, `page` (optional), `page-size` (optional)                                                        |
| Get Test Item by ID        | Retrieves details of a specific test item        | `test_item_id`                                                                                                |
| Run Auto Analysis          | Runs auto analysis on a launch                   | `launch_id`, `analyzer_mode`, `analyzer_type`, `analyzer_item_modes`                                          |
| Run Unique Error Analysis  | Runs unique error analysis on a launch           | `launch_id`, `remove_numbers`                                                                                 |

### Available Prompts

#### Analyze Launch

Analyzes a ReportPortal launch and provides detailed information about test results, failures, and statistics.

Parameters:
- `launch_id`: ID of the launch to analyze

### Available Resources

| Resource Type | Description                         | Properties |
|---------------|-------------------------------------|------------|
| Launch Resource | Structured access to launch data    | • Basic launch info (ID, name, description)<br>• Test execution statistics<br>• Timing information<br>• Status and execution mode |
| Test Item Resource | Structured access to test item data | • Basic test item info (ID, name, description)<br>• Test execution status and type<br>• Parent information and hierarchy position<br>• Issue details (when applicable)<br>• Timing information (start time, end time, duration)<br>• Test attributes and parameters<br>• Path to the test in the test suite hierarchy |

This table format makes the available resources more scannable while preserving all the key information about each resource type.

## Development

### Setting up Development Environment
```bash
# Install Task
go install github.com/go-task/task/v3/cmd/task@latest

# Install dependencies
task deps
```

### Building
```bash
# Build the server
task build
```

### Running Tests
```bash
# Run all tests
task test
```

### Running the MCP Server
```bash
# Build Docker Image
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
# Run linters
task lint

# Format code
task fmt
```

### Docker

```bash
# Build Docker image
task docker:build
```

### Adding New Tools

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
### Adding New Prompts

To add a new prompt, simply create a YAML file describing your prompt and place it in the `prompts` folder at the root of the project. The server will automatically read and initialize all prompts from this directory on startup—no code changes are required.

**Example:**

1. Use an existing or create a new file, e.g., `my_custom_prompt.yaml`, in the `prompts` folder.
2. Define your prompt logic and parameters in YAML format.
3. Rebuild the server to load the new prompt.

This approach allows you to extend the server's capabilities with custom prompts quickly and without modifying the codebase.

## License

This project is licensed under the [Apache 2.0 License](LICENSE).
