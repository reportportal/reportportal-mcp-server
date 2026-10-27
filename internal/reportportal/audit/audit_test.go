package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/reportportal/reportportal-mcp-server/internal/reportportal/utils"
)

func testEmitter(buf *bytes.Buffer) *Emitter {
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return New(ServerInfo{
		Name:     "reportportal-mcp-server",
		Version:  "1.5.0",
		Instance: "mcp-pod-1",
	}, log)
}

func auditEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for {
		var ev map[string]any
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if ev["msg"] == msgAudit {
			events = append(events, ev)
		}
	}
	return events
}

func TestMiddleware_ToolCallFieldsAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)

	const (
		resultSecret  = "RESULTSECRET-do-not-log"
		fileSecret    = "FILECONTENTSECRET"
		commentSecret = "COMMENTSECRET"
		filterSecret  = "FILTERTEXTSECRET"
		tokenSecret   = "TOKENSECRET-do-not-log"
		claimSecret   = "HIDDENCLAIMEMAIL"
		headerSecret  = "HEADERTOKENSECRET"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := &http.Client{Transport: WrapRoundTripper(srv.Client().Transport)}

	handler := em.Middleware()(func(
		ctx context.Context,
		method string,
		req mcp.Request,
	) (mcp.Result, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(httpReq)
		require.NoError(t, err)
		_ = resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: resultSecret}},
		}, nil
	})

	args := `{
		"project": "superadmin_personal",
		"launch_id": "123",
		"test_items_ids": ["10", "11"],
		"file_content": "` + fileSecret + `",
		"comment": "` + commentSecret + `",
		"filter-cnt-name": "` + filterSecret + `",
		"defect_type_comment": "` + commentSecret + `",
		"token": "` + tokenSecret + `",
		"description": "free text must stay out"
	}`
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "get_launches",
			Arguments: json.RawMessage(args),
		},
		Extra: &mcp.RequestExtra{
			TokenInfo: &auth.TokenInfo{
				UserID: "user-oid-1",
				Scopes: []string{"access_as_user"},
				Extra: map[string]any{
					"claims": map[string]any{
						"azp":                "aebc6443-996d-45c2-90f0-388ff96faa56",
						"preferred_username": "jane@corp.com",
						"email":              claimSecret,
						"access_token":       tokenSecret,
					},
				},
			},
			Header: http.Header{
				"X-Project":     []string{"header-project"},
				"Authorization": []string{"Bearer " + headerSecret},
			},
		},
	}

	result, err := handler(context.Background(), methodToolsCall, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	events := auditEvents(t, &buf)
	require.Len(t, events, 1)
	ev := events[0]

	assert.Equal(t, "INFO", ev["level"])
	assert.Equal(t, msgAudit, ev["msg"])
	assert.Equal(t, EventToolCall, ev["event"])
	assert.Equal(t, "aebc6443-996d-45c2-90f0-388ff96faa56", ev["client_app_id"])
	assert.Equal(t, "user-oid-1", ev["user_oid"])
	assert.Equal(t, "jane@corp.com", ev["user_name"])
	assert.Equal(t, authOAuth, ev["auth_method"])
	assert.Equal(t, "reportportal-mcp-server", ev["server_name"])
	assert.Equal(t, "1.5.0", ev["server_version"])
	assert.Equal(t, "mcp-pod-1", ev["server_instance"])
	assert.Equal(t, methodToolsCall, ev["method"])
	assert.Equal(t, "get_launches", ev["tool"])
	assert.Equal(t, []any{"access_as_user"}, ev["scopes"])
	assert.Equal(t, "superadmin_personal", ev["project"])
	assert.Equal(t, outcomeSuccess, ev["outcome"])
	assert.Equal(t, float64(http.StatusForbidden), ev["rp_status"])
	assert.Equal(t, float64(len(resultSecret)), ev["result_size"])
	assert.GreaterOrEqual(t, ev["duration_ms"], float64(1))

	ids, ok := ev["resource_ids"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "123", ids["launch_id"])
	assert.Equal(t, []any{"10", "11"}, ids["test_items_ids"])
	assert.NotContains(t, ids, "project")
	assert.NotContains(t, ids, "file_content")
	assert.NotContains(t, ids, "comment")
	assert.NotContains(t, ids, "filter-cnt-name")

	raw := buf.String()
	for _, secret := range []string{
		resultSecret, fileSecret, commentSecret, filterSecret,
		tokenSecret, claimSecret, headerSecret, "free text must stay out",
	} {
		assert.NotContains(t, raw, secret)
	}
	assert.NotContains(t, raw, "claims")
	assert.NotContains(t, raw, "Authorization")
}

