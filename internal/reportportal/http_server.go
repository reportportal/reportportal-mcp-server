package mcpreportportal

import (
	"fmt"
	"log/slog"
	"net/url"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

// HTTPServerConfig holds configuration for HTTP-enabled MCP server
type HTTPServerConfig struct {
	Version         string
	HostURL         *url.URL
	FallbackRPToken string
	DefaultProject  string
	UserID          string
	GA4Secret       string
	AnalyticsOn     bool
}

// NewHTTPServer creates an MCP server with batch-based analytics
func NewHTTPServer(config HTTPServerConfig) (*server.MCPServer, *Analytics, error) {
	// Create base MCP server
	mcpServer := server.NewMCPServer(
		"reportportal-mcp-server",
		config.Version,
		server.WithRecovery(),
		server.WithLogging(),
		server.WithResourceCapabilities(true, true),
		server.WithToolCapabilities(true),
	)

	// Initialize batch-based analytics
	var analytics *Analytics
	if config.AnalyticsOn && config.FallbackRPToken != "" && config.GA4Secret != "" {
		var err error
		analytics, err = NewAnalytics(
			config.UserID,
			config.GA4Secret,
			config.FallbackRPToken,
		)
		if err != nil {
			slog.Warn("Failed to initialize analytics", "error", err)
		} else {
			slog.Info("HTTP MCP server initialized with batch-based analytics",
				"has_ga4_secret", config.GA4Secret != "",
				"has_token", config.FallbackRPToken != "")
		}
	}

	// Create ReportPortal client with fallback token
	rpClient := gorp.NewClient(config.HostURL, config.FallbackRPToken)
	rpClient.APIClient.GetConfig().Middleware = QueryParamsMiddleware

	// Add launch management tools with batch-based analytics
	launches := NewLaunchResources(rpClient, config.DefaultProject, analytics)
	mcpServer.AddTool(launches.toolGetLaunches())
	mcpServer.AddTool(launches.toolGetLastLaunchByName())
	mcpServer.AddTool(launches.toolForceFinishLaunch())
	mcpServer.AddTool(launches.toolDeleteLaunch())
	mcpServer.AddTool(launches.toolRunAutoAnalysis())
	mcpServer.AddTool(launches.toolUniqueErrorAnalysis())
	mcpServer.AddTool(launches.toolRunQualityGate())
	mcpServer.AddResourceTemplate(launches.resourceLaunch())

	// Add test item management tools with batch-based analytics
	testItems := NewTestItemResources(rpClient, config.DefaultProject, analytics)
	mcpServer.AddTool(testItems.toolGetTestItemById())
	mcpServer.AddTool(testItems.toolGetTestItemsByFilter())
	mcpServer.AddTool(testItems.toolGetTestItemLogsByFilter())
	mcpServer.AddTool(testItems.toolGetTestItemAttachment())
	mcpServer.AddTool(testItems.toolGetTestSuitesByFilter())
	mcpServer.AddResourceTemplate(testItems.resourceTestItem())

	// Add prompts
	prompts, err := readPrompts(promptFiles, "prompts")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load prompts: %w", err)
	}

	for _, prompt := range prompts {
		mcpServer.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return mcpServer, analytics, nil
}

// CreateHTTPServerWithMiddleware creates a complete HTTP server setup
func CreateHTTPServerWithMiddleware(
	config HTTPServerConfig,
) (*server.StreamableHTTPServer, *Analytics, error) {
	// Create the MCP server
	mcpServer, analytics, err := NewHTTPServer(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create MCP server: %w", err)
	}

	// Create HTTP server with batch-based analytics
	httpServer := server.NewStreamableHTTPServer(mcpServer)

	// Note: Direct HTTP middleware integration is not supported by mark3labs/mcp-go
	// Analytics are implemented via batch-based approach at handler level
	slog.Info(
		"HTTP MCP server created with batch-based analytics",
		"analytics_type", "batch",
		"analytics_enabled", analytics != nil,
	)

	return httpServer, analytics, nil
}

// GetHTTPServerInfo returns information about the HTTP server configuration
func GetHTTPServerInfo(analytics *Analytics) map[string]interface{} {
	info := map[string]interface{}{
		"type": "http_mcp_server",
	}

	if analytics != nil {
		info["analytics"] = map[string]interface{}{
			"enabled":  true,
			"type":     "batch",
			"interval": batchSendInterval.String(),
		}
	} else {
		info["analytics"] = map[string]interface{}{
			"enabled": false,
		}
	}

	return info
}
