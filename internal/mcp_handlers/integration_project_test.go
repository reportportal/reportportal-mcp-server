package mcp_handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/reportportal/reportportal-mcp-server/internal/middleware"
	"github.com/reportportal/reportportal-mcp-server/internal/utils"
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
			name:            "request project takes precedence over HTTP header",
			httpHeaders:     map[string]string{"X-Project": "http-project"},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "use request project when no HTTP header",
			httpHeaders:     map[string]string{},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "use request project when empty HTTP header",
			httpHeaders:     map[string]string{"X-Project": ""},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "use request project when whitespace HTTP header",
			httpHeaders:     map[string]string{"X-Project": "   "},
			requestProject:  "request-project",
			expectedProject: "request-project",
			expectError:     false,
		},
		{
			name:            "fallback to HTTP header when no request project",
			httpHeaders:     map[string]string{"X-Project": "http-project"},
			requestProject:  "",
			expectedProject: "http-project",
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
			name:            "HTTP header with whitespace is trimmed when used",
			httpHeaders:     map[string]string{"X-Project": "  http-project  "},
			requestProject:  "",
			expectedProject: "http-project",
			expectError:     false,
		},
		{
			name:            "request project with whitespace is trimmed",
			httpHeaders:     map[string]string{"X-Project": "http-project"},
			requestProject:  "  request-project  ",
			expectedProject: "request-project",
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

			// Create MCP request
			var mcpRequest mcp.CallToolRequest
			mcpRequest.Params.Arguments = map[string]any{
				"project": tt.requestProject,
			}

			// Apply middleware to get context with project
			var ctx context.Context
			httpHandler := middleware.HTTPTokenMiddleware(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx = r.Context()
					w.WriteHeader(http.StatusOK)
				}),
			)

			rr := httptest.NewRecorder()
			httpHandler.ServeHTTP(rr, req)

			// Test extractProject with the context from middleware
			result, err := utils.ExtractProject(ctx, mcpRequest)

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
	// Request parameter should take precedence over HTTP header
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Project", "header-project")

	var capturedProject string
	var projectFound bool

	// Create a handler that simulates the MCP tool execution
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate MCP tool request with explicit project parameter
		// This should take precedence over the HTTP header
		var mcpRequest mcp.CallToolRequest
		mcpRequest.Params.Arguments = map[string]any{
			"project": "request-project",
		}

		// Extract project using our function
		project, err := utils.ExtractProject(r.Context(), mcpRequest)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		capturedProject = project
		projectFound = true
		w.WriteHeader(http.StatusOK)
	})

	// Apply middleware
	httpHandler := middleware.HTTPTokenMiddleware(handler)
	rr := httptest.NewRecorder()

	// Execute request
	httpHandler.ServeHTTP(rr, req)

	// Verify results - request parameter should win
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, projectFound)
	assert.Equal(t, "request-project", capturedProject)
}
