package mcphandlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yosida95/uritemplate/v3"
)

func TestLaunchByIdTemplate(t *testing.T) {
	uritmpl := uritemplate.MustNew(
		"reportportal://launch/{launch}{?filter,page,size,tab}")
	vals := uritmpl.Match("reportportal://launch/123?filter=xxx")
	require.Equal(t, vals.Get("filter").String(), "xxx")
	require.Equal(t, vals.Get("launch").String(), "123")
}

// TestListLaunchesTool tests the get_launches tool handler directly
func TestListLaunchesTool(t *testing.T) {
	ctx := context.Background()
	testProject := "test-project"
	expectedLaunches := testLaunches()
	launchesJSON, _ := json.Marshal(expectedLaunches)

	// Mock ReportPortal API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch", testProject), r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(launchesJSON)
	}))
	defer mockServer.Close()

	// Create launch resources with mocked RP client
	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(ctx, "")),
		nil,
		"",
		nil,
	)

	// Get the tool and handler
	_, handler := launchTools.toolGetLaunches()

	// Call the handler directly
	result, _, err := handler(ctx, &mcp.CallToolRequest{}, GetLaunchesArgs{Project: testProject})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	// Verify the response
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	assert.Equal(t, string(launchesJSON), textContent.Text)
}

// TestGetLaunchByIdTool tests the get_launch_by_id tool handler directly
func TestGetLaunchByIdTool(t *testing.T) {
	ctx := context.Background()
	testProject := "test-project"
	launchID := uint32(123)

	// Create expected launch response
	expectedLaunch := openapi.ComEpamReportportalBaseReportingLaunchResource{
		Id:        int64(launchID),
		Name:      "Test Launch by ID",
		Uuid:      "014b329b-a882-4c2d-9988-c2f6179a421b",
		Number:    int64(launchID),
		StartTime: time.Now(),
		Status:    string(gorp.Statuses.Passed),
	}

	launchJSON, _ := json.Marshal(expectedLaunch)

	// Mock ReportPortal API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetLaunch uses /api/v1/{projectKey}/launch/{launchId}
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch/%d", testProject, launchID), r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(launchJSON)
	}))
	defer mockServer.Close()

	// Create launch resources with mocked RP client
	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(ctx, "")),
		nil,
		"",
		nil,
	)

	// Get the tool and handler
	_, handler := launchTools.toolGetLaunchById()

	// Call the handler directly
	result, _, err := handler(
		ctx,
		&mcp.CallToolRequest{},
		LaunchIDArgs{Project: testProject, LaunchID: launchID},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	// Verify the response
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")

	// Verify the response contains the expected launch
	var responseLaunch openapi.ComEpamReportportalBaseReportingLaunchResource
	err = json.Unmarshal([]byte(textContent.Text), &responseLaunch)
	require.NoError(t, err)
	assert.Equal(t, expectedLaunch.Id, responseLaunch.Id)
	assert.Equal(t, expectedLaunch.Name, responseLaunch.Name)
	assert.Equal(t, expectedLaunch.Number, responseLaunch.Number)
}

