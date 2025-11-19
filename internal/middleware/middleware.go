package middleware

import (
	"net/http"

	"github.com/reportportal/reportportal-mcp-server/internal/utils"
)

func QueryParamsMiddleware(rq *http.Request) {
	// In HTTP mode, inject the token from request context (extracted from HTTP headers)
	// If no token exists in context, the request will proceed without authentication
	if token, ok := GetTokenFromContext(rq.Context()); ok && token != "" {
		rq.Header.Set("Authorization", "Bearer "+token)
	}

	// Handle query parameters from context
	paramsFromContext, ok := utils.QueryParamsFromContext(rq.Context())
	if ok && paramsFromContext != nil {
		// If query parameters are present in the context, add them to the request URL
		query := rq.URL.Query()
		for key, values := range paramsFromContext {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		rq.URL.RawQuery = query.Encode() // Encode the updated query parameters into the request URL
	}
}
