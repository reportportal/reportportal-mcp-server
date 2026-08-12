package utils

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/reportportal/goRP/v5/pkg/gorp"
	"github.com/reportportal/goRP/v5/pkg/openapi"
)

// defaultAttachmentUploadTimeout is used for the TMS attachment upload HTTP
// request when the underlying API client has no HTTPClient configured.
const defaultAttachmentUploadTimeout = 30 * time.Second

// tmsAttachmentMaxDecodedBytes is the upper bound on the decoded byte size of
// an inline attachment passed via the content field. The MCP protocol delivers
// the value as a JSON string so the full content is already resident in memory;
// this cap prevents an abnormally large value from being processed further.
// Matches the default limit used by the Import Launch from File tool.
const tmsAttachmentMaxDecodedBytes int64 = 50 * 1024 * 1024 // 50 MiB (52 428 800 bytes)

// ProjectKeyField is the MCP parameter name for the ReportPortal project identifier.
// Struct JSON tags (e.g. `json:"projectKey"`) must remain string literals and cannot
// reference this constant.
const ProjectKeyField = "projectKey"

// requirementIDCharset is the alphabet used for generated requirement IDs.
const requirementIDCharset = "abcdefghijklmnopqrstuvwxyz0123456789"

// requirementIDLength is the number of random characters following the leading underscore.
const requirementIDLength = 9

// DefaultLimitOffset is the default page size used by ApplyLimitOffset when the
// caller does not specify a limit but requests default-pagination behaviour.
const DefaultLimitOffset uint = 50

// ProjectKeySchema returns a JSON schema for the projectKey MCP tool parameter.
// Default is set only when defaultProjectKey is non-empty (JSON default is omitted otherwise).
// Returns an error if marshalling the default value fails (in practice unreachable for plain strings).
func ProjectKeySchema(defaultProjectKey string) (*jsonschema.Schema, error) {
	s := &jsonschema.Schema{
		Type:        "string",
		Description: "A unique project identifier within the ReportPortal instance.",
	}
	if defaultProjectKey != "" {
		b, err := json.Marshal(defaultProjectKey)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal default project key: %w", err)
		}
		s.Default = b
	}
	return s, nil
}

// ApplyPaginationOptions applies pagination to an API request from typed values.
// Zero values for page and pageSize fall back to defaults.
func ApplyPaginationOptions[T PaginatedRequest[T]](
	apiRequest T,
	page, pageSize uint,
	pageSort, defaultSort string,
) T {
	if page < FirstPage {
		page = FirstPage
	} else if page > math.MaxInt32 {
		page = math.MaxInt32
	}

	if pageSize <= 0 {
		pageSize = DefaultPageSize
	} else if pageSize > math.MaxInt32 {
		pageSize = math.MaxInt32
	}

	if pageSort == "" {
		pageSort = defaultSort
	}

	return apiRequest.
		PagePage(int32(page)).     //nolint:gosec
		PageSize(int32(pageSize)). //nolint:gosec
		PageSort(pageSort)
}

// LimitSchema returns the JSON schema for the "limit" pagination parameter.
// When defaultLimit is greater than zero, the description mentions the default value.
func LimitSchema(defaultLimit uint) *jsonschema.Schema {
	desc := "Maximum number of results to return"
	if defaultLimit > 0 {
		desc = fmt.Sprintf("%s (default %d)", desc, defaultLimit)
	}
	return &jsonschema.Schema{
		Type:        "integer",
		Description: desc,
		Minimum:     openapi.PtrFloat64(1),
	}
}

// OffsetSchema returns the JSON schema for the "offset" pagination parameter.
func OffsetSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "integer",
		Description: "Number of results to skip for pagination (default 0)",
		Minimum:     openapi.PtrFloat64(0),
	}
}

// ApplyLimitOffset writes "limit" and "offset" query parameters into q.
//
// Limit: when limit is zero and defaultLimit > 0, defaultLimit is used as the
// effective value. When both are zero, the "limit" parameter is omitted.
//
// Offset: when defaultLimit > 0, "offset" is always written (even as "0") so the
// server receives an explicit starting position. When defaultLimit == 0, "offset"
// is omitted unless its value is greater than zero.
func ApplyLimitOffset(q url.Values, limit, offset, defaultLimit uint) {
	delete(q, "limit")
	delete(q, "offset")
	if limit == 0 {
		limit = defaultLimit
	}
	if limit > 0 {
		q.Set("limit", strconv.FormatUint(uint64(limit), 10))
	}
	if offset > 0 || defaultLimit > 0 {
		q.Set("offset", strconv.FormatUint(uint64(offset), 10))
	}
}

