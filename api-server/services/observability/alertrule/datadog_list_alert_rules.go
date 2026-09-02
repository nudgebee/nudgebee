package alertrule

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/security"
)

// datadogMonitorPageSize is the per-request page size for GET /api/v1/monitor.
// Datadog caps page_size at 1000; 500 keeps individual responses modest while
// still paging a large monitor estate in few round trips.
const datadogMonitorPageSize = 500

// datadogMaxMonitorPages bounds the walk so a paging bug or an enormous estate
// cannot spin forever. 200 pages x 500 = 100k monitors, far beyond any real org.
const datadogMaxMonitorPages = 200

// datadogMonitor is the subset of the Datadog monitor payload we map onto an
// event_rules row.
type datadogMonitor struct {
	Id      int64    `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Query   string   `json:"query"`
	Message string   `json:"message"`
	Tags    []string `json:"tags"`
	Options struct {
		// Datadog reports a monitor muted indefinitely as silenced with a null
		// end time; that is the closest analogue to "disabled".
		Silenced map[string]*int64 `json:"silenced"`
	} `json:"options"`
	Priority *int `json:"priority"`
}

// ListAlertRules enumerates Datadog monitors for the account's configured
// Datadog integration.
func (s *DatadogAlertRuleSource) ListAlertRules(ctx *security.RequestContext, accountId string) ([]ExternalAlertRule, error) {
	apiKey, appKey, site, err := getDatadogConfigs(ctx, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Datadog configs: %w", err)
	}
	headers := datadogHeaders(apiKey, appKey)

	var out []ExternalAlertRule
	for page := 0; page < datadogMaxMonitorPages; page++ {
		url := fmt.Sprintf("https://%s/api/v1/monitor?page=%d&page_size=%d", site, page, datadogMonitorPageSize)

		monitors, err := fetchDatadogMonitorPage(url, headers)
		if err != nil {
			return nil, err
		}
		for _, m := range monitors {
			out = append(out, datadogMonitorToExternalRule(m))
		}
		// A short page is the last page. Datadog returns an empty array past
		// the end rather than an explicit total.
		if len(monitors) < datadogMonitorPageSize {
			return out, nil
		}
	}
	// Every page came back full, so there is very likely more. Returning the
	// partial set with ErrIncompleteListing lets the caller still refresh what
	// it fetched while suppressing deletion reconciliation — silently returning
	// a truncated list would make every unfetched monitor look deleted.
	ctx.GetLogger().Warn("datadog: monitor listing hit the page cap; results are truncated",
		"account_id", accountId, "max_pages", datadogMaxMonitorPages, "fetched", len(out))
	return out, ErrIncompleteListing
}

func fetchDatadogMonitorPage(url string, headers map[string]string) ([]datadogMonitor, error) {
	resp, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to list Datadog monitors: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Datadog response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("datadog API error (status %d): %s", resp.StatusCode, string(body))
	}

	var monitors []datadogMonitor
	if err := json.Unmarshal(body, &monitors); err != nil {
		return nil, fmt.Errorf("failed to parse Datadog monitor list: %w", err)
	}
	return monitors, nil
}

func datadogMonitorToExternalRule(m datadogMonitor) ExternalAlertRule {
	labels := datadogTagsToLabels(m.Tags)

	return ExternalAlertRule{
		ExternalRuleId: strconv.FormatInt(m.Id, 10),
		Name:           m.Name,
		AlertType:      datadogMonitorAlertType(m.Type),
		Query:          m.Query,
		Severity:       datadogPriorityToSeverity(m.Priority, labels["severity"]),
		Annotations: map[string]string{
			"description": m.Message,
			"summary":     m.Name,
		},
		Labels:  labels,
		Enabled: !datadogMonitorMutedIndefinitely(m),
		ProviderConfig: map[string]any{
			"datadog_monitor_id":   m.Id,
			"datadog_monitor_type": m.Type,
		},
	}
}

// datadogTagsToLabels inverts labelsToDatadogTags. A Datadog tag without a
// colon is a bare tag with no value; it is kept as a key with an empty value so
// no information is silently dropped.
func datadogTagsToLabels(tags []string) map[string]string {
	labels := make(map[string]string, len(tags))
	for _, tag := range tags {
		key, value, found := strings.Cut(tag, ":")
		if key == "" {
			continue
		}
		if !found {
			labels[key] = ""
			continue
		}
		labels[key] = value
	}
	return labels
}

// datadogMonitorAlertType maps a Datadog monitor type onto event_rules.alert_type,
// which is FK-constrained to 'log' / 'metric'.
func datadogMonitorAlertType(monitorType string) string {
	if strings.Contains(monitorType, "log") {
		return "log"
	}
	return "metric"
}

// datadogPriorityToSeverity maps a monitor onto event_rule_severity, which
// holds only 'critical' and 'warning'. Datadog's own priority is P1..P5 with
// no direct equivalent, so P1/P2 are treated as critical. An explicit
// `severity:` tag wins over priority because it is the operator's own intent.
func datadogPriorityToSeverity(priority *int, severityTag string) string {
	switch strings.ToLower(strings.TrimSpace(severityTag)) {
	case "critical", "error", "fatal", "high", "p1", "p2":
		return "critical"
	case "warning", "warn", "medium", "low", "info", "p3", "p4", "p5":
		return "warning"
	}
	if priority != nil && *priority > 0 && *priority <= 2 {
		return "critical"
	}
	return "warning"
}

// datadogMonitorMutedIndefinitely reports whether the monitor is silenced with
// no end time, which is Datadog's closest equivalent to a disabled rule. A
// silence with an end timestamp is temporary and left as enabled.
func datadogMonitorMutedIndefinitely(m datadogMonitor) bool {
	for _, until := range m.Options.Silenced {
		if until == nil {
			return true
		}
	}
	return false
}
