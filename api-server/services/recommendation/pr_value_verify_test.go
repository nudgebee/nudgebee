package recommendation

import (
	"strings"
	"testing"

	"nudgebee/services/account/adapter"

	"github.com/stretchr/testify/assert"
)

// The shape a refresh sends: memory as the byte count the recommendation
// carries, which the agent is shown as a rounded Mi quantity.
func refreshedValues() map[string]any {
	return map[string]any{
		"collector": map[string]any{
			"cpu":    map[string]any{"request": "265m"},
			"memory": map[string]any{"request": "290758860", "limit": "290758860"},
		},
	}
}

func refreshedDrifts() []valueDrift {
	return []valueDrift{
		{Container: "collector", Dimension: "cpu", Field: "request", Old: "318m", New: "265m", ChangePct: -16.7},
		{Container: "collector", Dimension: "memory", Field: "request", Old: "367910502", New: "290758860", ChangePct: -21},
		{Container: "collector", Dimension: "memory", Field: "limit", Old: "367910502", New: "290758860", ChangePct: -21},
	}
}

func TestValuesOnBranch_FindsTheRenderedValues(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{
		"    requests:",
		"      cpu: 265m",
		"      memory: 278Mi",
		"    limits:",
		`      memory: "278Mi"  # rightsized`,
	}
	ok, missing := valuesOnBranch(refreshedDrifts(), rendered, added)
	assert.True(t, ok, missing)
}

// The recorded values once moved while the branch still had the old ones — the
// agent had answered an unrelated review comment and reported success.
func TestValuesOnBranch_OldValuesAreNotTheNewOnes(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{"      cpu: 318m", "      memory: 351Mi", "      memory: 351Mi", "  # addressed review comment"}
	ok, missing := valuesOnBranch(refreshedDrifts(), rendered, added)
	assert.False(t, ok)
	assert.Contains(t, missing, "collector cpu request 265m")
}

// A request and a limit that both moved to one number need two lines; a single
// line must not vouch for both.
func TestValuesOnBranch_OneLineVouchesForOneValue(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{"      cpu: 265m", "      memory: 278Mi"}
	ok, missing := valuesOnBranch(refreshedDrifts(), rendered, added)
	assert.False(t, ok)
	assert.Contains(t, missing, "memory limit")
}

// Charts name their resource keys whatever their templates read, and an agent
// may write a quantity in another equal form.
func TestValuesOnBranch_KeyAndFormatAgnostic(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{
		`  "collectorCpuRequest": "0.265",`,
		"  app.mem_req: 290758860",
		"  - memLimit: 278Mi",
	}
	ok, missing := valuesOnBranch(refreshedDrifts(), rendered, added)
	assert.True(t, ok, missing)
}

// A number that happens to match, on a key that is not a resource, is not the
// value landing.
func TestValuesOnBranch_IgnoresNonResourceKeys(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{"      readinessProbeTimeout: 265m", "      replicas: 278Mi"}
	ok, missing := valuesOnBranch(refreshedDrifts(), rendered, added)
	assert.False(t, ok)
	assert.Contains(t, missing, "collector cpu request 265m")
}

// A memory line must not vouch for a cpu value, and the other way round.
func TestValuesOnBranch_KeepsDimensionsApart(t *testing.T) {
	values := map[string]any{"collector": map[string]any{"cpu": map[string]any{"request": "2"}}}
	drifts := []valueDrift{{Container: "collector", Dimension: "cpu", Field: "request", Old: "1", New: "2"}}
	ok, _ := valuesOnBranch(drifts, adapter.FormatRequestedValuesForPrompt(values), []string{"  memory: 2"})
	assert.False(t, ok)
}

// Charts that nest the field under a cpu:/memory: block write bare
// request/limit keys; the block's own line is often unchanged context.
func TestValuesOnBranch_AcceptsNestedRequestAndLimitKeys(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{"      request: 265m", "      request: 278Mi", "      limit: 278Mi"}
	ok, missing := valuesOnBranch(refreshedDrifts(), rendered, added)
	assert.True(t, ok, missing)
}

func TestValuesOnBranch_IgnoresLinesThatAreNotQuantities(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	added := []string{"image: collector:265m", "# cpu: 265m", "name: memory-278Mi"}
	ok, _ := valuesOnBranch(refreshedDrifts()[:1], rendered, added)
	assert.False(t, ok)
}

// The agent reads memory the way the values file writes it, not as a byte count.
func TestBuildValueRefreshPrompt_RendersMemory(t *testing.T) {
	rendered := adapter.FormatRequestedValuesForPrompt(refreshedValues())
	prompt := buildValueRefreshPrompt("https://github.com/acme/infra/pull/42", rendered, renderDrifts(refreshedDrifts()))

	assert.Contains(t, prompt, "278Mi")
	assert.Contains(t, prompt, "351Mi → 278Mi")
	assert.NotContains(t, prompt, "290758860")
	assert.False(t, strings.Contains(prompt, "Leave a short comment"),
		"the agent must not post its own comment; its summary is posted for it")
}
