package integration

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/reportportal/reportportal-mcp-server/internal/testdata"
)

func TestLLMClientMock_buildBody(t *testing.T) {
	client := NewLLMClientMock("http://example.com")

	tests := []struct {
		name                string
		body                *testdata.PostmanRequestBody
		expectedContentType string
		validateBody        func(t *testing.T, body []byte, contentType string)
	}{
		{
			name: "raw body",
			body: &testdata.PostmanRequestBody{
				Mode: testdata.BodyModeRaw,
				Raw:  `{"key":"value"}`,
			},
			expectedContentType: "",
			validateBody: func(t *testing.T, body []byte, contentType string) {
				assert.Equal(t, `{"key":"value"}`, string(body))
			},
		},
		{
			name: "urlencoded body",
			body: &testdata.PostmanRequestBody{
				Mode: testdata.BodyModeURLEncoded,
				URLEncoded: []testdata.PostmanKeyValue{
					{Key: "name", Value: "John Doe"},
					{Key: "email", Value: "john@example.com"},
					{Key: "disabled", Value: "should-not-appear", Disabled: true},
				},
			},
			expectedContentType: "application/x-www-form-urlencoded",
			validateBody: func(t *testing.T, body []byte, contentType string) {
				bodyStr := string(body)
				assert.Contains(t, bodyStr, "name=John+Doe")
				assert.Contains(t, bodyStr, "email=john%40example.com")
				assert.NotContains(t, bodyStr, "disabled")
			},
		},
		{
			name: "formdata body",
			body: &testdata.PostmanRequestBody{
				Mode: testdata.BodyModeFormData,
				FormData: []testdata.PostmanKeyValue{
					{Key: "username", Value: "testuser"},
					{Key: "password", Value: "secret123"},
					{Key: "disabled", Value: "should-not-appear", Disabled: true},
				},
			},
			expectedContentType: "multipart/form-data",
			validateBody: func(t *testing.T, body []byte, contentType string) {
				// Extract boundary from content type
				assert.True(t, strings.HasPrefix(contentType, "multipart/form-data; boundary="))

				// Parse the multipart body directly from bytes (no string conversion)
				boundary := strings.TrimPrefix(contentType, "multipart/form-data; boundary=")
				reader := multipart.NewReader(bytes.NewReader(body), boundary)

				// Read all parts, handling EOF explicitly
				parts := make(map[string]string)
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break // Expected end of multipart message
					}
					require.NoError(t, err, "Unexpected error reading multipart part")

					// Read part content
					content, err := io.ReadAll(part)
					require.NoError(t, err, "Failed to read part content")

					parts[part.FormName()] = string(content)

					// Close the part to free resources
					err = part.Close()
					require.NoError(t, err, "Failed to close multipart part")
				}

				// Verify expected fields are present
				assert.Equal(t, "testuser", parts["username"])
				assert.Equal(t, "secret123", parts["password"])
				assert.NotContains(t, parts, "disabled")
			},
		},
		{
			name: "empty formdata with raw fallback",
			body: &testdata.PostmanRequestBody{
				Mode:     testdata.BodyModeFormData,
				FormData: []testdata.PostmanKeyValue{},
				Raw:      "fallback-content",
			},
			expectedContentType: "multipart/form-data",
			validateBody: func(t *testing.T, body []byte, contentType string) {
				// Empty form data should still create a valid multipart message
				// Note: Raw fallback is intentionally ignored for formdata mode
				assert.True(t, strings.HasPrefix(contentType, "multipart/form-data; boundary="))
				assert.NotNil(
					t,
					body,
					"body should not be nil - empty multipart message is still valid",
				)
				assert.NotContains(
					t,
					string(body),
					"fallback-content",
					"raw fallback should not be used in formdata mode",
				)
			},
		},
		{
			name:                "nil body",
			body:                nil,
			expectedContentType: "",
			validateBody: func(t *testing.T, body []byte, contentType string) {
				assert.Nil(t, body)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, contentType := client.buildBody(tt.body)

			if tt.expectedContentType == "" {
				assert.Empty(t, contentType)
			} else {
				assert.True(t, strings.HasPrefix(contentType, tt.expectedContentType),
					"expected content type to start with %q, got %q", tt.expectedContentType, contentType)
			}

			tt.validateBody(t, body, contentType)
		})
	}
}

