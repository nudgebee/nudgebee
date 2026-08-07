//go:build e2e

package agents

import (
	"context"
	"fmt"
	nbprompts "nudgebee/llm/prompts"
	"os"
	"regexp"
	"strings"
	"testing"

	"nudgebee/llm/llms/googleai"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestGeminiEmptyContentProbe hits gemini-2.5-flash DIRECTLY (no agent, no
// ReAct planner, no tool-config gating) to find out which input shape triggers
// the empty-content STOP we see from the agent path. Run with:
//
//	LLM_PROVIDER_API_KEY=$(grep ^LLM_PROVIDER_API_KEY .env | cut -d= -f2- | tr -d '"') \
//	  go test -tags=e2e -run TestGeminiEmptyContentProbe ./agents -v -timeout 60s
//
// Three calls, each more like the real agent's input than the last. Whichever
// one first returns empty isolates the cause.
func TestGeminiEmptyContentProbe(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}

	client, err := googleai.New(
		context.Background(),
		googleai.WithAPIKey(apiKey),
		googleai.WithDefaultModel("gemini-2.5-flash"),
	)
	require.NoError(t, err)

	type probe struct {
		name     string
		messages []llms.MessageContent
	}

	// Call 1 — plain user query, no system prompt. Baseline.
	plain := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "List pods in the default namespace"),
	}

	// Call 2 — system prompt + user query. Same as a basic "you are an assistant" pattern.
	withSystem := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are a Kubernetes debugging assistant. Help the user investigate cluster issues."),
		llms.TextParts(llms.ChatMessageTypeHuman, "List pods in the default namespace"),
	}

	// Call 3 — ReAct-style system prompt mirroring planner_react_3.go's structure,
	// padded to ~12-13K input tokens to match what the agent sends.
	reactSystem := `You are a Kubernetes debugging agent. You operate in a strict ReAct format.

Available tools:
- shell_execute: Run a shell command on the cluster pod.

For every turn you MUST emit EXACTLY ONE of:
  <thought>...</thought><action>{"tool":"shell_execute","input":"kubectl ..."}</action>
  <finish>final answer to the user</finish>

Rules:
- No prose outside <thought>, <action>, or <finish>.
- Never invent tool output.
- Use <thought> to reason about what to do next, in 1-3 sentences.
- Use <action> to invoke a tool; the JSON must parse.
- Use <finish> only after you have enough observations to answer.
`
	// Pad the system prompt to ~12K tokens (~48K chars) of plausible-but-redundant
	// tool docs, mimicking the real agent where dozens of tools' schemas balloon
	// the prompt.
	var pad strings.Builder
	tools := []string{
		"kubectl_execute: Run kubectl <verb> against the cluster. Supports get, describe, logs, exec, top, rollout.",
		"helm_execute: Run helm list/status/values to inspect releases. Args: namespace, release.",
		"prometheus_query: Run a PromQL instant query. Args: query, time.",
		"loki_query: Run a LogQL query. Args: query, start, end, limit.",
		"datadog_logs: Fetch DataDog logs by service or hostname. Args: query, from, to.",
		"git_diff: Show git diff in the workspace. Args: path, range.",
		"file_read: Read a file from the workspace. Args: path, lines.",
		"file_write: Write a file in the workspace. Args: path, content.",
		"web_search: Search the web for documentation. Args: query.",
		"web_fetch: Fetch a URL and return its text. Args: url.",
	}
	for i := 0; i < 200; i++ {
		pad.WriteString(fmt.Sprintf("\nTool %d — %s", i, tools[i%len(tools)]))
		pad.WriteString("\n  Notes: This tool is part of the cluster-debugging toolkit. Use it when the situation calls for the corresponding capability.\n")
	}
	padded := reactSystem + "\n\n<tool-catalog>\n" + pad.String() + "\n</tool-catalog>"
	react := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, padded),
		llms.TextParts(llms.ChatMessageTypeHuman, "List pods in the default namespace"),
	}

	type probeWithOpts struct {
		name     string
		messages []llms.MessageContent
		opts     []llms.CallOption
	}

	// Single-message variant: agent's logs show inputMessages=1 — the system
	// prompt is concatenated into one human message (langchaingo prompt
	// templates do this when the prompt is a single ChatPromptTemplate).
	// The googleai wrapper takes a different code path for len(messages)==1
	// (googleai.go:165, generateFromSingleMessage).
	reactSingle := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, padded+"\n\nUser query: List pods in the default namespace"),
	}

	// Streaming sink — must be non-nil to flip to streaming branch in wrapper.
	streamCb := func(_ context.Context, _ []byte) error { return nil }

	// Bake the agent's exact options (planner_react_3.go:1471 + the streaming
	// tracker that llm_common.go always attaches at line 890) on top of the
	// padded ReAct prompt to find which one causes the empty-content STOP.
	probes := []probeWithOpts{
		{name: "1_plain", messages: plain, opts: []llms.CallOption{llms.WithMaxTokens(8192)}},
		{name: "2_with_system", messages: withSystem, opts: []llms.CallOption{llms.WithMaxTokens(8192)}},
		{name: "3_react_padded", messages: react, opts: []llms.CallOption{llms.WithMaxTokens(8192)}},
		{name: "4_react+T0", messages: react, opts: []llms.CallOption{llms.WithMaxTokens(8192), llms.WithTemperature(0.0)}},
		{name: "5_react+T0+STOP", messages: react, opts: []llms.CallOption{llms.WithMaxTokens(8192), llms.WithTemperature(0.0), llms.WithStopWords([]string{"<observation>"})}},
		{name: "6_react+T0+STOP+stream", messages: react, opts: []llms.CallOption{llms.WithMaxTokens(8192), llms.WithTemperature(0.0), llms.WithStopWords([]string{"<observation>"}), llms.WithStreamingFunc(streamCb)}},
		{name: "7_single+T0+STOP+stream", messages: reactSingle, opts: []llms.CallOption{llms.WithMaxTokens(8192), llms.WithTemperature(0.0), llms.WithStopWords([]string{"<observation>"}), llms.WithStreamingFunc(streamCb)}},
	}

	for _, p := range probes {
		// inputChars = total chars across all message parts; coarse proxy for "how big".
		inputChars := 0
		for _, m := range p.messages {
			for _, part := range m.Parts {
				if tp, ok := part.(llms.TextContent); ok {
					inputChars += len(tp.Text)
				}
			}
		}

		opts := append([]llms.CallOption{llms.WithModel("gemini-2.5-flash")}, p.opts...)
		resp, err := client.GenerateContent(context.Background(), p.messages, opts...)
		if err != nil {
			t.Logf("probe=%-18s ERROR err=%v", p.name, err)
			continue
		}
		if resp == nil || len(resp.Choices) == 0 {
			t.Logf("probe=%-18s NIL_RESPONSE inputChars=%d", p.name, inputChars)
			continue
		}
		c := resp.Choices[0]
		preview := strings.ReplaceAll(c.Content, "\n", " ")
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		var promptTok, outTok, thinkTok int
		if c.GenerationInfo != nil {
			if v, ok := c.GenerationInfo["PromptTokens"].(int32); ok {
				promptTok = int(v)
			}
			if v, ok := c.GenerationInfo["CompletionTokens"].(int32); ok {
				outTok = int(v)
			}
			if v, ok := c.GenerationInfo["ThinkingTokens"].(int); ok {
				thinkTok = v
			}
		}
		t.Logf("probe=%-18s OK inputChars=%5d stopReason=%s promptTok=%d outTok=%d thinkTok=%d contentLen=%d preview=%q",
			p.name, inputChars, c.StopReason, promptTok, outTok, thinkTok, len(c.Content), preview)
	}
}

