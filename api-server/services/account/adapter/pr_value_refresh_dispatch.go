package adapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/security"
)

// ValueRefreshOutcome is what came of a dispatched refresh.
type ValueRefreshOutcome int

const (
	// ValueRefreshUpdated — the changed values are on the branch, checked against
	// the pull request's own diff rather than taken from the agent's report.
	ValueRefreshUpdated ValueRefreshOutcome = iota
	// ValueRefreshFailed — the refresh could not run, or ran and the values are
	// not on the branch. The reason is recorded on the resolution row.
	ValueRefreshFailed
)

// ValueRefreshVerifier reports whether the lines a pull request adds carry the
// changed values, and if not, which value is missing. The caller supplies it
// because only the caller knows which values it asked for.
type ValueRefreshVerifier func(addedLines []string) (ok bool, missing string)

// valueRefreshRecheckDelay is how long to wait before reading the pull request's
// diff a second time when the first read does not show the values. GitHub can
// briefly serve the diff of the previous head right after a push.
var valueRefreshRecheckDelay = 10 * time.Second

// DispatchPRValueRefresh re-runs the code agent against an already-open pull
// request so it applies changed rightsizing values, and reports whether the
// update landed (#34959).
//
// It reuses the followup contract the lifecycle cron uses to address review
// comments — same branch, same credentials, same bounded background run — and
// sends the change as an instruction, which makes that run apply it and nothing
// else. onDone runs once the agent has finished.
//
// "Landed" is read from the pull request itself: whatever the agent reports,
// the refresh counts as done only when verify finds the changed values in the
// lines the pull request adds. An agent's success has not been enough — a run
// that answered an old review comment reported success too, and the new values
// were recorded while the branch still had the old ones.
//
// A review followup already running on the pull request is left to finish. The
// refresh does not take it over: there is no way to stop that agent's job, so
// taking the claim would put two agents on one branch. A refused refresh does
// not spend the cooldown and is retried on the next scheduled run.
func DispatchPRValueRefresh(
	ctx AccountAdapterContext,
	resolution *models.RecommendationResolution,
	prompt string,
	maxRefreshes int,
	cooldown time.Duration,
	verify ValueRefreshVerifier,
	onDone func(outcome ValueRefreshOutcome, message string),
) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		onDone(ValueRefreshFailed, "failed to reach the database: "+err.Error())
		return
	}

	meta, tenantID, err := prMetadataForResolution(resolution)
	if err != nil {
		onDone(ValueRefreshFailed, err.Error())
		return
	}

	gitToken, err := getGitTokenForTenant(dbms, tenantID, meta.Provider)
	if err != nil {
		onDone(ValueRefreshFailed, "failed to get git token: "+err.Error())
		return
	}

	// Checked before the cadence claim so that waiting for a review followup does
	// not cost the cooldown: waiting is not an attempt.
	busy, err := prFollowupRunInFlight(dbms, meta.PRURL)
	if err != nil {
		onDone(ValueRefreshFailed, "failed to check for a running followup: "+err.Error())
		return
	}
	if busy {
		const reason = "a review followup is running on this pull request; the update is retried on the next run"
		recordValueRefreshFailure(dbms, resolution.Id, reason)
		onDone(ValueRefreshFailed, reason)
		return
	}

	// Cadence guardrail (how OFTEN one PR may be rewritten): value_refresh_count /
	// last_value_refresh_at on the recommendation_resolution row, checked and
	// stamped atomically so two concurrent replicas can't both win the same
	// cooldown window. Independent of the pr_followup claim below.
	cadenceOK, err := claimValueRefreshCadence(dbms, resolution.Id, maxRefreshes, cooldown)
	if err != nil {
		onDone(ValueRefreshFailed, "failed to check the refresh cadence: "+err.Error())
		return
	}
	if !cadenceOK {
		onDone(ValueRefreshFailed, "another run is already updating this pull request, or a guardrail now blocks it")
		return
	}

	var createdAt time.Time
	if resolution.CreatedAt != nil {
		createdAt = *resolution.CreatedAt
	}
	followupID, addressedComments, claimed, err := claimPRFollowupForValueRefresh(dbms, meta.PRURL, tenantID, createdAt)
	if err != nil {
		recordValueRefreshFailure(dbms, resolution.Id, "failed to claim the pull request for updating: "+err.Error())
		onDone(ValueRefreshFailed, "failed to claim the pull request for updating: "+err.Error())
		return
	}
	if !claimed {
		// A terminal PR (merged/closed/unresolvable), or a review followup that
		// started between the check above and this claim.
		const reason = "pull request already reached a terminal state, or a review followup started on it"
		recordValueRefreshFailure(dbms, resolution.Id, reason)
		onDone(ValueRefreshFailed, reason)
		return
	}

	chatRequest := buildPRFollowupChatRequest(meta, gitToken, prompt, prompt, addressedComments)

	reqCtx := security.NewRequestContext(ctx.GetContext(), ctx.GetSecurityContext(), ctx.GetLogger(), nil, nil)

	// Every failure path below hands the claim back with the reason recorded, so a
	// refresh that did not land is visibly failed rather than indistinguishable
	// from one still running. Both keep the cooldown stamp.
	//
	// fail is for a refresh that could not be carried out — the agent errored, or
	// the diff could not be read. It does not count toward the cap: five gateway
	// errors must not retire a pull request.
	fail := func(message string) {
		reason := truncateForStatus("Could not update the pull request with the changed values: " + message)
		recordValueRefreshFailure(dbms, resolution.Id, reason)
		releasePRFollowupClaim(dbms, followupID, reason)
		onDone(ValueRefreshFailed, message)
	}
	// notLanded is for an agent that finished its run while the values are still
	// not on the branch. That counts toward the cap: the next run would repeat the
	// same request to the same agent, and past the cap a person should look.
	notLanded := func(message string) {
		reason := truncateForStatus("The code agent finished but the pull request does not have the changed values: " + message)
		recordValueRefreshNotLanded(dbms, resolution.Id, reason)
		releasePRFollowupClaim(dbms, followupID, reason)
		onDone(ValueRefreshFailed, message)
	}

	runPRFollowupAgent(reqCtx, tenantID, chatRequest,
		followupRunSpec{dbms: dbms, followupID: followupID, reattachable: false, logKey: "resolution_id", logID: resolution.Id},
		func(tenantCtx *security.RequestContext, _ string, response *llm.ChatCompletionResponse, err error) {
			if err != nil {
				fail(err.Error())
				return
			}
			if response == nil || len(response.Response) == 0 {
				fail("code agent returned an empty response")
				return
			}
			// success and no_op both get checked against the branch: success is not
			// proof, and a no_op may mean the values were already there.
			if outcome := classifyFollowupOutcome(response.Response); outcome.name != followupOutcomeSuccess.name && outcome.name != followupOutcomeNoOp.name {
				fail("code agent did not update the pull request: " + outcome.name)
				return
			}

			landed, missing, err := checkValueRefreshLanded(tenantCtx, dbms, tenantID, meta, verify)
			if err != nil {
				fail("could not read the pull request's changes to confirm the update: " + err.Error())
				return
			}
			switch {
			case landed:
				// The caller (recommendation.recordValueRefresh) records the new
				// values and clears the pr_followup mutex via ResetPRFollowupBudget
				// — deliberately not done here, so the mutex only clears once the
				// values are actually recorded.
				onDone(ValueRefreshUpdated, "")
			case !followupInstructionHandled(response.Response):
				// An agent that predates instructions reports "nothing to do"
				// without ever reading one. Not the agent's failure to charge.
				fail("the code agent does not support updating pull request values yet")
			default:
				notLanded(missing)
			}
		})
}

