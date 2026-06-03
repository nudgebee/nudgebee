package core

import (
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRelayWebhookTrigge(t *testing.T) {
	tenant, account, user := testenv.RequireTenant(t)
	// "{\"event\":{\"id\":\"01EXAMPLE3AAAAAAAAAAAAAAAA\",\"event_type\":\"incident.triggered\",\"resource_type\":\"incident\",\"occurred_at\":\"2025-02-05T05:56:12.562Z\",\"agent\":{\"html_url\":\"https://example-inc.pagerduty.com/services/PEXAMPLE/integrations/PEXAMPLE2\",\"id\":\"PEXAMPLE2\",\"self\":\"https://api.pagerduty.com/services/PEXAMPLE/integrations/PEXAMPLE2\",\"summary\":\"Events API V2\",\"type\":\"inbound_integration_reference\"},\"client\":{\"name\":\"Example Monitoring\",\"url\":\"https://monitoring.example.com/v2/organizations/example/compass/entities/00000000-0000-0000-0000-000000000000/health?alert_hash=14007879189170800275&at=1738419780&created_at=1738419900&from=1738414500&indicator=HighErrorCriticalLogs&label_set=cluster%3D%22cluster-name%22%2C+container%3D%22node-agent%22%2C+container_id%3D%22%2Fk8s%2Fdefault%2Fargo-rollouts-54c8dd8467-fb22l%2Fargo-rollouts%22%2C+endpoint%3D%22http%22%2C+instance%3D%2210.0.0.2%3A80%22%2C+job%3D%22nudgebee-agent%2Fnudgebee-node-agent%22%2C+level%3D%22error%22%2C+machine_id%3D%2200000000000000000000000000000000%22%2C+namespace%3D%22nudgebee-agent%22%2C+pattern_hash%3D%223bd20fb7b265576ec893e08c358c011f%22%2C+pod%3D%22nudgebee-agent-kp6hw%22%2C+prometheus%3D%22victoria%2Fvictoria-victoria-metrics-k8s-stack%22%2C+sample%3D%22time%3D%222025-01-28T10%3A55%3A20Z%22+level%3Derror+msg%3D%22error+retrieving+resource+lock+default%2Fargo-rollouts-controller-lock%3A+leases.coordination.k8s.io+%5C%22argo-rollouts-controller-lock%5C%22+is+forbidden%3A+User+%5C%22system%3Aserviceaccount%3Adefault%3Aargo-rollouts%5C%22+cannot+get+resource+%5C%22leases%5C%22+in+API+group+%5C%22coordination.k8s.io%5C%22+in+the+namespace+%5C%22default%5C%22%22+error%3D%22%3Cnil%3E%22%22%2C+source%3D%22stdout%2Fstderr%22%2C+system_uuid%3D%2200000000-0000-0000-0000-000000000000%22&nac_id=00000000-0000-0000-0000-000000000001&rule_id=00000000-0000-0000-0000-000000000001&rule_name=HighErrorCriticalLogs&rule_type=static_threshold&severity=breach&timestamp=1738419780&to=1738419780&utm_campaign=anomaly_alert&utm_medium=IM&utm_name=pagerduty&utm_region=10m&kpi=HighErrorCriticalLogs\"},\"data\":{\"id\":\"QEXAMPLE3\",\"type\":\"incident\",\"self\":\"https://api.pagerduty.com/incidents/QEXAMPLE3\",\"html_url\":\"https://example-inc.pagerduty.com/incidents/QEXAMPLE3\",\"number\":15,\"status\":\"triggered\",\"incident_key\":null,\"created_at\":\"2025-02-05T05:56:12Z\",\"title\":\"HighErrorCriticalLogs triggered on nudgebee-api-alerts \",\"service\":{\"html_url\":\"https://example-inc.pagerduty.com/services/PEXAMPLE\",\"id\":\"PEXAMPLE\",\"self\":\"https://api.pagerduty.com/services/PEXAMPLE\",\"summary\":\"Test-Service\",\"type\":\"service_reference\"},\"assignees\":[{\"html_url\":\"https://example-inc.pagerduty.com/users/PEXAMPLE3\",\"id\":\"PEXAMPLE3\",\"self\":\"https://api.pagerduty.com/users/PEXAMPLE3\",\"summary\":\"Test User\",\"type\":\"user_reference\"}],\"escalation_policy\":{\"html_url\":\"https://example-inc.pagerduty.com/escalation_policies/PEXAMPLE4\",\"id\":\"PEXAMPLE4\",\"self\":\"https://api.pagerduty.com/escalation_policies/PEXAMPLE4\",\"summary\":\"Default\",\"type\":\"escalation_policy_reference\"},\"teams\":[],\"priority\":null,\"urgency\":\"high\",\"conference_bridge\":null,\"resolve_reason\":null,\"incident_type\":{\"name\":\"incident_default\"}}}}"

	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)

	event := EventIncomingWebhook{
		WebhookId:        "01EXAMPLE3AAAAAAAAAAAAAAAA",
		EventType:        "incident",
		EventId:          "QEXAMPLE3",
		EventUrl:         "https://api.pagerduty.com/incidents/QEXAMPLE3",
		EventStatus:      "triggered",
		EventPriority:    "",
		EventCreatedAt:   time.Now(),
		EventEndsAt:      time.Now(),
		EventTitle:       "HighErrorCriticalLogs triggered on nudgebee-api-alerts",
		EventDescription: `**Agent URL -** https://example-inc.pagerduty.com/services/PEXAMPLE/integrations/PEXAMPLE2\n **Client -** Example Monitoring\n **Client URL -** https://monitoring.example.com/v2/organizations/example/compass/entities/00000000-0000-0000-0000-000000000000/health?alert_hash=14007879189170800275&at=1738419780&created_at=1738419900&from=1738414500&indicator=HighErrorCriticalLogs&label_set=cluster%3D%22cluster-name%22%2C+container%3D%22node-agent%22%2C+container_id%3D%22%2Fk8s%2Fdefault%2Fargo-rollouts-54c8dd8467-fb22l%2Fargo-rollouts%22%2C+endpoint%3D%22http%22%2C+instance%3D%2210.0.0.2%3A80%22%2C+job%3D%22nudgebee-agent%2Fnudgebee-node-agent%22%2C+level%3D%22error%22%2C+machine_id%3D%2200000000000000000000000000000000%22%2C+namespace%3D%22nudgebee-agent%22%2C+pattern_hash%3D%223bd20fb7b265576ec893e08c358c011f%22%2C+pod%3D%22nudgebee-agent-kp6hw%22%2C+prometheus%3D%22victoria%2Fvictoria-victoria-metrics-k8s-stack%22%2C+sample%3D%22time%3D%222025-01-28T10%3A55%3A20Z%22+level%3Derror+msg%3D%22error+retrieving+resource+lock+default%2Fargo-rollouts-controller-lock%3A+leases.coordination.k8s.io+%5C%22argo-rollouts-controller-lock%5C%22+is+forbidden%3A+User+%5C%22system%3Aserviceaccount%3Adefault%3Aargo-rollouts%5C%22+cannot+get+resource+%5C%22leases%5C%22+in+API+group+%5C%22coordination.k8s.io%5C%22+in+the+namespace+%5C%22default%5C%22%22+error%3D%22%3Cnil%3E%22%22%2C+source%3D%22stdout%2Fstderr%22%2C+system_uuid%3D%2200000000-0000-0000-0000-000000000000%22&nac_id=00000000-0000-0000-0000-000000000001&rule_id=00000000-0000-0000-0000-000000000001&rule_name=HighErrorCriticalLogs&rule_type=static_threshold&severity=breach&timestamp=1738419780&to=1738419780&utm_campaign=anomaly_alert&utm_medium=IM&utm_name=pagerduty&utm_region=10m&kpi=HighErrorCriticalLogs`,
		EventTags:        []string{"event_tags"},
		Investigation: EventIncomingWebhookInvestigation{
			RuleName:    "HighErrorCriticalLogs",
			Labels:      map[string]string{"cluster": "cluster-name", "container": "node-agent", "container_id": ""},
			Annotations: map[string]string{},
			RuleType:    "static_threshold",
			RuleId:      "00000000-0000-0000-0000-000000000001",
			Fingerprint: "14007879189170800275",
			Status:      eventtypes.EventStatusClosed,
		},
	}
	err := investigateWebhookEvent(ctxt, user, account, "pagerduty", event)
	assert.Equal(t, err, nil)
}

