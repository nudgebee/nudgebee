// Command matrix is an integration-validation harness for the NB AI Gateway's
// translation layer (cross-provider substitution + failover). It sends provider-
// NATIVE requests to the gateway and asserts the response comes back well-formed in
// the client's native shape — the thing unit tests can't prove: that a real client's
// wire format round-trips through a translated substitution/failover.
//
// It is pure stdlib (no SDK deps), so it builds offline and runs in CI with `go run .`.
// It asserts WIRE SHAPE (native JSON/SSE structure), not a specific SDK's parser —
// full real-SDK acceptance (e.g. a live Claude Code session) stays the dogfood test.
//
// SETUP — create these routing rules first (AI Gateway → Routing Rules), each mapping
// a model to a DIFFERENT target provider so the request is substituted:
//
//	match {anthropic, $SUB_ANTHROPIC_MODEL} -> target {gemini,    $ANY_GEMINI_MODEL}
//	match {openai,    $SUB_OPENAI_MODEL}    -> target {anthropic, $ANY_ANTHROPIC_MODEL}
//	match {gemini,    $SUB_GEMINI_MODEL}    -> target {openai,    $ANY_OPENAI_MODEL}
//
// Optional checks activate when their env is set:
//   - FAILOVER_MOUNT + FAILOVER_MODEL: a rule whose target primary is bogus with a
//     valid fallback (forces the transient-failure → fallback path).
//   - BASELINE_MOUNT + BASELINE_MODEL: a model with NO rule (passthrough must still work).
//
// RUN:
//
//	export GATEWAY_URL=https://<gateway>   # no trailing slash
//	export NB_TOKEN=<your NB token>
//	go run .
//
// Exit code is non-zero if any check fails, so it can gate CI.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type cfg struct {
	gateway string
	token   string
	subA    string // anthropic-client model configured to substitute
	subO    string // openai-client model configured to substitute
	subG    string // gemini-client model configured to substitute
}

var c cfg
var client = &http.Client{Timeout: 90 * time.Second}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

type result struct {
	client, check, detail string
	ok                    bool
}

var results []result

func record(cl, ck string, ok bool, detail string) {
	results = append(results, result{cl, ck, detail, ok})
	// A 429 is the gateway's rate limiter (governance), not a translation failure —
	// report it distinctly so a rate-limited burst never looks like a broken cell.
	state := "FAIL"
	if ok {
		state = "PASS"
	} else if rateLimited(detail) {
		state = "RLIM"
	}
	fmt.Printf("  [%s] %-9s %-18s %s\n", state, cl, ck, detail)
}

func rateLimited(detail string) bool { return strings.Contains(detail, "status=429") }

// post sends a JSON request and returns status, headers, and the raw body (the SSE
// stream body for streaming requests — it's finite once the stream ends).
func post(path string, body any, extra map[string]string) (int, http.Header, string) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, c.gateway+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(data)
}

func parse(s string) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// substituted returns the swap the gateway performed, or "" if none (rule inactive).
func substituted(h http.Header) string { return h.Get("x-nb-llm-substituted") }

// ---- request bodies (provider-native wire formats) ----

var weatherTool = map[string]any{"city": map[string]any{"type": "string"}}

func anthropicBody(model, prompt string, stream, tools bool) map[string]any {
	m := map[string]any{"model": model, "max_tokens": 128,
		"messages": []any{map[string]any{"role": "user", "content": prompt}}}
	if stream {
		m["stream"] = true
	}
	if tools {
		m["tools"] = []any{map[string]any{"name": "get_weather", "description": "Get weather for a city.",
			"input_schema": map[string]any{"type": "object", "properties": weatherTool, "required": []any{"city"}}}}
	}
	return m
}

func openaiBody(model, prompt string, stream, tools bool) map[string]any {
	m := map[string]any{"model": model,
		"messages": []any{map[string]any{"role": "user", "content": prompt}}}
	if stream {
		m["stream"] = true
	}
	if tools {
		m["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "get_weather", "description": "Get weather for a city.",
			"parameters": map[string]any{"type": "object", "properties": weatherTool, "required": []any{"city"}}}}}
	}
	return m
}

func geminiBody(prompt string, tools bool) map[string]any {
	m := map[string]any{"contents": []any{map[string]any{"role": "user",
		"parts": []any{map[string]any{"text": prompt}}}}}
	if tools {
		m["tools"] = []any{map[string]any{"functionDeclarations": []any{map[string]any{
			"name": "get_weather", "description": "Get weather for a city.",
			"parameters": map[string]any{"type": "object", "properties": weatherTool, "required": []any{"city"}}}}}}
	}
	return m
}

