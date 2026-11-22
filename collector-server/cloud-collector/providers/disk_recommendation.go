package providers

import (
	"fmt"
	"math"
	"sort"
	"time"

	"nudgebee/collector/cloud/config"
)

const (
	// diskIOPSStep and diskThroughputStepMiBps are what a recommended setting is
	// rounded up to, so a target reads as a setting someone would choose.
	diskIOPSStep            = 100.0
	diskThroughputStepMiBps = 1.0

	// diskPerformanceFetchGroup is how many disks have their usage fetched between
	// checks of the rule's time budget.
	diskPerformanceFetchGroup = 25
)

// DiskPerformanceProfile holds what a disk type allows and charges for its IOPS and
// throughput settings, in the units DiskConfig uses.
type DiskPerformanceProfile struct {
	// MinIOPS and MinThroughputMiBps are the lowest settings worth recommending:
	// the type's minimum, or its free baseline where it has one.
	MinIOPS            float64
	MinThroughputMiBps float64
	// MaxThroughputMiBpsPerIOPS and MinThroughputMiBpsPerIOPS tie throughput to
	// IOPS: throughput may not exceed IOPS times the first, nor fall below IOPS
	// times the second. Zero means the type has no such limit.
	MaxThroughputMiBpsPerIOPS float64
	MinThroughputMiBpsPerIOPS float64
	// MonthlyIOPSCost and MonthlyThroughputCost estimate the monthly price of a
	// setting. A nil function means the setting has no known price, and it is then
	// never recommended for change.
	MonthlyIOPSCost       func(iops float64) float64
	MonthlyThroughputCost func(mibps float64) float64
}

// DiskLinearCost prices every unit above a free allowance at one rate.
func DiskLinearCost(freeUnits, pricePerUnit float64) func(float64) float64 {
	return func(units float64) float64 {
		return math.Max(0, units-freeUnits) * pricePerUnit
	}
}

// DiskPriceTier prices the units up to UpTo at Price. The last tier of a list has
// UpTo 0, meaning no upper bound.
type DiskPriceTier struct {
	UpTo  float64
	Price float64
}

// DiskTieredCost prices units in successive tiers, each tier's units at its own rate.
func DiskTieredCost(tiers []DiskPriceTier) func(float64) float64 {
	return func(units float64) float64 {
		cost, from := 0.0, 0.0
		for _, tier := range tiers {
			if units <= from {
				break
			}
			to := units
			if tier.UpTo > 0 && tier.UpTo < units {
				to = tier.UpTo
			}
			cost += (to - from) * tier.Price
			from = to
		}
		return cost
	}
}

// DiskPerformanceThresholds are the tunable parts of the over-provisioning decision.
// They are provisional values, set from configuration.
type DiskPerformanceThresholds struct {
	// HeadroomPercent is added on top of the observed peak to get the setting the
	// workload needs.
	HeadroomPercent float64
	// MaxUtilizationPercent is the peak utilization below which a setting counts as
	// over-provisioned.
	MaxUtilizationPercent float64
	// MinMonthlySavings is the estimated saving below which nothing is recommended.
	MinMonthlySavings float64
}

// DiskPerformanceTarget is a lower-cost setting for a disk. A nil field means that
// setting should be left as it is.
type DiskPerformanceTarget struct {
	IOPS            *int64
	ThroughputMiBps *float64
	// MonthlySavings is an estimate from list prices.
	MonthlySavings float64
}

// diskSavingsPotential is the most a disk could save: the cost of what it has
// provisioned above the profile's lowest settings. It is zero for a disk already at
// those settings, or whose settings are not explicit or not priced.
func diskSavingsPotential(config DiskConfig, profile DiskPerformanceProfile) float64 {
	potential := 0.0
	if config.IOPSSource == DiskPerformanceExplicit && config.ProvisionedIOPS != nil && profile.MonthlyIOPSCost != nil {
		potential += math.Max(0, profile.MonthlyIOPSCost(float64(*config.ProvisionedIOPS))-profile.MonthlyIOPSCost(profile.MinIOPS))
	}
	if config.ThroughputSource == DiskPerformanceExplicit && config.ProvisionedThroughputMiBps != nil && profile.MonthlyThroughputCost != nil {
		potential += math.Max(0, profile.MonthlyThroughputCost(*config.ProvisionedThroughputMiBps)-profile.MonthlyThroughputCost(profile.MinThroughputMiBps))
	}
	return potential
}

