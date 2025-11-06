package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"

	mcpreportportal "github.com/reportportal/reportportal-mcp-server/internal/reportportal"
)

var (
	version = "version" // Application version
	commit  = "commit"  // Git commit hash
	date    = "date"    // Build date
)

func main() {
	// Create a context that listens for OS interrupt or termination signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Get MCP mode from environment variable, default to stdio
	rawMcpMode := strings.ToLower(os.Getenv("MCP_MODE"))
	slog.Debug("MCP_MODE env variable is set to: " + rawMcpMode)
	mcpMode := strings.ToLower(rawMcpMode)
	if mcpMode == "" {
		mcpMode = "stdio"
	}

	// Common flags for all modes
	commonFlags := []cli.Flag{
		&cli.StringFlag{
			Name:     "rp-host",
			Required: true,
			Sources:  cli.EnvVars("RP_HOST"),
			Usage:    "ReportPortal host URL",
		},
		&cli.StringFlag{
			Name:     "token",
			Required: false, // Optional for HTTP mode (can be passed per-request), required for stdio mode
			Sources:  cli.EnvVars("RP_API_TOKEN"),
			Usage:    "API token for authentication (required for stdio mode, optional for http mode)",
		},
		&cli.StringFlag{
			Name:     "project",
			Required: false,
			Sources:  cli.EnvVars("RP_PROJECT"),
			Value:    "",
			Usage:    "ReportPortal project name",
		},
		&cli.StringFlag{
			Name:     "log-level",
			Required: false,
			Sources:  cli.EnvVars("LOG_LEVEL"),
			Value:    slog.LevelInfo.String(),
			Usage:    "Logging level",
		},
		&cli.StringFlag{
			Name:     "user-id",
			Required: false,
			Sources:  cli.EnvVars("RP_USER_ID"),
			Value:    "",
			Usage:    "Unified user ID for analytics (empty = auto-generate)",
		},
		&cli.BoolFlag{
			Name:     "analytics-off",
			Required: false,
			Sources:  cli.EnvVars("RP_MCP_ANALYTICS_OFF"),
			Usage:    "Disable Google Analytics tracking",
			Value:    false,
		},
	}

	// HTTP-specific flags (only included when MCP_MODE is http)
	httpFlags := []cli.Flag{
		&cli.IntFlag{
			Name:     "port",
			Required: false,
			Sources:  cli.EnvVars("MCP_SERVER_PORT"),
			Usage:    "HTTP server port",
			Value:    8080,
		},
		&cli.StringFlag{
			Name:     "host",
			Required: false,
			Sources:  cli.EnvVars("MCP_SERVER_HOST"),
			Usage:    "HTTP bind host/interface (e.g., 0.0.0.0, 127.0.0.1, ::)",
			Value:    "",
		},
		&cli.IntFlag{
			Name:     "max-workers",
			Required: false,
			Sources:  cli.EnvVars("RP_MAX_WORKERS"),
			Usage:    "Maximum number of worker goroutines (0 = auto-detect as CPU count * 2)",
			Value:    0,
		},
		&cli.IntFlag{
			Name:     "connection-timeout",
			Required: false,
			Sources:  cli.EnvVars("RP_CONNECTION_TIMEOUT"),
			Usage:    "Connection timeout in seconds",
			Value:    30,
		},
	}

	// Build flags based on MCP mode
	var allFlags []cli.Flag
	allFlags = append(allFlags, commonFlags...)
	if mcpMode == "http" {
		allFlags = append(allFlags, httpFlags...)
	}

	// Define the CLI command structure
	cmd := &cli.Command{
		Version: fmt.Sprintf("%s (%s) %s", version, commit, date),
		Description: `ReportPortal MCP Server

ENVIRONMENT VARIABLES:
   MCP_MODE    Server mode: "stdio" (default) or "http"
               Controls which server type to run and which flags are available

AUTHENTICATION:
   stdio mode: RP_API_TOKEN is REQUIRED (must be set via environment variable or --token flag)
   http mode:  RP_API_TOKEN is COMPLETELY IGNORED (tokens must be passed per-request via 'Authorization: Bearer <token>' header)
               Environment variables and command line token arguments are not used in http mode for security reasons`,
		Flags:  allFlags,
		Before: initLogger(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Check mcpMode and run appropriate server
			switch mcpMode {
			case "http":
				return runStreamingServer(ctx, cmd)
			case "stdio":
				return runStdioServer(ctx, cmd)
			default:
				slog.Info(
					"unknown MCP_MODE, defaulting to stdio",
					"mode",
					mcpMode,
					"supported",
					"stdio, http",
				)
				return runStdioServer(ctx, cmd)
			}
		},
	}

	// Run the CLI command and handle any errors
	if err := cmd.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}

