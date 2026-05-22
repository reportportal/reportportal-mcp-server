package mcphandlers

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
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

// newTMSResourcesWithCounter creates a TMSResources backed by an httptest.Server
// and returns both the resources and an atomic counter incremented on every
// inbound HTTP request. Tests that expect validation to short-circuit before
// any network call can assert that the counter remains zero.
func newTMSResourcesWithCounter(t *testing.T) (*TMSResources, *atomic.Int64) {
	t.Helper()
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	), &requestCount
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
// for filter-eq-id and filter-eq-parentId carries minimum:1 and no upper bound,
// so that int64 IDs above MaxInt32 are accepted.
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
		require.Nil(t, prop.Maximum, "%s should have no maximum constraint", field)
	}
}

// TestGetTestFoldersByFilterTool_LargeIDReachesHTTP verifies that a filter-eq-id
// greater than math.MaxInt32 is accepted and forwarded as a query parameter to
// the HTTP layer, allowing int64 IDs from typical ReportPortal deployments.
func TestGetTestFoldersByFilterTool_LargeIDReachesHTTP(t *testing.T) {
	ctx := context.Background()

	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":[],"page":{"totalElements":0}}`))
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolGetTestFoldersByFilter()

	largeID := int64(math.MaxInt32) + 1
	_, _, callErr := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey: "test-project",
		FilterEqID: &largeID,
	})

	require.NoError(t, callErr, "large int64 ID should be accepted and forwarded")
	require.NotNil(t, capturedQuery, "HTTP request should reach the server")
	require.Equal(t, strconv.FormatInt(largeID, 10), capturedQuery.Get("filter.eq.id"))
}

// TestGetTestFoldersByFilterTool_ZeroID verifies that a filter-eq-id of 0
// (non-positive) is rejected before any API call is made.
func TestGetTestFoldersByFilterTool_ZeroID(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolGetTestFoldersByFilter()

	zero := int64(0)
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey: "test-project",
		FilterEqID: &zero,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "filter-eq-id out of range")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestGetTestFoldersByFilterTool_NegativeParentID verifies that a negative
// filter-eq-parentId is rejected before any API call is made.
func TestGetTestFoldersByFilterTool_NegativeParentID(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolGetTestFoldersByFilter()

	negative := int64(-1)
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey:       "test-project",
		FilterEqParentID: &negative,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "filter-eq-parentId out of range")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestDeleteFolderTool_ZeroID verifies that folderId of 0 is rejected before
// any API call is made.
func TestDeleteFolderTool_ZeroID(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolDeleteFolder()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, DeleteFolderArgs{
		ProjectKey: "test-project",
		FolderID:   0,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "folderId out of range")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestDeleteFolderTool_RequiredFields verifies that folderId is required.
func TestDeleteFolderTool_RequiredFields(t *testing.T) {
	tool, _ := newTMSResources(t).toolDeleteFolder()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	require.ElementsMatch(t, []string{"projectKey", "folderId"}, schema.Required)
}

// TestDeleteFolderTool_IDMinimumConstraint verifies the folderId schema has minimum:1.
func TestDeleteFolderTool_IDMinimumConstraint(t *testing.T) {
	tool, _ := newTMSResources(t).toolDeleteFolder()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["folderId"]
	require.True(t, ok, "folderId property should exist")
	require.Equal(t, "integer", prop.Type)
	require.NotNil(t, prop.Minimum)
	require.Equal(t, float64(1), *prop.Minimum)
}

// TestDeleteFolderTool_SuccessReachesHTTP verifies that a valid folderId causes
// an HTTP DELETE request to the correct path.
func TestDeleteFolderTool_SuccessReachesHTTP(t *testing.T) {
	ctx := context.Background()

	type httpReq struct{ method, path string }
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCh <- httpReq{method: r.Method, path: r.URL.Path}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolDeleteFolder()

	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, DeleteFolderArgs{
		ProjectKey: "test-project",
		FolderID:   42,
	})

	captured := <-reqCh
	require.NoError(t, callErr)
	require.Equal(t, http.MethodDelete, captured.method)
	require.Contains(t, captured.path, "/tms/folder/42")
	require.NotNil(t, result)
	require.False(t, result.IsError)
}

// TestGetTestFoldersByFilterTool_LargeParentIDReachesHTTP verifies that a
// filter-eq-parentId greater than math.MaxInt32 is accepted and forwarded as a
// query parameter to the HTTP layer.
func TestGetTestFoldersByFilterTool_LargeParentIDReachesHTTP(t *testing.T) {
	ctx := context.Background()

	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"content":[],"page":{"totalElements":0}}`))
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolGetTestFoldersByFilter()

	largeParentID := int64(math.MaxInt32) + 1
	_, _, callErr := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey:       "test-project",
		FilterEqParentID: &largeParentID,
	})

	require.NoError(t, callErr, "large int64 parent ID should be accepted and forwarded")
	require.NotNil(t, capturedQuery, "HTTP request should reach the server")
	require.Equal(t, strconv.FormatInt(largeParentID, 10), capturedQuery.Get("filter.eq.parentId"))
}
