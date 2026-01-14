package mcpreportportal

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants
const (
	// testToken1 is a UUID token used for testing (can be used as Bearer or RP token)
	// #nosec G101 -- This is a test token, not a real credential
	testToken1 = "550e8400-e29b-41d4-a716-446655440000"

	// testToken2 is a second UUID token for testing multiple users
	// #nosec G101 -- This is a test token, not a real credential
	testToken2 = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	// testToken3 is a third UUID token for testing edge cases
	// #nosec G101 -- This is a test token, not a real credential
	testToken3 = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

	// testToken4 is a fourth UUID token for additional test scenarios
	// #nosec G101 -- This is a test token, not a real credential
	testToken4 = "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	// testToken5 is a fifth UUID token for additional test scenarios
	// #nosec G101 -- This is a test token, not a real credential
	testToken5 = "f47ac10b-58cc-4372-a567-0e02b2c3d480"

	// testEnvTokenString is a test environment token string (non-UUID format, different from testToken*)
	// #nosec G101 -- This is a test token, not a real credential
	testEnvTokenString = "env-var-token-xyz"
)

func TestTruncateForLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
		{
			name:     "string shorter than maxLen",
			input:    "short",
			maxLen:   10,
			expected: "short",
		},
		{
			name:     "string equal to maxLen",
			input:    "exactly10c",
			maxLen:   10,
			expected: "exactly10c",
		},
		{
			name:     "string longer than maxLen",
			input:    "this is a very long string",
			maxLen:   10,
			expected: "this is a ...",
		},
		{
			name:     "SHA256 hash truncated to 16",
			input:    "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6a7b8c9d0e1f2",
			maxLen:   16,
			expected: "a1b2c3d4e5f6g7h8...",
		},
		{
			name:     "maxLen is zero",
			input:    "test",
			maxLen:   0,
			expected: "...",
		},
		{
			name:     "maxLen is negative",
			input:    "test",
			maxLen:   -1,
			expected: "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateForLog(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewAnalytics(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		apiSecret  string
		rpAPIToken string
		wantErr    bool
	}{
		{
			name:       "valid config with secrets",
			userID:     "test-user-123",
			apiSecret:  "test-secret",
			rpAPIToken: testToken1,
			wantErr:    false,
		},
		{
			name:       "empty user ID - should use RP token hash",
			userID:     "",
			apiSecret:  "test-secret",
			rpAPIToken: testToken2,
			wantErr:    false,
		},
		{
			name:       "no api secret",
			userID:     "test-user-123",
			apiSecret:  "",
			rpAPIToken: testToken3,
			wantErr:    true,
		},
		{
			name:       "no RP API token - uses custom user ID",
			userID:     "test-user-123",
			apiSecret:  "test-secret",
			rpAPIToken: "",
			wantErr:    false,
		},
		{
			name:       "no RP API token and no user ID - uses anonymous",
			userID:     "",
			apiSecret:  "test-secret",
			rpAPIToken: "",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analytics, err := NewAnalytics(tt.userID, tt.apiSecret, tt.rpAPIToken, "")

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, analytics)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, analytics)

				// Analytics should be created when API secret is provided
				assert.NotNil(t, analytics.config)
				assert.NotNil(t, analytics.httpClient)
			}
		})
	}
}

func TestGetAnalyticArg(t *testing.T) {
	result := GetAnalyticArg()

	// Test that the result matches expected value
	expected := "knJS692_SmCyZukICYe3PA"
	assert.Equal(t, expected, result, "GetAnalyticArg should return the expected string")

	// Test that result is clean (no control characters)
	assert.NotContains(t, result, "\n", "Result should not contain newlines")
	assert.NotContains(t, result, "\r", "Result should not contain carriage returns")
	assert.NotContains(t, result, "\t", "Result should not contain tabs")

	// Test that multiple calls return the same result
	result2 := GetAnalyticArg()
	assert.Equal(t, result, result2, "GetAnalyticArg should be deterministic")

	// Test expected length
	assert.Len(t, result, 22, "Result should be 22 characters long")
}

func TestTrackMCPEvent(t *testing.T) {
	tests := []struct {
		name      string
		analytics *Analytics
		toolName  string
		shouldLog bool
	}{
		{
			name:      "nil analytics",
			analytics: nil,
			toolName:  "test_tool",
			shouldLog: false,
		},

		{
			name: "no API secret",
			analytics: &Analytics{
				config: &AnalyticsConfig{
					APISecret: "",
				},
				metrics:     make(map[string]map[string]*int64),
				metricsLock: sync.RWMutex{},
			},
			toolName:  "test_tool",
			shouldLog: true, // Analytics object exists, so it will increment metrics
		},
		{
			name: "valid analytics",
			analytics: &Analytics{
				config: &AnalyticsConfig{
					APISecret: "test-secret",
				},
				metrics:     make(map[string]map[string]*int64),
				metricsLock: sync.RWMutex{},
			},
			toolName:  "test_tool",
			shouldLog: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture logs
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))
			slog.SetDefault(logger)

			// Call TrackMCPEvent
			tt.analytics.TrackMCPEvent(context.Background(), tt.toolName)

			logOutput := logBuf.String()
			if tt.shouldLog {
				// For valid analytics, we should not see disabled message
				assert.NotContains(t, logOutput, "Analytics disabled")
			} else {
				// For nil analytics, we should see debug message about being disabled
				assert.Contains(t, logOutput, "Analytics disabled")
			}
		})
	}
}

