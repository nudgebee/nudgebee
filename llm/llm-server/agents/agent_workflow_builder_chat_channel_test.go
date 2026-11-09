package agents

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An automation that posts to Slack used to be built with {{ Configs.slack_channel }} because the
// builder was told not to ask for the channel. On an account without that config it was saved
// INACTIVE and never ran. These tests pin the replacement: the builder asks, the model supplies
// no channel names, and code checks the typed answer against the workspace and stores its id.

var workspaceChannels = []chatChannel{
	{ID: "C0000000001", Name: "sre-oncall"},
	{ID: "C0000000002", Name: "sre-reports"},
	{ID: "C0000000003", Name: "platform"},
	{ID: "C0000000004", Name: "Deploys"},
}

// channelBuilder is a builder waiting on its clarifying questions, with a stubbed channel list.
func channelBuilder(channels []chatChannel, listed bool, questions ...ClarifyingQuestion) (*WorkflowBuilderAgent, *int) {
	agent := newWorkflowBuilderAgent("acct-1")
	calls := 0
	agent.chatChannelLister = func(*security.RequestContext) ([]chatChannel, bool) {
		calls++
		return channels, listed
	}
	agent.state = WorkflowBuilderState{
		Stage:               "clarification",
		OriginalQuery:       "post the daily report to slack",
		ClarifyingQuestions: questions,
		ClarifyingAnswers:   make([]string, len(questions)),
	}
	return agent, &calls
}

var (
	channelQuestion = ClarifyingQuestion{Question: "Which Slack channel should the report be posted to?", Kind: clarifyKindChatChannel, Options: []string{"Skip"}}
	triggerQuestion = ClarifyingQuestion{Question: "Run it every morning?", Options: []string{"Every morning (recommended)", "Manually", "Skip"}}
)

func answer(t *testing.T, agent *WorkflowBuilderAgent, text string) core.NBAgentResponse {
	t.Helper()
	response, err := agent.handleClarificationResponse(stepBudgetTestContext(), core.NBAgentRequest{Query: text})
	require.NoError(t, err)
	return response
}

// The model's options for a channel question are dropped: it has no channel data, so any name it
// offered would be invented.
func TestParseClarifyingQuestions_ChannelQuestionCarriesNoModelOptions(t *testing.T) {
	// However the model spells the kind.
	for _, kind := range []string{"chat_channel", "Chat_Channel", "chat-channel", " CHAT CHANNEL "} {
		raw := fmt.Sprintf(`{"questions":[{"question":"Which Slack channel?","kind":%q,"options":["#alerts","#engineering"]}]}`, kind)

		questions, err := parseClarifyingQuestionsJSON(raw, 3)

		require.NoError(t, err)
		require.Len(t, questions, 1)
		assert.Equal(t, clarifyKindChatChannel, questions[0].Kind, kind)
		assert.Equal(t, []string{"Skip"}, questions[0].Options, kind)
	}
}

// The channel question survives the cap on questions. The model lists it last, and it is the one
// answer that decides whether the automation can run.
func TestParseClarifyingQuestions_ChannelQuestionSurvivesTheCap(t *testing.T) {
	raw := `{"questions":[
		{"question":"What should it do?","options":["A"]},
		{"question":"Which trigger?","options":["B"]},
		{"question":"Which account?","options":["C"]},
		{"question":"Which Slack channel?","kind":"chat_channel","options":[]}
	]}`

	questions, err := parseClarifyingQuestionsJSON(raw, 3)

	require.NoError(t, err)
	require.Len(t, questions, 3)
	assert.Equal(t, []string{"What should it do?", "Which trigger?", "Which Slack channel?"},
		[]string{questions[0].Question, questions[1].Question, questions[2].Question})
}

