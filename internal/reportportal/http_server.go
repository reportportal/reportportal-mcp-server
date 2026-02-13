package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/urfave/cli/v3"

	"github.com/reportportal/reportportal-mcp-server/internal/config"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
	mcphandlers "github.com/reportportal/reportportal-mcp-server/internal/reportportal/mcp_handlers"
	app_middleware "github.com/reportportal/reportportal-mcp-server/internal/reportportal/middleware"
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
	mcpServer         *mcp.Server
	AnalyticsInstance *analytics.Analytics
	config            HTTPServerConfig
	Router            chi.Router   // Made public for CreateHTTPServerWithMiddleware
	mcpHTTPHandler    http.Handler // Official SDK HTTP handler
	httpClient        *http.Client // Direct HTTP client instead of ConnectionManager

	// State management
	running atomic.Bool
}

// MCPRequestPayload represents the basic JSON-RPC structure of MCP requests
type MCPRequestPayload struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
}

// NewHTTPServer creates a new enhanced HTTP server with Chi router
func NewHTTPServer(
	config HTTPServerConfig,
) (*HTTPServer, error) { // Validate required configuration
	if config.HostURL == nil {
		return nil, fmt.Errorf("HostURL is required in HTTP server configuration")
	}

	// Set defaults
	if config.MaxConcurrentRequests <= 0 {
		config.MaxConcurrentRequests = runtime.NumCPU() * 2 // HTTP-level concurrency limit
	}
	if config.ConnectionTimeout <= 0 {
		config.ConnectionTimeout = 30 * time.Second
	}

	// Create base MCP server
	mcpServer := mcp.NewServer(
		&mcp.Implementation{
			Name:    "reportportal-mcp-server",
			Version: config.Version,
		},
		&mcp.ServerOptions{
			// Add server options as needed
		},
	)

	// Create HTTP client
	httpClient := createHTTPClient(config.ConnectionTimeout)

	// Initialize batch-based analytics
	// Note: In HTTP mode, FallbackRPToken is always empty (tokens come from HTTP headers).
	// Analytics uses UserID for identification in HTTP mode.
	var analyticsInstance *analytics.Analytics
	if config.AnalyticsOn && config.GA4Secret != "" {
		var err error
		analyticsInstance, err = analytics.NewAnalytics(
			config.UserID,
			config.GA4Secret,
			"",                      // FallbackRPToken is always empty in HTTP mode
			config.HostURL.String(), // ReportPortal host URL for instance ID
		)
		if err != nil {
			slog.Warn("Failed to initialize analytics", "error", err)
		} else {
			slog.Info("HTTP MCP server initialized with batch-based analytics",
				"has_ga4_secret", config.GA4Secret != "",
				"uses_user_id", config.UserID != "")
		}
	}

	httpServer := &HTTPServer{
		mcpServer:         mcpServer,
		AnalyticsInstance: analyticsInstance,
		config:            config,
		httpClient:        httpClient,
	}

	// Initialize tools and resources
	if err := httpServer.initializeTools(); err != nil {
		return nil, fmt.Errorf("failed to initialize tools: %w", err)
	}

	// Initialize Chi router directly
	httpServer.setupChiRouter()

	return httpServer, nil
}

