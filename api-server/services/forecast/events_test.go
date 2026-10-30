package forecast

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/event"
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/observability"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type closeCall struct {
	fp, reason string
	stable     bool
}

type store struct {
	open     map[string]string // fingerprint -> finding_id
	cooling  map[string]bool
	closes   []closeCall
	upserted []event.Event
	// stale is what the stale-container lookup returns.
	stale []string
}

var syncNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func fakeStore(t *testing.T) *store {
	t.Helper()
	s := &store{open: map[string]string{}, cooling: map[string]bool{}}
	orig := [...]any{openFiring, inCooldown, closeFiring, upsertEvent, nowFn, limitedContainer, staleContainers, closeUnverified}
	origCooldown := config.Config.NBForecastRefireCooldownHours
	t.Cleanup(func() {
		openFiring = orig[0].(func(Target, string) (string, time.Time, error))
		inCooldown = orig[1].(func(Target, string, time.Time) (bool, error))
		closeFiring = orig[2].(func(Target, string, string, bool) error)
		upsertEvent = orig[3].(func(event.Event) error)
		nowFn = orig[4].(func() time.Time)
		limitedContainer = orig[5].(func(Target, Result) string)
		staleContainers = orig[6].(func(Target, Result) ([]string, error))
		closeUnverified = orig[7].(func(Target, string) error)
		config.Config.NBForecastRefireCooldownHours = origCooldown
	})
	config.Config.NBForecastRefireCooldownHours = 24
	nowFn = func() time.Time { return syncNow }
	limitedContainer = func(Target, Result) string { return "app" }
	openFiring = func(_ Target, fp string) (string, time.Time, error) {
		if id, ok := s.open[fp]; ok {
			return id, syncNow.Add(-5 * time.Hour), nil
		}
		return "", time.Time{}, nil
	}
	inCooldown = func(_ Target, fp string, _ time.Time) (bool, error) { return s.cooling[fp], nil }
	closeFiring = func(_ Target, fp, reason string, stable bool) error {
		s.closes = append(s.closes, closeCall{fp, reason, stable})
		return nil
	}
	closeUnverified = func(_ Target, fp string) error {
		s.closes = append(s.closes, closeCall{fp, CloseUnverified, true})
		return nil
	}
	upsertEvent = func(e event.Event) error { s.upserted = append(s.upserted, e); return nil }
	staleContainers = func(Target, Result) ([]string, error) { return s.stale, nil }
	return s
}

var target = Target{TenantId: "ten", AccountId: "acc", AccountName: "prod-cluster", SLOConfigId: "slo-1"}

func projected(level Level, eta float64) Result {
	at := syncNow.Add(time.Duration(eta * float64(time.Hour)))
	return Result{
		Status: StatusProjected, Condition: ConditionThrottling, ThresholdPct: 25, CurrentPct: 15,
		SlopePctPerDay: 5, ETAHours: &eta, ETAAt: &at, Level: level, HoursOfData: 48,
		Namespace: "shop", Workload: "order-service", Kind: "deployment", PerPodLimit: 0.5, SuggestedLimit: 0.65,
		Daily: []DailyRow{{Date: "2026-10-09", ValuePct: 10}, {Date: "2026-10-10", ValuePct: 15}, {Date: "2026-10-11", ValuePct: 20, Projected: true}},
	}
}

func TestSync_FirstProjectionInsertsAFreshFiring(t *testing.T) {
	s := fakeStore(t)
	res := projected(LevelMedium, 60)
	require.NoError(t, Sync(target, res, nil))

	require.Len(t, s.closes, 2)
	assert.Equal(t, closeCall{fingerprint(target, res, LevelBreached), CloseClean, true}, s.closes[0], "below the threshold, the breach alert closes after two clean runs")
	assert.Equal(t, fingerprint(target, res, LevelHigh), s.closes[1].fp, "the other level is closed before the insert")
	assert.Equal(t, CloseLevelChange, s.closes[1].reason)

	require.Len(t, s.upserted, 1)
	e := s.upserted[0]
	fp := fingerprint(target, res, LevelMedium)
	assert.Equal(t, fp, e.Fingerprint)
	assert.Equal(t, fmt.Sprintf("%s-%d", fp, syncNow.Unix()), e.FindingId, "one finding_id per firing")
	assert.Equal(t, Source, e.Source)
	assert.Equal(t, "CPUThrottlingPredicted", e.AggregationKey)
	assert.Equal(t, "MEDIUM", string(e.Priority))
	assert.Equal(t, "deployment", e.SubjectOwnerKind, "lowercase, the webhook convention")
	assert.Equal(t, "order-service", e.SubjectOwner)
	assert.Equal(t, "slo-1", e.Labels["slo_config_id"])
	assert.Equal(t, "app", e.Labels["container"], "the resize fix needs the one limited container")
	assert.Equal(t, "order-service: CPU throttling projected to reach 25% in ~2.5 days (now 15%)", e.Title)
	assert.True(t, e.StartsAt.Equal(syncNow))
}

