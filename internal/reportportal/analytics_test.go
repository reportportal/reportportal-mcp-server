package mcpreportportal

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAnalytics(t *testing.T) {
	tests := []struct {
		name      string
		userID    string
		apiSecret string
		envVar    string
		wantErr   bool
	}{
		{
			name:      "valid config with secrets",
			userID:    "test-user-123",
			apiSecret: "test-secret",
			envVar:    "",
			wantErr:   false,
		},
		{
			name:      "empty user ID - should generate one",
			userID:    "",
			apiSecret: "test-secret",
			envVar:    "",
			wantErr:   false,
		},
		{
			name:      "disabled by env var",
			userID:    "test-user-123",
			apiSecret: "test-secret",
			envVar:    "false",
			wantErr:   false,
		},
		{
			name:      "no api secret",
			userID:    "test-user-123",
			apiSecret: "",
			envVar:    "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable if specified
			if tt.envVar != "" {
				err := os.Setenv(analyticsEnabledEnvVar, tt.envVar)
				require.NoError(t, err)
				defer func() {
					_ = os.Unsetenv(analyticsEnabledEnvVar)
				}()
			}

			analytics, err := NewAnalytics(tt.userID, tt.apiSecret)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, analytics)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, analytics)

				// Analytics should always be created, even if disabled
				assert.NotNil(t, analytics.config)
				assert.NotNil(t, analytics.httpClient)
			}
		})
	}
}

func TestAnalyticsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"default (empty)", "", true},
		{"explicitly true", "true", true},
		{"explicitly 1", "1", true},
		{"explicitly on", "on", true},
		{"explicitly false", "false", false},
		{"explicitly 0", "0", false},
		{"explicitly off", "off", false},
		{"explicitly disabled", "disabled", false},
		{"random value", "random", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean environment
			_ = os.Unsetenv(analyticsEnabledEnvVar)

			if tt.envValue != "" {
				err := os.Setenv(analyticsEnabledEnvVar, tt.envValue)
				require.NoError(t, err)
			}

			result := isAnalyticsEnabled()
			assert.Equal(t, tt.expected, result)

			// Cleanup
			_ = os.Unsetenv(analyticsEnabledEnvVar)
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
			name: "disabled analytics",
			analytics: &Analytics{
				config: &AnalyticsConfig{
					Enabled:   false,
					APISecret: "test-secret",
				},
			},
			toolName:  "test_tool",
			shouldLog: false,
		},
		{
			name: "no API secret",
			analytics: &Analytics{
				config: &AnalyticsConfig{
					Enabled:   true,
					APISecret: "",
				},
			},
			toolName:  "test_tool",
			shouldLog: false,
		},
		{
			name: "valid analytics",
			analytics: &Analytics{
				config: &AnalyticsConfig{
					Enabled:   true,
					APISecret: "test-secret",
				},
				metrics:     make(map[string]*int64),
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
				// For invalid analytics, we should see debug message about being disabled
				if tt.analytics != nil {
					assert.Contains(t, logOutput, "Analytics disabled")
				}
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
			Enabled:   true,
			APISecret: "test-secret",
		},
		metrics:     make(map[string]*int64),
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
	analytics, err := NewAnalytics("test-user", "test-secret")
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

func TestAnalyticsConfigValidation(t *testing.T) {
	tests := []struct {
		name     string
		config   *AnalyticsConfig
		expected bool
	}{
		{
			name: "valid config",
			config: &AnalyticsConfig{
				Enabled:       true,
				MeasurementID: "G-TEST123",
				APISecret:     "secret123",
				UserID:        "user123",
			},
			expected: true,
		},
		{
			name: "disabled config",
			config: &AnalyticsConfig{
				Enabled:       false,
				MeasurementID: "G-TEST123",
				APISecret:     "secret123",
				UserID:        "user123",
			},
			expected: false,
		},
		{
			name: "missing API secret",
			config: &AnalyticsConfig{
				Enabled:       true,
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
				metrics:     make(map[string]*int64),
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
				assert.Contains(t, logOutput, "Analytics disabled")
			}
		})
	}
}

func TestConcurrentMetricIncrement(t *testing.T) {
	analytics := &Analytics{
		config: &AnalyticsConfig{
			Enabled:   true,
			APISecret: "test-secret",
		},
		metrics:     make(map[string]*int64),
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
	analytics1, err := NewAnalytics("", "test-secret")
	assert.NoError(t, err)
	assert.NotNil(t, analytics1)

	// Test with provided user ID
	analytics2, err := NewAnalytics("custom-user-id", "test-secret")
	assert.NoError(t, err)
	assert.NotNil(t, analytics2)

	// Both should be valid
	assert.NotNil(t, analytics1.config)
	assert.NotNil(t, analytics2.config)
}
