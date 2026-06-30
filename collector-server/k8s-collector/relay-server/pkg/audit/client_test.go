package audit

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testEvent() Event {
	return Event{
		TenantId:      "11111111-1111-1111-1111-111111111111",
		AccountId:     "22222222-2222-2222-2222-222222222222",
		EventTime:     time.Now().UTC(),
		EventCategory: CategoryK8sRelay,
		EventType:     TypeK8sRelayAgentConnected,
		EventState:    map[string]any{"status": "CONNECTED"},
		EventActor:    ActorK8sCollectorService,
		EventTarget:   "22222222-2222-2222-2222-222222222222",
		EventAction:   ActionUpdate,
		EventStatus:   StatusSuccess,
	}
}

func TestNewClient_DisabledWhenUnconfigured(t *testing.T) {
	if c := NewClient("", "tok"); c != nil {
		t.Fatalf("expected nil client when base URL empty, got %v", c)
	}
	if c := NewClient("http://x", ""); c != nil {
		t.Fatalf("expected nil client when token empty, got %v", c)
	}
	if c := NewClient("  ", "  "); c != nil {
		t.Fatalf("expected nil client when both blank, got %v", c)
	}
}

func TestEmit_NilClientIsNoop(t *testing.T) {
	var c *Client
	// Must not panic.
	c.Emit(slog.Default(), testEvent())
}

func TestEmit_SkipsEmptyTenant(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	ev := testEvent()
	ev.TenantId = ""
	c.Emit(slog.Default(), ev)

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("expected no HTTP call for empty tenant, got %d", got)
	}
}

func TestEmit_PostsAuditWithToken(t *testing.T) {
	type captured struct {
		token       string
		contentType string
		audits      []Event
	}
	ch := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req request
		_ = json.Unmarshal(body, &req)
		ch <- captured{
			token:       r.Header.Get("X-ACTION-TOKEN"),
			contentType: r.Header.Get("Content-Type"),
			audits:      req.Audits,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/", "s3cr3t") // trailing slash should be trimmed
	c.Emit(slog.Default(), testEvent())

	select {
	case got := <-ch:
		if got.token != "s3cr3t" {
			t.Errorf("X-ACTION-TOKEN = %q, want s3cr3t", got.token)
		}
		if got.contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got.contentType)
		}
		if len(got.audits) != 1 {
			t.Fatalf("audits len = %d, want 1", len(got.audits))
		}
		if got.audits[0].EventType != TypeK8sRelayAgentConnected {
			t.Errorf("event_type = %q, want %q", got.audits[0].EventType, TypeK8sRelayAgentConnected)
		}
		if got.audits[0].EventCategory != CategoryK8sRelay {
			t.Errorf("event_category = %q, want %q", got.audits[0].EventCategory, CategoryK8sRelay)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for audit POST")
	}
}

// A non-200 from the ingest endpoint must not panic and is swallowed.
func TestEmit_Non200IsSwallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	c.Emit(slog.Default(), testEvent()) // must not panic
}
