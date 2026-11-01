package agents

import (
	"os"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var logWindowTestNow = time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)

const (
	userAsksLastDay  = "@k8s_orchestrator Investigate what the api workload in namespace shop has been logging over the last 24 hours: what stands out, and when did it start?"
	fetchSevenToNine = "all logs for app api in shop from 2026-10-01T07:00:00Z to 2026-10-01T09:00:00Z, limit 5000"
)

func canonicalPlan(fields map[string]any) logQueryPlan {
	query := map[string]any{"where": map[string]any{"app": map[string]any{"_eq": "api"}}}
	for k, v := range fields {
		query[k] = v
	}
	return logQueryPlan{QueryType: "canonical", CanonicalQuery: query}
}

func nativePlan(native nativeLogQueryPlan) logQueryPlan {
	native.Query = `{app="api", namespace="shop"}`
	return logQueryPlan{QueryType: "native", Native: native}
}

// canonicalWindow lists the window controls a canonical plan ends up with.
func canonicalWindow(plan logQueryPlan) map[string]any {
	got := map[string]any{}
	for _, key := range []string{"time_range", "range", "start_time", "end_time"} {
		if v, ok := plan.CanonicalQuery[key]; ok {
			got[key] = v
		}
	}
	return got
}

// The logs agent asks for a slice of the window the user gave. It gets that
// slice, however the plan came back.
func TestApplyLogPlanConstraints_FetchInsideTheUsersWindow(t *testing.T) {
	request := core.NBAgentRequest{Query: fetchSevenToNine, OriginalQuery: userAsksLastDay, OriginalQueryFromUser: true}
	want := map[string]any{"start_time": "2026-10-01T07:00:00Z", "end_time": "2026-10-01T09:00:00Z"}

	for name, plan := range map[string]logQueryPlan{
		"plan kept the bounds":                canonicalPlan(map[string]any{"start_time": "2026-10-01T07:00:00Z", "end_time": "2026-10-01T09:00:00Z"}),
		"plan kept the bounds and added 2h":   canonicalPlan(map[string]any{"start_time": "2026-10-01T07:00:00Z", "end_time": "2026-10-01T09:00:00Z", "time_range": "2h"}),
		"plan dropped them for the user's":    canonicalPlan(map[string]any{"time_range": "24h"}),
		"plan stated no window":               canonicalPlan(nil),
		"plan wrote them as an ISO interval":  canonicalPlan(map[string]any{"time_range": "2026-10-01T07:00:00Z/2026-10-01T09:00:00Z"}),
		"plan wrote them as timestamp bounds": canonicalPlan(map[string]any{"where": map[string]any{"timestamp": map[string]any{"_gte": "2026-10-01T07:00:00Z", "_lte": "2026-10-01T09:00:00Z"}}}),
	} {
		t.Run("canonical/"+name, func(t *testing.T) {
			applyLogPlanConstraints(request, &plan, logWindowTestNow)
			assert.Equal(t, want, canonicalWindow(plan))
			assert.Equal(t, 5000, plan.CanonicalQuery["limit"])
		})
	}

	for name, native := range map[string]nativeLogQueryPlan{
		"plan kept the bounds":             {StartTime: "2026-10-01T07:00:00Z", EndTime: "2026-10-01T09:00:00Z"},
		"plan dropped them for the user's": {TimeRange: "24h"},
		"plan stated no window":            {},
	} {
		t.Run("native/"+name, func(t *testing.T) {
			plan := nativePlan(native)
			applyLogPlanConstraints(request, &plan, logWindowTestNow)
			assert.Equal(t, "2026-10-01T07:00:00Z", plan.Native.StartTime)
			assert.Equal(t, "2026-10-01T09:00:00Z", plan.Native.EndTime)
			assert.Empty(t, plan.Native.TimeRange)
			assert.Equal(t, 5000, plan.Native.Limit)
		})
	}
}

