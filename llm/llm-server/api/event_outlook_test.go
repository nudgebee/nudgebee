package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/events"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var outlookBase = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

func firingsAt(hours ...float64) []time.Time {
	out := make([]time.Time, len(hours))
	for i, h := range hours {
		out[i] = outlookBase.Add(time.Duration(h * float64(time.Hour)))
	}
	return out
}

func outlookAt(h float64) time.Time { return outlookBase.Add(time.Duration(h * float64(time.Hour))) }

func TestReadFirings(t *testing.T) {
	tests := []struct {
		name    string
		times   []time.Time
		now     time.Time
		pattern firingPattern
		count   int
		check   func(t *testing.T, h firingHistory)
	}{
		{name: "first", times: firingsAt(10), now: outlookAt(11), pattern: firingFirst, count: 1},
		{name: "flapping is one firing", times: firingsAt(0, 0.05, 0.1, 24), now: outlookAt(25), pattern: firingFew, count: 2},
		{
			name: "regular", times: firingsAt(0, 2, 4.1, 6, 8.05, 10), now: outlookAt(10.5), pattern: firingRegular, count: 6,
			check: func(t *testing.T, h firingHistory) {
				assert.Equal(t, 2*time.Hour, h.every.Round(time.Minute))
				assert.Equal(t, outlookAt(12), h.next.Truncate(time.Minute))
				assert.False(t, h.overdue)
			},
		},
		{
			name: "regular that stopped", times: firingsAt(0, 2, 4, 6, 8, 10), now: outlookAt(20), pattern: firingRegular, count: 6,
			check: func(t *testing.T, h firingHistory) { assert.True(t, h.overdue) },
		},
		{
			name: "burst", times: firingsAt(0, 0.5, 1, 30, 30.5, 31, 60, 60.5), now: outlookAt(61), pattern: firingBurst, count: 8,
			check: func(t *testing.T, h firingHistory) {
				assert.Equal(t, outlookAt(60.5).Add(36*time.Minute), h.by)
				assert.False(t, h.overdue)
			},
		},
		{
			name: "irregular: the prometheus OOMs", now: time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC), pattern: firingIrregular, count: 4,
			times: []time.Time{
				time.Date(2026, 10, 21, 21, 35, 0, 0, time.UTC), time.Date(2026, 10, 24, 5, 36, 0, 0, time.UTC),
				time.Date(2026, 10, 25, 15, 36, 0, 0, time.UTC), time.Date(2026, 10, 26, 13, 2, 0, 0, time.UTC),
			},
			check: func(t *testing.T, h firingHistory) {
				// The longest of the gaps so far (56 h) after the last firing.
				assert.Equal(t, time.Date(2026, 10, 28, 21, 3, 0, 0, time.UTC), h.by)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := readFirings(tt.times, tt.now)
			assert.Equal(t, tt.pattern, h.pattern)
			assert.Equal(t, tt.count, h.count)
			if tt.check != nil {
				tt.check(t, h)
			}
		})
	}
}