// checkValueRefreshLanded reads the lines the pull request adds and runs verify
// on them, reading a second time after a short wait if the first read does not
// show the values (GitHub can lag a push). err is set only when the diff could
// not be read.
func checkValueRefreshLanded(ctx *security.RequestContext, dbms *database.DatabaseManager, tenantID string, meta prMetadata, verify ValueRefreshVerifier) (landed bool, missing string, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(valueRefreshRecheckDelay)
		}
		added, ferr := fetchPRAddedLines(ctx, dbms, tenantID, meta)
		if ferr != nil {
			return false, "", ferr
		}
		if landed, missing = verify(added); landed {
			return true, "", nil
		}
	}
	return false, missing, nil
}

// followupInstructionHandled reads code-analysis's acknowledgement that it acted
// on the instruction. Absent means an agent that predates instructions.
func followupInstructionHandled(responses []string) bool {
	if len(responses) == 0 {
		return false
	}
	var agentResp map[string]any
	if err := json.Unmarshal([]byte(responses[0]), &agentResp); err != nil {
		return false
	}
	handled, _ := agentResp["followup_instruction_handled"].(bool)
	return handled
}

// prMetadataForResolution reads the pull request metadata a resolution carries,
// falling back to the tenant recorded in that metadata when the row itself has
// none (the same fallback the lifecycle cron uses).
func prMetadataForResolution(resolution *models.RecommendationResolution) (prMetadata, string, error) {
	var meta prMetadata

	blob, ok := resolution.Data.Object().(map[string]any)
	if !ok {
		return meta, "", errValueRefresh("pull request metadata is unreadable")
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return meta, "", errValueRefresh("pull request metadata is unreadable")
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, "", errValueRefresh("pull request metadata is unreadable")
	}

	if meta.PRURL == "" || meta.RepoURL == "" {
		return meta, "", errValueRefresh("pull request metadata is missing the url or repository")
	}
	if strings.TrimSpace(meta.TenantID) == "" {
		return meta, "", errValueRefresh("pull request metadata has no tenant to scope the run")
	}
	return meta, meta.TenantID, nil
}

