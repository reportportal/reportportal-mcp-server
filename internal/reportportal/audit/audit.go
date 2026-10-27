// Package audit writes one structured JSON event per MCP operation and per
// rejected bearer token. Events go to stdout so a log pipeline can collect them.
// Building an event never fails the MCP call.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

const (
	msgAudit = "audit"

	EventToolCall     = "mcp.tool_call"
	EventAuthRejected = "mcp.auth_rejected"

	methodToolsCall     = "tools/call"
	methodResourcesRead = "resources/read"
	methodPromptsGet    = "prompts/get"

	authOAuth  = "oauth"
	authAPIKey = "api_key"

	outcomeSuccess   = "success"
	outcomeToolError = "tool_error"
	outcomeError     = "error"

	// Stable rejection reasons. These never include token or claim text.
	ReasonEmptyToken       = "empty token"
	ReasonInvalidAPIKey    = "invalid API key" //nolint:gosec // reason label, not a credential
	ReasonExpired          = "expired"
	ReasonInvalidSignature = "invalid signature"
	ReasonInvalidAudience  = "invalid audience"
	ReasonInvalidIssuer    = "invalid issuer"
	ReasonInvalidToken     = "invalid token"
	ReasonMissingScope     = "missing required scope"
	ReasonMissingSubject   = "missing subject"
)

// allowedResourceIDs are argument names safe to record. Anything else,
// including comments, file content, and free-text filters, is dropped.
var allowedResourceIDs = map[string]struct{}{
	"launch_id":             {},
	"launch-id":             {},
	"test_item_id":          {},
	"test_items_ids":        {},
	"defect_type_id":        {},
	"parent-item-id":        {},
	"attachment-content-id": {},
	"filter-eq-parentId":    {},
	"filter-eq-launchId":    {},
}

var errCallPanicked = errors.New("mcp handler panicked")

// ServerInfo identifies this MCP process in every audit event.
type ServerInfo struct {
	Name     string
	Version  string
	Instance string
}

// Emitter writes audit events with a dedicated JSON logger.
type Emitter struct {
	info ServerInfo
	log  *slog.Logger
}

// New returns an emitter. A nil logger writes JSON lines to stdout.
func New(info ServerInfo, log *slog.Logger) *Emitter {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
	}
	if info.Name == "" {
		info.Name = "reportportal-mcp-server"
	}
	return &Emitter{info: info, log: log}
}

// Middleware records one event for tools/call, resources/read, and prompts/get.
func (e *Emitter) Middleware() mcp.Middleware {
	if e == nil {
		return func(next mcp.MethodHandler) mcp.MethodHandler { return next }
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(
			ctx context.Context,
			method string,
			req mcp.Request,
		) (result mcp.Result, err error) {
			if !isAuditedMethod(method) {
				return next(ctx, method, req)
			}

			rec := &StatusRecorder{}
			ctx = WithStatusRecorder(ctx, rec)
			start := time.Now()
			defer func() {
				recovered := recover()
				callErr := err
				if recovered != nil {
					callErr = errCallPanicked
				}
				e.emit(method, ctx, req, result, callErr, rec, time.Since(start))
				if recovered != nil {
					panic(recovered)
				}
			}()

			result, err = next(ctx, method, req)
			return result, err
		}
	}
}

// AuthRejected records a rejected bearer token. The event has no user fields.
func (e *Emitter) AuthRejected(r *http.Request, reason string) {
	if e == nil || e.log == nil {
		return
	}
	defer func() { _ = recover() }()

	ip, path := "", ""
	if r != nil {
		ip = remoteIP(r)
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	e.log.LogAttrs(context.Background(), slog.LevelInfo, msgAudit,
		slog.String("event", EventAuthRejected),
		slog.String("reason", reason),
		slog.String("remote_ip", ip),
		slog.String("path", path),
	)
}

// VerifyReason maps a JWT verification error to a stable reason that does not
// include claim values from the error text.
func VerifyReason(err error) string {
	if err == nil {
		return ReasonInvalidToken
	}
	var expired *oidc.TokenExpiredError
	if errors.As(err, &expired) {
		return ReasonExpired
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "expired"):
		return ReasonExpired
	case strings.Contains(msg, "signature") ||
		strings.Contains(msg, "not signed") ||
		strings.Contains(msg, "malformed jwt"):
		return ReasonInvalidSignature
	case strings.Contains(msg, "audience"):
		return ReasonInvalidAudience
	case strings.Contains(msg, "issuer") || strings.Contains(msg, "different provider"):
		return ReasonInvalidIssuer
	default:
		return ReasonInvalidToken
	}
}

