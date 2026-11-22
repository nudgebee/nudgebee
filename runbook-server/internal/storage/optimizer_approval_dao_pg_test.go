package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real-Postgres coverage for the approval rows of gated auto optimize runs. The
// approve-versus-expiry race is settled by these conditional updates, so they
// have to be checked against a database, not a mock.

func approvalTestDB(t *testing.T) *sqlx.DB {
	t.Helper()

	db, err := sqlx.Connect("postgres", requireTestDSN(t))
	require.NoError(t, err, "connect to TEST_POSTGRES_DSN")
	db.SetMaxOpenConns(1) // search_path below is per connection

	schema := fmt.Sprintf("approval_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`,
		schema, schema, schema))
	require.NoError(t, err)

	// The shape after the per-run approval migration.
	_, err = db.Exec(`
		CREATE TABLE auto_pilot_approvals (
			id uuid PRIMARY KEY,
			autopilot_id uuid NOT NULL,
			status text NOT NULL,
			created_at timestamp NOT NULL DEFAULT now(),
			updated_at timestamp,
			reviewer_comments text,
			reviewer_id uuid,
			tenant_id uuid NOT NULL,
			account_id uuid NOT NULL,
			attributes jsonb NOT NULL,
			auto_pilot_type text NOT NULL,
			policy_id uuid
		);
		CREATE TABLE auto_pilot_task (
			id uuid PRIMARY KEY,
			auto_pilot_id uuid,
			tenant_id uuid,
			account_id uuid,
			recommendation_id uuid,
			status text NOT NULL,
			created_at timestamp NOT NULL,
			updated_at timestamp NOT NULL,
			meta jsonb, attributes jsonb, resource_filter jsonb,
			scheduled_time timestamp, name text,
			reason text, error text, command text, task_id uuid, skipped_by uuid
		);
		CREATE TABLE recommendation (
			id uuid PRIMARY KEY,
			status text NOT NULL,
			is_dismissed boolean NOT NULL DEFAULT false,
			recommendation jsonb
		);`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return db
}

func fileApproval(t *testing.T, dao *OptimizerDao, aoID, tenantID, accountID uuid.UUID, expiresAt time.Time) model.AutoOptimizeApproval {
	t.Helper()
	a := model.AutoOptimizeApproval{
		ID: uuid.New(), AutoPilotID: aoID, TenantID: tenantID, AccountID: accountID,
		Status:    model.AutoOptimizeApprovalStatusPending,
		CreatedAt: time.Now().UTC(),
		Attributes: model.AutoOptimizeApprovalAttributes{
			TaskIDs: []uuid.UUID{uuid.New()}, ExpiresAt: expiresAt, PlanFingerprint: "fp",
		},
	}
	require.NoError(t, dao.ParkAutoOptimizeRun(context.Background(), nil, a))
	return a
}

func TestApprovalDao_DecideHasOneWinner(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	tenant, account := uuid.New(), uuid.New()
	a := fileApproval(t, dao, uuid.New(), tenant, account, now.Add(time.Hour))
	reviewer := uuid.New()
	comment := "ok"

	won, err := dao.DecideAutoOptimizeApproval(ctx, a.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, &reviewer, &comment, &now)
	require.NoError(t, err)
	assert.True(t, won)

	again, err := dao.DecideAutoOptimizeApproval(ctx, a.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusRejected, &reviewer, nil, nil)
	require.NoError(t, err)
	assert.False(t, again, "a decided approval cannot be decided again")

	got, err := dao.GetAutoOptimizeApproval(ctx, a.ID, tenant, account)
	require.NoError(t, err)
	assert.Equal(t, model.AutoOptimizeApprovalStatusApproved, got.Status)
	assert.Equal(t, &reviewer, got.ReviewerID)
	assert.Equal(t, "ok", *got.ReviewerComments)
	assert.Equal(t, a.Attributes.TaskIDs, got.Attributes.TaskIDs)
}

func TestApprovalDao_ApproveRefusedOnceWindowClosed(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	a := fileApproval(t, dao, uuid.New(), uuid.New(), uuid.New(), now.Add(-time.Second))
	reviewer := uuid.New()

	won, err := dao.DecideAutoOptimizeApproval(ctx, a.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusApproved, &reviewer, nil, &now)
	require.NoError(t, err)
	assert.False(t, won)
}

