package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/reportportal/reportportal-mcp-server/internal/testutil"
)

func TestExtractProject(t *testing.T) {
	tests := []struct {
		name                  string
		isHttpMode            bool   // If true, use projectFromHttpHeader; if false, use projectFromEnvVar
		projectFromEnvVar     string // Project from environment variable (used in stdio mode)
		projectFromHttpHeader string // Project from HTTP header (used in HTTP mode)
		projectFromRequest    string // Project from request parameter (highest priority)
		expectedProject       string
		expectError           bool
	}{
		{
			name:                  "projectFromRequest takes precedence over all in stdio mode",
			isHttpMode:            false,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "",
			projectFromRequest:    "request-project",
			expectedProject:       "request-project",
			expectError:           false,
		},
		{
			name:                  "projectFromRequest takes precedence over projectFromHttpHeader",
			isHttpMode:            true,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "header-project",
			projectFromRequest:    "request-project",
			expectedProject:       "request-project",
			expectError:           false,
		},
		{
			name:                  "projectFromHttpHeader used when no projectFromRequest in HTTP mode",
			isHttpMode:            true,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "header-project",
			projectFromRequest:    "",
			expectedProject:       "header-project",
			expectError:           false,
		},
		{
			name:                  "projectFromEnvVar used when no projectFromRequest in stdio mode",
			isHttpMode:            false,
			projectFromEnvVar:     "env-project",
			projectFromHttpHeader: "",
			projectFromRequest:    "",
			expectedProject:       "env-project",
			expectError:           false,
		},
		{
			name:                  "error when no project from any source in HTTP mode",
			isHttpMode:            true,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "",
			projectFromRequest:    "",
			expectedProject:       "",
			expectError:           true,
		},
		{
			name:                  "error when no project from any source in stdio mode",
			isHttpMode:            false,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "",
			projectFromRequest:    "",
			expectedProject:       "",
			expectError:           true,
		},
		{
			name:                  "projectFromRequest with whitespace is trimmed in stdio mode",
			isHttpMode:            false,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "",
			projectFromRequest:    "  request-project  ",
			expectedProject:       "request-project",
			expectError:           false,
		},
		{
			name:                  "projectFromHttpHeader with whitespace is trimmed in HTTP mode",
			isHttpMode:            true,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "  header-project  ",
			projectFromRequest:    "",
			expectedProject:       "header-project",
			expectError:           false,
		},
		{
			name:                  "projectFromEnvVar with whitespace is trimmed in stdio mode",
			isHttpMode:            false,
			projectFromEnvVar:     "  env-project  ",
			projectFromHttpHeader: "",
			projectFromRequest:    "",
			expectedProject:       "env-project",
			expectError:           false,
		},
		{
			name:                  "empty projectFromRequest falls back to projectFromHttpHeader in HTTP mode",
			isHttpMode:            true,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "header-project",
			projectFromRequest:    "   ",
			expectedProject:       "header-project",
			expectError:           false,
		},
		{
			name:                  "empty projectFromHttpHeader causes error in HTTP mode",
			isHttpMode:            true,
			projectFromEnvVar:     "",
			projectFromHttpHeader: "   ",
			projectFromRequest:    "",
			expectedProject:       "",
			expectError:           true,
		},
		{
			name:                  "empty projectFromEnvVar causes error in stdio mode",
			isHttpMode:            false,
			projectFromEnvVar:     "   ",
			projectFromHttpHeader: "",
			projectFromRequest:    "",
			expectedProject:       "",
			expectError:           true,
		},
		{
			name:                  "projectFromHttpHeader ignored in stdio mode, uses projectFromEnvVar",
			isHttpMode:            false,
			projectFromEnvVar:     "env-project",
			projectFromHttpHeader: "header-project",
			projectFromRequest:    "",
			expectedProject:       "env-project",
			expectError:           false,
		},
		{
			name:                  "projectFromEnvVar ignored in HTTP mode, uses projectFromHttpHeader",
			isHttpMode:            true,
			projectFromEnvVar:     "env-project",
			projectFromHttpHeader: "header-project",
			projectFromRequest:    "",
			expectedProject:       "header-project",
			expectError:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context
			ctx := context.Background()

			// Store project in context based on mode
			if tt.isHttpMode {
				// In HTTP mode, use project from HTTP header
				ctx = WithProjectInContext(ctx, tt.projectFromHttpHeader)
			} else {
				// In stdio mode, use project from environment variable
				ctx = WithProjectInContext(ctx, tt.projectFromEnvVar)
			}

			// Create mock request with project parameter
			request := testutil.NewMockCallToolRequest(tt.projectFromRequest)

			// Call extractProject with interface conversion
			result, err := ExtractProjectWithMock(ctx, &request)

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
