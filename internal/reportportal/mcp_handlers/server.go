package mcphandlers

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/urfave/cli/v3"

	"github.com/reportportal/reportportal-mcp-server/internal/config"
	"github.com/reportportal/reportportal-mcp-server/internal/promptreader"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/middleware"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

//go:embed prompts/*.yaml
var PromptFiles embed.FS

func NewServer(
	version string,
	hostUrl *url.URL,
	token,
	userID, project, analyticsAPISecret string,
	analyticsOn bool,
	tlsCfg *tls.Config,
) (*mcp.Server, *analytics.Analytics, error) {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "reportportal-mcp-server",
			Version: version,
		},
		&mcp.ServerOptions{
			// Add server options as needed
		},
	)

	// Build a shared HTTP client for this server instance (used by both gorp and analytics).
	httpClient := buildHTTPClient(tlsCfg)

	// Create a new ReportPortal client
	rpClient := gorp.NewClient(hostUrl, gorp.WithApiKeyAuth(context.Background(), token))
	rpClient.APIClient.GetConfig().Middleware = middleware.QueryParamsMiddleware
	rpClient.APIClient.GetConfig().HTTPClient = httpClient

	// Initialize analytics (disabled if analyticsOff is true)
	var analyticsInstance *analytics.Analytics
	if analyticsOn {
		var err error

		// Pass RP API token for secure hashing as user identifier
		analyticsInstance, err = analytics.NewAnalytics(
			userID,
			analyticsAPISecret,
			token,
			hostUrl.String(),
			tlsCfg,
		)
		if err != nil {
			slog.Warn("Failed to initialize analytics", "error", err)
		}
	}

	// Register all launch-related tools and resources
	RegisterLaunchTools(s, rpClient, project, analyticsInstance, httpClient)

	// Register all test item-related tools and resources
	RegisterTestItemTools(s, rpClient, project, analyticsInstance)

	prompts, err := ReadPrompts(PromptFiles, "prompts")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load prompts: %w", err)
	}
	for _, prompt := range prompts {
		// Add each prompt to the server
		s.AddPrompt(prompt.Prompt, prompt.Handler)
	}

	return s, analyticsInstance, nil
}

// readPrompts reads multiple YAML files containing prompt definitions
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
		prompts, err := promptreader.ReadPrompts(data)
		if err != nil {
			return nil, fmt.Errorf("error loading prompts from YAML: %w", err)
		}
		handlers = append(handlers, prompts...)

	}
	return handlers, nil
}

// buildHTTPClient creates an *http.Client with a 30 s timeout and optional TLS config.
// When tlsCfg is nil the default transport is used unchanged, preserving HTTP_PROXY and
// other default behaviours. When tlsCfg is non-nil the default transport is cloned and
// its TLSClientConfig is replaced so that proxy and dial settings are still inherited.
func buildHTTPClient(tlsCfg *tls.Config) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if tlsCfg != nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = tlsCfg
		client.Transport = t
	}
	return client
}

func newMCPServer(cmd *cli.Command) (*mcp.Server, *analytics.Analytics, error) {
	// Retrieve required parameters from the command flags
	token := cmd.String("token")                     // API token
	host := cmd.String("rp-host")                    // ReportPortal host URL
	userID := cmd.String("user-id")                  // Unified user ID for analytics
	project := cmd.String("project")                 // ReportPortal project name
	analyticsAPISecret := analytics.GetAnalyticArg() // Analytics API secret
	analyticsOff := cmd.Bool("analytics-off")        // Disable analytics flag

	// TLS settings
	insecureTLS := cmd.Bool("insecure")
	tlsCACert := cmd.String("tls-ca-cert")

	hostUrl, err := url.Parse(host)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid host URL: %w", err)
	}
	if hostUrl.Scheme == "" || hostUrl.Host == "" {
		return nil, nil, fmt.Errorf(
			"invalid host URL %q: scheme and host are required (e.g., https://reportportal.example.com)",
			host,
		)
	}

	tlsCfg, err := config.BuildTLSConfig(insecureTLS, tlsCACert)
	if err != nil {
		return nil, nil, fmt.Errorf("build TLS config: %w", err)
	}

	// Create a new stdio server using the ReportPortal client
	mcpServer, analyticsInstance, err := NewServer(
		fmt.Sprintf(
			"%s (%s) %s",
			config.Version,
			config.Commit,
			config.Date,
		),
		hostUrl,
		token,
		userID,
		project,
		analyticsAPISecret,
		!analyticsOff, // Convert analyticsOff to analyticsOn
		tlsCfg,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ReportPortal MCP server: %w", err)
	}
	return mcpServer, analyticsInstance, nil
}

// runStdioServer starts the ReportPortal MCP server in stdio mode.
func RunStdioServer(ctx context.Context, cmd *cli.Command) error {
	// Validate that token is provided for stdio mode (required)
	token := cmd.String("token")
	if token == "" {
		return fmt.Errorf(
			"RP_API_TOKEN is required for stdio mode (it can be passed via environment variable or --token flag)",
		)
	}

	rpProject := cmd.String("project")
	if rpProject != "" {
		// Add project to request context default project name from Environment variable
		ctx = utils.WithProjectInContext(ctx, rpProject)
	}
	mcpServer, analyticsInstance, err := newMCPServer(cmd)
	if err != nil {
		return err
	}

	// Log that the server is running
	slog.Info("ReportPortal MCP Server running on stdio")

	// Use the official SDK's StdioTransport
	t := &mcp.StdioTransport{}
	err = mcpServer.Run(ctx, t)
	if err != nil {
		return analytics.HandleServerError(err, analyticsInstance, "stdio")
	}

	analytics.StopAnalytics(analyticsInstance, "")
	return nil
}
