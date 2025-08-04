package mcpreportportal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
	"github.com/stretchr/testify/assert"
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

func TestListLaunchesTool(t *testing.T) {
	ctx := context.Background()
	testProject := "test-project"
	launches, _ := json.Marshal(testLaunches())
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("/api/v1/%s/launch", testProject), r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(launches)
	}))
	defer mockServer.Close()

	srv := mcptest.NewUnstartedServer(t)

	serverURL, _ := url.Parse(mockServer.URL)
	launchTools := NewLaunchResources(gorp.NewClient(serverURL, ""), testProject, nil)
	srv.AddTool(launchTools.toolGetLaunches())

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Close()

	client := srv.Client()

	var req mcp.CallToolRequest
	req.Params.Name = "get_launches"
	req.Params.Arguments = map[string]any{
		"project": testProject,
	}

	result, err := client.CallTool(ctx, req)
	require.NoError(t, err)

	var textContent mcp.TextContent
	require.IsType(t, textContent, result.Content[0])
	text := result.Content[0].(mcp.TextContent).Text

	assert.Equal(t, string(launches), text)
}

func testLaunches() *openapi.PageLaunchResource {
	launches := openapi.NewPageLaunchResource()
	launches.SetContent([]openapi.LaunchResource{
		{
			Id:        1,
			Name:      "Test Launch 1",
			Uuid:      "014b329b-a882-4c2d-9988-c2f6179a421b",
			Number:    1,
			StartTime: time.Now(),
			Status:    string(gorp.Statuses.Passed),
		},
		{
			Id:        2,
			Name:      "Test Launch 2",
			Uuid:      "014b329b-a882-4c2d-9988-c2f6179a421c",
			Number:    2,
			StartTime: time.Now(),
			Status:    string(gorp.Statuses.Passed),
		},
	})
	launches.SetPage(openapi.PageMetadata{
		TotalPages:    openapi.PtrInt64(1),
		HasNext:       openapi.PtrBool(false),
		Number:        openapi.PtrInt64(1),
		Size:          openapi.PtrInt64(int64(len(launches.Content))),
		TotalElements: openapi.PtrInt64(int64(len(launches.Content))),
	})

	return launches
}