func roundUpTo(value, step float64) float64 {
	return math.Ceil(value/step-1e-9) * step
}

// RecommendDiskPerformance decides whether a disk's IOPS or throughput is
// over-provisioned and, if so, returns the lower setting to move to.
//
// It recommends nothing unless the measurements are complete. Each setting is judged
// on its own: it must be explicit and priced, the cloud must not have flagged the
// workload as hitting it, and the observed peak must be under
// MaxUtilizationPercent of it. The target is the peak plus headroom, rounded up and
// never below the type's minimum — not the bare peak. The two targets are then
// adjusted so the pair is one the disk type accepts, and the result is returned only
// if it is cheaper by at least MinMonthlySavings.
func RecommendDiskPerformance(config DiskConfig, analysis DiskPerformanceAnalysis, profile DiskPerformanceProfile, thresholds DiskPerformanceThresholds) (DiskPerformanceTarget, bool) {
	if analysis.DataQuality != DiskDataQualityComplete {
		return DiskPerformanceTarget{}, false
	}
	headroom := 1 + thresholds.HeadroomPercent/100

	iopsKnown := config.IOPSSource == DiskPerformanceExplicit && config.ProvisionedIOPS != nil && *config.ProvisionedIOPS > 0
	throughputKnown := config.ThroughputSource == DiskPerformanceExplicit && config.ProvisionedThroughputMiBps != nil && *config.ProvisionedThroughputMiBps > 0

	var currentIOPS, targetIOPS float64
	if iopsKnown {
		currentIOPS = float64(*config.ProvisionedIOPS)
		targetIOPS = currentIOPS
		utilization := analysis.IOPSUtilizationPercent(config)
		exceeded := analysis.IOPSLimitExceeded != nil && *analysis.IOPSLimitExceeded
		if profile.MonthlyIOPSCost != nil && utilization != nil && !exceeded && *utilization < thresholds.MaxUtilizationPercent {
			needed := math.Max(roundUpTo(*analysis.PeakIOPS*headroom, diskIOPSStep), profile.MinIOPS)
			targetIOPS = math.Min(currentIOPS, needed)
		}
	}

	var currentThroughput, targetThroughput float64
	if throughputKnown {
		currentThroughput = *config.ProvisionedThroughputMiBps
		targetThroughput = currentThroughput
		utilization := analysis.ThroughputUtilizationPercent(config)
		exceeded := analysis.ThroughputLimitExceeded != nil && *analysis.ThroughputLimitExceeded
		if profile.MonthlyThroughputCost != nil && utilization != nil && !exceeded && *utilization < thresholds.MaxUtilizationPercent {
			needed := math.Max(roundUpTo(*analysis.PeakThroughputMiBps*headroom, diskThroughputStepMiBps), profile.MinThroughputMiBps)
			targetThroughput = math.Min(currentThroughput, needed)
		}
	}

	// Keep the pair valid. The current pair is valid, so capping at the current
	// value always is too.
	if iopsKnown && throughputKnown {
		if profile.MaxThroughputMiBpsPerIOPS > 0 && targetThroughput > targetIOPS*profile.MaxThroughputMiBpsPerIOPS {
			targetIOPS = math.Min(currentIOPS, roundUpTo(targetThroughput/profile.MaxThroughputMiBpsPerIOPS, diskIOPSStep))
		}
		if profile.MinThroughputMiBpsPerIOPS > 0 && targetThroughput < targetIOPS*profile.MinThroughputMiBpsPerIOPS {
			targetThroughput = math.Min(currentThroughput, roundUpTo(targetIOPS*profile.MinThroughputMiBpsPerIOPS, diskThroughputStepMiBps))
		}
	}

	target := DiskPerformanceTarget{}
	if iopsKnown && targetIOPS < currentIOPS {
		iops := int64(math.Ceil(targetIOPS))
		target.IOPS = &iops
		target.MonthlySavings += profile.MonthlyIOPSCost(currentIOPS) - profile.MonthlyIOPSCost(targetIOPS)
	}
	if throughputKnown && targetThroughput < currentThroughput {
		throughput := targetThroughput
		target.ThroughputMiBps = &throughput
		target.MonthlySavings += profile.MonthlyThroughputCost(currentThroughput) - profile.MonthlyThroughputCost(targetThroughput)
	}
	if target.IOPS == nil && target.ThroughputMiBps == nil {
		return DiskPerformanceTarget{}, false
	}
	if target.MonthlySavings <= 0 || target.MonthlySavings < thresholds.MinMonthlySavings {
		return DiskPerformanceTarget{}, false
	}
	return target, true
}