func TestKeepChatChannelQuestions(t *testing.T) {
	plain := func(q string) ClarifyingQuestion { return ClarifyingQuestion{Question: q} }
	channel := func(q string) ClarifyingQuestion {
		return ClarifyingQuestion{Question: q, Kind: clarifyKindChatChannel}
	}
	names := func(questions []ClarifyingQuestion) []string {
		out := make([]string, len(questions))
		for i, q := range questions {
			out[i] = q.Question
		}
		return out
	}

	assert.Equal(t, []string{"a", "b"}, names(keepChatChannelQuestions([]ClarifyingQuestion{plain("a"), plain("b")}, 3)), "under the cap nothing changes")
	assert.Equal(t, []string{"a", "b"}, names(keepChatChannelQuestions([]ClarifyingQuestion{plain("a"), plain("b"), plain("c")}, 2)), "without a channel question the cap cuts the tail")
	assert.Equal(t, []string{"a", "ch"}, names(keepChatChannelQuestions([]ClarifyingQuestion{plain("a"), plain("b"), channel("ch")}, 2)))
	assert.Equal(t, []string{"ch1", "ch2"}, names(keepChatChannelQuestions([]ClarifyingQuestion{channel("ch1"), channel("ch2"), channel("ch3")}, 2)), "nothing but channel questions: the cap still holds")
}

// A channel the workspace has is recorded by name for the plan and by id for the build, however
// the user typed it.
func TestChannelAnswer_IsCheckedAgainstTheWorkspace(t *testing.T) {
	for _, typed := range []string{"sre-reports", "#sre-reports", "  #SRE-Reports ", "C0000000002", "c0000000002"} {
		t.Run(typed, func(t *testing.T) {
			agent, calls := channelBuilder(workspaceChannels, true, channelQuestion, triggerQuestion)

			response := answer(t, agent, typed)

			assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question, "moves on to the next question")
			assert.Equal(t, "#sre-reports", agent.state.ClarifyingAnswers[0])
			assert.Equal(t, map[string]string{"sre-reports": "C0000000002"}, agent.state.ChatChannels)
			assert.Equal(t, 1, *calls)
		})
	}
}

// A name the workspace does not have is asked about once, offering channels that exist. Nothing
// is recorded and the question is not passed over.
func TestChannelAnswer_UnknownNameIsAskedAboutOnce(t *testing.T) {
	agent, _ := channelBuilder(workspaceChannels, true, channelQuestion, triggerQuestion)

	response := answer(t, agent, "sre")

	assert.Equal(t, core.ConversationStatusWaiting, response.Status)
	assert.Contains(t, response.FollowupRequest.Question, `couldn't find a Slack channel named "sre"`)
	assert.Equal(t, []string{"#sre-oncall", "#sre-reports", "Skip"}, response.FollowupRequest.FollowupOptions, "only channels the workspace has")
	assert.Equal(t, 0, agent.state.ClarifyingIndex)
	assert.Empty(t, agent.state.ClarifyingAnswers[0])
	assert.Empty(t, agent.state.ChatChannels)

	// Picking one of the offered channels settles it.
	response = answer(t, agent, "#sre-oncall")
	assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
	assert.Equal(t, "#sre-oncall", agent.state.ClarifyingAnswers[0])
	assert.Equal(t, map[string]string{"sre-oncall": "C0000000001"}, agent.state.ChatChannels)
}

// A second answer the workspace does not have is taken as typed: the user has named the channel
// twice, and a private channel the app is not in does not appear in the list.
func TestChannelAnswer_SecondUnknownNameIsTakenAsTyped(t *testing.T) {
	agent, _ := channelBuilder(workspaceChannels, true, channelQuestion, triggerQuestion)

	answer(t, agent, "finance-private")
	response := answer(t, agent, "finance-private")

	assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
	assert.Equal(t, "finance-private", agent.state.ClarifyingAnswers[0])
	assert.Empty(t, agent.state.ChatChannels)
}

