package mcpreportportal

import (
	"context"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithAndQueryParamsFromContext(t *testing.T) {
	ctx := context.Background()
	params := url.Values{}
	params.Add("foo", "bar")
	params.Add("baz", "qux")

	ctxWithParams := WithQueryParams(ctx, params)
	retrieved, ok := QueryParamsFromContext(ctxWithParams)
	assert.True(t, ok)
	assert.Equal(t, params, retrieved)

	// Test with context that does not have query params
	_, ok = QueryParamsFromContext(context.Background())
	assert.False(t, ok)
}

func TestQueryParamsFromContext_Absent(t *testing.T) {
	ctx := context.Background()
	val, ok := QueryParamsFromContext(ctx)
	assert.False(t, ok)
	assert.Nil(t, val)
}

func TestQueryParamsFromContext_PresentNil(t *testing.T) {
	ctx := WithQueryParams(context.Background(), nil)
	val, ok := QueryParamsFromContext(ctx)
	assert.True(t, ok)
	assert.Nil(t, val)
}

func TestQueryParamsFromContext_PresentEmpty(t *testing.T) {
	params := url.Values{}
	ctx := WithQueryParams(context.Background(), params)
	val, ok := QueryParamsFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, params, val)
}

func TestWithQueryParams_Overwrite(t *testing.T) {
	params1 := url.Values{}
	params1.Add("foo", "bar")
	ctx := WithQueryParams(context.Background(), params1)

	params2 := url.Values{}
	params2.Add("baz", "qux")
	ctx = WithQueryParams(ctx, params2)

	val, ok := QueryParamsFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, params2, val)
	assert.NotEqual(t, params1, val)
}
