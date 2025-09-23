package middleware

import (
	"net/http"
	"strings"

	"github.com/reportportal/reportportal-mcp-server/internal/security"
	"github.com/reportportal/reportportal-mcp-server/internal/utils"
)

func QueryParamsMiddleware(rq *http.Request) {
	// Prefer context token over fallback by adding Authorization from context
	if token, ok := security.GetTokenFromContext(rq.Context()); ok {
		token = strings.TrimSpace(token)
		if token != "" && !strings.ContainsAny(token, "\r\n") {
			rq.Header.Set("Authorization", "Bearer "+token)
		}
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
