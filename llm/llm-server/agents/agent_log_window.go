package agents

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// The window a log fetch runs with.
//
// Three parties state one: the user (the original request), the logs agent
// (the fetch command it wrote) and the plan the model produced from both. One
// rule settles them:
//
//	the fetch's window, cut down to the user's.
//
// A fetch inside the user's window keeps its own window. That is how the logs
// agent reads the five minutes before the first error, or the part of a window
// that a read which stopped at its line limit did not reach. A fetch wider than
// the user's window is cut back to it, so a paraphrase cannot widen what the
// user asked for. A fetch that misses the user's window altogether stands when
// the fetch command states it: that is a read of some other time on purpose,
// such as the same hour the day before. When only the plan came up with it, it
// gets the user's.
//
// The user's window counts only when the original request is the user's own
// text (OriginalQueryFromUser). An event investigation's "original request" is
// a generated prompt, and a phrase in it like "last 2h" is an instruction to
// the model, not a window anyone asked for.
//
// Where the fetch's window comes from, in order: an explicit "from A to B" in
// the fetch command, a relative range in the fetch command, the plan's own
// window. The command comes first because the plan is a model's reading of it
// and can drop it.

// logWindow is a window as one party asked for it.
type logWindow struct {
	start time.Time
	end   time.Time
	// endsNow: no end was named, so end is the `now` this was resolved with and
	// the real end is whenever the query runs. Written back without an end, so
	// "now" is not frozen at the moment the plan was checked.
	endsNow bool
	// rangeText is set, with endsNow, for a bare relative range, as written
	// ("30m"): start is that long before end.
	rangeText string
}

func (w logWindow) isBareRange() bool { return w.endsNow && w.rangeText != "" }

const logTimestampPattern = `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`

var (
	logTimestampRE = regexp.MustCompile(logTimestampPattern)
	// explicitLogWindowRE matches a window stated as two RFC3339 instants joined
	// by a connective: "from A to B", "between A and B", "A to B", "A/B".
	explicitLogWindowRE = regexp.MustCompile(`(?i)(` + logTimestampPattern + `)\s*(?:to|and|until|through|/|-|–|—)\s*(` + logTimestampPattern + `)`)
)

// commandLogWindow reads the window the fetch command states.
//
// An absolute window counts only as exactly two RFC3339 instants joined by a
// connective, in order, in a command that states no relative range as well:
// two timestamps that are search terms, or sit beside "last 1h", are not a
// window. planEnd is the plan's end instant, used when the command anchors a
// relative range ("last 30m ending at ...").
func commandLogWindow(command string, planEnd time.Time, now time.Time) (logWindow, bool) {
	_, rangeText, _, hasRange := parseExplicitLogQueryConstraints(command)
	if hasRange {
		return relativeLogWindow(rangeText, command, planEnd, now)
	}
	if len(logTimestampRE.FindAllString(command, -1)) != 2 {
		return logWindow{}, false
	}
	m := explicitLogWindowRE.FindStringSubmatch(command)
	if m == nil {
		return logWindow{}, false
	}
	start, errStart := time.Parse(time.RFC3339Nano, m[1])
	end, errEnd := time.Parse(time.RFC3339Nano, m[2])
	if errStart != nil || errEnd != nil || !start.Before(end) {
		return logWindow{}, false
	}
	return logWindow{start: start.UTC(), end: end.UTC()}, true
}

// relativeLogWindow resolves "last <range>" as text states it: ending at
// planEnd when the text anchors it to an end, otherwise ending now.
func relativeLogWindow(rangeText string, text string, planEnd time.Time, now time.Time) (logWindow, bool) {
	window, ok := parseRelativeWindow(rangeText)
	if !ok || window <= 0 {
		return logWindow{}, false
	}
	if !planEnd.IsZero() && explicitLogEndAnchorRE.MatchString(text) {
		return logWindow{start: planEnd.Add(-window), end: planEnd}, true
	}
	return logWindow{start: now.Add(-window), end: now, endsNow: true, rangeText: rangeText}, true
}

// planLogWindow reads the window the plan itself carries, and the plan's end
// instant when it names one. A canonical plan is read by the same resolver
// logs_execute_v2 runs it with, so every form the tool honours counts here.
func planLogWindow(plan logQueryPlan, now time.Time) (window logWindow, ok bool, planEnd time.Time) {
	if plan.QueryType == "native" {
		native := plan.Native
		if value := strings.TrimSpace(native.EndTime); value != "" {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				planEnd = parsed.UTC()
			}
		}
		end, endsNow := planEnd, false
		if end.IsZero() {
			end, endsNow = now, true
		}
		if value := strings.TrimSpace(native.StartTime); value != "" {
			start, err := time.Parse(time.RFC3339, value)
			if err != nil || !start.Before(end) {
				return logWindow{}, false, planEnd
			}
			return logWindow{start: start.UTC(), end: end, endsNow: endsNow}, true, planEnd
		}
		if value := strings.TrimSpace(native.TimeRange); value != "" {
			span, valid := parseRelativeWindow(value)
			if !valid || span <= 0 {
				return logWindow{}, false, planEnd
			}
			if endsNow {
				return logWindow{start: end.Add(-span), end: end, endsNow: true, rangeText: value}, true, planEnd
			}
			return logWindow{start: end.Add(-span), end: end}, true, planEnd
		}
		return logWindow{}, false, planEnd
	}

	encoded, err := json.Marshal(plan.CanonicalQuery)
	if err != nil {
		return logWindow{}, false, planEnd
	}
	query, err := toolcore.BuildLogQueryBuilder(toolcore.NbToolContext{}, string(encoded))
	if err != nil {
		return logWindow{}, false, planEnd
	}
	if value := strings.TrimSpace(query.EndTime); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			planEnd = parsed.UTC()
		}
	}
	resolved, found := tools.ResolveLogQueryWindow(query, nil, now)
	if !found {
		return logWindow{}, false, planEnd
	}
	window = logWindow{start: resolved.Start, end: resolved.End, endsNow: resolved.EndsNow}
	if resolved.Range > 0 {
		window.rangeText = strings.TrimSpace(query.TimeRange)
	}
	return window, true, planEnd
}