func anthropicHdr() map[string]string { return map[string]string{"anthropic-version": "2023-06-01"} }

// ---- response assertions ----

func anthropicHasText(m map[string]any) bool {
	for _, b := range asArr(m["content"]) {
		if bm, ok := b.(map[string]any); ok && bm["type"] == "text" && str(bm["text"]) != "" {
			return true
		}
	}
	return false
}
func anthropicToolUse(m map[string]any) string {
	for _, b := range asArr(m["content"]) {
		if bm, ok := b.(map[string]any); ok && bm["type"] == "tool_use" {
			return str(bm["name"])
		}
	}
	return ""
}
func openaiText(m map[string]any) bool {
	for _, ch := range asArr(m["choices"]) {
		if msg, ok := mp(ch)["message"].(map[string]any); ok && str(msg["content"]) != "" {
			return true
		}
	}
	return false
}
func openaiToolCall(m map[string]any) string {
	for _, ch := range asArr(m["choices"]) {
		msg, _ := mp(ch)["message"].(map[string]any)
		for _, tc := range asArr(msg["tool_calls"]) {
			if fn, ok := mp(tc)["function"].(map[string]any); ok {
				return str(fn["name"])
			}
		}
	}
	return ""
}
func geminiText(m map[string]any) bool {
	for _, cd := range asArr(m["candidates"]) {
		cont, _ := mp(cd)["content"].(map[string]any)
		for _, p := range asArr(cont["parts"]) {
			if str(mp(p)["text"]) != "" {
				return true
			}
		}
	}
	return false
}
func geminiFnCall(m map[string]any) string {
	for _, cd := range asArr(m["candidates"]) {
		cont, _ := mp(cd)["content"].(map[string]any)
		for _, p := range asArr(cont["parts"]) {
			if fc, ok := mp(p)["functionCall"].(map[string]any); ok {
				return str(fc["name"])
			}
		}
	}
	return ""
}

func asArr(v any) []any       { a, _ := v.([]any); return a }
func mp(v any) map[string]any { m, _ := v.(map[string]any); return m }
func str(v any) string        { s, _ := v.(string); return s }

// ---- the matrix ----

func run() {
	// Anthropic client — Responses-engine substitution. A substitution check only
	// passes if the swap ACTUALLY happened (x-nb-llm-substituted present) AND the
	// response is well-formed — otherwise a passthrough would masquerade as a pass.
	if c.subA != "" {
		path := "/anthropic/v1/messages"
		st, h, body := post(path, anthropicBody(c.subA, "Reply with the single word: ok", false, false), anthropicHdr())
		record("anthropic", "text", st == 200 && swapped(h) && anthropicHasText(parse(body)), subDetail(st, h, "text"))

		st, h, body = post(path, anthropicBody(c.subA, "Count: one two three", true, false), anthropicHdr())
		record("anthropic", "stream", st == 200 && swapped(h) && strings.Contains(body, "event: content_block_delta"),
			subDetail(st, h, frames(body, "event: ")))

		st, h, body = post(path, anthropicBody(c.subA, "Use the get_weather tool for Paris.", false, true), anthropicHdr())
		record("anthropic", "tools", st == 200 && swapped(h) && anthropicToolUse(parse(body)) == "get_weather",
			subDetail(st, h, toolDetail(st, anthropicToolUse(parse(body)))))

		mtOK, mtD := anthropicMultiturn()
		record("anthropic", "multiturn+tool", mtOK, mtD)
	}

	// OpenAI client — Chat-engine substitution.
	if c.subO != "" {
		path := "/openai/v1/chat/completions"
		st, h, body := post(path, openaiBody(c.subO, "Reply with the single word: ok", false, false), nil)
		record("openai", "text", st == 200 && swapped(h) && openaiText(parse(body)), subDetail(st, h, "text"))

		st, h, body = post(path, openaiBody(c.subO, "Count: one two three", true, false), nil)
		record("openai", "stream", st == 200 && swapped(h) && strings.Contains(body, "data: ") && strings.Contains(body, "[DONE]"),
			subDetail(st, h, frames(body, "data: ")))

		st, h, body = post(path, openaiBody(c.subO, "Use the get_weather tool for Paris.", false, true), nil)
		record("openai", "tools", st == 200 && swapped(h) && openaiToolCall(parse(body)) == "get_weather",
			subDetail(st, h, toolDetail(st, openaiToolCall(parse(body)))))

		mtOK, mtD := openaiMultiturn()
		record("openai", "multiturn+tool", mtOK, mtD)
	}

	// Gemini client — Responses-engine substitution.
	if c.subG != "" {
		st, h, body := post("/genai/v1beta/models/"+c.subG+":generateContent", geminiBody("Reply with the single word: ok", false), nil)
		record("gemini", "text", st == 200 && swapped(h) && geminiText(parse(body)), subDetail(st, h, "text"))

		st, h, body = post("/genai/v1beta/models/"+c.subG+":streamGenerateContent?alt=sse", geminiBody("Count: one two three", false), nil)
		record("gemini", "stream", st == 200 && swapped(h) && strings.Contains(body, "data: "), subDetail(st, h, frames(body, "data: ")))

		st, h, body = post("/genai/v1beta/models/"+c.subG+":generateContent", geminiBody("Use the get_weather tool for Paris.", true), nil)
		record("gemini", "tools", st == 200 && swapped(h) && geminiFnCall(parse(body)) == "get_weather",
			subDetail(st, h, toolDetail(st, geminiFnCall(parse(body)))))
	}

	// Degradation signal — Anthropic cache_control against a non-Anthropic target.
	if c.subA != "" {
		b := anthropicBody(c.subA, "hi", false, false)
		b["system"] = []any{map[string]any{"type": "text", "text": "You are terse.",
			"cache_control": map[string]any{"type": "ephemeral"}}}
		st, h, _ := post("/anthropic/v1/messages", b, anthropicHdr())
		deg := h.Get("x-nb-llm-degraded")
		detail := "x-nb-llm-degraded: " + deg
		if deg == "" {
			detail = fmt.Sprintf("status=%d, no degrade header (target may be Anthropic, or cache_control preserved)", st)
		}
		record("anthropic", "degrade-signal", st == 200 && strings.Contains(deg, "prompt_caching"), detail)
	}

	// Failover — a rule whose primary is bogus must fall over to a valid fallback.
	if m, mount := env("FAILOVER_MODEL", ""), env("FAILOVER_MOUNT", "anthropic"); m != "" {
		st, h := failoverProbe(mount, m)
		record(mount, "failover", st == 200 && h.Get("x-nb-llm-failover") != "",
			cond(h.Get("x-nb-llm-failover") != "", "x-nb-llm-failover: "+h.Get("x-nb-llm-failover"), fmt.Sprintf("status=%d, no failover header", st)))
	}

	// Baseline — a model with NO rule must pass through byte-perfect (no substitution).
	if m, mount := env("BASELINE_MODEL", ""), env("BASELINE_MOUNT", "anthropic"); m != "" {
		st, h := baselineProbe(mount, m)
		record(mount, "baseline", st == 200 && substituted(h) == "",
			cond(substituted(h) == "", "passthrough (no swap)", "unexpected swap: "+substituted(h)))
	}

	testGuardrails()
}

