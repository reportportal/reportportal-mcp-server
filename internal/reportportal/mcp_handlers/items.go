package mcphandlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
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

// projectSchema returns a JSON schema for the project parameter
func (lr *TestItemResources) projectSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Description: "Project name",
		Default:     mustMarshalJSON(lr.defaultProject),
	}
}

// RegisterTestItemTools registers all test item-related tools and resources with the MCP server
func RegisterTestItemTools(
	s *mcp.Server,
	rpClient *gorp.Client,
	defaultProject string,
	analyticsClient *analytics.Analytics,
) {
	testItems := NewTestItemResources(rpClient, analyticsClient, defaultProject)

	registerTool(s, testItems.toolGetTestItemById)
	registerTool(s, testItems.toolGetTestItemsByFilter)
	registerTool(s, testItems.toolGetTestItemLogsByFilter)
	registerTool(s, testItems.toolGetTestItemAttachment)
	registerTool(s, testItems.toolGetTestSuitesByFilter)
	registerTool(s, testItems.toolGetProjectDefectTypes)
	registerTool(s, testItems.toolUpdateDefectTypeForTestItems)
	registerTool(s, testItems.toolGetTestItemsHistory)

	registerResourceTemplate(s, testItems.resourceTestItem)
}

// TestItemResources is a struct that encapsulates the ReportPortal client.
type TestItemResources struct {
	client         *gorp.Client // Client to interact with the ReportPortal API
	defaultProject string       // Default project name
	analytics      *analytics.Analytics
}

func NewTestItemResources(
	client *gorp.Client,
	analytics *analytics.Analytics,
	project string,
) *TestItemResources {
	return &TestItemResources{
		client:         client,
		defaultProject: project,
		analytics:      analytics,
	}
}

// resolveSavedFilterIDByName returns the numeric filter ID for the filterId query parameter
// using GET /v1/{projectName}/filter with filter.eq.name.
func (lr *TestItemResources) resolveSavedFilterIDByName(
	ctx context.Context,
	project, filterName string,
) (string, error) {
	page, resp, err := lr.client.UserFilterAPI.GetAllFilters(ctx, project).
		FilterEqName(filterName).
		Execute()
	if err != nil {
		return "", fmt.Errorf("%s: %w", utils.ExtractResponseError(err, resp), err)
	}
	content := page.GetContent()
	if len(content) == 0 {
		return "", fmt.Errorf("no saved filter found with name %q", filterName)
	}
	return strconv.FormatInt(content[0].GetId(), 10), nil
}

// isAllDecimalDigits reports whether s is non-empty and contains only ASCII digits (saved filter IDs are numeric).
func isAllDecimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// resolveFilterIDForProvider returns the value for the filterId query parameter when using providerType=filter.
// All-decimal strings are treated as saved filter IDs and passed through; any other non-empty string is resolved
// as a saved filter name via resolveSavedFilterIDByName.
func (lr *TestItemResources) resolveFilterIDForProvider(
	ctx context.Context,
	project, filterIDOrName string,
) (filterID string, err error) {
	trimmed := strings.TrimSpace(filterIDOrName)
	if trimmed == "" {
		return "", fmt.Errorf("filter-id is empty")
	}
	if isAllDecimalDigits(trimmed) {
		slog.Debug(
			"filter-id is numeric; using as saved filter ID",
			"filterId",
			trimmed,
			"project",
			project,
		)
		return trimmed, nil
	}
	id, err := lr.resolveSavedFilterIDByName(ctx, project, trimmed)
	if err != nil {
		return "", err
	}
	slog.Debug(
		"resolved filter-id from saved filter name",
		"filterName",
		trimmed,
		"filterId",
		id,
		"project",
		project,
	)
	return id, nil
}

// GetTestItemsByFilterArgs holds filter and pagination params for get_test_items_by_filter.
type GetTestItemsByFilterArgs struct {
	Project                     string `json:"project"`
	LaunchID                    int32  `json:"launch-id"`
	Page                        uint   `json:"page"`
	PageSize                    uint   `json:"page-size"`
	PageSort                    string `json:"page-sort"`
	FilterCntName               string `json:"filter-cnt-name"`
	FilterHasCompositeAttribute string `json:"filter-has-compositeAttribute"`
	FilterHasAttributeKey       string `json:"filter-has-attributeKey"`
	FilterCntDescription        string `json:"filter-cnt-description"`
	FilterInStatus              string `json:"filter-in-status"`
	FilterEqHasRetries          string `json:"filter-eq-hasRetries"`
	FilterEqParentId            string `json:"filter-eq-parentId"`
	FilterBtwStartTimeFrom      string `json:"filter-btw-startTime-from"`
	FilterBtwStartTimeTo        string `json:"filter-btw-startTime-to"`
	FilterCntIssueComment       string `json:"filter-cnt-issueComment"`
	FilterInIgnoreAnalyzer      *bool  `json:"filter-in-ignoreAnalyzer"`
	FilterHasTicketId           string `json:"filter-has-ticketId"`
	FilterAnyPatternName        string `json:"filter-any-patternName"`
	FilterEqAutoAnalyzed        *bool  `json:"filter-eq-autoAnalyzed"`
	IncludeBeforeAfterHooks     *bool  `json:"include-before-after-hooks"`
	FilterAnyCompositeAttribute string `json:"filter-any-compositeAttribute"`
	FilterName                  string `json:"filter-name"`
	LaunchesLimit               uint32 `json:"launches-limit"`
}