// DiskPerformanceRuleOptions bounds one run of the over-provisioning rule.
type DiskPerformanceRuleOptions struct {
	Thresholds DiskPerformanceThresholds
	// MaxDisks caps how many disks have their usage fetched in one run.
	MaxDisks int
	// TimeBudget is how long the rule may keep starting new fetches. The rule runs
	// inside the recommendation sync, which has a fixed overall time limit shared
	// with every other rule of the account.
	TimeBudget time.Duration
	// Now is the end of the observation window.
	Now time.Time
}

// DiskPerformanceRuleOptionsFromConfig reads the rule's settings from configuration.
func DiskPerformanceRuleOptionsFromConfig() DiskPerformanceRuleOptions {
	return DiskPerformanceRuleOptions{
		Thresholds: DiskPerformanceThresholds{
			HeadroomPercent:       config.Config.CloudCollectorDiskPerformanceHeadroomPercent,
			MaxUtilizationPercent: config.Config.CloudCollectorDiskPerformanceMaxUtilizationPercent,
			MinMonthlySavings:     config.Config.CloudCollectorDiskPerformanceMinMonthlySavings,
		},
		MaxDisks:   config.Config.CloudCollectorDiskPerformanceMaxDisks,
		TimeBudget: time.Duration(config.Config.CloudCollectorDiskPerformanceTimeBudgetSeconds) * time.Second,
		Now:        time.Now(),
	}
}

// DiskSeriesFetcher fetches the usage history of a group of disks and passes each
// disk's series to yield. Disks it could not read are simply not yielded.
type DiskSeriesFetcher func(disks []DiskConfig, start, end time.Time, yield func(DiskPerformanceSeries)) error

// DiskPerformanceFinding is one over-provisioned disk: its configuration, what was
// observed, and the setting to move to.
type DiskPerformanceFinding struct {
	Config   DiskConfig
	Analysis DiskPerformanceAnalysis
	Target   DiskPerformanceTarget
}