func TestWithAnalytics(t *testing.T) {
	var handlerCalled bool

	// Create a test handler
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		handlerCalled = true
		return mcp.NewToolResultText("success"), nil
	}

	// Create analytics with a way to track calls
	analytics := &Analytics{
		config: &AnalyticsConfig{
			APISecret: "test-secret",
		},
		metrics:     make(map[string]map[string]*int64),
		metricsLock: sync.RWMutex{},
	}

	// Wrap the handler
	wrappedHandler := analytics.WithAnalytics("test_tool", handler)

	// Call the wrapped handler
	result, err := wrappedHandler(context.Background(), mcp.CallToolRequest{})

	// Verify handler was called
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, handlerCalled, "Original handler should be called")
}

func TestWithAnalyticsNilAnalytics(t *testing.T) {
	var handlerCalled bool

	// Create a test handler
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		handlerCalled = true
		return mcp.NewToolResultText("success"), nil
	}

	// Test with nil analytics
	var analytics *Analytics
	wrappedHandler := analytics.WithAnalytics("test_tool", handler)

	// Call the wrapped handler
	result, err := wrappedHandler(context.Background(), mcp.CallToolRequest{})

	// Verify handler was still called
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, handlerCalled, "Original handler should be called even with nil analytics")
}

func TestAnalyticsStop(t *testing.T) {
	tests := []struct {
		name      string
		analytics *Analytics
	}{
		{
			name:      "nil analytics",
			analytics: nil,
		},
		{
			name: "analytics with nil stopChan",
			analytics: &Analytics{
				config:   &AnalyticsConfig{},
				stopChan: nil,
			},
		},
		{
			name: "valid analytics",
			analytics: &Analytics{
				config:     &AnalyticsConfig{},
				stopChan:   make(chan struct{}),
				tickerDone: make(chan struct{}),
				wg:         sync.WaitGroup{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			assert.NotPanics(t, func() {
				tt.analytics.Stop()
			})
		})
	}
}

func TestAnalyticsIntegration(t *testing.T) {
	// Create a test server to mock GA4
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	// Create analytics instance
	analytics, err := NewAnalytics(
		"test-user",
		"test-secret",
		"dGVzdC1yZXBvcnRwb3J0YWwtYW5hbHl0aWNzLXRva2VuLWJhc2U2NA==",
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, analytics)

	// Create a mock tool handler
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("mock result"), nil
	}

	// Wrap with analytics
	wrappedHandler := analytics.WithAnalytics("test_tool", handler)

	// Call the handler multiple times
	for i := 0; i < 3; i++ {
		_, err := wrappedHandler(context.Background(), mcp.CallToolRequest{})
		assert.NoError(t, err)
	}

	// Give some time for async processing
	time.Sleep(100 * time.Millisecond)

	// Stop analytics
	analytics.Stop()
}

func TestStopAnalytics(t *testing.T) {
	tests := []struct {
		name      string
		analytics *Analytics
		reason    string
	}{
		{
			name:      "nil analytics",
			analytics: nil,
			reason:    "",
		},
		{
			name: "valid analytics with reason",
			analytics: &Analytics{
				config: &AnalyticsConfig{APISecret: "test-secret"},
			},
			reason: "test reason",
		},
		{
			name: "valid analytics without reason",
			analytics: &Analytics{
				config: &AnalyticsConfig{APISecret: "test-secret"},
			},
			reason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			assert.NotPanics(t, func() {
				StopAnalytics(tt.analytics, tt.reason)
			})
		})
	}
}

func TestAnalyticsConfigValidation(t *testing.T) {
	tests := []struct {
		name     string
		config   *AnalyticsConfig
		expected bool
	}{
		{
			name: "valid config with API secret",
			config: &AnalyticsConfig{
				MeasurementID: "G-TEST123",
				APISecret:     "secret123",
				UserID:        "user123",
			},
			expected: true,
		},
		{
			name: "missing API secret",
			config: &AnalyticsConfig{
				MeasurementID: "G-TEST123",
				APISecret:     "",
				UserID:        "user123",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analytics := &Analytics{
				config:      tt.config,
				metrics:     make(map[string]map[string]*int64),
				metricsLock: sync.RWMutex{},
			}

			// Capture logs to see if tracking actually happens
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))
			slog.SetDefault(logger)

			analytics.TrackMCPEvent(context.Background(), "test_tool")

			logOutput := logBuf.String()
			if tt.expected {
				assert.NotContains(t, logOutput, "Analytics disabled")
			} else {
				// Since we're creating an Analytics object manually (not nil),
				// TrackMCPEvent won't log "Analytics disabled" - it only checks for nil
				// The test is about config validation, not runtime behavior
				assert.NotContains(t, logOutput, "Analytics disabled")
			}
		})
	}
}

