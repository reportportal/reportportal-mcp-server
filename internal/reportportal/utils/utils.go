package utils

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	FirstPage                  = 1                       // Default starting page for pagination
	SingleResult               = 1                       // Default number of results per page
	DefaultPageSize            = 50                      // Default number of elements per page
	DefaultSortingForLaunches  = "startTime,number,DESC" // default sorting order for launches
	DefaultSortingForItems     = "startTime,DESC"        // default sorting order for items
	DefaultSortingForSuites    = "startTime,ASC"         // default sorting order for suites
	DefaultSortingForLogs      = "logTime,ASC"           // default sorting order for logs
	DefaultProviderType        = "launch"                // default provider type
	DefaultFilterEqHasChildren = "false"                 // items which don't have children
	DefaultFilterEqHasStats    = "true"
	DefaultFilterInType        = "STEP"
	DefaultFilterInTypeSuites  = "SUITE,TEST"
	AllFilterInTypes           = "BEFORE_SUITE,BEFORE_GROUPS,BEFORE_CLASS,BEFORE_TEST,TEST,BEFORE_METHOD,STEP,AFTER_METHOD,AFTER_TEST,AFTER_CLASS,AFTER_GROUPS,AFTER_SUITE"
	DefaultItemLogLevel        = "TRACE" // Default log level for test item logs
)

// PaginatedRequest is a generic interface for API requests that support pagination
type PaginatedRequest[T any] interface {
	PagePage(int32) T
	PageSize(int32) T
	PageSort(string) T
}

// SetPaginationProperties returns the standard pagination properties for JSON Schema.
func SetPaginationProperties(sortingParams string) map[string]*jsonschema.Schema {
	// Helper to create JSON default values
	intDefault := func(v int) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}
	stringDefault := func(v string) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}

	return map[string]*jsonschema.Schema{
		"page": {
			Type:        "integer",
			Description: "Page number",
			Default:     intDefault(FirstPage),
		},
		"page-size": {
			Type:        "integer",
			Description: "Page size",
			Default:     intDefault(DefaultPageSize),
		},
		"page-sort": {
			Type:        "string",
			Description: "Sorting fields and direction",
			Default:     stringDefault(sortingParams),
		},
	}
}

func ExtractResponseError(err error, rs *http.Response) (errText string) {
	errText = err.Error()
	if rs != nil && rs.Body != nil {
		// Check if the original error indicates the body is already closed
		if isAlreadyClosedError(err) {
			// Don't attempt to read from an already-closed body
			return errText + " (response body already processed)"
		}

		defer func() {
			if closeErr := rs.Body.Close(); closeErr != nil {
				// Only log close errors if it's not an already-closed body
				if !isAlreadyClosedError(closeErr) {
					errText = errText + " (body close error: " + closeErr.Error() + ")"
				}
			}
		}()

		if errContent, rErr := io.ReadAll(rs.Body); rErr == nil {
			errText = errText + ": " + string(errContent)
		} else {
			errText = errText + " (read error: " + rErr.Error() + ")"
		}
	}
	return errText
}

// Helper function to parse timestamp to Unix epoch
func parseTimestampToEpoch(timestampStr string) (int64, error) {
	if timestampStr == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	// Try parsing as Unix epoch first (if it's all digits)
	if epoch, err := strconv.ParseInt(timestampStr, 10, 64); err == nil {
		// If it's a reasonable Unix timestamp (after 1970 and before year 3000)
		if epoch > 0 { // roughly year 3000
			// If it looks like seconds, convert to milliseconds
			if epoch < 10000000000 { // less than year 2286 in seconds
				return epoch * 1000, nil
			}
			// Assume milliseconds for larger values
			if epoch < 32503680000000 { // roughly year 3000 in milliseconds
				return epoch, nil
			}
		}
	}
	// Try parsing as RFC3339 format
	if t, err := time.Parse(time.RFC3339, timestampStr); err == nil {
		return t.UnixMilli(), nil
	}
	// Try parsing as other common formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, timestampStr); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("unable to parse timestamp: %s", timestampStr)
}

// processStartTimeFilter processes start time interval filter and returns the formatted filter string
func ProcessStartTimeFilter(filterStartTimeFrom, filterStartTimeTo string) (string, error) {
	// Process start time interval filter
	var filterStartTime string
	if filterStartTimeFrom != "" && filterStartTimeTo != "" {
		fromEpoch, err := parseTimestampToEpoch(filterStartTimeFrom)
		if err != nil {
			return "", fmt.Errorf("invalid from timestamp: %v", err)
		}
		toEpoch, err := parseTimestampToEpoch(filterStartTimeTo)
		if err != nil {
			return "", fmt.Errorf("invalid to timestamp: %v", err)
		}
		if fromEpoch >= toEpoch {
			return "", fmt.Errorf("from timestamp must be earlier than to timestamp")
		}
		// Format as comma-separated values for ReportPortal API
		filterStartTime = fmt.Sprintf("%d,%d", fromEpoch, toEpoch)
	} else if filterStartTimeFrom != "" || filterStartTimeTo != "" {
		return "", fmt.Errorf("both from and to timestamps are required for time interval filter")
	}

	return filterStartTime, nil
}

