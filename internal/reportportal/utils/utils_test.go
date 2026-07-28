package utils

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ms(layout, value string) int64 {
	t, err := time.Parse(layout, value)
	if err != nil {
		panic("ms: bad fixture: " + err.Error())
	}
	return t.UnixMilli()
}

func TestParseTimestampToEpoch(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMs    int64
		wantError bool
	}{
		// Unix epoch – seconds
		{
			name:   "unix epoch seconds",
			input:  "1704110400",
			wantMs: 1704110400 * 1000,
		},
		// Unix epoch – milliseconds
		{
			name:   "unix epoch milliseconds",
			input:  "1704110400000",
			wantMs: 1704110400000,
		},
		// RFC3339 with Z
		{
			name:   "RFC3339 Z offset",
			input:  "2024-01-01T12:00:00Z",
			wantMs: ms(time.RFC3339, "2024-01-01T12:00:00Z"),
		},
		// RFC3339 with colon offset
		{
			name:   "RFC3339 colon offset +05:30",
			input:  "2024-01-01T12:00:00+05:30",
			wantMs: ms(time.RFC3339, "2024-01-01T12:00:00+05:30"),
		},
		// RFC3339Nano with Z
		{
			name:   "RFC3339Nano sub-second Z",
			input:  "2024-01-01T12:00:00.123Z",
			wantMs: ms(time.RFC3339Nano, "2024-01-01T12:00:00.123Z"),
		},
		// ISO8601 colon-less offset – the "Date Bug" cases from issue #98
		{
			name:   "ISO8601 no-colon offset +0000",
			input:  "2024-01-01T12:00:00+0000",
			wantMs: ms(time.RFC3339, "2024-01-01T12:00:00Z"),
		},
		{
			name:   "ISO8601 no-colon offset -0500",
			input:  "2024-01-01T12:00:00-0500",
			wantMs: ms(time.RFC3339, "2024-01-01T12:00:00-05:00"),
		},
		{
			name:   "ISO8601 no-colon offset with milliseconds",
			input:  "2024-01-01T12:00:00.456+0000",
			wantMs: ms(time.RFC3339Nano, "2024-01-01T12:00:00.456Z"),
		},
		{
			name:   "ISO8601 no-colon offset with milliseconds negative zone",
			input:  "2024-06-15T08:30:00.000-0300",
			wantMs: ms(time.RFC3339Nano, "2024-06-15T08:30:00.000-03:00"),
		},
		// Timezone-less formats
		{
			name:   "datetime without timezone",
			input:  "2024-01-01T12:00:00",
			wantMs: ms("2006-01-02T15:04:05", "2024-01-01T12:00:00"),
		},
		{
			name:   "datetime with space separator",
			input:  "2024-01-01 12:00:00",
			wantMs: ms("2006-01-02 15:04:05", "2024-01-01 12:00:00"),
		},
		{
			name:   "date only",
			input:  "2024-01-01",
			wantMs: ms("2006-01-02", "2024-01-01"),
		},
		// Error cases
		{
			name:      "empty string",
			input:     "",
			wantError: true,
		},
		{
			name:      "invalid string",
			input:     "not-a-date",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTimestampToEpoch(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf(
						"parseTimestampToEpoch(%q) expected error, got nil (result=%d)",
						tt.input,
						got,
					)
				}
				return
			}
			if err != nil {
				t.Errorf("parseTimestampToEpoch(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.wantMs {
				t.Errorf("parseTimestampToEpoch(%q) = %d, want %d (diff=%d ms)",
					tt.input, got, tt.wantMs, got-tt.wantMs)
			}
		})
	}
}

