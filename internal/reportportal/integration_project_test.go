package mcpreportportal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIntegration_ProjectExtractionFlow(t *testing.T) {
	tests := []struct {
		name            string
		httpHeaders     map[string]string
		requestProject  string
		expectedProject string
		expectError     bool
	}{
		{
			name:            "HTTP header project takes precedence",
			httpHeaders:     map[string]string{"X-Project": "http-project"},
			requestProject:  "request-project",
			expectedProject: "http-project",
			expectError:     false,
		},
		{
			name:            "fallback to request when no HTTP header",
			httpHeaders:     map[string]string{},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "fallback to request when empty HTTP header",
			httpHeaders:     map[string]string{"X-Project": ""},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "fallback to request when whitespace HTTP header",
			httpHeaders:     map[string]string{"X-Project": "   "},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "error when no project anywhere",
			httpHeaders:     map[string]string{},
			requestProject:  "",
			expectedProject: "",
			expectError:     true,
		},
		{
			name:            "HTTP header with whitespace is trimmed",
			httpHeaders:     map[string]string{"X-Project": "  http-project  "},
			requestProject:  "request-project",
			expectedProject: "http-project",
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create HTTP request with headers
			req := httptest.NewRequest("GET", "/test", nil)
			for key, value := range tt.httpHeaders {
				req.Header.Set(key, value)
			}

			// Create mock MCP request
			mcpRequest := MockCallToolRequest{
				project: tt.requestProject,
			}

			// Apply middleware to get context with project
			var ctx context.Context
			middleware := HTTPTokenMiddleware(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx = r.Context()
					w.WriteHeader(http.StatusOK)
				}),
			)

			rr := httptest.NewRecorder()
			middleware.ServeHTTP(rr, req)

			// Test extractProject with the context from middleware
			result, err := extractProjectWithMock(ctx, mcpRequest)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedProject, result)
			}
		})
	}
}

func TestIntegration_CompleteHTTPFlow(t *testing.T) {
	// Test the complete flow from HTTP request to tool execution
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Project", "integration-test-project")

	var capturedProject string
	var projectFound bool

	// Create a handler that simulates the MCP tool execution
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate MCP tool request
		mcpRequest := MockCallToolRequest{
			project: "fallback-project",
		}

		// Extract project using our function
		project, err := extractProjectWithMock(r.Context(), mcpRequest)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		capturedProject = project
		projectFound = true
		w.WriteHeader(http.StatusOK)
	})

	// Apply middleware
	middleware := HTTPTokenMiddleware(handler)
	rr := httptest.NewRecorder()

	// Execute request
	middleware.ServeHTTP(rr, req)

	// Verify results
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, projectFound)
	assert.Equal(t, "integration-test-project", capturedProject)
}
