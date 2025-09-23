package utils

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// ContextKey is a type for context keys to avoid collisions
type ContextKey string

var contextKeyQueryParams = ContextKey(
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
