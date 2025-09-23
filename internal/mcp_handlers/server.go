package mcp_handlers

import (
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"

	"github.com/reportportal/reportportal-mcp-server/internal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/middleware"
	"github.com/reportportal/reportportal-mcp-server/internal/promptreader"
)

//go:embed prompts/*.yaml
var PromptFiles embed.FS

func NewServer(
	version string,
	hostUrl *url.URL,
	token, defaultProject string,
	userID, analyticsAPISecret string,
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
	var analyticsClient *analytics.Analytics
	if analyticsOn {
		var err error
		// Pass RP API token for secure hashing as user identifier
		analyticsClient, err = analytics.NewAnalytics(userID, analyticsAPISecret, token)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize analytics: %w", err)
		}
	}

	// Register all launch-related tools and resources
	RegisterLaunchTools(s, rpClient, defaultProject, analyticsClient)

	// Register all test item-related tools and resources
	RegisterTestItemTools(s, rpClient, defaultProject, analyticsClient)

	prompts, err := ReadPrompts(PromptFiles, "prompts")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load prompts: %w", err)
	}
	for _, prompt := range prompts {
		// Add each prompt to the server
		s.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return s, analyticsClient, nil
}

// ReadPrompts reads multiple YAML files containing prompt definitions
func ReadPrompts(files embed.FS, dir string) ([]promptreader.PromptHandlerPair, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	handlers := make([]promptreader.PromptHandlerPair, 0, len(entries))
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
