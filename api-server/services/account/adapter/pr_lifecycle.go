package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/recommendation/coordinator"
	"nudgebee/services/security"
)

// prFollowupTable is the single table that owns the PR-followup claim,
// iteration counter and lifecycle state (#36457). Resolution rows
// (event_resolution / recommendation_resolution) used to each carry their own
// copy of this state, so one PR with a resolution row in both tables — or
// duplicate rows in one table — ran two independent followup loops with
// separate counters and separate verdicts. pr_followup is keyed by PR URL
// (UNIQUE constraint), so however many resolution rows nominate the same URL,
// they collapse onto one row. Resolution rows remain the durable metadata
// source (repo/branch/provider) and the durable terminal-state record
// (merged/closed/unresolvable — see prTerminalFields / markPRFollowupUnresolvableByURL);
// pr_followup only answers "should we run a followup on this PR right now."
const prFollowupTable = "pr_followup"

// prResolutionCandidate is one open PR-type resolution row, used only to
// discover a PR URL + the metadata and tenant needed to dispatch a followup
// for it. It carries none of the claim/iteration state — that lives solely
// on the pr_followup row keyed by PR URL.
type prResolutionCandidate struct {
	ID        string          `db:"id"`
	Data      json.RawMessage `db:"data"`
	TenantID  string          `db:"tenant"`
	CreatedAt time.Time       `db:"created_at"`
	TableName string
}

type prMetadata struct {
	PRURL       string `json:"pr_url"`
	PRNumber    any    `json:"pr_number"`
	RepoURL     string `json:"repo_url"`
	Branch      string `json:"branch"`
	Provider    string `json:"provider"`
	Org         string `json:"org"`
	Repo        string `json:"repo"`
	PRBranch    string `json:"pr_branch"`
	ProjectPath string `json:"project_path"`
	// TenantID is stored by the code_analyzer agent for conversation-originated PRs where
	// the events LEFT JOIN returns no tenant (no event row exists).
	TenantID string `json:"tenant_id"`
	// AccountID is stored by the code_analyzer agent and echoed back on the followup
	// request so llm-server can resolve account-scoped state (conversation,
	// workspace, budget) instead of rejecting the request for missing account.
	AccountID string `json:"account_id"`
}

// CheckAndFollowupOpenPRs polls resolution tables for open agent PRs and triggers followup
// via llm-server. Called from the RPC cron job hourly (cron_triggers.yaml: '0 * * * *');
// it is a backstop behind the GitHub webhook, which fans out to ProcessOpenPRFollowup
// within seconds.
func CheckAndFollowupOpenPRs(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	// Reclaim rows stranded in 'addressing' by a run whose process died before it
	// could finalize (rolling deploy / OOM / eviction). This runs first so a
	// reclaimed row is a normal needs_followup candidate for the query below.
	reclaimStuckAddressing(ctx, dbms)

	// Retire stale auto-PRs before dispatching so they drop out of this sweep
	// and stop churning no-op followups.
	markStaleResolutions(ctx, dbms)

	// Settle PR creations that never produced a URL. The orphan handling below
	// marks such a candidate 'unresolvable' but leaves status at InProgress, and
	// prResolutionOpenClause then excludes it from every later sweep — so nothing
	// revisits it, the resolution poller keeps calling the provider for it, and it
	// keeps counting as an active resolution against its recommendation. This
	// moves status off InProgress so the row is actually finished.
	markAbandonedPRCreations(ctx, dbms)

	candidates, err := queryOpenPRResolutionCandidates(dbms)
	if err != nil {
		return fmt.Errorf("failed to query open PR resolutions: %w", err)
	}

	if len(candidates) == 0 {
		ctx.GetLogger().Info("pr_lifecycle: no open PRs need attention")
		return nil
	}

	// Group by PR URL: however many resolution rows nominate the same URL
	// (an event-driven fix and an AutoOptimize recommendation converging on
	// one PR, or duplicate rows within a table), they dispatch through the
	// SAME pr_followup claim below — one run per PR, not one per row.
	groups, orphans := groupCandidatesByPRURL(candidates)

	ctx.GetLogger().Info("pr_lifecycle: found open PRs to check", "candidate_rows", len(candidates), "distinct_prs", len(groups))

	// A candidate with no derivable pr_url can't be grouped under any PR and
	// has no pr_followup entity to mark — retire it directly so it doesn't
	// get re-selected by every cron sweep forever.
	for _, c := range orphans {
		markResolutionRowUnresolvable(ctx, dbms, c, "bad_metadata")
	}

	for prURL, group := range groups {
		// Cron entries already passed the pr_followup cooldown inside the
		// claim below, so the shorter webhook debounce is a no-op for them;
		// pass false anyway to keep "external entry point" behaviour explicit.
		if err := dispatchPRFollowup(ctx, dbms, prURL, group, maxRedispatchChain, false, "cron"); err != nil {
			ctx.GetLogger().Error("pr_lifecycle: failed to process PR", "pr_url", prURL, "error", err)
			continue
		}
	}

	return nil
}

// followupIterationCap bounds how many times a followup that keeps reporting
// `failed` is retried before the claim gate (claimOrMarkResolution) stops
// admitting it. It only bites runs that actually fail: `no_op` and `success`
// leave the counter where it was or reset it, by design — a PR reviewed hours
// after creation must not be capped before the reviewer shows up. Because of
// that the counter can sit at 0 forever, so it is NOT the loop's exit: the
// unconditional exit is age. markStaleResolutions retires any followup still
// open followupStaleAfter after the PR was raised, whatever the counter says
// (#37472).
//
// The cron (cron_triggers.yaml: '0 */6 * * *') is a backstop behind GitHub App
// webhooks (see /api/webhooks/github in public_webhooks.go): real PR events
// fire ProcessOpenPRFollowup directly, and the 6h sweep only catches missed
// deliveries.
const (
	followupIterationCap = 5
	// followupWebhookDebounce collapses a burst of webhook events on the same PR
	// (a CI run emits many check_run transitions over several minutes) into a
	// single followup. The cron path already spaces itself with its 6h schedule,
	// but ProcessOpenPRFollowup (the webhook path) had no rate limit, so a
	// no-op-producing PR re-dispatched once per event — the observed storm of 15
	// followups in 16 minutes. A genuinely new event still triggers a followup
	// once the window elapses; the 6h cron is the backstop if it is debounced.
	followupWebhookDebounce = "10 minutes"
	// followupAddressingLease is how long a row may sit in 'addressing' before the
	// cron treats the claiming run as dead and reclaims it to 'needs_followup'. A
	// followup goroutine is hard-bounded at 35 min (see the context.WithTimeout in
	// dispatchPRFollowup's dispatch), after which it finalizes the row itself. But
	// if the process holding that goroutine dies mid-run — a rolling deploy SIGTERM,
	// OOM, or eviction — the goroutine is dropped and the row is stranded in
	// 'addressing' forever. last_pr_check_at is stamped at claim (and on every
	// subsequent pending-mark), so "no claim/finalize/signal for longer than the
	// run bound" is an unambiguous dead-run signal. The lease is set safely above
	// the 35-min bound so a genuinely in-flight run is never reclaimed.
	followupAddressingLease = "45 minutes"
	// followupStaleAfter retires a followup that has been open this long while
	// still unmerged. These are auto-generated PRs nobody reviewed or merged;
	// without a stale exit they churn a no-op followup every cron sweep forever,
	// because `no_op`/`success` never advance pr_iteration_count and so the
	// counter-gated cap never fires (#37472). The exit is age alone — deliberately
	// far beyond the ">2h late reviewer" case the counter design guards against —
	// so it does not depend on a counter that, by design, may never move. Stale
	// is "stop following up" only: the PR is left open on GitHub, and a real
	// webhook signal resurrects it (see ProcessOpenPRFollowup).
	followupStaleAfter = "3 days"
	// prCreationAbandonedAfter retires a resolution that has been "creating a pull
	// request" without ever recording a URL for this long. Such a row is a dead
	// end that cannot self-heal: GetRecommendationResolutionStatus has no URL to
	// poll so it answers InProgress forever, and the followup cron never sees the
	// row because its lifecycle state is not created/needs_followup. It then
	// blocks its recommendation from every later run, permanently — dev carried
	// two of these from February and March 2026, one of them blocking
	// ml-k8s-server for five months (#34959 follow-up).
	//
	// Creation is a bounded code-agent run measured in minutes, so a row past this
	// window did not survive to write its URL — the process died, or the agent
	// finished without opening a pull request. The window is set far above the run
	// bound so a genuinely in-flight creation is never retired, and far below the
	// months these rows actually sit. Deliberately a code path and not a one-off
	// cleanup script: #34943 shipped remediation SQL that was never run, and
	// on-prem installs have no operator step to run it in.
	prCreationAbandonedAfter = "6 hours"
	// maxRedispatchChain bounds how many times a single completion can chain a
	// re-dispatch for signals that arrived mid-run (pr_followup_pending). Each
	// link is a fresh full run, so this caps a worst-case "signal during every
	// run" loop; beyond it the cron remains the backstop. Coalescing (a single
	// boolean, not a queue) already collapses N concurrent signals into one
	// re-run, so this only bites pathological continuous-signal sources.
	maxRedispatchChain = 5
)

// prDBOpTimeout bounds the small claim / finalize / terminal UPDATEs. They run
// on an independent context.Background() (NOT the caller's request context) on
// purpose: the finalize is a must-complete write — if it inherited the dispatch
// goroutine's cancellation (e.g. the 35-min LLM bound expiring, or a webhook
// handler returning), a canceled run would fail to record its outcome and leave
// the row stuck in 'addressing' forever. The timeout still prevents an
// unresponsive DB from hanging/leaking the goroutine.
const prDBOpTimeout = 10 * time.Second

