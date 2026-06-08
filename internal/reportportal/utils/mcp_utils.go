package utils

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
)

// ProjectKeyField is the MCP parameter name for the ReportPortal project identifier.
// Struct JSON tags (e.g. `json:"projectKey"`) must remain string literals and cannot
// reference this constant.
const ProjectKeyField = "projectKey"

// requirementIDCharset is the alphabet used for generated requirement IDs.
const requirementIDCharset = "abcdefghijklmnopqrstuvwxyz0123456789"

// requirementIDLength is the number of random characters following the leading underscore.
const requirementIDLength = 9

// ProjectKeySchema returns a JSON schema for the projectKey MCP tool parameter.
// Default is set only when defaultProjectKey is non-empty (JSON default is omitted otherwise).
// Returns an error if marshalling the default value fails (in practice unreachable for plain strings).
func ProjectKeySchema(defaultProjectKey string) (*jsonschema.Schema, error) {
	s := &jsonschema.Schema{
		Type:        "string",
		Description: "A unique project identifier within the ReportPortal instance.",
	}
	if defaultProjectKey != "" {
		b, err := json.Marshal(defaultProjectKey)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal default project key: %w", err)
		}
		s.Default = b
	}
	return s, nil
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

// ExtractProject resolves the active project key using the agreed priority order:
//
//   - stdio mode: env variable (context, top priority) → tool input (fallback)
//   - HTTP mode:  env variable is ignored; HTTP header projectKey (context, top
//     priority) → tool input (fallback)
//
// In both modes the context-carried value wins; tool input is only used when
// no project has been placed in the context.
func ExtractProject(ctx context.Context, projectArg string) (string, error) {
	if project, ok := GetProjectFromContext(ctx); ok {
		return project, nil
	}
	if project := strings.TrimSpace(projectArg); project != "" {
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

// GenerateRequirementID produces a unique requirement identifier such as "_h5cbt84tg".
func GenerateRequirementID() string {
	b := make([]byte, requirementIDLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a time-based seed so the
		// id stays unique-ish rather than returning an empty/duplicate value.
		seed := time.Now().UnixNano()
		for i := range b {
			b[i] = byte((seed >> (uint(i) * 8)) & 0xff)
		}
	}
	for i := range b {
		b[i] = requirementIDCharset[int(b[i])%len(requirementIDCharset)]
	}
	return "_" + string(b)
}

// RequirementsSchema builds the JSON schema for the optional "requirements" array field.
func RequirementsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Description: "Optional list of requirement values linked to the manual scenario. A unique id is generated automatically for each entry.",
		Items: &jsonschema.Schema{
			Type:        "string",
			Description: "Requirement value/description",
		},
	}
}

// ToRequirementsRQ converts requirement values into the openapi request model,
// generating a unique id for each entry.
func ToRequirementsRQ(
	values []string,
) []openapi.ComEpamReportportalBaseCoreTmsDtoTmsRequirementRQ {
	result := make([]openapi.ComEpamReportportalBaseCoreTmsDtoTmsRequirementRQ, 0, len(values))
	for _, v := range values {
		item := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsRequirementRQ()
		item.SetId(GenerateRequirementID())
		item.SetValue(v)
		result = append(result, *item)
	}
	return result
}
