package mcphandlers

import (
	"context"
	"encoding/json"
	"io"
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

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
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

	require.ElementsMatch(t,
		[]string{"name", "type", "start-date", "end-date"},
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
		[]any{"LOW", "MEDIUM", "HIGH", "CRITICAL", "BLOCKER", "UNSPECIFIED"},
		priorityProp.Enum,
		"priority enum should match update_test_case and filter-in-priority values",
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

// TestGetTestFoldersByFilterTool_CntNameSchema verifies filter-cnt-name is exposed
// in the schema and maps to API filter.cnt.name.
func TestGetTestFoldersByFilterTool_CntNameSchema(t *testing.T) {
	tool, _ := newTMSResources(t).toolGetTestFoldersByFilter()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["filter-cnt-name"]
	require.True(t, ok, "filter-cnt-name property should exist")
	require.Equal(t, "string", prop.Type)
}

// TestGetTestFoldersByFilterTool_CntNameReachesHTTP verifies filter.cnt.name is forwarded.
func TestGetTestFoldersByFilterTool_CntNameReachesHTTP(t *testing.T) {
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

	_, _, callErr := handler(ctx, &mcp.CallToolRequest{}, GetTestFoldersByFilterArgs{
		ProjectKey:    "test-project",
		FilterCntName: "smoke",
	})

	require.NoError(t, callErr)
	require.NotNil(t, capturedQuery, "HTTP request should reach the server")
	require.Equal(t, "smoke", capturedQuery.Get("filter.cnt.name"))
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
	_, handler := res.toolDeleteTestFolder()

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
	tool, _ := newTMSResources(t).toolDeleteTestFolder()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	require.ElementsMatch(t, []string{"folderId"}, schema.Required)
}

// TestDeleteFolderTool_IDMinimumConstraint verifies the folderId schema has minimum:1.
func TestDeleteFolderTool_IDMinimumConstraint(t *testing.T) {
	tool, _ := newTMSResources(t).toolDeleteTestFolder()

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
	_, handler := res.toolDeleteTestFolder()

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

// TestDeleteTestCaseTool_RequiredFields verifies that testCaseId is required.
func TestDeleteTestCaseTool_RequiredFields(t *testing.T) {
	tool, _ := newTMSResources(t).toolDeleteTestCase()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	require.ElementsMatch(t, []string{"testCaseId"}, schema.Required)
}

// TestDeleteTestCaseTool_IDMinimumConstraint verifies the testCaseId schema has minimum:1.
func TestDeleteTestCaseTool_IDMinimumConstraint(t *testing.T) {
	tool, _ := newTMSResources(t).toolDeleteTestCase()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["testCaseId"]
	require.True(t, ok, "testCaseId property should exist")
	require.Equal(t, "integer", prop.Type)
	require.NotNil(t, prop.Minimum)
	require.Equal(t, float64(1), *prop.Minimum)
}

// TestDeleteTestCaseTool_ZeroID verifies that testCaseId of 0 is rejected before
// any API call is made.
func TestDeleteTestCaseTool_ZeroID(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolDeleteTestCase()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, DeleteTestCaseArgs{
		ProjectKey: "test-project",
		TestCaseID: 0,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "testCaseId out of range")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestDeleteTestCaseTool_SuccessReachesHTTP verifies that a valid testCaseId causes
// an HTTP DELETE request to the correct path.
func TestDeleteTestCaseTool_SuccessReachesHTTP(t *testing.T) {
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
	_, handler := res.toolDeleteTestCase()

	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, DeleteTestCaseArgs{
		ProjectKey: "test-project",
		TestCaseID: 99,
	})

	captured := <-reqCh
	require.NoError(t, callErr)
	require.Equal(t, http.MethodDelete, captured.method)
	require.Contains(t, captured.path, "/tms/test-case/99")
	require.NotNil(t, result)
	require.False(t, result.IsError)
}

// TestUpdateTestCaseTool_RequiredFields verifies that testCaseId is required.
func TestUpdateTestCaseTool_RequiredFields(t *testing.T) {
	tool, _ := newTMSResources(t).toolUpdateTestCase()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	require.ElementsMatch(t, []string{"testCaseId"}, schema.Required)
}

// TestUpdateTestCaseTool_PriorityEnum verifies the priority enum on update_test_case.
func TestUpdateTestCaseTool_PriorityEnum(t *testing.T) {
	tool, _ := newTMSResources(t).toolUpdateTestCase()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	priorityProp, ok := schema.Properties["priority"]
	require.True(t, ok, "priority property should exist")
	require.ElementsMatch(t,
		[]any{"LOW", "MEDIUM", "HIGH", "CRITICAL", "BLOCKER", "UNSPECIFIED"},
		priorityProp.Enum,
		"priority enum should match get_test_cases_by_filter filter-in-priority values",
	)
}

// TestUpdateTestCaseTool_ZeroID verifies that testCaseId of 0 is rejected before
// any API call is made.
func TestUpdateTestCaseTool_ZeroID(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolUpdateTestCase()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey: "test-project",
		TestCaseID: 0,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "testCaseId out of range")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestUpdateTestCaseTool_SuccessReachesHTTP verifies that a valid testCaseId causes
// an HTTP PATCH request to the correct path.
func TestUpdateTestCaseTool_SuccessReachesHTTP(t *testing.T) {
	ctx := context.Background()

	type httpReq struct{ method, path string }
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCh <- httpReq{method: r.Method, path: r.URL.Path}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":55,"name":"Updated TC"}`))
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	name := "Updated TC"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey: "test-project",
		TestCaseID: 55,
		Name:       &name,
	})

	captured := <-reqCh
	require.NoError(t, callErr)
	require.Equal(t, http.MethodPatch, captured.method)
	require.Contains(t, captured.path, "/tms/test-case/55")
	require.NotNil(t, result)
	require.False(t, result.IsError)
}

// TestUpdateTestCaseTool_PartialScenarioAllowed verifies that instructions and
// expected-result can each be supplied independently for the "text" type.
func TestUpdateTestCaseTool_PartialScenarioAllowed(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		path   string
		body   string
	}
	makeServer := func(t *testing.T) (*httptest.Server, chan httpReq) {
		t.Helper()
		ch := make(chan httpReq, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ := io.ReadAll(r.Body)
			ch <- httpReq{method: r.Method, path: r.URL.Path, body: string(rawBody)}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":1,"name":"TC"}`))
		}))
		t.Cleanup(srv.Close)
		return srv, ch
	}
	makeHandler := func(t *testing.T, srv *httptest.Server) func(context.Context, *mcp.CallToolRequest, UpdateTestCaseArgs) (*mcp.CallToolResult, any, error) {
		t.Helper()
		serverURL, err := url.Parse(srv.URL)
		require.NoError(t, err)
		res := NewTMSResources(
			gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
			nil,
			"",
		)
		_, h := res.toolUpdateTestCase()
		return h
	}

	descriptionType := "text"

	t.Run("instructions only", func(t *testing.T) {
		srv, reqCh := makeServer(t)
		handler := makeHandler(t, srv)
		instructions := "step 1"
		result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
			ProjectKey:   "test-project",
			TestCaseID:   1,
			TestCaseType: &descriptionType,
			Instructions: &instructions,
		})
		require.NoError(t, callErr)
		require.NotNil(t, result)
		require.False(t, result.IsError)
		require.Len(t, reqCh, 1)
		captured := <-reqCh
		require.Equal(t, http.MethodPatch, captured.method)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
		manual, ok := payload["manualScenario"].(map[string]any)
		require.True(t, ok, "manualScenario should be present")
		require.Equal(t, "TEXT", manual["manualScenarioType"])
		require.Equal(t, "step 1", manual["instructions"])
		require.NotContains(t, manual, "expectedResult")
	})

	t.Run("expected-result only", func(t *testing.T) {
		srv, reqCh := makeServer(t)
		handler := makeHandler(t, srv)
		expected := "pass"
		result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
			ProjectKey:     "test-project",
			TestCaseID:     1,
			TestCaseType:   &descriptionType,
			ExpectedResult: &expected,
		})
		require.NoError(t, callErr)
		require.NotNil(t, result)
		require.False(t, result.IsError)
		require.Len(t, reqCh, 1)
		captured := <-reqCh
		require.Equal(t, http.MethodPatch, captured.method)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
		manual, ok := payload["manualScenario"].(map[string]any)
		require.True(t, ok, "manualScenario should be present")
		require.Equal(t, "TEXT", manual["manualScenarioType"])
		require.Equal(t, "pass", manual["expectedResult"])
		require.NotContains(t, manual, "instructions")
	})
}

