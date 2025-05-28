package mcpreportportal

import (
	"math"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

func NewServer(version string, hostUrl *url.URL, token, project string) *server.MCPServer {
	s := server.NewMCPServer(
		"reportportal-mcp-server",
		version,
		server.WithRecovery(),
		server.WithLogging(),
		server.WithResourceCapabilities(true, true),
		server.WithToolCapabilities(true),
	)

	// Create a new ReportPortal client
	rpClient := gorp.NewClient(hostUrl, token)

	launches := &LaunchResources{client: rpClient, project: project}
	s.AddTool(launches.toolListLaunches())
	s.AddTool(launches.toolGetLastLaunchByName())
	s.AddTool(launches.toolForceFinishLaunch())
	s.AddTool(launches.toolDeleteLaunch())
	s.AddTool(launches.toolRunAutoAnalysis())
	s.AddTool(launches.toolUniqueErrorAnalysis())
	s.AddPrompt(launches.promptAnalyzeLaunch())
	s.AddResourceTemplate(launches.resourceLaunch())

	testItems := &TestItemResources{client: rpClient, project: project}
	s.AddTool(testItems.toolGetTestItemById())
	s.AddTool(testItems.toolListLaunchTestItems())

	return s
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
