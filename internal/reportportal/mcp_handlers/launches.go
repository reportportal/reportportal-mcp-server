package mcphandlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

const (
	importHTTPClientTimeout = 30 * time.Second
	// importMaxFileSizeBytes is the upper bound on the decoded file payload
	// accepted by the import tool. The MCP protocol delivers file_content as a
	// JSON string so the full content is already resident in memory; this limit
	// prevents an abnormally large value from being processed further.
	importMaxFileSizeBytes = 50 * 1024 * 1024 // 50 MiB
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
	registerTool(s, launches.toolUpdateLaunch)
	registerTool(s, launches.toolForceFinishLaunch)
	registerTool(s, launches.toolDeleteLaunch)
	registerTool(s, launches.toolRunAutoAnalysis)
	registerTool(s, launches.toolUniqueErrorAnalysis)
	registerTool(s, launches.toolRunQualityGate)
	registerTool(s, launches.toolImportLaunchFromFile)

	registerResourceTemplate(s, launches.resourceLaunch)
}

// importPluginInfo holds metadata for a single IMPORT-type plugin.
type importPluginInfo struct {
	Name             string   // canonical plugin name as returned by the API
	MimeTypes        []string // details.acceptFileMimeTypes (empty → use upload defaults)
	MaxFileSizeBytes int64    // from details.maxFileSize, or importMaxFileSizeBytes
}

func parseAcceptFileMimeTypes(v any) []string {
	switch x := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeMediaType(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// pickImportContentType chooses the multipart part Content-Type using optional
// explicit caller input, else by matching fileName's extension to plugin MimeTypes.
func pickImportContentType(mimeTypes []string, fileName, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		if len(mimeTypes) > 0 {
			want := normalizeMediaType(explicit)
			for _, m := range mimeTypes {
				if normalizeMediaType(m) == want {
					return m, nil
				}
			}
			return "", fmt.Errorf(
				"content_type %q is not in this plugin's acceptFileMimeTypes [%s]",
				explicit,
				strings.Join(mimeTypes, ", "),
			)
		}
		return explicit, nil
	}
	if len(mimeTypes) == 0 {
		return "application/octet-stream", nil
	}
	if len(mimeTypes) == 1 {
		return mimeTypes[0], nil
	}
	ext := strings.ToLower(path.Ext(fileName))
	if ext == "" {
		return "", fmt.Errorf(
			"file_name must include a file extension or set content_type to one of: %s",
			strings.Join(mimeTypes, ", "),
		)
	}
	for _, m := range mimeTypes {
		base := strings.TrimSpace(m)
		if i := strings.Index(base, ";"); i >= 0 {
			base = base[:i]
		}
		exts, _ := mime.ExtensionsByType(base)
		for _, e := range exts {
			if strings.ToLower(e) == ext {
				return m, nil
			}
		}
	}
	if t := mime.TypeByExtension(ext); t != "" {
		tNorm := normalizeMediaType(t)
		for _, m := range mimeTypes {
			if normalizeMediaType(m) == tNorm {
				return m, nil
			}
		}
	}
	return "", fmt.Errorf(
		"could not map file extension %q to an accepted MIME type; set content_type to one of: %s",
		ext,
		strings.Join(mimeTypes, ", "),
	)
}

// importPluginCache holds a thread-safe snapshot of available IMPORT-type plugins.
type importPluginCache struct {
	mu      sync.RWMutex
	plugins []importPluginInfo
}

// set replaces the cached plugin entries.
func (c *importPluginCache) set(plugins []importPluginInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plugins = plugins
}

// lookup returns a pointer to the importPluginInfo whose Name matches name
// (case-insensitive, whitespace-trimmed), or nil if not found.
func (c *importPluginCache) lookup(name string) *importPluginInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	normalized := strings.ToLower(strings.TrimSpace(name))
	for i := range c.plugins {
		if strings.ToLower(strings.TrimSpace(c.plugins[i].Name)) == normalized {
			info := c.plugins[i] // copy
			return &info
		}
	}
	return nil
}

// list returns a copy of the current plugin names.
func (c *importPluginCache) list() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, len(c.plugins))
	for i, p := range c.plugins {
		names[i] = p.Name
	}
	return names
}