func (e *Emitter) emit(
	method string,
	ctx context.Context,
	req mcp.Request,
	result mcp.Result,
	callErr error,
	rec *StatusRecorder,
	elapsed time.Duration,
) {
	if e == nil || e.log == nil {
		return
	}
	defer func() { _ = recover() }()

	access := accessFrom(method, ctx, req)
	who := identityFrom(req)
	clientName, clientVersion, sessionID := clientInfo(req)
	status, _ := rec.Status()

	e.log.LogAttrs(context.Background(), slog.LevelInfo, msgAudit,
		slog.String("event", EventToolCall),
		slog.String("client_app_id", who.clientAppID),
		slog.String("client_name", clientName),
		slog.String("client_version", clientVersion),
		slog.String("user_oid", who.userOID),
		slog.String("user_name", who.userName),
		slog.String("auth_method", who.authMethod),
		slog.String("server_name", e.info.Name),
		slog.String("server_version", e.info.Version),
		slog.String("server_instance", e.info.Instance),
		slog.String("session_id", sessionID),
		slog.String("method", method),
		slog.String("tool", access.tool),
		slog.Any("scopes", who.scopes),
		slog.String("project", access.project),
		slog.Any("resource_ids", access.ids),
		slog.String("outcome", outcomeOf(result, callErr)),
		slog.Int("rp_status", status),
		slog.Int("result_size", resultSize(result)),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	)
}

func isAuditedMethod(method string) bool {
	switch method {
	case methodToolsCall, methodResourcesRead, methodPromptsGet:
		return true
	default:
		return false
	}
}

type identity struct {
	clientAppID string
	userOID     string
	userName    string
	authMethod  string
	scopes      []string
}

func identityFrom(req mcp.Request) identity {
	id := identity{authMethod: authAPIKey, scopes: []string{}}
	if req == nil {
		return id
	}
	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return id
	}
	info := extra.TokenInfo
	id.userOID = info.UserID
	if info.Scopes != nil {
		id.scopes = append([]string(nil), info.Scopes...)
	}
	claims := claimsFrom(info)
	if claims == nil {
		return id
	}
	id.authMethod = authOAuth
	id.clientAppID = claimString(claims, "azp")
	if id.clientAppID == "" {
		id.clientAppID = claimString(claims, "appid")
	}
	id.userName = claimString(claims, "preferred_username")
	return id
}

func claimsFrom(info *auth.TokenInfo) map[string]any {
	if info == nil || info.Extra == nil {
		return nil
	}
	raw, ok := info.Extra["claims"]
	if !ok || raw == nil {
		return nil
	}
	claims, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return claims
}