// TestUpdateTestCaseTool_ScenarioFieldsRequireType verifies that supplying manual
// scenario fields without an explicit test-case-type is rejected, preventing a
// silent overwrite/downgrade of the existing scenario.
func TestUpdateTestCaseTool_ScenarioFieldsRequireType(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolUpdateTestCase()

	preconditions := "service is running"
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:    "test-project",
		TestCaseID:    1,
		Preconditions: &preconditions,
		// TestCaseType intentionally omitted
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "test-case-type must be specified")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestUpdateTestCaseTool_BothScenarioFieldsSendsSinglePatch verifies that when
// both instructions and expected-result are provided a single PATCH is issued
// (no prior GET).
func TestUpdateTestCaseTool_BothScenarioFieldsSendsSinglePatch(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		path   string
		body   string
	}
	reqCh := make(chan httpReq, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, path: r.URL.Path, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()
	descriptionType := "text"
	instructions := "step 1"
	expected := "pass"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:     "test-project",
		TestCaseID:     7,
		TestCaseType:   &descriptionType,
		Instructions:   &instructions,
		ExpectedResult: &expected,
	})
	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	// Exactly one request must have been made and it must be a PATCH (no GET).
	require.Len(t, reqCh, 1)
	captured := <-reqCh
	require.Equal(t, http.MethodPatch, captured.method)
	require.Contains(t, captured.path, "/tms/test-case/7")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in PATCH payload")
	require.Equal(t, "TEXT", manual["manualScenarioType"])
	require.Equal(t, "step 1", manual["instructions"])
	require.Equal(t, "pass", manual["expectedResult"])
}

