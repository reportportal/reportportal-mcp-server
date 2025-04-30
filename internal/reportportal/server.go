package mcpreportportal

import (
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

func NewServer(version string, hostUrl *url.URL, token, project string) *server.MCPServer {
	// Create a new ReportPortal client
	rpClient := gorp.NewClient(hostUrl, token)

	s := server.NewMCPServer(
		"reportportal-mcp-server",
		version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
	)
	launches := &LaunchResources{client: rpClient, project: project}
	s.AddTool(launches.toolListLaunches())
	s.AddTool(launches.toolGetLastLaunchByName())
	s.AddPrompt(launches.promptAnalyzeLaunch())
	s.AddResourceTemplate(launches.resourceReportPortalLaunches())

	testItems := &TestItemResources{client: rpClient, project: project}
	s.AddTool(testItems.toolGetTestItemById())
	s.AddTool(testItems.toolListLaunchTestItems())

	return s
}

// requiredParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request.
// 2. Checks if the parameter is of the expected type.
// 3. Checks if the parameter is not empty, i.e: non-zero value.
func requiredParam[T comparable](args map[string]interface{}, p string) (T, error) {
	var zero T

	// Check if the parameter is present in the request
	if _, ok := args[p]; !ok {
		return zero, fmt.Errorf("missing required parameter: %s", p)
	}

	// Check if the parameter is of the expected type
	if _, ok := args[p].(T); !ok {
		return zero, fmt.Errorf("parameter %s is not of type %T", p, zero)
	}

	if args[p].(T) == zero {
		return zero, fmt.Errorf("missing required parameter: %s", p)
	}

	return args[p].(T), nil
}

// requiredParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is of the expected type.
// 2. Checks if the parameter is not empty, i.e: non-zero value.
func param[T comparable](args map[string]interface{}, p string) (T, bool, error) {
	var zero T

	// Check if the parameter is present in the request
	if _, ok := args[p]; !ok {
		return zero, false, nil
	}

	// Check if the parameter is of the expected type
	if _, ok := args[p].(T); !ok {
		return zero, true, fmt.Errorf("parameter %s is not of type %T", p, zero)
	}

	return args[p].(T), true, nil
}