// toolGetTestItemsByFilter creates a tool to list test items for a specific launch.
func (lr *TestItemResources) toolGetTestItemsByFilter() (*mcp.Tool, ToolHandler[GetTestItemsByFilterArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)

	// Required parameters
	properties["project"] = lr.projectSchema()

	// Conditionally required parameters
	properties["launch-id"] = &jsonschema.Schema{
		Type: "integer",
		Description: "Maps to filter.eq.launchId. When set, providerType is launch. " +
			"Conditionally required if filter-name is not provided. " +
			"Must be non-negative; when querying by launch, use a positive ReportPortal launch ID (omit or 0 when using filter-name only).",
		Minimum: openapi.PtrFloat64(0),
	}
	properties["filter-name"] = &jsonschema.Schema{
		Type: "string",
		Description: "Maps to filterId (numeric saved filter ID, e.g. 197496). When set, providerType is filter. " +
			"Conditionally required if launch-id is not provided.",
	}

	// Add pagination parameters
	paginationProps := utils.SetPaginationProperties(utils.DefaultSortingForItems)
	for k, v := range paginationProps {
		properties[k] = v
	}

	// Add filter parameters
	properties["filter-cnt-name"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items name should contain this substring",
	}
	properties["filter-has-compositeAttribute"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items have this combination of the attribute values, format: attribute1,attribute2:attribute3,... etc. string without spaces",
	}
	properties["filter-has-attributeKey"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items have these attribute keys (one or few)",
	}
	properties["filter-cnt-description"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items description should contains this substring",
	}
	properties["filter-in-status"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items with status",
	}
	properties["filter-eq-hasRetries"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items have retries or not, can be a list of values: TRUE, FALSE, -- (default, filter is not applied)",
		Enum:        []any{"TRUE", "FALSE", "--"},
		Default:     mustMarshalJSON("--"),
	}
	properties["filter-eq-parentId"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items parent ID equals",
	}
	properties["filter-btw-startTime-from"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Test items with start time from timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
	}
	properties["filter-btw-startTime-to"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Test items with start time to timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
	}
	properties["filter-cnt-issueComment"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items defect comment should contains this substring",
	}
	properties["filter-in-ignoreAnalyzer"] = &jsonschema.Schema{
		Type:        "boolean",
		Description: "Items ignored in AA analysis",
	}
	properties["filter-has-ticketId"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items linked Bug tracking system ticket/issue id",
	}
	properties["filter-any-patternName"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items pattern name that test name matches in Pattern-Analysis",
	}
	properties["filter-eq-autoAnalyzed"] = &jsonschema.Schema{
		Type:        "boolean",
		Description: "Items analyzed by RP (AA)",
	}
	properties["include-before-after-hooks"] = &jsonschema.Schema{
		Type:        "boolean",
		Description: "Include all Before/After hook item types (BEFORE_SUITE, BEFORE_GROUPS, BEFORE_CLASS, BEFORE_TEST, TEST, BEFORE_METHOD, AFTER_METHOD, AFTER_TEST, AFTER_CLASS, AFTER_GROUPS, AFTER_SUITE, STEP) together with STEP items. Default: false (only STEP items)",
		Default:     mustMarshalJSON(false),
	}
	properties["filter-any-compositeAttribute"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Maps to filter.any.compositeAttribute. Format: attribute1Key:attribute1Value,attribute2Key:attribute2Value, attribute3Value, e.g. demo,platform:ios,build:1.2.3.",
	}
	properties["launches-limit"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Maps to launchesLimit when providerType is filter. Ignored for providerType launch. Default: 600 if omitted.",
		Default:     mustMarshalJSON(utils.DefaultLaunchesLimitForFilterProvider),
	}

	return &mcp.Tool{
			Name:        "get_test_items_by_filter",
			Description: "Get list of test items with optional filters, using a launch (filter.eq.launchId) or saved filter (filter.eq.name). Either launch-id or filter-name must be provided.",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_test_items_by_filter", func(ctx context.Context, request *mcp.CallToolRequest, args GetTestItemsByFilterArgs) (*mcp.CallToolResult, any, error) {
			slog.Debug("START PROCESSING")
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			if args.LaunchID == 0 && strings.TrimSpace(args.FilterName) == "" {
				return nil, nil, fmt.Errorf(
					"either launch-id or filter-name is required",
				)
			} else if args.LaunchID != 0 && strings.TrimSpace(args.FilterName) != "" {
				return nil, nil, fmt.Errorf(
					"provide either launch-id or filter-name, not both",
				)
			}
			if args.LaunchID < 0 {
				return nil, nil, fmt.Errorf("launch-id must be non-negative, got %d", args.LaunchID)
			}

			filterInType := utils.DefaultFilterInType
			if args.IncludeBeforeAfterHooks != nil && *args.IncludeBeforeAfterHooks {
				filterInType = utils.AllFilterInTypes
			}

			urlValues := url.Values{
				"filter.eq.hasStats":    {utils.DefaultFilterEqHasStats},
				"filter.eq.hasChildren": {utils.DefaultFilterEqHasChildren},
				"filter.in.type":        {filterInType},
			}
			if args.FilterAnyCompositeAttribute != "" {
				urlValues.Add("filter.any.compositeAttribute", args.FilterAnyCompositeAttribute)
			}

			providerType := utils.DefaultProviderType
			var resolvedFilterID string
			if strings.TrimSpace(args.FilterName) != "" {
				providerType = utils.FilterProviderType
				resolvedFilterID, err = lr.resolveFilterIDForProvider(ctx, project, args.FilterName)
				if err != nil {
					return nil, nil, err
				}
				urlValues.Add("filterId", resolvedFilterID)
				launchesLimit := args.LaunchesLimit
				if launchesLimit == 0 {
					launchesLimit = utils.DefaultLaunchesLimitForFilterProvider
				}
				urlValues.Add("launchesLimit", strconv.FormatUint(uint64(launchesLimit), 10))
			} else if args.LaunchID != 0 {
				// Launch provider expects top-level query param launchId (same as get_test_suites_by_filter); Params() only adds params[launchId].
				urlValues.Add("launchId", strconv.FormatInt(int64(args.LaunchID), 10))
			}

			urlValues.Add("providerType", providerType)

			// Add optional filters to urlValues if they have values
			if args.FilterCntName != "" {
				urlValues.Add("filter.cnt.name", args.FilterCntName)
			}
			if args.FilterCntDescription != "" {
				urlValues.Add("filter.cnt.description", args.FilterCntDescription)
			}
			if args.FilterInStatus != "" {
				urlValues.Add("filter.in.status", args.FilterInStatus)
			}
			if args.FilterEqParentId != "" {
				_, err := strconv.ParseUint(args.FilterEqParentId, 10, 64)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"invalid parent filter ID value: %s",
						args.FilterEqParentId,
					)
				}
				urlValues.Add("filter.eq.parentId", args.FilterEqParentId)
			}
			if args.FilterCntIssueComment != "" {
				urlValues.Add("filter.cnt.issueComment", args.FilterCntIssueComment)
			}
			if args.FilterHasTicketId != "" {
				urlValues.Add("filter.has.ticketId", args.FilterHasTicketId)
			}
			if args.FilterAnyPatternName != "" {
				urlValues.Add("filter.any.patternName", args.FilterAnyPatternName)
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
			if args.FilterInIgnoreAnalyzer != nil {
				urlValues.Add(
					"filter.in.ignoreAnalyzer",
					strconv.FormatBool(*args.FilterInIgnoreAnalyzer),
				)
			}

			ctxWithParams := utils.WithQueryParams(ctx, urlValues)
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API v2 expects them in a specific format
			requiredUrlParams := map[string]string{}
			if strings.TrimSpace(args.FilterName) == "" {
				requiredUrlParams["launchId"] = strconv.FormatInt(int64(args.LaunchID), 10)
			}
			// Build the API request with filters
			apiRequest := lr.client.TestItemAPI.GetTestItemsV2(ctxWithParams, project).
				Params(requiredUrlParams)

			// Apply pagination parameters
			apiRequest = utils.ApplyPaginationOptions(
				apiRequest,
				args.Page,
				args.PageSize,
				args.PageSort,
				utils.DefaultSortingForItems,
			)

			// Process attribute keys and combine with composite attributes
			filterAttributes := utils.ProcessAttributeKeys(
				args.FilterHasCompositeAttribute,
				args.FilterHasAttributeKey,
			)
			if filterAttributes != "" {
				apiRequest = apiRequest.FilterHasCompositeAttribute(filterAttributes)
			}
			if args.FilterEqHasRetries != "--" {
				apiRequest = apiRequest.FilterEqHasRetries(args.FilterEqHasRetries == "TRUE")
			}
			if args.FilterEqAutoAnalyzed != nil {
				apiRequest = apiRequest.FilterEqAutoAnalyzed(*args.FilterEqAutoAnalyzed)
			}

			// Execute the request
			_, response, err := apiRequest.Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			// Return the serialized launches as a text result
			return utils.ReadResponseBody(response)
		})
}

