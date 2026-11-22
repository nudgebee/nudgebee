package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

// Each option that changes a repository is raised by how its repository is known: the workload's own (one click), a
// connected one the person confirms, or one the person picks. The stored plan keeps what the model wrote.
func TestOptionRepos(t *testing.T) {
	swap(t, &integrationRepo, func(_ *security.RequestContext, _, repo string) string {
		if repo == "acme/infra" || repo == "https://github.com/acme/infra" {
			return "https://github.com/acme/infra"
		}
		return ""
	})
	plan := RemediationPlan{Actions: []RemediationAction{
		{ExecuteCommand: "kubectl scale deployment web --replicas=2"},
		{RequiredChange: &RequiredChange{Where: "code", Change: "add a timeout", Repo: "acme/web"}},
		{RequiredChange: &RequiredChange{Where: "deployment_source", Change: "replicaCount: 2", Repo: "acme/infra"}},
		{RequiredChange: &RequiredChange{Where: "deployment_source", Change: "replicaCount: 2", Repo: "sample-app"}},
		{RequiredChange: &RequiredChange{Where: "code", Change: "bound the cache"}},
		{RequiredChange: &RequiredChange{Where: "infrastructure", Change: "raise the quota"}},
		{RequiredChange: &RequiredChange{Where: "infrastructure", Change: "raise the node pool", Repo: "acme/infra"}},
		{RequiredChange: &RequiredChange{Where: "process", Change: "page the owner", Repo: "acme/web"}},
	}}
	got := optionRepos(nil, "a", "e", plan, workloadRepoFacts{Repos: []string{"git@github.com:acme/web.git"}})
	assert.Equal(t, []optionRepo{
		{Option: 2, Repo: "git@github.com:acme/web.git", Known: repoKnownWorkload},
		{Option: 3, Repo: "https://github.com/acme/infra", Known: repoKnownIntegration},
		{Option: 4, Repo: "sample-app"},
		{Option: 5},
		{Option: 7, Repo: "https://github.com/acme/infra", Known: repoKnownIntegration},
	}, got, "a quota change and a process change are made outside a repository")
	assert.Equal(t, "acme/web", plan.Actions[1].RequiredChange.Repo, "the stored plan keeps the model's repository")
	assert.Equal(t, pickKindPR, pickMode(plan.Actions[4]), "a code change always lives in a repository")
	assert.Equal(t, "manual", pickMode(plan.Actions[5]))
	assert.Equal(t, "manual", pickMode(plan.Actions[7]))
}

// A Raise a PR pick goes to the workload's repository, else to the connected one the person picked.
func TestPrRepoOf(t *testing.T) {
	facts := pickFacts{Repos: []optionRepo{{Option: 1, Repo: "https://github.com/acme/web", Known: repoKnownWorkload},
		{Option: 2, Repo: "https://github.com/acme/infra", Known: repoKnownIntegration}}}
	assert.Equal(t, "https://github.com/acme/web", prRepoOf(remediationPickRequest{Option: 1, Repo: "acme/other"}, facts), "the workload's wins")
	assert.Empty(t, prRepoOf(remediationPickRequest{Option: 2}, facts), "a connected guess is confirmed, not assumed")
	facts.PickedRepo = "https://github.com/acme/infra"
	assert.Equal(t, "https://github.com/acme/infra", prRepoOf(remediationPickRequest{Option: 2, Repo: "acme/infra"}, facts))
	facts.PickedRepo = ""
	assert.Empty(t, prRepoOf(remediationPickRequest{Option: 3, Repo: "acme/elsewhere"}, facts), "not connected")
}
