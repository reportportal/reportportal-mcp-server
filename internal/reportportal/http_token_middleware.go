package mcpreportportal

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// Context keys for passing data through request context
const (
	// RPTokenContextKey is used to store RP API token in request context
	RPTokenContextKey contextKey = "rp_api_token" //nolint:gosec // This is a context key, not a credential
	// RPProjectContextKey is used to store RP project parameter in request context
	RPProjectContextKey contextKey = "rp_project" //nolint:gosec // This is a context key, not a credential
)

// HTTPTokenMiddleware returns an HTTP middleware function that extracts RP API tokens and project parameters
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

		// Extract project parameter from request headers
		rpProject := extractRPProjectFromRequest(r)

		if rpProject != "" {
			// Add project to request context for use by MCP handlers
			r = r.WithContext(WithProjectInContext(r.Context(), rpProject))

			slog.Debug("Extracted RP project parameter from HTTP request",
				"source", "http_header",
				"method", r.Method,
				"path", r.URL.Path,
				"project", rpProject)
		} else {
			slog.Debug("No RP project parameter found in HTTP request headers",
				"method", r.Method,
				"path", r.URL.Path,
				"checked_headers", []string{"X-Project"})
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
			if !ValidateRPToken(token) {
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

// extractRPProjectFromRequest extracts RP project parameter from HTTP request headers
// Supports X-Project header
func extractRPProjectFromRequest(r *http.Request) string {
	project := strings.TrimSpace(r.Header.Get("X-Project"))
	if project != "" {
		slog.Debug("Valid RP project parameter extracted from request header",
			"source", "X-Project",
			"project", project)
		return project
	}
	return ""
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
