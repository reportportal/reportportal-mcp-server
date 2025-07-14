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

func extractPaging(request mcp.CallToolRequest) (int32, int32) {
	// Extract the "page" parameter from the request
	page := request.GetInt("page", firstPage)
	if page > math.MaxInt32 {
		page = math.MaxInt32
	}

	// Extract the "page-size" parameter from the request
	pageSize := request.GetInt("page-size", defaultPageSize)
	if pageSize > math.MaxInt32 {
		pageSize = math.MaxInt32
	}

	//nolint:gosec // the int32 is confirmed
	return int32(page), int32(pageSize)
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
	if filterAttributes != "" {
		return filterAttributes + "," + result
	}
	return result
}
