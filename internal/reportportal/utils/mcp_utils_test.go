package utils

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLimitSchema_WithDefault(t *testing.T) {
	s := LimitSchema(50)
	require.Equal(t, "integer", s.Type)
	require.Contains(t, s.Description, "default 50")
	require.NotNil(t, s.Minimum)
	require.Equal(t, float64(1), *s.Minimum)
}

func TestLimitSchema_WithoutDefault(t *testing.T) {
	s := LimitSchema(0)
	require.Equal(t, "integer", s.Type)
	require.NotContains(t, s.Description, "default")
	require.NotNil(t, s.Minimum)
	require.Equal(t, float64(1), *s.Minimum)
}

func TestOffsetSchema(t *testing.T) {
	s := OffsetSchema()
	require.Equal(t, "integer", s.Type)
	require.NotNil(t, s.Minimum)
	require.Equal(t, float64(0), *s.Minimum)
}

func TestApplyLimitOffset_DefaultApplied(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 0, 0, 50)
	require.Equal(t, "50", q.Get("limit"), "default limit should be applied")
	require.Equal(t, "0", q.Get("offset"), "offset should always be set when defaultLimit > 0")
}

func TestApplyLimitOffset_ExplicitValues(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 25, 100, 50)
	require.Equal(t, "25", q.Get("limit"))
	require.Equal(t, "100", q.Get("offset"))
}

func TestApplyLimitOffset_NoDefaultOmitsWhenZero(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 0, 0, 0)
	require.Empty(t, q.Get("limit"), "limit should be omitted when zero and no default")
	require.Empty(t, q.Get("offset"), "offset should be omitted when zero and no default")
}

func TestApplyLimitOffset_NoDefaultSetsWhenProvided(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 10, 20, 0)
	require.Equal(t, "10", q.Get("limit"))
	require.Equal(t, "20", q.Get("offset"))
}

func TestApplyLimitOffset_DefaultLimitOffsetConstant(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 0, 0, DefaultLimitOffset)
	require.Equal(t, "50", q.Get("limit"))
}
