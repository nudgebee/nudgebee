package budget

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

var accountBackfillQuery = regexp.QuoteMeta("SET conversation_account_id = s.account_id") + `[\s\S]*` +
	regexp.QuoteMeta("COALESCE(c.account_id, p.account_id)") + `[\s\S]*` +
	regexp.QuoteMeta("FOR UPDATE OF p SKIP LOCKED")

// Full batches keep going; the first short batch means nothing fillable is
// left, so the tick stops there.
func TestRunUsageAccountBackfillTick_StopsOnShortBatch(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectExec(accountBackfillQuery).WithArgs(accountBackfillBatchSize).
		WillReturnResult(sqlmock.NewResult(0, accountBackfillBatchSize))
	mock.ExpectExec(accountBackfillQuery).WithArgs(accountBackfillBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 12))

	RunUsageAccountBackfillTick(context.Background(), dm)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// A tick never runs more than accountBackfillBatchesPerTick batches, so one
// tick stays short even with a large backlog.
func TestRunUsageAccountBackfillTick_BoundedPerTick(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	for i := 0; i < accountBackfillBatchesPerTick; i++ {
		mock.ExpectExec(accountBackfillQuery).WithArgs(accountBackfillBatchSize).
			WillReturnResult(sqlmock.NewResult(0, accountBackfillBatchSize))
	}

	RunUsageAccountBackfillTick(context.Background(), dm)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// A tick that runs out of time stops quietly; the next tick carries on.
func TestRunUsageAccountBackfillTick_StopsOnTimeout(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // database/sql fails the batch before it reaches the database

	RunUsageAccountBackfillTick(ctx, dm)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// A failed batch ends the tick; the next tick retries.
func TestRunUsageAccountBackfillTick_StopsOnError(t *testing.T) {
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectExec(accountBackfillQuery).WithArgs(accountBackfillBatchSize).
		WillReturnError(errors.New("canceling statement due to statement timeout"))

	RunUsageAccountBackfillTick(context.Background(), dm)

	assert.NoError(t, mock.ExpectationsWereMet())
}
