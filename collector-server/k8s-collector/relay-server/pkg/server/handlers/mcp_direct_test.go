package handlers

import (
	"testing"
)

func TestParseSSEResponse_WithDataLines(t *testing.T) {
	body := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"tools\":[]}}\n\ndata: [DONE]\n")
	got := parseSSEResponse(body)
	want := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	if got != want {
		t.Errorf("parseSSEResponse() = %q, want %q", got, want)
	}
}

func TestParseSSEResponse_MultipleDataLines(t *testing.T) {
	body := []byte("data: {\"part\":\"one\"}\ndata: {\"part\":\"two\"}\ndata: [DONE]\n")
	got := parseSSEResponse(body)
	want := `{"part":"one"}{"part":"two"}`
	if got != want {
		t.Errorf("parseSSEResponse() = %q, want %q", got, want)
	}
}

func TestParseSSEResponse_NoDataLines_FallsBackToRawBody(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	body := []byte(raw)
	got := parseSSEResponse(body)
	if got != raw {
		t.Errorf("parseSSEResponse() = %q, want raw body %q", got, raw)
	}
}

func TestParseSSEResponse_EmptyBody(t *testing.T) {
	got := parseSSEResponse([]byte(""))
	if got != "" {
		t.Errorf("parseSSEResponse() = %q, want empty string", got)
	}
}

func TestParseSSEResponse_StopsAtDone(t *testing.T) {
	body := []byte("data: {\"before\":true}\ndata: [DONE]\ndata: {\"after\":true}\n")
	got := parseSSEResponse(body)
	want := `{"before":true}`
	if got != want {
		t.Errorf("parseSSEResponse() = %q, want %q", got, want)
	}
}