// ExtractProject resolves the active project key using the agreed priority order:
//
//   - stdio mode: env variable (context, top priority) → tool input (fallback)
//   - HTTP mode:  env variable is ignored; HTTP header projectKey (context, top
//     priority) → tool input (fallback)
//
// In both modes the context-carried value wins; tool input is only used when
// no project has been placed in the context.
func ExtractProject(ctx context.Context, projectArg string) (string, error) {
	if project, ok := GetProjectFromContext(ctx); ok {
		return project, nil
	}
	if project := strings.TrimSpace(projectArg); project != "" {
		return project, nil
	}
	return "", fmt.Errorf(
		"no project parameter found in request, HTTP header, or environment variable",
	)
}

// EventTracker interface for analytics tracking
type EventTracker interface {
	TrackMCPEvent(ctx context.Context, toolName string)
}

// WithAnalytics is a generic version of WithAnalytics for typed input structs.
func WithAnalytics[In any](
	tracker EventTracker,
	toolName string,
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error),
) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, any, error) {
		// Track the event before executing the tool (synchronous since it's just incrementing a counter)
		if tracker != nil {
			tracker.TrackMCPEvent(ctx, toolName)
		}

		// Execute the original handler
		return handler(ctx, req, args)
	}
}

// GenerateRequirementID produces a unique requirement identifier such as "_h5cbt84tg".
func GenerateRequirementID() string {
	b := make([]byte, requirementIDLength)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a time-based seed so the
		// id stays unique-ish rather than returning an empty/duplicate value.
		seed := time.Now().UnixNano()
		for i := range b {
			b[i] = byte((seed >> (uint(i) * 8)) & 0xff)
		}
	}
	for i := range b {
		b[i] = requirementIDCharset[int(b[i])%len(requirementIDCharset)]
	}
	return "_" + string(b)
}

// RequirementsSchema builds the JSON schema for the optional "requirements" array field.
// When isUpdate is true the description includes the clear/omit semantics for update operations.
func RequirementsSchema(isUpdate bool) *jsonschema.Schema {
	desc := "Optional list of requirement values linked to the test case. A unique id is generated automatically for each entry."
	if isUpdate {
		desc += " Pass an empty array ([]) to clear all existing requirements; omit the field to leave them unchanged."
	}
	return &jsonschema.Schema{
		Type:        "array",
		Description: desc,
		Items: &jsonschema.Schema{
			Type:        "string",
			Description: "Requirement value/description (must contain at least one non-whitespace character)",
			MinLength:   openapi.PtrInt(1),
			Pattern:     `\S`,
		},
	}
}

// ToRequirementsRQ converts requirement values into the openapi request model,
// generating a unique id for each entry.
func ToRequirementsRQ(
	values []string,
) []openapi.ComEpamReportportalBaseCoreTmsDtoTmsRequirementRQ {
	result := make([]openapi.ComEpamReportportalBaseCoreTmsDtoTmsRequirementRQ, 0, len(values))
	for _, v := range values {
		item := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsRequirementRQ()
		item.SetId(GenerateRequirementID())
		item.SetValue(v)
		result = append(result, *item)
	}
	return result
}

// Manual scenario type input values exposed to MCP clients. These map onto the
// API discriminator manualScenarioType: "text" -> TEXT, "steps" -> STEPS.
const (
	TestCaseTypeDescription = "text"
	TestCaseTypeWithSteps   = "steps"
)

// StepArg represents a single manual scenario step from tool input.
type StepArg struct {
	Instructions   string  `json:"instructions"`
	ExpectedResult *string `json:"expected-result,omitempty"`
}

// AttributeArg represents a single test case attribute (tag) from tool input.
// Only the key is required; value is not used for the TMS test case tag flow.
type AttributeArg struct {
	Key string `json:"key"`
}

// attributesItemSchema returns the shared object schema for a single attribute entry.
func attributesItemSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"key": {
				Type:        "string",
				Description: "Attribute key (tag name; must contain at least one non-whitespace character)",
				MinLength:   openapi.PtrInt(1),
				Pattern:     `\S`,
			},
		},
		Required:             []string{"key"},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

// AttributesSchema returns the JSON schema for the "attributes" field on test case tools.
// When isUpdate is true the description includes the clear/omit semantics for update operations.
func AttributesSchema(isUpdate bool) *jsonschema.Schema {
	desc := "Optional list of attributes (tags) to attach to the test case. Existing project attributes that match key are reused; missing ones are created automatically before being linked to the test case."
	if isUpdate {
		desc += " Pass an empty array ([]) to clear all existing attributes; omit the field to leave them unchanged."
	}
	return &jsonschema.Schema{
		Type:        "array",
		Description: desc,
		Items:       attributesItemSchema(),
	}
}

