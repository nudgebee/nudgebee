// Package coordinator is the single writer for recommendation resolution
// lifecycle transitions (lean M1 of the resolution-workflow design). Callers —
// the resolution poll, the PR-lifecycle reconciler, the GitHub webhook, the
// upcoming ticket-status sync — request transitions; the coordinator applies
// them in one transaction with row locks and a legality check, so racing
// requesters converge to recorded no-ops instead of overwriting each other.
//
// It deliberately imports neither the recommendation nor the adapter package,
// so both can call it without an import cycle.
package coordinator

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"
)

// Source identifies which requester asked for a transition; recorded in logs so
// races between requesters stay diagnosable.
type Source string

const (
	SourcePoll        Source = "poll"
	SourceWebhook     Source = "webhook"
	SourcePRLifecycle Source = "pr_lifecycle"
	SourceTicketSync  Source = "ticket_sync"
	SourceUser        Source = "user"
)

var (
	enabledOnce sync.Once
	enabled     bool
)

// Enabled gates whether call sites route transitions through the coordinator.
// Off by default so the legacy write paths stay byte-identical until the flag
// is flipped per environment.
func Enabled() bool {
	enabledOnce.Do(func() {
		v := os.Getenv("RESOLUTION_COORDINATOR_ENABLED")
		enabled = v == "true" || v == "1"
	})
	return enabled
}

// SettleResult reports what a transition request actually did. Applied=false
// with a Reason is a recorded no-op — the normal outcome when two requesters
// race and the second one loses.
type SettleResult struct {
	Applied              bool
	Reason               string
	RecommendationStatus models.RecommendationStatus
}

// legalSettle decides whether a resolution may move from current to target.
// Only InProgress rows settle, and only to a terminal status; everything else
// is a recorded no-op (duplicate webhook+poll delivery) or a caller bug.
func legalSettle(current, target models.RecommendationResolutionStatus) (bool, string) {
	if target != models.RecommendationResolutionStatusSuccess && target != models.RecommendationResolutionStatusFailed {
		return false, fmt.Sprintf("target status %s is not terminal", target)
	}
	if current == target {
		return false, fmt.Sprintf("already settled to %s", current)
	}
	if current != models.RecommendationResolutionStatusInProgress {
		return false, fmt.Sprintf("cannot settle a %s resolution", current)
	}
	return true, ""
}

// projectRecommendation returns the recommendation status a terminal resolution
// outcome projects to, and whether the projection applies from the
// recommendation's current status. Success closes the work (also from Open —
// the fix landed even if something reset the claim meanwhile); Failed hands an
// InProgress claim back for retry. Dismissed/Closed/Archive are user or
// terminal states a projection must never overwrite.
func projectRecommendation(outcome models.RecommendationResolutionStatus, current models.RecommendationStatus) (models.RecommendationStatus, bool) {
	switch outcome {
	case models.RecommendationResolutionStatusSuccess:
		if current == models.RecommendationStatusInProgress || current == models.RecommendationStatusOpen {
			return models.RecommendationStatusClosed, true
		}
	case models.RecommendationResolutionStatusFailed:
		if current == models.RecommendationStatusInProgress {
			return models.RecommendationStatusOpen, true
		}
	}
	return current, false
}

