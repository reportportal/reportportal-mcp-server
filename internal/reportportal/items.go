package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

// TestItemResources is a struct that encapsulates the ReportPortal client.
type TestItemResources struct {
	client  *gorp.Client // Client to interact with the ReportPortal API
	project string
}

// toolListLaunchTestItems creates a tool to list test items for a specific launch.
func (lr *TestItemResources) toolListLaunchTestItems() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_test_items_by_launch",
			// Tool metadata
			mcp.WithDescription("Get list of test items for a launch"),
			mcp.WithNumber("launch-id", // ID of the launch
				mcp.Description("Launch ID"),
			),
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
			page, pageSize := extractPaging(request)

			launchId, err := request.RequireInt("launch-id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch test items from ReportPortal using the provided page details
			items, _, err := lr.client.TestItemAPI.GetTestItemsV2(ctx, lr.project).
				FilterEqLaunchId(int32(launchId)). //nolint:gosec
				PagePage(page).
				PageSize(pageSize).
				PageSort(defaultSorting).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Serialize the launches into JSON format
			r, err := json.Marshal(items)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launches as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}

// toolGetTestItemById creates a tool to retrieve a test item by its ID.
func (lr *TestItemResources) toolGetTestItemById() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_test_item_by_id",
			// Tool metadata
			mcp.WithDescription("Get test item by ID"),
			mcp.WithString("test_item_id", // Parameter for specifying the test item ID
				mcp.Description("Test Item ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract the "launch" parameter from the request
			testItemID, err := request.RequireString("test_item_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the testItem with given ID
			testItem, _, err := lr.client.TestItemAPI.GetTestItem(ctx, testItemID, lr.project).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Serialize the first launch in the result into JSON format
			r, err := json.Marshal(testItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}
