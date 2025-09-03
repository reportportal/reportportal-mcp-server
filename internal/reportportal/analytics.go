package mcpreportportal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// Google Analytics 4 Measurement Protocol endpoint
	ga4EndpointURL = "https://www.google-analytics.com/mp/collect"

	// Configuration
	measurementID = "G-WJGRCEFLXF"

	userID = "692"

	HashAlgorithm = "SHA256-128bit"

	// Batch send interval for analytics data
	batchSendInterval = 10 * time.Second

	maxPerRequest = 25
)

// HashToken creates a secure hash of the token
func HashToken(token string) string {
	if token == "" {
		return ""
	}

	// Create full SHA256 hash of the token
	hash := sha256.Sum256([]byte(token))

	// Return full hash
	return hex.EncodeToString(hash[:])
}

// AnalyticsConfig holds the analytics configuration
type AnalyticsConfig struct {
	MeasurementID string
	APISecret     string
	UserID        string // Unified ID used as both client_id and user_id
}

// GAEvent represents a Google Analytics 4 event
type GAEvent struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
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

// NewAnalytics creates a new Analytics instance with mandatory RP API token for secure hashing
// Parameters:
//   - userID: Custom user identifier (used for logging purposes only)
//   - apiSecret: Google Analytics 4 API secret for authentication (required)
//   - rpAPIToken: ReportPortal API token for secure hashing (required, always used for actual user ID)
//
// Returns error if apiSecret or rpAPIToken is empty
func NewAnalytics(userID string, apiSecret string, rpAPIToken string) (*Analytics, error) {
	// Analytics enablement is now controlled by the caller (CLI flags)
	slog.Debug("Initializing analytics",
		"has_ga4_secret", apiSecret != "",
		"user_id", userID,
		"has_rp_token", rpAPIToken != "",
		"measurement_id", measurementID,
	)

	// If GA4 API secret is empty, disable analytics
	if apiSecret == "" {
		return nil, fmt.Errorf("analytics disabled: missing GA4 API secret")
	}

	// RP API token is required for security
	if rpAPIToken == "" {
		return nil, fmt.Errorf("analytics disabled: missing RP API token for secure hashing")
	}

	// Use RP API token's secure hash as the primary identifier
	hashedToken := HashToken(rpAPIToken)

	config := &AnalyticsConfig{
		MeasurementID: measurementID,
		APISecret:     apiSecret,
		UserID:        hashedToken,
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

	analytics.startMetricsProcessor()

	return analytics, nil
}

// TrackMCPEvent tracks an MCP tool event by incrementing its metric counter
func (a *Analytics) TrackMCPEvent(ctx context.Context, toolName string) {
	if a == nil {
		slog.Debug("Analytics disabled",
			"tool", toolName)
		return
	}
	// Simply increment the metric counter - the background processor will handle sending
	a.incrementMetric(toolName)
}

// sendBatchEvents sends multiple events to Google Analytics 4 in a single HTTP request
func (a *Analytics) sendBatchEvents(ctx context.Context, events []GAEvent) error {
	if len(events) == 0 {
		return nil
	}

	payload := GAPayload{
		ClientID:    a.config.UserID, // Contains number with "." postfix
		UserID:      a.config.UserID, // Contains number with "." postfix
		Events:      events,          // Multiple events in single request
		TimestampMS: time.Now().UnixMicro(),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal analytics payload: %w", err)
	}

	// Log the outgoing request details with pretty-printed JSON
	slog.Debug("GA4 Batch HTTP Request", "events_count", len(events))

	// Pretty print the JSON payload for debugging
	var prettyPayload interface{}
	if jsonErr := json.Unmarshal(jsonData, &prettyPayload); jsonErr == nil {
		if prettyData, prettyErr := json.MarshalIndent(prettyPayload, "", "  "); prettyErr == nil {
			slog.Debug("Batch request payload:", "json", string(prettyData))
		} else {
			slog.Debug("Batch request payload:", "json", string(jsonData))
		}
	} else {
		slog.Debug("Batch request payload:", "json", string(jsonData))
	}

	url := fmt.Sprintf("%s?measurement_id=%s&api_secret=%s",
		ga4EndpointURL, a.config.MeasurementID, a.config.APISecret)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create analytics request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		slog.Error("GA4 batch HTTP request failed",
			"error", err,
			"events_count", len(events),
		)
		return fmt.Errorf("failed to send analytics request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("Failed to close response body", "error", closeErr)
		}
	}()

	// Read response body for logging
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		slog.Warn("Failed to read GA4 response body", "error", readErr)
		body = []byte("failed to read response")
	}

	// Pretty print response body if it's JSON
	if len(body) > 0 {
		var prettyJSON interface{}
		if jsonErr := json.Unmarshal(body, &prettyJSON); jsonErr == nil {
			if prettyBody, prettyErr := json.MarshalIndent(prettyJSON, "", "  "); prettyErr == nil {
				slog.Debug("Batch response body:", "json", string(prettyBody))
			} else {
				slog.Debug("Batch response body:", "text", string(body))
			}
		} else {
			slog.Debug("Batch response body:", "text", string(body))
		}
	}

	// Log response details for all status codes
	statusInfo := fmt.Sprintf("%d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
	slog.Debug("GA4 Batch HTTP Response", "status", statusInfo, "events_count", len(events))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GA4 batch HTTP error: status=%s", statusInfo)
	}
	return nil
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

// startMetricsProcessor starts the background goroutine that sends analytics batches at regular intervals
func (a *Analytics) startMetricsProcessor() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(batchSendInterval)
		defer ticker.Stop()

		slog.Debug("Analytics metrics processor started", "interval", batchSendInterval)

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
	if a == nil {
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
	if a == nil {
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
		slog.Debug("No metrics to send")
		return
	}

	// Use the full hashed UserID for custom_user_id parameter
	customUserID := a.config.UserID

	// Collect all individual events for batch sending
	var events []GAEvent

	// Create individual events for each tool usage (matching analytics-client format)
	for toolName, count := range metrics {
		// Create multiple events if count > 1 (each tool usage gets its own event)
		for i := int64(0); i < count; i++ {
			event := GAEvent{
				Name: "mcp_event_triggered",
				Params: map[string]interface{}{
					"custom_user_id": customUserID,          // The unique number of the users
					"event_name":     "mcp_event_triggered", // Event name
					"tool":           toolName,              // The name of the tool
				},
			}
			events = append(events, event)
		}
	}

	// Send events in chunks to respect GA4 MP limits (e.g., 25 events/request)
	sent := 0
	failed := 0
	for start := 0; start < len(events); start += maxPerRequest {
		end := start + maxPerRequest
		if end > len(events) {
			end = len(events)
		}
		if err := a.sendBatchEvents(ctx, events[start:end]); err != nil {
			failed += end - start
			slog.Error("Failed to send batch tool events", "error", err, "events_count", end-start)
			continue
		}
		sent += end - start
	}
}

// StopAnalytics gracefully stops the analytics client if it exists
func StopAnalytics(analytics *Analytics, reason string) {
	if analytics != nil {
		if reason != "" {
			slog.Info("stopping analytics", "reason", reason)
		} else {
			slog.Info("stopping analytics...")
		}
		analytics.Stop()
	}
}
