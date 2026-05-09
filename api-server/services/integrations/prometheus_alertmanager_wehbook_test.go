package integrations

import (
	"nudgebee/services/integrations/core"
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
	prometheusAlertManagerWebhookIntegration, _ := core.GetIntegration(IntegrationPrometheusAlertManagerWebhook)
	assert.NotNil(t, prometheusAlertManagerWebhookIntegration)
	prometheusAlertManagerWebhookIntgeration, _ := prometheusAlertManagerWebhookIntegration.(PrometheusAlertManagerWebhook)
	assert.NotNil(t, prometheusAlertManagerWebhookIntgeration)

	userId := os.Getenv("TEST_USER")
	eventData, err := prometheusAlertManagerWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), prometheusAlertManagerWebhookPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
	eventData, err = prometheusAlertManagerWebhookIntgeration.ProcessEventWebook(security.NewRequestContextForUserTenant(userId, os.Getenv("TEST_TENANT"), nil, nil, nil), []core.IntegrationConfigValue{}, os.Getenv("TEST_ACCOUNT"), prometheusAlertManagerWebhookResolvedPayload)
	assert.Nil(t, err)
	assert.NotEmpty(t, eventData)
}

func TestTools_GetCreatePrometheusAlertManagerWebhookToolConfigs(t *testing.T) {
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
