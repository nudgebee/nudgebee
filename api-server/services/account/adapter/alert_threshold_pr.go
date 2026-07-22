package adapter

import (
	"fmt"
)

// AlertThresholdPRParams describes a single alert-threshold rule-file change to land via PR.
// Unlike rightsizing PRs (which resolve the repo from a k8s workload's annotations), an alert
// rule has no workload, so the repo/branch/file location are supplied explicitly by the caller.
type AlertThresholdPRParams struct {
	Provider      string // "github" (the only currently supported provider)
	AccountID     string // cloud account the code-agent conversation runs against
	Org           string
	Repo          string
	Branch        string
	FilePath      string // path of the rule file in the repo
	OldContent    string // the current rule expression (used to locate the rule)
	NewContent    string // replacement (the rewritten PromQL expr)
	AlertName     string
	ReferenceLink string
}

// ApplyAlertThresholdUsingCodeAgent routes an alert-threshold rule-file change through the
// code-analysis agent (@agent_code_2). The agent clones the repo with the tenant's git
// credentials, edits the rule's threshold expression, and opens a PR — the same path used by
// rightsizing and security-fix recommendation PRs. The resolution row identified by
// resolutionID is updated asynchronously as the run progresses, so this returns immediately.
func ApplyAlertThresholdUsingCodeAgent(ctx AccountAdapterContext, p AlertThresholdPRParams, resolutionID string) error {
	if p.Provider != "" && p.Provider != "github" {
		return fmt.Errorf("alert-threshold PR is currently supported only for GitHub integrations (got %q)", p.Provider)
	}
	if p.Org == "" || p.Repo == "" || p.FilePath == "" {
		return fmt.Errorf("git org, repo and file path are required")
	}
	if p.OldContent == "" || p.OldContent == p.NewContent {
		return fmt.Errorf("no rule change to write")
	}

	dispatchCodeAgentPR(ctx, codeAgentPRParams{
		AccountID:         p.AccountID,
		RecommendationID:  resolutionID,
		ResolutionID:      resolutionID,
		IsEventResolution: true,
		Org:               p.Org,
		Repo:              p.Repo,
		Branch:            p.Branch,
		Label:             "alert threshold code agent",
		SuccessMessage:    "Threshold PR raised successfully by code agent",
	}, func() (string, error) {
		return buildAlertThresholdPrompt(p), nil
	})

	return nil
}

// buildAlertThresholdPrompt renders the natural-language instructions for the code agent. It
// anchors on the alert name (robust against YAML whitespace/line-wrap differences) while
// constraining the change to a single threshold expression and instructing the agent to abort
// rather than guess if it cannot confidently find the target rule.
func buildAlertThresholdPrompt(p AlertThresholdPRParams) string {
	referenceSection := ""
	if p.ReferenceLink != "" {
		referenceSection = fmt.Sprintf("\n\n**Reference**: for more details, see [Nudgebee](%s).", p.ReferenceLink)
	}

	return fmt.Sprintf(`Update the threshold of a Prometheus alerting rule. This is a precise threshold-tuning edit, not an open-ended change.

**Repository**: %s/%s
**Branch**: %s
**Rule file**: %s
**Alert name**: %s

Find the alerting rule with that alert name in the rule file above. Its current expression is:

    %s

Change ONLY that rule's expression to:

    %s

**Instructions**:
1. Locate the rule by its alert name. The stored "current" expression may differ slightly in whitespace or line-wrapping from the file — match the rule by name and verify its expression is equivalent to the "current" one before editing.
2. Replace only that rule's expression with the "new" expression. The only intended change is the threshold value; do not modify any other rule, label, annotation, comment, or formatting.
3. If you cannot confidently identify the rule whose expression matches the "current" one, STOP and report it — do not guess or edit a different rule.

**PR description**: summarize that Nudgebee tuned the alert threshold, showing the old and new expression.%s`,
		p.Org,
		p.Repo,
		p.Branch,
		p.FilePath,
		p.AlertName,
		p.OldContent,
		p.NewContent,
		referenceSection,
	)
}
