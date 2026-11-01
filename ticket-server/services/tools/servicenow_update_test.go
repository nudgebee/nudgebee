package tools

import (
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/tickets-server/models"

	"github.com/gin-gonic/gin"
)

// snStub serves an incident lookup and a PUT, recording how many of each arrive and the PUT body.
type snStub struct {
	mu        sync.Mutex
	gets      int
	puts      int
	putBody   map[string]any
	putPath   string
	putStatus int
	// putError overrides the error body written on a non-2xx PUT; defaults to a bare
	// {"error":{"message":"denied"}} when empty.
	putError string
	delay    time.Duration
}

func (s *snStub) counts() (gets, puts int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets, s.puts
}

func newServiceNowStub(t *testing.T, st *snStub) models.TicketConfigurations {
	t.Helper()
	stub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if st.delay > 0 {
			time.Sleep(st.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		st.mu.Lock()
		defer st.mu.Unlock()
		if r.Method == http.MethodPut {
			st.puts++
			st.putPath = r.URL.Path
			var reader io.Reader = r.Body
			if r.Header.Get("Content-Encoding") == "gzip" { // kiota compresses request bodies
				if gz, err := gzip.NewReader(r.Body); err == nil {
					reader = gz
				}
			}
			body, _ := io.ReadAll(reader)
			_ = json.Unmarshal(body, &st.putBody)
			if st.putStatus != 0 {
				w.WriteHeader(st.putStatus)
				errBody := st.putError
				if errBody == "" {
					errBody = `{"error":{"message":"denied"}}`
				}
				_, _ = w.Write([]byte(errBody))
				return
			}
			_, _ = w.Write([]byte(`{"result":{"sys_id":"abc"}}`))
			return
		}
		st.gets++
		_, _ = w.Write([]byte(`{"result":[{"sys_id":"abc"}]}`))
	}))
	t.Cleanup(stub.Close)
	trustTestServerCert(t, stub)
	return models.TicketConfigurations{URL: stub.URL, Username: "u", Password: "p"}
}

// trustTestServerCert makes both HTTP call paths this file exercises accept the test
// server's self-signed cert. common.HttpClient() wraps whatever *http.Transport object
// http.DefaultTransport pointed to when the common package was initialized — a value
// captured once, so reassigning the http.DefaultTransport variable later (as tests that
// only exercise the SDK's kiota client do) never reaches it. Mutating that same
// *http.Transport's TLSClientConfig in place does, since both paths hold the same object.
// It mutates that process-wide object, so no test in this package may use t.Parallel().
func trustTestServerCert(t *testing.T, stub *httptest.Server) {
	t.Helper()
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is a %T, not *http.Transport", http.DefaultTransport)
	}
	pool := x509.NewCertPool()
	pool.AddCert(stub.Certificate())
	orig := transport.TLSClientConfig
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	t.Cleanup(func() { transport.TLSClientConfig = orig })
}

func TestServiceNowService_Update_AppliesEveryFieldInOnePut(t *testing.T) {
	st := &snStub{}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{
		Impact: "3", Urgency: "High", Severity: "Highest", Assignee: "sys-user-1", Description: "**hi**", Status: "In Progress",
		Raw: map[string]any{"resolution_code": "Solved (Permanently)", "resolution_notes": "**fixed**", "work_notes": "checked logs"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	gets, puts := st.counts()
	if gets != 1 || puts != 1 {
		t.Fatalf("want one lookup and one PUT, got %d and %d", gets, puts)
	}
	want := map[string]string{
		"urgency": "1", "impact": "3", "assigned_to": "sys-user-1", "description": "hi", "state": "2",
		"close_code": "Solved (Permanently)", "close_notes": "fixed", "work_notes": "checked logs",
	}
	for k, v := range want {
		if st.putBody[k] != v {
			t.Errorf("PUT %s = %v, want %s (body %v)", k, st.putBody[k], v, st.putBody)
		}
	}
}

// A ServiceNow rejection (e.g. a mandatory-field data policy) carries the real reason
// in the response body's "detail", not the generic top-level "message" the SDK's own
// error type prefers — the update must surface "detail". See #39314.
func TestServiceNowService_Update_SurfacesRejectionDetail(t *testing.T) {
	st := &snStub{putStatus: http.StatusForbidden, putError: `{"error":{"message":"Operation Failed","detail":"Data Policy Exception: The following fields are mandatory: Resolution code, Close notes"}}`}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Status: "Resolved"})
	if err == nil || !strings.Contains(err.Error(), "Resolution code, Close notes") {
		t.Errorf("expected the mandatory-fields detail in the error, got %v", err)
	}
}