// initializeTools sets up all MCP tools
func (hs *HTTPServer) initializeTools() error {
	// Create ReportPortal client with empty token in HTTP mode
	// The actual token will be injected per-request via QueryParamsMiddleware from HTTP headers
	rpClient := gorp.NewClient(hs.config.HostURL, hs.config.FallbackRPToken)

	// Use HTTP client
	rpClient.APIClient.GetConfig().HTTPClient = hs.httpClient
	rpClient.APIClient.GetConfig().Middleware = app_middleware.QueryParamsMiddleware

	// Register all launch-related tools and resources
	mcphandlers.RegisterLaunchTools(hs.mcpServer, rpClient, "", hs.AnalyticsInstance)

	// Register all test item-related tools and resources
	mcphandlers.RegisterTestItemTools(
		hs.mcpServer,
		rpClient,
		"",
		hs.AnalyticsInstance,
	)

	// Add prompts
	prompts, err := mcphandlers.ReadPrompts(mcphandlers.PromptFiles, "prompts")
	if err != nil {
		return fmt.Errorf("failed to load prompts: %w", err)
	}

	for _, prompt := range prompts {
		hs.mcpServer.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return nil
}

// HTTPServerWithMiddleware wraps HTTP server with token middleware
type HTTPServerWithMiddleware struct {
	Handler http.Handler
	MCP     *HTTPServer // Keep reference to underlying MCP server for lifecycle management
}

// Start starts the HTTP server
func (hs *HTTPServer) Start() error {
	if !hs.running.CompareAndSwap(false, true) {
		return fmt.Errorf("server is already running")
	}

	slog.Info("HTTP server started successfully",
		"connection_timeout", hs.config.ConnectionTimeout)

	return nil
}

// Stop gracefully shuts down the HTTP server
func (hs *HTTPServer) Stop() error {
	if !hs.running.CompareAndSwap(true, false) {
		return nil
	}

	slog.Info("Stopping HTTP server")

	// Stop analytics
	if hs.AnalyticsInstance != nil {
		hs.AnalyticsInstance.Stop()
	}

	slog.Info("HTTP server stopped successfully")
	return nil
}

// CreateHTTPServerWithMiddleware creates a complete HTTP server setup with middleware
func CreateHTTPServerWithMiddleware(
	config HTTPServerConfig,
) (*HTTPServerWithMiddleware, *analytics.Analytics, error) {
	// Create the MCP server with Chi router and middleware already configured
	mcpServer, err := NewHTTPServer(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create MCP server: %w", err)
	}

	// Use the Chi router as the handler (middleware already applied in setupRoutes)
	// The router includes HTTPTokenMiddleware for MCP routes and all other endpoints
	wrapper := &HTTPServerWithMiddleware{
		Handler: mcpServer.Router, // Use Chi router with all middleware
		MCP:     mcpServer,
	}

	slog.Info(
		"HTTP MCP server created with Chi router and integrated middleware",
		"router", "chi",
		"middleware", "HTTPTokenMiddleware+Throttle+Timeout",
		"analytics_type", "batch",
		"analytics_enabled", mcpServer.AnalyticsInstance != nil,
	)

	return wrapper, mcpServer.AnalyticsInstance, nil
}

// HTTPServerInfo provides typed information about HTTP server configuration
type HTTPServerInfo struct {
	Version               string        `json:"version"`
	MaxConcurrentRequests int           `json:"max_concurrent_requests"`
	ConnectionTimeout     string        `json:"connection_timeout"`
	ConcurrencyModel      string        `json:"concurrency_model"`
	ServerRunning         bool          `json:"server_running"`
	AnalyticsEnabled      bool          `json:"analytics_enabled"`
	Timestamp             time.Time     `json:"timestamp"`
	Type                  string        `json:"type"`
	Analytics             AnalyticsInfo `json:"analytics"`
}

// corsMiddleware handles CORS headers for SSE streams and API requests
// Exposes mcp-session-id header so clients can access it
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().
			Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, mcp-session-id")
		w.Header().Set("Access-Control-Expose-Headers", "mcp-session-id")
		w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours

		// Handle preflight OPTIONS requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// conditionalTimeoutMiddleware applies timeout only to non-SSE requests
// SSE streams need long-lived connections without request timeout
func (hs *HTTPServer) conditionalTimeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip timeout for SSE stream requests (they need long-lived connections)
		if hs.isSSEStreamRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Apply timeout for regular requests
		middleware.Timeout(hs.config.ConnectionTimeout)(next).ServeHTTP(w, r)
	})
}

// setupChiRouter creates and configures the Chi router with all routes and middleware
func (hs *HTTPServer) setupChiRouter() {
	r := chi.NewRouter()

	// Add CORS middleware first to ensure it applies to all routes
	r.Use(corsMiddleware)

	// Add Chi middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// Use conditional timeout that skips SSE streams
	r.Use(hs.conditionalTimeoutMiddleware)

	// Add HTTP concurrency control
	r.Use(middleware.Throttle(hs.config.MaxConcurrentRequests))

	// Create MCP HTTP handler using official SDK's StreamableHTTPHandler
	// This properly dispatches to all registered tools, prompts, and resources
	hs.mcpHTTPHandler = mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return hs.mcpServer
		},
		nil, // Use default options
	)

	hs.Router = r

	// Setup routes
	hs.setupRoutes()
}

// AnalyticsInfo provides typed information about analytics configuration
type AnalyticsInfo struct {
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type,omitempty"`
	Interval string `json:"interval,omitempty"`
}

