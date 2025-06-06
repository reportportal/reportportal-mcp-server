package mcpreportportal

import (
	"math"

	"github.com/mark3labs/mcp-go/mcp"
)

func newProjectParameter(defaultProject string) mcp.ToolOption {
	return mcp.WithString("project", // Parameter for specifying the project name)
		mcp.Description("Project name"),
		mcp.DefaultString(defaultProject),
		mcp.Required(),
	)
}

func extractProject(rq mcp.CallToolRequest) (string, error) {
	return rq.RequireString("project")
}

func extractPaging(request mcp.CallToolRequest) (int32, int32) {
	// Extract the "page" parameter from the request
	page := request.GetInt("page", firstPage)
	if page > math.MaxInt32 {
		page = math.MaxInt32
	}

	// Extract the "page-size" parameter from the request
	pageSize := request.GetInt("page-size", defaultPageSize)
	if pageSize > math.MaxInt32 {
		pageSize = math.MaxInt32
	}

	//nolint:gosec // the int32 is confirmed
	return int32(page), int32(pageSize)
}
