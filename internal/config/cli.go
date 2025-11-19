package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"

	"github.com/reportportal/reportportal-mcp-server/internal/analytics"
	httpserver "github.com/reportportal/reportportal-mcp-server/internal/http"
	"github.com/reportportal/reportportal-mcp-server/internal/mcp_handlers"
	"github.com/reportportal/reportportal-mcp-server/internal/utils"
)

// AppVersion holds build-time version information
type AppVersion struct {
	Version string
	Commit  string
	Date    string
}

// GetCommonFlags returns the common CLI flags used by all server modes (both stdio and http)
func GetCommonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "rp-host",
			Required: true,
			Sources:  cli.EnvVars("RP_HOST"),
			Usage:    "[GLOBAL/REQUIRED] ReportPortal host URL",
		},
		&cli.StringFlag{
			Name:     "project",
			Required: false,
			Sources:  cli.EnvVars("RP_PROJECT"),
			Value:    "",
			Usage:    "[GLOBAL/OPTIONAL] ReportPortal project name",
		},
		&cli.StringFlag{
			Name:     "log-level",
			Required: false,
			Sources:  cli.EnvVars("LOG_LEVEL"),
			Value:    slog.LevelInfo.String(),
			Usage:    "[GLOBAL/OPTIONAL] Logging level (DEBUG, INFO, WARN, ERROR)",
		},
		&cli.StringFlag{
			Name:     "user-id",
			Required: false,
			Sources:  cli.EnvVars("RP_USER_ID"),
			Value:    "",
			Usage:    "[GLOBAL/OPTIONAL] Custom user ID for analytics (used for analytics identification)",
		},
		&cli.BoolFlag{
			Name:     "analytics-off",
			Required: false,
			Sources:  cli.EnvVars("RP_MCP_ANALYTICS_OFF"),
			Usage:    "[GLOBAL/OPTIONAL] Disable Google Analytics tracking",
			Value:    false,
		},
	}
}

// GetHTTPFlags returns additional flags specific to HTTP mode only (not available in stdio mode)
func GetHTTPFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:     "port",
			Required: false,
			Sources:  cli.EnvVars("MCP_SERVER_PORT"),
			Usage:    "[HTTP-ONLY] HTTP server port",
			Value:    8080,
		},
		&cli.StringFlag{
			Name:     "host",
			Required: false,
			Sources:  cli.EnvVars("MCP_SERVER_HOST"),
			Usage:    "[HTTP-ONLY] HTTP bind host/interface (e.g., 0.0.0.0, 127.0.0.1, ::)",
			Value:    "",
		},
		&cli.IntFlag{
			Name:     "max-workers",
			Required: false,
			Sources:  cli.EnvVars("RP_MAX_WORKERS"),
			Usage:    "[HTTP-ONLY] Maximum number of worker goroutines (0 = auto-detect as CPU count * 2)",
			Value:    0,
		},
		&cli.IntFlag{
			Name:     "connection-timeout",
			Required: false,
			Sources:  cli.EnvVars("RP_CONNECTION_TIMEOUT"),
			Usage:    "[HTTP-ONLY] Connection timeout in seconds",
			Value:    30,
		},
	}
}

// GetStdioFlags returns flags specific to stdio mode only
func GetStdioFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "token",
			Required: false, // Will be validated as required in runStdioServer
			Sources:  cli.EnvVars("RP_API_TOKEN"),
			Usage:    "[STDIO-ONLY] API token for authentication (required for stdio mode)",
		},
	}
}

// GetMCPMode returns the MCP mode from environment variable, defaults to "stdio"
func GetMCPMode() string {
	rawMcpMode := strings.ToLower(os.Getenv("MCP_MODE"))
	slog.Debug("MCP_MODE env variable is set to: " + rawMcpMode)
	mcpMode := strings.ToLower(rawMcpMode)
	if mcpMode == "" {
		mcpMode = "stdio"
	}
	return mcpMode
}