func TestSync_OpenFiringIsUpdatedNotDuplicated(t *testing.T) {
	s := fakeStore(t)
	res := projected(LevelHigh, 20)
	s.open[fingerprint(target, res, LevelHigh)] = "existing-id"
	require.NoError(t, Sync(target, res, nil))

	require.Len(t, s.upserted, 1)
	assert.Equal(t, "existing-id", s.upserted[0].FindingId)
	assert.True(t, s.upserted[0].StartsAt.Equal(syncNow.Add(-5*time.Hour)), "the firing keeps its start")
	assert.Equal(t, "order-service: CPU throttling projected to reach 25% in ~20h (now 15%)", s.upserted[0].Title)
}

func TestSync_CooldownSuppressesARefire(t *testing.T) {
	s := fakeStore(t)
	res := projected(LevelHigh, 20)
	s.cooling[fingerprint(target, res, LevelHigh)] = true
	require.NoError(t, Sync(target, res, nil))
	assert.Empty(t, s.upserted)
}

func TestSync_NonProjectingStatusesCloseStably(t *testing.T) {
	for _, st := range []Status{StatusNotProjected, StatusInsufficientHistory, StatusNoLimit, StatusProviderUnsupported} {
		s := fakeStore(t)
		require.NoError(t, Sync(target, Result{Status: st, Condition: ConditionThrottling, Namespace: "shop", Workload: "order-service", Kind: "deployment"}, nil))
		require.Len(t, s.closes, 3, st)
		for _, c := range s.closes {
			assert.True(t, c.stable, "%s closes only after two clean runs", st)
			want := CloseClean
			if st == StatusInsufficientHistory {
				want = CloseUnverified
			}
			assert.Equal(t, want, c.reason, st)
		}
	}
}

func TestSync_BreachReplacesTheWarningsWithABreachAlert(t *testing.T) {
	s := fakeStore(t)
	res := Result{
		Status: StatusBreached, Condition: ConditionThrottling, ThresholdPct: 75, CurrentPct: 78, SlopePctPerDay: 24,
		Namespace: "demo", Workload: "nbdemo-orders", Kind: "deployment",
		Daily: []DailyRow{{Date: "2026-10-20", ValuePct: 22}, {Date: "2026-10-21", ValuePct: 46}, {Date: "2026-10-22", ValuePct: 70}},
	}
	require.NoError(t, Sync(target, res, nil))

	require.Len(t, s.closes, 2)
	for _, c := range s.closes {
		assert.False(t, c.stable, "a breach closes the warnings at once")
		assert.Equal(t, CloseBreached, c.reason)
	}
	require.Len(t, s.upserted, 1)
	e := s.upserted[0]
	assert.Equal(t, "CPUThrottlingSLOBreached", e.AggregationKey)
	assert.Equal(t, "HIGH", string(e.Priority), "a breach always notifies and is investigated")
	assert.Equal(t, fingerprint(target, res, LevelBreached), e.Fingerprint)
	assert.Equal(t, "nbdemo-orders: CPU throttling breached its 75% SLO (now 78%, rising 24 pp/day)", e.Title)
	md := e.Evidences[0].(eventtypes.EventEvidence).Data.(map[string]any)["data"].(string)
	assert.Contains(t, md, "gradual climb: 22% on 2026-10-20 to 78% now", "the alert carries the history that led to it")
	insights := e.Evidences[0].(eventtypes.EventEvidence).Insight
	require.Len(t, insights, 2)
	assert.Equal(t, "CPU throttling by day (daily mean): 2026-10-20 22%, 2026-10-21 46%, 2026-10-22 70%; now 78% (last 6h), rising 24 pp/day", insights[0].Message,
		"Nubi reads insights inline, so the climb must be in one")
	assert.Equal(t, "critical", insights[0].Severity)
	assert.Contains(t, insights[1].Message, "Breached the 75% SLO after a gradual climb; still rising")
}