// prResolutionOpenClause is the "still open" filter shared by every query
// that discovers PR resolution candidates: not yet flipped to a terminal
// state by MarkAllPRResolutionsTerminalByURL / markPRFollowupUnresolvableByURL.
// Matches the guard findOpenPRResolution (recommendation/service.go) already
// uses for its own duplicate-PR dedup check.
const prResolutionOpenClause = `(pr_lifecycle_state IS NULL OR pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable'))`

// queryOpenPRResolutionCandidates scans both resolution tables for open
// PR-type rows. Unlike the pre-#36457 query, this does NOT filter by
// cooldown/iteration cap — that discipline now lives entirely in the
// pr_followup claim (claimOrMarkResolution), which is the single point of
// truth per PR URL regardless of how many resolution rows nominate it. At the
// observed scale (dozens of open PRs), selecting every open candidate each
// sweep and letting the per-URL claim reject ineligible ones is simpler than
// re-deriving the cooldown/cap filter twice, and costs nothing measurable.
func queryOpenPRResolutionCandidates(dbms *database.DatabaseManager) ([]prResolutionCandidate, error) {
	var results []prResolutionCandidate

	eventQuery := `
		SELECT er.id, er.data, er.created_at,
		       COALESCE(e.tenant::text, '') AS tenant
		FROM event_resolution er
		LEFT JOIN events e ON er.event_id = e.id
		WHERE er.type = 'PullRequest'
		  AND er.status = 'InProgress'
		  AND ` + prResolutionOpenClause
	eventRows, err := dbms.Db.Queryx(eventQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query event_resolution: %w", err)
	}
	defer func() { _ = eventRows.Close() }()

	for eventRows.Next() {
		var row prResolutionCandidate
		row.TableName = "event_resolution"
		if err := eventRows.StructScan(&row); err != nil {
			return nil, fmt.Errorf("failed to scan event_resolution row: %w", err)
		}
		results = append(results, row)
	}

	// recommendation_resolution has no tenant column of its own; resolve it via
	// the parent recommendation. AutoOptimize PR metadata does not carry
	// tenant_id, so without this join the followup would fail with
	// "missing_tenant" and abandon every recommendation-driven PR.
	recQuery := `
		SELECT rr.id, rr.data, rr.created_at,
		       COALESCE(r.tenant_id::text, '') AS tenant
		FROM recommendation_resolution rr
		LEFT JOIN recommendation r ON rr.recommendation_id = r.id
		WHERE rr.type = 'PullRequest'
		  AND rr.status = 'InProgress'
		  AND ` + prResolutionOpenClause
	recRows, err := dbms.Db.Queryx(recQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query recommendation_resolution: %w", err)
	}
	defer func() { _ = recRows.Close() }()

	for recRows.Next() {
		var row prResolutionCandidate
		row.TableName = "recommendation_resolution"
		if err := recRows.StructScan(&row); err != nil {
			return nil, fmt.Errorf("failed to scan recommendation_resolution row: %w", err)
		}
		results = append(results, row)
	}

	return results, nil
}

// fetchResolutionCandidatesByURL loads every open resolution row for one PR
// URL, across both tables. Used by the webhook path, which already knows the
// URL and doesn't need the broad cron scan. event_resolution rows sort first
// to match the query above's ordering (and the pre-#36457 event-first
// tie-break), so dispatchPRFollowup's per-candidate metadata fallback tries
// them in the same order.
func fetchResolutionCandidatesByURL(dbms *database.DatabaseManager, prURL string) ([]prResolutionCandidate, error) {
	var results []prResolutionCandidate

	eventQuery := `
		SELECT er.id, er.data, er.created_at,
		       COALESCE(e.tenant::text, '') AS tenant
		FROM event_resolution er
		LEFT JOIN events e ON er.event_id = e.id
		WHERE er.type = 'PullRequest'
		  AND er.status = 'InProgress'
		  AND ` + prResolutionOpenClause + `
		  AND er.data->>'pr_url' = $1`
	if err := dbms.Db.Select(&results, eventQuery, prURL); err != nil {
		return nil, fmt.Errorf("failed to query event_resolution by pr_url: %w", err)
	}
	for i := range results {
		results[i].TableName = "event_resolution"
	}

	recQuery := `
		SELECT rr.id, rr.data, rr.created_at,
		       COALESCE(r.tenant_id::text, '') AS tenant
		FROM recommendation_resolution rr
		LEFT JOIN recommendation r ON rr.recommendation_id = r.id
		WHERE rr.type = 'PullRequest'
		  AND rr.status = 'InProgress'
		  AND ` + prResolutionOpenClause + `
		  AND rr.data->>'pr_url' = $1`
	var recResults []prResolutionCandidate
	if err := dbms.Db.Select(&recResults, recQuery, prURL); err != nil {
		return nil, fmt.Errorf("failed to query recommendation_resolution by pr_url: %w", err)
	}
	for i := range recResults {
		recResults[i].TableName = "recommendation_resolution"
	}

	return append(results, recResults...), nil
}

// HasOpenPRResolutionForURL reports whether we own an open PR-type resolution
// for the given URL — the webhook's "is this our PR" gate. A false result
// means "not our PR" (or a PR we've already retired to a terminal state), and
// the caller should 200-and-drop without touching pr_followup at all: once
// every resolution row for a URL is terminal, no fresh pr_followup row should
// ever be created for it again.
func HasOpenPRResolutionForURL(prURL string) (bool, error) {
	if prURL == "" {
		return false, nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, fmt.Errorf("failed to get database connection: %w", err)
	}
	candidates, err := fetchResolutionCandidatesByURL(dbms, prURL)
	if err != nil {
		return false, err
	}
	return len(candidates) > 0, nil
}

// prURLFromCandidate extracts the PR URL from a resolution candidate's data
// blob. Returns "" when the blob is absent, unparseable, or has no pr_url.
func prURLFromCandidate(c prResolutionCandidate) string {
	var meta prMetadata
	if err := json.Unmarshal(c.Data, &meta); err != nil {
		return ""
	}
	return meta.PRURL
}

// groupCandidatesByPRURL buckets resolution candidates by PR URL, preserving
// each table's scan order within a bucket (event_resolution rows first,
// matching the pre-#36457 event-first tie-break). A candidate with no usable
// pr_url can't be grouped under any PR — there's no pr_followup entity to
// claim without a URL — so it's returned separately as an orphan for the
// caller to retire directly (markResolutionRowUnresolvable).
func groupCandidatesByPRURL(candidates []prResolutionCandidate) (groups map[string][]prResolutionCandidate, orphans []prResolutionCandidate) {
	groups = make(map[string][]prResolutionCandidate)
	for _, c := range candidates {
		u := prURLFromCandidate(c)
		if u == "" {
			orphans = append(orphans, c)
			continue
		}
		groups[u] = append(groups[u], c)
	}
	return groups, orphans
}

// reclaimStuckAddressing returns pr_followup rows stranded in 'addressing'
// past followupAddressingLease back to 'needs_followup' so the normal
// claim/dispatch path picks them up. It is the recovery for a followup
// goroutine that died mid-run without finalizing (rolling deploy / OOM /
// eviction) — otherwise the row sits in 'addressing' forever. The iteration
// counter is preserved (the dead attempt is not charged, matching a no-op)
// and pr_followup_pending is cleared so the reclaimed run starts from a clean
// slate. Best-effort — a failure here must not stop the sweep, so the error
// is logged, not returned.
func reclaimStuckAddressing(ctx *security.RequestContext, dbms *database.DatabaseManager) {
	reclaimStuckAddressingInTable(ctx, dbms, prFollowupTable)
}

// reclaimStuckAddressingInTable is the table-parameterized core of
// reclaimStuckAddressing, split out so the SQL can be exercised against a
// throwaway table in tests. Returns the number of rows reclaimed.
func reclaimStuckAddressingInTable(ctx *security.RequestContext, dbms *database.DatabaseManager, table string) int64 {
	// now() on both sides: the lease compares last_pr_check_at (written by other
	// replicas' claims) against a single authoritative DB clock, and the fresh
	// stamp uses that same clock — no dependence on app/DB clock sync.
	res, err := dbms.Db.ExecContext(ctx.GetContext(),
		fmt.Sprintf(`UPDATE %s SET
			pr_lifecycle_state = 'needs_followup',
			pr_followup_pending = false,
			last_pr_check_at = now()
			WHERE pr_lifecycle_state = 'addressing'
			  AND last_pr_check_at < now() - $1::interval`, table),
		followupAddressingLease)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to reclaim stuck addressing rows", "table", table, "error", err)
		return 0
	}
	total, _ := res.RowsAffected()
	if total > 0 {
		ctx.GetLogger().Warn("pr_lifecycle: reclaimed rows stuck in addressing", "count", total, "lease", followupAddressingLease)
		common.MetricsPRFollowupReclaimed(ctx.GetContext(), total)
	}
	return total
}

// markStaleResolutions retires followups that have stayed open past
// followupStaleAfter with no merge. This is the lifecycle's only unconditional
// terminal exit: pr_iteration_count only advances on a `failed` outcome, so a
// PR that keeps producing `no_op`/`success` never reaches the cap and would
// otherwise be followed up every cron sweep until someone merges or closes it
// (#37472). Retirement is on age alone for that reason. The age is measured
// from created_at, which the two deliberate-restart paths
// (resurrectStalePRFollowup on a webhook, ResetPRFollowupBudget on a value
// refresh) bump to now() — so it reads as "start of the current followup
// cycle", and a resurrected or refreshed row is not re-retired on the next
// sweep. 'addressing' rows are left alone so a mid-flight run is never yanked.
// Best-effort — a failure here must not stop the sweep, so the error is logged,
// not returned.
func markStaleResolutions(ctx *security.RequestContext, dbms *database.DatabaseManager) {
	markStaleResolutionsInTable(ctx, dbms, prFollowupTable)
}

