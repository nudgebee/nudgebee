package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const pagerDutyPayload = `{"event":{"id":"01EXAMPLE1AAAAAAAAAAAAAAAA","event_type":"incident.triggered","resource_type":"incident","occurred_at":"2025-01-24T07:10:05.538Z","agent":{"html_url":"https://example-inc.pagerduty.com/services/PEXAMPLE/integrations/PEXAMPLE2","id":"PEXAMPLE2","self":"https://api.pagerduty.com/services/PEXAMPLE/integrations/PEXAMPLE2","summary":"Events API V2","type":"inbound_integration_reference"},"client":{"name":"Example Monitoring","url":"https://monitoring.example.com/v2/organizations/example/compass/entities/00000000-0000-0000-0000-000000000000/health?alert_hash=14996564410674116165&at=1739339400&created_at=1739339520&from=1739334120&indicator=HighErrorCriticalLogs&label_set=cluster%3D%22cluster-name%22%2C+container%3D%22node-agent%22%2C+container_id%3D%22%2Fk8s%2Fdefault%2Fargo-rollouts-54c8dd8467-b2q84%2Fargo-rollouts%22%2C+endpoint%3D%22http%22%2C+instance%3D%2210.0.0.1%3A80%22%2C+job%3D%22nudgebee-agent%2Fnudgebee-node-agent%22%2C+level%3D%22error%22%2C+machine_id%3D%2200000000000000000000000000000000%22%2C+namespace%3D%22nudgebee-agent%22%2C+pattern_hash%3D%223bd20fb7b265576ec893e08c358c011f%22%2C+pod%3D%22nudgebee-agent-75z6k%22%2C+prometheus%3D%22victoria%2Fvictoria-victoria-metrics-k8s-stack%22%2C+sample%3D%22time%3D%222025-02-11T12%3A30%3A23Z%22+level%3Derror+msg%3D%22error+retrieving+resource+lock+default%2Fargo-rollouts-controller-lock%3A+leases.coordination.k8s.io+%5C%22argo-rollouts-controller-lock%5C%22+is+forbidden%3A+User+%5C%22system%3Aserviceaccount%3Adefault%3Aargo-rollouts%5C%22+cannot+get+resource+%5C%22leases%5C%22+in+API+group+%5C%22coordination.k8s.io%5C%22+in+the+namespace+%5C%22default%5C%22%22+error%3D%22%3Cnil%3E%22%22%2C+source%3D%22stdout%2Fstderr%22%2C+system_uuid%3D%2200000000-0000-0000-0000-000000000000%22&nac_id=00000000-0000-0000-0000-000000000001&rule_id=00000000-0000-0000-0000-000000000001&rule_name=HighErrorCriticalLogs&rule_type=static_threshold&severity=breach&timestamp=1739339400&to=1739339400&utm_campaign=anomaly_alert&utm_medium=IM&utm_name=pagerduty&utm_region=10m&kpi=HighErrorCriticalLogs"},"data":{"id":"QEXAMPLE1","type":"incident","self":"https://api.pagerduty.com/incidents/QEXAMPLE1","html_url":"https://example-inc.pagerduty.com/incidents/QEXAMPLE1","number":7,"status":"triggered","incident_key":null,"created_at":"2025-01-24T07:10:05Z","title":"up rule triggered on nn ","service":{"html_url":"https://example-inc.pagerduty.com/services/PEXAMPLE","id":"PEXAMPLE","self":"https://api.pagerduty.com/services/PEXAMPLE","summary":"Test-Service","type":"service_reference"},"assignees":[{"html_url":"https://example-inc.pagerduty.com/users/PEXAMPLE3","id":"PEXAMPLE3","self":"https://api.pagerduty.com/users/PEXAMPLE3","summary":"Test User","type":"user_reference"}],"escalation_policy":{"html_url":"https://example-inc.pagerduty.com/escalation_policies/PEXAMPLE4","id":"PEXAMPLE4","self":"https://api.pagerduty.com/escalation_policies/PEXAMPLE4","summary":"Default","type":"escalation_policy_reference"},"teams":[],"priority":null,"urgency":"high","conference_bridge":null,"resolve_reason":null,"incident_type":{"name":"incident_default"}}}}`
const pagerDutyResolvedPayload = `{"event":{"id":"01EXAMPLE2AAAAAAAAAAAAAAAA","event_type":"incident.resolved","resource_type":"incident","occurred_at":"2025-02-15T14:42:34.992Z","agent":{"html_url":"https://example-inc.pagerduty.com/users/PEXAMPLE3","id":"PEXAMPLE3","self":"https://api.pagerduty.com/users/PEXAMPLE3","summary":"Test User","type":"user_reference"},"client":null,"data":{"id":"QEXAMPLE1","type":"incident","self":"https://api.pagerduty.com/incidents/QEXAMPLE2","html_url":"https://example-inc.pagerduty.com/incidents/QEXAMPLE2","number":29,"status":"resolved","incident_key":null,"created_at":"2025-02-12T10:15:08Z","title":"NGINXTooMany400s triggered on nudgebee-api-alerts ","service":{"html_url":"https://example-inc.pagerduty.com/services/PEXAMPLE","id":"PEXAMPLE","self":"https://api.pagerduty.com/services/PEXAMPLE","summary":"Test-Service","type":"service_reference"},"assignees":[],"escalation_policy":{"html_url":"https://example-inc.pagerduty.com/escalation_policies/PEXAMPLE4","id":"PEXAMPLE4","self":"https://api.pagerduty.com/escalation_policies/PEXAMPLE4","summary":"Default","type":"escalation_policy_reference"},"teams":[],"priority":null,"urgency":"high","conference_bridge":null,"resolve_reason":null,"incident_type":{"name":"incident_default"}}}}`
const pagerDutyPayloadChronosphere = `{
    "event": {
      "id": "01FVRJDBZPAFCIQO0U5B2ZJ2GQ",
      "event_type": "incident.triggered",
      "resource_type": "incident",
      "occurred_at": "2025-08-02T05:33:07.523Z",
      "agent": null,
      "client": {
        "name": "Chronosphere",
        "url": "https://example.chronosphere.io/monitors/critical-staging-aws-inf-sqs-visible-msg?receiver=techops-alert-optimisation-1&receiver-type=pagerduty&status=CRITICAL&end=1754113087037&start=1754109487037&signal=%7B%22dimension_QueueName%22%3A%22load-worker-staging%22%2C%22tag_alert%22%3A%22SRE-OTR-FTL%22%2C%22tag_env%22%3A%22staging%22%7D"
      },
      "data": {
        "id": "Q2X369CTEOAGYM",
        "type": "incident",
        "self": "https://api.pagerduty.com/incidents/Q2X369CTEOAGYM",
        "html_url": "https://example-inc.pagerduty.com/incidents/Q2X369CTEOAGYM",
        "number": 320624,
        "status": "triggered",
        "incident_key": null,
        "created_at": "2025-08-02T05:33:07Z",
        "title": "[Critical] Critical | Staging | AWS INF | SQS | ApproximateNumberOfMessagesVisible breached Upper Threshold {dimension_QueueName=\"load-worker-staging\", tag_alert=\"SRE-OTR-FTL\", tag_env=\"staging\"}",
        "service": {
          "id": "P6V6QVD",
          "type": "service_reference",
          "self": "https://api.pagerduty.com/services/P6V6QVD",
          "html_url": "https://example-inc.pagerduty.com/services/P6V6QVD",
          "summary": "SRE-OTR-FTL"
        },
        "assignees": [
          {
            "id": "PD2F4XG",
            "type": "user_reference",
            "self": "https://api.pagerduty.com/users/PD2F4XG",
            "html_url": "https://example-inc.pagerduty.com/users/PD2F4XG",
            "summary": "test.user"
          }
        ],
        "escalation_policy": {
          "id": "P2HUWGV",
          "type": "escalation_policy_reference",
          "self": "https://api.pagerduty.com/escalation_policies/P2HUWGV",
          "html_url": "https://example-inc.pagerduty.com/escalation_policies/P2HUWGV",
          "summary": "OTR-LTL-New_Merge"
        },
        "teams": [
          {
            "id": "PH65BB6",
            "type": "team_reference",
            "self": "https://api.pagerduty.com/teams/PH65BB6",
            "html_url": "https://example-inc.pagerduty.com/teams/PH65BB6",
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

const pagerDutyPayloadNoClient = `{"event":{"id":"01FVSY3TYZRNGRHL3BIQ1RTDG1","event_type":"incident.triggered","resource_type":"incident","occurred_at":"2025-08-02T15:45:53.839Z","agent":null,"client":null,"data":{"id":"Q19Z7CCJDTBD40","type":"incident","self":"https://api.pagerduty.com/incidents/Q19Z7CCJDTBD40","html_url":"https://example-inc.pagerduty.com/incidents/Q19Z7CCJDTBD40","number":320681,"status":"triggered","incident_key":null,"created_at":"2025-08-02T15:45:53Z","title":"ALARM: CRITICAL | AWS APP | ELB | mm-carrier-updates-worker-stg-al2-r2... in US East (N. Virginia)","service":{"id":"PZ55WG8","type":"service_reference","self":"https://api.pagerduty.com/services/PZ55WG8","html_url":"https://example-inc.pagerduty.com/services/PZ55WG8","summary":"Event_Catchall_ocean"},"assignees":[{"id":"PJ9XKQO","type":"user_reference","self":"https://api.pagerduty.com/users/PJ9XKQO","html_url":"https://example-inc.pagerduty.com/users/PJ9XKQO","summary":"user@example.com"}],"escalation_policy":{"id":"PKEADJH","type":"escalation_policy_reference","self":"https://api.pagerduty.com/escalation_policies/PKEADJH","html_url":"https://example-inc.pagerduty.com/escalation_policies/PKEADJH","summary":"SRE-ISBU-Ocean"},"teams":[{"id":"P3O4AC3","type":"team_reference","self":"https://api.pagerduty.com/teams/P3O4AC3","html_url":"https://example-inc.pagerduty.com/teams/P3O4AC3","summary":"ISBU-Ocean"}],"priority":null,"urgency":"high","conference_bridge":null,"resolve_reason":null,"incident_type":{"name":"incident_default"}}}}`

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
		expectedKind      string
		wantSkip          bool // expects the nb_skip_workload_match label to be set
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
		{
			// KubePersistentVolumeFillingUp: PVC is the subject, not the
			// kubelet scrape job. Regression test for subject=kubelet.
			name:              "persistentvolumeclaim wins over kubelet scrape job",
			initialSubject:    "",
			labels:            map[string]string{"job": "kubelet", "namespace": "hive", "persistentvolumeclaim": "dfs-hive-metastore-hdfs-datanode-0"},
			title:             "PersistentVolume is filling up.",
			expectedSubject:   "dfs-hive-metastore-hdfs-datanode-0",
			expectedNamespace: "hive",
			expectedKind:      "persistentvolumeclaim",
		},
		{
			// Real K8s Job alert: job_name is the subject; job is the scraper.
			name:            "job_name resolves the Job, not the scrape job",
			initialSubject:  "",
			labels:          map[string]string{"job": "kube-state-metrics", "job_name": "backup-1234", "namespace": "ops"},
			title:           "KubeJobFailed",
			expectedSubject: "backup-1234",
			expectedKind:    "job",
		},
		{
			// A bare exporter job must not become the subject.
			name:            "kubelet scrape job is not a subject",
			initialSubject:  "",
			labels:          map[string]string{"job": "kubelet"},
			title:           "KubeletTooManyPods",
			expectedSubject: "",
		},
		{
			// nb_alert_job that is an exporter is also rejected.
			name:            "nb_alert_job exporter is not a subject",
			initialSubject:  "",
			labels:          map[string]string{"nb_alert_job": "kubelet", "job": "kubelet"},
			title:           "KubePersistentVolumeFillingUp",
			expectedSubject: "",
		},
		{
			// Alertmanager TargetDown: the bare `service` label resolves the
			// subject deterministically instead of falling to the LLM. `job` here
			// is the exporter and is correctly ignored in favour of `service`.
			name:              "bare service label resolves subject",
			initialSubject:    "",
			labels:            map[string]string{"service": "nudgebee-prometheus-kube-p-kubelet", "namespace": "kube-system", "job": "kubelet"},
			title:             "One or more targets are unreachable.",
			expectedSubject:   "nudgebee-prometheus-kube-p-kubelet",
			expectedNamespace: "kube-system",
		},
		{
			// A bare exporter in the `service` label must NOT become the subject
			// (the scrape-target guard now also covers the service keys).
			name:            "bare exporter service is not a subject",
			initialSubject:  "",
			labels:          map[string]string{"service": "node-exporter"},
			title:           "TargetDown",
			expectedSubject: "",
		},
		{
			// KubeDeploymentReplicasMismatch about the kube-state-metrics
			// deployment itself. The bare `deployment=kube-state-metrics` exporter
			// name is still skipped, but the helm-prefixed pod label names the real
			// workload and resolves deterministically instead of falling to the LLM.
			// Regression test for the substring guard dropping every candidate.
			name:           "KSM deployment alert resolves via prefixed pod",
			initialSubject: "",
			labels: map[string]string{
				"deployment": "kube-state-metrics",
				"pod":        "victoria-kube-state-metrics",
				"service":    "victoria-kube-state-metrics",
				"container":  "kube-state-metrics",
				"namespace":  "victoria",
				"job":        "kube-state-metrics",
			},
			title:             "Deployment has not matched the expected number of replicas.",
			expectedSubject:   "victoria-kube-state-metrics",
			expectedNamespace: "victoria",
		},
		{
			// Guard intact: when every candidate is the bare exporter name, exact
			// match still filters them all and nothing resolves (LLM fallback).
			name:           "bare kube-state-metrics labels are still filtered",
			initialSubject: "",
			labels: map[string]string{
				"deployment": "kube-state-metrics",
				"pod":        "kube-state-metrics",
				"container":  "kube-state-metrics",
				"job":        "kube-state-metrics",
			},
			title:           "KubeStateMetricsListErrors",
			expectedSubject: "",
		},
		{
			// container_id label carries /k8s/<ns>/<pod>/<workload>; the workload
			// (4th segment) is the subject, not the ReplicaSet-hashed pod.
			name:              "container_id k8s path resolves workload",
			initialSubject:    "",
			labels:            map[string]string{"container_id": "/k8s/default/argo-rollouts-54c8dd8467-b2q84/argo-rollouts"},
			title:             "up rule triggered",
			expectedSubject:   "argo-rollouts",
			expectedNamespace: "default",
		},
		{
			// Alertmanager title path /k8s/<ns>/<pod>/<workload> — last resort when
			// no label carries the subject (PagerDuty enrichment race). Workload wins.
			name:              "title k8s path resolves workload not pod",
			initialSubject:    "",
			labels:            map[string]string{},
			title:             "[FIRING:1] ApplicationAPIFailures kubernetes-apps /k8s/demo/load-generator-86b88dd659-z7wrw/load-generator GET /api/cart critical",
			expectedSubject:   "load-generator",
			expectedNamespace: "demo",
		},
		{
			// 3-segment /k8s/<ns>/<workload> title still resolves the workload.
			name:              "title k8s path 3-segment resolves workload",
			initialSubject:    "",
			labels:            map[string]string{},
			title:             "alert /k8s/demo/checkout firing",
			expectedSubject:   "checkout",
			expectedNamespace: "demo",
		},
		{
			// Declared subject from a rendered markdown body.details string
			// ("Subject Name/Namespace/Type"). The pod name + kind are carried
			// through so the downstream pod->owner lookup can resolve them.
			name:           "declared subject_name (pod) resolves with namespace and kind",
			initialSubject: "",
			labels: map[string]string{
				"subject_name":      "flagd-5d6b76f8b8-87njz",
				"subject_namespace": "demo",
				"subject_type":      "pod",
			},
			title:             "Investigate Event - High P95 latency for flagd in demo namespace",
			expectedSubject:   "flagd-5d6b76f8b8-87njz",
			expectedNamespace: "demo",
			expectedKind:      "pod",
		},
		{
			// The explicitly declared subject wins over inferred label keys.
			name:           "declared subject_name wins over job label",
			initialSubject: "",
			labels: map[string]string{
				"subject_name": "checkout",
				"subject_type": "deployment",
				"job":          "kubelet",
			},
			title:           "Investigate Event - something",
			expectedSubject: "checkout",
			expectedKind:    "deployment",
		},
		{
			// A PostgreSQL database-name subject (datname) is a logical database,
			// not a workload: typed as "database" and marked to skip the workload
			// name-match so it isn't pinned onto an unrelated same-named workload.
			name:            "datname resolves as database subject and opts out of workload match",
			initialSubject:  "",
			labels:          map[string]string{"datname": "nudgebee"},
			title:           "PostgreSQLCacheHitRatio",
			expectedSubject: "nudgebee",
			expectedKind:    "database",
			wantSkip:        true,
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
			if tt.expectedKind != "" {
				assert.Equal(t, tt.expectedKind, payload.EventSubjectKind)
			}
			// A non-pod resource subject must not get a fabricated pod label.
			if tt.expectedKind != "" && tt.expectedKind != "pod" {
				assert.Empty(t, payload.Investigation.Labels["pod"],
					"non-pod subject should not fabricate a pod label")
			}
			// A logical-database (datname) subject must opt out of the workload
			// name-match; other subjects must not.
			_, gotSkip := payload.Investigation.Labels[core.SkipWorkloadMatchLabel]
			assert.Equal(t, tt.wantSkip, gotSkip, "skip-workload-match label")
		})
	}
}

// TestApplyStringDetailsSubjectLabels covers mining the declared subject from an
// unstructured markdown body.details string (the AlertSourceUnknown shape PagerDuty
// delivers for rendered alerts that carry no __pd_cef_payload map).
func TestApplyStringDetailsSubjectLabels(t *testing.T) {
	const rawAlertContent = "**Title**: High P95 latency for flagd in demo namespace\n" +
		"**Priority**: MEDIUM\n" +
		"**Aggregation Key**: OtelDemoHighLatency\n" +
		"**Subject Type**: pod\n" +
		"**Subject Name**: flagd-5d6b76f8b8-87njz\n" +
		"**Subject Namespace**: demo\n"

	t.Run("mines subject fields from markdown string", func(t *testing.T) {
		labels := map[string]string{}
		applyStringDetailsSubjectLabels(rawAlertContent, labels)
		assert.Equal(t, "flagd-5d6b76f8b8-87njz", labels["subject_name"])
		assert.Equal(t, "demo", labels["subject_namespace"])
		assert.Equal(t, "pod", labels["subject_type"])
	})

	t.Run("does not overwrite existing labels", func(t *testing.T) {
		labels := map[string]string{"subject_name": "already-set", "subject_namespace": "kept"}
		applyStringDetailsSubjectLabels(rawAlertContent, labels)
		assert.Equal(t, "already-set", labels["subject_name"])
		assert.Equal(t, "kept", labels["subject_namespace"])
		assert.Equal(t, "pod", labels["subject_type"]) // still filled since it was empty
	})

	t.Run("no-op without a Subject Name", func(t *testing.T) {
		labels := map[string]string{}
		applyStringDetailsSubjectLabels("**Priority**: HIGH\nsome free text", labels)
		assert.Empty(t, labels["subject_name"])
		assert.Empty(t, labels["subject_type"])
	})

	t.Run("no-op on empty inputs", func(t *testing.T) {
		labels := map[string]string{}
		applyStringDetailsSubjectLabels("", labels)
		assert.Empty(t, labels)
		applyStringDetailsSubjectLabels(rawAlertContent, nil) // must not panic
	})
}

// cloudOptimizationDetails is the verbatim body.details string PagerDuty returned
// for incident Q03KT7QRZ82X20, the orphaned-EBS-volume alert that fell through to
// the LLM and came back not_found. Produced by getTicketDescription in
// app/src/components/cloudaccount/common.tsx.
const cloudOptimizationDetails = "Recommendation: aws_ec2_orphaned_volume\n" +
	"Service: AmazonEC2\n" +
	"Instance: vol-060625d190b54ab95\n" +
	"Severity: Medium\n" +
	"Estimated Savings: $0.80\n" +
	"Details: Identify unused (unattached) Amazon Elastic Block Store (EBS) volumes available within your AWS cloud account and delete these volumes in order to lower the cost of your AWS bill and reduce the risk of confidential and sensitive data leaks.\n"

// TestApplyCloudOptimizationSubjectLabels covers the deterministic subject mine for
// NudgeBee Cloud Optimization incidents delivered as an unstructured body.details
// string (no __pd_cef_payload map).
func TestApplyCloudOptimizationSubjectLabels(t *testing.T) {
	t.Run("resolves the orphaned-volume subject that previously reached the LLM", func(t *testing.T) {
		labels := map[string]string{}
		applyCloudOptimizationSubjectLabels(cloudOptimizationDetails, labels)
		assert.Equal(t, "vol-060625d190b54ab95", labels["subject_name"])
		assert.Equal(t, "cloud-resource", labels["subject_type"])
		assert.Equal(t, "cloud_optimization_details", labels["nb_subject_match"])
		assert.Equal(t, "cloud-optimization", labels["nb_subject_resolution"])
	})

	t.Run("subject survives resolveSubjectFromLabels without a fabricated pod label", func(t *testing.T) {
		p := &core.EventIncomingWebhook{}
		p.Investigation.Labels = map[string]string{}
		applyCloudOptimizationSubjectLabels(cloudOptimizationDetails, p.Investigation.Labels)
		resolveSubjectFromLabels(p)
		assert.Equal(t, "vol-060625d190b54ab95", p.EventSubjectName)
		assert.Equal(t, "cloud-resource", p.EventSubjectKind)
		// A pod=<volume-id> label would point the pod metric/log enrichers at a pod
		// that does not exist.
		assert.Empty(t, p.Investigation.Labels["pod"])
		// The LLM fallback is keyed off an empty subject; a resolved one skips it.
		assert.NotEqual(t, "unresolved", p.Investigation.Labels["nb_subject_resolution"])
	})

	t.Run("free-text Details prose never supplies the subject", func(t *testing.T) {
		// "Instance:" inside the Details sentence must not be read as the field:
		// extractPlainField is line-anchored.
		labels := map[string]string{}
		applyCloudOptimizationSubjectLabels(
			"Recommendation: aws_rds_idle\nDetails: Check the Instance: i-decoy for details\n", labels)
		assert.Empty(t, labels["subject_name"])
	})

	t.Run("no-op when the recommendation carries no resolvable resource", func(t *testing.T) {
		for _, instance := range []string{"N/A", "n/a", ""} {
			labels := map[string]string{}
			applyCloudOptimizationSubjectLabels(
				"Recommendation: aws_ec2_orphaned_volume\nService: AmazonEC2\nInstance: "+instance+"\n", labels)
			assert.Empty(t, labels["subject_name"], "instance=%q", instance)
		}
	})

	t.Run("no-op without the Recommendation discriminator", func(t *testing.T) {
		labels := map[string]string{}
		applyCloudOptimizationSubjectLabels("Service: AmazonEC2\nInstance: vol-123\n", labels)
		assert.Empty(t, labels["subject_name"])
	})

	t.Run("a declared markdown Subject Name wins", func(t *testing.T) {
		labels := map[string]string{"subject_name": "flagd", "subject_type": "pod"}
		applyCloudOptimizationSubjectLabels(cloudOptimizationDetails, labels)
		assert.Equal(t, "flagd", labels["subject_name"])
		assert.Equal(t, "pod", labels["subject_type"])
		// Nothing else is written either — the body did not describe this subject.
		assert.Empty(t, labels["nb_subject_match"])
	})

	t.Run("no-op on empty inputs", func(t *testing.T) {
		labels := map[string]string{}
		applyCloudOptimizationSubjectLabels("", labels)
		assert.Empty(t, labels)
		applyCloudOptimizationSubjectLabels(cloudOptimizationDetails, nil) // must not panic
	})

	// Every Cloud Optimization rule shape observed in event_incoming_webhooks: an
	// AWS resource id, a slash-qualified name, an EC2 instance id, a synthetic
	// Cost Explorer RI id, and an Azure rule that genuinely has no resource.
	t.Run("observed rule shapes", func(t *testing.T) {
		tests := []struct {
			name        string
			rule        string
			instance    string
			wantSubject string
		}{
			{"orphaned EBS volume", "aws_ec2_orphaned_volume", "vol-060625d190b54ab95", "vol-060625d190b54ab95"},
			{"alternate instances (slash-qualified name)", "aws_ec2_alternate_instances", "log-cache/bc91f6b1-f348-43e3-8d5d-57502ec19b65", "log-cache/bc91f6b1-f348-43e3-8d5d-57502ec19b65"},
			{"underutilized EC2 instance", "aws_ec2_underutilized", "i-0a37a5e93a6f665fa", "i-0a37a5e93a6f665fa"},
			{"cost-explorer RI recommendation", "aws_native_ce_ri_recommendation", "ce-ri-amazon-elastic-compute-cloud---compute-1", "ce-ri-amazon-elastic-compute-cloud---compute-1"},
			{"azure unattached disk carries no resource", "azure_disk_unattached_volume", "N/A", ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				labels := map[string]string{}
				applyCloudOptimizationSubjectLabels(
					"Recommendation: "+tt.rule+"\nService: AmazonEC2\nInstance: "+tt.instance+"\nSeverity: Medium\n", labels)
				assert.Equal(t, tt.wantSubject, labels["subject_name"])
			})
		}
	})
}

// TestExtractGCPMonitoringSubject covers deterministic parsing of GCP Cloud
// Monitoring alert titles routed through PagerDuty (Cloud SQL, GCE, replica,
// with/without the metric-labels datasource hint, and non-GCP titles).
func TestExtractGCPMonitoringSubject(t *testing.T) {
	const viewIncident = " | View incident: https://console.cloud.google.com/monitoring/alerting/alerts/0.abc?channelType=pagerduty&project=example-gcp-proj"
	tests := []struct {
		name         string
		title        string
		wantResource string
		wantProject  string
		wantKind     string
	}{
		{
			name:         "cloudsql slow-queries (log-based metric) sets cloudsql_database kind",
			title:        "logging/user/dev-pg-slow-queries for example-gcp-proj example-pg-instance with metric labels {log=cloudsql.googleapis.com/postgres.log} is above the threshold of 5.000 with a value of 6.000." + viewIncident,
			wantResource: "example-pg-instance",
			wantProject:  "example-gcp-proj",
			wantKind:     "cloudsql_database",
		},
		{
			name:         "CPU utilization (no datasource hint) resolves resource, kind empty",
			title:        "CPU utilization for example-gcp-proj example-pg-instance is above the threshold of 0.700 with a value of 0.712." + viewIncident,
			wantResource: "example-pg-instance",
			wantProject:  "example-gcp-proj",
			wantKind:     "",
		},
		{
			name:         "Uptime below-threshold resolves the replica resource",
			title:        "Uptime for example-gcp-proj example-pg-replica is below the threshold of 1 with a value of 0." + viewIncident,
			wantResource: "example-pg-replica",
			wantProject:  "example-gcp-proj",
			wantKind:     "",
		},
		{
			name:         "metric absence alert resolves resource, kind empty",
			title:        "Scheduler Heartbeats for example-gcp-proj example-pg-instance is absent." + viewIncident,
			wantResource: "example-pg-instance",
			wantProject:  "example-gcp-proj",
			wantKind:     "",
		},
		{
			name:         "project falls back to leading for-clause token when URL lacks it",
			title:        "CPU utilization for my-proj my-instance is above the threshold of 0.9 with a value of 0.95. | Policy: p | View incident: https://console.cloud.google.com/monitoring/alerting/alerts/0.abc",
			wantResource: "my-instance",
			wantProject:  "my-proj",
			wantKind:     "",
		},
		{
			name:         "non-GCP title returns empty",
			title:        "[FIRING:1] HighErrorCriticalLogs custom-alerts nudgebee-agent critical",
			wantResource: "",
			wantProject:  "",
			wantKind:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, project, kind := extractGCPMonitoringSubject(tt.title)
			assert.Equal(t, tt.wantResource, resource, "resource")
			assert.Equal(t, tt.wantProject, project, "project")
			assert.Equal(t, tt.wantKind, kind, "kind")
		})
	}
}

// TestApplyGCPMonitoringSubject covers the end-to-end apply: subject + kind set,
// GCP project mirrored into the cluster label (ingest guard needs a cluster), and
// no-op when a subject already exists.
func TestApplyGCPMonitoringSubject(t *testing.T) {
	t.Run("cloudsql alert resolves subject, kind and cluster", func(t *testing.T) {
		p := &core.EventIncomingWebhook{
			EventTitle: "logging/user/dev-pg-slow-queries for example-gcp-proj example-pg-instance with metric labels {log=cloudsql.googleapis.com/postgres.log} is above the threshold of 5.000 with a value of 6.000. | View incident: https://console.cloud.google.com/monitoring/alerting/alerts/0.abc?channelType=pagerduty&project=example-gcp-proj",
			Investigation: core.EventIncomingWebhookInvestigation{
				Labels: map[string]string{},
			},
		}
		applyGCPMonitoringSubject(p)
		assert.Equal(t, "example-pg-instance", p.EventSubjectName)
		assert.Equal(t, "cloudsql_database", p.EventSubjectKind)
		assert.Equal(t, "example-gcp-proj", p.Investigation.Labels["cluster"])
		assert.Equal(t, "example-gcp-proj", p.Investigation.Labels["project_id"])
		assert.Equal(t, "gcp_monitoring_title", p.Investigation.Labels["nb_subject_match"])
	})

	t.Run("existing subject is left untouched", func(t *testing.T) {
		p := &core.EventIncomingWebhook{
			EventSubjectName: "checkout",
			EventSubjectKind: "deployment",
			EventTitle:       "CPU utilization for example-gcp-proj example-pg-instance is above the threshold of 0.7 with a value of 0.8. | View incident: https://console.cloud.google.com/monitoring/alerting/alerts/0.abc?project=example-gcp-proj",
			Investigation:    core.EventIncomingWebhookInvestigation{Labels: map[string]string{}},
		}
		applyGCPMonitoringSubject(p)
		assert.Equal(t, "checkout", p.EventSubjectName)
		assert.Equal(t, "deployment", p.EventSubjectKind)
		assert.Empty(t, p.Investigation.Labels["cluster"])
	})

	t.Run("preserves an existing cluster label", func(t *testing.T) {
		p := &core.EventIncomingWebhook{
			EventTitle: "CPU utilization for example-gcp-proj example-pg-instance is above the threshold of 0.7 with a value of 0.8. | View incident: https://console.cloud.google.com/monitoring/alerting/alerts/0.abc?project=example-gcp-proj",
			Investigation: core.EventIncomingWebhookInvestigation{
				Labels: map[string]string{"cluster": "pre-existing"},
			},
		}
		applyGCPMonitoringSubject(p)
		assert.Equal(t, "example-pg-instance", p.EventSubjectName)
		assert.Equal(t, "pre-existing", p.Investigation.Labels["cluster"])
	})

	t.Run("nil label map is initialized so cluster is set (ingest guard needs it)", func(t *testing.T) {
		p := &core.EventIncomingWebhook{
			EventTitle:    "CPU utilization for example-gcp-proj example-pg-instance is above the threshold of 0.7 with a value of 0.8. | View incident: https://console.cloud.google.com/monitoring/alerting/alerts/0.abc?project=example-gcp-proj",
			Investigation: core.EventIncomingWebhookInvestigation{Labels: nil},
		}
		applyGCPMonitoringSubject(p)
		assert.Equal(t, "example-pg-instance", p.EventSubjectName)
		assert.NotNil(t, p.Investigation.Labels)
		assert.Equal(t, "example-gcp-proj", p.Investigation.Labels["cluster"])
	})
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
			SourceUrl: "https://example.chronosphere.io/monitors/critical-staging-aws-inf-sqs-visible-msg",
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
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
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

func TestResolveEventTitleFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   "",
		},
		{
			name:   "no usable labels",
			labels: map[string]string{"severity": "critical"},
			want:   "",
		},
		{
			name:   "alertname only",
			labels: map[string]string{"alertname": "ApplicationAPIFailures"},
			want:   "ApplicationAPIFailures",
		},
		{
			// Regression: Grafana/Alertmanager firing text keys the summary as a plain
			// "summary" label (annotation_summary is rarely populated). It must win over
			// alertname so the title is the readable summary, not the alert rule id.
			name: "plain summary label wins over alertname",
			labels: map[string]string{
				"alertname": "ApplicationAPIFailures",
				"summary":   "High 5xx rate - POST /request",
			},
			want: "High 5xx rate - POST /request",
		},
		{
			name: "annotation_summary wins over everything",
			labels: map[string]string{
				"alertname":          "ApplicationAPIFailures",
				"summary":            "High 5xx rate - POST /request",
				"annotation_summary": "Service degraded: 11% of requests failing",
			},
			want: "Service degraded: 11% of requests failing",
		},
		{
			name: "empty summary falls through to alertname",
			labels: map[string]string{
				"alertname": "ApplicationAPIFailures",
				"summary":   "",
			},
			want: "ApplicationAPIFailures",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveEventTitleFromLabels(tt.labels))
		})
	}
}
