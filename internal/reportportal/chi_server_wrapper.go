package mcpreportportal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/server"
)

// ChiMCPServerWrapper integrates Chi router with MCP StreamableHTTPServer
type ChiMCPServerWrapper struct {
	httpServer       *HTTPServer
	streamableServer *server.StreamableHTTPServer
	chiRouter        *ChiRouter
}

// NewChiMCPServerWrapper creates a wrapper that combines Chi routing with MCP server
func NewChiMCPServerWrapper(
	httpServer *HTTPServer,
	streamableServer *server.StreamableHTTPServer,
) *ChiMCPServerWrapper {
	return &ChiMCPServerWrapper{
		httpServer:       httpServer,
		streamableServer: streamableServer,
		chiRouter:        httpServer.chiRouter,
	}
}

// ServeHTTP implements http.Handler and routes requests appropriately
func (wrapper *ChiMCPServerWrapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if this is an MCP request
	if wrapper.isMCPRequest(r) {
		// Handle MCP requests with the streamable server
		wrapper.streamableServer.ServeHTTP(w, r)
		return
	}

	// Handle regular HTTP requests with Chi router
	if wrapper.chiRouter != nil {
		wrapper.chiRouter.ServeHTTP(w, r)
	} else {
		http.Error(w, "Router not initialized", http.StatusInternalServerError)
	}
}

// MCPRequestPayload represents the basic JSON-RPC structure of MCP requests
type MCPRequestPayload struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
}

// isMCPRequest determines if a request should be handled by MCP server
// Uses strict detection to prevent misrouting non-MCP requests
func (wrapper *ChiMCPServerWrapper) isMCPRequest(r *http.Request) bool {
	// MCP requests must be POST requests with JSON content type
	if r.Method != "POST" {
		return false
	}

	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return false
	}

	// Only allow explicit MCP endpoints - no broad path matching
	path := r.URL.Path
	switch path {
	case "/mcp":
		return wrapper.validateMCPPayload(r)
	case "/api/mcp":
		return wrapper.validateMCPPayload(r)
	default:
		// Check for MCP subpaths (e.g., /mcp/tools, /api/mcp/resources)
		if strings.HasPrefix(path, "/mcp/") || strings.HasPrefix(path, "/api/mcp/") {
			return wrapper.validateMCPPayload(r)
		}
		return false
	}
}

// validateMCPPayload checks if the request body contains valid JSON-RPC structure
func (wrapper *ChiMCPServerWrapper) validateMCPPayload(r *http.Request) bool {
	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}

	// Restore the request body so it can be read again by the MCP handler
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	// Try to parse as JSON-RPC
	var payload MCPRequestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}

	// Validate JSON-RPC 2.0 structure
	if payload.JSONRPC != "2.0" || payload.Method == "" {
		return false
	}

	return true
}

// Start starts both the HTTP server and the underlying services
func (wrapper *ChiMCPServerWrapper) Start() error {
	return wrapper.httpServer.Start()
}

// Stop stops both the HTTP server and the underlying services
func (wrapper *ChiMCPServerWrapper) Stop() error {
	return wrapper.httpServer.Stop()
}

// Shutdown gracefully shuts down the server with context
func (wrapper *ChiMCPServerWrapper) Shutdown(ctx context.Context) error {
	return wrapper.httpServer.Stop()
}
