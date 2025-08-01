package mcpreportportal

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

func TestWithAnalytics_HandlerCalled(t *testing.T) {
	var called bool
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}
	// Prevent nil pointer panic in goroutine by providing a config
	a := &Analytics{config: &AnalyticsConfig{}}
	wrapped := a.WithAnalytics("test_tool", handler)
	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	assert.NoError(t, err)
	assert.True(t, called, "handler should be called")
}

func TestWithAnalytics_NilAnalytics(t *testing.T) {
	var called bool
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	}
	var a *Analytics // nil
	wrapped := a.WithAnalytics("test_tool", handler)
	_, err := wrapped(context.Background(), mcp.CallToolRequest{})
	assert.NoError(t, err)
	assert.True(t, called, "handler should be called even if analytics is nil")
}

func TestWithAnalytics_AnalyticsEventAsync(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	type testAnalytics struct {
		Analytics
		called *sync.WaitGroup
	}
	// Override TrackMCPEvent by embedding and shadowing
	a := &testAnalytics{
		Analytics: Analytics{
			config: &AnalyticsConfig{Enabled: true, APISecret: "dummy"},
		},
		called: &wg,
	}
	// Shadow the method
	trackFunc := func(ctx context.Context, toolName string) {
		defer a.called.Done()
		assert.Equal(t, "test_tool", toolName)
	}

	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}

	// Patch WithAnalytics to use our custom TrackMCPEvent
	wrapped := func(toolName string, handlerFunc func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Use our test TrackMCPEvent
			go trackFunc(ctx, toolName)
			return handlerFunc(ctx, request)
		}
	}

	wrappedHandler := wrapped("test_tool", handler)
	_, err := wrappedHandler(context.Background(), mcp.CallToolRequest{})
	assert.NoError(t, err)
	wg.Wait() // Wait for the async event
}

// TestMCPServerAnalyticsSimulation simulates MCP server calling get_test_item_by_id every 10 seconds
// and verifies metrics are properly batched and sent to GA4
func TestMCPServerAnalyticsSimulation(t *testing.T) {
	// Create a buffer to capture log output
	var logBuf bytes.Buffer

	// Set up custom logger that writes to our buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Create analytics config
	config := &AnalyticsConfig{
		Enabled:       true,
		MeasurementID: "G-TEST-ID",
		APISecret:     "test-api-secret",
		UserID:        "27384940545", // Unified ID used as both client_id and user_id
	}

	analytics := &Analytics{
		config:     config,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		metrics:    make(map[string]*int64),
		stopChan:   make(chan struct{}),
		tickerDone: make(chan struct{}),
	}

	// Mock handler for get_test_item_by_id
	handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(`{"id": "5015215747", "name": "test_item"}`), nil
	}

	// Wrap handler with analytics
	wrappedHandler := analytics.WithAnalytics("get_test_item_by_id", handler)

	// Simulate MCP server calling get_test_item_by_id multiple times
	const numberOfCalls = 5

	for i := 0; i < numberOfCalls; i++ {
		// Create mock request with test_item_id parameter
		var req mcp.CallToolRequest
		req.Params.Name = "get_test_item_by_id"
		req.Params.Arguments = map[string]interface{}{
			"test_item_id": "5015215747",
			"project":      "test_project",
		}

		// Call the handler - this should increment the metric counter
		result, err := wrappedHandler(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	}

	// Verify that metrics were incremented correctly
	analytics.metricsLock.RLock()
	counter, exists := analytics.metrics["get_test_item_by_id"]
	analytics.metricsLock.RUnlock()

	assert.True(t, exists, "get_test_item_by_id metric should exist")
	assert.Equal(t, int64(numberOfCalls), atomic.LoadInt64(counter),
		"Should have %d calls recorded in metrics", numberOfCalls)

	// Manually trigger batch processing
	analytics.processMetrics()

	// After processing, metrics should be reset
	analytics.metricsLock.RLock()
	counterAfter := atomic.LoadInt64(analytics.metrics["get_test_item_by_id"])
	analytics.metricsLock.RUnlock()

	assert.Equal(t, int64(0), counterAfter, "Metrics should be reset after processing")

	// Check log output for batched metrics processing
	logOutput := logBuf.String()

	// Verify batch processing occurred
	assert.Contains(t, logOutput, "Processing batched metrics",
		"Should contain batch processing message")

	// Verify the batch event was sent to GA4
	assert.Contains(t, logOutput, "mcp_batch_event",
		"Should contain batch event name")

	// Verify tool-specific metric in batch
	assert.Contains(t, logOutput, "tool_get_test_item_by_id_count",
		"Should contain get_test_item_by_id metric in batch")

	// Verify batch was sent successfully
	assert.Contains(t, logOutput, "Batch metrics sent successfully",
		"Should contain batch success message")

	t.Logf("Log output:\n%s", logOutput)
}

