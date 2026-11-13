package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/reportportal/reportportal-mcp-server/internal/config"
	mcpreportportal "github.com/reportportal/reportportal-mcp-server/internal/reportportal"
	mcphandlers "github.com/reportportal/reportportal-mcp-server/internal/reportportal/mcp_handlers"
)

func withStartupRPHostLog(
	next func(context.Context, *cli.Command) error,
) func(context.Context, *cli.Command) error {
	return func(ctx context.Context, cmd *cli.Command) error {
		slog.Info(
			"ReportPortal MCP Server connecting to ReportPortal",
			"rp_host",
			cmd.String("rp-host"),
		)
		return next(ctx, cmd)
	}
}

func main() {
	// Create a context that listens for OS interrupt or termination signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the CLI command structure
	cmd := config.InitAppConfig(
		withStartupRPHostLog(mcpreportportal.RunStreamingServer),
		withStartupRPHostLog(mcphandlers.RunStdioServer),
	)

	// Run the CLI command and handle any errors
	if err := cmd.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}
