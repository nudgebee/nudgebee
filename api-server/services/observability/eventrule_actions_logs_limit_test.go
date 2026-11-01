package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/nudgebee/logparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var limitWindowStart = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

var checkoutPods = []string{"checkout-7d9f6c-x2k4p", "checkout-7d9f6c-m8q1z", "checkout-7d9f6c-r5t7w"}

// lokiLine is a line as LokiSource returns it: the level is its reading of the line.
func lokiLine(at time.Time, pod, line string) OutputLog {
	return OutputLog{
		Timestamp: at.Format(time.RFC3339Nano),
		Message:   line,
		Labels:    map[string]any{"namespace": "shop", "pod": pod},
		Severity:  (&LokiSource{}).extractSeverity(line),
	}
}

func requestLine(at time.Time, i int) string {
	if i%50 == 0 {
		// Routine, and mentions errors: it must not pass for one.
		return fmt.Sprintf(`level=info ts=%s caller=cache.go:88 msg="cache refresh finished" entries=5120 errors=0`, at.Format(time.RFC3339Nano))
	}
	return fmt.Sprintf(`level=info ts=%s caller=server.go:212 msg="request completed" method=GET path=/api/v1/orders/%d status=200 duration_ms=%d`,
		at.Format(time.RFC3339Nano), 1000+i, 5+i%40)
}

// busyCheckoutWindow is the hour before an alert on a busy service, oldest first: 20
// failed database queries in its first seconds, then 3000 routine request lines.
func busyCheckoutWindow() []OutputLog {
	var window []OutputLog
	for i := 0; i < 20; i++ {
		at := limitWindowStart.Add(time.Duration(i) * time.Second)
		window = append(window, lokiLine(at, checkoutPods[i%3], fmt.Sprintf(
			`level=error ts=%s caller=repository.go:57 msg="query failed" err="dial tcp 192.0.2.15:5432: connect: connection refused" order_id=%d`,
			at.Format(time.RFC3339Nano), 5000+i)))
	}
	for i := 0; i < 3000; i++ {
		at := limitWindowStart.Add(30*time.Second + time.Duration(i)*time.Second)
		window = append(window, lokiLine(at, checkoutPods[i%3], requestLine(at, i)))
	}
	return window
}

// lokiBackward answers the way Loki's query_range does with direction=backward: the
// newest limit lines of an oldest-first window that match pattern ("" matches all).
func lokiBackward(window []OutputLog, pattern string, limit int) []OutputLog {
	var re *regexp.Regexp
	if pattern != "" {
		re = regexp.MustCompile(pattern)
	}
	var out []OutputLog
	for i := len(window) - 1; i >= 0 && len(out) < limit; i-- {
		if re == nil || re.MatchString(window[i].Message) {
			out = append(out, window[i])
		}
	}
	return out
}

func countLevel(logs []OutputLog, severity string) int {
	n := 0
	for _, l := range logs {
		if l.Severity == severity {
			n++
		}
	}
	return n
}

func TestKeepFlaggedLines_KeepsTheWindowsErrorsWhenItOverflows(t *testing.T) {
	window := busyCheckoutWindow()
	recent := lokiBackward(window, "", 1000)
	require.Zero(t, countLevel(recent, "error"), "the defect: the newest 1000 lines hold none of the errors")

	var queries int
	kept, note := keepFlaggedLines(recent, 1000, time.Minute, func(pattern string, limit int, _ time.Duration) ([]OutputLog, error) {
		queries++
		return lokiBackward(window, pattern, limit), nil
	})

	assert.Len(t, kept, 1000, "the card's line budget is unchanged")
	assert.Equal(t, 20, countLevel(kept, "error"), "every error in the window is on the card")
	assert.Equal(t, 1, queries, "one narrowed query when the window's errors and warnings fit the reserve")
	assert.Equal(t, window[19].Message, kept[980].Message, "newest first: the errors are the oldest lines kept")
	for _, l := range kept[:980] {
		assert.Equal(t, "info", l.Severity)
	}
	assert.Equal(t, "The queried window has more lines than the 1000 a log card holds. This card keeps every line in it that looks like an error or warning (20), and the 980 most recent other lines.", note)
}

