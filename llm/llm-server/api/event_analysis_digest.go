package api

import (
	"context"
	"encoding/json"
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
		// Still sweep for undelivered digests: a week can be generated on one
		// tick and fail to publish on the same tick (exchange down, pod
		// restart), which leaves nothing to generate but something to send.
		deliverGeneratedDigests()
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
				"error", genErr, "tenant_id", p.TenantID, "period_start", p.PeriodStart)
		}
	}

	scanCtx.GetLogger().Info("digest: run complete", "generated", len(periods)-failures, "failed", failures)

	// Delivery runs after every generation pass, over whatever is complete and
	// unsent — including weeks generated on an earlier tick. Separate from
	// generation so a channel outage costs a delayed message, not a lost digest.
	deliverGeneratedDigests()
	return nil
}

// deliverGeneratedDigests runs the delivery sweep under its own context.
//
// It must not reuse the generation scan's context: that one carries a
// digestGenerationTimeout deadline set before the LLM work started, and a full
// run over a busy tenant routinely outlives it — the sweep would then fail with
// "context deadline exceeded" on every tick that actually had digests to send.
func deliverGeneratedDigests() {
	ctx, cancel := newDigestContext("")
	defer cancel()
	notifyGeneratedDigests(ctx)
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

	// Every real class, unbounded. A cap ranked by volume silently dropped whole
	// accounts from the review — the busiest account's classes filled the cut and
	// the quieter ones vanished, incidents and all. At ~7s and ~$0.002 a class
	// there is nothing to save by curating here; the reduce stage is what decides
	// what leads the review.
	classes, err := events.GetDigestClasses(ctx, p)
	if err != nil {
		return storeDigestFailure(ctx, p, fmt.Errorf("classes: %w", err), source)
	}
	if len(classes) == 0 {
		// A period with analyses but no resolvable classes is a real, empty week
		// rather than a failure — store it so the gap scan stops re-selecting it.
		// An empty-but-present briefing, not nil: the gap scan treats a generated
		// row with a NULL briefing as one still owing a structured reduce, so nil
		// here would re-queue every empty week on every tick, forever.
		return events.UpsertDigest(ctx, p, metrics, classes, []classFinding{}, digestBriefing{},
			"No event analyses were recorded for this period.", events.DigestStatusGenerated, "", source)
	}

	if learnings, lerr := events.CountLearningsCaptured(ctx, p); lerr != nil {
		ctx.GetLogger().Warn("digest: learnings count failed", "error", lerr, "tenant_id", p.TenantID)
	} else {
		metrics.Learnings = learnings
	}

	findings := make([]classFinding, 0, len(classes))
	var textless int
	for _, c := range classes {
		// Stop rather than grind through the rest: once the context is done every
		// remaining LLM call fails immediately, producing a run of useless warnings
		// and no findings.
		if cerr := ctx.GetContext().Err(); cerr != nil {
			return storeDigestFailure(ctx, p, fmt.Errorf("cancelled after %d/%d classes: %w",
				len(findings), len(classes), cerr), source)
		}
		finding, ferr := summariseClass(ctx, p, c)
		if ferr != nil {
			// A dead context is not a per-class problem: every remaining call will
			// fail the same way, so stop here rather than logging a warning per
			// class. The pre-loop check catches it between iterations; this catches
			// a cancellation that lands mid-call.
			if errors.Is(ferr, context.Canceled) || errors.Is(ferr, context.DeadlineExceeded) {
				return storeDigestFailure(ctx, p, fmt.Errorf("cancelled after %d/%d classes: %w",
					len(findings), len(classes), ferr), source)
			}
			// A class that fails to summarise is dropped from the prose, not fatal
			// — the briefing is still worth producing from the classes that worked.
			if errors.Is(ferr, errClassHasNoText) {
				textless++
			}
			ctx.GetLogger().Warn("digest: class summary skipped",
				"error", ferr, "account_id", c.AccountID, "class", c.AggregationKey)
			continue
		}
		findings = append(findings, finding)
	}

	if len(findings) == 0 {
		// Every class was empty of prose: the analyses ran but produced nothing to
		// summarise. Store the counters as a generated (if thin) digest — marking
		// it failed would put it back in the queue on every tick forever, and no
		// retry can conjure text that was never written.
		if textless == len(classes) {
			return events.UpsertDigest(ctx, p, metrics, classes, []classFinding{}, digestBriefing{},
				"Event analyses ran this period but recorded no findings text, so there is "+
					"nothing to summarise. The counters and failure classes below still apply.",
				events.DigestStatusGenerated, "", source)
		}
		return storeDigestFailure(ctx, p, fmt.Errorf("every class summary failed for %d classes", len(classes)), source)
	}

	// Carry-over is what makes the digest stateful. Without it the briefing prompt
	// cannot honour its own ranking rule ("a class recurring for the fourth week
	// outranks a higher-volume INFO class") and silently falls back to volume.
	// A failure here is not fatal: the week still reads correctly, just without
	// history, which beats losing the whole digest.
	var prior []events.PriorClass
	var presentKeys []events.ClassRef
	prior, perr := events.GetPriorClasses(ctx, p, events.DigestLookbackWeeks)
	if perr != nil {
		ctx.GetLogger().Warn("digest: prior classes unavailable",
			"error", perr, "tenant_id", p.TenantID)
	} else if presentKeys, perr = events.GetPeriodClassKeys(ctx, p); perr != nil {
		// Without the full class list, "stopped recurring" cannot be decided
		// safely, so drop the history rather than claim a pattern was fixed.
		ctx.GetLogger().Warn("digest: period class keys unavailable",
			"error", perr, "tenant_id", p.TenantID)
		prior = nil
	}
	resolved := annotateCarryOver(findings, prior, presentKeys)

	briefing, err := synthesiseBriefing(ctx, p, metrics, findings, resolved)
	if err != nil {
		// Map stage succeeded, reduce failed: keep the findings so the week still
		// renders its counters and class table, and mark partial — the gap scan
		// re-queues partial rows, so the next tick retries the synthesis.
		// Same reasoning as storeDigestFailure: a synthesis that failed on a dead
		// context would lose the class findings it already paid for.
		writeCtx, cancel := writeContext(ctx)
		defer cancel()
		if upErr := events.UpsertDigest(writeCtx, p, metrics, classes, findings, nil, "",
			events.DigestStatusPartial, err.Error(), source); upErr != nil {
			// Both matter: upErr says the record was lost, err says why the run
			// failed in the first place. %w on err keeps errors.Is working for the
			// context sentinels the caller checks.
			return fmt.Errorf("generateDigestForPeriod: storing partial failed (%v) after synthesis: %w", upErr, err)
		}
		return fmt.Errorf("generateDigestForPeriod: synthesis: %w", err)
	}

	return events.UpsertDigest(ctx, p, metrics, classes, findings, briefing, briefing.WhatBrokeLede,
		events.DigestStatusGenerated, "", source)
}

