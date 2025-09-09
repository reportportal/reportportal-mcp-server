package mcpreportportal

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// ChiRouter wraps chi router with MCP server integration
type ChiRouter struct {
	router          chi.Router
	httpServer      *HTTPServer
	analytics       *Analytics
	tokenMiddleware *HTTPTokenMiddleware
}

// NewChiRouter creates a new Chi router with MCP server integration
func NewChiRouter(httpServer *HTTPServer, analytics *Analytics) *ChiRouter {
	r := chi.NewRouter()

	// Add Chi middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(httpServer.config.ConnectionTimeout))

	// Add HTTP concurrency control
	r.Use(middleware.Throttle(httpServer.config.MaxConcurrentRequests))

	chiRouter := &ChiRouter{
		router:          r,
		httpServer:      httpServer,
		analytics:       analytics,
		tokenMiddleware: NewHTTPTokenMiddleware(),
	}

	// Setup routes
	chiRouter.setupRoutes()

	return chiRouter
}

// setupRoutes configures all the routes
func (cr *ChiRouter) setupRoutes() {
	// Health check endpoint
	cr.router.Get("/health", cr.healthHandler)

	// Server info endpoint
	cr.router.Get("/info", cr.serverInfoHandler)

	// Metrics endpoint (if analytics enabled)
	if cr.analytics != nil {
		cr.router.Get("/metrics", cr.metricsHandler)
	}

	// API routes - MCP endpoints handled by ChiMCPServerWrapper
	// Public status endpoint (moved from /api/status to remove auth requirement)
	cr.router.Get("/api/status", cr.statusHandler)

	// MCP endpoints - these will be handled by the ChiMCPServerWrapper
	// We don't set them up here as they're handled by the wrapper's logic

	// Static files or documentation (if needed in the future)
	cr.router.Get("/", cr.rootHandler)
}

// healthHandler returns server health status
func (cr *ChiRouter) healthHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"version":   cr.httpServer.config.Version,
	}

	if cr.httpServer.running {
		health["server_status"] = "running"
	} else {
		health["server_status"] = "stopped"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(health)
}

// serverInfoHandler returns comprehensive server information (merged /info and /status)
func (cr *ChiRouter) serverInfoHandler(w http.ResponseWriter, r *http.Request) {
	// Merge info and status data into comprehensive response
	info := GetHTTPServerInfo(cr.analytics)

	// Server configuration
	info["version"] = cr.httpServer.config.Version
	info["max_concurrent_requests"] = cr.httpServer.config.MaxConcurrentRequests
	info["connection_timeout"] = cr.httpServer.config.ConnectionTimeout.String()
	info["concurrency_model"] = "chi_throttle"

	// Runtime status
	info["server_running"] = cr.httpServer.running
	info["analytics_enabled"] = cr.analytics != nil
	info["timestamp"] = time.Now().UTC()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// metricsHandler returns analytics metrics (if available)
func (cr *ChiRouter) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if cr.analytics == nil {
		http.Error(w, "Analytics not enabled", http.StatusNotFound)
		return
	}

	// Return basic analytics information
	metrics := map[string]interface{}{
		"enabled":  true,
		"type":     "batch",
		"interval": "10s",
		"batched":  true,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"analytics": metrics,
		"timestamp": time.Now().UTC(),
	})
}

// statusHandler is an alias for serverInfoHandler for backward compatibility
func (cr *ChiRouter) statusHandler(w http.ResponseWriter, r *http.Request) {
	cr.serverInfoHandler(w, r)
}

// rootHandler serves the root endpoint
func (cr *ChiRouter) rootHandler(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"service":     "ReportPortal MCP Server",
		"version":     cr.httpServer.config.Version,
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

// GetRouter returns the underlying chi router
func (cr *ChiRouter) GetRouter() chi.Router {
	return cr.router
}

// ServeHTTP implements http.Handler interface
func (cr *ChiRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cr.router.ServeHTTP(w, r)
}
