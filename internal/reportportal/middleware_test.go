package mcpreportportal

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryParamsMiddleware(t *testing.T) {
	params := url.Values{}
	params.Add("a", "1")
	params.Add("b", "2")
	params.Add("b", "3")

	req, _ := http.NewRequest("GET", "http://example.com/path?x=9", nil)
	ctx := WithQueryParams(req.Context(), params)
	req = req.WithContext(ctx)

	QueryParamsMiddleware(req)

	got := req.URL.Query()
	assert.Equal(t, []string{"9"}, got["x"])
	assert.Equal(t, []string{"1"}, got["a"])
	assert.Equal(t, []string{"2", "3"}, got["b"])
}
