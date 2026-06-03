package integrations

import (
	"log/slog"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDatadogWebhookE2E1(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	err := core.ProcessEventWebook(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), "https://app.nudgebee.com/api/webhooks/datadog", map[string]string{"Accept": "*/*", "Accept-Encoding": "gzip, deflate", "Authorization": "Bearer MySuperSecretToken", "Content-Length": "1396", "Content-Type": "application/json; charset=utf-8", "Traceparent": "00-000000000000000056658dd645b6615b-5a5e82b22b828899-00", "User-Agent": "python-requests/2.32.4", "X-Datadog-Parent-Id": "6511785812970080409", "X-Datadog-Trace-Id": "6225538011341676891", "X-Forwarded-For": "192.168.1.2", "X-Forwarded-Host": "app.nudgebee.com", "X-Forwarded-Port": "443", "X-Forwarded-Proto": "https", "X-Forwarded-Scheme": "https", "X-Real-Ip": "192.168.1.2", "X-Request-Id": "a40df18af6adc556617b4c734669933f", "X-Scheme": "https"}, datadogPayload)
	time.Sleep(10 * time.Minute)
	assert.NoError(t, err, "should process datadog webhook without error")
}

func TestDatadogWebhookE2E2(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	err := core.ProcessEventWebook(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), "https://app.nudgebee.com/api/webhooks/datadog", map[string]string{"Accept": "*/*", "Accept-Encoding": "gzip, deflate", "Authorization": "Bearer MySuperSecretToken", "Content-Length": "1396", "Content-Type": "application/json; charset=utf-8", "Traceparent": "00-000000000000000056658dd645b6615b-5a5e82b22b828899-00", "User-Agent": "python-requests/2.32.4", "X-Datadog-Parent-Id": "6511785812970080409", "X-Datadog-Trace-Id": "6225538011341676891", "X-Forwarded-For": "192.168.1.2", "X-Forwarded-Host": "app.nudgebee.com", "X-Forwarded-Port": "443", "X-Forwarded-Proto": "https", "X-Forwarded-Scheme": "https", "X-Real-Ip": "192.168.1.2", "X-Request-Id": "a40df18af6adc556617b4c734669933f", "X-Scheme": "https"}, datadogPayload2)
	time.Sleep(10 * time.Minute)
	assert.NoError(t, err, "should process datadog webhook without error")
}
