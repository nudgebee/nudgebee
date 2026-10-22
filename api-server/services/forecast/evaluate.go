package forecast

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/internal/database"
	"nudgebee/services/observability"
	"nudgebee/services/security"
)

// Request names one workload and the SLO it is forecast against.
type Request struct {
	AccountId string
	Namespace string
	Name      string
	Kind      string
	// Container is the one container a container-scoped preset measures; required for
	// those and ignored by the others.
	Container    string
	Condition    Condition
	ThresholdPct float64
	// EndTime is the end of the 3-day window; zero means now. A past end time replays
	// the forecast as it would have run then, which is how the backtest works.
	EndTime time.Time
}

// historyWindow is how much history one forecast reads. Three whole days keep the
// daily cycle out of the slope while staying short enough to follow a recent change.
const historyWindow = 72 * time.Hour

const bucket = time.Hour

// sampleStep is the resolution the series are fetched at; hourly() averages them into
// buckets. Fetching at the bucket width instead fails twice: a VictoriaMetrics-style
// store snaps the step grid to whole hours, so the current hour (a limit set minutes
// ago) is never sampled, and one 30-minute rate per hour sees only half of it.
const sampleStep = 5 * time.Minute

// forecastKeys are the utilisation keys one forecast of p reads: its value, the
// limit and usage it is measured against, and, for workload totals, the pod count that
// makes those per pod; plus any evidence-only key.
func forecastKeys(p Preset) []string {
	var keys []string
	seen := map[observability.UtilisationKey]bool{}
	podCount := observability.KeyPodCount
	if p.LimitShape == LimitPerPod {
		podCount = ""
	}
	for _, k := range []observability.UtilisationKey{p.Value, p.Per, p.Limit, p.Usage, podCount, p.OOMRestarts} {
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, string(k))
		}
	}
	return keys
}

// Seams for tests: the provider lookup, the metrics fetch and the pod inventory.
var (
	metricsProviderFor = observability.GetLogsMetricsTracesProvider
	fetchUtilisation   = observability.FetchMetricUtilisation
	workloadPods       = podsFromInventory
	inventoryKind      = kindFromInventory
)

// Series is what one evaluation fetched, kept so the caller can show exactly the
// series the forecast was fitted on.
type Series map[string]observability.QueryResult

// Evaluate forecasts one workload against one CPU SLO threshold.
func Evaluate(ctx *security.RequestContext, req Request) Result {
	res, _ := EvaluateWithSeries(ctx, req)
	return res
}