// claimValueRefreshCadence checks and stamps the value-refresh cadence
// guardrail (how often ONE pull request may be rewritten) on its own row lock,
// independent of the pr_followup mutex claim. Reports whether the claim was
// won.
//
// The cap and cooldown are conditions of the update rather than a separate
// check, so two replicas evaluating the same row at the same moment cannot
// both decide to proceed: the claim STAMPS the cooldown it checks, so the
// second update — serialised behind the first by the row lock — sees the
// fresh stamp and loses. A refresh that fails hands the stamp back
// (recordValueRefreshFailure), which KEEPS that stamp, so a failed attempt
// retries once the cooldown elapses rather than on the very next run.
//
// (now() AT TIME ZONE 'UTC'), not bare now(): the column is `timestamp without
// time zone` and every other writer puts naive UTC in it (recordValueRefresh
// writes time.Now().UTC()), while this statement then compares it against a UTC
// bound in $3. Bare now() stores session-local, so on a session ahead of UTC the
// row it just wrote reads as being in the future and the cooldown silently
// stretches by the offset — 11.5h after a claim under Asia/Kolkata, but 6h after
// a successful record. Two different cooldowns for the same column.
//
// value_refresh_count counts agent runs that finished — rewrites that landed,
// and runs that ended without the values on the branch (recordValueRefreshNotLanded)
// — never infra failures, and the failing-refresh loop is bounded by the cadence
// as well: a failure keeps the stamp (see recordValueRefreshFailure) rather than
// handing it back, so a refresh that cannot run retries once per cooldown rather
// than on every scheduled run. Charging the counter for infra failures would be
// the wrong lever — five expired tokens or gateway 5xxs would exhaust a pull
// request's rewrite budget for its entire life.
func claimValueRefreshCadence(dbms *database.DatabaseManager, resolutionID string, maxRefreshes int, cooldown time.Duration) (bool, error) {
	result, err := dbms.Db.Exec(
		`UPDATE recommendation_resolution
		 SET last_value_refresh_at = (now() AT TIME ZONE 'UTC')
		 WHERE id = $1
		   AND value_refresh_count < $2
		   AND (last_value_refresh_at IS NULL OR last_value_refresh_at < $3)`,
		resolutionID, maxRefreshes, time.Now().UTC().Add(-cooldown))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// recordValueRefreshFailure records why a refresh did not land, and KEEPS the
// cadence stamp the claim wrote.
//
// It used to clear it, so a failed attempt retried on the very next scheduled
// run. That was harmless while nothing reselected a recommendation whose pull
// request was open — but the optimizer now does, hourly, so clearing it means a
// refresh that cannot run dispatches a code agent every hour for the life of the
// pull request. Keeping the stamp bounds that to one attempt per cooldown while
// leaving the rewrite budget (value_refresh_count) untouched by infra failures.
//
// A NULL stamp also reads as "due" to the optimizer's selection predicate, so
// clearing it here would reselect the workload on the next run regardless.
//
// Guarded against a PR that went terminal concurrently, matching
// recordValueRefresh's own terminal guard, so this never resurrects a dead
// resolution's status_message.
func recordValueRefreshFailure(dbms *database.DatabaseManager, resolutionID, reason string) {
	_, _ = dbms.Db.Exec(
		`UPDATE recommendation_resolution
		 SET status_message = $1, updated_at = now()
		 WHERE id = $2
		   AND (pr_lifecycle_state IS NULL OR pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable'))`,
		reason, resolutionID)
}

// recordValueRefreshNotLanded records a refresh whose agent run finished while
// the changed values are still not on the branch, and charges it to the rewrite
// budget. Like recordValueRefreshFailure it keeps the cadence stamp. Charging it
// is what bounds the loop: the next run would send the same request to the same
// agent, and once the cap is reached the pull request is left for a person.
func recordValueRefreshNotLanded(dbms *database.DatabaseManager, resolutionID, reason string) {
	_, _ = dbms.Db.Exec(
		`UPDATE recommendation_resolution
		 SET status_message = $1, value_refresh_count = value_refresh_count + 1, updated_at = now()
		 WHERE id = $2
		   AND (pr_lifecycle_state IS NULL OR pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable'))`,
		reason, resolutionID)
}

// prFollowupRunInFlight reports whether a followup run currently holds the
// pr_followup mutex for prURL. A PR with no pr_followup row has none.
func prFollowupRunInFlight(dbms *database.DatabaseManager, prURL string) (bool, error) {
	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	var state string
	err := dbms.Db.QueryRowContext(dbCtx, `SELECT pr_lifecycle_state FROM pr_followup WHERE pr_url = $1`, prURL).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == "addressing", nil
}

// claimPRFollowupForValueRefresh claims the pr_followup mutex for prURL. It
// refuses a terminal PR (merged/closed/unresolvable — nothing to refresh) and a
// row already 'addressing': a review followup is running there, and there is no
// way to stop its agent, so claiming over it would put two agents on one branch.
// A plain WHERE-conditioned UPDATE is enough for atomicity here (no CTE needed)
// — Postgres serialises concurrent UPDATEs on the same row, so a second claim
// always re-evaluates the WHERE against the first claim's committed result.
//
// Also returns addressed_comments (#36865), which the followup request carries
// as a fallback skip set.
func claimPRFollowupForValueRefresh(dbms *database.DatabaseManager, prURL, tenantID string, createdAt time.Time) (followupID string, addressed json.RawMessage, claimed bool, err error) {
	followupID, addressed, err = findOrCreatePRFollowup(dbms, prURL, tenantID, createdAt)
	if err != nil {
		return "", nil, false, err
	}
	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	res, err := dbms.Db.ExecContext(dbCtx,
		`UPDATE pr_followup SET pr_lifecycle_state = 'addressing', pr_followup_pending = false, last_pr_check_at = now()
		 WHERE id = $1 AND pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable', 'addressing')`,
		followupID)
	if err != nil {
		return followupID, addressed, false, err
	}
	n, _ := res.RowsAffected()
	return followupID, addressed, n > 0, nil
}

// releasePRFollowupClaim hands the pr_followup mutex back to 'needs_followup'
// after a refresh that did not land, restoring the row to a normal open state
// rather than reconstructing whatever state a review-followup loop had it in
// — simpler, and safe since a value refresh only ever runs on a PR that was
// already open. Guarded on the row still being 'addressing' so a concurrent
// terminal transition (PR closed mid-run) is never overwritten back to open.
func releasePRFollowupClaim(dbms *database.DatabaseManager, followupID, reason string) {
	_, _ = dbms.Db.Exec(
		`UPDATE pr_followup SET pr_lifecycle_state = 'needs_followup', status_message = $1
		 WHERE id = $2 AND pr_lifecycle_state = 'addressing'`,
		reason, followupID)
}

type valueRefreshError string

func (e valueRefreshError) Error() string { return string(e) }

func errValueRefresh(msg string) error { return valueRefreshError(msg) }