// GetTestItemByIdArgs holds params for get_test_item_by_id.
type GetTestItemByIdArgs struct {
	Project    string `json:"project"`
	TestItemID string `json:"test_item_id"`
}

// toolGetTestItemById creates a tool to retrieve a test item by its ID.
func (lr *TestItemResources) toolGetTestItemById() (*mcp.Tool, ToolHandler[GetTestItemByIdArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()
	properties["test_item_id"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Test Item ID",
	}

	return &mcp.Tool{
			Name:        "get_test_item_by_id",
			Description: "Get test item by ID",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project", "test_item_id"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_test_item_by_id", func(ctx context.Context, request *mcp.CallToolRequest, args GetTestItemByIdArgs) (*mcp.CallToolResult, any, error) {
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}
			// Extract the "test_item_id" parameter from the request
			if args.TestItemID == "" {
				return nil, nil, fmt.Errorf("test_item_id is required")
			}

			// Fetch the testItem with given ID
			_, response, err := lr.client.TestItemAPI.GetTestItem(ctx, args.TestItemID, project).
				Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			// Return the serialized testItem as a text result
			return utils.ReadResponseBody(response)
		})
}

// resourceTestItem creates a resource template for accessing test items by URI.
func (lr *TestItemResources) resourceTestItem() (*mcp.ResourceTemplate, mcp.ResourceHandler) {
	return &mcp.ResourceTemplate{
			Name:        "reportportal-test-item-by-id",
			Description: "Access ReportPortal test items by URI (reportportal://{project}/testitem/{testItemId})",
			MIMEType:    "application/json",
			URITemplate: "reportportal://{project}/testitem/{testItemId}",
		}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			// Parse the URI to extract parameters
			uri := request.Params.URI
			project, testItemId, err := parseTestItemURI(uri)
			if err != nil {
				return nil, err
			}

			// Fetch the test item from ReportPortal
			testItem, _, err := lr.client.TestItemAPI.GetTestItem(ctx, testItemId, project).
				Execute()
			if err != nil {
				return nil, fmt.Errorf("failed to get test item: %w", err)
			}

			// Marshal the test item to JSON
			testItemPayload, err := json.Marshal(testItem)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			// Return the resource contents
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      uri,
						MIMEType: "application/json",
						Text:     string(testItemPayload),
					},
				},
			}, nil
		}
}

