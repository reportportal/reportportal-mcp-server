package oauth

import (
	"fmt"
	"net/url"
	"strings"
)

// Config holds OAuth resource-server settings for HTTP mode.
type Config struct {
	Enabled             bool
	PublicURL           string
	Issuer              string
	JWKSURL             string
	Audience            string
	Scope               string
	ExpectedScope       string
	ResourceMetadataURL string
	PublicURLPath       string
}

// BuildConfig validates OAuth settings when enabled and derives metadata URLs and scope names.
func BuildConfig(
	enabled bool,
	publicURL string,
	issuer string,
	jwksURL string,
	audience string,
	scope string,
) (Config, error) {
	cfg := Config{Enabled: enabled}
	if !enabled {
		return cfg, nil
	}

	publicURL = strings.TrimSpace(publicURL)
	issuer = strings.TrimSpace(issuer)
	jwksURL = strings.TrimSpace(jwksURL)
	audience = strings.TrimSpace(audience)
	scope = strings.TrimSpace(scope)
	publicURL = strings.TrimRight(publicURL, "/")

	missing := missingOAuthFields(publicURL, issuer, jwksURL, audience, scope)
	if len(missing) > 0 {
		return Config{}, fmt.Errorf(
			"when --oauth-enabled is true, the following are required: %s",
			strings.Join(missing, ", "),
		)
	}

	if err := validateOAuthHTTPSURL(publicURL, "public-url"); err != nil {
		return Config{}, err
	}
	if err := validateOAuthHTTPSURL(issuer, "oauth-issuer"); err != nil {
		return Config{}, err
	}
	if err := validateOAuthHTTPSURL(jwksURL, "oauth-jwks-url"); err != nil {
		return Config{}, err
	}

	parsedPublic, err := url.Parse(publicURL)
	if err != nil {
		return Config{}, fmt.Errorf("invalid public-url: %w", err)
	}

	expectedScope := scopeAfterLastSlash(scope)
	if expectedScope == "" {
		return Config{}, fmt.Errorf("oauth-scope must contain a scope name after the last '/'")
	}

	origin := parsedPublic.Scheme + "://" + parsedPublic.Host
	publicPath := parsedPublic.Path
	if publicPath == "/" {
		publicPath = ""
	}

	cfg.PublicURL = publicURL
	cfg.Issuer = issuer
	cfg.JWKSURL = jwksURL
	cfg.Audience = audience
	cfg.Scope = scope
	cfg.ExpectedScope = expectedScope
	cfg.PublicURLPath = publicPath
	cfg.ResourceMetadataURL = origin + "/.well-known/oauth-protected-resource" + publicPath

	return cfg, nil
}

func missingOAuthFields(publicURL, issuer, jwksURL, audience, scope string) []string {
	var missing []string
	if publicURL == "" {
		missing = append(missing, "--public-url (MCP_PUBLIC_URL)")
	}
	if issuer == "" {
		missing = append(missing, "--oauth-issuer (MCP_OAUTH_ISSUER)")
	}
	if jwksURL == "" {
		missing = append(missing, "--oauth-jwks-url (MCP_OAUTH_JWKS_URL)")
	}
	if audience == "" {
		missing = append(missing, "--oauth-audience (MCP_OAUTH_AUDIENCE)")
	}
	if scope == "" {
		missing = append(missing, "--oauth-scope (MCP_OAUTH_SCOPE)")
	}
	return missing
}

func scopeAfterLastSlash(scope string) string {
	if scope == "" {
		return ""
	}
	if idx := strings.LastIndex(scope, "/"); idx >= 0 && idx < len(scope)-1 {
		return scope[idx+1:]
	}
	return scope
}

func validateOAuthHTTPSURL(raw string, fieldName string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s must be an absolute URL: %w", fieldName, err)
	}
	if !u.IsAbs() || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s must be an absolute URL with scheme and host", fieldName)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("%s must use https (http is allowed only for loopback hosts)", fieldName)
	default:
		return fmt.Errorf("%s must use https (http is allowed only for loopback hosts)", fieldName)
	}
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	default:
		return false
	}
}
