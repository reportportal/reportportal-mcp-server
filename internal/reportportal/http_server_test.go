package mcpreportportal

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPServer_WithoutRPAPIToken(t *testing.T) {
	tests := []struct {
		name               string
		config             HTTPServerConfig
		expectAnalytics    bool
		expectError        bool
		expectedErrMessage string
	}{
		{
			name: "server starts without RP_API_TOKEN",
			config: HTTPServerConfig{
				Version: "1.0.0",
				HostURL: mustParseURL("https://reportportal.example.com"),
				// FallbackRPToken is empty
				FallbackRPToken:       "",
				UserID:                "test-user",
				GA4Secret:             "test-secret",
				AnalyticsOn:           true,
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: true, // Analytics should be initialized using UserID as alternative identifier
			expectError:     false,
		},
		{
			name: "server starts with invalid RP_API_TOKEN",
			config: HTTPServerConfig{
				Version:               "1.0.0",
				HostURL:               mustParseURL("https://reportportal.example.com"),
				FallbackRPToken:       "invalid-token", // Invalid token format (but still used for hash)
				UserID:                "test-user",
				GA4Secret:             "test-secret",
				AnalyticsOn:           true,
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: true, // Analytics should be initialized using RP token hash as identifier
			expectError:     false,
		},
		{
			name: "server starts with valid RP_API_TOKEN",
			config: HTTPServerConfig{
				Version:               "1.0.0",
				HostURL:               mustParseURL("https://reportportal.example.com"),
				FallbackRPToken:       "550e8400-e29b-41d4-a716-446655440000", // Valid UUID token
				UserID:                "test-user",
				GA4Secret:             "test-secret",
				AnalyticsOn:           true,
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: true, // Analytics should be initialized with valid token
			expectError:     false,
		},
		{
			name: "server starts without analytics enabled",
			config: HTTPServerConfig{
				Version:               "1.0.0",
				HostURL:               mustParseURL("https://reportportal.example.com"),
				FallbackRPToken:       "",
				UserID:                "test-user",
				GA4Secret:             "test-secret",
				AnalyticsOn:           false, // Analytics disabled
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: false, // Analytics should not be initialized when disabled
			expectError:     false,
		},
		{
			name: "server starts without GA4 secret",
			config: HTTPServerConfig{
				Version:               "1.0.0",
				HostURL:               mustParseURL("https://reportportal.example.com"),
				FallbackRPToken:       "550e8400-e29b-41d4-a716-446655440000",
				UserID:                "test-user",
				GA4Secret:             "", // No GA4 secret
				AnalyticsOn:           true,
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: false, // Analytics should not be initialized without GA4 secret
			expectError:     false,
		},
		{
			name: "server starts with default configuration values",
			config: HTTPServerConfig{
				Version:         "1.0.0",
				HostURL:         mustParseURL("https://reportportal.example.com"),
				FallbackRPToken: "",
				// MaxConcurrentRequests and ConnectionTimeout will use defaults
			},
			expectAnalytics: false,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpServer, err := NewHTTPServer(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.expectedErrMessage != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMessage)
				}
				assert.Nil(t, httpServer)
				return
			}

			// Server should be created successfully
			require.NoError(t, err)
			require.NotNil(t, httpServer)

			// Verify server components are initialized
			assert.NotNil(t, httpServer.mcpServer, "MCP server should be initialized")
			assert.NotNil(t, httpServer.httpClient, "HTTP client should be initialized")
			assert.NotNil(t, httpServer.Router, "Chi router should be initialized")
			assert.NotNil(t, httpServer.streamableServer, "Streamable server should be initialized")

			// Verify analytics initialization based on configuration
			if tt.expectAnalytics {
				assert.NotNil(t, httpServer.analytics, "Analytics should be initialized")
			} else {
				assert.Nil(t, httpServer.analytics, "Analytics should not be initialized")
			}

			// Verify config defaults are applied
			assert.NotZero(
				t,
				httpServer.config.MaxConcurrentRequests,
				"MaxConcurrentRequests should have a default value",
			)
			assert.NotZero(
				t,
				httpServer.config.ConnectionTimeout,
				"ConnectionTimeout should have a default value",
			)

			// Verify server is not running by default
			httpServer.runningMux.RLock()
			assert.False(
				t,
				httpServer.running,
				"Server should not be running immediately after creation",
			)
			httpServer.runningMux.RUnlock()

			// Test server lifecycle
			err = httpServer.Start()
			assert.NoError(t, err, "Server should start successfully")

			httpServer.runningMux.RLock()
			assert.True(t, httpServer.running, "Server should be marked as running after Start()")
			httpServer.runningMux.RUnlock()

			// Verify we can't start an already running server
			err = httpServer.Start()
			assert.Error(t, err, "Starting an already running server should return an error")
			assert.Contains(t, err.Error(), "already running")

			// Test server stop
			err = httpServer.Stop()
			assert.NoError(t, err, "Server should stop successfully")

			httpServer.runningMux.RLock()
			assert.False(t, httpServer.running, "Server should not be running after Stop()")
			httpServer.runningMux.RUnlock()
		})
	}
}

func TestCreateHTTPServerWithMiddleware_WithoutRPAPIToken(t *testing.T) {
	tests := []struct {
		name               string
		config             HTTPServerConfig
		expectAnalytics    bool
		expectError        bool
		expectedErrMessage string
	}{
		{
			name: "server with middleware starts without RP_API_TOKEN",
			config: HTTPServerConfig{
				Version:               "1.0.0",
				HostURL:               mustParseURL("https://reportportal.example.com"),
				FallbackRPToken:       "",
				UserID:                "test-user",
				GA4Secret:             "test-secret",
				AnalyticsOn:           true,
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: true, // Analytics should be initialized using UserID
			expectError:     false,
		},
		{
			name: "server with middleware starts with valid token",
			config: HTTPServerConfig{
				Version:               "1.0.0",
				HostURL:               mustParseURL("https://reportportal.example.com"),
				FallbackRPToken:       "550e8400-e29b-41d4-a716-446655440000",
				UserID:                "test-user",
				GA4Secret:             "test-secret",
				AnalyticsOn:           true,
				MaxConcurrentRequests: 10,
				ConnectionTimeout:     30 * time.Second,
			},
			expectAnalytics: true,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper, analytics, err := CreateHTTPServerWithMiddleware(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.expectedErrMessage != "" {
					assert.Contains(t, err.Error(), tt.expectedErrMessage)
				}
				assert.Nil(t, wrapper)
				assert.Nil(t, analytics)
				return
			}

			// Server wrapper should be created successfully
			require.NoError(t, err)
			require.NotNil(t, wrapper)
			require.NotNil(t, wrapper.MCP)

			// Verify handler is set up
			assert.NotNil(t, wrapper.Handler, "HTTP handler should be initialized")

			// Verify analytics based on configuration
			if tt.expectAnalytics {
				assert.NotNil(t, analytics, "Analytics should be returned")
				assert.NotNil(t, wrapper.MCP.analytics, "MCP server should have analytics")
			} else {
				assert.Nil(t, analytics, "Analytics should not be returned")
				assert.Nil(t, wrapper.MCP.analytics, "MCP server should not have analytics")
			}

			// Verify routes are set up correctly
			assert.NotNil(t, wrapper.MCP.Router, "Router should be initialized")
		})
	}
}

func TestHTTPServerConfig_Defaults(t *testing.T) {
	config := HTTPServerConfig{
		Version:         "1.0.0",
		HostURL:         mustParseURL("https://reportportal.example.com"),
		FallbackRPToken: "",
		// No MaxConcurrentRequests or ConnectionTimeout specified
	}

	httpServer, err := NewHTTPServer(config)
	require.NoError(t, err)
	require.NotNil(t, httpServer)

	// Verify defaults are applied
	assert.Greater(t, httpServer.config.MaxConcurrentRequests, 0,
		"MaxConcurrentRequests should have a positive default value")
	assert.Greater(t, httpServer.config.ConnectionTimeout, time.Duration(0),
		"ConnectionTimeout should have a positive default value")
	assert.Equal(t, 30*time.Second, httpServer.config.ConnectionTimeout,
		"ConnectionTimeout default should be 30 seconds")
}

func TestHTTPServer_StartStop(t *testing.T) {
	config := HTTPServerConfig{
		Version:         "1.0.0",
		HostURL:         mustParseURL("https://reportportal.example.com"),
		FallbackRPToken: "",
	}

	httpServer, err := NewHTTPServer(config)
	require.NoError(t, err)
	require.NotNil(t, httpServer)

	// Test multiple start/stop cycles
	for i := 0; i < 3; i++ {
		err = httpServer.Start()
		assert.NoError(t, err, "Server should start successfully on cycle %d", i)

		// Verify server is running
		httpServer.runningMux.RLock()
		running := httpServer.running
		httpServer.runningMux.RUnlock()
		assert.True(t, running, "Server should be running after Start() on cycle %d", i)

		err = httpServer.Stop()
		assert.NoError(t, err, "Server should stop successfully on cycle %d", i)

		// Verify server is stopped
		httpServer.runningMux.RLock()
		running = httpServer.running
		httpServer.runningMux.RUnlock()
		assert.False(t, running, "Server should not be running after Stop() on cycle %d", i)
	}
}

func TestHTTPServer_StopIdempotent(t *testing.T) {
	config := HTTPServerConfig{
		Version:         "1.0.0",
		HostURL:         mustParseURL("https://reportportal.example.com"),
		FallbackRPToken: "",
	}

	httpServer, err := NewHTTPServer(config)
	require.NoError(t, err)
	require.NotNil(t, httpServer)

	// Stop server multiple times without starting it
	for i := 0; i < 3; i++ {
		err = httpServer.Stop()
		assert.NoError(t, err, "Stop should be idempotent and not error on call %d", i)
	}
}

func TestGetHTTPServerInfo(t *testing.T) {
	tests := []struct {
		name             string
		analytics        *Analytics
		expectAnalytics  bool
		expectedType     string
		expectedInterval string
	}{
		{
			name:            "server info without analytics",
			analytics:       nil,
			expectAnalytics: false,
		},
		{
			name: "server info with analytics",
			analytics: &Analytics{
				config: &AnalyticsConfig{
					APISecret: "test-secret",
				},
			},
			expectAnalytics:  true,
			expectedType:     "batch",
			expectedInterval: batchSendInterval.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := GetHTTPServerInfo(tt.analytics)

			assert.Equal(t, "http_mcp_server", info.Type)

			if tt.expectAnalytics {
				assert.True(t, info.Analytics.Enabled)
				assert.Equal(t, tt.expectedType, info.Analytics.Type)
				assert.Equal(t, tt.expectedInterval, info.Analytics.Interval)
			} else {
				assert.False(t, info.Analytics.Enabled)
			}
		})
	}
}

// mustParseURL is a helper function to parse URLs for tests
func mustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return u
}