// TestGemini25FlashRealPromptReproducer takes the REAL ReAct planner system
// prompt (planner_react_3_base.txt) plus the live human message dumped from
// the failing test, and replays it directly against gemini-2.5-flash. This
// isolates the bug: if Gemini still returns empty, we've reproduced outside
// the agent flow and can iteratively trim the prompt to find the trigger.
//
//	LLM_PROVIDER_API_KEY=$(grep ^LLM_PROVIDER_API_KEY .env | cut -d= -f2- | tr -d '"') \
//	  go test -tags=e2e -run TestGemini25FlashRealPromptReproducer ./agents -v -timeout 120s
func TestGemini25FlashRealPromptReproducer(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}

	// The actual ReAct planner system prompt the agent uses.
	systemBytes, err := readReact3BaseForProbe()
	require.NoError(t, err, "read planner_react_3_base.txt")

	// The live human message captured from the failing run (see
	// /tmp/nb_empty_content_gemini-2.5-flash_*.txt). Trimmed to the essential
	// shape: today's date + skill-lists + empty task_context + a question.
	liveHuman := `
**TODAY's Date:** June 04, 2026

<skill-lists>
Additional knowledge bases available for this account. Relevant knowledge has already been retrieved for you above; use the load_skills tool to load one of these by name ONLY if you need expert guidance the retrieved knowledge does not cover.
name: redis_internal_troubleshooting - description: Internal Nudgebee guide for diagnosing and resolving Redis OOM (Out of Memory) issues.
name: rabbitmq_internal_guide - description: Guide for fixing RabbitMQ queue bottlenecks and memory watermark blocks.
name: postgres_performance_tuning - description: Internal Nudgebee guidelines for Postgres query optimization and connection pool management.
</skill-lists>

<task_context>
**Previous Conversation Context:** 
**Previous Messages (History):**

</task_context>


<question>Hi there, what can you help me with?</question>`

	client, err := googleai.New(context.Background(), googleai.WithAPIKey(apiKey), googleai.WithDefaultModel("gemini-2.5-flash"))
	require.NoError(t, err)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, string(systemBytes)),
		llms.TextParts(llms.ChatMessageTypeHuman, liveHuman),
	}

	// Match the agent's exact call options (planner_react_3.go:1471).
	streamCb := func(_ context.Context, _ []byte) error { return nil }
	opts := []llms.CallOption{
		llms.WithModel("gemini-2.5-flash"),
		llms.WithMaxTokens(8192),
		llms.WithTemperature(0.0),
		llms.WithStopWords([]string{"<observation>"}),
		llms.WithStreamingFunc(streamCb),
	}

	resp, err := client.GenerateContent(context.Background(), messages, opts...)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.Choices)
	c := resp.Choices[0]

	var outTok int
	if c.GenerationInfo != nil {
		if v, ok := c.GenerationInfo["CompletionTokens"].(int32); ok {
			outTok = int(v)
		}
	}
	t.Logf("RESULT model=gemini-2.5-flash stopReason=%s outTok=%d contentLen=%d", c.StopReason, outTok, len(c.Content))
	if outTok == 0 || len(c.Content) == 0 {
		t.Logf("REPRODUCED: empty content from gemini-2.5-flash on real planner prompt + live human msg")
	} else {
		t.Logf("DID NOT REPRODUCE: gemini-2.5-flash returned content — bug requires different conditions")
	}
}

