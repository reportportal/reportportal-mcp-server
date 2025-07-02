package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/yosida95/uritemplate/v3"
)

const itemsDefaultSorting = "startTime,DESC" // default sorting order for test items

// TestItemResources is a struct that encapsulates the ReportPortal client.
type TestItemResources struct {
	client           *gorp.Client // Client to interact with the ReportPortal API
	projectParameter mcp.ToolOption
}

func NewTestItemResources(client *gorp.Client, defaultProject string) *TestItemResources {
	return &TestItemResources{
		client:           client,
		projectParameter: newProjectParameter(defaultProject),
	}
}

// toolListLaunchTestItems creates a tool to list test items for a specific launch.
func (lr *TestItemResources) toolListLaunchTestItems() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_test_items_by_filter",
			// Tool metadata
			mcp.WithDescription("Get list of test items for a launch"),
			lr.projectParameter,
			mcp.WithNumber("launch-id", // ID of the launch
				mcp.Description("Launch ID"),
			),
			mcp.WithNumber("page", // Parameter for specifying the page number
				mcp.DefaultNumber(firstPage),
				mcp.Description("Page number"),
			),
			mcp.WithNumber("page-size", // Parameter for specifying the page size
				mcp.DefaultNumber(defaultPageSize),
				mcp.Description("Page size"),
			),
			mcp.WithString("page.sort", // Sorting fields and direction
				mcp.DefaultString(itemsDefaultSorting),
				mcp.Description("Sorting fields and direction"),
			),

			// Optional filters
			mcp.WithString("filter.cnt.name", // Item name
				mcp.Description("Item name"),
			),
			mcp.WithString("filter.has.compositeAttribute", // Item attributes
				mcp.Description("Item attributes"),
			),
			mcp.WithString("filter.cnt.description", // Item description
				mcp.Description("Item description"),
			),
			mcp.WithString("filter.in.status", // Item status
				mcp.Description("Item status"),
			),
			mcp.WithBoolean("filter.eq.hasRetries", // Has retries
				mcp.Description("Item has retries"),
			),
			mcp.WithString("filter.eq.parentId", // Parent ID
				mcp.Description("Item's parent ID"),
			),
			mcp.WithString("filter.btw.startTime.from", // Start time from timestamp
				mcp.Description("Start time from timestamp (RFC3339 format or Unix epoch)"),
			),
			mcp.WithString("filter.btw.startTime.to", // Start time to timestamp
				mcp.Description("Start time to timestamp (RFC3339 format or Unix epoch)"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slog.Debug("START PROCESSING")
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Extract the "page" parameter from the request
			page, pageSize := extractPaging(request)

			launchId, err := request.RequireInt("launch-id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract optional filter parameters
			filterName := request.GetString("filter.cnt.name", "")
			filterAttributes := request.GetString("filter.has.compositeAttribute", "")
			filterDescription := request.GetString("filter.cnt.description", "")
			filterStartTimeFrom := request.GetString("filter.btw.startTime.from", "")
			filterStartTimeTo := request.GetString("filter.btw.startTime.to", "")
			filterStatus := request.GetString("filter.in.status", "")
			filterHasRetries := request.GetBool("filter.eq.hasRetries", false)
			filterParentId := request.GetString("filter.eq.parentId", "")

			// Process start time interval filter
			var filterStartTime string
			if filterStartTimeFrom != "" && filterStartTimeTo != "" {
				fromEpoch, err := parseTimestampToEpoch(filterStartTimeFrom)
				if err != nil {
					return mcp.NewToolResultError(
						fmt.Sprintf("invalid from timestamp: %v", err),
					), nil
				}
				toEpoch, err := parseTimestampToEpoch(filterStartTimeTo)
				if err != nil {
					return mcp.NewToolResultError(fmt.Sprintf("invalid to timestamp: %v", err)), nil
				}
				if fromEpoch >= toEpoch {
					return mcp.NewToolResultError(
						"from timestamp must be earlier than to timestamp",
					), nil
				}
				// Format as comma-separated values for ReportPortal API
				filterStartTime = fmt.Sprintf("%d,%d", fromEpoch, toEpoch)
			} else if filterStartTimeFrom != "" || filterStartTimeTo != "" {
				return mcp.NewToolResultError("both from and to timestamps are required for time interval filter"), nil
			}

			urlValues := url.Values{
				"providerType":          {defaultProviderType},
				"filter.eq.hasStats":    {filterEqHasStats},
				"filter.eq.hasChildren": {filterEqHasChildren},
				"filter.in.type":        {filterInType},
			}
			urlValues.Add("launchId", strconv.Itoa(launchId))

			// Add optional filters to urlValues if they have values
			if filterName != "" {
				urlValues.Add("filter.cnt.name", filterName)
			}
			if filterAttributes != "" {
				urlValues.Add("filter.has.compositeAttribute", filterAttributes)
			}
			if filterDescription != "" {
				urlValues.Add("filter.cnt.description", filterDescription)
			}
			if filterStartTime != "" {
				urlValues.Add("filter.btw.startTime", filterStartTime)
			}
			if filterStatus != "" {
				urlValues.Add("filter.in.status", filterStatus)
			}
			if filterParentId != "" {
				_, err := strconv.ParseUint(filterParentId, 10, 64)
				if err != nil {
					return mcp.NewToolResultError(
						fmt.Sprintf("invalid parent filter ID value: %s", filterParentId),
					), nil
				}
				urlValues.Add("filter.eq.parentId", filterParentId)
			}

			ctxWithParams := WithQueryParams(ctx, urlValues)
			// Prepare "params" for the API request because the ReportPortal API v2 expects them in a specific format
			params := map[string]string{
				"launchId": strconv.Itoa(launchId),
			}
			// Build the API request with filters
			apiRequest := lr.client.TestItemAPI.GetTestItemsV2(ctxWithParams, project).
				PagePage(page).
				PageSize(pageSize).
				PageSort(itemsDefaultSorting).
				Params(params)

			if filterAttributes != "" {
				apiRequest = apiRequest.FilterHasCompositeAttribute(filterAttributes)
			}
			if filterHasRetries {
				apiRequest = apiRequest.FilterEqHasRetries(filterHasRetries)
			}
			// if filterParentIdInt != 0 {
			// 	apiRequest = apiRequest.FilterEqParentId(int32(filterParentIdInt))
			// }

			// Execute the request
			items, rs, err := apiRequest.Execute()
			if err != nil {
				return mcp.NewToolResultError(extractResponseError(err, rs)), nil
			}

			// Serialize the launches into JSON format
			r, err := json.Marshal(items)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized launches as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}

// toolGetTestItemById creates a tool to retrieve a test item by its ID.
func (lr *TestItemResources) toolGetTestItemById() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_test_item_by_id",
			// Tool metadata
			mcp.WithDescription("Get test item by ID"),
			mcp.WithString("test_item_id", // Parameter for specifying the test item ID
				mcp.Description("Test Item ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Extract the "test_item_id" parameter from the request
			testItemID, err := request.RequireString("test_item_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Fetch the testItem with given ID
			testItem, _, err := lr.client.TestItemAPI.GetTestItem(ctx, testItemID, project).
				Execute()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Serialize the first testItem in the result into JSON format
			r, err := json.Marshal(testItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the serialized testItem as a text result
			return mcp.NewToolResultText(string(r)), nil
		}
}

func (lr *TestItemResources) resourceTestItem() (mcp.ResourceTemplate, server.ResourceTemplateHandlerFunc) {
	tmpl := uritemplate.MustNew("reportportal://{project}/testitem/{testItemId}")

	return mcp.NewResourceTemplate(tmpl.Raw(), "reportportal-test-item-by-id"),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			paramValues := tmpl.Match(request.Params.URI)
			if len(paramValues) == 0 {
				return nil, fmt.Errorf("incorrect URI: %s", request.Params.URI)
			}

			project, found := paramValues["project"]
			if !found || project.String() == "" {
				return nil, fmt.Errorf("missing project in URI: %s", request.Params.URI)
			}
			testItemIdStr, found := paramValues["testItemId"]
			if !found || testItemIdStr.String() == "" {
				return nil, fmt.Errorf("missing testItemId in URI: %s", request.Params.URI)
			}

			testItem, _, err := lr.client.TestItemAPI.GetTestItem(ctx, testItemIdStr.String(), project.String()).
				Execute()
			if err != nil {
				return nil, fmt.Errorf("failed to get test item: %w", err)
			}

			testItemPayload, err := json.Marshal(testItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      request.Params.URI,
					MIMEType: "application/json",
					Text:     string(testItemPayload),
				},
			}, nil
		}
}
