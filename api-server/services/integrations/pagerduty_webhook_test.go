package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const pagerDutyPayload = `{"event":{"id":"01FEAHT2OFHFT7RC7ECXMKNQYK","event_type":"incident.triggered","resource_type":"incident","occurred_at":"2025-01-24T07:10:05.538Z","agent":{"html_url":"https://nudgebee.pagerduty.com/services/P5PP4N4/integrations/PXMQRWJ","id":"PXMQRWJ","self":"https://api.pagerduty.com/services/P5PP4N4/integrations/PXMQRWJ","summary":"Events API V2","type":"inbound_integration_reference"},"client":{"name":"Last9 Dashboard","url":"https://app.last9.io/v2/organizations/nudgebee/compass/entities/17d2d015-0d0e-4e36-bf63-81e4f6884374/health?alert_hash=14996564410674116165&at=1739339400&created_at=1739339520&from=1739334120&indicator=HighErrorCriticalLogs&label_set=cluster%3D%22cluster-name%22%2C+container%3D%22node-agent%22%2C+container_id%3D%22%2Fk8s%2Fdefault%2Fargo-rollouts-54c8dd8467-b2q84%2Fargo-rollouts%22%2C+endpoint%3D%22http%22%2C+instance%3D%22172.31.94.166%3A80%22%2C+job%3D%22nudgebee-agent%2Fnudgebee-node-agent%22%2C+level%3D%22error%22%2C+machine_id%3D%22ec215c85dac11bfd99d7f3feac5e1750%22%2C+namespace%3D%22nudgebee-agent%22%2C+pattern_hash%3D%223bd20fb7b265576ec893e08c358c011f%22%2C+pod%3D%22nudgebee-agent-75z6k%22%2C+prometheus%3D%22victoria%2Fvictoria-victoria-metrics-k8s-stack%22%2C+sample%3D%22time%3D%222025-02-11T12%3A30%3A23Z%22+level%3Derror+msg%3D%22error+retrieving+resource+lock+default%2Fargo-rollouts-controller-lock%3A+leases.coordination.k8s.io+%5C%22argo-rollouts-controller-lock%5C%22+is+forbidden%3A+User+%5C%22system%3Aserviceaccount%3Adefault%3Aargo-rollouts%5C%22+cannot+get+resource+%5C%22leases%5C%22+in+API+group+%5C%22coordination.k8s.io%5C%22+in+the+namespace+%5C%22default%5C%22%22+error%3D%22%3Cnil%3E%22%22%2C+source%3D%22stdout%2Fstderr%22%2C+system_uuid%3D%22ec215c85-dac1-1bfd-99d7-f3feac5e1750%22&nac_id=169c9e6e-e8a4-4e88-9658-1702b5e89a19&rule_id=169c9e6e-e8a4-4e88-9658-1702b5e89a19&rule_name=HighErrorCriticalLogs&rule_type=static_threshold&severity=breach&timestamp=1739339400&to=1739339400&utm_campaign=anomaly_alert&utm_medium=IM&utm_name=pagerduty&utm_region=10m&kpi=HighErrorCriticalLogs"},"data":{"id":"Q2STH1QX0AJT5J","type":"incident","self":"https://api.pagerduty.com/incidents/Q2STH1QX0AJT5J","html_url":"https://nudgebee.pagerduty.com/incidents/Q2STH1QX0AJT5J","number":7,"status":"triggered","incident_key":null,"created_at":"2025-01-24T07:10:05Z","title":"up rule triggered on nn ","service":{"html_url":"https://nudgebee.pagerduty.com/services/P5PP4N4","id":"P5PP4N4","self":"https://api.pagerduty.com/services/P5PP4N4","summary":"Last9-Dev-Eks","type":"service_reference"},"assignees":[{"html_url":"https://nudgebee.pagerduty.com/users/PSQL7T5","id":"PSQL7T5","self":"https://api.pagerduty.com/users/PSQL7T5","summary":"shiv pratap singh","type":"user_reference"}],"escalation_policy":{"html_url":"https://nudgebee.pagerduty.com/escalation_policies/P2F4GX5","id":"P2F4GX5","self":"https://api.pagerduty.com/escalation_policies/P2F4GX5","summary":"Default","type":"escalation_policy_reference"},"teams":[],"priority":null,"urgency":"high","conference_bridge":null,"resolve_reason":null,"incident_type":{"name":"incident_default"}}}}`
const pagerDutyResolvedPayload = `{"event":{"id":"01FGCEBJ08K7FIL9YHQNAH8KZJ","event_type":"incident.resolved","resource_type":"incident","occurred_at":"2025-02-15T14:42:34.992Z","agent":{"html_url":"https://nudgebee.pagerduty.com/users/PSQL7T5","id":"PSQL7T5","self":"https://api.pagerduty.com/users/PSQL7T5","summary":"shiv pratap singh","type":"user_reference"},"client":null,"data":{"id":"Q2STH1QX0AJT5J","type":"incident","self":"https://api.pagerduty.com/incidents/Q06D419EX7GF1K","html_url":"https://nudgebee.pagerduty.com/incidents/Q06D419EX7GF1K","number":29,"status":"resolved","incident_key":null,"created_at":"2025-02-12T10:15:08Z","title":"NGINXTooMany400s triggered on nudgebee-api-alerts ","service":{"html_url":"https://nudgebee.pagerduty.com/services/P5PP4N4","id":"P5PP4N4","self":"https://api.pagerduty.com/services/P5PP4N4","summary":"Last9-Dev-Eks","type":"service_reference"},"assignees":[],"escalation_policy":{"html_url":"https://nudgebee.pagerduty.com/escalation_policies/P2F4GX5","id":"P2F4GX5","self":"https://api.pagerduty.com/escalation_policies/P2F4GX5","summary":"Default","type":"escalation_policy_reference"},"teams":[],"priority":null,"urgency":"high","conference_bridge":null,"resolve_reason":null,"incident_type":{"name":"incident_default"}}}}`
const pagerDutyPayloadChronosphere = `{
    "event": {
      "id": "01FVRJDBZPAFCIQO0U5B2ZJ2GQ",
      "event_type": "incident.triggered",
      "resource_type": "incident",
      "occurred_at": "2025-08-02T05:33:07.523Z",
      "agent": null,
      "client": {
        "name": "Chronosphere",
        "url": "https://fourkites.chronosphere.io/monitors/critical-staging-aws-inf-sqs-visible-msg?receiver=techops-alert-optimisation-1&receiver-type=pagerduty&status=CRITICAL&end=1754113087037&start=1754109487037&signal=%7B%22dimension_QueueName%22%3A%22load-worker-staging%22%2C%22tag_alert%22%3A%22SRE-OTR-FTL%22%2C%22tag_env%22%3A%22staging%22%7D"
      },
      "data": {
        "id": "Q2X369CTEOAGYM",
        "type": "incident",
        "self": "https://api.pagerduty.com/incidents/Q2X369CTEOAGYM",
        "html_url": "https://fourkites-inc.pagerduty.com/incidents/Q2X369CTEOAGYM",
        "number": 320624,
        "status": "triggered",
        "incident_key": null,
        "created_at": "2025-08-02T05:33:07Z",
        "title": "[Critical] Critical | Staging | AWS INF | SQS | ApproximateNumberOfMessagesVisible breached Upper Threshold {dimension_QueueName=\"load-worker-staging\", tag_alert=\"SRE-OTR-FTL\", tag_env=\"staging\"}",
        "service": {
          "id": "P6V6QVD",
          "type": "service_reference",
          "self": "https://api.pagerduty.com/services/P6V6QVD",
          "html_url": "https://fourkites-inc.pagerduty.com/services/P6V6QVD",
          "summary": "SRE-OTR-FTL"
        },
        "assignees": [
          {
            "id": "PD2F4XG",
            "type": "user_reference",
            "self": "https://api.pagerduty.com/users/PD2F4XG",
            "html_url": "https://fourkites-inc.pagerduty.com/users/PD2F4XG",
            "summary": "suriyalakshmi.p"
          }
        ],
        "escalation_policy": {
          "id": "P2HUWGV",
          "type": "escalation_policy_reference",
          "self": "https://api.pagerduty.com/escalation_policies/P2HUWGV",
          "html_url": "https://fourkites-inc.pagerduty.com/escalation_policies/P2HUWGV",
          "summary": "OTR-LTL-New_Merge"
        },
        "teams": [
          {
            "id": "PH65BB6",
            "type": "team_reference",
            "self": "https://api.pagerduty.com/teams/PH65BB6",
            "html_url": "https://fourkites-inc.pagerduty.com/teams/PH65BB6",
            "summary": "OTR-Team"
          }
        ],
        "priority": null,
        "urgency": "high",
        "conference_bridge": null,
        "resolve_reason": null,
        "incident_type": {
          "name": "incident_default"
        }
      }
    }
  }`

