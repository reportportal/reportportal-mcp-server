package mcpreportportal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/xid"
)

const (
	// Google Analytics 4 Measurement Protocol endpoint
	ga4EndpointURL = "https://www.google-analytics.com/mp/collect"

	// Configuration
	measurementID = "G-WJGRCEFLXF"

	// Environment variable to control analytics
	analyticsEnabledEnvVar = "RP_MCP_ANALYTICS_ENABLED"

	// User ID storage (unified client_id and user_id)
	userIDFileName = ".reportportal-mcp-user-id"

	userID = "692"
)

// AnalyticsConfig holds the analytics configuration
type AnalyticsConfig struct {
	Enabled       bool
	MeasurementID string
	APISecret     string
	UserID        string // Unified ID used as both client_id and user_id
}

// GAEvent represents a Google Analytics 4 event
type GAEvent struct {
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters"`
}

// GAPayload represents the full GA4 payload
type GAPayload struct {
	ClientID    string    `json:"client_id"`
	UserID      string    `json:"user_id,omitempty"`
	Events      []GAEvent `json:"events"`
	TimestampMS int64     `json:"timestamp_micros"`
}

// Analytics handles Google Analytics tracking with batched metrics
type Analytics struct {
	config     *AnalyticsConfig
	httpClient *http.Client

	// Metrics system with atomic counters
	metrics     map[string]*int64 // tool name -> counter
	metricsLock sync.RWMutex      // protects metrics map

	// Background processing
	stopChan   chan struct{}
	wg         sync.WaitGroup
	tickerDone chan struct{}
}

// NewAnalytics creates a new Analytics instance
func NewAnalytics(userID string, apiSecret string) (*Analytics, error) {
	enabled := isAnalyticsEnabled()

	slog.Debug("Initializing analytics",
		"enabled_by_env", enabled,
		"has_api_secret", apiSecret != "",
		"user_id", userID,
		"measurement_id", measurementID,
	)

	// Use provided userID or generate a persistent one
	finalUserID := userID
	if finalUserID == "" {
		generatedID, err := getOrCreateUserID()
		if err != nil {
			slog.Warn("Failed to get user ID, analytics disabled", "error", err)
			enabled = false
			finalUserID = "unknown"
		} else {
			finalUserID = generatedID
		}
	}

	config := &AnalyticsConfig{
		Enabled:       enabled,
		MeasurementID: measurementID,
		APISecret:     apiSecret,
		UserID:        finalUserID,
	}

	if enabled && apiSecret != "" {
		userIDPreview := finalUserID
		if len(finalUserID) > 8 {
			userIDPreview = finalUserID[:8] + "..."
		}
		slog.Debug("Analytics initialized and enabled",
			"measurement_id", measurementID,
			"user_id", userIDPreview,
		)
	} else {
		slog.Debug("Analytics disabled",
			"enabled", enabled,
			"has_api_secret", apiSecret != "",
			"reason", func() string {
				if !enabled {
					return "disabled by environment variable"
				}
				if apiSecret == "" {
					return "missing API secret"
				}
				return "unknown"
			}(),
		)
	}

	analytics := &Analytics{
		config: config,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		metrics:    make(map[string]*int64),
		stopChan:   make(chan struct{}),
		tickerDone: make(chan struct{}),
	}

	// Start background metrics processing if analytics is enabled
	if config.Enabled && apiSecret != "" {
		analytics.startMetricsProcessor()
	}

	return analytics, nil
}

// TrackMCPEvent tracks an MCP tool event by incrementing its metric counter
func (a *Analytics) TrackMCPEvent(ctx context.Context, toolName string) {
	if a == nil || !a.config.Enabled || a.config.APISecret == "" {
		slog.Debug("Analytics disabled or missing API secret",
			"enabled", a != nil && a.config.Enabled,
			"has_secret", a != nil && a.config.APISecret != "",
			"tool", toolName)
		return
	}
	// Simply increment the metric counter - the background processor will handle sending
	a.incrementMetric(toolName)
}

