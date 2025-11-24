package testdata

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostmanRequestBody_IsValidMode(t *testing.T) {
	tests := []struct {
		name     string
		body     *PostmanRequestBody
		expected bool
	}{
		{
			name:     "nil body",
			body:     nil,
			expected: true,
		},
		{
			name:     "empty mode",
			body:     &PostmanRequestBody{Mode: ""},
			expected: true,
		},
		{
			name:     "valid raw mode",
			body:     &PostmanRequestBody{Mode: BodyModeRaw},
			expected: true,
		},
		{
			name:     "valid urlencoded mode",
			body:     &PostmanRequestBody{Mode: BodyModeURLEncoded},
			expected: true,
		},
		{
			name:     "valid formdata mode",
			body:     &PostmanRequestBody{Mode: BodyModeFormData},
			expected: true,
		},
		{
			name:     "valid graphql mode",
			body:     &PostmanRequestBody{Mode: BodyModeGraphQL},
			expected: true,
		},
		{
			name:     "invalid mode",
			body:     &PostmanRequestBody{Mode: "invalid"},
			expected: false,
		},
		{
			name:     "invalid mode with typo",
			body:     &PostmanRequestBody{Mode: "graphQL"},
			expected: false,
		},
		{
			name:     "file mode is not supported (no File field in struct)",
			body:     &PostmanRequestBody{Mode: "file"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.body.IsValidMode()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPostmanRequestBody_ValidateMode(t *testing.T) {
	tests := []struct {
		name        string
		body        *PostmanRequestBody
		expectError bool
	}{
		{
			name:        "valid mode",
			body:        &PostmanRequestBody{Mode: BodyModeRaw},
			expectError: false,
		},
		{
			name:        "empty mode",
			body:        &PostmanRequestBody{Mode: ""},
			expectError: false,
		},
		{
			name:        "invalid mode",
			body:        &PostmanRequestBody{Mode: "invalid"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.body.ValidateMode()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid body mode")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseTestCase_ValidatesBodyMode(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid test case",
			json: `{
				"name": "test",
				"reportPortalMock": {
					"requestResponsePairs": []
				},
				"llmClientMock": {
					"request": {
						"method": "POST",
						"url": {"raw": "http://example.com"}
					},
					"expectedResponse": {
						"code": 200
					}
				}
			}`,
			expectError: false,
		},
		{
			name: "valid test case with raw body mode",
			json: `{
				"name": "test",
				"reportPortalMock": {
					"requestResponsePairs": []
				},
				"llmClientMock": {
					"request": {
						"method": "POST",
						"url": {"raw": "http://example.com"},
						"body": {"mode": "raw", "raw": "test"}
					},
					"expectedResponse": {
						"code": 200
					}
				}
			}`,
			expectError: false,
		},
		{
			name: "invalid body mode in LLM client request",
			json: `{
				"name": "test",
				"reportPortalMock": {
					"requestResponsePairs": []
				},
				"llmClientMock": {
					"request": {
						"method": "POST",
						"url": {"raw": "http://example.com"},
						"body": {"mode": "invalid"}
					},
					"expectedResponse": {
						"code": 200
					}
				}
			}`,
			expectError: true,
			errorMsg:    "invalid LLM client request",
		},
		{
			name: "invalid body mode in ReportPortal mock request",
			json: `{
				"name": "test",
				"reportPortalMock": {
					"requestResponsePairs": [
						{
							"request": {
								"method": "GET",
								"url": {"raw": "http://example.com"},
								"body": {"mode": "badmode"}
							},
							"response": {
								"code": 200
							}
						}
					]
				},
				"llmClientMock": {
					"request": {
						"method": "POST",
						"url": {"raw": "http://example.com"}
					},
					"expectedResponse": {
						"code": 200
					}
				}
			}`,
			expectError: true,
			errorMsg:    "invalid ReportPortal mock request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseTestCase([]byte(tt.json))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParsePostmanCollection_ValidatesBodyMode(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid collection",
			json: `{
				"info": {
					"name": "Test Collection",
					"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
				},
				"item": [
					{
						"name": "Test Request",
						"request": {
							"method": "POST",
							"url": {"raw": "http://example.com"},
							"body": {"mode": "raw", "raw": "test"}
						},
						"response": []
					}
				]
			}`,
			expectError: false,
		},
		{
			name: "invalid body mode in item",
			json: `{
				"info": {
					"name": "Test Collection",
					"schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
				},
				"item": [
					{
						"name": "Test Request",
						"request": {
							"method": "POST",
							"url": {"raw": "http://example.com"},
							"body": {"mode": "invalidmode"}
						},
						"response": []
					}
				]
			}`,
			expectError: true,
			errorMsg:    "invalid request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePostmanCollection([]byte(tt.json))
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBodyModeConstants(t *testing.T) {
	// Verify constants match expected values
	expectedModes := map[string]string{
		"BodyModeRaw":        "raw",
		"BodyModeURLEncoded": "urlencoded",
		"BodyModeFormData":   "formdata",
		"BodyModeGraphQL":    "graphql",
	}

	actualModes := map[string]string{
		"BodyModeRaw":        BodyModeRaw,
		"BodyModeURLEncoded": BodyModeURLEncoded,
		"BodyModeFormData":   BodyModeFormData,
		"BodyModeGraphQL":    BodyModeGraphQL,
	}

	for name, expected := range expectedModes {
		actual := actualModes[name]
		assert.Equal(t, expected, actual, "Constant %s should match", name)
	}
}

// TestBodyModeInJSON verifies that body modes can be marshaled/unmarshaled correctly
func TestBodyModeInJSON(t *testing.T) {
	body := &PostmanRequestBody{
		Mode: BodyModeRaw,
		Raw:  "test data",
	}

	// Marshal to JSON
	data, err := json.Marshal(body)
	require.NoError(t, err, "Failed to marshal")

	// Unmarshal back
	var decoded PostmanRequestBody
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err, "Failed to unmarshal")

	// Verify
	assert.Equal(t, BodyModeRaw, decoded.Mode)
	assert.Equal(t, "test data", decoded.Raw)

	// Verify mode is valid
	assert.True(t, decoded.IsValidMode(), "Mode should be valid after unmarshal")
}
