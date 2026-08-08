package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

const (
	// digestJobName is the scheduler's handle for the weekly digest generator.
	digestJobName = "event_analysis_digest"

	// digestMaxClasses bounds the map stage: one LLM call per failure class, so
	// the tail of one-off classes is dropped rather than summarised. Dropped
	// classes still appear in top_classes counts — only their prose is skipped.
	digestMaxClasses = 12

	// digestRollupClasses is how many classes the counters cover. Larger than the
	// map stage because the rollup is one query regardless of size, and the
	// tenant-level merge re-ranks across accounts — so a class that misses one
	// account's top 12 can still matter tenant-wide.
	digestRollupClasses = 20

	// digestMaxAnalysesPerClass bounds how many analyses feed one class summary.
	// The bodies repeat heavily within a class, so the newest few carry the same
	// findings as all of them at a fraction of the tokens.
	digestMaxAnalysesPerClass = 6

	// digestMaxCharsPerAnalysis truncates a single analysis body. log_analysis
	// averages ~5.5k chars; the tail is usually raw log spool rather than finding.
	digestMaxCharsPerAnalysis = 6000

	// digestGenerationTimeout bounds one account-week: ~13 LLM calls worst case.
	digestGenerationTimeout = 10 * time.Minute
)

// RegisterEventAnalysisDigestJob schedules the weekly digest generator.
//
// Runs every 6 hours rather than weekly on purpose. gocron holds no state across
// restarts and never replays a missed tick, so a weekly schedule would turn one
// unlucky pod restart into a permanently missing week. The job is convergent —
// it fills whichever (account, week) slots have no row — so a frequent tick
// makes the recovery window hours instead of a week, and a tick with nothing to
// do costs one indexed query.
func RegisterEventAnalysisDigestJob() error {
	if err := common.NewLeaderCronJob(digestJobName, generateMissingDigests, "0 */6 * * *"); err != nil {
		return fmt.Errorf("RegisterEventAnalysisDigestJob: %w", err)
	}
	return nil
}

// generateMissingDigests fills every pending (account, week) digest slot.
//
// One account's failure does not abort the others: the slot is written with
// status=failed and the error text, and the next tick picks it up again because
// the gap scan treats failed rows as pending.
func generateMissingDigests() error {
	scanCtx, cancelScan := newDigestContext("")
	defer cancelScan()

	periods, err := events.FindPendingDigestPeriods(scanCtx)
	if err != nil {
		return fmt.Errorf("generateMissingDigests: finding pending periods: %w", err)
	}
	if len(periods) == 0 {
		return nil
	}

	scanCtx.GetLogger().Info("digest: generating", "pending", len(periods))

	var failures int
	for _, p := range periods {
		// One context per period: each carries its own tenant for pricing and
		// prompt resolution, and its own timeout so a stuck account cannot hold
		// the whole run open. Wrapped in a closure so cancel is deferred — a
		// panic mid-generation would otherwise leak the context's timer for the
		// rest of the run.
		genErr := func(p events.DigestPeriod) error {
			ctx, cancel := newDigestContext(p.TenantID)
			defer cancel()
			return generateDigestForPeriod(ctx, p, events.DigestSourceScheduled)
		}(p)
		if genErr != nil {
			failures++
			scanCtx.GetLogger().Error("digest: generation failed",
				"error", genErr, "account_id", p.CloudAccountID, "period_start", p.PeriodStart)
		}
	}

	scanCtx.GetLogger().Info("digest: run complete", "generated", len(periods)-failures, "failed", failures)
	return nil
}

