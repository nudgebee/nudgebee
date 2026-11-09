package common

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	slogformatter "github.com/samber/slog-formatter"
	sloggin "github.com/samber/slog-gin"
)

func TestRedactQuerySecrets(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no secrets", "env=prod&cluster=us-east-1", "env=prod&cluster=us-east-1"},
		{"token only", "token=abc123", "token=REDACTED"},
		{"token among labels keeps order", "env=prod&token=abc123&team=sre", "env=prod&token=REDACTED&team=sre"},
		{"case insensitive", "Token=abc&AUTHORIZATION=Bearer%20x", "Token=REDACTED&AUTHORIZATION=REDACTED"},
		{"escaped key", "%74oken=abc", "%74oken=REDACTED"},
		{"repeated token", "token=a&token=b", "token=REDACTED&token=REDACTED"},
		{"substring key untouched", "csrf_token=x&my_token=y", "csrf_token=x&my_token=y"},
		{"other secret keys", "api_key=k&apikey=k&access_token=t&secret=s&password=p",
			"api_key=REDACTED&apikey=REDACTED&access_token=REDACTED&secret=REDACTED&password=REDACTED"},
		{"key without value untouched", "token&env=prod", "token&env=prod"},
		{"bad escape in key falls back to raw key", "%zz=1&token=abc", "%zz=1&token=REDACTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactQuerySecrets(tt.in); got != tt.want {
				t.Errorf("RedactQuerySecrets(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactURLSecrets(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/api/webhooks/pagerduty", "/api/webhooks/pagerduty"},
		{"/api/webhooks/pagerduty?token=abc&env=prod", "/api/webhooks/pagerduty?token=REDACTED&env=prod"},
		{"https://h/api/webhooks/datadog?env=prod&token=abc#frag", "https://h/api/webhooks/datadog?env=prod&token=REDACTED#frag"},
		{"/p?", "/p?"},
	}
	for _, tt := range tests {
		if got := RedactURLSecrets(tt.in); got != tt.want {
			t.Errorf("RedactURLSecrets(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRedactRequestLogAttr_AccessLog drives a real request through the same
// logger chain cmd/main.go builds (trace handler -> slog-formatter -> JSON
// handler) and the slog-gin access-log middleware, and asserts the webhook
// token never reaches the log while the rest of the query does.
func TestRedactRequestLogAttr_AccessLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	logger := slog.New(NewTraceLogHandler(
		slogformatter.NewFormatterHandler(slogformatter.ErrorFormatter("error"))(
			slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: RedactRequestLogAttr}),
		),
	))

	r := gin.New()
	r.Use(sloggin.NewWithConfig(logger, sloggin.DefaultConfig()))
	r.POST("/api/webhooks/pagerduty", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/pagerduty?env=prod&token=s3cr3t-webhook-token", strings.NewReader("{}"))
	r.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if strings.Contains(out, "s3cr3t-webhook-token") {
		t.Fatalf("access log contains the webhook token: %s", out)
	}
	if !strings.Contains(out, "env=prod\\u0026token=REDACTED") && !strings.Contains(out, "env=prod&token=REDACTED") {
		t.Fatalf("access log lost the redacted query: %s", out)
	}
}

func TestRedactRequestLogAttr(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		attr   slog.Attr
		want   string
	}{
		{"access-log query", []string{"request"}, slog.String("query", "env=x&token=abc"), "env=x&token=REDACTED"},
		{"top-level query", nil, slog.String("query", "token=abc"), "token=REDACTED"},
		{"url in other group", []string{"webhook"}, slog.String("url", "/api/webhooks/grafana?token=abc"), "/api/webhooks/grafana?token=REDACTED"},
		{"uri", nil, slog.String("uri", "/p?access_token=abc"), "/p?access_token=REDACTED"},
		{"query without secrets unchanged", nil, slog.String("query", "sum(rate(x[5m]))"), "sum(rate(x[5m]))"},
		{"other key unchanged", nil, slog.String("path", "/p?token=abc"), "/p?token=abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactRequestLogAttr(tt.groups, tt.attr); got.Value.String() != tt.want {
				t.Errorf("got %q, want %q", got.Value.String(), tt.want)
			}
		})
	}
}
