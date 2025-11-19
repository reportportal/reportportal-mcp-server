package mcp_handlers

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"path/filepath"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"

	"github.com/reportportal/reportportal-mcp-server/internal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/middleware"
	"github.com/reportportal/reportportal-mcp-server/internal/promptreader"
)

//go:embed prompts/*.yaml
var promptFiles embed.FS

func NewServer(
	version string,
	hostUrl *url.URL,
	token,
	userID, project, analyticsAPISecret string,
	analyticsOn bool,
) (*server.MCPServer, *analytics.Analytics, error) {
	s := server.NewMCPServer(
		"reportportal-mcp-server",
		version,
		server.WithRecovery(),
		server.WithLogging(),
		server.WithResourceCapabilities(true, true),
		server.WithToolCapabilities(true),
	)

	// Create a new ReportPortal client
	rpClient := gorp.NewClient(hostUrl, token)
	rpClient.APIClient.GetConfig().Middleware = middleware.QueryParamsMiddleware

	// Initialize analytics (disabled if analyticsOff is true)
	// Note: Analytics initialization uses "best-effort" approach - failures are logged
	// but don't prevent server startup, consistent with HTTP server behavior
	var analyticsClient *analytics.Analytics
	if analyticsOn && analyticsAPISecret != "" {
		var err error
		// Pass RP API token for secure hashing as user identifier
		analyticsClient, err = analytics.NewAnalytics(userID, analyticsAPISecret, token)
		if err != nil {
			slog.Warn("Failed to initialize analytics", "error", err)
		} else {
			slog.Info("MCP server initialized with batch-based analytics",
				"has_ga4_secret", analyticsAPISecret != "",
				"uses_user_id", userID != "")
		}
	}

	// Register launch tools and resources
	RegisterLaunchTools(s, rpClient, analyticsClient, project)

	// Register test item tools and resources
	RegisterTestItemTools(s, rpClient, analyticsClient, project)

	// Register prompts
	prompts, err := readPrompts(promptFiles, "prompts")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load prompts: %w", err)
	}
	for _, prompt := range prompts {
		// Add each prompt to the server
		s.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return s, analyticsClient, nil
}

// readPrompts reads multiple YAML files containing prompt definitions
func readPrompts(files embed.FS, dir string) ([]promptreader.PromptHandlerPair, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	handlers := make([]promptreader.PromptHandlerPair, len(entries))
	for _, entry := range entries {
		// The path separator is a forward slash, even on Windows systems
		// https://pkg.go.dev/embed
		// https://github.com/reportportal/reportportal-mcp-server/issues/9
		data, err := fs.ReadFile(files, filepath.Clean(dir)+"/"+entry.Name())
		if err != nil {
			return nil, err
		}
		prompts, err := promptreader.LoadPromptsFromYAML(data)
		if err != nil {
			return nil, fmt.Errorf("error loading prompts from YAML: %w", err)
		}
		handlers = append(handlers, prompts...)

	}
	return handlers, nil
}
