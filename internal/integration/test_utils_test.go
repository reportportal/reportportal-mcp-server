package integration

import (
	"testing"
)

func TestNormalizeAndCompareJSON(t *testing.T) {
	tests := []struct {
		name     string
		actual   string
		expected string
		want     bool
	}{
		{
			name:     "identical JSON",
			actual:   `{"a":1,"b":"test"}`,
			expected: `{"a":1,"b":"test"}`,
			want:     true,
		},
		{
			name:     "different field order",
			actual:   `{"b":"test","a":1}`,
			expected: `{"a":1,"b":"test"}`,
			want:     true,
		},
		{
			name:     "different whitespace",
			actual:   `{"a": 1, "b": "test"}`,
			expected: `{"a":1,"b":"test"}`,
			want:     true,
		},
		{
			name:     "different values",
			actual:   `{"a":1,"b":"test"}`,
			expected: `{"a":2,"b":"test"}`,
			want:     false,
		},
		{
			name:     "large integers preserved (>53 bits)",
			actual:   `{"id":9007199254740993}`,
			expected: `{"id":9007199254740993}`,
			want:     true,
		},
		{
			name:     "large integers different values",
			actual:   `{"id":9007199254740993}`,
			expected: `{"id":9007199254740994}`,
			want:     false,
		},
		{
			name:     "nested objects",
			actual:   `{"outer":{"inner":{"value":42}}}`,
			expected: `{"outer":{"inner":{"value":42}}}`,
			want:     true,
		},
		{
			name:     "nested objects different order",
			actual:   `{"outer":{"c":3,"b":2,"a":1}}`,
			expected: `{"outer":{"a":1,"b":2,"c":3}}`,
			want:     true,
		},
		{
			name:     "arrays",
			actual:   `{"items":[1,2,3]}`,
			expected: `{"items":[1,2,3]}`,
			want:     true,
		},
		{
			name:     "arrays different order (should fail)",
			actual:   `{"items":[3,2,1]}`,
			expected: `{"items":[1,2,3]}`,
			want:     false,
		},
		{
			name:     "not JSON - identical strings",
			actual:   `not json`,
			expected: `not json`,
			want:     true,
		},
		{
			name:     "not JSON - different strings",
			actual:   `not json`,
			expected: `different`,
			want:     false,
		},
		{
			name:     "not JSON - whitespace trimmed",
			actual:   `  not json  `,
			expected: `not json`,
			want:     true,
		},
		{
			name:     "one valid JSON, one invalid",
			actual:   `{"valid":true}`,
			expected: `not json`,
			want:     false,
		},
		{
			name:     "null values",
			actual:   `{"a":null,"b":"test"}`,
			expected: `{"a":null,"b":"test"}`,
			want:     true,
		},
		{
			name:     "boolean values",
			actual:   `{"flag":true}`,
			expected: `{"flag":true}`,
			want:     true,
		},
		{
			name:     "float values",
			actual:   `{"pi":3.14159}`,
			expected: `{"pi":3.14159}`,
			want:     true,
		},
		{
			name:     "very large test case ID (from ReportPortal)",
			actual:   `{"testCaseHash":1559429329,"launchId":9340675}`,
			expected: `{"launchId":9340675,"testCaseHash":1559429329}`,
			want:     true,
		},
		{
			name:     "empty objects",
			actual:   `{}`,
			expected: `{}`,
			want:     true,
		},
		{
			name:     "empty arrays",
			actual:   `[]`,
			expected: `[]`,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAndCompareJSON(tt.actual, tt.expected)
			if got != tt.want {
				t.Errorf("normalizeAndCompareJSON() = %v, want %v", got, tt.want)
				t.Logf("  actual:   %s", tt.actual)
				t.Logf("  expected: %s", tt.expected)
			}
		})
	}
}
