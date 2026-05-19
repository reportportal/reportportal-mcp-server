package mcphandlers

import (
	"context"
	"math"
	"net/url"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/stretchr/testify/require"
)

func newTMSResources(t *testing.T) *TMSResources {
	t.Helper()
	serverURL, err := url.Parse("http://localhost:8080")
	require.NoError(t, err)
	return NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
}

// TestAddTestCasesToTestPlanTool_ArraySchema mirrors TestUpdateDefectTypeForTestItemsTool
// (items_test.go) and guards against the VS Code / GitHub Copilot regression where
// array parameters without an "items" sub-schema are silently mishandled.
func TestAddTestCasesToTestPlanTool_ArraySchema(t *testing.T) {
	tool, _ := newTMSResources(t).toolAddTestCasesToTestPlan()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["test-case-ids"]
	require.True(t, ok, "test-case-ids property should exist")
	require.Equal(t, "array", prop.Type, "test-case-ids should be an array type")
	require.NotNil(t, prop.Items, "test-case-ids must have items property (VS Code compatibility)")
	require.Equal(t, "integer", prop.Items.Type, "items should be of type integer")
	require.NotNil(t, prop.Items.Minimum, "items should have a minimum constraint")
	require.Equal(t, float64(1), *prop.Items.Minimum, "items minimum should be 1")
}

// TestCreateMilestoneTool_TypeEnum verifies that the type field carries the correct
// enum values so that MCP clients and IDE completions can validate without an API call.
func TestCreateMilestoneTool_TypeEnum(t *testing.T) {
	tool, _ := newTMSResources(t).toolCreateMilestone()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	typeProp, ok := schema.Properties["type"]
	require.True(t, ok, "type property should exist")
	require.ElementsMatch(t,
		[]any{"SPRINT", "RELEASE", "OTHER"},
		typeProp.Enum,
		"type enum should contain SPRINT, RELEASE, OTHER",
	)
}

// TestCreateMilestoneTool_StatusEnum verifies the optional status field enum.
func TestCreateMilestoneTool_StatusEnum(t *testing.T) {
	tool, _ := newTMSResources(t).toolCreateMilestone()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	statusProp, ok := schema.Properties["status"]
	require.True(t, ok, "status property should exist")
	require.ElementsMatch(t,
		[]any{"ACTIVE", "CLOSED"},
		statusProp.Enum,
		"status enum should contain ACTIVE, CLOSED",
	)
}

// TestCreateMilestoneTool_RequiredFields verifies the required fields list.
func TestCreateMilestoneTool_RequiredFields(t *testing.T) {
	tool, _ := newTMSResources(t).toolCreateMilestone()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	// projectKey is required when no default project is configured
	require.ElementsMatch(t,
		[]string{"projectKey", "name", "type", "start-date", "end-date"},
		schema.Required,
	)
}

// TestCreateTestCaseTool_PriorityEnum verifies the priority enum on create_test_case.
func TestCreateTestCaseTool_PriorityEnum(t *testing.T) {
	tool, _ := newTMSResources(t).toolCreateTestCase()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	priorityProp, ok := schema.Properties["priority"]
	require.True(t, ok, "priority property should exist")
	require.ElementsMatch(t,
		[]any{"LOW", "MEDIUM", "HIGH", "CRITICAL"},
		priorityProp.Enum,
		"priority enum should contain LOW, MEDIUM, HIGH, CRITICAL",
	)
}

// ---------------------------------------------------------------------------
// Runtime validation tests — handler-level guard assertions (no HTTP calls).
// ---------------------------------------------------------------------------
//
// Skipped findings (handler has no guard, constraint is schema-only):
//   • toolCreateTestCase  invalid priority  — handler passes *string directly to
//     the API without validating enum membership; only the JSON-schema enum
//     constrains this on the client side.
//   • toolAddTestCasesToTestPlan non-integer IDs — the Go type []int64 prevents
//     non-integers at the struct level; no runtime path to test.
//   • toolAddTestCasesToTestPlan IDs < 1 — handler only checks for an empty
//     slice; the minimum:1 constraint is enforced by the schema alone.

// TestCreateMilestoneTool_InvalidWhitespaceName verifies that a name consisting
// entirely of whitespace is rejected before any API call is made.
func TestCreateMilestoneTool_InvalidWhitespaceName(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolCreateMilestone()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateMilestoneArgs{
		ProjectKey: "test-project",
		Name:       "   ",
		Type:       "SPRINT",
		StartDate:  "2026-01-01T00:00:00Z",
		EndDate:    "2026-12-31T00:00:00Z",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "empty or whitespace")
}

