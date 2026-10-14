package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// computeBlastRadius — pure logic, no I/O
// ---------------------------------------------------------------------------

func TestComputeBlastRadius_Empty(t *testing.T) {
	score, level := computeBlastRadius(nil)
	assert.Equal(t, 0, score)
	assert.Equal(t, riskLow, level)
}

func TestComputeBlastRadius_FewLightNodes(t *testing.T) {
	nodes := makeNodes(5, "Deployment")
	score, level := computeBlastRadius(nodes)
	// 5 * 2 = 10 base, 0 heavy bonus → score 10
	assert.Equal(t, 10, score)
	assert.Equal(t, riskLow, level)
}

func TestComputeBlastRadius_MediumRange(t *testing.T) {
	// 13 light nodes → 26 base → medium
	nodes := makeNodes(13, "Service")
	score, level := computeBlastRadius(nodes)
	assert.Equal(t, 26, score)
	assert.Equal(t, riskMedium, level)
}

func TestComputeBlastRadius_HeavyNodesAddBonus(t *testing.T) {
	// 2 light nodes (4 base) + 1 Redis (+10) = 14 → low
	nodes := append(makeNodes(2, "Deployment"), makeNodes(1, "Redis")...)
	score, level := computeBlastRadius(nodes)
	assert.Equal(t, 14, score)
	assert.Equal(t, riskLow, level)
}

func TestComputeBlastRadius_HighRisk(t *testing.T) {
	// 25 light nodes → 50 base → high
	nodes := makeNodes(25, "Service")
	score, level := computeBlastRadius(nodes)
	assert.Equal(t, 50, score)
	assert.Equal(t, riskHigh, level)
}

func TestComputeBlastRadius_Critical(t *testing.T) {
	// 25 light (50 base) + 3 Redis (30 bonus, capped to 25 for total cap) → 100 critical
	nodes := append(makeNodes(25, "Deployment"), makeNodes(3, "Redis")...)
	score, level := computeBlastRadius(nodes)
	assert.Equal(t, 100, score)
	assert.Equal(t, riskCritical, level)
}

func TestComputeBlastRadius_BaseScoreCappedAt50(t *testing.T) {
	// 100 light nodes → base would be 200, capped to 50
	nodes := makeNodes(100, "Pod")
	score, _ := computeBlastRadius(nodes)
	assert.LessOrEqual(t, score, 100)
}

func TestComputeBlastRadius_HeavyBonusCappedAt50(t *testing.T) {
	// 10 Redis nodes → 100 bonus, capped to 50 → total capped at 100
	nodes := makeNodes(10, "Redis")
	score, level := computeBlastRadius(nodes)
	assert.Equal(t, 100, score)
	assert.Equal(t, riskCritical, level)
}

// ---------------------------------------------------------------------------
// splitVerdictAndRecommendation
// ---------------------------------------------------------------------------

func TestSplitVerdictAndRecommendation_BothPresent(t *testing.T) {
	raw := "VERDICT: This is risky.\nRECOMMENDATION: Deploy during off-hours."
	verdict, rec := splitVerdictAndRecommendation(raw)
	assert.Equal(t, "This is risky.", verdict)
	assert.Equal(t, "Deploy during off-hours.", rec)
}

func TestSplitVerdictAndRecommendation_VerdictOnly(t *testing.T) {
	raw := "VERDICT: Something might break."
	verdict, rec := splitVerdictAndRecommendation(raw)
	assert.Equal(t, "Something might break.", verdict)
	assert.Empty(t, rec)
}

func TestSplitVerdictAndRecommendation_FallbackToFull(t *testing.T) {
	raw := "Plain response with no labels."
	verdict, rec := splitVerdictAndRecommendation(raw)
	assert.Equal(t, raw, verdict)
	assert.Empty(t, rec)
}

// ---------------------------------------------------------------------------
// parseModelParamString
// ---------------------------------------------------------------------------

func TestParseModelParamString_Empty(t *testing.T) {
	p, m := parseModelParamString("")
	assert.Empty(t, p)
	assert.Empty(t, m)
}

