package api

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// A duplicate whose main event's plan is older than the newest firing's
// investigation. With the loop on the read returns that plan (its relevance marks it stale); before, the
// page got no plan and regenerated, which it no longer does.
func TestSavedPlanShown_DuplicateWithAnOlderMainPlan(t *testing.T) {
	saved := &events.EventAnalysis{RelatedEventId: "main", Analysis: `{"root_cause":"bad image"}`}
	predates := func() bool { return true } // the newest investigation is newer than the plan

	stubLoopOn(t, true)
	assert.True(t, savedPlanShown("acct", "main", saved, predates), "loop on: the main event's plan is returned")
	assert.False(t, savedPlanShown("acct", "dup", saved, predates), "a plan saved for another event is still not this read's")

	stubLoopOn(t, false)
	assert.False(t, savedPlanShown("acct", "main", saved, predates), "loop off: a plan older than its analysis is not returned, as before")
	assert.True(t, savedPlanShown("acct", "main", saved, func() bool { return false }), "loop off: a current plan is returned, as before")
	assert.False(t, savedPlanShown("acct", "dup", saved, func() bool { return false }), "loop off: a duplicate has no plan of its own, as before")
}

// With the loop on a duplicate reads its main event's plan, with the main event's info.
func TestPlanEventOf_DuplicateReadsItsMainEvent(t *testing.T) {
	stubLoopOn(t, true)
	swap(t, &primaryEventId, func(_ *security.RequestContext, _, eventId string) string {
		if eventId == "dup" {
			return "main"
		}
		return ""
	})
	db, mock := verdictDB(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, fingerprint, aggregation_key, created_at FROM events WHERE id = $1`)).WithArgs("main", "acct").
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}).AddRow("main", "fp", "KubePodNotReady", time.Now()))
	repo := events.NewEventAnalysisRepository(&common.DatabaseManager{Db: db})
	ctx := security.NewRequestContextForSuperAdmin()

	id, info := planEventAndInfo(ctx, repo, "acct", "dup", &events.EventInfo{ID: "dup", Fingerprint: "fp"})
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "main", id)
	require.NotNil(t, info)
	assert.Equal(t, "main", info.ID)

	mainInfo := &events.EventInfo{ID: "main", Fingerprint: "fp"}
	id, info = planEventAndInfo(ctx, repo, "acct", "main", mainInfo)
	assert.Equal(t, "main", id, "a main event reads its own plan")
	assert.Same(t, mainInfo, info)
}