func TestKeepFlaggedLines_WindowThatFitsIsUntouched(t *testing.T) {
	recent := lokiBackward(busyCheckoutWindow(), "", 999)
	kept, note := keepFlaggedLines(recent, 1000, time.Minute, func(string, int, time.Duration) ([]OutputLog, error) {
		t.Fatal("a window that fit must not be queried again")
		return nil, nil
	})
	assert.Equal(t, recent, kept)
	assert.Empty(t, note)
}

// warningHeavyWindow is 5 errors, then 700 warnings — more than a card's reserve — then
// 2000 routine lines, oldest first.
func warningHeavyWindow() []OutputLog {
	var window []OutputLog
	at := limitWindowStart
	for i := 0; i < 5; i++ {
		window = append(window, lokiLine(at, checkoutPods[0], `ERROR [payment-client] charge failed: upstream returned 503 Service Unavailable`))
		at = at.Add(time.Second)
	}
	for i := 0; i < 700; i++ {
		window = append(window, lokiLine(at, checkoutPods[i%3], fmt.Sprintf(`WARN [pool] connection pool at %d%% of capacity`, 80+i%20)))
		at = at.Add(time.Second)
	}
	for i := 0; i < 2000; i++ {
		window = append(window, lokiLine(at, checkoutPods[i%3], requestLine(at, i)))
		at = at.Add(time.Second)
	}
	return window
}

func TestKeepFlaggedLines_WarningsDoNotCrowdOutErrors(t *testing.T) {
	window := warningHeavyWindow()
	recent := lokiBackward(window, "", 1000)

	var queries int
	kept, note := keepFlaggedLines(recent, 1000, time.Minute, func(pattern string, limit int, _ time.Duration) ([]OutputLog, error) {
		queries++
		return lokiBackward(window, pattern, limit), nil
	})

	assert.Len(t, kept, 1000)
	assert.Equal(t, 5, countLevel(kept, "error"))
	// The 40 routine lines that mention errors take 40 of the narrowed query's 500 slots
	// before their level drops them, so 460 warnings come back rather than 495.
	assert.Equal(t, 460, countLevel(kept, "warning"), "the most recent warnings fill the rest of the reserve")
	assert.Equal(t, 2, queries, "an errors-only query when errors and warnings overflow the reserve")
	assert.Contains(t, note, "every line in it that looks like an error, then the most recent that look like warnings (465), and the 535 most recent other lines")
}

func TestKeepFlaggedLines_SlowFirstQuerySkipsTheErrorsOnlyQuery(t *testing.T) {
	window := warningHeavyWindow()
	var timeouts []time.Duration
	kept, note := keepFlaggedLines(lokiBackward(window, "", 1000), 1000, 100*time.Millisecond, func(pattern string, limit int, timeout time.Duration) ([]OutputLog, error) {
		timeouts = append(timeouts, timeout)
		time.Sleep(60 * time.Millisecond) // more than half the budget
		return lokiBackward(window, pattern, limit), nil
	})
	assert.Equal(t, []time.Duration{100 * time.Millisecond}, timeouts, "the first query gets the whole budget and no second query runs")
	assert.Len(t, kept, 1000)
	assert.Contains(t, note, "the most recent of its lines that look like errors or warnings")
}

func TestKeepFlaggedLines_SourceThatCannotFilterKeepsTheCardAndSaysSo(t *testing.T) {
	recent := lokiBackward(busyCheckoutWindow(), "", 1000)
	kept, note := keepFlaggedLines(recent, 1000, time.Minute, func(string, int, time.Duration) ([]OutputLog, error) {
		return nil, errors.New("log source cannot filter lines")
	})
	assert.Equal(t, recent, kept)
	assert.Equal(t, "The queried window has more lines than the 1000 a log card holds. This card shows only part of it, so errors logged elsewhere in the window may not be here.", note)
}