const pagerDutyPayloadNoClient = `{"event":{"id":"01FVSY3TYZRNGRHL3BIQ1RTDG1","event_type":"incident.triggered","resource_type":"incident","occurred_at":"2025-08-02T15:45:53.839Z","agent":null,"client":null,"data":{"id":"Q19Z7CCJDTBD40","type":"incident","self":"https://api.pagerduty.com/incidents/Q19Z7CCJDTBD40","html_url":"https://fourkites-inc.pagerduty.com/incidents/Q19Z7CCJDTBD40","number":320681,"status":"triggered","incident_key":null,"created_at":"2025-08-02T15:45:53Z","title":"ALARM: CRITICAL | AWS APP | ELB | mm-carrier-updates-worker-stg-al2-r2... in US East (N. Virginia)","service":{"id":"PZ55WG8","type":"service_reference","self":"https://api.pagerduty.com/services/PZ55WG8","html_url":"https://fourkites-inc.pagerduty.com/services/PZ55WG8","summary":"Event_Catchall_ocean"},"assignees":[{"id":"PJ9XKQO","type":"user_reference","self":"https://api.pagerduty.com/users/PJ9XKQO","html_url":"https://fourkites-inc.pagerduty.com/users/PJ9XKQO","summary":"padmanaban.p@fourkites.com"}],"escalation_policy":{"id":"PKEADJH","type":"escalation_policy_reference","self":"https://api.pagerduty.com/escalation_policies/PKEADJH","html_url":"https://fourkites-inc.pagerduty.com/escalation_policies/PKEADJH","summary":"SRE-ISBU-Ocean"},"teams":[{"id":"P3O4AC3","type":"team_reference","self":"https://api.pagerduty.com/teams/P3O4AC3","html_url":"https://fourkites-inc.pagerduty.com/teams/P3O4AC3","summary":"ISBU-Ocean"}],"priority":null,"urgency":"high","conference_bridge":null,"resolve_reason":null,"incident_type":{"name":"incident_default"}}}}`

