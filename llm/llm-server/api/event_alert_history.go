package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/cloud"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
)

// alertHistoryWindows are the views of the alert's own metric saved before
// the investigation runs: days at a coarse step for its normal level (a climb
// under a day/night cycle shows here), and the last hours at a fine step for
// how it is moving now (a climb of the last hour is three or four points of
// the coarse view). Each is saved under its name, which the analysis tool
// reports as the series name.
var alertHistoryWindows = []struct {
	name       string
	span, step time.Duration
	cloudStep  time.Duration // CloudWatch periods: standard metrics resolve to 5 minutes
}{
	{tools.AlertHistoryNormalView, 72 * time.Hour, 15 * time.Minute, 15 * time.Minute},
	{tools.AlertHistoryNowView, 2 * time.Hour, time.Minute, 5 * time.Minute},
}

// alertHistoryTimeout bounds how long the history fetch can hold up the start
// of an investigation.
const alertHistoryTimeout = 30 * time.Second

// alertHistoryFileName is the workspace file holding the alert's own metric
// history for one event.
func alertHistoryFileName(eventId string) string {
	return fmt.Sprintf("alert_metric_history_%s.json", eventId)
}

// saveAlertMetricHistory saves the series an alert's rule compares against a
// level, for the days up to the incident, and returns the prompt note naming
// the file — or "" when the rule has no level (a state or presence test, or no
// rule), the series cannot be told apart from others, or the query fails.
//
// The investigation answers "will it become critical, and when" from this
// series. Left to the agent, it analysed the 10-minute evidence window or
// asked for days at a fine step and timed out; a hint in the tool's output to
// fetch a day was ignored. Rules that test a level are where a forecast means
// anything, and their own expression names the series, so this needs no
// knowledge of the alert type.
func saveAlertMetricHistory(ctx *security.RequestContext, repo *events.EventAnalysisRepository, accountId, sessionId string, event events.Event, labels map[string]any, window investigationWindow, hasWindow bool) string {
	expr, _, err := repo.GetEventRuleDefinition(ctx, accountId, event.AggregationKey)
	if err != nil || expr == "" {
		return ""
	}
	end := time.Now()
	if hasWindow && window.End != nil {
		end = *window.End
	}
	series, op, level, isProm := tools.AlertLevelQuery(expr)
	if !isProm {
		var ok bool
		if series, op, level, ok = tools.CloudWatchAlarmCommand(expr, end.Add(-time.Hour), end, time.Minute); !ok {
			return ""
		}
	}
	// The views are fetched together and bounded by alertHistoryTimeout, so a
	// slow metrics source delays the investigation by at most that long; a view
	// not back in time is left out.
	qctx, cancel := context.WithTimeout(ctx.GetContext(), alertHistoryTimeout)
	defer cancel()
	rctx := security.NewRequestContext(qctx, ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	type result struct {
		name  string
		view  any
		found bool
		err   error
	}
	results := make(chan result, len(alertHistoryWindows))
	for _, w := range alertHistoryWindows {
		go func() {
			start := end.Add(-w.span)
			r := result{name: w.name}
			if isProm {
				var s any
				s, r.found, r.err = tools.FetchAlertMetricHistory(*rctx, accountId, series, op, alertLabelStrings(labels), start, end, w.step)
				r.view = []any{s}
			} else {
				cmd, _, _, _ := tools.CloudWatchAlarmCommand(expr, start, end, w.cloudStep)
				r.view, r.found, r.err = fetchCloudAlarmHistory(rctx, accountId, cmd, w.name)
			}
			results <- r
		}()
	}
	views := map[string]any{}
collect:
	for range alertHistoryWindows {
		select {
		case r := <-results:
			if r.err != nil {
				ctx.GetLogger().Warn("analyzer: alert metric history not fetched", "error", r.err, "event_id", event.Id, "view", r.name)
				continue
			}
			if r.found {
				views[r.name] = r.view
			}
		case <-qctx.Done():
			ctx.GetLogger().Warn("analyzer: alert metric history timed out", "event_id", event.Id, "timeout", alertHistoryTimeout)
			break collect
		}
	}
	if len(views) == 0 {
		ctx.GetLogger().Info("analyzer: alert metric history skipped, no series or datapoints for this alert", "event_id", event.Id)
		return ""
	}
	raw, err := common.MarshalJson(views)
	if err != nil {
		return ""
	}
	body := string(raw)
	file := saveOverflowToWorkspace(ctx, accountId, sessionId, alertHistoryFileName(event.Id), body)
	if file == "" {
		return ""
	}
	ctx.GetLogger().Info("analyzer: alert metric history saved", "event_id", event.Id, "file", file)
	return fmt.Sprintf("## Alert Metric History\nThe metric this alert compares (`%s`; it fires when %s %s), up to %s, is saved as `%s` in %d views, one series each: %s. "+
		"Use it with analyze_metric_series to say whether the metric is getting worse and when it reaches the next level: the normal-level view for its usual level and slow climbs, the now view for how it is moving at the alert.",
		core.TruncateHead(series, 300), op, strconv.FormatFloat(level, 'g', -1, 64), common.FormatPresentationTime(&end), file, len(views), alertHistoryViewNames(views))
}

// executeAlarmCli runs a read-only cloud CLI command for the account; tests
// replace it.
var executeAlarmCli = cloud.ExecuteV2

// fetchCloudAlarmHistory runs a CloudWatch alarm's metric query and returns
// its output, labelled with the view's name, when it has datapoints.
func fetchCloudAlarmHistory(ctx *security.RequestContext, accountId, cmd, name string) (any, bool, error) {
	sc := ctx.GetSecurityContext()
	if sc == nil {
		return nil, false, fmt.Errorf("fetchCloudAlarmHistory: no security context")
	}
	res, err := executeAlarmCli(cloud.CloudExecuteCliCommandRequest{AccountID: accountId, TenantID: sc.GetTenantId(), UserID: sc.GetUserId(), Command: cmd})
	if err != nil {
		return nil, false, fmt.Errorf("fetchCloudAlarmHistory: %w", err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		return nil, false, fmt.Errorf("fetchCloudAlarmHistory: exit %d: %s", res.ExitCode, core.TruncateHead(res.Stderr, 300))
	}
	var out map[string]any
	if json.Unmarshal([]byte(res.Stdout), &out) != nil {
		return nil, false, nil
	}
	if dps, _ := out["Datapoints"].([]any); len(dps) == 0 {
		return nil, false, nil
	}
	out["Label"] = name
	return out, true, nil
}

// alertHistoryViewNames lists the saved views in the windows' order.
func alertHistoryViewNames(views map[string]any) string {
	names := make([]string, 0, len(views))
	for _, w := range alertHistoryWindows {
		if _, ok := views[w.name]; ok {
			names = append(names, "\""+w.name+"\"")
		}
	}
	return strings.Join(names, " and ")
}

// alertLabelStrings keeps the string-valued labels of the alert.
func alertLabelStrings(labels map[string]any) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if s, ok := v.(string); ok && s != "" {
			out[k] = s
		}
	}
	return out
}