// newDigestContext builds the background request context the generator runs
// under. Mirrors the memory-maintenance jobs: tenant-admin security context,
// summary model tier, global cache scope, and a hard timeout so a hung LLM call
// surfaces rather than pinning the leader's job slot.
func newDigestContext(tenantID string) (*security.RequestContext, context.CancelFunc) {
	// The gap scan runs before any tenant is known and only touches the DB, so it
	// gets no security context — building a tenant-admin one for an empty tenant
	// makes the account lookup fail and log an error for a non-problem.
	var sc *security.SecurityContext
	if tenantID != "" {
		sc = security.NewSecurityContextForTenantAdmin(tenantID)
	}
	bg, cancel := context.WithTimeout(context.Background(), digestGenerationTimeout)
	rc := security.NewRequestContext(
		context.WithValue(
			context.WithValue(bg, core.ContextKeyModelTier, core.ModelTierSummary),
			core.ContextKeyCacheScope, core.CacheScopeGlobal),
		sc, slog.Default(), nil, nil,
	)
	return rc, cancel
}

// errClassHasNoText marks a class the map stage cannot reduce because none of
// its analyses carry prose. Distinct from a real failure: an analysis row can be
// COMPLETED with empty analysis AND summary, which is an empty week rather than
// something a retry would fix.
var errClassHasNoText = errors.New("class has no analysis text")

// generateDigestForPeriod builds and stores one account-week digest.
//
// Map: one LLM call per failure class, reducing that class's analyses to a
// single finding. Reduce: one call over those findings plus the counters.
// The class findings are stored alongside the briefing so a partial digest still
// renders its per-class detail, and so a reviewer can audit why a class was
// judged synthetic. A retry re-runs the map stage rather than reusing them —
// reuse would need the stored findings to be revalidated against the week's
// current analyses, which is not worth the complexity at ~$0.02 a run.
func generateDigestForPeriod(ctx *security.RequestContext, p events.DigestPeriod, source string) error {
	metrics, err := events.GetDigestMetrics(ctx, p)
	if err != nil {
		return storeDigestFailure(ctx, p, fmt.Errorf("metrics: %w", err), source)
	}

	classes, err := events.GetDigestClasses(ctx, p, digestRollupClasses)
	if err != nil {
		return storeDigestFailure(ctx, p, fmt.Errorf("classes: %w", err), source)
	}
	if len(classes) == 0 {
		// A period with analyses but no resolvable classes is a real, empty week
		// rather than a failure — store it so the gap scan stops re-selecting it.
		return events.UpsertDigest(ctx, p, metrics, classes, []classFinding{},
			"No event analyses were recorded for this period.", events.DigestStatusGenerated, "", source)
	}

	if learnings, lerr := events.CountLearningsCaptured(ctx, p); lerr != nil {
		ctx.GetLogger().Warn("digest: learnings count failed", "error", lerr, "account_id", p.CloudAccountID)
	} else {
		metrics.Learnings = learnings
	}

	// Summarised classes are the head of the rollup: `classes` (up to 20) is what
	// gets stored as counters, `summarised` (up to 12) is what costs an LLM call.
	summarised := classes
	if len(summarised) > digestMaxClasses {
		summarised = summarised[:digestMaxClasses]
	}

	findings := make([]classFinding, 0, len(summarised))
	var textless int
	for _, c := range summarised {
		// Stop rather than grind through the rest: once the context is done every
		// remaining LLM call fails immediately, producing a run of useless warnings
		// and no findings.
		if cerr := ctx.GetContext().Err(); cerr != nil {
			return storeDigestFailure(ctx, p, fmt.Errorf("cancelled after %d/%d classes: %w",
				len(findings), len(summarised), cerr), source)
		}
		finding, ferr := summariseClass(ctx, p, c)
		if ferr != nil {
			// A dead context is not a per-class problem: every remaining call will
			// fail the same way, so stop here rather than logging a warning per
			// class. The pre-loop check catches it between iterations; this catches
			// a cancellation that lands mid-call.
			if errors.Is(ferr, context.Canceled) || errors.Is(ferr, context.DeadlineExceeded) {
				return storeDigestFailure(ctx, p, fmt.Errorf("cancelled after %d/%d classes: %w",
					len(findings), len(summarised), ferr), source)
			}
			// A class that fails to summarise is dropped from the prose, not fatal
			// — the briefing is still worth producing from the classes that worked.
			if errors.Is(ferr, errClassHasNoText) {
				textless++
			}
			ctx.GetLogger().Warn("digest: class summary skipped",
				"error", ferr, "account_id", p.CloudAccountID, "class", c.AggregationKey)
			continue
		}
		findings = append(findings, finding)
	}

	if len(findings) == 0 {
		// Every class was empty of prose: the analyses ran but produced nothing to
		// summarise. Store the counters as a generated (if thin) digest — marking
		// it failed would put it back in the queue on every tick forever, and no
		// retry can conjure text that was never written.
		if textless == len(summarised) {
			return events.UpsertDigest(ctx, p, metrics, classes, []classFinding{},
				"Event analyses ran this period but recorded no findings text, so there is "+
					"nothing to summarise. The counters and failure classes below still apply.",
				events.DigestStatusGenerated, "", source)
		}
		return storeDigestFailure(ctx, p, fmt.Errorf("every class summary failed for %d classes", len(summarised)), source)
	}

	briefing, err := synthesiseBriefing(ctx, p, metrics, findings)
	if err != nil {
		// Map stage succeeded, reduce failed: keep the findings so the week still
		// renders its counters and class table, and mark partial — the gap scan
		// re-queues partial rows, so the next tick retries the synthesis.
		// Same reasoning as storeDigestFailure: a synthesis that failed on a dead
		// context would lose the class findings it already paid for.
		writeCtx, cancel := writeContext(ctx)
		defer cancel()
		if upErr := events.UpsertDigest(writeCtx, p, metrics, classes, findings, "",
			events.DigestStatusPartial, err.Error(), source); upErr != nil {
			// Both matter: upErr says the record was lost, err says why the run
			// failed in the first place. %w on err keeps errors.Is working for the
			// context sentinels the caller checks.
			return fmt.Errorf("generateDigestForPeriod: storing partial failed (%v) after synthesis: %w", upErr, err)
		}
		return fmt.Errorf("generateDigestForPeriod: synthesis: %w", err)
	}

	return events.UpsertDigest(ctx, p, metrics, classes, findings, briefing,
		events.DigestStatusGenerated, "", source)
}