// TestCreateTestCaseTool_PreconditionsAndRequirementsReachHTTP verifies that the
// preconditions and requirements fields are forwarded into the manual scenario of
// the create_test_case POST payload.
func TestCreateTestCaseTool_PreconditionsAndRequirementsReachHTTP(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		path   string
		body   string
	}
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, path: r.URL.Path, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolCreateTestCase()

	preconditions := "logged in as admin"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey:    "test-project",
		Name:          "TC",
		TestFolderID:  1,
		Preconditions: &preconditions,
		Requirements:  &[]string{"must do X", "must do Y"},
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	captured := <-reqCh
	require.Equal(t, http.MethodPost, captured.method)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in POST payload")

	precond, ok := manual["preconditions"].(map[string]any)
	require.True(t, ok, "preconditions should be present in manual scenario")
	require.Equal(t, "logged in as admin", precond["value"])

	reqs, ok := manual["requirements"].([]any)
	require.True(t, ok, "requirements should be present in manual scenario")
	require.Len(t, reqs, 2)
	first, ok := reqs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "must do X", first["value"])
	firstID, ok := first["id"].(string)
	require.True(t, ok, "requirement id should be generated as a string")
	require.Regexp(t, `^_[a-z0-9]{9}$`, firstID, "generated id should match the _xxxxxxxxx format")
	second, ok := reqs[1].(map[string]any)
	require.True(t, ok)
	secondID, ok := second["id"].(string)
	require.True(t, ok)
	require.NotEqual(t, firstID, secondID, "generated requirement ids should be unique")
}

// TestUpdateTestCaseTool_PreconditionsOnlySendsScenario verifies that preconditions
// can be set independently of instructions/expected-result on update_test_case.
func TestUpdateTestCaseTool_PreconditionsOnlySendsScenario(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		path   string
		body   string
	}
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, path: r.URL.Path, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	descriptionType := "text"
	preconditions := "service is running"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:    "test-project",
		TestCaseID:    7,
		TestCaseType:  &descriptionType,
		Preconditions: &preconditions,
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	captured := <-reqCh
	require.Equal(t, http.MethodPatch, captured.method)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in PATCH payload")
	require.Equal(t, "TEXT", manual["manualScenarioType"])
	precond, ok := manual["preconditions"].(map[string]any)
	require.True(t, ok, "preconditions should be present in manual scenario")
	require.Equal(t, "service is running", precond["value"])
}