// setupRoutes configures all the routes
func (hs *HTTPServer) setupRoutes() {
	// Health check endpoint
	hs.Router.Get("/health", hs.healthHandler)

	// Server info endpoint
	hs.Router.Get("/info", hs.serverInfoHandler)

	// Metrics endpoint (if analytics enabled)
	if hs.AnalyticsInstance != nil {
		hs.Router.Get("/metrics", hs.metricsHandler)
	}

	// Public status endpoint
	hs.Router.Get("/api/status", hs.serverInfoHandler)

	// Static files or documentation (if needed in the future)
	hs.Router.Get("/", hs.rootHandler)

	// MCP endpoints using chi.Group pattern
	hs.Router.Group(func(mcpRouter chi.Router) {
		// Add MCP-specific middleware for token extraction and validation
		mcpRouter.Use(app_middleware.HTTPTokenMiddleware)
		mcpRouter.Use(hs.mcpMiddleware)

		// Handle all MCP endpoints
		mcpRouter.Handle("/mcp", hs.mcpHTTPHandler)
		mcpRouter.Handle("/api/mcp", hs.mcpHTTPHandler)
		mcpRouter.Handle("/mcp/*", hs.mcpHTTPHandler)
		mcpRouter.Handle("/api/mcp/*", hs.mcpHTTPHandler)
	})
}

// GetHTTPServerInfo returns information about the HTTP server configuration
func GetHTTPServerInfo(analyticsInstance *analytics.Analytics) HTTPServerInfo {
	info := HTTPServerInfo{
		Type: "http_mcp_server",
	}

	if analyticsInstance != nil {
		info.Analytics = AnalyticsInfo{
			Enabled:  true,
			Type:     "batch",
			Interval: analytics.BatchSendInterval.String(),
		}
	} else {
		info.Analytics = AnalyticsInfo{
			Enabled: false,
		}
	}

	return info
}

// healthHandler returns server health status
func (hs *HTTPServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"version":   hs.config.Version,
	}

	w.Header().Set("Content-Type", "application/json")

	if hs.running.Load() {
		health["server_status"] = "running"
	} else {
		health["server_status"] = "stopped"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	_ = json.NewEncoder(w).Encode(health)
}

// serverInfoHandler returns comprehensive server information (merged /info and /status)
func (hs *HTTPServer) serverInfoHandler(w http.ResponseWriter, r *http.Request) {
	// Merge info and status data into comprehensive response
	info := GetHTTPServerInfo(hs.AnalyticsInstance)

	// Server configuration
	info.Version = hs.config.Version
	info.MaxConcurrentRequests = hs.config.MaxConcurrentRequests
	info.ConnectionTimeout = hs.config.ConnectionTimeout.String()
	info.ConcurrencyModel = "chi_throttle"

	// Runtime status
	info.ServerRunning = hs.running.Load()
	info.Analytics.Enabled = hs.AnalyticsInstance != nil
	info.Timestamp = time.Now().UTC()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// metricsHandler returns analytics metrics (if available)
func (hs *HTTPServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if hs.AnalyticsInstance == nil {
		http.Error(w, "Analytics not enabled", http.StatusNotFound)
		return
	}

	// Return basic analytics information
	metrics := AnalyticsInfo{
		Enabled:  true,
		Type:     "batch",
		Interval: analytics.BatchSendInterval.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"analytics": metrics,
		"timestamp": time.Now().UTC(),
	})
}

