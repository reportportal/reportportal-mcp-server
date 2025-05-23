package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/yosida95/uritemplate/v3"
)

const (
	firstPage       = 1                       // Default starting page for pagination
	singleResult    = 1                       // Default number of results per page
	defaultPageSize = 20                      // Default number of items per page
	defaultSorting  = "startTime,number,DESC" // default sorting order for API requests
)

//nolint:lll
var tmplAnalyzeLaunch = template.Must(template.New("reportportal_analyze_launch").Parse(
	`Provide comprehensive analyzis of test execution reported to ReportPortal as launch named '{{.name}}'.
Focus on the following aspects:
1. Test Execution Status: Provide a summary of the test execution status, including the number of passed, failed, and skipped tests.
2. Test Duration: Analyze the duration of the test execution and identify any tests that took significantly longer than others.
3. Test Failures: Identify any tests that failed and provide details on the failure reasons.
4. Comparative Analysis: If applicable, compare the current test execution with previous executions to identify trends or regressions.`),
)

// LaunchResources is a struct that encapsulates the ReportPortal client.
type LaunchResources struct {
	client  *gorp.Client // Client to interact with the ReportPortal API
	project string
}

// toolListLaunches creates a tool to retrieve a paginated list of launches from ReportPortal.
func (lr *LaunchResources) toolListLaunches() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_launches",
			// Tool metadata
			mcp.WithDescription("Get list of last ReportPortal launches"),
			mcp.WithNumber("page", // Parameter for specifying the page number
				mcp.DefaultNumber(firstPage),
				mcp.Description("Page number"),
			),
			mcp.WithNumber("page-size", // Parameter for specifying the page size
				mcp.DefaultNumber(defaultPageSize),
				mcp.Description("Page size"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			page, pageSize := extractPaging(request)

			// Fetch the launches from ReportPortal using the provided page details
			launches, _, err := lr.client.LaunchAPI.GetProjectLaunches(ctx, lr.project).
				PagePage(page).
				PageSize(pageSize).
				PageSort(defaultSorting).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Serialize the launches into JSON format
			r, err := json.Marshal(launches)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launches as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}

// toolGetLastLaunchByName creates a tool to retrieve the last launch by its name.
func (lr *LaunchResources) toolGetLastLaunchByName() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_last_launch_by_name",
			// Tool metadata
			mcp.WithDescription("Get list of last ReportPortal launches"),
			mcp.WithString("launch", // Parameter for specifying the launch name
				mcp.Description("Launch name"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract the "launch" parameter from the request
			launchName, err := request.RequireString("launch")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			launches, _, err := lr.client.LaunchAPI.GetProjectLaunches(ctx, lr.project).
				FilterEqName(launchName).
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
			mcp.WithString("launch_id", // Parameter for specifying the launch name
				mcp.Description("Launch ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract the "launch" parameter from the request
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			_, _, err = lr.client.LaunchAPI.DeleteLaunch(ctx, int64(launchID), lr.project).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Return the serialized launch as a text result
			return mcp.NewToolResultText(fmt.Sprintf("Launch '%d' has been deleted", launchID)), nil
		}
}

func (lr *LaunchResources) toolForceFinishLaunch() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("launch_force_finish",
			// Tool metadata
			mcp.WithDescription("Delete ReportPortal launch"),
			mcp.WithString("launch_id", // Parameter for specifying the launch name
				mcp.Description("Launch ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract the "launch" parameter from the request
			launchID, err := request.RequireInt("launch_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the launches matching the provided name
			_, _, err = lr.client.LaunchAPI.ForceFinishLaunch(ctx, int64(launchID), lr.project).
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

func (lr *LaunchResources) promptAnalyzeLaunch() (mcp.Prompt, server.PromptHandlerFunc) {
	return mcp.NewPrompt("reportportal_analyze_launch",
			mcp.WithPromptDescription("A complex prompt"),
			mcp.WithArgument("name",
				mcp.ArgumentDescription("Name of the launch to analyze"),
				mcp.RequiredArgument(),
			),
		), func(ctx context.Context, rq mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			arguments := rq.Params.Arguments

			var promptStr strings.Builder
			if err := tmplAnalyzeLaunch.Execute(&promptStr, arguments); err != nil {
				return nil, err
			}

			return &mcp.GetPromptResult{
				Description: "Analyze last ReportPortal launch by name",
				Messages: []mcp.PromptMessage{
					{
						Role: mcp.RoleUser,
						Content: mcp.TextContent{
							Type: "text",
							Text: promptStr.String(),
						},
					},
				},
			}, nil
		}
}

func (lr *LaunchResources) resourceLaunch() (mcp.ResourceTemplate, server.ResourceTemplateHandlerFunc) {
	tmpl := uritemplate.MustNew("reportportal://launch/{launchId}")

	return mcp.NewResourceTemplate(tmpl.Raw(), "reportportal-launch-by-id"),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			paramValues := tmpl.Match(request.Params.URI)
			if len(paramValues) == 0 {
				return nil, fmt.Errorf("incorrect URI: %s", request.Params.URI)
			}

			launchIdStr, found := paramValues["launchId"]
			if !found || launchIdStr.String() == "" {
				return nil, fmt.Errorf("missing launchId in URI: %s", request.Params.URI)
			}

			launchId, err := strconv.Atoi(launchIdStr.String())
			if err != nil {
				return nil, fmt.Errorf("invalid launchId: %w", err)
			}

			launchPage, _, err := lr.client.LaunchAPI.GetProjectLaunches(ctx, lr.project).
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