func TestKeepFlaggedLines_SaysWhenTheWindowHasNoErrors(t *testing.T) {
	window := busyCheckoutWindow()[20:]
	kept, note := keepFlaggedLines(lokiBackward(window, "", 1000), 1000, time.Minute, func(pattern string, limit int, _ time.Duration) ([]OutputLog, error) {
		return lokiBackward(window, pattern, limit), nil
	})
	assert.Len(t, kept, 1000)
	assert.Equal(t, "The queried window has more lines than the 1000 a log card holds. None of its lines look like errors or warnings; these are the 1000 most recent.", note)
}

// The line patterns are looser than logparser.GuessLevel on purpose; they must never be
// narrower, or a line the card would show as an error is never asked for.
func TestErrorLinePatternsCoverWhatGuessLevelReads(t *testing.T) {
	lines := []string{
		`level=error ts=2026-09-28T10:00:00Z msg="query failed"`,
		`{"level":"error","msg":"charge failed","status":503}`,
		`2026-09-28 10:00:00.123 ERROR 1 --- [nio-8080-exec-3] c.e.orders.OrderController : request failed`,
		`ERROR:root:payment declined`,
		`[2026-09-28T10:00:00Z] FATAL out of memory`,
		`time="2026-09-28T10:00:00Z" level=fatal msg="cannot bind :8080"`,
		`E0928 10:00:00.000000       1 controller.go:114] sync failed`,
		`F0928 10:00:00.000000       1 main.go:40] exiting`,
		`level=warn msg="retrying request" attempt=2`,
		`WARNING: deprecated flag --insecure`,
		`W0928 10:00:00.000000       1 reflector.go:424] watch ended`,
		`2026-09-28T10:00:00Z [crit] upstream timed out`,
	}
	for _, line := range lines {
		switch logparser.GuessLevel(line) {
		case logparser.LevelError, logparser.LevelCritical:
			assert.True(t, errorLines.MatchString(line), line)
			assert.True(t, errorOrWarningLines.MatchString(line), line)
		case logparser.LevelWarning:
			assert.True(t, errorOrWarningLines.MatchString(line), line)
		default:
			t.Errorf("fixture is not error or warning level: %s", line)
		}
	}
	for _, line := range []string{
		`level=info msg="stderr closed"`,
		`I0928 10:00:00.000000       1 leaderelection.go:258] successfully acquired lease`,
		`GET /api/v1/orders?referrer=home 200`,
	} {
		assert.False(t, errorOrWarningLines.MatchString(line), line)
	}
}

func TestLokiWithLineFilter(t *testing.T) {
	q, ok := (&LokiSource{}).withLineFilter(` {namespace="shop", pod=~"checkout-.*"} `, errorLines.String())
	require.True(t, ok)
	assert.Equal(t, "{namespace=\"shop\", pod=~\"checkout-.*\"} |~ `"+errorLines.String()+"`", q)

	for _, notALogQuery := range []string{
		`sum(count_over_time({namespace="shop"}[5m]))`,
		`query=%7Bnamespace%3D%22shop%22%7D&limit=100`,
		``,
	} {
		_, ok := (&LokiSource{}).withLineFilter(notALogQuery, errorLines.String())
		assert.False(t, ok, notALogQuery)
	}

	var _ lineFilterSource = &LokiSaasSource{}
}