// ManualScenarioArgs carries the manual scenario inputs shared by the
// create_test_case and update_test_case tools. Requirements is a pointer so a
// nil value (field omitted) can be distinguished from an explicit empty slice
// (field provided as []), which clears the existing requirements.
// IsUpdate changes the steps validation rule: on a create, Steps must be
// non-nil and non-empty; on an update, Steps may be nil (leave existing steps
// unchanged), but if provided must still be non-empty.
type ManualScenarioArgs struct {
	TestCaseType   *string
	Instructions   *string
	ExpectedResult *string
	Preconditions  *string
	Requirements   *[]string
	Steps          *[]StepArg
	IsUpdate       bool
}

// TestCaseTypeSchema builds the JSON schema for the optional "test-case-type" field.
// When isUpdate is true the description reflects update semantics (no default, omit to leave unchanged);
// otherwise it reflects create semantics (omitted type defaults to "text").
func TestCaseTypeSchema(isUpdate bool) *jsonschema.Schema {
	var desc string
	if isUpdate {
		desc = `Manual scenario type. "text" stores a plain text scenario via instructions/expected-result without test steps; "steps" stores an ordered list of steps. Required when changing any manual scenario field (instructions, expected-result, preconditions, requirements, steps); omit it to leave the entire scenario unchanged. When set to "steps", steps are optional: omit them to leave existing steps unchanged and only update other scenario fields (e.g. requirements).`
	} else {
		desc = `Manual scenario type. "text" stores a plain text scenario via instructions/expected-result without test steps; "steps" stores an ordered list of steps. Defaults to "text".`
	}
	return &jsonschema.Schema{
		Type:        "string",
		Description: desc,
		Enum:        []any{TestCaseTypeDescription, TestCaseTypeWithSteps},
	}
}

// StepsSchema builds the JSON schema for the optional "steps" array field.
func StepsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Description: `Ordered list of manual test steps. Required on create when test-case-type is "steps"; must contain at least one step. On update, omit to leave existing steps unchanged; if provided, must contain at least one step. Must be omitted for "text".`,
		Items: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"instructions": {
					Type:        "string",
					Description: "Step instructions / action to perform (must contain at least one non-whitespace character)",
					MinLength:   openapi.PtrInt(1),
					Pattern:     `\S`,
				},
				"expected-result": {
					Type:        "string",
					Description: "Optional expected result of the step",
				},
			},
			Required:             []string{"instructions"},
			AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		},
	}
}

// ToStepsRQ converts step arguments into the openapi request model.
func ToStepsRQ(steps []StepArg) []openapi.ComEpamReportportalBaseCoreTmsDtoTmsStepRQ {
	result := make([]openapi.ComEpamReportportalBaseCoreTmsDtoTmsStepRQ, 0, len(steps))
	for _, s := range steps {
		item := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsStepRQ()
		item.SetInstructions(s.Instructions)
		if s.ExpectedResult != nil {
			item.SetExpectedResult(*s.ExpectedResult)
		}
		result = append(result, *item)
	}
	return result
}

func newPreconditionsRQ(
	value string,
) openapi.ComEpamReportportalBaseCoreTmsDtoTmsManualScenarioPreconditionsRQ {
	pre := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsManualScenarioPreconditionsRQ()
	pre.SetValue(value)
	return *pre
}