// InitLogger returns a CLI before function that initializes logging
func InitLogger() func(ctx context.Context, command *cli.Command) (context.Context, error) {
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

// BuildHTTPServerConfig creates HTTPServerConfig from CLI flags with smart defaults.
func BuildHTTPServerConfig(
	cmd *cli.Command,
	appVersion AppVersion,
) (httpserver.HTTPServerConfig, error) {
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
		return httpserver.HTTPServerConfig{}, fmt.Errorf("invalid host URL: %w", err)
	}

	return httpserver.HTTPServerConfig{
		Version: fmt.Sprintf(
			"%s (%s) %s",
			appVersion.Version,
			appVersion.Commit,
			appVersion.Date,
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

// NewMCPServer creates a new MCP server from CLI command configuration
func NewMCPServer(
	cmd *cli.Command,
	appVersion AppVersion,
) (*server.MCPServer, *analytics.Analytics, error) {
	// Retrieve required parameters from the command flags
	token := cmd.String("token")                     // API token
	host := cmd.String("rp-host")                    // ReportPortal host URL
	userID := cmd.String("user-id")                  // Unified user ID for analytics
	project := cmd.String("project")                 // ReportPortal project name
	analyticsAPISecret := analytics.GetAnalyticArg() // Analytics API secret
	analyticsOff := cmd.Bool("analytics-off")        // Disable analytics flag

	hostUrl, err := url.Parse(host)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid host URL: %w", err)
	}

	// Create a new stdio server using the ReportPortal client
	mcpServer, analyticsClient, err := mcp_handlers.NewServer(
		appVersion.Version,
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
	return mcpServer, analyticsClient, nil
}

// HandleServerError processes server errors, distinguishing between graceful shutdowns and actual errors.
// Returns nil for graceful shutdowns, or the original error for actual problems.
func HandleServerError(
	err error,
	analyticsClient *analytics.Analytics,
	serverType string,
) error {
	// Check for successful completion or expected shutdown errors
	if err == nil ||
		errors.Is(err, http.ErrServerClosed) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		slog.Info("server shutdown completed", "type", serverType)
		analytics.StopAnalytics(analyticsClient, "")
		return nil
	}

	slog.Error("server error occurred", "type", serverType, "error", err)
	analytics.StopAnalytics(analyticsClient, "server error")
	return fmt.Errorf("error running %s server: %w", serverType, err)
}

// RunStdioServer starts the ReportPortal MCP server in stdio mode.
func RunStdioServer(ctx context.Context, cmd *cli.Command, appVersion AppVersion) error {
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
		ctx = utils.WithProjectInContext(ctx, rpProject)
	}
	mcpServer, analyticsClient, err := NewMCPServer(cmd, appVersion)
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
		analytics.StopAnalytics(analyticsClient, "")
	case err := <-errC: // Error occurred while running the server
		return HandleServerError(err, analyticsClient, "stdio")
	}

	return nil
}

// RunStreamingServer starts the ReportPortal MCP server in streaming mode over HTTP.
func RunStreamingServer(ctx context.Context, cmd *cli.Command, appVersion AppVersion) error {
	// Build HTTP server configuration from CLI flags with performance tuning
	httpConfig, err := BuildHTTPServerConfig(cmd, appVersion)
	if err != nil {
		return fmt.Errorf("failed to build HTTP server config: %w", err)
	}

	httpServer, analyticsClient, err := httpserver.CreateHTTPServerWithMiddleware(httpConfig)
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
		analytics.StopAnalytics(analyticsClient, "")
		sCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(sCtx); err != nil {
			slog.Error("error during server shutdown", "error", err)
		}
		if err := httpServer.MCP.Stop(); err != nil {
			slog.Error("error stopping HTTP server", "error", err)
		}
	case err := <-errC: // Error occurred while running the server
		return HandleServerError(err, analyticsClient, "http")
	}

	return nil
}

// RunApp creates and runs the CLI application with proper configuration
func RunApp(ctx context.Context, appVersion AppVersion) error {
	mcpMode := GetMCPMode()
	// Build flags based on MCP mode
	var allFlags []cli.Flag
	allFlags = append(allFlags, GetCommonFlags()...)
	if mcpMode == "http" {
		allFlags = append(allFlags, GetHTTPFlags()...)
	} else {
		// stdio mode (default) - add stdio-specific flags
		allFlags = append(allFlags, GetStdioFlags()...)
	}

	// Define the CLI command structure
	cmd := &cli.Command{
		Version: fmt.Sprintf("%s (%s) %s", appVersion.Version, appVersion.Commit, appVersion.Date),
		Description: `ReportPortal MCP Server

ENVIRONMENT VARIABLES:
   MCP_MODE    Server mode: "stdio" (default) or "http"
               Controls which server type to run and which flags are available

FLAG CATEGORIES:
   [GLOBAL/REQUIRED]  - Required for all modes (stdio and http)
   [GLOBAL/OPTIONAL]  - Optional for all modes (stdio and http)
   [STDIO-ONLY]       - Only available when MCP_MODE=stdio (default)
   [HTTP-ONLY]        - Only available when MCP_MODE=http

AUTHENTICATION:
   stdio mode: RP_API_TOKEN is REQUIRED (must be set via environment variable or --token flag)
   http mode:  RP_API_TOKEN and --token are COMPLETELY IGNORED
               Tokens MUST be passed per-request via 'Authorization: Bearer <token>' header

ANALYTICS:
   stdio mode: RP_API_TOKEN is required for analytics (used for secure user identification)
   http mode:  Analytics uses RP_USER_ID env var for identification
               Use --analytics-off or RP_MCP_ANALYTICS_OFF=true to disable analytics

USAGE EXAMPLES:
   # Run in stdio mode (default)
   reportportal-mcp-server --rp-host https://reportportal.example.com --token YOUR_TOKEN

   # Run in http mode with custom port
   MCP_MODE=http reportportal-mcp-server --rp-host https://reportportal.example.com --port 9090`,
		Flags:  allFlags,
		Before: InitLogger(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Check mcpMode and run appropriate server
			switch mcpMode {
			case "http":
				return RunStreamingServer(ctx, cmd, appVersion)
			case "stdio":
				return RunStdioServer(ctx, cmd, appVersion)
			default:
				slog.Info(
					"unknown MCP_MODE, defaulting to stdio",
					"mode",
					mcpMode,
					"supported",
					"stdio, http",
				)
				return RunStdioServer(ctx, cmd, appVersion)
			}
		},
	}

	// Run the CLI command and handle any errors
	return cmd.Run(ctx, os.Args)
}
