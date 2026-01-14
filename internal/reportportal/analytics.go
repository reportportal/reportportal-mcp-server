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
	"strings"
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

	// Timeout for fetching instance ID from ReportPortal
	instanceIDFetchTimeout = 5 * time.Second

	// Pre-computed hash for anonymous mode to avoid repeated computation
	anonymousUserIDHash = "fc8c4264d21cd5dac3de0e2255396f6cd0809d353aa8071873060ba1867ac0b3"
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

// truncateForLog safely truncates a string for logging purposes
// Returns the first maxLen characters followed by "..." if the string is longer
func truncateForLog(s string, maxLen int) string {
	// Handle empty string
	if s == "" {
		return ""
	}

	// Handle invalid maxLen
	if maxLen <= 0 {
		return "..."
	}

	// Return as-is if within limit
	if len(s) <= maxLen {
		return s
	}

	// Truncate and add ellipsis
	return s[:maxLen] + "..."
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

	// ReportPortal instance ID (fetched lazily on first use, retried until successful)
	instanceID        string      // ReportPortal instance ID from /api/info endpoint
	instanceIDFetched atomic.Bool // Atomic flag indicating if instanceID is fetched (for fast-path check)
	instanceIDLock    sync.Mutex  // Protects instanceID field during fetch
	rpHostURL         string      // ReportPortal host URL for lazy fetching

	// Metrics system with atomic counters
	// Map structure: userID -> toolName -> counter
	metrics     map[string]map[string]*int64 // userID -> (tool name -> counter)
	metricsLock sync.RWMutex                 // protects metrics map

	// Background processing
	stopChan   chan struct{}
	wg         sync.WaitGroup
	tickerDone chan struct{}
}

// ensureInstanceID lazily fetches the instance ID if not already set
// Retries on each call until a non-empty instance ID is retrieved
// Thread-safe for concurrent calls using atomic bool for fast-path check
func (a *Analytics) ensureInstanceID() {
	// Fast path: check atomic flag without any locks
	if a.instanceIDFetched.Load() {
		return
	}

	// Slow path: need to fetch (mutex lock)
	a.instanceIDLock.Lock()
	defer a.instanceIDLock.Unlock()

	// Double-check after acquiring lock (another goroutine might have fetched it)
	if a.instanceIDFetched.Load() {
		return
	}

	// Check if host URL is configured
	if a.rpHostURL == "" {
		slog.Debug("Cannot fetch instance ID: host URL is empty")
		return
	}

	// Attempt to fetch instance ID
	fetchedID := fetchInstanceID(a.rpHostURL, a.httpClient)
	if fetchedID != "" {
		a.instanceID = fetchedID
		a.instanceIDFetched.Store(true) // Mark as fetched
		slog.Debug("Successfully fetched and stored instance ID", "instance_id", fetchedID)
	} else {
		slog.Debug("Instance ID fetch returned empty, will retry on next tool execution")
	}
}

// fetchInstanceID retrieves the ReportPortal instance ID from the /api/info endpoint
// The endpoint is available without authentication
func fetchInstanceID(hostURL string, httpClient *http.Client) string {
	// Build the API info endpoint URL
	apiURL := fmt.Sprintf("%s/api/info", strings.TrimSuffix(hostURL, "/"))

	ctx, cancel := context.WithTimeout(context.Background(), instanceIDFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		slog.Warn("Failed to create request for instance ID", "error", err)
		return ""
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Warn("Failed to fetch instance ID from ReportPortal", "error", err, "url", apiURL)
		return ""
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("Failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("Unexpected status code when fetching instance ID",
			"status", resp.StatusCode,
			"url", apiURL)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("Failed to read instance ID response body", "error", err)
		return ""
	}

	// Parse the JSON response
	var apiInfo map[string]interface{}
	if err := json.Unmarshal(body, &apiInfo); err != nil {
		slog.Warn("Failed to parse instance ID response", "error", err)
		return ""
	}

	// Navigate to extensions.result['server.details.instance']
	extensions, ok := apiInfo["extensions"].(map[string]interface{})
	if !ok {
		slog.Warn("Instance ID: extensions field not found or invalid type")
		return ""
	}

	result, ok := extensions["result"].(map[string]interface{})
	if !ok {
		slog.Warn("Instance ID: extensions.result field not found or invalid type")
		return ""
	}

	instanceID, ok := result["server.details.instance"].(string)
	if !ok {
		slog.Warn("Instance ID: server.details.instance field not found or invalid type")
		return ""
	}

	slog.Debug("Successfully fetched ReportPortal instance ID",
		"instance_id", instanceID)
	return instanceID
}

