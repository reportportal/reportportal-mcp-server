package mcphandlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	registerTool(s, tms.toolGetTestFoldersByFilter)
	registerTool(s, tms.toolGetTestCasesByFilter)
	registerTool(s, tms.toolCreateFolder)
	registerTool(s, tms.toolDeleteFolder)
	registerTool(s, tms.toolCreateTestCase)
	registerTool(s, tms.toolUpdateTestCase)
	registerTool(s, tms.toolDeleteTestCase)
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

// GetTestFoldersByFilterArgs represents the arguments for the get_test_folders_by_filter tool.
type GetTestFoldersByFilterArgs struct {
	ProjectKey       string `json:"projectKey"`
	FilterEqID       *int64 `json:"filter-eq-id,omitempty"`
	FilterEqParentID *int64 `json:"filter-eq-parentId,omitempty"`
	FilterEqName     string `json:"filter-eq-name,omitempty"`
	FilterCntName    string `json:"filter-cnt-name,omitempty"`
}

func (tr *TMSResources) toolGetTestFoldersByFilter() (*mcp.Tool, ToolHandler[GetTestFoldersByFilterArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
		pkSchema = &jsonschema.Schema{Type: "string"}
	}
	return &mcp.Tool{
			Name:        "get_test_folders_by_filter",
			Description: "Get test folders for a project from ReportPortal TMS. All filters are optional. Without filters, returns the first page of folders for the project; pagination is not supported by this tool, so the response may be incomplete for large folder sets. To detect truncation, compare page.totalElements with len(content) (or check page.hasNext); if more items exist than returned, narrow the results using filter-eq-parentId, filter-eq-name, or filter-cnt-name.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"filter-eq-id": {
						Type:        "integer",
						Description: "Filter folders by id",
						Minimum:     openapi.PtrFloat64(1),
					},
					"filter-eq-parentId": {
						Type:        "integer",
						Description: "Filter folders by parent folder id",
						Minimum:     openapi.PtrFloat64(1),
					},
					"filter-eq-name": {
						Type:        "string",
						Description: "Filter folders by name (exact match; API filter.eq.name)",
					},
					"filter-cnt-name": {
						Type:        "string",
						Description: "Filter folders by name substring (API filter.cnt.name)",
					},
				},
				Required: utils.RequiredFields(),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_test_folders_by_filter",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetTestFoldersByFilterArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				if args.FilterEqID != nil && *args.FilterEqID < 1 {
					return nil, nil, fmt.Errorf("filter-eq-id out of range: must be >= 1")
				}
				if args.FilterEqParentID != nil && *args.FilterEqParentID < 1 {
					return nil, nil, fmt.Errorf("filter-eq-parentId out of range: must be >= 1")
				}

				cfg := tr.client.GetConfig()
				folderURL := fmt.Sprintf(
					"%s://%s/api/v1/project/%s/tms/folder",
					cfg.Scheme, cfg.Host, url.PathEscape(project),
				)

				httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, folderURL, nil)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to build folder request: %w", err)
				}

				for k, v := range cfg.DefaultHeader {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Accept", "application/json")

				query := httpReq.URL.Query()
				if args.FilterEqID != nil {
					query.Set("filter.eq.id", strconv.FormatInt(*args.FilterEqID, 10))
				}
				if args.FilterEqParentID != nil {
					query.Set("filter.eq.parentId", strconv.FormatInt(*args.FilterEqParentID, 10))
				}
				if args.FilterEqName != "" {
					query.Set("filter.eq.name", args.FilterEqName)
				}
				if args.FilterCntName != "" {
					query.Set("filter.cnt.name", args.FilterCntName)
				}
				httpReq.URL.RawQuery = query.Encode()

				if cfg.Middleware != nil {
					cfg.Middleware(httpReq)
				}

				httpClient := cfg.HTTPClient
				if httpClient == nil {
					httpClient = &http.Client{Timeout: importHTTPClientTimeout}
				}

				resp, err := httpClient.Do(httpReq)
				if err != nil {
					return nil, nil, fmt.Errorf("folder request failed: %w", err)
				}

				if resp.StatusCode >= 300 {
					defer resp.Body.Close() //nolint:errcheck
					respBody, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						return nil, nil, fmt.Errorf(
							"folder request failed (HTTP %d)",
							resp.StatusCode,
						)
					}
					return nil, nil, fmt.Errorf(
						"folder request failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return utils.ReadResponseBody(resp)
			},
		)
}

// GetTestCasesByFilterArgs represents the arguments for the get_test_cases_by_filter tool.
type GetTestCasesByFilterArgs struct {
	ProjectKey            string   `json:"projectKey"`
	FilterEqID            *int64   `json:"filter-eq-id,omitempty"`
	FilterEqTestFolderID  *int64   `json:"filter-eq-testFolderId,omitempty"`
	FilterHasAttributeKey string   `json:"filter-has-attributeKey,omitempty"`
	FilterInPriority      []string `json:"filter-in-priority,omitempty"`
	FilterCntName         string   `json:"filter-cnt-name,omitempty"`
}

