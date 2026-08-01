package prompts_repo

import (
	"strings"
	"testing"
)

// TestLeanPrompts_RouteAutomationNotShell pins that every cloud lean prompt tells the
// planner to build automations via the `automation` agent — not by hand-writing a
// CronJob/script via shell/CLI. Regression guard for the observed failure where lean
// grabbed shell to hand-roll a K8s CronJob (which failed on escaping) instead of
// delegating to the automation agent (which succeeds via its own builder tooling).
func TestLeanPrompts_RouteAutomationNotShell(t *testing.T) {
	for _, name := range []string{
		PromptAgentK8sLean, PromptAgentAwsLean, PromptAgentGcpLean, PromptAgentAzureLean,
	} {
		t.Run(name, func(t *testing.T) {
			p := GetPrompt(name)
			if p == "" {
				t.Fatalf("prompt %s did not load", name)
			}
			lower := strings.ToLower(p)
			if !strings.Contains(lower, "automation") {
				t.Errorf("%s: expected automation-routing guidance (the `automation` agent)", name)
			}
			// Must steer away from hand-writing the automation via CLI.
			if !strings.Contains(lower, "never hand-write") {
				t.Errorf("%s: expected an explicit 'NEVER hand-write ... via shell/CLI' constraint", name)
			}
		})
	}
}
