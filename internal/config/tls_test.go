package config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCACertPEM is a self-signed ECDSA certificate generated once for test
// purposes only; it carries no private key and is never trusted by any
// production system.
const testCACertPEM = `-----BEGIN CERTIFICATE-----
MIIBQzCB6qADAgECAgEBMAoGCCqGSM49BAMCMBIxEDAOBgNVBAMTB3Rlc3QtY2Ew
HhcNMjYwNDI5MTE1MjUxWhcNMzYwNDI2MTI1MjUxWjASMRAwDgYDVQQDEwd0ZXN0
LWNhMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEBlM4b7Fw6OUzelGfUR4MXGAP
2dx9suKtmlXnB3M8lRyHzxCr4CgZKh8PwI5/lM7DOoXqxM8Yus8vMfXcUkWRP6Mx
MC8wDgYDVR0PAQH/BAQDAgIEMB0GA1UdDgQWBBRo7egNHQhXn4DS0DWJT0rhLrd1
4jAKBggqhkjOPQQDAgNIADBFAiEA7oYGOmZf2htv90/oIJXGXADGyMfNvkJbBnY2
r8QtFAYCIADXIP7KrQrKwvvdOW0pUx48PUgK5+Fob7eZlWRvcauQ
-----END CERTIFICATE-----
`

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tls_test_*.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return filepath.Clean(f.Name())
}

func TestBuildTLSConfig_DefaultNil(t *testing.T) {
	cfg, err := BuildTLSConfig(false, "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil *tls.Config, got non-nil")
	}
}

func TestBuildTLSConfig_InsecureOnly(t *testing.T) {
	cfg, err := BuildTLSConfig(true, "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil *tls.Config")
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion=TLS12 (0x%04x), got 0x%04x", tls.VersionTLS12, cfg.MinVersion)
	}
}

func TestBuildTLSConfig_InsecureWithCACert_ReturnsError(t *testing.T) {
	path := writeTempFile(t, testCACertPEM)
	_, err := BuildTLSConfig(true, path)
	if err == nil {
		t.Fatal("expected an error when both --insecure and --tls-ca-cert are set")
	}
	if !strings.Contains(err.Error(), "--insecure") ||
		!strings.Contains(err.Error(), "--tls-ca-cert") {
		t.Errorf("error message should mention both flags, got: %v", err)
	}
}

func TestBuildTLSConfig_InvalidPEM(t *testing.T) {
	path := writeTempFile(t, "this is not a valid PEM certificate")
	_, err := BuildTLSConfig(false, path)
	if err == nil {
		t.Fatal("expected an error for invalid PEM content")
	}
	if !strings.Contains(err.Error(), "no valid PEM") {
		t.Errorf("expected error mentioning 'no valid PEM', got: %v", err)
	}
}

func TestBuildTLSConfig_LoadCustomCA(t *testing.T) {
	path := writeTempFile(t, testCACertPEM)
	cfg, err := BuildTLSConfig(false, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil *tls.Config")
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs to be populated")
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be false when only a CA cert is provided")
	}

	// Verify the test cert is actually trusted by the returned pool: parse it
	// and run x509.Certificate.Verify against the pool.
	block, _ := pem.Decode([]byte(testCACertPEM))
	if block == nil {
		t.Fatal("failed to decode embedded testCACertPEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse test cert: %v", err)
	}
	fixedTime := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := cert.Verify(
		x509.VerifyOptions{Roots: cfg.RootCAs, CurrentTime: fixedTime},
	); err != nil {
		t.Errorf("test cert not found in RootCAs pool: %v", err)
	}
}
