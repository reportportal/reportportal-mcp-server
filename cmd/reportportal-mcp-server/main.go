package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/urfave/cli/v3"

	mcpreportportal "github.com/reportportal/reportportal-mcp-server/pkg/reportportal"
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
		Name:           "server",                                         // Command name
		Description:    `ReportPortal MCP Server`,                        // Command description
		DefaultCommand: "stdio",                                          // Default subcommand
		Commands: []*cli.Command{
			{
				Name:        "stdio", // Subcommand to start the server in stdio mode
				Description: "Start ReportPortal MCP Server in stdio mode",
				Action:      runStdioServer, // Function to execute for this subcommand
				Before: func(ctx context.Context, command *cli.Command) (context.Context, error) {
					// Set up default logging configuration
					slog.SetDefault(
						slog.New(
							slog.NewTextHandler(
								os.Stderr,
								&slog.HandlerOptions{Level: slog.LevelInfo},
							),
						),
					)

					return ctx, nil
				},
				Flags: []cli.Flag{
					// Define required flags for the subcommand
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
						Required: true,
						Sources:  cli.EnvVars("RP_PROJECT"),
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

// runStdioServer starts the ReportPortal MCP server in stdio mode.
func runStdioServer(ctx context.Context, cmd *cli.Command) error {
	// Retrieve required parameters from the command flags
	token := cmd.String("token")     // API token
	host := cmd.String("host")       // ReportPortal host URL
	project := cmd.String("project") // ReportPortal project name

	// Create a new ReportPortal client
	rpClient := gorp.NewClient(host, project, token)

	// Create a new stdio server using the ReportPortal client
	stdioServer := server.NewStdioServer(mcpreportportal.NewServer(rpClient))

	// Start listening for messages in a separate goroutine
	errC := make(chan error, 1)
	go func() {
		in, out := io.Reader(os.Stdin), io.Writer(os.Stdout) // Use standard input/output
		errC <- stdioServer.Listen(ctx, in, out)             // Start the server
	}()

	// Log that the server is running
	slog.Info("ReportPortal MCP Server running on stdio\n")

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