func claimString(claims map[string]any, key string) string {
	v, ok := claims[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

type accessed struct {
	tool    string
	project string
	ids     map[string]any
}

func accessFrom(method string, ctx context.Context, req mcp.Request) accessed {
	acc := accessed{ids: map[string]any{}}
	if req == nil {
		return acc
	}
	switch method {
	case methodToolsCall:
		call, ok := req.(*mcp.CallToolRequest)
		if ok && call != nil && call.Params != nil {
			acc.tool = call.Params.Name
			acc.project, acc.ids = fromRawArgs(call.Params.Arguments)
		}
	case methodResourcesRead:
		read, ok := req.(*mcp.ReadResourceRequest)
		if ok && read != nil && read.Params != nil {
			acc.tool = read.Params.URI
			acc.project = projectFromURI(read.Params.URI)
		}
	case methodPromptsGet:
		get, ok := req.(*mcp.GetPromptRequest)
		if ok && get != nil && get.Params != nil {
			acc.tool = get.Params.Name
			acc.project, acc.ids = fromStringArgs(get.Params.Arguments)
		}
	}
	if acc.project == "" {
		acc.project = projectFromRequest(ctx, req)
	}
	if acc.ids == nil {
		acc.ids = map[string]any{}
	}
	return acc
}

func fromRawArgs(raw json.RawMessage) (string, map[string]any) {
	ids := map[string]any{}
	if len(raw) == 0 {
		return "", ids
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ids
	}
	project := stringArg(args, "project")
	for key, val := range args {
		if _, ok := allowedResourceIDs[key]; !ok {
			continue
		}
		id, ok := sanitizeID(val)
		if !ok {
			continue
		}
		ids[key] = id
	}
	return project, ids
}

func fromStringArgs(args map[string]string) (string, map[string]any) {
	ids := map[string]any{}
	if len(args) == 0 {
		return "", ids
	}
	var project string
	for key, val := range args {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		if key == "project" {
			project = val
			continue
		}
		if _, ok := allowedResourceIDs[key]; !ok {
			continue
		}
		ids[key] = val
	}
	return project, ids
}

func stringArg(args map[string]json.RawMessage, key string) string {
	raw, ok := args[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func sanitizeID(raw json.RawMessage) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return coerceID(v)
}

func coerceID(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, false
		}
		return t, true
	case json.Number:
		return t.String(), true
	case []any:
		items := make([]any, 0, len(t))
		for _, item := range t {
			coerced, ok := coerceID(item)
			if !ok {
				return nil, false
			}
			items = append(items, coerced)
		}
		return items, len(items) > 0
	default:
		return nil, false
	}
}

func projectFromURI(uri string) string {
	const prefix = "reportportal://"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(uri, prefix)
	project, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return project
}

func projectFromRequest(ctx context.Context, req mcp.Request) string {
	if req != nil {
		if extra := req.GetExtra(); extra != nil && extra.Header != nil {
			if project := strings.TrimSpace(extra.Header.Get("X-Project")); project != "" {
				return project
			}
		}
	}
	if project, ok := utils.GetProjectFromContext(ctx); ok {
		return project
	}
	return ""
}

func clientInfo(req mcp.Request) (name, version, sessionID string) {
	if req == nil {
		return "", "", ""
	}
	// A nil *ServerSession stored in the Request interface is not a nil interface.
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok || ss == nil {
		return "", "", ""
	}
	sessionID = ss.ID()
	params := ss.InitializeParams()
	if params == nil || params.ClientInfo == nil {
		return "", "", sessionID
	}
	return params.ClientInfo.Name, params.ClientInfo.Version, sessionID
}

func outcomeOf(result mcp.Result, err error) string {
	if err != nil {
		return outcomeError
	}
	if call, ok := result.(*mcp.CallToolResult); ok && call != nil && call.IsError {
		return outcomeToolError
	}
	return outcomeSuccess
}

func resultSize(result mcp.Result) int {
	switch r := result.(type) {
	case *mcp.CallToolResult:
		if r == nil {
			return 0
		}
		n := 0
		for _, c := range r.Content {
			if text, ok := c.(*mcp.TextContent); ok && text != nil {
				n += len(text.Text)
			}
		}
		return n
	case *mcp.ReadResourceResult:
		if r == nil {
			return 0
		}
		n := 0
		for _, c := range r.Contents {
			if c != nil {
				n += len(c.Text)
			}
		}
		return n
	case *mcp.GetPromptResult:
		if r == nil {
			return 0
		}
		n := 0
		for _, msg := range r.Messages {
			if msg == nil {
				continue
			}
			if text, ok := msg.Content.(*mcp.TextContent); ok && text != nil {
				n += len(text.Text)
			}
		}
		return n
	default:
		return 0
	}
}

func remoteIP(r *http.Request) string {
	if r == nil || r.RemoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
