package event

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"nudgebee/services/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rollback dialog shows the values a change recorded. Whether the workload was changed again
// since is something only the server knows, and it used to say so only after the click, as a
// refusal. CheckEventRollback asks the apply's own question ahead of the click: these tests hold
// the two to the same answer and hold the check to reading only.

const (
	rollbackTestResource = "0d4f6a2c-91b7-4e38-a5c0-6f2b8d1e7a93"
	rollbackTestPath     = "spec.template.spec.containers[0].image"
	rollbackTestWriter   = `{"TenantId":"` + applyTestTenant + `","UserId":"u1","AccountIds":["` + applyTestEventAccount + `"],"Roles":["tenant_admin"]}`
	rollbackTestReader   = `{"TenantId":"` + applyTestTenant + `","UserId":"u1","AccountIds":["` + applyTestEventAccount + `"],"Roles":["tenant_admin_readonly"]}`
)

var rollbackTestChangedAt = time.Date(2026, 3, 4, 10, 2, 41, 0, time.UTC)

func rollbackDiffEvidences(old, new string) []byte {
	return fmt.Appendf(nil, `[{"type":"diff","data":{"resource_name":"deployment/shop/checkout.yaml","updated_values":[{"path":%q,"old":%q,"new":%q}]}}]`,
		rollbackTestPath, old, new)
}

// expectRollbackTargetLoads is the three reads that find what a rollback acts on: the account,
// the change event (shop/checkout's image went v1 -> v2) and the workload it belongs to.
func expectRollbackTargetLoads(mock sqlmock.Sqlmock) {
	expectRollbackTargetLoadsFor(mock, "ConfigurationChange")
}

// expectRollbackTargetLoadsFor is the same three reads for an alert of the given rule that carries
// that change as evidence.
func expectRollbackTargetLoadsFor(mock sqlmock.Sqlmock, aggregationKey string) {
	mock.ExpectQuery("FROM cloud_accounts WHERE id = \\$1").
		WithArgs(applyTestEventAccount).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_type"}).AddRow(applyTestEventAccount, "kubernetes"))
	mock.ExpectQuery("FROM events WHERE id = \\$1").
		WithArgs(applyTestEventID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "aggregation_key", "finding_type", "subject_namespace", "starts_at", "evidences", "tenant", "cloud_account_id", "cloud_resource_id"}).
			AddRow(applyTestEventID, aggregationKey, "configuration_change", "shop", rollbackTestChangedAt, rollbackDiffEvidences("v1", "v2"),
				applyTestTenant, applyTestEventAccount, rollbackTestResource))
	mock.ExpectQuery("FROM cloud_resourses WHERE id = \\$1").
		WithArgs(rollbackTestResource).
		WillReturnRows(sqlmock.NewRows([]string{"id", "resourse_id"}).AddRow(rollbackTestResource, "shop/Deployment/checkout"))
}

// expectLaterChanges is the guard's read: the changes recorded on the same workload since.
func expectLaterChanges(mock sqlmock.Sqlmock, laterImages ...string) {
	rows := sqlmock.NewRows([]string{"id", "starts_at", "evidences"})
	previous := "v2"
	for i, image := range laterImages {
		rows.AddRow(fmt.Sprintf("later-%d", i), rollbackTestChangedAt.Add(time.Duration(i+1)*17*time.Minute), rollbackDiffEvidences(previous, image))
		previous = image
	}
	mock.ExpectQuery("FROM events WHERE finding_type = 'configuration_change'").
		WithArgs(applyTestTenant, applyTestEventAccount, rollbackTestChangedAt, applyTestEventID, rollbackTestResource).
		WillReturnRows(rows)
}

func TestCheckEventRollback_AllowsWhenNothingChangedSince(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectRollbackTargetLoads(mock)
	expectLaterChanges(mock)

	resp, err := CheckEventRollback(applyResolutionTestContext(t, rollbackTestWriter), EventRollbackCheckRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
	})

	require.NoError(t, err)
	assert.Equal(t, EventRollbackCheckResponse{Allowed: true}, resp)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The reported case: the workload was changed again after the event. The check must say what the