// fakeLokiServer serves query_range over window the way Loki does — the newest limit
// lines matching the query's line filter, grouped into one stream per pod — and counts
// the requests it gets.
func fakeLokiServer(t *testing.T, window []OutputLog, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs on the server's goroutine, not the test's.
		requests.Add(1)
		params, err := url.ParseQuery(r.URL.RawQuery)
		var limit int
		if !assert.NoError(t, err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := fmt.Sscan(params.Get("limit"), &limit); !assert.NoError(t, err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		assert.Equal(t, "backward", params.Get("direction"))
		logql := params.Get("query")
		assert.True(t, strings.HasPrefix(logql, `{namespace="shop", pod=~"checkout-.*"}`), logql)
		pattern := ""
		if _, filter, ok := strings.Cut(logql, " |~ `"); ok {
			pattern = strings.TrimSuffix(filter, "`")
		}

		values := map[string][][]string{}
		for _, l := range lokiBackward(window, pattern, limit) {
			at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
			pod := l.Labels["pod"].(string)
			values[pod] = append(values[pod], []string{fmt.Sprint(at.UnixNano()), l.Message})
		}
		type stream struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		}
		result := []stream{}
		for _, pod := range checkoutPods {
			if len(values[pod]) > 0 {
				result = append(result, stream{Stream: map[string]string{"namespace": "shop", "pod": pod}, Values: values[pod]})
			}
		}
		body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "streams", "result": result}})
		_, _ = w.Write(body)
	}))
}

// patchCardLogSource routes fetchCardLogs to a Loki at server: FetchLogs runs the card's
// query itself, and the narrowed queries resolve the same source.
func patchCardLogSource(t *testing.T, server *httptest.Server) {
	t.Helper()
	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))
	patches := gomonkey.NewPatches()
	// Not GetLogsMetricsTracesProvider: it is a one-line wrapper, inlined into its
	// callers, so a patch on it never takes effect.
	patches.ApplyFunc(getLogsMetricsTracesProviderWithIntegration, func(_ *security.RequestContext, _, _, _, _ string) (string, string, *core.IntegrationDto, error) {
		return "loki", "user", nil, nil
	})
	patches.ApplyFunc(resolveLogLabelMapping, func(_ *security.RequestContext, _ string, _ LogSource, _ *labelMappingOverride) ResolvedLabelMapping {
		return ResolvedLabelMapping{}
	})
	patches.ApplyFunc(FetchLogs, func(ctx *security.RequestContext, req FetchLogRequest) (FetchLogsResult, error) {
		logs, err := (&LokiSaasSource{}).QueryLogs(ctx, req)
		return FetchLogsResult{Logs: logs, Query: req.Query, Provider: "loki"}, err
	})
	t.Cleanup(patches.Reset)
}

func TestFetchCardLogs_LokiCardCarriesTheWindowsErrors(t *testing.T) {
	var requests atomic.Int32
	server := fakeLokiServer(t, busyCheckoutWindow(), &requests)
	defer server.Close()
	patchCardLogSource(t, server)

	result, note, err := fetchCardLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="shop", pod=~"checkout-.*"}`,
		StartTime: limitWindowStart.UnixMilli(),
		EndTime:   limitWindowStart.Add(time.Hour).UnixMilli(),
		Limit:     1000,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, requests.Load(), "the card's query, then one narrowed to errors and warnings")
	require.Len(t, result.Logs, 1000)
	assert.Equal(t, 20, countLevel(result.Logs, "error"))

	resp := buildLogsActionResponse(result.Logs, "", putLogTruncation(addExecutedQueryInfo(map[string]any{}, result), note), actionLogMaxInsightErrors, nil, nil, nil)
	require.NotNil(t, resp)
	ai := resp.GetAdditionalInfo()
	assert.Equal(t, true, ai["log_truncated"])
	assert.Equal(t, note, ai["log_truncation_note"])
	assert.Equal(t, `{namespace="shop", pod=~"checkout-.*"}`, ai["executed_query"], "the card names the query it was asked, not the narrowed one")

	groups := ai["log_groups"].([]playbooks.LogLineGroup)
	assert.Equal(t, "error", groups[0].Level)
	assert.Equal(t, 20, groups[0].Count)

	insights := resp.GetInsights()
	require.NotEmpty(t, insights)
	assert.Equal(t, "Critical", insights[0].Severity)
	assert.Contains(t, insights[0].Message, "connection refused")
	assert.Equal(t, playbooks.PlaybookActionResponseInsight{Message: note, Severity: "Info"}, insights[len(insights)-1])
}

func TestFetchCardLogs_SlowLokiLeavesTheCardAsItWas(t *testing.T) {
	window := busyCheckoutWindow()
	var requests atomic.Int32
	fast := fakeLokiServer(t, window, &requests)
	defer fast.Close()
	// Answers the card's own query at once, and sits on a narrowed one until the
	// client gives up.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "|~") {
			requests.Add(1)
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		fast.Config.Handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	patchCardLogSource(t, server)
	budget := flaggedLinesBudget
	flaggedLinesBudget = 300 * time.Millisecond
	t.Cleanup(func() { flaggedLinesBudget = budget })

	started := time.Now()
	result, note, err := fetchCardLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="shop", pod=~"checkout-.*"}`,
		StartTime: limitWindowStart.UnixMilli(),
		EndTime:   limitWindowStart.Add(time.Hour).UnixMilli(),
		Limit:     1000,
	})
	elapsed := time.Since(started)

	require.NoError(t, err, "a narrowed query timing out is not the card's failure")
	assert.Less(t, elapsed, 3*time.Second, "the narrowed query gave up at its budget")
	assert.EqualValues(t, 2, requests.Load(), "the card's query and one narrowed query; no retry, no errors-only follow-up")
	assert.Equal(t, lokiBackward(window, "", 1000), newestFirst(result.Logs), "the card keeps the lines it had")
	assert.Contains(t, note, "This card shows only part of it")
}

