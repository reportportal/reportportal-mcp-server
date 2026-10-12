package mcphandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDefectTypesFromJson(t *testing.T) {
	tests := []struct {
		name        string
		rawBody     []byte
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid project JSON with defect types",
			rawBody: []byte(`{
				"projectId": 123,
				"projectName": "test_project",
				"entryType": "INTERNAL",
				"configuration": {
					"subTypes": {
						"NO_DEFECT": {
							"locator": "nd001",
							"typeRef": "NO_DEFECT",
							"longName": "No Defect",
							"shortName": "ND",
							"color": "#777777"
						},
						"TO_INVESTIGATE": {
							"locator": "ti001",
							"typeRef": "TO_INVESTIGATE",
							"longName": "To Investigate",
							"shortName": "TI",
							"color": "#ffb743"
						}
					},
					"entryType": "INTERNAL"
				}
			}`),
			expectError: false,
		},
		{
			name:        "invalid JSON",
			rawBody:     []byte(`{invalid json`),
			expectError: true,
			errorMsg:    "failed to parse response JSON",
		},
		{
			name: "missing configuration field",
			rawBody: []byte(`{
				"projectId": 1,
				"projectName": "test_project"
			}`),
			expectError: true,
			errorMsg:    "configuration field not found or invalid in response",
		},
		{
			name: "missing subTypes field",
			rawBody: []byte(`{
				"configuration": {
					"entryType": "INTERNAL"
				},
				"projectId": 1
			}`),
			expectError: true,
			errorMsg:    "configuration/subTypes field not found in response",
		},
		{
			name: "configuration is not an object",
			rawBody: []byte(`{
				"configuration": "invalid",
				"projectId": 1
			}`),
			expectError: true,
			errorMsg:    "configuration field not found or invalid in response",
		},
		{
			name: "empty subTypes",
			rawBody: []byte(`{
				"configuration": {
					"subTypes": {}
				}
			}`),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getDefectTypesFromJson(tt.rawBody)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorMsg != "" && err.Error()[:len(tt.errorMsg)] != tt.errorMsg {
					t.Errorf(
						"expected error message to start with %q, got %q",
						tt.errorMsg,
						err.Error(),
					)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if result == "" {
					t.Errorf("expected non-empty result")
				}
				// Verify it's valid JSON
				if result[0] != '{' && result[0] != '[' {
					t.Errorf("result should be valid JSON, got: %s", result)
				}
			}
		})
	}
}

func TestGetDefectTypesFromJson_VerifyContent(t *testing.T) {
	rawBody := []byte(`{
		"configuration": {
			"subTypes": {
				"NO_DEFECT": {
					"locator": "nd001"
				}
			}
		}
	}`)

	result, err := getDefectTypesFromJson(rawBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The result should contain the defect type data
	if result == "" {
		t.Fatal("result should not be empty")
	}

	// Verify the result contains expected keys
	expectedStrings := []string{"NO_DEFECT", "locator", "nd001"}
	for _, expected := range expectedStrings {
		if !contains(result, expected) {
			t.Errorf("result should contain %q, got: %s", expected, result)
		}
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// TestUpdateDefectTypeForTestItemsTool verifies the test_items_ids array parameter
// has the "items" property required for VS Code / GitHub Copilot compatibility.
// See: https://github.com/reportportal/reportportal-mcp-server/issues/66
func TestUpdateDefectTypeForTestItemsTool(t *testing.T) {
	serverURL, _ := url.Parse("http://localhost:8080")
	tool, _ := NewTestItemResources(
		gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
		nil,
		"",
	).toolUpdateDefectTypeForTestItems()

	// Verify test_items_ids is an array with items property (critical for VS Code compatibility)
	schema, ok := tool.InputSchema.(*jsonschema.Schema)
	require.True(t, ok, "InputSchema should be a *jsonschema.Schema")

	testItemsIDsProp, ok := schema.Properties["test_items_ids"]
	require.True(t, ok, "test_items_ids property should exist")
	require.Equal(t, "array", testItemsIDsProp.Type, "test_items_ids should be an array type")
	require.NotNil(t, testItemsIDsProp.Items, "test_items_ids must have items property (issue #66)")
	require.Equal(t, "string", testItemsIDsProp.Items.Type, "items should be of type string")

	ignoreAnalyzerProp, ok := schema.Properties["ignore_analyzer"]
	require.True(t, ok, "ignore_analyzer property should exist")
	require.Equal(t, "boolean", ignoreAnalyzerProp.Type)
	require.NotContains(t, schema.Required, "ignore_analyzer")
}

func TestUpdateDefectTypeForTestItemsTool_IgnoreAnalyzer(t *testing.T) {
	tests := []struct {
		name           string
		ignoreAnalyzer *bool
		expected       any
	}{
		{name: "true", ignoreAnalyzer: openapi.PtrBool(true), expected: true},
		{name: "false", ignoreAnalyzer: openapi.PtrBool(false), expected: false},
		{name: "omitted", ignoreAnalyzer: nil, expected: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			testProject := "test-project"

			var issue map[string]any
			mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/"+testProject+"/item", r.URL.Path)
				assert.Equal(t, http.MethodPut, r.Method)

				var body struct {
					Issues []struct {
						Issue map[string]any `json:"issue"`
					} `json:"issues"`
				}
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				if assert.Len(t, body.Issues, 1) {
					issue = body.Issues[0].Issue
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}))
			defer mockServer.Close()

			serverURL, _ := url.Parse(mockServer.URL)
			_, handler := NewTestItemResources(
				gorp.NewClient(serverURL, gorp.WithApiKeyAuth(ctx, "")),
				nil,
				"",
			).toolUpdateDefectTypeForTestItems()

			_, _, err := handler(ctx, &mcp.CallToolRequest{}, UpdateDefectTypeArgs{
				Project:        testProject,
				TestItemsIDs:   []string{"1474305"},
				DefectTypeID:   "si001",
				IgnoreAnalyzer: tt.ignoreAnalyzer,
			})
			require.NoError(t, err)
			require.NotNil(t, issue)

			value, present := issue["ignoreAnalyzer"]
			if tt.expected == nil {
				assert.False(t, present, "ignoreAnalyzer must be omitted when not set")
			} else {
				assert.Equal(t, tt.expected, value)
			}
		})
	}
}
