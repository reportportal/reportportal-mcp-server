package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/yosida95/uritemplate/v3"
)

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
	options := []mcp.ToolOption{
		// Tool metadata
		mcp.WithDescription(
			"Get list of test items for a specific launch ID with optional filters",
		),
		lr.projectParameter,
		mcp.WithNumber("launch-id", // ID of the launch
			mcp.Description("Items with specific Launch ID, this is a required parameter"),
		),
	}

	// Add pagination parameters
	options = append(options, setPaginationOptions(defaultSortingForItems)...)

	// Add other parameters
	options = append(options, []mcp.ToolOption{
		// Optional filters
		mcp.WithString("filter.cnt.name", // Item name
			mcp.Description("Items name should contain this substring"),
		),
		mcp.WithString(
			"filter.has.compositeAttribute", // Item attributes
			mcp.Description(
				"Items have this combination of the attribute values, format: attribute1,attribute2:attribute3,... etc. string without spaces",
			),
		),
		mcp.WithString(
			"filter.has.attributeKey", // Item attribute keys
			mcp.Description(
				"Items have these attribute keys (one or few)",
			),
		),
		mcp.WithString("filter.cnt.description", // Item description
			mcp.Description("Items description should contains this substring"),
		),
		mcp.WithString("filter.in.status", // Item status
			mcp.Description("Items with status"),
		),
		mcp.WithBoolean("filter.eq.hasRetries", // Has retries
			mcp.Description("Items have retries"),
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
	}...)

	return mcp.NewTool(
			"list_test_items_by_filter",
			options...), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slog.Debug("START PROCESSING")
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			launchId, err := request.RequireInt("launch-id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract optional filter parameters
			filterName := request.GetString("filter.cnt.name", "")
			filterAttributes := request.GetString("filter.has.compositeAttribute", "")
			filterAttributeKeys := request.GetString("filter.has.attributeKey", "")
			filterDescription := request.GetString("filter.cnt.description", "")
			filterStartTimeFrom := request.GetString("filter.btw.startTime.from", "")
			filterStartTimeTo := request.GetString("filter.btw.startTime.to", "")
			filterStatus := request.GetString("filter.in.status", "")
			filterHasRetries := request.GetBool("filter.eq.hasRetries", false)
			filterParentId := request.GetString("filter.eq.parentId", "")

			urlValues := url.Values{
				"providerType":          {defaultProviderType},
				"filter.eq.hasStats":    {defaultFilterEqHasStats},
				"filter.eq.hasChildren": {defaultFilterEqHasChildren},
				"filter.in.type":        {defaultFilterInType},
			}
			urlValues.Add("launchId", strconv.Itoa(launchId))

			// Add optional filters to urlValues if they have values
			if filterName != "" {
				urlValues.Add("filter.cnt.name", filterName)
			}
			if filterDescription != "" {
				urlValues.Add("filter.cnt.description", filterDescription)
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

			filterStartTime, err := processStartTimeFilter(filterStartTimeFrom, filterStartTimeTo)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if filterStartTime != "" {
				urlValues.Add("filter.btw.startTime", filterStartTime)
			}

			ctxWithParams := WithQueryParams(ctx, urlValues)
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API v2 expects them in a specific format
			requiredUrlParams := map[string]string{
				"launchId": strconv.Itoa(launchId),
			}
			// Build the API request with filters
			apiRequest := lr.client.TestItemAPI.GetTestItemsV2(ctxWithParams, project).
				Params(requiredUrlParams)

			// Apply pagination parameters
			apiRequest = applyPaginationOptions(apiRequest, request, defaultSortingForItems)

			// Process attribute keys and combine with composite attributes
			filterAttributes = processAttributeKeys(filterAttributes, filterAttributeKeys)
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
			lr.projectParameter,
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

func (lr *TestItemResources) toolGetTestItemAttachment() (mcp.Tool, server.ToolHandlerFunc) {
	return mcp.NewTool("get_test_item_attachment_by_id",
			// Tool metadata
			mcp.WithDescription("Get test item attachment by ID"),
			lr.projectParameter,
			mcp.WithString("attachment-content-id", // Parameter for specifying the test item ID
				mcp.Description("Attachment binary content ID"),
			),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			project, err := extractProject(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract the "attachment-content-id" parameter from the request
			attachmentIdStr, err := request.RequireString("attachment-content-id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			attachmentId, err := strconv.ParseInt(attachmentIdStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid attachment ID value: %s", attachmentIdStr)
			}

			// Fetch the attachment with given ID
			response, err := lr.client.FileStorageAPI.GetFile(ctx, attachmentId, project).
				Execute()
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

			contentType := response.Header.Get("Content-Type")

			// Return appropriate MCP result type based on content type
			if isTextContent(contentType) {
				return mcp.NewToolResultResource(
					fmt.Sprintf("Text content (%s, %d bytes)", contentType, len(rawBody)),
					mcp.TextResourceContents{
						URI:      response.Request.URL.String(),
						MIMEType: contentType,
						Text:     string(rawBody),
					},
				), nil
			} else {
				return mcp.NewToolResultResource(
					fmt.Sprintf("Binary content (%s, %d bytes)", contentType, len(rawBody)),
					mcp.BlobResourceContents{
						URI:      response.Request.URL.String(),
						MIMEType: contentType,
						Blob:     string(rawBody),
					},
				), nil
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
				mcp.DefaultString(defaultSortingForLogs),
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
				mcp.Description("Logs with attachment only"),
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

			parentIdStr, err := request.RequireString("parent-item-id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract optional filter parameters
			filterLogLevel := request.GetString("filter.gte.level", "")
			filterLogContains := request.GetString("filter.cnt.message", "")
			filterLogHasAttachment := request.GetBool("filter.ex.binaryContent", false)
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
			if filterLogHasAttachment {
				urlValues.Add(
					"filter.ex.binaryContent",
					strconv.FormatBool(filterLogHasAttachment),
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
			}

			ctxWithParams := WithQueryParams(ctx, urlValues)
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API expects them in a specific format
			requiredUrlParams := map[string]string{
				"parentId": parentIdStr,
			}
			// Build the API request with filters
			apiRequest := lr.client.LogAPI.GetNestedItems(ctxWithParams, parentIdValue, project).
				Params(requiredUrlParams)

			// Apply pagination parameters
			apiRequest = applyPaginationOptions(apiRequest, request, defaultSortingForLogs)

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