func initLogger() func(ctx context.Context, command *cli.Command) (context.Context, error) {
	return func(ctx context.Context, command *cli.Command) (context.Context, error) {
		// Set up default logging configuration
		var logLevel slog.Level
		if err := logLevel.UnmarshalText([]byte(command.String("log-level"))); err != nil {
			return nil, err
		}
		slog.SetDefault(
			slog.New(
				slog.NewTextHandler(
					os.Stderr,
					&slog.HandlerOptions{Level: logLevel},
				),
			),
		)

		return ctx, nil
	}
}

// handleServerError processes server errors, distinguishing between graceful shutdowns and actual errors.
// Returns nil for graceful shutdowns, or the original error for actual problems.
func handleServerError(err error, analytics *mcpreportportal.Analytics, serverType string) error {
	// Check for successful completion or expected shutdown errors
	if err == nil ||
		errors.Is(err, http.ErrServerClosed) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		slog.Info("server shutdown completed", "type", serverType)
		mcpreportportal.StopAnalytics(analytics, "")
		return nil
	}

	slog.Error("server error occurred", "type", serverType, "error", err)
	mcpreportportal.StopAnalytics(analytics, "server error")
	return fmt.Errorf("error running %s server: %w", serverType, err)
}

// buildHTTPServerConfig creates HTTPServerConfig from CLI flags with smart defaults.
// This replaces the removed GetProductionConfig/GetHighTrafficConfig factory functions.
func buildHTTPServerConfig(cmd *cli.Command) (mcpreportportal.HTTPServerConfig, error) {
	// Retrieve required parameters from CLI flags
	host := cmd.String("rp-host")
	// Note: token is intentionally ignored in HTTP mode - tokens MUST come from HTTP headers only
	userID := cmd.String("user-id")
	analyticsAPISecret := mcpreportportal.GetAnalyticArg()
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
		return mcpreportportal.HTTPServerConfig{}, fmt.Errorf("invalid host URL: %w", err)
	}

	return mcpreportportal.HTTPServerConfig{
		Version:               fmt.Sprintf("%s (%s) %s", version, commit, date),
		HostURL:               hostUrl,
		FallbackRPToken:       "", // Always empty in HTTP mode - tokens MUST come from HTTP request headers
		UserID:                userID,
		GA4Secret:             analyticsAPISecret,
		AnalyticsOn:           !analyticsOff,
		MaxConcurrentRequests: maxWorkers,
		ConnectionTimeout:     time.Duration(connectionTimeoutSec) * time.Second,
	}, nil
}

func newMCPServer(cmd *cli.Command) (*server.MCPServer, *mcpreportportal.Analytics, error) {
	// Retrieve required parameters from the command flags
	token := cmd.String("token")                           // API token
	host := cmd.String("rp-host")                          // ReportPortal host URL
	userID := cmd.String("user-id")                        // Unified user ID for analytics
	project := cmd.String("project")                       // ReportPortal project name
	analyticsAPISecret := mcpreportportal.GetAnalyticArg() // Analytics API secret
	analyticsOff := cmd.Bool("analytics-off")              // Disable analytics flag

	hostUrl, err := url.Parse(host)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid host URL: %w", err)
	}

	// Create a new stdio server using the ReportPortal client
	mcpServer, analytics, err := mcpreportportal.NewServer(
		version,
		hostUrl,
		token,
		userID,
		project,
		analyticsAPISecret,
		!analyticsOff, // Convert analyticsOff to analyticsOn
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ReportPortal MCP server: %w", err)
	}
	return mcpServer, analytics, nil
}

