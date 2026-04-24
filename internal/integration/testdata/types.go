// Package testdata provides shared types for test fixtures and Postman collections.
// These types are used by both integration tests and the testdata verification tool.
package testdata

import (
	"encoding/json"
	"fmt"
)

// Valid body modes for PostmanRequestBody
const (
	BodyModeRaw        = "raw"
	BodyModeURLEncoded = "urlencoded"
	BodyModeFormData   = "formdata"
	BodyModeGraphQL    = "graphql"
)

// PostmanRequest represents an HTTP request
type PostmanRequest struct {
	Method      string              `json:"method"`
	Header      []PostmanHeader     `json:"header,omitempty"`
	Body        *PostmanRequestBody `json:"body,omitempty"`
	URL         PostmanURL          `json:"url"`
	Description string              `json:"description,omitempty"`
	Variable    []PostmanVariable   `json:"variable,omitempty"`
}

// PostmanHeader represents an HTTP header
type PostmanHeader struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// PostmanRequestBody represents request body
type PostmanRequestBody struct {
	Mode                    string            `json:"mode,omitempty"` // Use BodyMode* constants
	Raw                     string            `json:"raw,omitempty"`
	URLEncoded              []PostmanKeyValue `json:"urlencoded,omitempty"`
	FormData                []PostmanKeyValue `json:"formdata,omitempty"`
	GraphQL                 interface{}       `json:"graphql,omitempty"`
	Options                 interface{}       `json:"options,omitempty"`
	DisablePrerequestEditor bool              `json:"disablePrerequestEditor,omitempty"`
}

// IsValidMode checks if the Mode field contains a valid value
func (b *PostmanRequestBody) IsValidMode() bool {
	if b == nil || b.Mode == "" {
		return true // Empty mode is valid (omitempty)
	}
	switch b.Mode {
	case BodyModeRaw, BodyModeURLEncoded, BodyModeFormData, BodyModeGraphQL:
		return true
	default:
		return false
	}
}

// ValidateMode returns an error if the Mode field is invalid
func (b *PostmanRequestBody) ValidateMode() error {
	if !b.IsValidMode() {
		return fmt.Errorf("invalid body mode %q: expected one of [%s, %s, %s, %s]",
			b.Mode, BodyModeRaw, BodyModeURLEncoded, BodyModeFormData, BodyModeGraphQL)
	}
	return nil
}

// PostmanKeyValue represents a key-value pair
type PostmanKeyValue struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	Type        string `json:"type,omitempty"`
}

// PostmanURL represents a URL
type PostmanURL struct {
	Raw      string              `json:"raw,omitempty"`
	Protocol string              `json:"protocol,omitempty"`
	Host     []string            `json:"host,omitempty"`
	Path     []string            `json:"path,omitempty"`
	Query    []PostmanQueryParam `json:"query,omitempty"`
	Variable []PostmanVariable   `json:"variable,omitempty"`
}

// PostmanQueryParam represents a query parameter
type PostmanQueryParam struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// PostmanVariable represents a variable
type PostmanVariable struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// PostmanResponse represents an expected response
type PostmanResponse struct {
	Name            string          `json:"name"`
	OriginalRequest PostmanRequest  `json:"originalRequest,omitempty"`
	Status          string          `json:"status,omitempty"`
	Code            int             `json:"code"`
	Header          []PostmanHeader `json:"header,omitempty"`
	Body            string          `json:"body,omitempty"`
	ResponseTime    int             `json:"responseTime,omitempty"`
	ResponseSize    int             `json:"responseSize,omitempty"`
}

// TestCase represents a single integration test case
type TestCase struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	EndpointPath string `json:"endpointPath,omitempty"` // ReportPortal API endpoint path pattern (e.g., "/v1/{projectKey}/item/{itemId}")

	// ReportPortal Mock configuration
	ReportPortalMock ReportPortalMockConfig `json:"reportPortalMock"`

	// LLM Client Mock configuration
	LLMClientMock LLMClientMockConfig `json:"llmClientMock"`
}

// ReportPortalMockConfig defines request/response pairs for ReportPortal mock
type ReportPortalMockConfig struct {
	RequestResponsePairs []RequestResponsePair `json:"requestResponsePairs"`
}

// RequestResponsePair defines a single request/response pair
type RequestResponsePair struct {
	Request  PostmanRequest  `json:"request"`
	Response PostmanResponse `json:"response"`
}

// LLMClientMockConfig defines the LLM client request and expected response
type LLMClientMockConfig struct {
	Request          PostmanRequest  `json:"request"`
	ExpectedResponse PostmanResponse `json:"expectedResponse"`
}

// ParseTestCase parses a test case JSON
func ParseTestCase(data []byte) (*TestCase, error) {
	var testCase TestCase
	if err := json.Unmarshal(data, &testCase); err != nil {
		return nil, fmt.Errorf("failed to parse test case: %w", err)
	}

	// Validate request body mode
	if err := validateRequestBody(&testCase.LLMClientMock.Request); err != nil {
		return nil, fmt.Errorf("invalid LLM client request: %w", err)
	}

	// Validate LLM client expected response original request
	if err := validateRequestBody(&testCase.LLMClientMock.ExpectedResponse.OriginalRequest); err != nil {
		return nil, fmt.Errorf("invalid LLM client expected response original request: %w", err)
	}

	for i, pair := range testCase.ReportPortalMock.RequestResponsePairs {
		if err := validateRequestBody(&pair.Request); err != nil {
			return nil, fmt.Errorf("invalid ReportPortal mock request at index %d: %w", i, err)
		}
		// Validate response original request
		if err := validateRequestBody(&pair.Response.OriginalRequest); err != nil {
			return nil, fmt.Errorf("invalid ReportPortal mock response original request at index %d: %w", i, err)
		}
	}

	return &testCase, nil
}

// validateRequestBody validates the body mode of a request
func validateRequestBody(req *PostmanRequest) error {
	if req.Body != nil {
		if err := req.Body.ValidateMode(); err != nil {
			return err
		}
	}
	return nil
}
