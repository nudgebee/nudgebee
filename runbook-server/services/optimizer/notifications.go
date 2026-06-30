package optimizer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
)

// taskOutcome is the user-facing classification of a completed optimize task.
type taskOutcome int

const (
	outcomeApplied  taskOutcome = iota // in-place / direct cluster change (synchronous, no PR)
	outcomePR                          // GitOps PR requested (created asynchronously)
	outcomeTicket                      // ticket created
	outcomeFailed                      // execution failed
	outcomeNoChange                    // skipped or dry-run — not a change
)

// classifyTask maps a completed task row to its outcome. See the decision table
// in the plan: only Status + Attributes.{ResolutionID,TicketLink} are needed.
func classifyTask(t model.AutoOptimizeTask) taskOutcome {
	switch t.Status {
	case string(model.AutopilotTaskStatusFailed):
		return outcomeFailed
	case string(model.AutopilotTaskStatusSkipped), string(model.AutoOptimizeStatusDryrun):
		return outcomeNoChange
	}
	// Complete:
	if t.Attributes.TicketLink != nil && *t.Attributes.TicketLink != "" {
		return outcomeTicket
	}
	if t.Attributes.ResolutionID != nil {
		return outcomePR
	}
	return outcomeApplied
}

// prTaskRecommendationIDs returns the (deduped, order-preserving) recommendation
// IDs of the run's GitOps-PR tasks.
func prTaskRecommendationIDs(tasks []model.AutoOptimizeTask) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{})
	var ids []uuid.UUID
	for _, t := range tasks {
		if classifyTask(t) != outcomePR || t.RecommendationID == nil {
			continue
		}
		if _, ok := seen[*t.RecommendationID]; ok {
			continue
		}
		seen[*t.RecommendationID] = struct{}{}
		ids = append(ids, *t.RecommendationID)
	}
	return ids
}

// resolutionSettled reports whether a recommendation's PR has reached a terminal
// state for the run: a PR URL exists, or creation failed.
func resolutionSettled(rs []model.RecommendationResolution) bool {
	url, _ := resolutionPRURL(rs)
	_, failed := resolutionFailure(rs)
	return url != "" || failed
}

func resolutionPRURL(rs []model.RecommendationResolution) (string, bool) {
	for _, r := range rs {
		if r.TypeReferenceID != "" {
			return r.TypeReferenceID, true
		}
	}
	return "", false
}

func resolutionFailure(rs []model.RecommendationResolution) (string, bool) {
	for _, r := range rs {
		if r.Status == string(model.RecommendationResolutionStatusFailed) {
			msg := ""
			if r.StatusMessage != nil {
				msg = *r.StatusMessage
			}
			return msg, true
		}
	}
	return "", false
}

// sendCompletionSummary builds and sends the change-gated run summary. No-op
// when the run made no change (all skipped / dry-run / nothing).
func (s *optimizerService) sendCompletionSummary(ctx context.Context, ao *model.AutoOptimize, taskIDs []uuid.UUID) {
	if len(taskIDs) == 0 {
		return
	}
	tasks, err := s.dao.GetAutoOptimizeTasksByIDs(ctx, taskIDs)
	if err != nil {
		slog.Error("Failed to fetch tasks for completion summary", "auto_optimize_id", ao.ID, "error", err)
		return
	}

	if _, hasChange := buildCompletionSummary(ao, tasks, ""); !hasChange {
		return
	}
	s.sendToConfiguredChannels(ao, func(platform string) string {
		body, _ := buildCompletionSummary(ao, tasks, platform)
		return body
	})
}