// apply says, in the apply's words, and neither may have written anything to say it — sqlmock
// fails any statement it was not told to expect, so a write on either path fails this test.
func TestCheckEventRollback_GivesTheApplysRefusalForALaterChange(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectRollbackTargetLoads(mock)
	expectLaterChanges(mock, "v3")

	resp, err := CheckEventRollback(applyResolutionTestContext(t, rollbackTestWriter), EventRollbackCheckRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
	})

	require.NoError(t, err)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Reason, `"`+rollbackTestPath+`" was changed again by a later change (2026-03-04T10:19:41Z)`)
	require.NoError(t, mock.ExpectationsWereMet())

	// The same event, the same later change, asked of the apply. An empty change id means the
	// event's own change on both paths, so both must reach the guard with the same change.
	expectRollbackTargetLoads(mock)
	expectApplyResolutionProceeds(mock)
	expectLaterChanges(mock, "v3")

	_, applyErr := ApplyEventResolution(applyResolutionTestContext(t, rollbackTestWriter), EventRecommendationApplyRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
		Data:      map[string]any{"revert": true},
	})

	require.Error(t, applyErr)
	assert.Equal(t, resp.Reason, applyErr.Error())
	require.NoError(t, mock.ExpectationsWereMet())
}

// The guard fails closed when it cannot read the later changes, and for the apply that is right:
// it must not revert blind. For the check it is not an answer about the change, so it must come
// back as a failure the dialog treats as "unknown" — not as "changed again", with the rollback
// taken away because of a database blip.
func TestCheckEventRollback_AFailedLookupIsNotAnAnswer(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectRollbackTargetLoads(mock)
	mock.ExpectQuery("FROM events WHERE finding_type = 'configuration_change'").WillReturnError(errors.New("connection reset"))

	resp, err := CheckEventRollback(applyResolutionTestContext(t, rollbackTestWriter), EventRollbackCheckRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
	})

	require.Error(t, err)
	assert.Equal(t, EventRollbackCheckResponse{}, resp)

	// The apply still refuses, with the bad request it has always returned.
	expectRollbackTargetLoads(mock)
	expectApplyResolutionProceeds(mock)
	mock.ExpectQuery("FROM events WHERE finding_type = 'configuration_change'").WillReturnError(errors.New("connection reset"))

	_, applyErr := ApplyEventResolution(applyResolutionTestContext(t, rollbackTestWriter), EventRecommendationApplyRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
		Data:      map[string]any{"revert": true},
	})

	var refusal common.Error
	require.ErrorAs(t, applyErr, &refusal)
	assert.Equal(t, common.ErrorBadRequest("event resolution: cannot verify whether a later change touched the same fields; not reverting"), refusal)
	assert.Equal(t, refusal.Error(), applyErr.Error())
	require.NoError(t, mock.ExpectationsWereMet())
}

// The ticket's last step: the later change is itself undone, and the rollback is then possible.
func TestCheckEventRollback_AllowsOnceTheLaterChangeIsUndone(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectRollbackTargetLoads(mock)
	expectLaterChanges(mock, "v3", "v2")

	resp, err := CheckEventRollback(applyResolutionTestContext(t, rollbackTestWriter), EventRollbackCheckRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
	})

	require.NoError(t, err)
	assert.Equal(t, EventRollbackCheckResponse{Allowed: true}, resp)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Every later change is needed to know what a field holds now. Past what one check reads, the
// answer is a refusal that says so and says what to do, never a verdict on part of the history.
func TestCheckEventRollback_RefusesRatherThanJudgePartOfTheHistory(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectRollbackTargetLoads(mock)
	rows := sqlmock.NewRows([]string{"id", "starts_at", "evidences"})
	for i := 0; i <= laterChangesCap; i++ {
		// Not one of them touches the image this rollback writes.
		rows.AddRow(fmt.Sprintf("later-%d", i), rollbackTestChangedAt.Add(time.Duration(i+1)*time.Minute),
			[]byte(`[{"type":"diff","data":{"updated_values":[{"path":"spec.replicas","old":2,"new":3}]}}]`))
	}
	mock.ExpectQuery("FROM events WHERE finding_type = 'configuration_change' .* ORDER BY starts_at ASC, id ASC LIMIT 1001").WillReturnRows(rows)

	resp, err := CheckEventRollback(applyResolutionTestContext(t, rollbackTestWriter), EventRollbackCheckRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
	})

	require.NoError(t, err)
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Reason, "more than 1000 changes have been recorded on this workload since this one")
	assert.Contains(t, resp.Reason, "Roll back with the tool that deploys it instead")
	require.NoError(t, mock.ExpectationsWereMet())
}