// TestUpdateTestCaseTool_EmptyRequirementsClears verifies that providing an
// explicit empty requirements array sends "requirements": [] in the PATCH
// payload so the backend clears existing requirements.
func TestUpdateTestCaseTool_EmptyRequirementsClears(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		body   string
	}
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	descriptionType := "text"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:   "test-project",
		TestCaseID:   7,
		TestCaseType: &descriptionType,
		Requirements: &[]string{},
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	captured := <-reqCh
	require.Equal(t, http.MethodPatch, captured.method)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in PATCH payload")
	reqs, ok := manual["requirements"].([]any)
	require.True(t, ok, "requirements should be present (as an empty array) in manual scenario")
	require.Empty(t, reqs, "requirements should be an explicit empty array to clear them")
}

// TestTestCaseTools_RequirementsSchema verifies the requirements array schema on
// both create_test_case and update_test_case tools.
func TestTestCaseTools_RequirementsSchema(t *testing.T) {
	tr := newTMSResources(t)
	for name, toolFn := range map[string]func() (*mcp.Tool, ToolHandler[CreateTestCaseArgs, any]){
		"create_test_case": tr.toolCreateTestCase,
	} {
		tool, _ := toolFn()
		schema, ok := tool.InputSchema.(*jsonschema.Schema)
		require.True(t, ok, "%s InputSchema should be a *jsonschema.Schema", name)

		_, ok = schema.Properties["preconditions"]
		require.True(t, ok, "%s should expose preconditions property", name)

		reqProp, ok := schema.Properties["requirements"]
		require.True(t, ok, "%s should expose requirements property", name)
		require.Equal(t, "array", reqProp.Type)
		require.NotNil(t, reqProp.Items)
		require.Equal(t, "string", reqProp.Items.Type)
	}

	updTool, _ := tr.toolUpdateTestCase()
	updSchema, ok := updTool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "update_test_case InputSchema should be a *jsonschema.Schema")
	_, ok = updSchema.Properties["preconditions"]
	require.True(t, ok, "update_test_case should expose preconditions property")
	updReqProp, ok := updSchema.Properties["requirements"]
	require.True(t, ok, "update_test_case should expose requirements property")
	require.Equal(t, "array", updReqProp.Type)
	require.NotNil(t, updReqProp.Items)
	require.Equal(t, "string", updReqProp.Items.Type)
}

// TestTestCaseTools_TypeAndStepsSchema verifies the test-case-type enum and steps
// array schema on both create_test_case and update_test_case tools.
func TestTestCaseTools_TypeAndStepsSchema(t *testing.T) {
	tr := newTMSResources(t)
	createTool, _ := tr.toolCreateTestCase()
	updTool, _ := tr.toolUpdateTestCase()
	for name, tool := range map[string]*mcp.Tool{
		"create_test_case": createTool,
		"update_test_case": updTool,
	} {
		schema, ok := tool.InputSchema.(*jsonschema.Schema)
		require.True(t, ok, "%s InputSchema should be a *jsonschema.Schema", name)

		typeProp, ok := schema.Properties["test-case-type"]
		require.True(t, ok, "%s should expose test-case-type property", name)
		require.Equal(t, "string", typeProp.Type)
		require.ElementsMatch(t,
			[]any{"text", "steps"},
			typeProp.Enum,
			"%s test-case-type enum should match expected values", name,
		)

		stepsProp, ok := schema.Properties["steps"]
		require.True(t, ok, "%s should expose steps property", name)
		require.Equal(t, "array", stepsProp.Type)
		require.NotNil(t, stepsProp.Items)
		require.Equal(t, "object", stepsProp.Items.Type)
		require.ElementsMatch(t, []string{"instructions"}, stepsProp.Items.Required)
		_, ok = stepsProp.Items.Properties["instructions"]
		require.True(t, ok, "%s step item should expose instructions", name)
		_, ok = stepsProp.Items.Properties["expected-result"]
		require.True(t, ok, "%s step item should expose expected-result", name)
	}
}

