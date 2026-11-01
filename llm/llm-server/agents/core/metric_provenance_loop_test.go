package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"
)

// This is runtime integration coverage with a scripted model, not a model-quality benchmark.
func TestMetricProvenancePlannerExecution(t *testing.T) {
	previous := config.Config
	t.Cleanup(func() { config.Config = previous })
	previousDAO := GetConversationDao()
	conversationDaoMutex.Lock()
	SetConversationDao(&metricLoopDAO{claimCaptureDAO{IConversationDao: previousDAO}})
	conversationDaoMutex.Unlock()
	t.Cleanup(func() {
		conversationDaoMutex.Lock()
		defer conversationDaoMutex.Unlock()
		SetConversationDao(previousDAO)
	})
	config.Config.LlmServerAgentMaxScratchpadChars = 4096
	config.Config.LlmServerScratchpadSummarizationEnabled = false
	config.Config.ClaimCritiqueFeedbackEnabled = false
	config.Config.CritiqueContinuationEnabled = false
	config.Config.LlmTraceEnabled = false

	for _, engine := range []string{"react3", "react4"} {
		for _, tc := range []struct {
			name, body, answer string
			status             int
			wantNoData         bool
		}{
			{"empty", "No data found for query: up", "Metric unavailable [E1].", http.StatusOK, true},
			{"zero", `{"series":[{"pointlist":[[1,0]]}]}`, "Metric is 0 [E1].", http.StatusOK, false},
			{"normal", `{"series":[{"pointlist":[[1,0.245]]}]}`, "Latency is 245 ms [E1].", http.StatusOK, false},
			{"provider failure", "fixture provider unavailable", "Metric query failed [E1].", http.StatusServiceUnavailable, false},
		} {
			t.Run(engine+"/"+tc.name, func(t *testing.T) {
				config.Config.LlmServerAgentMaxScratchpadChars = 200000
				if tc.wantNoData {
					config.Config.LlmServerAgentMaxScratchpadChars = 4096
				}
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path == "/metric" {
						w.WriteHeader(tc.status)
						_, _ = io.WriteString(w, tc.body)
						return
					}
					_, _ = io.WriteString(w, "discovered series: up\n"+strings.Repeat("fixture series metadata ", 900))
				}))
				defer server.Close()
				metric := &metricLoopHTTPTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "prometheus_execute"}, endpoint: server.URL + "/metric"}
				discovery := &metricLoopHTTPTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "discover_metric_series"}, endpoint: server.URL + "/discover"}
				agent := &MockAgent{SupportedTools: []toolcore.NBTool{metric, discovery}}
				model := &metricLoopModel{engine: engine, answer: tc.answer}
				withFakeLLMModel(t, model)
				ctx := llmOverrideContext()
				request := NBAgentRequest{Query: "Read the metric and check available series.", EnableCritique: true}
				var planner NBAgentPlanner
				var err error
				if engine == "react3" {
					prompt, resolvedTools, promptErr := reActCreatePrompt3(ctx, "Inspect the supplied fixture metrics.", agent.SupportedTools, "", nil, request, agent)
					err = promptErr
					planner = &NBReActPlanner3{ctx: ctx, request: request, nbAgent: agent, llm: model, prompt: prompt, tools: resolvedTools, enableCritique: true, maxRefinementAttempts: 2, llmSummarizationDisabled: true, compressionTracker: NewCompressionTracker()}
				} else {
					planner, err = NewReActAgent4(ctx, request, agent, "Inspect the supplied fixture metrics.", nil, "")
				}
				require.NoError(t, err)
				executionRequest := request
				executionRequest.AccountId = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
				executionRequest.UserId = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
				executionRequest.ConversationId = "cccccccc-cccc-cccc-cccc-cccccccccccc"
				executionRequest.MessageId = "dddddddd-dddd-dddd-dddd-dddddddddddd"
				executor := &plannerExecutor{ctx: ctx, agent: agent, agentRequest: executionRequest, toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)}}
				tools := getNameToTool(agent.SupportedTools)
				finished := false
				for i := 0; i < 12; i++ {
					actions, finish, planErr := planner.Plan(ctx.GetContext(), executor.steps, request.Query)
					require.NoError(t, planErr)
					if finish != nil {
						require.Equal(t, tc.answer, finish.Data)
						finished = true
						break
					}
					require.Len(t, actions, 1)
					step, toolFinish, actionErr := executor.doAction(tools, actions[0], "")
					require.NoError(t, actionErr)
					require.Nil(t, toolFinish)
					executor.steps = append(executor.steps, step)
				}
				require.True(t, finished)
				require.Len(t, executor.steps, 11, "all observations must come from dispatched tools")
				require.EqualValues(t, 11, calls.Load(), "each tool must reach the local HTTP fixture")
				require.Equal(t, "E1", executor.steps[0].Action.DisplayID)
				require.Equal(t, tc.body, executor.steps[0].Observation)
				if tc.status == http.StatusOK {
					require.Equal(t, ToolStatusSuccess, executor.steps[0].Status)
				} else {
					require.Equal(t, ToolStatusFailure, executor.steps[0].Status)
				}
				require.GreaterOrEqual(t, len(model.sent), 13, "eleven action turns, final generation and critique")
				finalPrompt := model.sent[len(model.sent)-2]
				critiquePrompt := model.sent[len(model.sent)-1]
				if engine == "react3" {
					rawSize := 0
					for _, step := range executor.steps {
						rawSize += len(step.Observation)
					}
					require.Less(t, len(planner.(*NBReActPlanner3).buildScratchpad(executor.steps)), rawSize, "the runtime must actually compress the scratchpad")
				} else {
					require.True(t, planner.(*NBReActPlanner4).compressionActive(executor.steps))
					require.True(t, strings.Contains(finalPrompt, "output truncated"), "old observations must actually be compressed")
				}
				marker := "[METRIC-NO-DATA] prometheus_execute (step E1) returned no metric series."
				for _, prompt := range []string{finalPrompt, critiquePrompt} {
					if tc.wantNoData {
						require.True(t, strings.Contains(prompt, marker), "the actual no-data evidence and citation must survive")
					} else {
						require.False(t, strings.Contains(prompt, marker), "valid data and failures must not become successful empty reads")
						require.True(t, strings.Contains(prompt, tc.body), "the actual observation must survive")
					}
				}
				require.Contains(t, critiquePrompt, tc.answer, "the critique receives the cited final candidate")
			})
		}
	}
}

