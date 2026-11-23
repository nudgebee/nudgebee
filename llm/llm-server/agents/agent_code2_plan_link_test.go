package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// C3: the step a PR carries out is the plan's only source change, or the only one naming the repository.
func TestPrPlanOption(t *testing.T) {
	plan := `{"actions":[
		{"action":"Raise limit","execute_command":"kubectl set resources deploy/api --limits=memory=1Gi"},
		{"action":"Fix the chart","required_change":{"where":"helm chart in example-org/infra","target":"values.yaml","change":"memory 1Gi"}}]}`
	assert.Equal(t, 2, prPlanOption(plan, "https://github.com/acme/other"), "the only source change")

	two := `{"actions":[
		{"action":"Fix the chart","required_change":{"where":"helm chart","target":"acme-infra/values.yaml","change":"memory 1Gi"}},
		{"action":"Fix the leak","required_change":{"where":"code","target":"api/cache.go","change":"bound the cache"}}]}`
	assert.Equal(t, 1, prPlanOption(two, "https://github.com/acme/acme-infra"), "the one naming the repository")
	assert.Equal(t, 0, prPlanOption(two, "https://github.com/acme/unrelated"), "not clear: no option")
	assert.Equal(t, 0, prPlanOption(`{"actions":[{"execute_command":"kubectl rollout undo deploy/api"}]}`, ""), "no source change")
	assert.Equal(t, 0, prPlanOption(`not json`, ""))
}
