package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// BuildTLSConfig constructs a *tls.Config from the provided options.
// Returns nil when both insecure is false and caCertPath is empty, so callers
// can fall back to the Go default TLS behaviour.
//
// insecure disables certificate verification entirely (equivalent to curl -k).
// caCertPath is an optional path to a PEM file with trusted CA certificate(s)
// that are appended to the system cert pool. If the system cert pool cannot be
// loaded the function returns an error rather than silently falling back to a
// pool that trusts only the custom CA, which would break outbound HTTPS to any
// host not covered by that CA (e.g. Google Analytics endpoints).
func BuildTLSConfig(insecure bool, caCertPath string) (*tls.Config, error) {
	if insecure && caCertPath != "" {
		return nil, fmt.Errorf(
			"--insecure cannot be used together with --tls-ca-cert: choose one or the other",
		)
	}

	if !insecure && caCertPath == "" {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: insecure, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	}

	if caCertPath != "" {
		pemBytes, err := os.ReadFile(caCertPath) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("read CA cert file %q: %w", caCertPath, err)
		}

		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf(
				"load system cert pool: %w; on Windows, ensure the system certificate store is accessible or use --insecure",
				err,
			)
		}

		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("no valid PEM certificates found in %q", caCertPath)
		}

		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}
