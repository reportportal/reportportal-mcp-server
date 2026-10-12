package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
)

const (
	testAudience      = "test-audience"
	testIssuer        = "https://issuer.test/v2.0"
	testScope         = "api://app/access_as_user"
	testExpectedScope = "access_as_user"
)

type testJWKS struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	jwks   jose.JSONWebKeySet
}

func newTestJWKS(t *testing.T) *testJWKS {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	jwk := jose.JSONWebKey{Key: key.Public(), Use: "sig", Algorithm: string(jose.RS256)}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))

	return &testJWKS{server: srv, key: key, jwks: jwks}
}

func (j *testJWKS) close() {
	j.server.Close()
}

func (j *testJWKS) signToken(
	t *testing.T,
	claims map[string]interface{},
	skew time.Duration,
) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: j.key},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	require.NoError(t, err)

	now := time.Now().Add(skew)
	payload := map[string]interface{}{
		"iss": testIssuer,
		"aud": testAudience,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
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

func newTestVerifier(t *testing.T, jwks *testJWKS) auth.TokenVerifier {
	t.Helper()
	cfg, err := BuildConfig(
		true,
		"https://mcp.example.com/mcp",
		testIssuer,
		jwks.server.URL,
		testAudience,
		testScope,
	)
	require.NoError(t, err)
	verifier, err := NewTokenVerifier(context.Background(), cfg)
	require.NoError(t, err)
	return verifier
}

func TestTokenVerifier_ValidJWT(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{
		"oid": "user-oid",
		"scp": "access_as_user",
	}, 0)

	info, err := verifier(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "user-oid", info.UserID)
	assert.Equal(t, []string{"access_as_user"}, info.Scopes)
	assert.NotNil(t, info.Extra["claims"])
}

func TestTokenVerifier_WrongAud(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{"aud": "wrong"}, 0)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_WrongIss(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{"iss": "https://sts.windows.net/tenant/"}, 0)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_V1StyleIssuer(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{
		"iss": "https://sts.windows.net/tenant-id/",
	}, 0)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_Expired(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{"scp": "access_as_user"}, -2*time.Hour)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_WrongScope(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{"scp": "other_scope"}, 0)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_MissingScopeRejected(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{"oid": "oid"}, 0)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_MissingSubjectRejected(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{"scp": "access_as_user"}, 0)
	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_BadSignature(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	other := newTestJWKS(t)
	defer other.close()
	token := other.signToken(t, map[string]interface{}{"scp": "access_as_user"}, 0)

	_, err := verifier(context.Background(), token, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_SubFallback(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	token := jwks.signToken(t, map[string]interface{}{
		"sub": "subject-id",
		"scp": "access_as_user",
	}, 0)
	info, err := verifier(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "subject-id", info.UserID)
}

func TestTokenVerifier_ValidAPIKey(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	apiKey := "550e8400-e29b-41d4-a716-446655440000" //nolint:gosec // test UUID, not a real credential
	info, err := verifier(context.Background(), apiKey, nil)
	require.NoError(t, err)
	assert.Equal(t, analytics.HashToken(apiKey), info.UserID)
	assert.True(t, info.Expiration.After(time.Now()))
}

func TestTokenVerifier_ShortAPIKey(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	_, err := verifier(context.Background(), "short", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenVerifier_GarbageString(t *testing.T) {
	jwks := newTestJWKS(t)
	defer jwks.close()
	verifier := newTestVerifier(t, jwks)

	_, err := verifier(context.Background(), "totally-invalid", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestIsJWT(t *testing.T) {
	assert.True(t, isJWT("eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIn0.sig"))
	assert.False(t, isJWT("plain-api-key-that-is-long-enough"))
	assert.False(t, isJWT("a.b.c"))
}
