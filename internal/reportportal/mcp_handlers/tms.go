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

	registerTool(s, tms.toolCreateMilestone)
	registerTool(s, tms.toolGetMilestonesByFilter)

	registerTool(s, tms.toolCreateTestPlan)
	registerTool(s, tms.toolAddTestCasesToTestPlan)
	registerTool(s, tms.toolGetTestPlanByID)

	registerTool(s, tms.toolCreateTestFolder)
	registerTool(s, tms.toolDeleteTestFolder)
	registerTool(s, tms.toolGetTestFoldersByFilter)

	registerTool(s, tms.toolCreateTestCase)
	registerTool(s, tms.toolGetTestCasesByFilter)
	registerTool(s, tms.toolGetTestCasesForTestPlan)
	registerTool(s, tms.toolUpdateTestCase)
	registerTool(s, tms.toolDeleteTestCase)

	registerTool(s, tms.toolGetManualLaunches)
	registerTool(s, tms.toolGetManualLaunchExecutions)
}

// GetMilestonesByFilterArgs represents the arguments for the get_milestones_by_filter tool.
type GetMilestonesByFilterArgs struct {
	ProjectKey    string `json:"projectKey"`
	FilterCntName string `json:"filter-cnt-name"`
	FilterID      *int64 `json:"filter-id"`
	Limit         uint   `json:"limit"`
	Offset        uint   `json:"offset"`
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
					"filter-cnt-name": {
						Type:        "string",
						Description: "Filter milestones by name substring (API filter.cnt.name)",
					},
					"filter-id": {
						Type:        "integer",
						Description: "Filter milestones by ID",
						Minimum:     openapi.PtrFloat64(1),
					},
					"limit":  utils.LimitSchema(utils.DefaultLimitOffset),
					"offset": utils.OffsetSchema(),
				},
				Required: nil,
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
				utils.ApplyLimitOffset(query, args.Limit, args.Offset, utils.DefaultLimitOffset)
				if args.FilterCntName != "" {
					query.Set("filter.cnt.name", args.FilterCntName)
				}
				if args.FilterID != nil {
					if *args.FilterID < 1 {
						return nil, nil, fmt.Errorf("filter-id out of range: must be >= 1")
					}
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
				Required: []string{"id"},
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
	Limit      uint   `json:"limit"`
	Offset     uint   `json:"offset"`
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
					"limit":  utils.LimitSchema(utils.DefaultLimitOffset),
					"offset": utils.OffsetSchema(),
				},
				Required: []string{"test-plan-id"},
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

				cfg := tr.client.GetConfig()
				testCasePlanURL := fmt.Sprintf(
					"%s://%s/api/v1/project/%s/tms/test-plan/%d/test-case",
					cfg.Scheme, cfg.Host, url.PathEscape(project), args.TestPlanID,
				)

				httpReq, err := http.NewRequestWithContext(
					ctx,
					http.MethodGet,
					testCasePlanURL,
					nil,
				)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"failed to build test cases for test plan request: %w",
						err,
					)
				}

				for k, v := range cfg.DefaultHeader {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Accept", "application/json")

				query := httpReq.URL.Query()
				utils.ApplyLimitOffset(query, args.Limit, args.Offset, utils.DefaultLimitOffset)
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
					return nil, nil, fmt.Errorf("test cases for test plan request failed: %w", err)
				}

				if resp.StatusCode >= 300 {
					defer resp.Body.Close() //nolint:errcheck
					respBody, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						return nil, nil, fmt.Errorf(
							"test cases for test plan request failed (HTTP %d)",
							resp.StatusCode,
						)
					}
					return nil, nil, fmt.Errorf(
						"test cases for test plan request failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return utils.ReadResponseBody(resp)
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
	Limit            uint   `json:"limit"`
	Offset           uint   `json:"offset"`
}

func (tr *TMSResources) toolGetTestFoldersByFilter() (*mcp.Tool, ToolHandler[GetTestFoldersByFilterArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
		pkSchema = &jsonschema.Schema{Type: "string"}
	}
	return &mcp.Tool{
			Name:        "get_test_folders_by_filter",
			Description: "Get test folders for a project from ReportPortal TMS. All filters and pagination parameters are optional.",
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
					"limit":  utils.LimitSchema(utils.DefaultLimitOffset),
					"offset": utils.OffsetSchema(),
				},
				Required: nil,
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
				utils.ApplyLimitOffset(query, args.Limit, args.Offset, utils.DefaultLimitOffset)
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
	Limit                 uint     `json:"limit"`
	Offset                uint     `json:"offset"`
}

