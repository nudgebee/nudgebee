package notification

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"

	"nudgebee/services/security"
)

// An alert on NudgeBee's own workload (the agent's namespace, or one the
// account listed) is not sent.
func TestProcessEvent_OwnWorkloadIsNotNotified(t *testing.T) {
	mock := mockMetastore(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("account-1", "nudgebee-agent").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	err := ProcessEvent(ctx, map[string]any{
		"id":                "event-1",
		"priority":          "HIGH",
		"source":            "prometheus",
		"tenant":            "tenant-1",
		"cloud_account_id":  "account-1",
		"subject_namespace": "nudgebee-agent",
	})
	// No broker is configured in tests, so reaching the publish returns an
	// error: NoError proves the check returned before publishing.
	assert.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Expectations were not met: %v", err)
	}
}

// A return or resolve follows the same rule as the firing alert, and a failed
// check sends rather than drops.
func TestProcessEventReturned_OwnWorkload(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		own      *bool
		wantSent int
	}{
		{name: "own workload is not sent", own: &yes, wantSent: 0},
		{name: "customer workload is sent", own: &no, wantSent: 1},
		{name: "a failed check still sends", own: nil, wantSent: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := mockMetastore(t)
			sent := capturePublished(t)

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			ctx := security.NewRequestContext(context.TODO(), &security.SecurityContext{}, logger, nil, nil)

			mock.ExpectQuery(`SELECT nb_status FROM events WHERE id = \$1`).
				WithArgs("event-1").
				WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow("OPEN"))
			check := mock.ExpectQuery(`SELECT EXISTS`).WithArgs("account-1", "nudgebee-agent")
			if tc.own == nil {
				check.WillReturnError(assert.AnError)
			} else {
				check.WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(*tc.own))
			}

			err := ProcessEventReturned(ctx, map[string]any{
				"id":                "event-1",
				"priority":          "HIGH",
				"source":            "anomaly",
				"status":            "FIRING",
				"tenant":            "tenant-1",
				"cloud_account_id":  "account-1",
				"subject_namespace": "nudgebee-agent",
			})
			assert.NoError(t, err)

			assert.Len(t, *sent, tc.wantSent)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("Expectations were not met: %v", err)
			}
		})
	}
}