// FindOverprovisionedDisks runs the over-provisioning check over a set of disks.
//
// Usage history is expensive to fetch, so the disks are narrowed first, from their
// configuration alone, to those that are attached and are paying for performance
// above their type's lowest setting — a disk already there has nothing to save. What
// remains is ordered by how much it could save, capped at MaxDisks, and fetched a
// group at a time until the time budget runs out, so a large account spends the
// budget on its most valuable disks. The order is stable across runs, which keeps
// the same disks inside the cap from one sync to the next.
//
// A fetch failure is logged and never fails the caller, which is producing other
// recommendations in the same pass.
//
// The second result lists the candidate disks that could not be evaluated: their
// usage could not be read, their history was not complete enough to judge, or the
// cap or time budget was reached before them. They
// are reported separately from disks that were evaluated and found fine, so a caller
// can leave their existing recommendations untouched instead of closing them.
func FindOverprovisionedDisks(ctx CloudProviderContext, disks []DiskConfig, profileFor func(DiskConfig) (DiskPerformanceProfile, bool), fetch DiskSeriesFetcher, options DiskPerformanceRuleOptions) (findings []DiskPerformanceFinding, unevaluated []DiskConfig) {
	type candidate struct {
		config    DiskConfig
		profile   DiskPerformanceProfile
		potential float64
	}
	var candidates []candidate
	for _, disk := range disks {
		if !disk.Attached || !disk.HasAdjustablePerformance() {
			continue
		}
		profile, ok := profileFor(disk)
		if !ok {
			continue
		}
		potential := diskSavingsPotential(disk, profile)
		if potential <= 0 || potential < options.Thresholds.MinMonthlySavings {
			continue
		}
		candidates = append(candidates, candidate{disk, profile, potential})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].potential != candidates[j].potential {
			return candidates[i].potential > candidates[j].potential
		}
		return candidates[i].config.DiskID < candidates[j].config.DiskID
	})
	if options.MaxDisks > 0 && len(candidates) > options.MaxDisks {
		ctx.GetLogger().Warn("disk performance rule: more candidate disks than the per-run cap, analysing the highest-saving ones",
			"candidates", len(candidates), "cap", options.MaxDisks)
		for _, c := range candidates[options.MaxDisks:] {
			unevaluated = append(unevaluated, c.config)
		}
		candidates = candidates[:options.MaxDisks]
	}

	start, end := DiskObservationRange(options.Now)
	deadline := time.Now().Add(options.TimeBudget)

	for offset := 0; offset < len(candidates); offset += diskPerformanceFetchGroup {
		if options.TimeBudget > 0 && time.Now().After(deadline) {
			ctx.GetLogger().Warn("disk performance rule: time budget used up, remaining disks not analysed",
				"analysed", offset, "remaining", len(candidates)-offset, "budget", options.TimeBudget.String())
			for _, c := range candidates[offset:] {
				unevaluated = append(unevaluated, c.config)
			}
			break
		}
		group := candidates[offset:min(offset+diskPerformanceFetchGroup, len(candidates))]
		byID := make(map[string]candidate, len(group))
		configs := make([]DiskConfig, 0, len(group))
		for _, c := range group {
			byID[c.config.DiskID] = c
			configs = append(configs, c.config)
		}

		evaluated := make(map[string]bool, len(group))
		err := fetch(configs, start, end, func(series DiskPerformanceSeries) {
			c, ok := byID[series.DiskID]
			if !ok {
				return
			}
			analysis := AnalyzeDiskPerformance(series)
			// Too little history to judge is the same as not having read the disk:
			// nothing can be concluded either way, so a recommendation it already
			// has must not be closed. Otherwise a disk whose coverage dips below
			// complete for a while would have its row archived and raised again.
			if analysis.DataQuality != DiskDataQualityComplete {
				return
			}
			evaluated[series.DiskID] = true
			if target, ok := RecommendDiskPerformance(c.config, analysis, c.profile, options.Thresholds); ok {
				findings = append(findings, DiskPerformanceFinding{Config: c.config, Analysis: analysis, Target: target})
			}
		})
		if err != nil {
			ctx.GetLogger().Error("disk performance rule: failed to fetch usage for some disks", "error", err)
		}
		// A disk the fetch never yielded, or yielded with incomplete history, could
		// not be judged — which is different from one judged and found fine.
		for _, c := range group {
			if !evaluated[c.config.DiskID] {
				unevaluated = append(unevaluated, c.config)
			}
		}
	}
	return findings, unevaluated
}

