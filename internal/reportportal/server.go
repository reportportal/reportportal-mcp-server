package mcpreportportal

import (
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"

	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"

	"github.com/reportportal/reportportal-mcp-server/internal/promptreader"
)

//go:embed prompts/*.yaml
var promptFiles embed.FS

func NewServer(
	version string,
	hostUrl *url.URL,
	token, defaultProject string,
	userID, analyticsAPISecret string,
	analyticsOn bool,
) (*server.MCPServer, *Analytics, error) {
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
	rpClient.APIClient.GetConfig().Middleware = QueryParamsMiddleware

	// Initialize analytics (disabled if analyticsOff is true)
	var analytics *Analytics
	if analyticsOn {
		var err error
		analytics, err = NewAnalytics(userID, analyticsAPISecret)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize analytics: %w", err)
		}
	}

	launches := NewLaunchResources(rpClient, defaultProject, analytics)
	s.AddTool(launches.toolGetLaunches())
	s.AddTool(launches.toolGetLastLaunchByName())
	s.AddTool(launches.toolForceFinishLaunch())
	s.AddTool(launches.toolDeleteLaunch())
	s.AddTool(launches.toolRunAutoAnalysis())
	s.AddTool(launches.toolUniqueErrorAnalysis())
	s.AddTool(launches.toolRunQualityGate())
	s.AddResourceTemplate(launches.resourceLaunch())

	testItems := NewTestItemResources(rpClient, defaultProject, analytics)
	s.AddTool(testItems.toolGetTestItemById())
	s.AddTool(testItems.toolGetTestItemsByFilter())
	s.AddTool(testItems.toolGetTestItemLogsByFilter())
	s.AddTool(testItems.toolGetTestItemAttachment())
	s.AddTool(testItems.toolGetTestSuitesByFilter())
	s.AddResourceTemplate(testItems.resourceTestItem())

	prompts, err := readPrompts(promptFiles, "prompts")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load prompts: %w", err)
	}
	for _, prompt := range prompts {
		// Add each prompt to the server
		s.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return s, analytics, nil
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