// ProcessAttributeKeys processes attribute keys by adding ":" suffix where needed
// and combines them with existing attributes.
func ProcessAttributeKeys(filterAttributes, filterAttributeKeys string) string {
	if filterAttributeKeys == "" {
		return filterAttributes
	}

	var processed []string
	for _, key := range strings.Split(filterAttributeKeys, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		if colonIndex := strings.Index(key, ":"); colonIndex > 0 && colonIndex < len(key)-1 {
			processed = append(processed, key[colonIndex+1:]) // Extract postfix
		} else if !strings.HasSuffix(key, ":") {
			processed = append(processed, key+":") // Add suffix
		} else {
			processed = append(processed, key) // Keep as is
		}
	}

	result := strings.Join(processed, ",")
	if filterAttributes != "" && result != "" {
		return filterAttributes + "," + result
	} else if filterAttributes != "" {
		return filterAttributes
	}
	return result
}

func IsTextContent(mediaType string) bool {
	lowerType := strings.ToLower(mediaType)

	// Text types (most common)
	if strings.HasPrefix(lowerType, "text/") {
		return true
	}

	// Popular application types that are text-based
	switch lowerType {
	case "application/json",
		"application/xml",
		"application/javascript",
		"application/xhtml+xml",
		"application/yaml",
		"application/csv":
		return true
	}

	return false
}

// isAlreadyClosedError checks if the error indicates that the response body is already closed.
// This helps avoid unnecessary error logging when closing an already-closed body.
func isAlreadyClosedError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Common error messages for already-closed bodies across different HTTP implementations
	return strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "http: read on closed response body") ||
		strings.Contains(errStr, "already closed") ||
		strings.Contains(errStr, "connection closed")
}

// readResponseBodyRaw safely reads an HTTP response body and ensures proper cleanup.
// It returns the raw body bytes along with any error, suitable for custom content type handling.
func ReadResponseBodyRaw(response *http.Response) ([]byte, error) {
	// Ensure response body is always closed
	if response == nil || response.Body == nil {
		return nil, fmt.Errorf("empty HTTP response body")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			// Only log if it's not a "already closed" type error
			// Some HTTP implementations return specific errors for already-closed bodies
			if !isAlreadyClosedError(closeErr) {
				slog.Error("failed to close response body", "error", closeErr)
			}
		}
	}()

	// Read the response body
	rawBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	return rawBody, nil
}

// ReadResponseBody safely reads an HTTP response body and returns the result as an MCP tool result.
//
// IMPORTANT CONTRACT: This function encodes all read/processing failures in the returned
// mcp.CallToolResult structure and does NOT return them via the error return value.
//
// Return values:
//   - *mcp.CallToolResult: Always non-nil. On success, contains the response body as text content.
//     On failure, IsError is set to true and Content contains the error message.
//   - any: Always nil (reserved for future use).
//   - error: Always nil. Failures are reported via CallToolResult.IsError and CallToolResult.Content.
//
// Callers should check result.IsError to determine success/failure, NOT the error return value.
func ReadResponseBody(response *http.Response) (*mcp.CallToolResult, any, error) {
	rawBody, err := ReadResponseBodyRaw(response)
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("failed to read response body: %v", err)},
			},
			IsError: true,
		}, nil, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(rawBody)}},
	}, nil, nil
}

// ParseReportPortalURI parses a ReportPortal URI of the form "reportportal://{part0}/{expectedSegment}/{part2}"
// and extracts the first and third path segments, validating the structure.
//
// Parameters:
//   - uri: The full URI to parse (e.g., "reportportal://myproject/testitem/123")
//   - expectedSegment: The expected middle segment (e.g., "testitem", "launch")
//
// Returns:
//   - part0: The first path segment (typically the project name)
//   - part2: The third path segment (typically an ID)
//   - err: Error if the URI format is invalid
func ParseReportPortalURI(uri, expectedSegment string) (part0, part2 string, err error) {
	// Expected format: reportportal://{part0}/{expectedSegment}/{part2}
	if len(uri) < 15 || uri[:15] != "reportportal://" {
		return "", "", fmt.Errorf("invalid URI format: %s", uri)
	}

	// Remove the scheme
	path := uri[15:]

	// Split the path into parts
	parts := []string{}
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}

	// Validate format: {part0}/{expectedSegment}/{part2}
	if len(parts) != 3 || parts[1] != expectedSegment {
		return "", "", fmt.Errorf(
			"invalid URI format, expected reportportal://{part0}/%s/{part2}: %s",
			expectedSegment,
			uri,
		)
	}

	part0 = parts[0]
	part2 = parts[2]

	if part0 == "" {
		return "", "", fmt.Errorf("missing first segment in URI: %s", uri)
	}
	if part2 == "" {
		return "", "", fmt.Errorf("missing third segment in URI: %s", uri)
	}

	return part0, part2, nil
}
