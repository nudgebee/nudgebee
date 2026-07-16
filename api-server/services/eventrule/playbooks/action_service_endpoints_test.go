package playbooks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildServiceEndpointsReport covers the three live states the enricher
// distinguishes: still broken (Critical), recovered (Info), and
// selector-now-matches-but-zero-addresses (High — scale-to-zero / not-ready).
func TestBuildServiceEndpointsReport(t *testing.T) {
	workloads := []workloadTemplate{
		{name: "web", labels: map[string]string{"app": "web"}},
		{name: "worker", labels: map[string]string{"app": "worker"}},
	}

	t.Run("still broken -> Critical insight + comparison table", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport(
			"demo", "web-svc", map[string]string{"app": "wrong-label"}, 0, workloads)
		assert.Contains(t, text, "0 addresses — still not routing traffic")
		assert.Contains(t, text, "app=wrong-label")
		assert.Contains(t, text, "| web | `app=web` | ✗ |")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "Critical", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "matches none of the 2 workload pod template(s)")
		}
	})

	t.Run("recovered -> Info insight", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport(
			"demo", "web-svc", map[string]string{"app": "web"}, 3, workloads)
		assert.Contains(t, text, "3 address(es) — appears recovered")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "Info", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "appears resolved")
		}
	})

	t.Run("selector matches template but zero addresses -> High insight", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport(
			"demo", "web-svc", map[string]string{"app": "web"}, 0, workloads)
		assert.Contains(t, text, "| web | `app=web` | ✓ |")
		if assert.Len(t, insights, 1) {
			assert.Equal(t, "High", insights[0].Severity)
			assert.Contains(t, insights[0].Message, "scaled to zero")
		}
	})

	t.Run("selector removed -> noted, no insight", func(t *testing.T) {
		text, insights := buildServiceEndpointsReport("demo", "web-svc", nil, 0, workloads)
		assert.Contains(t, text, "selector was removed")
		assert.Empty(t, insights)
	})
}

// TestServiceEndpointsResponseShape locks the wire contract the frontend
// depends on: type=markdown with a top-level data string, and
// actual_action_name=text_enricher so TextEnricherDynamicCard renders it
// without any new frontend registration.
func TestServiceEndpointsResponseShape(t *testing.T) {
	resp := serviceEndpointsMarkdownResponse{
		Title:          "Service endpoint status (live)",
		Data:           "**Live check**",
		AdditionalInfo: map[string]any{"actual_action_name": "text_enricher"},
		Insight:        []PlaybookActionResponseInsight{{Message: "m", Severity: "Critical"}},
	}
	assert.Equal(t, "markdown", resp.GetFormatName())
	assert.Equal(t, "**Live check**", resp.GetData())
	assert.Equal(t, "text_enricher", resp.GetAdditionalInfo()["actual_action_name"])
}

func TestWorkloadTemplateHelpers(t *testing.T) {
	list := []any{
		map[string]any{
			"metadata": map[string]any{"name": "web"},
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				},
			},
		},
		map[string]any{"metadata": map[string]any{}}, // nameless — skipped
	}
	ws := workloadTemplatesFromList(list)
	if assert.Len(t, ws, 1) {
		assert.Equal(t, "web", ws[0].name)
		assert.Equal(t, map[string]string{"app": "web"}, ws[0].labels)
	}

	assert.True(t, labelsContain(map[string]string{"app": "web", "tier": "fe"}, map[string]string{"app": "web"}))
	assert.False(t, labelsContain(map[string]string{"app": "web"}, map[string]string{"app": "other"}))
	assert.False(t, labelsContain(map[string]string{"app": "web"}, nil))
	// Empty-string selector value must not match a MISSING label key.
	assert.False(t, labelsContain(map[string]string{"app": "web"}, map[string]string{"foo": ""}))
	assert.True(t, labelsContain(map[string]string{"foo": ""}, map[string]string{"foo": ""}))

	ep := map[string]any{"subsets": []any{
		map[string]any{"addresses": []any{map[string]any{"ip": "10.0.0.1"}, map[string]any{"ip": "10.0.0.2"}}},
		map[string]any{}, // no addresses
	}}
	assert.Equal(t, 2, endpointsAddressCount(ep))

	// Selector with dotted user keys survives (snake conversion skips them).
	svc := map[string]any{"spec": map[string]any{"selector": map[string]any{"app.kubernetes.io/name": "web"}}}
	sel := serviceSelectorFromDict(svc)
	assert.Equal(t, "web", sel["app.kubernetes.io/name"])
	assert.True(t, strings.Contains(formatLabelMap(sel), "app.kubernetes.io/name=web"))
}