func TestTools_ParsePagerDutyWebhookPayloadResolved(t *testing.T) {
	userId := os.Getenv("TEST_USER")

	pagerDutyIntegration, _ := core.GetIntegration(IntegrationPagerdutyWebhook)
	assert.NotNil(t, pagerDutyIntegration)
	pagerDutyWebhookIntgeration, _ := pagerDutyIntegration.(PagerDutyWebhook)
	assert.NotNil(t, pagerDutyWebhookIntgeration)

	eventData, err := pagerDutyWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), pagerDutyResolvedPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

func TestTools_ParsePagerDutyWebhookPayload(t *testing.T) {
	pagerDutyIntegration, _ := core.GetIntegration(IntegrationPagerdutyWebhook)
	assert.NotNil(t, pagerDutyIntegration)
	pagerDutyWebhookIntgeration, _ := pagerDutyIntegration.(PagerDutyWebhook)
	assert.NotNil(t, pagerDutyWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := pagerDutyWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), pagerDutyPayloadChronosphere)
	assert.NotEmpty(t, eventData)
	assert.Nil(t, err)
}

func TestTools_ParsePagerDutyWebhookPayloadChronosphere(t *testing.T) {
	pagerDutyIntegration, _ := core.GetIntegration(IntegrationPagerdutyWebhook)
	assert.NotNil(t, pagerDutyIntegration)
	pagerDutyWebhookIntgeration, _ := pagerDutyIntegration.(PagerDutyWebhook)
	assert.NotNil(t, pagerDutyWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := pagerDutyWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), pagerDutyPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
	eventData, err = pagerDutyWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), pagerDutyResolvedPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

