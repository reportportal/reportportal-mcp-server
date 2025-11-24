package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpreportportal "github.com/reportportal/reportportal-mcp-server/internal/reportportal"
)

const (
	// mcpInitializeTimeout is the timeout for MCP initialize handshake requests
	mcpInitializeTimeout = 30 * time.Second
)

// getTestDataDir returns the path to the testdata directory
func getTestDataDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get caller information for test data directory resolution")
	}
	testDir := filepath.Dir(filename)
	// Go up from internal/integration to project root, then to testdata
	projectRoot := filepath.Join(testDir, "..", "..")
	return filepath.Join(projectRoot, "testdata")
}

// TestIntegration runs integration tests based on Postman collection format
func TestIntegration(t *testing.T) {
	// Find all test case files
	testDataDir := getTestDataDir()
	testCaseFiles, err := filepath.Glob(filepath.Join(testDataDir, "*.json"))
	if err != nil {
		t.Fatalf("Failed to find test case files: %v", err)
	}

	if len(testCaseFiles) == 0 {
		t.Skipf("No test case files found in %s directory", testDataDir)
	}

	for _, testFile := range testCaseFiles {
		testFile := testFile // Capture loop variable for subtest closure (future-proof for t.Parallel)
		t.Run(filepath.Base(testFile), func(t *testing.T) {
			runTestCase(t, testFile)
		})
	}
}

// runTestCase runs a single test case
func runTestCase(t *testing.T, testCasePath string) {
	// Read test case file
	//nolint:gosec // testCasePath is from controlled test directory
	data, err := os.ReadFile(
		testCasePath,
	)
	require.NoError(t, err, "Failed to read test case file")

	// Parse test case
	testCase, err := ParseTestCase(data)
	require.NoError(t, err, "Failed to parse test case")

	// Start ReportPortal mock server
	rpMock := NewReportPortalMockServer(testCase.ReportPortalMock.RequestResponsePairs)
	defer rpMock.Close()

	// Parse ReportPortal mock URL
	rpMockURL, err := url.Parse(rpMock.URL())
	require.NoError(t, err, "Failed to parse ReportPortal mock URL")

	// Create and start MCP Server
	mcpServer, err := mcpreportportal.NewHTTPServer(mcpreportportal.HTTPServerConfig{
		Version:         "test-version",
		HostURL:         rpMockURL,
		FallbackRPToken: "",
		AnalyticsOn:     false,
	})
	require.NoError(t, err, "Failed to create MCP server")

	// Start MCP server using httptest
	// Note: httptest.NewServer immediately starts the actual HTTP listener,
	// while mcpServer.Start() (called below) only sets internal state flags
	// used by endpoints like /health to report "running" status.
	mcpHTTPServer := httptest.NewServer(mcpServer.Router)
	defer mcpHTTPServer.Close()

	mcpServerURL := mcpHTTPServer.URL

	// Set internal running state (required for health checks and analytics lifecycle)
	err = mcpServer.Start()
	require.NoError(t, err, "Failed to set server running state")
	defer func() {
		if err := mcpServer.Stop(); err != nil {
			t.Logf("Failed to stop MCP server: %v", err)
		}
	}()

	// Create LLM Client Mock
	llmClient := NewLLMClientMock(mcpServerURL)

	// Initialize MCP session first
	sessionID, err := initializeMCPSession(context.Background(), mcpServerURL)
	require.NoError(t, err, "Failed to initialize MCP session")

	// Add session ID to request headers
	// Make a deep copy to avoid modifying the original test case
	requestWithSession := testCase.LLMClientMock.Request
	if requestWithSession.Header == nil {
		requestWithSession.Header = []PostmanHeader{
			{Key: "mcp-session-id", Value: sessionID},
		}
	} else {
		// Create a new slice with capacity for existing headers plus one
		headers := make([]PostmanHeader, len(requestWithSession.Header), len(requestWithSession.Header)+1)
		copy(headers, requestWithSession.Header)
		requestWithSession.Header = append(headers, PostmanHeader{
			Key:   "mcp-session-id",
			Value: sessionID,
		})
	}

	// Send request from LLM Client Mock to MCP Server
	resp, err := llmClient.SendRequest(context.Background(), requestWithSession)
	require.NoError(t, err, "Failed to send request from LLM client mock")

	// Ensure response body is closed to prevent connection leaks
	defer func() { _ = resp.Body.Close() }()

	// Read response body for debugging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Logf("MCP Server Response Status: %d", resp.StatusCode)
		t.Logf("MCP Server Response Body: %s", string(bodyBytes))
		// Reset body for validation
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Validate response
	err = llmClient.ValidateResponse(resp, testCase.LLMClientMock.ExpectedResponse)
	if err != nil {
		t.Logf("Response validation error: %v", err)
	}
	assert.NoError(t, err, "Response validation failed")

	// Verify that ReportPortal mock received and successfully matched all expected requests
	requestLog := rpMock.GetRequestLog()
	expectedCount := len(testCase.ReportPortalMock.RequestResponsePairs)
	matchedCount := rpMock.GetMatchedCount()

	if expectedCount > 0 {
		assert.NotEmpty(t, requestLog, "ReportPortal mock should have received requests")

		// Log what was received for debugging
		t.Logf("ReportPortal mock received %d request(s), %d matched successfully",
			len(requestLog), matchedCount)
		for i, req := range requestLog {
			matchStatus := "UNMATCHED"
			if req.Matched {
				matchStatus = "MATCHED"
			}
			t.Logf("  Request %d [%s]: %s %s", i+1, matchStatus, req.Method, req.Path)
			if len(req.QueryParams) > 0 {
				t.Logf("    Query params: %v", req.QueryParams)
			}
			t.Logf("    Headers: %v", req.Headers)
		}

		// Verify that all expected requests were successfully matched
		assert.Equal(t, expectedCount, matchedCount,
			"All expected request/response pairs should be matched. "+
				"Expected %d matches but got %d. Check logs above for unmatched requests.",
			expectedCount, matchedCount)
	}
}