type metricLoopDAO struct{ claimCaptureDAO }

func (*metricLoopDAO) SaveCritique(*ConversationCritique) error { return nil }

type metricLoopHTTPTool struct {
	MockContextCapturingTool
	endpoint string
}

func (tool *metricLoopHTTPTool) Call(ctx toolcore.NbToolContext, request toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	r, err := http.NewRequestWithContext(ctx.Ctx.GetContext(), http.MethodGet, tool.endpoint+"?q="+url.QueryEscape(request.Command), nil)
	if err != nil {
		return toolcore.NBToolResponse{}, err
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		return toolcore.NBToolResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return toolcore.NBToolResponse{}, err
	}
	status := toolcore.NBToolResponseStatusSuccess
	if response.StatusCode != http.StatusOK {
		status = toolcore.NBToolResponseStatusError
	}
	return toolcore.NBToolResponse{Data: string(body), Status: status}, nil
}

type metricLoopModel struct {
	fakeLLMModel
	engine, answer string
	sent           []string
}

func (model *metricLoopModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	model.sent = append(model.sent, metricLoopTranscript(messages))
	i := len(model.sent) - 1
	if i >= 12 {
		return newFakeContentResponse("<decision>accept</decision><feedback></feedback>", "stop"), nil
	}
	if i == 11 {
		return newFakeContentResponse("<final_answer><content>"+model.answer+"</content></final_answer>", "stop"), nil
	}
	tool := "discover_metric_series"
	if i == 0 {
		tool = "prometheus_execute"
	}
	args, _ := json.Marshal(map[string]string{"query": fmt.Sprintf("fixture-%d", i)})
	if model.engine == "react3" {
		return newFakeContentResponse("<thought_action><thought>Inspect fixture evidence.</thought><action><tool_name>"+tool+"</tool_name><tool_input>"+string(args)+"</tool_input></action></thought_action>", "stop"), nil
	}
	response := newFakeContentResponse("Inspect fixture evidence.", "tool_calls")
	response.Choices[0].ToolCalls = []llms.ToolCall{{ID: fmt.Sprintf("fixture-call-%d", i), Type: "function", FunctionCall: &llms.FunctionCall{Name: tool, Arguments: string(args)}}}
	return response, nil
}

func metricLoopTranscript(messages []llms.MessageContent) string {
	var text strings.Builder
	for _, message := range messages {
		for _, part := range message.Parts {
			switch value := part.(type) {
			case llms.TextContent:
				text.WriteString(value.Text)
			case llms.ToolCallResponse:
				text.WriteString(value.Content)
			}
		}
	}
	return text.String()
}