func TestExtractWebhookToken(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		headers  map[string]string
		expected string
	}{
		{
			name:     "exact token query param",
			url:      "https://nb.example/api/webhooks/grafana?token=abc123",
			expected: "abc123",
		},
		{
			name:     "relative request path (no scheme/host)",
			url:      "/api/webhooks/grafana?token=EXAMPLE_GRAFANA_WEBHOOK_TOKEN",
			expected: "EXAMPLE_GRAFANA_WEBHOOK_TOKEN",
		},
		{
			name:     "token alongside other params",
			url:      "https://nb.example/api/webhooks/grafana?env=prod&token=abc123&cluster=us-east-1",
			expected: "abc123",
		},
		{
			name:     "unrelated key containing token= must not collide",
			url:      "https://nb.example/api/webhooks/grafana?my_token=wrong&csrf_token=alsoWrong",
			expected: "",
		},
		{
			name:     "fragment must not bleed into token value",
			url:      "https://nb.example/api/webhooks/grafana?token=abc123#section",
			expected: "abc123",
		},
		{
			name:     "percent-encoded token is decoded",
			url:      "https://nb.example/api/webhooks/grafana?token=abc%2B123",
			expected: "abc+123",
		},
		{
			name:     "Authorization header Bearer canonical",
			url:      "https://nb.example/api/webhooks/grafana",
			headers:  map[string]string{"Authorization": "Bearer xyz789"},
			expected: "xyz789",
		},
		{
			name:     "authorization header lowercase",
			url:      "https://nb.example/api/webhooks/grafana",
			headers:  map[string]string{"authorization": "Bearer xyz789"},
			expected: "xyz789",
		},
		{
			name:     "AUTHORIZATION header uppercase",
			url:      "https://nb.example/api/webhooks/grafana",
			headers:  map[string]string{"AUTHORIZATION": "Bearer xyz789"},
			expected: "xyz789",
		},
		{
			name:     "malformed Bearer (no trailing space) does not panic",
			url:      "https://nb.example/api/webhooks/grafana",
			headers:  map[string]string{"Authorization": "Bearer"},
			expected: "",
		},
		{
			name:     "non-Bearer auth scheme does not panic",
			url:      "https://nb.example/api/webhooks/grafana",
			headers:  map[string]string{"Authorization": "Basic dXNlcjpwYXNz"},
			expected: "",
		},
		{
			name:     "raw header without scheme does not panic",
			url:      "https://nb.example/api/webhooks/grafana",
			headers:  map[string]string{"Authorization": "abc123"},
			expected: "",
		},
		{
			name:     "URL token wins over header",
			url:      "https://nb.example/api/webhooks/grafana?token=fromUrl",
			headers:  map[string]string{"Authorization": "Bearer fromHeader"},
			expected: "fromUrl",
		},
		{
			name:     "no token anywhere",
			url:      "https://nb.example/api/webhooks/grafana?env=prod",
			headers:  map[string]string{},
			expected: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractWebhookToken(tc.url, tc.headers)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestParseAccountMapping(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		expected *AccountMapping
	}{
		{
			name: "rules canonical shape with single-value match",
			raw:  `{"rules":[{"match":{"env":"prod","region":"us"},"accountId":"acc-A"},{"match":{"env":"dev"},"accountId":"acc-B"}]}`,
			expected: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"prod"}, "region": {"us"}}, AccountId: "acc-A"},
				{Match: map[string][]string{"env": {"dev"}}, AccountId: "acc-B"},
			}},
		},
		{
			name: "rules with array values for OR-within-key",
			raw:  `{"rules":[{"match":{"env":["na","eu"],"region":"us"},"accountId":"acc-A"}]}`,
			expected: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"na", "eu"}, "region": {"us"}}, AccountId: "acc-A"},
			}},
		},
		{
			name:     "rules with empty match map are dropped",
			raw:      `{"rules":[{"match":{},"accountId":"acc-A"},{"match":{"env":"prod"},"accountId":"acc-B"}]}`,
			expected: &AccountMapping{Rules: []AccountMappingRule{{Match: map[string][]string{"env": {"prod"}}, AccountId: "acc-B"}}},
		},
		{
			name:     "rules with empty accountId are dropped",
			raw:      `{"rules":[{"match":{"env":"prod"},"accountId":""}]}`,
			expected: &AccountMapping{Rules: []AccountMappingRule{}},
		},
		{
			name: "legacy flat shape",
			raw:  `{"labelName":"env","dev":"22222222-2222-2222-2222-222222222222","prod":"44444444-4444-4444-4444-444444444444"}`,
			expected: &AccountMapping{Legacy: map[string]string{
				"labelName": "env",
				"dev":       "22222222-2222-2222-2222-222222222222",
				"prod":      "44444444-4444-4444-4444-444444444444",
			}},
		},
		{
			name: "legacy nested shape with label/value objects",
			raw:  `{"labelName":"env","dev":{"label":"k8s-dev","value":"22222222-2222-2222-2222-222222222222"},"prod":{"label":"k8s-prod","value":"44444444-4444-4444-4444-444444444444"}}`,
			expected: &AccountMapping{Legacy: map[string]string{
				"labelName": "env",
				"dev":       "22222222-2222-2222-2222-222222222222",
				"prod":      "44444444-4444-4444-4444-444444444444",
			}},
		},
		{
			name:     "legacy nested entry without value field is dropped",
			raw:      `{"labelName":"env","dev":{"label":"k8s-dev"}}`,
			expected: &AccountMapping{Legacy: map[string]string{"labelName": "env"}},
		},
		{
			name:     "missing setting returns nil",
			raw:      "",
			expected: nil,
		},
		{
			name:     "invalid JSON returns nil",
			raw:      `{"labelName":`,
			expected: nil,
		},
		{
			name:     "JSON null returns nil",
			raw:      `null`,
			expected: nil,
		},
		{
			name:     "rules with non-object match are dropped",
			raw:      `{"rules":[{"match":"oops","accountId":"acc-A"},{"match":{"env":"prod"},"accountId":"acc-B"}]}`,
			expected: &AccountMapping{Rules: []AccountMappingRule{{Match: map[string][]string{"env": {"prod"}}, AccountId: "acc-B"}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := []IntegrationConfigValue{}
			if tc.raw != "" {
				settings = append(settings, IntegrationConfigValue{Name: "account_mapping", Value: tc.raw})
			}
			got := ParseAccountMapping(settings, nil)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestApplyAccountMapping(t *testing.T) {
	cases := []struct {
		name     string
		mapping  *AccountMapping
		labels   map[string]string
		fallback string
		expected string
	}{
		{
			name:     "nil mapping returns fallback",
			mapping:  nil,
			labels:   map[string]string{"env": "prod"},
			fallback: "fallback-acc",
			expected: "fallback-acc",
		},
		{
			name:     "empty labels returns fallback",
			mapping:  &AccountMapping{Rules: []AccountMappingRule{{Match: map[string][]string{"env": {"prod"}}, AccountId: "acc-A"}}},
			labels:   map[string]string{},
			fallback: "fallback-acc",
			expected: "fallback-acc",
		},
		{
			name: "rule first-match-wins",
			mapping: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"prod"}, "region": {"us"}}, AccountId: "acc-A"},
				{Match: map[string][]string{"env": {"prod"}}, AccountId: "acc-B"},
			}},
			labels:   map[string]string{"env": "prod", "region": "us"},
			fallback: "fallback-acc",
			expected: "acc-A",
		},
		{
			name: "rule AND requires every key to match",
			mapping: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"prod"}, "region": {"us"}}, AccountId: "acc-A"},
			}},
			labels:   map[string]string{"env": "prod", "region": "eu"},
			fallback: "fallback-acc",
			expected: "fallback-acc",
		},
		{
			name: "rule value-list OR matches any listed value",
			mapping: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"na", "eu"}}, AccountId: "acc-A"},
			}},
			labels:   map[string]string{"env": "eu"},
			fallback: "fallback-acc",
			expected: "acc-A",
		},
		{
			name: "rule value-list OR misses when label absent from list",
			mapping: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"na", "eu"}}, AccountId: "acc-A"},
			}},
			labels:   map[string]string{"env": "prod"},
			fallback: "fallback-acc",
			expected: "fallback-acc",
		},
		{
			name: "rule missing label key is no match",
			mapping: &AccountMapping{Rules: []AccountMappingRule{
				{Match: map[string][]string{"env": {"prod"}, "region": {"us"}}, AccountId: "acc-A"},
			}},
			labels:   map[string]string{"env": "prod"},
			fallback: "fallback-acc",
			expected: "fallback-acc",
		},
		{
			name:     "legacy flat: matched value returns mapped account",
			mapping:  &AccountMapping{Legacy: map[string]string{"labelName": "env", "prod": "acc-A"}},
			labels:   map[string]string{"env": "prod"},
			fallback: "fallback-acc",
			expected: "acc-A",
		},
		{
			name:     "legacy flat: unmatched value returns fallback",
			mapping:  &AccountMapping{Legacy: map[string]string{"labelName": "env", "prod": "acc-A"}},
			labels:   map[string]string{"env": "staging"},
			fallback: "fallback-acc",
			expected: "fallback-acc",
		},
		{
			name:     "legacy flat: defaults labelName to env when omitted",
			mapping:  &AccountMapping{Legacy: map[string]string{"prod": "acc-A"}},
			labels:   map[string]string{"env": "prod"},
			fallback: "fallback-acc",
			expected: "acc-A",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyAccountMapping(tc.fallback, tc.labels, tc.mapping)
			assert.Equal(t, tc.expected, got)
		})
	}
}