func TestProcessAttributeKeys(t *testing.T) {
	tests := []struct {
		name                string
		filterAttributes    string
		filterAttributeKeys string
		expected            string
	}{
		// Basic cases
		{
			name:                "empty filterAttributeKeys returns filterAttributes",
			filterAttributes:    "existing",
			filterAttributeKeys: "",
			expected:            "existing",
		},
		{
			name:                "empty filterAttributes and filterAttributeKeys returns empty",
			filterAttributes:    "",
			filterAttributeKeys: "",
			expected:            "",
		},

		// Single key cases
		{
			name:                "single key without colon gets colon suffix",
			filterAttributes:    "",
			filterAttributeKeys: "key1",
			expected:            "key1:",
		},
		{
			name:                "single key with colon suffix keeps as is",
			filterAttributes:    "",
			filterAttributeKeys: "key1:",
			expected:            "key1:",
		},
		{
			name:                "single key:value pair extracts value",
			filterAttributes:    "",
			filterAttributeKeys: "key1:value1",
			expected:            "value1",
		},

		// Multiple keys cases
		{
			name:                "multiple keys without colons get colon suffixes",
			filterAttributes:    "",
			filterAttributeKeys: "key1,key2,key3",
			expected:            "key1:,key2:,key3:",
		},
		{
			name:                "multiple keys with colon suffixes keep as is",
			filterAttributes:    "",
			filterAttributeKeys: "key1:,key2:,key3:",
			expected:            "key1:,key2:,key3:",
		},
		{
			name:                "multiple key:value pairs extract values",
			filterAttributes:    "",
			filterAttributeKeys: "key1:value1,key2:value2,key3:value3",
			expected:            "value1,value2,value3",
		},

		// Mixed cases
		{
			name:                "mixed keys and key:value pairs",
			filterAttributes:    "",
			filterAttributeKeys: "key1,key2:value2,key3:",
			expected:            "key1:,value2,key3:",
		},
		{
			name:                "mixed with existing filterAttributes",
			filterAttributes:    "existing",
			filterAttributeKeys: "key1,key2:value2",
			expected:            "existing,key1:,value2",
		},

		// Whitespace handling
		{
			name:                "keys with whitespace are trimmed",
			filterAttributes:    "",
			filterAttributeKeys: " key1 , key2:value2 , key3: ",
			expected:            "key1:,value2,key3:",
		},
		{
			name:                "empty keys after trimming are skipped",
			filterAttributes:    "",
			filterAttributeKeys: "key1,,  ,key2:value2",
			expected:            "key1:,value2",
		},

		// Edge cases
		{
			name:                "colon at beginning creates invalid key:value",
			filterAttributes:    "",
			filterAttributeKeys: ":value",
			expected:            ":value:",
		},
		{
			name:                "key with empty value extracts empty",
			filterAttributes:    "",
			filterAttributeKeys: "key:",
			expected:            "key:",
		},
		{
			name:                "multiple colons extracts postfix after first colon",
			filterAttributes:    "",
			filterAttributeKeys: "key:val:ue",
			expected:            "val:ue",
		},
		{
			name:                "multiple colons at start gets colon suffix",
			filterAttributes:    "",
			filterAttributeKeys: ":key:val:ue",
			expected:            ":key:val:ue:",
		},

		// Complex real-world scenarios
		{
			name:                "complex mixed scenario",
			filterAttributes:    "pre1,pre2:prevalue",
			filterAttributeKeys: "env:prod, region , status:active, debug: ",
			expected:            "pre1,pre2:prevalue,prod,region:,active,debug:",
		},
		{
			name:                "only whitespace and commas",
			filterAttributes:    "existing",
			filterAttributeKeys: " , , ",
			expected:            "existing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessAttributeKeys(tt.filterAttributes, tt.filterAttributeKeys)
			if result != tt.expected {
				t.Errorf("ProcessAttributeKeys(%q, %q) = %q, want %q",
					tt.filterAttributes, tt.filterAttributeKeys, result, tt.expected)
			}
		})
	}
}

func TestProcessAttributeKeys_Performance(t *testing.T) {
	// Test with a large number of keys to ensure performance
	filterAttributes := "existing1,existing2"

	// Build a large filterAttributeKeys string
	var keys []string
	for i := 0; i < 1000; i++ {
		keys = append(keys, "key"+string(rune(i))+":")
	}
	largeFilterAttributeKeys := strings.Join(keys, ",")

	// This should not panic or take too long
	result := ProcessAttributeKeys(filterAttributes, largeFilterAttributeKeys)

	// Basic validation - should contain the original attributes
	if !strings.HasPrefix(result, filterAttributes) {
		t.Errorf("Result should start with filterAttributes")
	}

	// Should contain many keys
	if len(strings.Split(result, ",")) < 1000 {
		t.Errorf("Result should contain many processed keys")
	}
}

func TestReadAPIResponse_SuccessPassthrough(t *testing.T) {
	body := `{"id":1}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}

	result, _, err := ReadAPIResponse(resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success result, got %#v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if text.Text != body {
		t.Fatalf("body = %q, want %q", text.Text, body)
	}
}

func TestReadAPIResponse_DecodeFailureOn2xxReturnsBody(t *testing.T) {
	body := `{"id":1800,"manualScenario":{"manualScenarioType":"TEXT","id":1798,"requirements":[]}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	decodeErr := errors.New(
		"data matches more than one schema in oneOf(ComEpamReportportalBaseCoreTmsDtoTmsTestCaseRSManualScenario)",
	)

	result, _, err := ReadAPIResponse(resp, decodeErr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success result for 2xx decode failure, got %#v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if text.Text != body {
		t.Fatalf("body = %q, want %q", text.Text, body)
	}
}

func TestReadAPIResponse_HTTPErrorPropagates(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewBufferString(`{"message":"bad request"}`)),
	}
	apiErr := errors.New("400 Bad Request")

	result, _, err := ReadAPIResponse(resp, apiErr)
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	if result != nil {
		t.Fatalf("expected nil result, got %#v", result)
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %q, want body detail", err.Error())
	}
}