// TestGemini25FlashBisectSystemPrompt halves the planner system prompt and
// tests each half + the live human message. Whichever half reproduces empty
// content contains the trigger. Run iteratively, halving again each round.
func TestGemini25FlashBisectSystemPrompt(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}
	bytes, err := readReact3BaseForProbe()
	require.NoError(t, err)
	full := string(bytes)
	half := len(full) / 2

	liveHuman := `
**TODAY's Date:** June 04, 2026
<task_context></task_context>
<question>Hi there, what can you help me with?</question>`

	client, err := googleai.New(context.Background(), googleai.WithAPIKey(apiKey), googleai.WithDefaultModel("gemini-2.5-flash"))
	require.NoError(t, err)
	streamCb := func(_ context.Context, _ []byte) error { return nil }
	opts := []llms.CallOption{
		llms.WithModel("gemini-2.5-flash"),
		llms.WithMaxTokens(8192),
		llms.WithTemperature(0.0),
		llms.WithStopWords([]string{"<observation>"}),
		llms.WithStreamingFunc(streamCb),
	}

	probe := func(name, sys string) {
		messages := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, sys),
			llms.TextParts(llms.ChatMessageTypeHuman, liveHuman),
		}
		resp, err := client.GenerateContent(context.Background(), messages, opts...)
		if err != nil {
			t.Logf("probe=%-12s ERROR %v", name, err)
			return
		}
		c := resp.Choices[0]
		t.Logf("probe=%-12s sysChars=%6d stopReason=%s contentLen=%d", name, len(sys), c.StopReason, len(c.Content))
	}

	probe("first_half", full[:half])
	probe("second_half", full[half:])
	probe("first_quarter", full[:half/2])
	probe("third_quarter", full[half:half+half/2])
}

