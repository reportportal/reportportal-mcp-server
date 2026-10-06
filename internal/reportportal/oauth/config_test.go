package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConfig_OAuthOff(t *testing.T) {
	cfg, err := BuildConfig(false, "", "", "", "", "")
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
	assert.Empty(t, cfg.PublicURL)
}

func TestBuildConfig_Valid(t *testing.T) {
	cfg, err := BuildConfig(
		true,
		"https://mcp.example.com/mcp",
		"https://login.microsoftonline.com/tenant/v2.0",
		"https://login.microsoftonline.com/tenant/discovery/v2.0/keys",
		"audience-guid",
		"api://app-id/access_as_user",
	)
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "https://mcp.example.com/mcp", cfg.PublicURL)
	assert.Equal(t, "access_as_user", cfg.ExpectedScope)
	assert.Equal(
		t,
		"https://mcp.example.com/.well-known/oauth-protected-resource/mcp",
		cfg.ResourceMetadataURL,
	)
	assert.Equal(t, "/mcp", cfg.PublicURLPath)
}

func TestBuildConfig_LoopbackHTTP(t *testing.T) {
	cfg, err := BuildConfig(
		true,
		"http://127.0.0.1:8080/mcp",
		"http://localhost/v2.0",
		"http://127.0.0.1/keys",
		"aud",
		"api://x/scope",
	)
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
}

func TestBuildConfig_MissingFields(t *testing.T) {
	base := []string{
		"https://mcp.example.com/mcp",
		"https://issuer.example/v2.0",
		"https://issuer.example/keys",
		"aud",
		"api://x/scope",
	}
	flags := []string{
		"public-url",
		"oauth-issuer",
		"oauth-jwks-url",
		"oauth-audience",
		"oauth-scope",
	}

	for i := range base {
		args := append([]string{}, base...)
		args[i] = ""
		cfg, err := BuildConfig(true, args[0], args[1], args[2], args[3], args[4])
		require.Error(t, err)
		assert.False(t, cfg.Enabled)
		assert.Contains(t, err.Error(), "required")
		assert.Contains(t, err.Error(), flags[i])
	}
}

func TestBuildConfig_TrimsWhitespace(t *testing.T) {
	cfg, err := BuildConfig(
		true,
		" https://mcp.example.com/mcp/ ",
		" https://issuer.example/v2.0 ",
		" https://issuer.example/keys ",
		" aud-guid ",
		" api://app-id/access_as_user ",
	)
	require.NoError(t, err)
	assert.Equal(t, "https://mcp.example.com/mcp", cfg.PublicURL)
	assert.Equal(t, "https://issuer.example/v2.0", cfg.Issuer)
	assert.Equal(t, "https://issuer.example/keys", cfg.JWKSURL)
	assert.Equal(t, "aud-guid", cfg.Audience)
	assert.Equal(t, "api://app-id/access_as_user", cfg.Scope)
	assert.Equal(t, "access_as_user", cfg.ExpectedScope)
}

func TestBuildConfig_PublicURLTrailingSlash(t *testing.T) {
	withSlash, err := BuildConfig(
		true,
		"https://mcp.example.com/mcp/",
		"https://issuer.example/v2.0",
		"https://issuer.example/keys",
		"aud",
		"api://x/access_as_user",
	)
	require.NoError(t, err)

	withoutSlash, err := BuildConfig(
		true,
		"https://mcp.example.com/mcp",
		"https://issuer.example/v2.0",
		"https://issuer.example/keys",
		"aud",
		"api://x/access_as_user",
	)
	require.NoError(t, err)

	assert.Equal(t, withoutSlash.PublicURL, withSlash.PublicURL)
	assert.Equal(t, withoutSlash.ResourceMetadataURL, withSlash.ResourceMetadataURL)
	assert.Equal(t, "/mcp", withSlash.PublicURLPath)
}

func TestBuildConfig_PublicURLHostOnlyVariants(t *testing.T) {
	rootSlash, err := BuildConfig(
		true,
		"https://mcp.example.com/",
		"https://issuer.example/v2.0",
		"https://issuer.example/keys",
		"aud",
		"api://x/access_as_user",
	)
	require.NoError(t, err)

	root, err := BuildConfig(
		true,
		"https://mcp.example.com",
		"https://issuer.example/v2.0",
		"https://issuer.example/keys",
		"aud",
		"api://x/access_as_user",
	)
	require.NoError(t, err)

	assert.Equal(t, "https://mcp.example.com", rootSlash.PublicURL)
	assert.Equal(t, root.PublicURL, rootSlash.PublicURL)
	assert.Empty(t, root.PublicURLPath)
	assert.Equal(
		t,
		"https://mcp.example.com/.well-known/oauth-protected-resource",
		root.ResourceMetadataURL,
	)
}

func TestBuildConfig_InvalidURLScheme(t *testing.T) {
	_, err := BuildConfig(
		true,
		"http://mcp.example.com/mcp",
		"https://login.microsoftonline.com/tenant/v2.0",
		"https://login.microsoftonline.com/tenant/discovery/v2.0/keys",
		"aud",
		"api://x/scope",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public-url")
}
