package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/reportportal/reportportal-mcp-server/internal/integration/testdata"
	mcpreportportal "github.com/reportportal/reportportal-mcp-server/internal/reportportal"
)

const defaultHTTPTimeout = 30 * time.Second

// headerRoundTripper injects a fixed set of HTTP headers into every outgoing
// request. It is used to forward all test-case request headers (except
// Content-Type, which the SDK manages itself) into the MCP-protocol requests
// made by the SDK client.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// captureRoundTripper wraps an http.RoundTripper and records the HTTP status
// code and headers of the MCP tools/call response. It identifies the relevant
// request by peeking at the JSON-RPC method in the request body.
type captureRoundTripper struct {
	base       http.RoundTripper
	mu         sync.Mutex
	statusCode int
	headers    http.Header
}

// snapshot returns a consistent copy of the captured status code and response
// headers, safe to call from any goroutine after the round-trip completes.
func (c *captureRoundTripper) snapshot() (statusCode int, headers http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusCode, c.headers.Clone()
}

func (c *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	isToolCall := false
	if req.Body != nil && req.Method == http.MethodPost {
		body, readErr := io.ReadAll(req.Body)
		_ = req.Body.Close()
		// Restore body before any error return so the downstream transport
		// always receives a readable (possibly partial) body.
		req.Body = io.NopCloser(bytes.NewReader(body))
		if readErr != nil {
			return nil, fmt.Errorf("captureRoundTripper: read request body for %s %s: %w",
				req.Method, req.URL, readErr)
		}
		var rpcReq struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &rpcReq) == nil && rpcReq.Method == "tools/call" {
			isToolCall = true
		}
	}

	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	if isToolCall {
		c.mu.Lock()
		c.statusCode = resp.StatusCode
		c.headers = resp.Header.Clone()
		c.mu.Unlock()
	}

	return resp, nil
}