// markStaleResolutionsInTable is the table-parameterized core of
// markStaleResolutions, split out so the SQL can be exercised against a
// throwaway table in tests. Returns the number of rows retired.
func markStaleResolutionsInTable(ctx *security.RequestContext, dbms *database.DatabaseManager, table string) int64 {
	msg := fmt.Sprintf("retired: open and unmerged for %s", followupStaleAfter)
	res, err := dbms.Db.ExecContext(ctx.GetContext(),
		fmt.Sprintf(`UPDATE %s SET
			pr_lifecycle_state = 'stale',
			status_message = $1,
			pr_followup_pending = false,
			last_pr_check_at = now()
			WHERE pr_lifecycle_state IN ('created', 'needs_followup')
			  AND created_at < now() - $2::interval`, table),
		msg, followupStaleAfter)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to mark stale resolutions", "table", table, "error", err)
		return 0
	}
	total, _ := res.RowsAffected()
	if total > 0 {
		ctx.GetLogger().Info("pr_lifecycle: retired stale followups", "count", total, "stale_after", followupStaleAfter)
	}
	return total
}

// markAbandonedPRCreations retires resolutions stuck at "creating a pull request"
// with no URL recorded, past prCreationAbandonedAfter. Best-effort — a failure
// here must not stop the sweep, so the error is logged, not returned.
//
// 'unresolvable' is the right terminal state rather than 'closed': no pull
// request was ever opened, so there is nothing on the provider that closed.
//
// The part that matters is `status`, and it is why this is not redundant with
// the orphan handling in CheckAndFollowupOpenPRs. markResolutionRowUnresolvable
// sets pr_lifecycle_state and nothing else, so the row keeps status InProgress
// while prResolutionOpenClause now excludes 'unresolvable' — it is dropped from
// every future candidate sweep and never revisited. Left there it is polled
// forever by CheckRecommendationResolutions, and it counts as an active
// resolution that blocks its recommendation from ever being optimized again.
// Dev carried two of these from February and March 2026, one blocking
// ml-k8s-server for five months. Moving status off InProgress, the way
// prTerminalFields does, is what actually finishes the row.
func markAbandonedPRCreations(ctx *security.RequestContext, dbms *database.DatabaseManager) {
	markAbandonedPRCreationsInTables(ctx, dbms, prResolutionTables)
}

// markAbandonedPRCreationsInTables is the table-parameterized core of
// markAbandonedPRCreations, split out so the SQL can be exercised against
// throwaway tables in tests. Returns the number of rows retired.
func markAbandonedPRCreationsInTables(ctx *security.RequestContext, dbms *database.DatabaseManager, tables []string) int64 {
	msg := fmt.Sprintf("no pull request was ever created — gave up after %s", prCreationAbandonedAfter)
	var total int64
	for _, tbl := range tables {
		// (now() AT TIME ZONE 'UTC') on both sides, not bare now(). These columns
		// are `timestamp without time zone` holding UTC, and comparing one against
		// timestamptz now() makes Postgres reinterpret it in the session timezone —
		// so the window silently shrinks by the session's offset. Measured on a
		// scratch database: under Asia/Kolkata the bare-now() form retires a row
		// only three hours old, killing a pull-request creation that is still in
		// flight. Still one DB clock on both sides, now independent of the session.
		//
		// Idempotence comes from status, not from the lifecycle state: this update
		// moves the row off InProgress, so the next sweep's WHERE no longer matches
		// it. Guarding on the lifecycle state instead would have skipped the very
		// rows this exists for — dev's two are already marked 'unresolvable' while
		// still sitting at status InProgress, which is what keeps them in the poll
		// set forever. 'merged' and 'closed' are still excluded: those record a real
		// provider outcome, so the row is not an abandoned creation whatever its URL
		// column says.
		res, err := dbms.Db.ExecContext(ctx.GetContext(),
			fmt.Sprintf(`UPDATE %s SET
				pr_lifecycle_state = 'unresolvable',
				status = $1,
				status_message = $2,
				pr_followup_pending = false,
				last_pr_check_at = (now() AT TIME ZONE 'UTC')
				WHERE type = 'PullRequest'
				  AND status = 'InProgress'
				  AND (type_reference_id IS NULL OR type_reference_id NOT LIKE 'http%%')
				  AND (pr_lifecycle_state IS NULL
				       OR pr_lifecycle_state NOT IN ('merged', 'closed'))
				  AND created_at < (now() AT TIME ZONE 'UTC') - $3::interval`, tbl),
			string(RecommendationResolutionStatusFailed), msg, prCreationAbandonedAfter)
		if err != nil {
			ctx.GetLogger().Error("pr_lifecycle: failed to retire abandoned PR creations", "table", tbl, "error", err)
			continue
		}
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}
	if total > 0 {
		ctx.GetLogger().Info("pr_lifecycle: retired abandoned PR creations",
			"count", total, "abandoned_after", prCreationAbandonedAfter)
	}
	return total
}

// findOrCreatePRFollowup returns the id of the pr_followup row for prURL,
// creating it if this is the first time any code path has considered this URL
// (lazy seed — see the package doc comment on prFollowupTable: no batch
// backfill migration, rows appear on first sighting). createdAt seeds the new
// row's created_at from the resolution row's own created_at (when known) so
// the 3-day stale clock anchors to when the PR was actually raised, not to
// whenever this row happened to be lazily created; pass the zero time to fall
// back to now() (used by callers that only know the PR URL, not a specific
// resolution row's created_at).
//
// Also returns the row's addressed_comments (#36865) — the set of comments a
// previous run already answered. It rides on this statement's RETURNING rather
// than a second SELECT because every dispatch path already calls this, and the
// followup request needs the set anyway.
func findOrCreatePRFollowup(dbms *database.DatabaseManager, prURL, tenantID string, createdAt time.Time) (id string, addressed json.RawMessage, err error) {
	return findOrCreatePRFollowupInTable(dbms, prFollowupTable, prURL, tenantID, createdAt)
}

