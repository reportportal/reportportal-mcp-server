package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"

	"github.com/reportportal/reportportal-mcp-server/internal/testdata"
)

const (
	// httpTimeout is the default timeout for HTTP requests
	httpTimeout = 30 * time.Second
	// overallTimeout is the maximum time allowed for the entire verification process
	overallTimeout = 5 * time.Minute
)

// ErrSkipped is a sentinel error indicating a test case was skipped
var ErrSkipped = errors.New("test skipped")

// MCPResponse represents a JSON-RPC response from MCP server
type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  *MCPResult  `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPResult represents the result field in MCP response
type MCPResult struct {
	Content []MCPContent `json:"content,omitempty"`
	IsError bool         `json:"isError,omitempty"`
}

// MCPContent represents content in MCP result
type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCPError represents an error in MCP response
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// InitializeRequest represents MCP initialize request
type InitializeRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	ID      int                    `json:"id"`
	Params  map[string]interface{} `json:"params"`
}

// JSONRPCRequest represents a generic JSON-RPC 2.0 request for validation
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	ID      interface{} `json:"id"`
	Params  interface{} `json:"params,omitempty"`
}

var (
	mcpServerURL = flag.String("url", "http://localhost:8080/mcp", "MCP server URL")
	testDataDir  = flag.String(
		"dir",
		"testdata",
		"Test data directory (searched recursively for .json files)",
	)
	verbose = flag.Bool("v", false, "Verbose output")

	// httpClient is a shared HTTP client with timeout for all requests
	httpClient = &http.Client{Timeout: httpTimeout}
)

// normalizeMCPURL ensures the URL points to a valid MCP endpoint.
// If the URL doesn't end with /mcp or /api/mcp, it appends /mcp.
// This prevents requests from going to the wrong endpoint when users provide base URLs.
func normalizeMCPURL(rawURL string) string {
	// Parse the URL to handle it properly
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, fall back to simple string manipulation
		urlStr := strings.TrimRight(rawURL, "/")
		if strings.HasSuffix(urlStr, "/mcp") || strings.HasSuffix(urlStr, "/api/mcp") {
			return urlStr
		}
		return urlStr + "/mcp"
	}

	// Trim all trailing slashes from the path
	parsedURL.Path = strings.TrimRight(parsedURL.Path, "/")

	// Check if URL path already ends with a valid MCP endpoint
	if strings.HasSuffix(parsedURL.Path, "/mcp") || strings.HasSuffix(parsedURL.Path, "/api/mcp") {
		return parsedURL.String()
	}

	// Append /mcp to ensure requests go to the correct endpoint
	parsedURL.Path += "/mcp"
	return parsedURL.String()
}

func main() {
	flag.Parse()

	// Color setup
	cyan := color.New(color.FgCyan, color.Bold)
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	yellow := color.New(color.FgYellow)

	// Check required environment variables
	rpToken := os.Getenv("RP_API_TOKEN")
	rpProject := os.Getenv("RP_PROJECT")

	if rpToken == "" {
		_, _ = red.Println("Error: RP_API_TOKEN environment variable is required")
		os.Exit(1)
	}

	if rpProject == "" {
		_, _ = red.Println("Error: RP_PROJECT environment variable is required")
		os.Exit(1)
	}

	// Normalize MCP server URL to ensure it points to the correct endpoint
	normalizedURL := normalizeMCPURL(*mcpServerURL)

	_, _ = cyan.Printf("==> Verifying test fixtures against MCP server at %s\n", normalizedURL)
	_, _ = cyan.Printf("    Using project: %s\n\n", rpProject)

	// Step 1: Initialize MCP session
	_, _ = yellow.Println("[1/3] Initializing MCP session...")

	// Create parent context with overall timeout to bound the entire verification process
	parentCtx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	// Create per-request context for initialization (bounded by both httpTimeout and parentCtx)
	initCtx, initCancel := context.WithTimeout(parentCtx, httpTimeout)
	defer initCancel()

	sessionID, err := initializeMCPSession(initCtx, normalizedURL, rpToken, rpProject)
	if err != nil {
		_, _ = red.Printf("Failed to initialize MCP session: %v\n", err)
		os.Exit(1)
	}

	_, _ = green.Printf("    Session ID: %s\n", sessionID)

	// Step 2: Discover test files
	_, _ = yellow.Println("\n[2/3] Discovering test fixtures...")

	testFiles, err := discoverTestFiles(*testDataDir)
	if err != nil {
		_, _ = red.Printf("Failed to discover test files: %v\n", err)
		os.Exit(1)
	}

	if len(testFiles) == 0 {
		_, _ = yellow.Printf("No test files found in %s\n", *testDataDir)
		os.Exit(0)
	}

	_, _ = green.Printf("    Found %d test file(s)\n", len(testFiles))

	// Step 3: Verify each test case
	_, _ = yellow.Println("\n[3/3] Verifying test cases...")

	results := struct {
		Total   int
		Success int
		Failed  int
		Skipped int
	}{}

	for _, testFile := range testFiles {
		results.Total++
		_, _ = cyan.Printf("\n  Testing: %s\n", filepath.Base(testFile))

		// Create per-request context (bounded by both httpTimeout and parentCtx)
		// This ensures each test gets its own timeout, not "whatever time is left"
		testCtx, testCancel := context.WithTimeout(parentCtx, httpTimeout)
		success, err := verifyTestCase(
			testCtx,
			testFile,
			normalizedURL,
			sessionID,
			rpToken,
			rpProject,
		)
		testCancel() // Clean up context resources immediately after request

		if err != nil {
			if errors.Is(err, ErrSkipped) {
				_, _ = yellow.Printf("    ⚠ Skipped: %v\n", err)
				results.Skipped++
			} else {
				_, _ = red.Printf("    ✗ Failed: %v\n", err)
				results.Failed++
			}
			continue
		}

		if success {
			_, _ = green.Println("    ✓ Passed: Received valid response from MCP server")
			results.Success++
		} else {
			_, _ = red.Println("    ✗ Failed: Tool execution returned error")
			results.Failed++
		}
	}

	// Summary
	_, _ = cyan.Println("\n" + strings.Repeat("=", 60))
	_, _ = cyan.Println("Verification Summary:")
	_, _ = cyan.Println(strings.Repeat("=", 60))
	fmt.Printf("  Total:   %d\n", results.Total)
	_, _ = green.Printf("  Success: %d\n", results.Success)
	if results.Failed > 0 {
		_, _ = red.Printf("  Failed:  %d\n", results.Failed)
	} else {
		fmt.Printf("  Failed:  %d\n", results.Failed)
	}
	_, _ = yellow.Printf("  Skipped: %d\n", results.Skipped)

	if results.Failed > 0 {
		_, _ = red.Println("\n⚠ Some tests failed. Check the output above for details.")
		os.Exit(1)
	} else {
		_, _ = green.Println("\n✓ All tests passed!")
		os.Exit(0)
	}
}

// initializeMCPSession initializes an MCP session and returns the session ID
func initializeMCPSession(ctx context.Context, serverURL, token, project string) (string, error) {
	initReq := InitializeRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		ID:      0,
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "testdata-verifier",
				"version": "1.0.0",
			},
		},
	}

	body, err := json.Marshal(initReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal initialize request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", serverURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Project", project)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body for debugging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check HTTP status code - accept any 2xx status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf(
			"initialize failed with status %d: %s",
			resp.StatusCode,
			string(bodyBytes),
		)
	}

	sessionID := resp.Header.Get("mcp-session-id")
	if sessionID == "" {
		return "", fmt.Errorf("no mcp-session-id in response headers, body: %s", string(bodyBytes))
	}

	return sessionID, nil
}

// discoverTestFiles finds all JSON test files in the directory (recursively)
func discoverTestFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only include .json files
		if !info.IsDir() && filepath.Ext(path) == ".json" {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	return files, nil
}

// validateResponse validates the actual MCP response against the expected response
func validateResponse(
	actual *MCPResponse,
	actualStatusCode int,
	expected *testdata.PostmanResponse,
) error {
	// Validate HTTP status code if expected
	if expected.Code != 0 && actualStatusCode != expected.Code {
		return fmt.Errorf(
			"status code mismatch: expected %d, got %d",
			expected.Code,
			actualStatusCode,
		)
	}

	// If there's no expected response body, skip body validation
	if expected.Body == "" {
		return nil
	}

	// Marshal the actual MCP response to JSON for comparison
	actualBytes, err := json.Marshal(actual)
	if err != nil {
		return fmt.Errorf("failed to marshal actual response: %w", err)
	}

	// Parse both as generic JSON for structural comparison
	var expectedJSON, actualJSON interface{}

	if err := json.Unmarshal([]byte(expected.Body), &expectedJSON); err != nil {
		return fmt.Errorf("failed to parse expected response as JSON: %w", err)
	}

	if err := json.Unmarshal(actualBytes, &actualJSON); err != nil {
		return fmt.Errorf("failed to parse actual response as JSON: %w", err)
	}

	// Normalize nested JSON strings in content[].text fields
	normalizeContentText(expectedJSON)
	normalizeContentText(actualJSON)

	// Compare JSON structures using normalized indented output
	expectedNormalized, err := json.MarshalIndent(expectedJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal expected JSON for comparison: %w", err)
	}

	actualNormalized, err := json.MarshalIndent(actualJSON, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal actual JSON for comparison: %w", err)
	}

	if string(expectedNormalized) != string(actualNormalized) {
		return fmt.Errorf(
			"response JSON mismatch:\n  expected:\n%s\n  actual:\n%s",
			string(expectedNormalized),
			string(actualNormalized),
		)
	}

	return nil
}

// normalizeContentText normalizes JSON strings within MCP content[].text fields
// This handles cases where the text field contains JSON that may have different formatting
func normalizeContentText(obj interface{}) {
	switch v := obj.(type) {
	case map[string]interface{}:
		// Check if this is a result.content array
		if result, ok := v["result"].(map[string]interface{}); ok {
			if content, ok := result["content"].([]interface{}); ok {
				for _, item := range content {
					if contentItem, ok := item.(map[string]interface{}); ok {
						if text, ok := contentItem["text"].(string); ok {
							// Try to parse the text as JSON
							var textJSON interface{}
							if err := json.Unmarshal([]byte(text), &textJSON); err == nil {
								// If it's valid JSON, normalize it and update
								normalized, err := json.Marshal(textJSON)
								if err != nil {
									fmt.Printf("Warning: failed to marshal normalized JSON content: %v\n", err)
								} else {
									contentItem["text"] = string(normalized)
								}
							}
						}
					}
				}
			}
		}
		// Recursively process nested objects
		for _, val := range v {
			normalizeContentText(val)
		}
	case []interface{}:
		for _, item := range v {
			normalizeContentText(item)
		}
	}
}

// validateJSONRPCRequest validates that the raw body is a valid JSON-RPC 2.0 request
func validateJSONRPCRequest(rawBody string) error {
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(rawBody), &req); err != nil {
		return fmt.Errorf("request body is not valid JSON: %w", err)
	}

	// Validate JSON-RPC version
	if req.JSONRPC != "2.0" {
		return fmt.Errorf("invalid or missing jsonrpc field: expected \"2.0\", got %q", req.JSONRPC)
	}

	// Validate method field
	if req.Method == "" {
		return fmt.Errorf("missing required field: method")
	}

	// Validate required fields presence (must be present in JSON, even if null/empty)
	// For MCP JSON-RPC requests, all four fields (jsonrpc, method, id, params) are required
	var rawJSON map[string]interface{}
	if err := json.Unmarshal([]byte(rawBody), &rawJSON); err != nil {
		return fmt.Errorf("failed to parse JSON for field validation: %w", err)
	}

	if _, hasID := rawJSON["id"]; !hasID {
		return fmt.Errorf("missing required field: id (must be string, number, or null)")
	}

	if _, hasParams := rawJSON["params"]; !hasParams {
		return fmt.Errorf("missing required field: params (must be object, array, or null)")
	}

	return nil
}

// verifyTestCase verifies a single test case against the MCP server
func verifyTestCase(
	ctx context.Context,
	testFile, serverURL, sessionID, token, project string,
) (bool, error) {
	// Read and parse test case
	data, err := os.ReadFile(testFile) //nolint:gosec // testFile is from controlled test directory
	if err != nil {
		return false, fmt.Errorf("failed to read file: %w", err)
	}

	testCase, err := testdata.ParseTestCase(data)
	if err != nil {
		return false, fmt.Errorf("failed to parse test case: %w", err)
	}

	// Check if test case has required fields
	if testCase.LLMClientMock.Request.Body == nil || testCase.LLMClientMock.Request.Body.Raw == "" {
		return false, fmt.Errorf("no request body found: %w", ErrSkipped)
	}

	// Validate JSON-RPC 2.0 structure before sending
	if err := validateJSONRPCRequest(testCase.LLMClientMock.Request.Body.Raw); err != nil {
		return false, fmt.Errorf("invalid JSON-RPC request in fixture: %w", err)
	}

	// Build request
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		serverURL,
		strings.NewReader(testCase.LLMClientMock.Request.Body.Raw),
	)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Project", project)
	req.Header.Set("mcp-session-id", sessionID)

	// Send request
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	if *verbose {
		fmt.Printf("      Response: %s\n", string(bodyBytes))
	}

	// Parse MCP response
	var mcpResp MCPResponse
	if err := json.Unmarshal(bodyBytes, &mcpResp); err != nil {
		return false, fmt.Errorf("failed to parse MCP response: %w", err)
	}

	// Check for errors
	if mcpResp.Error != nil {
		return false, fmt.Errorf("MCP error: %s", mcpResp.Error.Message)
	}

	if mcpResp.Result != nil && mcpResp.Result.IsError {
		if len(mcpResp.Result.Content) > 0 {
			fmt.Printf(
				"      %s\n",
				color.New(color.FgHiBlack).Sprint(mcpResp.Result.Content[0].Text),
			)
		}
		return false, nil
	}

	// Validate response against expected response
	if err := validateResponse(&mcpResp, resp.StatusCode, &testCase.LLMClientMock.ExpectedResponse); err != nil {
		return false, fmt.Errorf("response validation failed: %w", err)
	}

	return true, nil
}
