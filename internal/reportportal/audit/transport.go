package audit

import (
	"context"
	"net/http"
	"sync"
)

type statusKey struct{}

// StatusRecorder collects ReportPortal HTTP statuses for one MCP operation.
// When a tool makes more than one API call, the first error status (>= 400)
// wins so a later success cannot hide a denied request. Without errors the
// last status wins.
type StatusRecorder struct {
	mu     sync.Mutex
	status int
	seen   bool
}

// Record stores a response status. A nil recorder is ignored.
func (r *StatusRecorder) Record(code int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen && r.status >= http.StatusBadRequest {
		return
	}
	r.status = code
	r.seen = true
}

// Status returns the recorded HTTP status.
func (r *StatusRecorder) Status() (int, bool) {
	if r == nil {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.seen
}

// WithStatusRecorder returns a context that carries rec.
func WithStatusRecorder(ctx context.Context, rec *StatusRecorder) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, statusKey{}, rec)
}

// RecorderFromContext returns the recorder stored by WithStatusRecorder.
func RecorderFromContext(ctx context.Context) *StatusRecorder {
	if ctx == nil {
		return nil
	}
	rec, _ := ctx.Value(statusKey{}).(*StatusRecorder)
	return rec
}

type statusRoundTripper struct {
	base http.RoundTripper
}

// WrapRoundTripper records the ReportPortal response status on the recorder
// stored in the request context. Requests without a recorder are unchanged.
func WrapRoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*statusRoundTripper); ok {
		return base
	}
	return &statusRoundTripper{base: base}
}

// IsRecordingTransport reports whether rt records ReportPortal statuses.
func IsRecordingTransport(rt http.RoundTripper) bool {
	_, ok := rt.(*statusRoundTripper)
	return ok
}

func (t *statusRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	resp, err := t.base.RoundTrip(req)
	recordStatus(req, resp)
	return resp, err
}

func recordStatus(req *http.Request, resp *http.Response) {
	defer func() { _ = recover() }()
	if req == nil || resp == nil {
		return
	}
	if rec := RecorderFromContext(req.Context()); rec != nil {
		rec.Record(resp.StatusCode)
	}
}
