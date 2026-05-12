package mcphandlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

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
	registerTool(s, tms.toolCreateFolder)
	registerTool(s, tms.toolCreateTestCase)
	registerTool(s, tms.toolCreateMilestone)
	registerTool(s, tms.toolCreateTestPlan)
	registerTool(s, tms.toolAddTestCasesToTestPlan)
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

// CreateFolderArgs represents the arguments for the create_folder tool.
type CreateFolderArgs struct {
	ProjectKey         string  `json:"projectKey"`
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	ParentTestFolderID *int64  `json:"parent-test-folder-id,omitempty"`
}

func (tr *TMSResources) toolCreateFolder() (*mcp.Tool, ToolHandler[CreateFolderArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_folder",
			Description: "Create a new test folder in the ReportPortal TMS. Supports creating subfolders by providing a parent folder ID.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"name": {
						Type:        "string",
						Description: "Name of the test folder",
					},
					"description": {
						Type:        "string",
						Description: "Optional description of the test folder",
					},
					"parent-test-folder-id": {
						Type:        "integer",
						Description: "Optional ID of the parent folder to create a subfolder",
						Minimum:     openapi.PtrFloat64(1),
					},
				},
				Required: utils.RequiredFields("name"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"create_folder",
			func(ctx context.Context, req *mcp.CallToolRequest, args CreateFolderArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestFolderRQ()
				rq.SetName(args.Name)
				if args.Description != nil {
					rq.SetDescription(*args.Description)
				}
				if args.ParentTestFolderID != nil {
					rq.SetParentTestFolderId(*args.ParentTestFolderID)
				}

				_, response, err := tr.client.TestFolderAPI.CreateTestFolder(ctx, project).
					ComEpamReportportalBaseCoreTmsDtoTmsTestFolderRQ(*rq).
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

// CreateTestCaseArgs represents the arguments for the create_test_case tool.
type CreateTestCaseArgs struct {
	ProjectKey     string  `json:"projectKey"`
	Name           string  `json:"name"`
	Description    *string `json:"description,omitempty"`
	Priority       *string `json:"priority,omitempty"`
	TestFolderID   *int64  `json:"test-folder-id,omitempty"`
	Instructions   *string `json:"instructions,omitempty"`
	ExpectedResult *string `json:"expectedResult,omitempty"`
}

func (tr *TMSResources) toolCreateTestCase() (*mcp.Tool, ToolHandler[CreateTestCaseArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_test_case",
			Description: "Create a new test case in the ReportPortal TMS with a TEXT manual scenario type",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"name": {
						Type:        "string",
						Description: "Name of the test case",
					},
					"description": {
						Type:        "string",
						Description: "Optional description of the test case",
					},
					"priority": {
						Type:        "string",
						Description: "Priority of the test case",
						Enum:        []any{"LOW", "MEDIUM", "HIGH", "CRITICAL"},
					},
					"test-folder-id": {
						Type:        "integer",
						Description: "Optional ID of the folder to place the test case in",
						Minimum:     openapi.PtrFloat64(1),
					},
					"instructions": {
						Type:        "string",
						Description: "Optional manual scenario instructions / test steps (TEXT scenario type)",
					},
					"expectedResult": {
						Type:        "string",
						Description: "Optional expected result for the manual scenario",
					},
				},
				Required: utils.RequiredFields("name"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"create_test_case",
			func(ctx context.Context, req *mcp.CallToolRequest, args CreateTestCaseArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				textScenario := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQ(
					"TEXT",
				)
				if args.Instructions != nil {
					textScenario.SetInstructions(*args.Instructions)
				}
				if args.ExpectedResult != nil {
					textScenario.SetExpectedResult(*args.ExpectedResult)
				}
				scenario := openapi.ComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQAsComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario(
					textScenario,
				)

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQ()
				rq.SetName(args.Name)
				rq.SetManualScenario(scenario)
				if args.Description != nil {
					rq.SetDescription(*args.Description)
				}
				if args.Priority != nil {
					rq.SetPriority(*args.Priority)
				}
				if args.TestFolderID != nil {
					rq.SetTestFolderId(*args.TestFolderID)
				}

				_, response, err := tr.client.TestCaseAPI.CreateTestCase(ctx, project).
					ComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQ(*rq).
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

// CreateMilestoneArgs represents the arguments for the create_milestone tool.
type CreateMilestoneArgs struct {
	ProjectKey string  `json:"projectKey"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Status     *string `json:"status,omitempty"`
	StartDate  string  `json:"startDate"`
	EndDate    string  `json:"endDate"`
}

func (tr *TMSResources) toolCreateMilestone() (*mcp.Tool, ToolHandler[CreateMilestoneArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_milestone",
			Description: "Create a new milestone in the ReportPortal TMS",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"name": {
						Type:        "string",
						Description: "Name of the milestone",
					},
					"type": {
						Type:        "string",
						Description: "Type of the milestone (e.g. SPRINT, RELEASE, OTHER)",
					},
					"status": {
						Type:        "string",
						Description: "Status of the milestone (e.g. ACTIVE, CLOSED)",
					},
					"startDate": {
						Type:        "string",
						Description: "Start date of the milestone in RFC3339 format (e.g. 2026-01-01T00:00:00Z)",
					},
					"endDate": {
						Type:        "string",
						Description: "End date of the milestone in RFC3339 format (e.g. 2026-12-31T00:00:00Z)",
					},
				},
				Required: utils.RequiredFields("name", "type", "startDate", "endDate"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"create_milestone",
			func(ctx context.Context, req *mcp.CallToolRequest, args CreateMilestoneArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsMilestoneRQ()
				rq.SetName(args.Name)
				rq.SetType(args.Type)
				if args.Status != nil {
					rq.SetStatus(*args.Status)
				}
				startDate, parseErr := time.Parse(time.RFC3339, args.StartDate)
				if parseErr != nil {
					return nil, nil, fmt.Errorf(
						"invalid startDate format, expected RFC3339: %w",
						parseErr,
					)
				}

				endDate, parseErr := time.Parse(time.RFC3339, args.EndDate)
				if parseErr != nil {
					return nil, nil, fmt.Errorf(
						"invalid endDate format, expected RFC3339: %w",
						parseErr,
					)
				}
				if endDate.Before(startDate) {
					return nil, nil, fmt.Errorf(
						"endDate (%s) must not be before startDate (%s)",
						args.EndDate, args.StartDate,
					)
				}
				rq.SetStartDate(startDate)
				rq.SetEndDate(endDate)

				_, response, err := tr.client.MilestoneAPI.CreateMilestone(ctx, project).
					ComEpamReportportalBaseCoreTmsDtoTmsMilestoneRQ(*rq).
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

// CreateTestPlanArgs represents the arguments for the create_test_plan tool.
type CreateTestPlanArgs struct {
	ProjectKey  string  `json:"projectKey"`
	Name        string  `json:"name"`
	MilestoneID int64   `json:"milestone-id"`
	Description *string `json:"description,omitempty"`
}

func (tr *TMSResources) toolCreateTestPlan() (*mcp.Tool, ToolHandler[CreateTestPlanArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_test_plan",
			Description: "Create a new test plan in the ReportPortal TMS. A milestone must exist before creating a test plan.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"name": {
						Type:        "string",
						Description: "Name of the test plan",
					},
					"milestone-id": {
						Type:        "integer",
						Description: "ID of the milestone this test plan belongs to (required)",
						Minimum:     openapi.PtrFloat64(1),
					},
					"description": {
						Type:        "string",
						Description: "Optional description of the test plan",
					},
				},
				Required: utils.RequiredFields("name", "milestone-id"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"create_test_plan",
			func(ctx context.Context, req *mcp.CallToolRequest, args CreateTestPlanArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestPlanRQ()
				rq.SetName(args.Name)
				rq.SetMilestoneId(args.MilestoneID)
				if args.Description != nil {
					rq.SetDescription(*args.Description)
				}

				_, response, err := tr.client.TestPlanAPI.CreateTestPlan(ctx, project).
					ComEpamReportportalBaseCoreTmsDtoTmsTestPlanRQ(*rq).
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

// AddTestCasesToTestPlanArgs represents the arguments for the add_test_cases_to_test_plan tool.
type AddTestCasesToTestPlanArgs struct {
	ProjectKey  string  `json:"projectKey"`
	TestPlanID  int64   `json:"test-plan-id"`
	TestCaseIDs []int64 `json:"test-case-ids"`
}

func (tr *TMSResources) toolAddTestCasesToTestPlan() (*mcp.Tool, ToolHandler[AddTestCasesToTestPlanArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "add_test_cases_to_test_plan",
			Description: "Add multiple test cases to an existing TMS test plan",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"test-plan-id": {
						Type:        "integer",
						Description: "ID of the test plan to add test cases to",
						Minimum:     openapi.PtrFloat64(1),
					},
					"test-case-ids": {
						Type:        "array",
						Description: "List of test case IDs to add to the test plan (must not be empty)",
						Items:       &jsonschema.Schema{Type: "integer"},
					},
				},
				Required: utils.RequiredFields("test-plan-id", "test-case-ids"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"add_test_cases_to_test_plan",
			func(ctx context.Context, req *mcp.CallToolRequest, args AddTestCasesToTestPlanArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}
				if len(args.TestCaseIDs) == 0 {
					return nil, nil, fmt.Errorf("test-case-ids must not be empty")
				}

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoBatchBatchAddTestCasesToPlanRQ(
					args.TestCaseIDs,
				)

				_, response, err := tr.client.TestPlanAPI.AddTestCasesToPlan(ctx, args.TestPlanID, project).
					ComEpamReportportalBaseCoreTmsDtoBatchBatchAddTestCasesToPlanRQ(*rq).
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
