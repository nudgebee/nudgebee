package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Approval rows for gated auto optimize runs. Every status change is a
// conditional UPDATE on the current status, so an approve racing an expiry or a
// cancel has exactly one winner and the loser sees zero rows.

const approvalColumns = `id, autopilot_id, tenant_id, account_id, status, reviewer_id,
	reviewer_comments, created_at, updated_at, attributes`

type approvalRow struct {
	ID               uuid.UUID  `db:"id"`
	AutoPilotID      uuid.UUID  `db:"autopilot_id"`
	TenantID         uuid.UUID  `db:"tenant_id"`
	AccountID        uuid.UUID  `db:"account_id"`
	Status           string     `db:"status"`
	ReviewerID       *uuid.UUID `db:"reviewer_id"`
	ReviewerComments *string    `db:"reviewer_comments"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        *time.Time `db:"updated_at"`
	Attributes       []byte     `db:"attributes"`
}

func (r approvalRow) toModel() (model.AutoOptimizeApproval, error) {
	a := model.AutoOptimizeApproval{
		ID:               r.ID,
		AutoPilotID:      r.AutoPilotID,
		TenantID:         r.TenantID,
		AccountID:        r.AccountID,
		Status:           model.AutoOptimizeApprovalStatus(r.Status),
		ReviewerID:       r.ReviewerID,
		ReviewerComments: r.ReviewerComments,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
	if len(r.Attributes) > 0 {
		if err := json.Unmarshal(r.Attributes, &a.Attributes); err != nil {
			return a, fmt.Errorf("failed to unmarshal approval attributes %s: %w", r.ID, err)
		}
	}
	return a, nil
}

func approvalsFromRows(rows []approvalRow) ([]model.AutoOptimizeApproval, error) {
	out := make([]model.AutoOptimizeApproval, 0, len(rows))
	for _, r := range rows {
		a, err := r.toModel()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ParkAutoOptimizeRun saves a gated run's planned tasks and files its approval
// in one transaction, so there is never a parked task without an approval to
// release it, or an approval naming tasks that were not saved.
func (d *OptimizerDao) ParkAutoOptimizeRun(ctx context.Context, tasks []model.AutoOptimizeTask, a model.AutoOptimizeApproval) (retErr error) {
	attrs, err := json.Marshal(a.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal approval attributes: %w", err)
	}
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin park transaction: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()
	if err := saveAutoOptimizeTasks(ctx, tx, tasks); err != nil {
		return fmt.Errorf("failed to save planned tasks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO auto_pilot_approvals
			(id, autopilot_id, tenant_id, account_id, status, created_at, attributes, auto_pilot_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.AutoPilotID, a.TenantID, a.AccountID, string(a.Status), a.CreatedAt, string(attrs),
		model.AutoOptimizeApprovalType); err != nil {
		return fmt.Errorf("failed to file auto optimize approval: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit park transaction: %w", err)
	}
	return nil
}

// GetAutoOptimizeApproval returns the approval only within the caller's tenant
// and account; anything else is reported as not found.
func (d *OptimizerDao) GetAutoOptimizeApproval(ctx context.Context, id, tenantID, accountID uuid.UUID) (*model.AutoOptimizeApproval, error) {
	var row approvalRow
	err := d.db.GetContext(ctx, &row, `SELECT `+approvalColumns+` FROM auto_pilot_approvals
		WHERE id = $1 AND tenant_id = $2 AND account_id = $3 AND auto_pilot_type = $4`,
		id, tenantID, accountID, model.AutoOptimizeApprovalType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, common.ErrorNotFound("approval not found")
	}
	if err != nil {
		return nil, err
	}
	a, err := row.toModel()
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetPendingAutoOptimizeApprovals returns the automation's PENDING approvals,
// expired or not.
func (d *OptimizerDao) GetPendingAutoOptimizeApprovals(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeApproval, error) {
	var rows []approvalRow
	err := d.db.SelectContext(ctx, &rows, `SELECT `+approvalColumns+` FROM auto_pilot_approvals
		WHERE autopilot_id = $1 AND status = $2 AND auto_pilot_type = $3
		ORDER BY created_at`,
		autoOptimizeID, string(model.AutoOptimizeApprovalStatusPending), model.AutoOptimizeApprovalType)
	if err != nil {
		return nil, err
	}
	return approvalsFromRows(rows)
}

// DecideAutoOptimizeApproval moves an approval from one status to another and
// reports whether it did. With unexpiredAt set, the move only happens while the
// approval's window is still open at that time.
func (d *OptimizerDao) DecideAutoOptimizeApproval(ctx context.Context, id uuid.UUID, from, to model.AutoOptimizeApprovalStatus, reviewerID *uuid.UUID, comments *string, unexpiredAt *time.Time) (bool, error) {
	query := `UPDATE auto_pilot_approvals
		SET status = $1, reviewer_id = $2, reviewer_comments = $3, updated_at = $4
		WHERE id = $5 AND status = $6`
	args := []any{string(to), reviewerID, comments, time.Now().UTC(), id, string(from)}
	if unexpiredAt != nil {
		query += ` AND (attributes->>'expires_at')::timestamptz > $7`
		args = append(args, *unexpiredAt)
	}
	res, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to update approval %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ExpireAutoOptimizeApprovals marks the automation's PENDING approvals whose
// window closed by now as EXPIRED and returns them.
func (d *OptimizerDao) ExpireAutoOptimizeApprovals(ctx context.Context, autoOptimizeID uuid.UUID, now time.Time) ([]model.AutoOptimizeApproval, error) {
	var rows []approvalRow
	err := d.db.SelectContext(ctx, &rows, `UPDATE auto_pilot_approvals
		SET status = $1, updated_at = $2
		WHERE autopilot_id = $3 AND status = $4 AND auto_pilot_type = $5
			AND (attributes->>'expires_at')::timestamptz <= $2
		RETURNING `+approvalColumns,
		string(model.AutoOptimizeApprovalStatusExpired), now, autoOptimizeID,
		string(model.AutoOptimizeApprovalStatusPending), model.AutoOptimizeApprovalType)
	if err != nil {
		return nil, fmt.Errorf("failed to expire approvals for %s: %w", autoOptimizeID, err)
	}
	return approvalsFromRows(rows)
}

// CancelPendingAutoOptimizeApprovals marks every PENDING approval of the
// automation CANCELLED and returns them.
func (d *OptimizerDao) CancelPendingAutoOptimizeApprovals(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeApproval, error) {
	var rows []approvalRow
	err := d.db.SelectContext(ctx, &rows, `UPDATE auto_pilot_approvals
		SET status = $1, updated_at = $2
		WHERE autopilot_id = $3 AND status = $4 AND auto_pilot_type = $5
		RETURNING `+approvalColumns,
		string(model.AutoOptimizeApprovalStatusCancelled), time.Now().UTC(), autoOptimizeID,
		string(model.AutoOptimizeApprovalStatusPending), model.AutoOptimizeApprovalType)
	if err != nil {
		return nil, fmt.Errorf("failed to cancel approvals for %s: %w", autoOptimizeID, err)
	}
	return approvalsFromRows(rows)
}

// MoveAutoOptimizeTasks changes the status of the given tasks that are still in
// from, and returns the ids it actually moved. A nil reason keeps the stored one.
func (d *OptimizerDao) MoveAutoOptimizeTasks(ctx context.Context, ids []uuid.UUID, from, to model.AutopilotTaskStatus, reason *string) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var moved []uuid.UUID
	err := d.db.SelectContext(ctx, &moved, `UPDATE auto_pilot_task
		SET status = $1, reason = COALESCE($2, reason), updated_at = $3
		WHERE id = ANY($4) AND status = $5
		RETURNING id`,
		string(to), reason, time.Now().UTC(), pq.Array(ids), string(from))
	if err != nil {
		return nil, fmt.Errorf("failed to move tasks from %s to %s: %w", from, to, err)
	}
	return moved, nil
}

// SetAutoOptimizeExecutionStatus writes only execution_status, so it cannot
// undo a concurrent edit or status change the way a full save from an older
// read would.
func (d *OptimizerDao) SetAutoOptimizeExecutionStatus(ctx context.Context, id uuid.UUID, status model.AutopilotExecutionStatus) error {
	_, err := d.db.ExecContext(ctx, `UPDATE auto_pilot SET execution_status = $1 WHERE id = $2`, string(status), id)
	if err != nil {
		return fmt.Errorf("failed to set execution status for %s: %w", id, err)
	}
	return nil
}

// GetRecommendationSnapshots returns the current status and content of the
// given recommendations. Missing ids are omitted.
func (d *OptimizerDao) GetRecommendationSnapshots(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]model.RecommendationSnapshot, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]model.RecommendationSnapshot{}, nil
	}
	var rows []struct {
		ID             uuid.UUID `db:"id"`
		Status         string    `db:"status"`
		IsDismissed    bool      `db:"is_dismissed"`
		Recommendation []byte    `db:"recommendation"`
	}
	if err := d.db.SelectContext(ctx, &rows, `SELECT id, status, is_dismissed, recommendation
		FROM recommendation WHERE id = ANY($1)`, pq.Array(ids)); err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]model.RecommendationSnapshot, len(rows))
	for _, r := range rows {
		snap := model.RecommendationSnapshot{Status: r.Status, IsDismissed: r.IsDismissed}
		if len(r.Recommendation) > 0 {
			if err := json.Unmarshal(r.Recommendation, &snap.Content); err != nil {
				return nil, fmt.Errorf("failed to unmarshal recommendation %s: %w", r.ID, err)
			}
		}
		out[r.ID] = snap
	}
	return out, nil
}