// SettleResolution moves one resolution to a terminal status and projects the
// outcome onto its recommendation, transactionally. Illegal transitions
// (already settled, wrong target) return a recorded no-op, not an error.
func SettleResolution(ctx *security.RequestContext, resolutionId string, outcome models.RecommendationResolutionStatus, message string, source Source) (SettleResult, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return SettleResult{}, err
	}
	tx, err := dbms.Db.BeginTxx(ctx.GetContext(), nil)
	if err != nil {
		return SettleResult{}, fmt.Errorf("coordinator: begin settle transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res := models.RecommendationResolution{}
	if err := tx.GetContext(ctx.GetContext(), &res, `SELECT * FROM recommendation_resolution WHERE id = $1 FOR UPDATE`, resolutionId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SettleResult{Reason: "resolution not found"}, nil
		}
		return SettleResult{}, fmt.Errorf("coordinator: load resolution %s: %w", resolutionId, err)
	}

	if ok, reason := legalSettle(res.Status, outcome); !ok {
		ctx.GetLogger().Info("coordinator: settle no-op", "resolution_id", resolutionId, "outcome", outcome, "source", source, "reason", reason)
		return SettleResult{Reason: reason}, nil
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx.GetContext(), `UPDATE recommendation_resolution SET status = $2, status_message = $3, updated_at = $4 WHERE id = $1`,
		res.Id, outcome, truncateMessage(message), now); err != nil {
		return SettleResult{}, fmt.Errorf("coordinator: settle resolution %s: %w", resolutionId, err)
	}

	result := SettleResult{Applied: true}
	rec := models.Recommendation{}
	err = tx.GetContext(ctx.GetContext(), &rec, `SELECT * FROM recommendation WHERE id = $1 FOR UPDATE`, res.RecommendationId)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The recommendation was purged by retention; the resolution row is
		// still worth settling for the attempt history.
		result.Reason = "recommendation no longer exists"
	case err != nil:
		return SettleResult{}, fmt.Errorf("coordinator: load recommendation %s: %w", res.RecommendationId, err)
	default:
		target, applies := projectRecommendation(outcome, rec.Status)
		if applies {
			if _, err := tx.ExecContext(ctx.GetContext(), `UPDATE recommendation SET status = $2, updated_at = $3 WHERE id = $1`, rec.Id, target, now); err != nil {
				return SettleResult{}, fmt.Errorf("coordinator: project recommendation %s: %w", rec.Id, err)
			}
		} else if rec.Status != target {
			result.Reason = fmt.Sprintf("recommendation stays %s", rec.Status)
		}
		result.RecommendationStatus = target
	}

	if err := tx.Commit(); err != nil {
		return SettleResult{}, fmt.Errorf("coordinator: commit settle for %s: %w", resolutionId, err)
	}
	ctx.GetLogger().Info("coordinator: resolution settled",
		"resolution_id", res.Id, "recommendation_id", res.RecommendationId, "outcome", outcome, "source", source, "recommendation_status", result.RecommendationStatus)
	return result, nil
}

// ReconcileSettledRecommendations settles InProgress recommendations against
// their most recent resolution once it reaches a terminal state: closed when it
// succeeded, re-opened for another attempt when it failed. The pull request
// webhook and the followup cron retire resolution rows directly, so the
// per-resolution poll — which only reads InProgress rows — never sees those
// outcomes. The most recent resolution decides; an earlier failed attempt must
// not re-open a recommendation whose retry has since landed. Set-based on
// purpose: this runs every cron tick over the whole backlog.
func ReconcileSettledRecommendations(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	_, err = dbms.Db.ExecContext(ctx.GetContext(), `
		UPDATE recommendation r
		SET status = CASE WHEN latest.status = $1 THEN $2 ELSE $3 END, updated_at = NOW()
		FROM (
			SELECT DISTINCT ON (rr.recommendation_id) rr.recommendation_id, rr.status
			FROM recommendation_resolution rr
			JOIN recommendation rec ON rec.id = rr.recommendation_id
			WHERE rec.status = $4
			ORDER BY rr.recommendation_id, rr.created_at DESC NULLS LAST
		) latest
		WHERE r.id = latest.recommendation_id AND r.status = $4 AND latest.status <> $5`,
		models.RecommendationResolutionStatusSuccess, models.RecommendationStatusClosed, models.RecommendationStatusOpen,
		models.RecommendationStatusInProgress, models.RecommendationResolutionStatusInProgress)
	if err != nil {
		return fmt.Errorf("coordinator: reconcile settled recommendations: %w", err)
	}
	return nil
}

func truncateMessage(s string) string {
	const maxLen = 800
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + " …(truncated)"
}

// legalDismissal decides whether a dismissal-state change may apply from the
// recommendation's current status. Dismiss/snooze only takes an Open
// recommendation out of circulation — never one being worked (InProgress) or
// already settled; reactivation only applies to a Dismissed one.
func legalDismissal(current models.RecommendationStatus, dismissed bool) (bool, string) {
	if dismissed {
		if current != models.RecommendationStatusOpen {
			return false, fmt.Sprintf("cannot dismiss a %s recommendation", current)
		}
		return true, ""
	}
	if current != models.RecommendationStatusDismissed {
		return false, fmt.Sprintf("cannot reactivate a %s recommendation", current)
	}
	return true, ""
}

