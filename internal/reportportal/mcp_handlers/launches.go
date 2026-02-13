package mcphandlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

// ToolHandler is a function type for MCP tool handlers with typed input and output.
type ToolHandler[In, Out any] func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error)

// registerTool is a helper to register a tool that returns both tool definition and handler
func registerTool[In, Out any](s *mcp.Server, getTool func() (*mcp.Tool, ToolHandler[In, Out])) {
	tool, handler := getTool()
	mcp.AddTool(s, tool, mcp.ToolHandlerFor[In, Out](handler))
}

// registerResourceTemplate is a helper to register a resource template with its handler
func registerResourceTemplate(
	s *mcp.Server,
	getResourceTemplate func() (*mcp.ResourceTemplate, mcp.ResourceHandler),
) {
	template, handler := getResourceTemplate()
	s.AddResourceTemplate(template, handler)
}

// mustMarshalJSON marshals a value to JSON or panics on error.
//
// This function intentionally panics on marshal failure because it is only used with
// known-safe, compile-time literals and simple slices (e.g., during tool registration/init)
// where json.Marshal cannot fail. Examples include string literals, boolean values, and
// simple string slices used as schema defaults.
//
// WARNING: Do NOT use this function with user-supplied data or runtime values that could
// cause json.Marshal to fail, as this will result in unintended panics. For such cases,
// handle json.Marshal errors explicitly instead.
func mustMarshalJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return b
}

// RegisterLaunchTools registers all launch-related tools and resources with the MCP server
func RegisterLaunchTools(
	s *mcp.Server,
	rpClient *gorp.Client,
	defaultProject string,
	analyticsClient *analytics.Analytics,
) {
	launches := NewLaunchResources(rpClient, analyticsClient, defaultProject)

	registerTool(s, launches.toolGetLaunches)
	registerTool(s, launches.toolGetLastLaunchByName)
	registerTool(s, launches.toolGetLaunchById)
	registerTool(s, launches.toolForceFinishLaunch)
	registerTool(s, launches.toolDeleteLaunch)
	registerTool(s, launches.toolRunAutoAnalysis)
	registerTool(s, launches.toolUniqueErrorAnalysis)
	registerTool(s, launches.toolRunQualityGate)

	registerResourceTemplate(s, launches.resourceLaunch)
}

// LaunchResources is a struct that encapsulates the ReportPortal client.
type LaunchResources struct {
	client         *gorp.Client // Client to interact with the ReportPortal API
	defaultProject string       // Default project name
	analytics      *analytics.Analytics
}

func NewLaunchResources(
	client *gorp.Client,
	analytics *analytics.Analytics,
	project string,
) *LaunchResources {
	return &LaunchResources{
		client:         client,
		defaultProject: project,
		analytics:      analytics,
	}
}

// projectSchema returns a JSON schema for the project parameter
func (lr *LaunchResources) projectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Description: "Project name",
		Default:     mustMarshalJSON(lr.defaultProject),
	}
}

// GetLaunchesArgs holds all filter and pagination params for get_launches.
type GetLaunchesArgs struct {
	Project                     string `json:"project"`
	Page                        uint   `json:"page"`
	PageSize                    uint   `json:"page-size"`
	PageSort                    string `json:"page-sort"`
	FilterCntName               string `json:"filter-cnt-name"`
	FilterHasCompositeAttribute string `json:"filter-has-compositeAttribute"`
	FilterHasAttributeKey       string `json:"filter-has-attributeKey"`
	FilterCntDescription        string `json:"filter-cnt-description"`
	FilterBtwStartTimeFrom      string `json:"filter-btw-startTime-from"`
	FilterBtwStartTimeTo        string `json:"filter-btw-startTime-to"`
	FilterGteNumber             uint32 `json:"filter-gte-number"`
	FilterInUser                string `json:"filter-in-user"`
}