// An answer that is not a channel name is never recorded as one. A reply such as "whatever you
// think" is asked about once and then counts as no answer, which leaves the builder where it was
// before it asked — it does not become the channel of a saved automation.
func TestChannelAnswer_ASentenceIsNeverRecordedAsAChannel(t *testing.T) {
	const sentence = "Use a manual trigger. For anything else, use your best judgement."

	agent, _ := channelBuilder(workspaceChannels, true, channelQuestion, triggerQuestion)
	first := answer(t, agent, sentence)
	assert.Contains(t, first.FollowupRequest.Question, "couldn't find a Slack channel")
	assert.Equal(t, []string{"Skip"}, first.FollowupRequest.FollowupOptions, "nothing in the workspace resembles a sentence")
	second := answer(t, agent, sentence)
	assert.Equal(t, triggerQuestion.Question, second.FollowupRequest.Question)
	assert.Empty(t, agent.state.ClarifyingAnswers[0])
	assert.Empty(t, agent.state.ChatChannels)
	assert.NotContains(t, agent.buildClarificationContext(), "manual trigger")

	// The same holds when the workspace cannot be listed at all.
	agent, _ = channelBuilder(nil, false, channelQuestion, triggerQuestion)
	answer(t, agent, sentence)
	assert.Empty(t, agent.state.ClarifyingAnswers[0])
}

// Each channel question gets its own second ask: a miss on one does not use up the other's.
func TestChannelAnswer_EachQuestionIsAskedAgainOnce(t *testing.T) {
	second := ClarifyingQuestion{Question: "And where should failures go?", Kind: clarifyKindChatChannel, Options: []string{"Skip"}}
	agent, _ := channelBuilder(workspaceChannels, true, channelQuestion, second, triggerQuestion)

	answer(t, agent, "sre")
	answer(t, agent, "sre-reports")
	require.Equal(t, 1, agent.state.ClarifyingIndex)

	response := answer(t, agent, "plat")
	assert.Contains(t, response.FollowupRequest.Question, `couldn't find a Slack channel named "plat"`)
	assert.Equal(t, []string{"#platform", "Skip"}, response.FollowupRequest.FollowupOptions)
	assert.Equal(t, 1, agent.state.ClarifyingIndex)

	answer(t, agent, "#platform")
	assert.Equal(t, []string{"#sre-reports", "#platform", ""}, agent.state.ClarifyingAnswers)
	assert.Equal(t, map[string]string{"sre-reports": "C0000000002", "platform": "C0000000003"}, agent.state.ChatChannels)
}

// What was learned from the answers has to outlive the turn: the agent is rebuilt from its saved
// state before the plan is approved and again before the build.
func TestChannelAnswer_SurvivesBetweenTurns(t *testing.T) {
	agent, _ := channelBuilder(workspaceChannels, true, channelQuestion, triggerQuestion)
	answer(t, agent, "sre")
	saved, err := agent.MarshalState()
	require.NoError(t, err)

	next := newWorkflowBuilderAgent("acct-1")
	next.chatChannelLister = agent.chatChannelLister
	require.NoError(t, next.UnmarshalState(saved))
	assert.Equal(t, clarifyKindChatChannel, next.state.ClarifyingQuestions[0].Kind)

	// The second miss is not asked about again, because the first was remembered.
	response := answer(t, next, "sre-private")
	assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
	assert.Equal(t, "sre-private", next.state.ClarifyingAnswers[0])

	next.state.ChatChannels = map[string]string{"sre-reports": "C0000000002"}
	saved, err = next.MarshalState()
	require.NoError(t, err)
	build := newWorkflowBuilderAgent("acct-1")
	require.NoError(t, build.UnmarshalState(saved))
	assert.Equal(t, map[string]string{"sre-reports": "C0000000002"}, build.state.ChatChannels)
}