func TestTools_ParsePagerDutyWebhookPayloadNoClient(t *testing.T) {
	pagerDutyIntegration, _ := core.GetIntegration(IntegrationPagerdutyWebhook)
	assert.NotNil(t, pagerDutyIntegration)
	pagerDutyWebhookIntgeration, _ := pagerDutyIntegration.(PagerDutyWebhook)
	assert.NotNil(t, pagerDutyWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := pagerDutyWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), pagerDutyPayloadNoClient)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

func TestExtractServiceFromPipeTitle(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:     "Chronosphere style with service name",
			title:    "Critical | Prod | EKS | booking-service | low apdex",
			expected: "booking-service",
		},
		{
			name:     "Service name with multiple segments",
			title:    "Critical | EKS | shipment-master-service | Apdex breached",
			expected: "shipment-master-service",
		},
		{
			name:     "No pipe delimiter",
			title:    "High CPU usage on prod server",
			expected: "",
		},
		{
			name:     "All known keywords, no service",
			title:    "Critical | Prod | AWS | High Error Rate",
			expected: "",
		},
		{
			name:     "Service name among keywords",
			title:    "Warning | staging | courier-worker | timeout errors",
			expected: "courier-worker",
		},
		{
			name:     "Single word segments (not service names)",
			title:    "Alert | Production | Down",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractServiceFromPipeTitle(tt.title)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractServiceFromSigNozURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "Empty URL",
			url:      "",
			expected: "",
		},
		{
			name:     "Non-SigNoz URL",
			url:      "https://grafana.example.com/dashboard",
			expected: "",
		},
		{
			name:     "SigNoz URL with service.name in compositeQuery",
			url:      `https://telemetry.example.com/logs?compositeQuery={"builderQueries":{"A":{"filters":{"items":[{"key":{"key":"service.name"},"value":"courier-worker-prod"}]}}}}`,
			expected: "courier-worker",
		},
		{
			name:     "SigNoz URL with service name no env suffix",
			url:      `https://telemetry.example.com/logs?compositeQuery={"builderQueries":{"A":{"filters":{"items":[{"key":{"key":"service.name"},"value":"booking-service"}]}}}}`,
			expected: "booking-service",
		},
		{
			name:     "URL without compositeQuery param",
			url:      "https://telemetry.example.com/logs?other=param",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractServiceFromSigNozURL(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStripEnvSuffix(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"prod suffix", "courier-worker-prod", "courier-worker"},
		{"production suffix", "api-production", "api"},
		{"staging suffix", "booking-service-staging", "booking-service"},
		{"no suffix", "payment-service", "payment-service"},
		{"dev suffix", "auth-dev", "auth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripEnvSuffix(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveSubjectFromLabels(t *testing.T) {
	tests := []struct {
		name              string
		initialSubject    string
		labels            map[string]string
		title             string
		expectedSubject   string
		expectedNamespace string
	}{
		{
			name:            "Already has subject - skip",
			initialSubject:  "existing-pod",
			labels:          map[string]string{"job": "some-service"},
			title:           "Alert on something",
			expectedSubject: "existing-pod",
		},
		{
			name:            "Resolve from nb_alert_job",
			initialSubject:  "",
			labels:          map[string]string{"nb_alert_job": "payment-service", "job": "other-thing"},
			title:           "Alert on something",
			expectedSubject: "payment-service",
		},
		{
			name:            "Resolve from job label",
			initialSubject:  "",
			labels:          map[string]string{"job": "order-service"},
			title:           "Alert on something",
			expectedSubject: "order-service",
		},
		{
			name:              "Resolve namespace from labels",
			initialSubject:    "",
			labels:            map[string]string{"job": "api-service", "namespace": "production"},
			title:             "Alert on something",
			expectedSubject:   "api-service",
			expectedNamespace: "production",
		},
		{
			name:            "Fallback to pipe title parsing",
			initialSubject:  "",
			labels:          map[string]string{},
			title:           "Critical | Prod | EKS | booking-service | low apdex",
			expectedSubject: "booking-service",
		},
		{
			name:              "Resolve from destination_workload_name (Grafana)",
			initialSubject:    "",
			labels:            map[string]string{"destination_workload_name": "cloud-collector-server", "destination_workload_namespace": "example-on-prem-test"},
			title:             "[FIRING:1] HighAPIFailureRate nudgebee (pd cloud-collector-server example-on-prem-test)",
			expectedSubject:   "cloud-collector-server",
			expectedNamespace: "example-on-prem-test",
		},
		{
			name:            "destination_workload_name takes priority over job",
			initialSubject:  "",
			labels:          map[string]string{"destination_workload_name": "cloud-collector-server", "job": "some-other-thing"},
			title:           "[FIRING:1] HighAPIFailureRate",
			expectedSubject: "cloud-collector-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := &core.EventIncomingWebhook{
				EventSubjectName: tt.initialSubject,
				EventTitle:       tt.title,
				Investigation: core.EventIncomingWebhookInvestigation{
					Labels: tt.labels,
				},
			}
			resolveSubjectFromLabels(payload)
			assert.Equal(t, tt.expectedSubject, payload.EventSubjectName)
			if tt.expectedNamespace != "" {
				assert.Equal(t, tt.expectedNamespace, payload.EventSubjectNamespace)
			}
		})
	}
}

