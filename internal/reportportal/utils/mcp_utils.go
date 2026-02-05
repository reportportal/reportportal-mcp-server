package utils

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

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
	} else if pageInt > math.MaxInt32 {
		pageInt = math.MaxInt32
	}

	// Extract the "page-size" parameter from the request
	pageSizeInt := request.GetInt("page-size", DefaultPageSize)
	if pageSizeInt <= 0 {
		pageSizeInt = DefaultPageSize
	} else if pageSizeInt > math.MaxInt32 {
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

func ExtractProject(ctx context.Context, rq mcp.CallToolRequest) (string, error) {
	// Use project parameter from request
	if project := strings.TrimSpace(rq.GetString("project", "")); project != "" {
		return project, nil
	}
	// Fallback to project from context (request's HTTP header or environment variable, depends on MCP mode)
	if project, ok := GetProjectFromContext(ctx); ok {
		return project, nil
	}
	return "", fmt.Errorf(
		"no project parameter found in request, HTTP header, or environment variable",
	)
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
