package forecast

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event"
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/forecast/keys"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability"
)

// Source is events.source for every forecast event.
const Source = "forecast"

// Close reasons, stamped into the closed event's labels. The re-fire cooldown skips
// closes that say nothing about the workload's health: the user removing the SLO, a
// forecast moving between urgency levels, or a forecast that could not be evaluated.
const (
	CloseClean       = "clean"
	CloseBreached    = "breached"
	CloseLevelChange = "level_change"
	CloseUnwatched   = "unwatched"
	CloseUnverified  = "unverified"
)

// sloCadence is how often the SLO run evaluates a CPU SLO. A clean evaluation closes
// an event only once its last projecting run is more than one cadence old, which
// means two consecutive clean runs.
const sloCadence = time.Hour

// unverifiedAfter is how long an open event may go without a run that confirms it
// while every run fails. A failed run says nothing about the workload, so one or two
// leave the event alone; a day of them means the alert no longer describes anything.
const unverifiedAfter = 24 * time.Hour

// AggregationKey is the event type of a projected breach of condition.
func AggregationKey(c Condition) string {
	switch c {
	case ConditionSaturation:
		return keys.CPUSaturationPredicted
	case ConditionMemorySaturation:
		return keys.MemorySaturationPredicted
	}
	return keys.CPUThrottlingPredicted
}

// BreachedAggregationKey is the event type of an actual breach of condition's SLO.
func BreachedAggregationKey(c Condition) string {
	switch c {
	case ConditionSaturation:
		return keys.CPUSaturationSLOBreached
	case ConditionMemorySaturation:
		return keys.MemorySaturationSLOBreached
	}
	return keys.CPUThrottlingSLOBreached
}

// Target identifies the SLO a forecast belongs to.
type Target struct {
	TenantId    string
	AccountId   string
	AccountName string
	SLOConfigId string
}

// fingerprint is one per workload, condition and level, and for a container-scoped
// preset per container too: the condition decides the shape, so a memory fingerprint
// always carries the container. "|" cannot appear in a UUID, a DNS-1123 name or the
// enums, so two workloads can never share one (see anomalyFingerprint for the collision
// "-" allowed). The "cpuforecast" prefix predates memory and is kept so CPU
// fingerprints, and the alerts open under them, are unchanged.
func fingerprint(t Target, res Result, level Level) string {
	parts := []string{"cpuforecast", t.AccountId, res.Namespace, res.Kind, res.Workload, string(res.Condition)}
	if Presets[res.Condition].ContainerScoped {
		parts = append(parts, res.Container)
	}
	return strings.Join(append(parts, string(level)), "|")
}

// Seams for tests.
var (
	openFiring       = openFiringEvent
	inCooldown       = recentlyClosed
	closeFiring      = closeFiringEvents
	closeUnverified  = closeUnverifiedEvents
	staleContainers  = staleContainerFingerprints
	upsertEvent      = func(e event.Event) error { _, err := event.InsertEvent(e, "", event.ScheduledDetection()); return err }
	publishResolve   = publishResolved
	limitedContainer = onlyLimitedContainer
	nowFn            = func() time.Time { return time.Now().UTC() }
)

// Sync applies one evaluation to the workload's forecast events. A failed evaluation
// says nothing about the workload, so it changes nothing, except to close an event no
// run has confirmed for unverifiedAfter.
func Sync(t Target, res Result, series Series) error {
	// Repair first, whatever this run concludes: an alert of a container the SLO no
	// longer watches (the container was changed and its close failed) has no other
	// evaluator to close it.
	if Presets[res.Condition].ContainerScoped && res.Container != "" {
		if err := closeStaleContainers(t, res); err != nil {
			return err
		}
	}
	switch res.Status {
	case StatusError:
		var errs []error
		for _, level := range []Level{LevelMedium, LevelHigh, LevelBreached} {
			errs = append(errs, closeUnverified(t, fingerprint(t, res, level)))
		}
		return errors.Join(errs...)
	case StatusBreached:
		// The prediction came true: the warnings give way to the breach alert, which
		// carries the same trend evidence.
		if err := closeBoth(t, res, CloseBreached, false); err != nil {
			return err
		}
		return raiseLevel(t, res, series, LevelBreached)
	case StatusProjected:
		// Below the threshold again: the breach alert closes once that holds for two
		// runs, the same damping as a warning.
		if err := closeFiring(t, fingerprint(t, res, LevelBreached), CloseClean, true); err != nil {
			return err
		}
		return raise(t, res, series)
	case StatusInsufficientHistory:
		// Too little recent data (a limit change, a workload idle or scaled to zero)
		// says nothing about health, so the close starts no re-fire cooldown.
		return closeAll(t, res, CloseUnverified, true)
	default:
		return closeAll(t, res, CloseClean, true)
	}
}