// TestLLMLabelExtraction_Grafana tests the LLM extraction with a real Grafana PagerDuty alert payload.
// Requires TEST_USER, TEST_TENANT, TEST_ACCOUNT env vars and a running LLM server.
func TestLLMLabelExtraction_Grafana(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	if userId == "" {
		t.Skip("TEST_USER not set, skipping LLM integration test")
	}

	sc := security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil)
	accountId := os.Getenv("TEST_ACCOUNT")

	// Simulate a Grafana alert that came through PagerDuty with enriched labels
	// but NO subject_name set (deterministic parser didn't find it).
	// The LLM should extract "cloud-collector-server" from the title/labels.
	parsedPayload := &core.EventIncomingWebhook{
		EventTitle:       "[FIRING:1] HighAPIFailureRate nudgebee (pd cloud-collector-server example-on-prem-test)",
		EventDescription: "**Alert Name:** HighAPIFailureRate\n**Source:** Grafana\n**Folder:** nudgebee\n**Value:** A=33.33, B=33.33, C=1",
		Investigation: core.EventIncomingWebhookInvestigation{
			Labels: map[string]string{
				"alertname":         "HighAPIFailureRate",
				"grafana_folder":    "nudgebee",
				"alert_value":       "A=33.33333641975251, B=33.33333641975251, C=1",
				"source_url":        "http://localhost:3000/alerting/grafana/ceyby8qbinmkgb/view?orgId=1",
				"silenceURL":        "http://localhost:3000/alerting/silence/new?alertmanager=grafana&matcher=destination_workload_name%3Dcloud-collector-server&matcher=destination_workload_namespace%3Dexample-on-prem-test",
				"nb_alert_source":   "grafana",
				"nb_alert_name":     "HighAPIFailureRate",
				"nb_webhook_source": "pagerduty_webhook",
			},
			SourceUrl: "http://localhost:3000/alerting/grafana/ceyby8qbinmkgb/view?orgId=1",
		},
	}

	core.ResolveSubjectViaLLM(sc, parsedPayload, accountId)

	t.Logf("LLM extracted subject_name: %q", parsedPayload.EventSubjectName)
	t.Logf("LLM extracted namespace: %q", parsedPayload.EventSubjectNamespace)
	t.Logf("nb_llm_match label: %q", parsedPayload.Investigation.Labels["nb_llm_match"])

	// The LLM should ideally extract "cloud-collector-server" from the title
	// but we don't assert exact match since LLM output varies and workloads may not exist
	if parsedPayload.EventSubjectName != "" {
		t.Logf("LLM successfully identified subject: %s", parsedPayload.EventSubjectName)
	} else {
		t.Logf("LLM did not find a match (may be expected if k8s_workloads table is empty)")
	}
}

