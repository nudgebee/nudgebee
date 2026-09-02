package api

import (
	"testing"
	"time"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
)

func ref(account, key string) events.ClassRef {
	return events.ClassRef{AccountID: account, AggregationKey: key}
}

func TestAnnotateCarryOver(t *testing.T) {
	seen := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)

	findings := []classFinding{
		{AggregationKey: "KubePodCrashLooping", AccountID: "acct-a"},
		{AggregationKey: "NewThisWeek", AccountID: "acct-a"},
	}
	prior := []events.PriorClass{
		{AggregationKey: "KubePodCrashLooping", AccountID: "acct-a", Weeks: 3, Events: 8, LastSeen: seen},
		{AggregationKey: "StoppedRecurring", AccountID: "acct-a", Weeks: 2, Events: 5, LastSeen: seen},
	}

	resolved := annotateCarryOver(findings, prior,
		[]events.ClassRef{ref("acct-a", "KubePodCrashLooping"), ref("acct-a", "NewThisWeek")})

	assert.Equal(t, 3, findings[0].CarriedOverWeeks, "class present in 3 earlier weeks")
	assert.Zero(t, findings[1].CarriedOverWeeks, "class absent from earlier weeks is new")

	if assert.Len(t, resolved, 1, "only the class missing this week is resolved") {
		assert.Equal(t, "StoppedRecurring", resolved[0].AggregationKey)
		assert.Equal(t, 2, resolved[0].Weeks)
	}
}

// The same aggregation key in two accounts is two unrelated incidents with
// independent recurrence histories. Keying carry-over on the class alone would
// mark one account's class as carried over because the *other* account's
// appeared — and would hide a class that genuinely stopped.
func TestAnnotateCarryOverSameKeyDifferentAccounts(t *testing.T) {
	findings := []classFinding{{AggregationKey: "HighErrorCriticalLogs", AccountID: "acct-a"}}
	prior := []events.PriorClass{
		{AggregationKey: "HighErrorCriticalLogs", AccountID: "acct-a", Weeks: 2},
		{AggregationKey: "HighErrorCriticalLogs", AccountID: "acct-b", Weeks: 4},
	}

	resolved := annotateCarryOver(findings, prior,
		[]events.ClassRef{ref("acct-a", "HighErrorCriticalLogs")})

	assert.Equal(t, 2, findings[0].CarriedOverWeeks, "takes acct-a's history, not acct-b's")
	if assert.Len(t, resolved, 1, "acct-b's class stopped and must be reported") {
		assert.Equal(t, "acct-b", resolved[0].AccountID)
	}
}

// A first-ever digest has no prior weeks. Every class must read as new and
// nothing may be reported as resolved — an empty history is not evidence that
// anything stopped.
func TestAnnotateCarryOverNoHistory(t *testing.T) {
	findings := []classFinding{{AggregationKey: "OnlyClass", AccountID: "acct-a"}}

	assert.Empty(t, annotateCarryOver(findings, nil, []events.ClassRef{ref("acct-a", "OnlyClass")}),
		"no prior weeks means nothing resolved")
	assert.Zero(t, findings[0].CarriedOverWeeks)
}

// A class the period still saw but that ranked below the map-stage bound has no
// finding, yet it plainly did not stop. Presence is decided by the period's full
// class list, never by the summarised subset.
func TestAnnotateCarryOverUnsummarisedClassIsNotResolved(t *testing.T) {
	findings := []classFinding{{AggregationKey: "Summarised", AccountID: "acct-a"}}
	prior := []events.PriorClass{{AggregationKey: "BelowTheCut", AccountID: "acct-a", Weeks: 3}}

	resolved := annotateCarryOver(findings, prior,
		[]events.ClassRef{ref("acct-a", "Summarised"), ref("acct-a", "BelowTheCut")})

	assert.Empty(t, resolved, "a class still present this period has not stopped")
}