// findOrCreatePRFollowupInTable is the table-parameterized core of
// findOrCreatePRFollowup, split out so the SQL can be exercised against a
// throwaway table in tests.
func findOrCreatePRFollowupInTable(dbms *database.DatabaseManager, table, prURL, tenantID string, createdAt time.Time) (id string, addressed json.RawMessage, err error) {
	var dbCreatedAt *time.Time
	if !createdAt.IsZero() {
		dbCreatedAt = &createdAt
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (pr_url, tenant_id, created_at)
		VALUES ($1, $2, COALESCE($3, now()))
		ON CONFLICT (pr_url) DO UPDATE SET pr_url = %s.pr_url
		RETURNING id, addressed_comments`, table, table)
	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	if err := dbms.Db.QueryRowContext(dbCtx, query, prURL, tenantID, dbCreatedAt).Scan(&id, &addressed); err != nil {
		return "", nil, fmt.Errorf("failed to find-or-create %s row: %w", table, err)
	}
	return id, addressed, nil
}

// claimOrMarkResolution performs the atomic claim-or-mark and returns the row's
// state and iteration count as they were BEFORE the statement, so the caller can
// tell what happened:
//   - created/needs_followup & under cap -> claimed (row is now 'addressing'); run it.
//   - addressing                          -> a run is in flight; pr_followup_pending was set
//     so that run re-dispatches for this newer signal.
//   - created/needs_followup & at cap      -> nothing changed; caller skips (recovery sweep handles it).
//   - terminal/unresolvable                -> nothing changed; caller skips.
//
// It is a single statement against the live row under a per-row lock (FOR UPDATE),
// so it linearizes with applyFollowupOutcome's finalize: no interleaving can both
// miss the re-dispatch and skip the claim, so a signal arriving any time during a
// run is never lost and the pending flag is never orphaned.
func claimOrMarkResolution(dbms *database.DatabaseManager, tableName, id string, bypassDebounce bool) (claimed bool, oldState string, oldIters int, err error) {
	// The claim to 'addressing' additionally requires that the row was not checked
	// within the webhook debounce window, so a burst of webhook events on one PR
	// collapses to a single followup. A row skipped for debounce (or cap) keeps
	// its state; only a genuine claim flips it to 'addressing'.
	//
	// The debounce window is measured against the caller's clock ($3), not the DB's
	// now(), so the comparison does not depend on app/DB clock synchronisation.
	//
	// This is a LEADING debounce: last_pr_check_at is NOT reset for an event that is
	// ignored *solely because* it fell inside the window (debounceBlocked). That
	// keeps the window anchored to the last claim, so a PR under a continuous event
	// stream still gets a followup once per window instead of being starved until
	// the events stop. For every OTHER outcome — a claim, a mark-pending, or a skip
	// for cap/terminal reasons — last_pr_check_at is advanced to $3, which the cron
	// relies on to space its own re-selection (queryOpenPRResolutionCandidates).
	//
	// bypassDebounce skips only the debounce clause (never the cap/state guards):
	// the internal mid-run re-dispatch uses it because it fires for a signal that
	// genuinely arrived during the just-finished run, and finalize has already
	// stamped last_pr_check_at, which would otherwise debounce it away.
	// $3 MUST be cast: it is an untyped bind parameter, and in `$3 - interval '...'`
	// Postgres infers an untyped operand of `- interval` as `interval` (interval -
	// interval = interval). That makes the whole window expression an interval, so
	// `last_check < <window>` becomes `timestamp < interval` and every claim fails
	// with "operator does not exist: timestamp without time zone < interval",
	// silently killing all cron- and webhook-driven followups. Pinning the type to
	// timestamptz keeps the expression a timestamp.
	window := fmt.Sprintf("$3::timestamptz - interval '%s'", followupWebhookDebounce)
	debounceClause := fmt.Sprintf("AND (cur.last_check IS NULL OR cur.last_check < %s)", window)
	// debounceBlocked is true exactly when the row would have been claimed but for
	// the debounce window — the one case where we preserve last_pr_check_at.
	debounceBlocked := fmt.Sprintf("cur.old_state IN ('created', 'needs_followup') AND cur.old_iters < $2 AND cur.last_check IS NOT NULL AND cur.last_check >= %s", window)
	if bypassDebounce {
		debounceClause = ""
		debounceBlocked = "false"
	}
	query := fmt.Sprintf(`
		WITH cur AS (
			SELECT pr_lifecycle_state AS old_state, pr_iteration_count AS old_iters, last_pr_check_at AS last_check
			FROM %s WHERE id = $1 FOR UPDATE
		),
		upd AS (
			UPDATE %s t SET
				pr_lifecycle_state = CASE
					WHEN cur.old_state IN ('created', 'needs_followup') AND cur.old_iters < $2
						%s
						THEN 'addressing'
					ELSE t.pr_lifecycle_state END,
				pr_followup_pending = CASE
					WHEN cur.old_state = 'addressing' THEN true
					ELSE t.pr_followup_pending END,
				last_pr_check_at = CASE WHEN %s THEN t.last_pr_check_at ELSE $3 END
			FROM cur WHERE t.id = $1
			RETURNING cur.old_state, cur.old_iters, t.pr_lifecycle_state AS new_state
		)
		SELECT old_state, old_iters, new_state FROM upd`, tableName, tableName, debounceClause, debounceBlocked)
	dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
	defer cancel()
	var newState string
	if err = dbms.Db.QueryRowContext(dbCtx, query, id, followupIterationCap, time.Now()).
		Scan(&oldState, &oldIters, &newState); err != nil {
		return false, oldState, oldIters, err
	}
	// A claim happened only when a claimable row actually transitioned to
	// 'addressing' — this folds in the cap and debounce checks so the caller does
	// not re-derive (and mis-derive) the decision.
	claimed = (oldState == "created" || oldState == "needs_followup") && newState == "addressing"
	return claimed, oldState, oldIters, nil
}

// dispatchPRFollowup drives one PR's followup: find (or lazily create) its
// pr_followup row, claim it, and — if claimed — dispatch the agent. candidates
// is every open resolution row that nominates prURL; they're tried in order
// (event_resolution first) until one yields usable metadata and a tenant, so a
// bad/incomplete sibling row doesn't sink a PR a working sibling could still
// service. Only when every candidate fails is the PR marked unresolvable.
func dispatchPRFollowup(ctx *security.RequestContext, dbms *database.DatabaseManager, prURL string, candidates []prResolutionCandidate, redispatchBudget int, bypassDebounce bool, trigger string) error {
	meta, tenantID, createdAt, failReason := resolveFollowupMetadata(candidates)
	if failReason != "" {
		// No candidate yielded a usable pr_url+repo_url+tenant: nothing to claim
		// against. If at least one candidate had a real (if incomplete) pr_url,
		// mark the PR unresolvable so it stops being re-scanned every sweep;
		// otherwise (pathological: prURL itself came from the caller, e.g. a
		// webhook match, but no candidate parses) just log and stop.
		if prURL != "" {
			markPRFollowupUnresolvableByURL(ctx, dbms, prURL, tenantID, createdAt, failReason)
		}
		ctx.GetLogger().Warn("pr_lifecycle: no usable resolution candidate for PR", "pr_url", prURL, "reason", failReason)
		return nil
	}

	// Confirm the PR is still open before doing any work. The GitHub webhook is
	// what normally retires a closed PR (and clears pr_followup_pending), but it
	// is not always delivered in prod, and neither resolution poll covers
	// agent-raised PR rows — the event poll reads resolver_type='User' only, and
	// the recommendation poll never writes pr_followup. Without this a closed PR
	// keeps a 'needs_followup' row and is dispatched (and commented on) every
	// sweep until the 3-day stale exit (#37472). An unknown result (provider
	// unreachable) falls through to the normal path — no worse than before this
	// check. Skipped on the internal mid-run re-dispatch: the state was just
	// checked and the agent just ran.
	if trigger != "redispatch" {
		if state, known := resolvePRState(ctx, dbms, tenantID, meta); known && state.closed {
			ctx.GetLogger().Info("pr_lifecycle: PR is closed on the provider, retiring followup",
				"pr_url", prURL, "merged", state.merged, "trigger", trigger)
			if _, terr := MarkAllPRResolutionsTerminalByURL(ctx, prURL, state.merged); terr != nil {
				ctx.GetLogger().Error("pr_lifecycle: failed to retire closed PR", "pr_url", prURL, "error", terr)
			}
			return nil
		}
	}

	followupID, addressedComments, err := findOrCreatePRFollowup(dbms, prURL, tenantID, createdAt)
	if err != nil {
		return err
	}

	claimed, oldState, oldIters, err := claimOrMarkResolution(dbms, prFollowupTable, followupID, bypassDebounce)
	if err != nil {
		return fmt.Errorf("failed to claim pr_followup row: %w", err)
	}

	if !claimed {
		switch oldState {
		case "addressing":
			ctx.GetLogger().Info("pr_lifecycle: followup in flight, marked pending for re-dispatch", "pr_url", prURL)
		case "created", "needs_followup":
			ctx.GetLogger().Info("pr_lifecycle: skipping, iteration cap reached or within debounce window",
				"pr_url", prURL, "iteration_count", oldIters)
		case "stale":
			// The cron never resurrects a stale row (see resurrectStalePRFollowup
			// doc) — only a genuine webhook signal does, before calling here.
			ctx.GetLogger().Info("pr_lifecycle: skipping stale PR", "pr_url", prURL)
		default:
			ctx.GetLogger().Info("pr_lifecycle: skipping, state not actionable", "pr_url", prURL, "state", oldState)
		}
		return nil
	}

	gitToken, err := getGitTokenForTenant(dbms, tenantID, meta.Provider)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to get git token", "tenant", tenantID, "error", err)
		markPRFollowupUnresolvableByURL(ctx, dbms, prURL, tenantID, createdAt, "missing_git_token")
		return fmt.Errorf("failed to get git token: %w", err)
	}

	ctx.GetLogger().Info("pr_lifecycle: triggering followup for PR",
		"pr_url", prURL, "current_iteration_count", oldIters)
	common.MetricsPRFollowupDispatch(ctx.GetContext(), prFollowupTable, trigger)

	chatRequest := buildPRFollowupChatRequest(meta, gitToken,
		fmt.Sprintf("Follow up on PR %s — address CI failures and review comments", meta.PRURL),
		addressedComments)

	runPRFollowupAgent(ctx, tenantID, chatRequest, "pr_url", prURL, func(tenantCtx *security.RequestContext, response *llm.ChatCompletionResponse, err error) {
		var outcome followupOutcome
		if err != nil {
			tenantCtx.GetLogger().Error("pr_lifecycle: followup failed", "pr_url", prURL, "error", err)
			outcome = followupOutcomeFailed
		} else {
			outcome = classifyFollowupOutcome(response.Response)
			tenantCtx.GetLogger().Info("pr_lifecycle: followup completed",
				"pr_url", prURL,
				"response_status", response.Status,
				"outcome", outcome.name,
				"unresolved", outcome.unresolved,
				"base_synced", outcome.baseSynced,
				"new_state", outcome.newState)
		}
		common.MetricsPRFollowupOutcome(tenantCtx.GetContext(), outcome.name, outcome.unresolved, outcome.baseSynced)

		// Finalize atomically and learn whether a new actionable signal arrived
		// while this run was in flight (pr_followup_pending). If so, re-dispatch
		// once for it rather than waiting up to a full cron cooldown. Bounded by
		// redispatchBudget so a continuous-signal source can't loop forever.
		wasPending := applyFollowupOutcome(tenantCtx, dbms, prFollowupTable, followupID, outcome)
		if wasPending && redispatchBudget > 0 {
			tenantCtx.GetLogger().Info("pr_lifecycle: signal arrived during run, re-dispatching",
				"pr_url", prURL, "remaining_budget", redispatchBudget-1)
			// bypassDebounce: this fires for a real signal that arrived mid-run;
			// finalize just stamped last_pr_check_at=now(), which would otherwise
			// debounce the re-dispatch away. Bounded by redispatchBudget. The
			// candidates/metadata don't change mid-run, so no re-fetch is needed.
			if perr := dispatchPRFollowup(tenantCtx, dbms, prURL, candidates, redispatchBudget-1, true, "redispatch"); perr != nil {
				tenantCtx.GetLogger().Error("pr_lifecycle: re-dispatch failed", "pr_url", prURL, "error", perr)
			}
		}
	})

	return nil
}

// resolveFollowupMetadata tries each candidate in order until one yields a
// parseable, complete prMetadata and a resolvable tenant. Returns a non-empty
// failReason ("bad_metadata", "missing_metadata", or "missing_tenant") only
// when every candidate failed — matching what each of those reasons meant in
// the pre-#36457 single-row code, just evaluated across the whole group
// before giving up instead of on the first row alone.
func resolveFollowupMetadata(candidates []prResolutionCandidate) (meta prMetadata, tenantID string, createdAt time.Time, failReason string) {
	failReason = "missing_metadata"
	for _, c := range candidates {
		var m prMetadata
		if err := json.Unmarshal(c.Data, &m); err != nil {
			failReason = "bad_metadata"
			continue
		}
		if m.PRURL == "" || m.RepoURL == "" {
			failReason = "missing_metadata"
			continue
		}
		// For conversation-originated PRs (Slack flow), the events LEFT JOIN
		// returns empty tenant because event_id holds a conversation UUID, not
		// an event UUID. Fall back to tenant_id stored in the PR metadata by
		// the code_analyzer agent.
		tid := c.TenantID
		if tid == "" && m.TenantID != "" {
			tid = m.TenantID
		}
		if tid == "" {
			failReason = "missing_tenant"
			continue
		}
		return m, tid, c.CreatedAt, ""
	}
	return prMetadata{}, "", time.Time{}, failReason
}

// applyFollowupOutcome writes the run's terminal effect on the row and returns
// whether a new actionable signal arrived mid-run (pr_followup_pending was set).
// It does both in ONE row-locked statement so it linearizes with a concurrent
// claim-or-mark (claimOrMarkResolution): either the mark commits first (we read
// was_pending=true and the caller re-dispatches) or this finalize commits first
// (the concurrent dispatch then sees a non-addressing state and claims directly)
// — no interleaving loses the signal or orphans the flag. The terminal guard
// stops a PR-close that landed mid-run from being overwritten back to an open
// state.
func applyFollowupOutcome(ctx *security.RequestContext, dbms *database.DatabaseManager, tableName, id string, outcome followupOutcome) (wasPending bool) {
	// counterExpr is built from our own constant outcome (delta 0/1), never user
	// input — no injection surface.
	counterExpr := fmt.Sprintf("pr_iteration_count + %d", outcome.counterDelta)
	if outcome.resetCounter {
		counterExpr = "0"
	}
	// addressed_comments accumulates in the SAME locked statement rather than a
	// follow-up UPDATE, so a concurrent claim can never interleave between the
	// finalize and the record and lose entries (#36865). The union is by
	// (source, comment_id) with the newest addressed_at winning, so a comment
	// answered again in a later run updates its action instead of duplicating.
	// $3 is a jsonb array; an empty array makes the whole expression a no-op.
	const addressedExpr = `(
		SELECT COALESCE(jsonb_agg(s.v ORDER BY s.v->>'addressed_at'), '[]'::jsonb) FROM (
			SELECT DISTINCT ON (e->>'source', e->>'comment_id') e AS v
			FROM jsonb_array_elements(COALESCE(t.addressed_comments, '[]'::jsonb) || $3::jsonb) e
			ORDER BY e->>'source', e->>'comment_id', e->>'addressed_at' DESC
		) s
	)`
	query := fmt.Sprintf(`
		WITH cur AS (
			SELECT pr_followup_pending AS was_pending, pr_lifecycle_state AS st
			FROM %s WHERE id = $1 FOR UPDATE
		),
		upd AS (
			UPDATE %s t SET
				pr_lifecycle_state = CASE WHEN cur.st IN ('closed', 'merged', 'unresolvable')
					THEN t.pr_lifecycle_state ELSE $2 END,
				pr_iteration_count = CASE WHEN cur.st IN ('closed', 'merged', 'unresolvable')
					THEN t.pr_iteration_count ELSE %s END,
				addressed_comments = CASE WHEN cur.st IN ('closed', 'merged', 'unresolvable')
					THEN t.addressed_comments ELSE %s END,
				last_pr_check_at = now(),
				pr_followup_pending = false
			FROM cur WHERE t.id = $1
			RETURNING cur.was_pending
		)
		SELECT was_pending FROM upd`, tableName, tableName, counterExpr, addressedExpr)
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), prDBOpTimeout)
	defer cancel()
	if err := dbms.Db.QueryRowContext(dbCtx, query, id, outcome.newState, outcome.addressedCommentsJSON()).
		Scan(&wasPending); err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to apply outcome",
			"id", id, "outcome", outcome.name, "error", err)
		return false
	}
	return wasPending
}

// ProcessOpenPRFollowup dispatches a followup for a single PR, identified by
// its URL. Used by the GitHub webhook handler (api/public_webhooks.go) to
// react to PR events without waiting for the next cron tick. The caller is
// expected to have already confirmed ownership via HasOpenPRResolutionForURL.
func ProcessOpenPRFollowup(ctx *security.RequestContext, prURL string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	candidates, err := fetchResolutionCandidatesByURL(dbms, prURL)
	if err != nil {
		return fmt.Errorf("failed to fetch resolution candidates for %s: %w", prURL, err)
	}
	if len(candidates) == 0 {
		// Not our PR (or every resolution row for it is already terminal) —
		// nothing to do, and nothing should be lazily created for it.
		return nil
	}

	// Webhook-only resurrection: a real PR signal (this path is reached only
	// from the GitHub webhook, never the cron) overrides the cron's staleness.
	// The cron never resurrects — it only re-stales — so blind hourly polling
	// stays suppressed while genuine activity always gets a followup.
	if err := resurrectStalePRFollowupByURL(ctx, dbms, prURL); err != nil {
		return fmt.Errorf("failed to resurrect stale pr_followup for %s: %w", prURL, err)
	}

	// Webhook entry: debounce a burst of events on the same PR into one followup.
	return dispatchPRFollowup(ctx, dbms, prURL, candidates, maxRedispatchChain, false, "webhook")
}

// resurrectStalePRFollowupByURL returns a cron-retired ('stale') pr_followup
// row to active followup with a fresh iteration budget. Called only from the
// webhook path: a genuine PR signal overrides the age/iteration staleness the
// cron applied. Guarded on the current state still being 'stale' so a
// concurrent terminal (merge/close) is never overwritten back to active. A
// no-op (not an error) when no pr_followup row exists yet for this URL —
// dispatchPRFollowup will lazily create one in the normal 'created' state.
func resurrectStalePRFollowupByURL(ctx *security.RequestContext, dbms *database.DatabaseManager, prURL string) error {
	n, err := resurrectStalePRFollowupInTable(ctx, dbms, prFollowupTable, prURL)
	if err != nil {
		return err
	}
	if n > 0 {
		ctx.GetLogger().Info("pr_lifecycle: resurrected stale pr_followup on webhook signal", "pr_url", prURL)
	}
	return nil
}

// resurrectStalePRFollowupInTable is the table-parameterized core of
// resurrectStalePRFollowupByURL, split out so the SQL can be exercised
// against a throwaway table in tests. Returns the number of rows resurrected.
func resurrectStalePRFollowupInTable(ctx *security.RequestContext, dbms *database.DatabaseManager, table, prURL string) (int64, error) {
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), prDBOpTimeout)
	defer cancel()
	// created_at is bumped to now(): markStaleResolutions retires purely on
	// created_at age (#37472), so without this a webhook that resurrects a
	// >3-day-old row would see it re-retired on the very next cron sweep. The
	// column now means "when the current followup cycle began", not "when the PR
	// was first seen" — a deliberate restart resets that clock.
	res, err := dbms.Db.ExecContext(dbCtx,
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = 'needs_followup',
			pr_iteration_count = 0, pr_followup_pending = false, created_at = now()
			WHERE pr_url = $1 AND pr_lifecycle_state = 'stale'`, table),
		prURL)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ResetPRFollowupBudget resets a PR's review-followup iteration budget back
