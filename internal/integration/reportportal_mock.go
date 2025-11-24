package integration

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
)

// ReportPortalMockServer is a mock HTTP server that simulates ReportPortal API
type ReportPortalMockServer struct {
	server       *httptest.Server
	requestPairs []RequestResponsePair
	mutex        sync.RWMutex
	requestLog   []MockRequestLog
	matchedCount int // tracks successfully matched requests
}

// MockRequestLog logs incoming requests for debugging
type MockRequestLog struct {
	Method      string
	Path        string
	QueryParams map[string][]string
	Headers     map[string][]string
	Body        string
	Matched     bool // indicates if this request was successfully matched
}

// NewReportPortalMockServer creates a new ReportPortal mock server
func NewReportPortalMockServer(requestPairs []RequestResponsePair) *ReportPortalMockServer {
	mock := &ReportPortalMockServer{
		requestPairs: requestPairs,
		requestLog:   make([]MockRequestLog, 0),
	}

	mock.server = httptest.NewServer(http.HandlerFunc(mock.handleRequest))

	return mock
}

// URL returns the base URL of the mock server
func (m *ReportPortalMockServer) URL() string {
	return m.server.URL
}

// Close shuts down the mock server
func (m *ReportPortalMockServer) Close() {
	m.server.Close()
}

// GetRequestLog returns a copy of the log of all requests received
func (m *ReportPortalMockServer) GetRequestLog() []MockRequestLog {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	// Return a copy to prevent data races
	logCopy := make([]MockRequestLog, len(m.requestLog))
	copy(logCopy, m.requestLog)
	return logCopy
}

// GetMatchedCount returns the number of successfully matched requests
func (m *ReportPortalMockServer) GetMatchedCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.matchedCount
}

// ClearRequestLog clears the request log
func (m *ReportPortalMockServer) ClearRequestLog() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.requestLog = make([]MockRequestLog, 0)
	m.matchedCount = 0
}

// handleRequest handles incoming HTTP requests and matches them to predefined responses
func (m *ReportPortalMockServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	// Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Log the request as unmatched initially and capture its index
	logIndex := m.logRequest(r, string(bodyBytes), false)

	// Find matching request/response pair
	m.mutex.RLock()
	slog.Debug("Requesting pairs", "pairs", m.requestPairs)
	var matchedPair *RequestResponsePair
	for i, pair := range m.requestPairs {
		slog.Debug(
			"Trying to match request",
			"pairIndex",
			i,
			"method",
			r.Method,
			"path",
			r.URL.Path,
			"expectedMethod",
			pair.Request.Method,
			"expectedPath",
			m.buildPath(pair.Request.URL),
		)
		if m.matchesRequest(r, string(bodyBytes), pair.Request) {
			slog.Debug("Request matched", "pairIndex", i)
			pairCopy := pair
			matchedPair = &pairCopy
			break
		}
		slog.Debug("Request did not match", "pairIndex", i)
	}
	// Release the read lock now that we're done reading requestPairs
	m.mutex.RUnlock()

	// If we found a match, mark it and write the response
	if matchedPair != nil {
		// Mark the specific logged request as matched using its index
		m.markRequestAsMatched(logIndex)

		// Write response headers
		for _, header := range matchedPair.Response.Header {
			if !header.Disabled {
				w.Header().Set(header.Key, header.Value)
			}
		}

		// Set status code
		statusCode := matchedPair.Response.Code
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		w.WriteHeader(statusCode)

		// Write response body
		if matchedPair.Response.Body != "" {
			_, err := w.Write([]byte(matchedPair.Response.Body))
			if err != nil {
				slog.Error("Failed to write response body", "error", err)
			}
		}

		return
	}

	// No matching request found
	slog.Warn(
		"No matching request found",
		"method",
		r.Method,
		"path",
		r.URL.Path,
		"query",
		r.URL.RawQuery,
	)
	http.Error(w, "No matching mock response found", http.StatusNotFound)
}

// logRequest logs the incoming request and returns its index
func (m *ReportPortalMockServer) logRequest(r *http.Request, body string, matched bool) int {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	headers := make(map[string][]string)
	for key, values := range r.Header {
		headers[key] = values
	}

	queryParams := make(map[string][]string)
	for key, values := range r.URL.Query() {
		queryParams[key] = values
	}

	m.requestLog = append(m.requestLog, MockRequestLog{
		Method:      r.Method,
		Path:        r.URL.Path,
		QueryParams: queryParams,
		Headers:     headers,
		Body:        body,
		Matched:     matched,
	})

	return len(m.requestLog) - 1
}

