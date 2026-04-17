package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/reportportal/reportportal-mcp-server/internal/config"
	mcpreportportal "github.com/reportportal/reportportal-mcp-server/internal/reportportal"
	mcphandlers "github.com/reportportal/reportportal-mcp-server/internal/reportportal/mcp_handlers"
)

func main() {
	// Create a context that listens for OS interrupt or termination signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the CLI command structure
	cmd := config.InitAppConfig(mcpreportportal.RunStreamingServer, mcphandlers.RunStdioServer)

	// Run the CLI command and handle any errors
	if err := cmd.Run(ctx, os.Args); err != nil {
		log.Fatal(err)
	}
}