func TestApprovalDao_ExpireOnlyClosedPending(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	aoID, tenant, account := uuid.New(), uuid.New(), uuid.New()
	closed := fileApproval(t, dao, aoID, tenant, account, now.Add(-time.Minute))
	open := fileApproval(t, dao, aoID, tenant, account, now.Add(time.Hour))
	other := fileApproval(t, dao, uuid.New(), tenant, account, now.Add(-time.Minute))

	expired, err := dao.ExpireAutoOptimizeApprovals(ctx, aoID, now)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, closed.ID, expired[0].ID)
	assert.Equal(t, closed.Attributes.TaskIDs, expired[0].Attributes.TaskIDs)

	pending, err := dao.GetPendingAutoOptimizeApprovals(ctx, aoID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, open.ID, pending[0].ID)

	otherPending, err := dao.GetPendingAutoOptimizeApprovals(ctx, other.AutoPilotID)
	require.NoError(t, err)
	assert.Len(t, otherPending, 1, "another automation's approvals are untouched")
}

func TestApprovalDao_CancelReturnsOnlyPending(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	aoID, tenant, account := uuid.New(), uuid.New(), uuid.New()
	decided := fileApproval(t, dao, aoID, tenant, account, now.Add(time.Hour))
	_, err := dao.DecideAutoOptimizeApproval(ctx, decided.ID, model.AutoOptimizeApprovalStatusPending, model.AutoOptimizeApprovalStatusRejected, nil, nil, nil)
	require.NoError(t, err)
	open := fileApproval(t, dao, aoID, tenant, account, now.Add(time.Hour))

	cancelled, err := dao.CancelPendingAutoOptimizeApprovals(ctx, aoID)
	require.NoError(t, err)
	require.Len(t, cancelled, 1)
	assert.Equal(t, open.ID, cancelled[0].ID)
	assert.Equal(t, model.AutoOptimizeApprovalStatusCancelled, cancelled[0].Status)

	got, err := dao.GetAutoOptimizeApproval(ctx, decided.ID, tenant, account)
	require.NoError(t, err)
	assert.Equal(t, model.AutoOptimizeApprovalStatusRejected, got.Status)
}

func TestApprovalDao_GetIsTenantAndAccountScoped(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	tenant, account := uuid.New(), uuid.New()
	a := fileApproval(t, dao, uuid.New(), tenant, account, time.Now().UTC().Add(time.Hour))

	_, err := dao.GetAutoOptimizeApproval(ctx, a.ID, uuid.New(), account)
	var ce common.Error
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 404, ce.Code)

	_, err = dao.GetAutoOptimizeApproval(ctx, a.ID, tenant, uuid.New())
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, 404, ce.Code)
}

func insertTask(t *testing.T, db *sqlx.DB, recID uuid.UUID, status string, createdAt, updatedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db.Exec(`INSERT INTO auto_pilot_task (id, recommendation_id, status, created_at, updated_at, scheduled_time, name, meta, attributes, resource_filter)
		VALUES ($1, $2, $3, $4, $5, $4, 'task', '{}', '{}', '{}')`, id, recID, status, createdAt, updatedAt)
	require.NoError(t, err)
	return id
}

func TestApprovalDao_MoveOnlyFromStatus(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	parked := insertTask(t, db, uuid.New(), string(model.AutopilotTaskStatusPendingApproval), now, now)
	done := insertTask(t, db, uuid.New(), string(model.AutopilotTaskStatusComplete), now, now)
	reason := "Rejected by reviewer"

	moved, err := dao.MoveAutoOptimizeTasks(ctx, []uuid.UUID{parked, done},
		model.AutopilotTaskStatusPendingApproval, model.AutopilotTaskStatusSkipped, &reason)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{parked}, moved, "only the task still in from is reported as moved")

	var rows []struct {
		ID     uuid.UUID `db:"id"`
		Status string    `db:"status"`
		Reason *string   `db:"reason"`
	}
	require.NoError(t, db.Select(&rows, `SELECT id, status, reason FROM auto_pilot_task`))
	for _, r := range rows {
		switch r.ID {
		case parked:
			assert.Equal(t, string(model.AutopilotTaskStatusSkipped), r.Status)
			assert.Equal(t, reason, *r.Reason)
		case done:
			assert.Equal(t, string(model.AutopilotTaskStatusComplete), r.Status, "a task already past from is left alone")
			assert.Nil(t, r.Reason)
		}
	}
}