// CloseAll closes every open forecast event of an SLO whose watch has ended: the SLO
// was disabled or deleted, or it now watches another container. container is the one
// the SLO watched; it is ignored for a preset that is not container-scoped.
func CloseAll(t Target, namespace, kind, workload, container string, c Condition) error {
	return closeAll(t, Result{Namespace: namespace, Kind: normalizeKind(kind), Workload: workload, Container: container, Condition: c}, CloseUnwatched, false)
}

// closeStaleContainers closes the open alerts of res's workload and condition that
// belong to a container other than res's.
func closeStaleContainers(t Target, res Result) error {
	fps, err := staleContainers(t, res)
	if err != nil {
		return err
	}
	var errs []error
	for _, fp := range fps {
		if err := closeFiring(t, fp, CloseUnwatched, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// staleContainerFingerprints finds them by the alert's own columns, so the
// (cloud_account_id, tenant, finding_type, aggregation_key) index serves it rather than
// a prefix match on fingerprint.
func staleContainerFingerprints(t Target, res Result) ([]string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var fps []string
	err = dbms.Db.Select(&fps, `SELECT DISTINCT fingerprint FROM events
		WHERE cloud_account_id = $1 AND tenant = $2 AND finding_type IN ($3, $4) AND aggregation_key IN ($3, $4)
		  AND source = $5 AND status = 'FIRING' AND subject_namespace = $6 AND subject_owner = $7
		  AND subject_owner_kind = $10
		  AND labels->>'condition' = $8 AND labels->>'container' IS DISTINCT FROM $9`,
		t.AccountId, t.TenantId, AggregationKey(res.Condition), BreachedAggregationKey(res.Condition),
		Source, res.Namespace, res.Workload, string(res.Condition), res.Container, res.Kind)
	return fps, err
}

// closeAll closes the warnings and the breach alert.
func closeAll(t Target, res Result, reason string, stable bool) error {
	return errors.Join(
		closeBoth(t, res, reason, stable),
		closeFiring(t, fingerprint(t, res, LevelBreached), reason, stable),
	)
}

func closeBoth(t Target, res Result, reason string, stable bool) error {
	var errs []error
	for _, level := range []Level{LevelMedium, LevelHigh} {
		if err := closeFiring(t, fingerprint(t, res, level), reason, stable); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func raise(t Target, res Result, series Series) error {
	other := LevelHigh
	if res.Level == LevelHigh {
		other = LevelMedium
	}
	// Close the other level first, so triage never sees two open forecasts on one
	// workload and an escalation is a fresh insert that notifies.
	if err := closeFiring(t, fingerprint(t, res, other), CloseLevelChange, false); err != nil {
		return err
	}
	return raiseLevel(t, res, series, res.Level)
}

// raiseLevel updates the open event of level, or inserts a new firing of it when none
// is open and the level is not cooling down.
func raiseLevel(t Target, res Result, series Series, level Level) error {
	fp := fingerprint(t, res, level)
	findingId, startsAt, err := openFiring(t, fp)
	if err != nil {
		return err
	}
	now := nowFn()
	if findingId == "" {
		cooling, err := inCooldown(t, fp, now.Add(-time.Duration(config.Config.NBForecastRefireCooldownHours)*time.Hour))
		if err != nil {
			return err
		}
		if cooling {
			return nil
		}
		// One finding_id per firing, as webhook alerts do: a re-crossing after a close
		// is a new row, so it is triaged, notified and investigated again.
		findingId = fmt.Sprintf("%s-%d", fp, now.Unix())
		startsAt = now
	}
	return upsertEvent(buildEvent(t, res, series, level, fp, findingId, startsAt))
}

func buildEvent(t Target, res Result, series Series, level Level, fp, findingId string, startsAt time.Time) event.Event {
	key := AggregationKey(res.Condition)
	priority := eventtypes.EventPriority(res.Level)
	if level == LevelBreached {
		key = BreachedAggregationKey(res.Condition)
		priority = eventtypes.EventPriority(LevelHigh)
	}
	title := Title(res)
	labels := map[string]string{
		"workload_kind": res.Kind,
		"condition":     string(res.Condition),
	}
	if t.SLOConfigId != "" {
		labels["slo_config_id"] = t.SLOConfigId
	}
	// The resolution path sizes one container. A container-scoped forecast names its
	// own, which is part of its identity and so never changes; a workload-wide one names
	// a container only when exactly one has a CPU limit, otherwise the event offers
	// replicas and restart, not a resize.
	if Presets[res.Condition].ContainerScoped {
		labels["container"] = res.Container
	} else if c := limitedContainer(t, res); c != "" {
		labels["container"] = c
	}
	return event.Event{
		AccountId:        t.AccountId,
		Tenant:           t.TenantId,
		Source:           Source,
		Title:            title,
		Description:      title,
		Failure:          "false",
		FindingType:      key,
		Category:         key,
		AggregationKey:   key,
		Priority:         priority,
		SubjectName:      res.Workload,
		SubjectNamespace: res.Namespace,
		SubjectType:      res.Kind,
		SubjectOwner:     res.Workload,
		SubjectOwnerKind: res.Kind,
		Cluster:          t.AccountName,
		Status:           "FIRING",
		StartsAt:         &startsAt,
		FindingId:        findingId,
		Fingerprint:      fp,
		Labels:           labels,
		Evidences:        Evidences(res, series),
	}
}

// Title states the projection in one line, e.g.
// "order-service: CPU throttling projected to reach 25% in ~38h (now 15%)", or the
// breach: "order-service: CPU throttling breached its 75% SLO (now 78%, rising 24 pp/day)".
func Title(res Result) string {
	what := conditionNoun(res.Condition)
	subject := res.Workload
	if res.Container != "" {
		subject += "/" + res.Container
	}
	ofLimit := Presets[res.Condition].OfLimit
	if res.Status == StatusBreached {
		slo := trimFloat(res.ThresholdPct) + "%"
		if ofLimit {
			slo += " of limit"
		}
		trend := ""
		if res.SlopePctPerDay > 0 {
			trend = fmt.Sprintf(", rising %s pp/day", trimFloat(res.SlopePctPerDay))
		}
		return fmt.Sprintf("%s: %s breached its %s SLO (now %s%%%s)", subject, what, slo, trimFloat(res.CurrentPct), trend)
	}
	eta := "soon"
	if res.ETAHours != nil {
		eta = "in ~" + humanHours(*res.ETAHours)
	}
	suffix := ""
	if ofLimit {
		suffix = " of its limit"
	}
	return fmt.Sprintf("%s: %s projected to reach %s%%%s %s (now %s%%)",
		subject, what, trimFloat(res.ThresholdPct), suffix, eta, trimFloat(res.CurrentPct))
}

func conditionNoun(c Condition) string {
	if p, ok := Presets[c]; ok {
		return p.Noun
	}
	return string(c)
}

// resourceOf names the limited resource of c, e.g. "CPU" or "memory".
func resourceOf(c Condition) string {
	if p, ok := Presets[c]; ok {
		return p.Resource
	}
	return "resource"
}

// quantity formats a limit or usage figure: cores as millicores ("350m"), the way
// Kubernetes writes them, and any other unit as "<value> <unit>".
func quantity(v float64, unit string) string {
	if unit != "" && unit != UnitCores {
		return trimFloat(v) + " " + unit
	}
	return trimFloat(v*1000) + "m"
}

// slopePhrase words the fitted slope; a breach can be fitted on a window whose trend
// is flat or falling, and "rising -3 pp/day" would misreport it.
func slopePhrase(slope float64) string {
	// Worded from the rounded figure, so a slope of -0.02 reads "flat", not "falling 0".
	switch shown := trimFloat(math.Abs(slope)); {
	case shown == "0":
		return "flat"
	case slope > 0:
		return fmt.Sprintf("rising %s pp/day", shown)
	default:
		return fmt.Sprintf("falling %s pp/day", shown)
	}
}

func humanHours(h float64) string {
	if h < 48 {
		return fmt.Sprintf("%.0fh", max(h, 1))
	}
	return fmt.Sprintf("%.1f days", h/24)
}

func trimFloat(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", v), "0"), ".")
}

// Evidences are rebuilt on every run from the series the forecast was fitted on, so
// the chart under the event is exactly what fired.
func Evidences(res Result, series Series) []any {
	out := []any{summaryEvidence(res), tableEvidence(res)}
	p := Presets[res.Condition]
	seen := map[observability.UtilisationKey]bool{}
	for _, key := range []observability.UtilisationKey{p.Value, p.Usage, p.Limit} {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if r, ok := series[string(key)]; ok && len(r.Payload) > 0 {
			out = append(out, observability.UtilisationEvidence(r, evidenceTitle(key)))
		}
	}
	return out
}

var evidenceTitles = map[observability.UtilisationKey]string{
	observability.KeyCPUThrottlingRatio:            "CPU throttling (share of CFS periods)",
	observability.KeyCPUUsageLimited:               "CPU usage (cores, containers with a CPU limit)",
	observability.KeyCPULimit:                      "CPU limit (cores, all pods)",
	observability.KeyContainerMemoryRSSLimitRatio:  "Memory (RSS) as a share of the limit (highest pod)",
	observability.KeyContainerMemoryRSSMiB:         "Memory RSS (MiB, busiest pod)",
	observability.KeyContainerMemoryLimitPerPodMiB: "Memory limit (MiB, largest per pod)",
}

func evidenceTitle(key observability.UtilisationKey) string {
	if t, ok := evidenceTitles[key]; ok {
		return t
	}
	return string(key)
}

func summaryEvidence(res Result) eventtypes.EventEvidence {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n\n", Title(res))
	fmt.Fprintf(&b, "- Threshold: %s%%\n", trimFloat(res.ThresholdPct))
	fmt.Fprintf(&b, "- Now (last 6h): %s%%, %s\n", trimFloat(res.CurrentPct), slopePhrase(res.SlopePctPerDay))
	if res.Status == StatusBreached && len(res.Daily) > 0 {
		first := res.Daily[0]
		fmt.Fprintf(&b, "- Breached after a gradual climb: %s%% on %s to %s%% now; see the daily trend\n",
			trimFloat(first.ValuePct), first.Date, trimFloat(res.CurrentPct))
	}
	if res.ETAAt != nil {
		fmt.Fprintf(&b, "- Projected breach: %s UTC\n", res.ETAAt.Format("2006-01-02 15:04"))
	}
	resource := resourceOf(res.Condition)
	fmt.Fprintf(&b, "- Fitted on %d hours of data since the %s limit last changed\n", res.HoursOfData, resource)
	if res.PerPodLimit > 0 {
		fmt.Fprintf(&b, "- %s limit per pod: %s", resource, quantity(res.PerPodLimit, res.LimitUnit))
		if res.SuggestedLimit > 0 {
			fmt.Fprintf(&b, "; suggested: %s", quantity(res.SuggestedLimit, res.LimitUnit))
		}
		b.WriteString("\n")
	}
	return eventtypes.EventEvidence{
		Type:    "markdown",
		Insight: trendInsights(res),
		Data:    map[string]any{"data": b.String(), "name": conditionNoun(res.Condition) + " forecast"},
		AdditionalInfo: map[string]any{
			"action_name":        "metric_forecast",
			"actual_action_name": "metric_forecast",
		},
	}
}

// trendInsights states the climb and the forecast as plain sentences. Insights are the
// part of an evidence item Nubi's investigation reads inline; the markdown body and
// the table reach it only as a file it may not open, so without these its report
// explains the cause but not the trend that led to the alert.
func trendInsights(res Result) []eventtypes.EventEvidenceInsight {
	severity := "warning"
	if res.Status == StatusBreached {
		severity = "critical"
	}
	var observed []DailyRow
	var projected []string
	for _, d := range res.Daily {
		if d.Projected {
			projected = append(projected, fmt.Sprintf("%s %s%%", d.Date, trimFloat(d.ValuePct)))
		} else {
			observed = append(observed, d)
		}
	}
	insights := []eventtypes.EventEvidenceInsight{}
	if len(observed) > 0 {
		days := make([]string, 0, len(observed))
		for _, d := range observed {
			days = append(days, fmt.Sprintf("%s %s%%", d.Date, trimFloat(d.ValuePct)))
		}
		basis := "daily mean"
		if Presets[res.Condition].HourlyPeak {
			basis = "daily mean of hourly peaks"
		}
		msg := fmt.Sprintf("%s by day (%s): %s; now %s%% (last 6h), %s",
			conditionNoun(res.Condition), basis, strings.Join(days, ", "), trimFloat(res.CurrentPct), slopePhrase(res.SlopePctPerDay))
		first, last := observed[0], observed[len(observed)-1]
		if first.PerPodUsage > 0 && last.PerPodUsage > 0 && res.PerPodLimit > 0 {
			msg += fmt.Sprintf(". %s usage per pod went from %s to %s against a %s limit", resourceOf(res.Condition),
				quantity(first.PerPodUsage, res.LimitUnit), quantity(last.PerPodUsage, res.LimitUnit), quantity(res.PerPodLimit, res.LimitUnit))
		}
		insights = append(insights, eventtypes.EventEvidenceInsight{Message: msg, Severity: severity})
	}
	switch {
	case res.Status == StatusBreached:
		msg := fmt.Sprintf("Breached the %s%% SLO after a gradual climb", trimFloat(res.ThresholdPct))
		if res.SlopePctPerDay > 0 {
			msg += fmt.Sprintf("; still rising, so it will get worse unless the %s limit is raised or the load drops", resourceOf(res.Condition))
		}
		insights = append(insights, eventtypes.EventEvidenceInsight{Message: msg, Severity: severity})
	case res.ETAAt != nil && res.ETAHours != nil:
		msg := fmt.Sprintf("Forecast: reaches the %s%% SLO around %s UTC (in ~%s)",
			trimFloat(res.ThresholdPct), res.ETAAt.Format("2006-01-02 15:04"), humanHours(*res.ETAHours))
		if len(projected) > 0 {
			msg += "; projected daily mean: " + strings.Join(projected, ", ")
		}
		if res.SuggestedLimit > 0 {
			msg += fmt.Sprintf(". Suggested %s limit per pod: %s, sized for the usage projected a day past the breach",
				resourceOf(res.Condition), quantity(res.SuggestedLimit, res.LimitUnit))
		}
		insights = append(insights, eventtypes.EventEvidenceInsight{Message: msg, Severity: severity})
	}
	if res.OOMRestarts6h > 0 {
		// Evidence, not the breach: kube-state-metrics counts restarts, not kills, and
		// the kill itself is the OOMKilled alert's (which agentless accounts do not get).
		insights = append(insights, eventtypes.EventEvidenceInsight{
			Message: fmt.Sprintf("%d restarts in the last 6h, last exit OOMKilled (restart-based; a worker process killed inside a running container is not counted). The container's OOMKilled alert, if one was raised, covers the kills.",
				res.OOMRestarts6h),
			Severity: "critical",
		})
	}
	return insights
}

func tableEvidence(res Result) eventtypes.EventEvidence {
	rows := make([][]string, 0, len(res.Daily))
	for _, d := range res.Daily {
		kind := "observed"
		if d.Projected {
			kind = "projected"
		}
		limit := ""
		if d.PerPodLimit > 0 {
			limit = quantity(d.PerPodLimit, res.LimitUnit)
		}
		usage := ""
		if d.PerPodUsage > 0 {
			usage = quantity(d.PerPodUsage, res.LimitUnit)
		}
		rows = append(rows, []string{d.Date, trimFloat(d.ValuePct) + "%", usage, limit, kind})
	}
	return eventtypes.EventEvidence{
		Type:    "table",
		Insight: []eventtypes.EventEvidenceInsight{},
		Data: map[string]any{
			"column_renderers": map[string]any{},
			"headers":          tableHeaders(res.Condition),
			"rows":             rows,
			"table_name":       "Daily trend",
		},
		AdditionalInfo: map[string]any{
			"action_name":        "metric_forecast",
			"actual_action_name": "metric_forecast",
		},
	}
}

// tableHeaders name the usage and limit columns. A per-pod preset's usage is the busiest
// pod and its limit the largest per pod, which during a rollout can be different pods.
func tableHeaders(c Condition) []string {
	resource := strings.ToLower(resourceOf(c))
	if Presets[c].LimitShape == LimitPerPod {
		usage := resource + " of busiest pod"
		if Presets[c].HourlyPeak {
			usage = "peak " + usage
		}
		return []string{"day", string(c), usage, "largest " + resource + " limit per pod", ""}
	}
	return []string{"day", string(c), resource + " usage per pod", resource + " limit per pod", ""}
}

func openFiringEvent(t Target, fp string) (string, time.Time, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", time.Time{}, err
	}
	var row struct {
		FindingId string    `db:"finding_id"`
		StartsAt  time.Time `db:"starts_at"`
	}
	err = dbms.Db.Get(&row, `SELECT finding_id, starts_at FROM events
		WHERE tenant = $1 AND cloud_account_id = $2 AND fingerprint = $3 AND source = $4 AND status = 'FIRING'
		ORDER BY starts_at DESC LIMIT 1`, t.TenantId, t.AccountId, fp, Source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	return row.FindingId, row.StartsAt, err
}

func recentlyClosed(t Target, fp string, since time.Time) (bool, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, err
	}
	var cooling bool
	err = dbms.Db.Get(&cooling, `SELECT EXISTS (SELECT 1 FROM events
		WHERE tenant = $1 AND cloud_account_id = $2 AND fingerprint = $3 AND source = $4
		  AND status = 'RESOLVED' AND ends_at > $5
		  AND COALESCE(labels->>'close_reason', '') NOT IN ($6, $7, $8))`,
		t.TenantId, t.AccountId, fp, Source, since, CloseUnwatched, CloseLevelChange, CloseUnverified)
	return cooling, err
}

// closeFiringEvents resolves the open event of fp. With stable set it only closes an
// event whose last projecting run is over one cadence old, so a single clean run
// between two projecting ones does not close and reopen it.
func closeFiringEvents(t Target, fp, reason string, stable bool) error {
	now := nowFn()
	notAfter := now
	if stable {
		notAfter = now.Add(-sloCadence * 3 / 2)
	}
	return closeFiringBefore(t, fp, reason, now, notAfter)
}

// closeUnverifiedEvents resolves the open event of fp once no run has confirmed it for
// unverifiedAfter.
func closeUnverifiedEvents(t Target, fp string) error {
	now := nowFn()
	return closeFiringBefore(t, fp, CloseUnverified, now, now.Add(-unverifiedAfter))
}

// closeFiringBefore resolves the open event of fp if it was last updated at or before
// notAfter.
func closeFiringBefore(t Target, fp, reason string, now, notAfter time.Time) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	// ends_at and updated_at are `timestamp` without a zone; binding UTC values from Go
	// keeps them consistent with every other write (see closeAnomalyEventIfOpen).
	rows, err := dbms.Db.Queryx(`UPDATE events
		   SET status = 'RESOLVED',
		       ends_at = $5,
		       updated_at = $5,
		       nb_status = CASE
		                     WHEN nb_status IN ('OPEN', 'ACKNOWLEDGED', 'INVESTIGATING', 'ACTION_REQUIRED')
		                     THEN 'RESOLVED' ELSE nb_status
		                   END,
		       labels = COALESCE(labels, '{}'::jsonb) || jsonb_build_object('close_reason', $6::text)
		 WHERE tenant = $1 AND cloud_account_id = $2 AND fingerprint = $3 AND source = $4
		   AND status = 'FIRING' AND updated_at <= $7
		 RETURNING id`, t.TenantId, t.AccountId, fp, Source, now, reason, notAfter)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		publishResolve(id)
	}
	return nil
}

// onlyLimitedContainer returns the one container of the workload that has a CPU limit,
// or "" when there are none or several.
func onlyLimitedContainer(t Target, res Result) string {
	return event.SingleLimitedContainer(t.AccountId, res.Namespace, res.Kind, res.Workload, "cpu")
}

// publishResolved delivers the resolved notification the way anomaly closes do;
// a status change alone notifies no one.
func publishResolved(eventId string) {
	err := common.MqPublish(
		config.Config.RabbitMqEventPostProcessExchange,
		config.Config.RabbitMqEventPostProcessQueue,
		map[string]any{"event_id": eventId, "notify_resolved": true},
		common.MqPublishWithExpiration(time.Hour),
		common.MqPublishWithBackgroundRetry(),
	)
	if err != nil {
		slog.Error("forecast: failed to publish resolved event", "error", err, "event_id", eventId)
	}
}