// classFinding is the map stage's output for one failure class: the counters
// carried through verbatim, plus the LLM's reduction of that class's analyses.
type classFinding struct {
	AggregationKey  string `json:"aggregation_key"`
	Events          int    `json:"events"`
	NewIncidents    int    `json:"new_incidents"`
	WorstRecurrence int    `json:"worst_recurrence"`
	Services        int    `json:"services"`
	Priority        string `json:"priority"`
	Owner           string `json:"owner,omitempty"`
	Finding         string `json:"finding"`
	// Synthetic is the map stage's verdict that this class is test/rule-exercising
	// traffic rather than real failures. Nothing in the event data marks such a
	// burst — triage never tagged it — so the judgement is the only thing that
	// keeps it out of the top-patterns list and inside the noise figure.
	Synthetic bool `json:"synthetic,omitempty"`
}

// syntheticVerdict reads the map stage's `Synthetic: yes|no` line. Absent or
// unparseable means not synthetic: the prompt is told to default to `no`, and a
// missed line must never silently hide a real failure class.
// syntheticVerdictRe matches the map stage's verdict line after markup is
// stripped. The leading class absorbs any list marker the model chooses —
// "-", "+", "*" or "1." — which a fixed TrimLeft set kept missing. The trailing
// The tail matters as much as the head: the verdict must be the whole token.
// A bare \b is not enough — a hyphen satisfies it, so "Synthetic: yes-associated
// failures" would match "yes" and hide a real class. One optional punctuation
// mark is allowed through, since "Synthetic: yes." is a form the model uses.
var syntheticVerdictRe = regexp.MustCompile(`^[-+0-9.#\s]*synthetic\s*:\s*(yes|no)[.,;:?!]?(?:\s|$)`)

