package recommendation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/services/account/adapter"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
)

// pr_value_refresh.go rewrites an already-open rightsizing pull request when its
// recommendation has moved materially, instead of leaving it stale or opening a
// second one (#34959).
//
// The mechanism is the one that already addresses review comments on an open pull
// request: re-run the code agent with followup set against the same branch. Only
// the instruction differs.

const (
	// valueRefreshCooldown keeps a workload sitting near its threshold from
	// rewriting its pull request on every hourly run. The materiality threshold
	// (the rule's change_pct, typically 10%) and the rule's buffer (10-15%)
	// overlap, so small genuine movement can repeatedly cross the line.
	valueRefreshCooldown = 6 * time.Hour

	// valueRefreshCap bounds how many times one pull request may be rewritten.
	// Past this the numbers are clearly unsettled and a human should look, rather
	// than the pull request churning indefinitely under a reviewer.
	valueRefreshCap = 5
)

// refreshDecision explains what the guard did, for logging and for the message
// handed back to the caller.
type refreshDecision struct {
	Refreshed bool
	Reason    string
}

// maybeRefreshOpenPR decides whether the open pull request behind existingPR
// should be rewritten with newValues, and dispatches the rewrite if so.
//
// It is deliberately conservative: anything it cannot establish (wrong resolver,
// no threshold, unreadable payload, cooldown, cap) leaves the pull request
// exactly as it is, which is the behaviour that existed before this feature.
func maybeRefreshOpenPR(
	ctx adapter.AccountAdapterContext,
	existingPR *models.RecommendationResolution,
	newValues map[string]any,
	thresholds map[string]float64,
) refreshDecision {
	if len(thresholds) == 0 {
		return refreshDecision{Reason: "caller did not opt in to refreshing an open pull request"}
	}
	// Only a scheduled auto optimize's own pull request is rewritten. One a person
	// raised by hand is left alone.
	if existingPR.ResolverType != models.RecommendationResolutionResolverTypeAutoOptimize {
		return refreshDecision{Reason: "pull request was not raised by an auto optimize"}
	}

	oldValues := storedResolutionValues(existingPR)
	if oldValues == nil {
		return refreshDecision{Reason: "open pull request has no recorded values to compare against"}
	}

	drifts := detectValueDrift(oldValues, newValues, thresholds)
	if len(drifts) == 0 {
		return refreshDecision{Reason: "open pull request is still within the change threshold"}
	}

	if blocked := valueRefreshBlocked(existingPR); blocked != "" {
		ctx.GetLogger().Info("pr_value_refresh: drift detected but refresh is held back",
			"resolution_id", existingPR.Id, "reason", blocked, "drift", describeDrifts(drifts))
		return refreshDecision{Reason: blocked}
	}

	summary := describeDrifts(drifts)
	ctx.GetLogger().Info("pr_value_refresh: refreshing open pull request with changed values",
		"resolution_id", existingPR.Id, "pr_url", existingPR.TypeReferenceId, "drift", summary)

	dispatchValueRefresh(ctx, existingPR, newValues, drifts)

	return refreshDecision{
		Refreshed: true,
		Reason:    fmt.Sprintf("recommendation has changed since this pull request was raised (%s); updating it", summary),
	}
}

// newValuesFromApplyRequest pulls the freshly computed rightsizing values out of
// an apply request, so they can be compared against what the open pull request
// already proposes.
//
// The payload also carries non-container keys such as rich_description; those are
// simply not shaped like a container entry and are ignored by the comparison.
func newValuesFromApplyRequest(query RecommendationApplyRequest) map[string]any {
	values, ok := query.Data.(map[string]any)
	if !ok {
		return nil
	}
	return values
}

