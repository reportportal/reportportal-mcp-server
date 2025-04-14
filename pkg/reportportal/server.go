package mcpreportportal

import (
	"fmt"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

func NewServer(client *gorp.Client) *server.MCPServer {
	s := server.NewMCPServer(
		"reportportal-mcp-server",
		"0.0.1",
		server.WithResourceCapabilities(true, true),
		server.WithLogging())
	launches := &LaunchResources{client: client}
	s.AddTool(launches.toolListLaunches())
	s.AddTool(launches.toolGetLastLaunchByName())
	s.AddPrompt(launches.promptAnalyzeLaunch())
	s.AddResourceTemplate(launches.resourceReportPortalLaunches())

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
