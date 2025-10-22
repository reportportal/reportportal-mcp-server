package mcpreportportal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractProject(t *testing.T) {
	tests := []struct {
		name              string
		contextProject    string
		contextHasProject bool
		requestProject    string
		expectedProject   string
		expectError       bool
	}{
		{
			name:              "project from context takes precedence",
			contextProject:    "context-project",
			contextHasProject: true,
			requestProject:    "request-project",
			expectedProject:   "context-project",
			expectError:       false,
		},
		{
			name:              "fallback to request when no context project",
			contextProject:    "",
			contextHasProject: false,
			requestProject:    "request-project",
			expectedProject:   "request-project",
			expectError:       false,
		},
		{
			name:              "fallback to request when context project is empty",
			contextProject:    "",
			contextHasProject: true,
			requestProject:    "request-project",
			expectedProject:   "request-project",
			expectError:       false,
		},
		{
			name:              "error when no project in context or request",
			contextProject:    "",
			contextHasProject: false,
			requestProject:    "",
			expectedProject:   "",
			expectError:       true,
		},
		{
			name:              "context project with whitespace is trimmed",
			contextProject:    "  context-project  ",
			contextHasProject: true,
			requestProject:    "request-project",
			expectedProject:   "context-project",
			expectError:       false,
		},
		{
			name:              "empty context project falls back to request",
			contextProject:    "   ",
			contextHasProject: true,
			requestProject:    "request-project",
			expectedProject:   "request-project",
			expectError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context
			ctx := context.Background()
			if tt.contextHasProject {
				ctx = WithProjectInContext(ctx, tt.contextProject)
			}

			// Create mock request
			request := MockCallToolRequest{
				project: tt.requestProject,
			}

			// Call extractProject with interface conversion
			result, err := extractProjectWithMock(ctx, request)

			// Verify result
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedProject, result)
			}
		})
	}
}
