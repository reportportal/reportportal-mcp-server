package mcpreportportal

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	firstPage                  = 1                       // Default starting page for pagination
	singleResult               = 1                       // Default number of results per page
	defaultPageSize            = 50                      // Default number of elements per page
	defaultSortingForLaunches  = "startTime,number,DESC" // default sorting order for launches
	defaultSortingForItems     = "startTime,DESC"        // default sorting order for items
	defaultSortingForSuites    = "startTime,ASC"         // default sorting order for suites
	defaultSortingForLogs      = "logTime,ASC"           // default sorting order for logs
	defaultProviderType        = "launch"                // default provider type
	defaultFilterEqHasChildren = "false"                 // items which don't have children
	defaultFilterEqHasStats    = "true"
	defaultFilterInType        = "STEP"
	defaultFilterInTypeSuites  = "SUITE,TEST"
	defaultItemLogLevel        = "TRACE" // Default log level for test item logs
)

// PaginatedRequest is a generic interface for API requests that support pagination
type PaginatedRequest[T any] interface {
	PagePage(int32) T
	PageSize(int32) T
	PageSort(string) T
}

// setPaginationOptions returns the standard pagination parameters for MCP tools
func setPaginationOptions(sortingParams string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("page", // Parameter for specifying the page number
			mcp.DefaultNumber(firstPage),
			mcp.Description("Page number"),
		),
		mcp.WithNumber("page-size", // Parameter for specifying the page size
			mcp.DefaultNumber(defaultPageSize),
			mcp.Description("Page size"),
		),
		mcp.WithString("page-sort", // Sorting fields and direction
			mcp.DefaultString(sortingParams),
			mcp.Description("Sorting fields and direction"),
		),
	}
}

// applyPaginationOptions extracts pagination from request and applies it to API request
func applyPaginationOptions[T PaginatedRequest[T]](
	apiRequest T,
	request mcp.CallToolRequest,
	sortingParams string,
) T {
	// Extract the "page" parameter from the request
	pageInt := request.GetInt("page", firstPage)
	if pageInt > math.MaxInt32 {
		pageInt = math.MaxInt32
	}

	// Extract the "page-size" parameter from the request
	pageSizeInt := request.GetInt("page-size", defaultPageSize)
	if pageSizeInt > math.MaxInt32 {
		pageSizeInt = math.MaxInt32
	}

	// Extract the "page-sort" parameter from the request
	pageSort := request.GetString("page-sort", sortingParams)

	// Apply pagination directly
	return apiRequest.
		PagePage(int32(pageInt)).     //nolint:gosec
		PageSize(int32(pageSizeInt)). //nolint:gosec
		PageSort(pageSort)
}

func newProjectParameter(defaultProject string) mcp.ToolOption {
	return mcp.WithString("project", // Parameter for specifying the project name)
		mcp.Description("Project name"),
		mcp.DefaultString(defaultProject),
		mcp.Required(),
	)
}

func extractProject(rq mcp.CallToolRequest) (string, error) {
	return rq.RequireString("project")
}

func extractResponseError(err error, rs *http.Response) (errText string) {
	errText = err.Error()
	if rs != nil && rs.Body != nil {
		defer func() {
			if closeErr := rs.Body.Close(); closeErr != nil {
				// Log the close error or handle it appropriately
				// You can add logging here if needed
				errText = errText + " (body close error: " + closeErr.Error() + ")"
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
		if epoch > 0 && epoch < 32503680000 { // roughly year 3000
			// If it looks like seconds, convert to milliseconds
			if epoch < 10000000000 { // less than year 2286 in seconds
				return epoch * 1000, nil
			}
			return epoch, nil
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
func processStartTimeFilter(filterStartTimeFrom, filterStartTimeTo string) (string, error) {
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

// processAttributeKeys processes attribute keys by adding ":" suffix where needed and combines with existing attributes
func processAttributeKeys(filterAttributes, filterAttributeKeys string) string {
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

func isTextContent(mediaType string) bool {
	lowerType := strings.ToLower(mediaType)

	// Text types (most common)
	if strings.HasPrefix(lowerType, "text/") {
		return true
	}

	// Popular application types that are text-based
	switch lowerType {
	case "application/json":
	case "application/xml":
	case "application/javascript":
	case "application/xhtml+xml":
	case "application/yaml":
	case "application/csv":
		return true
	}

	return false
}
