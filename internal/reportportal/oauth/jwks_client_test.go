package oauth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWKSHTTPClient_DoesNotSkipTLSVerification(t *testing.T) {
	client := JWKSHTTPClient()
	require.NotNil(t, client)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "expected *http.Transport")
	if transport.TLSClientConfig != nil {
		assert.False(
			t,
			transport.TLSClientConfig.InsecureSkipVerify,
			"JWKS client must not inherit InsecureSkipVerify from RP TLS config",
		)
	}
}