// storedResolutionValues pulls the rightsizing payload out of a resolution's data
// blob. The blob also carries pull request metadata, so the values live under
// "data".
func storedResolutionValues(res *models.RecommendationResolution) map[string]any {
	blob, ok := res.Data.Object().(map[string]any)
	if !ok {
		return nil
	}
	values, ok := blob["data"].(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	return values
}

// valueRefreshBlocked returns a human-readable reason when a guardrail says not to
// refresh right now, or "" when a refresh may proceed.
func valueRefreshBlocked(res *models.RecommendationResolution) string {
	if res.ValueRefreshCount >= valueRefreshCap {
		return fmt.Sprintf("pull request has already been updated %d times; leaving it for review", res.ValueRefreshCount)
	}
	if res.LastValueRefreshAt != nil && time.Since(*res.LastValueRefreshAt) < valueRefreshCooldown {
		return fmt.Sprintf("pull request was updated less than %s ago", valueRefreshCooldown)
	}
	return ""
}

// dispatchValueRefresh re-runs the code agent against the open pull request with
// the changed values, and records the outcome.
//
// The recorded values are only advanced once the agent reports success. Writing
// them optimistically would leave the database claiming numbers the pull request
// does not contain — the same class of problem as #34924.
func dispatchValueRefresh(
	ctx adapter.AccountAdapterContext,
	existingPR *models.RecommendationResolution,
	newValues map[string]any,
	drifts []valueDrift,
) {
	summary := describeDrifts(drifts)
	prompt := buildValueRefreshPrompt(existingPR.TypeReferenceId, newValues, drifts)

	adapter.DispatchPRValueRefresh(ctx, existingPR, prompt, valueRefreshCap, valueRefreshCooldown, func(outcome adapter.ValueRefreshOutcome, message string) {
		switch outcome {
		case adapter.ValueRefreshUpdated:
			if err := recordValueRefresh(ctx, existingPR, newValues, summary); err != nil {
				ctx.GetLogger().Error("pr_value_refresh: pull request updated but recording it failed",
					"resolution_id", existingPR.Id, "error", err)
			}
		case adapter.ValueRefreshUnnecessary:
			// Not a problem, so not an error: the branch already says what we would
			// have written. The values are still not recorded — we asked the agent to
			// change the branch and it did not, so claiming they are on it would be
			// the #34924 mistake.
			ctx.GetLogger().Info("pr_value_refresh: pull request needed no update",
				"resolution_id", existingPR.Id, "pr_url", existingPR.TypeReferenceId, "detail", message)
		default:
			// The dispatcher has already handed the claim back and recorded the
			// reason on the row, so the next run can retry rather than finding it
			// apparently mid-flight.
			ctx.GetLogger().Error("pr_value_refresh: failed to update open pull request",
				"resolution_id", existingPR.Id, "pr_url", existingPR.TypeReferenceId, "error", message)
		}
	})
}

// buildValueRefreshPrompt tells the agent what to change and why. The "why" is
// not decoration: it ends up in the commit message and the pull request comment,
// so a reviewer coming back to a pull request that moved under them can see the
// reason without digging.
func buildValueRefreshPrompt(prURL string, newValues map[string]any, drifts []valueDrift) string {
	valuesJSON, _ := json.Marshal(newValues)

	return fmt.Sprintf(
		"The rightsizing recommendation behind PR %s has changed since the pull request was raised. "+
			"Update the existing branch so it applies these values instead: %s\n\n"+
			"What changed: %s\n\n"+
			"Change only the resource values already covered by this pull request — do not alter "+
			"anything else, and do not open a new pull request. Leave a short comment on the pull "+
			"request explaining that the recommendation moved and what the new numbers are, so a "+
			"reviewer is not surprised by the update.",
		prURL, string(valuesJSON), summaryOrNone(drifts))
}

func summaryOrNone(drifts []valueDrift) string {
	if len(drifts) == 0 {
		return "no material change"
	}
	return describeDrifts(drifts)
}

// recordValueRefresh advances the stored values and the guardrail counters after
// a refresh has actually landed on the branch.
//
// pr_iteration_count is reset because the pull request's contents changed: review
// comments raised against the previous numbers may no longer apply, so the review
// follow-up budget should start again rather than be consumed by this rewrite.
func recordValueRefresh(
	ctx adapter.AccountAdapterContext,
	existingPR *models.RecommendationResolution,
	newValues map[string]any,
	summary string,
) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	valuesJSON, err := json.Marshal(map[string]any{"data": newValues})
	if err != nil {
		return err
	}

	// The terminal guard is what stops a merge landing mid-refresh from being
	// undone. Without it this write stamps 'created' over 'merged' while leaving
	// status = 'Success', and that pair is unrecoverable: every lifecycle cron
	// query filters status = 'InProgress', so nothing ever re-terminalises the
	// row, while the open-PR guard keeps treating it as the recommendation's open
	// PR — pinning it to a PR that merged months ago and blocking every PR after.
	// Every other writer of pr_lifecycle_state guards on the state it replaces;
	// this one did not.
	res, err := dbms.Db.ExecContext(context.Background(), `
		UPDATE recommendation_resolution
		SET data = COALESCE(data, '{}'::jsonb) || $1::jsonb,
			value_refresh_count = value_refresh_count + 1,
			last_value_refresh_at = $2,
			pr_iteration_count = 0,
			pr_lifecycle_state = 'created',
			status_message = $3,
			updated_at = $2
		WHERE id = $4
		  AND (pr_lifecycle_state IS NULL
		       OR pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable'))`,
		string(valuesJSON), time.Now().UTC(),
		truncateRefreshMessage("PR updated — "+summary), existingPR.Id)
	if err != nil {
		return err
	}

	// No row means the PR reached a terminal state while the agent was running.
	// The branch was updated for nothing, but the terminal state is the truth and
	// is left alone.
	if rows, raErr := res.RowsAffected(); raErr == nil && rows == 0 {
		ctx.GetLogger().Info("pr_value_refresh: pull request went terminal during the refresh; leaving it terminal",
			"resolution_id", existingPR.Id, "pr_url", existingPR.TypeReferenceId)
	}
	return nil
}

func truncateRefreshMessage(s string) string {
	const maxLen = 800
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + " …(truncated)"
}
