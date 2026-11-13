package mcpreportportal

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rpoauth "github.com/reportportal/reportportal-mcp-server/internal/reportportal/oauth"
)

const (
	oauthTestIssuer   = "https://issuer.test/v2.0"
	oauthTestAudience = "test-audience"
	oauthTestScope    = "api://app/access_as_user"
)

const mcpInitializeBody = `{
	"jsonrpc": "2.0",
	"method": "initialize",
	"id": 1,
	"params": {
		"protocolVersion": "2024-11-05",
		"capabilities": {},
		"clientInfo": {"name": "oauth-test", "version": "1.0"}
	}
}`

type oauthTestJWKS struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newOAuthTestJWKS(t *testing.T) *oauthTestJWKS {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	jwk := jose.JSONWebKey{Key: key.Public(), Use: "sig", Algorithm: string(jose.RS256)}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))

	return &oauthTestJWKS{server: srv, key: key}
}

func (j *oauthTestJWKS) close() {
	j.server.Close()
}

func (j *oauthTestJWKS) signToken(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: j.key},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	require.NoError(t, err)

	now := time.Now()
	payload := map[string]interface{}{
		"iss": oauthTestIssuer,
		"aud": oauthTestAudience,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
		"scp": "access_as_user",
		"oid": "test-user-oid",
	}
	for k, v := range claims {
		payload[k] = v
	}

	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	signed, err := signer.Sign(raw)
	require.NoError(t, err)
	compact, err := signed.CompactSerialize()
	require.NoError(t, err)
	return compact
}

func oauthHTTPServerConfig(t *testing.T, jwks *oauthTestJWKS, publicURL string) HTTPServerConfig {
	t.Helper()
	oauthCfg, err := rpoauth.BuildConfig(
		true,
		publicURL,
		oauthTestIssuer,
		jwks.server.URL,
		oauthTestAudience,
		oauthTestScope,
	)
	require.NoError(t, err)
	return HTTPServerConfig{
		Version: "test",
		HostURL: mustParseURL("https://reportportal.example.com"),
		OAuth:   oauthCfg,
	}
}

func postMCPInitialize(
	t *testing.T,
	router http.Handler,
	bearerToken string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(mcpInitializeBody)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHTTPServer_OAuthMetadataRoutes_PublicURLVariants(t *testing.T) {
	jwks := newOAuthTestJWKS(t)
	defer jwks.close()

	cases := []struct {
		name         string
		inputURL     string
		resource     string
		pathSuffixes []string
	}{
		{
			name:         "path with trailing slash",
			inputURL:     "https://mcp.example.com/mcp/",
			resource:     "https://mcp.example.com/mcp",
			pathSuffixes: []string{"/mcp"},
		},
		{
			name:         "path without trailing slash",
			inputURL:     "https://mcp.example.com/mcp",
			resource:     "https://mcp.example.com/mcp",
			pathSuffixes: []string{"/mcp"},
		},
		{
			name:         "host with trailing slash",
			inputURL:     "https://mcp.example.com/",
			resource:     "https://mcp.example.com",
			pathSuffixes: []string{""},
		},
		{
			name:         "host only",
			inputURL:     "https://mcp.example.com",
			resource:     "https://mcp.example.com",
			pathSuffixes: []string{""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := NewHTTPServer(oauthHTTPServerConfig(t, jwks, tc.inputURL))
			require.NoError(t, err)

			expected := map[string]interface{}{
				"resource":                 tc.resource,
				"authorization_servers":    []interface{}{oauthTestIssuer},
				"scopes_supported":         []interface{}{oauthTestScope},
				"bearer_methods_supported": []interface{}{"header"},
			}

			paths := []string{"/.well-known/oauth-protected-resource"}
			for _, suffix := range tc.pathSuffixes {
				paths = append(paths, "/.well-known/oauth-protected-resource"+suffix)
			}

			for _, path := range paths {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				srv.Router.ServeHTTP(rec, req)
				require.Equal(t, http.StatusOK, rec.Code, "path %s", path)

				var body map[string]interface{}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, expected, body)
			}
		})
	}
}

func TestHTTPServer_OAuthUnauthorizedWithoutToken(t *testing.T) {
	jwks := newOAuthTestJWKS(t)
	defer jwks.close()

	srv, err := NewHTTPServer(oauthHTTPServerConfig(t, jwks, "https://mcp.example.com/mcp"))
	require.NoError(t, err)

	rec := postMCPInitialize(t, srv.Router, "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "resource_metadata=")
	assert.Contains(
		t,
		rec.Header().Get("WWW-Authenticate"),
		"https://mcp.example.com/.well-known/oauth-protected-resource/mcp",
	)
}

func TestHTTPServer_OAuthAcceptsJWT(t *testing.T) {
	jwks := newOAuthTestJWKS(t)
	defer jwks.close()

	srv, err := NewHTTPServer(oauthHTTPServerConfig(t, jwks, "https://mcp.example.com/mcp"))
	require.NoError(t, err)

	token := jwks.signToken(t, nil)
	rec := postMCPInitialize(t, srv.Router, token)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"result"`)
}

func TestHTTPServer_OAuthAcceptsAPIKey(t *testing.T) {
	jwks := newOAuthTestJWKS(t)
	defer jwks.close()

	srv, err := NewHTTPServer(oauthHTTPServerConfig(t, jwks, "https://mcp.example.com/mcp"))
	require.NoError(t, err)

	apiKey := "550e8400-e29b-41d4-a716-446655440000" //nolint:gosec // test UUID, not a real credential
	rec := postMCPInitialize(t, srv.Router, apiKey)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"result"`)
}

func TestHTTPServer_OAuthRejectsJWTBadSignature(t *testing.T) {
	jwks := newOAuthTestJWKS(t)
	defer jwks.close()
	other := newOAuthTestJWKS(t)
	defer other.close()

	srv, err := NewHTTPServer(oauthHTTPServerConfig(t, jwks, "https://mcp.example.com/mcp"))
	require.NoError(t, err)

	token := other.signToken(t, nil)
	rec := postMCPInitialize(t, srv.Router, token)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHTTPServer_OAuthDisabledNoMetadataRoute(t *testing.T) {
	srv, err := NewHTTPServer(HTTPServerConfig{
		Version: "test",
		HostURL: mustParseURL("https://reportportal.example.com"),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