// LaunchResources is a struct that encapsulates the ReportPortal client.
type LaunchResources struct {
	client         *gorp.Client // Client to interact with the ReportPortal API
	defaultProject string       // Default project name
	analytics      *analytics.Analytics
	importPlugins  importPluginCache
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

// fetchAndCacheImportPlugins calls GET /api/v1/plugin, filters entries whose groupType
// is "IMPORT", and stores their metadata in the local cache.
func (lr *LaunchResources) fetchAndCacheImportPlugins(ctx context.Context) error {
	plugins, _, err := lr.client.PluginAPI.GetPlugins(ctx).Execute()
	if err != nil {
		return fmt.Errorf("get plugins: %w", err)
	}
	var infos []importPluginInfo
	for _, p := range plugins {
		if p.GroupType != nil && strings.EqualFold(*p.GroupType, "IMPORT") && p.Name != nil {
			info := importPluginInfo{
				Name:             *p.Name,
				MaxFileSizeBytes: importMaxFileSizeBytes,
			}
			details := p.GetDetails()
			if mimes, ok := details["acceptFileMimeTypes"]; ok {
				info.MimeTypes = parseAcceptFileMimeTypes(mimes)
			}
			if maxSize, ok := details["maxFileSize"]; ok {
				if f, ok := maxSize.(float64); ok && f > 0 {
					pluginMax := int64(f)
					if pluginMax < info.MaxFileSizeBytes {
						info.MaxFileSizeBytes = pluginMax
					}
				}
			}
			infos = append(infos, info)
		}
	}
	lr.importPlugins.set(infos)
	slog.Debug("import plugin cache refreshed", "plugins", infos)
	return nil
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

// UpdateLaunchAttribute represents a single key/value attribute for a launch.
type UpdateLaunchAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// UpdateLaunchArgs holds params for update_launch.
type UpdateLaunchArgs struct {
	Project     string                  `json:"project"`
	LaunchID    uint32                  `json:"launch_id"`
	Description *string                 `json:"description,omitempty"`
	Attributes  []UpdateLaunchAttribute `json:"attributes,omitempty"`
}

func (lr *LaunchResources) toolUpdateLaunch() (*mcp.Tool, ToolHandler[UpdateLaunchArgs, any]) {
	return &mcp.Tool{
			Name:        "update_launch",
			Description: "Update launch attributes and description in ReportPortal",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"project": lr.projectSchema(),
					"launch_id": {
						Type:        "integer",
						Description: "Launch ID",
					},
					"description": {
						Type:        "string",
						Description: "New description for the launch. Replaces the existing description.",
					},
					"attributes": {
						Type:        "array",
						Description: "List of attributes to set on the launch. Each attribute has a key (optional) and a value. Replaces all existing attributes.",
						Items: &jsonschema.Schema{
							Type: "object",
							Properties: map[string]*jsonschema.Schema{
								"key": {
									Type:        "string",
									Description: "Attribute key (may be empty for tag-style attributes)",
								},
								"value": {
									Type:        "string",
									Description: "Attribute value",
								},
							},
							Required: []string{"value"},
						},
					},
				},
				Required: []string{"launch_id"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"update_launch",
			func(ctx context.Context, req *mcp.CallToolRequest, args UpdateLaunchArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.LaunchID == 0 {
					return nil, nil, fmt.Errorf("launch_id is required")
				}

				if args.Description == nil && args.Attributes == nil {
					return nil, nil, fmt.Errorf(
						"at least one of description or attributes must be provided",
					)
				}

				updateRQ := openapi.UpdateLaunchRQ{}
				if args.Description != nil {
					updateRQ.SetDescription(*args.Description)
				}
				if args.Attributes != nil {
					attrs := make([]openapi.ItemAttributeResource, 0, len(args.Attributes))
					for i, a := range args.Attributes {
						if strings.TrimSpace(a.Value) == "" {
							if trimmedKey := strings.TrimSpace(a.Key); trimmedKey != "" {
								return nil, nil, fmt.Errorf(
									"attribute[%d] key=%q has empty value",
									i,
									trimmedKey,
								)
							}
							return nil, nil, fmt.Errorf("attribute[%d] has empty value", i)
						}
						attr := openapi.ItemAttributeResource{Value: a.Value}
						if trimmedKey := strings.TrimSpace(a.Key); trimmedKey != "" {
							attr.SetKey(trimmedKey)
						}
						attrs = append(attrs, attr)
					}
					updateRQ.SetAttributes(attrs)
				}

				rs, response, err := lr.client.LaunchAPI.
					UpdateLaunch(ctx, int64(args.LaunchID), project).
					UpdateLaunchRQ(updateRQ).
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

// ImportLaunchFromFileArgs holds parameters for importing a launch from a file.
type ImportLaunchFromFileArgs struct {
	Project         string `json:"project"`
	PluginName      string `json:"plugin_name"`
	FileName        string `json:"file_name"`
	FileContent     string `json:"file_content"`
	ContentEncoding string `json:"content_encoding"`
	ContentType     string `json:"content_type"`
}

// toolImportLaunchFromFile creates a tool to import a launch into ReportPortal from a file passed inline.
func (lr *LaunchResources) toolImportLaunchFromFile() (*mcp.Tool, ToolHandler[ImportLaunchFromFileArgs, any]) {
	properties := map[string]*jsonschema.Schema{
		"project": lr.projectSchema(),
		"plugin_name": {
			Type: "string",
			Description: "Name of the import plugin to use (e.g. 'junit'). " +
				"Available import plugins (groupType: \"IMPORT\") and their accepted MIME types " +
				"(details.acceptFileMimeTypes) can be listed via the GET /api/v1/plugin endpoint.",
		},
		"file_name": {
			Type:        "string",
			Description: "File name with extension (e.g. 'results.xml', 'report.zip'). Used as the multipart upload filename.",
		},
		"file_content": {
			Type: "string",
			Description: "Content of the file to import. " +
				"Plain text (e.g. raw XML) by default; set content_encoding to \"base64\" for binary files (e.g. ZIP archives).",
		},
		"content_encoding": {
			Type:        "string",
			Description: "Encoding of file_content. Omit or set to \"none\" for plain text files (e.g. XML). Set to \"base64\" for binary files (e.g. ZIP archives).",
			Enum:        []any{"none", "base64"},
			Default:     mustMarshalJSON("none"),
		},
		"content_type": {
			Type: "string",
			Description: "Optional IANA media type for the file part (must match an entry in the plugin's acceptFileMimeTypes when that list is non-empty). " +
				"If omitted, the type is chosen from the file extension and the plugin's accepted MIME list.",
		},
	}

	return &mcp.Tool{
			Name: "import_launch_from_file",
			Description: "Import a launch into ReportPortal from a file passed inline. " +
				"Pass plain text content directly (e.g. raw XML) or base64-encoded content for binary files (e.g. ZIP). " +
				"The plugin_name must match a plugin with groupType \"IMPORT\" available on the server " +
				"(retrievable via GET /api/v1/plugin). Each import plugin defines the accepted file " +
				"MIME types in its details.acceptFileMimeTypes field.",
			InputSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: properties,
				Required:   []string{"plugin_name", "file_name", "file_content"},
			},
		},
		utils.WithAnalytics(
			lr.analytics,
			"import_launch_from_file",
			func(ctx context.Context, req *mcp.CallToolRequest, args ImportLaunchFromFileArgs) (*mcp.CallToolResult, any, error) {
				project, err := utils.ExtractProject(ctx, args.Project)
				if err != nil {
					return nil, nil, err
				}

				if args.PluginName == "" {
					return nil, nil, fmt.Errorf("plugin_name is required")
				}
				if args.FileName == "" {
					return nil, nil, fmt.Errorf("file_name is required")
				}
				if args.FileContent == "" {
					return nil, nil, fmt.Errorf("file_content is required")
				}

				// Validate plugin_name against the known import-plugin cache.
				// If not found, refresh the cache once using the current request
				// context (carries auth token in HTTP mode) and check again.
				pluginInfo := lr.importPlugins.lookup(args.PluginName)
				if pluginInfo == nil {
					if refreshErr := lr.fetchAndCacheImportPlugins(ctx); refreshErr != nil {
						return nil, nil, fmt.Errorf(
							"failed to refresh import plugins cache: %w",
							refreshErr,
						)
					}
					pluginInfo = lr.importPlugins.lookup(args.PluginName)
					if pluginInfo == nil {
						slog.Warn("plugin_name not found in available import plugins",
							"plugin_name", args.PluginName,
							"available_plugins", strings.Join(lr.importPlugins.list(), ", "))
						return nil, nil, fmt.Errorf(
							"plugin %q not found; available import plugins: [%s]",
							args.PluginName, strings.Join(lr.importPlugins.list(), ", "))
					}
				}

				// Fail fast before allocating the multipart body: measure the decoded
				// size against the plugin's limit. For plain text the byte count is
				// exact; for base64 content we run the real decoder into io.Discard so
				// the measurement is precise rather than estimated.
				maxFileSizeBytes := pluginInfo.MaxFileSizeBytes
				var decodedSize int64
				contentEncoding := strings.ToLower(strings.TrimSpace(args.ContentEncoding))
				switch contentEncoding {
				case "", "none":
					decodedSize = int64(len(args.FileContent))
				case "base64":
					dec := base64.NewDecoder(
						base64.StdEncoding,
						strings.NewReader(args.FileContent),
					)
					var countErr error
					decodedSize, countErr = io.Copy(
						io.Discard,
						io.LimitReader(dec, maxFileSizeBytes+1),
					)
					if countErr != nil {
						return nil, nil, fmt.Errorf("invalid base64 content: %w", countErr)
					}
				default:
					return nil, nil, fmt.Errorf(
						"unsupported content_encoding %q; expected \"none\" or \"base64\"",
						args.ContentEncoding,
					)
				}
				if decodedSize > maxFileSizeBytes {
					return nil, nil, fmt.Errorf(
						"file too large: decoded size %d bytes exceeds limit %d bytes",
						decodedSize,
						maxFileSizeBytes,
					)
				}

				// Build the multipart body by copying directly from the source
				// reader, avoiding an intermediate []byte allocation for the file
				// content. The body is bounded by maxFileSizeBytes.
				var body bytes.Buffer
				mw := multipart.NewWriter(&body)

				mimeType, mimeErr := pickImportContentType(
					pluginInfo.MimeTypes,
					args.FileName,
					args.ContentType,
				)
				if mimeErr != nil {
					return nil, nil, mimeErr
				}
				// quoteEscaper handles \, ", \r, \n per multipart spec
				escapedFilename := strings.NewReplacer(
					`\`, `\\`,
					`"`, `\"`,
					"\r", "",
					"\n", "",
					"\x00", "",
				).Replace(args.FileName)
				fh := make(textproto.MIMEHeader)
				fh.Set(
					"Content-Disposition",
					fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapedFilename),
				)
				fh.Set("Content-Type", mimeType)
				part, err := mw.CreatePart(fh)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to create multipart field: %w", err)
				}
				var src io.Reader = strings.NewReader(args.FileContent)
				if contentEncoding == "base64" {
					src = base64.NewDecoder(base64.StdEncoding, src)
				}
				if _, err = io.Copy(part, src); err != nil {
					return nil, nil, fmt.Errorf("failed to write file content: %w", err)
				}
				if err = mw.Close(); err != nil {
					return nil, nil, fmt.Errorf("failed to finalise multipart body: %w", err)
				}

				// Reuse the same APIClient config (host, scheme, auth headers, middleware)
				// so HTTP-mode token injection and other settings work identically.
				cfg := lr.client.GetConfig()
				// Snapshot reference-type config fields before use. GetConfig returns a
				// pointer to shared state. Config fields are only written during
				// initialization (before the server starts serving requests) and are
				// never mutated afterwards, so no synchronization is required here.
				// The copies below are a defensive measure to avoid relying on that
				// immutability guarantee implicitly.
				localHeaders := make(map[string]string, len(cfg.DefaultHeader))
				for k, v := range cfg.DefaultHeader {
					localHeaders[k] = v
				}
				localMw := cfg.Middleware
				localResponseMw := cfg.ResponseMiddleware
				localHTTPClient := cfg.HTTPClient

				importURL := fmt.Sprintf("%s://%s/api/v1/plugin/%s/%s/import",
					cfg.Scheme, cfg.Host,
					url.PathEscape(project), url.PathEscape(pluginInfo.Name))

				httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, importURL, &body)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to build import request: %w", err)
				}
				// Apply defaults first so that request-specific headers can override them.
				for k, v := range localHeaders {
					httpReq.Header.Set(k, v)
				}
				httpReq.Header.Set("Content-Type", mw.FormDataContentType())
				httpReq.Header.Set("Accept", "application/json")
				if localMw != nil {
					localMw(httpReq)
				}

				httpClient := localHTTPClient
				if httpClient == nil {
					httpClient = &http.Client{Timeout: importHTTPClientTimeout}
				}
				resp, err := httpClient.Do(httpReq)
				if err != nil {
					return nil, nil, fmt.Errorf("import request failed: %w", err)
				}
				defer resp.Body.Close() //nolint:errcheck

				respBody, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to read import response: %w", err)
				}
				if localResponseMw != nil {
					if mwErr := localResponseMw(resp, respBody); mwErr != nil {
						return nil, nil, fmt.Errorf("import response middleware error: %w", mwErr)
					}
				}

				if resp.StatusCode >= 300 {
					return nil, nil, fmt.Errorf(
						"import failed (HTTP %d): %s",
						resp.StatusCode,
						string(respBody),
					)
				}

				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: string(respBody)}},
				}, nil, nil
			},
		)
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
			project, launchIdStr, err := utils.ParseReportPortalURI(uri, "launch")
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