// TestLLMLabelExtraction_Chronosphere tests LLM extraction with a Chronosphere-style pipe-delimited alert.
func TestLLMLabelExtraction_Chronosphere(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	if userId == "" {
		t.Skip("TEST_USER not set, skipping LLM integration test")
	}

	sc := security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil)
	accountId := os.Getenv("TEST_ACCOUNT")

	parsedPayload := &core.EventIncomingWebhook{
		EventTitle:       "[Critical] Critical | Staging | AWS INF | SQS | ApproximateNumberOfMessagesVisible breached Upper Threshold {dimension_QueueName=\"load-worker-staging\", tag_alert=\"SRE-OTR-FTL\", tag_env=\"staging\"}",
		EventDescription: "Chronosphere alert for SQS queue threshold breach",
		Investigation: core.EventIncomingWebhookInvestigation{
			Labels: map[string]string{
				"nb_alert_source":     "chronosphere",
				"nb_alert_name":       "Critical | Staging | AWS INF | SQS | ApproximateNumberOfMessagesVisible breached Upper Threshold",
				"environment":         "staging",
				"monitorName":         "critical-staging-aws-inf-sqs-visible-msg",
				"dimension_QueueName": "load-worker-staging",
				"nb_webhook_source":   "pagerduty_webhook",
			},
			SourceUrl: "https://fourkites.chronosphere.io/monitors/critical-staging-aws-inf-sqs-visible-msg",
		},
	}

	core.ResolveSubjectViaLLM(sc, parsedPayload, accountId)

	t.Logf("LLM extracted subject_name: %q", parsedPayload.EventSubjectName)
	t.Logf("LLM extracted namespace: %q", parsedPayload.EventSubjectNamespace)
	t.Logf("nb_llm_match: %q", parsedPayload.Investigation.Labels["nb_llm_match"])
	t.Logf("aws_service_name: %q", parsedPayload.Investigation.Labels["aws_service_name"])

	if parsedPayload.EventSubjectName != "" {
		t.Logf("LLM successfully identified subject: %s", parsedPayload.EventSubjectName)
	}
}

func TestTools_GetCreatePagerDutyToolConfigs(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil)
	toolConfigName := "last9-pd-events"

	err := core.DeleteIntegrationConfig(sc, IntegrationPagerdutyWebhook, toolConfigName, "")
	assert.Nil(t, err)

	config, err := core.CreateIntegrationConfig(sc, "", IntegrationPagerdutyWebhook, toolConfigName, []core.IntegrationConfigValue{
		{
			Name:  "token",
			Value: "EXAMPLE_PAGERDUTY_WEBHOOK_TOKEN",
		},
	},
		map[string]any{
			"env": "dev",
		}, []string{accountId}, false, "",
	)

	assert.Nil(t, err)
	assert.NotEmpty(t, config.Name)

	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationPagerdutyWebhook)
	assert.Nil(t, err)
	assert.NotEmpty(t, configs)

	err = core.ProcessEventWebook(sc, "http://app.nudgebee.com/api/webhooks/pagerduty?token=EXAMPLE_PAGERDUTY_WEBHOOK_TOKEN", map[string]string{}, pagerDutyPayload)
	assert.Nil(t, err)

}
