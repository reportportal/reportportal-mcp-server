package mcpreportportal_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yosida95/uritemplate/v3"
)

func TestLaunchByIdTemplate(t *testing.T) {
	uritmpl := uritemplate.MustNew(
		"reportportal://launch/{launch}{?filter,page,size,tab}")
	vals := uritmpl.Match("reportportal://launch/123?filter=xxx")
	require.Equal(t, vals.Get("filter").String(), "xxx")
	require.Equal(t, vals.Get("launch").String(), "123")
}