func TestOutlookLine(t *testing.T) {
	now := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	eta := time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC)
	irregular := readFirings([]time.Time{
		time.Date(2026, 10, 21, 21, 35, 0, 0, time.UTC), time.Date(2026, 10, 24, 5, 36, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 15, 36, 0, 0, time.UTC), time.Date(2026, 10, 26, 13, 2, 0, 0, time.UTC),
	}, now)
	first := readFirings(firingsAt(10), now)
	near := &memoryHeadroom{container: "prometheus", limitMiB: 4000, currentPct: 88.81, firstPct: 84.63, firstDate: "2026-10-23", slopePerDay: 1.07, thresholdPct: 90}
	tests := []struct {
		name string
		h    firingHistory
		m    *memoryHeadroom
		want []string
		not  []string
	}{
		{
			name: "repeat OOM near its limit: yes, cause in place",
			h:    irregular, m: near,
			want: []string{"- **Will it happen again:** Yes.", "prometheus's hourly memory peaks are at 88.8% of its 4000 MiB limit (84.6% on 2026-10-23)",
				"any spike above the usual peak can reach the limit again while it stays at 4000 MiB",
				"fired 4 times on this workload in 30 days at irregular gaps", "by Oct 28 21:03 UTC", "(as of Oct 26 14:00 UTC)"},
		},
		{
			name: "projected climb gives a time",
			h:    irregular, m: &memoryHeadroom{container: "app", limitMiB: 512, currentPct: 70, slopePerDay: 10, thresholdPct: 90, etaAt: &eta},
			want: []string{"Yes.", "rising about 10 points a day; at that rate they reach 90% of it around Oct 28 06:00 UTC"},
		},
		{
			name: "first firing with no cause in place is not called recurring",
			h:    first,
			want: []string{"Not known.", "first firing on this workload in 30 days"},
			not:  []string{"Likely", "Yes"},
		},
		{
			name: "first firing near the limit is a yes",
			h:    first, m: near,
			want: []string{"Yes.", "88.8%"},
		},
		{
			name: "pattern overdue without a cause in place is not called likely",
			h:    readFirings(firingsAt(0, 2, 4, 6, 8, 10), outlookAt(20)),
			want: []string{"Not known.", "has passed without a firing"},
			not:  []string{"Likely"},
		},
		{
			name: "low peaks: the kill was a spike",
			h:    irregular, m: &memoryHeadroom{container: "app", limitMiB: 1024, currentPct: 40},
			want: []string{"Likely.", "came from a spike well above the usual peak"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := outlookLine(tt.h, tt.m, now)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
			for _, w := range tt.not {
				assert.NotContains(t, got, w)
			}
		})
	}
}

func TestWithOutlookLine(t *testing.T) {
	line := "- **Will it happen again:** Yes. X. (as of Oct 26 14:00 UTC)"
	tests := []struct {
		name   string
		report string
		want   string
	}{
		{
			name:   "after Hurting now, replacing the agent's Getting worse",
			report: "### 📝 Event Summary\n- **Title:** t\n\n### 🔍 Quick Investigation\n- **Hurting now:** No.\n- **Getting worse:** Flat, no forecast.\n- **Alert:** real.\n\n### Causality Chain (Root Cause)\n- x",
			want:   "### 📝 Event Summary\n- **Title:** t\n\n### 🔍 Quick Investigation\n- **Hurting now:** No.\n" + line + "\n- **Alert:** real.\n\n### Causality Chain (Root Cause)\n- x",
		},
		{
			name:   "replaces an earlier run's line",
			report: "### 🔍 Quick Investigation\n- **Hurting now:** No.\n- **Will it happen again:** Likely. old (as of Oct 21 10:00 UTC)\n- **Alert:** real.",
			want:   "### 🔍 Quick Investigation\n- **Hurting now:** No.\n" + line + "\n- **Alert:** real.",
		},
		{
			name:   "no Hurting now: first line of the section",
			report: "### 🔍 Quick Investigation\n- **Alert:** real.",
			want:   "### 🔍 Quick Investigation\n" + line + "\n- **Alert:** real.",
		},
		{
			name:   "no Quick Investigation: a section after the Event Summary",
			report: "### 📝 Event Summary\n- **Title:** t\n\n### 📋 Findings\n- **Symptom:** s",
			want:   "### 📝 Event Summary\n- **Title:** t\n\n### 🔍 Quick Investigation\n" + line + "\n\n### 📋 Findings\n- **Symptom:** s",
		},
		{
			name:   "no sections at all",
			report: "free text answer",
			want:   "free text answer\n\n### 🔍 Quick Investigation\n" + line + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, withOutlookLine(tt.report, line))
		})
	}
	assert.Equal(t, "unchanged", withOutlookLine("unchanged", ""))
	twice := withOutlookLine(withOutlookLine(tests[0].report, line), line)
	assert.Equal(t, 1, strings.Count(twice, "Will it happen again"))
}

