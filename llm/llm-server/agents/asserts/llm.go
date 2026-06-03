//go:build e2e

package asserts

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// ============================================================
// Tier 4 — LLM-as-judge semantic assertions
// ============================================================
//
// These call an actual LLM to evaluate whether a free-form agent
// response satisfies a list of expected claims. Cost is ~$0.001 per call
// at the summary-tier model (gemini-2.5-flash by default). Use sparingly
// — pure substring or count checks are always preferred when they can
// express the assertion.
//
// Determinism: temperature 0.0 + fast-task thinking level. Gemini at temp
// 0 is still slightly non-deterministic across runs; a flaky claim is
// almost always a sign the claim is borderline, not a sign the judge is
// broken.

// JudgeMode controls how multiple expected claims combine into the
// overall verdict.
type JudgeMode int

const (
	// JudgeAll requires every expected claim to be satisfied. Default.
	JudgeAll JudgeMode = iota
	// JudgeAny requires at least one expected claim to be satisfied.
	// Useful when expected_output lists alternative valid answers.
	JudgeAny
)

// JudgeResult is the structured verdict from an LLM-as-judge call.
type JudgeResult struct {
	// Matched is the overall verdict: true if the response satisfies the
	// claims under the chosen JudgeMode.
	Matched bool
	// PerItem holds one entry per expected claim, in input order.
	PerItem []JudgeItemResult
	// Reason is a one-line summary; for All-mode failures it names the
	// first unsatisfied claim, for Any-mode failures it lists what was
	// looked for.
	Reason string
	// Raw is the unparsed LLM output, retained for debugging when parsing
	// goes off the rails.
	Raw string
}

// JudgeItemResult is the verdict on a single expected claim.
type JudgeItemResult struct {
	Expected string
	Matched  bool
	Reason   string // judge's one-line justification
	// Parsed records whether the judge actually emitted a CLAIM line for this
	// item. We can't infer "judge was silent" from (Matched=false, Reason="")
	// alone — that shape is also produced by a legitimate "CLAIM N: NO" with
	// no justification. Tracking it as an explicit flag lets us distinguish a
	// real failed assertion (silence is parse error, fatal) from a real NO
	// verdict (normal test failure).
	Parsed bool
}

// JudgeOption configures a SemanticMatch / LLMClaims call.
type JudgeOption func(*judgeConfig)

type judgeConfig struct {
	mode  JudgeMode
	model string // optional override; empty => summary-tier default
}

// WithJudgeMode overrides the default JudgeAll behaviour.
func WithJudgeMode(m JudgeMode) JudgeOption {
	return func(c *judgeConfig) { c.mode = m }
}

// WithJudgeModel overrides the LLM model used for judgement. Default is
// whatever model is wired up for the "summary" tier in the LLM-server's
// config (typically gemini-2.5-flash). Override sparingly — a more
// capable model adds cost and rarely improves accuracy for this task.
func WithJudgeModel(model string) JudgeOption {
	return func(c *judgeConfig) { c.model = model }
}

// JudgeClaims is the raw-return form of the semantic-match check. Use
// this when you want to inspect per-item results, log without failing,
// or chain custom logic on the verdict.
//
// Returns an error only on LLM-call or parse failure. A "the response
// doesn't match" verdict is a successful return with Matched=false.
func JudgeClaims(
	sc *security.RequestContext,
	expected []string,
	actual string,
	opts ...JudgeOption,
) (JudgeResult, error) {
	if sc == nil {
		return JudgeResult{}, fmt.Errorf("semantic_judge: RequestContext is nil")
	}
	cfg := judgeConfig{mode: JudgeAll}
	for _, opt := range opts {
		opt(&cfg)
	}
	if len(expected) == 0 {
		return JudgeResult{Matched: true, Reason: "no claims to evaluate"}, nil
	}

	prompt := buildJudgePrompt(expected, actual)
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	}

	options := []llms.CallOption{
		llms.WithTemperature(0.0),
		core.WithThinkingLevel(core.ThinkingLevelFastTask),
	}
	if cfg.model != "" {
		options = append(options, llms.WithModel(cfg.model))
	}

	completion, err := core.GenerateAndTrackLLMContent(
		sc,
		sc.GetSecurityContext().GetUserId(),
		firstAccount(sc),
		"",   // conversationId — not persisted; judge calls don't belong to a user conversation
		"",   // messageId — same
		"semantic_judge", // agentId
		false, // trackContent — don't write to llm_conversation_messages
		messages,
		true, // cleanupMarkdown — strip code fences if model wraps output
		options...,
	)
	if err != nil {
		return JudgeResult{}, fmt.Errorf("semantic_judge: LLM call failed: %w", err)
	}
	if completion == nil || len(completion.Choices) == 0 {
		return JudgeResult{}, fmt.Errorf("semantic_judge: empty LLM response")
	}

	raw := strings.TrimSpace(completion.Choices[0].Content)
	result, err := parseJudgeOutput(raw, expected)
	if err != nil {
		return JudgeResult{Raw: raw}, fmt.Errorf("semantic_judge: parse failed: %w; raw=%q", err, raw)
	}

	// Apply JudgeMode to compute final Matched.
	switch cfg.mode {
	case JudgeAny:
		result.Matched = false
		for _, item := range result.PerItem {
			if item.Matched {
				result.Matched = true
				break
			}
		}
		if !result.Matched {
			result.Reason = "no expected claim was satisfied"
		}
	default: // JudgeAll
		result.Matched = true
		var firstFailing string
		for _, item := range result.PerItem {
			if !item.Matched {
				result.Matched = false
				if firstFailing == "" {
					firstFailing = item.Expected
				}
			}
		}
		if !result.Matched {
			result.Reason = fmt.Sprintf("not all claims satisfied; first failing: %q", firstFailing)
		}
	}

	return result, nil
}

