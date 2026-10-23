package mcphandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/stretchr/testify/require"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
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
				"projectKey": "test_project",
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
				"projectKey": "test_project"
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
			result, err := utils.GetDefectTypesFromJSON(tt.rawBody)

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

	result, err := utils.GetDefectTypesFromJSON(rawBody)
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
}

// TestGetTestItemAttachmentTool tests that the get_test_item_attachment_by_id tool handler
// returns image, text and other binary attachments in the expected content form.
func TestGetTestItemAttachmentTool(t *testing.T) {
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	zipBytes := []byte{'P', 'K', 0x03, 0x04}

	tests := []struct {
		name        string
		contentType string
		body        []byte
		check       func(t *testing.T, content mcp.Content)
	}{
		{
			name:        "image is returned as image content",
			contentType: "image/jpeg",
			body:        jpegBytes,
			check: func(t *testing.T, content mcp.Content) {
				image, ok := content.(*mcp.ImageContent)
				require.True(t, ok, "expected *mcp.ImageContent, got %T", content)
				require.Equal(t, "image/jpeg", image.MIMEType)
				require.Equal(t, jpegBytes, image.Data)
			},
		},
		{
			name:        "text is returned as text content",
			contentType: "text/plain",
			body:        []byte("hello"),
			check: func(t *testing.T, content mcp.Content) {
				text, ok := content.(*mcp.TextContent)
				require.True(t, ok, "expected *mcp.TextContent, got %T", content)
				require.Equal(t, "Text content (text/plain, 5 bytes)\nhello", text.Text)
			},
		},
		{
			name:        "other binary is returned as base64 text",
			contentType: "application/zip",
			body:        zipBytes,
			check: func(t *testing.T, content mcp.Content) {
				text, ok := content.(*mcp.TextContent)
				require.True(t, ok, "expected *mcp.TextContent, got %T", content)
				require.Equal(
					t,
					"Binary content (application/zip, 4 bytes)\nBase64: UEsDBA==",
					text.Text,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", tt.contentType)
					_, _ = w.Write(tt.body)
				}),
			)
			defer server.Close()

			serverURL, _ := url.Parse(server.URL)
			_, handler := NewTestItemResources(
				gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "")),
				nil,
				"",
			).toolGetTestItemAttachment()

			result, _, err := handler(
				context.Background(),
				&mcp.CallToolRequest{},
				GetTestItemAttachmentArgs{ProjectKey: "test-project", AttachmentContentID: "42"},
			)
			require.NoError(t, err)
			require.Len(t, result.Content, 1)
			tt.check(t, result.Content[0])
		})
	}
}