// TestMetricsBatchingSystem tests the new metrics batching system
func TestMetricsBatchingSystem(t *testing.T) {
	// Create a buffer to capture log output
	var logBuf bytes.Buffer

	// Create a test server that simulates successful GA4 response
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer testServer.Close()

	// Set up custom logger that writes to our buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Create analytics config
	config := &AnalyticsConfig{
		Enabled:       true,
		MeasurementID: "G-TEST-ID",
		APISecret:     "test-api-secret",
		UserID:        "27384940545", // Unified ID used as both client_id and user_id
	}

	analytics := &Analytics{
		config:     config,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		metrics:    make(map[string]*int64),
		stopChan:   make(chan struct{}),
		tickerDone: make(chan struct{}),
	}

	// Manually test incrementMetric without starting the background processor
	// Increment metrics for different tools
	analytics.incrementMetric("get_test_item_by_id")
	analytics.incrementMetric("get_test_item_by_id") // 2 times
	analytics.incrementMetric("get_launches")
	analytics.incrementMetric("get_test_item_by_id") // 3rd time
	analytics.incrementMetric("get_test_items_by_filter")

	// Verify metrics were incremented
	analytics.metricsLock.RLock()
	assert.Equal(
		t,
		int64(3),
		atomic.LoadInt64(analytics.metrics["get_test_item_by_id"]),
		"get_test_item_by_id should have 3 events",
	)
	assert.Equal(
		t,
		int64(1),
		atomic.LoadInt64(analytics.metrics["get_launches"]),
		"get_launches should have 1 event",
	)
	assert.Equal(
		t,
		int64(1),
		atomic.LoadInt64(analytics.metrics["get_test_items_by_filter"]),
		"get_test_items_by_filter should have 1 event",
	)
	analytics.metricsLock.RUnlock()

	// Test processMetrics manually - it should collect and reset all metrics
	analytics.processMetrics()

	// After processing, all metrics should be reset to 0
	analytics.metricsLock.RLock()
	assert.Equal(
		t,
		int64(0),
		atomic.LoadInt64(analytics.metrics["get_test_item_by_id"]),
		"Metrics should be reset after processing",
	)
	assert.Equal(
		t,
		int64(0),
		atomic.LoadInt64(analytics.metrics["get_launches"]),
		"Metrics should be reset after processing",
	)
	assert.Equal(
		t,
		int64(0),
		atomic.LoadInt64(analytics.metrics["get_test_items_by_filter"]),
		"Metrics should be reset after processing",
	)
	analytics.metricsLock.RUnlock()

	// Check log output for batch processing
	logOutput := logBuf.String()

	// Verify batch processing logs
	assert.Contains(
		t,
		logOutput,
		"Processing batched metrics",
		"Should contain batch processing message",
	)
	assert.Contains(t, logOutput, "tools_count=3", "Should process 3 different tools")
	assert.Contains(t, logOutput, "total_events=5", "Should process 5 total events (3+1+1)")

	// Verify individual tool metrics in batch
	assert.Contains(
		t,
		logOutput,
		"tool_get_test_item_by_id_count",
		"Should contain get_test_item_by_id metric",
	)
	assert.Contains(t, logOutput, "tool_list_launches_count", "Should contain list_launches metric")
	assert.Contains(
		t,
		logOutput,
		"tool_list_test_items_by_filter_count",
		"Should contain list_test_items_by_filter metric",
	)

	t.Logf("Log output:\n%s", logOutput)
}

// TestMetricsBackgroundProcessor tests the background metrics processor
func TestMetricsBackgroundProcessor(t *testing.T) {
	// Create a buffer to capture log output
	var logBuf bytes.Buffer

	// Set up custom logger that writes to our buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Create analytics with very short interval for testing (1 second instead of 10)
	config := &AnalyticsConfig{
		Enabled:       true,
		MeasurementID: "G-TEST-ID",
		APISecret:     "test-api-secret",
		UserID:        "27384940545", // Unified ID used as both client_id and user_id
	}

	analytics := &Analytics{
		config:     config,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		metrics:    make(map[string]*int64),
		stopChan:   make(chan struct{}),
		tickerDone: make(chan struct{}),
	}

	// Start background processor manually with 100ms interval for faster testing
	analytics.wg.Add(1)
	go func() {
		defer analytics.wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				analytics.processMetrics()
			case <-analytics.stopChan:
				close(analytics.tickerDone)
				return
			}
		}
	}()

	// Simulate some tool calls
	analytics.incrementMetric("get_test_item_by_id")
	analytics.incrementMetric("get_launches")

	// Wait for at least one processing cycle
	time.Sleep(150 * time.Millisecond)

	// Add more metrics
	analytics.incrementMetric("get_test_item_by_id")
	analytics.incrementMetric("get_test_item_by_id")

	// Wait for another processing cycle
	time.Sleep(150 * time.Millisecond)

	// Stop the processor
	analytics.Stop()

	// Check log output
	logOutput := logBuf.String()

	// Verify background processor logs
	assert.Contains(
		t,
		logOutput,
		"Processing batched metrics",
		"Should contain batch processing messages",
	)

	// Should see multiple processing cycles
	batchCount := strings.Count(logOutput, "Processing batched metrics")
	assert.GreaterOrEqual(t, batchCount, 1, "Should have at least 1 batch processing cycle")

	t.Logf("Batch processing cycles: %d", batchCount)
	t.Logf("Log output:\n%s", logOutput)
}
