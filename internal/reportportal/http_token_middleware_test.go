package mcpreportportal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractRPProjectFromRequest(t *testing.T) {
	tests := []struct {
		name           string
		headers        map[string]string
		expectedResult string
	}{
		{
			name:           "valid X-Project header",
			headers:        map[string]string{"X-Project": "test-project"},
			expectedResult: "test-project",
		},
		{
			name:           "X-Project header with whitespace",
			headers:        map[string]string{"X-Project": "  test-project  "},
			expectedResult: "test-project",
		},
		{
			name:           "empty X-Project header",
			headers:        map[string]string{"X-Project": ""},
			expectedResult: "",
		},
		{
			name:           "missing X-Project header",
			headers:        map[string]string{},
			expectedResult: "",
		},
		{
			name:           "X-Project header with only whitespace",
			headers:        map[string]string{"X-Project": "   "},
			expectedResult: "",
		},
		{
			name:           "case insensitive header name",
			headers:        map[string]string{"x-project": "test-project"},
			expectedResult: "test-project",
		},
		{
			name: "other headers present but no X-Project",
			headers: map[string]string{
				"Authorization": "Bearer token",
				"Content-Type":  "application/json",
			},
			expectedResult: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)

			// Set headers
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			result := extractRPProjectFromRequest(req)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestWithProjectInContext(t *testing.T) {
	ctx := context.Background()
	project := "test-project"

	// Test adding project to context
	ctxWithProject := WithProjectInContext(ctx, project)

	// Test retrieving project from context
	retrievedProject, ok := GetProjectFromContext(ctxWithProject)
	assert.True(t, ok)
	assert.Equal(t, project, retrievedProject)

	// Test that original context doesn't have project
	_, ok = GetProjectFromContext(ctx)
	assert.False(t, ok)
}

func TestGetProjectFromContext(t *testing.T) {
	tests := []struct {
		name            string
		contextValue    interface{}
		expectedProject string
		expectedOk      bool
	}{
		{
			name:            "valid project string",
			contextValue:    "test-project",
			expectedProject: "test-project",
			expectedOk:      true,
		},
		{
			name:            "empty project string",
			contextValue:    "",
			expectedProject: "",
			expectedOk:      false,
		},
		{
			name:            "nil context value",
			contextValue:    nil,
			expectedProject: "",
			expectedOk:      false,
		},
		{
			name:            "non-string context value",
			contextValue:    123,
			expectedProject: "",
			expectedOk:      false,
		},
		{
			name:            "context without project key",
			contextValue:    nil, // This will be handled by the context not having the key
			expectedProject: "",
			expectedOk:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			if tt.contextValue != nil {
				ctx = context.WithValue(ctx, RPProjectContextKey, tt.contextValue)
			}

			project, ok := GetProjectFromContext(ctx)
			assert.Equal(t, tt.expectedOk, ok)
			assert.Equal(t, tt.expectedProject, project)
		})
	}
}

func TestHTTPTokenMiddleware_ProjectExtraction(t *testing.T) {
	tests := []struct {
		name            string
		headers         map[string]string
		expectProject   bool
		expectedProject string
	}{
		{
			name:            "X-Project header present",
			headers:         map[string]string{"X-Project": "test-project"},
			expectProject:   true,
			expectedProject: "test-project",
		},
		{
			name:            "X-Project header missing",
			headers:         map[string]string{},
			expectProject:   false,
			expectedProject: "",
		},
		{
			name:            "X-Project header empty",
			headers:         map[string]string{"X-Project": ""},
			expectProject:   false,
			expectedProject: "",
		},
		{
			name:            "X-Project header with whitespace",
			headers:         map[string]string{"X-Project": "  test-project  "},
			expectProject:   true,
			expectedProject: "test-project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test handler that checks the context
			var capturedProject string
			var projectFound bool

			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				project, ok := GetProjectFromContext(r.Context())
				capturedProject = project
				projectFound = ok
				w.WriteHeader(http.StatusOK)
			})

			// Create middleware
			middleware := HTTPTokenMiddleware(testHandler)

			// Create request with headers
			req := httptest.NewRequest("GET", "/test", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			// Create response recorder
			rr := httptest.NewRecorder()

			// Execute middleware
			middleware.ServeHTTP(rr, req)

			// Verify response
			assert.Equal(t, http.StatusOK, rr.Code)

			// Verify project extraction
			assert.Equal(t, tt.expectProject, projectFound)
			if tt.expectProject {
				assert.Equal(t, tt.expectedProject, capturedProject)
			}
		})
	}
}

func TestHTTPTokenMiddleware_CombinedTokenAndProject(t *testing.T) {
	// Test that both token and project can be extracted simultaneously
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer 1234567890123456")
	req.Header.Set("X-Project", "test-project")

	var capturedToken, capturedProject string
	var tokenFound, projectFound bool

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := GetTokenFromContext(r.Context())
		capturedToken = token
		tokenFound = ok

		project, ok := GetProjectFromContext(r.Context())
		capturedProject = project
		projectFound = ok

		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPTokenMiddleware(testHandler)
	rr := httptest.NewRecorder()

	middleware.ServeHTTP(rr, req)

	// Verify both token and project were extracted
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, tokenFound)
	assert.Equal(t, "1234567890123456", capturedToken)
	assert.True(t, projectFound)
	assert.Equal(t, "test-project", capturedProject)
}

func TestHTTPTokenMiddleware_NoHeaders(t *testing.T) {
	// Test middleware with no headers
	req := httptest.NewRequest("GET", "/test", nil)

	var tokenFound, projectFound bool

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, tokenFound = GetTokenFromContext(r.Context())
		_, projectFound = GetProjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPTokenMiddleware(testHandler)
	rr := httptest.NewRecorder()

	middleware.ServeHTTP(rr, req)

	// Verify no token or project were found
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, tokenFound)
	assert.False(t, projectFound)
}
