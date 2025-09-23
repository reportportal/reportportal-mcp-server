package http

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Test helper to create HTTP requests with JSON payloads
func createTestRequest(t *testing.T, payload string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", "/test", io.NopCloser(bytes.NewBufferString(payload)))
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// Test helper to create HTTPServer instance
func createTestHTTPServer(t *testing.T) *HTTPServer {
	t.Helper()
	// Create a test URL
	testURL, err := url.Parse("https://test-reportportal.example.com")
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}

	config := HTTPServerConfig{
		HostURL:           testURL,
		FallbackRPToken:   "test-token-12345",
		ConnectionTimeout: 30 * time.Second,
		AnalyticsOn:       false, // Disable analytics for tests
	}
	srv, err := NewHTTPServer(config)
	if err != nil {
		t.Fatalf("failed to create HTTP server: %v", err)
	}
	return srv
}

func TestValidateSingleRequest(t *testing.T) {
	server := createTestHTTPServer(t)

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected bool
	}{
		{
			name: "valid single request",
			data: map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "test_method",
				"id":      1,
			},
			expected: true,
		},
		{
			name: "missing jsonrpc field",
			data: map[string]interface{}{
				"method": "test_method",
				"id":     1,
			},
			expected: false,
		},
		{
			name: "missing method field",
			data: map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
			},
			expected: false,
		},
		{
			name: "wrong jsonrpc version",
			data: map[string]interface{}{
				"jsonrpc": "1.0",
				"method":  "test_method",
				"id":      1,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.validateSingleRequest(tt.data)
			if result != tt.expected {
				t.Errorf("validateSingleRequest() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestValidateBatchRequest(t *testing.T) {
	server := createTestHTTPServer(t)

	tests := []struct {
		name     string
		batch    []interface{}
		expected bool
	}{
		{
			name: "valid batch with multiple requests",
			batch: []interface{}{
				map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "method1",
					"id":      1,
				},
				map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "method2",
					"id":      2,
				},
			},
			expected: true,
		},
		{
			name:     "empty batch",
			batch:    []interface{}{},
			expected: false,
		},
		{
			name: "batch with invalid request",
			batch: []interface{}{
				map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "valid_method",
					"id":      1,
				},
				map[string]interface{}{
					"jsonrpc": "1.0", // invalid version
					"method":  "invalid_method",
					"id":      2,
				},
			},
			expected: false,
		},
		{
			name: "batch with non-object item",
			batch: []interface{}{
				map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "valid_method",
					"id":      1,
				},
				"invalid_string_item",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.validateBatchRequest(tt.batch)
			if result != tt.expected {
				t.Errorf("validateBatchRequest() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestValidateMCPPayload(t *testing.T) {
	server := createTestHTTPServer(t)

	tests := []struct {
		name        string
		payload     string
		contentType string
		expected    bool
	}{
		{
			name:        "valid single request",
			payload:     `{"jsonrpc":"2.0","method":"test","id":1}`,
			contentType: "application/json",
			expected:    true,
		},
		{
			name:        "valid batch request",
			payload:     `[{"jsonrpc":"2.0","method":"test1","id":1},{"jsonrpc":"2.0","method":"test2","id":2}]`,
			contentType: "application/json",
			expected:    true,
		},
		{
			name:        "empty batch",
			payload:     `[]`,
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "invalid json",
			payload:     `{"jsonrpc":"2.0","method":"test"`,
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "non-json content type",
			payload:     `{"jsonrpc":"2.0","method":"test","id":1}`,
			contentType: "text/plain",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := createTestRequest(t, tt.payload)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			} else {
				req.Header.Del("Content-Type")
			}

			result := server.isMCPRequest(req)
			if result != tt.expected {
				t.Errorf(
					"isMCPRequest() = %v, expected %v for payload: %s",
					result,
					tt.expected,
					tt.payload,
				)
			}
		})
	}
}

func TestIsMCPRequest(t *testing.T) {
	server := createTestHTTPServer(t)

	tests := []struct {
		name        string
		method      string
		contentType string
		payload     string
		expected    bool
	}{
		{
			name:        "valid POST with JSON content",
			method:      "POST",
			contentType: "application/json",
			payload:     `{"jsonrpc":"2.0","method":"test","id":1}`,
			expected:    true,
		},
		{
			name:        "GET method",
			method:      "GET",
			contentType: "application/json",
			payload:     `{"jsonrpc":"2.0","method":"test","id":1}`,
			expected:    false,
		},
		{
			name:        "POST with XML content",
			method:      "POST",
			contentType: "application/xml",
			payload:     `<xml></xml>`,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(
				tt.method,
				"/test",
				io.NopCloser(bytes.NewBufferString(tt.payload)),
			)
			req.Header.Set("Content-Type", tt.contentType)

			result := server.isMCPRequest(req)
			if result != tt.expected {
				t.Errorf("isMCPRequest() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