// BuildManualScenario constructs a test case manual scenario request from tool
// input. The test case type selects between a TEXT ("text") scenario and a
// STEPS ("steps") scenario; an empty/nil type defaults to TEXT.
func BuildManualScenario(
	a ManualScenarioArgs,
) (openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario, error) {
	var zero openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario

	tcType := TestCaseTypeDescription
	if a.TestCaseType != nil && strings.TrimSpace(*a.TestCaseType) != "" {
		tcType = strings.TrimSpace(*a.TestCaseType)
	}

	if a.Requirements != nil {
		for i, r := range *a.Requirements {
			if strings.TrimSpace(r) == "" {
				return zero, fmt.Errorf("requirements[%d] value must be non-empty", i)
			}
		}
	}

	switch tcType {
	case TestCaseTypeDescription:
		if a.Steps != nil {
			return zero, fmt.Errorf(
				`steps are only valid when test-case-type is "steps"`,
			)
		}
		text := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQ("TEXT")
		if a.Instructions != nil {
			text.SetInstructions(*a.Instructions)
		}
		if a.ExpectedResult != nil {
			text.SetExpectedResult(*a.ExpectedResult)
		}
		if a.Preconditions != nil {
			text.SetPreconditions(newPreconditionsRQ(*a.Preconditions))
		}
		if a.Requirements != nil {
			text.SetRequirements(ToRequirementsRQ(*a.Requirements))
		}
		return openapi.ComEpamReportportalBaseCoreTmsDtoTmsTextManualScenarioRQAsComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario(
			text,
		), nil

	case TestCaseTypeWithSteps:
		if a.Instructions != nil || a.ExpectedResult != nil {
			return zero, fmt.Errorf(
				`instructions and expected-result are not valid for "steps"; provide them inside each step`,
			)
		}
		if a.Steps == nil && !a.IsUpdate {
			return zero, fmt.Errorf(
				`steps must not be empty when test-case-type is "steps"`,
			)
		}
		if a.Steps != nil && len(*a.Steps) == 0 {
			return zero, fmt.Errorf(
				`steps must not be empty when test-case-type is "steps"`,
			)
		}
		if a.Steps != nil {
			for i, s := range *a.Steps {
				if strings.TrimSpace(s.Instructions) == "" {
					return zero, fmt.Errorf("steps[%d] instructions must be non-empty", i)
				}
			}
		}
		steps := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsStepsManualScenarioRQ("STEPS")
		if a.Steps != nil {
			steps.SetSteps(ToStepsRQ(*a.Steps))
		}
		if a.Preconditions != nil {
			steps.SetPreconditions(newPreconditionsRQ(*a.Preconditions))
		}
		if a.Requirements != nil {
			steps.SetRequirements(ToRequirementsRQ(*a.Requirements))
		}
		return openapi.ComEpamReportportalBaseCoreTmsDtoTmsStepsManualScenarioRQAsComEpamReportportalBaseCoreTmsDtoTmsTestCaseRQManualScenario(
			steps,
		), nil

	default:
		return zero, fmt.Errorf(
			"invalid test-case-type %q: must be %q or %q",
			tcType, TestCaseTypeDescription, TestCaseTypeWithSteps,
		)
	}
}

// ExecutionAttachmentArg describes an attachment to link to a test case execution.
// Callers must provide exactly one of:
//   - ID (plus FileName/FileType/FileSize) to reference an attachment that was
//     already uploaded via the TMS attachment upload endpoint, or
//   - Content (base64-encoded file bytes) plus FileName to have the tool upload
//     the attachment automatically before linking it to the execution.
type ExecutionAttachmentArg struct {
	ID       int64  `json:"id,omitempty"`
	FileName string `json:"fileName,omitempty"`
	FileType string `json:"fileType,omitempty"`
	FileSize int64  `json:"fileSize,omitempty"`
	Content  string `json:"content,omitempty"`
}

// ExecutionCommentAttachmentRQ is the attachment representation sent to the
// update-execution endpoint: an id referencing an already-uploaded attachment
// plus its metadata. It never carries raw file content.
type ExecutionCommentAttachmentRQ struct {
	ID       int64  `json:"id"`
	FileName string `json:"fileName"`
	FileType string `json:"fileType"`
	FileSize int64  `json:"fileSize"`
}

// TMSAttachmentUploadRS is the response returned by the TMS attachment upload
// endpoint (POST /project/{projectKey}/tms/attachment/upload).
type TMSAttachmentUploadRS struct {
	ID       int64  `json:"id"`
	FileName string `json:"fileName"`
	FileType string `json:"fileType"`
	FileSize int64  `json:"fileSize"`
}