// LLMClaims is the test-friendly wrapper around JudgeClaims. On mismatch
// it calls t.Errorf with per-claim failure detail. On LLM-call error it
// calls t.Fatalf — a network blip or parse failure should fail the test
// loudly, not silently mask a real problem.
//
// Returns the JudgeResult so callers can log per-item verdicts for CI
// debug visibility.
func LLMClaims(
	t *testing.T,
	sc *security.RequestContext,
	expected []string,
	actual string,
	opts ...JudgeOption,
) JudgeResult {
	t.Helper()
	result, err := JudgeClaims(sc, expected, actual, opts...)
	if err != nil {
		t.Fatalf("LLMClaims: %v", err)
	}

	// Always log per-item verdicts (passes too) — visibility wins when
	// debugging which claim caused a fail.
	for _, item := range result.PerItem {
		verdict := "PASS"
		if !item.Matched {
			verdict = "FAIL"
		}
		t.Logf("  [judge %s] %s: %s", verdict, truncate(item.Expected, 80), item.Reason)
	}

	if !result.Matched {
		t.Errorf("LLMClaims: %s", result.Reason)
	}
	return result
}

// ============================================================
// Internals
// ============================================================

const judgePromptTmpl = `You are evaluating whether an AI investigation agent's response satisfies a set of expected claims.

EXPECTED CLAIMS (the response must support each one):
%s

AGENT'S RESPONSE:
%s

For each claim, output a single line in this exact format:
CLAIM <N>: <YES or NO> -- <one-line justification>

Then output a final line:
OVERALL: <YES or NO>

Rules:
- A claim is satisfied if the response's content supports it, allowing for paraphrase, synonyms, and additional detail.
- A claim is NOT satisfied if the response is silent on it, contradicts it, or only loosely tangential.
- Be strict. Hedges like "I would investigate further", "you should check X", "the system might be misconfigured" do NOT satisfy a claim that asks the agent to identify a specific root cause.
- OVERALL is YES only if every CLAIM is YES.
- Do not output anything else — no preamble, no explanation, just the CLAIM lines and the OVERALL line.`

func buildJudgePrompt(expected []string, actual string) string {
	var claims strings.Builder
	for i, c := range expected {
		fmt.Fprintf(&claims, "%d. %s\n", i+1, strings.TrimSpace(c))
	}
	return fmt.Sprintf(judgePromptTmpl, strings.TrimRight(claims.String(), "\n"), strings.TrimSpace(actual))
}

// claimLineRe matches: "CLAIM 1: YES -- because ...", with em-dash, or with
// no justification at all ("CLAIM 1: YES"). Tolerates lowercase yes/no,
// missing leading whitespace, hyphen variants. The justification group is
// optional so that a judge response which omits "-- ..." still parses (the
// per-claim verdict is the load-bearing part; the reason is for humans).
var claimLineRe = regexp.MustCompile(`(?i)^\s*CLAIM\s+(\d+)\s*:\s*(YES|NO)\b(?:[\s\-—–:]+(.*))?`)

func parseJudgeOutput(raw string, expected []string) (JudgeResult, error) {
	lines := strings.Split(raw, "\n")
	items := make([]JudgeItemResult, len(expected))
	for i, c := range expected {
		items[i].Expected = c
	}

	for _, line := range lines {
		m := claimLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > len(expected) {
			continue // claim number out of range — skip
		}
		items[n-1].Matched = strings.EqualFold(m[2], "YES")
		// m[3] is the optional justification capture; empty string when the
		// judge omitted "-- <reason>". Safe to TrimSpace either way.
		items[n-1].Reason = strings.TrimSpace(m[3])
		items[n-1].Parsed = true
	}

	// Sanity: every claim must have been addressed. Use the explicit Parsed
	// flag — a NO verdict with an empty reason is a legitimate test failure,
	// not a parse error.
	for i, item := range items {
		if !item.Parsed {
			return JudgeResult{Raw: raw, PerItem: items},
				fmt.Errorf("judge did not address claim %d (%q)", i+1, item.Expected)
		}
	}

	return JudgeResult{
		PerItem: items,
		Raw:     raw,
	}, nil
}

// firstAccount returns the first accountId from the security context's
// account list. The judge call needs a valid accountId for LLM tracking;
// any account the caller has access to is fine.
func firstAccount(sc *security.RequestContext) string {
	accounts := sc.GetSecurityContext().GetAccountIds()
	if len(accounts) == 0 {
		return ""
	}
	return accounts[0]
}