func (tr *TMSResources) toolGetTestCasesByFilter() (*mcp.Tool, ToolHandler[GetTestCasesByFilterArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
		pkSchema = &jsonschema.Schema{Type: "string"}
	}
	return &mcp.Tool{
			Name:        "get_test_cases_by_filter",
			Description: "Get test cases for a project from ReportPortal TMS. All filters are optional. Without filters, returns the first page of test cases for the project; pagination is not supported by this tool, so the response may be incomplete for large test case sets. To detect truncation, compare page.totalElements with len(content) (or check page.hasNext); if more items exist than returned, narrow the results using filter-eq-testFolderId, filter-cnt-name, filter-eq-id, filter-has-attributeKey, or filter-in-priority.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"filter-eq-id": {
						Type:        "integer",
						Description: "Filter test cases by id",
						Minimum:     openapi.PtrFloat64(1),
					},
					"filter-eq-testFolderId": {
						Type:        "integer",
						Description: "Filter test cases by parent test folder id",
						Minimum:     openapi.PtrFloat64(1),
					},
					"filter-has-attributeKey": {
						Type:        "string",
						Description: "Filter test cases that have the specified attribute key (tag)",
					},
					"filter-in-priority": {
						Type:        "array",
						Description: "Filter test cases by one or more priority values",
						Items: &jsonschema.Schema{
							Type: "string",
							Enum: []any{
								"CRITICAL",
								"MEDIUM",
								"HIGH",
								"LOW",
								"UNSPECIFIED",
							},
						},
					},
					"filter-cnt-name": {
						Type:        "string",
						Description: "Filter test cases by name substring (API filter.cnt.name)",
					},
				},
				Required: utils.RequiredFields(),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_test_cases_by_filter",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetTestCasesByFilterArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				if args.FilterEqID != nil && *args.FilterEqID < 1 {
					return nil, nil, fmt.Errorf("filter-eq-id out of range: must be >= 1")
				}
				if args.FilterEqTestFolderID != nil && *args.FilterEqTestFolderID < 1 {
					return nil, nil, fmt.Errorf("filter-eq-testFolderId out of range: must be >= 1")
				}

				cfg := tr.client.GetConfig()
				testCaseURL := fmt.Sprintf(
					"%s://%s/api/v1/project/%s/tms/test-case",
					cfg.Scheme, cfg.Host, url.PathEscape(project),
				)

				httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, testCaseURL, nil)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to build test case request: %w", err)
				}

				for k, v := range cfg.DefaultHeader {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Accept", "application/json")

				query := httpReq.URL.Query()
				if args.FilterEqID != nil {
					query.Set("filter.eq.id", strconv.FormatInt(*args.FilterEqID, 10))
				}
				if args.FilterEqTestFolderID != nil {
					query.Set(
						"filter.eq.testFolderId",
						strconv.FormatInt(*args.FilterEqTestFolderID, 10),
					)
				}
				if args.FilterHasAttributeKey != "" {
					query.Set("filter.has.attributeKey", args.FilterHasAttributeKey)
				}
				if len(args.FilterInPriority) > 0 {
					query.Set("filter.in.priority", strings.Join(args.FilterInPriority, ","))
				}
				if args.FilterCntName != "" {
					query.Set("filter.cnt.name", args.FilterCntName)
				}
				httpReq.URL.RawQuery = query.Encode()

				if cfg.Middleware != nil {
					cfg.Middleware(httpReq)
				}

				httpClient := cfg.HTTPClient
				if httpClient == nil {
					httpClient = &http.Client{Timeout: importHTTPClientTimeout}
				}

				resp, err := httpClient.Do(httpReq)
				if err != nil {
					return nil, nil, fmt.Errorf("test case request failed: %w", err)
				}

				if resp.StatusCode >= 300 {
					defer resp.Body.Close() //nolint:errcheck
					respBody, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						return nil, nil, fmt.Errorf(
							"test case request failed (HTTP %d)",
							resp.StatusCode,
						)
					}
					return nil, nil, fmt.Errorf(
						"test case request failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return utils.ReadResponseBody(resp)
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
			Description: "Create a new test folder in the ReportPortal TMS. Supports creating subfolders by providing a parent folder ID. This tool mutates TMS data.",
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
				if strings.TrimSpace(args.Name) == "" {
					return nil, nil, fmt.Errorf("name must not be empty or whitespace")
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

// DeleteFolderArgs represents the arguments for the delete_folder tool.
type DeleteFolderArgs struct {
	ProjectKey string `json:"projectKey"`
	FolderID   int64  `json:"folderId"`
}

func (tr *TMSResources) toolDeleteFolder() (*mcp.Tool, ToolHandler[DeleteFolderArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "delete_folder",
			Description: "Delete a test folder by its ID from the ReportPortal TMS. This tool mutates TMS data and the operation is irreversible.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"folderId": {
						Type:        "integer",
						Description: "ID of the test folder to delete",
						Minimum:     openapi.PtrFloat64(1),
					},
				},
				Required: utils.RequiredFields("folderId"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"delete_folder",
			func(ctx context.Context, req *mcp.CallToolRequest, args DeleteFolderArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}
				if args.FolderID < 1 {
					return nil, nil, fmt.Errorf("folderId out of range: must be >= 1")
				}

				response, err := tr.client.TestFolderAPI.DeleteTestFolder(ctx, args.FolderID, project).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				if response != nil && response.ContentLength != 0 {
					return utils.ReadResponseBody(response)
				}
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("folder %d deleted successfully", args.FolderID),
						},
					},
				}, nil, nil
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
	ExpectedResult *string `json:"expected-result,omitempty"`
}

