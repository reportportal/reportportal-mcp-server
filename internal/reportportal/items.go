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

// toolListTestItemsByFilter creates a tool to list test items for a specific launch.
func (lr *TestItemResources) toolListTestItemsByFilter() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool(
			"list_test_items_by_filter",
			// Tool metadata
			mcp.WithDescription(
				"Get list of test items for for a specific launch ID with optional filters",
			),
			lr.projectParameter,
			mcp.WithNumber("launch-id", // ID of the launch
				mcp.Description("Items with specific Launch ID, this is a required parameter"),
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
				mcp.Description("Items name should contains this substring"),
			),
			mcp.WithString(
				"filter.has.compositeAttribute", // Item attributes
				mcp.Description(
					"Items has this combination of attributes, format: attribute1,attribute2:attribute3,... etc. string without spaces",
				),
			),
			mcp.WithString("filter.cnt.description", // Item description
				mcp.Description("Items description should contains this substring"),
			),
			mcp.WithString("filter.in.status", // Item status
				mcp.Description("Items with status"),
			),
			mcp.WithBoolean("filter.eq.hasRetries", // Has retries
				mcp.Description("Items has retries"),
			),
			mcp.WithString("filter.eq.parentId", // Parent ID
				mcp.Description("Items parent ID equals"),
			),
			mcp.WithString(
				"filter.btw.startTime.from", // Start time from timestamp
				mcp.Description(
					"Test items with start time from timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
				),
			),
			mcp.WithString(
				"filter.btw.startTime.to", // Start time to timestamp
				mcp.Description(
					"Test items with start time to timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
				),
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
			itemPageSorting := request.GetString("filter.page.sort", itemsDefaultSorting)

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
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API v2 expects them in a specific format
			requiredUrlParams := map[string]string{
				"launchId": strconv.Itoa(launchId),
			}
			// Build the API request with filters
			apiRequest := lr.client.TestItemAPI.GetTestItemsV2(ctxWithParams, project).
				PagePage(page).
				PageSize(pageSize).
				PageSort(itemPageSorting).
				Params(requiredUrlParams)

			if filterAttributes != "" {
				apiRequest = apiRequest.FilterHasCompositeAttribute(filterAttributes)
			}
			if filterHasRetries {
				apiRequest = apiRequest.FilterEqHasRetries(filterHasRetries)
			}

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

func (lr *TestItemResources) resourceTestItemAttachment() (mcp.ResourceTemplate, server.ResourceTemplateHandlerFunc) {
	tmpl := uritemplate.MustNew("reportportal://data/{project}/{dataId}")

	return mcp.NewResourceTemplate(tmpl.Raw(), "reportportal-log-attachment-by-id",
			mcp.WithTemplateDescription("Returns log attachment file by attachment ID")),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			paramValues := tmpl.Match(request.Params.URI)
			if len(paramValues) == 0 {
				return nil, fmt.Errorf("incorrect URI: %s", request.Params.URI)
			}
			// Extract the "project" parameter from the request
			project, found := paramValues["project"]
			if !found || project.String() == "" {
				return nil, fmt.Errorf("missing project in URI: %s", request.Params.URI)
			}
			// Extract the "dataId" parameter from the request
			attachmentIdStr, found := paramValues["dataId"]
			if !found || attachmentIdStr.String() == "" {
				return nil, fmt.Errorf("missing dataId in URI: %s", request.Params.URI)
			}
			attachmentId, err := strconv.ParseInt(attachmentIdStr.String(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid attachment ID value: %s", attachmentIdStr.String())
			}
			// Fetch the attachment with given ID
			response, err := lr.client.FileStorageAPI.GetFile(ctx, attachmentId, project.String()).
				Execute()
			if err != nil {
				return nil, fmt.Errorf("failed to get file: %w", err)
			}
			defer func() {
				if closeErr := response.Body.Close(); closeErr != nil {
					slog.Error("failed to close response body", "error", closeErr)
				}
			}()
			rawBody, err := io.ReadAll(response.Body)
			if err != nil {
				return nil, fmt.Errorf("failed to read response body: %w", err)
			}
			contentType := response.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "application/octet-stream" // default binary type
			}

			// Check if content is text-based
			if strings.HasPrefix(strings.ToLower(contentType), "text/") ||
				strings.Contains(strings.ToLower(contentType), "json") ||
				strings.Contains(strings.ToLower(contentType), "xml") {
				return []mcp.ResourceContents{
					mcp.TextResourceContents{
						URI:      request.Params.URI,
						MIMEType: contentType,
						Text:     string(rawBody),
					},
				}, nil
			} else {
				return []mcp.ResourceContents{
					mcp.BlobResourceContents{
						URI:      request.Params.URI,
						MIMEType: contentType,
						Blob:     string(rawBody),
					},
				}, nil
			}
		}
}

// toolGetTestItemLogsByFilter creates a tool to get test items logs for a specific launch.
func (lr *TestItemResources) toolGetTestItemLogsByFilter() (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool(
			"get_test_item_logs_by_filter",
			mcp.WithDescription(
				"Get list of logs for test item with specific item ID with optional filters",
			),
			lr.projectParameter,
			mcp.WithString("parent-item-id", // ID of the parent item
				mcp.Description("Items with specific Parent Item ID, this is a required parameter"),
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
			mcp.WithString("filter.gte.level", // Item's log level
				mcp.DefaultString(defaultItemLogLevel),
				mcp.Description("Get logs only with specific log level"),
			),
			mcp.WithString(
				"filter.cnt.message", // Log with specific content
				mcp.Description(
					"Log should contains this substring",
				),
			),
			mcp.WithBoolean("filter.ex.binaryContent", // Item description
				mcp.DefaultBool(false),
				mcp.Description("Logs with attachments only"),
			),
			mcp.WithString(
				"filter.in.status", // Item status
				mcp.Description(
					"Items with status, can be a list of values: PASSED, FAILED, SKIPPED, INTERRUPTED, IN_PROGRESS, WARN, INFO",
				),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slog.Debug("START PROCESSING")
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Extract the "page" parameter from the request
			page, pageSize := extractPaging(request)

			parentIdStr, err := request.RequireString("parent-item-id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract optional filter parameters
			itemPageSorting := request.GetString("page.sort", "")
			filterLogLevel := request.GetString("filter.gte.level", "")
			filterLogContains := request.GetString("filter.cnt.message", "")
			filterLogHasAttachments := request.GetBool("filter.ex.binaryContent", false)
			filterLogStatus := request.GetString("filter.in.status", "")

			// Process optional log level filter
			urlValues := url.Values{}
			// Add optional filters to urlValues if they have values
			if filterLogLevel != "" {
				urlValues.Add("filter.gte.level", filterLogLevel)
			}
			if filterLogContains != "" {
				urlValues.Add("filter.cnt.message", filterLogContains)
			}
			if filterLogHasAttachments {
				urlValues.Add(
					"filter.ex.binaryContent",
					strconv.FormatBool(filterLogHasAttachments),
				)
			}
			if filterLogStatus != "" {
				urlValues.Add("filter.in.status", filterLogStatus)
			}
			// Validate parentIdStr and convert it to int64
			var parentIdValue int64
			if parentIdStr != "" {
				var err error
				parentIdValue, err = strconv.ParseInt(parentIdStr, 10, 64)
				if err != nil || parentIdValue < 0 {
					return mcp.NewToolResultError(
						fmt.Sprintf("invalid parent filter ID value: %s", parentIdStr),
					), nil
				}
				// urlValues.Add("filter.eq.parentId", parentIdStr)
			}

			ctxWithParams := WithQueryParams(ctx, urlValues)
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API expects them in a specific format
			requiredUrlParams := map[string]string{
				"parentId": parentIdStr,
			}
			// Build the API request with filters
			apiRequest := lr.client.LogAPI.GetNestedItems(ctxWithParams, parentIdValue, project).
				PagePage(page).
				PageSize(pageSize).
				PageSort(itemPageSorting).
				Params(requiredUrlParams)

			// Execute the request
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
				return nil, fmt.Errorf("failed to read response body: %w", err)
			}
			return mcp.NewToolResultText(string(rawBody)), nil
		}
}
