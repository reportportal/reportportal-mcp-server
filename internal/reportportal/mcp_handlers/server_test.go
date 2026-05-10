package mcphandlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// TestNewServer_BearerTokenSentWithTLSConfig is a regression test for the bug
// where rpClient.APIClient.GetConfig().HTTPClient was overwritten with a plain
// HTTP client, silently dropping the Authorization header from every outbound
// request and causing 401 Unauthorized responses.
//
// The test constructs a non-nil tlsCfg (the exact code-path that previously
// triggered the bug), makes a real API call to a TLS test server, and asserts
// that the Authorization: Bearer token header is present.
func TestNewServer_BearerTokenSentWithTLSConfig(t *testing.T) {
	const testToken = "regression-test-bearer-token"

	captured := make(chan string, 1)

	// httptest.NewTLSServer starts an HTTPS server with a self-signed certificate.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case captured <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"content":[],"page":{"number":1,"size":10,"totalElements":0,"totalPages":0}}`,
		))
	}))
	defer ts.Close()

	// ts.Client() already trusts the test server's self-signed certificate.
	tlsCfg := ts.TLS.Clone()
	tlsCfg.InsecureSkipVerify = true //nolint:gosec // test-only server

	serverURL, err := url.Parse(ts.URL)
	require.NoError(t, err)

	// Verify NewServer itself doesn't error with a non-nil tlsCfg.
	_, _, err = NewServer("v-test", serverURL, testToken, "", "test-project", "", false, tlsCfg)
	require.NoError(t, err, "NewServer must succeed with a custom TLS config")

	// Exercise the exact client-creation code path from NewServer (tlsCfg != nil
	// branch). Before the fix the HTTPClient was overwritten with a plain client;
	// this call would reach the server without an Authorization header.
	authCtx := context.WithValue(context.Background(), oauth2.HTTPClient, buildHTTPClient(tlsCfg))
	rpClient := gorp.NewClient(serverURL, gorp.WithApiKeyAuth(authCtx, testToken))
	_, _, _ = rpClient.APIClient.LaunchApi.GetProjectLaunches(context.Background(), "test-project").Execute()

	var authHeader string
	select {
	case authHeader = <-captured:
	default:
		t.Fatal("no outbound request reached the mock server")
	}

	require.Equal(t,
		"Bearer "+testToken,
		authHeader,
		"outbound ReportPortal API requests must carry the Bearer token even when a custom TLS config is set",
	)
}