// toolGetLaunches creates a tool to retrieve a paginated list of launches from ReportPortal.
func (lr *LaunchResources) toolGetLaunches() (*mcp.Tool, ToolHandler[GetLaunchesArgs, any]) {
	// Build JSON Schema for input parameters
	properties := utils.SetPaginationProperties(utils.DefaultSortingForLaunches)
	properties["project"] = lr.projectSchema()
	properties["filter-cnt-name"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Launches name should contain this substring",
	}
	properties["filter-has-compositeAttribute"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Launches have this combination of the attributes values, format: attribute1,attribute2:attribute3,... etc. string without spaces",
	}
	properties["filter-has-attributeKey"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Launches have these attribute keys (one or few)",
	}
	properties["filter-cnt-description"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Launches description should contain this substring",
	}
	properties["filter-btw-startTime-from"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Test launches with start time from timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
	}
	properties["filter-btw-startTime-to"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Test launches with start time to timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
	}
	properties["filter-gte-number"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Launch has number greater than",
	}
	properties["filter-in-user"] = &jsonschema.Schema{
		Type:        "string",
		Description: "List of the owner names",
	}

	return &mcp.Tool{
			Name:        "get_launches",
			Description: "Get list of last ReportPortal launches",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"get_launches",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetLaunchesArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				urlValues := url.Values{}

				// Add optional filters to urlValues if they have values
				if args.FilterCntName != "" {
					urlValues.Add("filter.cnt.name", args.FilterCntName)
				}
				if args.FilterCntDescription != "" {
					urlValues.Add("filter.cnt.description", args.FilterCntDescription)
				}
				filterStartTime, err := utils.ProcessStartTimeFilter(
					args.FilterBtwStartTimeFrom,
					args.FilterBtwStartTimeTo,
				)
				if err != nil {
					return nil, nil, err
				}
				if filterStartTime != "" {
					urlValues.Add("filter.btw.startTime", filterStartTime)
				}
				if args.FilterInUser != "" {
					urlValues.Add("filter.in.user", args.FilterInUser)
				}
				if args.FilterGteNumber > 0 {
					urlValues.Add(
						"filter.gte.number",
						strconv.FormatUint(uint64(args.FilterGteNumber), 10),
					)
				}

				ctxWithParams := utils.WithQueryParams(ctx, urlValues)
				// Build API request and apply pagination directly
				apiRequest := lr.client.LaunchAPI.GetProjectLaunches(ctxWithParams, project)

				// Apply pagination parameters
				apiRequest = utils.ApplyPaginationOptions(
					apiRequest,
					args.Page,
					args.PageSize,
					args.PageSort,
					utils.DefaultSortingForLaunches,
				)

				// Process attribute keys and combine with composite attributes
				filterAttributes := utils.ProcessAttributeKeys(
					args.FilterHasCompositeAttribute,
					args.FilterHasAttributeKey,
				)
				if filterAttributes != "" {
					apiRequest = apiRequest.FilterHasCompositeAttribute(filterAttributes)
				}

				_, response, err := apiRequest.Execute()
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

// LaunchIDArgs is shared by tools that only need a project and launch ID.
type LaunchIDArgs struct {
	Project  string `json:"project"`
	LaunchID uint32 `json:"launch_id"`
}

func (lr *LaunchResources) toolRunQualityGate() (*mcp.Tool, ToolHandler[LaunchIDArgs, any]) {
	return &mcp.Tool{
			Name:        "run_quality_gate",
			Description: "Run quality gate on ReportPortal launches",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
				},
				Required: []string{"launch_id"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"run_quality_gate",
			func(ctx context.Context, req *mcp.CallToolRequest, args LaunchIDArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				_, response, err := lr.client.PluginAPI.ExecutePluginCommand(ctx, "startQualityGate", "quality gate", project).
					RequestBody(map[string]interface{}{
						"async":    false,
						"launchId": args.LaunchID,
					}).
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

// GetLastLaunchByNameArgs holds params for get_last_launch_by_name.
type GetLastLaunchByNameArgs struct {
	Project  string `json:"project"`
	Launch   string `json:"launch"`
	Page     uint   `json:"page"`
	PageSize uint   `json:"page-size"`
	PageSort string `json:"page-sort"`
}

// toolGetLastLaunchByName creates a tool to retrieve the last launch by its name.
func (lr *LaunchResources) toolGetLastLaunchByName() (*mcp.Tool, ToolHandler[GetLastLaunchByNameArgs, any]) {
	properties := utils.SetPaginationProperties(utils.DefaultSortingForLaunches)
	properties["project"] = lr.projectSchema()
	properties["launch"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Launch name",
	}

	return &mcp.Tool{
			Name:        "get_last_launch_by_name",
			Description: "Get list of last ReportPortal launches",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"launch"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"get_last_launch_by_name",
			func(ctx context.Context, req *mcp.CallToolRequest, args GetLastLaunchByNameArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.Launch == "" {
					return nil, nil, fmt.Errorf("launch parameter is required")
				}

				urlValues := url.Values{
					"filter.cnt.name": {args.Launch},
				}
				ctxWithParams := utils.WithQueryParams(ctx, urlValues)
				apiRequest := lr.client.LaunchAPI.GetProjectLaunches(ctxWithParams, project)
				apiRequest = utils.ApplyPaginationOptions(
					apiRequest,
					args.Page,
					args.PageSize,
					args.PageSort,
					utils.DefaultSortingForLaunches,
				)

				launches, _, err := apiRequest.Execute()
				if err != nil {
					return nil, nil, err
				}

				if len(launches.Content) < 1 {
					return nil, nil, fmt.Errorf("no launches found")
				}

				r, err := json.Marshal(launches.Content[0])
				if err != nil {
					return nil, nil, fmt.Errorf("failed to marshal response: %w", err)
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: string(r)}},
				}, nil, nil
			},
		)
}

// toolGetLaunchById creates a tool to retrieve a specific launch by its ID directly.
func (lr *LaunchResources) toolGetLaunchById() (*mcp.Tool, ToolHandler[LaunchIDArgs, any]) {
	return &mcp.Tool{
			Name:        "get_launch_by_id",
			Description: "Get a specific launch by its ID directly",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
				},
				Required: []string{"launch_id"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"get_launch_by_id",
			func(ctx context.Context, req *mcp.CallToolRequest, args LaunchIDArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				launch, response, err := lr.client.LaunchAPI.GetLaunch(ctx, strconv.FormatUint(uint64(args.LaunchID), 10), project).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				r, err := json.Marshal(launch)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to marshal response: %w", err)
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: string(r)}},
				}, nil, nil
			},
		)
}