// parseTestItemURI parses a URI like "reportportal://{project}/testitem/{testItemId}"
// and extracts the project and testItemId parameters.
func parseTestItemURI(uri string) (project, testItemId string, err error) {
	return utils.ParseReportPortalURI(uri, "testitem")
}

// GetTestItemAttachmentArgs holds params for get_test_item_attachment_by_id.
type GetTestItemAttachmentArgs struct {
	Project             string `json:"project"`
	AttachmentContentID string `json:"attachment-content-id"`
}

func (lr *TestItemResources) toolGetTestItemAttachment() (*mcp.Tool, ToolHandler[GetTestItemAttachmentArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()
	properties["attachment-content-id"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Attachment binary content ID",
	}

	return &mcp.Tool{
			Name:        "get_test_item_attachment_by_id",
			Description: "Get test item attachment by ID",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project", "attachment-content-id"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_test_item_attachment_by_id", func(ctx context.Context, request *mcp.CallToolRequest, args GetTestItemAttachmentArgs) (*mcp.CallToolResult, any, error) {
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			// Extract the "attachment-content-id" parameter from the request
			if args.AttachmentContentID == "" {
				return nil, nil, fmt.Errorf("attachment-content-id is required")
			}
			attachmentId, err := strconv.ParseInt(args.AttachmentContentID, 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"invalid attachment ID value: %s",
					args.AttachmentContentID,
				)
			}

			// Fetch the attachment with given ID
			response, err := lr.client.FileStorageAPI.GetFile(ctx, attachmentId, project).
				Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			// Handle response body with cleanup
			rawBody, err := utils.ReadResponseBodyRaw(response)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read attachment body: %w", err)
			}

			contentType := response.Header.Get("Content-Type")

			// Return appropriate MCP result type based on content type
			if utils.IsTextContent(contentType) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf(
								"Text content (%s, %d bytes)\n%s",
								contentType,
								len(rawBody),
								string(rawBody),
							),
						},
					},
				}, nil, nil
			} else {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("Binary content (%s, %d bytes)\nBase64: %s", contentType, len(rawBody), base64.StdEncoding.EncodeToString(rawBody)),
						},
					},
				}, nil, nil
			}
		})
}

// GetTestItemLogsByFilterArgs holds filter and pagination params for get_test_item_logs_by_filter.
type GetTestItemLogsByFilterArgs struct {
	Project               string `json:"project"`
	ParentItemID          string `json:"parent-item-id"`
	Page                  uint   `json:"page"`
	PageSize              uint   `json:"page-size"`
	PageSort              string `json:"page-sort"`
	FilterGteLevel        string `json:"filter-gte-level"`
	FilterCntMessage      string `json:"filter-cnt-message"`
	FilterExBinaryContent string `json:"filter-ex-binaryContent"`
	FilterInStatus        string `json:"filter-in-status"`
}

