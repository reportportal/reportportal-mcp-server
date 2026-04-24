package mcphandlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectInProcess wires an in-memory MCP client to the given server and returns
// a ready-to-use ClientSession. The caller must Close it when done.
func connectInProcess(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	_, err := s.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	return cs
}

// emptyLaunchPageJSON returns a minimal valid ReportPortal launches-page body
// that the gorp-generated client can unmarshal without errors.
func emptyLaunchPageJSON(t *testing.T) []byte {
	t.Helper()
	page := openapi.NewPageLaunchResource()
	page.SetPage(openapi.PageMetadata{
		TotalPages:    openapi.PtrInt64(0),
		HasNext:       openapi.PtrBool(false),
		Number:        openapi.PtrInt64(1),
		Size:          openapi.PtrInt64(0),
		TotalElements: openapi.PtrInt64(0),
	})
	data, err := json.Marshal(page)
	require.NoError(t, err)
	return data
}

// TestNewServer_BearerTokenSentWithTLSConfig calls NewServer with a non-nil
// TLS config, triggers a real outbound ReportPortal API call through the
// registered MCP tool, and asserts that the oauth2 transport still injects the
// Bearer token — verifying the fix for the bug where overwriting HTTPClient
// dropped the Authorization header.
func TestNewServer_BearerTokenSentWithTLSConfig(t *testing.T) {
	const token = "test-api-token"
	const project = "test-project"

	var capturedAuth atomic.Value
	fakeRP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(emptyLaunchPageJSON(t))
	}))
	defer fakeRP.Close()

	tlsCfg := fakeRP.Client().Transport.(*http.Transport).TLSClientConfig //nolint:forcetypeassert
	rpURL, err := url.Parse(fakeRP.URL)
	require.NoError(t, err)

	mcpSrv, _, err := NewServer("test", rpURL, token, "", project, "", false, tlsCfg)
	require.NoError(t, err)

	cs := connectInProcess(t, mcpSrv)
	defer func() { require.NoError(t, cs.Close()) }()

	// Trigger a real outbound HTTP call from the registered tool to the fake RP server.
	// We discard the tool result; the assertion is on the auth header captured above.
	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_launches",
		Arguments: map[string]any{"projectKey": project},
	})
	require.NoError(t, err, "CallTool returned protocol error")

	auth, _ := capturedAuth.Load().(string)
	assert.True(t, strings.HasPrefix(auth, "Bearer "),
		"expected Authorization header to start with 'Bearer ', got: %q", auth)
	assert.Contains(t, auth, token)
}

// TestNewServer_BearerTokenSentWithoutTLS is the nil-tlsCfg counterpart of
// TestNewServer_BearerTokenSentWithTLSConfig and covers the most common
// production configuration. This test would have caught the original regression
// (plain HTTPClient overwrite) had it existed before the bug was introduced.
func TestNewServer_BearerTokenSentWithoutTLS(t *testing.T) {
	const token = "test-api-token"
	const project = "test-project"

	var capturedAuth atomic.Value
	fakeRP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(emptyLaunchPageJSON(t))
	}))
	defer fakeRP.Close()

	rpURL, err := url.Parse(fakeRP.URL)
	require.NoError(t, err)

	mcpSrv, _, err := NewServer("test", rpURL, token, "", project, "", false, nil)
	require.NoError(t, err)

	cs := connectInProcess(t, mcpSrv)
	defer func() { require.NoError(t, cs.Close()) }()

	_, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_launches",
		Arguments: map[string]any{"projectKey": project},
	})
	require.NoError(t, err, "CallTool returned protocol error")

	auth, _ := capturedAuth.Load().(string)
	assert.True(t, strings.HasPrefix(auth, "Bearer "),
		"expected Authorization header to start with 'Bearer ', got: %q", auth)
	assert.Contains(t, auth, token)
}
