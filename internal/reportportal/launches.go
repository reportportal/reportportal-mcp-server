package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
	"github.com/yosida95/uritemplate/v3"
)

// LaunchResources is a struct that encapsulates the ReportPortal client.
type LaunchResources struct {
	client           *gorp.Client // Client to interact with the ReportPortal API
	projectParameter mcp.ToolOption
}

func NewLaunchResources(client *gorp.Client, defaultProject string) *LaunchResources {
	return &LaunchResources{
		client:           client,
		projectParameter: newProjectParameter(defaultProject),
	}
}

// toolListLaunches creates a tool to retrieve a paginated list of launches from ReportPortal.
func (lr *LaunchResources) toolListLaunches() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	options := []mcp.ToolOption{
		// Tool metadata
		mcp.WithDescription("Get list of last ReportPortal launches"),
		lr.projectParameter,
	}

	// Add pagination parameters
	options = append(options, setPaginationOptions()...)

	// Add other parameters
	options = append(options, []mcp.ToolOption{
		// Optional filters
		mcp.WithString("filter.cnt.name", // Item name
			mcp.Description("Launches name should contain this substring"),
		),
		mcp.WithString(
			"filter.has.compositeAttribute", // Item attributes
			mcp.Description(
				"Launches has this combination of the attributes values, format: attribute1,attribute2:attribute3,... etc. string without spaces",
			),
		),
		mcp.WithString(
			"filter.has.attributeKey", // Item attribute keys
			mcp.Description(
				"Launches have these attribute keys (one or few)",
			),
		),
		mcp.WithString("filter.cnt.description", // Item description
			mcp.Description("Launches description should contain this substring"),
		),
		mcp.WithString(
			"filter.btw.startTime.from", // Start time from timestamp
			mcp.Description(
				"Test launches with start time from timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
			),
		),
		mcp.WithString(
			"filter.btw.startTime.to", // Start time to timestamp
			mcp.Description(
				"Test launches with start time to timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
			),
		),

		// Additional filters
		mcp.WithNumber("filter.gte.number", // Has number
			mcp.Description("Launch has number greater than"),
		),
		mcp.WithString("filter.in.user", // Owner name
			mcp.Description("List of the owner names"),
		),
	}...)

	return mcp.NewTool(
			"list_launches",
			options...), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract optional filter parameters
			filterLaunchName := request.GetString("filter.cnt.name", "")
			filterAttributes := request.GetString("filter.has.compositeAttribute", "")
			filterAttributeKeys := request.GetString("filter.has.attributeKey", "")
			filterDescription := request.GetString("filter.cnt.description", "")
			filterStartTimeFrom := request.GetString("filter.btw.startTime.from", "")
			filterStartTimeTo := request.GetString("filter.btw.startTime.to", "")
			filterGreaterThanNumber := request.GetInt("filter.gte.number", 0)
			filterUserNames := request.GetString("filter.in.user", "")

			urlValues := url.Values{}

			// Add optional filters to urlValues if they have values
			if filterLaunchName != "" {
				urlValues.Add("filter.cnt.name", filterLaunchName)
			}
			if filterDescription != "" {
				urlValues.Add("filter.cnt.description", filterDescription)
			}
			filterStartTime, err := processStartTimeFilter(filterStartTimeFrom, filterStartTimeTo)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if filterStartTime != "" {
				urlValues.Add("filter.btw.startTime", filterStartTime)
			}
			if filterUserNames != "" {
				urlValues.Add("filter.in.user", filterUserNames)
			}
			if filterGreaterThanNumber > 0 {
				urlValues.Add("filter.gte.number", strconv.Itoa(filterGreaterThanNumber))
			}

			ctxWithParams := WithQueryParams(ctx, urlValues)
			// Build API request and apply pagination directly
			apiRequest := lr.client.LaunchAPI.GetProjectLaunches(ctxWithParams, project)

			// Apply pagination parameters
			apiRequest = applyPaginationOptions(apiRequest, request)

			// Process attribute keys and combine with composite attributes
			filterAttributes = processAttributeKeys(filterAttributes, filterAttributeKeys)
			if filterAttributes != "" {
				apiRequest = apiRequest.FilterHasCompositeAttribute(filterAttributes)
			}

			_, response, err := apiRequest.Execute()
			if err != nil {
				return mcp.NewToolResultError(extractResponseError(err, response)), nil
			}

			defer func() {
				if closeErr := response.Body.Close(); closeErr != nil {
					slog.Error("failed to close response body", "error", closeErr)
				}
			}()
			rawBody, err := io.ReadAll(response.Body)
			if err != nil {
				return mcp.NewToolResultError(
					fmt.Sprintf("failed to read response body: %v", err),
				), nil
			}
			return mcp.NewToolResultText(string(rawBody)), nil
		}
}