func (lr *LaunchResources) toolDeleteLaunch() (*mcp.Tool, ToolHandler[LaunchIDArgs, any]) {
	return &mcp.Tool{
			Name:        "launch_delete",
			Description: "Delete ReportPortal launch",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
				},
				Required: []string{"launch_id"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"launch_delete",
			func(ctx context.Context, req *mcp.CallToolRequest, args LaunchIDArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				_, _, err = lr.client.LaunchAPI.DeleteLaunch(ctx, int64(args.LaunchID), project).
					Execute()
				if err != nil {
					return nil, nil, err
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("Launch '%d' has been deleted", args.LaunchID),
						},
					},
				}, nil, nil
			},
		)
}

// RunAutoAnalysisArgs holds params for run_auto_analysis.
type RunAutoAnalysisArgs struct {
	Project           string   `json:"project"`
	LaunchID          uint32   `json:"launch_id"`
	AnalyzerMode      string   `json:"analyzer_mode"`
	AnalyzerType      string   `json:"analyzer_type"`
	AnalyzerItemModes []string `json:"analyzer_item_modes"`
}

func (lr *LaunchResources) toolRunAutoAnalysis() (*mcp.Tool, ToolHandler[RunAutoAnalysisArgs, any]) {
	return &mcp.Tool{
			Name:        "run_auto_analysis",
			Description: "Run auto analysis on ReportPortal launch",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
					"analyzer_mode": {
						Type:        "string",
						Description: "Analyzer mode, only one of the values is allowed",
						Enum: []any{
							"all",
							"launch_name",
							"current_launch",
							"previous_launch",
							"current_and_the_same_name",
						},
						Default: mustMarshalJSON("current_launch"),
					},
					"analyzer_type": {
						Type:        "string",
						Description: "Analyzer type, only one of the values is allowed",
						Enum:        []any{"autoAnalyzer", "patternAnalyzer"},
						Default:     mustMarshalJSON("autoAnalyzer"),
					},
					"analyzer_item_modes": {
						Type:        "array",
						Description: "Analyze items modes, one or more of the values are allowed",
						Items: &jsonschema.Schema{
							Type: "string",
							Enum: []any{"to_investigate", "auto_analyzed", "manually_analyzed"},
						},
						Default: mustMarshalJSON([]string{"to_investigate"}),
					},
				},
				Required: []string{
					"launch_id",
					"analyzer_mode",
					"analyzer_type",
					"analyzer_item_modes",
				},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"run_auto_analysis",
			func(ctx context.Context, req *mcp.CallToolRequest, args RunAutoAnalysisArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				analyzerItemModes := args.AnalyzerItemModes
				if len(analyzerItemModes) == 0 {
					analyzerItemModes = []string{"to_investigate"}
				}

				rs, response, err := lr.client.LaunchAPI.
					StartLaunchAnalyzer(ctx, project).
					AnalyzeLaunchRQ(openapi.AnalyzeLaunchRQ{
						LaunchId:         int64(args.LaunchID),
						AnalyzerMode:     strings.ToUpper(args.AnalyzerMode),
						AnalyzerTypeName: strings.ToUpper(args.AnalyzerType),
						AnalyzeItemsMode: analyzerItemModes,
					}).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: rs.GetMessage()}},
				}, nil, nil
			},
		)
}

