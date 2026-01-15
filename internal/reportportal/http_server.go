package mcpreportportal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
	mcpServer        *server.MCPServer
	analytics        *Analytics
	config           HTTPServerConfig
	Router           chi.Router // Made public for CreateHTTPServerWithMiddleware
	streamableServer *server.StreamableHTTPServer
	httpClient       *http.Client // Direct HTTP client instead of ConnectionManager

	// State management
	running    bool
	runningMux sync.RWMutex
}

// MCPRequestPayload represents the basic JSON-RPC structure of MCP requests
type MCPRequestPayload struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
}

// NewHTTPServer creates a new enhanced HTTP server with Chi router
func NewHTTPServer(config HTTPServerConfig) (*HTTPServer, error) {
	// Set defaults
	if config.MaxConcurrentRequests <= 0 {
		config.MaxConcurrentRequests = runtime.NumCPU() * 2 // HTTP-level concurrency limit
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
	// Note: In HTTP mode, FallbackRPToken is always empty (tokens come from HTTP headers).
	// Analytics uses UserID for identification in HTTP mode.
	var analytics *Analytics
	if config.AnalyticsOn && config.GA4Secret != "" {
		var err error
		analytics, err = NewAnalytics(
			config.UserID,
			config.GA4Secret,
			"", // FallbackRPToken is always empty in HTTP mode
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
		mcpServer:  mcpServer,
		analytics:  analytics,
		config:     config,
		httpClient: httpClient,
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
	rpClient.APIClient.GetConfig().Middleware = QueryParamsMiddleware

	// Add launch management tools with analytics
	launches := NewLaunchResources(rpClient, hs.analytics, "")

	hs.mcpServer.AddTool(launches.toolGetLaunches())
	hs.mcpServer.AddTool(launches.toolGetLastLaunchByName())
	hs.mcpServer.AddTool(launches.toolGetLaunchById())
	hs.mcpServer.AddTool(launches.toolForceFinishLaunch())
	hs.mcpServer.AddTool(launches.toolDeleteLaunch())
	hs.mcpServer.AddTool(launches.toolRunAutoAnalysis())
	hs.mcpServer.AddTool(launches.toolUniqueErrorAnalysis())
	hs.mcpServer.AddTool(launches.toolRunQualityGate())

	hs.mcpServer.AddResourceTemplate(launches.resourceLaunch())

	// Add test item tools
	testItems := NewTestItemResources(rpClient, hs.analytics, "")

	hs.mcpServer.AddTool(testItems.toolGetTestItemById())
	hs.mcpServer.AddTool(testItems.toolGetTestItemsByFilter())
	hs.mcpServer.AddTool(testItems.toolGetTestItemLogsByFilter())
	hs.mcpServer.AddTool(testItems.toolGetTestItemAttachment())
	hs.mcpServer.AddTool(testItems.toolGetTestSuitesByFilter())
	hs.mcpServer.AddTool(testItems.toolGetProjectDefectTypes())
	hs.mcpServer.AddTool(testItems.toolUpdateDefectTypeForTestItems())

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

// HTTPServerWithMiddleware wraps StreamableHTTPServer with token middleware
type HTTPServerWithMiddleware struct {
	*server.StreamableHTTPServer
	Handler http.Handler
	MCP     *HTTPServer // Keep reference to underlying MCP server for lifecycle management
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

// CreateHTTPServerWithMiddleware creates a complete HTTP server setup with middleware
func CreateHTTPServerWithMiddleware(
	config HTTPServerConfig,
) (*HTTPServerWithMiddleware, *Analytics, error) {
	// Create the MCP server with Chi router and middleware already configured
	mcpServer, err := NewHTTPServer(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create MCP server: %w", err)
	}

	// Use the Chi router as the handler (middleware already applied in setupRoutes)
	// The router includes HTTPTokenMiddleware for MCP routes and all other endpoints
	wrapper := &HTTPServerWithMiddleware{
		StreamableHTTPServer: mcpServer.streamableServer, // Use the existing streamable server
		Handler:              mcpServer.Router,           // Use Chi router with all middleware
		MCP:                  mcpServer,
	}

	slog.Info(
		"HTTP MCP server created with Chi router and integrated middleware",
		"router", "chi",
		"middleware", "HTTPTokenMiddleware+Throttle+Timeout",
		"analytics_type", "batch",
		"analytics_enabled", mcpServer.analytics != nil,
	)

	return wrapper, mcpServer.analytics, nil
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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

	// Create streamable server for MCP functionality
	hs.streamableServer = server.NewStreamableHTTPServer(hs.mcpServer)

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
	if hs.analytics != nil {
		hs.Router.Get("/metrics", hs.metricsHandler)
	}

	// Public status endpoint
	hs.Router.Get("/api/status", hs.serverInfoHandler)

	// Static files or documentation (if needed in the future)
	hs.Router.Get("/", hs.rootHandler)

	// MCP endpoints using chi.Group pattern
	hs.Router.Group(func(mcpRouter chi.Router) {
		// Add MCP-specific middleware for token extraction and validation
		mcpRouter.Use(HTTPTokenMiddleware)
		mcpRouter.Use(hs.mcpMiddleware)

		// Handle all MCP endpoints
		mcpRouter.Handle("/mcp", hs.streamableServer)
		mcpRouter.Handle("/api/mcp", hs.streamableServer)
		mcpRouter.Handle("/mcp/*", hs.streamableServer)
		mcpRouter.Handle("/api/mcp/*", hs.streamableServer)
	})
}

// GetHTTPServerInfo returns information about the HTTP server configuration
func GetHTTPServerInfo(analytics *Analytics) HTTPServerInfo {
	info := HTTPServerInfo{
		Type: "http_mcp_server",
	}

	if analytics != nil {
		info.Analytics = AnalyticsInfo{
			Enabled:  true,
			Type:     "batch",
			Interval: batchSendInterval.String(),
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

	if hs.running {
		health["server_status"] = "running"
	} else {
		health["server_status"] = "stopped"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

// serverInfoHandler returns comprehensive server information (merged /info and /status)
func (hs *HTTPServer) serverInfoHandler(w http.ResponseWriter, r *http.Request) {
	// Merge info and status data into comprehensive response
	info := GetHTTPServerInfo(hs.analytics)

	// Server configuration
	info.Version = hs.config.Version
	info.MaxConcurrentRequests = hs.config.MaxConcurrentRequests
	info.ConnectionTimeout = hs.config.ConnectionTimeout.String()
	info.ConcurrencyModel = "chi_throttle"

	// Runtime status
	info.ServerRunning = hs.running
	info.Analytics.Enabled = hs.analytics != nil
	info.Timestamp = time.Now().UTC()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// metricsHandler returns analytics metrics (if available)
func (hs *HTTPServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if hs.analytics == nil {
		http.Error(w, "Analytics not enabled", http.StatusNotFound)
		return
	}

	// Return basic analytics information
	metrics := AnalyticsInfo{
		Enabled:  true,
		Type:     "batch",
		Interval: batchSendInterval.String(),
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
		// Allow SSE stream requests (GET with Accept: text/event-stream)
		// and regular MCP JSON-RPC requests (POST with application/json)
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
// Uses strict detection to prevent misrouting non-MCP requests
func (hs *HTTPServer) isMCPRequest(r *http.Request) bool {
	// MCP requests must be POST requests with JSON content type
	if r.Method != "POST" {
		return false
	}

	contentType := r.Header.Get("Content-Type")
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}
	if !strings.EqualFold(strings.TrimSpace(contentType), "application/json") {
		return false
	}

	return hs.validateMCPPayload(r)
}

// validateMCPPayload checks if the request body contains valid JSON-RPC structure
// Supports both single requests and batch requests (arrays)
func (hs *HTTPServer) validateMCPPayload(r *http.Request) bool {
	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}

	// Restore the request body so it can be read again by the MCP handler
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	// Check if it's valid JSON first
	var rawJSON interface{}
	if err := json.Unmarshal(body, &rawJSON); err != nil {
		return false
	}

	// Handle batch requests (arrays)
	if batchData, ok := rawJSON.([]interface{}); ok {
		return hs.validateBatchRequest(batchData)
	}

	// Handle single request (object)
	if objData, ok := rawJSON.(map[string]interface{}); ok {
		return hs.validateSingleRequest(objData)
	}

	// Invalid JSON-RPC structure (neither object nor array)
	return false
}

// validateSingleRequest validates a single JSON-RPC request object
func (hs *HTTPServer) validateSingleRequest(data map[string]interface{}) bool {
	// Check for required JSON-RPC 2.0 fields
	jsonrpc, hasJSONRPC := data["jsonrpc"]
	method, hasMethod := data["method"]

	if !hasJSONRPC || !hasMethod {
		return false
	}

	// Validate JSON-RPC version
	jsonrpcStr, ok := jsonrpc.(string)
	if !ok || jsonrpcStr != "2.0" {
		return false
	}

	// Validate method field
	methodStr, ok := method.(string)
	if !ok || methodStr == "" {
		return false
	}

	return true
}

// validateBatchRequest validates a JSON-RPC batch request (array of requests)
func (hs *HTTPServer) validateBatchRequest(batch []interface{}) bool {
	// Batch requests cannot be empty
	if len(batch) == 0 {
		return false
	}

	// Validate each request in the batch
	for _, item := range batch {
		requestObj, ok := item.(map[string]interface{})
		if !ok {
			return false // Each item in batch must be an object
		}

		if !hs.validateSingleRequest(requestObj) {
			return false // All requests in batch must be valid
		}
	}

	return true
}
