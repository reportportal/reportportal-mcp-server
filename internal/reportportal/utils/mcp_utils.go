package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProjectKeyField is the MCP parameter name for the ReportPortal project identifier.
// Struct JSON tags (e.g. `json:"projectKey"`) must remain string literals and cannot
// reference this constant.
const ProjectKeyField = "projectKey"

// RequiredFields builds the Required slice for a tool's InputSchema.
// ProjectKeyField is always included first: in HTTP mode there is no env-var
// fallback so it must be supplied by the caller; in stdio mode it still
// appears in Required (the schema carries a default value so clients can
// pre-fill it) so the field is consistently visible across both modes.
// Duplicates in others are silently dropped.
func RequiredFields(others ...string) []string {
	result := []string{ProjectKeyField}
	seen := map[string]bool{ProjectKeyField: true}
	for _, f := range others {
		if !seen[f] {
			seen[f] = true
			result = append(result, f)
		}
	}
	return result
}

// ProjectKeySchema returns a JSON schema for the projectKey MCP tool parameter.
// Default is set only when defaultProjectKey is non-empty (JSON default is omitted otherwise).
func ProjectKeySchema(defaultProjectKey string) *jsonschema.Schema {
	s := &jsonschema.Schema{
		Type: "string",
		Description: "URL-safe project key: the identifier from ReportPortal URLs after the '#' " +
			"(not the project display name).",
	}
	if defaultProjectKey != "" {
		b, err := json.Marshal(defaultProjectKey)
		if err != nil {
			panic(fmt.Sprintf("failed to marshal JSON: %v", err))
		}
		s.Default = b
	}
	return s
}

// ApplyPaginationOptions applies pagination to an API request from typed values.
// Zero values for page and pageSize fall back to defaults.
func ApplyPaginationOptions[T PaginatedRequest[T]](
	apiRequest T,
	page, pageSize uint,
	pageSort, defaultSort string,
) T {
	if page < FirstPage {
		page = FirstPage
	} else if page > math.MaxInt32 {
		page = math.MaxInt32
	}

	if pageSize <= 0 {
		pageSize = DefaultPageSize
	} else if pageSize > math.MaxInt32 {
		pageSize = math.MaxInt32
	}

	if pageSort == "" {
		pageSort = defaultSort
	}

	return apiRequest.
		PagePage(int32(page)).     //nolint:gosec
		PageSize(int32(pageSize)). //nolint:gosec
		PageSort(pageSort)
}

// ExtractProject extracts the project from a typed argument string or context fallback.
func ExtractProject(ctx context.Context, projectArg string) (string, error) {
	if project := strings.TrimSpace(projectArg); project != "" {
		return project, nil
	}
	if project, ok := GetProjectFromContext(ctx); ok {
		return project, nil
	}
	return "", fmt.Errorf(
		"no project parameter found in request, HTTP header, or environment variable",
	)
}

// EventTracker interface for analytics tracking
type EventTracker interface {
	TrackMCPEvent(ctx context.Context, toolName string)
}

// WithAnalytics is a generic version of WithAnalytics for typed input structs.
func WithAnalytics[In any](
	tracker EventTracker,
	toolName string,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
		// Track the event before executing the tool (synchronous since it's just incrementing a counter)
		if tracker != nil {
			tracker.TrackMCPEvent(ctx, toolName)
		}

		// Execute the original handler
		return handler(ctx, req, args)
	}
}