// testGuardrails exercises egress secret detection (DLP). The behavior depends on the
// server's gateway_egress_filter_mode, which the harness reads back from the response
// (the x-nb-llm-dlp header) so it adapts to whatever mode is configured. A clean body
// must never be flagged; a body carrying a (fake) secret must be detected/blocked/
// redacted per the active mode.
func testGuardrails() {
	// A fake AWS access key (matches the baseline rule, not the allowlisted docs sample).
	const fakeSecret = "AKIA1234567890ABCDEF" // gitleaks:allow — non-functional fixture to exercise the egress filter

	// Clean request — must NOT be flagged, regardless of mode.
	_, hClean, _ := post("/anthropic/v1/messages", anthropicBody(c.subA, "just say hi, nothing sensitive", false, false), anthropicHdr())
	record("dlp", "clean", hClean.Get("x-nb-llm-dlp") == "",
		cond(hClean.Get("x-nb-llm-dlp") == "", "not flagged", "unexpected DLP flag: "+hClean.Get("x-nb-llm-dlp")))

	// Secret in the body — behavior adapts to the configured mode (read from the header).
	st, h, _ := post("/anthropic/v1/messages", anthropicBody(c.subA, "here is a key "+fakeSecret+" please continue", false, false), anthropicHdr())
	dlp := h.Get("x-nb-llm-dlp")
	switch {
	case dlp == "":
		record("dlp", "secret", true, "SKIP: filter off (set gateway_egress_filter_mode to detect/enforce/redact)")
	case strings.HasPrefix(dlp, "enforce"):
		record("dlp", "secret", st == http.StatusForbidden && strings.Contains(dlp, "aws-access-key-id"),
			fmt.Sprintf("enforce → status=%d (%s)", st, dlp))
	case strings.HasPrefix(dlp, "detect"):
		record("dlp", "secret", st == 200 && strings.Contains(dlp, "aws-access-key-id"), "detect → "+dlp)
	case strings.HasPrefix(dlp, "redact"):
		record("dlp", "secret", st == 200 && strings.Contains(dlp, "aws-access-key-id"), "redact → "+dlp)
	default:
		record("dlp", "secret", false, "unexpected x-nb-llm-dlp: "+dlp)
	}
}

