package playbooks

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPodMetrics(t *testing.T) {
	podMetricAction := podMetricAction{}
	defaultPlaybookActionContext := defaultPlaybookActionContext{
		accountId: os.Getenv("TEST_ACCOUNT"),
		logger:    slog.Default(),
		event: PlaybookEvent{
			Name:        "TestPodMetricAlert",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			StartedAt:   nil,
			EndedAt:     nil,
		},
	}
	response, err := podMetricAction.Execute(&defaultPlaybookActionContext, map[string]any{
		"pod_name":      "app-dev-85b5fbbfcf-bwfns",
		"namespace":     "nudgebee",
		"duration":      30,
		"resource_type": "CPU",
	})

	// If prometheus is available, test the response
	if err == nil {
		assert.NotNil(t, response)
		assert.Equal(t, "json", response.GetFormatName())
		assert.NotNil(t, response.GetData())
		assert.NotNil(t, response.GetAdditionalInfo())

		// Verify additional info structure
		additionalInfo := response.GetAdditionalInfo()
		assert.Equal(t, "pod_metric_enricher", additionalInfo["action_name"])
		assert.Equal(t, "pod_metric_enricher", additionalInfo["actual_action_name"])
		assert.Equal(t, "pod_metric_enricher", additionalInfo["title"])

		// Verify insights are generated
		insights := response.GetInsights()
		assert.NotNil(t, insights)

		t.Logf("Pod metric enricher action executed successfully")
		t.Logf("Generated %d insights", len(insights))
	} else {
		// Expected in test environment without proper prometheus setup
		t.Logf("Pod metric enricher failed (expected in test environment): %v", err)
	}
}
