package mcpreportportal

import (
	"context"
	"strings"

	"github.com/stretchr/testify/assert"
)

// MockCallToolRequest is a mock implementation of mcp.CallToolRequest for testing
type MockCallToolRequest struct {
	project string
}

// NewMockCallToolRequest creates a new MockCallToolRequest with the specified project
func NewMockCallToolRequest(project string) MockCallToolRequest {
	return MockCallToolRequest{project: project}
}

func (m MockCallToolRequest) RequireString(key string) (string, error) {
	if key == "project" {
		if m.project == "" {
			return "", assert.AnError
		}
		return m.project, nil
	}
	return "", assert.AnError
}

func (m MockCallToolRequest) GetString(key string, defaultValue string) string {
	if key == "project" {
		return m.project
	}
	return defaultValue
}

func (m MockCallToolRequest) GetInt(key string, defaultValue int) int {
	return defaultValue
}

func (m MockCallToolRequest) GetBool(key string, defaultValue bool) bool {
	return defaultValue
}

func (m MockCallToolRequest) GetStringSlice(key string) ([]string, error) {
	return nil, assert.AnError
}

func (m MockCallToolRequest) RequireInt(key string) (int, error) {
	return 0, assert.AnError
}

func (m MockCallToolRequest) RequireBool(key string) (bool, error) {
	return false, assert.AnError
}

func (m MockCallToolRequest) RequireStringSlice(key string) ([]string, error) {
	return nil, assert.AnError
}

// extractProjectWithMock is a test helper that works with MockCallToolRequest
func extractProjectWithMock(ctx context.Context, rq MockCallToolRequest) (string, error) {
	// First try to get project from context (from HTTP header)
	if project, ok := GetProjectFromContext(ctx); ok {
		return project, nil
	}

	project, err := rq.RequireString("project")
	return strings.TrimSpace(project), err
}