// jsonRPCToolCall is the JSON-RPC payload sent by the LLM client mock to call a tool.
type jsonRPCToolCall struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	ID      any    `json:"id"`
	Params  struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"params"`
}

// expectedToolContent is the content element of a JSON-RPC tools/call result.
type expectedToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// jsonRPCToolResponse is the expected JSON-RPC response for a tools/call request.
type jsonRPCToolResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  struct {
		Content []expectedToolContent `json:"content"`
		IsError bool                  `json:"isError,omitempty"`
	} `json:"result"`
}

// getTestDataDir returns the path to the testdata directory relative to this file.
func getTestDataDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get caller information for test data directory resolution")
	}
	return filepath.Join(filepath.Dir(filename), "testdata")
}

// TestIntegration discovers *.json test-case files under testdata/ and runs each
// one through a real MCP session using the official Go SDK StreamableClientTransport.
func TestIntegration(t *testing.T) {
	testDataDir := getTestDataDir()
	testCaseFiles, err := filepath.Glob(filepath.Join(testDataDir, "*.json"))
	require.NoError(t, err, "Failed to glob test case files")

	if len(testCaseFiles) == 0 {
		t.Skipf("No test case files found in %s", testDataDir)
	}

	for _, testFile := range testCaseFiles {
		testFile := testFile
		t.Run(filepath.Base(testFile), func(t *testing.T) {
			runTestCaseWithSDKClient(t, testFile)
		})
	}
}

// runTestCaseWithSDKClient loads a JSON test-case file and executes it.
func runTestCaseWithSDKClient(t *testing.T, testCasePath string) {
	t.Helper()

	//nolint:gosec // path comes from controlled test directory
	raw, err := os.ReadFile(testCasePath)
	require.NoError(t, err, "read test case file")

	tc, err := testdata.ParseTestCase(raw)
	require.NoError(t, err, "parse test case")

	executeTestCase(t, tc)
}

// executeTestCase runs an already-parsed TestCase through a full MCP session.
func executeTestCase(t *testing.T, tc *testdata.TestCase) {
	t.Helper()

	// ── 1. Start the ReportPortal mock server ────────────────────────────────
	rpMock := NewReportPortalMockServer(tc.ReportPortalMock.RequestResponsePairs)
	defer rpMock.Close()

	rpMockURL, err := url.Parse(rpMock.URL())
	require.NoError(t, err, "parse RP mock URL")

	// ── 2. Build the MCP HTTP server ─────────────────────────────────────────
	mcpServer, err := mcpreportportal.NewHTTPServer(mcpreportportal.HTTPServerConfig{
		Version:         "test-version",
		HostURL:         rpMockURL,
		FallbackRPToken: "",
		AnalyticsOn:     false,
	})
	require.NoError(t, err, "create MCP server")

	// httptest.NewServer starts the actual HTTP listener immediately.
	// mcpServer.Start() only flips the internal "running" flag used by /health.
	mcpHTTPServer := httptest.NewServer(mcpServer.Router)
	defer mcpHTTPServer.Close()

	require.NoError(t, mcpServer.Start(), "set server running state")
	defer func() {
		if stopErr := mcpServer.Stop(); stopErr != nil {
			t.Logf("warning: failed to stop MCP server: %v", stopErr)
		}
	}()

	// ── 3. Build the SDK client with header injection ────────────────────────
	authHeaders := extractSDKHeaders(tc.LLMClientMock.Request.Header)

	sdkClient := mcp.NewClient(
		&mcp.Implementation{Name: "integration-test", Version: "1.0.0"},
		// Disable the root capability to keep test sessions clean.
		&mcp.ClientOptions{
			Capabilities: &mcp.ClientCapabilities{},
		},
	)

	// capture sits between headerRoundTripper and the real transport so it sees
	// the final HTTP response for every tools/call request.
	capture := &captureRoundTripper{base: http.DefaultTransport}

	transport := &mcp.StreamableClientTransport{
		Endpoint: mcpHTTPServer.URL + "/mcp",
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
			Transport: &headerRoundTripper{
				base:    capture,
				headers: authHeaders,
			},
		},
		// Disable the standalone SSE stream: integration tests only need
		// request/response and don't require server-initiated notifications.
		DisableStandaloneSSE: true,
	}

	ctx := t.Context()
	session, err := sdkClient.Connect(ctx, transport, nil)
	require.NoError(t, err, "connect MCP client")
	defer func() { _ = session.Close() }()

	// ── 4. Parse the tool-call from the request body ─────────────────────────
	toolName, toolArgs, err := parseToolCall(tc.LLMClientMock.Request)
	require.NoError(t, err, "parse tool call from request body")

	t.Logf("Calling tool %q with args %v", toolName, toolArgs)

	// ── 5. Call the tool via the SDK ─────────────────────────────────────────
	result, callErr := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: toolArgs,
	})
	require.NoError(t, callErr, "CallTool returned protocol error")

	// ── 6. Validate the result content ───────────────────────────────────────
	expectedContents, expectedIsError, err := parseExpectedContents(
		tc.LLMClientMock.ExpectedResponse.Body,
	)
	require.NoError(t, err, "parse expected response body")

	t.Logf("Tool result: isError=%v, content count=%d", result.IsError, len(result.Content))

	assert.Equal(t, expectedIsError, result.IsError, "tool call isError should match expected")
	require.Len(t, result.Content, len(expectedContents),
		"result content length should match expected")

	for i, expected := range expectedContents {
		actual, ok := result.Content[i].(*mcp.TextContent)
		require.True(t, ok, "content[%d] should be *mcp.TextContent", i)
		assert.Equal(t, "text", expected.Type, "expected content type should be text")
		assert.True(t,
			normalizeAndCompareJSON(actual.Text, expected.Text),
			"content[%d] text mismatch\n  actual:   %s\n  expected: %s",
			i, actual.Text, expected.Text,
		)
	}

	// ── 6b. Validate HTTP status code and headers of the tools/call response ──
	capturedStatus, capturedHeaders := capture.snapshot()

	if tc.LLMClientMock.ExpectedResponse.Code != 0 {
		require.NotZero(t, capturedStatus,
			"no tools/call HTTP response was captured; check the MCP transport")
		assert.Equal(t, tc.LLMClientMock.ExpectedResponse.Code, capturedStatus,
			"tools/call HTTP status code should match expected")
	}

	for _, h := range tc.LLMClientMock.ExpectedResponse.Header {
		if h.Disabled {
			continue
		}
		assert.Equal(t, h.Value, capturedHeaders.Get(h.Key),
			"tools/call response header %q should match expected", h.Key)
	}

	// ── 7. Verify all RP mock expectations were satisfied ────────────────────
	requestLog := rpMock.GetRequestLog()
	expectedCount := len(tc.ReportPortalMock.RequestResponsePairs)
	matchedCount := rpMock.GetMatchedCount()

	if expectedCount > 0 {
		assert.NotEmpty(t, requestLog, "ReportPortal mock should have received requests")

		t.Logf("ReportPortal mock: %d received, %d matched", len(requestLog), matchedCount)
		for i, req := range requestLog {
			status := "UNMATCHED"
			if req.Matched {
				status = "MATCHED"
			}
			t.Logf("  [%d] %s: %s %s", i+1, status, req.Method, req.Path)
			if len(req.QueryParams) > 0 {
				t.Logf("    query: %v", req.QueryParams)
			}
		}

		assert.Equal(t, expectedCount, matchedCount,
			"all RP mock request/response pairs should be matched "+
				"(expected %d, got %d)", expectedCount, matchedCount)
	}
}

// extractSDKHeaders collects all non-disabled request headers from a test-case
// definition for injection into MCP-protocol requests. Content-Type is excluded
// because the SDK sets it automatically and injecting a duplicate would conflict.
func extractSDKHeaders(headers []testdata.PostmanHeader) map[string]string {
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		if h.Disabled {
			continue
		}
		// Content-Type is set by the SDK; skip it to avoid conflicts.
		if strings.EqualFold(h.Key, "Content-Type") {
			continue
		}
		out[h.Key] = h.Value
	}
	return out
}

// parseToolCall extracts the MCP tool name and arguments from the raw JSON-RPC
// body of the LLM client mock request.
func parseToolCall(req testdata.PostmanRequest) (name string, args map[string]any, err error) {
	if req.Body == nil || req.Body.Raw == "" {
		return "", nil, fmt.Errorf("empty or missing JSON-RPC request body")
	}

	var call jsonRPCToolCall
	if jsonErr := json.Unmarshal([]byte(req.Body.Raw), &call); jsonErr != nil {
		return "", nil, jsonErr
	}

	if call.JSONRPC != "2.0" {
		return "", nil, fmt.Errorf(
			"unexpected jsonrpc version in fixture: expected \"2.0\", got %q",
			call.JSONRPC,
		)
	}

	if call.Method != "tools/call" {
		return "", nil, fmt.Errorf(
			"unexpected JSON-RPC method in fixture: expected \"tools/call\", got %q",
			call.Method,
		)
	}

	if call.Params.Name == "" {
		return "", nil, fmt.Errorf("tool name is empty in JSON-RPC request body: %s", req.Body.Raw)
	}

	return call.Params.Name, call.Params.Arguments, nil
}

// parseExpectedContents extracts the content slice and the isError flag from a
// JSON-RPC tools/call response body so they can be compared with the actual
// CallToolResult.
func parseExpectedContents(body string) ([]expectedToolContent, bool, error) {
	if body == "" {
		return nil, false, nil
	}

	var resp jsonRPCToolResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, false, err
	}

	return resp.Result.Content, resp.Result.IsError, nil
}
