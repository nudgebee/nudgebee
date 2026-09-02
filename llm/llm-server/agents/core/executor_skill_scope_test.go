package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOrchestratorSkillScopeName pins that every mode-variant orchestrator handle
// scopes back to its canonical <cloud>_orchestrator for KB/skill resolution, across
// all clouds and modes, while canonical names and non-orchestrator agents return "".
func TestOrchestratorSkillScopeName(t *testing.T) {
	cases := map[string]string{
		// mode-variant handles → canonical base (all clouds, all modes)
		"k8s_orchestrator_lean":       "k8s_orchestrator",
		"k8s_orchestrator_direct":     "k8s_orchestrator",
		"k8s_orchestrator_native":     "k8s_orchestrator",
		"k8s_orchestrator_delegating": "k8s_orchestrator",
		"aws_orchestrator_lean":       "aws_orchestrator",
		"aws_orchestrator_direct":     "aws_orchestrator",
		"gcp_orchestrator_lean":       "gcp_orchestrator",
		"azure_orchestrator_lean":     "azure_orchestrator",
		"datadog_orchestrator_lean":   "datadog_orchestrator",
		// future/unknown mode suffixes still resolve via the convention
		"k8s_orchestrator_experimental": "k8s_orchestrator",

		// canonical names → "" (already in ownSkillNames via GetName)
		"k8s_orchestrator":   "",
		"aws_orchestrator":   "",
		"gcp_orchestrator":   "",
		"azure_orchestrator": "",

		// non-orchestrator agents → ""
		"postgres":       "",
		"logs":           "",
		"delegate_agent": "",
		"":               "",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, orchestratorSkillScopeName(name))
		})
	}
}