// classFinding is the map stage's output for one failure class: the counters
// carried through verbatim, plus the LLM's reduction of that class's analyses.
//
// There is no synthetic field. Deciding noise was previously the map stage's job,
// which cost us a real production incident — a RabbitMQ queue backlog was judged
// test traffic and dropped from the briefing. That decision is now made in SQL
// from the aggregation key alone, before a class ever reaches the map stage.
type classFinding struct {
	AggregationKey string `json:"aggregation_key"`
	// Account attribution: at tenant scope the same aggregation key can appear in
	// several accounts as unrelated incidents, so a finding is only identified by
	// the pair.
	AccountID       string `json:"cloud_account_id"`
	AccountName     string `json:"account_name"`
	Events          int    `json:"events"`
	NewIncidents    int    `json:"new_incidents"`
	Recurrences     int    `json:"recurrences"`
	WorstRecurrence int    `json:"worst_recurrence"`
	Services        int    `json:"services"`
	Priority        string `json:"priority"`
	Owner           string `json:"owner,omitempty"`
	// CarriedOverWeeks is how many earlier digests already reported this class.
	// Zero means new this week.
	CarriedOverWeeks int `json:"carried_over_weeks,omitempty"`

	// The map stage's structured verdict. Typed rather than one markdown blob so
	// the UI renders each part as what it is and reads the verdicts directly —
	// parsing them back out of prose needed a regex that broke on every markup
	// variant the model chose.
	//
	// Label is a human name for the class; the aggregation key stays as the
	// identity and is still shown, because it is what someone greps for.
	Label       string `json:"label"`
	Headline    string `json:"headline"`
	Problem     string `json:"problem"`
	Fix         string `json:"fix"`
	BlastRadius string `json:"blast_radius,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
	Cause       string `json:"cause,omitempty"`
	Env         string `json:"env,omitempty"`
}

// summariseClass reduces one failure class's analyses into a single finding.
func summariseClass(ctx *security.RequestContext, p events.DigestPeriod, c events.DigestClass) (classFinding, error) {
	finding := classFinding{
		AggregationKey:  c.AggregationKey,
		AccountID:       c.AccountID,
		AccountName:     c.AccountName,
		Events:          c.Events,
		NewIncidents:    c.NewIncidents,
		Recurrences:     c.Recurrences,
		WorstRecurrence: c.WorstRecurrence,
		Services:        c.Services,
		Priority:        c.Priority,
		Owner:           c.Owner,
	}

	bodies, err := events.GetClassAnalysisText(ctx, p, c.AccountID, c.AggregationKey, digestMaxAnalysesPerClass)
	if err != nil {
		return finding, fmt.Errorf("summariseClass: loading analyses: %w", err)
	}
	if len(bodies) == 0 {
		return finding, fmt.Errorf("summariseClass: class %q: %w", c.AggregationKey, errClassHasNoText)
	}

	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptEventDigestClassSummary, c.AccountID)
	if err != nil {
		return finding, fmt.Errorf("summariseClass: loading prompt: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Failure Class\n%s\n\nAccount: %s\n\n", c.AggregationKey, c.AccountName)
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
	if c.Namespaces != "" {
		fmt.Fprintf(&sb, "Namespaces: %s\n", c.Namespaces)
	}
	if c.Clusters != "" {
		fmt.Fprintf(&sb, "Clusters: %s\n", c.Clusters)
	}
	for i, body := range bodies {
		fmt.Fprintf(&sb, "\n## Analysis %d\n%s\n", i+1, core.TruncateMiddle(body, digestMaxCharsPerAnalysis/2, digestMaxCharsPerAnalysis/2))
	}

	completion, err := core.GenerateAndTrackLLMContent(
		ctx, "", c.AccountID, "", "", "event_digest_class_summary", false,
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

	var verdict classFinding
	if err := json.Unmarshal([]byte(stripJSONFence(completion.Choices[0].Content)), &verdict); err != nil {
		return finding, fmt.Errorf("summariseClass: class %q: %w", c.AggregationKey, err)
	}
	if verdict.Problem == "" {
		return finding, fmt.Errorf("summariseClass: class %q: response has no problem", c.AggregationKey)
	}
	// Counters stay authoritative — only the model's own fields are taken, so a
	// hallucinated event count cannot overwrite the query's.
	finding.Label = verdict.Label
	finding.Headline = verdict.Headline
	finding.Problem = verdict.Problem
	finding.Fix = verdict.Fix
	finding.BlastRadius = verdict.BlastRadius
	finding.Confidence = verdict.Confidence
	finding.Cause = verdict.Cause
	finding.Env = verdict.Env
	return finding, nil
}

// digestBriefing is the reduce stage's structured output.
//
// Structured rather than markdown so the UI renders each section as what it is —
// patterns as cards with a stance, the plan as a table, hygiene as figures.
// A single markdown blob forces a generic renderer and loses all of that.
type digestBriefing struct {
	Snapshot      []string         `json:"snapshot"`
	WhatBrokeLede string           `json:"what_broke_lede"`
	Patterns      []digestPattern  `json:"patterns"`
	Plan          []digestPlanItem `json:"plan"`
	Hygiene       digestHygiene    `json:"hygiene"`
	// CarriedOver and Resolved are derived from stored digests, never from the
	// model — they are exact, and a hallucinated "resolved" claims a fix that
	// did not happen.
	CarriedOver []digestCarryOver `json:"carried_over"`
	Resolved    []digestCarryOver `json:"resolved"`
}

type digestPattern struct {
	Title  string `json:"title"`
	Stance string `json:"stance"`
	Body   string `json:"body"`
}

type digestPlanItem struct {
	Priority string `json:"priority"`
	Action   string `json:"action"`
	Area     string `json:"area"`
	Owner    string `json:"owner"`
}

type digestHygiene struct {
	Noise      string `json:"noise"`
	Pipeline   string `json:"pipeline"`
	Confidence string `json:"confidence"`
}

type digestCarryOver struct {
	AggregationKey string `json:"aggregation_key"`
	AccountName    string `json:"account_name,omitempty"`
	Weeks          int    `json:"weeks"`
}

// jsonFence matches a ```json … ``` wrapper. Both prompts forbid one, but models
// add it often enough that stripping is cheaper than a retry.
var jsonFence = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*(.*?)\\s*```\\s*$")

// stripJSONFence removes a ```json … ``` wrapper if present.
func stripJSONFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if m := jsonFence.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	return trimmed
}

