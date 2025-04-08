package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

const (
	firstPage       = 1  // Default starting page for pagination
	defaultPageSize = 20 // Default number of items per page
)

// LaunchResources is a struct that encapsulates the ReportPortal client.
type LaunchResources struct {
	client *gorp.Client // Client to interact with the ReportPortal API
}

// listLaunches creates a tool to retrieve a paginated list of launches from ReportPortal.
func (lr *LaunchResources) listLaunches() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_launches",
			// Tool metadata
			mcp.WithDescription("Get list of last ReportPortal launches"),
			mcp.WithNumber("page", // Parameter for specifying the page number
				mcp.DefaultNumber(firstPage),
				mcp.Description("Page number"),
			),
			mcp.WithNumber("page-size", // Parameter for specifying the page size
				mcp.DefaultNumber(defaultPageSize),
				mcp.Description("Page size"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract the "page" parameter from the request
			page, err := requiredParam[float64](request, "page")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract the "page-size" parameter from the request
			pageSize, err := requiredParam[float64](request, "page-size")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches from ReportPortal using the provided page details
			launches, err := lr.client.GetLaunchesPage(gorp.PageDetails{
				PageNumber: int(page),
				PageSize:   int(pageSize),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Serialize the launches into JSON format
			r, err := json.Marshal(launches)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launches as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}

// getLastLaunchByName creates a tool to retrieve the last launch by its name.
func (lr *LaunchResources) getLastLaunchByName() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_last_launch_by_name",
			// Tool metadata
			mcp.WithDescription("Get list of last ReportPortal launches"),
			mcp.WithString("launch", // Parameter for specifying the launch name
				mcp.Description("Launch name"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract the "launch" parameter from the request
			launchName, err := requiredParam[string](request, "launch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			launches, err := lr.client.GetLaunchesByFilterString("launch.name=" + launchName)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Check if any launches were found
			if len(launches.Content) < 1 {
				return mcp.NewToolResultError("No launches found"), nil
			}

			// Serialize the first launch in the result into JSON format
			r, err := json.Marshal(launches.Content[0])
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}