func TestMiddleware_AppIDFallbackAndAPIKey(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	handler := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})

	_, err := handler(context.Background(), methodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "get_launches",
			Arguments: json.RawMessage(`{"launch-id": 99}`),
		},
		Extra: &mcp.RequestExtra{
			TokenInfo: &auth.TokenInfo{
				UserID: "hashed-api-key",
				Extra: map[string]any{
					"claims": map[string]any{"appid": "app-from-appid"},
				},
			},
			Header: http.Header{"X-Project": []string{"from-header"}},
		},
	})
	require.NoError(t, err)

	ev := auditEvents(t, &buf)[0]
	assert.Equal(t, "app-from-appid", ev["client_app_id"])
	assert.Equal(t, "from-header", ev["project"])
	ids := ev["resource_ids"].(map[string]any)
	assert.Equal(t, "99", ids["launch-id"])

	buf.Reset()
	_, err = handler(context.Background(), methodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "get_launches"},
		Extra: &mcp.RequestExtra{
			TokenInfo: &auth.TokenInfo{UserID: "hashed-api-key"},
		},
	})
	require.NoError(t, err)
	ev = auditEvents(t, &buf)[0]
	assert.Equal(t, authAPIKey, ev["auth_method"])
	assert.Equal(t, "hashed-api-key", ev["user_oid"])
	assert.Empty(t, ev["user_name"])
	assert.Empty(t, ev["client_app_id"])
}

func TestMiddleware_Outcomes(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)

	toolErr := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "TOOLERRORSECRET"}},
		}, nil
	})
	_, err := toolErr(context.Background(), methodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "get_launches"},
	})
	require.NoError(t, err)
	assert.Equal(t, outcomeToolError, auditEvents(t, &buf)[0]["outcome"])
	assert.NotContains(t, buf.String(), "TOOLERRORSECRET")

	buf.Reset()
	protoErr := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return nil, errors.New("PROTOCOLSECRET")
	})
	_, err = protoErr(context.Background(), methodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "missing"},
	})
	require.Error(t, err)
	assert.Equal(t, outcomeError, auditEvents(t, &buf)[0]["outcome"])
	assert.NotContains(t, buf.String(), "PROTOCOLSECRET")
}

func TestMiddleware_PanicStillAudited(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	handler := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		panic("PANICSECRET")
	})

	require.PanicsWithValue(t, "PANICSECRET", func() {
		_, _ = handler(context.Background(), methodToolsCall, &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{Name: "get_launches"},
		})
	})
	events := auditEvents(t, &buf)
	require.Len(t, events, 1)
	assert.Equal(t, outcomeError, events[0]["outcome"])
	assert.NotContains(t, buf.String(), "PANICSECRET")
}

func TestMiddleware_SkipsOtherMethods(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	called := 0
	handler := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		called++
		return nil, nil
	})
	_, err := handler(context.Background(), "tools/list", &mcp.CallToolRequest{})
	require.NoError(t, err)
	assert.Equal(t, 1, called)
	assert.Empty(t, auditEvents(t, &buf))
}

func TestMiddleware_ResourceAndPrompt(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	handler := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return nil, nil
	})

	ctx := utils.WithProjectInContext(context.Background(), "ctx-project")
	_, err := handler(ctx, methodResourcesRead, &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "reportportal://superadmin_personal/launch/123",
		},
	})
	require.NoError(t, err)

	_, err = handler(ctx, methodPromptsGet, &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name: "reportportal_analyze_launch",
			Arguments: map[string]string{
				"launch_id": "55",
				"name":      "PROMPTNAMESECRET",
			},
		},
	})
	require.NoError(t, err)

	events := auditEvents(t, &buf)
	require.Len(t, events, 2)

	assert.Equal(t, methodResourcesRead, events[0]["method"])
	assert.Equal(t, "reportportal://superadmin_personal/launch/123", events[0]["tool"])
	assert.Equal(t, "superadmin_personal", events[0]["project"])

	assert.Equal(t, methodPromptsGet, events[1]["method"])
	assert.Equal(t, "reportportal_analyze_launch", events[1]["tool"])
	assert.Equal(t, "ctx-project", events[1]["project"])
	ids := events[1]["resource_ids"].(map[string]any)
	assert.Equal(t, "55", ids["launch_id"])
	assert.NotContains(t, buf.String(), "PROMPTNAMESECRET")
}

