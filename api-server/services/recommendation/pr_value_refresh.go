package recommendation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
		reason := "open pull request is still within the change threshold"
		// The verdict only covers what was compared. A value the pull request sets
		// that this run produced nothing for is named, so the reason does not read
		// as vouching for it.
		if leftOut := fieldsRunLeftOut(oldValues, newValues, thresholds); len(leftOut) > 0 {
			reason += "; not compared because this run produced no value: " + strings.Join(leftOut, ", ")
		}
		return refreshDecision{Reason: reason}
	}

	if blocked := valueRefreshBlocked(existingPR); blocked != "" {
		ctx.GetLogger().Info("pr_value_refresh: drift detected but refresh is held back",
			"resolution_id", existingPR.Id, "reason", blocked, "drift", describeDrifts(drifts))
		return refreshDecision{Reason: blocked}
	}

	summary := describeDrifts(drifts)
	ctx.GetLogger().Info("pr_value_refresh: refreshing open pull request with changed values",
		"resolution_id", existingPR.Id, "pr_url", existingPR.TypeReferenceId, "drift", summary)

	dispatchRefresh(ctx, existingPR, mergeRefreshValues(oldValues, newValues), drifts)

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

// mergeRefreshValues returns what the open pull request should propose after the
// update: newValues, plus every value the pull request records that this run
// produced nothing for, minus dimensions left with no value at all. The result is
// what the agent is asked to apply and what is recorded once it lands.
//
// A run does not always produce every field the pull request sets. The rule can
// reject one dimension while another passes — a cpu change past its max-change
// limit, say — and the payload then carries that dimension empty. Sent as it is,
// the empty dimension reads as "remove it": the agent put the base branch's cpu
// back on a pull request whose memory limit was the only thing that had moved.
// Recorded as it is, the stored values forget the pull request ever set it.
// Carrying the pull request's own value over keeps both saying what the pull
// request still proposes — which is what detectValueDrift already assumes when
// it ignores a field the recommendation dropped. A dimension with nothing to
// carry over is left out rather than sent empty, for the same reason.
//
// A field counts as produced when it holds a readable quantity, the same test
// detectValueDrift applies. Neither input is modified.
func mergeRefreshValues(oldValues, newValues map[string]any) map[string]any {
	merged := make(map[string]any, len(newValues)+len(oldValues))
	for key, value := range newValues {
		merged[key] = value
	}

	containers := make(map[string]struct{}, len(newValues)+len(oldValues))
	for container := range newValues {
		containers[container] = struct{}{}
	}
	for container := range oldValues {
		containers[container] = struct{}{}
	}

	for container := range containers {
		newEntry := newValues[container]
		newDims, isContainer := newEntry.(map[string]any)
		if newEntry != nil && !isContainer {
			continue // not shaped like a container entry; left as the run sent it
		}
		oldDims, _ := oldValues[container].(map[string]any)

		dims := make(map[string]any, len(oldDims)+len(newDims))
		for dimension, newDim := range newDims {
			newFields, isFields := newDim.(map[string]any)
			if newDim != nil && !isFields {
				dims[dimension] = newDim
				continue
			}
			oldFields, _ := oldDims[dimension].(map[string]any)
			if fields := mergeRefreshFields(oldFields, newFields); len(fields) > 0 {
				dims[dimension] = fields
			}
		}
		for dimension, oldDim := range oldDims {
			if _, sent := newDims[dimension]; sent {
				continue
			}
			oldFields, _ := oldDim.(map[string]any)
			if fields := mergeRefreshFields(oldFields, nil); len(fields) > 0 {
				dims[dimension] = fields
			}
		}

		if len(dims) > 0 {
			merged[container] = dims
		} else {
			delete(merged, container)
		}
	}
	return merged
}

// mergeRefreshFields is one dimension's fields: what the run produced, with the
// pull request's recorded value for any field it did not.
func mergeRefreshFields(oldFields, newFields map[string]any) map[string]any {
	fields := make(map[string]any, len(oldFields)+len(newFields))
	for field, value := range newFields {
		if _, _, produced := quantityOf(newFields, field); produced {
			fields[field] = value
		}
	}
	for field, value := range oldFields {
		if _, present := fields[field]; present {
			continue
		}
		if _, _, recorded := quantityOf(oldFields, field); recorded {
			fields[field] = value
		}
	}
	return fields
}