// TestGemini25FlashAutoBisect recursively halves the planner system prompt
// to find the minimal substring that still reproduces empty content.
// Single test, ~15 API calls, ~45s total.
func TestGemini25FlashAutoBisect(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}
	bytes, err := readReact3BaseForProbe()
	require.NoError(t, err)
	full := string(bytes)

	liveHuman := `
**TODAY's Date:** June 04, 2026
<task_context></task_context>
<question>Hi there, what can you help me with?</question>`

	client, err := googleai.New(context.Background(), googleai.WithAPIKey(apiKey), googleai.WithDefaultModel("gemini-2.5-flash"))
	require.NoError(t, err)
	streamCb := func(_ context.Context, _ []byte) error { return nil }
	baseOpts := []llms.CallOption{
		llms.WithModel("gemini-2.5-flash"),
		llms.WithMaxTokens(8192),
		llms.WithTemperature(0.0),
		llms.WithStopWords([]string{"<observation>"}),
		llms.WithStreamingFunc(streamCb),
	}

	// Returns true if Gemini emits empty content (the bug reproducer).
	isEmpty := func(sys string) bool {
		messages := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, sys),
			llms.TextParts(llms.ChatMessageTypeHuman, liveHuman),
		}
		resp, err := client.GenerateContent(context.Background(), messages, baseOpts...)
		if err != nil || resp == nil || len(resp.Choices) == 0 {
			return false
		}
		return len(resp.Choices[0].Content) == 0
	}

	// Binary search: find smallest prefix that still reproduces empty.
	lo, hi := 0, len(full)
	for hi-lo > 200 {
		mid := (lo + hi) / 2
		empty := isEmpty(full[:mid])
		t.Logf("bisect prefix[0:%d] empty=%v", mid, empty)
		if empty {
			hi = mid // narrower works → trigger is in [0:mid)
		} else {
			lo = mid // need more → trigger is in [mid:hi)
		}
	}
	t.Logf("MINIMAL FAILING PREFIX: %d chars", hi)
	t.Logf("==== last 500 chars of failing prefix ====\n%s", full[max(0, hi-500):hi])
	t.Logf("==== next 500 chars (the trigger likely starts around here) ====\n%s", full[hi:min(len(full), hi+500)])
}

// TestGemini25FlashStopWordHypothesis confirms the bug is the interaction
// between WithStopWords(["<observation>"]) and the prompt literally
// mentioning <observation>. Test: same prompt, no stop word → should
// produce content.
func TestGemini25FlashStopWordHypothesis(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}
	bytes, err := readReact3BaseForProbe()
	require.NoError(t, err)
	full := string(bytes)
	liveHuman := `<question>Hi there, what can you help me with?</question>`
	client, err := googleai.New(context.Background(), googleai.WithAPIKey(apiKey), googleai.WithDefaultModel("gemini-2.5-flash"))
	require.NoError(t, err)
	streamCb := func(_ context.Context, _ []byte) error { return nil }
	run := func(name string, opts ...llms.CallOption) {
		messages := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, full),
			llms.TextParts(llms.ChatMessageTypeHuman, liveHuman),
		}
		resp, err := client.GenerateContent(context.Background(), messages, opts...)
		if err != nil {
			t.Logf("%-30s ERROR %v", name, err)
			return
		}
		c := resp.Choices[0]
		preview := strings.ReplaceAll(c.Content, "\n", " ")
		if len(preview) > 80 {
			preview = preview[:80] + "…"
		}
		t.Logf("%-30s stopReason=%s contentLen=%d preview=%q", name, c.StopReason, len(c.Content), preview)
	}
	base := []llms.CallOption{
		llms.WithModel("gemini-2.5-flash"),
		llms.WithMaxTokens(8192),
		llms.WithTemperature(0.0),
		llms.WithStreamingFunc(streamCb),
	}
	run("WITH stop=<observation>", append(base, llms.WithStopWords([]string{"<observation>"}))...)
	run("WITHOUT stop", base...)
	run("WITH stop=NEVER_EMIT_THIS", append(base, llms.WithStopWords([]string{"NEVER_EMIT_THIS_STRING"}))...)
}

