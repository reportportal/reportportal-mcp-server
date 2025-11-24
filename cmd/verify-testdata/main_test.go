package main

import (
	"strings"
	"testing"
)

func TestNormalizeMCPURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "base URL without path",
			input:    "http://localhost:8080",
			expected: "http://localhost:8080/mcp",
		},
		{
			name:     "base URL with trailing slash",
			input:    "http://localhost:8080/",
			expected: "http://localhost:8080/mcp",
		},
		{
			name:     "URL already has /mcp",
			input:    "http://localhost:8080/mcp",
			expected: "http://localhost:8080/mcp",
		},
		{
			name:     "URL has /mcp with trailing slash",
			input:    "http://localhost:8080/mcp/",
			expected: "http://localhost:8080/mcp",
		},
		{
			name:     "URL has /api/mcp",
			input:    "http://localhost:8080/api/mcp",
			expected: "http://localhost:8080/api/mcp",
		},
		{
			name:     "URL has /api/mcp with trailing slash",
			input:    "http://localhost:8080/api/mcp/",
			expected: "http://localhost:8080/api/mcp",
		},
		{
			name:     "URL with different path",
			input:    "http://localhost:8080/api",
			expected: "http://localhost:8080/api/mcp",
		},
		{
			name:     "URL with different path and trailing slash",
			input:    "http://localhost:8080/api/",
			expected: "http://localhost:8080/api/mcp",
		},
		{
			name:     "HTTPS URL",
			input:    "https://example.com:9090",
			expected: "https://example.com:9090/mcp",
		},
		{
			name:     "complex URL with path",
			input:    "https://example.com/reportportal",
			expected: "https://example.com/reportportal/mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMCPURL(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeMCPURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestValidateJSONRPCRequest(t *testing.T) {
	tests := []struct {
		name          string
		rawBody       string
		expectError   bool
		errorContains string
	}{
		{
			name: "valid JSON-RPC 2.0 request",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "tools/call",
				"id": 1,
				"params": {"name": "test"}
			}`,
			expectError: false,
		},
		{
			name: "valid with string id",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "initialize",
				"id": "req-123",
				"params": {}
			}`,
			expectError: false,
		},
		{
			name: "valid with null id",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "tools/list",
				"id": null,
				"params": {}
			}`,
			expectError: false,
		},
		{
			name: "valid with null params",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "tools/call",
				"id": 1,
				"params": null
			}`,
			expectError: false,
		},
		{
			name:          "invalid - not JSON",
			rawBody:       `not json at all`,
			expectError:   true,
			errorContains: "request body is not valid JSON",
		},
		{
			name: "invalid - missing jsonrpc field",
			rawBody: `{
				"method": "tools/call",
				"id": 1
			}`,
			expectError:   true,
			errorContains: "invalid or missing jsonrpc field",
		},
		{
			name: "invalid - wrong jsonrpc version",
			rawBody: `{
				"jsonrpc": "1.0",
				"method": "tools/call",
				"id": 1
			}`,
			expectError:   true,
			errorContains: "invalid or missing jsonrpc field: expected \"2.0\", got \"1.0\"",
		},
		{
			name: "invalid - missing method",
			rawBody: `{
				"jsonrpc": "2.0",
				"id": 1
			}`,
			expectError:   true,
			errorContains: "missing required field: method",
		},
		{
			name: "invalid - empty method",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "",
				"id": 1,
				"params": {}
			}`,
			expectError:   true,
			errorContains: "missing required field: method",
		},
		{
			name: "invalid - missing id",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "tools/call",
				"params": {}
			}`,
			expectError:   true,
			errorContains: "missing required field: id",
		},
		{
			name: "invalid - missing params",
			rawBody: `{
				"jsonrpc": "2.0",
				"method": "tools/call",
				"id": 1
			}`,
			expectError:   true,
			errorContains: "missing required field: params",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJSONRPCRequest(tt.rawBody)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
					return
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf(
						"expected error to contain %q but got %q",
						tt.errorContains,
						err.Error(),
					)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
