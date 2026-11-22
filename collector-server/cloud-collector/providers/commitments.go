package providers

import (
	"sort"
	"strings"
	"time"

	"nudgebee/collector/cloud/providers/constants"
)

// CommitmentServiceName is the service the commitment recommendations are
// stored under. It has to match the native-service name the recommendation sync
// runs (case-insensitively): each pass archives the Open rows whose payload
// service_name equals the pass's own name, so a different name here would leave
// stale commitment rows Open forever. It is also part of account_object_id, so
// renaming it orphans every existing row.
const CommitmentServiceName = "Commitments"

// Provider-neutral commitment types.
const (
	CommitmentTypeSavingsPlan          = "savings_plan"
	CommitmentTypeReservedInstance     = "reserved_instance"
	CommitmentTypeCommittedUseDiscount = "committed_use_discount"
)

// Commitment is a discount bought in exchange for committed usage or spend — an
// AWS Savings Plan or Reserved Instance, an Azure reservation, a GCP committed
// use discount. Nothing provider-specific belongs on it; that goes in
// CommitmentResource.Metadata.
type Commitment struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// AccountID is the provider account that owns the commitment, which is not
	// always the account that reported it: an organization's payer account can
	// see the commitments its member accounts bought.
	AccountID string               `json:"account_id"`
	Type      string               `json:"type"`
	Resources []CommitmentResource `json:"resources"`
	StartTime time.Time            `json:"start_time"`
	EndTime   time.Time            `json:"end_time"`
	Status    string               `json:"status"`
}

// CommitmentResource is one thing a commitment covers. A commitment can cover
// several at once — a GCP resource-based commitment covers vCPU and memory
// together — hence a list on Commitment.
type CommitmentResource struct {
	Type         string         `json:"type"`
	Quantity     float64        `json:"quantity"`
	Unit         string         `json:"unit"`
	Service      string         `json:"service"`
	Region       string         `json:"region"`
	ResourceType string         `json:"resource_type"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// Coverage is how much eligible usage was covered by commitments over a window.
// It is reported per group (account, service, region), not per commitment.
type Coverage struct {
	Provider       string    `json:"provider"`
	CommitmentType string    `json:"commitment_type"`
	AccountID      string    `json:"account_id"`
	Service        string    `json:"service"`
	Region         string    `json:"region"`
	CoveragePct    float64   `json:"coverage_pct"`
	CoveredUsage   float64   `json:"covered_usage"`
	TotalUsage     float64   `json:"total_usage"`
	UsageUnit      string    `json:"usage_unit"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
}

