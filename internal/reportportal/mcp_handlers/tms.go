package mcphandlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

// TMSResources encapsulates the ReportPortal client for TMS-related tools.
type TMSResources struct {
	client            *gorp.Client
	defaultProjectKey string
	analytics         *analytics.Analytics
}

// NewTMSResources creates a new TMSResources instance.
func NewTMSResources(
	client *gorp.Client,
	analyticsClient *analytics.Analytics,
	projectKey string,
) *TMSResources {
	return &TMSResources{
		client:            client,
		defaultProjectKey: projectKey,
		analytics:         analyticsClient,
	}
}

// RegisterTMSTools registers all TMS-related tools with the MCP server.
func RegisterTMSTools(
	s *mcp.Server,
	rpClient *gorp.Client,
	defaultProjectKey string,
	analyticsClient *analytics.Analytics,
) {
	tms := NewTMSResources(rpClient, analyticsClient, defaultProjectKey)

	registerTool(s, tms.toolGetMilestonesByFilter)
	registerTool(s, tms.toolGetTestPlanByID)
	registerTool(s, tms.toolGetTestCasesForTestPlan)
}

// GetMilestonesByFilterArgs represents the arguments for the get_milestones_by_filter tool.
type GetMilestonesByFilterArgs struct {
	ProjectKey string `json:"projectKey"`
	FilterName string `json:"filter-name"`
	FilterID   *int64 `json:"filter-id"`
}

func (tr *TMSResources) toolGetMilestonesByFilter() (*mcp.Tool, ToolHandler[GetMilestonesByFilterArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "get_milestones_by_filter",
			Description: "Get milestones from ReportPortal TMS, optionally filtered by name or ID",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"filter-name": {
						Type:        "string",
						Description: "Filter milestones by name (exact match)",
					},
					"filter-id": {
						Type:        "integer",
						Description: "Filter milestones by ID",
					},
				},
				Required: utils.RequiredFields(),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_milestones_by_filter",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetMilestonesByFilterArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				cfg := tr.client.GetConfig()
				milestoneURL := fmt.Sprintf(
					"%s://%s/api/v1/project/%s/tms/milestone",
					cfg.Scheme, cfg.Host, url.PathEscape(project),
				)

				httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, milestoneURL, nil)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to build milestone request: %w", err)
				}

				for k, v := range cfg.DefaultHeader {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Accept", "application/json")

				query := httpReq.URL.Query()
				if args.FilterName != "" {
					query.Set("filter.eq.name", args.FilterName)
				}
				if args.FilterID != nil {
					query.Set("filter.eq.id", strconv.FormatInt(*args.FilterID, 10))
				}
				httpReq.URL.RawQuery = query.Encode()

				// Apply middleware to inject auth token and context query params.
				if cfg.Middleware != nil {
					cfg.Middleware(httpReq)
				}

				httpClient := cfg.HTTPClient
				if httpClient == nil {
					httpClient = &http.Client{Timeout: importHTTPClientTimeout}
				}

				resp, err := httpClient.Do(httpReq)
				if err != nil {
					return nil, nil, fmt.Errorf("milestone request failed: %w", err)
				}

				if resp.StatusCode >= 300 {
					defer resp.Body.Close() //nolint:errcheck
					respBody, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						return nil, nil, fmt.Errorf(
							"milestone request failed (HTTP %d)",
							resp.StatusCode,
						)
					}
					return nil, nil, fmt.Errorf(
						"milestone request failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return utils.ReadResponseBody(resp)
			},
		)
}

// GetTestPlanByIDArgs represents the arguments for the get_test_plan_by_id tool.
type GetTestPlanByIDArgs struct {
	ProjectKey string `json:"projectKey"`
	ID         int64  `json:"id"`
}

func (tr *TMSResources) toolGetTestPlanByID() (*mcp.Tool, ToolHandler[GetTestPlanByIDArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "get_test_plan_by_id",
			Description: "Get a TMS test plan by its ID",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"id": {
						Type:        "integer",
						Description: "Test plan ID",
						Minimum:     openapi.PtrFloat64(1),
					},
				},
				Required: utils.RequiredFields("id"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_test_plan_by_id",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetTestPlanByIDArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				_, response, err := tr.client.TestPlanAPI.GetTestPlanById(ctx, args.ID, project).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				return utils.ReadResponseBody(response)
			},
		)
}

// GetTestCasesForTestPlanArgs represents the arguments for the get_test_cases_for_test_plan tool.
type GetTestCasesForTestPlanArgs struct {
	ProjectKey string `json:"projectKey"`
	TestPlanID int64  `json:"test-plan-id"`
}

func (tr *TMSResources) toolGetTestCasesForTestPlan() (*mcp.Tool, ToolHandler[GetTestCasesForTestPlanArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "get_test_cases_for_test_plan",
			Description: "Get test cases assigned to a TMS test plan",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"test-plan-id": {
						Type:        "integer",
						Description: "Test plan ID to retrieve test cases for",
						Minimum:     openapi.PtrFloat64(1),
					},
				},
				Required: utils.RequiredFields("test-plan-id"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_test_cases_for_test_plan",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetTestCasesForTestPlanArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				_, response, err := tr.client.TestPlanAPI.GetTestCasesAddedToPlan(ctx, project, args.TestPlanID).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				return utils.ReadResponseBody(response)
			},
		)
}
