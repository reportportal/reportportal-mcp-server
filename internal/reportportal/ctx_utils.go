package mcpreportportal

import (
	"context"
	"net/url"
)

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
