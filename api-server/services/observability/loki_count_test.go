package observability

import (
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// lokiMatrix renders a query_range body: one series per (labels, points) pair, each
// point (unix seconds, value).
func lokiMatrix(series ...lokiTestSeries) map[string]any {
	result := make([]any, 0, len(series))
	for _, s := range series {
		metric := map[string]any{}
		for k, v := range s.labels {
			metric[k] = v
		}
		values := make([]any, 0, len(s.points))
		for _, p := range s.points {
			values = append(values, []any{float64(p[0]), strconv.FormatInt(p[1], 10)})
		}
		result = append(result, map[string]any{"metric": metric, "values": values})
	}
	return map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": result}}
}

type lokiTestSeries struct {
	labels map[string]string
	points [][2]int64
}

// fakeLokiRange answers query_range calls by the first registered prefix of their LogQL
// and records every call's parameters.
type fakeLokiRange struct {
	mu      sync.Mutex
	answers []struct {
		prefix string
		body   map[string]any
	}
	err   error
	fails map[string]error
	calls []url.Values
}

func (f *fakeLokiRange) fail(prefix string, err error) *fakeLokiRange {
	if f.fails == nil {
		f.fails = map[string]error{}
	}
	f.fails[prefix] = err
	return f
}

func (f *fakeLokiRange) on(prefix string, body map[string]any) *fakeLokiRange {
	f.answers = append(f.answers, struct {
		prefix string
		body   map[string]any
	}{prefix, body})
	return f
}

func (f *fakeLokiRange) run(params string) (map[string]any, error) {
	v, _ := url.ParseQuery(params)
	f.mu.Lock()
	f.calls = append(f.calls, v)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	q := v.Get("query")
	for prefix, err := range f.fails {
		// Only the unnarrowed query fails: the fallback's narrowed one names its groups.
		if strings.HasPrefix(q, prefix) && !strings.Contains(q, "=~") {
			return nil, err
		}
	}
	for _, a := range f.answers {
		if strings.HasPrefix(q, a.prefix) {
			return a.body, nil
		}
	}
	return lokiMatrix(), nil
}

func (f *fakeLokiRange) call(prefix string) url.Values {
	for _, c := range f.calls {
		if strings.HasPrefix(c.Get("query"), prefix) {
			return c
		}
	}
	return nil
}

var (
	lokiTestStart = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	lokiTestEnd   = time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
)

func lokiTestQuery(groupBy []string, interval time.Duration) LogCountQuery {
	return LogCountQuery{
		Request: FetchLogRequest{
			Query:     `{namespace="prod", level="error"} |= "timeout"`,
			StartTime: lokiTestStart.UnixMilli(),
			EndTime:   lokiTestEnd.UnixMilli(),
		},
		GroupBy:  groupBy,
		Interval: interval,
		Limit:    2,
	}
}

// lokiGridPoints stamps values on the points of q's one range piece: point i at
// first+i·step, in the seconds Loki answers with.
func lokiGridPoints(t *testing.T, q LogCountQuery, values ...int64) [][2]int64 {
	t.Helper()
	pieces, err := lokiCountPieces(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 1 || pieces[0].firstMs == pieces[0].endMs {
		t.Fatalf("pieces = %+v, want one range piece", pieces)
	}
	p := pieces[0]
	if n := (p.endMs-p.firstMs)/p.stepMs + 1; int64(len(values)) != n {
		t.Fatalf("%d values for a %d-point piece", len(values), n)
	}
	out := make([][2]int64, len(values))
	for i, v := range values {
		out[i] = [2]int64{(p.firstMs + int64(i)*p.stepMs) / 1000, v}
	}
	return out
}

func TestLokiFineStep(t *testing.T) {
	for _, tc := range []struct {
		interval, want time.Duration
	}{
		{10 * time.Minute, 5 * time.Minute},
		{time.Hour, 15 * time.Minute},
		{24 * time.Hour, 15 * time.Minute},
		{30 * time.Minute, 15 * time.Minute},
		{15 * time.Minute, 15 * time.Minute},
		{time.Minute, time.Minute},
		{7 * time.Minute, time.Minute},
		{45 * time.Second, 15 * time.Second},
		{90 * time.Second, 30 * time.Second},
	} {
		if got := lokiFineStep(tc.interval); got != tc.want {
			t.Errorf("lokiFineStep(%v) = %v, want %v", tc.interval, got, tc.want)
		}
	}
}

func lokiWindowQuery(start, end time.Time, interval time.Duration) LogCountQuery {
	q := lokiTestQuery(nil, interval)
	q.Request.StartTime, q.Request.EndTime = start.UnixMilli(), end.UnixMilli()
	return q
}

func TestLokiCountPieces(t *testing.T) {
	at := func(hm string) time.Time {
		tm, _ := time.Parse("15:04:05", hm)
		return time.Date(2026, 10, 22, tm.Hour(), tm.Minute(), tm.Second(), 0, time.UTC)
	}
	ms := func(hm string) int64 { return at(hm).UnixMilli() }
	m := time.Minute.Milliseconds()
	cases := []struct {
		name       string
		start, end string
		interval   time.Duration
		want       []lokiPiece
	}{
		{"aligned hour: interior only", "09:00:00", "10:00:00", 0,
			[]lokiPiece{{rangeMs: 15 * m, firstMs: ms("09:15:00"), endMs: ms("10:00:00"), stepMs: 15 * m}}},
		{"unaligned hour: head, interior, tail", "09:10:00", "10:10:00", 0, []lokiPiece{
			{rangeMs: 5 * m, firstMs: ms("09:15:00"), endMs: ms("09:15:00"), stepMs: 5 * m},
			{rangeMs: 15 * m, firstMs: ms("09:30:00"), endMs: ms("10:00:00"), stepMs: 15 * m},
			{rangeMs: 10 * m, firstMs: ms("10:10:00"), endMs: ms("10:10:00"), stepMs: 10 * m},
		}},
		{"inside one block: one piece", "09:02:00", "09:13:00", 0,
			[]lokiPiece{{rangeMs: 11 * m, firstMs: ms("09:13:00"), endMs: ms("09:13:00"), stepMs: 11 * m}}},
		{"one boundary, no whole block: head and tail", "09:10:00", "09:20:00", 0, []lokiPiece{
			{rangeMs: 5 * m, firstMs: ms("09:15:00"), endMs: ms("09:15:00"), stepMs: 5 * m},
			{rangeMs: 5 * m, firstMs: ms("09:20:00"), endMs: ms("09:20:00"), stepMs: 5 * m},
		}},
		{"10m interval: 5m points", "09:00:00", "10:00:00", 10 * time.Minute,
			[]lokiPiece{{rangeMs: 5 * m, firstMs: ms("09:05:00"), endMs: ms("10:00:00"), stepMs: 5 * m}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lokiCountPieces(lokiWindowQuery(at(tc.start), at(tc.end), tc.interval))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("pieces = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("piece %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}

	t.Run("too many points for Loki", func(t *testing.T) {
		q := lokiTestQuery(nil, 0)
		q.Request.StartTime = lokiTestEnd.Add(-200 * 24 * time.Hour).UnixMilli()
		if _, err := lokiCountPieces(q); err == nil || !strings.Contains(err.Error(), "too long to count on Loki") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("an interval count refuses a window off the interval grid", func(t *testing.T) {
		q := lokiWindowQuery(at("09:07:00"), at("10:00:00"), 10*time.Minute)
		if _, err := lokiCountPieces(q); err == nil || !strings.Contains(err.Error(), "aligned") {
			t.Errorf("err = %v", err)
		}
	})
}

// TestLokiCountPieces_BlockRule checks, over a spread of windows and intervals, that every
// point's range stays inside one 15-minute block and that the pieces together cover the
// window exactly once.
func TestLokiCountPieces_BlockRule(t *testing.T) {
	blockMs := lokiCountBlock.Milliseconds()
	base := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	type window struct {
		start, end time.Time
		interval   time.Duration
	}
	var windows []window
	for _, off := range []time.Duration{0, time.Millisecond, 7 * time.Minute, 10 * time.Minute, 14*time.Minute + 59*time.Second} {
		for _, length := range []time.Duration{time.Minute, 9 * time.Minute, 20 * time.Minute, time.Hour, 6*time.Hour + 123*time.Millisecond} {
			windows = append(windows, window{base.Add(off), base.Add(off + length), 0})
		}
	}
	for _, interval := range []time.Duration{time.Minute, 7 * time.Minute, 10 * time.Minute, time.Hour, 24 * time.Hour} {
		start := base.Truncate(interval)
		windows = append(windows, window{start, start.Add(3 * interval), interval})
	}
	for _, w := range windows {
		q := lokiWindowQuery(w.start, w.end, w.interval)
		pieces, err := lokiCountPieces(q)
		if err != nil {
			t.Fatalf("%v–%v @%v: %v", w.start, w.end, w.interval, err)
		}
		cover := q.Request.StartTime
		for _, p := range pieces {
			for t0 := p.firstMs; t0 <= p.endMs; t0 += p.stepMs {
				from := t0 - p.rangeMs
				if from != cover {
					t.Fatalf("%v–%v @%v: point %d covers from %d, want %d (gap or overlap)", w.start, w.end, w.interval, t0, from, cover)
				}
				if blockStart := (t0 - 1) / blockMs * blockMs; from < blockStart {
					t.Fatalf("%v–%v @%v: range (%d, %d] crosses the block boundary %d", w.start, w.end, w.interval, from, t0, blockStart)
				}
				cover = t0
			}
		}
		if cover != q.Request.EndTime {
			t.Fatalf("%v–%v @%v: pieces end at %d, want %d", w.start, w.end, w.interval, cover, q.Request.EndTime)
		}
	}
}

const lokiTestSel = `{namespace="prod", level="error"} |= "timeout"`

// lokiCount plans q and runs the plan through run, as fetchLogCount does.
func lokiCount(q LogCountQuery, run lokiQueryRange) (LogCountRows, error) {
	plan, err := lokiPlanLogCount(q)
	if err != nil {
		return LogCountRows{}, err
	}
	return lokiRunLogCount(q, plan, run)
}

func TestLokiCount_TotalOnly(t *testing.T) {
	q := lokiTestQuery(nil, 0)
	f := (&fakeLokiRange{}).on("sum(count_over_time(", lokiMatrix(lokiTestSeries{points: lokiGridPoints(t, q, 100, 200, 300, 212)}))
	rows, err := lokiCount(q, f.run)
	if err != nil {
		t.Fatal(err)
	}
	wantQ := `sum(count_over_time(` + lokiTestSel + ` [900000ms]))`
	if len(f.calls) != 1 || f.calls[0].Get("query") != wantQ {
		t.Fatalf("calls = %v, want one %s", f.calls, wantQ)
	}
	c := f.calls[0]
	if c.Get("start") != strconv.FormatInt(lokiTestStart.Add(15*time.Minute).UnixMilli()*1_000_000, 10) ||
		c.Get("end") != strconv.FormatInt(lokiTestEnd.UnixMilli()*1_000_000, 10) || c.Get("step") != "900" {
		t.Errorf("params = %v, want start+15m … end, step 900", c)
	}
	if rows.Total != 812 || len(rows.Groups) != 1 || rows.Groups[0].Values[0] != 812 || rows.Groups[0].Timestamps[0] != lokiTestEnd.Unix() {
		t.Errorf("rows = %+v, want one point of 812 at the window end", rows)
	}
}

func TestLokiCount_Grouped(t *testing.T) {
	q := lokiTestQuery([]string{"pod"}, 0)
	f := (&fakeLokiRange{}).
		on("sum(count_over_time(", lokiMatrix(lokiTestSeries{points: lokiGridPoints(t, q, 300, 300, 300, 100)})).
		on("sum by (pod)", lokiMatrix(
			lokiTestSeries{labels: map[string]string{"pod": "api"}, points: lokiGridPoints(t, q, 200, 200, 200, 50)},
			lokiTestSeries{labels: map[string]string{"pod": "worker"}, points: lokiGridPoints(t, q, 50, 50, 50, 0)},
			lokiTestSeries{labels: map[string]string{"pod": "cron"}, points: lokiGridPoints(t, q, 0, 0, 0, 1)},
		))
	rows, err := lokiCount(q, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.call(`sum by (pod) (count_over_time(`+lokiTestSel+` | pod!="" [900000ms]))`) == nil {
		t.Fatalf("calls = %v, want total + one grouped query", f.calls)
	}
	// 1000 total; 650 + 150 + 1 labelled.
	if rows.Total != 1000 || rows.Ungrouped != 199 || rows.GroupsTotal != 3 || !rows.Complete {
		t.Errorf("total/ungrouped/groups_total/complete = %d/%d/%d/%v, want 1000/199/3/true", rows.Total, rows.Ungrouped, rows.GroupsTotal, rows.Complete)
	}
	sums := map[string]float64{}
	for _, g := range rows.Groups {
		if len(g.Values) != 1 || g.Timestamps[0] != lokiTestEnd.Unix() {
			t.Errorf("group %v = %+v, want one point at the window end", g.Metric, g)
		}
		sums[g.Metric["pod"]] = g.Values[0]
	}
	if sums["api"] != 650 || sums["worker"] != 150 || sums["cron"] != 1 {
		t.Errorf("sums = %v", sums)
	}
}

func TestLokiCount_GroupedFoldsIntoBuckets(t *testing.T) {
	q := lokiTestQuery([]string{"pod"}, 30*time.Minute)
	f := (&fakeLokiRange{}).
		on("sum(count_over_time(", lokiMatrix(lokiTestSeries{points: lokiGridPoints(t, q, 1, 2, 3, 4)})).
		on("sum by (pod)", lokiMatrix(lokiTestSeries{labels: map[string]string{"pod": "api"}, points: lokiGridPoints(t, q, 1, 2, 3, 4)}))
	rows, err := lokiCount(q, f.run)
	if err != nil {
		t.Fatal(err)
	}
	api := rows.Groups[0]
	start := lokiTestStart.Unix()
	if len(api.Timestamps) != 2 || api.Timestamps[0] != start || api.Timestamps[1] != start+1800 || api.Values[0] != 3 || api.Values[1] != 7 {
		t.Errorf("api = %+v, want [3 7] at the two 30m bucket starts", api)
	}
}

func TestLokiCount_DayInHourlyBuckets(t *testing.T) {
	q := lokiTestQuery(nil, time.Hour)
	q.Request.StartTime = lokiTestEnd.Add(-24 * time.Hour).UnixMilli()
	values := make([]int64, 96)
	for i := range values {
		values[i] = int64(i % 4) // 0+1+2+3 = 6 per hour
	}
	f := (&fakeLokiRange{}).on("sum(count_over_time(", lokiMatrix(lokiTestSeries{points: lokiGridPoints(t, q, values...)}))
	rows, err := lokiCount(q, f.run)
	if err != nil {
		t.Fatal(err)
	}
	g := rows.Groups[0]
	if len(g.Timestamps) != 24 || g.Timestamps[0] != q.Request.StartTime/1000 || rows.Total != 144 {
		t.Fatalf("got %d buckets from %d, total %d; want 24 from %d, total 144", len(g.Timestamps), g.Timestamps[0], rows.Total, q.Request.StartTime/1000)
	}
	for i, v := range g.Values {
		if v != 6 {
			t.Fatalf("bucket %d = %v, want 6", i, v)
		}
	}
}

func TestLokiCount_TooManySeriesFallsBackToTopGroups(t *testing.T) {
	q := lokiTestQuery([]string{"pod"}, 0)
	f := (&fakeLokiRange{}).
		fail("sum by (pod)", errors.New("loki count: HTTP 400: maximum of series (500) reached for a single query")).
		on(`sum(count_over_time(`+lokiTestSel+` | pod!=""`, lokiMatrix(lokiTestSeries{points: lokiGridPoints(t, q, 200, 200, 200, 200)})).
		on("sum(count_over_time(", lokiMatrix(lokiTestSeries{points: lokiGridPoints(t, q, 250, 250, 250, 250)})).
		on("topk(", lokiMatrix(
			lokiTestSeries{labels: map[string]string{"pod": "api"}, points: [][2]int64{{lokiTestEnd.Unix(), 900}}},
			lokiTestSeries{labels: map[string]string{"pod": "worker"}, points: [][2]int64{{lokiTestEnd.Unix(), 400}}},
		)).
		on("count(", lokiMatrix(lokiTestSeries{points: [][2]int64{{lokiTestEnd.Unix(), 612}}})).
		on("sum by (pod) (count_over_time({", lokiMatrix(
			lokiTestSeries{labels: map[string]string{"pod": "api"}, points: lokiGridPoints(t, q, 150, 150, 150, 150)},
			lokiTestSeries{labels: map[string]string{"pod": "worker"}, points: lokiGridPoints(t, q, 50, 50, 50, 50)},
			lokiTestSeries{labels: map[string]string{"pod": "other"}, points: lokiGridPoints(t, q, 1, 0, 0, 0)},
		))
	rows, err := lokiCount(q, f.run)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`topk(2, sum by (pod) (count_over_time(` + lokiTestSel + ` | pod!="" [3600000ms])))`,
		`count(sum by (pod) (count_over_time(` + lokiTestSel + ` | pod!="" [3600000ms])))`,
		`sum(count_over_time(` + lokiTestSel + ` | pod!="" [900000ms]))`,
		`sum by (pod) (count_over_time({namespace="prod", level="error", pod=~"api|worker"} |= "timeout" | pod!="" [900000ms]))`,
	} {
		if f.call(want) == nil {
			t.Errorf("no call ran\n  %s", want)
		}
	}
	if rows.Complete || rows.Total != 1000 || rows.Ungrouped != 200 || rows.GroupsTotal != 612 || len(rows.Groups) != 2 {
		t.Errorf("complete/total/ungrouped/groups_total/groups = %v/%d/%d/%d/%d, want false/1000/200/612/2",
			rows.Complete, rows.Total, rows.Ungrouped, rows.GroupsTotal, len(rows.Groups))
	}
	if rows.Groups[0].Values[0] != 600 {
		t.Errorf("api = %+v, want the exact fine count 600, not the topk 900", rows.Groups[0])
	}
}

func TestLokiCount_RefusesEmptySelector(t *testing.T) {
	for _, sel := range []string{"", "{}"} {
		q := lokiTestQuery(nil, 0)
		q.Request.Query = sel
		if _, err := lokiPlanLogCount(q); err == nil || !strings.Contains(err.Error(), "at least one label filter") {
			t.Errorf("plan, selector %q: err = %v", sel, err)
		}
		if _, err := lokiRunLogCount(q, LogCountPlan{Queries: []string{"x"}}, (&fakeLokiRange{}).run); err == nil || !strings.Contains(err.Error(), "at least one label filter") {
			t.Errorf("run, selector %q: err = %v", sel, err)
		}
	}
}

func TestLokiCountError(t *testing.T) {
	for _, tc := range []struct{ backend, want string }{
		{"loki count: HTTP 400: maximum of series (500) reached for a single query", "max_query_series"},
		{"loki count: HTTP 400: the query time range exceeds the limit (query length: 800h, limit: 721h)", "max_query_length"},
		{"loki count: HTTP 400: exceeded maximum resolution of 11,000 points per timeseries", "wider interval"},
	} {
		f := &fakeLokiRange{err: errors.New(tc.backend)}
		_, err := lokiCount(lokiTestQuery(nil, 0), f.run)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.backend) {
			t.Errorf("err = %v, want %q and Loki's own message", err, tc.want)
		}
	}
}

func TestLokiCount_PlanIsWhatRuns(t *testing.T) {
	unaligned := lokiTestQuery(nil, 0)
	unaligned.Request.StartTime = lokiTestStart.Add(10 * time.Minute).UnixMilli()
	unaligned.Request.EndTime = lokiTestEnd.Add(10 * time.Minute).UnixMilli()
	for _, base := range []LogCountQuery{lokiTestQuery(nil, 10*time.Minute), unaligned} {
		for _, groupBy := range [][]string{nil, {"pod"}, {"pod", "namespace"}} {
			q := base
			q.GroupBy = groupBy
			plan, err := lokiPlanLogCount(q)
			if err != nil {
				t.Fatal(err)
			}
			f := &fakeLokiRange{}
			if _, err := lokiRunLogCount(q, plan, f.run); err != nil {
				t.Fatal(err)
			}
			ran := make([]string, 0, len(f.calls))
			for _, c := range f.calls {
				ran = append(ran, c.Get("query"))
			}
			sort.Strings(ran)
			want := append([]string(nil), plan.Queries...)
			sort.Strings(want)
			if strings.Join(ran, "\n") != strings.Join(want, "\n") {
				t.Errorf("interval %v, group_by %v: ran\n  %s\nplanned\n  %s", q.Interval, groupBy, strings.Join(ran, "\n  "), strings.Join(want, "\n  "))
			}
		}
	}
	// The grouped query reads only lines that have every group label.
	plan, _ := lokiPlanLogCount(lokiTestQuery([]string{"pod", "namespace"}, 0))
	want := `sum by (pod, namespace) (count_over_time(` + lokiTestSel + ` | pod!="" | namespace!="" [900000ms]))`
	if len(plan.Queries) != 2 || plan.Queries[1] != want {
		t.Errorf("plan = %q, want the grouped query\n  %s", plan.Queries, want)
	}
}

// The window 09:10–10:10 is counted as (09:10, 09:15] + three whole blocks + (10:00, 10:10],
// and every group's pieces add up to one count.
func TestLokiCount_UnalignedWindowSumsItsPieces(t *testing.T) {
	q := lokiTestQuery([]string{"pod"}, 0)
	q.Request.StartTime = lokiTestStart.Add(10 * time.Minute).UnixMilli()
	q.Request.EndTime = lokiTestEnd.Add(10 * time.Minute).UnixMilli()
	t1015 := lokiTestStart.Add(15 * time.Minute).Unix()
	t1010 := lokiTestEnd.Add(10 * time.Minute).Unix()
	grouped := func(rng string) string {
		return `sum by (pod) (count_over_time(` + lokiTestSel + ` | pod!="" [` + rng + `]))`
	}
	total := func(rng string) string { return `sum(count_over_time(` + lokiTestSel + ` [` + rng + `]))` }
	interior := [][2]int64{{t1015 + 900, 100}, {t1015 + 1800, 200}, {t1015 + 2700, 300}}
	f := (&fakeLokiRange{}).
		on(total("300000ms"), lokiMatrix(lokiTestSeries{points: [][2]int64{{t1015, 10}}})).
		on(total("900000ms"), lokiMatrix(lokiTestSeries{points: interior})).
		on(total("600000ms"), lokiMatrix(lokiTestSeries{points: [][2]int64{{t1010, 7}}})).
		on(grouped("300000ms"), lokiMatrix(lokiTestSeries{labels: map[string]string{"pod": "api"}, points: [][2]int64{{t1015, 4}}})).
		on(grouped("900000ms"), lokiMatrix(
			lokiTestSeries{labels: map[string]string{"pod": "api"}, points: [][2]int64{{t1015 + 900, 50}, {t1015 + 1800, 50}, {t1015 + 2700, 50}}},
			lokiTestSeries{labels: map[string]string{"pod": "worker"}, points: [][2]int64{{t1015 + 900, 300}}},
		)).
		on(grouped("600000ms"), lokiMatrix(lokiTestSeries{labels: map[string]string{"pod": "api"}, points: [][2]int64{{t1010, 1}}}))
	rows, err := lokiCount(q, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 6 {
		t.Fatalf("%d calls, want 6 (head, interior, tail × total, grouped)", len(f.calls))
	}
	// Head and tail are instant queries on their own block's part.
	head := f.call(total("300000ms"))
	if head.Get("start") != head.Get("end") || head.Get("end") != strconv.FormatInt(t1015*1_000_000_000, 10) {
		t.Errorf("head params = %v, want an instant query at 09:15", head)
	}
	if rows.Total != 617 || rows.GroupsTotal != 2 || rows.Ungrouped != 617-455 {
		t.Errorf("total/groups_total/ungrouped = %d/%d/%d, want 617/2/%d", rows.Total, rows.GroupsTotal, rows.Ungrouped, 617-455)
	}
	sums := map[string]float64{}
	for _, g := range rows.Groups {
		if len(g.Values) != 1 || g.Timestamps[0] != t1010 {
			t.Errorf("group %v = %+v, want one point at the window end", g.Metric, g)
		}
		sums[g.Metric["pod"]] = g.Values[0]
	}
	if len(rows.Groups) != 2 || sums["api"] != 155 || sums["worker"] != 300 {
		t.Errorf("groups = %v, want api 155 (4+150+1, merged) and worker 300", sums)
	}
}

func TestLokiRunLogCount_RefusesAPlanOfTheWrongShape(t *testing.T) {
	q := lokiTestQuery([]string{"pod"}, 0)
	if _, err := lokiRunLogCount(q, LogCountPlan{Queries: []string{"only the total"}}, (&fakeLokiRange{}).run); err == nil || !strings.Contains(err.Error(), "has 2 queries, got 1") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveLokiGroupByFields(t *testing.T) {
	labels := []OutputLogLabel{{Label: "pod"}, {Label: "namespace"}, {Label: "app"}}
	if got, err := resolveLokiGroupByFields(labels, []string{"pod", "app"}); err != nil || strings.Join(got, ",") != "pod,app" {
		t.Errorf("got %v, %v", got, err)
	}
	_, err := resolveLokiGroupByFields(labels, []string{"detected_level"})
	if err == nil || !strings.Contains(err.Error(), "not a stream label") || !strings.Contains(err.Error(), "app, namespace, pod") || !strings.Contains(err.Error(), "structured metadata") {
		t.Errorf("err = %v", err)
	}
	if _, err := resolveLokiGroupByFields(labels, []string{"log"}); err == nil || !strings.Contains(err.Error(), "not a label") {
		t.Errorf("err = %v", err)
	}
}

func TestLokiNarrowSelector(t *testing.T) {
	groups := []Result{{Metric: map[string]string{"pod": `a"b`}}, {Metric: map[string]string{"pod": "c.d"}}}
	got := lokiNarrowSelector(`{app="x}y"} |= "z"`, []string{"pod"}, groups)
	want := `{app="x}y", pod=~"a\"b|c\\.d"} |= "z"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