// When the channel list cannot be read, or no workspace is connected, the answer is taken as
// typed rather than blocking the build on a check that cannot be made.
func TestChannelAnswer_IsTakenAsTypedWhenTheWorkspaceCannotBeListed(t *testing.T) {
	for name, listed := range map[string]bool{"list unavailable": false, "no channels": true} {
		t.Run(name, func(t *testing.T) {
			agent, _ := channelBuilder(nil, listed, channelQuestion, triggerQuestion)

			response := answer(t, agent, "#sre-reports")

			assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
			assert.Equal(t, "#sre-reports", agent.state.ClarifyingAnswers[0])
			assert.Empty(t, agent.state.ChatChannels)
		})
	}
}

// Skipping the question, and answering any other question, never touches the channel list.
func TestChannelAnswer_OnlyChannelAnswersAreChecked(t *testing.T) {
	agent, calls := channelBuilder(workspaceChannels, true, channelQuestion, triggerQuestion, triggerQuestion)

	answer(t, agent, "Skip")
	answer(t, agent, "platform")

	assert.Equal(t, 0, *calls)
	assert.Equal(t, []string{"", "platform", ""}, agent.state.ClarifyingAnswers)
	assert.Empty(t, agent.state.ChatChannels)
}

// The build writes the channel by the name the user gave; the id goes in its place, in every
// parameter that holds a Slack channel and at any depth. Everything else is left as written.
func TestRewriteChatChannels(t *testing.T) {
	var workflow map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"name":"daily-report","definition":{"tasks":[
		{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"#SRE-Reports","message":"done"}},
		{"id":"ask","type":"core.approval","params":{"im_channel":"sre-reports"}},
		{"id":"read","type":"notifications.read_thread","params":{"provider":"slack","channel_id":"sre-reports"}},
		{"id":"loop","type":"core.foreach","tasks":[
			{"id":"nested","type":"notifications.im","params":{"provider":"slack","channel":"sre-reports"}}
		]},
		{"id":"from-config","type":"notifications.im","params":{"provider":"slack","channel":"{{ Configs.slack_channel }}"}},
		{"id":"other-name","type":"notifications.im","params":{"provider":"slack","channel":"platform"}},
		{"id":"already-id","type":"notifications.im","params":{"provider":"slack","channel":"C0000000002"}},
		{"id":"teams","type":"notifications.im","params":{"provider":"ms_teams","channel":"sre-reports"}},
		{"id":"no-params","type":"core.print"}
	]}}`), &workflow))

	replaced := rewriteChatChannels(workflow, map[string]string{"sre-reports": "C0000000002"})

	assert.Equal(t, 4, replaced)
	channelOf := map[string]string{}
	tasks := workflow["definition"].(map[string]interface{})["tasks"].([]interface{})
	walkTaskMaps(tasks, func(task map[string]interface{}) {
		params, _ := task["params"].(map[string]interface{})
		for _, key := range chatChannelParams {
			if value, ok := params[key].(string); ok {
				channelOf[task["id"].(string)] = value
			}
		}
	})
	assert.Equal(t, map[string]string{
		"post":        "C0000000002",
		"ask":         "C0000000002",
		"read":        "C0000000002",
		"nested":      "C0000000002",
		"from-config": "{{ Configs.slack_channel }}",
		"other-name":  "platform",
		"already-id":  "C0000000002",
		"teams":       "sre-reports",
	}, channelOf)

	assert.Zero(t, rewriteChatChannels(workflow, nil))
	assert.Zero(t, rewriteChatChannels(nil, map[string]string{"sre-reports": "C0000000002"}))
}

// When the plan makes the channel an automation input, the name sits in that input's default and
// the task only refers to the input. The id goes into that default — and into no other input,
// since a channel can share its name with an ordinary value.
func TestRewriteChatChannels_InputDefault(t *testing.T) {
	var workflow map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"name":"daily-report","definition":{
		"inputs":[
			{"id":"slack_channel","type":"string","default":"#sre-reports"},
			{"id":"approval-channel","type":"string","default":"sre-reports"},
			{"id":"team","type":"string","default":"sre-reports"},
			{"id":"limit","type":"number","default":5},
			{"id":"no_default","type":"string"}
		],
		"tasks":[
			{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"{{ Inputs.slack_channel }}"}},
			{"id":"ask","type":"core.approval","params":{"im_channel":"{{ Inputs['approval-channel'] }}"}},
			{"id":"note","type":"core.print","params":{"message":"team {{ Inputs.team }}"}}
		]
	}}`), &workflow))

	replaced := rewriteChatChannels(workflow, map[string]string{"sre-reports": "C0000000002"})

	assert.Equal(t, 2, replaced)
	defaults := map[string]interface{}{}
	for _, raw := range workflow["definition"].(map[string]interface{})["inputs"].([]interface{}) {
		input := raw.(map[string]interface{})
		defaults[input["id"].(string)] = input["default"]
	}
	assert.Equal(t, map[string]interface{}{
		"slack_channel":    "C0000000002",
		"approval-channel": "C0000000002",
		"team":             "sre-reports",
		"limit":            float64(5),
		"no_default":       nil,
	}, defaults)
}