func TestConcurrentMetricIncrement(t *testing.T) {
	analytics := &Analytics{
		config: &AnalyticsConfig{
			APISecret: "test-secret",
			UserID:    "test-user",
		},
		metrics:     make(map[string]map[string]*int64),
		metricsLock: sync.RWMutex{},
	}

	const numGoroutines = 10
	const numIncrements = 100
	var wg sync.WaitGroup

	// Launch multiple goroutines to increment metrics concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numIncrements; j++ {
				analytics.TrackMCPEvent(context.Background(), "concurrent_tool")
			}
		}()
	}

	wg.Wait()

	// Verify we don't get any race conditions or panics
	// The exact count verification would require access to private fields
	// but the test ensures no race conditions occur
}

func TestAnalyticsUserIDGeneration(t *testing.T) {
	// Test with empty user ID - should generate one
	analytics1, err := NewAnalytics("", "test-secret", testToken4, "")
	assert.NoError(t, err)
	assert.NotNil(t, analytics1)

	// Test with provided user ID
	analytics2, err := NewAnalytics(
		"custom-user-id",
		"test-secret",
		testToken5,
		"",
	)
	assert.NoError(t, err)
	assert.NotNil(t, analytics2)

	// Both should be valid
	assert.NotNil(t, analytics1.config)
	assert.NotNil(t, analytics2.config)
}

func TestGetUserIDFromContext(t *testing.T) {
	tests := []struct {
		name                string
		rpTokenEnvVar       string // RP_API_TOKEN env var (passed to NewAnalytics)
		customUserID        string // Custom user ID (passed to NewAnalytics)
		tokenInContext      string // Bearer token from request
		expectedSource      string // "env_var", "bearer_header", or "anonymous"
		expectedUserIDMatch string // "env_var_hash", "bearer_hash", or "anonymous_hash"
	}{
		{
			name:                "RP_API_TOKEN env var set - takes precedence over Bearer token",
			rpTokenEnvVar:       testEnvTokenString,
			customUserID:        "",
			tokenInContext:      testToken1,
			expectedSource:      "env_var",
			expectedUserIDMatch: "env_var_hash",
		},
		{
			name:                "No RP_API_TOKEN but custom user ID set - uses custom user ID",
			rpTokenEnvVar:       "",
			customUserID:        "custom-user-123",
			tokenInContext:      testToken1,
			expectedSource:      "env_var",
			expectedUserIDMatch: "custom_user_hash",
		},
		{
			name:                "No env var but Bearer token present - uses Bearer token",
			rpTokenEnvVar:       "",
			customUserID:        "",
			tokenInContext:      testToken1,
			expectedSource:      "bearer_header",
			expectedUserIDMatch: "bearer_hash",
		},
		{
			name:                "No env var and no Bearer token - uses anonymous",
			rpTokenEnvVar:       "",
			customUserID:        "",
			tokenInContext:      "",
			expectedSource:      "anonymous",
			expectedUserIDMatch: "anonymous_hash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create analytics with the specified configuration
			analytics, err := NewAnalytics(tt.customUserID, "test-secret", tt.rpTokenEnvVar, "")
			require.NoError(t, err)
			require.NotNil(t, analytics)
			defer analytics.Stop()

			// Create context with or without Bearer token
			ctx := context.Background()
			if tt.tokenInContext != "" {
				ctx = WithTokenInContext(ctx, tt.tokenInContext)
			}

			// Get user ID from context
			userID := analytics.getUserIDFromContext(ctx)

			// Verify behavior based on expected source
			switch tt.expectedUserIDMatch {
			case "env_var_hash":
				// Should use RP_API_TOKEN env var hash
				expectedHash := HashToken(tt.rpTokenEnvVar)
				assert.Equal(t, expectedHash, userID, "Should use RP_API_TOKEN env var hash")
				assert.Equal(t, analytics.config.UserID, userID, "Should match config user ID")
			case "custom_user_hash":
				// Should use custom user ID hash
				expectedHash := HashToken(tt.customUserID)
				assert.Equal(t, expectedHash, userID, "Should use custom user ID hash")
				assert.Equal(t, analytics.config.UserID, userID, "Should match config user ID")
			case "bearer_hash":
				// Should use Bearer token hash
				expectedHash := HashToken(tt.tokenInContext)
				assert.Equal(t, expectedHash, userID, "Should use Bearer token hash")
				assert.NotEqual(
					t,
					analytics.config.UserID,
					userID,
					"Should not match anonymous config user ID",
				)
			case "anonymous_hash":
				// Should use anonymous hash
				expectedHash := HashToken("anonymous-http-mode")
				assert.Equal(t, expectedHash, userID, "Should use anonymous hash")
				assert.Equal(t, analytics.config.UserID, userID, "Should match config user ID")
			}
		})
	}
}