// sendEvent sends an event to Google Analytics 4
func (a *Analytics) sendEvent(ctx context.Context, event GAEvent) error {
	payload := GAPayload{
		ClientID:    a.config.UserID, // Use unified ID for client identification
		UserID:      a.config.UserID, // Use same unified ID for user identification
		Events:      []GAEvent{event},
		TimestampMS: time.Now().UnixMicro(),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal analytics payload: %w", err)
	}

	slog.Debug("Sending GA4 request",
		"measurement_id", a.config.MeasurementID,
		"payload_size", len(jsonData),
		"event_name", event.Name,
	)

	url := fmt.Sprintf("%s?measurement_id=%s&api_secret=%s",
		ga4EndpointURL, a.config.MeasurementID, a.config.APISecret)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create analytics request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	startTime := time.Now()
	resp, err := a.httpClient.Do(req)
	if err != nil {
		slog.Error("GA4 HTTP request failed",
			"error", err,
			"measurement_id", a.config.MeasurementID,
			"duration_ms", time.Since(startTime).Milliseconds(),
		)
		return fmt.Errorf("failed to send analytics request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("Failed to close response body", "error", closeErr)
		}
	}()

	duration := time.Since(startTime)

	// Read response body for logging
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		slog.Warn("Failed to read GA4 response body", "error", readErr)
		body = []byte("failed to read response")
	}

	// Log response details for all status codes
	responseFields := []interface{}{
		"status_code", resp.StatusCode,
		"status_text", http.StatusText(resp.StatusCode),
		"duration_ms", duration.Milliseconds(),
		"response_size", len(body),
		"measurement_id", a.config.MeasurementID,
		"content_type", resp.Header.Get("Content-Type"),
	}

	// Add response headers for debugging
	if len(resp.Header) > 0 {
		responseFields = append(responseFields, "response_headers", fmt.Sprintf("%v", resp.Header))
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		slog.Debug("GA4 analytics event sent successfully",
			responseFields...,
		)
		if len(body) > 0 {
			slog.Debug("GA4 response body (200 OK)", "body", string(body))
		}

	case resp.StatusCode == http.StatusNoContent:
		slog.Debug("GA4 analytics event accepted (no content response)",
			responseFields...,
		)

	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// Client errors (400-499)
		slog.Error("GA4 client error - check payload format or authentication",
			append(responseFields, "response_body", string(body))...,
		)
		return fmt.Errorf(
			"analytics client error %d (%s): %s",
			resp.StatusCode,
			http.StatusText(resp.StatusCode),
			string(body),
		)

	case resp.StatusCode >= 500:
		// Server errors (500+)
		slog.Error("GA4 server error - Google Analytics service issue",
			append(responseFields, "response_body", string(body))...,
		)
		return fmt.Errorf(
			"analytics server error %d (%s): %s",
			resp.StatusCode,
			http.StatusText(resp.StatusCode),
			string(body),
		)

	default:
		// Unexpected status codes
		slog.Warn("GA4 unexpected response status",
			append(responseFields, "response_body", string(body))...,
		)
		return fmt.Errorf(
			"analytics unexpected status %d (%s): %s",
			resp.StatusCode,
			http.StatusText(resp.StatusCode),
			string(body),
		)
	}

	return nil
}

// isAnalyticsEnabled checks if analytics is enabled via environment variable
func isAnalyticsEnabled() bool {
	envValue := os.Getenv(analyticsEnabledEnvVar)
	// Default to true if not set, false if explicitly set to "false", "0", or "off"
	if envValue == "" {
		return true
	}
	switch envValue {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

// getOrCreateUserID gets existing user ID or creates a new one
func getOrCreateUserID() (string, error) {
	userIDPath, err := getUserIDPath()
	if err != nil {
		return "", fmt.Errorf("failed to get user ID path: %w", err)
	}

	// Validate the path to prevent directory traversal
	if !filepath.IsAbs(userIDPath) {
		return "", fmt.Errorf("user ID path must be absolute")
	}

	// Try to read existing user ID
	if data, err := os.ReadFile(filepath.Clean(userIDPath)); err == nil {
		userID := string(bytes.TrimSpace(data))
		if len(userID) > 0 {
			slog.Debug("Using existing user ID", "path", userIDPath)
			return userID, nil
		}
	}

	// Create new user ID
	userID := xid.New().String()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(userIDPath), 0o750); err != nil {
		return "", fmt.Errorf("failed to create user ID directory: %w", err)
	}

	// Write user ID to file
	if err := os.WriteFile(userIDPath, []byte(userID), 0o600); err != nil {
		return "", fmt.Errorf("failed to write user ID: %w", err)
	}

	slog.Debug("Created new user ID", "path", userIDPath, "user_id", userID)
	return userID, nil
}

// getUserIDPath returns the cross-platform path for storing user ID
func getUserIDPath() (string, error) {
	var baseDir string

	switch runtime.GOOS {
	case "windows":
		// Use APPDATA on Windows
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA environment variable not set")
		}
		baseDir = filepath.Join(appData, "ReportPortalMCP")

	case "darwin":
		// Use Application Support on macOS
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, "Library", "Application Support", "ReportPortalMCP")

	default:
		// Use XDG config directory on Linux/Unix
		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			xdgConfig = filepath.Join(homeDir, ".config")
		}
		baseDir = filepath.Join(xdgConfig, "reportportal-mcp")
	}

	return filepath.Join(baseDir, userIDFileName), nil
}

