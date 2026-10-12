package oauth

import (
	"net/http"
	"time"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

const jwksHTTPTimeout = 30 * time.Second

// JWKSHTTPClient returns an HTTP client for IdP JWKS fetches. It always uses the system
// trust store and is not affected by ReportPortal TLS settings (e.g. RP_INSECURE_TLS).
func JWKSHTTPClient() *http.Client {
	transport := utils.NewBaseTransport()
	return &http.Client{
		Transport: transport,
		Timeout:   jwksHTTPTimeout,
	}
}