// toolGetTestItemLogsByFilter creates a tool to get test items logs for a specific launch.
func (lr *TestItemResources) toolGetTestItemLogsByFilter() (*mcp.Tool, ToolHandler[GetTestItemLogsByFilterArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()
	properties["parent-item-id"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items with specific Parent Item ID, this is a required parameter",
	}
	properties["page"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Page number",
		Default:     mustMarshalJSON(utils.FirstPage),
	}
	properties["page-size"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Page size",
		Default:     mustMarshalJSON(utils.DefaultPageSize),
	}
	properties["page-sort"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Sorting fields and direction",
		Default:     mustMarshalJSON(utils.DefaultSortingForLogs),
	}
	properties["filter-gte-level"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Get logs only with specific log level",
		Default:     mustMarshalJSON(utils.DefaultItemLogLevel),
	}
	properties["filter-cnt-message"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Log should contains this substring",
	}
	properties["filter-ex-binaryContent"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Logs with attachment or without, can be a list of values: TRUE, FALSE, -- (default, filter is not applied)",
		Enum:        []any{"TRUE", "FALSE", "--"},
		Default:     mustMarshalJSON("--"),
	}
	properties["filter-in-status"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items with status, can be a list of values: PASSED, FAILED, SKIPPED, INTERRUPTED, IN_PROGRESS, WARN, INFO",
	}

	return &mcp.Tool{
			Name:        "get_test_item_logs_by_filter",
			Description: "Get list of logs for test item with specific item ID with optional filters",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project", "parent-item-id"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_test_item_logs_by_filter", func(ctx context.Context, request *mcp.CallToolRequest, args GetTestItemLogsByFilterArgs) (*mcp.CallToolResult, any, error) {
			slog.Debug("START PROCESSING")
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			if args.ParentItemID == "" {
				return nil, nil, fmt.Errorf("parent-item-id is required")
			}

			// Process optional log level filter
			urlValues := url.Values{}
			// Add optional filters to urlValues if they have values
			if args.FilterGteLevel != "" {
				urlValues.Add("filter.gte.level", args.FilterGteLevel)
			}
			if args.FilterCntMessage != "" {
				urlValues.Add("filter.cnt.message", args.FilterCntMessage)
			}
			if args.FilterExBinaryContent != "--" {
				urlValues.Add(
					"filter.ex.binaryContent",
					strconv.FormatBool(args.FilterExBinaryContent == "TRUE"),
				)
			}
			if args.FilterInStatus != "" {
				urlValues.Add("filter.in.status", args.FilterInStatus)
			}
			// Validate ParentItemID and convert it to int64
			parentIdValue, err := strconv.ParseInt(args.ParentItemID, 10, 64)
			if err != nil || parentIdValue < 0 {
				return nil, nil, fmt.Errorf("invalid parent filter ID value: %s", args.ParentItemID)
			}

			ctxWithParams := utils.WithQueryParams(ctx, urlValues)
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API expects them in a specific format
			requiredUrlParams := map[string]string{
				"parentId": args.ParentItemID,
			}
			// Build the API request with filters
			apiRequest := lr.client.LogAPI.GetNestedItems(ctxWithParams, parentIdValue, project).
				Params(requiredUrlParams)

			// Apply pagination parameters
			apiRequest = utils.ApplyPaginationOptions(
				apiRequest,
				args.Page,
				args.PageSize,
				args.PageSort,
				utils.DefaultSortingForLogs,
			)

			// Execute the request
			_, response, err := apiRequest.Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			return utils.ReadResponseBody(response)
		})
}

// GetTestSuitesByFilterArgs holds filter and pagination params for get_test_suites_by_filter.
type GetTestSuitesByFilterArgs struct {
	Project                     string `json:"project"`
	LaunchID                    uint32 `json:"launch-id"`
	Page                        uint   `json:"page"`
	PageSize                    uint   `json:"page-size"`
	PageSort                    string `json:"page-sort"`
	FilterCntName               string `json:"filter-cnt-name"`
	FilterHasCompositeAttribute string `json:"filter-has-compositeAttribute"`
	FilterHasAttributeKey       string `json:"filter-has-attributeKey"`
	FilterCntDescription        string `json:"filter-cnt-description"`
	FilterEqParentId            string `json:"filter-eq-parentId"`
	FilterBtwStartTimeFrom      string `json:"filter-btw-startTime-from"`
	FilterBtwStartTimeTo        string `json:"filter-btw-startTime-to"`
}

// toolGetTestSuitesByFilter creates a tool to get test suites for a specific launch.
func (lr *TestItemResources) toolGetTestSuitesByFilter() (*mcp.Tool, ToolHandler[GetTestSuitesByFilterArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()
	properties["launch-id"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Suites with specific Launch ID, this is a required parameter",
	}

	// Add pagination parameters
	paginationProps := utils.SetPaginationProperties(utils.DefaultSortingForSuites)
	for k, v := range paginationProps {
		properties[k] = v
	}

	// Add filter parameters
	properties["filter-cnt-name"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites name should contain this substring",
	}
	properties["filter-has-compositeAttribute"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites have this combination of the attribute values, format: attribute1,attribute2:attribute3,... etc. string without spaces",
	}
	properties["filter-has-attributeKey"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites have these attribute keys (one or few)",
	}
	properties["filter-cnt-description"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites description should contains this substring",
	}
	properties["filter-eq-parentId"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites parent ID equals",
	}
	properties["filter-btw-startTime-from"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites with start time from timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
	}
	properties["filter-btw-startTime-to"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Suites with start time to timestamp (GMT timezone(UTC+00:00), RFC3339 format or Unix epoch)",
	}

	return &mcp.Tool{
			Name:        "get_test_suites_by_filter",
			Description: "Get list of test suites for a specific launch ID with optional filters",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project", "launch-id"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_test_suites_by_filter", func(ctx context.Context, request *mcp.CallToolRequest, args GetTestSuitesByFilterArgs) (*mcp.CallToolResult, any, error) {
			slog.Debug("START PROCESSING")
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			if args.LaunchID == 0 {
				return nil, nil, fmt.Errorf("launch-id is required")
			}

			urlValues := url.Values{
				"providerType":   {utils.DefaultProviderType},
				"filter.in.type": {utils.DefaultFilterInTypeSuites},
			}
			urlValues.Add("launchId", strconv.FormatUint(uint64(args.LaunchID), 10))

			// Add optional filters to urlValues if they have values
			if args.FilterCntName != "" {
				urlValues.Add("filter.cnt.name", args.FilterCntName)
			}
			if args.FilterCntDescription != "" {
				urlValues.Add("filter.cnt.description", args.FilterCntDescription)
			}
			if args.FilterEqParentId != "" {
				_, err := strconv.ParseUint(args.FilterEqParentId, 10, 64)
				if err != nil {
					return nil, nil, fmt.Errorf(
						"invalid parent filter ID value: %s",
						args.FilterEqParentId,
					)
				}
				urlValues.Add("filter.eq.parentId", args.FilterEqParentId)
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

			ctxWithParams := utils.WithQueryParams(ctx, urlValues)
			// Prepare "requiredUrlParams" for the API request because the ReportPortal API v2 expects them in a specific format
			requiredUrlParams := map[string]string{
				"launchId": strconv.FormatUint(uint64(args.LaunchID), 10),
			}
			// Build the API request with filters
			apiRequest := lr.client.TestItemAPI.GetTestItemsV2(ctxWithParams, project).
				Params(requiredUrlParams)

			// Apply pagination parameters
			apiRequest = utils.ApplyPaginationOptions(
				apiRequest,
				args.Page,
				args.PageSize,
				args.PageSort,
				utils.DefaultSortingForSuites,
			)

			// Process attribute keys and combine with composite attributes
			filterAttributes := utils.ProcessAttributeKeys(
				args.FilterHasCompositeAttribute,
				args.FilterHasAttributeKey,
			)
			if filterAttributes != "" {
				apiRequest = apiRequest.FilterHasCompositeAttribute(filterAttributes)
			}

			// Execute the request
			_, response, err := apiRequest.Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			// Return the serialized test suites as a text result
			return utils.ReadResponseBody(response)
		})
}

// getDefectTypesFromJson extracts defect types from the project JSON response.
// It parses the raw JSON and returns the configuration/subTypes field as a JSON string.
func getDefectTypesFromJson(rawBody []byte) (string, error) {
	// Parse the JSON response
	var projectData map[string]interface{}
	if err := json.Unmarshal(rawBody, &projectData); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %v", err)
	}

	// Extract configuration/subtypes
	configuration, ok := projectData["configuration"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("configuration field not found or invalid in response")
	}

	subtypes, ok := configuration["subTypes"]
	if !ok {
		return "", fmt.Errorf("configuration/subTypes field not found in response")
	}

	// Serialize only the subtypes
	subtypesJSON, err := json.Marshal(subtypes)
	if err != nil {
		return "", fmt.Errorf("failed to serialize defect types: %v", err)
	}

	return string(subtypesJSON), nil
}

// ProjectArgs holds just the project parameter.
type ProjectArgs struct {
	Project string `json:"project"`
}

// toolGetProjectDefectTypes creates a tool to retrieve all defect types for a specific project.
func (lr *TestItemResources) toolGetProjectDefectTypes() (*mcp.Tool, ToolHandler[ProjectArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()

	return &mcp.Tool{
			Name:        "get_project_defect_types",
			Description: "Get all defect types for a specific project, returns a JSON which contains a list of defect types in the 'configuration/subtypes' array and represents the defect type ID. Example: {\"NO_DEFECT\": { \"locator\": \"nd001\" }} (where NO_DEFECT is the defect type name, nd001 is the defect type unique id)",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_project_defect_types", func(ctx context.Context, request *mcp.CallToolRequest, args ProjectArgs) (*mcp.CallToolResult, any, error) {
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			// Fetch the project with given ID
			_, response, err := lr.client.ProjectAPI.GetProject(ctx, project).
				Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			// Read and parse the response to extract configuration/subtypes
			rawBody, err := utils.ReadResponseBodyRaw(response)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read response body: %w", err)
			}

			// Extract defect types from JSON
			defectTypesJSON, err := getDefectTypesFromJson(rawBody)
			if err != nil {
				return nil, nil, err
			}

			// Return only the defect types data
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: defectTypesJSON},
				},
			}, nil, nil
		})
}

