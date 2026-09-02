package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm-gateway/config"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirstUserMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai string content", `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"what is up"}]}`, "what is up"},
		{"anthropic text blocks", `{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":"there"}]}]}`, "hello there"},
		{"gemini contents/parts", `{"contents":[{"role":"user","parts":[{"text":"gem question"}]}]}`, "gem question"},
		{"gemini roleless", `{"contents":[{"parts":[{"text":"no role"}]}]}`, "no role"},
		{"whitespace collapsed", "{\"messages\":[{\"role\":\"user\",\"content\":\"line1\\n\\n   line2\"}]}", "line1 line2"},
		{"wrapper tokens kept raw", `{"messages":[{"role":"user","content":"<session> hi"}]}`, "<session> hi"},
		{"no user message", `{"messages":[{"role":"system","content":"sys"}]}`, ""},
		{"no messages field", `{"model":"x"}`, ""},
		{"invalid json", `garbage`, ""},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, firstUserMessage([]byte(c.body)))
		})
	}

	// Length cap: 300-rune content is truncated to firstMessageMaxLen runes.
	long := `{"messages":[{"role":"user","content":"` + strings.Repeat("a", 300) + `"}]}`
	assert.Len(t, []rune(firstUserMessage([]byte(long))), firstMessageMaxLen)
}

func withCapture(t *testing.T, on bool) {
	t.Helper()
	prevC := config.Config.CaptureAttributes
	prevH := config.Config.SessionHeader
	config.Config.CaptureAttributes = on
	config.Config.SessionHeader = "x-nb-session-id"
	t.Cleanup(func() {
		config.Config.CaptureAttributes = prevC
		config.Config.SessionHeader = prevH
	})
}

