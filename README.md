# ReportPortal MCP Server

This repository contains a ReportPortal MCP Server. 
It allows users to interact with ReportPortal directly from GitHub Copilot / Claude / etc chat to query and analyze test execution results.

## Features

The ReportPortal MCP server provides the following functionality:

- List launches with pagination
- Get launch details by name
- Filter launches using various criteria
- View test execution reports

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

| Variable     | Description                   | Default |
|--------------|-------------------------------|---------|
| `RP_HOST`    | ReportPortal host URL         |         |
| `RP_PROJECT` | ReportPortal project name     |         |
| `RP_TOKEN`   | ReportPortal API token        |         |
| `MCP_PORT`   | Port to run the MCP server on | `4389`  |

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

#### List Launches

Lists ReportPortal launches with pagination support.

Parameters:

- `page` (optional): Page number (default: 1)
- `page-size` (optional): Number of items per page (default: 20)

#### Get Last Launch by Name

Retrieves the most recent launch with the specified name.

Parameters:

- `launch`: Launch name to search for

#### Force Finish Launch

Forces a launch to finish regardless of its current state.

Parameters:
- `launch_id`: ID of the launch to force finish

#### Delete Launch

Deletes a specific launch from ReportPortal.

Parameters:
- `launch_id`: ID of the launch to delete

#### Get Last Launch by Filter

Retrieves the most recent launch matching specified filters.

Parameters:

- `name` (optional): Filter by launch name
- `description` (optional): Filter by launch description
- `uuid` (optional): Filter by launch UUID
- `status` (optional): Filter by launch status (IN_PROGRESS, PASSED, FAILED, STOPPED, SKIPPED, INTERRUPTED)
- `start_time` (optional): Filter by start time (unix timestamp)
- `end_time` (optional): Filter by end time (unix timestamp)
- `attributes` (optional): Filter by attributes (comma-separated key:value pairs)
- `mode` (optional): Filter by launch mode (DEFAULT or DEBUG)
- `sort` (optional): Sort direction and field (default: "desc(startTime)")

#### List Test Items by Launch

Lists test items for a specific launch with pagination support.

Parameters:
- `launch-id`: ID of the launch to get test items for
- `page` (optional): Page number (default: 1)
- `page-size` (optional): Number of items per page (default: 20)

#### Get Test Item by ID

Retrieves details of a specific test item.

Parameters:
- `test_item_id`: ID of the test item to retrieve

### Available Prompts

#### Analyze Launch

Analyzes a ReportPortal launch and provides detailed information about test results, failures, and statistics.

Parameters:
- `launch_id`: ID of the launch to analyze

### Available Resources

#### Launch Resource

Provides structured access to launch data with the following properties:
- Basic launch info (ID, name, description)
- Test execution statistics
- Timing information
- Status and execution mode

Based on your request, I'll update the Development section of your README.md with details on how to run and debug the MCP server, especially using modelcontextprotocol/inspector:

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

# Run tests with coverage
task test:coverage
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

## License

This project is licensed under the [Apache 2.0 License](LICENSE).