// rootHandler serves the root endpoint
func (hs *HTTPServer) rootHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"service":     "ReportPortal MCP Server",
		"version":     hs.config.Version,
		"description": "Model Context Protocol server for ReportPortal integration",
		"endpoints": map[string]string{
			"health":  "/health",
			"info":    "/info",
			"metrics": "/metrics",
			"api":     "/api/*",
			"mcp":     "/api/mcp",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// mcpMiddleware is middleware specifically for MCP requests
func (hs *HTTPServer) mcpMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow SSE stream requests (GET with Accept: text/event-stream),
		// regular MCP JSON-RPC requests (POST with application/json),
		// and MCP DELETE session-termination requests
		if !hs.isMCPRequest(r) && !hs.isSSEStreamRequest(r) {
			http.Error(w, "Invalid MCP request", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isSSEStreamRequest checks if this is an SSE stream request
func (hs *HTTPServer) isSSEStreamRequest(r *http.Request) bool {
	// SSE streams use GET requests with Accept: text/event-stream
	if r.Method != "GET" {
		return false
	}

	accept := r.Header.Get("Accept")
	// HTTP content negotiation tokens are case-insensitive (RFC 7231)
	// Split on commas and check each value case-insensitively
	acceptValues := strings.Split(accept, ",")
	for _, value := range acceptValues {
		// Trim whitespace and check case-insensitively
		trimmed := strings.TrimSpace(value)
		// Handle media type parameters (e.g., "text/event-stream; charset=utf-8")
		if idx := strings.Index(trimmed, ";"); idx != -1 {
			trimmed = trimmed[:idx]
		}
		if strings.EqualFold(strings.TrimSpace(trimmed), "text/event-stream") {
			return true
		}
	}
	return false
}

// isMCPRequest determines if a request should be handled by MCP server
// Performs basic method and content-type checks without body validation
func (hs *HTTPServer) isMCPRequest(r *http.Request) bool {
	// MCP POST requests must have JSON content type
	if r.Method == "POST" {
		contentType := r.Header.Get("Content-Type")
		if idx := strings.Index(contentType, ";"); idx != -1 {
			contentType = contentType[:idx]
		}
		return strings.EqualFold(strings.TrimSpace(contentType), "application/json")
	}

	// MCP DELETE requests for session termination
	if r.Method == "DELETE" {
		return true
	}

	return false
}

// RunStreamingServer starts the ReportPortal MCP server in streaming mode with HTTP token extraction.
func RunStreamingServer(ctx context.Context, cmd *cli.Command) error {
	// Build HTTP server configuration from CLI flags with performance tuning
	serverConfig, err := buildHTTPServerConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to build HTTP server config: %w", err)
	}

	serverHandler, analyticsInstance, err := CreateHTTPServerWithMiddleware(serverConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP MCP server: %w", err)
	}
	// Build address from --port and --host
	port := cmd.Int("port")
	host := cmd.String("host")
	addr := fmt.Sprintf("%s:%d", host, port)

	// Create HTTP server with the Chi router as handler
	// CRITICAL: Use MCP.Router directly to ensure Chi middleware and endpoints are active
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           serverHandler.MCP.Router, // Use Chi router directly with throttling/health/info/metrics
		ReadHeaderTimeout: 10 * time.Second,         // Prevent Slowloris attacks
		ReadTimeout:       30 * time.Second,         // Total time for reading request
		WriteTimeout:      0,                        // Total time for writing response
		IdleTimeout:       120 * time.Second,        // Time to wait for next request
	}

	// Start the HTTP server
	if err := serverHandler.MCP.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Start listening for messages in a separate goroutine
	errC := make(chan error, 1)
	go func() {
		errC <- httpServer.ListenAndServe()
	}()

	// Log that the server is running
	slog.Info("ReportPortal MCP Server running in streaming mode", "addr", addr)

	// Wait for a shutdown signal or an error from the server
	select {
	case <-ctx.Done(): // Context canceled (e.g., SIGTERM received)
		slog.Info("shutting down server...")
		analytics.StopAnalytics(analyticsInstance, "")
		sCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(sCtx); err != nil {
			slog.Error("error during server shutdown", "error", err)
		}
		if err := serverHandler.MCP.Stop(); err != nil {
			slog.Error("error stopping HTTP server", "error", err)
		}
	case err := <-errC: // Error occurred while running the server
		return analytics.HandleServerError(err, analyticsInstance, "http")
	}

	return nil
}

// buildHTTPServerConfig creates HTTPServerConfig from CLI flags with smart defaults.
// This replaces the removed GetProductionConfig/GetHighTrafficConfig factory functions.
func buildHTTPServerConfig(cmd *cli.Command) (HTTPServerConfig, error) {
	// Retrieve required parameters from CLI flags
	host := cmd.String("rp-host")
	// Note: RP_API_TOKEN and --token flag are not available in HTTP mode
	// Tokens MUST come from HTTP request headers (Authorization: Bearer <token>)
	userID := cmd.String("user-id")
	analyticsAPISecret := analytics.GetAnalyticArg()
	analyticsOff := cmd.Bool("analytics-off")

	// Performance tuning parameters with defaults
	maxWorkers := cmd.Int("max-workers")
	connectionTimeoutSec := cmd.Int("connection-timeout")

	// Apply auto-detection for zero values
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU() * 2
	}

	hostUrl, err := url.Parse(host)
	if err != nil {
		return HTTPServerConfig{}, fmt.Errorf("invalid host URL: %w", err)
	}
	if hostUrl.Scheme == "" || hostUrl.Host == "" {
		return HTTPServerConfig{}, fmt.Errorf(
			"host URL must include scheme and host (e.g., https://reportportal.example.com)",
		)
	}

	return HTTPServerConfig{
		Version: fmt.Sprintf(
			"%s (%s) %s",
			config.Version,
			config.Commit,
			config.Date,
		),
		HostURL:               hostUrl,
		FallbackRPToken:       "", // Always empty - RP_API_TOKEN is not available in HTTP mode
		UserID:                userID,
		GA4Secret:             analyticsAPISecret,
		AnalyticsOn:           !analyticsOff,
		MaxConcurrentRequests: maxWorkers,
		ConnectionTimeout:     time.Duration(connectionTimeoutSec) * time.Second,
	}, nil
}
