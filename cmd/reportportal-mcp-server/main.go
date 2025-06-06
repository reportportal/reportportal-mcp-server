package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
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

	// Define the CLI command structure
	cmd := &cli.Command{
		Version:        fmt.Sprintf("%s (%s) %s", version, commit, date), // Display version info
		Description:    `ReportPortal MCP Server`,                        // Command description
		DefaultCommand: "stdio",                                          // Default subcommand
		// Define required flags for the subcommand
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "host",                 // ReportPortal host URL
				Required: true,                   // Mark as required
				Sources:  cli.EnvVars("RP_HOST"), // Allow setting via environment variable
			},
			&cli.StringFlag{
				Name:     "token", // API token for authentication
				Required: true,
				Sources:  cli.EnvVars("RP_API_TOKEN"),
			},
			&cli.StringFlag{
				Name:     "project", // ReportPortal project name
				Required: false,
				Sources:  cli.EnvVars("RP_PROJECT"),
			},
			&cli.StringFlag{
				Name:     "log-level", // Logging level
				Required: false,
				Sources:  cli.EnvVars("LOG_LEVEL"),
				Value:    slog.LevelInfo.String(),
			},
		},
		Commands: []*cli.Command{
			{
				Name:        "stdio", // Subcommand to start the server in stdio mode
				Description: "Start ReportPortal MCP Server in stdio mode",
				Action:      runStdioServer, // Function to execute for this subcommand
				Before:      initLogger(),
			},
			{
				Name:        "sse", // Subcommand to start the server in SSE mode
				Description: "Start ReportPortal MCP Server in SSE mode",
				Action:      runSSEServer, // Function to execute for this subcommand
				Before:      initLogger(),
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "addr",
						Required: false,
						Sources:  cli.EnvVars("ADDR"),
						Value:    ":8080", // Default address to bind the SSE server
					},
				},
			},
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

func newMCPServer(cmd *cli.Command) (*server.MCPServer, error) {
	// Retrieve required parameters from the command flags
	token := cmd.String("token")     // API token
	host := cmd.String("host")       // ReportPortal host URL
	project := cmd.String("project") // ReportPortal project name

	hostUrl, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("invalid host URL: %w", err)
	}

	// Create a new stdio server using the ReportPortal client
	mcpServer, err := mcpreportportal.NewServer(version, hostUrl, token, project)
	if err != nil {
		return nil, fmt.Errorf("failed to create ReportPortal MCP server: %w", err)
	}
	return mcpServer, nil
}

// runStdioServer starts the ReportPortal MCP server in stdio mode.
func runStdioServer(ctx context.Context, cmd *cli.Command) error {
	mcpServer, err := newMCPServer(cmd)
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
		slog.Error("shutting down server...")
	case err := <-errC: // Error occurred while running the server
		if err != nil {
			return fmt.Errorf("error running server: %w", err)
		}
	}

	return nil
}

// runSSEServer starts the ReportPortal MCP server in SSE mode.
func runSSEServer(ctx context.Context, cmd *cli.Command) error {
	mcpServer, err := newMCPServer(cmd)
	if err != nil {
		return fmt.Errorf("failed to create ReportPortal MCP server: %w", err)
	}
	sseServer := server.NewSSEServer(mcpServer)
	addr := cmd.String("addr") // Address to bind the SSE server to

	// Start listening for messages in a separate goroutine
	errC := make(chan error, 1)
	go func() {
		errC <- sseServer.Start(addr)
	}()

	// Log that the server is running
	slog.Info("ReportPortal MCP Server running on SSE", "addr", addr)

	// Wait for a shutdown signal or an error from the server
	select {
	case <-ctx.Done(): // Context canceled (e.g., SIGTERM received)
		slog.Error("shutting down server...")
		sCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sseServer.Shutdown(sCtx); err != nil {
			slog.Error("error during server shutdown", "error", err)
		}
	case err := <-errC: // Error occurred while running the server
		if err != nil {
			return fmt.Errorf("error running server: %w", err)
		}
	}

	return nil
}