func TestTrendInsights_ProjectionStatesTheClimbUsageAndForecast(t *testing.T) {
	res := projected(LevelHigh, 20)
	res.Daily[0].PerPodUsage, res.Daily[1].PerPodUsage = 0.3, 0.42
	insights := trendInsights(res)
	require.Len(t, insights, 2)
	assert.Equal(t, "CPU throttling by day (daily mean): 2026-10-09 10%, 2026-10-10 15%; now 15% (last 6h), rising 5 pp/day."+
		" CPU usage per pod went from 300m to 420m against a 500m limit", insights[0].Message)
	assert.Equal(t, "warning", insights[0].Severity)
	assert.Equal(t, "Forecast: reaches the 25% SLO around "+res.ETAAt.Format("2006-01-02 15:04")+" UTC (in ~20h); projected daily mean: 2026-10-11 20%."+
		" Suggested CPU limit per pod: 650m, sized for the usage projected a day past the breach", insights[1].Message)
}

func TestSlopePhrase(t *testing.T) {
	assert.Equal(t, "rising 2.5 pp/day", slopePhrase(2.5))
	assert.Equal(t, "falling 3 pp/day", slopePhrase(-3))
	assert.Equal(t, "flat", slopePhrase(-0.02), "worded from the rounded figure, never \"falling 0\"")
	assert.Equal(t, "flat", slopePhrase(0.04))
	assert.Equal(t, "rising 0.1 pp/day", slopePhrase(0.06))
	assert.Equal(t, "flat", slopePhrase(0))
}

func TestTableEvidence_HasAUsageColumn(t *testing.T) {
	res := projected(LevelHigh, 20)
	res.Daily[0].PerPodUsage = 0.3
	data := tableEvidence(res).Data.(map[string]any)
	assert.Equal(t, []string{"day", "cpu_throttling", "cpu usage per pod", "cpu limit per pod", ""}, data["headers"])
	assert.Equal(t, []string{"2026-10-09", "10%", "300m", "", "observed"}, data["rows"].([][]string)[0])
}

// A failed run raises nothing and closes only what no run has confirmed for a day, as
// unverified, so the alert can fire again as soon as the metrics come back.
func TestSync_ErrorClosesOnlyUnverified(t *testing.T) {
	s := fakeStore(t)
	res := projected(LevelHigh, 10)
	res.Status = StatusError
	require.NoError(t, Sync(target, res, nil))
	assert.Empty(t, s.upserted)
	assert.Equal(t, []closeCall{
		{fingerprint(target, res, LevelMedium), CloseUnverified, true},
		{fingerprint(target, res, LevelHigh), CloseUnverified, true},
		{fingerprint(target, res, LevelBreached), CloseUnverified, true},
	}, s.closes)
}

func TestCloseAll_IsUnwatched(t *testing.T) {
	s := fakeStore(t)
	require.NoError(t, CloseAll(target, "shop", "Deployment", "order-service", "", ConditionSaturation))
	require.Len(t, s.closes, 3, "both warnings and the breach alert")
	for _, c := range s.closes {
		assert.Equal(t, CloseUnwatched, c.reason)
		assert.False(t, c.stable)
		assert.True(t, strings.Contains(c.fp, "|deployment|order-service|cpu_saturation|"), c.fp)
	}
}

func TestEvidences_ShowTheFittedSeries(t *testing.T) {
	series := Series{
		"cpu_throttling_ratio": {QueryKey: "cpu_throttling_ratio", Query: "q", Payload: []observability.Result{{Timestamps: []int64{1}, Values: []float64{0.1}}}},
		"cpu_usage_limited":    {QueryKey: "cpu_usage_limited"},
	}
	ev := Evidences(projected(LevelHigh, 20), series)
	require.Len(t, ev, 3, "summary, daily table, and only the series that came back")
	assert.Equal(t, "markdown", ev[0].(eventtypes.EventEvidence).Type)
	table := ev[1].(eventtypes.EventEvidence)
	assert.Equal(t, "table", table.Type)
	assert.Len(t, table.Data.(map[string]any)["rows"], 3)
	assert.Equal(t, "prometheus", ev[2].(map[string]any)["type"])
}

func TestQuantity_WritesCoresAsMillicores(t *testing.T) {
	assert.Equal(t, "350m", quantity(0.35, UnitCores))
	assert.Equal(t, "350m", quantity(0.35, ""), "cores when the unit is unset")
}