// syntheticVerdict reads the map stage's `Synthetic: yes|no` line. Absent or
// unparseable means not synthetic: the prompt is told to default to `no`, and a
// missed line must never silently hide a real failure class.
//
// Emphasis, code ticks and quotes are stripped before matching: the model emits
// the verdict as "- **Synthetic**: yes" but also as "`yes`" and "\"yes\"", and any
// of those wrappers would otherwise fail the match and read as "no" — silently
// treating a test burst as real traffic.
func syntheticVerdict(finding string) bool {
	stripMarkup := strings.NewReplacer("*", "", "_", "", "`", "", `"`, "", "'", "")
	for _, line := range strings.Split(finding, "\n") {
		l := strings.TrimSpace(stripMarkup.Replace(strings.ToLower(line)))
		if m := syntheticVerdictRe.FindStringSubmatch(l); m != nil {
			return m[1] == "yes"
		}
	}
	return false
}

// summariseClass reduces one failure class's analyses into a single finding.
func summariseClass(ctx *security.RequestContext, p events.DigestPeriod, c events.DigestClass) (classFinding, error) {
	finding := classFinding{
		AggregationKey:  c.AggregationKey,
		Events:          c.Events,
		NewIncidents:    c.NewIncidents,
		WorstRecurrence: c.WorstRecurrence,
		Services:        c.Services,
		Priority:        c.Priority,
		Owner:           c.Owner,
	}

	bodies, err := events.GetClassAnalysisText(ctx, p, c.AggregationKey, digestMaxAnalysesPerClass)
	if err != nil {
		return finding, fmt.Errorf("summariseClass: loading analyses: %w", err)
	}
	if len(bodies) == 0 {
		return finding, fmt.Errorf("summariseClass: class %q: %w", c.AggregationKey, errClassHasNoText)
	}

	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptEventDigestClassSummary, p.CloudAccountID)
	if err != nil {
		return finding, fmt.Errorf("summariseClass: loading prompt: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Failure Class\n%s\n\n", c.AggregationKey)
	fmt.Fprintf(&sb, "Occurred %d times this period across %d services (priority %s).\n",
		c.Events, c.Services, c.Priority)
	if c.WorstRecurrence > 1 {
		fmt.Fprintf(&sb, "Worst recurrence count for a single fingerprint: %d.\n", c.WorstRecurrence)
	}
	if c.Title != "" {
		fmt.Fprintf(&sb, "Event title: %s\n", c.Title)
	}
	if c.Source != "" {
		fmt.Fprintf(&sb, "Event source: %s\n", c.Source)
	}
	fmt.Fprintf(&sb, "Traffic shape: active on %d distinct day(s), spanning %.1f hours end to end.\n",
		c.ActiveDays, c.SpanHours)
	for i, body := range bodies {
		fmt.Fprintf(&sb, "\n## Analysis %d\n%s\n", i+1, core.TruncateMiddle(body, digestMaxCharsPerAnalysis/2, digestMaxCharsPerAnalysis/2))
	}

	completion, err := core.GenerateAndTrackLLMContent(
		ctx, "", p.CloudAccountID, "", "", "event_digest_class_summary", false,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, sb.String()),
		}, false,
		llms.WithTemperature(0.0),
	)
	if err != nil {
		return finding, fmt.Errorf("summariseClass: LLM call: %w", err)
	}
	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return finding, fmt.Errorf("summariseClass: empty response for class %q", c.AggregationKey)
	}

	finding.Finding = completion.Choices[0].Content
	// The verdict may only confirm a burst, never hide a recurring class. The
	// model has flagged genuine failures as synthetic (a class active 13 days
	// running), and a false positive here removes a real incident from the
	// briefing entirely — so traffic spanning more than a single day overrides it.
	finding.Synthetic = syntheticVerdict(finding.Finding) && c.ActiveDays <= 1
	return finding, nil
}

