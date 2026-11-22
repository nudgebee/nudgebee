package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The step-1 summary is the first thing an on-call engineer reads, and it is also
// fed verbatim into the investigation prompt and the detailed-response synthesis.
// It regressed once into a triage-mechanics report (~77% of the body was score-factor
// arithmetic, dedup bookkeeping and unrelated co-firing alerts before the reader
// learned what was broken), so the contract is pinned here: still call the triage
// tools, never narrate the triage internals.
func TestBuildEventSummaryQuery_OnCallContract(t *testing.T) {
	q := buildEventSummaryQuery("aa3f7199-b608-40dc-80a3-61c918b64e0a")

	assert.Contains(t, q, "aa3f7199-b608-40dc-80a3-61c918b64e0a", "query must name the event id")

	// The triage tools still have to be exercised — step 1 is the only stage of the
	// automatic pipeline that runs the events agent.
	for _, tool := range []string{"get_event_triage_explanation", "get_event_incident_assembly"} {
		assert.Contains(t, q, tool, "query must still exercise %s", tool)
	}

	// ...but not as a narrated triage report.
	for _, banned := range []string{
		"explain how Nudgebee auto-triaged",
		"the score_factors that produced it",
	} {
		assert.NotContains(t, q, banned, "query must not ask the agent to narrate triage internals")
	}

	// The rendered card was inconsistent across runs (numbered bold list items one
	// run, H3 headings the next), so the section headings are pinned verbatim.
	for _, heading := range []string{
		"### What is broken",
		"### Since when",
		"### What changed just before",
		"### What else is firing",
		"### Priority",
	} {
		assert.Contains(t, q, heading, "query must pin the %q section", heading)
	}

	// Correlated signals are what on-call wants after "what changed" — the cause
	// tiers alone dropped the confirmed same-incident alerts.
	for _, tier := range []string{"same_incident", "impact", "cause.config_changes", "cause.upstream"} {
		assert.Contains(t, q, tier, "query must ask for the %s assembly tier", tier)
	}
	assert.Contains(t, q, "SKIP the chronic tier", "query must exclude background-noise correlations")

	lower := strings.ToLower(q)
	assert.Contains(t, lower, "on-call brief", "query must frame the output for an on-call reader")
	assert.Contains(t, lower, "300 words", "query must cap the brief's length")
}

// #41579: the event_brief agent fetches the tools itself, so its instruction asks
// "from" each output — and must keep the exact format of the events-agent text.
func TestBuildEventBriefQuery_PrefetchedKeepsFormat(t *testing.T) {
	const id = "aa3f7199-b608-40dc-80a3-61c918b64e0a"
	agentQ := buildEventBriefQuery(id, false)
	directQ := buildEventBriefQuery(id, true)

	assert.Equal(t, buildEventSummaryQuery(id), agentQ, "the events-agent path must be unchanged")
	assert.Contains(t, agentQ, "Call get_event_triage_explanation and state the firing history")
	assert.Contains(t, agentQ, "Call get_event_incident_assembly and report its cause tiers")

	assert.Contains(t, directQ, id)
	assert.NotContains(t, directQ, "Call get_event_", "prefetched data must not be requested again")
	assert.Contains(t, directQ, "From the get_event_triage_explanation output, state the firing history")
	assert.Contains(t, directQ, "From the get_event_incident_assembly output, report its cause tiers")

	// Everything after the source phrases is shared text.
	tail := func(q string) string { return q[strings.Index(q, "### What else is firing"):] }
	assert.Equal(t, tail(agentQ), tail(directQ))
	for _, heading := range []string{"### What is broken", "### Since when", "### What changed just before", "### What else is firing", "### Priority"} {
		assert.Contains(t, directQ, heading)
	}
}

func TestBuildInlinedRelatedAlertsBlock(t *testing.T) {
	assembly := `{"note":"Alerts grouped around this event.","event_id":"e1","same_incident":[{"event_id":"s1"}],` +
		`"cause":{"config_changes":[],"upstream":[{"event_id":"u1"}]},"impact":[],` +
		`"chronic":[{"event_id":"c1"},{"event_id":"c2"},{"event_id":"c3"}],"co_firing":[]}`
	block := buildInlinedRelatedAlertsBlock(assembly, "acct-1")

	assert.Contains(t, block, "## Related-Alert Candidates")
	assert.Contains(t, block, "already included below")
	assert.Contains(t, block, "do not ask the events tool")
	assert.NotContains(t, block, "Only when that information is absent", "the fetch-it-yourself fallback wording must not be used")
	assert.Contains(t, block, "### Related Alerts Check")
	assert.Contains(t, block, "/investigate?id=<event_id>&accountId=acct-1")
	assert.Contains(t, block, "\nAlerts grouped around this event.\n", "the caveat leads the data as text")

	assert.Contains(t, block, `"chronic_count":3`)
	assert.NotContains(t, block, `"c1"`, "chronic entries are folded to a count")
	assert.Contains(t, block, `"u1"`)
	assert.Contains(t, block, `"s1"`)
}