// EvaluateWithSeries is Evaluate plus the series it fetched, for a caller that shows
// them as evidence.
func EvaluateWithSeries(ctx *security.RequestContext, req Request) (Result, Series) {
	now := time.Now().UTC()
	end := req.EndTime
	if end.IsZero() {
		end = now
	}
	// The window ends now, not at the last whole hour: a limit set a few minutes ago
	// must already count, or saving a CPU SLO right after adding one is refused as
	// "no CPU limit". Samples still land in whole clock-hour buckets (see hourly).
	end = end.UTC()
	start := end.Add(-historyWindow)
	preset, known := PresetFor(req.Condition)
	if req.ThresholdPct <= 0 {
		req.ThresholdPct = preset.DefaultThreshold
	}
	if req.Kind == "" {
		// A caller that does not know the kind (Check now, Nubi) would otherwise be
		// treated as a Deployment, and a StatefulSet would find no pods.
		req.Kind = inventoryKind(req.AccountId, req.Namespace, req.Name)
	}
	req.Kind = normalizeKind(req.Kind)

	res := Result{
		Condition:    req.Condition,
		ThresholdPct: req.ThresholdPct,
		Namespace:    req.Namespace,
		Workload:     req.Name,
		Kind:         req.Kind,
		Container:    req.Container,
		EvaluatedAt:  now,
		Daily:        []DailyRow{},
	}
	fail := func(status Status, reason string) (Result, Series) {
		res.Status, res.Reason = status, reason
		return res, nil
	}
	if !known {
		return fail(StatusError, fmt.Sprintf("unknown metric %q; one of %s", req.Condition, strings.Join(ConditionNames(), ", ")))
	}
	if !preset.ContainerScoped {
		res.Container = ""
	} else if req.Container == "" {
		// Never run unscoped: the fingerprint, the label and the fix all name it.
		return fail(StatusError, fmt.Sprintf("%s needs a container", req.Condition))
	}

	provider, _, err := metricsProviderFor(ctx, req.AccountId, "", "metrics", "")
	if err != nil {
		return fail(StatusError, "could not resolve the account's metrics provider: "+err.Error())
	}
	res.Provider = provider
	if !providerSupported(provider) {
		return fail(StatusProviderUnsupported, fmt.Sprintf("%s forecasts do not support the %q metrics provider yet", preset.Resource, provider))
	}

	pods, err := workloadPods(req.AccountId, req.Namespace, req.Name, req.Kind, start)
	if err != nil {
		return fail(StatusError, "could not read the workload's pods: "+err.Error())
	}
	if len(pods) == 0 {
		return fail(StatusInsufficientHistory, "no pods of this workload were seen in the last 3 days")
	}

	out, err := fetchWithTimeout(ctx, observability.GetUtilisationTrendRequest{
		AccountId:    req.AccountId,
		StartTime:    start.UnixMilli(),
		EndTime:      end.UnixMilli(),
		StepInterval: int(sampleStep.Seconds()),
		Step:         int(sampleStep.Seconds()),
		PodNames:     pods,
		Request:      metricsRequest(req, preset),
	})
	if err != nil {
		return fail(StatusError, "could not fetch metrics: "+err.Error())
	}

	series := Series{}
	for _, r := range out.Results {
		if r.Error != nil && *r.Error != "" {
			return fail(StatusError, fmt.Sprintf("metrics query %s failed: %s", r.QueryKey, *r.Error))
		}
		series[r.QueryKey] = r
	}

	usage := hourly(series[string(preset.Usage)])
	limit := hourly(series[string(preset.Limit)])
	perPodLimit, perPodUsage := limit, usage
	if preset.LimitShape != LimitPerPod {
		podCount := hourly(series[string(observability.KeyPodCount)])
		perPodLimit, perPodUsage = divide(limit, podCount, 1), divide(usage, podCount, 1)
	}

	values := scale(hourly(series[string(preset.Value)]), 100)
	if preset.Per != "" {
		values = divide(hourly(series[string(preset.Value)]), hourly(series[string(preset.Per)]), 100)
	}

	fit := Fit(FitInput{
		Condition:     req.Condition,
		ThresholdPct:  req.ThresholdPct,
		Value:         values,
		PerPodLimit:   perPodLimit,
		PerPodUsage:   perPodUsage,
		RoundStep:     preset.RoundStep,
		NoLimitReason: preset.NoLimitReason,
		Resource:      preset.Resource,
		Now:           end,
		Params:        paramsFor(req.Condition),
	})
	fit.LimitUnit = preset.Unit
	fit.Namespace, fit.Workload, fit.Kind, fit.Container = res.Namespace, res.Workload, res.Kind, res.Container
	if preset.OOMRestarts != "" {
		fit.OOMRestarts6h = int(math.Round(lastSample(series[string(preset.OOMRestarts)])))
	}
	fit.Provider = res.Provider
	fit.EvaluatedAt = now
	return fit, series
}

// metricsRequest is the observability request of one forecast; a container-scoped
// preset narrows every key to its container.
func metricsRequest(req Request, preset Preset) map[string]any {
	m := map[string]any{
		"workload_namespace": req.Namespace,
		"workload_name":      req.Name,
		"kind":               req.Kind,
		"metrics":            forecastKeys(preset),
	}
	if preset.ContainerScoped {
		m["container_name"] = req.Container
	}
	return m
}

// lastSample is the series' latest finite sample, or 0.
func lastSample(r observability.QueryResult) float64 {
	if len(r.Payload) == 0 {
		return 0
	}
	s := r.Payload[0]
	for i := min(len(s.Values), len(s.Timestamps)) - 1; i >= 0; i-- {
		if v := s.Values[i]; !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v
		}
	}
	return 0
}

func paramsFor(c Condition) Params {
	// Throttling swings more for the same pressure than a saturation does, so it has
	// its own, lower bar for what counts as a climb.
	rise := config.Config.NBForecastMinRiseSaturationPct
	if c == ConditionThrottling {
		rise = config.Config.NBForecastMinRiseThrottlingPct
	}
	return Params{
		MinHours:        max(config.Config.NBForecastMinHours, minFitHours),
		MinRisePct:      rise,
		MaxETAHours:     config.Config.NBForecastMaxETAHours,
		HighWithinHours: config.Config.NBForecastHighWithinHours,
	}
}