// DiskPerformanceRecommendationData builds the provider-neutral payload of an
// over-provisioned disk recommendation: what is provisioned, what was observed over
// which window, and what to change it to. Providers add their own identifiers to it.
func DiskPerformanceRecommendationData(finding DiskPerformanceFinding, thresholds DiskPerformanceThresholds) map[string]any {
	config, analysis, target := finding.Config, finding.Analysis, finding.Target
	observationDays := int(math.Round(analysis.ObservationEnd.Sub(analysis.ObservationStart).Hours() / 24))

	data := map[string]any{
		"disk_id":                  config.DiskID,
		"disk_name":                config.DiskName,
		"disk_type":                config.DiskType,
		"disk_region":              config.Region,
		"observation_start":        analysis.ObservationStart.Format(time.RFC3339),
		"observation_end":          analysis.ObservationEnd.Format(time.RFC3339),
		"observation_days":         observationDays,
		"metric_period_seconds":    analysis.PeriodSeconds,
		"peak_resolution_seconds":  analysis.PeakResolutionSeconds,
		"coverage_percent":         math.Round(analysis.CoveragePercent*10) / 10,
		"headroom_percent":         thresholds.HeadroomPercent,
		"estimated_monthly_saving": math.Round(target.MonthlySavings*100) / 100,
		"savings_is_estimate":      true,
	}
	if config.CapacityGiB != nil {
		data["disk_size_gib"] = *config.CapacityGiB
	}

	reason := fmt.Sprintf("Over the last %d days", observationDays)
	if config.ProvisionedIOPS != nil && analysis.PeakIOPS != nil {
		data["current_iops"] = *config.ProvisionedIOPS
		data["peak_iops"] = math.Round(*analysis.PeakIOPS)
		if utilization := analysis.IOPSUtilizationPercent(config); utilization != nil {
			data["iops_utilization_percent"] = math.Round(*utilization*10) / 10
		}
		reason += fmt.Sprintf(" the disk peaked at %.0f IOPS against %d provisioned", *analysis.PeakIOPS, *config.ProvisionedIOPS)
	}
	if config.ProvisionedThroughputMiBps != nil && analysis.PeakThroughputMiBps != nil {
		data["current_throughput_mibps"] = math.Round(*config.ProvisionedThroughputMiBps*10) / 10
		data["peak_throughput_mibps"] = math.Round(*analysis.PeakThroughputMiBps*10) / 10
		if utilization := analysis.ThroughputUtilizationPercent(config); utilization != nil {
			data["throughput_utilization_percent"] = math.Round(*utilization*10) / 10
		}
		if config.ProvisionedIOPS != nil && analysis.PeakIOPS != nil {
			reason += " and"
		}
		reason += fmt.Sprintf(" its throughput peaked at %.1f MiB/s against %.1f provisioned", *analysis.PeakThroughputMiBps, *config.ProvisionedThroughputMiBps)
	}
	reason += fmt.Sprintf(" (busiest %d-minute measurement).", max(analysis.PeakResolutionSeconds, 60)/60)

	if target.IOPS != nil {
		data["recommended_iops"] = *target.IOPS
		reason += fmt.Sprintf(" Lower IOPS to %d.", *target.IOPS)
	}
	if target.ThroughputMiBps != nil {
		data["recommended_throughput_mibps"] = math.Round(*target.ThroughputMiBps*10) / 10
		reason += fmt.Sprintf(" Lower throughput to %.1f MiB/s.", *target.ThroughputMiBps)
	}
	reason += fmt.Sprintf(" The recommended settings keep %.0f%% headroom above the observed peak. Disks are limited per second, so bursts shorter than the measurement can exceed the peak shown. The saving is an estimate from list prices.", thresholds.HeadroomPercent)
	data["reason"] = reason
	return data
}

// UnevaluatedRecommendation builds the placeholder that tells the store step a rule
// could not be evaluated for a resource in this run. See Recommendation.Unevaluated.
func UnevaluatedRecommendation(ruleName string, category RecommendationCategory, resource Resource) Recommendation {
	return Recommendation{
		CategoryName:        category,
		RuleName:            ruleName,
		Unevaluated:         true,
		ResourceServiceName: resource.ServiceName,
		ResourceId:          resource.Id,
		ResourceType:        resource.Type,
		ResourceRegion:      resource.Region,
	}
}