// to a fresh start. Called after a value refresh (#34959) lands new numbers
// on an already-open PR: review comments raised against the previous numbers
// may no longer apply, so the review-followup budget should start again
// rather than stay consumed by a rewrite the reviewer hasn't seen yet. A
// no-op if the PR is already terminal (merged/closed/unresolvable) — the
// value-refresh caller already guards its own resolution-row write the same
// way, so this mirrors that guard rather than resurrecting a dead PR. Also a
// no-op if no pr_followup row exists yet — a fresh one is lazily created at
// pr_iteration_count=0/'created' already, so there's nothing to reset.
//
// created_at is bumped to now() for the same reason as resurrectStalePRFollowup:
// markStaleResolutions retires on created_at age alone (#37472), so a refresh
// landing on a PR more than 3 days old would otherwise get its fresh budget
// immediately retired on the next cron sweep.
func ResetPRFollowupBudget(ctx AccountAdapterContext, prURL string) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	dbCtx, cancel := context.WithTimeout(ctx.GetContext(), prDBOpTimeout)
	defer cancel()
	_, err = dbms.Db.ExecContext(dbCtx,
		fmt.Sprintf(`UPDATE %s SET pr_iteration_count = 0, pr_lifecycle_state = 'created',
			pr_followup_pending = false, created_at = now(), updated_at = now()
			WHERE pr_url = $1 AND pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable')`, prFollowupTable),
		prURL)
	return err
}

// prTerminalFields returns the (pr_lifecycle_state, status, status_message)
// triple a PR-terminal event maps to. A merge means the fix landed → Success;
// a close without merge means the PR was abandoned → Failed. Both also flip the
// user-facing `status` column (not just pr_lifecycle_state) so the resolution
// list stops showing "In Progress" once the PR reaches a terminal state.
func prTerminalFields(merged bool) (state, status, msg string) {
	if merged {
		return "merged", string(RecommendationResolutionStatusSuccess), "PR merged — followup complete"
	}
	return "closed", string(RecommendationResolutionStatusFailed), "PR closed without merge — no further followup"
}