func providerSupported(provider string) bool {
	for _, p := range strings.Split(config.Config.NBForecastProviders, ",") {
		if strings.EqualFold(strings.TrimSpace(p), provider) && provider != "" {
			return true
		}
	}
	return false
}

func normalizeKind(kind string) string {
	switch strings.ToLower(kind) {
	case "statefulset", "daemonset":
		return strings.ToLower(kind)
	default:
		return "deployment"
	}
}

// podsFromInventory lists every pod the collector saw for the workload since start,
// live or not, so pods replaced by a rollout inside the window still count. The name
// prefix would be wrong twice: it misses nothing that matters here, but it lets
// "services-server" pick up "services-server-worker".
func podsFromInventory(accountId, namespace, name, kind string, start time.Time) ([]string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var pods []string
	err = dbms.Db.Select(&pods, `SELECT DISTINCT name FROM k8s_pods
		WHERE cloud_account_id = $1 AND namespace = $2 AND workload_name = $3
		  AND lower(workload_type) = $4 AND last_seen >= $5
		ORDER BY name`, accountId, namespace, name, kind, start)
	return pods, err
}

// kindFromInventory is the kind of the workload's most recently seen pod owner, or ""
// when the inventory has none.
func kindFromInventory(accountId, namespace, name string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	var kind string
	if err := dbms.Db.Get(&kind, `SELECT workload_type FROM k8s_pods
		WHERE cloud_account_id = $1 AND namespace = $2 AND workload_name = $3 AND workload_type IS NOT NULL
		ORDER BY last_seen DESC LIMIT 1`, accountId, namespace, name); err != nil {
		return ""
	}
	return kind
}

func fetchWithTimeout(ctx *security.RequestContext, req observability.GetUtilisationTrendRequest) (observability.OutputMetricQuery, error) {
	type answer struct {
		out observability.OutputMetricQuery
		err error
	}
	done := make(chan answer, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- answer{err: fmt.Errorf("metrics fetch panicked: %v", r)}
			}
		}()
		out, err := fetchUtilisation(ctx, req)
		done <- answer{out, err}
	}()
	timeout := time.Duration(config.Config.NBForecastFetchTimeoutSeconds) * time.Second
	select {
	case a := <-done:
		return a.out, a.err
	case <-time.After(timeout):
		return observability.OutputMetricQuery{}, fmt.Errorf("timed out after %s", timeout)
	}
}

// hourly turns a summed series into hourly buckets, averaging any samples that land
// in the same hour. A provider that ignores the requested step cannot fake the
// minimum history this way: it is counted in buckets, never in samples.
func hourly(r observability.QueryResult) []Point {
	if len(r.Payload) == 0 {
		return nil
	}
	type acc struct{ sum, n float64 }
	buckets := map[int64]*acc{}
	s := r.Payload[0]
	for i, ts := range s.Timestamps {
		if i >= len(s.Values) {
			break
		}
		v := s.Values[i]
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		h := time.Unix(ts, 0).UTC().Truncate(bucket).Unix()
		if buckets[h] == nil {
			buckets[h] = &acc{}
		}
		buckets[h].sum += v
		buckets[h].n++
	}
	keys := make([]int64, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]Point, 0, len(keys))
	for _, k := range keys {
		out = append(out, Point{T: time.Unix(k, 0).UTC(), V: buckets[k].sum / buckets[k].n})
	}
	return out
}

func scale(in []Point, factor float64) []Point {
	out := make([]Point, len(in))
	for i, p := range in {
		out[i] = Point{T: p.T, V: p.V * factor}
	}
	return out
}

// divide pairs a and b by hour and returns a/b*factor wherever both exist and b > 0.
func divide(a, b []Point, factor float64) []Point {
	byHour := make(map[int64]float64, len(b))
	for _, p := range b {
		byHour[p.T.Unix()] = p.V
	}
	out := make([]Point, 0, len(a))
	for _, p := range a {
		if d, ok := byHour[p.T.Unix()]; ok && d > 0 {
			out = append(out, Point{T: p.T, V: p.V / d * factor})
		}
	}
	return out
}