// Parked tasks hold their recommendation for the approval window; an approved
// task holds it for the stale bound measured from the approval.
func TestActiveTasks_CountParkedAndApprovedTasks(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	long := now.Add(-model.AutoOptimizeApprovalWindow - time.Hour)
	beyondStale := now.Add(-model.ScheduledTaskStaleAfter - time.Minute)

	parkedRec, oldParkedRec, approvedRec, staleRec := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	insertTask(t, db, parkedRec, string(model.AutopilotTaskStatusPendingApproval), now.Add(-20*time.Hour), now.Add(-20*time.Hour))
	insertTask(t, db, oldParkedRec, string(model.AutopilotTaskStatusPendingApproval), long, long)
	insertTask(t, db, approvedRec, string(model.AutopilotTaskStatusScheduled), now.Add(-20*time.Hour), now.Add(-time.Minute))
	insertTask(t, db, staleRec, string(model.AutopilotTaskStatusScheduled), beyondStale, beyondStale)

	active, err := dao.GetActiveTasksForRecommendations(ctx, []uuid.UUID{parkedRec, oldParkedRec, approvedRec, staleRec})
	require.NoError(t, err)
	assert.Contains(t, active, parkedRec)
	assert.NotContains(t, active, oldParkedRec, "a parked task past the window no longer blocks")
	assert.Contains(t, active, approvedRec, "an approved task counts from its approval")
	assert.NotContains(t, active, staleRec)
}

func TestApprovalDao_RecommendationSnapshots(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	open, dismissed := uuid.New(), uuid.New()
	content, _ := json.Marshal(map[string]any{"cpu": "250m"})
	_, err := db.Exec(`INSERT INTO recommendation (id, status, is_dismissed, recommendation) VALUES ($1, 'Open', false, $2), ($3, 'Open', true, NULL)`,
		open, string(content), dismissed)
	require.NoError(t, err)

	snaps, err := dao.GetRecommendationSnapshots(ctx, []uuid.UUID{open, dismissed, uuid.New()})
	require.NoError(t, err)
	require.Len(t, snaps, 2)
	assert.True(t, snaps[open].StillApplicable())
	assert.Equal(t, map[string]any{"cpu": "250m"}, snaps[open].Content)
	assert.False(t, snaps[dismissed].StillApplicable())
}

// The tasks and the approval land together or not at all.
func TestApprovalDao_ParkIsAtomic(t *testing.T) {
	db := approvalTestDB(t)
	dao := &OptimizerDao{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	aoID, tenant, account := uuid.New(), uuid.New(), uuid.New()
	task := func() model.AutoOptimizeTask {
		return model.AutoOptimizeTask{ID: uuid.New(), AutoPilotID: aoID, TenantID: tenant, AccountID: account,
			Status: string(model.AutopilotTaskStatusPendingApproval), Name: "task", CreatedAt: now, UpdatedAt: now, ScheduledTime: now,
			Meta: map[string]any{}}
	}
	first := fileApproval(t, dao, aoID, tenant, account, now.Add(time.Hour))

	// Reusing the first approval's id makes the insert fail after the tasks were written.
	clash := first
	clash.Attributes.TaskIDs = nil
	err := dao.ParkAutoOptimizeRun(ctx, []model.AutoOptimizeTask{task(), task()}, clash)
	require.Error(t, err)
	var n int
	require.NoError(t, db.Get(&n, `SELECT count(*) FROM auto_pilot_task`))
	assert.Equal(t, 0, n, "a failed approval insert leaves no parked tasks behind")

	ok := model.AutoOptimizeApproval{ID: uuid.New(), AutoPilotID: aoID, TenantID: tenant, AccountID: account,
		Status: model.AutoOptimizeApprovalStatusPending, CreatedAt: now,
		Attributes: model.AutoOptimizeApprovalAttributes{ExpiresAt: now.Add(time.Hour)}}
	require.NoError(t, dao.ParkAutoOptimizeRun(ctx, []model.AutoOptimizeTask{task(), task()}, ok))
	require.NoError(t, db.Get(&n, `SELECT count(*) FROM auto_pilot_task`))
	assert.Equal(t, 2, n)
}
