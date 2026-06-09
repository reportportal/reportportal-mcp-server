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
		Description: "Optional list of requirement values linked to the manual scenario. A unique id is generated automatically for each entry. On update, pass an empty array ([]) to clear all existing requirements; omit the field to leave them unchanged.",
		Items: &jsonschema.Schema{
			Type:        "string",
			Description: "Requirement value/description (must contain at least one non-whitespace character)",
			MinLength:   openapi.PtrInt(1),
			Pattern:     `\S`,
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

// Manual scenario type input values exposed to MCP clients. These map onto the
// API discriminator manualScenarioType: "description" -> TEXT, "test case with
// steps" -> STEPS.
const (
	TestCaseTypeDescription = "description"
	TestCaseTypeWithSteps   = "test case with steps"
)

// StepArg represents a single manual scenario step from tool input.
type StepArg struct {
	Instructions   string  `json:"instructions"`
	ExpectedResult *string `json:"expected-result,omitempty"`
}

// AttributeArg represents a single test case attribute (key/value pair) from tool input.
type AttributeArg struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// attributesItemSchema returns the shared object schema for a single attribute entry.
func attributesItemSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"key": {
				Type:        "string",
				Description: "Attribute key (must contain at least one non-whitespace character)",
				MinLength:   openapi.PtrInt(1),
				Pattern:     `\S`,
			},
			"value": {
				Type:        "string",
				Description: "Attribute value (must contain at least one non-whitespace character)",
				MinLength:   openapi.PtrInt(1),
				Pattern:     `\S`,
			},
		},
		Required: []string{"key", "value"},
	}
}

// AttributesCreateSchema returns the JSON schema for the "attributes" field on
// create_test_case. Existing project attributes that match are reused; missing
// ones are created automatically.
func AttributesCreateSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Description: "Optional list of attributes (key/value pairs) to attach to the test case. Existing project attributes that match both key and value are reused; missing ones are created automatically before being linked to the test case.",
		Items:       attributesItemSchema(),
	}
}

// AttributesUpdateSchema returns the JSON schema for the "attributes" field on
// update_test_case. Pass an empty array ([]) to clear all existing attributes;
// omit the field entirely to leave them unchanged.
func AttributesUpdateSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Description: "Optional list of attributes (key/value pairs) to attach to the test case. Existing project attributes that match both key and value are reused; missing ones are created automatically before being linked to the test case. Pass an empty array ([]) to clear all existing attributes; omit the field to leave them unchanged.",
		Items:       attributesItemSchema(),
	}
}

// ManualScenarioArgs carries the manual scenario inputs shared by the
// create_test_case and update_test_case tools. Requirements is a pointer so a
// nil value (field omitted) can be distinguished from an explicit empty slice
// (field provided as []), which clears the existing requirements.
type ManualScenarioArgs struct {
	TestCaseType   *string
	Instructions   *string
	ExpectedResult *string
	Preconditions  *string
	Requirements   *[]string
	Steps          []StepArg
}

// TestCaseTypeCreateSchema builds the JSON schema for the optional "test-case-type"
// field on create_test_case, where an omitted type defaults to "description".
func TestCaseTypeCreateSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Description: `Manual scenario type. "description" stores a plain text scenario via instructions/expected-result without test steps; "test case with steps" stores an ordered list of steps. Defaults to "description".`,
		Enum:        []any{TestCaseTypeDescription, TestCaseTypeWithSteps},
	}
}

// TestCaseTypeUpdateSchema builds the JSON schema for the optional "test-case-type"
// field on update_test_case. Unlike create there is no default: the type must be
// provided whenever any manual scenario field is changed, and omitting it leaves
// the existing scenario unchanged.
func TestCaseTypeUpdateSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Description: `Manual scenario type. "description" stores a plain text scenario via instructions/expected-result without test steps; "test case with steps" stores an ordered list of steps. Required when changing the manual scenario (instructions, expected-result, preconditions, requirements, steps); omit it to leave the scenario unchanged.`,
		Enum:        []any{TestCaseTypeDescription, TestCaseTypeWithSteps},
	}
}

