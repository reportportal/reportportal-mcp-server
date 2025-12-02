package mcpreportportal

import (
	"net/url"
	"testing"

	"github.com/reportportal/goRP/v5/pkg/gorp"
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
		gorp.NewClient(serverURL, ""),
		nil,
		"",
	).toolUpdateDefectTypeForTestItems()

	// Verify test_items_ids is an array with items property (critical for VS Code compatibility)
	propMap, ok := tool.InputSchema.Properties["test_items_ids"].(map[string]interface{})
	require.True(t, ok, "test_items_ids property should exist and be a map")
	require.Equal(t, "array", propMap["type"], "test_items_ids should be an array type")

	itemsMap, ok := propMap["items"].(map[string]interface{})
	require.True(t, ok, "test_items_ids must have items property (issue #66)")
	require.Equal(t, "string", itemsMap["type"], "items should be of type string")
}