// UploadTMSAttachment uploads raw file content as a TMS attachment via
// POST /project/{projectKey}/tms/attachment/upload (multipart/form-data, field
// name "file") and returns the metadata of the stored attachment, including
// its ID, to be referenced later when linking it to a test case execution.
func UploadTMSAttachment(
	ctx context.Context,
	client *gorp.Client,
	project string,
	fileName string,
	fileType string,
	content []byte,
) (*TMSAttachmentUploadRS, error) {
	if int64(len(content)) > tmsAttachmentMaxDecodedBytes {
		return nil, fmt.Errorf(
			"attachment %q exceeds the %d-byte decoded size limit (%d MiB)",
			fileName, tmsAttachmentMaxDecodedBytes, tmsAttachmentMaxDecodedBytes/(1024*1024),
		)
	}

	mimeType := strings.TrimSpace(fileType)
	if mimeType == "" {
		if guessed := mime.TypeByExtension(path.Ext(fileName)); guessed != "" {
			mimeType = guessed
		} else {
			mimeType = http.DetectContentType(content)
		}
	} else {
		// Parse then re-format to strip CR/LF and prevent multipart header injection.
		mediaType, params, parseErr := mime.ParseMediaType(mimeType)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid fileType %q: %w", fileType, parseErr)
		}
		if mimeType = mime.FormatMediaType(mediaType, params); mimeType == "" {
			return nil, fmt.Errorf("invalid fileType %q: could not format media type", fileType)
		}
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// quoteEscaper handles \, ", \r, \n per multipart spec
	escapedFilename := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\r", "",
		"\n", "",
		"\x00", "",
	).Replace(path.Base(fileName))
	fh := make(textproto.MIMEHeader)
	fh.Set(
		"Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapedFilename),
	)
	fh.Set("Content-Type", mimeType)
	part, err := mw.CreatePart(fh)
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart field: %w", err)
	}
	if _, err = part.Write(content); err != nil {
		return nil, fmt.Errorf("failed to write attachment content: %w", err)
	}
	if err = mw.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalise multipart body: %w", err)
	}

	cfg := client.GetConfig()
	uploadURL := fmt.Sprintf(
		"%s://%s/api/v1/project/%s/tms/attachment/upload",
		cfg.Scheme, cfg.Host,
		url.PathEscape(project),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return nil, fmt.Errorf("failed to build attachment upload request: %w", err)
	}
	for k, v := range cfg.DefaultHeader {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("Accept", "application/json")
	if cfg.Middleware != nil {
		cfg.Middleware(httpReq)
	}

	srcClient := cfg.HTTPClient
	if srcClient == nil {
		srcClient = &http.Client{}
	}
	copyClient := *srcClient
	if copyClient.Timeout == 0 {
		copyClient.Timeout = defaultAttachmentUploadTimeout
	}
	httpClient := &copyClient

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("attachment upload request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read attachment upload response: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewBuffer(respBody))
	if cfg.ResponseMiddleware != nil {
		if mwErr := cfg.ResponseMiddleware(resp, respBody); mwErr != nil {
			return nil, fmt.Errorf("attachment upload response middleware error: %w", mwErr)
		}
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"attachment upload failed (HTTP %d): %s",
			resp.StatusCode,
			string(respBody),
		)
	}

	var uploaded TMSAttachmentUploadRS
	if err := json.Unmarshal(respBody, &uploaded); err != nil {
		return nil, fmt.Errorf("failed to parse attachment upload response: %w", err)
	}
	if uploaded.ID == 0 {
		return nil, fmt.Errorf(
			"attachment upload response did not contain an attachment id: %s",
			string(respBody),
		)
	}
	if uploaded.FileName == "" {
		uploaded.FileName = fileName
	}
	if uploaded.FileType == "" {
		uploaded.FileType = mimeType
	}
	if uploaded.FileSize == 0 {
		uploaded.FileSize = int64(len(content))
	}
	return &uploaded, nil
}