func (lr *LaunchResources) toolRunQualityGate() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("run_quality_gate",
			// Tool metadata
			mcp.WithDescription("Run quality gate on ReportPortal launches"),
			lr.projectParameter,
			mcp.WithNumber("launch_id", // Parameter for specifying the launch ID
				mcp.Description("Launch ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			_, rs, err := lr.client.PluginAPI.ExecutePluginCommand(ctx, "startQualityGate", "quality gate", project).
				RequestBody(map[string]interface{}{
					"async":    false, // Run the quality gate synchronously
					"launchId": launchID,
				}).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// we don't do any special handling of the response, just return it as text
			resBytes, err := io.ReadAll(rs.Body)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(resBytes)), nil
		}
}

// toolGetLastLaunchByName creates a tool to retrieve the last launch by its name.
func (lr *LaunchResources) toolGetLastLaunchByName() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_last_launch_by_name",
			// Tool metadata
			mcp.WithDescription("Get list of last ReportPortal launches"),
			lr.projectParameter,
			mcp.WithString("launch", // Parameter for specifying the launch name
				mcp.Description("Launch name"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract the "launch" parameter from the request
			launchName, err := request.RequireString("launch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Prepare the query parameters for filtering launches by name
			// We use url.Values to create the query parameters
			urlValues := url.Values{
				"filter.cnt.name": {launchName},
			}
			ctxWithParams := WithQueryParams(ctx, urlValues)

			// Fetch the launches matching the provided name
			launches, _, err := lr.client.LaunchAPI.GetProjectLaunches(ctxWithParams, project).
				PagePage(firstPage).
				PageSize(singleResult).
				PageSort(defaultSorting).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Check if any launches were found
			if len(launches.Content) < 1 {
				return mcp.NewToolResultError("No launches found"), nil
			}

			// Serialize the first launch in the result into JSON format
			r, err := json.Marshal(launches.Content[0])
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}

func (lr *LaunchResources) toolDeleteLaunch() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("launch_delete",
			// Tool metadata
			mcp.WithDescription("Delete ReportPortal launch"),
			lr.projectParameter,
			mcp.WithString("launch_id", // Parameter for specifying the launch name
				mcp.Description("Launch ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Extract the "launch" parameter from the request
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			_, _, err = lr.client.LaunchAPI.DeleteLaunch(ctx, int64(launchID), project).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(fmt.Sprintf("Launch '%d' has been deleted", launchID)), nil
		}
}

func (lr *LaunchResources) toolRunAutoAnalysis() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("run_auto_analysis",
			// Tool metadata
			mcp.WithDescription("Run auto analysis on ReportPortal launch"),
			lr.projectParameter,
			mcp.WithString("launch_id", // Parameter for specifying the launch name
				mcp.Description("Launch ID"),
			),
			mcp.WithString(
				"analyzer_mode",
				mcp.Description("Analyzer mode"),
				mcp.Enum(
					"all",
					"launch_name",
					"current_launch",
					"previous_launch",
					"current_and_the_same_name",
				),
				mcp.DefaultString("current_launch"),
				mcp.Required(),
			),
			mcp.WithString("analyzer_type",
				mcp.Description("Analyzer type"),
				mcp.Enum("autoAnalyzer", "patternAnalyzer"),
				mcp.DefaultString("autoAnalyzer"),
				mcp.Required(),
			),
			mcp.WithArray("analyzer_item_modes",
				mcp.Description("Analyzer item modes"),
				mcp.Enum("to_investigate", "auto_analyzed", "manually_analyzed"),
				mcp.DefaultArray([]string{"to_investigate"}),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract the "launch" parameter from the request
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			analyzerMode, err := request.RequireString("analyzer_mode")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			analyzerType, err := request.RequireString("analyzer_type")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			analyzerItemModes, err := request.RequireStringSlice("analyzer_item_modes")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			rs, _, err := lr.client.LaunchAPI.
				StartLaunchAnalyzer(ctx, project).
				AnalyzeLaunchRQ(openapi.AnalyzeLaunchRQ{
					LaunchId:         int64(launchID),
					AnalyzerMode:     strings.ToUpper(analyzerMode),
					AnalyzerTypeName: strings.ToUpper(analyzerType),
					AnalyzeItemsMode: analyzerItemModes,
				}).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(rs.GetMessage()), nil
		}
}

func (lr *LaunchResources) toolUniqueErrorAnalysis() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("run_unique_error_analysis",
			// Tool metadata
			mcp.WithDescription("Run unique error analysis on ReportPortal launch"),
			lr.projectParameter,
			mcp.WithString("launch_id", // Parameter for specifying the launch name
				mcp.Description("Launch ID"),
			),
			mcp.WithBoolean(
				"remove_numbers",
				mcp.Description("Remove numbers from analyzed logs"),
				mcp.DefaultBool(false),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Extract the "launch" parameter from the request
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			removeNumbers, err := request.RequireBool("remove_numbers")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			rs, _, err := lr.client.LaunchAPI.
				CreateClusters(ctx, project).
				CreateClustersRQ(openapi.CreateClustersRQ{
					LaunchId:      int64(launchID),
					RemoveNumbers: openapi.PtrBool(removeNumbers),
				}).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(rs.GetMessage()), nil
		}
}

func (lr *LaunchResources) toolForceFinishLaunch() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("launch_force_finish",
			// Tool metadata
			mcp.WithDescription("Delete ReportPortal launch"),
			lr.projectParameter,
			mcp.WithString("launch_id", // Parameter for specifying the launch name
				mcp.Description("Launch ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Extract the "launch" parameter from the request
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			_, _, err = lr.client.LaunchAPI.ForceFinishLaunch(ctx, int64(launchID), project).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(
				fmt.Sprintf("Launch '%d' has been forcefully finished", launchID),
			), nil
		}
}

func (lr *LaunchResources) resourceLaunch() (mcp.ResourceTemplate, server.ResourceTemplateHandlerFunc) {
	tmpl := uritemplate.MustNew("reportportal://{project}/launch/{launchId}")

	return mcp.NewResourceTemplate(tmpl.Raw(), "reportportal-launch-by-id"),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			paramValues := tmpl.Match(request.Params.URI)
			if len(paramValues) == 0 {
				return nil, fmt.Errorf("incorrect URI: %s", request.Params.URI)
			}
			project, found := paramValues["project"]
			if !found || project.String() == "" {
				return nil, fmt.Errorf("missing project in URI: %s", request.Params.URI)
			}
			launchIdStr, found := paramValues["launchId"]
			if !found || launchIdStr.String() == "" {
				return nil, fmt.Errorf("missing launchId in URI: %s", request.Params.URI)
			}

			launchId, err := strconv.Atoi(launchIdStr.String())
			if err != nil {
				return nil, fmt.Errorf("invalid launchId: %w", err)
			}

			launchPage, _, err := lr.client.LaunchAPI.GetProjectLaunches(ctx, project.String()).
				FilterEqId(int32(launchId)). //nolint:gosec
				Execute()
			if err != nil {
				return nil, fmt.Errorf("failed to get launch page: %w", err)
			}

			if len(launchPage.Content) < 1 {
				return nil, fmt.Errorf("launch not found: %d", launchId)
			}

			launchPayload, err := json.Marshal(launchPage.Content[0])
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      request.Params.URI,
					MIMEType: "application/json",
					Text:     string(launchPayload),
				},
			}, nil
		}
}