// runStdioServer starts the ReportPortal MCP server in stdio mode.
func runStdioServer(ctx context.Context, cmd *cli.Command) error {
	// Validate that token is provided for stdio mode (required)
	token := cmd.String("token")
	if token == "" {
		return fmt.Errorf(
			"RP_API_TOKEN is required for stdio mode (it can be passed via environment variable or --token flag)",
		)
	}

	rpProject := cmd.String("project")
	if rpProject != "" {
		// Add project to request context default project name from Environment variable
		ctx = mcpreportportal.WithProjectInContext(ctx, rpProject)
	}
	mcpServer, analytics, err := newMCPServer(cmd)
	if err != nil {
		return fmt.Errorf("failed to create ReportPortal MCP server: %w", err)
	}
	stdioServer := server.NewStdioServer(mcpServer)

	// Start listening for messages in a separate goroutine
	errC := make(chan error, 1)
	go func() {
		in, out := io.Reader(os.Stdin), io.Writer(os.Stdout) // Use standard input/output
		errC <- stdioServer.Listen(ctx, in, out)             // Start the server
	}()

	// Log that the server is running
	slog.Info("ReportPortal MCP Server running on stdio")

	// Wait for a shutdown signal or an error from the server
	select {
	case <-ctx.Done(): // Context canceled (e.g., SIGTERM received)
		slog.Info("shutting down server...")
		mcpreportportal.StopAnalytics(analytics, "")
	case err := <-errC: // Error occurred while running the server
		return handleServerError(err, analytics, "stdio")
	}

	return nil
}

// runStreamingServer starts the ReportPortal MCP server in streaming mode with HTTP token extraction.
func runStreamingServer(ctx context.Context, cmd *cli.Command) error {
	// Build HTTP server configuration from CLI flags with performance tuning
	config, err := buildHTTPServerConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to build HTTP server config: %w", err)
	}

	httpServer, analytics, err := mcpreportportal.CreateHTTPServerWithMiddleware(config)
	if err != nil {
		return fmt.Errorf("failed to create HTTP MCP server: %w", err)
	}
	// Build address from --port and --host
	port := cmd.Int("port")
	host := cmd.String("host")
	addr := fmt.Sprintf("%s:%d", host, port)

	// Create HTTP server with the Chi router as handler
	// CRITICAL: Use MCP.Router directly to ensure Chi middleware and endpoints are active
	server := &http.Server{
		Addr:              addr,
		Handler:           httpServer.MCP.Router, // Use Chi router directly with throttling/health/info/metrics
		ReadHeaderTimeout: 10 * time.Second,      // Prevent Slowloris attacks
		ReadTimeout:       30 * time.Second,      // Total time for reading request
		WriteTimeout:      30 * time.Second,      // Total time for writing response
		IdleTimeout:       120 * time.Second,     // Time to wait for next request
	}

	// Start the HTTP server
	if err := httpServer.MCP.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Start listening for messages in a separate goroutine
	errC := make(chan error, 1)
	go func() {
		errC <- server.ListenAndServe()
	}()

	// Log that the server is running
	slog.Info("ReportPortal MCP Server running in streaming mode", "addr", addr)

	// Wait for a shutdown signal or an error from the server
	select {
	case <-ctx.Done(): // Context canceled (e.g., SIGTERM received)
		slog.Info("shutting down server...")
		mcpreportportal.StopAnalytics(analytics, "")
		sCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(sCtx); err != nil {
			slog.Error("error during server shutdown", "error", err)
		}
		if err := httpServer.MCP.Stop(); err != nil {
			slog.Error("error stopping HTTP server", "error", err)
		}
	case err := <-errC: // Error occurred while running the server
		return handleServerError(err, analytics, "http")
	}

	return nil
}