// The same swap on a definition held as JSON, as it is just before a save. A definition with
// nothing to swap comes back byte for byte.
func TestRewriteChatChannelsJSON(t *testing.T) {
	channels := map[string]string{"sre-reports": "C0000000002"}
	definition := `{"name":"daily-report","definition":{"tasks":[{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"#sre-reports"}}]}}`

	assert.JSONEq(t, `{"name":"daily-report","definition":{"tasks":[{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"C0000000002"}}]}}`,
		rewriteChatChannelsJSON(definition, channels))

	untouched := `{"name": "daily-report",  "definition": {"tasks": [{"id": "post", "params": {"channel": "platform"}}]}}`
	assert.Equal(t, untouched, rewriteChatChannelsJSON(untouched, channels))
	assert.Equal(t, definition, rewriteChatChannelsJSON(definition, nil))
	assert.Equal(t, "not json", rewriteChatChannelsJSON("not json", channels))
}

// channelTestWorkflowServer stands in for the workflow server: it records the body of every
// request by "METHOD path-suffix" and answers 200 with reply.
func channelTestWorkflowServer(t *testing.T, reply string) map[string]string {
	t.Helper()
	var mu sync.Mutex
	bodies := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies[r.Method+" "+r.URL.Path[strings.LastIndex(r.URL.Path, "/"):]] = string(body)
		mu.Unlock()
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	previous := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = previous })
	return bodies
}

const channelByNameWorkflow = `{"name":"daily-report","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[
	{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"#sre-reports","message":"done"}}
]}}`

// validate puts the id in place before the workflow server sees the definition, so what is
// validated — and recorded as validated — is what will be saved.
func TestValidate_SendsTheChannelIdNotTheName(t *testing.T) {
	bodies := channelTestWorkflowServer(t, `{"ok":true}`)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.ChatChannels = map[string]string{"sre-reports": "C0000000002"}
	require.NoError(t, json.Unmarshal([]byte(channelByNameWorkflow), &agent.state.WorkingWorkflow))

	result := agent.executeWorkflowTool(stepBudgetTestContext(), "validate", "", "")

	require.Contains(t, result, "Validation OK")
	sent := bodies["POST /validate"]
	assert.Contains(t, sent, `"channel":"C0000000002"`)
	assert.NotContains(t, sent, "sre-reports")
	assert.Contains(t, agent.validatedSnapshot, `"channel":"C0000000002"`)
}