// synthesiseBriefing turns the counters and per-class findings into the briefing.
func synthesiseBriefing(
	ctx *security.RequestContext,
	p events.DigestPeriod,
	metrics events.DigestMetrics,
	findings []classFinding,
) (string, error) {
	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptEventDigestBriefing, p.CloudAccountID)
	if err != nil {
		return "", fmt.Errorf("synthesiseBriefing: loading prompt: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Period\n%s to %s\n\n",
		p.PeriodStart.Format("Jan 2, 2006"), p.PeriodEnd.Format("Jan 2, 2006"))
	fmt.Fprintf(&sb, "## Telemetry Counters\n"+
		"- Analyses completed: %d (failed: %d)\n"+
		"- Events analysed: %d across %d services\n"+
		"- Distinct failure classes: %d\n"+
		"- New: %d | Recurring: %d\n"+
		"- P1 (HIGH/CRITICAL) share: %d%%\n"+
		"- Alert noise (duplicate/false-positive) share: %d%%\n"+
		"- Learnings captured into the context layer: %d\n\n",
		metrics.Analyses, metrics.Failed, metrics.EventsAnalysed, metrics.Services,
		metrics.FailureClasses, metrics.NewIncidents, metrics.Recurring,
		metrics.P1Pct, metrics.NoisePct, metrics.Learnings)

	var syntheticEvents int
	for _, f := range findings {
		if f.Synthetic {
			syntheticEvents += f.Events
		}
	}
	if syntheticEvents > 0 {
		fmt.Fprintf(&sb, "Synthetic/test traffic detected this period: %d events across the classes marked "+
			"SYNTHETIC below. Treat them as alert noise, keep them out of the failure patterns, and raise a "+
			"guardrail to tag them at source.\n\n", syntheticEvents)
	}

	sb.WriteString("## Failure Class Findings\n")
	for _, f := range findings {
		owner := f.Owner
		if owner == "" {
			owner = "Unassigned"
		}
		marker := ""
		if f.Synthetic {
			marker = " [SYNTHETIC — exclude from failure patterns]"
		}
		fmt.Fprintf(&sb, "\n### %s%s\nEvents: %d | New: %d | Worst recurrence: %d | Services: %d | Priority: %s | Owner: %s\n\n%s\n",
			f.AggregationKey, marker, f.Events, f.NewIncidents, f.WorstRecurrence,
			f.Services, f.Priority, owner, f.Finding)
	}

	completion, err := core.GenerateAndTrackLLMContent(
		ctx, "", p.CloudAccountID, "", "", "event_digest_briefing", false,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, sb.String()),
		}, false,
		llms.WithTemperature(0.0),
	)
	if err != nil {
		return "", fmt.Errorf("synthesiseBriefing: LLM call: %w", err)
	}
	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return "", fmt.Errorf("synthesiseBriefing: empty response")
	}
	return completion.Choices[0].Content, nil
}

// digestWriteTimeout bounds the detached write used to persist a failed or
// partial outcome. Short on purpose: it is a single upsert, and the run that
// produced it has already given up.
const digestWriteTimeout = 15 * time.Second

// writeContext returns a context for persisting an outcome, detached from ctx's
// cancellation.
//
// The failure paths are reached *because* the context died, and the DAO executes
// through ctx — so writing the failure with the same context fails immediately
// and the outcome is lost to the logs. WithoutCancel keeps the request's values
// (tenant, trace, logger) while dropping the cancellation.
func writeContext(ctx *security.RequestContext) (*security.RequestContext, context.CancelFunc) {
	detached, cancel := context.WithTimeout(
		context.WithoutCancel(ctx.GetContext()), digestWriteTimeout)
	return security.NewRequestContext(detached, ctx.GetSecurityContext(),
		ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter()), cancel
}

// storeDigestFailure records a failed attempt so the outcome is visible in the
// data rather than only in logs, and returns the original error.
func storeDigestFailure(ctx *security.RequestContext, p events.DigestPeriod, cause error, source string) error {
	writeCtx, cancel := writeContext(ctx)
	defer cancel()
	if err := events.UpsertDigest(writeCtx, p, events.DigestMetrics{}, nil, nil, "",
		events.DigestStatusFailed, cause.Error(), source); err != nil {
		return fmt.Errorf("storeDigestFailure: %w (original: %v)", err, cause)
	}
	return cause
}
