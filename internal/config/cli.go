package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
)

var (
	Version = "version" // Application version
	Commit  = "commit"  // Git commit hash
	Date    = "date"    // Build date
)

// ServerDescription provides the main CLI help text for the ReportPortal MCP Server
const ServerDescription = `ReportPortal MCP Server

ENVIRONMENT VARIABLES:
   MCP_MODE    Server mode: "stdio" (default) or "http"
               Controls which server type to run and which flags are available

AUTHENTICATION:
   stdio mode: RP_API_TOKEN is REQUIRED (must be set via environment variable or --token flag)
   http mode:  RP_API_TOKEN and --token are COMPLETELY IGNORED
               Tokens MUST be passed per-request via 'Authorization: Bearer <token>' header

ANALYTICS:
   stdio mode: RP_API_TOKEN is required for analytics (used for secure user identification)
   http mode:  Analytics uses RP_USER_ID env var for identification
               Use --analytics-off or RP_MCP_ANALYTICS_OFF=true to disable analytics`

// GetCommonFlags returns the common CLI flags used by all server modes (both stdio and http)
func GetCommonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "rp-host",
			Required: true,
			Sources:  cli.EnvVars("RP_HOST"),
			Usage:    "ReportPortal host URL",
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
			Usage:    "Custom user ID for analytics (used for analytics identification)",
		},
		&cli.BoolFlag{
			Name:     "analytics-off",
			Required: false,
			Sources:  cli.EnvVars("RP_MCP_ANALYTICS_OFF"),
			Usage:    "Disable Google Analytics tracking",
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

// GetStdioFlags returns additional flags specific to stdio mode only (not available in HTTP mode)
func GetStdioFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "token",
			Required: false, // Will be validated as required in runStdioServer
			Sources:  cli.EnvVars("RP_API_TOKEN"),
			Usage:    "API token for authentication (required for stdio mode)",
		},
	}
}

// GetMCPMode returns the MCP mode from environment variable, defaults to "stdio"
func GetMCPMode() string {
	// Get MCP mode from environment variable, default to stdio
	rawMcpMode := os.Getenv("MCP_MODE")
	mcpMode := strings.ToLower(strings.TrimSpace(rawMcpMode))
	slog.Debug("MCP_MODE env variable is set to: " + rawMcpMode)
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

// InitAppConfig initializes and returns the CLI command structure based on the MCP mode
func InitAppConfig(
	runHTTPServer, runStdioServer func(context.Context, *cli.Command) error,
) *cli.Command {
	InitLogger()

	// Get MCP mode from environment variable, default to stdio
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
		Version:     fmt.Sprintf("%s (%s) %s", Version, Commit, Date),
		Description: ServerDescription,
		Flags:       allFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Check mcpMode and run appropriate server
			switch mcpMode {
			case "http":
				return runHTTPServer(ctx, cmd)
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

	return cmd
}