func TestMiddleware_ErrorStatusAndContextProject(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	codes := []int{http.StatusOK, http.StatusUnauthorized, http.StatusOK}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(codes[n])
		n++
	}))
	defer srv.Close()
	client := &http.Client{Transport: WrapRoundTripper(srv.Client().Transport)}

	handler := em.Middleware()(func(
		ctx context.Context,
		method string,
		req mcp.Request,
	) (mcp.Result, error) {
		for range codes {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			require.NoError(t, err)
			resp, err := client.Do(httpReq)
			require.NoError(t, err)
			_ = resp.Body.Close()
		}
		return &mcp.CallToolResult{}, nil
	})

	ctx := utils.WithProjectInContext(context.Background(), "from-context")
	_, err := handler(ctx, methodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "get_launches"},
	})
	require.NoError(t, err)
	ev := auditEvents(t, &buf)[0]
	assert.Equal(t, float64(http.StatusUnauthorized), ev["rp_status"])
	assert.Equal(t, "from-context", ev["project"])
}

func TestMiddleware_ClientInfoFromSession(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "reportportal-mcp-server",
		Version: "1.5.0",
	}, nil)
	server.AddReceivingMiddleware(em.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "get_launches"}, func(
		context.Context, *mcp.CallToolRequest, map[string]any,
	) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, nil, nil
	})
	server.AddResource(&mcp.Resource{
		URI:  "reportportal://demo/launch/1",
		Name: "launch",
	}, func(
		context.Context, *mcp.ReadResourceRequest,
	) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:  "reportportal://demo/launch/1",
				Text: "RESOURCESECRET",
			}},
		}, nil
	})
	server.AddPrompt(&mcp.Prompt{Name: "analyze"}, func(
		context.Context, *mcp.GetPromptRequest,
	) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: "PROMPTSECRET"},
			}},
		}, nil
	})

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "Visual Studio Code",
		Version: "1.107.0",
	}, nil)
	session, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, auditEvents(t, &buf))

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_launches",
		Arguments: map[string]any{"project": "demo", "launch_id": "7"},
	})
	require.NoError(t, err)
	_, err = session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "reportportal://demo/launch/1",
	})
	require.NoError(t, err)
	_, err = session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "analyze"})
	require.NoError(t, err)

	events := auditEvents(t, &buf)
	require.Len(t, events, 3)
	methods := map[string]map[string]any{}
	for _, ev := range events {
		methods[ev["method"].(string)] = ev
		assert.Equal(t, "Visual Studio Code", ev["client_name"])
		assert.Equal(t, "1.107.0", ev["client_version"])
		assert.Equal(t, EventToolCall, ev["event"])
	}
	require.Contains(t, methods, methodToolsCall)
	require.Contains(t, methods, methodResourcesRead)
	require.Contains(t, methods, methodPromptsGet)
	assert.Equal(t, "get_launches", methods[methodToolsCall]["tool"])
	assert.Equal(t, "demo", methods[methodToolsCall]["project"])
	assert.Equal(t, outcomeSuccess, methods[methodToolsCall]["outcome"])
	assert.NotContains(t, buf.String(), "RESOURCESECRET")
	assert.NotContains(t, buf.String(), "PROMPTSECRET")
}

func TestMiddleware_ToolErrorFromHandler(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v"}, nil)
	server.AddReceivingMiddleware(em.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "get_launches"}, func(
		context.Context, *mcp.CallToolRequest, map[string]any,
	) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("HANDLERSECRET")
	})

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).
		Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_launches"})
	require.NoError(t, err)
	events := auditEvents(t, &buf)
	require.Len(t, events, 1)
	assert.Equal(t, outcomeToolError, events[0]["outcome"])
	assert.NotContains(t, buf.String(), "HANDLERSECRET")
	assert.Equal(t, float64(len("HANDLERSECRET")), events[0]["result_size"])
}