// A plan that states its window in a form only the log tool reads is left as
// it is when that window is inside the user's: writing the user's range over it
// is how the window used to be lost.
func TestApplyLogPlanConstraints_PlanWindowInAnyFormIsNotOverwritten(t *testing.T) {
	// The command names no window; the plan does.
	request := core.NBAgentRequest{Query: "logs for app api around the first error", OriginalQuery: userAsksLastDay, OriginalQueryFromUser: true}

	interval := canonicalPlan(map[string]any{"time_range": "2026-10-01T07:00:00Z/2026-10-01T09:00:00Z"})
	applyLogPlanConstraints(request, &interval, logWindowTestNow)
	assert.Equal(t, map[string]any{"time_range": "2026-10-01T07:00:00Z/2026-10-01T09:00:00Z"}, canonicalWindow(interval))

	bounds := canonicalPlan(map[string]any{"where": map[string]any{"timestamp": map[string]any{"_gte": "2026-10-01T07:00:00Z", "_lte": "2026-10-01T09:00:00Z"}}})
	applyLogPlanConstraints(request, &bounds, logWindowTestNow)
	assert.Empty(t, canonicalWindow(bounds), "no range written beside the filter's own bounds")

	pair := nativePlan(nativeLogQueryPlan{StartTime: "2026-10-01T07:00:00Z", EndTime: "2026-10-01T09:00:00Z"})
	applyLogPlanConstraints(request, &pair, logWindowTestNow)
	assert.Equal(t, "2026-10-01T07:00:00Z", pair.Native.StartTime)
	assert.Equal(t, "2026-10-01T09:00:00Z", pair.Native.EndTime)
	assert.Empty(t, pair.Native.TimeRange)
}

// A native plan that states a start and a range cannot run. It is settled on
// its start, with or without a window from the user.
func TestApplyLogPlanConstraints_NativeWindowStatedTwiceIsSettled(t *testing.T) {
	for name, request := range map[string]core.NBAgentRequest{
		"inside the user's window": {Query: "logs for app api", OriginalQuery: userAsksLastDay, OriginalQueryFromUser: true},
		"no user window":           {Query: "logs for app api"},
	} {
		t.Run(name, func(t *testing.T) {
			plan := nativePlan(nativeLogQueryPlan{StartTime: "2026-10-01T08:00:00Z", TimeRange: "30m"})
			applyLogPlanConstraints(request, &plan, logWindowTestNow)
			assert.Equal(t, "2026-10-01T08:00:00Z", plan.Native.StartTime)
			assert.Empty(t, plan.Native.TimeRange)

			start, end, _, _, err := nativeQueryExecutionArgs(plan.Native, services_server.ObservabilityProvider{}, logWindowTestNow)
			require.NoError(t, err)
			assert.Equal(t, time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), start)
			assert.Equal(t, logWindowTestNow, end)
		})
	}
}