// userLogWindow is the window the user's own request states. Nothing is read
// from an original request the user did not write.
func userLogWindow(request core.NBAgentRequest, planEnd time.Time, now time.Time) (logWindow, bool) {
	original := strings.TrimSpace(request.OriginalQuery)
	if !request.OriginalQueryFromUser || original == "" {
		return logWindow{}, false
	}
	_, rangeText, _, hasRange := parseExplicitLogQueryConstraints(original)
	if !hasRange {
		return logWindow{}, false
	}
	return relativeLogWindow(rangeText, original, planEnd, now)
}

// boundLogWindow cuts the fetch's window down to the user's. fromCommand says
// the fetch command states the fetch's window, not only the plan. changed is
// false when the fetch's window stands as it is.
func boundLogWindow(fetch logWindow, user logWindow, fromCommand bool) (result logWindow, changed bool) {
	// Two bare ranges both end when the query runs: the shorter one is the
	// answer, and stays a range.
	if fetch.isBareRange() && user.isBareRange() {
		if !fetch.start.Before(user.start) {
			return fetch, false
		}
		return user, true
	}
	start, end := fetch.start, fetch.end
	if user.start.After(start) {
		start = user.start
	}
	if user.end.Before(end) {
		end = user.end
	}
	switch {
	case !start.Before(end):
		// No overlap: the fetch is about some other time than the user asked for.
		if fromCommand {
			return fetch, false
		}
		return user, true
	case start.Equal(fetch.start) && end.Equal(fetch.end):
		return fetch, false
	case start.Equal(user.start) && end.Equal(user.end):
		// The fetch covers the user's whole window; say it the way the user did.
		return user, true
	}
	endsNow := fetch.endsNow
	if end.Equal(user.end) && !end.Equal(fetch.end) {
		endsNow = user.endsNow
	}
	return logWindow{start: start, end: end, endsNow: endsNow}, true
}

// applyLogPlanConstraints settles the plan's window and limit from the fetch
// command, the plan and the user's request.
//
// A window under five minutes is still widened by the log tool when it runs a
// canonical query (ExpandNarrowTimeWindow), by up to fifteen minutes either
// side. That widening is deliberate for alert windows of a few seconds and is
// not undone here, so such a fetch can reach slightly past the user's window.
func applyLogPlanConstraints(request core.NBAgentRequest, plan *logQueryPlan, now time.Time) {
	commandLimit, _, commandHasLimit, _ := parseExplicitLogQueryConstraints(request.Query)
	limit, hasLimit := commandLimit, commandHasLimit
	if request.OriginalQueryFromUser {
		if userLimit, _, userHasLimit, _ := parseExplicitLogQueryConstraints(request.OriginalQuery); userHasLimit {
			limit, hasLimit = userLimit, true
		}
	}
	if hasLimit {
		if plan.QueryType == "native" {
			plan.Native.Limit = limit
		} else {
			plan.CanonicalQuery["limit"] = limit
		}
	}

	planWindow, planHasWindow, planEnd := planLogWindow(*plan, now)
	fetch, hasFetch := commandLogWindow(request.Query, planEnd, now)
	fromCommand := hasFetch
	if !hasFetch {
		fetch, hasFetch = planWindow, planHasWindow
	}
	user, hasUser := userLogWindow(request, planEnd, now)
	// A native plan that states a start and a range as well is rejected when it
	// runs. It is settled on the window read from it, which is its start.
	statedTwice := plan.QueryType == "native" && strings.TrimSpace(plan.Native.StartTime) != "" && strings.TrimSpace(plan.Native.TimeRange) != ""

	switch {
	case hasFetch && hasUser:
		result, changed := boundLogWindow(fetch, user, fromCommand)
		if changed || fromCommand || statedTwice {
			writeLogPlanWindow(plan, result)
		}
	case hasFetch:
		if fromCommand || statedTwice {
			writeLogPlanWindow(plan, fetch)
		}
	case hasUser:
		writeLogPlanWindow(plan, user)
	}
}

// writeLogPlanWindow makes w the plan's only window.
func writeLogPlanWindow(plan *logQueryPlan, w logWindow) {
	start, end, timeRange := w.start.UTC().Format(time.RFC3339Nano), w.end.UTC().Format(time.RFC3339Nano), ""
	switch {
	case w.isBareRange():
		start, end, timeRange = "", "", w.rangeText
	case w.endsNow:
		end = ""
	}
	if plan.QueryType == "native" {
		plan.Native.StartTime, plan.Native.EndTime, plan.Native.TimeRange = start, end, timeRange
		return
	}
	for _, key := range []string{"time_range", "range", "duration", "start_time", "end_time", "start", "end", "starttime", "endtime"} {
		delete(plan.CanonicalQuery, key)
	}
	if timeRange != "" {
		plan.CanonicalQuery["time_range"] = timeRange
	}
	if start != "" {
		plan.CanonicalQuery["start_time"] = start
	}
	if end != "" {
		plan.CanonicalQuery["end_time"] = end
	}
}