// ResolveExecutionCommentAttachments turns the tool's attachment arguments into the
// attachment references expected by the update-execution endpoint. Attachments
// carrying Content are uploaded on the fly via UploadTMSAttachment; attachments
// carrying only an ID are assumed to have been uploaded already and are passed
// through as-is.
func ResolveExecutionCommentAttachments(
	ctx context.Context,
	client *gorp.Client,
	project string,
	attachments []ExecutionAttachmentArg,
) ([]ExecutionCommentAttachmentRQ, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	resolved := make([]ExecutionCommentAttachmentRQ, 0, len(attachments))

	// decodedContents holds pre-validated, pre-decoded bytes for every content
	// attachment so the second pass only needs to perform network calls.
	decodedContents := make(map[int][]byte, len(attachments))
	var totalDecodedBytes int64

	// First pass: validate every attachment's filename, id/content exclusivity,
	// required existing-attachment metadata, and base64 content before uploading
	// anything, so a later validation failure cannot leave earlier uploads
	// orphaned server-side.
	for i, att := range attachments {
		if strings.TrimSpace(att.FileName) == "" {
			return nil, fmt.Errorf("executionComment.attachments[%d].fileName is required", i)
		}

		hasID := att.ID > 0
		hasContent := att.Content != ""

		switch {
		case hasID && hasContent:
			return nil, fmt.Errorf(
				"executionComment.attachments[%d]: provide either id (existing attachment) "+
					"or content (new attachment to upload), not both",
				i,
			)
		case hasID:
			if strings.TrimSpace(att.FileType) == "" || att.FileSize <= 0 {
				return nil, fmt.Errorf(
					"executionComment.attachments[%d]: fileType and fileSize are required "+
						"when referencing an existing attachment by id",
					i,
				)
			}
		case hasContent:
			dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(att.Content))
			decoded, decodeErr := io.ReadAll(io.LimitReader(dec, tmsAttachmentMaxDecodedBytes+1))
			if decodeErr != nil {
				return nil, fmt.Errorf(
					"executionComment.attachments[%d]: invalid base64 content: %w",
					i, decodeErr,
				)
			}
			if int64(len(decoded)) > tmsAttachmentMaxDecodedBytes {
				return nil, fmt.Errorf(
					"executionComment.attachments[%d]: decoded size %d bytes exceeds limit %d bytes",
					i,
					len(decoded),
					tmsAttachmentMaxDecodedBytes,
				)
			}
			totalDecodedBytes += int64(len(decoded))
			if totalDecodedBytes > tmsAttachmentMaxDecodedBytes {
				return nil, fmt.Errorf(
					"executionComment.attachments: total decoded size %d bytes exceeds limit %d bytes",
					totalDecodedBytes,
					tmsAttachmentMaxDecodedBytes,
				)
			}
			decodedContents[i] = decoded
		default:
			return nil, fmt.Errorf(
				"executionComment.attachments[%d]: either id (existing attachment) or "+
					"content (new attachment to upload) must be provided",
				i,
			)
		}
	}

	// Second pass: upload valid content attachments and build resolved results
	// in the original order.
	uploadedAttachmentIDs := make([]string, 0, len(decodedContents))
	for i, att := range attachments {
		if decoded, ok := decodedContents[i]; ok {
			uploaded, uploadErr := UploadTMSAttachment(
				ctx,
				client,
				project,
				att.FileName,
				att.FileType,
				decoded,
			)
			if uploadErr != nil {
				if len(uploadedAttachmentIDs) > 0 {
					return nil, fmt.Errorf(
						"executionComment.attachments[%d]: %w; successfully uploaded attachment IDs: %s",
						i,
						uploadErr,
						strings.Join(uploadedAttachmentIDs, ", "),
					)
				}
				return nil, fmt.Errorf("executionComment.attachments[%d]: %w", i, uploadErr)
			}
			uploadedAttachmentIDs = append(
				uploadedAttachmentIDs,
				strconv.FormatInt(uploaded.ID, 10),
			)
			resolved = append(resolved, ExecutionCommentAttachmentRQ{
				ID:       uploaded.ID,
				FileName: uploaded.FileName,
				FileType: uploaded.FileType,
				FileSize: uploaded.FileSize,
			})
		} else {
			resolved = append(resolved, ExecutionCommentAttachmentRQ{
				ID:       att.ID,
				FileName: att.FileName,
				FileType: att.FileType,
				FileSize: att.FileSize,
			})
		}
	}
	return resolved, nil
}

// ResolveTestCaseAttributes ensures that every requested attribute (tag, identified
// by key only) exists for the project and returns the request models that link them
// to a test case. For each attribute it first looks the attribute up via
// GET /v1/project/{projectKey}/tms/attribute (filtered by key); if no match exists
// it creates the attribute via POST to the same endpoint. The resulting list
// references each attribute by its id and key so it can be attached during test
// case creation or update.
func ResolveTestCaseAttributes(
	ctx context.Context,
	client *gorp.Client,
	project string,
	attributes []AttributeArg,
) ([]openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseAttributeRQ, error) {
	// Pre-validate all keys before making any HTTP calls.
	seen := make(map[string]struct{}, len(attributes))
	for i, attr := range attributes {
		key := strings.TrimSpace(attr.Key)
		if key == "" {
			return nil, fmt.Errorf("attributes[%d] key must not be empty or whitespace", i)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("attributes[%d] duplicate key %q", i, key)
		}
		seen[key] = struct{}{}
	}

	result := make(
		[]openapi.ComEpamReportportalBaseCoreTmsDtoTmsTestCaseAttributeRQ,
		0,
		len(attributes),
	)
	for _, attr := range attributes {
		key := strings.TrimSpace(attr.Key)

		// 1. Look up an existing attribute matching the key.
		page, response, err := client.TMSAttributeControllerAPI.GetAllAttributes(ctx, project).
			FilterEqKey(key).
			Execute()
		if err != nil {
			return nil, fmt.Errorf(
				"failed to look up attribute %q: %s: %w",
				key, ExtractResponseError(err, response), err,
			)
		}

		var attributeID int64
		found := false
		for _, existing := range page.GetContent() {
			if existing.GetKey() == key {
				attributeID = existing.GetId()
				found = true
				break
			}
		}

		// 2. Create the attribute when it does not exist yet.
		if !found {
			createRQ := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsAttributeRQ()
			createRQ.SetKey(key)
			created, createResp, createErr := client.TMSAttributeControllerAPI.
				CreateAttribute(ctx, project).
				ComEpamReportportalBaseCoreTmsDtoTmsAttributeRQ(*createRQ).
				Execute()
			if createErr != nil {
				// A 409 Conflict means a concurrent caller raced through the
				// same GET→POST window and created this attribute first. Retry
				// the lookup to obtain the id it just created instead of
				// surfacing a spurious duplicate error.
				if createResp != nil && createResp.StatusCode == http.StatusConflict {
					retryPage, _, retryErr := client.TMSAttributeControllerAPI.
						GetAllAttributes(ctx, project).
						FilterEqKey(key).
						Execute()
					if retryErr == nil {
						for _, existing := range retryPage.GetContent() {
							if existing.GetKey() == key {
								attributeID = existing.GetId()
								found = true
								break
							}
						}
					}
				}
				if !found {
					return nil, fmt.Errorf(
						"failed to create attribute %q: %s: %w",
						key, ExtractResponseError(createErr, createResp), createErr,
					)
				}
			} else {
				attributeID = created.GetId()
			}
		}

		// 3. Link the (existing or newly created) attribute to the test case.
		tcAttr := openapi.NewComEpamReportportalBaseCoreTmsDtoTmsTestCaseAttributeRQ()
		tcAttr.SetId(attributeID)
		tcAttr.SetKey(key)
		result = append(result, *tcAttr)
	}
	return result, nil
}

