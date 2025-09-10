package mcpreportportal

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// validRPTokenPrefixes defines the recognized ReportPortal API token prefixes
var validRPTokenPrefixes = []string{
	"rp_",           // ReportPortal prefix
	"bearer_",       // Bearer token
	"api_key_",      // API key format
	"reportportal_", // Full name prefix
}

// contextKey is a type for context keys to avoid collisions
type contextKey string

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

// ValidateRPToken performs comprehensive validation on RP API tokens
// Returns true if the token appears to be a valid ReportPortal API token
func ValidateRPToken(token string) bool {
	if token == "" {
		return false
	}

	// Check for valid UUID format using proper parsing
	if uuid.Validate(token) == nil {
		return true
	}

	// Check for known ReportPortal token prefixes
	tokenLower := strings.ToLower(token)
	for _, prefix := range validRPTokenPrefixes {
		if strings.HasPrefix(tokenLower, prefix) {
			return true
		}
	}

	// Minimum length check for security tokens
	return len(token) >= 16
}

// HasValidPrefix checks if the token has a recognized ReportPortal prefix
func HasValidPrefix(token string) bool {
	tokenLower := strings.ToLower(token)

	for _, prefix := range validRPTokenPrefixes {
		if strings.HasPrefix(tokenLower, prefix) {
			return true
		}
	}
	return false
}
