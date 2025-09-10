package mcpreportportal

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func IsUUIDFormat(token string) bool {
	return uuid.Validate(token) == nil
}

func TestIsUUIDFormat(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected bool
	}{
		{
			name:     "valid UUID v4",
			token:    "014b329b-a882-4c2d-9988-c2f6179a421b",
			expected: true,
		},
		{
			name:     "valid UUID with uppercase",
			token:    "014B329B-A882-4C2D-9988-C2F6179A421B",
			expected: true,
		},
		{
			name:     "36 chars with 4 dashes but not valid UUID",
			token:    "------------------------------------",
			expected: false,
		},
		{
			name:     "valid format but invalid hex chars",
			token:    "gggg-hhhh-iiii-jjjj-kkkkkkkkkkkk",
			expected: false,
		},
		{
			name:     "too short",
			token:    "123-456-789",
			expected: false,
		},
		{
			name:     "too long",
			token:    "123456789-1234-1234-1234-123456789012345",
			expected: false,
		},
		{
			name:     "empty string",
			token:    "",
			expected: false,
		},
		{
			name:     "valid UUID nil",
			token:    "00000000-0000-0000-0000-000000000000",
			expected: true,
		},
		{
			name:     "wrong dash positions",
			token:    "12345678-12341234-1234-123456789012",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsUUIDFormat(tt.token)
			assert.Equal(t, tt.expected, result,
				"IsUUIDFormat(%s) = %v, expected %v", tt.token, result, tt.expected)
		})
	}
}

func TestValidateRPToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected bool
	}{
		{
			name:     "valid UUID should pass",
			token:    "014b329b-a882-4c2d-9988-c2f6179a421b",
			expected: true,
		},
		{
			name:     "fake UUID (dashes only) should fail",
			token:    "------------------------------------",
			expected: false,
		},
		{
			name:     "RP prefix token should pass",
			token:    "rp_test_token_123456789",
			expected: true,
		},
		{
			name:     "bearer prefix should pass",
			token:    "bearer_test_token_123456789",
			expected: true,
		},
		{
			name:     "long token without prefix should pass",
			token:    "this_is_a_very_long_token_that_should_pass",
			expected: true,
		},
		{
			name:     "short token should fail",
			token:    "short",
			expected: false,
		},
		{
			name:     "empty token should fail",
			token:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateRPToken(tt.token)
			assert.Equal(t, tt.expected, result,
				"ValidateRPToken(%s) = %v, expected %v", tt.token, result, tt.expected)
		})
	}
}

func TestUUIDValidationEdgeCases(t *testing.T) {
	// Test the specific case mentioned in the issue
	fakeUUID := "------------------------------------"

	// This should now fail with proper validation
	assert.False(t, IsUUIDFormat(fakeUUID),
		"Fake UUID with only dashes should not pass validation")
	assert.False(t, ValidateRPToken(fakeUUID),
		"Fake UUID with only dashes should not pass token validation")

	// Test other edge cases
	edgeCases := []string{
		"aaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",     // looks like UUID but invalid hex
		"1111-2222-3333-4444-555555555555",     // valid hex but invalid UUID format
		"||||||||||||||||||||||||||||||||||||", // 36 chars, wrong separator
		"12341234-1234-1234-1234-12341234123X", // invalid hex character at end
		"12341234_1234_1234_1234_123412341234", // wrong separators (underscores)
	}

	for _, edgeCase := range edgeCases {
		assert.False(t, IsUUIDFormat(edgeCase),
			"Edge case '%s' should not pass UUID validation", edgeCase)
	}
}
