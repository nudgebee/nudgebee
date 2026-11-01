package agents

// codeOwnershipInstructions applies only to companion issues in this code-fix chat.
const codeOwnershipInstructions = `For a GitHub/GitLab companion issue created as part of this same code-fix chat, reuse automated_fix_pr_info.ownership.handle from code_analyzer when present. This is best-effort automatic assignment, not a request to ask the user to choose an assignee. Preserve any explicit user-supplied assignee and any existing non-empty issue assignment. If the companion issue is being created now, validate the exact handle against the destination repository and include it in the create call. If the companion issue was already created earlier in this chat, use its recorded URL/identifier to read its current assignees and assign it only if still unassigned. Revalidate eligibility for a different repository; never transfer identities between providers or hosts. Verify assignment after writing. On unresolved identity or assignment failure, keep the issue and PR open and report a warning; do not prompt for a fallback owner, recreate either artifact, or close it. Apply this only to companion GitHub/GitLab issues created in this chat, not unrelated/pre-existing issues or other ticket systems. Do not create an issue merely to satisfy assignment. PR publication still requires the existing tenant flag or explicit raise_pr=true.`

func addCompanionIssueGuidance(response map[string]any) {
	pr, ok := response["automated_fix_pr_info"].(map[string]any)
	if !ok {
		return
	}
	if _, ok := pr["ownership"].(map[string]any); ok {
		response["companion_issue_assignment_guidance"] = codeOwnershipInstructions
	}
}