func TestLLMClientMock_SendRequest_ContentType(t *testing.T) {
	// Create a test HTTP server to capture actual requests sent by SendRequest
	var capturedRequest *http.Request
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedRequest = r
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewLLMClientMock(server.URL)

	t.Run("auto-set content type for urlencoded", func(t *testing.T) {
		mu.Lock()
		capturedRequest = nil // Reset
		mu.Unlock()

		req := testdata.PostmanRequest{
			Method: "POST",
			URL: testdata.PostmanURL{
				Path: []string{"test"},
			},
			Body: &testdata.PostmanRequestBody{
				Mode: testdata.BodyModeURLEncoded,
				URLEncoded: []testdata.PostmanKeyValue{
					{Key: "key", Value: "value"},
				},
			},
		}

		resp, err := client.SendRequest(context.Background(), req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Verify Content-Type was automatically set
		mu.Lock()
		captured := capturedRequest
		var contentType string
		if captured != nil {
			contentType = captured.Header.Get("Content-Type")
		}
		mu.Unlock()

		assert.NotNil(t, captured, "Request should have been captured")
		assert.Equal(
			t,
			"application/x-www-form-urlencoded",
			contentType,
		)
	})

	t.Run("auto-set content type for formdata", func(t *testing.T) {
		mu.Lock()
		capturedRequest = nil // Reset
		mu.Unlock()

		req := testdata.PostmanRequest{
			Method: "POST",
			URL: testdata.PostmanURL{
				Path: []string{"test"},
			},
			Body: &testdata.PostmanRequestBody{
				Mode: testdata.BodyModeFormData,
				FormData: []testdata.PostmanKeyValue{
					{Key: "field", Value: "value"},
				},
			},
		}

		resp, err := client.SendRequest(context.Background(), req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Verify Content-Type was automatically set with boundary
		mu.Lock()
		captured := capturedRequest
		var contentType string
		if captured != nil {
			contentType = captured.Header.Get("Content-Type")
		}
		mu.Unlock()

		assert.NotNil(t, captured, "Request should have been captured")
		assert.True(
			t,
			strings.HasPrefix(
				contentType,
				"multipart/form-data; boundary=",
			),
		)
	})

	t.Run("override user content type for multipart to include boundary", func(t *testing.T) {
		mu.Lock()
		capturedRequest = nil // Reset
		mu.Unlock()

		req := testdata.PostmanRequest{
			Method: "POST",
			URL: testdata.PostmanURL{
				Path: []string{"test"},
			},
			Header: []testdata.PostmanHeader{
				{
					Key:   "Content-Type",
					Value: "multipart/form-data",
				}, // User provides generic Content-Type without boundary
			},
			Body: &testdata.PostmanRequestBody{
				Mode: testdata.BodyModeFormData,
				FormData: []testdata.PostmanKeyValue{
					{Key: "field", Value: "value"},
				},
			},
		}

		resp, err := client.SendRequest(context.Background(), req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Verify Content-Type was overridden with the correct boundary parameter
		// This is CRITICAL: the boundary in Content-Type must match the body encoding
		mu.Lock()
		captured := capturedRequest
		var contentType string
		if captured != nil {
			contentType = captured.Header.Get("Content-Type")
		}
		mu.Unlock()

		assert.NotNil(t, captured, "Request should have been captured")
		assert.True(
			t,
			strings.HasPrefix(contentType, "multipart/form-data; boundary="),
			"Content-Type should include boundary parameter, got: %s", contentType,
		)
		// Verify the user's generic header was overridden
		assert.NotEqual(
			t,
			"multipart/form-data",
			contentType,
			"Generic Content-Type should be replaced with one containing boundary",
		)
	})
}
