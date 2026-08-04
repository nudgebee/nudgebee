package adapter

import (
	"encoding/json"
	"strings"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/security"
)

// ValueRefreshOutcome is what came of a dispatched refresh. "Did not update the
// branch" is deliberately split in two: the agent declining because there is
// nothing to change is a different event from the refresh being unable to run,
// and they want different handling — different cooldown treatment, and different
// log severity, since only one of them is a problem.
type ValueRefreshOutcome int

const (
	// ValueRefreshUpdated — the agent changed the branch; the new values are live.
	ValueRefreshUpdated ValueRefreshOutcome = iota
	// ValueRefreshUnnecessary — the agent looked and found nothing to change.
	ValueRefreshUnnecessary
	// ValueRefreshFailed — the refresh could not be run or the agent errored.
	ValueRefreshFailed
)

// DispatchPRValueRefresh re-runs the code agent against an already-open pull
// request so it applies changed rightsizing values, and reports whether the
// update landed (#34959).
//
// It reuses the same followup contract the lifecycle cron uses to address review
// comments — same branch, same credentials, same bounded background run — with a
// different instruction. onDone runs once the agent has finished; success is only
// reported when the agent actually reported success, so the caller can hold off
// recording the new values until they are really on the branch.
//
// Priority over an in-flight review followup is deliberate: a value refresh is
// dispatched even when the row is mid-followup. It reclaims the row rather than
// running a second agent against the same branch concurrently, since two agents
// pushing the same branch would corrupt it.
func DispatchPRValueRefresh(
	ctx AccountAdapterContext,
	resolution *models.RecommendationResolution,
	prompt string,
	maxRefreshes int,
	cooldown time.Duration,
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

	// Take the row for this refresh even if a review followup holds it: the
	// numbers being right outranks a review iteration, and the review budget is
	// reset once the refresh lands anyway. The claim is what actually serialises
	// this — checking the guardrails and then dispatching would leave a window
	// for a second replica to slip through and run a second agent against the
	// same branch, so the guardrails are part of the same atomic update.
	claimed, err := claimResolutionForValueRefresh(dbms, resolution.Id, maxRefreshes, cooldown)
	if err != nil {
		onDone(ValueRefreshFailed, "failed to claim the pull request for updating: "+err.Error())
		return
	}
	if !claimed {
		onDone(ValueRefreshFailed, "another run is already updating this pull request, or a guardrail now blocks it")
		return
	}

	chatRequest := buildPRFollowupChatRequest(meta, gitToken, prompt)

	reqCtx := security.NewRequestContext(ctx.GetContext(), ctx.GetSecurityContext(), ctx.GetLogger(), nil, nil)

	// Every failure path below hands the claim back with the reason recorded, so a
	// refresh that did not land is visibly failed rather than indistinguishable
	// from one still running.
	fail := func(message string) {
		releaseValueRefreshClaim(dbms, resolution.Id,
			truncateForStatus("Could not update the pull request with the changed values: "+message))
		onDone(ValueRefreshFailed, message)
	}

	// A no_op is the agent having looked and decided the branch already says what
	// we asked for. That is an answer, not a failure, so it keeps the cooldown the
	// claim consumed — handing it back would re-run the agent on the very next
	// scheduled run and every one after it, since nothing about the inputs has
	// changed. Only the lifecycle state is restored.
	settle := func(message string) {
		restoreValueRefreshState(dbms, resolution.Id,
			truncateForStatus("Pull request already matches the changed values: "+message))
		onDone(ValueRefreshUnnecessary, message)
	}

	runPRFollowupAgent(reqCtx, tenantID, chatRequest, "resolution_id", resolution.Id,
		func(tenantCtx *security.RequestContext, response *llm.ChatCompletionResponse, err error) {
			if err != nil {
				fail(err.Error())
				return
			}
			if response == nil || len(response.Response) == 0 {
				fail("code agent returned an empty response")
				return
			}
			outcome := classifyFollowupOutcome(response.Response)
			switch outcome.name {
			case followupOutcomeSuccess.name:
				onDone(ValueRefreshUpdated, "")
			case followupOutcomeNoOp.name:
				settle("code agent reported " + outcome.name)
			default:
				fail("code agent did not update the pull request: " + outcome.name)
			}
		})
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

// claimResolutionForValueRefresh marks the row as being worked on, so neither the
// lifecycle cron nor another replica dispatches onto the same branch while this
// refresh is in flight. Reports whether the claim was won.
//
// The cap and cooldown are conditions of the update rather than a separate check,
// so two replicas evaluating the same row at the same moment cannot both decide
// to proceed. Deliberately not conditioned on pr_lifecycle_state: a value refresh
// preempts an in-flight review followup rather than yielding to it.
//
// The claim also STAMPS the cooldown it checks. A claim that only checked the
// guardrails would let two concurrent refreshes both win (neither update changes
// what the other's WHERE reads) and run two agents against the same branch.
// Consuming the cooldown at claim time makes the second update — serialised
// behind the first by the row lock — see the fresh stamp and lose. A refresh
// that fails hands the stamp back (releaseValueRefreshClaim), so a failed
// attempt still retries on the next run rather than waiting out the cooldown.
func claimResolutionForValueRefresh(dbms *database.DatabaseManager, resolutionID string, maxRefreshes int, cooldown time.Duration) (bool, error) {
	result, err := dbms.Db.Exec(
		`UPDATE recommendation_resolution
		 SET pr_lifecycle_state = 'addressing', last_pr_check_at = now(), last_value_refresh_at = now()
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

// releaseValueRefreshClaim hands the row back after a refresh that did not land,
// recording why. Without this the row would sit in 'addressing' until the
// lifecycle cron's 45-minute lease reclaimed it, with no visible reason — a
// failure that looks identical to a run still in progress.
//
// The cooldown stamp the claim consumed is handed back too: a failed refresh
// should retry on the next run, not silently wait out a cooldown that no landed
// update ever earned. Clearing it (rather than restoring the previous value) is
// equivalent — the claim only succeeded because any previous stamp had already
// expired. On a crash this release never runs and the claim-time stamp stands,
// which errs towards pausing a crashing refresh instead of hot-looping it.
func releaseValueRefreshClaim(dbms *database.DatabaseManager, resolutionID, reason string) {
	_, _ = dbms.Db.Exec(
		`UPDATE recommendation_resolution
		 SET pr_lifecycle_state = 'created', status_message = $1, updated_at = now(), last_value_refresh_at = NULL
		 WHERE id = $2 AND pr_lifecycle_state = 'addressing'`,
		reason, resolutionID)
}

// restoreValueRefreshState hands the row back after a refresh the agent declined
// to make, keeping the cooldown stamp the claim consumed.
//
// The difference from releaseValueRefreshClaim is deliberate and is the whole
// point: a failed refresh should retry on the next run, but a no_op should not.
// The agent already read the branch and found nothing to change, and the next
// run computes the same drift from the same values — so handing the cooldown
// back buys another identical agent run every hour, indefinitely.
func restoreValueRefreshState(dbms *database.DatabaseManager, resolutionID, reason string) {
	_, _ = dbms.Db.Exec(
		`UPDATE recommendation_resolution
		 SET pr_lifecycle_state = 'created', status_message = $1, updated_at = now()
		 WHERE id = $2 AND pr_lifecycle_state = 'addressing'`,
		reason, resolutionID)
}

type valueRefreshError string

func (e valueRefreshError) Error() string { return string(e) }

func errValueRefresh(msg string) error { return valueRefreshError(msg) }