// NewAnalytics creates a new Analytics instance
// Parameters:
//   - userID: Custom user identifier (if empty, a generic ID will be generated)
//   - apiSecret: Google Analytics 4 API secret for authentication (required)
//   - rpAPIToken: ReportPortal API token for secure hashing (optional, used when available)
//   - rpHostURL: ReportPortal host URL for fetching instance ID (optional)
//
// Returns error if apiSecret is empty
func NewAnalytics(
	userID string,
	apiSecret string,
	rpAPIToken string,
	rpHostURL string,
) (*Analytics, error) {
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

	// Determine the user identifier to use
	var analyticsUserID string
	if rpAPIToken != "" {
		// Prefer RP API token's secure hash as the primary identifier
		analyticsUserID = HashToken(rpAPIToken)
		slog.Debug("Using RP token hash as user ID for analytics")
	} else if userID != "" {
		// Use provided user ID if available
		analyticsUserID = HashToken(userID)
		slog.Debug("Using custom user ID for analytics", "user_id_hash", truncateForLog(analyticsUserID, 16))
	} else {
		// Generate a generic identifier for anonymous tracking
		analyticsUserID = anonymousUserIDHash
		slog.Debug("Using anonymous user ID for analytics (no token or user ID provided)")
	}

	config := &AnalyticsConfig{
		MeasurementID: measurementID,
		APISecret:     apiSecret,
		UserID:        analyticsUserID,
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	analytics := &Analytics{
		config:     config,
		httpClient: httpClient,
		rpHostURL:  rpHostURL,                          // Store for lazy fetching
		instanceID: "",                                 // Will be fetched lazily on first use
		metrics:    make(map[string]map[string]*int64), // userID -> toolName -> counter
		stopChan:   make(chan struct{}),
		tickerDone: make(chan struct{}),
	}

	analytics.startMetricsProcessor()

	return analytics, nil
}

// TrackMCPEvent tracks an MCP tool event by incrementing its metric counter
// It extracts the RP token from context (if available) and uses it for per-user tracking
func (a *Analytics) TrackMCPEvent(ctx context.Context, toolName string) {
	if a == nil {
		slog.Debug("Analytics disabled",
			"tool", toolName)
		return
	}

	// Extract token from context and determine user ID
	userID := a.getUserIDFromContext(ctx)

	// Increment the metric counter for this user - the background processor will handle sending
	a.incrementMetric(userID, toolName)
}

// getUserIDFromContext extracts the user ID for analytics tracking
// Priority: 1. Default config user ID (from RP_API_TOKEN env var), 2. Token from context (Bearer header)
func (a *Analytics) getUserIDFromContext(ctx context.Context) string {
	// First check if config UserID is from RP token or custom user ID (not anonymous)
	// If RP_API_TOKEN env var was set, config.UserID will be its hash
	// We want to use the env var token if it was provided
	if a.config.UserID != anonymousUserIDHash {
		// Config has a real user ID (from RP_API_TOKEN env var or RP_USER_ID)
		slog.Debug("Using RP_API_TOKEN or RP_USER_ID for analytics", "source", "env_var")
		return a.config.UserID
	}

	// If no env var token/user ID was set (anonymous mode), try to get token from context
	if token, ok := GetTokenFromContext(ctx); ok && token != "" {
		// Hash the Bearer token to get a secure user identifier
		hashedToken := HashToken(token)
		slog.Debug("Using Bearer token from request for analytics", "source", "bearer_header")
		return hashedToken
	}

	// Fall back to anonymous identifier
	slog.Debug("Using anonymous user ID for analytics", "source", "anonymous")
	return a.config.UserID
}

// sendBatchEventsForUser sends multiple events to Google Analytics 4 with a specific user ID
func (a *Analytics) sendBatchEventsForUser(
	ctx context.Context,
	userID string,
	events []GAEvent,
) error {
	if len(events) == 0 {
		return nil
	}

	payload := GAPayload{
		ClientID:    userID, // User-specific hashed identifier
		UserID:      userID, // User-specific hashed identifier
		Events:      events, // Multiple events in single request
		TimestampMS: time.Now().UnixMicro(),
	}

	return a.sendPayload(ctx, payload)
}

// sendPayload sends a GA4 payload via HTTP
func (a *Analytics) sendPayload(ctx context.Context, payload GAPayload) error {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal analytics payload: %w", err)
	}

	// Log the outgoing request details with pretty-printed JSON
	slog.Debug("GA4 Batch HTTP Request", "events_count", len(payload.Events))

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
			"events_count", len(payload.Events),
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
	slog.Debug("GA4 Batch HTTP Response", "status", statusInfo, "events_count", len(payload.Events))

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

// incrementMetric atomically increments the counter for a tool and user
func (a *Analytics) incrementMetric(userID, toolName string) {
	if a == nil {
		return
	}

	a.metricsLock.RLock()
	userMetrics, userExists := a.metrics[userID]
	a.metricsLock.RUnlock()

	if !userExists {
		// Create new user metrics map if it doesn't exist
		a.metricsLock.Lock()
		// Double-check pattern to avoid race conditions
		if userMetrics, userExists = a.metrics[userID]; !userExists {
			userMetrics = make(map[string]*int64)
			a.metrics[userID] = userMetrics
		}
		a.metricsLock.Unlock()
	}

	// Now get or create the tool counter for this user
	a.metricsLock.RLock()
	counter, exists := userMetrics[toolName]
	a.metricsLock.RUnlock()

	if !exists {
		// Create new counter if it doesn't exist
		a.metricsLock.Lock()
		// Double-check pattern to avoid race conditions
		if counter, exists = userMetrics[toolName]; !exists {
			counter = new(int64)
			userMetrics[toolName] = counter
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

	// Collect all non-zero metrics per user and reset them
	// Structure: userID -> toolName -> count
	metricsToSend := make(map[string]map[string]int64)

	a.metricsLock.RLock()
	for userID, userMetrics := range a.metrics {
		for toolName, counter := range userMetrics {
			if counter != nil {
				count := atomic.SwapInt64(counter, 0) // Atomically get and reset
				if count > 0 {
					if metricsToSend[userID] == nil {
						metricsToSend[userID] = make(map[string]int64)
					}
					metricsToSend[userID][toolName] = count
				}
			}
		}
	}
	a.metricsLock.RUnlock()

	if len(metricsToSend) == 0 {
		slog.Debug("No metrics to send")
		return
	}

	// Calculate total events across all users
	totalEvents := int64(0)
	totalTools := 0
	for _, userMetrics := range metricsToSend {
		totalTools += len(userMetrics)
		for _, count := range userMetrics {
			totalEvents += count
		}
	}

	slog.Debug("Processing batched metrics",
		"users_count", len(metricsToSend),
		"tools_count", totalTools,
		"total_events", totalEvents,
	)

	// Send metrics as a batch to GA4 per user
	a.sendBatchMetricsPerUser(context.Background(), metricsToSend)
}

// sendBatchMetricsPerUser sends multiple tool metrics per user as batch events to GA4
func (a *Analytics) sendBatchMetricsPerUser(
	ctx context.Context,
	metricsPerUser map[string]map[string]int64,
) {
	if len(metricsPerUser) == 0 {
		slog.Debug("No metrics to send")
		return
	}

	// Process each user's metrics separately
	for userID, metrics := range metricsPerUser {
		a.sendBatchMetrics(ctx, userID, metrics)
	}
}

// sendBatchMetrics sends multiple tool metrics as a batch event to GA4 for a specific user
func (a *Analytics) sendBatchMetrics(ctx context.Context, userID string, metrics map[string]int64) {
	if len(metrics) == 0 {
		slog.Debug("No metrics to send for user")
		return
	}

	// Lazily fetch instance ID on first batch send
	a.ensureInstanceID()

	// Collect all individual events for batch sending
	var events []GAEvent

	// Get instanceID (protected by mutex to ensure memory visibility)
	var currentInstanceID string
	if a.instanceIDFetched.Load() {
		a.instanceIDLock.Lock()
		currentInstanceID = a.instanceID
		a.instanceIDLock.Unlock()
	}

	// Create individual events for each tool usage (matching analytics-client format)
	for toolName, count := range metrics {
		// Create multiple events if count > 1 (each tool usage gets its own event)
		for i := int64(0); i < count; i++ {
			params := map[string]interface{}{
				"custom_user_id": userID,                // The hashed user identifier
				"event_name":     "mcp_event_triggered", // Event name
				"tool":           toolName,              // The name of the tool
			}

			// Add instanceID if available
			if currentInstanceID != "" {
				params["instanceID"] = currentInstanceID
			}

			event := GAEvent{
				Name:   "mcp_event_triggered",
				Params: params,
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
		// Use userID for the batch events
		if err := a.sendBatchEventsForUser(ctx, userID, events[start:end]); err != nil {
			failed += end - start
			slog.Error(
				"Failed to send batch tool events",
				"error",
				err,
				"events_count",
				end-start,
				"user_id_prefix",
				truncateForLog(userID, 16),
			)
			continue
		}
		sent += end - start
	}

	if sent > 0 {
		slog.Debug(
			"Sent batch metrics for user",
			"user_id_prefix",
			truncateForLog(userID, 16),
			"sent",
			sent,
			"failed",
			failed,
		)
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
