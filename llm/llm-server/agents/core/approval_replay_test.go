//go:build approvalreplay

// Approval-check safety replay (#40902). Not part of the normal test run: it
// calls real providers. It builds the approval check's real task (tool prompt +
// shared rules + options) and asks a decision model and the LLM the same
// question, so their "read" verdicts can be compared on a labelled set.
// Dataset, scoring and how to run it: llm/benchmark/approval_safety/README.md.
package core

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/llms/classify"
	"nudgebee/llm/llms/decision"
	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// replayCase is one labelled tool call. Label is the expected request type
// (read, create, update, delete); empty for unlabelled real traffic.
type replayCase struct {
	ID       string `json:"id"`
	Tool     string `json:"tool"`
	Input    string `json:"input"`
	Label    string `json:"label,omitempty"`
	Category string `json:"category,omitempty"`
	Note     string `json:"note,omitempty"`
}

type replayVerdict struct {
	Label      string             `json:"label,omitempty"`
	Raw        string             `json:"raw,omitempty"`
	Confidence *float64           `json:"confidence,omitempty"`
	Probs      map[string]float64 `json:"probabilities,omitempty"`
	LatencyMs  int64              `json:"latency_ms"`
	Error      string             `json:"error,omitempty"`
}

type replayResult struct {
	replayCase
	Static   string          `json:"static"`         // the static rules' verdict; "" = undecided
	HasLLM   bool            `json:"has_llm_prompt"` // the tool has an LLM prompt for undecided input
	Decision *replayVerdict  `json:"decision,omitempty"`
	LLM      []replayVerdict `json:"llm,omitempty"`
}

func replayCtx() *security.RequestContext {
	return security.NewRequestContext(context.Background(), &security.SecurityContext{}, nil, nil, nil)
}

// staticAndPrompt runs the tool's static rules and, when they do not decide,
// returns the tool's LLM prompt, as authorizeToolCall does.
func staticAndPrompt(ctx *security.RequestContext, c replayCase) (static string, prompt string, err error) {
	// Other tests in this package register a mock shell_execute, so the real
	// shell tool is built directly; any other mock is an error.
	var tool toolcore.NBTool = tools.ShellTool{AccountId: toolcore.SystemAccountIDForProbe}
	if c.Tool != toolcore.ToolExecuteShellCommand {
		t, ok := toolcore.GetNBTool(toolcore.SystemAccountIDForProbe, c.Tool)
		if !ok || t == nil {
			return "", "", fmt.Errorf("tool %q not registered", c.Tool)
		}
		if _, mock := t.(mockTool); mock {
			return "", "", fmt.Errorf("tool %q is a test mock in this package", c.Tool)
		}
		tool = t
	}
	if v, ok := tool.(toolcore.ToolRequestInference); ok {
		rt, err := v.InferToolRequestType(ctx, c.Tool, c.Input)
		if err != nil {
			return "", "", fmt.Errorf("static: %w", err)
		}
		if rt != "" {
			return string(rt), "", nil
		}
	}
	if v, ok := tool.(toolcore.ToolRequestInferencePrompt); ok {
		p, err := v.InferToolRequestTypePrompt(ctx, c.Tool, c.Input)
		if err != nil {
			return "", "", fmt.Errorf("prompt: %w", err)
		}
		return "", p, nil
	}
	return "", "", nil
}

// normalizeVerdict maps a task label to what authorizeToolCall does with it:
// write/execute count as create, unknown routes to approval (update).
func normalizeVerdict(label string) string {
	switch label {
	case toolRequestTypeWrite, toolRequestTypeExecute:
		return string(toolcore.ToolRequestTypeCreate)
	case toolRequestTypeUnknown, "":
		return string(toolcore.ToolRequestTypeUpdate)
	}
	return label
}

