package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/reportportal/reportportal-mcp-server/internal/config"
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

	// Create application version info
	appVersion := config.AppVersion{
		Version: version,
		Commit:  commit,
		Date:    date,
	}

	// Run the application using the config module
	if err := config.RunApp(ctx, appVersion); err != nil {
		slog.Error("application error", "error", err)
		// Explicitly stop the signal context on error
		stop()
		os.Exit(1)
	}
}
