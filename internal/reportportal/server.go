package mcpreportportal

import (
	"embed"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"

	"github.com/reportportal/reportportal-mcp-server/internal/promptreader"
)

//go:embed prompts/*.yaml
var promptFiles embed.FS

func NewServer(version string, hostUrl *url.URL, token, project string) (*server.MCPServer, error) {
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

	launches := &LaunchResources{client: rpClient, project: project}
	s.AddTool(launches.toolListLaunches())
	s.AddTool(launches.toolGetLastLaunchByName())
	s.AddTool(launches.toolForceFinishLaunch())
	s.AddTool(launches.toolDeleteLaunch())
	s.AddTool(launches.toolRunAutoAnalysis())
	s.AddTool(launches.toolUniqueErrorAnalysis())
	s.AddResourceTemplate(launches.resourceLaunch())

	testItems := &TestItemResources{client: rpClient, project: project}
	s.AddTool(testItems.toolGetTestItemById())
	s.AddTool(testItems.toolListLaunchTestItems())
	s.AddResourceTemplate(testItems.resourceTestItem())

	prompts, err := readPrompts(promptFiles, "prompts")
	if err != nil {
		return nil, fmt.Errorf("failed to load prompts: %w", err)
	}
	for _, prompt := range prompts {
		// Add each prompt to the server
		s.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return s, nil
}

func extractPaging(request mcp.CallToolRequest) (int32, int32) {
	// Extract the "page" parameter from the request
	page := request.GetInt("page", firstPage)
	if page > math.MaxInt32 {
		page = math.MaxInt32
	}

	// Extract the "page-size" parameter from the request
	pageSize := request.GetInt("page-size", defaultPageSize)
	if pageSize > math.MaxInt32 {
		pageSize = math.MaxInt32
	}

	//nolint:gosec // the int32 is confirmed
	return int32(page), int32(pageSize)
}

// readPrompts reads multiple YAML files containing prompt definitions
func readPrompts(files embed.FS, dir string) ([]promptreader.PromptHandlerPair, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, err
	}
	handlers := make([]promptreader.PromptHandlerPair, len(entries))
	for _, entry := range entries {
		data, err := fs.ReadFile(files, filepath.Join(dir, entry.Name()))
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