// TestCreateTestCaseTool_StepsReachHTTP verifies that test-case-type "test case
// with steps" produces a STEPS manual scenario carrying the provided steps.
func TestCreateTestCaseTool_StepsReachHTTP(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		body   string
	}
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolCreateTestCase()

	tcType := "steps"
	expected := "page loads"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey:   "test-project",
		Name:         "TC",
		TestFolderID: 1,
		TestCaseType: &tcType,
		Steps: &[]utils.StepArg{
			{Instructions: "open the page", ExpectedResult: &expected},
			{Instructions: "click submit"},
		},
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	captured := <-reqCh
	require.Equal(t, http.MethodPost, captured.method)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in POST payload")
	require.Equal(t, "STEPS", manual["manualScenarioType"])
	steps, ok := manual["steps"].([]any)
	require.True(t, ok, "steps should be present in manual scenario")
	require.Len(t, steps, 2)
	first, ok := steps[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "open the page", first["instructions"])
	require.Equal(t, "page loads", first["expectedResult"])
}

// TestCreateTestCaseTool_StepsRequiredForStepsType verifies that selecting the
// steps type without supplying steps is rejected before any API call.
func TestCreateTestCaseTool_StepsRequiredForStepsType(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolCreateTestCase()

	tcType := "steps"
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey:   "test-project",
		Name:         "TC",
		TestFolderID: 1,
		TestCaseType: &tcType,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "steps must not be empty")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestCreateTestCaseTool_StepsRejectedForDescriptionType verifies that supplying
// steps with the default "text" type is rejected.
func TestCreateTestCaseTool_StepsRejectedForDescriptionType(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolCreateTestCase()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey:   "test-project",
		Name:         "TC",
		TestFolderID: 1,
		Steps:        &[]utils.StepArg{{Instructions: "do thing"}},
	})

	require.Error(t, err)
	require.Contains(
		t,
		err.Error(),
		`steps are only valid when test-case-type is "steps"`,
	)
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestUpdateTestCaseTool_StepsReachHTTP verifies the steps type works on update.
func TestUpdateTestCaseTool_StepsReachHTTP(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		body   string
	}
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	tcType := "steps"
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:   "test-project",
		TestCaseID:   7,
		TestCaseType: &tcType,
		Steps:        &[]utils.StepArg{{Instructions: "step one"}},
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	captured := <-reqCh
	require.Equal(t, http.MethodPatch, captured.method)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in PATCH payload")
	require.Equal(t, "STEPS", manual["manualScenarioType"])
	steps, ok := manual["steps"].([]any)
	require.True(t, ok)
	require.Len(t, steps, 1)
}

// TestUpdateTestCaseTool_StepsTypeWithoutStepsUpdatesRequirements verifies that
// when test-case-type is "steps" but no steps are supplied, the
// update is accepted and only the provided scenario fields (e.g. requirements)
// are included in the PATCH payload without a "steps" key.
func TestUpdateTestCaseTool_StepsTypeWithoutStepsUpdatesRequirements(t *testing.T) {
	ctx := context.Background()
	type httpReq struct {
		method string
		body   string
	}
	reqCh := make(chan httpReq, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		reqCh <- httpReq{method: r.Method, body: string(rawBody)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":374,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	tcType := "steps"
	reqs := []string{"https://www.google.com"}
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:   "test-project",
		TestCaseID:   374,
		TestCaseType: &tcType,
		Requirements: &reqs,
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	captured := <-reqCh
	require.Equal(t, http.MethodPatch, captured.method)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(captured.body), &payload))
	manual, ok := payload["manualScenario"].(map[string]any)
	require.True(t, ok, "manualScenario should be present in PATCH payload")
	require.Equal(t, "STEPS", manual["manualScenarioType"])
	_, hasSteps := manual["steps"]
	require.False(t, hasSteps, "steps key must be absent when no steps were provided")
	requirements, ok := manual["requirements"].([]any)
	require.True(t, ok, "requirements should be present in the scenario")
	require.Len(t, requirements, 1)
	first, ok := requirements[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://www.google.com", first["value"])
}

// TestUpdateTestCaseTool_EmptyStepsRejected verifies that passing an explicit
// empty steps slice (steps: []) on update is rejected with a validation error.
// Steps must contain at least one entry whether on create or update; omit the
// field entirely to leave existing steps unchanged.
func TestUpdateTestCaseTool_EmptyStepsRejected(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolUpdateTestCase()

	tcType := "steps"
	emptySteps := []utils.StepArg{}
	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:   "test-project",
		TestCaseID:   8,
		TestCaseType: &tcType,
		Steps:        &emptySteps,
	})

	require.Error(t, callErr)
	require.Nil(t, result)
	require.Contains(t, callErr.Error(), "steps must not be empty")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestUpdateTestCaseTool_InstructionsRejectedForStepsType verifies that
