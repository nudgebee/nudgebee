package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const prometheusAlertManagerWebhookPayload = `{
	"receiver": "serices-server",
	"status": "firing",
	"alerts": [
	  {
		"status": "firing",
		"labels": {
		  "alertgroup": "kubernetes-resources",
		  "alertname": "CPUThrottlingHigh",
		  "cluster": "cluster-name",
		  "container": "alertmanager",
		  "namespace": "victoria",
		  "pod": "vmalertmanager-victoria-victoria-metrics-k8s-stack-0",
		  "severity": "info"
		},
		"annotations": {
		  "description": "55.97% throttling of CPU in namespace victoria for container alertmanager in pod vmalertmanager-victoria-victoria-metrics-k8s-stack-0.",
		  "runbook_url": "https://runbooks.prometheus-operator.dev/runbooks/kubernetes/cputhrottlinghigh",
		  "summary": "Processes experience elevated CPU throttling."
		},
		"startsAt": "2025-04-14T15:24:15Z",
		"endsAt": "0001-01-01T00:00:00Z",
		"generatorURL": "http://vmalert-victoria-victoria-metrics-k8s-stack-78497f95cf-xsg6v:8080/vmalert/alert?group_id=16761697894879140610&alert_id=1493036818384931702",
		"fingerprint": "1e6df20d7ad22b17"
	  }
	],
	"groupLabels": {
	  "alertgroup": "kubernetes-resources",
	  "alertname": "CPUThrottlingHigh",
	  "cluster": "cluster-name",
	  "container": "alertmanager",
	  "namespace": "victoria",
	  "pod": "vmalertmanager-victoria-victoria-metrics-k8s-stack-0",
	  "severity": "info"
	},
	"commonLabels": {
	  "alertgroup": "kubernetes-resources",
	  "alertname": "CPUThrottlingHigh",
	  "cluster": "cluster-name",
	  "container": "alertmanager",
	  "namespace": "victoria",
	  "pod": "vmalertmanager-victoria-victoria-metrics-k8s-stack-0",
	  "severity": "info"
	},
	"commonAnnotations": {
	  "description": "55.97% throttling of CPU in namespace victoria for container alertmanager in pod vmalertmanager-victoria-victoria-metrics-k8s-stack-0.",
	  "runbook_url": "https://runbooks.prometheus-operator.dev/runbooks/kubernetes/cputhrottlinghigh",
	  "summary": "Processes experience elevated CPU throttling."
	},
	"externalURL": "http://vmalertmanager-victoria-victoria-metrics-k8s-stack-0:9093",
	"version": "4",
	"groupKey": "{}/{severity=~\".*\"}:{alertgroup=\"kubernetes-resources\", alertname=\"CPUThrottlingHigh\", cluster=\"cluster-name\", container=\"alertmanager\", namespace=\"victoria\", pod=\"vmalertmanager-victoria-victoria-metrics-k8s-stack-0\", severity=\"info\"}",
	"truncatedAlerts": 0
  }
  `
const prometheusAlertManagerWebhookResolvedPayload = ``

func TestTools_ParsePrometheusAlertManagerWebhookPayloadResolved(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	userId := os.Getenv("TEST_USER")

	prometheusAlertManagerWebhookIntegration, _ := core.GetIntegration(IntegrationPrometheusAlertManagerWebhook)
	assert.NotNil(t, prometheusAlertManagerWebhookIntegration)
	prometheusAlertManagerWebhookIntgeration, _ := prometheusAlertManagerWebhookIntegration.(PrometheusAlertManagerWebhook)
	assert.NotNil(t, prometheusAlertManagerWebhookIntgeration)

	eventData, err := prometheusAlertManagerWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), prometheusAlertManagerWebhookResolvedPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