// markRequestAsMatched marks a specific logged request as matched by index
func (m *ReportPortalMockServer) markRequestAsMatched(index int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if index >= 0 && index < len(m.requestLog) {
		m.requestLog[index].Matched = true
		m.matchedCount++
	}
}

// matchesRequest checks if the incoming request matches the expected request
func (m *ReportPortalMockServer) matchesRequest(
	r *http.Request,
	body string,
	expected PostmanRequest,
) bool {
	// Check HTTP method
	if !strings.EqualFold(expected.Method, r.Method) {
		slog.Debug("Method mismatch", "expected", expected.Method, "got", r.Method)
		return false
	}

	// Check URL path
	expectedPath := m.buildPath(expected.URL)
	if expectedPath != r.URL.Path {
		slog.Debug("Path mismatch", "expected", expectedPath, "got", r.URL.Path)
		return false
	}

	// Check query parameters
	if !m.matchesQueryParams(r, expected.URL.Query) {
		return false
	}

	// Check headers
	if !m.matchesHeaders(r, expected.Header) {
		return false
	}

	// Check body (if present)
	// NOTE: This implementation only supports matching "raw" body mode (JSON payloads).
	// Other body modes (urlencoded, formdata, etc.) are not currently matched.
	//
	// This limitation is acceptable because:
	// - The ReportPortal client library (goRP) communicates exclusively using JSON payloads
	// - All ReportPortal API endpoints expect and return JSON
	//
	// If future test cases require matching non-raw body modes for ReportPortal requests,
	// this logic must be extended to handle:
	// - expected.Body.URLEncoded: Compare form-urlencoded key-value pairs
	// - expected.Body.FormData: Compare multipart form data key-value pairs
	// - Other modes as needed
	if expected.Body != nil && expected.Body.Raw != "" {
		// Normalize JSON for comparison
		if !m.matchesBody(body, expected.Body.Raw) {
			return false
		}
	}

	return true
}

// buildPath constructs the path from PostmanURL
func (m *ReportPortalMockServer) buildPath(url PostmanURL) string {
	// If path segments are provided, use them (more reliable)
	if len(url.Path) > 0 {
		return "/" + strings.Join(url.Path, "/")
	}

	// Otherwise, extract from raw URL using url.Parse for robust parsing
	if url.Raw != "" {
		parsedURL, err := neturl.Parse(url.Raw)
		if err != nil {
			// Fall back to "/" if parsing fails
			return "/"
		}
		if parsedURL.Path == "" {
			return "/"
		}
		return parsedURL.Path
	}

	return "/"
}

// matchesQueryParams checks if query parameters match
func (m *ReportPortalMockServer) matchesQueryParams(
	r *http.Request,
	expected []PostmanQueryParam,
) bool {
	if len(expected) == 0 {
		return true
	}

	for _, param := range expected {
		if param.Disabled {
			continue
		}
		values, ok := r.URL.Query()[param.Key]
		if !ok || len(values) == 0 {
			slog.Debug(
				"Query parameter missing",
				"key",
				param.Key,
				"expected",
				param.Value,
			)
			return false
		}
		// Check if any value matches
		found := false
		for _, value := range values {
			if value == param.Value {
				found = true
				break
			}
		}
		if !found {
			slog.Debug(
				"Query parameter value mismatch",
				"key",
				param.Key,
				"expected",
				param.Value,
				"actual",
				values,
			)
			return false
		}
	}

	return true
}

// matchesHeaders checks if headers match
func (m *ReportPortalMockServer) matchesHeaders(r *http.Request, expected []PostmanHeader) bool {
	if len(expected) == 0 {
		return true
	}

	for _, header := range expected {
		if header.Disabled {
			continue
		}
		value := r.Header.Get(header.Key)
		if value != header.Value {
			slog.Debug(
				"Header mismatch",
				"key",
				header.Key,
				"expected",
				header.Value,
				"actual",
				value,
			)
			return false
		}
	}

	return true
}

// matchesBody checks if request body matches (with JSON normalization)
func (m *ReportPortalMockServer) matchesBody(actual, expected string) bool {
	return normalizeAndCompareJSON(actual, expected)
}

// AddRequestPair adds a new request/response pair dynamically
func (m *ReportPortalMockServer) AddRequestPair(pair RequestResponsePair) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.requestPairs = append(m.requestPairs, pair)
}

// Reset resets the mock server with new request pairs
func (m *ReportPortalMockServer) Reset(requestPairs []RequestResponsePair) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.requestPairs = requestPairs
	m.requestLog = make([]MockRequestLog, 0)
	m.matchedCount = 0
}
