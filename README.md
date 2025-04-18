# ReportPortal MCP Server

This repository contains a ReportPortal integration with GitHub Copilot chat through Microsoft
Copilot for Pull Requests (MCP). It allows users to interact with ReportPortal directly from GitHub
Copilot chat to query and analyze test execution results.

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

#### Get Last Launch by Filter

Retrieves the most recent launch matching specified filters.

Parameters:

- `name` (optional): Filter by launch name
- `description` (optional): Filter by launch description
- `uuid` (optional): Filter by launch UUID
- `status` (optional): Filter by launch status (IN_PROGRESS, PASSED, FAILED, STOPPED, SKIPPED,
  INTERRUPTED)
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

## Development

### Running Tests

```bash
go test ./...
```

### Adding New Tools

To add a new tool, create a new method in the appropriate resource file and add it to the server in
the `NewServer` function.

## License

This project is licensed under the [MIT License](LICENSE).