func readCases(t *testing.T, path string) []replayCase {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var cases []replayCase
	if strings.HasSuffix(path, ".csv") { // export.sql output: tool_name, parameters, n
		rows, err := csv.NewReader(f).ReadAll()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, r := range rows {
			if i == 0 || len(r) < 2 {
				continue
			}
			cases = append(cases, replayCase{ID: fmt.Sprintf("real-%d", i), Tool: r[0], Input: r[1]})
		}
		return cases
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		var c replayCase
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("parse %s: %v: %s", path, err, line)
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return cases
}

// TestApprovalReplayFilter keeps the real tool calls the static rules leave to
// the LLM check (the only calls a decision model would ever see).
//
//	APPROVAL_REPLAY_IN=calls.csv APPROVAL_REPLAY_OUT=undecided.jsonl
func TestApprovalReplayFilter(t *testing.T) {
	in, out := os.Getenv("APPROVAL_REPLAY_IN"), os.Getenv("APPROVAL_REPLAY_OUT")
	if in == "" || out == "" {
		t.Skip("set APPROVAL_REPLAY_IN and APPROVAL_REPLAY_OUT")
	}
	ctx := replayCtx()
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	enc := json.NewEncoder(w)
	counts := map[string]int{}
	for _, c := range readCases(t, in) {
		static, prompt, err := staticAndPrompt(ctx, c)
		switch {
		case err != nil:
			counts["error"]++
		case static != "":
			counts["static:"+static]++
		case prompt == "":
			counts["no_prompt"]++
		default:
			counts["llm:"+c.Tool]++
			if err := enc.Encode(c); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("counts: %v", counts)
}

// TestApprovalReplayRun asks the decision model once and the LLM
// APPROVAL_REPLAY_LLM_RUNS times (default 2) for every case, after the static
// rules, and writes one result line per case.
//
//	APPROVAL_REPLAY_IN=cases.jsonl APPROVAL_REPLAY_OUT=results.jsonl
//	CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_API_TOKEN, DECISION_MODEL (default clef)
//	GEMINI_API_KEY, GEMINI_MODEL (default gemini-3.8-flash), GEMINI_BASE_URL (optional)
func TestApprovalReplayRun(t *testing.T) {
	in, out := os.Getenv("APPROVAL_REPLAY_IN"), os.Getenv("APPROVAL_REPLAY_OUT")
	if in == "" || out == "" {
		t.Skip("set APPROVAL_REPLAY_IN and APPROVAL_REPLAY_OUT")
	}
	ctx := replayCtx()
	goCtx := context.Background()

	var dc *decision.Client
	if tok := os.Getenv("CLOUDFLARE_API_TOKEN"); tok != "" {
		var err error
		dc, err = decision.New(decision.Config{
			Provider: decision.ProviderCloudflare, Model: envOr("DECISION_MODEL", "clef"),
			APIKey: tok, Endpoint: os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var gem llms.Model
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		opts := []googleai.Option{googleai.WithAPIKey(key), googleai.WithDefaultModel(envOr("GEMINI_MODEL", "gemini-3.8-flash"))}
		if base := os.Getenv("GEMINI_BASE_URL"); base != "" {
			opts = append(opts, googleai.WithBaseURL(base))
		}
		g, err := googleai.New(goCtx, opts...)
		if err != nil {
			t.Fatal(err)
		}
		gem = g
	}
	if dc == nil && gem == nil {
		t.Skip("set CLOUDFLARE_API_TOKEN and/or GEMINI_API_KEY")
	}
	llmRuns := 2
	if v := os.Getenv("APPROVAL_REPLAY_LLM_RUNS"); v != "" {
		fmt.Sscanf(v, "%d", &llmRuns)
	}

	// The LLM backend exactly as the approval check runs it (prompt, retries,
	// label matching), with the provider call swapped for a direct one so no
	// usage rows are written.
	if gem != nil {
		oldGenerate := classify.ClassificationGenerate
		t.Cleanup(func() { classify.ClassificationGenerate = oldGenerate })
		classify.ClassificationGenerate = func(_ *security.RequestContext, _, _, _, _, _ string, _ bool, messages []llms.MessageContent, _ bool, options ...llms.CallOption) (*llms.ContentResponse, error) {
			return gem.GenerateContent(goCtx, messages, options...)
		}
	}

	cases := readCases(t, in)
	results := make([]replayResult, len(cases))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, c := range cases {
		wg.Add(1)
		go func(i int, c replayCase) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("case %s panicked: %v\n%s", c.ID, p, debug.Stack())
				}
			}()
			r := replayResult{replayCase: c}
			static, prompt, err := staticAndPrompt(ctx, c)
			if err != nil {
				r.Static = "error: " + err.Error()
				results[i] = r
				return
			}
			r.Static, r.HasLLM = static, prompt != ""
			if prompt == "" {
				results[i] = r
				return
			}
			task := toolRequestTypeTask(prompt)
			input := wrapUntrustedToolInput(c.Input)
			if dc != nil {
				r.Decision = askDecision(goCtx, dc, task, input)
			}
			if gem != nil {
				for run := 0; run < llmRuns; run++ {
					start := time.Now()
					d, err := classify.ClassifyWithLLM(ctx, classify.ClassificationCall{KeepCallerTier: true}, task, input)
					v := replayVerdict{Label: normalizeVerdict(d.Label()), Raw: d.Raw, LatencyMs: time.Since(start).Milliseconds()}
					if err != nil {
						v.Error = err.Error()
					}
					r.LLM = append(r.LLM, v)
				}
			}
			results[i] = r
		}(i, c)
	}
	wg.Wait()

	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	enc := json.NewEncoder(w)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

// askDecision asks the decision model the approval check's question, as
// classifyWithDecision does, without the usage row or the 2 s budget (the
// latency is recorded instead).
func askDecision(ctx context.Context, dc *decision.Client, task classify.ClassificationTask, input string) *replayVerdict {
	options := make([]decision.Option, len(task.Options))
	for i, opt := range task.Options {
		options[i] = decision.Option{Label: opt.Label, Description: opt.Description}
	}
	start := time.Now()
	choice, err := dc.Choose(ctx, input, strings.TrimSpace(task.Instructions), options)
	v := &replayVerdict{LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		v.Error = err.Error()
		return v
	}
	conf := classify.DecisionConfidence(choice)
	v.Raw, v.Confidence, v.Probs = choice.Choice, &conf, choice.Probabilities
	if labels, ok := classify.MatchClassificationLabels(task, choice.Choice); ok && !choice.Refused {
		v.Label = normalizeVerdict(labels[0])
	}
	return v
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestApprovalReplayStatic writes each case's static verdict (no provider calls).
func TestApprovalReplayStatic(t *testing.T) {
	in, out := os.Getenv("APPROVAL_REPLAY_IN"), os.Getenv("APPROVAL_REPLAY_OUT")
	if in == "" || out == "" {
		t.Skip("set APPROVAL_REPLAY_IN and APPROVAL_REPLAY_OUT")
	}
	ctx := replayCtx()
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	enc := json.NewEncoder(w)
	for _, c := range readCases(t, in) {
		r := replayResult{replayCase: c}
		static, prompt, err := staticAndPrompt(ctx, c)
		if err != nil {
			static = "error: " + err.Error()
		}
		r.Static, r.HasLLM = static, prompt != ""
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}