// TestIntegrationFromPostmanCollection runs tests from a Postman collection file
func TestIntegrationFromPostmanCollection(t *testing.T) {
	// Collections are now at project root, not in testdata
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get caller information for collections directory resolution")
	}
	projectRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	collectionsDir := filepath.Join(projectRoot, "collections")

	collectionFiles, err := filepath.Glob(filepath.Join(collectionsDir, "*.json"))
	if err != nil {
		t.Fatalf("Failed to find collection files: %v", err)
	}

	if len(collectionFiles) == 0 {
		t.Skipf("No Postman collection files found in %s directory", collectionsDir)
	}

	for _, collectionFile := range collectionFiles {
		collectionFile := collectionFile // Capture loop variable for subtest closure (future-proof for t.Parallel)
		t.Run(filepath.Base(collectionFile), func(t *testing.T) {
			runPostmanCollection(t, collectionFile)
		})
	}
}

// runPostmanCollection runs tests from a Postman collection
func runPostmanCollection(t *testing.T, collectionPath string) {
	// Read collection file
	//nolint:gosec // collectionPath is from controlled test directory
	data, err := os.ReadFile(
		collectionPath,
	)
	require.NoError(t, err, "Failed to read collection file")

	// Parse collection
	collection, err := ParsePostmanCollection(data)
	require.NoError(t, err, "Failed to parse Postman collection")

	// Process each item in the collection
	processCollectionItems(t, collection.Item, "")
}

// processCollectionItems recursively processes collection items
func processCollectionItems(t *testing.T, items []PostmanItem, prefix string) {
	for _, item := range items {
		item := item // Capture loop variable for subtest closure (future-proof for t.Parallel)
		testName := prefix + item.Name

		// If item has nested items, it's a folder
		if len(item.Item) > 0 {
			processCollectionItems(t, item.Item, testName+"/")
			continue
		}

		// If item has a request, it's a test case
		if item.Request.Method != "" {
			t.Run(testName, func(t *testing.T) {
				// Extract test case configuration from item
				// This assumes the item.response contains the ReportPortal mock config
				// and the item.request is the LLM client request

				// For now, we'll create a minimal test case
				// In a real implementation, you'd extract this from the collection structure
				t.Logf("Running test case: %s", testName)
				t.Skip("Postman collection test case extraction not fully implemented")
			})
		}
	}
}

// initializeMCPSession initializes an MCP session and returns the session ID.
// This function sends the MCP initialize handshake directly using its own HTTP client
// rather than going through LLMClientMock.SendRequest, since the initialize request
// doesn't use the PostmanRequest format.
func initializeMCPSession(ctx context.Context, mcpServerURL string) (string, error) {
	// Create initialize request
	initRequest := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"id":      0,
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "integration-test",
				"version": "1.0.0",
			},
		},
	}

	jsonData, err := json.Marshal(initRequest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal initialize request: %w", err)
	}

	// Send initialize request with dedicated HTTP client for MCP handshake
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		mcpServerURL+"/mcp",
		bytes.NewReader(jsonData),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: mcpInitializeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send initialize request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read initialize response: %w", err)
	}

	// Check HTTP status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf(
			"initialize request failed with status %d: %s",
			resp.StatusCode,
			string(bodyBytes),
		)
	}

	// Extract session ID from response header
	sessionID := resp.Header.Get("mcp-session-id")
	if sessionID == "" {
		return "", fmt.Errorf("no session ID in response headers, body: %s", string(bodyBytes))
	}

	return sessionID, nil
}