// StepsSchema builds the JSON schema for the optional "steps" array field.
func StepsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Description: `Ordered list of manual test steps. Required when test-case-type is "test case with steps"; must be omitted for "description".`,
		Items: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"instructions": {
					Type:        "string",
					Description: "Step instructions / action to perform (must contain at least one non-whitespace character)",
					MinLength:   openapi.PtrInt(1),
					Pattern:     `\S`,
				},
				"expected-result": {
					Type:        "string",
					Description: "Optional expected result of the step",
				},
			},
			Required: []string{"instructions"},
		},
	}
}

// ToStepsRQ converts step arguments into the openapi request model.
func ToStepsRQ(steps []StepArg) []openapi.ComEpamReportportalBaseCoreTmsDtoTmsStepRQ {
	result := make([]openapi.ComEpamReportportalBaseCoreTmsDtoTmsStepRQ, 0, len(steps))
	for _, s := range steps {
		item := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsStepRQ()
		item.SetInstructions(s.Instructions)
		if s.ExpectedResult != nil {
			item.SetExpectedResult(*s.ExpectedResult)
		}
		result = append(result, *item)
	}
	return result
}

func newPreconditionsRQ(
	value string,
) openapi.ComEpamReportportalBaseCoreTmsDtoTmsManualScenarioPreconditionsRQ {
	pre := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsManualScenarioPreconditionsRQ()
	pre.SetValue(value)
	return *pre
}

// BuildManualScenario constructs a test case manual scenario request from tool
// input. The test case type selects between a TEXT ("description") scenario and a
// STEPS ("test case with steps") scenario; an empty/nil type defaults to TEXT.
func BuildManualScenario(
	a ManualScenarioArgs,
) (openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario, error) {
	var zero openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario

	tcType := TestCaseTypeDescription
	if a.TestCaseType != nil && strings.TrimSpace(*a.TestCaseType) != "" {
		tcType = strings.TrimSpace(*a.TestCaseType)
	}

	if a.Requirements != nil {
		for i, r := range *a.Requirements {
			if strings.TrimSpace(r) == "" {
				return zero, fmt.Errorf("requirements[%d] value must be non-empty", i)
			}
		}
	}

	switch tcType {
	case TestCaseTypeDescription:
		if len(a.Steps) > 0 {
			return zero, fmt.Errorf(
				`steps are only valid when test-case-type is "test case with steps"`,
			)
		}
		if (a.Instructions != nil) != (a.ExpectedResult != nil) {
			return zero, fmt.Errorf(
				"instructions and expected-result must both be provided together to set a TEXT manual scenario",
			)
		}
		text := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQ("TEXT")
		if a.Instructions != nil {
			text.SetInstructions(*a.Instructions)
		}
		if a.ExpectedResult != nil {
			text.SetExpectedResult(*a.ExpectedResult)
		}
		if a.Preconditions != nil {
			text.SetPreconditions(newPreconditionsRQ(*a.Preconditions))
		}
		if a.Requirements != nil {
			text.SetRequirements(ToRequirementsRQ(*a.Requirements))
		}
		return openapi.ComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQAsComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario(
			text,
		), nil

	case TestCaseTypeWithSteps:
		if a.Instructions != nil || a.ExpectedResult != nil {
			return zero, fmt.Errorf(
				`instructions and expected-result are not valid for "test case with steps"; provide them inside each step`,
			)
		}
		if len(a.Steps) == 0 {
			return zero, fmt.Errorf(
				`steps must not be empty when test-case-type is "test case with steps"`,
			)
		}
		for i, s := range a.Steps {
			if strings.TrimSpace(s.Instructions) == "" {
				return zero, fmt.Errorf("steps[%d] instructions must be non-empty", i)
			}
		}
		steps := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsStepsManualScenarioRQ("STEPS")
		steps.SetSteps(ToStepsRQ(a.Steps))
		if a.Preconditions != nil {
			steps.SetPreconditions(newPreconditionsRQ(*a.Preconditions))
		}
		if a.Requirements != nil {
			steps.SetRequirements(ToRequirementsRQ(*a.Requirements))
		}
		return openapi.ComEpamReportportalBaseCoreTmsDtoTmsStepsManualScenarioRQAsComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario(
			steps,
		), nil

	default:
		return zero, fmt.Errorf(
			"invalid test-case-type %q: must be %q or %q",
			tcType, TestCaseTypeDescription, TestCaseTypeWithSteps,
		)
	}
}
