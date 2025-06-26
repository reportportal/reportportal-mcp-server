package mcpreportportal

import "net/http"

func QueryParamsMiddleware(rq *http.Request) {
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