// TestGemini25FlashContentMutations isolates which specific content trait
// in the prompt triggers empty output on gemini-2.5-flash.
func TestGemini25FlashContentMutations(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}
	bytes, err := readReact3BaseForProbe()
	require.NoError(t, err)
	full := string(bytes)
	liveHuman := `<question>Hi there, what can you help me with?</question>`
	client, err := googleai.New(context.Background(), googleai.WithAPIKey(apiKey), googleai.WithDefaultModel("gemini-2.5-flash"))
	require.NoError(t, err)
	streamCb := func(_ context.Context, _ []byte) error { return nil }
	opts := []llms.CallOption{
		llms.WithModel("gemini-2.5-flash"),
		llms.WithMaxTokens(8192),
		llms.WithTemperature(0.0),
		llms.WithStreamingFunc(streamCb),
	}
	run := func(name, sys string) {
		messages := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, sys),
			llms.TextParts(llms.ChatMessageTypeHuman, liveHuman),
		}
		resp, err := client.GenerateContent(context.Background(), messages, opts...)
		if err != nil {
			t.Logf("%-40s ERROR %v", name, err)
			return
		}
		c := resp.Choices[0]
		t.Logf("%-40s contentLen=%-4d stopReason=%s", name, len(c.Content), c.StopReason)
	}

	// 0: baseline — full prompt unmodified (reproduces empty)
	run("0_full_prompt", full)
	// 1: replace literal word "STOP" (uppercase) with "halt"
	run("1_no_STOP_word", strings.ReplaceAll(full, "STOP", "halt"))
	// 2: rename <observation> to <obs> everywhere
	run("2_obs_renamed", strings.ReplaceAll(full, "<observation>", "<obs>"))
	// 3: fill all {{.placeholder}} template vars with placeholders so they're not empty
	r := regexp.MustCompile(`\{\{[.][^}]+\}\}`)
	run("3_no_template_vars", r.ReplaceAllString(full, "(no special rules apply)"))
	// 4: strip the entire "One step at a time" block + the strict constraints near the trigger
	stripped := full
	if i := strings.Index(stripped, "**One step at a time:**"); i >= 0 {
		j := strings.Index(stripped[i:], "{{.context_management_rules}}")
		if j > 0 {
			stripped = stripped[:i] + stripped[i+j:]
		}
	}
	run("4_no_one_step_block", stripped)
	// 5: remove all uppercase "MUST" / "MUST NOT" / "ONLY" emphasis
	mutated := full
	for _, s := range []string{"MUST NOT", "MUST", "ONLY", "STOP", "NEVER"} {
		mutated = strings.ReplaceAll(mutated, s, strings.ToLower(s))
	}
	run("5_no_uppercase_emphasis", mutated)
}

// TestGemini25FlashSizeVsContent disentangles "is it the size of the prompt"
// vs "is it the content"? Test different chunks of different sizes.
func TestGemini25FlashSizeVsContent(t *testing.T) {
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_PROVIDER_API_KEY required")
	}
	bytes, err := readReact3BaseForProbe()
	require.NoError(t, err)
	full := string(bytes)
	liveHuman := `<question>Hi there, what can you help me with?</question>`
	client, err := googleai.New(context.Background(), googleai.WithAPIKey(apiKey), googleai.WithDefaultModel("gemini-2.5-flash"))
	require.NoError(t, err)
	streamCb := func(_ context.Context, _ []byte) error { return nil }
	opts := []llms.CallOption{
		llms.WithModel("gemini-2.5-flash"),
		llms.WithMaxTokens(8192),
		llms.WithTemperature(0.0),
		llms.WithStreamingFunc(streamCb),
	}
	run := func(name, sys string) {
		messages := []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, sys),
			llms.TextParts(llms.ChatMessageTypeHuman, liveHuman),
		}
		resp, err := client.GenerateContent(context.Background(), messages, opts...)
		if err != nil {
			t.Logf("%-32s ERROR %v", name, err)
			return
		}
		c := resp.Choices[0]
		t.Logf("%-32s sysLen=%-5d contentLen=%-4d stopReason=%s", name, len(sys), len(c.Content), c.StopReason)
	}

	// A. The trigger-window alone (197 chars: 4122-4319). Does it fail by itself?
	run("A_trigger_window_only", full[4122:4319])
	// B. First 4319 chars (known failing baseline)
	run("B_prefix_4319", full[:4319])
	// C. Last 12567 chars (known working second half)
	run("C_last_half_works", full[12567:])
	// D. Last 4319 chars (size matched to failing prefix)
	run("D_last_4319", full[len(full)-4319:])
	// E. Middle 4319 chars
	mid := len(full) / 2
	run("E_middle_4319", full[mid-2160:mid+2159])
	// F. First 4319 chars reversed character-by-character. Same content, different order.
	reversed := []byte(full[:4319])
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	run("F_first_4319_reversed", string(reversed))
}

// readReact3BaseForProbe resolves the ReAct3 base prompt from the versioned tree.
// It replaces a direct os.ReadFile of prompts_repo/planner_react_3_base.txt, which
// no longer exists.
func readReact3BaseForProbe() ([]byte, error) {
	nbprompts.SetGlobalLoaderForTesting(nbprompts.NewLoaderForTesting())
	body := nbprompts.GetPrompt(context.Background(), nbprompts.PromptReact3Base, "")
	if body == "" {
		return nil, fmt.Errorf("react_3_base prompt resolved empty")
	}
	return []byte(body), nil
}