// MarkAllPRResolutionsTerminalByURL retires every open resolution whose PR has
// just closed or merged, across BOTH event_resolution and recommendation_resolution,
// AND the pr_followup row for that URL. A single PR can be tracked by rows in
// both resolution tables (e.g. an event-driven apply and an AutoOptimize
// recommendation apply that converge on the same PR), and a merge/close
// terminates all of them — so we match on pr_url rather than a single row id.
// findOpenPRResolution (recommendation/service.go) reads a resolution row's
// OWN pr_lifecycle_state for its duplicate-PR dedup check, so that column
// must keep reflecting terminal state even though the followup loop itself no
// longer reads it. Returns the number of resolution rows transitioned.
//
// Only open/active rows are transitioned; the `pr_lifecycle_state IN (...)`
// guard leaves terminal/unresolvable rows untouched, so a close event landing
// mid-run lets the in-flight finalize free to no-op (it preserves terminal
// states; see applyFollowupOutcome). Besides pr_lifecycle_state, the
// user-facing `status` column is set (Success on merge, Failed on close) so
// the UI reflects the terminal outcome.
func MarkAllPRResolutionsTerminalByURL(ctx *security.RequestContext, prURL string, merged bool) (int64, error) {
	if prURL == "" {
		return 0, nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, fmt.Errorf("failed to get database connection: %w", err)
	}
	total, err := markPRResolutionsTerminalByURL(ctx, dbms, prResolutionTables, prURL, merged)

	// Also flip the pr_followup row so an in-flight run's finalize sees the
	// terminal guard (applyFollowupOutcome) and doesn't resurrect it. A plain
	// UPDATE, not an upsert: if no pr_followup row exists yet, there's nothing
	// in-flight to guard.
	state, _, msg := prTerminalFields(merged)
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), prDBOpTimeout)
	defer cancel()
	if _, ferr := dbms.Db.ExecContext(dbCtx,
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, status_message = $2,
			pr_followup_pending = false, last_pr_check_at = now()
			WHERE pr_url = $3 AND pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable')`, prFollowupTable),
		state, msg, prURL); ferr != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to mark pr_followup terminal", "pr_url", prURL, "error", ferr)
	}

	return total, err
}

// prResolutionTables are the two tables that can carry an agent-PR resolution row.
var prResolutionTables = []string{"event_resolution", "recommendation_resolution"}

// markPRResolutionsTerminalByURL is the table-parameterized core of
// MarkAllPRResolutionsTerminalByURL, split out so the SQL can be exercised
// against throwaway tables in tests (the real tables carry FKs/fixtures).
func markPRResolutionsTerminalByURL(ctx *security.RequestContext, dbms *database.DatabaseManager, tables []string, prURL string, merged bool) (int64, error) {
	state, status, msg := prTerminalFields(merged)

	var total int64
	var settleErrs error
	for _, tableName := range tables {
		// Under the coordinator, recommendation lifecycle status is not this
		// reconciler's to write: the UPDATE here keeps only the PR-machinery
		// columns, and the status transition (plus the recommendation
		// projection) is requested per row — a duplicate webhook/poll delivery
		// lands as a recorded no-op instead of an overwrite. event_resolution
		// stays on the legacy path; the coordinator governs recommendations only.
		if tableName == "recommendation_resolution" {
			outcome := models.RecommendationResolutionStatusFailed
			if merged {
				outcome = models.RecommendationResolutionStatusSuccess
			}
			dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
			ids := []string{}
			err := dbms.Db.SelectContext(dbCtx, &ids,
				`UPDATE recommendation_resolution SET pr_lifecycle_state = $1,
					pr_followup_pending = false, last_pr_check_at = $2
					WHERE data->>'pr_url' = $3 AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing', 'stale')
					RETURNING id`,
				state, time.Now(), prURL)
			cancel()
			if err != nil {
				return total, fmt.Errorf("failed to mark recommendation_resolution rows terminal: %w", err)
			}
			for _, id := range ids {
				// A failure here leaves pr_lifecycle_state terminal with the status
				// still InProgress; the resolution poll settles such rows on its
				// next tick, so the pair converges rather than wedges. Errors are
				// joined instead of short-circuiting, so every row still gets its
				// settle attempt and no table's marking is skipped.
				if _, err := coordinator.SettleResolution(ctx, id, outcome, msg, coordinator.SourceWebhook); err != nil {
					settleErrs = errors.Join(settleErrs, fmt.Errorf("failed to settle recommendation resolution %s: %w", id, err))
					continue
				}
				total++
			}
			continue
		}
		dbCtx, cancel := context.WithTimeout(context.Background(), prDBOpTimeout)
		res, execErr := dbms.Db.ExecContext(dbCtx,
			fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, status = $2, status_message = $3,
				pr_followup_pending = false, last_pr_check_at = $4
				WHERE data->>'pr_url' = $5 AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing', 'stale')`, tableName),
			state, status, msg, time.Now(), prURL)
		cancel()
		if execErr != nil {
			return total, fmt.Errorf("failed to mark %s rows terminal: %w", tableName, execErr)
		}
		if n, raErr := res.RowsAffected(); raErr == nil {
			total += n
		}
	}
	ctx.GetLogger().Info("pr_lifecycle: marked PR resolutions terminal",
		"pr_url", prURL, "merged", merged, "state", state, "status", status, "rows", total)
	return total, settleErrs
}

// markResolutionRowUnresolvable retires a single resolution row whose own
// data is unusable (unparseable, or missing pr_url/repo_url) — a defect in
// that specific row, discovered before any pr_url is known, so there is no
// pr_followup entity to involve. Without this the row would stay at
// pr_lifecycle_state='created' and be re-selected by every cron sweep forever.
func markResolutionRowUnresolvable(ctx *security.RequestContext, dbms *database.DatabaseManager, c prResolutionCandidate, reason string) {
	_, err := dbms.Db.ExecContext(ctx.GetContext(),
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = $1, pr_iteration_count = $2, status_message = $3, last_pr_check_at = $4 WHERE id = $5`, c.TableName),
		"unresolvable", followupIterationCap, "pr_lifecycle followup unresolvable: "+reason, time.Now(), c.ID)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to mark resolution row unresolvable",
			"id", c.ID, "table", c.TableName, "reason", reason, "error", err)
	}
}

// markPRFollowupUnresolvableByURL retires the pr_followup row for a PR whose
// resolution candidates all have a usable pr_url but can't otherwise be
// serviced (no resolvable tenant, no git token). Marks pr_followup unresolvable
// AND fans 'unresolvable' out to every resolution row for the URL — otherwise
// findOpenPRResolution (recommendation/service.go) would see those rows stuck
// at 'created' forever and never let autopilot raise a fresh PR for a
// permanently-abandoned one.
func markPRFollowupUnresolvableByURL(ctx *security.RequestContext, dbms *database.DatabaseManager, prURL, tenantID string, createdAt time.Time, reason string) {
	msg := "pr_lifecycle followup unresolvable: " + reason
	followupID, _, err := findOrCreatePRFollowup(dbms, prURL, tenantID, createdAt)
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to find-or-create pr_followup for unresolvable mark", "pr_url", prURL, "error", err)
		return
	}
	// Guarded on the row not already being terminal: a PR-close/merge webhook
	// landing concurrently (MarkAllPRResolutionsTerminalByURL) may have already
	// flipped this row to 'merged'/'closed' — without the guard this UPDATE
	// would clobber that back to 'unresolvable' with a misleading message, and
	// discard a pending flag set by a genuine concurrent re-dispatch signal.
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), prDBOpTimeout)
	_, err = dbms.Db.ExecContext(dbCtx,
		fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = 'unresolvable', pr_iteration_count = $1,
			status_message = $2, last_pr_check_at = now(), pr_followup_pending = false
			WHERE id = $3 AND pr_lifecycle_state NOT IN ('merged', 'closed', 'unresolvable')`, prFollowupTable),
		followupIterationCap, msg, followupID)
	cancel()
	if err != nil {
		ctx.GetLogger().Error("pr_lifecycle: failed to mark pr_followup unresolvable", "pr_url", prURL, "reason", reason, "error", err)
		return
	}

	for _, tableName := range prResolutionTables {
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), prDBOpTimeout)
		_, ferr := dbms.Db.ExecContext(dbCtx,
			fmt.Sprintf(`UPDATE %s SET pr_lifecycle_state = 'unresolvable', status_message = $1,
				pr_followup_pending = false, last_pr_check_at = now()
				WHERE data->>'pr_url' = $2 AND pr_lifecycle_state IN ('created', 'needs_followup', 'addressing', 'stale')`, tableName),
			msg, prURL)
		cancel()
		if ferr != nil {
			ctx.GetLogger().Error("pr_lifecycle: failed to fan unresolvable out to resolution rows",
				"table", tableName, "pr_url", prURL, "error", ferr)
		}
	}
}

