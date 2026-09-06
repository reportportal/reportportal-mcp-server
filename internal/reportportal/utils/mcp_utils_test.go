package utils

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/stretchr/testify/require"
)

// redirectToHTTPTransport simulates an upload endpoint that issues a same-host
// redirect downgrading from HTTPS to plain HTTP, to verify UploadTMSAttachment
// never forwards the Authorization header to that HTTP destination.
type redirectToHTTPTransport struct {
	calls int
}

func (rt *redirectToHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls++
	if strings.EqualFold(req.URL.Scheme, "https") {
		location := "http://" + req.URL.Host + req.URL.Path
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{location}},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}
	// A plain-HTTP request reaching here would mean the redirect was followed
	// and the Authorization header may have leaked; fail loudly if it happens.
	if req.Header.Get("Authorization") != "" {
		return nil, fmt.Errorf("Authorization header sent over plain HTTP")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    req,
	}, nil
}

func TestUploadTMSAttachment_RejectsHTTPSToHTTPRedirect(t *testing.T) {
	serverURL, err := url.Parse("https://reportportal.example.com")
	require.NoError(t, err)

	client := gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), "test-token"))
	transport := &redirectToHTTPTransport{}
	client.APIClient.GetConfig().HTTPClient = &http.Client{Transport: transport}

	_, err = UploadTMSAttachment(
		context.Background(),
		client,
		"project",
		"a.txt",
		"text/plain",
		[]byte("data"),
	)

	require.Error(t, err)
	require.Equal(t, 1, transport.calls, "the HTTP redirect target must never be dispatched")
}

func TestSameHostHTTPSRedirectPolicy(t *testing.T) {
	allowed, err := url.Parse("https://reportportal.example.com")
	require.NoError(t, err)
	policy := SameHostHTTPSRedirectPolicy(allowed)

	tests := []struct {
		name        string
		target      string
		expectError bool
	}{
		{
			name:        "same host HTTPS redirect is allowed",
			target:      "https://reportportal.example.com/redirected",
			expectError: false,
		},
		{
			name:        "HTTPS to HTTP downgrade is rejected",
			target:      "http://reportportal.example.com/redirected",
			expectError: true,
		},
		{
			name:        "redirect to a different host is rejected",
			target:      "https://attacker.example.com/redirected",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetURL, parseErr := url.Parse(tt.target)
			require.NoError(t, parseErr)
			err := policy(&http.Request{URL: targetURL}, nil)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBuildManualScenario_AcceptsExactAttachmentSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 1, "fileName": "attachment.bin", "fileSize": 52428800}`))
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := gorp.NewClient(serverURL, gorp.WithApiKeyAuth(context.Background(), ""))

	encoded := base64.StdEncoding.EncodeToString(make([]byte, tmsAttachmentMaxDecodedBytes))
	var content strings.Builder
	for start := 0; start < len(encoded); start += 76 {
		end := start + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		content.WriteString(encoded[start:end])
		content.WriteString("\r\n")
	}

	attachments := []ExecutionAttachmentArg{{
		FileName: "attachment.bin",
		Content:  content.String(),
	}}
	scenario, uploadedIDs, err := BuildManualScenario(
		context.Background(),
		client,
		"project",
		ManualScenarioArgs{Attachments: &attachments},
	)

	require.NoError(t, err)
	require.Equal(t, []string{"1"}, uploadedIDs)
	require.NotNil(t, scenario)
}

func TestLimitSchema_WithDefault(t *testing.T) {
	s := LimitSchema(50)
	require.Equal(t, "integer", s.Type)
	require.Contains(t, s.Description, "default 50")
	require.NotNil(t, s.Minimum)
	require.Equal(t, float64(1), *s.Minimum)
}

func TestLimitSchema_WithoutDefault(t *testing.T) {
	s := LimitSchema(0)
	require.Equal(t, "integer", s.Type)
	require.NotContains(t, s.Description, "default")
	require.NotNil(t, s.Minimum)
	require.Equal(t, float64(1), *s.Minimum)
}

func TestOffsetSchema(t *testing.T) {
	s := OffsetSchema()
	require.Equal(t, "integer", s.Type)
	require.NotNil(t, s.Minimum)
	require.Equal(t, float64(0), *s.Minimum)
}

func TestApplyLimitOffset_DefaultApplied(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 0, 0, 50)
	require.Equal(t, "50", q.Get("limit"), "default limit should be applied")
	require.Equal(t, "0", q.Get("offset"), "offset should always be set when defaultLimit > 0")
}

func TestApplyLimitOffset_ExplicitValues(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 25, 100, 50)
	require.Equal(t, "25", q.Get("limit"))
	require.Equal(t, "100", q.Get("offset"))
}

func TestApplyLimitOffset_NoDefaultOmitsWhenZero(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 0, 0, 0)
	require.Empty(t, q.Get("limit"), "limit should be omitted when zero and no default")
	require.Empty(t, q.Get("offset"), "offset should be omitted when zero and no default")
}

func TestApplyLimitOffset_NoDefaultSetsWhenProvided(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 10, 20, 0)
	require.Equal(t, "10", q.Get("limit"))
	require.Equal(t, "20", q.Get("offset"))
}

func TestApplyLimitOffset_DefaultLimitOffsetConstant(t *testing.T) {
	q := url.Values{}
	ApplyLimitOffset(q, 0, 0, DefaultLimitOffset)
	require.Equal(t, "50", q.Get("limit"))
}