func (tr *TMSResources) toolGetTestCasesByFilter() (*mcp.Tool, ToolHandler[GetTestCasesByFilterArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
		pkSchema = &jsonschema.Schema{Type: "string"}
	}
	return &mcp.Tool{
			Name:        "get_test_cases_by_filter",
			Description: "Get test cases for a project from ReportPortal TMS. All filters and pagination parameters are optional.",
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
								"LOW",
								"MEDIUM",
								"HIGH",
								"CRITICAL",
								"BLOCKER",
								"UNSPECIFIED",
							},
						},
					},
					"filter-cnt-name": {
						Type:        "string",
						Description: "Filter test cases by name substring (API filter.cnt.name)",
					},
					"limit":  utils.LimitSchema(utils.DefaultLimitOffset),
					"offset": utils.OffsetSchema(),
				},
				Required: nil,
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
				utils.ApplyLimitOffset(query, args.Limit, args.Offset, utils.DefaultLimitOffset)
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

func (tr *TMSResources) toolCreateTestFolder() (*mcp.Tool, ToolHandler[CreateFolderArgs, any]) {
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
				Required: []string{"name"},
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

func (tr *TMSResources) toolDeleteTestFolder() (*mcp.Tool, ToolHandler[DeleteFolderArgs, any]) {
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
				Required: []string{"folderId"},
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

// resolveTestCaseAttributes ensures that every requested attribute (tag, identified
// by key only) exists for the project and returns the request models that link them
// to a test case. For each attribute it first looks the attribute up via
// GET /v1/project/{projectKey}/tms/attribute (filtered by key); if no match exists
// it creates the attribute via POST to the same endpoint. The resulting list
// references each attribute by its id and key so it can be attached during test
// case creation or update.
func (tr *TMSResources) resolveTestCaseAttributes(
	ctx context.Context,
	project string,
	attributes []utils.AttributeArg,
) ([]openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseAttributeRQ, error) {
	// Pre-validate all keys before making any HTTP calls.
	seen := make(map[string]struct{}, len(attributes))
	for i, attr := range attributes {
		key := strings.TrimSpace(attr.Key)
		if key == "" {
			return nil, fmt.Errorf("attributes[%d] key must not be empty or whitespace", i)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("attributes[%d] duplicate key %q", i, key)
		}
		seen[key] = struct{}{}
	}

	result := make(
		[]openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseAttributeRQ,
		0,
		len(attributes),
	)
	for _, attr := range attributes {
		key := strings.TrimSpace(attr.Key)

		// 1. Look up an existing attribute matching the key.
		page, response, err := tr.client.TMSAttributeControllerAPI.GetAllAttributes(ctx, project).
			FilterEqKey(key).
			Execute()
		if err != nil {
			return nil, fmt.Errorf(
				"failed to look up attribute %q: %s: %w",
				key, utils.ExtractResponseError(err, response), err,
			)
		}

		var attributeID int64
		found := false
		for _, existing := range page.GetContent() {
			if existing.GetKey() == key {
				attributeID = existing.GetId()
				found = true
				break
			}
		}

		// 2. Create the attribute when it does not exist yet.
		if !found {
			createRQ := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsAttributeRQ()
			createRQ.SetKey(key)
			created, createResp, createErr := tr.client.TMSAttributeControllerAPI.
				CreateAttribute(ctx, project).
				ComEpamReportportalBaseCoreTmsDtoTmsAttributeRQ(*createRQ).
				Execute()
			if createErr != nil {
				// A 409 Conflict means a concurrent caller raced through the
				// same GET→POST window and created this attribute first. Retry
				// the lookup to obtain the id it just created instead of
				// surfacing a spurious duplicate error.
				if createResp != nil && createResp.StatusCode == http.StatusConflict {
					retryPage, _, retryErr := tr.client.TMSAttributeControllerAPI.
						GetAllAttributes(ctx, project).
						FilterEqKey(key).
						Execute()
					if retryErr == nil {
						for _, existing := range retryPage.GetContent() {
							if existing.GetKey() == key {
								attributeID = existing.GetId()
								found = true
								break
							}
						}
					}
				}
				if !found {
					return nil, fmt.Errorf(
						"failed to create attribute %q: %s: %w",
						key, utils.ExtractResponseError(createErr, createResp), createErr,
					)
				}
			} else {
				attributeID = created.GetId()
			}
		}

		// 3. Link the (existing or newly created) attribute to the test case.
		tcAttr := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestCaseAttributeRQ()
		tcAttr.SetId(attributeID)
		tcAttr.SetKey(key)
		result = append(result, *tcAttr)
	}
	return result, nil
}

// CreateTestCaseArgs represents the arguments for the create_test_case tool.
type CreateTestCaseArgs struct {
	ProjectKey     string               `json:"projectKey"`
	Name           string               `json:"name"`
	Description    *string              `json:"description,omitempty"`
	Priority       *string              `json:"priority,omitempty"`
	TestFolderID   int64                `json:"test-folder-id"`
	TestCaseType   *string              `json:"test-case-type,omitempty"`
	Instructions   *string              `json:"instructions,omitempty"`
	ExpectedResult *string              `json:"expected-result,omitempty"`
	Steps          *[]utils.StepArg     `json:"steps,omitempty"`
	Preconditions  *string              `json:"preconditions,omitempty"`
	Requirements   *[]string            `json:"requirements,omitempty"`
	Attributes     []utils.AttributeArg `json:"attributes,omitempty"`
}

func (tr *TMSResources) toolCreateTestCase() (*mcp.Tool, ToolHandler[CreateTestCaseArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "create_test_case",
			Description: `Create a new test case in the ReportPortal TMS. Use test-case-type to choose the scenario type: "text" (default) for a plain text scenario via instructions/expected-result, or "steps" for an ordered list of steps. This tool mutates TMS data.`,
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
						Enum: []any{
							"LOW",
							"MEDIUM",
							"HIGH",
							"CRITICAL",
							"BLOCKER",
							"UNSPECIFIED",
						},
					},
					"test-folder-id": {
						Type:        "integer",
						Description: "ID of the folder to place the test case in",
						Minimum:     openapi.PtrFloat64(1),
					},
					"test-case-type": utils.TestCaseTypeSchema(false),
					"instructions": {
						Type:        "string",
						Description: `Optional instructions for the "text" type.`,
					},
					"expected-result": {
						Type:        "string",
						Description: `Optional expected result for the "text" type.`,
					},
					"steps": utils.StepsSchema(),
					"preconditions": {
						Type:        "string",
						Description: "Optional preconditions for the test case",
					},
					"requirements": utils.RequirementsSchema(false),
					"attributes":   utils.AttributesSchema(false),
				},
				Required: []string{"name", "test-folder-id"},
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
				if args.TestFolderID < 1 {
					return nil, nil, fmt.Errorf("test-folder-id out of range: must be >= 1")
				}

				// The API requires a manual scenario object, so it is always
				// included; an unspecified test-case-type defaults to TEXT.
				scenario, err := utils.BuildManualScenario(utils.ManualScenarioArgs{
					TestCaseType:   args.TestCaseType,
					Instructions:   args.Instructions,
					ExpectedResult: args.ExpectedResult,
					Preconditions:  args.Preconditions,
					Requirements:   args.Requirements,
					Steps:          args.Steps,
				})
				if err != nil {
					return nil, nil, err
				}

				rq := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQ()
				rq.SetName(args.Name)
				rq.SetManualScenario(scenario)
				if args.Description != nil {
					rq.SetDescription(*args.Description)
				}
				if args.Priority != nil {
					rq.SetPriority(*args.Priority)
				}
				rq.SetTestFolderId(args.TestFolderID)
				if len(args.Attributes) > 0 {
					attrs, attrErr := tr.resolveTestCaseAttributes(ctx, project, args.Attributes)
					if attrErr != nil {
						return nil, nil, attrErr
					}
					rq.SetAttributes(attrs)
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
				Required: []string{"name", "type", "start-date", "end-date"},
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
				Required: []string{"name", "milestone-id"},
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
	ProjectKey     string                `json:"projectKey"`
	TestCaseID     int64                 `json:"testCaseId"`
	Name           *string               `json:"name,omitempty"`
	Description    *string               `json:"description,omitempty"`
	Priority       *string               `json:"priority,omitempty"`
	TestFolderID   *int64                `json:"test-folder-id,omitempty"`
	TestCaseType   *string               `json:"test-case-type,omitempty"`
	Instructions   *string               `json:"instructions,omitempty"`
	ExpectedResult *string               `json:"expected-result,omitempty"`
	Steps          *[]utils.StepArg      `json:"steps,omitempty"`
	Preconditions  *string               `json:"preconditions,omitempty"`
	Requirements   *[]string             `json:"requirements,omitempty"`
	Attributes     *[]utils.AttributeArg `json:"attributes,omitempty"`
}

func (tr *TMSResources) toolUpdateTestCase() (*mcp.Tool, ToolHandler[UpdateTestCaseArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "update_test_case",
			Description: `Update an existing test case in the ReportPortal TMS. Only provided fields are updated; omitted fields remain unchanged. Provide test-case-type to change the scenario type: "text" uses instructions/expected-result, "steps" uses the steps array. When test-case-type is "steps", steps may be omitted to leave existing steps unchanged and only update other fields such as requirements or preconditions. This tool mutates TMS data.`,
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
						Enum: []any{
							"LOW",
							"MEDIUM",
							"HIGH",
							"CRITICAL",
							"BLOCKER",
							"UNSPECIFIED",
						},
					},
					"test-folder-id": {
						Type:        "integer",
						Description: "ID of the folder to move the test case into",
						Minimum:     openapi.PtrFloat64(1),
					},
					"test-case-type": utils.TestCaseTypeSchema(true),
					"instructions": {
						Type:        "string",
						Description: `Optional instructions for the "text" type. Can be provided independently of expected-result.`,
					},
					"expected-result": {
						Type:        "string",
						Description: `Optional expected result for the "text" type. Can be provided independently of instructions.`,
					},
					"steps": utils.StepsSchema(),
					"preconditions": {
						Type:        "string",
						Description: "Preconditions for the test case",
					},
					"requirements": utils.RequirementsSchema(true),
					"attributes":   utils.AttributesSchema(true),
				},
				Required: []string{"testCaseId"},
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
				hasScenarioFields := args.Instructions != nil ||
					args.ExpectedResult != nil || args.Preconditions != nil ||
					args.Requirements != nil || args.Steps != nil
				if args.TestCaseType != nil || hasScenarioFields {
					if args.TestCaseType == nil && hasScenarioFields {
						return nil, nil, fmt.Errorf(
							"test-case-type must be specified when updating manual scenario fields (instructions, expected-result, preconditions, requirements, steps)",
						)
					}
					scenario, scenarioErr := utils.BuildManualScenario(utils.ManualScenarioArgs{
						TestCaseType:   args.TestCaseType,
						Instructions:   args.Instructions,
						ExpectedResult: args.ExpectedResult,
						Preconditions:  args.Preconditions,
						Requirements:   args.Requirements,
						Steps:          args.Steps,
						IsUpdate:       true,
					})
					if scenarioErr != nil {
						return nil, nil, scenarioErr
					}
					rq.SetManualScenario(scenario)
				}

				if args.Attributes != nil {
					attrs, attrErr := tr.resolveTestCaseAttributes(ctx, project, *args.Attributes)
					if attrErr != nil {
						return nil, nil, attrErr
					}
					rq.SetAttributes(attrs)
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
				Required: []string{"testCaseId"},
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

// GetManualLaunchesArgs represents the arguments for the get_manual_launches tool.
type GetManualLaunchesArgs struct {
	ProjectKey                  string   `json:"projectKey"`
	Limit                       uint     `json:"limit"`
	Offset                      uint     `json:"offset"`
	FilterCntName               string   `json:"filter-cnt-name,omitempty"`
	FilterInItemStatus          []string `json:"filter-in-itemStatus,omitempty"`
	FilterEqCompletion          string   `json:"filter-eq-completion,omitempty"`
	FilterGtStartTime           string   `json:"filter-gt-startTime,omitempty"`
	FilterLtEndTime             string   `json:"filter-lt-endTime,omitempty"`
	FilterEqTestPlanID          *int64   `json:"filter-eq-testPlanId,omitempty"`
	FilterHasCompositeAttribute string   `json:"filter-has-compositeAttribute,omitempty"`
}

func (tr *TMSResources) toolGetManualLaunches() (*mcp.Tool, ToolHandler[GetManualLaunchesArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "get_manual_launches",
			Description: "Get manual launches from ReportPortal TMS by filter. All filters and pagination parameters are optional.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"limit":               utils.LimitSchema(0),
					"offset":              utils.OffsetSchema(),
					"filter-cnt-name": {
						Type:        "string",
						Description: "Filter manual launches by name substring",
					},
					"filter-in-itemStatus": {
						Type:        "array",
						Description: "Filter by one or more item execution statuses",
						Items: &jsonschema.Schema{
							Type: "string",
							Enum: []any{"PASSED", "FAILED", "SKIPPED", "IN_PROGRESS"},
						},
					},
					"filter-eq-completion": {
						Type:        "string",
						Description: "Filter by completion status; omit to return all results",
						Enum:        []any{"has_not_executed", "done"},
					},
					"filter-gt-startTime": {
						Type:        "string",
						Description: "Return launches with start time after this timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
					},
					"filter-lt-endTime": {
						Type:        "string",
						Description: "Return launches with end time before this timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
					},
					"filter-eq-testPlanId": {
						Type:        "integer",
						Description: "Filter launches by test plan ID",
						Minimum:     openapi.PtrFloat64(1),
					},
					"filter-has-compositeAttribute": {
						Type:        "string",
						Description: "Filter launches that have this combination of attributes (format: key1:value1,key2:value2)",
					},
				},
				Required: nil,
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_manual_launches",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetManualLaunchesArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				if args.FilterEqTestPlanID != nil && *args.FilterEqTestPlanID < 1 {
					return nil, nil, fmt.Errorf("filter-eq-testPlanId out of range: must be >= 1")
				}

				cfg := tr.client.GetConfig()
				manualLaunchURL := fmt.Sprintf(
					"%s://%s/api/v1/project/%s/launch/manual",
					cfg.Scheme, cfg.Host, url.PathEscape(project),
				)

				httpReq, err := http.NewRequestWithContext(
					ctx,
					http.MethodGet,
					manualLaunchURL,
					nil,
				)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to build manual launches request: %w", err)
				}

				for k, v := range cfg.DefaultHeader {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Accept", "application/json")

				query := httpReq.URL.Query()
				utils.ApplyLimitOffset(query, args.Limit, args.Offset, 0)
				if args.FilterCntName != "" {
					query.Set("filter.cnt.name", args.FilterCntName)
				}
				if len(args.FilterInItemStatus) > 0 {
					query.Set("filter.in.itemStatus", strings.Join(args.FilterInItemStatus, ","))
				}
				if args.FilterEqCompletion != "" {
					query.Set("filter.eq.completion", args.FilterEqCompletion)
				}
				if args.FilterGtStartTime != "" {
					epoch, parseErr := utils.ParseTimestampMillis(args.FilterGtStartTime)
					if parseErr != nil {
						return nil, nil, fmt.Errorf("invalid filter-gt-startTime: %w", parseErr)
					}
					query.Set("filter.gt.startTime", strconv.FormatInt(epoch, 10))
				}
				if args.FilterLtEndTime != "" {
					epoch, parseErr := utils.ParseTimestampMillis(args.FilterLtEndTime)
					if parseErr != nil {
						return nil, nil, fmt.Errorf("invalid filter-lt-endTime: %w", parseErr)
					}
					query.Set("filter.lt.endTime", strconv.FormatInt(epoch, 10))
				}
				if args.FilterEqTestPlanID != nil {
					query.Set(
						"filter.eq.testPlanId",
						strconv.FormatInt(*args.FilterEqTestPlanID, 10),
					)
				}
				if args.FilterHasCompositeAttribute != "" {
					query.Set("filter.has.compositeAttribute", args.FilterHasCompositeAttribute)
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
					return nil, nil, fmt.Errorf("manual launches request failed: %w", err)
				}

				if resp.StatusCode >= 300 {
					defer resp.Body.Close() //nolint:errcheck
					respBody, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						return nil, nil, fmt.Errorf(
							"manual launches request failed (HTTP %d)",
							resp.StatusCode,
						)
					}
					return nil, nil, fmt.Errorf(
						"manual launches request failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return utils.ReadResponseBody(resp)
			},
		)
}

// GetManualLaunchExecutionsArgs represents the arguments for the get_manual_launch_executions tool.
type GetManualLaunchExecutionsArgs struct {
	ProjectKey           string   `json:"projectKey"`
	LaunchID             int64    `json:"launchId"`
	Limit                uint     `json:"limit"`
	Offset               uint     `json:"offset"`
	FilterCntName        string   `json:"filter-cnt-name,omitempty"`
	FilterInPriority     []string `json:"filter-in-priority,omitempty"`
	FilterInAttributeKey string   `json:"filter-in-attributeKey,omitempty"`
}

func (tr *TMSResources) toolGetManualLaunchExecutions() (*mcp.Tool, ToolHandler[GetManualLaunchExecutionsArgs, any]) {
	pkSchema, err := utils.ProjectKeySchema(tr.defaultProjectKey)
	if err != nil {
		slog.Error("failed to build project key schema", "error", err)
	}
	return &mcp.Tool{
			Name:        "get_manual_launch_executions",
			Description: "Get test case executions for a manual launch from ReportPortal TMS by filter. All filters and pagination parameters are optional.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					utils.ProjectKeyField: pkSchema,
					"launchId": {
						Type:        "integer",
						Description: "ID of the manual launch",
						Minimum:     openapi.PtrFloat64(1),
					},
					"limit":  utils.LimitSchema(0),
					"offset": utils.OffsetSchema(),
					"filter-cnt-name": {
						Type:        "string",
						Description: "Filter executions by test case name substring",
					},
					"filter-in-priority": {
						Type:        "array",
						Description: "Filter by one or more test case priority values",
						Items: &jsonschema.Schema{
							Type: "string",
							Enum: []any{
								"BLOCKER",
								"CRITICAL",
								"HIGH",
								"LOW",
								"MEDIUM",
								"UNSPECIFIED",
							},
						},
					},
					"filter-in-attributeKey": {
						Type:        "string",
						Description: "Filter executions by tags (format: tag1,tag2,tag3)",
					},
				},
				Required: []string{"launchId"},
			},
		},
		utils.WithAnalytics(
			tr.analytics,
			"get_manual_launch_executions",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetManualLaunchExecutionsArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.ProjectKey)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract project: %w", err)
				}

				if args.LaunchID < 1 {
					return nil, nil, fmt.Errorf("launchId out of range: must be >= 1")
				}

				cfg := tr.client.GetConfig()
				executionURL := fmt.Sprintf(
					"%s://%s/api/v1/project/%s/launch/manual/%d/test-case/execution",
					cfg.Scheme, cfg.Host, url.PathEscape(project), args.LaunchID,
				)

				httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, executionURL, nil)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"failed to build manual launch executions request: %w",
						err,
					)
				}

				for k, v := range cfg.DefaultHeader {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Accept", "application/json")

				query := httpReq.URL.Query()
				utils.ApplyLimitOffset(query, args.Limit, args.Offset, 0)
				if args.FilterCntName != "" {
					query.Set("filter.cnt.name", args.FilterCntName)
				}
				if len(args.FilterInPriority) > 0 {
					query.Set("filter.in.priority", strings.Join(args.FilterInPriority, ","))
				}
				if args.FilterInAttributeKey != "" {
					query.Set("filter.in.attributeKey", args.FilterInAttributeKey)
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
					return nil, nil, fmt.Errorf("manual launch executions request failed: %w", err)
				}

				if resp.StatusCode >= 300 {
					defer resp.Body.Close() //nolint:errcheck
					respBody, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						return nil, nil, fmt.Errorf(
							"manual launch executions request failed (HTTP %d)",
							resp.StatusCode,
						)
					}
					return nil, nil, fmt.Errorf(
						"manual launch executions request failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return utils.ReadResponseBody(resp)
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
				Required: []string{"test-plan-id", "test-case-ids"},
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