// The user's window bounds a fetch from both sides.
func TestApplyLogPlanConstraints_UsersWindowBoundsTheFetch(t *testing.T) {
	user := func(text string) core.NBAgentRequest {
		return core.NBAgentRequest{OriginalQuery: text, OriginalQueryFromUser: true}
	}

	t.Run("a fetch that straddles the start keeps the part inside", func(t *testing.T) {
		request := user(userAsksLastDay)
		// The user's window starts at 2026-09-30T09:30:00Z.
		request.Query = "all logs for app api from 2026-09-30T09:28:00Z to 2026-09-30T09:33:00Z, limit 500"
		plan := canonicalPlan(nil)
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, map[string]any{"start_time": "2026-09-30T09:30:00Z", "end_time": "2026-09-30T09:33:00Z"}, canonicalWindow(plan))
	})

	t.Run("a wider relative range is cut back to the user's", func(t *testing.T) {
		request := user("show api errors last 30m")
		request.Query = "all logs for app api last 24h, limit 5000"
		plan := canonicalPlan(map[string]any{"time_range": "24h"})
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, map[string]any{"time_range": "30m"}, canonicalWindow(plan))
	})

	t.Run("a narrower relative range is kept", func(t *testing.T) {
		request := user(userAsksLastDay)
		request.Query = "all logs for app api last 30m, limit 500"
		plan := canonicalPlan(map[string]any{"time_range": "24h"})
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, map[string]any{"time_range": "30m"}, canonicalWindow(plan))
	})

	t.Run("a fetch from before the user's window to now is the user's window", func(t *testing.T) {
		request := user("show api errors last 30m")
		request.Query = "logs for app api"
		plan := nativePlan(nativeLogQueryPlan{StartTime: "2026-09-24T10:00:00Z"})
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, "30m", plan.Native.TimeRange, "still ends when the query runs, not when the plan was checked")
		assert.Empty(t, plan.Native.StartTime)
		assert.Empty(t, plan.Native.EndTime)
	})

	t.Run("a fetch starting inside the user's window and running to now keeps its start", func(t *testing.T) {
		request := user(userAsksLastDay)
		request.Query = "logs for app api"
		plan := canonicalPlan(map[string]any{"start_time": "2026-10-01T08:00:00Z"})
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, map[string]any{"start_time": "2026-10-01T08:00:00Z"}, canonicalWindow(plan))
	})

	t.Run("a fetch the command states for some other time stands", func(t *testing.T) {
		request := user("compare api errors in the last 1h with the same hour yesterday")
		request.Query = "all logs for app api from 2026-09-30T08:30:00Z to 2026-09-30T09:30:00Z"
		want := map[string]any{"start_time": "2026-09-30T08:30:00Z", "end_time": "2026-09-30T09:30:00Z"}
		for name, plan := range map[string]logQueryPlan{
			"plan kept it":            canonicalPlan(map[string]any{"start_time": "2026-09-30T08:30:00Z", "end_time": "2026-09-30T09:30:00Z"}),
			"plan took the user's 1h": canonicalPlan(map[string]any{"time_range": "1h"}),
			"plan stated no window":   canonicalPlan(nil),
		} {
			applyLogPlanConstraints(request, &plan, logWindowTestNow)
			assert.Equal(t, want, canonicalWindow(plan), name)
		}
	})

	t.Run("some other time that only the plan came up with gets the user's", func(t *testing.T) {
		request := user("show api errors last 30m")
		request.Query = "logs for app api"
		plan := canonicalPlan(map[string]any{"start_time": "2026-09-20T07:00:00Z", "end_time": "2026-09-20T09:00:00Z"})
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, map[string]any{"time_range": "30m"}, canonicalWindow(plan))
	})

	t.Run("a window the user anchored in the past is treated the same way", func(t *testing.T) {
		request := user("show api errors last 30m ending at 2026-09-20T10:00:00Z")
		request.Query = "all logs for app api from 2026-09-20T09:40:00Z to 2026-09-20T09:50:00Z"
		inside := canonicalPlan(map[string]any{"time_range": "30m", "end_time": "2026-09-20T10:00:00Z"})
		applyLogPlanConstraints(request, &inside, logWindowTestNow)
		assert.Equal(t, map[string]any{"start_time": "2026-09-20T09:40:00Z", "end_time": "2026-09-20T09:50:00Z"}, canonicalWindow(inside))

		request.Query = "all logs for app api from 2026-09-19T09:40:00Z to 2026-09-19T09:50:00Z"
		outside := canonicalPlan(map[string]any{"time_range": "30m", "end_time": "2026-09-20T10:00:00Z"})
		applyLogPlanConstraints(request, &outside, logWindowTestNow)
		assert.Equal(t, map[string]any{"start_time": "2026-09-19T09:40:00Z", "end_time": "2026-09-19T09:50:00Z"}, canonicalWindow(outside), "the day before, stated by the command, stands")
	})
}