// The later changes are read without their before/after manifests — nearly the whole column.
func TestLaterChangesOnResource_ReadsOnlyTheRecordedFieldChanges(t *testing.T) {
	assert.Contains(t, laterDiffsOnly, "'updated_values', ev->'data'->'updated_values'")
	assert.NotContains(t, laterDiffsOnly, "'old'")
	assert.NotContains(t, laterDiffsOnly, "'new'")
}

// The rollback row is shown on any alert that carries a recorded change. On the kinds of alert
// that have a fix of their own, that fix's branch took the request first and refused it for
// lacking its own fields, so the rollback of a bad image could not be run from the image-pull
// alert it caused. The request now reaches the rollback's own guard there.
func TestApplyEventResolution_RollbackRowReachesTheRollbackOnEveryKindOfAlert(t *testing.T) {
	for _, aggregationKey := range []string{"image_pull_backoff_reporter", "PodMemoryReachingLimit", "CPUThrottlingHigh", "KubePersistentVolumeFillingUp"} {
		t.Run(aggregationKey, func(t *testing.T) {
			mock := setupApplyResolutionTest(t)
			expectRollbackTargetLoadsFor(mock, aggregationKey)
			expectApplyResolutionProceeds(mock)
			expectLaterChanges(mock, "v3")

			_, err := ApplyEventResolution(applyResolutionTestContext(t, rollbackTestWriter), EventRecommendationApplyRequest{
				AccountId: applyTestEventAccount,
				EventId:   applyTestEventID,
				Data:      map[string]any{"revert": true, "card_id": "LastDeploymentCard_0"},
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), `"`+rollbackTestPath+`" was changed again by a later change`)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// The older "Resolve Event" dialog sends its own fields and no card id. Its requests stay on the
// alert's own branch exactly as before — they must not start rolling a workload back.
func TestApplyEventResolution_OlderResolveDialogStaysOnItsOwnBranch(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectRollbackTargetLoadsFor(mock, "CPUThrottlingHigh")
	expectApplyResolutionProceeds(mock)

	_, err := ApplyEventResolution(applyResolutionTestContext(t, rollbackTestWriter), EventRecommendationApplyRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
		Data:      map[string]any{"revert": true, "restart": false, "size": "", "increase_replicas": ""},
	})

	// The CPU-throttling branch's own first check, not the rollback guard: no later-changes read.
	require.Error(t, err)
	assert.Equal(t, "resolution: kind or owner is not correct", err.Error())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCheckEventRollback_RefusesACallerWhoCannotRollBack(t *testing.T) {
	mock := setupApplyResolutionTest(t)

	_, err := CheckEventRollback(applyResolutionTestContext(t, rollbackTestReader), EventRollbackCheckRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
	})

	assert.Equal(t, common.ErrorUnauthorized("error: account access not found"), err)
	require.NoError(t, mock.ExpectationsWereMet(), "a refused caller still caused a read")
}

// A refusal is an answer the dialog shows; a fault is not. Reporting a failed lookup as "blocked"
// would put a reason in front of the operator that is not about their change.
func TestRollbackCheckResult(t *testing.T) {
	allowed, err := rollbackCheckResult(nil)
	require.NoError(t, err)
	assert.True(t, allowed.Allowed)

	for _, refusal := range []error{
		common.ErrorBadRequest("event resolution: \"spec.replicas\" was changed again by a later change"),
		common.ErrorUnauthorized("event resolution: the selected change belongs to another account"),
		fmt.Errorf("wrapped: %w", common.ErrorBadRequest("event resolution: the selected change no longer exists")),
	} {
		resp, err := rollbackCheckResult(refusal)
		require.NoError(t, err)
		assert.False(t, resp.Allowed)
		assert.NotEmpty(t, resp.Reason)
	}

	for _, fault := range []error{
		errors.New("resolution: service key is not correct"),
		common.ErrorInternal("connection reset"),
		revertUnverified{common.ErrorBadRequest("event resolution: cannot verify whether a later change exists; not reverting")},
	} {
		_, err := rollbackCheckResult(fault)
		assert.Equal(t, fault, err)
	}
}
