package api

import (
	"strings"
	"testing"
)

func TestPlanMarkdown(t *testing.T) {
	md := planMarkdown(RemediationPlan{Summary: "S", RootCause: "RC", Actions: []RemediationAction{
		{Action: "Raise limit", Title: "Raise the memory limit to 200Mi", Kind: RemediationKindFix, Confidence: 80,
			ExecuteCommand: "kubectl set resources x", RollbackCommand: "kubectl rollout undo x", TradeOff: "restarts the pod"},
		{Title: "Restart", Kind: RemediationKindMitigation, Confidence: 40},
		{Action: "Edit values", Kind: RemediationKindFix, RequiredChange: &RequiredChange{Target: "values.yaml", Change: "memory 200Mi"}},
	}})
	for _, want := range []string{
		"## Remediation plan\n\nS", "### Root cause\nRC", "#### 1. Raise limit\n✅ **Fix** · 80% confidence",
		"Raise the memory limit to 200Mi", "**Run**\n```bash\nkubectl set resources x", "**Rollback**",
		"> **Trade-off:** restarts the pod", "\n\n---\n\n#### 2. Restart\n⚠️ **Mitigation** · 40% confidence",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if !strings.Contains(md, "**Change in source** · `values.yaml`\nmemory 200Mi") || strings.Contains(md, "()") {
		t.Errorf("change without a location rendered badly:\n%s", md)
	}
	if strings.Contains(md, "**Verify**") {
		t.Errorf("empty verify command rendered:\n%s", md)
	}
}