// The agent path bounds a narrowed query on the relay itself, so the relay neither waits
// its default 180s nor retries.
func TestLokiQueryLogs_TimeoutBoundsTheRelayRequest(t *testing.T) {
	var sent []relay.RelayExecuteRequest
	patches := gomonkey.ApplyFunc(relay.Execute, func(req relay.RelayExecuteRequest) (map[string]any, error) {
		sent = append(sent, req)
		return map[string]any{"data": map[string]any{"data": map[string]any{"data": map[string]any{"resultType": "streams", "result": []any{}}}}}, nil
	})
	defer patches.Reset()

	req := FetchLogRequest{AccountId: "acc-1", Query: `{namespace="shop"}`, Limit: 500}
	_, err := (&LokiSource{}).QueryLogs(newLokiSaasCtx(), req)
	require.NoError(t, err)
	req.Timeout = 30 * time.Second
	_, err = (&LokiSource{}).QueryLogs(newLokiSaasCtx(), req)
	require.NoError(t, err)

	require.Len(t, sent, 2)
	assert.Zero(t, sent[0].TimeoutSeconds, "the card's own query keeps the relay defaults")
	assert.Zero(t, sent[0].MaxAttempts)
	assert.Equal(t, 30, sent[1].TimeoutSeconds)
	assert.Equal(t, 1, sent[1].MaxAttempts)
}

func TestFetchCardLogs_WindowThatFitsIsQueriedOnce(t *testing.T) {
	var requests atomic.Int32
	server := fakeLokiServer(t, busyCheckoutWindow()[:900], &requests)
	defer server.Close()
	patchCardLogSource(t, server)

	result, note, err := fetchCardLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="shop", pod=~"checkout-.*"}`,
		StartTime: limitWindowStart.UnixMilli(),
		EndTime:   limitWindowStart.Add(time.Hour).UnixMilli(),
		Limit:     1000,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, requests.Load())
	assert.Len(t, result.Logs, 900)
	assert.Empty(t, note)

	resp := buildLogsActionResponse(result.Logs, "", putLogTruncation(map[string]any{}, note), actionLogMaxInsightErrors, nil, nil, nil)
	assert.NotContains(t, resp.GetAdditionalInfo(), "log_truncated")
	for _, in := range resp.GetInsights() {
		assert.NotEqual(t, "Info", in.Severity)
	}
}