// TestPlanAndCaseIDsProperties returns the shared "test-plan-id" / "test-case-ids"
// JSON schema properties used by tools that add or remove test cases from a test plan.
func TestPlanAndCaseIDsProperties(
	testPlanIDDesc, testCaseIDsDesc string,
) map[string]*jsonschema.Schema {
	return map[string]*jsonschema.Schema{
		"test-plan-id": {
			Type:        "integer",
			Description: testPlanIDDesc,
			Minimum:     openapi.PtrFloat64(1),
		},
		"test-case-ids": {
			Type:        "array",
			Description: testCaseIDsDesc,
			MinItems:    openapi.PtrInt(1),
			Items: &jsonschema.Schema{
				Type:    "integer",
				Minimum: openapi.PtrFloat64(1),
			},
		},
	}
}

// ValidatePlanAndTestCaseIDs validates the "test-plan-id" / "test-case-ids" arguments
// shared by tools that add or remove test cases from a test plan.
func ValidatePlanAndTestCaseIDs(testPlanID int64, testCaseIDs []int64) error {
	if testPlanID <= 0 {
		return fmt.Errorf("test-plan-id must be a positive integer")
	}
	if len(testCaseIDs) == 0 {
		return fmt.Errorf("test-case-ids must not be empty")
	}
	for _, id := range testCaseIDs {
		if id <= 0 {
			return fmt.Errorf(
				"each test case ID must be a positive integer, got %d",
				id,
			)
		}
	}
	return nil
}

// ToolHandler is a function type for MCP tool handlers with typed input and output.
type ToolHandler[In, Out any] func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error)

// RegisterTool is a helper to register a tool that returns both tool definition and handler.
func RegisterTool[In, Out any](s *mcp.Server, getTool func() (*mcp.Tool, ToolHandler[In, Out])) {
	tool, handler := getTool()
	mcp.AddTool(s, tool, mcp.ToolHandlerFor[In, Out](handler))
}

// RegisterResourceTemplate is a helper to register a resource template with its handler.
func RegisterResourceTemplate(
	s *mcp.Server,
	getResourceTemplate func() (*mcp.ResourceTemplate, mcp.ResourceHandler),
) {
	template, handler := getResourceTemplate()
	s.AddResourceTemplate(template, handler)
}

// MustMarshalJSON marshals a value to JSON or panics on error.
//
// This function intentionally panics on marshal failure because it is only used with
// known-safe, compile-time literals and simple slices (e.g., during tool registration/init)
// where json.Marshal cannot fail. Examples include string literals, boolean values, and
// simple string slices used as schema defaults.
//
// WARNING: Do NOT use this function with user-supplied data or runtime values that could
// cause json.Marshal to fail, as this will result in unintended panics. For such cases,
// handle json.Marshal errors explicitly instead.
func MustMarshalJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return b
}

