package integration

import (
	"github.com/reportportal/reportportal-mcp-server/internal/testdata"
)

type (
	PostmanCollection      = testdata.PostmanCollection
	PostmanInfo            = testdata.PostmanInfo
	PostmanItem            = testdata.PostmanItem
	PostmanRequest         = testdata.PostmanRequest
	PostmanHeader          = testdata.PostmanHeader
	PostmanRequestBody     = testdata.PostmanRequestBody
	PostmanKeyValue        = testdata.PostmanKeyValue
	PostmanURL             = testdata.PostmanURL
	PostmanQueryParam      = testdata.PostmanQueryParam
	PostmanVariable        = testdata.PostmanVariable
	PostmanResponse        = testdata.PostmanResponse
	TestCase               = testdata.TestCase
	ReportPortalMockConfig = testdata.ReportPortalMockConfig
	RequestResponsePair    = testdata.RequestResponsePair
	LLMClientMockConfig    = testdata.LLMClientMockConfig
)

// ParsePostmanCollection parses a Postman collection JSON
func ParsePostmanCollection(data []byte) (*PostmanCollection, error) {
	return testdata.ParsePostmanCollection(data)
}

// ParseTestCase parses a test case JSON
func ParseTestCase(data []byte) (*TestCase, error) {
	return testdata.ParseTestCase(data)
}