// instructions/expected-result are rejected when the steps type is selected.
func TestUpdateTestCaseTool_InstructionsRejectedForStepsType(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolUpdateTestCase()

	tcType := "steps"
	instructions := "do thing"
	_, _, err := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey:   "test-project",
		TestCaseID:   1,
		TestCaseType: &tcType,
		Instructions: &instructions,
		Steps:        &[]utils.StepArg{{Instructions: "step one"}},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), `not valid for "steps"`)
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestGetTestCasesByFilterTool_PriorityItemsSchema verifies that filter-in-priority
// is an array with the correct enum on its items sub-schema.
func TestGetTestCasesByFilterTool_PriorityItemsSchema(t *testing.T) {
	tool, _ := newTMSResources(t).toolGetTestCasesByFilter()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["filter-in-priority"]
	require.True(t, ok, "filter-in-priority property should exist")
	require.Equal(t, "array", prop.Type, "filter-in-priority should be an array type")
	require.NotNil(t, prop.Items, "filter-in-priority must have items sub-schema")
	require.Equal(t, "string", prop.Items.Type, "items should be of type string")
	require.ElementsMatch(t,
		[]any{"BLOCKER", "CRITICAL", "MEDIUM", "HIGH", "LOW", "UNSPECIFIED"},
		prop.Items.Enum,
		"items enum should contain all priority values",
	)
}

// TestGetTestCasesByFilterTool_AttributeKeySchema verifies that filter-has-attributeKey
// is a string property in the schema.
func TestGetTestCasesByFilterTool_AttributeKeySchema(t *testing.T) {
	tool, _ := newTMSResources(t).toolGetTestCasesByFilter()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["filter-has-attributeKey"]
	require.True(t, ok, "filter-has-attributeKey property should exist")
	require.Equal(t, "string", prop.Type)
}

// TestGetTestCasesByFilterTool_CntNameSchema verifies that filter-cnt-name
// is a string property in the schema (contains match, not exact).
func TestGetTestCasesByFilterTool_CntNameSchema(t *testing.T) {
	tool, _ := newTMSResources(t).toolGetTestCasesByFilter()

	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	prop, ok := schema.Properties["filter-cnt-name"]
	require.True(t, ok, "filter-cnt-name property should exist")
	require.Equal(t, "string", prop.Type)

	_, eqNameExists := schema.Properties["filter-eq-name"]
	require.False(
		t,
		eqNameExists,
		"filter-eq-name should no longer exist (replaced by filter-cnt-name)",
	)
}

// TestGetTestCasesByFilterTool_FiltersReachHTTP verifies that filter-has-attributeKey,
// filter-in-priority, and filter-cnt-name are forwarded as the correct query params.
func TestGetTestCasesByFilterTool_FiltersReachHTTP(t *testing.T) {
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
	_, handler := res.toolGetTestCasesByFilter()

	_, _, callErr := handler(ctx, &mcp.CallToolRequest{}, GetTestCasesByFilterArgs{
		ProjectKey:            "test-project",
		FilterHasAttributeKey: "smoke",
		FilterInPriority:      []string{"CRITICAL", "HIGH"},
		FilterCntName:         "login",
	})

	require.NoError(t, callErr)
	require.NotNil(t, capturedQuery, "HTTP request should reach the server")
	require.Equal(t, "smoke", capturedQuery.Get("filter.has.attributeKey"))
	require.Equal(t, "CRITICAL,HIGH", capturedQuery.Get("filter.in.priority"))
	require.Equal(t, "login", capturedQuery.Get("filter.cnt.name"))
	require.Empty(t, capturedQuery.Get("filter.eq.name"), "filter.eq.name should not be set")
}

