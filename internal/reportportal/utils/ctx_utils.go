package utils

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// contextKey is a type for context keys to avoid collisions
type ContextKey string

// Context keys for passing data through request context
const (
	// RPTokenContextKey is used to store RP API token in request context
	RPTokenContextKey ContextKey = "rp_api_token" //nolint:gosec // This is a context key, not a credential
	// RPProjectContextKey is used to store RP project parameter in request context
	RPProjectContextKey ContextKey = "rp_project" //nolint:gosec // This is a context key, not a credential
	// Key for storing query parameters in the context
	ContextKeyQueryParams ContextKey = "queryParams" //nolint:gosec // This is a context key, not a credential
)

func WithQueryParams(ctx context.Context, queryParams url.Values) context.Context {
	// Create a new context with the query parameters
	return context.WithValue(ctx, ContextKeyQueryParams, queryParams)
}

func QueryParamsFromContext(ctx context.Context) (url.Values, bool) {
	// Retrieve the query parameters from the context
	queryParams, ok := ctx.Value(ContextKeyQueryParams).(url.Values)
	return queryParams, ok
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

// WithTokenInContext adds RP API token to request context
func WithTokenInContext(ctx context.Context, token string) context.Context {
	// Trim whitespace from token
	token = strings.TrimSpace(token)
	return context.WithValue(ctx, RPTokenContextKey, token)
}

// GetTokenFromContext extracts RP API token from request context
func GetTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(RPTokenContextKey).(string)
	return token, ok && token != ""
}
