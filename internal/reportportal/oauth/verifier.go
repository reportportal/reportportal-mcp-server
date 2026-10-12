package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/analytics"
	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

// NewTokenVerifier returns an MCP SDK TokenVerifier for OAuth-enabled HTTP mode.
func NewTokenVerifier(ctx context.Context, cfg Config) (auth.TokenVerifier, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("oauth is not enabled")
	}

	oidcCtx := oidc.ClientContext(ctx, JWKSHTTPClient())

	keySet := oidc.NewRemoteKeySet(oidcCtx, cfg.JWKSURL)
	verifier := oidc.NewVerifier(cfg.Issuer, keySet, &oidc.Config{ClientID: cfg.Audience})

	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		token = strings.TrimSpace(token)
		if token == "" {
			return nil, fmt.Errorf("%w: empty token", auth.ErrInvalidToken)
		}

		if !isJWT(token) {
			if !utils.ValidateRPToken(token) {
				return nil, fmt.Errorf("%w: invalid API key", auth.ErrInvalidToken)
			}
			return &auth.TokenInfo{
				UserID:     analytics.HashToken(token),
				Expiration: time.Now().Add(time.Hour),
			}, nil
		}

		idToken, err := verifier.Verify(ctx, token)
		if err != nil {
			slog.Warn("OAuth JWT verification failed", "reason", err.Error())
			return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
		}

		var claims map[string]interface{}
		if err := idToken.Claims(&claims); err != nil {
			slog.Warn("OAuth JWT claims parse failed", "reason", err.Error())
			return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
		}

		scp := claimString(claims, "scp")
		if !slices.Contains(strings.Fields(scp), cfg.ExpectedScope) {
			oid, sub := userIDFromClaims(claims)
			slog.Warn("OAuth JWT scope rejected", "oid", oid, "sub", sub)
			return nil, fmt.Errorf("%w: missing required scope", auth.ErrInvalidToken)
		}

		userID := claimString(claims, "oid")
		if userID == "" {
			userID = claimString(claims, "sub")
		}
		if userID == "" {
			slog.Warn("OAuth JWT missing subject")
			return nil, fmt.Errorf("%w: missing oid or sub", auth.ErrInvalidToken)
		}

		return &auth.TokenInfo{
			UserID:     userID,
			Scopes:     strings.Fields(scp),
			Expiration: idToken.Expiry,
			Extra: map[string]any{
				"claims": claims,
			},
		}, nil
	}, nil
}

func isJWT(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var obj map[string]interface{}
	return json.Unmarshal(header, &obj) == nil
}

func claimString(claims map[string]interface{}, key string) string {
	v, ok := claims[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func userIDFromClaims(claims map[string]interface{}) (oid, sub string) {
	return claimString(claims, "oid"), claimString(claims, "sub")
}