// An event investigation's original request is the generated investigation
// prompt. It says "Do NOT fetch broad relative ranges like \"last 2h\"", and
// that sentence used to be read as a two-hour window: about half of the log
// reads in event investigations ran over the last two hours instead of the
// incident's.
func TestApplyLogPlanConstraints_GeneratedPromptSuppliesNothing(t *testing.T) {
	raw, err := os.ReadFile("../prompts/default/v1/utilities/event_investigation.yaml")
	require.NoError(t, err)
	investigationPrompt := string(raw)
	_, rangeText, _, hasRange := parseExplicitLogQueryConstraints(investigationPrompt)
	require.True(t, hasRange, "the prompt still contains a phrase that parses as a window")
	require.Equal(t, "2h", rangeText)

	request := core.NBAgentRequest{
		Query:         "all logs for pod api-7d9 in shop from 2026-10-01T03:00:00Z to 2026-10-01T03:20:00Z, limit 500",
		OriginalQuery: investigationPrompt,
		// Not the user's text: the conversation is an event investigation.
		OriginalQueryFromUser: false,
	}

	t.Run("the incident window the logs agent asked for stands", func(t *testing.T) {
		canonical := canonicalPlan(map[string]any{"start_time": "2026-10-01T03:00:00Z", "end_time": "2026-10-01T03:20:00Z"})
		applyLogPlanConstraints(request, &canonical, logWindowTestNow)
		assert.Equal(t, map[string]any{"start_time": "2026-10-01T03:00:00Z", "end_time": "2026-10-01T03:20:00Z"}, canonicalWindow(canonical))

		native := nativePlan(nativeLogQueryPlan{StartTime: "2026-10-01T03:00:00Z", EndTime: "2026-10-01T03:20:00Z"})
		applyLogPlanConstraints(request, &native, logWindowTestNow)
		assert.Equal(t, "2026-10-01T03:00:00Z", native.Native.StartTime)
		assert.Equal(t, "2026-10-01T03:20:00Z", native.Native.EndTime)
		assert.Empty(t, native.Native.TimeRange)
	})

	t.Run("a fetch with no window of its own gets none from the prompt", func(t *testing.T) {
		request.Query = "all logs for pod api-7d9 in shop"
		planned := canonicalPlan(map[string]any{"time_range": "24h"})
		applyLogPlanConstraints(request, &planned, logWindowTestNow)
		assert.Equal(t, map[string]any{"time_range": "24h"}, canonicalWindow(planned))

		bare := canonicalPlan(nil)
		applyLogPlanConstraints(request, &bare, logWindowTestNow)
		assert.Empty(t, canonicalWindow(bare), "left to the log tool, which falls back to the event's own window")
	})

	t.Run("the same prompt as a user's text would still bound the fetch", func(t *testing.T) {
		request.Query = "all logs for pod api-7d9 in shop"
		request.OriginalQueryFromUser = true
		plan := canonicalPlan(map[string]any{"time_range": "24h"})
		applyLogPlanConstraints(request, &plan, logWindowTestNow)
		assert.Equal(t, map[string]any{"time_range": "2h"}, canonicalWindow(plan), "which is why the flag, not the text, decides")
	})
}

// With no user window, the fetch command decides, as it did before the plan
// step existed: its explicit window and limit override what the plan wrote.
func TestApplyLogPlanConstraints_CommandOverridesThePlan(t *testing.T) {
	plan := canonicalPlan(map[string]any{"limit": 5000, "range": "24h"})
	applyLogPlanConstraints(core.NBAgentRequest{Query: "logs last 15m limit 25"}, &plan, logWindowTestNow)
	assert.Equal(t, 25, plan.CanonicalQuery["limit"])
	assert.Equal(t, map[string]any{"time_range": "15m"}, canonicalWindow(plan))
	assert.Equal(t, map[string]any{"app": map[string]any{"_eq": "api"}}, plan.CanonicalQuery["where"])

	untouched := canonicalPlan(map[string]any{"limit": 5000})
	applyLogPlanConstraints(core.NBAgentRequest{Query: "find errors"}, &untouched, logWindowTestNow)
	assert.Equal(t, 5000, untouched.CanonicalQuery["limit"])
	assert.Empty(t, canonicalWindow(untouched))

	noOriginalWindow := canonicalPlan(map[string]any{"start_time": "2026-10-01T07:00:00Z", "end_time": "2026-10-01T09:00:00Z"})
	applyLogPlanConstraints(core.NBAgentRequest{Query: "logs for app api", OriginalQuery: "why is api failing", OriginalQueryFromUser: true}, &noOriginalWindow, logWindowTestNow)
	assert.Equal(t, map[string]any{"start_time": "2026-10-01T07:00:00Z", "end_time": "2026-10-01T09:00:00Z"}, canonicalWindow(noOriginalWindow))
}

