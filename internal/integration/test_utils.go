package integration

import (
	"bytes"
	"encoding/json"
	"strings"
)

// normalizeAndCompareJSON compares two strings as JSON, normalizing whitespace and field order.
// If either string is not valid JSON, it falls back to trimmed string comparison.
// Uses UseNumber() to preserve numeric precision for large integers.
func normalizeAndCompareJSON(actual, expected string) bool {
	// Try to parse as JSON and compare
	var actualJSON, expectedJSON interface{}

	// Use UseNumber() to avoid float64 precision loss for large integers
	actualDecoder := json.NewDecoder(strings.NewReader(actual))
	actualDecoder.UseNumber()
	if err := actualDecoder.Decode(&actualJSON); err != nil {
		// Not JSON, do string comparison
		return strings.TrimSpace(actual) == strings.TrimSpace(expected)
	}

	expectedDecoder := json.NewDecoder(strings.NewReader(expected))
	expectedDecoder.UseNumber()
	if err := expectedDecoder.Decode(&expectedJSON); err != nil {
		// Expected is not JSON, do string comparison
		return strings.TrimSpace(actual) == strings.TrimSpace(expected)
	}

	// Both are JSON, compare normalized
	actualBytes, err := json.Marshal(actualJSON)
	if err != nil {
		return false
	}
	expectedBytes, err := json.Marshal(expectedJSON)
	if err != nil {
		return false
	}

	// Use bytes.Equal for efficient byte slice comparison
	return bytes.Equal(actualBytes, expectedBytes)
}