func TestParseModelParamString_ProviderAndModel(t *testing.T) {
	p, m := parseModelParamString("openai:gpt-4o")
	assert.Equal(t, "openai", p)
	assert.Equal(t, "gpt-4o", m)
}

func TestParseModelParamString_ModelOnly(t *testing.T) {
	p, m := parseModelParamString("claude-3-5-sonnet")
	assert.Empty(t, p)
	assert.Equal(t, "claude-3-5-sonnet", m)
}

// ---------------------------------------------------------------------------
// buildNodeSummary
// ---------------------------------------------------------------------------

func TestBuildNodeSummary_Empty(t *testing.T) {
	out := buildNodeSummary(nil)
	assert.Contains(t, out, "(none)")
}

func TestBuildNodeSummary_WithNamespace(t *testing.T) {
	nodes := []map[string]any{
		{"name": "checkout-db", "kind": "PostgreSQL", "namespace": "prod"},
	}
	out := buildNodeSummary(nodes)
	assert.Contains(t, out, "checkout-db")
	assert.Contains(t, out, "PostgreSQL")
	assert.Contains(t, out, "prod")
}

func TestBuildNodeSummary_TruncatesAt20(t *testing.T) {
	nodes := makeNodes(25, "Service")
	out := buildNodeSummary(nodes)
	assert.Contains(t, out, "5 more")
}

// ---------------------------------------------------------------------------
// BlastRadiusPreflightTask — metadata validation
// ---------------------------------------------------------------------------

func TestBlastRadiusPreflightTask_Metadata(t *testing.T) {
	task := &BlastRadiusPreflightTask{}
	assert.Equal(t, taskNameBlastRadiusPreflight, task.GetName())
	assert.NotEmpty(t, task.GetDisplayName())
	assert.NotEmpty(t, task.GetDescription())

	notes := task.RuntimeNotes()
	require.NotEmpty(t, notes)
}

func TestBlastRadiusPreflightTask_InputSchema(t *testing.T) {
	task := &BlastRadiusPreflightTask{}
	schema := task.InputSchema()
	require.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "resource_name")
	assert.True(t, schema.Properties["resource_name"].Required)
	assert.Contains(t, schema.Properties, "on_high_risk")
	assert.Contains(t, schema.Properties, "risk_threshold")
	assert.Contains(t, schema.Properties, "include_llm_verdict")
}

func TestBlastRadiusPreflightTask_OutputSchema(t *testing.T) {
	task := &BlastRadiusPreflightTask{}
	schema := task.OutputSchema()
	require.NotNil(t, schema)
	for _, key := range []string{"risk_score", "risk_level", "affected_node_count", "affected_nodes", "is_blocked"} {
		assert.Contains(t, schema.Properties, key, "missing output property: %s", key)
	}
}

func TestBlastRadiusPreflightTask_SchemaValidation_MissingRequired(t *testing.T) {
	task := &BlastRadiusPreflightTask{}
	err := task.InputSchema().Validate(map[string]any{})
	assert.Error(t, err, "expected validation error for missing resource_name")
}

func TestBlastRadiusPreflightTask_SchemaValidation_ValidMinimal(t *testing.T) {
	task := &BlastRadiusPreflightTask{}
	err := task.InputSchema().Validate(map[string]any{
		"resource_name": "nginx",
	})
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// riskOrder ordering invariant
// ---------------------------------------------------------------------------

func TestRiskOrderIsMonotonic(t *testing.T) {
	assert.Less(t, riskOrder[riskLow], riskOrder[riskMedium])
	assert.Less(t, riskOrder[riskMedium], riskOrder[riskHigh])
	assert.Less(t, riskOrder[riskHigh], riskOrder[riskCritical])
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// makeNodes builds a slice of n fake nodes with the given kind.
func makeNodes(n int, kind string) []map[string]any {
	nodes := make([]map[string]any, n)
	for i := range nodes {
		nodes[i] = map[string]any{
			"id":   fmt.Sprintf("node-%d", i),
			"name": fmt.Sprintf("svc-%d", i),
			"kind": kind,
		}
	}
	return nodes
}