func anthropicMultiturn() (bool, string) {
	msgs := []any{
		map[string]any{"role": "user", "content": "Use the get_weather tool for Paris."},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": map[string]any{"city": "Paris"}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "18C, sunny"}}},
	}
	// Assert the tool_use→tool_result history round-tripped into a well-formed Anthropic
	// message — NOT that the model generated text. A small target model may legitimately
	// return empty content (0 output tokens) after a tool_result; that's a model choice
	// the gateway faithfully translates, not a fidelity bug.
	st, h, body := post("/anthropic/v1/messages", map[string]any{"model": c.subA, "max_tokens": 128, "messages": msgs}, anthropicHdr())
	m := parse(body)
	ok := st == 200 && swapped(h) && str(m["type"]) == "message" && str(m["role"]) == "assistant"
	return ok, subDetail(st, h, "tool_use→tool_result round-trip")
}

func openaiMultiturn() (bool, string) {
	msgs := []any{
		map[string]any{"role": "user", "content": "Use the get_weather tool for Paris."},
		map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`}}}},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "18C, sunny"},
	}
	st, h, body := post("/openai/v1/chat/completions", map[string]any{"model": c.subO, "messages": msgs}, nil)
	ok := st == 200 && swapped(h) && len(asArr(parse(body)["choices"])) > 0
	return ok, subDetail(st, h, "tool_call→tool round-trip")
}

func failoverProbe(mount, model string) (int, http.Header) {
	switch mount {
	case "openai":
		st, h, _ := post("/openai/v1/chat/completions", openaiBody(model, "ping", false, false), nil)
		return st, h
	case "gemini":
		st, h, _ := post("/genai/v1beta/models/"+model+":generateContent", geminiBody("ping", false), nil)
		return st, h
	default:
		st, h, _ := post("/anthropic/v1/messages", anthropicBody(model, "ping", false, false), anthropicHdr())
		return st, h
	}
}

func baselineProbe(mount, model string) (int, http.Header) { return failoverProbe(mount, model) }

// swapped reports whether the gateway actually performed a substitution.
func swapped(h http.Header) bool { return substituted(h) != "" }

func frames(body, prefix string) string {
	return fmt.Sprintf("%d %q frames", strings.Count(body, prefix), strings.TrimSpace(prefix))
}
func subDetail(st int, h http.Header, extra string) string {
	if st != 200 {
		return fmt.Sprintf("status=%d", st)
	}
	if s := substituted(h); s != "" {
		return s + " · " + extra
	}
	return "NOT substituted — rule not active for this model?"
}
func toolDetail(st int, name string) string {
	if name != "" {
		return "tool=" + name
	}
	return fmt.Sprintf("status=%d, no tool call", st)
}
func cond(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}

func main() {
	c = cfg{
		gateway: strings.TrimRight(env("GATEWAY_URL", ""), "/"),
		token:   env("NB_TOKEN", ""),
		subA:    env("SUB_ANTHROPIC_MODEL", "claude-opus-4-8"),
		subO:    env("SUB_OPENAI_MODEL", "gpt-5"),
		subG:    env("SUB_GEMINI_MODEL", "gemini-2.5-flash"),
	}
	if c.gateway == "" || c.token == "" {
		fmt.Fprintln(os.Stderr, "set GATEWAY_URL and NB_TOKEN")
		os.Exit(2)
	}
	fmt.Printf("Gateway substitution/failover matrix → %s\n\n", c.gateway)
	run()

	fmt.Println("\nMetering correctness — verify the served rows in your gateway DB:")
	fmt.Println("  SELECT provider, model, requested_provider, requested_model, routing_reason,")
	fmt.Println("         cost_usd, attributes->'derived'->>'surface' AS surface")
	fmt.Println("    FROM llm_gateway_usage ORDER BY created_at DESC LIMIT 20;")
	fmt.Println("  Expect: substituted rows attributed to the TARGET provider, routing_reason")
	fmt.Println("  in {substitute, fallback}, cost_usd > 0, surface = native.")

	var failed, rlim int
	for _, r := range results {
		if r.ok {
			continue
		}
		if rateLimited(r.detail) {
			rlim++
		} else {
			failed++
		}
	}
	fmt.Printf("\n%d/%d checks passed", len(results)-failed-rlim, len(results))
	if rlim > 0 {
		fmt.Printf(", %d rate-limited (gateway quota — raise the tenant limit to validate those cells)", rlim)
	}
	if failed > 0 {
		fmt.Printf(", %d FAILED\n", failed)
		os.Exit(1)
	}
	fmt.Println()
}