func TestResolveSession_HeaderWins(t *testing.T) {
	withCapture(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
	c.Request.Header.Set("x-nb-session-id", "sess-1")

	body := []byte(`{"metadata":{"user_id":"user-9"}}`)
	id, src := resolveSession(c, body, "fp-abc")
	assert.Equal(t, "sess-1", id, "header wins over metadata + fingerprint")
	assert.Equal(t, "header", src)
}

func TestResolveSession_FallsBackToMetadataUserID(t *testing.T) {
	withCapture(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)

	id, src := resolveSession(c, []byte(`{"metadata":{"user_id":"user-9"}}`), "fp-abc")
	assert.Equal(t, "user-9", id, "metadata wins over fingerprint")
	assert.Equal(t, "metadata.user_id", src)
}

func TestResolveSession_NestedSessionIDInUserID(t *testing.T) {
	withCapture(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)

	// Claude Code encodes user_id as a JSON blob carrying the real session_id.
	uid := `{\"device_id\":\"254a15e1\",\"account_uuid\":\"\",\"session_id\":\"85692b00-741d-436a-9189-3db01c5200fe\"}`
	id, src := resolveSession(c, []byte(`{"metadata":{"user_id":"`+uid+`"}}`), "fp-abc")
	assert.Equal(t, "85692b00-741d-436a-9189-3db01c5200fe", id, "nested session_id wins over the raw blob")
	assert.Equal(t, "metadata.session_id", src)

	// A JSON user_id with no session_id must NOT become a raw-JSON session id;
	// it falls through to the inferred fingerprint.
	uid2 := `{\"device_id\":\"abc\"}`
	id2, src2 := resolveSession(c, []byte(`{"metadata":{"user_id":"`+uid2+`"}}`), "fp-abc")
	assert.Equal(t, "fp-abc", id2)
	assert.Equal(t, "inferred", src2)
}

func TestResolveSession_InferredFromFingerprint(t *testing.T) {
	withCapture(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)

	// No header, no metadata → fall back to the inferred prefix fingerprint.
	id, src := resolveSession(c, []byte(`{"model":"x"}`), "fp-abc")
	assert.Equal(t, "fp-abc", id)
	assert.Equal(t, "inferred", src)
}

func TestResolveSession_NoneWhenAbsent(t *testing.T) {
	withCapture(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)

	id, src := resolveSession(c, []byte(`{"model":"x"}`), "")
	assert.Empty(t, id)
	assert.Empty(t, src)
}

func TestExtractRequestAttributes_StructureOnly(t *testing.T) {
	withCapture(t, true)
	body := []byte(`{
		"model":"claude-haiku-4-5","max_tokens":64,"temperature":0.5,"stream":true,
		"system":[{"type":"text","text":"secret policy"}],
		"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"yo"}],
		"tools":[{"name":"get_weather"},{"name":"search"}]
	}`)
	a := extractRequestAttributes(schemas.Anthropic, body)

	assert.Equal(t, 64, a["max_tokens"])
	assert.Equal(t, 0.5, a["temperature"])
	assert.Equal(t, true, a["stream"])
	assert.Equal(t, 2, a["message_count"])
	assert.Equal(t, true, a["has_system"])
	assert.Equal(t, []string{"get_weather", "search"}, a["tool_names"])
	assert.Equal(t, 2, a["tool_count"])
	// Message/system CONTENT must NOT be captured — only structure (counts/flags).
	assert.NotContains(t, a, "messages")
	assert.NotContains(t, a, "system")
	for _, v := range a {
		assert.NotEqual(t, "secret policy", v)
		assert.NotEqual(t, "hi", v)
	}
}

func TestExtractRequestAttributes_OpenAIToolShape(t *testing.T) {
	withCapture(t, true)
	body := []byte(`{"model":"gpt-4o","tools":[{"type":"function","function":{"name":"lookup"}}]}`)
	a := extractRequestAttributes(schemas.OpenAI, body)
	assert.Equal(t, []string{"lookup"}, a["tool_names"])
}

func TestExtractRequestAttributes_GeminiToolShape(t *testing.T) {
	withCapture(t, true)
	// Gemini declares many functions under a single tools[].functionDeclarations.
	body := []byte(`{"tools":[{"functionDeclarations":[{"name":"get_weather"},{"name":"search"}]}]}`)
	a := extractRequestAttributes(schemas.Gemini, body)
	assert.Equal(t, []string{"get_weather", "search"}, a["tool_names"])
	assert.Equal(t, 2, a["tool_count"])
}

func TestExtractToolActivity_Anthropic(t *testing.T) {
	// A mid-conversation request: the last assistant turn made two parallel tool_use
	// calls; the following user turn returned results — one of them an error.
	body := []byte(`{
		"model":"claude-haiku-4-5",
		"messages":[
			{"role":"user","content":"weather in SF and NYC?"},
			{"role":"assistant","content":[
				{"type":"text","text":"let me check"},
				{"type":"tool_use","id":"a1","name":"get_weather","input":{"city":"SF"}},
				{"type":"tool_use","id":"a2","name":"get_weather","input":{"city":"NYC"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"a1","content":"72F"},
				{"type":"tool_result","tool_use_id":"a2","is_error":true,"content":"timeout"}
			]}
		]
	}`)
	called, failed := extractToolActivity(body)
	assert.Equal(t, []string{"get_weather", "get_weather"}, called) // 2 calls this turn
	assert.Equal(t, []string{"get_weather"}, failed)                // the a2 error only
}

func TestExtractToolActivity_OpenAICallsNoFailure(t *testing.T) {
	// OpenAI: assistant tool_calls give calls; tool-role results carry no is_error, so
	// no failures are inferred.
	body := []byte(`{
		"messages":[
			{"role":"user","content":"lookup foo"},
			{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup"}}]},
			{"role":"tool","tool_call_id":"c1","content":"error: not found"}
		]
	}`)
	called, failed := extractToolActivity(body)
	assert.Equal(t, []string{"lookup"}, called)
	assert.Empty(t, failed)
}

func TestExtractToolActivity_GeminiCalls(t *testing.T) {
	body := []byte(`{"contents":[
		{"role":"user","parts":[{"text":"weather?"}]},
		{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"SF"}}}]}
	]}`)
	called, failed := extractToolActivity(body)
	assert.Equal(t, []string{"get_weather"}, called)
	assert.Empty(t, failed)
}

func TestExtractToolActivity_NoToolsEmpty(t *testing.T) {
	called, failed := extractToolActivity([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	assert.Empty(t, called)
	assert.Empty(t, failed)
}

func TestExtractRequestAttributes_ToolActivity(t *testing.T) {
	withCapture(t, true)
	body := []byte(`{
		"model":"claude-haiku-4-5","tools":[{"name":"get_weather"}],
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"a1","name":"get_weather","input":{"city":"SF"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"a1","is_error":true,"content":"boom"}]}
		]
	}`)
	a := extractRequestAttributes(schemas.Anthropic, body)
	assert.Equal(t, []string{"get_weather"}, a["called_tools"])
	assert.Equal(t, 1, a["called_count"])
	assert.Equal(t, []string{"get_weather"}, a["failed_tools"])
	assert.Equal(t, 1, a["failed_count"])
	// Tool ARGUMENTS must never be captured — only names.
	for _, v := range a {
		assert.NotEqual(t, "SF", v)
	}
}

func TestExtractRequestAttributes_DisabledReturnsNil(t *testing.T) {
	withCapture(t, false)
	assert.Nil(t, extractRequestAttributes(schemas.Anthropic, []byte(`{"model":"x","max_tokens":8}`)))
}

func TestExtractResponseAttributes(t *testing.T) {
	withCapture(t, true)
	anth := extractResponseAttributes([]byte(`{"stop_reason":"end_turn","content":[{"type":"tool_use"}]}`))
	assert.Equal(t, "end_turn", anth["stop_reason"])
	assert.Equal(t, true, anth["used_tool"])

	oai := extractResponseAttributes([]byte(`{"choices":[{"finish_reason":"stop"}]}`))
	assert.Equal(t, "stop", oai["stop_reason"])
}

func TestRespondedModel(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"openai unary":        {`{"id":"x","model":"gpt-4o-mini-2024-07-18","choices":[]}`, "gpt-4o-mini-2024-07-18"},
		"anthropic unary":     {`{"type":"message","model":"claude-opus-4-8-20260101","content":[]}`, "claude-opus-4-8-20260101"},
		"gemini unary":        {`{"candidates":[],"modelVersion":"gemini-2.5-flash"}`, "gemini-2.5-flash"},
		"openai sse chunk":    {"data: {\"id\":\"x\",\"model\":\"gpt-4o-mini-2024-07-18\",\"choices\":[{\"delta\":{}}]}\n\n", "gpt-4o-mini-2024-07-18"},
		"anthropic sse start": {"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-8-20260101\"}}\n\n", "claude-opus-4-8-20260101"},
		"gemini sse chunk":    {"data: {\"candidates\":[],\"modelVersion\":\"gemini-2.5-flash\"}\n\n", "gemini-2.5-flash"},
		// A comment line (": ...") or event line that contains "data:" must NOT be
		// mistaken for the data payload — only a line STARTING with "data:" counts.
		"sse comment before data": {": ping has data: bait\nevent: message_start\ndata: {\"model\":\"gpt-4o-mini-2024-07-18\"}\n\n", "gpt-4o-mini-2024-07-18"},
		"empty":                   {"", ""},
		"no model":                {`{"choices":[]}`, ""},
		"garbage":                 {"not json", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, respondedModel([]byte(c.body)))
		})
	}
}

