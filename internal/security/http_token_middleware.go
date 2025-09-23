package security

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/reportportal/reportportal-mcp-server/internal/utils"
)

// Context keys for passing data through request context
const (
	// RPTokenContextKey is used to store RP API token in request context
	RPTokenContextKey utils.ContextKey = "rp_api_token" //nolint:gosec // This is a context key, not a credential
)

// HTTPTokenMiddleware returns an HTTP middleware function that extracts RP API tokens
func HTTPTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract RP API token from request headers
		rpToken := extractRPTokenFromRequest(r)

		if rpToken != "" {
			// Add token to request context for use by MCP handlers
			r = r.WithContext(WithTokenInContext(r.Context(), rpToken))

			slog.Debug("Extracted RP API token from HTTP request",
				"source", "http_header",
				"method", r.Method,
				"path", r.URL.Path)
		} else {
			slog.Debug("No RP API token found in HTTP request headers",
				"method", r.Method,
				"path", r.URL.Path,
				"checked_headers", []string{"Authorization"})
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// extractRPTokenFromRequest extracts RP API token from HTTP request headers
// Only supports Authorization Bearer tokens
func extractRPTokenFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := strings.TrimSpace(parts[1])

			// Validate the extracted token before processing
			if !utils.ValidateRPToken(token) {
				slog.Debug("Invalid RP API token rejected",
					"source", "Authorization Bearer",
					"validation", "failed")
				return ""
			}
			slog.Debug("Valid RP API token extracted from request header",
				"source", "Authorization Bearer",
				"validation", "passed")
			return token
		}
	}
	return ""
}

// WithTokenInContext adds RP API token to request context
func WithTokenInContext(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, RPTokenContextKey, token)
}

// GetTokenFromContext extracts RP API token from request context
func GetTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(RPTokenContextKey).(string)
	return token, ok && token != ""
}
