package insight

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInsight(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	insights, err := GetInsights(ctxt, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, insights)
	b, _ := json.MarshalIndent(insights, "", "  ")
	fmt.Println(string(b))
}

func TestInsightForCloudAccount(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	insights, err := GetInsights(ctxt, os.Getenv("TEST_CLOUD_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, insights)
}

func TestProcessRuleForAccount(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)
	err := Process(ctxt, os.Getenv("TEST_CLOUD_ACCOUNT"))
	assert.Nil(t, err)
}

func TestProcessRule(t *testing.T) {
	ctxt := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)
	err := Process(ctxt)
	assert.Nil(t, err)
}

func TestPrometheusInstantRule(t *testing.T) {

	insightRule := InsightRule{
		UniqueID:           "18",
		InsightFormat:      "{} PV(s) Getting Full (<15% space)",
		Type:               InsightTypePrometheus,
		Source:             InsightSourcePrometheus,
		InsightCategory:    "",
		InsightSubCategory: "",
		InsightUIFilters:   []InsightUIFilters{},
		Threshold:          0.15,
		Range:              -1,
		GroupedBy:          []string{"namespace", "persistentvolumeclaim"},
		Filters:            []InsightFilters{},
		Query:              "(   kubelet_volume_stats_available_bytes{job=\"kubelet\", namespace=~\".*\", metrics_path=\"/metrics\"}     /   kubelet_volume_stats_capacity_bytes{job=\"kubelet\", namespace=~\".*\", metrics_path=\"/metrics\"} ) < 0.90 and kubelet_volume_stats_used_bytes{job=\"kubelet\", namespace=~\".*\", metrics_path=\"/metrics\"} > 0 and predict_linear(   kubelet_volume_stats_available_bytes{job=\"kubelet\", namespace=~\".*\", metrics_path=\"/metrics\"}[6h],   90 * 24 * 3600 ) < 0 unless on (cluster, namespace, persistentvolumeclaim)   kube_persistentvolumeclaim_access_mode{access_mode=\"ReadOnlyMany\"} == 1 unless on (cluster, namespace, persistentvolumeclaim)   kube_persistentvolumeclaim_labels{label_excluded_from_alerts=\"true\"} == 1",
	}
	insight, err := ProcessPrometheusInstantRule(insightRule, os.Getenv("TEST_ACCOUNT"))
	assert.NotNil(t, insight)
	assert.Nil(t, err)

}