func TestRewriteModel_AnthropicBody(t *testing.T) {
	body := []byte(`{"model":"nb-reasoning","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	nb, path := rewriteModel(schemas.Anthropic, body, "/v1/messages", "claude-opus-4-8")
	assert.Equal(t, "/v1/messages", path)
	// model replaced; other fields preserved.
	var m map[string]any
	require.NoError(t, json.Unmarshal(nb, &m))
	assert.Equal(t, "claude-opus-4-8", m["model"])
	assert.Equal(t, float64(8), m["max_tokens"])
	assert.NotNil(t, m["messages"])
}

func TestRewriteModel_GeminiPath(t *testing.T) {
	_, path := rewriteModel(schemas.Gemini, nil, "/models/nb-fast:generateContent", "gemini-2.5-flash")
	assert.Equal(t, "/models/gemini-2.5-flash:generateContent", path)

	_, path2 := rewriteModel(schemas.Gemini, nil, "/models/nb-fast", "gemini-2.5-flash")
	assert.Equal(t, "/models/gemini-2.5-flash", path2)
}

func TestRewriteModel_EmptyResolvedIsNoop(t *testing.T) {
	body := []byte(`{"model":"x"}`)
	nb, path := rewriteModel(schemas.Anthropic, body, "/v1/messages", "")
	assert.Equal(t, body, nb)
	assert.Equal(t, "/v1/messages", path)
}

func TestModelFamily(t *testing.T) {
	cases := map[string]string{
		"claude-haiku-4-5-20251001": "claude-haiku",
		"claude-opus-4-8":           "claude-opus",
		"gpt-4o-mini":               "gpt-4o",
		"gemini-2.0-flash":          "gemini-2.0",
		"unknown":                   "unknown",
	}
	for in, want := range cases {
		assert.Equal(t, want, modelFamily(in), in)
	}
}

func TestBuildAttributes_ActualAndDerived(t *testing.T) {
	withCapture(t, true)
	req := extractRequestAttributes(schemas.Anthropic, []byte(`{"model":"claude-haiku-4-5","max_tokens":8}`))
	a := buildAttributes(schemas.Anthropic, "claude-haiku-4-5", "header", req, nil, nil)

	assert.Equal(t, 8, a.Actual["max_tokens"])
	assert.Equal(t, "anthropic", a.Derived["provider"])
	assert.Equal(t, "claude-haiku", a.Derived["model_family"])
	assert.Equal(t, "header", a.Derived["session_source"])
}
