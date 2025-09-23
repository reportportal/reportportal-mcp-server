package utils

import (
	"strings"
	"testing"
)

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
			name:                "single key:value pair preserves full key:value",
			filterAttributes:    "",
			filterAttributeKeys: "key1:value1",
			expected:            "key1:value1",
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
			name:                "multiple key:value pairs preserve full key:value",
			filterAttributes:    "",
			filterAttributeKeys: "key1:value1,key2:value2,key3:value3",
			expected:            "key1:value1,key2:value2,key3:value3",
		},

		// Mixed cases
		{
			name:                "mixed keys and key:value pairs",
			filterAttributes:    "",
			filterAttributeKeys: "key1,key2:value2,key3:",
			expected:            "key1:,key2:value2,key3:",
		},
		{
			name:                "mixed with existing filterAttributes",
			filterAttributes:    "existing",
			filterAttributeKeys: "key1,key2:value2",
			expected:            "existing,key1:,key2:value2",
		},

		// Whitespace handling
		{
			name:                "keys with whitespace are trimmed",
			filterAttributes:    "",
			filterAttributeKeys: " key1 , key2:value2 , key3: ",
			expected:            "key1:,key2:value2,key3:",
		},
		{
			name:                "empty keys after trimming are skipped",
			filterAttributes:    "",
			filterAttributeKeys: "key1,,  ,key2:value2",
			expected:            "key1:,key2:value2",
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
			name:                "multiple colons preserves full key:value",
			filterAttributes:    "",
			filterAttributeKeys: "key:val:ue",
			expected:            "key:val:ue",
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
			expected:            "pre1,pre2:prevalue,env:prod,region:,status:active,debug:",
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
