package mcpreportportal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/reportportal/reportportal-mcp-server/internal/config"
)

func parseHTTPCLI(t *testing.T, args []string) *cli.Command {
	t.Helper()
	t.Setenv("RP_HOST", "https://reportportal.example.com")

	var parsed *cli.Command
	cmd := &cli.Command{
		Name:  "mcp-server",
		Flags: append(config.GetCommonFlags(), config.GetHTTPFlags()...),
		Action: func(ctx context.Context, c *cli.Command) error {
			parsed = c
			return nil
		},
	}
	runArgs := append([]string{"mcp-server"}, args...)
	require.NoError(t, cmd.Run(context.Background(), runArgs))
	require.NotNil(t, parsed)
	return parsed
}

func TestBuildHTTPServerConfig_OAuthOff(t *testing.T) {
	cmd := parseHTTPCLI(t, nil)
	cfg, err := buildHTTPServerConfig(cmd)
	require.NoError(t, err)
	assert.False(t, cfg.OAuth.Enabled)
}

func TestBuildHTTPServerConfig_OAuthValid(t *testing.T) {
	cmd := parseHTTPCLI(t, []string{
		"--oauth-enabled",
		"--public-url", "https://mcp.example.com/mcp",
		"--oauth-issuer", "https://login.microsoftonline.com/tenant/v2.0",
		"--oauth-jwks-url", "https://login.microsoftonline.com/tenant/discovery/v2.0/keys",
		"--oauth-audience", "00000000-0000-0000-0000-000000000001",
		"--oauth-scope", "api://app/access_as_user",
	})
	cfg, err := buildHTTPServerConfig(cmd)
	require.NoError(t, err)
	assert.True(t, cfg.OAuth.Enabled)
	assert.Equal(t, "access_as_user", cfg.OAuth.ExpectedScope)
}

func TestBuildHTTPServerConfig_OAuthMissingPublicURL(t *testing.T) {
	cmd := parseHTTPCLI(t, []string{
		"--oauth-enabled",
		"--oauth-issuer", "https://login.microsoftonline.com/tenant/v2.0",
		"--oauth-jwks-url", "https://login.microsoftonline.com/tenant/discovery/v2.0/keys",
		"--oauth-audience", "aud",
		"--oauth-scope", "api://app/scope",
	})
	_, err := buildHTTPServerConfig(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public-url")
}
