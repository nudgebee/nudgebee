package observability

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func esTestFields() []OutputLogLabelFields {
	return []OutputLogLabelFields{
		{Field: "kubernetes.pod_name", Attributes: map[string]any{"type": "text"}},
		{Field: "kubernetes.pod_name.keyword", Attributes: map[string]any{"type": "keyword"}},
		{Field: "kubernetes.namespace", Attributes: map[string]any{"type": "keyword"}},
		{Field: "message", Attributes: map[string]any{"type": "text"}},
		{Field: "status", Attributes: map[string]any{"type": "long"}},
	}
}

func TestResolveESGroupByFields(t *testing.T) {
	t.Run("canonical name takes the spelling the index has, as keyword", func(t *testing.T) {
		got, err := resolveESGroupByFields(esTestFields(), []string{"pod", "namespace", "status"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"kubernetes.pod_name.keyword", "kubernetes.namespace", "status"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a field already mapped to its keyword is used verbatim", func(t *testing.T) {
		got, err := resolveESGroupByFields(esTestFields(), []string{"kubernetes.pod_name.keyword"})
		if err != nil || got[0] != "kubernetes.pod_name.keyword" {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("an absent field names what can be grouped", func(t *testing.T) {
		_, err := resolveESGroupByFields(esTestFields(), []string{"container"})
		if err == nil || !strings.Contains(err.Error(), `cannot group by "container"`) || !strings.Contains(err.Error(), "namespace, pod") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a text field without keyword is refused", func(t *testing.T) {
		_, err := resolveESGroupByFields(esTestFields(), []string{"message"})
		if err == nil || !strings.Contains(err.Error(), "text field") {
			t.Fatalf("err = %v", err)
		}
	})
}

func esTestQuery(groupBy []string, interval time.Duration) LogCountQuery {
	return LogCountQuery{
		Request: FetchLogRequest{
			Query:     `{"query":{"term":{"level":"error"}}}`,
			StartTime: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC).UnixMilli(),
			EndTime:   time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC).UnixMilli(),
		},
		GroupBy:  groupBy,
		Interval: interval,
		Limit:    100,
	}
}

func TestESAggregateQueryBody(t *testing.T) {
	q := esTestQuery([]string{"kubernetes.pod_name.keyword"}, 10*time.Minute)
	body, err := esAggregateQueryBody(q)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(body)
	for _, want := range []string{
		`"size":0`,
		`"track_total_hits":true`,
		`{"term":{"level":"error"}}`,
		fmt.Sprintf(`"gte":%d`, q.Request.StartTime),
		fmt.Sprintf(`"lt":%d`, q.Request.EndTime),
		`{"g0":{"terms":{"field":"kubernetes.pod_name.keyword","missing_bucket":true}}}`,
		`{"t":{"date_histogram":{"field":"@timestamp","fixed_interval":"600s"}}}`,
		fmt.Sprintf(`"size":%d`, esAggregatePageSize),
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("body lacks %s\n%s", want, got)
		}
	}
	if strings.Contains(string(got), `"lte"`) {
		t.Errorf("window end must be exclusive: %s", got)
	}

	plain, _ := esAggregateQueryBody(esTestQuery(nil, 0))
	if _, ok := plain["aggs"]; ok {
		t.Errorf("a count with no groups and no buckets needs no aggregation: %v", plain)
	}
}

// esPage renders one composite page; buckets are (pod or nil, bucket ms, count).
func esPage(total int64, afterKey string, buckets ...[3]any) []byte {
	var b strings.Builder
	for i, bk := range buckets {
		if i > 0 {
			b.WriteString(",")
		}
		pod := "null"
		if bk[0] != nil {
			pod = fmt.Sprintf("%q", bk[0])
		}
		fmt.Fprintf(&b, `{"key":{"g0":%s,"t":%d},"doc_count":%d}`, pod, bk[1], bk[2])
	}
	after := ""
	if afterKey != "" {
		after = fmt.Sprintf(`"after_key":{"g0":%q,"t":0},`, afterKey)
	}
	return []byte(fmt.Sprintf(`{"hits":{"total":{"value":%d,"relation":"eq"}},"aggregations":{"g":{%s"buckets":[%s]}}}`, total, after, b.String()))
}

// withESAggregatePageSize pages composite results n buckets at a time for one test.
func withESAggregatePageSize(t *testing.T, n int) {
	old := esAggregatePageSize
	esAggregatePageSize = n
	t.Cleanup(func() { esAggregatePageSize = old })
}

// esPlanAndRun plans q's count and runs the plan through search, as the source does.
func esPlanAndRun(t *testing.T, q LogCountQuery, search func(map[string]any) ([]byte, error)) (LogCountRows, error) {
	t.Helper()
	plan, err := esCountPlan(q)
	if err != nil {
		t.Fatal(err)
	}
	return esRunCount(q, plan, search)
}

func TestESCount_PlanIsWhatRunSends(t *testing.T) {
	q := esTestQuery([]string{"kubernetes.pod_name.keyword"}, 10*time.Minute)
	plan, err := esCountPlan(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Queries) != 1 {
		t.Fatalf("plan = %v, want one query", plan.Queries)
	}
	var sent []byte
	if _, err := esRunCount(q, plan, func(body map[string]any) ([]byte, error) {
		sent, _ = json.Marshal(body)
		return esPage(0, ""), nil
	}); err != nil {
		t.Fatal(err)
	}
	if !assertJSONEqual(plan.Queries[0], string(sent)) {
		t.Errorf("first page sent\n%s\nbut the plan (what logs_get_query previews) is\n%s", sent, plan.Queries[0])
	}
}

func assertJSONEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}

func TestESCount_PagesAndMeasuresUngrouped(t *testing.T) {
	withESAggregatePageSize(t, 2)
	q := esTestQuery([]string{"kubernetes.pod_name.keyword"}, 10*time.Minute)
	t0 := q.Request.StartTime
	t1 := t0 + (10 * time.Minute).Milliseconds()
	pages := [][]byte{
		esPage(130, "api", [3]any{nil, t0, 7}, [3]any{"api", t0, 50}),
		// api's second bucket arrives on the next page.
		esPage(130, "cron", [3]any{"api", t1, 40}, [3]any{"cron", t0, 0}),
		// A short last page still carries an after_key; following it would be a 4th,
		// empty search.
		esPage(130, "worker", [3]any{"worker", t0, 33}),
	}
	var afters []any
	call := 0
	rows, err := esPlanAndRun(t, q, func(body map[string]any) ([]byte, error) {
		composite, _ := esCompositeOf(body)
		afters = append(afters, composite["after"])
		call++
		return pages[call-1], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if call != 3 || afters[0] != nil || afters[1] == nil {
		t.Errorf("calls = %d, afters = %v; want 3 pages, the 2nd sent with an after key", call, afters)
	}
	if rows.Total != 130 || rows.Ungrouped != 7 || !rows.Complete {
		t.Errorf("total/ungrouped/complete = %d/%d/%v, want 130/7/true", rows.Total, rows.Ungrouped, rows.Complete)
	}
	if len(rows.Groups) != 3 {
		t.Fatalf("groups = %+v, want api, cron and worker", rows.Groups)
	}
	api := rows.Groups[0]
	if api.Metric["kubernetes.pod_name.keyword"] != "api" || len(api.Values) != 2 || api.Values[0]+api.Values[1] != 90 {
		t.Errorf("api = %+v, want two buckets summing to 90", api)
	}
	if api.Timestamps[1] != t1/1000 {
		t.Errorf("api bucket stamped %d, want %d (bucket start, seconds)", api.Timestamps[1], t1/1000)
	}
}

func TestESCount_StopsAtPageCap(t *testing.T) {
	withESAggregatePageSize(t, 1)
	q := esTestQuery([]string{"kubernetes.pod_name.keyword"}, 0)
	calls := 0
	rows, err := esPlanAndRun(t, q, func(map[string]any) ([]byte, error) {
		calls++
		return esPage(10, fmt.Sprintf("p%d", calls), [3]any{fmt.Sprintf("p%d", calls), int64(0), 1}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != esAggregateMaxPages || rows.Complete {
		t.Errorf("calls/complete = %d/%v, want %d/false", calls, rows.Complete, esAggregateMaxPages)
	}
}

func TestESCount_TotalOnly(t *testing.T) {
	q := esTestQuery(nil, 0)
	rows, err := esPlanAndRun(t, q, func(body map[string]any) ([]byte, error) {
		return []byte(`{"hits":{"total":{"value":42,"relation":"eq"}}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows.Total != 42 || len(rows.Groups) != 1 || rows.Groups[0].Values[0] != 42 {
		t.Errorf("rows = %+v, want one unlabelled group of 42", rows)
	}
}

func TestESCount_LowerBoundTotalIsAnError(t *testing.T) {
	_, err := esPlanAndRun(t, esTestQuery(nil, 0), func(map[string]any) ([]byte, error) {
		return []byte(`{"hits":{"total":{"value":10000,"relation":"gte"}}}`), nil
	})
	if err == nil || !strings.Contains(err.Error(), "lower bound") {
		t.Fatalf("err = %v", err)
	}
}