func TestOOMKilledContainer(t *testing.T) {
	pod := `{"status":{"containerStatuses":[
		{"name":"config-reloader","lastState":{}},
		{"name":"prometheus","lastState":{"terminated":{"reason":"OOMKilled","exitCode":137}}}]}}`
	var obj any
	require.NoError(t, json.Unmarshal([]byte(pod), &obj))

	t.Run("pod object in the evidence", func(t *testing.T) {
		ev := events.InvestigateData{Others: []events.InvestigateDataInsight{{Data: map[string]any{"obj": obj}}}}
		assert.Equal(t, "prometheus", oomKilledContainer(ev))
	})
	t.Run("pod object carried as JSON text", func(t *testing.T) {
		ev := events.InvestigateData{Others: []events.InvestigateDataInsight{{Data: pod}}}
		assert.Equal(t, "prometheus", oomKilledContainer(ev))
	})
	t.Run("no OOM-killed container", func(t *testing.T) {
		ev := events.InvestigateData{Others: []events.InvestigateDataInsight{{Data: map[string]any{"name": "app", "lastState": map[string]any{"terminated": map[string]any{"reason": "Error"}}}}}}
		assert.Equal(t, "", oomKilledContainer(ev))
	})
}

func TestWithOutlookLineKeepsWrappedBullet(t *testing.T) {
	report := "### 🔍 Quick Investigation\n- **Hurting now:** No, the pod restarted\n  and is running.\n- **Alert:** real."
	want := "### 🔍 Quick Investigation\n- **Hurting now:** No, the pod restarted\n  and is running.\nLINE\n- **Alert:** real."
	assert.Equal(t, want, withOutlookLine(report, "LINE"))
}

func TestOutlookLineCurrent(t *testing.T) {
	start := time.Date(2026, 10, 26, 13, 2, 30, 0, time.UTC)
	line := func(asOf string) string {
		return "### 🔍 Quick Investigation\n- **Will it happen again:** Yes. X. (as of " + asOf + " UTC)\n"
	}
	tests := []struct {
		name   string
		report string
		start  *time.Time
		want   bool
	}{
		{"computed for this firing", line("Oct 26 13:10"), &start, true},
		{"computed in the firing's minute", line("Oct 26 13:02"), &start, true},
		{"computed for an earlier firing", line("Oct 25 21:09"), &start, false},
		{"agent's Getting worse line", "- **Getting worse:** Flat, no forecast.", &start, false},
		{"no line", "### 🔍 Quick Investigation\n- **Hurting now:** No.", &start, false},
		{"no start time", line("Oct 25 21:09"), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, outlookLineCurrent(tt.report, tt.start))
		})
	}
	newYear := time.Date(2026, 12, 31, 23, 50, 0, 0, time.UTC)
	assert.True(t, outlookLineCurrent(line("Jan 1 00:05"), &newYear), "a line computed after midnight on Dec 31 is current")
	jan1 := time.Date(2027, 1, 1, 0, 10, 0, 0, time.UTC)
	assert.False(t, outlookLineCurrent(line("Dec 31 23:50"), &jan1), "a line from the old year predates a Jan 1 firing")
	assert.Nil(t, mergeFirings(nil))
	assert.Zero(t, medianDuration(nil))
}

func TestNextCriticalLevel(t *testing.T) {
	series := `a_inuse / db_sql_connection_max_open`
	rules := []events.EventRuleExpr{
		{Alert: "Filling", Expr: series + ` > 0.3`},
		{Alert: "Half", Expr: series + ` > 0.5`},
		{Alert: "Full", Expr: series + ` >= 1`},
		{Alert: "Other", Expr: `b / c > 0.4`},
		{Alert: "Empty", Expr: series + ` < 0.1`},
	}
	got, ok := nextCriticalLevel(rules, "Filling", series, ">", 0.3)
	require.True(t, ok)
	assert.Equal(t, criticalLevel{value: 0.5, name: "Half"}, got, "the nearest level past the alert's own")

	got, ok = nextCriticalLevel(rules[:1], "Filling", series, ">", 0.3)
	require.True(t, ok)
	assert.Equal(t, criticalLevel{value: 1}, got, "no other rule: the end of the scale")

	_, ok = nextCriticalLevel(rules, "Full", series, ">=", 1)
	assert.False(t, ok, "nothing lies past the end of the scale")

	_, ok = nextCriticalLevel(nil, "x", `rate(a[1m]) / rate(b[1m])`, ">", 0.5)
	assert.False(t, ok)
}

