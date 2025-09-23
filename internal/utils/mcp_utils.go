package utils

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// SetPaginationOptions returns the standard pagination parameters for MCP tools
func SetPaginationOptions(sortingParams string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("page", // Parameter for specifying the page number
			mcp.DefaultNumber(FirstPage),
			mcp.Description("Page number"),
		),
		mcp.WithNumber("page-size", // Parameter for specifying the page size
			mcp.DefaultNumber(DefaultPageSize),
			mcp.Description("Page size"),
		),
		mcp.WithString("page-sort", // Sorting fields and direction
			mcp.DefaultString(sortingParams),
			mcp.Description("Sorting fields and direction"),
		),
	}
}

// ApplyPaginationOptions extracts pagination from request and applies it to API request
func ApplyPaginationOptions[T PaginatedRequest[T]](
	apiRequest T,
	request mcp.CallToolRequest,
	sortingParams string,
) T {
	// Extract the "page" parameter from the request
	pageInt := request.GetInt("page", FirstPage)
	if pageInt < FirstPage {
		pageInt = FirstPage
	}
	if pageInt > math.MaxInt32 {
		pageInt = math.MaxInt32
	}

	// Extract the "page-size" parameter from the request
	pageSizeInt := request.GetInt("page-size", DefaultPageSize)
	if pageSizeInt < 1 {
		pageSizeInt = 1
	}
	if pageSizeInt > math.MaxInt32 {
		pageSizeInt = math.MaxInt32
	}

	// Extract the "page-sort" parameter from the request
	pageSort := request.GetString("page-sort", sortingParams)

	// Apply pagination directly
	return apiRequest.
		PagePage(int32(pageInt)).     //nolint:gosec
		PageSize(int32(pageSizeInt)). //nolint:gosec
		PageSort(pageSort)
}

func NewProjectParameter(defaultProject string) mcp.ToolOption {
	return mcp.WithString("project", // Parameter for specifying the project name)
		mcp.Description("Project name"),
		mcp.DefaultString(defaultProject),
		mcp.Required(),
	)
}

func ExtractProject(rq mcp.CallToolRequest) (string, error) {
	return rq.RequireString("project")
}

// EventTracker interface for analytics tracking
type EventTracker interface {
	TrackMCPEvent(ctx context.Context, toolName string)
}

// WithAnalytics wraps a tool handler to add analytics tracking
func WithAnalytics(
	tracker EventTracker,
	toolName string,
	handler server.ToolHandlerFunc,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Track the event before executing the tool (synchronous since it's just incrementing a counter)
		if tracker != nil {
			tracker.TrackMCPEvent(ctx, toolName)
		}

		// Execute the original handler
		return handler(ctx, request)
	}
}

// ReadResponseBody safely reads an HTTP response body and ensures proper cleanup.
// It handles the defer close pattern with graceful error handling and returns an MCP tool result.
// This is a convenience wrapper around readResponseBodyRaw for MCP tool results.
func ReadResponseBody(response *http.Response) (*mcp.CallToolResult, error) {
	rawBody, err := ReadResponseBodyRaw(response)
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("failed to read response body: %v", err),
		), nil
	}

	return mcp.NewToolResultText(string(rawBody)), nil
}
