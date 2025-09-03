package mcpreportportal

import "net/http"

func QueryParamsMiddleware(rq *http.Request) {
	// Prefer context token over fallback by adding Authorization from context
	if token, ok := GetTokenFromContext(rq.Context()); ok && token != "" {
		rq.Header.Set("Authorization", "Bearer "+token)
	}

	// Handle query parameters from context
	paramsFromContext, ok := QueryParamsFromContext(rq.Context())
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