// UniqueErrorAnalysisArgs holds params for run_unique_error_analysis.
type UniqueErrorAnalysisArgs struct {
	Project       string `json:"project"`
	LaunchID      uint32 `json:"launch_id"`
	RemoveNumbers bool   `json:"remove_numbers"`
}

func (lr *LaunchResources) toolUniqueErrorAnalysis() (*mcp.Tool, ToolHandler[UniqueErrorAnalysisArgs, any]) {
	return &mcp.Tool{
			Name:        "run_unique_error_analysis",
			Description: "Run unique error analysis on ReportPortal launch",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
					"remove_numbers": {
						Type:        "boolean",
						Description: "Remove numbers from analyzed logs",
						Default:     mustMarshalJSON(false),
					},
				},
				Required: []string{"launch_id"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"run_unique_error_analysis",
			func(ctx context.Context, req *mcp.CallToolRequest, args UniqueErrorAnalysisArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				rs, response, err := lr.client.LaunchAPI.
					CreateClusters(ctx, project).
					CreateClustersRQ(openapi.CreateClustersRQ{
						LaunchId:      int64(args.LaunchID),
						RemoveNumbers: openapi.PtrBool(args.RemoveNumbers),
					}).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: rs.GetMessage()}},
				}, nil, nil
			},
		)
}

func (lr *LaunchResources) toolForceFinishLaunch() (*mcp.Tool, ToolHandler[LaunchIDArgs, any]) {
	return &mcp.Tool{
			Name:        "launch_force_finish",
			Description: "Force finish launch",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
				},
				Required: []string{"launch_id"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"launch_force_finish",
			func(ctx context.Context, req *mcp.CallToolRequest, args LaunchIDArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				_, response, err := lr.client.LaunchAPI.ForceFinishLaunch(ctx, int64(args.LaunchID), project).
					Execute()
				if err != nil {
					return nil, nil, fmt.Errorf(
						"%s: %w",
						utils.ExtractResponseError(err, response),
						err,
					)
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf(
								"Launch '%d' has been forcefully finished",
								args.LaunchID,
							),
						},
					},
				}, nil, nil
			},
		)
}

// parseLaunchURI parses a URI like "reportportal://{project}/launch/{launchId}"
// and extracts the project and launchId parameters.
func parseLaunchURI(uri string) (project, launchId string, err error) {
	return utils.ParseReportPortalURI(uri, "launch")
}

// resourceLaunch creates a resource template for accessing launches by URI.
func (lr *LaunchResources) resourceLaunch() (*mcp.ResourceTemplate, mcp.ResourceHandler) {
	return &mcp.ResourceTemplate{
			Name:        "reportportal-launch-by-id",
			Description: "Access ReportPortal launches by URI (reportportal://{project}/launch/{launchId})",
			MIMEType:    "application/json",
			URITemplate: "reportportal://{project}/launch/{launchId}",
		}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			// Parse the URI to extract parameters

			uri := request.Params.URI
			project, launchIdStr, err := parseLaunchURI(uri)
			if err != nil {
				return nil, err
			}

			// Convert launchId to integer
			launchId, err := strconv.ParseUint(launchIdStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid launchId: %w", err)
			}

			// Fetch the launch from ReportPortal
			launchPage, _, err := lr.client.LaunchAPI.GetProjectLaunches(ctx, project).
				FilterEqId(int32(launchId)). //nolint:gosec
				Execute()
			if err != nil {
				return nil, fmt.Errorf("failed to get launch page: %w", err)
			}

			if len(launchPage.Content) < 1 {
				return nil, fmt.Errorf("launch not found: %d", launchId)
			}

			// Marshal the launch to JSON
			launchPayload, err := json.Marshal(launchPage.Content[0])
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the resource contents
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      uri,
						MIMEType: "application/json",
						Text:     string(launchPayload),
					},
				},
			}, nil
		}
}