func TestServiceNowService_Update_TrailingSlashOnConfigURLDoesNotDoubleUp(t *testing.T) {
	st := &snStub{}
	cfg := newServiceNowStub(t, st)
	cfg.URL += "/"
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	if err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Status: "Resolved"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if strings.Contains(st.putPath, "//") {
		t.Errorf("PUT path has a double slash: %q", st.putPath)
	}
}

func TestServiceNowService_Update_SurfacesAGenericMessageForAnHTMLErrorPage(t *testing.T) {
	st := &snStub{putStatus: http.StatusBadGateway, putError: `<html><body>502 Bad Gateway</body></html>`}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Status: "Resolved"})
	if err == nil || strings.Contains(err.Error(), "<html>") {
		t.Errorf("expected a generic message, not the raw HTML page, got %v", err)
	}
}

func TestServiceNowService_Update_FailureIsOnePutAndAnError(t *testing.T) {
	st := &snStub{putStatus: http.StatusForbidden}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Urgency: "Low", Assignee: "sys-user-1"})
	if err == nil {
		t.Fatal("expected an error when the PUT is refused")
	}
	if _, puts := st.counts(); puts != 1 {
		t.Errorf("a refused update must not be followed by more writes, got %d PUTs", puts)
	}
}

func TestServiceNowService_Update_RejectsSeveralAssigneesBeforeAnyRequest(t *testing.T) {
	st := &snStub{}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Severity: "High", Assignees: []string{"a", "b"}})
	if err == nil || !strings.Contains(err.Error(), "only a single assignee") {
		t.Fatalf("expected the single-assignee error, got %v", err)
	}
	if gets, puts := st.counts(); gets != 0 || puts != 0 {
		t.Errorf("nothing may be sent when the update is invalid, got %d lookups and %d PUTs", gets, puts)
	}
}

func TestServiceNowService_Update_NothingToChangeSendsNothing(t *testing.T) {
	st := &snStub{}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	if err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gets, puts := st.counts(); gets != 0 || puts != 0 {
		t.Errorf("expected no requests, got %d lookups and %d PUTs", gets, puts)
	}
}

func TestServiceNowService_Update_GivesUpOnAnUnresponsiveInstance(t *testing.T) {
	st := &snStub{delay: 1500 * time.Millisecond}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	orig := servicenowRequestTimeout
	servicenowRequestTimeout = 200 * time.Millisecond
	defer func() { servicenowRequestTimeout = orig }()

	start := time.Now()
	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Urgency: "High"})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 1200*time.Millisecond {
		t.Errorf("took %v; the request timeout should have cut it off near 200ms", elapsed)
	}
}

func TestServiceNowStateCode(t *testing.T) {
	for in, want := range map[string]string{
		"new": "1", "Open": "1", "In Progress": "2", "acknowledged": "2", "on_hold": "3",
		"Resolved": "6", "closed": "7", "cancelled": "8", "canceled": "8", "5": "5",
	} {
		if got := serviceNowStateCode(in); got != want {
			t.Errorf("serviceNowStateCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceNowService_Update_IgnoresSeverityAndWritesOnlyGivenLevels(t *testing.T) {
	st := &snStub{}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Severity: "Highest", Urgency: "2"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if st.putBody["urgency"] != "2" {
		t.Errorf("urgency = %v, want 2", st.putBody["urgency"])
	}
	if _, ok := st.putBody["impact"]; ok {
		t.Errorf("impact must be left alone when not supplied, body %v", st.putBody)
	}

	st2 := &snStub{}
	cfg2 := newServiceNowStub(t, st2)
	err = (&ServiceNowService{}).Update(ctx, cfg2, "INC0010001", models.UpdateFields{Severity: "Highest"})
	if err == nil || !strings.Contains(err.Error(), "Impact and Urgency") {
		t.Errorf("a severity-only update must fail loudly, got %v", err)
	}
	if _, puts := st2.counts(); puts != 0 {
		t.Errorf("a severity-only update must not write anything, got %d PUTs", puts)
	}
}

func TestServiceNowService_Update_RejectsUnknownUrgency(t *testing.T) {
	st := &snStub{}
	cfg := newServiceNowStub(t, st)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	err := (&ServiceNowService{}).Update(ctx, cfg, "INC0010001", models.UpdateFields{Urgency: "Critical"})
	if err == nil || !strings.Contains(err.Error(), "urgency") {
		t.Errorf("urgency=Critical must be rejected, got %v", err)
	}
	if gets, puts := st.counts(); gets != 0 || puts != 0 {
		t.Errorf("a rejected value must not reach ServiceNow, got %d GETs and %d PUTs", gets, puts)
	}
}