// UpdateDefectTypeArgs holds params for update_defect_type_for_test_items.
type UpdateDefectTypeArgs struct {
	Project           string   `json:"project"`
	TestItemsIDs      []string `json:"test_items_ids"`
	DefectTypeID      string   `json:"defect_type_id"`
	DefectTypeComment string   `json:"defect_type_comment"`
}

// toolUpdateDefectTypeForTestItems creates a tool to update the defect type for a list of specific test items.
func (lr *TestItemResources) toolUpdateDefectTypeForTestItems() (*mcp.Tool, ToolHandler[UpdateDefectTypeArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()
	properties["test_items_ids"] = &jsonschema.Schema{
		Type:        "array",
		Description: "Array of test items IDs",
		Items: &jsonschema.Schema{
			Type: "string",
		},
	}
	properties["defect_type_id"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Defect Type ID, all possible values can be received from the tool 'get_project_defect_types'. Example: {\"NO_DEFECT\": { \"locator\": \"nd001\" }} (where NO_DEFECT is the defect type name, nd001 is the defect type unique id)",
	}
	properties["defect_type_comment"] = &jsonschema.Schema{
		Type:        "string",
		Description: "The defect type comment provides a detailed description of the root cause of the test failure",
	}

	return &mcp.Tool{
			Name:        "update_defect_type_for_test_items",
			Description: "This tool is used to update the defect type for a specific test items. The defect type has a unique id which can be received from the tool 'get_project_defect_types'. Example: {\"NO_DEFECT\": { \"locator\": \"nd001\" }} (where NO_DEFECT is the defect type name, nd001 is the defect type unique id)",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project", "test_items_ids", "defect_type_id"},
			},
		}, utils.WithAnalytics(lr.analytics, "update_defect_type_for_test_items", func(ctx context.Context, request *mcp.CallToolRequest, args UpdateDefectTypeArgs) (*mcp.CallToolResult, any, error) {
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			// Extract the "defect_type_id" parameter from the request
			if args.DefectTypeID == "" {
				return nil, nil, fmt.Errorf("defect_type_id is required")
			}

			if len(args.TestItemsIDs) == 0 {
				return nil, nil, fmt.Errorf(
					"test_items_ids is required and must be a non-empty array",
				)
			}

			// Build the list of issues
			issues := make([]openapi.IssueDefinition, 0, len(args.TestItemsIDs))
			var commentPtr *string
			if args.DefectTypeComment != "" {
				commentPtr = &args.DefectTypeComment
			}
			for _, testItemIdStr := range args.TestItemsIDs {
				testItemId, err := strconv.ParseInt(testItemIdStr, 10, 64)
				if err != nil {
					return nil, nil, fmt.Errorf("invalid test item ID '%s': %w", testItemIdStr, err)
				}
				if testItemId <= 0 {
					return nil, nil, fmt.Errorf(
						"invalid non-positive test item ID '%s'",
						testItemIdStr,
					)
				}
				issues = append(issues, openapi.IssueDefinition{
					TestItemId: testItemId,
					Issue: openapi.Issue{
						IssueType:    args.DefectTypeID,
						AutoAnalyzed: openapi.PtrBool(false),
						Comment:      commentPtr,
					},
				})
			}

			apiRequest := lr.client.TestItemAPI.DefineTestItemIssueType(ctx, project).
				DefineIssueRQ(openapi.DefineIssueRQ{
					Issues: issues,
				})

			// Execute the request
			_, response, err := apiRequest.Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			// Return the serialized testItem as a text result
			return utils.ReadResponseBody(response)
		})
}

// GetTestItemsHistoryArgs holds filter and pagination params for get_test_items_history.
type GetTestItemsHistoryArgs struct {
	Project                     string   `json:"project"`
	FilterEqLaunchId            int32    `json:"filter-eq-launchId"`
	FilterEqParentId            uint64   `json:"filter-eq-parentId"`
	Page                        uint     `json:"page"`
	PageSize                    uint     `json:"page-size"`
	PageSort                    string   `json:"page-sort"`
	HistoryDepth                int32    `json:"historyDepth"`
	HistoryBase                 string   `json:"type"`
	FilterCntName               string   `json:"filter-cnt-name"`
	FilterHasCompositeAttribute string   `json:"filter-has-compositeAttribute"`
	FilterAnyCompositeAttribute string   `json:"filter-any-compositeAttribute"`
	FilterCntDescription        string   `json:"filter-cnt-description"`
	FilterBtwStartTimeFrom      string   `json:"filter-btw-startTime-from"`
	FilterBtwStartTimeTo        string   `json:"filter-btw-startTime-to"`
	FilterInStatus              []string `json:"filter-in-status"`
	FilterEqHasRetries          string   `json:"filter-eq-hasRetries"`
	FilterCntIssueComment       string   `json:"filter-cnt-issueComment"`
	FilterEqAutoAnalyzed        *bool    `json:"filter-eq-autoAnalyzed"`
	FilterInIgnoreAnalyzer      *bool    `json:"filter-in-ignoreAnalyzer"`
	FilterHasTicketId           string   `json:"filter-has-ticketId"`
	FilterAnyPatternName        string   `json:"filter-any-patternName"`
}

// toolGetTestItemsHistory creates a tool to retrieve history of test items.
func (lr *TestItemResources) toolGetTestItemsHistory() (*mcp.Tool, ToolHandler[GetTestItemsHistoryArgs, any]) {
	properties := make(map[string]*jsonschema.Schema)
	properties["project"] = lr.projectSchema()
	properties["filter-eq-launchId"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Filter by Launch ID. Conditionally required if Parent ID is not provided.",
		Minimum:     openapi.PtrFloat64(0),
	}
	properties["filter-eq-parentId"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Filter by Parent Test Item ID (suite ID). Conditionally required if Launch ID is not provided.",
	}

	paginationProps := utils.SetPaginationProperties(utils.DefaultSortingForItems)
	for k, v := range paginationProps {
		properties[k] = v
	}

	properties["historyDepth"] = &jsonschema.Schema{
		Type:        "integer",
		Description: "Depth of history to retrieve. Allowed values: 1–30.",
		Default:     mustMarshalJSON(10),
		Minimum:     openapi.PtrFloat64(1),
		Maximum:     openapi.PtrFloat64(30),
	}
	properties["type"] = &jsonschema.Schema{
		Type:        "string",
		Description: "History base: 'table' collects history from all launches (default), 'line' collects history from launches with the same name.",
		Enum:        []any{"table", "line"},
		Default:     mustMarshalJSON("table"),
	}
	properties["filter-cnt-name"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items whose name contains this substring",
	}
	properties["filter-has-compositeAttribute"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items that have this combination of attribute values. Format: key:value,key2:value2,value3 (no spaces)",
	}
	properties["filter-any-compositeAttribute"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Maps to filter.any.compositeAttribute. Format: attribute1Key:attribute1Value,attribute2Key:attribute2Value,attribute3Value, e.g. demo,platform:ios,build:1.2.3",
	}
	properties["filter-cnt-description"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items whose description contains this substring",
	}
	properties["filter-btw-startTime-from"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items with start time from this timestamp (GMT/UTC+00:00, RFC3339 format or Unix epoch in ms)",
	}
	properties["filter-btw-startTime-to"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items with start time up to this timestamp (GMT/UTC+00:00, RFC3339 format or Unix epoch in ms)",
	}
	properties["filter-in-status"] = &jsonschema.Schema{
		Type:        "array",
		Description: "Filter by execution status",
		Items: &jsonschema.Schema{
			Type: "string",
			Enum: []any{
				"PASSED",
				"FAILED",
				"SKIPPED",
				"INTERRUPTED",
				"IN_PROGRESS",
			},
		},
		UniqueItems: true,
	}
	properties["filter-eq-hasRetries"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Filter items that have retries (TRUE), don't have retries (FALSE), or skip this filter (--)",
		Enum:        []any{"TRUE", "FALSE", "--"},
		Default:     mustMarshalJSON("--"),
	}
	properties["filter-cnt-issueComment"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Items whose defect comment contains this substring",
	}
	properties["filter-eq-autoAnalyzed"] = &jsonschema.Schema{
		Type:        "boolean",
		Description: "Filter items analyzed by ReportPortal Auto-Analyzer (AA)",
	}
	properties["filter-in-ignoreAnalyzer"] = &jsonschema.Schema{
		Type:        "boolean",
		Description: "Filter items ignored in AA analysis",
	}
	properties["filter-has-ticketId"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Filter items linked to a bug tracking system ticket/issue by its ID",
	}
	properties["filter-any-patternName"] = &jsonschema.Schema{
		Type:        "string",
		Description: "Filter items whose name matches a pattern name in Pattern Analysis",
	}

	return &mcp.Tool{
			Name:        "get_test_items_history",
			Description: "Get history of test items for a specific launch or parent suite. Either filter-eq-launchId or filter-eq-parentId must be provided.",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"project"},
			},
		}, utils.WithAnalytics(lr.analytics, "get_test_items_history", func(ctx context.Context, request *mcp.CallToolRequest, args GetTestItemsHistoryArgs) (*mcp.CallToolResult, any, error) {
			slog.Debug("START PROCESSING")
			project, err := utils.ExtractProject(ctx, args.Project)
			if err != nil {
				return nil, nil, err
			}

			if args.FilterEqLaunchId == 0 && args.FilterEqParentId == 0 {
				return nil, nil, fmt.Errorf(
					"either filter-eq-launchId or filter-eq-parentId is required",
				)
			}

			if args.HistoryDepth != 0 && (args.HistoryDepth < 1 || args.HistoryDepth > 30) {
				return nil, nil, fmt.Errorf("historyDepth must be between 1 and 30")
			}

			urlValues := url.Values{
				"filter.eq.hasStats":    {utils.DefaultFilterEqHasStats},
				"filter.eq.hasChildren": {utils.DefaultFilterEqHasChildren},
				"filter.in.type":        {utils.DefaultFilterInType},
			}

			if args.FilterEqParentId != 0 {
				urlValues.Add(
					"filter.eq.parentId",
					strconv.FormatUint(uint64(args.FilterEqParentId), 10),
				)
			}

			if args.FilterCntName != "" {
				urlValues.Add("filter.cnt.name", args.FilterCntName)
			}
			if args.FilterCntDescription != "" {
				urlValues.Add("filter.cnt.description", args.FilterCntDescription)
			}
			if len(args.FilterInStatus) > 0 {
				urlValues.Add("filter.in.status", strings.Join(args.FilterInStatus, ","))
			}
			if args.FilterCntIssueComment != "" {
				urlValues.Add("filter.cnt.issueComment", args.FilterCntIssueComment)
			}
			if args.FilterHasTicketId != "" {
				urlValues.Add("filter.has.ticketId", args.FilterHasTicketId)
			}
			if args.FilterAnyPatternName != "" {
				urlValues.Add("filter.any.patternName", args.FilterAnyPatternName)
			}
			if args.FilterInIgnoreAnalyzer != nil {
				urlValues.Add(
					"filter.in.ignoreAnalyzer",
					strconv.FormatBool(*args.FilterInIgnoreAnalyzer),
				)
			}
			if args.FilterHasCompositeAttribute != "" {
				urlValues.Add("filter.has.compositeAttribute", args.FilterHasCompositeAttribute)
			}
			if args.FilterAnyCompositeAttribute != "" {
				urlValues.Add("filter.any.compositeAttribute", args.FilterAnyCompositeAttribute)
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

			ctxWithParams := utils.WithQueryParams(ctx, urlValues)
			apiRequest := lr.client.TestItemAPI.GetItemsHistory(ctxWithParams, project)

			if args.FilterEqLaunchId != 0 {
				apiRequest = apiRequest.FilterEqLaunchId(
					args.FilterEqLaunchId,
				)
			}
			if args.HistoryDepth > 0 {
				apiRequest = apiRequest.HistoryDepth(args.HistoryDepth)
			} else {
				apiRequest = apiRequest.HistoryDepth(10)
			}
			if args.HistoryBase != "" {
				apiRequest = apiRequest.Type_(args.HistoryBase)
			}
			if args.FilterEqHasRetries != "--" && args.FilterEqHasRetries != "" {
				apiRequest = apiRequest.FilterEqHasRetries(args.FilterEqHasRetries == "TRUE")
			}
			if args.FilterEqAutoAnalyzed != nil {
				apiRequest = apiRequest.FilterEqAutoAnalyzed(*args.FilterEqAutoAnalyzed)
			}

			apiRequest = utils.ApplyPaginationOptions(
				apiRequest,
				args.Page,
				args.PageSize,
				args.PageSort,
				utils.DefaultSortingForItems,
			)

			_, response, err := apiRequest.Execute()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%s: %w",
					utils.ExtractResponseError(err, response),
					err,
				)
			}

			return utils.ReadResponseBody(response)
		})
}