// buildPRFollowupChatRequest builds the envelope that re-runs the code agent
// against an already-open pull request. The agent (invoked via its
// "@agent_code_2" back-compat alias) unmarshals it into CodeAgent2Request and,
// because followup is set with a pr_url, works on that pull request's existing
// branch rather than opening a new one.
//
// prompt is the only thing that varies between callers: the lifecycle cron asks
// it to address CI failures and review comments, while a value refresh asks it to
// apply changed rightsizing numbers (#34959). Keeping one builder means both get
// the same branch fallback and credential handling.
// addressedComments is the set a previous run already answered (#36865),
// forwarded so code-analysis — which has no metastore access — can use it as a
// fallback skip source alongside GitHub's own resolved-thread state. Omitted
// from the payload when empty, so nothing changes for a PR with no history.
func buildPRFollowupChatRequest(meta prMetadata, gitToken, prompt string, addressedComments json.RawMessage) llm.ConversationApiRequest {
	prBranch := meta.PRBranch
	if prBranch == "" {
		prBranch = meta.Branch
	}

	followupQuery := map[string]any{
		"query":     prompt,
		"followup":  true,
		"pr_url":    meta.PRURL,
		"git_repo":  meta.RepoURL,
		"pr_branch": prBranch,
		"git_token": gitToken,
	}
	// json.RawMessage marshals through verbatim, so the array is embedded as an
	// array rather than a quoted string. Guarded on a non-empty array so a fresh
	// row's '[]' does not add noise to every request. Trimmed before comparing
	// so the check doesn't depend on the driver/column always returning compact
	// jsonb — cheap, and removes that assumption entirely.
	trimmed := strings.TrimSpace(string(addressedComments))
	if trimmed != "" && trimmed != "[]" && trimmed != "null" {
		followupQuery["addressed_comments"] = addressedComments
	}
	followupQueryJSON, _ := json.Marshal(followupQuery)

	return llm.ConversationApiRequest{
		Query:     "@agent_code_2 " + string(followupQueryJSON),
		Source:    "pr_lifecycle",
		AccountId: meta.AccountID,
	}
}

// runPRFollowupAgent runs a pull-request followup conversation in the background
// and hands the result to onDone.
//
// It owns the parts every caller needs identically: a tenant-scoped context
// (llm-server requires x-tenant-id, and the cron's own context has no tenant), a
// 35-minute bound so a stuck llm-server cannot leak the goroutine — slightly
// above executeFollowup's 30-minute poll so a legitimate run is never preempted —
// and panic recovery, because many of these are in flight at once and one bad
// followup must not take the process down.
func runPRFollowupAgent(ctx *security.RequestContext, tenantID string, chatRequest llm.ConversationApiRequest, logKey string, logID any, onDone func(*security.RequestContext, *llm.ChatCompletionResponse, error)) {
	tenantCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())

	go func() {
		defer func() {
			if r := recover(); r != nil {
				tenantCtx.GetLogger().Error("pr_lifecycle: panic in followup goroutine", logKey, logID, "recover", r)
			}
		}()

		// Built on context.Background() because the caller's ctx may be a request
		// context — this background work must not die when the handler returns.
		bgCtx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
		defer cancel()
		boundedCtx := security.NewRequestContext(bgCtx, tenantCtx.GetSecurityContext(),
			tenantCtx.GetLogger(), tenantCtx.GetTracer(), tenantCtx.GetMeter())

		response, err := llm.ChatCompletion(boundedCtx, chatRequest)
		onDone(tenantCtx, response, err)
	}()
}

// followupOutcome encapsulates how a single followup result should mutate the
// resolution row. Tri-state because the iteration counter must distinguish:
//
//	success — agent committed/replied; reset count, mark created
//	failed  — real failure or unparseable response; bump count
//	no_op   — nothing actionable was found, or the planner produced no
//	          observable change. Do NOT bump count: the cron must be free to
//	          retry when a reviewer eventually shows up. Without this, a PR
//	          reviewed >2h after creation gets capped before signal arrives.
type followupOutcome struct {
	name     string
	newState string
	// counterDelta is added to pr_iteration_count. 0 = no-op, 1 = failed,
	// -<huge> = success-reset (we clamp to 0 in the UPDATE).
	counterDelta int
	resetCounter bool
	// unresolved distinguishes a no_op where the agent had actionable input
	// (review comments / CI failure) but couldn't apply a change, from a no_op
	// where there was genuinely nothing to do. It is observability-only — it does
	// NOT change counterDelta/newState — surfaced via the outcome metric so the
	// otherwise-invisible "97% no_op is mostly couldn't-apply" churn is measurable.
	unresolved bool
	// addressed carries the comments this run actually answered on the PR, to be
	// merged into pr_followup.addressed_comments by applyFollowupOutcome (#36865).
	// Populated from the agent's additive `addressed_comments` response field;
	// nil for an older agent that does not emit it, which makes the merge a no-op.
	addressed []models.AddressedComment
	// baseSynced marks a run that merged the PR's base branch into the PR branch
	// and pushed it (#36864). Like unresolved, observability-only: a base sync is
	// a real change to the PR and is classified "success" on that basis, so this
	// only tags the metric. Most base syncs never invoke the planner at all, so
	// without this tag they would be indistinguishable from real comment/CI fixes.
	baseSynced bool
}

// addressedCommentsJSON renders the run's addressed comments as the jsonb
// argument applyFollowupOutcome merges. Always a valid JSON array — an empty
// one when there is nothing to record, which makes the merge expression a
// no-op rather than a NULL that would wipe the column.
func (o followupOutcome) addressedCommentsJSON() []byte {
	if len(o.addressed) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(o.addressed)
	if err != nil {
		return []byte("[]")
	}
	return b
}

var (
	followupOutcomeSuccess = followupOutcome{name: "success", newState: "created", counterDelta: 0, resetCounter: true}
	followupOutcomeFailed  = followupOutcome{name: "failed", newState: "needs_followup", counterDelta: 1}
	followupOutcomeNoOp    = followupOutcome{name: "no_op", newState: "needs_followup", counterDelta: 0}
)

// classifyFollowupOutcome reads the agent response and decides how to advance
// the resolution row. The contract with code-analysis (see
// performFollowupAnalysis in agentic_analyze.go) is:
//
//	execution_status="success" — agent committed, edited PR metadata, or
//	                             posted a verified comment reply
//	execution_status="no_op"   — nothing actionable found, or planner ran
//	                             but produced no observable change
//	execution_status="failed"  — agent attempted work and explicitly failed
//
// Additive, backward-compatible field: followup_unresolved=true on a no_op means
// the agent DID have actionable input (review comments / CI failure) but couldn't
// apply a change (the non-convergence path). It is observability-only here — the
// outcome is still no_op (counter-neutral); we only tag the metric. An older
// code-analysis that never emits the field simply reports unresolved=false.
//
// Anything else (parse failure, missing field, unexpected value) collapses to
// "failed" so the cron retries rather than mistaking noise for a fix.
//
// Historical note: an earlier implementation read agent_resp["success"], a key
// that the followup code path has never set. Every real run was therefore
// classified as a failure, the iteration counter incremented, and PRs hit the
// cap before any reviewer feedback could arrive. The execution_status field
// is what the followup handler actually emits; reading it here closes that
// gap. Do not revert to the bare "success" key without adding a producer.
// parseAddressedComments converts the agent's additive `addressed_comments`
// response field into typed entries (#36865).
//
// Deliberately lenient: this is a bookkeeping record, never a correctness gate,
// so a malformed or absent field yields nil and the merge becomes a no-op
// rather than failing an otherwise-successful run. Entries missing a source, a
// comment id, or an action are dropped individually — a partial record is worth
// more than none, and a half-identified entry could not be matched back to a
// comment anyway. A missing addressed_at is stamped now() so the merge's
// newest-wins ordering still has something to sort on.
func parseAddressedComments(raw any) []models.AddressedComment {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []models.AddressedComment
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		source, _ := m["source"].(string)
		action, _ := m["action"].(string)
		// JSON numbers decode as float64 through map[string]any, which is the
		// normal case here (our own agent marshals an int64 field). Also accept a
		// string, in case this ever reads a hand-built or LLM-composed payload
		// that quoted the id — well under 2^53 either way, so the round-trip is
		// exact.
		var commentID int64
		switch id := m["comment_id"].(type) {
		case float64:
			commentID = int64(id)
		case string:
			commentID, _ = strconv.ParseInt(id, 10, 64)
		}
		if source == "" || action == "" || commentID <= 0 {
			continue
		}
		entry := models.AddressedComment{
			Source:    source,
			CommentID: commentID,
			Action:    action,
		}
		if ts, ok := m["addressed_at"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				entry.AddressedAt = parsed.UTC()
			}
		}
		if entry.AddressedAt.IsZero() {
			entry.AddressedAt = time.Now().UTC()
		}
		out = append(out, entry)
	}
	return out
}

func classifyFollowupOutcome(responses []string) followupOutcome {
	if len(responses) == 0 {
		return followupOutcomeFailed
	}
	var agentResp map[string]any
	if err := common.UnmarshalJson([]byte(responses[0]), &agentResp); err != nil {
		return followupOutcomeFailed
	}
	// addressed_comments is additive and may be absent (older agent). Parsed
	// outside the status switch: a run can answer comments and still finish as a
	// no_op (every reply skipped as unverified), and that record is worth keeping
	// either way.
	addressed := parseAddressedComments(agentResp["addressed_comments"])
	// followup_base_synced is additive and may be absent (older agent). It is
	// read outside the status switch because a base sync can be the run's only
	// work — a "success" that never ran the planner — or can ride along with a
	// normal comment/CI fix.
	baseSynced, _ := agentResp["followup_base_synced"].(bool)

	status, _ := agentResp["execution_status"].(string)
	switch status {
	case "success", "partial_success":
		outcome := followupOutcomeSuccess
		outcome.addressed = addressed
		outcome.baseSynced = baseSynced
		return outcome
	case "no_op":
		outcome := followupOutcomeNoOp
		outcome.addressed = addressed
		outcome.baseSynced = baseSynced
		// followup_unresolved is additive and may be absent (older agent) — a
		// missing/false value keeps the plain no_op semantics.
		if unresolved, ok := agentResp["followup_unresolved"].(bool); ok && unresolved {
			outcome.unresolved = true
		}
		return outcome
	default:
		// Backwards compatibility: some legacy code paths emit a top-level
		// `success: true/false` instead of execution_status. Honour it when
		// present so we don't break older agents during a rolling deploy.
		if success, ok := agentResp["success"].(bool); ok && success {
			return followupOutcomeSuccess
		}
		return followupOutcomeFailed
	}
}