// The user's limit wins over the command's only when the original request is
// the user's own.
func TestApplyLogPlanConstraints_Limit(t *testing.T) {
	request := core.NBAgentRequest{Query: "all logs for app api limit 500", OriginalQuery: "show api logs limit 100"}

	generated := nativePlan(nativeLogQueryPlan{Limit: 1000})
	applyLogPlanConstraints(request, &generated, logWindowTestNow)
	assert.Equal(t, 500, generated.Native.Limit)

	request.OriginalQueryFromUser = true
	fromUser := nativePlan(nativeLogQueryPlan{Limit: 1000})
	applyLogPlanConstraints(request, &fromUser, logWindowTestNow)
	assert.Equal(t, 100, fromUser.Native.Limit)
}

func TestCommandLogWindow(t *testing.T) {
	noEnd := time.Time{}
	absolute := func(start, end string) logWindow {
		s, _ := time.Parse(time.RFC3339, start)
		e, _ := time.Parse(time.RFC3339, end)
		return logWindow{start: s, end: e}
	}

	for command, want := range map[string]logWindow{
		"all logs for app api from 2026-10-01T07:00:00Z to 2026-10-01T09:00:00Z, limit 5000": absolute("2026-10-01T07:00:00Z", "2026-10-01T09:00:00Z"),
		"all logs for app api between 2026-10-01T07:00:00Z and 2026-10-01T09:00:00Z":         absolute("2026-10-01T07:00:00Z", "2026-10-01T09:00:00Z"),
		"logs 2026-10-01T07:00:00Z/2026-10-01T09:00:00Z for app api":                         absolute("2026-10-01T07:00:00Z", "2026-10-01T09:00:00Z"),
		"logs from 2026-10-01T12:30:00.5+05:30 to 2026-10-01T14:30:00+05:30":                 absolute("2026-10-01T07:00:00.5Z", "2026-10-01T09:00:00Z"),
		"all logs for app api last 30m, limit 500":                                           {start: logWindowTestNow.Add(-30 * time.Minute), end: logWindowTestNow, endsNow: true, rangeText: "30m"},
		`{"where":{"app":{"_eq":"api"}},"time_range":"15m","limit":200}`:                     {start: logWindowTestNow.Add(-15 * time.Minute), end: logWindowTestNow, endsNow: true, rangeText: "15m"},
	} {
		got, ok := commandLogWindow(command, noEnd, logWindowTestNow)
		require.True(t, ok, command)
		assert.True(t, want.start.Equal(got.start) && want.end.Equal(got.end), "%s: got %s → %s", command, got.start, got.end)
		assert.Equal(t, want.endsNow, got.endsNow, command)
		assert.Equal(t, want.rangeText, got.rangeText, command)
	}

	for _, command := range []string{
		"all logs for app api",
		// Search terms, not a window.
		`logs mentioning "2026-10-01T07:00:00Z" and also "2026-10-01T09:00:00Z" for app api`,
		"errors at 2026-10-01T07:00:00Z, again at 2026-10-01T09:00:00Z",
		// One instant, or three.
		"logs since 2026-10-01T07:00:00Z",
		"logs from 2026-10-01T07:00:00Z to 2026-10-01T09:00:00Z excluding 2026-10-01T08:00:00Z",
		// Backwards.
		"logs from 2026-10-01T09:00:00Z to 2026-10-01T07:00:00Z",
	} {
		_, ok := commandLogWindow(command, noEnd, logWindowTestNow)
		assert.False(t, ok, command)
	}

	t.Run("a relative range beside two timestamps is the window", func(t *testing.T) {
		got, ok := commandLogWindow("logs last 1h mentioning 2026-10-01T07:00:00Z to 2026-10-01T09:00:00Z", noEnd, logWindowTestNow)
		require.True(t, ok)
		assert.Equal(t, "1h", got.rangeText)
	})

	t.Run("a relative range the command anchors ends where the plan says", func(t *testing.T) {
		planEnd := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
		got, ok := commandLogWindow("show api errors last 30m ending at 2026-09-20T10:00:00Z", planEnd, logWindowTestNow)
		require.True(t, ok)
		assert.Equal(t, planEnd.Add(-30*time.Minute), got.start)
		assert.Equal(t, planEnd, got.end)
		assert.False(t, got.endsNow)
	})
}