// TestCreateMilestoneTool_InvalidStartDateFormat verifies that a start-date
// that is not RFC3339 is rejected with a descriptive error.
func TestCreateMilestoneTool_InvalidStartDateFormat(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolCreateMilestone()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateMilestoneArgs{
		ProjectKey: "test-project",
		Name:       "Sprint 1",
		Type:       "SPRINT",
		StartDate:  "2026/01/01",
		EndDate:    "2026-12-31T00:00:00Z",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid start-date format")
}

// TestCreateMilestoneTool_InvalidEndDateFormat verifies that a valid start-date
// paired with a malformed end-date is rejected.
func TestCreateMilestoneTool_InvalidEndDateFormat(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolCreateMilestone()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateMilestoneArgs{
		ProjectKey: "test-project",
		Name:       "Sprint 1",
		Type:       "SPRINT",
		StartDate:  "2026-01-01T00:00:00Z",
		EndDate:    "31-12-2026",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid end-date format")
}

// TestCreateMilestoneTool_InvalidEndDateBeforeStartDate verifies that an
// end-date earlier than start-date is rejected even when both are valid RFC3339.
func TestCreateMilestoneTool_InvalidEndDateBeforeStartDate(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolCreateMilestone()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateMilestoneArgs{
		ProjectKey: "test-project",
		Name:       "Sprint 1",
		Type:       "SPRINT",
		StartDate:  "2026-06-01T00:00:00Z",
		EndDate:    "2026-01-01T00:00:00Z",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be before")
}

// TestCreateTestCaseTool_InvalidWhitespaceName verifies that a whitespace-only
// name is rejected before any API call is made.
func TestCreateTestCaseTool_InvalidWhitespaceName(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolCreateTestCase()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey: "test-project",
		Name:       "\t  \n",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "empty or whitespace")
}

// TestAddTestCasesToTestPlanTool_InvalidEmptyArray verifies that an empty
// test-case-ids slice is rejected with a clear error before any API call.
func TestAddTestCasesToTestPlanTool_InvalidEmptyArray(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolAddTestCasesToTestPlan()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, AddTestCasesToTestPlanArgs{
		ProjectKey:  "test-project",
		TestPlanID:  42,
		TestCaseIDs: []int64{},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be empty")
}

// TestGetTestFoldersByFilterTool_IntegerFilterBounds verifies that the JSON schema
// for filter-eq-id and filter-eq-parentId carries minimum:1 and maximum:MaxInt32.
func TestGetTestFoldersByFilterTool_IntegerFilterBounds(t *testing.T) {
	tool, _ := newTMSResources(t).toolGetTestFoldersByFilter()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	for _, field := range []string{"filter-eq-id", "filter-eq-parentId"} {
		prop, ok := schema.Properties[field]
		require.True(t, ok, "%s property should exist", field)
		require.Equal(t, "integer", prop.Type, "%s should be integer type", field)
		require.NotNil(t, prop.Minimum, "%s should have a minimum constraint", field)
		require.Equal(t, float64(1), *prop.Minimum, "%s minimum should be 1", field)
		require.NotNil(t, prop.Maximum, "%s should have a maximum constraint", field)
		require.Equal(
			t,
			float64(math.MaxInt32),
			*prop.Maximum,
			"%s maximum should be MaxInt32",
			field,
		)
	}
}

// TestGetTestFoldersByFilterTool_OutOfRangeID verifies that a filter-eq-id
// exceeding math.MaxInt32 is rejected with a descriptive error.
func TestGetTestFoldersByFilterTool_OutOfRangeID(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolGetTestFoldersByFilter()

	outOfRange := int64(math.MaxInt32) + 1
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey: "test-project",
		FilterEqID: &outOfRange,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "filter-eq-id out of range")
}

// TestGetTestFoldersByFilterTool_ZeroID verifies that a filter-eq-id of 0
// (non-positive) is rejected before any API call is made.
func TestGetTestFoldersByFilterTool_ZeroID(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolGetTestFoldersByFilter()

	zero := int64(0)
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey: "test-project",
		FilterEqID: &zero,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "filter-eq-id out of range")
}

// TestGetTestFoldersByFilterTool_NegativeParentID verifies that a negative
// filter-eq-parentId is rejected before any API call is made.
func TestGetTestFoldersByFilterTool_NegativeParentID(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolGetTestFoldersByFilter()

	negative := int64(-1)
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey:       "test-project",
		FilterEqParentID: &negative,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "filter-eq-parentId out of range")
}

// TestGetTestFoldersByFilterTool_OutOfRangeParentID verifies that a
// filter-eq-parentId exceeding math.MaxInt32 is rejected.
func TestGetTestFoldersByFilterTool_OutOfRangeParentID(t *testing.T) {
	ctx := context.Background()
	_, handler := newTMSResources(t).toolGetTestFoldersByFilter()

	outOfRange := int64(math.MaxInt32) + 1
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey:       "test-project",
		FilterEqParentID: &outOfRange,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "filter-eq-parentId out of range")
}