// buildCompletionSummary returns the run summary (with platform-appropriate
// links) and whether it should be sent (true iff the run produced at least one
// change or failure). The hasChange result is platform-independent.
func buildCompletionSummary(ao *model.AutoOptimize, tasks []model.AutoOptimizeTask, platform string) (string, bool) {
	var applied, prs, tickets, failed []model.AutoOptimizeTask
	skipped := 0
	for _, t := range tasks {
		switch classifyTask(t) {
		case outcomeApplied:
			applied = append(applied, t)
		case outcomePR:
			prs = append(prs, t)
		case outcomeTicket:
			tickets = append(tickets, t)
		case outcomeFailed:
			failed = append(failed, t)
		case outcomeNoChange:
			skipped++
		}
	}

	if len(applied)+len(prs)+len(tickets)+len(failed) == 0 {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s — run complete\n", optimizeDisplayName(ao))

	if len(applied) > 0 {
		fmt.Fprintf(&b, "\nApplied in-place (%d):\n", len(applied))
		writeResourceLines(&b, applied, true)
	}
	if len(prs) > 0 {
		fmt.Fprintf(&b, "\nPull requests in progress (%d) — links to follow:\n", len(prs))
		writeResourceLines(&b, prs, false)
	}
	if len(tickets) > 0 {
		fmt.Fprintf(&b, "\nTickets created (%d):\n", len(tickets))
		for _, t := range tickets {
			link := ""
			if t.Attributes.TicketLink != nil {
				link = *t.Attributes.TicketLink
			}
			if link != "" {
				fmt.Fprintf(&b, "  - %s — %s\n", resourceLabel(t), formatLink(platform, link, "View ticket"))
			} else {
				fmt.Fprintf(&b, "  - %s\n", resourceLabel(t))
			}
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(&b, "\nFailed (%d):\n", len(failed))
		for _, t := range failed {
			reason := taskReason(t)
			if reason != "" {
				fmt.Fprintf(&b, "  - %s — %s\n", resourceLabel(t), reason)
			} else {
				fmt.Fprintf(&b, "  - %s\n", resourceLabel(t))
			}
		}
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "\n%d skipped.\n", skipped)
	}

	fmt.Fprintf(&b, "\n%s", formatLink(platform, autoOptimizeDeepLink(ao), "View in Nudgebee"))
	return b.String(), true
}

// buildPRsReadySummary returns the PR-ready follow-up body (with
// platform-appropriate links), or "" if no PR has settled yet (no URL and no
// failure to report).
func buildPRsReadySummary(ao *model.AutoOptimize, tasks []model.AutoOptimizeTask, resolutions map[uuid.UUID][]model.RecommendationResolution, platform string) string {
	// recommendation_id -> resource label (first PR task for that recommendation)
	labels := make(map[uuid.UUID]string)
	var order []uuid.UUID
	for _, t := range tasks {
		if classifyTask(t) != outcomePR || t.RecommendationID == nil {
			continue
		}
		if _, ok := labels[*t.RecommendationID]; ok {
			continue
		}
		labels[*t.RecommendationID] = resourceLabel(t)
		order = append(order, *t.RecommendationID)
	}

	var ready, failed []string
	for _, rid := range order {
		rs := resolutions[rid]
		if url, ok := resolutionPRURL(rs); ok {
			ready = append(ready, fmt.Sprintf("  - %s — %s", labels[rid], formatLink(platform, url, prLabel(url))))
			continue
		}
		if msg, ok := resolutionFailure(rs); ok {
			if msg != "" {
				failed = append(failed, fmt.Sprintf("  - %s — failed: %s", labels[rid], firstLine(msg, 140)))
			} else {
				failed = append(failed, fmt.Sprintf("  - %s — failed", labels[rid]))
			}
		}
	}

	if len(ready) == 0 && len(failed) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s — pull requests\n", optimizeDisplayName(ao))
	if len(ready) > 0 {
		fmt.Fprintf(&b, "\nPull requests created (%d):\n%s\n", len(ready), strings.Join(ready, "\n"))
	}
	if len(failed) > 0 {
		fmt.Fprintf(&b, "\nPR creation failed (%d):\n%s\n", len(failed), strings.Join(failed, "\n"))
	}
	fmt.Fprintf(&b, "\n%s", formatLink(platform, autoOptimizeDeepLink(ao), "View in Nudgebee"))
	return b.String()
}

func writeResourceLines(b *strings.Builder, tasks []model.AutoOptimizeTask, withReason bool) {
	for _, t := range tasks {
		reason := ""
		if withReason {
			reason = taskReason(t)
		}
		if reason != "" {
			fmt.Fprintf(b, "  - %s — %s\n", resourceLabel(t), reason)
		} else {
			fmt.Fprintf(b, "  - %s\n", resourceLabel(t))
		}
	}
}

func resourceLabel(t model.AutoOptimizeTask) string {
	label := strings.Trim(t.ResourceFilter.String(), "/")
	if label == "" {
		return t.Name
	}
	return label
}

func taskReason(t model.AutoOptimizeTask) string {
	if t.Reason != nil {
		return firstLine(*t.Reason, 140)
	}
	return ""
}

// formatLink renders a hyperlink in the syntax the target platform understands:
// Slack and Google Chat use <url|label>; MS Teams (AdaptiveCard) uses Markdown
// [label](url). Falls back to the bare label when there's no URL.
func formatLink(platform, url, label string) string {
	if url == "" {
		return label
	}
	if platform == "ms_teams" {
		return fmt.Sprintf("[%s](%s)", label, url)
	}
	return fmt.Sprintf("<%s|%s>", url, label)
}

// prLabel derives a short label from a PR/MR URL ("PR #318" when the trailing
// path segment is numeric), falling back to "View PR".
func prLabel(url string) string {
	trimmed := strings.TrimRight(url, "/")
	if i := strings.LastIndexByte(trimmed, '/'); i >= 0 {
		tail := trimmed[i+1:]
		if tail != "" && strings.IndexFunc(tail, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
			return "PR #" + tail
		}
	}
	return "View PR"
}

func optimizeDisplayName(ao *model.AutoOptimize) string {
	if ao.Name != nil && *ao.Name != "" {
		return *ao.Name
	}
	return "Auto optimize"
}

func autoOptimizeDeepLink(ao *model.AutoOptimize) string {
	return fmt.Sprintf("%s/auto-pilot/task/%s?accountId=%s",
		strings.TrimRight(config.Config.BaseUrl, "/"), ao.ID, ao.AccountID)
}

// firstLine returns the first non-empty line of s, truncated to max runes.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