// WithAnalytics wraps a tool handler to add analytics tracking
func (a *Analytics) WithAnalytics(
	toolName string,
	handler server.ToolHandlerFunc,
) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Track the event before executing the tool (synchronous since it's just incrementing a counter)
		if a != nil {
			a.TrackMCPEvent(ctx, toolName)
		}

		// Execute the original handler
		return handler(ctx, request)
	}
}

func GetAnalyticArg() string {
	seed := uint32(0x1337)
	p1Bytes := []byte{107, 110, 74, 83}
	for i := range p1Bytes {
		p1Bytes[i] ^= byte(seed >> (i * 8))
		p1Bytes[i] ^= byte(seed >> (i * 8))
	}
	prefix1 := string(p1Bytes)
	value := string(rune(95))
	encoded := base64.StdEncoding.EncodeToString([]byte{74, 96, 178, 102, 233})
	prefix4 := encoded[:7]

	finalResult := prefix1 + userID + value + prefix4 + "ICYe3PA"
	return finalResult
}

// startMetricsProcessor starts the background goroutine that processes metrics every 10 seconds
func (a *Analytics) startMetricsProcessor() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		slog.Debug("Analytics metrics processor started", "interval", "10s")

		for {
			select {
			case <-ticker.C:
				a.processMetrics()
			case <-a.stopChan:
				slog.Debug("Analytics metrics processor stopped")
				close(a.tickerDone)
				return
			}
		}
	}()
}

// Stop gracefully shuts down the analytics system
func (a *Analytics) Stop() {
	if a == nil || a.stopChan == nil {
		return
	}

	slog.Debug("Stopping analytics metrics processor")
	close(a.stopChan)

	// Wait for ticker goroutine to finish
	select {
	case <-a.tickerDone:
		slog.Debug("Analytics metrics processor stopped gracefully")
	case <-time.After(5 * time.Second):
		slog.Warn("Analytics metrics processor stop timeout")
	}

	a.wg.Wait()
}

// incrementMetric atomically increments the counter for a tool
func (a *Analytics) incrementMetric(toolName string) {
	if a == nil || !a.config.Enabled {
		return
	}

	a.metricsLock.RLock()
	counter, exists := a.metrics[toolName]
	a.metricsLock.RUnlock()

	if !exists {
		// Create new counter if it doesn't exist
		a.metricsLock.Lock()
		// Double-check pattern to avoid race conditions
		if counter, exists = a.metrics[toolName]; !exists {
			counter = new(int64)
			a.metrics[toolName] = counter
		}
		a.metricsLock.Unlock()
	}

	// Atomically increment the counter
	atomic.AddInt64(counter, 1)
}

// processMetrics collects and sends all non-zero metrics to GA4
func (a *Analytics) processMetrics() {
	if a == nil || !a.config.Enabled {
		return
	}

	// Collect all non-zero metrics and reset them
	metricsToSend := make(map[string]int64)

	a.metricsLock.RLock()
	for toolName, counter := range a.metrics {
		if counter != nil {
			count := atomic.SwapInt64(counter, 0) // Atomically get and reset
			if count > 0 {
				metricsToSend[toolName] = count
			}
		}
	}
	a.metricsLock.RUnlock()

	if len(metricsToSend) == 0 {
		slog.Debug("No metrics to send")
		return
	}

	slog.Debug("Processing batched metrics",
		"tools_count", len(metricsToSend),
		"total_events", func() int64 {
			var total int64
			for _, count := range metricsToSend {
				total += count
			}
			return total
		}(),
	)

	// Send metrics as a batch to GA4
	a.sendBatchMetrics(context.Background(), metricsToSend)
}

// sendBatchMetrics sends multiple tool metrics as a batch event to GA4
func (a *Analytics) sendBatchMetrics(ctx context.Context, metrics map[string]int64) {
	if len(metrics) == 0 {
		return
	}

	// Create batch event with all tool metrics
	event := GAEvent{
		Name: "mcp_batch_event",
		Parameters: map[string]interface{}{
			"user_id":    a.config.UserID,
			"platform":   runtime.GOOS,
			"version":    "1.0.0",
			"batch_size": len(metrics),
		},
	}

	// Add each tool metric as a parameter
	for toolName, count := range metrics {
		// Use tool_<name>_count as parameter name
		paramName := fmt.Sprintf("tool_%s_count", toolName)
		event.Parameters[paramName] = count

		slog.Debug("Adding tool metric to batch",
			"tool", toolName,
			"count", count,
			"param_name", paramName,
		)
	}

	if err := a.sendEvent(ctx, event); err != nil {
		slog.Error("Failed to send batch metrics", "error", err, "metrics_count", len(metrics))
	} else {
		slog.Debug("Batch metrics sent successfully",
			"tools_count", len(metrics),
			"measurement_id", a.config.MeasurementID,
		)
	}
}
