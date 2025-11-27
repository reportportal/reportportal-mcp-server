package mcp_handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
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

func TestListLaunchesTool(t *testing.T) {
	ctx := context.Background()
	testProject := "test-project"
	launches, _ := json.Marshal(testLaunches())
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch", testProject), r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(launches)
	}))
	defer mockServer.Close()

	srv := mcptest.NewUnstartedServer(t)

	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(gorp.NewClient(serverURL, ""), nil, "")
	srv.AddTool(launchTools.toolGetLaunches())

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Close()

	client := srv.Client()

	var req mcp.CallToolRequest
	req.Params.Name = "get_launches"
	req.Params.Arguments = map[string]any{
		"project": testProject,
	}

	result, err := client.CallTool(ctx, req)
	require.NoError(t, err)

	var textContent mcp.TextContent
	require.IsType(t, textContent, result.Content[0])
	text := result.Content[0].(mcp.TextContent).Text

	assert.Equal(t, string(launches), text)
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
	var capturedRequest *openapi.AnalyzeLaunchRQ
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch/analyze", testProject), r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		// Parse request body
		var reqBody openapi.AnalyzeLaunchRQ
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

	srv := mcptest.NewUnstartedServer(t)

	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(gorp.NewClient(serverURL, ""), nil, "")
	tool, handler := launchTools.toolRunAutoAnalysis()
	srv.AddTool(tool, handler)

	// Verify the tool schema includes items property for array parameter
	// This is critical for GitHub Copilot compatibility
	toolSchema := tool.InputSchema
	require.NotNil(t, toolSchema)
	require.NotNil(t, toolSchema.Properties)

	analyzerItemModesProp, exists := toolSchema.Properties["analyzer_item_modes"]
	require.True(t, exists, "analyzer_item_modes parameter should exist in schema")

	// Verify it's an array type with items property (critical for GitHub Copilot compatibility)
	// Properties are stored as map[string]any, so we need to check the JSON schema structure
	propMap, ok := analyzerItemModesProp.(map[string]interface{})
	require.True(t, ok, "analyzer_item_modes property should be a map")
	require.Equal(t, "array", propMap["type"], "analyzer_item_modes should be an array type")
	require.NotNil(
		t,
		propMap["items"],
		"analyzer_item_modes must have items property for GitHub Copilot compatibility",
	)

	// Verify items have enum values
	itemsMap, ok := propMap["items"].(map[string]interface{})
	require.True(t, ok, "items should be a map")
	require.NotNil(t, itemsMap["enum"], "items should have enum values")

	// Enum can be stored as []interface{} or []string, handle both cases
	enumValue := itemsMap["enum"]
	var enumValues []interface{}
	switch v := enumValue.(type) {
	case []interface{}:
		enumValues = v
	case []string:
		enumValues = make([]interface{}, len(v))
		for i, s := range v {
			enumValues[i] = s
		}
	default:
		require.Fail(t, "enum should be an array", "got type: %T", enumValue)
	}

	expectedEnumValues := []string{"to_investigate", "auto_analyzed", "manually_analyzed"}
	actualEnumValues := make([]string, len(enumValues))
	for i, v := range enumValues {
		actualEnumValues[i] = v.(string)
	}
	assert.Equal(
		t,
		expectedEnumValues,
		actualEnumValues,
		"enum values should match expected values",
	)

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Close()

	client := srv.Client()

	// Test with valid enum values
	var req mcp.CallToolRequest
	req.Params.Name = "run_auto_analysis"
	req.Params.Arguments = map[string]any{
		"project":             testProject,
		"launch_id":           launchID,
		"analyzer_mode":       "current_launch",
		"analyzer_type":       "autoAnalyzer",
		"analyzer_item_modes": []string{"to_investigate", "auto_analyzed"},
	}

	result, err := client.CallTool(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)

	var textContent mcp.TextContent
	require.IsType(t, textContent, result.Content[0])
	text := result.Content[0].(mcp.TextContent).Text
	assert.Equal(t, expectedMessage, text)

	// Verify the API was called with correct parameters
	require.NotNil(t, capturedRequest)
	assert.Equal(t, int64(launchID), capturedRequest.LaunchId)
	assert.Equal(t, "CURRENT_LAUNCH", capturedRequest.AnalyzerMode)
	assert.Equal(t, "AUTOANALYZER", capturedRequest.AnalyzerTypeName)
	assert.Equal(t, []string{"to_investigate", "auto_analyzed"}, capturedRequest.AnalyzeItemsMode)
}

func testLaunches() *openapi.PageLaunchResource {
	launches := openapi.NewPageLaunchResource()
	launches.SetContent([]openapi.LaunchResource{
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
	launches.SetPage(openapi.PageMetadata{
		TotalPages:    openapi.PtrInt64(1),
		HasNext:       openapi.PtrBool(false),
		Number:        openapi.PtrInt64(1),
		Size:          openapi.PtrInt64(int64(len(launches.Content))),
		TotalElements: openapi.PtrInt64(int64(len(launches.Content))),
	})

	return launches
}