// observedPRState is what a direct provider lookup reports about a pull request
// (GitHub) or merge request (GitLab): closed at all, and — if closed — merged.
type observedPRState struct {
	closed bool
	merged bool
}

// resolvePRState asks the provider directly whether a PR/MR is still open, so
// dispatchPRFollowup can retire a PR that was closed while the GitHub webhook —
// the signal that normally does this — was not being delivered (#37472).
//
// Returns known=false (never a hard error) when the state cannot be determined:
// no integration config, a GitHub App token failure, a network error, a non-200
// response, or an unparseable URL. The caller treats "unknown" as "still open"
// and proceeds exactly as it did before this check existed, so a provider
// outage degrades to the pre-existing behaviour rather than stalling followups.
func resolvePRState(ctx *security.RequestContext, dbms *database.DatabaseManager, tenantID string, meta prMetadata) (state observedPRState, known bool) {
	integrationType := "github"
	if strings.EqualFold(meta.Provider, "gitlab") {
		integrationType = "gitlab"
	}
	cfg, err := loadGitIntegrationConfig(dbms, tenantID, integrationType)
	if err != nil {
		ctx.GetLogger().Warn("pr_lifecycle: cannot verify PR state — no integration config",
			"pr_url", meta.PRURL, "provider", integrationType, "error", err)
		return observedPRState{}, false
	}
	if integrationType == "gitlab" {
		return resolveGitLabMRState(ctx, meta, cfg)
	}
	return resolveGitHubPRState(ctx, meta, cfg)
}

// loadGitIntegrationConfig returns the decrypted integration_config_values
// (keyed by config name: url, username, password, auth_type, …) for the
// tenant's first enabled integration of the given type. Keyed by integration
// type, not by provider name, because that is all the followup path knows —
// prMetadata carries "github"/"gitlab", never the integration's own name.
func loadGitIntegrationConfig(dbms *database.DatabaseManager, tenantID, integrationType string) (map[string]string, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant ID is empty")
	}
	var integrationID string
	if err := dbms.Db.QueryRowx(`
		SELECT i.id::text FROM integrations i
		WHERE i.tenant_id = $1 AND i.type = $2 AND i.status = 'enabled'
		LIMIT 1`, tenantID, integrationType).Scan(&integrationID); err != nil {
		return nil, fmt.Errorf("no enabled %s integration for tenant %s: %w", integrationType, tenantID, err)
	}
	rows, err := dbms.Db.Queryx(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values WHERE integration_id = $1`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("querying integration_config_values (%s): %w", integrationID, err)
	}
	defer func() { _ = rows.Close() }()
	configs := make(map[string]string)
	for rows.Next() {
		var name, value string
		var encrypted bool
		if err := rows.Scan(&name, &value, &encrypted); err != nil {
			return nil, err
		}
		if encrypted && value != "" {
			decrypted, derr := common.Decrypt(value)
			if derr != nil {
				return nil, fmt.Errorf("decrypt config %q: %w", name, derr)
			}
			value = decrypted
		}
		configs[name] = value
	}
	return configs, rows.Err()
}

// resolveGitHubPRState does GET /repos/{org}/{repo}/pulls/{n} and reads state +
// merged_at. Matches the existing GetRecommendationResolutionStatus path: the
// public api.github.com host (GHE PR-status is not supported there either), and
// an installation token minted from the App installation id for auth_type
// "application".
func resolveGitHubPRState(ctx *security.RequestContext, meta prMetadata, cfg map[string]string) (observedPRState, bool) {
	org, repo, number := prCoordinatesFromMeta(meta)
	if org == "" || repo == "" || number == "" {
		return observedPRState{}, false
	}
	token := cfg["password"]
	if cfg["auth_type"] == "application" {
		// password is the GitHub App installation id, not a usable API token.
		tokCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), 30*time.Second)
		appToken, terr := common.GetGithubAppInstallationToken(tokCtx, token)
		cancel()
		if terr != nil {
			ctx.GetLogger().Warn("pr_lifecycle: cannot verify PR state — GitHub App token failed",
				"pr_url", meta.PRURL, "error", terr)
			return observedPRState{}, false
		}
		token = appToken
	}
	body, status, ok := httpGetJSON(ctx,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%s", org, repo, number),
		map[string]string{"Authorization": "Bearer " + token, "Accept": "application/vnd.github+json"})
	if !ok || status != 200 {
		ctx.GetLogger().Warn("pr_lifecycle: PR state lookup did not return 200", "pr_url", meta.PRURL, "status", status)
		return observedPRState{}, false
	}
	return githubPRStateFromBody(body), true
}

// resolveGitLabMRState does GET /projects/{path}/merge_requests/{iid} and maps
// the GitLab MR state (opened / closed / merged / locked) onto observedPRState.
func resolveGitLabMRState(ctx *security.RequestContext, meta prMetadata, cfg map[string]string) (observedPRState, bool) {
	base := cfg["url"]
	if base == "" {
		base = "https://gitlab.com"
	}
	projectPath, mrIID, err := parseGitLabMRURL(meta.PRURL, base)
	if err != nil {
		ctx.GetLogger().Warn("pr_lifecycle: cannot verify MR state — unparseable MR URL", "mr_url", meta.PRURL, "error", err)
		return observedPRState{}, false
	}
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	body, status, ok := httpGetJSON(ctx,
		fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s", strings.TrimSuffix(base, "/"), encodedPath, mrIID),
		map[string]string{"PRIVATE-TOKEN": cfg["password"]})
	if !ok || status != 200 {
		ctx.GetLogger().Warn("pr_lifecycle: MR state lookup did not return 200", "mr_url", meta.PRURL, "status", status)
		return observedPRState{}, false
	}
	return gitlabMRStateFromBody(body), true
}

// githubPRStateFromBody / gitlabMRStateFromBody are the pure response→state maps,
// split out so the classification is unit-testable without a live provider.
func githubPRStateFromBody(body map[string]any) observedPRState {
	st, _ := body["state"].(string)
	return observedPRState{closed: st == "closed", merged: body["merged_at"] != nil}
}

func gitlabMRStateFromBody(body map[string]any) observedPRState {
	switch st, _ := body["state"].(string); st {
	case "merged":
		return observedPRState{closed: true, merged: true}
	case "closed":
		return observedPRState{closed: true}
	default: // opened, locked, or anything unexpected → treat as still open
		return observedPRState{}
	}
}

// httpGetJSON is a small GET-and-decode-object helper for the provider lookups.
// Any failure returns ok=false; the status code is still reported when the
// request itself completed.
func httpGetJSON(ctx *security.RequestContext, url string, headers map[string]string) (body map[string]any, status int, ok bool) {
	resp, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		ctx.GetLogger().Warn("pr_lifecycle: provider GET failed", "url", url, "error", err)
		return nil, 0, false
	}
	defer func() {
		// Drain before close so the keep-alive connection can be reused even on
		// the io.ReadAll error path below.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, false
	}
	if err := common.UnmarshalJson(raw, &body); err != nil {
		return nil, resp.StatusCode, false
	}
	return body, resp.StatusCode, true
}

// prCoordinatesFromMeta returns org, repo and PR number for a GitHub PR, taking
// each from prMetadata when set and otherwise parsing the PR URL
// (https://github.com/<org>/<repo>/pull/<n>).
func prCoordinatesFromMeta(meta prMetadata) (org, repo, number string) {
	org, repo = meta.Org, meta.Repo
	number = prNumberString(meta.PRNumber)
	if org != "" && repo != "" && number != "" {
		return org, repo, number
	}
	parts := strings.Split(strings.TrimRight(meta.PRURL, "/"), "/")
	if len(parts) >= 4 {
		if number == "" {
			number = parts[len(parts)-1]
		}
		if repo == "" {
			repo = parts[len(parts)-3]
		}
		if org == "" {
			org = parts[len(parts)-4]
		}
	}
	return org, repo, number
}

// prNumberString renders the prMetadata.PRNumber (JSON-decoded, so typically
// float64 or string) as a decimal string; "" for anything unusable.
func prNumberString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case json.Number:
		return n.String()
	default:
		return ""
	}
}

// getGitTokenForTenant retrieves a git token for the given tenant by querying integrations directly.
func getGitTokenForTenant(dbms *database.DatabaseManager, tenantID string, provider string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("tenant ID is empty")
	}

	integrationType := "github"
	if provider == "gitlab" {
		integrationType = "gitlab"
	}

	var integrationID string
	err := dbms.Db.QueryRowx(`
		SELECT i.id::text
		FROM integrations i
		WHERE i.tenant_id = $1 AND i.type = $2 AND i.status = 'enabled'
		LIMIT 1
	`, tenantID, integrationType).Scan(&integrationID)
	if err != nil {
		return "", fmt.Errorf("no %s integration found for tenant %s: %w", integrationType, tenantID, err)
	}

	var password string
	var isEncrypted bool
	err = dbms.Db.QueryRowx(`
		SELECT value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1 AND name = 'password'
	`, integrationID).Scan(&password, &isEncrypted)
	if err != nil {
		return "", fmt.Errorf("no password config found for integration %s: %w", integrationID, err)
	}

	if isEncrypted && password != "" {
		decrypted, err := common.Decrypt(password)
		if err != nil {
			return "", fmt.Errorf("failed to decrypt token: %w", err)
		}
		return decrypted, nil
	}

	return password, nil
}