// valueRefreshBlocked returns a human-readable reason when a guardrail says not to
// refresh right now, or "" when a refresh may proceed.
func valueRefreshBlocked(res *models.RecommendationResolution) string {
	if res.ValueRefreshCount >= valueRefreshCap {
		return fmt.Sprintf("the code agent has already been asked to update this pull request %d times; leaving it for review", res.ValueRefreshCount)
	}
	if res.LastValueRefreshAt != nil && time.Since(*res.LastValueRefreshAt) < valueRefreshCooldown {
		return fmt.Sprintf("pull request was updated less than %s ago", valueRefreshCooldown)
	}
	return ""
}

// dispatchRefresh is dispatchValueRefresh; a variable so a test can see what a
// refresh is asked to apply without a database or a code agent.
var dispatchRefresh = dispatchValueRefresh

// dispatchValueRefresh re-runs the code agent against the open pull request with
// the changed values, and records the outcome.
//
// The recorded values are only advanced once the pull request's own diff shows
// them (valuesOnBranch). Writing them on the agent's word left the database
// claiming numbers the pull request did not contain — the same class of problem
// as #34924 — and, because later drift is measured from the recorded values, it
// also hid the real drift from every run after.
//
// values is the full set the pull request should propose after the update
// (mergeRefreshValues), not the run's payload alone.
func dispatchValueRefresh(
	ctx adapter.AccountAdapterContext,
	existingPR *models.RecommendationResolution,
	values map[string]any,
	drifts []valueDrift,
) {
	summary := describeDrifts(drifts)
	// The agent is shown memory the way the values file writes it ("278Mi"), not
	// as the byte count the recommendation carries, and the check looks for either.
	rendered := adapter.FormatRequestedValuesForPrompt(values)
	prompt := buildValueRefreshPrompt(existingPR.TypeReferenceId, rendered, renderDrifts(drifts))
	verify := func(addedLines []string) (bool, string) {
		return valuesOnBranch(drifts, rendered, addedLines)
	}

	adapter.DispatchPRValueRefresh(ctx, existingPR, prompt, valueRefreshCap, valueRefreshCooldown, verify, func(outcome adapter.ValueRefreshOutcome, message string) {
		switch outcome {
		case adapter.ValueRefreshUpdated:
			if err := recordValueRefresh(ctx, existingPR, values, summary); err != nil {
				ctx.GetLogger().Error("pr_value_refresh: pull request updated but recording it failed",
					"resolution_id", existingPR.Id, "error", err)
			}
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
			"anything else, and do not open a new pull request. Write the values exactly as given. "+
			"In your summary, say that the recommendation moved and what the new numbers are; it is "+
			"posted on the pull request so a reviewer is not surprised by the update.",
		prURL, string(valuesJSON), summaryOrNone(drifts))
}

func summaryOrNone(drifts []valueDrift) string {
	if len(drifts) == 0 {
		return "no material change"
	}
	return describeDrifts(drifts)
}

// recordValueRefresh advances the stored values and the rewrite budget after a
// refresh has actually landed on the branch.
//
// value_refresh_count counts agent runs that finished — this one, and runs that
// ended without the values on the branch — but never an attempt that failed on
// infrastructure (an expired git token, a gateway 5xx, an empty agent response):
// five transient failures must not retire a pull request for its entire life.
// Those are bounded by the cadence stamp instead (see recordValueRefreshFailure).
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
		return nil
	}

	// The resolution row's own pr_iteration_count/pr_lifecycle_state above are
	// no longer what drives the review-followup loop (#36457) — pr_followup,
	// keyed by PR URL, is. Reset that too, or a review-followup budget that
	// should restart after this rewrite would stay consumed by comments raised
	// against the numbers this refresh just replaced.
	if err := adapter.ResetPRFollowupBudget(ctx, existingPR.TypeReferenceId); err != nil {
		ctx.GetLogger().Error("pr_value_refresh: failed to reset pr_followup budget after refresh",
			"resolution_id", existingPR.Id, "pr_url", existingPR.TypeReferenceId, "error", err)
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