// Utilization is how much of what was committed was actually consumed over a
// window.
type Utilization struct {
	Provider string `json:"provider"`
	// CommitmentID ties the figure to one commitment. Empty when the figure is an
	// aggregate for the commitment type.
	CommitmentID   string  `json:"commitment_id"`
	CommitmentType string  `json:"commitment_type"`
	UtilizationPct float64 `json:"utilization_pct"`
	CommittedUsage float64 `json:"committed_usage"`
	UsedUsage      float64 `json:"used_usage"`
	UnusedUsage    float64 `json:"unused_usage"`
	// UsageUnit says what the three usage figures measure: a currency code for
	// spend-based commitments, a unit such as HOURS or VCPU for resource-based
	// ones.
	UsageUnit string `json:"usage_unit"`
	// UnusedCost is the waste as money, and is only meaningful when Currency is
	// set. It is filled in only from a monetary figure the provider itself
	// returned — never derived from an assumed price.
	UnusedCost float64   `json:"unused_cost"`
	Currency   string    `json:"currency"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
}

const (
	// A commitment used below commitmentUtilizationMediumPct is flagged, and
	// below commitmentUtilizationHighPct it is flagged as High.
	commitmentUtilizationMediumPct = 80.0
	commitmentUtilizationHighPct   = 50.0

	// A commitment is flagged once it has commitmentExpiryWarningDays or fewer
	// left, and as Critical with commitmentExpiryCriticalDays or fewer.
	commitmentExpiryWarningDays  = 30
	commitmentExpiryCriticalDays = 7
)

const (
	// Coverage below commitmentCoverageMediumPct is flagged, and below
	// commitmentCoverageHighPct it is flagged as High.
	commitmentCoverageMediumPct = 70.0
	commitmentCoverageHighPct   = 40.0

	// commitmentCoverageMinUncoveredUsage is the uncovered usage, in the
	// coverage's own unit, below which low coverage is not worth raising: a few
	// dollars left on demand are not worth another commitment.
	commitmentCoverageMinUncoveredUsage = 100.0
)

// CommitmentRecommendations applies the commitment rules to one account's
// commitments and their utilization, as of now. The rules are provider-neutral:
// each provider only has to produce the Commitment and Utilization values.
//
// A commitment yields at most one recommendation per rule, identified by the
// commitment's own ID so the same finding lands on the same row on every sync.
// Commitments that have already ended yield nothing: providers keep reporting
// utilization for a commitment for as long as it falls inside the lookback
// window, which would otherwise flag an expired commitment as underused for
// weeks after it stopped existing.
func CommitmentRecommendations(commitments []Commitment, utilizations []Utilization, now time.Time) []Recommendation {
	utilizationByCommitment := map[string]Utilization{}
	for _, u := range utilizations {
		if u.CommitmentID != "" {
			utilizationByCommitment[u.CommitmentID] = u
		}
	}

	recommendations := []Recommendation{}
	for _, c := range commitments {
		if c.ID == "" || (!c.EndTime.IsZero() && !c.EndTime.After(now)) {
			continue
		}
		utilization, hasUtilization := utilizationByCommitment[c.ID]
		// A commitment with nothing committed in the window has no utilization to
		// judge — it reads as 0% used, not as a finding.
		hasUtilization = hasUtilization && utilization.CommittedUsage > 0

		if hasUtilization && utilization.UtilizationPct < commitmentUtilizationMediumPct {
			severity := RecommendationSeverityMedium
			if utilization.UtilizationPct < commitmentUtilizationHighPct {
				severity = RecommendationSeverityHigh
			}
			data := commitmentData(c)
			addUtilizationData(data, utilization)
			recommendations = append(recommendations, commitmentRecommendation(c, constants.UnderutilizedCommitment, severity, data))
		}

		if c.EndTime.IsZero() {
			continue
		}
		daysRemaining := int(c.EndTime.Sub(now).Hours() / 24)
		if daysRemaining <= commitmentExpiryWarningDays {
			severity := RecommendationSeverityHigh
			if daysRemaining <= commitmentExpiryCriticalDays {
				severity = RecommendationSeverityCritical
			}
			data := commitmentData(c)
			data["days_remaining"] = daysRemaining
			// How well the commitment was used is what sizes its renewal.
			if hasUtilization {
				addUtilizationData(data, utilization)
			}
			recommendations = append(recommendations, commitmentRecommendation(c, constants.CommitmentExpiringSoon, severity, data))
		}
	}
	return recommendations
}

// commitmentData is the part of a recommendation's payload that says which
// commitment it is about. An id alone is a UUID for most commitments, so the
// payload also carries what the commitment covers and when it ends: whoever
// reads the row should not need a second lookup to recognise it.
func commitmentData(c Commitment) map[string]any {
	data := map[string]any{
		"commitment_id":   c.ID,
		"commitment_type": c.Type,
	}
	if !c.EndTime.IsZero() {
		data["expiry_date"] = c.EndTime.UTC().Format(time.RFC3339)
	}
	if len(c.Resources) == 0 {
		return data
	}

	// What the commitment covers is stated once at the top level, from its first
	// resource, and in full under "resources" when it covers more than one.
	covered := func(r CommitmentResource) map[string]any {
		entry := map[string]any{"committed_quantity": r.Quantity, "committed_unit": r.Unit}
		for key, value := range map[string]string{"service": r.Service, "region": r.Region, "resource_type": r.ResourceType} {
			if value != "" {
				entry[key] = value
			}
		}
		return entry
	}
	for key, value := range covered(c.Resources[0]) {
		data[key] = value
	}
	if len(c.Resources[0].Metadata) > 0 {
		data["details"] = c.Resources[0].Metadata
	}
	if len(c.Resources) > 1 {
		resources := make([]map[string]any, 0, len(c.Resources))
		for _, r := range c.Resources {
			resources = append(resources, covered(r))
		}
		data["resources"] = resources
	}
	return data
}

func addUtilizationData(data map[string]any, u Utilization) {
	data["utilization_percentage"] = u.UtilizationPct
	data["total_commitment"] = u.CommittedUsage
	data["used_commitment"] = u.UsedUsage
	data["unused_commitment"] = u.UnusedUsage
	data["usage_unit"] = u.UsageUnit
	data["window_days"] = int(u.EndTime.Sub(u.StartTime).Hours() / 24)
	if u.Currency != "" {
		data["unused_cost"] = u.UnusedCost
		data["currency"] = u.Currency
	}
}

// commitmentRecommendation builds the account-level row for one rule on one
// commitment. It is filed under RightSizing, the category the recommendations
// to buy a commitment already use. Savings stays zero: an unused commitment is money already spent,
// not a saving that acting on the recommendation recovers, so the waste is
// carried in the payload instead. DedupeGroup stays empty because the
// aws_commitment groups hold alternative ways to buy one commitment, of which
// only the best counts — a finding about an existing commitment is not one of
// those alternatives.
func commitmentRecommendation(c Commitment, ruleName string, severity RecommendationSeverity, data map[string]any) Recommendation {
	return Recommendation{
		CategoryName:        RecommendationCategoryRightSizing,
		RuleName:            ruleName,
		Severity:            severity,
		Action:              RecommendationActionModify,
		Data:                data,
		ResourceServiceName: CommitmentServiceName,
		ResourceId:          c.ID,
		ResourceType:        strings.ReplaceAll(c.Type, "_", "-"),
		ResourceRegion:      "global",
	}
}

// totalCoverage adds coverage rows up per commitment type and unit, in the order
// each first appears. The percentage is recomputed from the summed usage: an
// average of per-service percentages would weigh a $5 service like a $5,000 one.
func totalCoverage(coverages []Coverage) []Coverage {
	totals := []Coverage{}
	indexByKey := map[string]int{}
	for _, c := range coverages {
		key := c.CommitmentType + "|" + c.UsageUnit
		index, seen := indexByKey[key]
		if !seen {
			index = len(totals)
			indexByKey[key] = index
			totals = append(totals, Coverage{
				Provider:       c.Provider,
				CommitmentType: c.CommitmentType,
				AccountID:      c.AccountID,
				UsageUnit:      c.UsageUnit,
				StartTime:      c.StartTime,
				EndTime:        c.EndTime,
			})
		}
		totals[index].CoveredUsage += c.CoveredUsage
		totals[index].TotalUsage += c.TotalUsage
	}
	for i := range totals {
		if totals[i].TotalUsage > 0 {
			totals[i].CoveragePct = totals[i].CoveredUsage / totals[i].TotalUsage * 100
		}
	}
	return totals
}

// CoverageRecommendations raises one account-level finding per commitment type
// whose eligible usage is poorly covered, as of the coverage rows' own window.
// The row is identified by the commitment type, so it is the same row on every
// sync, and carries the per-service breakdown showing where the uncovered usage
// is.
//
// It is raised only for a commitment type the account already owns at least one
// commitment of. An account that owns none is 0% covered by definition, and is
// already told to buy one, with the saving, by the provider's own purchase
// recommendations; a second row saying the same with no saving is noise. For an
// account that has committed, low coverage says its commitments have fallen
// behind its usage.
func CoverageRecommendations(coverages []Coverage, commitments []Commitment) []Recommendation {
	ownedTypes := map[string]bool{}
	for _, c := range commitments {
		ownedTypes[c.Type] = true
	}

	recommendations := []Recommendation{}
	for _, total := range totalCoverage(coverages) {
		if !ownedTypes[total.CommitmentType] {
			continue
		}
		uncovered := total.TotalUsage - total.CoveredUsage
		if total.TotalUsage <= 0 || total.CoveragePct >= commitmentCoverageMediumPct || uncovered < commitmentCoverageMinUncoveredUsage {
			continue
		}
		severity := RecommendationSeverityMedium
		if total.CoveragePct < commitmentCoverageHighPct {
			severity = RecommendationSeverityHigh
		}

		byService := []map[string]any{}
		for _, c := range coverages {
			if c.CommitmentType != total.CommitmentType || c.UsageUnit != total.UsageUnit {
				continue
			}
			byService = append(byService, map[string]any{
				"service":             c.Service,
				"coverage_percentage": c.CoveragePct,
				"covered_usage":       c.CoveredUsage,
				"total_usage":         c.TotalUsage,
				"uncovered_usage":     c.TotalUsage - c.CoveredUsage,
			})
		}
		sort.SliceStable(byService, func(i, j int) bool {
			return byService[i]["uncovered_usage"].(float64) > byService[j]["uncovered_usage"].(float64)
		})

		recommendations = append(recommendations, Recommendation{
			CategoryName: RecommendationCategoryRightSizing,
			RuleName:     constants.LowCommitmentCoverage,
			Severity:     severity,
			Action:       RecommendationActionModify,
			Data: map[string]any{
				"commitment_type":     total.CommitmentType,
				"coverage_percentage": total.CoveragePct,
				"covered_usage":       total.CoveredUsage,
				"total_usage":         total.TotalUsage,
				"uncovered_usage":     uncovered,
				"usage_unit":          total.UsageUnit,
				"window_days":         int(total.EndTime.Sub(total.StartTime).Hours() / 24),
				"by_service":          byService,
			},
			ResourceServiceName: CommitmentServiceName,
			ResourceId:          total.CommitmentType,
			ResourceType:        "commitment-coverage",
			ResourceRegion:      "global",
		})
	}
	return recommendations
}