// TestCreateTestCaseTool_DuplicateAttributeKeyRejected verifies that two attribute
// entries whose keys are identical after whitespace trimming are rejected before
// any HTTP call is made.
func TestCreateTestCaseTool_DuplicateAttributeKeyRejected(t *testing.T) {
	ctx := context.Background()
	res, requestCount := newTMSResourcesWithCounter(t)
	_, handler := res.toolCreateTestCase()

	_, _, err := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey:   "test-project",
		Name:         "TC",
		TestFolderID: 1,
		Attributes: []utils.AttributeArg{
			{Key: "env"},
			{Key: " env "},
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate key")
	require.Zero(t, requestCount.Load(), "no HTTP request should be made when validation fails")
}

// TestResolveTestCaseAttributes_ConflictOnCreateRetriesLookup verifies the TOCTOU
// race window where two concurrent callers both issue a GET (miss) then a POST for
// the same attribute (tag key). The losing caller receives 409 Conflict on POST.
// The implementation must then retry the GET, obtain the id created by the winner,
// and succeed rather than surfacing a spurious duplicate-attribute error.
func TestResolveTestCaseAttributes_ConflictOnCreateRetriesLookup(t *testing.T) {
	ctx := context.Background()

	var callCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1:
			// Initial GET – attribute not found yet (both concurrent callers see this).
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"content":[],"page":{"totalElements":0}}`))
		case 2:
			// POST – race loser: the attribute was already created by the concurrent winner.
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"attribute already exists"}`))
		case 3:
			// Retry GET – attribute now exists with id 42.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(
				[]byte(
					`{"content":[{"id":42,"key":"env","value":"staging"}],"page":{"totalElements":1}}`,
				),
			)
		case 4:
			// Final POST to create the test case itself.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":99,"name":"TC"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolCreateTestCase()

	result, _, callErr := handler(ctx, &mcp.CallToolRequest{}, CreateTestCaseArgs{
		ProjectKey:   "test-project",
		Name:         "TC",
		TestFolderID: 1,
		Attributes:   []utils.AttributeArg{{Key: "env"}},
	})

	require.NoError(t, callErr)
	require.NotNil(t, result)
	require.False(t, result.IsError, "expected success after 409 retry, got: %v", result)
	require.Equal(t, int64(4), callCount.Load(),
		"expected exactly 4 HTTP calls: GET (miss), POST (409), retry GET (hit), POST test-case")
}

// TestUpdateTestCaseTool_OmittedAttributesLeavesFieldAbsent verifies that when
// Attributes is nil (field omitted) the PATCH payload does not contain an
// "attributes" key, so existing server-side attributes are left unchanged.
func TestUpdateTestCaseTool_OmittedAttributesLeavesFieldAbsent(t *testing.T) {
	ctx := context.Background()

	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		capturedBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	name := "TC"
	_, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey: "test-project",
		TestCaseID: 7,
		Name:       &name,
		// Attributes intentionally omitted (nil pointer)
	})

	require.NoError(t, callErr)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(capturedBody), &payload))
	_, present := payload["attributes"]
	require.False(t, present, "omitted Attributes must not appear in PATCH payload")
}

// TestUpdateTestCaseTool_EmptyAttributesClears verifies that passing an explicit
// empty Attributes slice sends "attributes":[] in the PATCH body so the server
// clears all existing attributes on the test case.
func TestUpdateTestCaseTool_EmptyAttributesClears(t *testing.T) {
	ctx := context.Background()

	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		capturedBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":7,"name":"TC"}`))
	}))
	t.Cleanup(srv.Close)

	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	res := NewTMSResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	)
	_, handler := res.toolUpdateTestCase()

	name := "TC"
	emptyAttrs := []utils.AttributeArg{}
	_, _, callErr := handler(ctx, &mcp.CallToolRequest{}, UpdateTestCaseArgs{
		ProjectKey: "test-project",
		TestCaseID: 7,
		Name:       &name,
		Attributes: &emptyAttrs,
	})

	require.NoError(t, callErr)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(capturedBody), &payload))
	attrs, present := payload["attributes"]
	require.True(t, present, "explicit empty Attributes must appear in PATCH payload to clear them")
	attrSlice, ok := attrs.([]any)
	require.True(t, ok, "attributes value should be a JSON array")
	require.Empty(t, attrSlice, "attributes array should be empty to clear existing attributes")
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