func TestBuildInlinedRelatedAlertsBlock_CapsAndToleratesNonJSON(t *testing.T) {
	big := strings.Repeat("x", 3*maxInlinedAssemblyBytes)
	block := buildInlinedRelatedAlertsBlock(big, "acct-1")
	assert.Less(t, len(block), maxInlinedAssemblyBytes+3000, "the assembly is capped")
	assert.Contains(t, block, "### Related Alerts Check")
}

// The 8 KB cut must drop the weakest tier, not same_incident.
func TestBuildInlinedRelatedAlertsBlock_StrongestTiersFirst(t *testing.T) {
	assembly := `{"note":"n","co_firing":[{"event_id":"cf"}],"same_incident":[{"event_id":"si"}],"cause":{"upstream":[{"event_id":"up"}]},"impact":[{"event_id":"im"}],"window":{"start":1}}`
	block := buildInlinedRelatedAlertsBlock(assembly, "a")
	si, cause, impact, cf, win := strings.Index(block, `"si"`), strings.Index(block, `"up"`), strings.Index(block, `"im"`), strings.Index(block, `"cf"`), strings.Index(block, `"window"`)
	assert.True(t, si >= 0 && si < cause && cause < impact && impact < cf && cf < win, "order: same_incident, cause, impact, co_firing, then the rest")
	var parsed map[string]any
	body := block[strings.Index(block, "```json\n")+len("```json\n") : strings.LastIndex(block, "\n```")]
	assert.NoError(t, json.Unmarshal([]byte(body), &parsed), "an uncut body is valid JSON")
}

// #41579: the direct brief is one prompt for the LLM agent: guidance, the
// prefetched five-section instruction, then every read's output tagged with its
// tool name.
func TestComposeDirectEventBriefQuery(t *testing.T) {
	const id = "aa3f7199-b608-40dc-80a3-61c918b64e0a"
	q := composeDirectEventBriefQuery(id, []string{`{"id":"e1"}`, `{"duplicate_info":{}}`, `{"same_incident":[]}`, `{"series":[]}`})

	assert.True(t, strings.HasPrefix(q, eventBriefGuidance), "guidance leads")
	assert.Contains(t, q, buildEventBriefQuery(id, true))
	for _, tag := range []string{
		"<get_event_by_id>\n{\"id\":\"e1\"}\n</get_event_by_id>",
		"<get_event_triage_explanation>\n{\"duplicate_info\":{}}\n</get_event_triage_explanation>",
		"<get_event_incident_assembly>\n{\"same_incident\":[]}\n</get_event_incident_assembly>",
		"<get_event_evidence>\n{\"series\":[]}\n</get_event_evidence>",
	} {
		assert.Contains(t, q, tag)
	}
	assert.Less(t, strings.Index(q, "### Priority"), strings.Index(q, "<get_event_by_id>"), "instruction comes before the data")
	// Recovery finds this run's message by the event id in its query.
	assert.Contains(t, q, id)
	// The LLM agent parses a query that is a JSON object; this one must not be.
	assert.False(t, strings.HasPrefix(strings.TrimSpace(q), "{"))
}

// A failed assembly fetch must not bring back "call get_event_incident_assembly":
// the orchestrator does not declare it and detoured to the events sub-agent.
func TestBuildRelatedAlertsUnavailableBlock(t *testing.T) {
	block := buildRelatedAlertsUnavailableBlock()
	assert.Contains(t, block, "## Related-Alert Candidates")
	assert.Contains(t, block, "### Related Alerts Check")
	assert.Contains(t, block, "Do not try to fetch it")
	assert.NotContains(t, block, "call get_event_incident_assembly")
	assert.NotContains(t, strings.ToLower(block), "only when that information is absent")
}

// The brief feeds the investigation prompt and the final report, and its reads
// are a subset of the stored evidence, so it must never call data absent.
func TestDirectEventBrief_NeverClaimsDataAbsent(t *testing.T) {
	q := composeDirectEventBriefQuery("e1", []string{"a", "b", "c", "d"})
	assert.NotContains(t, q, "say that the information is unavailable")
	assert.NotContains(t, q, "call it unavailable")
	assert.Contains(t, q, "leave it out")
	assert.Contains(t, q, "never write that it is unavailable, missing or unknown")
}

// A failed read must come out as "not checked", never as "nothing qualifies":
// a false "no changes before the alert" would reach the final report.
func TestComposeDirectEventBriefQuery_FailedReadIsNotChecked(t *testing.T) {
	q := composeDirectEventBriefQuery("e1", []string{`{"id":"e1"}`, `{"duplicate_info":{}}`, eventBriefNotLoaded + ": get_event_incident_assembly: timeout", `{}`})
	assert.Contains(t, q, "<get_event_incident_assembly>\nNOT LOADED: get_event_incident_assembly: timeout\n</get_event_incident_assembly>")
	assert.Contains(t, q, `write the single bullet "- Not checked in this brief." instead of saying that nothing qualifies`)
	for _, section := range []string{"What changed just before", "What else is firing", "Since when", "Priority"} {
		assert.Contains(t, q, section)
	}
	assert.Contains(t, q, "get_event_incident_assembly for What changed just before and What else is firing")
	assert.Contains(t, q, "get_event_triage_explanation for Since when")
}