// The save does the same on the definition it is handed: a build that did not end at a
// validated finalize saves the model's own copy, which still carries the name.
func TestSave_SendsTheChannelIdNotTheName(t *testing.T) {
	bodies := channelTestWorkflowServer(t, `{"id":"wf-1"}`)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = "create"
	agent.state.ChatChannels = map[string]string{"sre-reports": "C0000000002"}

	_, err := agent.checkMissingConfigs(stepBudgetTestContext(), core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1"}, channelByNameWorkflow)

	require.NoError(t, err)
	saved := bodies["POST /workflows"]
	assert.Contains(t, saved, `"channel":"C0000000002"`)
	assert.NotContains(t, saved, "sre-reports")
}

// The swap on the automation as the build holds it.
func TestRewriteChatChannels_OnTheWorkingAutomation(t *testing.T) {
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.ChatChannels = map[string]string{"sre-reports": "C0000000002"}
	require.NoError(t, json.Unmarshal([]byte(`{"name":"daily-report","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[
		{"id":"post","type":"notifications.im","params":{"provider":"slack","channel":"#sre-reports","message":"done"}}
	]}}`), &agent.state.WorkingWorkflow))

	rewriteChatChannels(agent.state.WorkingWorkflow, agent.state.ChatChannels)

	assert.Contains(t, agent.toolFinalize(), `"channel": "C0000000002"`)
}

func TestSuggestChatChannels(t *testing.T) {
	assert.Equal(t, []string{"#sre-oncall", "#sre-reports"}, suggestChatChannels(workspaceChannels, "#SRE", 8))
	assert.Equal(t, []string{"#platform"}, suggestChatChannels(workspaceChannels, "platform-team", 8), "a longer name that contains a channel's name")
	assert.Equal(t, []string{"#sre-oncall"}, suggestChatChannels(workspaceChannels, "sre", 1), "capped")
	assert.Empty(t, suggestChatChannels(workspaceChannels, "finance", 8))
	assert.Empty(t, suggestChatChannels(workspaceChannels, " # ", 8))
}

// The channel list comes from the notification service, for the caller's tenant. A reply without
// a list is "could not be read", which is not the same as an empty workspace.
func TestListSlackChannels(t *testing.T) {
	var gotToken, gotContentType string
	var gotBody map[string]map[string]string
	reply := `{"data":[{"name":"sre-reports","id":"C0000000002","is_private":false}]}`
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/channels/list", r.URL.Path)
		gotToken = r.Header.Get("X-ACTION-TOKEN")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	defer server.Close()

	defer func(url, token string) {
		config.Config.NotificationServerUrl, config.Config.NotificationServerToken = url, token
	}(config.Config.NotificationServerUrl, config.Config.NotificationServerToken)
	config.Config.NotificationServerUrl, config.Config.NotificationServerToken = server.URL+"/", "test-token"

	channels, ok := listSlackChannels(stepBudgetTestContext())
	require.True(t, ok)
	assert.Equal(t, []chatChannel{{ID: "C0000000002", Name: "sre-reports"}}, channels)
	assert.Equal(t, "test-token", gotToken)
	assert.Equal(t, "application/json", gotContentType, "the token header must not displace the body's content type")
	assert.Equal(t, "slack", gotBody["input"]["platform"])
	assert.Equal(t, "tenant-1", gotBody["session_variables"]["tenant_id"])

	reply = `{"data":[]}`
	channels, ok = listSlackChannels(stepBudgetTestContext())
	assert.True(t, ok, "no workspace connected is an answer")
	assert.Empty(t, channels)

	for name, failure := range map[string]struct {
		status int
		reply  string
	}{
		"service failed":  {http.StatusOK, `{}`},
		"error object":    {http.StatusOK, `{"error":{"message":"Unable to list channels"}}`},
		"not json":        {http.StatusOK, `oops`},
		"server error":    {http.StatusInternalServerError, `{"data":[]}`},
		"not authorised":  {http.StatusUnauthorized, `{"detail":"invalid token"}`},
		"list of nothing": {http.StatusOK, `[]`},
	} {
		status, reply = failure.status, failure.reply
		_, ok = listSlackChannels(stepBudgetTestContext())
		assert.False(t, ok, name)
	}

	config.Config.NotificationServerUrl = ""
	_, ok = listSlackChannels(stepBudgetTestContext())
	assert.False(t, ok, "no notification service configured")
}
