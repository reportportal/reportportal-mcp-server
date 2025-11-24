package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/reportportal/reportportal-mcp-server/internal/testdata"
)

const (
	// defaultHTTPTimeout is the default timeout for HTTP requests in tests
	defaultHTTPTimeout = 30 * time.Second
)

// LLMClientMock simulates an LLM client that makes requests to the MCP Server
type LLMClientMock struct {
	mcpServerURL string
	httpClient   *http.Client
}

// NewLLMClientMock creates a new LLM client mock
func NewLLMClientMock(mcpServerURL string) *LLMClientMock {
	return &LLMClientMock{
		mcpServerURL: strings.TrimSuffix(mcpServerURL, "/"),
		httpClient:   &http.Client{Timeout: defaultHTTPTimeout},
	}
}

// SendRequest sends a request to the MCP Server and returns the response.
// The caller is responsible for closing the response body.
// The provided context controls request cancellation and timeout.
func (l *LLMClientMock) SendRequest(
	ctx context.Context,
	request PostmanRequest,
) (*http.Response, error) {
	// Build URL
	url := l.buildURL(request.URL)

	// Create HTTP request with context for cancellation and timeout support
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(request.Method), url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	for _, header := range request.Header {
		if !header.Disabled {
			req.Header.Set(header.Key, header.Value)
		}
	}

	// Add body if present
	if request.Body != nil {
		body, contentType := l.buildBody(request.Body)
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))

			// Set Content-Type from buildBody if provided
			// CRITICAL: For multipart/form-data, the Content-Type includes a boundary parameter
			// that MUST match the body encoding. Always use the Content-Type from buildBody
			// to ensure the header matches the actual body format.
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
		}
	}

	// Send request
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// SendMCPRequest sends a JSON-RPC MCP request to the MCP server.
// The caller is responsible for closing the response body.
// The provided context controls request cancellation and timeout.
func (l *LLMClientMock) SendMCPRequest(
	ctx context.Context,
	method string,
	params map[string]interface{},
	headers map[string]string,
) (*http.Response, error) {
	// Build JSON-RPC request
	jsonRPCRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      1,
		"params":  params,
	}

	jsonData, err := json.Marshal(jsonRPCRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON-RPC request: %w", err)
	}

	// Create HTTP request
	endpoint := strings.TrimSuffix(l.mcpServerURL, "/")
	if !strings.HasSuffix(endpoint, "/mcp") && !strings.HasSuffix(endpoint, "/api/mcp") {
		endpoint += "/mcp"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type
	req.Header.Set("Content-Type", "application/json")

	// Add custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Send request
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// ValidateResponse validates that the response matches the expected response
func (l *LLMClientMock) ValidateResponse(actual *http.Response, expected PostmanResponse) error {
	// Check status code
	if actual.StatusCode != expected.Code {
		return fmt.Errorf(
			"status code mismatch: expected %d, got %d",
			expected.Code,
			actual.StatusCode,
		)
	}

	// Check headers
	for _, expectedHeader := range expected.Header {
		if expectedHeader.Disabled {
			continue
		}
		actualValue := actual.Header.Get(expectedHeader.Key)
		if actualValue != expectedHeader.Value {
			return fmt.Errorf(
				"header %s mismatch: expected %s, got %s",
				expectedHeader.Key,
				expectedHeader.Value,
				actualValue,
			)
		}
	}

	// Read and validate body
	defer func() { _ = actual.Body.Close() }()
	bodyBytes, err := io.ReadAll(actual.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Compare body (with JSON normalization if needed)
	if expected.Body != "" {
		// Try to extract and compare JSON content from text fields for MCP responses
		if l.matchesMCPResponse(string(bodyBytes), expected.Body) {
			return nil
		}
		if !l.matchesBody(string(bodyBytes), expected.Body) {
			return fmt.Errorf(
				"body mismatch: expected %s, got %s",
				expected.Body,
				string(bodyBytes),
			)
		}
	}

	return nil
}

// buildURL constructs the full URL from PostmanURL
func (l *LLMClientMock) buildURL(urlData PostmanURL) string {
	if urlData.Raw != "" {
		// If raw URL is provided and it's absolute, use it
		if strings.HasPrefix(urlData.Raw, "http://") || strings.HasPrefix(urlData.Raw, "https://") {
			return urlData.Raw
		}
		// Otherwise, join relative URL with base URL using path.Join for robustness
		// Ensure relative URL starts with "/" for proper joining
		relPath := urlData.Raw
		if !strings.HasPrefix(relPath, "/") {
			relPath = "/" + relPath
		}
		return l.mcpServerURL + relPath
	}

	// Build from components
	var fullURL strings.Builder
	if urlData.Protocol != "" {
		fullURL.WriteString(urlData.Protocol)
		fullURL.WriteString("://")
	} else {
		// Use base URL protocol
		if strings.HasPrefix(l.mcpServerURL, "https://") {
			fullURL.WriteString("https://")
		} else {
			fullURL.WriteString("http://")
		}
	}

	// Host
	if len(urlData.Host) > 0 {
		fullURL.WriteString(strings.Join(urlData.Host, "."))
	} else {
		// Extract host from base URL
		baseURL := strings.TrimPrefix(l.mcpServerURL, "http://")
		baseURL = strings.TrimPrefix(baseURL, "https://")
		if idx := strings.Index(baseURL, "/"); idx != -1 {
			fullURL.WriteString(baseURL[:idx])
		} else {
			fullURL.WriteString(baseURL)
		}
	}

	// Path
	if len(urlData.Path) > 0 {
		fullURL.WriteString("/")
		fullURL.WriteString(strings.Join(urlData.Path, "/"))
	}

	// Query parameters
	if len(urlData.Query) > 0 {
		queryParts := make([]string, 0, len(urlData.Query))
		for _, param := range urlData.Query {
			if !param.Disabled {
				queryParts = append(
					queryParts,
					fmt.Sprintf("%s=%s", url.QueryEscape(param.Key), url.QueryEscape(param.Value)),
				)
			}
		}
		if len(queryParts) > 0 {
			fullURL.WriteString("?")
			fullURL.WriteString(strings.Join(queryParts, "&"))
		}
	}

	return fullURL.String()
}

// buildBody constructs the request body and returns the body bytes and optional content type.
// For multipart/form-data, the content type includes the boundary parameter.
//
// This function panics on encoding errors (e.g., multipart form field write failures)
// because such errors indicate test configuration issues that should fail immediately
// rather than silently proceeding with incorrect request bodies.
func (l *LLMClientMock) buildBody(body *PostmanRequestBody) ([]byte, string) {
	if body == nil {
		return nil, ""
	}

	switch body.Mode {
	case testdata.BodyModeRaw:
		return []byte(body.Raw), ""
	case testdata.BodyModeURLEncoded:
		// Build URL-encoded form data
		parts := make([]string, 0, len(body.URLEncoded))
		for _, kv := range body.URLEncoded {
			if !kv.Disabled {
				parts = append(
					parts,
					fmt.Sprintf("%s=%s", url.QueryEscape(kv.Key), url.QueryEscape(kv.Value)),
				)
			}
		}
		return []byte(strings.Join(parts, "&")), "application/x-www-form-urlencoded"
	case testdata.BodyModeFormData:
		// Build multipart/form-data
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		for _, kv := range body.FormData {
			if !kv.Disabled {
				// Note: Currently only supports text fields, not file uploads
				// File uploads would require kv.Type == "file" and different handling
				if err := writer.WriteField(kv.Key, kv.Value); err != nil {
					// In test context, encoding errors indicate test configuration issues
					// and should fail immediately rather than silently fall back
					panic(fmt.Sprintf("failed to write multipart form field %q: %v", kv.Key, err))
				}
			}
		}

		// Close the writer to finalize the multipart message
		if err := writer.Close(); err != nil {
			// In test context, encoding errors indicate test configuration issues
			// and should fail immediately rather than silently fall back
			panic(fmt.Sprintf("failed to close multipart form writer: %v", err))
		}

		return buf.Bytes(), writer.FormDataContentType()
	default:
		if body.Raw != "" {
			return []byte(body.Raw), ""
		}
		return nil, ""
	}
}

// matchesBody checks if response body matches expected (with JSON normalization)
func (l *LLMClientMock) matchesBody(actual, expected string) bool {
	return normalizeAndCompareJSON(actual, expected)
}

// matchesMCPResponse extracts JSON content from MCP response text fields and compares them
func (l *LLMClientMock) matchesMCPResponse(actual, expected string) bool {
	var actualResp, expectedResp map[string]interface{}

	if err := json.Unmarshal([]byte(actual), &actualResp); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(expected), &expectedResp); err != nil {
		return false
	}

	// Extract text content from result.content[0].text
	actualText := l.extractMCPTextContent(actualResp)
	expectedText := l.extractMCPTextContent(expectedResp)

	if actualText == "" || expectedText == "" {
		return false
	}

	// Compare the JSON content inside the text field
	return l.matchesBody(actualText, expectedText)
}

// extractMCPTextContent extracts the text content from an MCP response
func (l *LLMClientMock) extractMCPTextContent(resp map[string]interface{}) string {
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		return ""
	}

	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		return ""
	}

	firstItem, ok := content[0].(map[string]interface{})
	if !ok {
		return ""
	}

	text, ok := firstItem["text"].(string)
	if !ok {
		return ""
	}

	return text
}