// parseDigestBriefing decodes the reduce stage's response.
func parseDigestBriefing(raw string) (digestBriefing, error) {
	var b digestBriefing
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &b); err != nil {
		return b, fmt.Errorf("parseDigestBriefing: %w", err)
	}
	return b, nil
}

// annotateCarryOver stamps each finding with how many earlier weeks already
// reported it, and returns the classes that earlier weeks reported but this
// period did not see at all — the ones that stopped recurring.
//
// Keyed on (account, class) throughout. The same aggregation key in two
// accounts is two unrelated incidents with independent recurrence histories, so
// keying on the class alone would report one as carried over because the other
// appeared.
//
// present must be the period's complete class list, not the summarised findings.
// Findings are bounded, so a class that merely ranked below the cut would
// otherwise be announced as resolved.
func annotateCarryOver(findings []classFinding, prior []events.PriorClass, present []events.ClassRef) []events.PriorClass {
	weeksByKey := make(map[string]int, len(prior))
	for _, pc := range prior {
		weeksByKey[events.ClassRef{AccountID: pc.AccountID, AggregationKey: pc.AggregationKey}.Key()] = pc.Weeks
	}
	for i := range findings {
		ref := events.ClassRef{AccountID: findings[i].AccountID, AggregationKey: findings[i].AggregationKey}
		findings[i].CarriedOverWeeks = weeksByKey[ref.Key()]
	}

	seen := make(map[string]struct{}, len(present))
	for _, r := range present {
		seen[r.Key()] = struct{}{}
	}

	var resolved []events.PriorClass
	for _, pc := range prior {
		ref := events.ClassRef{AccountID: pc.AccountID, AggregationKey: pc.AggregationKey}
		if _, still := seen[ref.Key()]; !still {
			resolved = append(resolved, pc)
		}
	}
	return resolved
}

