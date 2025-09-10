package mcpreportportal

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
)

// createHTTPClient creates a reusable HTTP client with optimal settings
func createHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     timeout,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true, // HTTP/2 always enabled for optimal performance
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// HTTPServerConfig holds configuration for the HTTP-enabled MCP server
type HTTPServerConfig struct {
	Version         string
	HostURL         *url.URL
	FallbackRPToken string
	DefaultProject  string
	UserID          string
	GA4Secret       string
	AnalyticsOn     bool

	// HTTP settings
	MaxConcurrentRequests int           // Chi Throttle limit
	ConnectionTimeout     time.Duration // Request timeout
	// HTTP/2 is always enabled for optimal performance
}

// HTTPServer is an enhanced MCP server with Chi router
type HTTPServer struct {
	mcpServer  *server.MCPServer
	analytics  *Analytics
	config     HTTPServerConfig
	chiRouter  *ChiRouter
	httpClient *http.Client // Direct HTTP client instead of ConnectionManager

	// State management
	running    bool
	runningMux sync.RWMutex
}

// NewHTTPServer creates a new enhanced HTTP server with Chi router
func NewHTTPServer(config HTTPServerConfig) (*HTTPServer, error) {
	// Set defaults
	if config.MaxConcurrentRequests <= 0 {
		config.MaxConcurrentRequests = runtime.NumCPU() * 10 // HTTP-level concurrency limit
	}
	if config.ConnectionTimeout <= 0 {
		config.ConnectionTimeout = 30 * time.Second
	}

	// Create base MCP server
	mcpServer := server.NewMCPServer(
		"reportportal-mcp-server",
		config.Version,
		server.WithRecovery(),
		server.WithLogging(),
		server.WithResourceCapabilities(true, true),
		server.WithToolCapabilities(true),
	)

	// Create HTTP client
	httpClient := createHTTPClient(config.ConnectionTimeout)

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
			slog.Warn("Failed to initialize hybrid analytics", "error", err)
		} else {
			slog.Info("HTTP server initialized with hybrid analytics",
				"has_ga4_secret", config.GA4Secret != "",
				"has_fallback_token", config.FallbackRPToken != "",
				"max_concurrent_requests", config.MaxConcurrentRequests)
		}
	}

	httpServer := &HTTPServer{
		mcpServer:  mcpServer,
		analytics:  analytics,
		config:     config,
		httpClient: httpClient,
	}

	// Initialize tools and resources
	if err := httpServer.initializeTools(); err != nil {
		return nil, fmt.Errorf("failed to initialize tools: %w", err)
	}

	// Initialize Chi router
	httpServer.chiRouter = NewChiRouter(httpServer, analytics)

	return httpServer, nil
}