func TestCriticalWords(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 19, 0, 0, time.UTC)
	since := time.Date(2026, 11, 2, 10, 5, 0, 0, time.UTC)
	full := criticalLevel{value: 1, name: "PoolExhausted"}
	rising := tools.AlertProjection{Current: 0.4, At: at, Rising: true, RatePerHour: 1.5, Since: since,
		Earliest: at.Add(23 * time.Minute), Latest: at.Add(26 * time.Minute), Hours: 0.4}

	assert.Equal(t, "- **Becomes critical:** Between Nov 2 10:42 UTC and Nov 2 10:45 UTC, about 24 min after this reading. "+
		"Now 40% at Nov 2 10:19 UTC; `PoolExhausted` fires at 100%; moving about 2.5 points a minute since Nov 2 10:05 UTC.",
		criticalWords(rising, full, true, true))

	assert.Contains(t, criticalWords(rising, criticalLevel{value: 1}, true, true), "the limit is 100%")

	past := tools.AlertProjection{Current: 1, At: at, Past: true, PastSince: since}
	assert.Equal(t, "- **Became critical:** Yes, at Nov 2 10:05 UTC, where `PoolExhausted` fires at 100%. Now 100% at Nov 2 10:19 UTC.",
		criticalWords(past, full, true, true))

	flat := tools.AlertProjection{Current: 40, At: at, Reason: "no steady trend"}
	assert.Equal(t, "- **Becomes critical:** No time can be given: no steady trend. Now 40 at Nov 2 10:19 UTC; the limit is 100.",
		criticalWords(flat, criticalLevel{value: 100}, true, false))

	assert.Empty(t, criticalWords(flat, criticalLevel{}, false, false))
	assert.Contains(t, criticalWords(tools.AlertProjection{Current: 40, At: at, Rising: true, RatePerHour: 6}, criticalLevel{}, false, false),
		"No critical level is set for this metric. Now 40 at Nov 2 10:19 UTC, moving about 6 an hour.")
}

func TestWithOutlookLineKeepsCriticalLine(t *testing.T) {
	agent := "- **Becomes critical:** Around 10:42 [Analyze Metric Series - E6](#task-1).\n"
	report := "### 🔍 Quick Investigation\n- **Hurting now:** No.\n" + agent + "- **Will it happen again:** old.\n- **Alert:** Real.\n"

	assert.Empty(t, fallbackCriticalLine(report, "- **Becomes critical:** computed."), "the agent's line stands")
	got := withOutlookLine(report, joinOutlookLines(fallbackCriticalLine(report, "- **Becomes critical:** computed."), "- **Will it happen again:** new."))
	assert.Equal(t, "### 🔍 Quick Investigation\n- **Hurting now:** No.\n"+agent+"- **Will it happen again:** new.\n- **Alert:** Real.\n", got)

	bare := "### 🔍 Quick Investigation\n- **Hurting now:** No.\n- **Alert:** Real.\n"
	got = withOutlookLine(bare, joinOutlookLines(fallbackCriticalLine(bare, "- **Becomes critical:** computed."), "- **Will it happen again:** new."))
	assert.Equal(t, "### 🔍 Quick Investigation\n- **Hurting now:** No.\n- **Becomes critical:** computed.\n- **Will it happen again:** new.\n- **Alert:** Real.\n", got)
}

func TestWithOutlookLineKeepsWrappedCriticalLine(t *testing.T) {
	report := "### 🔍 Quick Investigation\n- **Hurting now:** No.\n- **Becomes critical:** Around 10:42\n  [Analyze Metric Series - E6](#task-1).\n- **Alert:** Real.\n"
	got := withOutlookLine(report, "- **Will it happen again:** new.")
	assert.Equal(t, "### 🔍 Quick Investigation\n- **Hurting now:** No.\n- **Becomes critical:** Around 10:42\n  [Analyze Metric Series - E6](#task-1).\n- **Will it happen again:** new.\n- **Alert:** Real.\n", got)
}

func TestNextLevelNote(t *testing.T) {
	assert.Contains(t, nextLevelNote(criticalLevel{value: 1, name: "PoolExhausted"}, true), "The next level is 1, where `PoolExhausted` fires.")
	assert.Contains(t, nextLevelNote(criticalLevel{value: 100}, true), "The next level is 100, the limit this metric is a share of.")
	assert.Contains(t, nextLevelNote(criticalLevel{}, false), "No further level is defined")
}