// briefingAccountID picks an account to attribute the tenant-level reduce call
// to. The review spans accounts and belongs to none of them, but cost tracking
// and prompt overrides both key on an account, so the largest contributor stands
// in rather than passing an empty id that would attribute the spend to nobody.
func briefingAccountID(findings []classFinding) string {
	best, bestEvents := "", -1
	for _, f := range findings {
		if f.Events > bestEvents {
			best, bestEvents = f.AccountID, f.Events
		}
	}
	return best
}

// synthesiseBriefing turns the counters and per-class findings into the briefing.
func synthesiseBriefing(
	ctx *security.RequestContext,
	p events.DigestPeriod,
	metrics events.DigestMetrics,
	findings []classFinding,
	resolved []events.PriorClass,
) (digestBriefing, error) {
	var briefing digestBriefing
	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptEventDigestBriefing, briefingAccountID(findings))
	if err != nil {
		return briefing, fmt.Errorf("synthesiseBriefing: loading prompt: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Period\n%s to %s\n\n",
		p.PeriodStart.Format("Jan 2, 2006"), p.PeriodEnd.Format("Jan 2, 2006"))

	fmt.Fprintf(&sb, "## Pipeline Health\n"+
		"- Events entering analysis: %d\n"+
		"- Fully analysed (summary, investigation, log, detailed): %d (%d%%)\n"+
		"- Events with a failed analysis: %d\n"+
		"- Root-cause stage (rca_analysis) ran on: %d events (%d%%)\n\n",
		metrics.EventsAnalysed, metrics.EventsComplete, metrics.CompletionPct, metrics.FailedEvents,
		metrics.RcaEvents, metrics.RcaPct)

	fmt.Fprintf(&sb, "## Telemetry Counters\n"+
		"- Real incident events: %d of %d analysed, across %d services\n"+
		"- Distinct failure classes: %d\n"+
		"- New: %d | Recurring: %d (%d%% of real events)\n"+
		"- P1 (HIGH/CRITICAL) share: %d%%\n"+
		"- Noise share: %d%% (%d test-traffic, %d suppressed by rule)\n"+
		"- Learnings captured into the context layer: %d\n\n",
		metrics.RealEvents, metrics.EventsAnalysed, metrics.Services,
		metrics.FailureClasses, metrics.NewIncidents, metrics.Recurrences,
		metrics.RecurrencePct, metrics.P1Pct, metrics.NoisePct,
		metrics.SyntheticEvents, metrics.SuppressedEvents, metrics.Learnings)

	if len(metrics.NoiseClasses) > 0 {
		sb.WriteString("## Excluded As Noise\n")
		for _, n := range metrics.NoiseClasses {
			fmt.Fprintf(&sb, "- %s in %s — %d events (%d%% of the week's volume), %s\n",
				n.AggregationKey, n.AccountName, n.Events, n.Pct, n.Reason)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Failure Class Findings\n")
	for _, f := range findings {
		owner := f.Owner
		if owner == "" {
			owner = "Unassigned"
		}
		// The week number is precomputed rather than left as "reported in N earlier
		// weeks" for the model to add one to: asked to do that arithmetic it
		// produced two different numbers for the same class in one briefing.
		history := "new this week"
		if f.CarriedOverWeeks > 0 {
			history = fmt.Sprintf("carried over — week %d of an ongoing pattern", f.CarriedOverWeeks+1)
		}
		fmt.Fprintf(&sb, "\n### %s [%s] (account: %s)\nEvents: %d | New: %d | Recurrences: %d | "+
			"Worst recurrence: %d | Services: %d | Priority: %s | Owner: %s | Env: %s | "+
			"Cause: %s | Confidence: %s | History: %s\nProblem: %s\nFix: %s\nBlast radius: %s\n",
			f.Label, f.AggregationKey, f.AccountName, f.Events, f.NewIncidents, f.Recurrences,
			f.WorstRecurrence, f.Services, f.Priority, owner, f.Env, f.Cause, f.Confidence,
			history, f.Problem, f.Fix, f.BlastRadius)
	}

	completion, err := core.GenerateAndTrackLLMContent(
		ctx, "", briefingAccountID(findings), "", "", "event_digest_briefing", false,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
			llms.TextParts(llms.ChatMessageTypeHuman, sb.String()),
		}, false,
		llms.WithTemperature(0.0),
	)
	if err != nil {
		return briefing, fmt.Errorf("synthesiseBriefing: LLM call: %w", err)
	}
	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return briefing, fmt.Errorf("synthesiseBriefing: empty response")
	}

	briefing, err = parseDigestBriefing(completion.Choices[0].Content)
	if err != nil {
		return briefing, fmt.Errorf("synthesiseBriefing: %w", err)
	}

	// Carry-over is attached after the model returns, from the stored digests
	// rather than the response. These two lists claim a pattern is still open or
	// finally fixed, which is exactly the kind of claim a model should not be
	// trusted to make when the data already answers it exactly.
	for _, f := range findings {
		if f.CarriedOverWeeks > 0 {
			briefing.CarriedOver = append(briefing.CarriedOver,
				digestCarryOver{
					AggregationKey: f.AggregationKey,
					AccountName:    f.AccountName,
					Weeks:          f.CarriedOverWeeks + 1,
				})
		}
	}
	for _, r := range resolved {
		briefing.Resolved = append(briefing.Resolved,
			digestCarryOver{
				AggregationKey: r.AggregationKey,
				AccountName:    r.AccountName,
				Weeks:          r.Weeks,
			})
	}
	return briefing, nil
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
	if err := events.UpsertDigest(writeCtx, p, events.DigestMetrics{}, nil, nil, nil, "",
		events.DigestStatusFailed, cause.Error(), source); err != nil {
		return fmt.Errorf("storeDigestFailure: %w (original: %v)", err, cause)
	}
	return cause
}