// initializeTools sets up all MCP tools
func (hs *HTTPServer) initializeTools() error {
	// Create ReportPortal client with HTTP client
	rpClient := gorp.NewClient(hs.config.HostURL, hs.config.FallbackRPToken)

	// Use HTTP client
	rpClient.APIClient.GetConfig().HTTPClient = hs.httpClient
	rpClient.APIClient.GetConfig().Middleware = QueryParamsMiddleware

	// Add launch management tools with analytics
	launches := NewLaunchResources(rpClient, hs.config.DefaultProject, hs.analytics)

	hs.mcpServer.AddTool(launches.toolGetLaunches())
	hs.mcpServer.AddTool(launches.toolGetLastLaunchByName())
	hs.mcpServer.AddTool(launches.toolForceFinishLaunch())
	hs.mcpServer.AddTool(launches.toolDeleteLaunch())
	hs.mcpServer.AddTool(launches.toolRunAutoAnalysis())
	hs.mcpServer.AddTool(launches.toolUniqueErrorAnalysis())
	hs.mcpServer.AddTool(launches.toolRunQualityGate())

	hs.mcpServer.AddResourceTemplate(launches.resourceLaunch())

	// Add test item tools
	testItems := NewTestItemResources(rpClient, hs.config.DefaultProject, hs.analytics)

	hs.mcpServer.AddTool(testItems.toolGetTestItemById())
	hs.mcpServer.AddTool(testItems.toolGetTestItemsByFilter())
	hs.mcpServer.AddTool(testItems.toolGetTestItemLogsByFilter())
	hs.mcpServer.AddTool(testItems.toolGetTestItemAttachment())
	hs.mcpServer.AddTool(testItems.toolGetTestSuitesByFilter())

	hs.mcpServer.AddResourceTemplate(testItems.resourceTestItem())

	// Add prompts
	prompts, err := readPrompts(promptFiles, "prompts")
	if err != nil {
		return fmt.Errorf("failed to load prompts: %w", err)
	}

	for _, prompt := range prompts {
		hs.mcpServer.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return nil
}

// Start starts the HTTP server
func (hs *HTTPServer) Start() error {
	hs.runningMux.Lock()
	defer hs.runningMux.Unlock()

	if hs.running {
		return fmt.Errorf("server is already running")
	}

	hs.running = true
	slog.Info("HTTP server started successfully",
		"max_concurrent_requests", hs.config.MaxConcurrentRequests,
		"connection_timeout", hs.config.ConnectionTimeout)

	return nil
}

// Stop gracefully shuts down the HTTP server
func (hs *HTTPServer) Stop() error {
	hs.runningMux.Lock()
	defer hs.runningMux.Unlock()

	if !hs.running {
		return nil
	}

	slog.Info("Stopping HTTP server")

	// Stop analytics
	if hs.analytics != nil {
		hs.analytics.Stop()
	}

	hs.running = false
	slog.Info("HTTP server stopped successfully")
	return nil
}

// GetMCPServer returns the underlying MCP server
func (hs *HTTPServer) GetMCPServer() *server.MCPServer {
	return hs.mcpServer
}

// GetChiRouter returns the Chi router for HTTP routing
func (hs *HTTPServer) GetChiRouter() chi.Router {
	if hs.chiRouter == nil {
		return nil
	}
	return hs.chiRouter.GetRouter()
}

// CreateHTTPServerWithMiddleware creates a streamable HTTP server with middleware support
// This provides the interface expected by main.go for the streaming server
func CreateHTTPServerWithMiddleware(
	config HTTPServerConfig,
) (*server.StreamableHTTPServer, *Analytics, error) {
	// Create the enhanced HTTP server
	httpServer, err := NewHTTPServer(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Create streamable HTTP server wrapper
	streamableServer := server.NewStreamableHTTPServer(httpServer.GetMCPServer())

	slog.Info("Streamable HTTP server created with Chi router",
		"max_concurrent_requests", config.MaxConcurrentRequests,
		"analytics_enabled", config.AnalyticsOn,
		"chi_router_enabled", true)

	return streamableServer, httpServer.analytics, nil
}

// CreateChiHTTPServerWithMiddleware creates a Chi-enhanced HTTP server with MCP integration
// This provides both REST API endpoints and MCP functionality
func CreateChiHTTPServerWithMiddleware(
	config HTTPServerConfig,
) (*ChiMCPServerWrapper, *Analytics, error) {
	// Create the enhanced HTTP server
	httpServer, err := NewHTTPServer(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Create streamable HTTP server wrapper for MCP functionality
	streamableServer := server.NewStreamableHTTPServer(httpServer.GetMCPServer())

	// Create Chi wrapper that combines both
	wrapper := NewChiMCPServerWrapper(httpServer, streamableServer)

	slog.Info("Chi HTTP server created with MCP integration",
		"max_concurrent_requests", config.MaxConcurrentRequests,
		"analytics_enabled", config.AnalyticsOn,
		"chi_router_enabled", true,
		"rest_api_enabled", true)

	return wrapper, httpServer.analytics, nil
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