func TestAuthRejected_NoUserFields(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "203.0.113.5:443"
	req.Header.Set("Authorization", "Bearer TOKENSECRET")
	em.AuthRejected(req, ReasonInvalidSignature)

	events := auditEvents(t, &buf)
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, EventAuthRejected, ev["event"])
	assert.Equal(t, ReasonInvalidSignature, ev["reason"])
	assert.Equal(t, "203.0.113.5", ev["remote_ip"])
	assert.Equal(t, "/mcp", ev["path"])
	for _, key := range []string{
		"user_oid", "user_name", "client_app_id", "scopes", "tool", "project",
	} {
		assert.NotContains(t, ev, key)
	}
	assert.NotContains(t, buf.String(), "TOKENSECRET")
}

func TestAuthRejected_NilRequest(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	em.AuthRejected(nil, ReasonEmptyToken)
	ev := auditEvents(t, &buf)[0]
	assert.Equal(t, ReasonEmptyToken, ev["reason"])
	assert.Empty(t, ev["remote_ip"])
	assert.Empty(t, ev["path"])
}

func TestVerifyReason(t *testing.T) {
	cases := []struct {
		err  string
		want string
	}{
		{"oidc: token is expired (Token Expiry: 1)", ReasonExpired},
		{"failed to verify signature: crypto/rsa: verification error", ReasonInvalidSignature},
		{"oidc: id token not signed", ReasonInvalidSignature},
		{"oidc: malformed jwt: bad", ReasonInvalidSignature},
		{`oidc: expected audience "aud" got ["other"]`, ReasonInvalidAudience},
		{
			`oidc: id token issued by a different provider, expected "a" got "b"`,
			ReasonInvalidIssuer,
		},
		{"something else", ReasonInvalidToken},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, VerifyReason(errors.New(tc.err)), tc.err)
	}
	assert.Equal(t, ReasonInvalidToken, VerifyReason(nil))

	expired := fmt.Errorf("verify: %w", &oidc.TokenExpiredError{Expiry: time.Now()})
	assert.Equal(t, ReasonExpired, VerifyReason(expired))
}

func TestStatusRecorder_FirstErrorWins(t *testing.T) {
	cases := []struct {
		name  string
		codes []int
		want  int
	}{
		{"success only keeps last", []int{http.StatusFound, http.StatusOK}, http.StatusOK},
		{
			"later success does not hide error",
			[]int{http.StatusForbidden, http.StatusOK},
			http.StatusForbidden,
		},
		{
			"first error wins",
			[]int{http.StatusOK, http.StatusUnauthorized, http.StatusInternalServerError},
			http.StatusUnauthorized,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &StatusRecorder{}
			for _, code := range tc.codes {
				rec.Record(code)
			}
			status, ok := rec.Status()
			assert.True(t, ok)
			assert.Equal(t, tc.want, status)
		})
	}
}

func TestWrapRoundTripper_IgnoresMissingRecorder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: WrapRoundTripper(srv.Client().Transport)}
	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.True(t, IsRecordingTransport(client.Transport))
	assert.True(t, IsRecordingTransport(WrapRoundTripper(client.Transport)))
}

func TestStatusRecorder_NilSafe(t *testing.T) {
	var rec *StatusRecorder
	rec.Record(http.StatusOK)
	_, ok := rec.Status()
	assert.False(t, ok)
	assert.Nil(t, RecorderFromContext(context.Background()))
}

func TestRemoteIPWithoutPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.RemoteAddr = "198.51.100.10"
	var buf bytes.Buffer
	testEmitter(&buf).AuthRejected(req, ReasonExpired)
	assert.Equal(t, "198.51.100.10", auditEvents(t, &buf)[0]["remote_ip"])
}

func TestMiddleware_DropsObjectIDs(t *testing.T) {
	var buf bytes.Buffer
	em := testEmitter(&buf)
	handler := em.Middleware()(func(
		context.Context, string, mcp.Request,
	) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	_, err := handler(context.Background(), methodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name: "get_launches",
			Arguments: json.RawMessage(
				`{"launch_id":{"nested":"NESTEDSECRET"},"test_item_id":"42"}`,
			),
		},
	})
	require.NoError(t, err)
	ids := auditEvents(t, &buf)[0]["resource_ids"].(map[string]any)
	assert.Equal(t, "42", ids["test_item_id"])
	assert.NotContains(t, ids, "launch_id")
	assert.NotContains(t, buf.String(), "NESTEDSECRET")
	assert.NotContains(t, buf.String(), "nested")
}