func (tr *TMSResources) toolCreateTestCase() (*mcp.Tool, ToolHandler[CreateTestCaseArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_test_case",
			Description: "Create a new test case in the ReportPortal TMS with a TEXT manual scenario type. This tool mutates TMS data.",
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
					"expected-result": {
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
				if strings.TrimSpace(args.Name) == "" {
					return nil, nil, fmt.Errorf("name must not be empty or whitespace")
				}

				// The API requires a manual scenario object; TEXT is the only
				// supported type, so it is always included even when instructions
				// and expected-result are omitted.
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
	StartDate  string  `json:"start-date"`
	EndDate    string  `json:"end-date"`
}

func (tr *TMSResources) toolCreateMilestone() (*mcp.Tool, ToolHandler[CreateMilestoneArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_milestone",
			Description: "Create a new milestone in the ReportPortal TMS. This tool mutates TMS data.",
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
						Description: "Type of the milestone",
						Enum:        []any{"SPRINT", "RELEASE", "OTHER"},
					},
					"status": {
						Type:        "string",
						Description: "Optional status of the milestone",
						Enum:        []any{"ACTIVE", "CLOSED"},
					},
					"start-date": {
						Type:        "string",
						Description: "Start date of the milestone in RFC3339 format (e.g. 2026-01-01T00:00:00Z)",
					},
					"end-date": {
						Type:        "string",
						Description: "End date of the milestone in RFC3339 format (e.g. 2026-12-31T00:00:00Z); must not be before start-date",
					},
				},
				Required: utils.RequiredFields("name", "type", "start-date", "end-date"),
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
				if strings.TrimSpace(args.Name) == "" {
					return nil, nil, fmt.Errorf("name must not be empty or whitespace")
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
						"invalid start-date format, expected RFC3339: %w",
						parseErr,
					)
				}

				endDate, parseErr := time.Parse(time.RFC3339, args.EndDate)
				if parseErr != nil {
					return nil, nil, fmt.Errorf(
						"invalid end-date format, expected RFC3339: %w",
						parseErr,
					)
				}
				if endDate.Before(startDate) {
					return nil, nil, fmt.Errorf(
						"end-date (%s) must not be before start-date (%s)",
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
			Description: "Create a new test plan in the ReportPortal TMS. A milestone must exist before creating a test plan. This tool mutates TMS data.",
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
				if strings.TrimSpace(args.Name) == "" {
					return nil, nil, fmt.Errorf("name must not be empty or whitespace")
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

// UpdateTestCaseArgs represents the arguments for the update_test_case tool.
type UpdateTestCaseArgs struct {
	ProjectKey     string  `json:"projectKey"`
	TestCaseID     int64   `json:"testCaseId"`
	Name           *string `json:"name,omitempty"`
	Description    *string `json:"description,omitempty"`
	Priority       *string `json:"priority,omitempty"`
	TestFolderID   *int64  `json:"test-folder-id,omitempty"`
	Instructions   *string `json:"instructions,omitempty"`
	ExpectedResult *string `json:"expected-result,omitempty"`
}

func (tr *TMSResources) toolUpdateTestCase() (*mcp.Tool, ToolHandler[UpdateTestCaseArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "update_test_case",
			Description: "Update an existing test case in the ReportPortal TMS. Only provided fields are updated; omitted fields remain unchanged. This tool mutates TMS data.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"testCaseId": {
						Type:        "integer",
						Description: "ID of the test case to update",
						Minimum:     openapi.PtrFloat64(1),
					},
					"name": {
						Type:        "string",
						Description: "New name for the test case",
					},
					"description": {
						Type:        "string",
						Description: "New description for the test case",
					},
					"priority": {
						Type:        "string",
						Description: "New priority for the test case",
						Enum:        []any{"LOW", "MEDIUM", "HIGH", "CRITICAL"},
					},
					"test-folder-id": {
						Type:        "integer",
						Description: "ID of the folder to move the test case into",
						Minimum:     openapi.PtrFloat64(1),
					},
					"instructions": {
						Type:        "string",
						Description: "New manual scenario instructions / test steps (TEXT scenario type)",
					},
					"expected-result": {
						Type:        "string",
						Description: "New expected result for the manual scenario",
					},
				},
				Required: utils.RequiredFields("testCaseId"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"update_test_case",
			func(ctx context.Context, req *mcp.CallToolRequest, args UpdateTestCaseArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}
				if args.TestCaseID < 1 {
					return nil, nil, fmt.Errorf("testCaseId out of range: must be >= 1")
				}

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQ()
				if args.Name != nil {
					rq.SetName(*args.Name)
				}
				if args.Description != nil {
					rq.SetDescription(*args.Description)
				}
				if args.Priority != nil {
					rq.SetPriority(*args.Priority)
				}
				if args.TestFolderID != nil {
					rq.SetTestFolderId(*args.TestFolderID)
				}
				if args.Instructions != nil || args.ExpectedResult != nil {
					existing, getResp, getErr := tr.client.TestCaseAPI.GetTestCaseById(ctx, project, args.TestCaseID).
						Execute()
					if getErr != nil {
						return nil, nil, fmt.Errorf(
							"could not fetch existing test case to merge manual scenario fields: %s: %w",
							utils.ExtractResponseError(getErr, getResp),
							getErr,
						)
					}
					if existing == nil {
						return nil, nil, fmt.Errorf(
							"could not retrieve the existing test case; cannot perform a partial manual-scenario update",
						)
					}
					ms, ok := existing.GetManualScenarioOk()
					if !ok || ms == nil ||
						ms.ComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRS == nil {
						return nil, nil, fmt.Errorf(
							"partial manual-scenario update is only supported when the existing test case already contains a TEXT manual scenario; " +
								"the current test case has no TEXT manual scenario — supply both instructions and expected-result to create one from scratch",
						)
					}
					textRS := ms.ComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRS
					textScenario := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQ(
						"TEXT",
					)
					existingInstructions := ""
					if textRS.HasInstructions() {
						existingInstructions = textRS.GetInstructions()
					}
					existingExpectedResult := ""
					if textRS.HasExpectedResult() {
						existingExpectedResult = textRS.GetExpectedResult()
					}
					textScenario.SetInstructions(existingInstructions)
					textScenario.SetExpectedResult(existingExpectedResult)
					if args.Instructions != nil {
						textScenario.SetInstructions(*args.Instructions)
					}
					if args.ExpectedResult != nil {
						textScenario.SetExpectedResult(*args.ExpectedResult)
					}
					scenario := openapi.ComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQAsComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario(
						textScenario,
					)
					rq.SetManualScenario(scenario)
				}

				_, response, err := tr.client.TestCaseAPI.PatchTestCase(ctx, project, args.TestCaseID).
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

// DeleteTestCaseArgs represents the arguments for the delete_test_case tool.
type DeleteTestCaseArgs struct {
	ProjectKey string `json:"projectKey"`
	TestCaseID int64  `json:"testCaseId"`
}

func (tr *TMSResources) toolDeleteTestCase() (*mcp.Tool, ToolHandler[DeleteTestCaseArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "delete_test_case",
			Description: "Delete a test case by its ID from the ReportPortal TMS. This tool mutates TMS data and the operation is irreversible.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"testCaseId": {
						Type:        "integer",
						Description: "ID of the test case to delete",
						Minimum:     openapi.PtrFloat64(1),
					},
				},
				Required: utils.RequiredFields("testCaseId"),
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"delete_test_case",
			func(ctx context.Context, req *mcp.CallToolRequest, args DeleteTestCaseArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}
				if args.TestCaseID < 1 {
					return nil, nil, fmt.Errorf("testCaseId out of range: must be >= 1")
				}

				response, err := tr.client.TestCaseAPI.DeleteTestCase(ctx, project, args.TestCaseID).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				if response != nil && response.ContentLength != 0 {
					return utils.ReadResponseBody(response)
				}
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("test case %d deleted successfully", args.TestCaseID),
						},
					},
				}, nil, nil
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
			Description: "Add multiple test cases to an existing TMS test plan. This tool mutates TMS data.",
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
						Description: "List of test case IDs (each ≥ 1) to add to the test plan (must not be empty)",
						Items: &jsonschema.Schema{
							Type:    "integer",
							Minimum: openapi.PtrFloat64(1),
						},
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