// TestGetLaunchByIdTool_NotFound tests error handling when a launch is not found
func TestGetLaunchByIdTool_NotFound(t *testing.T) {
	ctx := context.Background()
	testProject := "test-project"
	launchID := uint32(999)

	// Mock ReportPortal API server returning 404
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetLaunch uses /api/v1/{projectKey}/launch/{launchId}
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch/%d", testProject, launchID), r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)

		// Return 404 for launch not found
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(
			[]byte(
				`{"errorCode": 40004, "message": "Launch '999' not found. Did you use correct Launch ID?"}`,
			),
		)
	}))
	defer mockServer.Close()

	// Create launch resources with mocked RP client
	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(ctx, "")),
		nil,
		"",
		nil,
	)

	// Get the tool and handler
	_, handler := launchTools.toolGetLaunchById()

	// Call the handler directly - should return an error
	_, _, err := handler(
		ctx,
		&mcp.CallToolRequest{},
		LaunchIDArgs{Project: testProject, LaunchID: launchID},
	)

	// Verify that an error is returned
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestRunAutoAnalysisTool tests the run_auto_analysis tool to ensure:
//  1. The tool schema correctly includes the "items" property for array parameters
//     (critical for GitHub Copilot compatibility - fixes "array type must have items" error)
//  2. The enum values for analyzer_item_modes are correctly defined
//  3. The tool handler correctly processes requests and calls the ReportPortal API
func TestRunAutoAnalysisTool(t *testing.T) {
	ctx := context.Background()
	testProject := "test-project"
	launchID := 123
	expectedMessage := "Auto analysis started successfully"

	// Track the request payload to verify correct parameters
	var capturedRequest *openapi.ComEpamReportportalBaseModelLaunchAnalyzeLaunchRQ
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch/analyze", testProject), r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		// Parse request body
		var reqBody openapi.ComEpamReportportalBaseModelLaunchAnalyzeLaunchRQ
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		capturedRequest = &reqBody

		// Return success response - using map to match actual API response structure
		response := map[string]interface{}{
			"message": expectedMessage,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	// Create launch resources with mocked RP client
	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(ctx, "")),
		nil,
		"",
		nil,
	)

	// Get the tool and handler
	tool, handler := launchTools.toolRunAutoAnalysis()

	// Verify the tool schema includes items property for array parameter
	// This is critical for GitHub Copilot compatibility
	inputSchema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")
	require.NotNil(t, inputSchema)
	require.NotNil(t, inputSchema.Properties)

	analyzerItemModesProp, exists := inputSchema.Properties["analyzer_item_modes"]
	require.True(t, exists, "analyzer_item_modes parameter should exist in schema")

	// Verify it's an array type with items property
	require.Equal(
		t,
		"array",
		analyzerItemModesProp.Type,
		"analyzer_item_modes should be an array type",
	)
	require.NotNil(
		t,
		analyzerItemModesProp.Items,
		"analyzer_item_modes must have items property for GitHub Copilot compatibility",
	)

	// Verify items have enum values
	require.NotNil(t, analyzerItemModesProp.Items.Enum, "items should have enum values")
	expectedEnumValues := []any{"to_investigate", "auto_analyzed", "manually_analyzed"}
	assert.Equal(
		t,
		expectedEnumValues,
		analyzerItemModesProp.Items.Enum,
		"enum values should match expected values",
	)

	// Call the handler directly
	result, _, err := handler(ctx, &mcp.CallToolRequest{}, RunAutoAnalysisArgs{
		Project:           testProject,
		LaunchID:          uint32(launchID),
		AnalyzerMode:      "current_launch",
		AnalyzerType:      "autoAnalyzer",
		AnalyzerItemModes: []string{"to_investigate", "auto_analyzed"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	// Verify the response
	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent")
	assert.Equal(t, expectedMessage, textContent.Text)

	// Verify the API was called with correct parameters
	require.NotNil(t, capturedRequest)
	assert.Equal(t, int64(launchID), capturedRequest.LaunchId)
	assert.Equal(t, "CURRENT_LAUNCH", capturedRequest.AnalyzerMode)
	assert.Equal(t, "AUTOANALYZER", capturedRequest.AnalyzerTypeName)
	assert.Equal(t, []string{"to_investigate", "auto_analyzed"}, capturedRequest.AnalyzeItemsMode)
}

func testLaunches() *openapi.ComEpamReportportalBaseModelPageComEpamReportportalBaseReportingLaunchResource {
	launches := openapi.NewComEpamReportportalBaseModelPageComEpamReportportalBaseReportingLaunchResource()
	launches.SetContent([]openapi.ComEpamReportportalBaseReportingLaunchResource{
		{
			Id:        1,
			Name:      "Test Launch 1",
			Uuid:      "014b329b-a882-4c2d-9988-c2f6179a421b",
			Number:    1,
			StartTime: time.Now(),
			Status:    string(gorp.Statuses.Passed),
		},
		{
			Id:        2,
			Name:      "Test Launch 2",
			Uuid:      "014b329b-a882-4c2d-9988-c2f6179a421c",
			Number:    2,
			StartTime: time.Now(),
			Status:    string(gorp.Statuses.Passed),
		},
	})
	launches.SetPage(openapi.ComEpamReportportalBaseModelPagePageMetadata{
		TotalPages:    openapi.PtrInt64(1),
		HasNext:       openapi.PtrBool(false),
		Number:        openapi.PtrInt64(1),
		Size:          openapi.PtrInt64(int64(len(launches.Content))),
		TotalElements: openapi.PtrInt64(int64(len(launches.Content))),
	})

	return launches
}