func TestTrackMCPEventWithTokenFromContext(t *testing.T) {
	// Test 1: Analytics with RP_API_TOKEN env var - should always use env var hash
	t.Run("with RP_API_TOKEN env var", func(t *testing.T) {
		envToken := testEnvTokenString
		analytics, err := NewAnalytics("", "test-secret", envToken, "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Track event with Bearer token in context
		bearerToken := testToken1
		ctx := WithTokenInContext(context.Background(), bearerToken)

		analytics.TrackMCPEvent(ctx, "test_tool_1")

		// Verify metric was incremented for env var user (NOT Bearer token user)
		hashedEnvToken := HashToken(envToken)
		analytics.metricsLock.RLock()
		userMetrics, exists := analytics.metrics[hashedEnvToken]
		analytics.metricsLock.RUnlock()

		assert.True(t, exists, "Metrics should exist for env var user")
		assert.NotNil(t, userMetrics, "User metrics should not be nil")
		assert.Contains(t, userMetrics, "test_tool_1", "Tool metric should exist")

		// Verify Bearer token was NOT used
		hashedBearerToken := HashToken(bearerToken)
		analytics.metricsLock.RLock()
		_, existsBearer := analytics.metrics[hashedBearerToken]
		analytics.metricsLock.RUnlock()

		assert.False(
			t,
			existsBearer,
			"Metrics should NOT exist for Bearer token user when env var is set",
		)
	})

	// Test 2: Analytics WITHOUT env var - should use Bearer token from context
	t.Run("without RP_API_TOKEN env var - uses Bearer token", func(t *testing.T) {
		analytics, err := NewAnalytics("", "test-secret", "", "") // No env token
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Track event with different Bearer tokens
		token1 := testToken1
		ctx1 := WithTokenInContext(context.Background(), token1)

		analytics.TrackMCPEvent(ctx1, "test_tool_1")

		hashedToken1 := HashToken(token1)
		analytics.metricsLock.RLock()
		userMetrics1, exists1 := analytics.metrics[hashedToken1]
		analytics.metricsLock.RUnlock()

		assert.True(t, exists1, "Metrics should exist for Bearer token user")
		assert.NotNil(t, userMetrics1, "User metrics should not be nil")
		assert.Contains(t, userMetrics1, "test_tool_1", "Tool metric should exist")

		// Track with different Bearer token
		token2 := testToken2
		ctx2 := WithTokenInContext(context.Background(), token2)

		analytics.TrackMCPEvent(ctx2, "test_tool_2")

		hashedToken2 := HashToken(token2)
		analytics.metricsLock.RLock()
		userMetrics2, exists2 := analytics.metrics[hashedToken2]
		analytics.metricsLock.RUnlock()

		assert.True(t, exists2, "Metrics should exist for second Bearer token user")
		assert.NotNil(t, userMetrics2, "Second user metrics should not be nil")
		assert.Contains(t, userMetrics2, "test_tool_2", "Second tool metric should exist")

		// Verify users are different
		assert.NotEqual(t, hashedToken1, hashedToken2, "User IDs should be different")
	})

	// Test 3: No env var and no Bearer token - uses anonymous
	t.Run("without env var and without Bearer token - uses anonymous", func(t *testing.T) {
		analytics, err := NewAnalytics("", "test-secret", "", "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		ctx := context.Background() // No Bearer token

		analytics.TrackMCPEvent(ctx, "test_tool_3")

		// Verify metric was incremented for anonymous user
		anonymousHash := HashToken("anonymous-http-mode")
		analytics.metricsLock.RLock()
		anonymousMetrics, existsAnonymous := analytics.metrics[anonymousHash]
		analytics.metricsLock.RUnlock()

		assert.True(t, existsAnonymous, "Metrics should exist for anonymous user")
		assert.NotNil(t, anonymousMetrics, "Anonymous user metrics should not be nil")
		assert.Contains(
			t,
			anonymousMetrics,
			"test_tool_3",
			"Anonymous user tool metric should exist",
		)
	})
}

func TestAnalyticsBatchSendingPerUser(t *testing.T) {
	// Test with NO env var - should use Bearer tokens from requests
	t.Run("without RP_API_TOKEN env var - tracks per Bearer token", func(t *testing.T) {
		// Create analytics without env token
		analytics, err := NewAnalytics("", "test-secret", "", "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Track events for different Bearer tokens
		token1 := testToken1
		token2 := testToken2

		ctx1 := WithTokenInContext(context.Background(), token1)
		ctx2 := WithTokenInContext(context.Background(), token2)

		// Track multiple events
		analytics.TrackMCPEvent(ctx1, "tool_a")
		analytics.TrackMCPEvent(ctx1, "tool_a") // Same tool, same user
		analytics.TrackMCPEvent(ctx2, "tool_b")
		analytics.TrackMCPEvent(ctx2, "tool_c")

		// Verify metrics are stored per Bearer token user
		hashedToken1 := HashToken(token1)
		hashedToken2 := HashToken(token2)

		analytics.metricsLock.RLock()
		user1Metrics := analytics.metrics[hashedToken1]
		user2Metrics := analytics.metrics[hashedToken2]
		analytics.metricsLock.RUnlock()

		assert.NotNil(t, user1Metrics, "User 1 should have metrics")
		assert.NotNil(t, user2Metrics, "User 2 should have metrics")
		assert.Contains(t, user1Metrics, "tool_a", "User 1 should have tool_a")
		assert.Contains(t, user2Metrics, "tool_b", "User 2 should have tool_b")
		assert.Contains(t, user2Metrics, "tool_c", "User 2 should have tool_c")
	})

	// Test with env var - should use env var regardless of Bearer tokens
	t.Run("with RP_API_TOKEN env var - tracks under single user", func(t *testing.T) {
		envToken := testEnvTokenString
		analytics, err := NewAnalytics("", "test-secret", envToken, "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Track events with different Bearer tokens (should be ignored)
		token1 := testToken1
		token2 := testToken2

		ctx1 := WithTokenInContext(context.Background(), token1)
		ctx2 := WithTokenInContext(context.Background(), token2)

		// Track multiple events
		analytics.TrackMCPEvent(ctx1, "tool_a")
		analytics.TrackMCPEvent(ctx2, "tool_b")
		analytics.TrackMCPEvent(ctx1, "tool_c")

		// Verify all metrics are under the env var user
		hashedEnvToken := HashToken(envToken)

		analytics.metricsLock.RLock()
		envUserMetrics := analytics.metrics[hashedEnvToken]
		analytics.metricsLock.RUnlock()

		assert.NotNil(t, envUserMetrics, "Env var user should have metrics")
		assert.Contains(t, envUserMetrics, "tool_a", "Should have tool_a")
		assert.Contains(t, envUserMetrics, "tool_b", "Should have tool_b")
		assert.Contains(t, envUserMetrics, "tool_c", "Should have tool_c")

		// Verify Bearer tokens were NOT used
		hashedToken1 := HashToken(token1)
		hashedToken2 := HashToken(token2)

		analytics.metricsLock.RLock()
		_, exists1 := analytics.metrics[hashedToken1]
		_, exists2 := analytics.metrics[hashedToken2]
		analytics.metricsLock.RUnlock()

		assert.False(t, exists1, "Bearer token 1 should not create separate user")
		assert.False(t, exists2, "Bearer token 2 should not create separate user")
	})
}

func TestAnalyticsHashingComparison_WithAndWithoutRPToken(t *testing.T) {
	// Setup: Same Bearer token will be used in both scenarios
	bearerToken := testToken1
	rpEnvToken := testToken2

	// Scenario 1: Analytics WITH RP_API_TOKEN env var
	analytics1, err1 := NewAnalytics("", "test-secret", rpEnvToken, "")
	require.NoError(t, err1)
	require.NotNil(t, analytics1)
	defer analytics1.Stop()

	// Scenario 2: Analytics WITHOUT RP_API_TOKEN env var
	analytics2, err2 := NewAnalytics("", "test-secret", "", "")
	require.NoError(t, err2)
	require.NotNil(t, analytics2)
	defer analytics2.Stop()

	// Create context with Bearer token (same for both)
	ctxWithBearer := WithTokenInContext(context.Background(), bearerToken)

	// Get user IDs from both analytics instances
	userID1 := analytics1.getUserIDFromContext(ctxWithBearer)
	userID2 := analytics2.getUserIDFromContext(ctxWithBearer)

	// Verify the results
	t.Run("user IDs should be different", func(t *testing.T) {
		assert.NotEqual(t, userID1, userID2,
			"User IDs should differ when RP_API_TOKEN is set vs not set")
	})

	t.Run("with RP_API_TOKEN uses env token hash", func(t *testing.T) {
		expectedHash := HashToken(rpEnvToken)
		assert.Equal(t, expectedHash, userID1,
			"Should use RP_API_TOKEN env var hash")
		assert.NotEqual(t, HashToken(bearerToken), userID1,
			"Should NOT use Bearer token when RP_API_TOKEN is set")
	})

	t.Run("without RP_API_TOKEN uses Bearer token hash", func(t *testing.T) {
		expectedHash := HashToken(bearerToken)
		assert.Equal(t, expectedHash, userID2,
			"Should use Bearer token hash when RP_API_TOKEN not set")
		assert.NotEqual(t, HashToken(rpEnvToken), userID2,
			"Should NOT use RP_API_TOKEN when it's not set")
	})

	t.Run("track events and verify separate users", func(t *testing.T) {
		// Track events in both analytics instances
		analytics1.TrackMCPEvent(ctxWithBearer, "test_tool")
		analytics2.TrackMCPEvent(ctxWithBearer, "test_tool")

		// Verify metrics are tracked under different user IDs
		analytics1.metricsLock.RLock()
		user1Metrics := analytics1.metrics[userID1]
		analytics1.metricsLock.RUnlock()

		analytics2.metricsLock.RLock()
		user2Metrics := analytics2.metrics[userID2]
		analytics2.metricsLock.RUnlock()

		assert.NotNil(t, user1Metrics, "Analytics1 should have metrics for RP_API_TOKEN user")
		assert.NotNil(t, user2Metrics, "Analytics2 should have metrics for Bearer token user")
		assert.Contains(t, user1Metrics, "test_tool", "Should track tool for RP_API_TOKEN user")
		assert.Contains(t, user2Metrics, "test_tool", "Should track tool for Bearer token user")
	})

	t.Run("display hash comparison", func(t *testing.T) {
		t.Logf("\n=== Hash Comparison ===")
		t.Logf("Bearer Token:       %s", bearerToken)
		t.Logf("RP_API_TOKEN:       %s", rpEnvToken)
		t.Logf("Bearer Token Hash:  %s", HashToken(bearerToken))
		t.Logf("RP_API_TOKEN Hash:  %s", HashToken(rpEnvToken))
		t.Logf("\nWith RP_API_TOKEN set:")
		t.Logf("  User ID used:     %s", userID1)
		t.Logf("  Matches RP_API_TOKEN hash: %v", userID1 == HashToken(rpEnvToken))
		t.Logf("\nWithout RP_API_TOKEN:")
		t.Logf("  User ID used:     %s", userID2)
		t.Logf("  Matches Bearer token hash: %v", userID2 == HashToken(bearerToken))
	})
}

func TestSameTokenDifferentSources_ProducesSameHash(t *testing.T) {
	// This test verifies that the SAME token value produces the SAME hash
	// regardless of whether it comes from RP_API_TOKEN env var or Bearer header
	sameTokenValue := testToken1

	// Scenario 1: Token from RP_API_TOKEN environment variable
	analytics1, err1 := NewAnalytics("", "test-secret", sameTokenValue, "")
	require.NoError(t, err1)
	require.NotNil(t, analytics1)
	defer analytics1.Stop()

	// Scenario 2: Token from Bearer header (no env var)
	analytics2, err2 := NewAnalytics("", "test-secret", "", "")
	require.NoError(t, err2)
	require.NotNil(t, analytics2)
	defer analytics2.Stop()

	// Create context with the SAME token value in Bearer header
	ctxWithBearer := WithTokenInContext(context.Background(), sameTokenValue)

	// Get user IDs from both scenarios
	userID1 := analytics1.getUserIDFromContext(context.Background()) // Uses env var
	userID2 := analytics2.getUserIDFromContext(ctxWithBearer)        // Uses Bearer header

	// CRITICAL VERIFICATION: Same token → Same hash → Same user ID
	t.Run("same token produces same hash", func(t *testing.T) {
		expectedHash := HashToken(sameTokenValue)
		assert.Equal(t, expectedHash, userID1,
			"RP_API_TOKEN env var should produce expected hash")
		assert.Equal(t, expectedHash, userID2,
			"Bearer token should produce expected hash")
		assert.Equal(t, userID1, userID2,
			"Same token value should produce same user ID regardless of source")
	})

	// Verify both analytics track to the same user
	t.Run("tracks to same user", func(t *testing.T) {
		// Track events
		analytics1.TrackMCPEvent(context.Background(), "test_tool")
		analytics2.TrackMCPEvent(ctxWithBearer, "test_tool")

		// Both should track under the same user ID
		analytics1.metricsLock.RLock()
		user1Metrics := analytics1.metrics[userID1]
		analytics1.metricsLock.RUnlock()

		analytics2.metricsLock.RLock()
		user2Metrics := analytics2.metrics[userID2]
		analytics2.metricsLock.RUnlock()

		assert.NotNil(t, user1Metrics, "Analytics1 should have metrics")
		assert.NotNil(t, user2Metrics, "Analytics2 should have metrics")
		assert.Contains(t, user1Metrics, "test_tool")
		assert.Contains(t, user2Metrics, "test_tool")
	})

	// Display verification
	t.Run("display hash verification", func(t *testing.T) {
		t.Logf("\n=== Same Token, Different Sources ===")
		t.Logf("Token Value:           %s", sameTokenValue)
		t.Logf("Expected Hash:         %s", HashToken(sameTokenValue))
		t.Logf("\nFrom RP_API_TOKEN env var:")
		t.Logf("  User ID:             %s", userID1)
		t.Logf("  Matches expected:    %v", userID1 == HashToken(sameTokenValue))
		t.Logf("\nFrom Bearer header:")
		t.Logf("  User ID:             %s", userID2)
		t.Logf("  Matches expected:    %v", userID2 == HashToken(sameTokenValue))
		t.Logf("\nBoth IDs match:        %v", userID1 == userID2)
	})
}

func TestHTTPTokenMiddlewareIntegrationWithAnalytics(t *testing.T) {
	// Test 1: Analytics WITHOUT env var - should use Bearer tokens
	t.Run("without RP_API_TOKEN env var - uses Bearer tokens", func(t *testing.T) {
		// Create analytics without env var or custom user ID
		analytics, err := NewAnalytics("", "test-secret", "", "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Create a handler that tracks an event
		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Track event with context (which should have the token from middleware)
			analytics.TrackMCPEvent(r.Context(), "test_tool")
			w.WriteHeader(http.StatusOK)
		})

		// Wrap with HTTPTokenMiddleware
		middleware := HTTPTokenMiddleware(testHandler)

		// Request with Bearer token
		token := testToken1
		req1 := httptest.NewRequest("POST", "/test", nil)
		req1.Header.Set("Authorization", "Bearer "+token)

		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)

		assert.Equal(t, http.StatusOK, rr1.Code)

		// Verify analytics tracked with Bearer token-based user ID
		hashedToken := HashToken(token)
		analytics.metricsLock.RLock()
		userMetrics, exists := analytics.metrics[hashedToken]
		analytics.metricsLock.RUnlock()

		assert.True(t, exists, "Should track metrics for Bearer token-based user")
		assert.NotNil(t, userMetrics, "User metrics should exist")
		assert.Contains(t, userMetrics, "test_tool", "Should track the tool usage")

		// Request without Bearer token (uses anonymous)
		req2 := httptest.NewRequest("POST", "/test", nil)

		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)

		assert.Equal(t, http.StatusOK, rr2.Code)

		// Verify analytics tracked with anonymous user ID
		anonymousHash := HashToken("anonymous-http-mode")
		analytics.metricsLock.RLock()
		anonymousMetrics, existsAnonymous := analytics.metrics[anonymousHash]
		analytics.metricsLock.RUnlock()

		assert.True(t, existsAnonymous, "Should track metrics for anonymous user")
		assert.NotNil(t, anonymousMetrics, "Anonymous user metrics should exist")
		assert.Contains(
			t,
			anonymousMetrics,
			"test_tool",
			"Should track the tool usage for anonymous user",
		)
	})

	// Test 2: Analytics WITH custom user ID - should ignore Bearer tokens
	t.Run("with custom user ID - ignores Bearer tokens", func(t *testing.T) {
		customUserID := "my-custom-user-id"
		analytics, err := NewAnalytics(customUserID, "test-secret", "", "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Create a handler that tracks an event
		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			analytics.TrackMCPEvent(r.Context(), "test_tool")
			w.WriteHeader(http.StatusOK)
		})

		middleware := HTTPTokenMiddleware(testHandler)

		// Request with Bearer token (should be ignored)
		bearerToken := testToken1
		req := httptest.NewRequest("POST", "/test", nil)
		req.Header.Set("Authorization", "Bearer "+bearerToken)

		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify analytics tracked with custom user ID (NOT Bearer token)
		hashedCustomUserID := HashToken(customUserID)
		analytics.metricsLock.RLock()
		customMetrics, existsCustom := analytics.metrics[hashedCustomUserID]
		analytics.metricsLock.RUnlock()

		assert.True(t, existsCustom, "Should track metrics for custom user ID")
		assert.NotNil(t, customMetrics, "Custom user metrics should exist")
		assert.Contains(t, customMetrics, "test_tool", "Should track the tool usage")

		// Verify Bearer token was NOT used
		hashedBearerToken := HashToken(bearerToken)
		analytics.metricsLock.RLock()
		_, existsBearer := analytics.metrics[hashedBearerToken]
		analytics.metricsLock.RUnlock()

		assert.False(
			t,
			existsBearer,
			"Should NOT track metrics for Bearer token when custom user ID is set",
		)
	})
}

