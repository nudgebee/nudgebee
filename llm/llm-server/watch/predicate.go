package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/llm/security"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
)

// Predicate evaluates whether an observation satisfies the watch's
// termination condition. Each kind interprets the expression string
// differently. Implementations must be safe for concurrent use — the
// dispatcher may run multiple polls in parallel.
type Predicate interface {
	// Eval returns Met=true when the observation satisfies the condition.
	// Errors are transient: callers may retry the watch on the next poll.
	Eval(ctx *security.RequestContext, w Watch, obs Observation) (PredicateResult, error)
}

// LLMJudgeClient is the minimal LLM surface the llm_judge predicate
// requires. It exists so tests can inject a fake without pulling in the
// real langchaingo provider stack.
type LLMJudgeClient interface {
	GenerateContent(ctx context.Context, messages []llms.MessageContent, opts ...llms.CallOption) (*llms.ContentResponse, error)
}

// LLMJudgeClientFactory builds a judge client for the given watch. Wired in
// from agents/core via SetLLMJudgeClientFactory at startup so the watch
// package does not need to import agents/core directly (which would break
// the layering — agents/core is the LLM owner, watch is a consumer).
type LLMJudgeClientFactory func(ctx *security.RequestContext, w Watch) (LLMJudgeClient, error)

var (
	llmJudgeClientFactoryMu sync.RWMutex
	llmJudgeClientFactory   LLMJudgeClientFactory
)

// SetLLMJudgeClientFactory installs the LLM client builder. Called once at
// process init from a higher layer (agents/core) so the watch package stays
// below agents/core in the import graph. Guarded by a mutex purely to make
// the race detector happy in tests that re-bootstrap.
func SetLLMJudgeClientFactory(f LLMJudgeClientFactory) {
	llmJudgeClientFactoryMu.Lock()
	defer llmJudgeClientFactoryMu.Unlock()
	llmJudgeClientFactory = f
}

func getLLMJudgeClientFactory() LLMJudgeClientFactory {
	llmJudgeClientFactoryMu.RLock()
	defer llmJudgeClientFactoryMu.RUnlock()
	return llmJudgeClientFactory
}

// CompilePredicate validates a predicate at create-time so a malformed
// expression fails fast in the watch_resource tool rather than only being
// surfaced when the dispatcher first runs.
func CompilePredicate(kind PredicateKind, expr string) (Predicate, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, fmt.Errorf("predicate: expression must not be empty")
	}
	switch kind {
	case PredicateKindRegex:
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("predicate: invalid regex %q: %w", expr, err)
		}
		return &regexPredicate{re: re, expr: expr}, nil
	case PredicateKindSubstring:
		return &substringPredicate{needle: expr}, nil
	case PredicateKindLLMJudge:
		return &llmJudgePredicate{question: expr}, nil
	default:
		return nil, fmt.Errorf("predicate: unsupported kind %q", kind)
	}
}

type regexPredicate struct {
	re   *regexp.Regexp
	expr string
}

func (p *regexPredicate) Eval(_ *security.RequestContext, _ Watch, obs Observation) (PredicateResult, error) {
	match := p.re.FindString(obs.Data)
	met := match != ""
	summary := ""
	if met {
		summary = fmt.Sprintf("regex %q matched: %s", p.expr, truncate(match, 200))
	} else {
		summary = fmt.Sprintf("regex %q not matched", p.expr)
	}
	return PredicateResult{Met: met, Summary: summary}, nil
}

type substringPredicate struct {
	needle string
}

func (p *substringPredicate) Eval(_ *security.RequestContext, _ Watch, obs Observation) (PredicateResult, error) {
	met := strings.Contains(obs.Data, p.needle)
	summary := ""
	if met {
		summary = fmt.Sprintf("found substring %q", truncate(p.needle, 200))
	} else {
		summary = fmt.Sprintf("substring %q not found", truncate(p.needle, 200))
	}
	return PredicateResult{Met: met, Summary: summary}, nil
}

type llmJudgePredicate struct {
	question string
}

// llmJudgeResponse is the strict JSON the judge prompt asks the model to
// emit. Anything else is treated as a transient error. Field set is
// deliberately identical to PredicateResult so a single type conversion
// turns the wire format into the executor's value; if the wire format
// adds a field later the conversion will fail to compile and force us
// to handle the new field explicitly.
type llmJudgeResponse PredicateResult

func (p *llmJudgePredicate) Eval(ctx *security.RequestContext, w Watch, obs Observation) (PredicateResult, error) {
	factory := getLLMJudgeClientFactory()
	if factory == nil {
		return PredicateResult{}, fmt.Errorf("llm_judge: no LLM client factory registered")
	}
	client, err := factory(ctx, w)
	if err != nil {
		return PredicateResult{}, fmt.Errorf("llm_judge: failed to build client: %w", err)
	}

	prompt := fmt.Sprintf(
		`You are evaluating whether a watched resource has reached a termination condition.
Termination condition: %s

Latest observation:
---
%s
---

Reply with strict JSON only, no prose, no code fences:
{"met": true|false, "summary": "<one-sentence description of current state>"}`,
		p.question, truncate(obs.Data, 16000),
	)

	resp, err := client.GenerateContent(ctx.GetContext(), []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: prompt}}},
	})
	if err != nil {
		return PredicateResult{}, fmt.Errorf("llm_judge: model invocation failed: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return PredicateResult{}, fmt.Errorf("llm_judge: empty model response")
	}

	raw := strings.TrimSpace(resp.Choices[0].Content)
	// Tolerate ```json fences that some providers add despite the prompt.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	// Strip any chatty prose before the first '{'. We deliberately do NOT
	// also trim trailing prose with LastIndex("}") — that is greedy across
	// the multi-object case ({"met":true}{"met":false}) and silently
	// concatenates both into a single unparseable blob. Instead, decode
	// with json.Decoder so it reads exactly one complete JSON value and
	// stops; any trailing noise (extra objects, prose) is ignored.
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}

	var parsed llmJudgeResponse
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&parsed); err != nil {
		return PredicateResult{}, fmt.Errorf("llm_judge: response is not valid JSON: %w (got: %s)", err, truncate(raw, 200))
	}
	return PredicateResult(parsed), nil
}

// truncate clips s to at most n bytes and walks back to the previous valid
// UTF-8 boundary so the result is never invalid UTF-8. Postgres `text`
// columns reject invalid UTF-8 at write time, so naïve byte-slicing of a
// multibyte run would surface as INSERT/UPDATE failures inside the
// dispatcher.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
