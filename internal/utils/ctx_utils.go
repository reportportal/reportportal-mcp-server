package utils

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// contextKey is a type for context keys to avoid collisions
type contextKey string

const (
	// RPProjectContextKey is used to store RP project parameter in request context
	RPProjectContextKey contextKey = "rp_project" //nolint:gosec // This is a context key, not a credential
)

var contextKeyQueryParams = contextKey(
	"queryParams",
) // Key for storing query parameters in the context

func WithQueryParams(ctx context.Context, queryParams url.Values) context.Context {
	// Create a new context with the query parameters
	return context.WithValue(ctx, contextKeyQueryParams, queryParams)
}

func QueryParamsFromContext(ctx context.Context) (url.Values, bool) {
	// Retrieve the query parameters from the context
	queryParams, ok := ctx.Value(contextKeyQueryParams).(url.Values)
	return queryParams, ok
}

// WithProjectInContext adds RP project parameter to request context
func WithProjectInContext(ctx context.Context, project string) context.Context {
	// Trim whitespace from project parameter
	project = strings.TrimSpace(project)
	return context.WithValue(ctx, RPProjectContextKey, project)
}

// GetProjectFromContext extracts RP project parameter from request context
func GetProjectFromContext(ctx context.Context) (string, bool) {
	project, ok := ctx.Value(RPProjectContextKey).(string)
	res := strings.TrimSpace(project)
	return res, ok && res != ""
}

// ValidateRPToken performs validation on RP API tokens
// Returns true if the token appears to be a valid ReportPortal API token
func ValidateRPToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	// Check for valid UUID format using proper parsing
	if uuid.Validate(token) == nil {
		return true
	}

	// Fallback: minimum length check for non-UUID tokens
	return len(token) >= 16
}