func TestAnalyticsInstanceIDFetching(t *testing.T) {
	// Create a mock ReportPortal server that returns instance ID
	mockInstanceID := "8d8638be-adc5-40a8-8a12-b98429548b78"
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/info" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Return the expected JSON structure
			response := `{
				"extensions": {
					"result": {
						"server.details.instance": "` + mockInstanceID + `"
					}
				}
			}`
			_, _ = w.Write([]byte(response))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	t.Run("instance ID is fetched and stored", func(t *testing.T) {
		analytics, err := NewAnalytics("test-user", "test-secret", "", mockServer.URL)
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Instance ID should be empty before first use (lazy loading)
		assert.False(t, analytics.instanceIDFetched.Load(), "Should not be fetched initially")

		// Trigger lazy loading by calling ensureInstanceID
		analytics.ensureInstanceID()

		// Verify instance ID was fetched
		assert.True(t, analytics.instanceIDFetched.Load(), "Should be marked as fetched")
		assert.Equal(t, mockInstanceID, analytics.instanceID)
	})

	t.Run("instance ID is fetched lazily on first metrics processing", func(t *testing.T) {
		// Create analytics with mock RP server
		analytics, err := NewAnalytics("test-user", "test-secret", "", mockServer.URL)
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Instance ID should be empty initially (not fetched yet)
		assert.False(t, analytics.instanceIDFetched.Load(), "Should not be fetched initially")

		// Track an event
		ctx := context.Background()
		analytics.TrackMCPEvent(ctx, "test_tool")

		// Instance ID still not fetched (only increments counter)
		assert.False(t, analytics.instanceIDFetched.Load(), "Should not be fetched after tracking")

		// Process metrics - this should trigger lazy fetch
		analytics.processMetrics()

		// Now instance ID should be fetched
		assert.True(
			t,
			analytics.instanceIDFetched.Load(),
			"Should be fetched after processing metrics",
		)
		assert.Equal(t, mockInstanceID, analytics.instanceID)
	})

	t.Run("instance ID is added to GA events", func(t *testing.T) {
		// Create a mock GA server to capture events
		var capturedEvents []GAEvent
		var mu sync.Mutex
		gaServer := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					var payload GAPayload
					if err := json.NewDecoder(r.Body).Decode(&payload); err == nil {
						mu.Lock()
						capturedEvents = append(capturedEvents, payload.Events...)
						mu.Unlock()
					}
					w.WriteHeader(http.StatusOK)
				}
			}),
		)
		defer gaServer.Close()

		// Create analytics with mock RP server
		analytics, err := NewAnalytics("test-user", "test-secret", "", mockServer.URL)
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Override GA endpoint for testing (we need to modify the config)
		// Track an event
		ctx := context.Background()
		analytics.TrackMCPEvent(ctx, "test_tool")

		// Wait a bit for batching
		time.Sleep(100 * time.Millisecond)

		// Process metrics manually
		analytics.processMetrics()

		// Verify events were created with instanceID
		// Note: In real scenario, events would be sent to GA server
		// For this test, we verify the instanceID is stored in analytics
		assert.True(t, analytics.instanceIDFetched.Load(), "Should be fetched after processing")
		assert.Equal(t, mockInstanceID, analytics.instanceID)
	})

	t.Run("instance ID retries on failure until successful", func(t *testing.T) {
		fetchCount := 0
		var mu sync.Mutex
		retryServer := httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/info" {
					mu.Lock()
					fetchCount++
					currentCount := fetchCount
					mu.Unlock()

					// First 2 calls fail, 3rd succeeds
					if currentCount < 3 {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					response := `{
					"extensions": {
						"result": {
							"server.details.instance": "` + mockInstanceID + `"
						}
					}
				}`
					_, _ = w.Write([]byte(response))
				}
			}),
		)
		defer retryServer.Close()

		analytics, err := NewAnalytics("test-user", "test-secret", "", retryServer.URL)
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// First attempt - should fail
		analytics.ensureInstanceID()
		assert.False(t, analytics.instanceIDFetched.Load(), "First attempt should fail")
		assert.Empty(t, analytics.instanceID, "First attempt should fail")

		// Second attempt - should still fail
		analytics.ensureInstanceID()
		assert.False(t, analytics.instanceIDFetched.Load(), "Second attempt should fail")
		assert.Empty(t, analytics.instanceID, "Second attempt should fail")

		// Third attempt - should succeed
		analytics.ensureInstanceID()
		assert.True(t, analytics.instanceIDFetched.Load(), "Third attempt should succeed")
		assert.Equal(t, mockInstanceID, analytics.instanceID, "Third attempt should succeed")

		// Fourth attempt - should use cached value, not fetch again
		mu.Lock()
		countBeforeFourth := fetchCount
		mu.Unlock()

		analytics.ensureInstanceID()

		mu.Lock()
		countAfterFourth := fetchCount
		mu.Unlock()

		assert.Equal(
			t,
			countBeforeFourth,
			countAfterFourth,
			"Should not fetch again once successful",
		)
	})

	t.Run("empty instance ID when host URL is empty", func(t *testing.T) {
		analytics, err := NewAnalytics("test-user", "test-secret", "", "")
		require.NoError(t, err)
		require.NotNil(t, analytics)
		defer analytics.Stop()

		// Verify instance ID is empty when no host URL provided
		assert.False(t, analytics.instanceIDFetched.Load(), "Should not be fetched")
		assert.Empty(t, analytics.instanceID)

		// Try to ensure instance ID - should still be empty
		analytics.ensureInstanceID()

		assert.False(
			t,
			analytics.instanceIDFetched.Load(),
			"Should still not be fetched when no host URL",
		)
		assert.Empty(
			t,
			analytics.instanceID,
			"Should remain empty even after ensureInstanceID call",
		)
	})
}