// SetDismissal dismisses (snoozedUntil == nil), snoozes (snoozedUntil set), or
// reactivates (dismissed == false) a recommendation. A snoozed recommendation
// is a Dismissed one carrying snoozed_until, so every Open-filtering consumer
// suppresses it with no query changes; ExpireSnoozes returns it to Open.
func SetDismissal(ctx *security.RequestContext, recommendationId, accountId string, dismissed bool, reason string, snoozedUntil *time.Time, actor string) (SettleResult, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return SettleResult{}, err
	}
	tx, err := dbms.Db.BeginTxx(ctx.GetContext(), nil)
	if err != nil {
		return SettleResult{}, fmt.Errorf("coordinator: begin dismissal transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rec := models.Recommendation{}
	if err := tx.GetContext(ctx.GetContext(), &rec, `SELECT * FROM recommendation WHERE id = $1 AND cloud_account_id = $2 FOR UPDATE`, recommendationId, accountId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SettleResult{Reason: "recommendation not found"}, nil
		}
		return SettleResult{}, fmt.Errorf("coordinator: load recommendation %s: %w", recommendationId, err)
	}

	if ok, reason := legalDismissal(rec.Status, dismissed); !ok {
		ctx.GetLogger().Info("coordinator: dismissal no-op", "recommendation_id", recommendationId, "dismissed", dismissed, "reason", reason)
		return SettleResult{Reason: reason, RecommendationStatus: rec.Status}, nil
	}

	now := time.Now().UTC()
	target := models.RecommendationStatusOpen
	if dismissed {
		target = models.RecommendationStatusDismissed
	}
	var reasonPtr *string
	if dismissed && reason != "" {
		truncated := truncateMessage(reason)
		reasonPtr = &truncated
	}
	if _, err := tx.ExecContext(ctx.GetContext(), `UPDATE recommendation
		SET status = $2, is_dismissed = $3, dismissed_reason = $4, snoozed_until = $5, updated_at = $6, updated_by = $7
		WHERE id = $1`,
		rec.Id, target, dismissed, reasonPtr, snoozedUntil, now, actor); err != nil {
		return SettleResult{}, fmt.Errorf("coordinator: set dismissal for %s: %w", rec.Id, err)
	}
	if err := tx.Commit(); err != nil {
		return SettleResult{}, fmt.Errorf("coordinator: commit dismissal for %s: %w", rec.Id, err)
	}
	ctx.GetLogger().Info("coordinator: dismissal updated",
		"recommendation_id", rec.Id, "dismissed", dismissed, "snoozed_until", snoozedUntil, "actor", actor)
	return SettleResult{Applied: true, RecommendationStatus: target}, nil
}

// ExpireSnoozes returns snoozed recommendations whose snooze has lapsed to
// Open. Set-based and unconditional (not flag-gated): a snooze created while
// the coordinator was enabled must still expire if the flag is later turned
// off.
func ExpireSnoozes(ctx *security.RequestContext) (int64, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, err
	}
	// updated_by clears to NULL: no user performed this transition, and the
	// column is a uuid FK to users, so a system marker string cannot be stored.
	res, err := dbms.Db.ExecContext(ctx.GetContext(), `UPDATE recommendation
		SET status = $1, is_dismissed = false, dismissed_reason = NULL, snoozed_until = NULL, updated_at = NOW(), updated_by = NULL
		WHERE status = $2 AND snoozed_until IS NOT NULL AND snoozed_until <= NOW()`,
		models.RecommendationStatusOpen, models.RecommendationStatusDismissed)
	if err != nil {
		return 0, fmt.Errorf("coordinator: expire snoozes: %w", err)
	}
	count, _ := res.RowsAffected()
	if count > 0 {
		ctx.GetLogger().Info("coordinator: snoozes expired", "count", count)
	}
	return count, nil
}