func TestTools_ParsePrometheusAlertManagerWebhookPayload(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	prometheusAlertManagerWebhookIntegration, _ := core.GetIntegration(IntegrationPrometheusAlertManagerWebhook)
	assert.NotNil(t, prometheusAlertManagerWebhookIntegration)
	prometheusAlertManagerWebhookIntgeration, _ := prometheusAlertManagerWebhookIntegration.(PrometheusAlertManagerWebhook)
	assert.NotNil(t, prometheusAlertManagerWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := prometheusAlertManagerWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), prometheusAlertManagerWebhookPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
	// runbook_url arrives as an Alertmanager annotation; it must be surfaced into
	// Investigation.Labels so the alert-rule evidence card renders the runbook link.
	assert.Equal(t, "https://runbooks.prometheus-operator.dev/runbooks/kubernetes/cputhrottlinghigh", eventData[0].Investigation.Labels["runbook_url"])
	eventData, err = prometheusAlertManagerWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), prometheusAlertManagerWebhookResolvedPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

func TestTools_GetCreatePrometheusAlertManagerWebhookToolConfigs(t *testing.T) {
	testenv.RequireEnv(t, testenv.User, testenv.Tenant, testenv.Account)
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil)
	toolConfigName := "nudgebee-gke-webhook"

	err := core.DeleteIntegrationConfig(sc, IntegrationPrometheusAlertManagerWebhook, toolConfigName, "")
	assert.Nil(t, err)

	config, err := core.CreateIntegrationConfig(sc, "", IntegrationPrometheusAlertManagerWebhook, toolConfigName, []core.IntegrationConfigValue{
		{
			Name:  "token",
			Value: "EXAMPLE_PROMETHEUS_ALERTMANAGER_WEBHOOK_TOKEN",
		},
	},
		map[string]any{
			"env": "dev",
		}, []string{accountId}, false, "",
	)

	assert.Nil(t, err)
	assert.NotEmpty(t, config.Name)

	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationPrometheusAlertManagerWebhook)
	assert.Nil(t, err)
	assert.NotEmpty(t, configs)

	err = core.ProcessEventWebook(sc, "https://app.nudgebee.com/api/webhooks/prometheus-alertmanager?token=EXAMPLE_PROMETHEUS_ALERTMANAGER_WEBHOOK_TOKEN", map[string]string{}, prometheusAlertManagerWebhookPayload)
	assert.Nil(t, err)

}