// ParseAcceptFileMimeTypes normalizes a plugin's details.acceptFileMimeTypes value
// (decoded from JSON as either []interface{} or []string) into a []string,
// dropping empty entries. Any other shape (including nil) yields nil.
func ParseAcceptFileMimeTypes(v any) []string {
	switch x := v.(type) {
	case []interface{}:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// NormalizeMediaType lower-cases a media type and strips any parameters
// (e.g. "; charset=utf-8"), so two type strings can be compared for equality.
func NormalizeMediaType(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// PickImportContentType chooses the multipart part Content-Type using optional
// explicit caller input, else by matching fileName's extension to plugin MimeTypes.
func PickImportContentType(mimeTypes []string, fileName, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		if len(mimeTypes) > 0 {
			want := NormalizeMediaType(explicit)
			for _, m := range mimeTypes {
				if NormalizeMediaType(m) == want {
					return m, nil
				}
			}
			return "", fmt.Errorf(
				"content_type %q is not in this plugin's acceptFileMimeTypes [%s]",
				explicit,
				strings.Join(mimeTypes, ", "),
			)
		}
		return explicit, nil
	}
	if len(mimeTypes) == 0 {
		return "application/octet-stream", nil
	}
	if len(mimeTypes) == 1 {
		return mimeTypes[0], nil
	}
	ext := strings.ToLower(path.Ext(fileName))
	if ext == "" {
		return "", fmt.Errorf(
			"file_name must include a file extension or set content_type to one of: %s",
			strings.Join(mimeTypes, ", "),
		)
	}
	for _, m := range mimeTypes {
		base := strings.TrimSpace(m)
		if i := strings.Index(base, ";"); i >= 0 {
			base = base[:i]
		}
		exts, _ := mime.ExtensionsByType(base)
		for _, e := range exts {
			if strings.ToLower(e) == ext {
				return m, nil
			}
		}
	}
	if t := mime.TypeByExtension(ext); t != "" {
		tNorm := NormalizeMediaType(t)
		for _, m := range mimeTypes {
			if NormalizeMediaType(m) == tNorm {
				return m, nil
			}
		}
	}
	return "", fmt.Errorf(
		"could not map file extension %q to an accepted MIME type; set content_type to one of: %s",
		ext,
		strings.Join(mimeTypes, ", "),
	)
}

// IsAllDecimalDigits reports whether s is non-empty and contains only ASCII digits
// (saved filter IDs are numeric).
func IsAllDecimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// GetDefectTypesFromJSON extracts defect types from the project JSON response.
// It parses the raw JSON and returns the configuration/subTypes field as a JSON string.
func GetDefectTypesFromJSON(rawBody []byte) (string, error) {
	var projectData map[string]interface{}
	if err := json.Unmarshal(rawBody, &projectData); err != nil {
		return "", fmt.Errorf("failed to parse response JSON: %w", err)
	}

	configuration, ok := projectData["configuration"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("configuration field not found or invalid in response")
	}

	subtypes, ok := configuration["subTypes"]
	if !ok {
		return "", fmt.Errorf("configuration/subTypes field not found in response")
	}

	subtypesJSON, err := json.Marshal(subtypes)
	if err != nil {
		return "", fmt.Errorf("failed to serialize defect types: %w", err)
	}

	return string(subtypesJSON), nil
}

// ResolveSavedFilterIDByName returns the numeric filter ID for the filterId query parameter
// using GET /v1/{projectKey}/filter with filter.eq.name.
func ResolveSavedFilterIDByName(
	ctx context.Context,
	client *gorp.Client,
	project, filterName string,
) (string, error) {
	page, resp, err := client.UserFilterAPI.GetAllFilters(ctx, project).
		FilterEqName(filterName).
		Execute()
	if err != nil {
		return "", fmt.Errorf("%s: %w", ExtractResponseError(err, resp), err)
	}
	content := page.GetContent()
	if len(content) == 0 {
		return "", fmt.Errorf("no saved filter found with name %q", filterName)
	}
	return strconv.FormatInt(content[0].GetId(), 10), nil
}

// ResolveFilterIDForProvider returns the value for the filterId query parameter when using providerType=filter.
// All-decimal strings are treated as saved filter IDs and passed through; any other non-empty string is resolved
// as a saved filter name via ResolveSavedFilterIDByName.
func ResolveFilterIDForProvider(
	ctx context.Context,
	client *gorp.Client,
	project, filterIDOrName string,
) (filterID string, err error) {
	trimmed := strings.TrimSpace(filterIDOrName)
	if trimmed == "" {
		return "", fmt.Errorf("filter-id is empty")
	}
	if IsAllDecimalDigits(trimmed) {
		if strings.TrimLeft(trimmed, "0") == "" {
			return "", fmt.Errorf("filter-id must be greater than zero")
		}
		slog.Debug(
			"filter-id is numeric; using as saved filter ID",
			"filterId",
			trimmed,
			"project",
			project,
		)
		return trimmed, nil
	}
	id, err := ResolveSavedFilterIDByName(ctx, client, project, trimmed)
	if err != nil {
		return "", err
	}
	slog.Debug(
		"resolved filter-id from saved filter name",
		"filterName",
		trimmed,
		"filterId",
		id,
		"project",
		project,
	)
	return id, nil
}