func TestExtractPromSubject(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		wantKind string
		wantName string
	}{
		{
			name:     "datname only falls back to database name",
			labels:   map[string]string{"alertname": "PostgreSQLCacheHitRatio", "datname": "temporal_visibility", "severity": "warning"},
			wantKind: "database",
			wantName: "temporal_visibility",
		},
		{
			name:     "rdsadmin datname falls back to database name (not a workload)",
			labels:   map[string]string{"alertname": "PostgreSQLCacheHitRatio", "datname": "rdsadmin"},
			wantKind: "database",
			wantName: "rdsadmin",
		},
		{
			name:     "server host (RDS endpoint) preferred over datname, port stripped",
			labels:   map[string]string{"datname": "temporal_visibility", "server": "mydb.abc123.us-east-1.rds.amazonaws.com:5432"},
			wantKind: "database",
			wantName: "mydb.abc123.us-east-1.rds.amazonaws.com",
		},
		{
			name:     "instance host preferred over datname, port stripped",
			labels:   map[string]string{"datname": "temporal", "instance": "10.0.0.1:9187"},
			wantKind: "database",
			wantName: "10.0.0.1",
		},
		{
			name:     "loopback server skipped, falls through to real instance host",
			labels:   map[string]string{"datname": "temporal", "server": "localhost:5432", "instance": "postgres-0:9187"},
			wantKind: "database",
			wantName: "postgres-0",
		},
		{
			name:     "controller wins over datname/host",
			labels:   map[string]string{"deployment": "postgres", "datname": "temporal_visibility", "instance": "10.0.0.1:9187"},
			wantKind: "deployment",
			wantName: "postgres",
		},
		{
			name:     "pod wins over datname/host",
			labels:   map[string]string{"pod": "postgres-0", "datname": "temporal", "server": "10.0.0.1:5432"},
			wantKind: "pod",
			wantName: "postgres-0",
		},
		{
			name:     "app_id resolves workload when no k8s controller/pod labels present",
			labels:   map[string]string{"alertname": "HighErrorCriticalLogs", "app_id": "/k8s/prod/checkout-api", "namespace": "shop"},
			wantKind: "workload",
			wantName: "checkout-api",
		},
		{
			name:     "controller wins over app_id",
			labels:   map[string]string{"deployment": "checkout", "app_id": "/k8s/prod/checkout-api"},
			wantKind: "deployment",
			wantName: "checkout",
		},
		{
			name:     "app_id wins over service and pod",
			labels:   map[string]string{"app_id": "/k8s/prod/checkout-api", "service": "checkout-svc", "pod": "checkout-api-abc123"},
			wantKind: "workload",
			wantName: "checkout-api",
		},
		{
			name:     "malformed app_id ignored, falls through to pod",
			labels:   map[string]string{"app_id": "checkout-api", "pod": "checkout-api-abc123"},
			wantKind: "pod",
			wantName: "checkout-api-abc123",
		},
		{
			name:     "persistentvolumeclaim wins over instance/node host and job scraper (PV filling up)",
			labels:   map[string]string{"alertname": "KubePersistentVolumeFillingUp", "persistentvolumeclaim": "data-opensearch-0", "namespace": "search", "instance": "node-pool-abc:10250", "job": "kubelet"},
			wantKind: "persistentvolumeclaim",
			wantName: "data-opensearch-0",
		},
		{
			name:     "persistentvolumeclaim wins over service and pod",
			labels:   map[string]string{"persistentvolumeclaim": "data-db-0", "service": "kubelet", "pod": "db-0"},
			wantKind: "persistentvolumeclaim",
			wantName: "data-db-0",
		},
		{
			name:     "hpa resolves to horizontalpodautoscaler",
			labels:   map[string]string{"hpa": "checkout-hpa", "instance": "1.2.3.4:9090"},
			wantKind: "horizontalpodautoscaler",
			wantName: "checkout-hpa",
		},
		{
			name:     "job_name resolves to job (not the prometheus scrape job label)",
			labels:   map[string]string{"job_name": "nightly-backup", "job": "kubelet"},
			wantKind: "job",
			wantName: "nightly-backup",
		},
		{
			name:     "controller wins over persistentvolumeclaim",
			labels:   map[string]string{"statefulset": "opensearch", "persistentvolumeclaim": "data-opensearch-0"},
			wantKind: "statefulset",
			wantName: "opensearch",
		},
		{
			name:     "no subject keys",
			labels:   map[string]string{"alertname": "SomethingElse", "severity": "warning"},
			wantKind: "",
			wantName: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, name := extractPromSubject(tt.labels)
			assert.Equal(t, tt.wantKind, kind, "kind")
			assert.Equal(t, tt.wantName, name, "name")
		})
	}
}

func TestExtractAppIDWorkload(t *testing.T) {
	tests := []struct {
		name  string
		appID string
		want  string
	}{
		{name: "standard /k8s/{ns}/{workload}", appID: "/k8s/prod/checkout-api", want: "checkout-api"},
		{name: "app-group middle segment (not a namespace)", appID: "/k8s/newrelic/newrelic-bundle-nri-kube-events", want: "newrelic-bundle-nri-kube-events"},
		{name: "empty", appID: "", want: ""},
		{name: "not a k8s path", appID: "checkout-api", want: ""},
		{name: "wrong prefix", appID: "/vm/prod/checkout-api", want: ""},
		{name: "too few segments", appID: "/k8s/prod", want: ""},
		{name: "too many segments (pod path handled elsewhere)", appID: "/k8s/prod/pod-x/checkout-api", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractAppIDWorkload(tt.appID), "workload")
		})
	}
}
