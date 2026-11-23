package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An automation that DMs a person on Slack was built with the person's name as user_id, and every
// run failed with "User not found" (#40460). These tests pin the fix: the name is looked up in the
// workspace, one match is settled without asking, anything else is asked about, and validate never
// lets a name through.

var workspaceUsers = []chatUser{
	{ID: "U08E9FC4D96", Name: "utsav.lanjewar", RealName: "Utsav Lanjewar", DisplayName: "Utsav L", Email: "utsav@example.com"},
	{ID: "U0000000002", Name: "utsav.k", RealName: "Utsav Kumar", DisplayName: "utsav.k"},
	{ID: "U0000000003", Name: "priya", RealName: "Priya Shah", DisplayName: "Priya"},
	{ID: "U0000000004", Name: "priya.s", RealName: "Priya Shah"},
}

const utsavAnswer = "Utsav Lanjewar (@utsav.lanjewar) — Slack member ID U08E9FC4D96 (use this ID as user_id)"

func dmQuestion(recipient string) ClarifyingQuestion {
	return ClarifyingQuestion{Question: "Who should get the direct message?", Kind: clarifyKindChatUser, Recipient: recipient, Options: []string{"Skip"}}
}

// userBuilder is a builder that has just received its clarifying questions, with a stubbed member list.
func userBuilder(users []chatUser, listed bool, questions ...ClarifyingQuestion) (*WorkflowBuilderAgent, *int) {
	agent := newWorkflowBuilderAgent("acct-1")
	calls := 0
	agent.chatUserLister = func(*security.RequestContext) ([]chatUser, bool) {
		calls++
		return users, listed
	}
	agent.state = WorkflowBuilderState{
		Stage:               "clarification",
		OriginalQuery:       "send hi on slack as a direct message to Utsav Lanjewar",
		ClarifyingQuestions: questions,
		ClarifyingAnswers:   make([]string, len(questions)),
	}
	return agent, &calls
}

// settle runs what handleIntentAndPlan does before the first question is shown.
func settle(agent *WorkflowBuilderAgent) {
	agent.settleChatUserQuestions(stepBudgetTestContext())
	agent.skipAnsweredQuestions()
}

func TestParseClarifyingQuestions_UserQuestionKeepsTheRecipientAndDropsModelOptions(t *testing.T) {
	raw := `{"questions":[{"question":"Who gets the DM?","kind":"Chat-User","recipient":"Utsav Lanjewar","options":["U123","Utsav"]}]}`

	questions, err := parseClarifyingQuestionsJSON(raw, 3)

	require.NoError(t, err)
	require.Len(t, questions, 1)
	assert.Equal(t, clarifyKindChatUser, questions[0].Kind)
	assert.Equal(t, "Utsav Lanjewar", questions[0].Recipient)
	assert.Equal(t, []string{"Skip"}, questions[0].Options)
}

func TestKeepChatChannelQuestions_KeepsUserQuestions(t *testing.T) {
	questions := []ClarifyingQuestion{{Question: "a"}, {Question: "b"}, dmQuestion("Utsav")}
	kept := keepChatChannelQuestions(questions, 2)
	require.Len(t, kept, 2)
	assert.Equal(t, clarifyKindChatUser, kept[1].Kind)
}

// The ticket's case: one member has that name, so nothing is asked and the id is recorded.
func TestChatUser_OneMatchIsSettledWithoutAsking(t *testing.T) {
	for _, named := range []string{"Utsav Lanjewar", "utsav lanjewar", "@utsav.lanjewar", "Utsav L", "UTSAV@example.com", "U08E9FC4D96"} {
		t.Run(named, func(t *testing.T) {
			agent, _ := userBuilder(workspaceUsers, true, dmQuestion(named), triggerQuestion)

			settle(agent)

			assert.Equal(t, 1, agent.state.ClarifyingIndex, "the DM question is passed over")
			assert.Equal(t, utsavAnswer, agent.state.ClarifyingAnswers[0])
			assert.Equal(t, "U08E9FC4D96", agent.state.ChatUsers["utsav lanjewar"])
			assert.Contains(t, agent.buildClarificationContext(), "U08E9FC4D96")
		})
	}

	// The only question: every question is answered, so the plan follows straight away.
	agent, _ := userBuilder(workspaceUsers, true, dmQuestion("Utsav Lanjewar"))
	settle(agent)
	assert.Equal(t, 1, agent.state.ClarifyingIndex)
}

// A name nobody has is asked about, offering members whose names are close. Never built with.
func TestChatUser_NoMatchIsAsked(t *testing.T) {
	agent, _ := userBuilder(workspaceUsers, true, dmQuestion("Utsav"), triggerQuestion)

	settle(agent)

	require.Equal(t, 0, agent.state.ClarifyingIndex)
	question := agent.state.ClarifyingQuestions[0]
	assert.Contains(t, question.Question, `couldn't find a Slack user named "Utsav"`)
	assert.Equal(t, []string{"Utsav Kumar (@utsav.k)", "Utsav Lanjewar (@utsav.lanjewar)", "Skip"}, question.Options)
	assert.Empty(t, agent.state.ChatUsers)

	// Picking an offered member settles it.
	response := answer(t, agent, "Utsav Lanjewar (@utsav.lanjewar)")
	assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
	assert.Equal(t, utsavAnswer, agent.state.ClarifyingAnswers[0])
	assert.Equal(t, "U08E9FC4D96", agent.state.ChatUsers["utsav"])
}

// Two members of the same name: the user picks one.
func TestChatUser_SeveralMatchesAreAsked(t *testing.T) {
	agent, _ := userBuilder(workspaceUsers, true, dmQuestion("Priya Shah"), triggerQuestion)

	settle(agent)

	require.Equal(t, 0, agent.state.ClarifyingIndex)
	question := agent.state.ClarifyingQuestions[0]
	assert.Contains(t, question.Question, `More than one Slack user matches "Priya Shah"`)
	assert.Equal(t, []string{"Priya Shah (@priya)", "Priya Shah (@priya.s)", "Skip"}, question.Options)

	answer(t, agent, "Priya Shah (@priya.s)")
	assert.Contains(t, agent.state.ClarifyingAnswers[0], "U0000000004")
	assert.Equal(t, "U0000000004", agent.state.ChatUsers["priya shah"])
}

// An answer that still names nobody is asked about once more; after that only a member id is kept.
func TestChatUser_UnknownAnswerIsAskedAboutOnce(t *testing.T) {
	agent, _ := userBuilder(workspaceUsers, true, dmQuestion(""), triggerQuestion)
	settle(agent)
	assert.Equal(t, "Who should get the direct message?", agent.state.ClarifyingQuestions[0].Question, "nobody named: the model's question stands")

	response := answer(t, agent, "Priya Shah")
	assert.Contains(t, response.FollowupRequest.Question, "More than one Slack user")
	assert.Equal(t, 0, agent.state.ClarifyingIndex)

	response = answer(t, agent, "Priya")
	assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
	assert.Contains(t, agent.state.ClarifyingAnswers[0], "U0000000003")

	agent, _ = userBuilder(workspaceUsers, true, dmQuestion(""), triggerQuestion)
	answer(t, agent, "Rahul")
	response = answer(t, agent, "Rahul again")
	assert.Equal(t, triggerQuestion.Question, response.FollowupRequest.Question)
	assert.Empty(t, agent.state.ClarifyingAnswers[0], "a name that names nobody is no answer, never the user_id")
}

// When the members cannot be listed, the user is asked for the member id; a name is not accepted.
func TestChatUser_ListFailureAsksForTheMemberId(t *testing.T) {
	for name, listed := range map[string]bool{"list unavailable": false, "no members": true} {
		t.Run(name, func(t *testing.T) {
			agent, _ := userBuilder(nil, listed, dmQuestion("Utsav Lanjewar"), triggerQuestion)

			settle(agent)

			require.Equal(t, 0, agent.state.ClarifyingIndex, "asked, not accepted as typed")
			assert.Contains(t, agent.state.ClarifyingQuestions[0].Question, `couldn't look up "Utsav Lanjewar"`)
			assert.Contains(t, agent.state.ClarifyingQuestions[0].Question, "U012AB3CD")

			answer(t, agent, "Utsav Lanjewar")
			assert.Empty(t, agent.state.ClarifyingAnswers[0])
		})
	}

	agent, _ := userBuilder(nil, false, dmQuestion("Utsav Lanjewar"), triggerQuestion)
	settle(agent)
	answer(t, agent, " U08E9FC4D96 ")
	assert.Equal(t, "Slack member ID U08E9FC4D96 (use this ID as user_id)", agent.state.ClarifyingAnswers[0])
}

// Skipping does not look anyone up.
func TestChatUser_SkipIsNotLookedUp(t *testing.T) {
	agent, calls := userBuilder(workspaceUsers, true, dmQuestion(""), triggerQuestion)
	answer(t, agent, "Skip")
	assert.Equal(t, 0, *calls)
	assert.Empty(t, agent.state.ClarifyingAnswers[0])
}

func TestChatUser_SurvivesBetweenTurns(t *testing.T) {
	agent, _ := userBuilder(workspaceUsers, true, dmQuestion("Utsav Lanjewar"))
	settle(agent)
	saved, err := agent.MarshalState()
	require.NoError(t, err)

	build := newWorkflowBuilderAgent("acct-1")
	require.NoError(t, build.UnmarshalState(saved))
	assert.Equal(t, "U08E9FC4D96", build.state.ChatUsers["utsav lanjewar"])
	assert.Equal(t, "Utsav Lanjewar", build.state.ClarifyingQuestions[0].Recipient)
}

// The name goes, the id comes in; templates, ids, and other providers are left as written.
func TestRewriteChatUsers(t *testing.T) {
	var workflow map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"name":"say-hi","definition":{
		"inputs":[{"id":"who","type":"string","default":"Utsav Lanjewar"},{"id":"other","type":"string","default":"Utsav Lanjewar"}],
		"tasks":[
		{"id":"dm","type":"notifications.dm","params":{"provider":"slack","user_id":"Utsav Lanjewar","message":"hi"}},
		{"id":"no-provider","type":"notifications.dm","params":{"user_id":"utsav lanjewar"}},
		{"id":"loop","type":"core.foreach","tasks":[{"id":"nested","type":"notifications.dm","params":{"provider":"slack","user_id":"Utsav Lanjewar"}}]},
		{"id":"template","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Configs.slack_user_id | default('Utsav Lanjewar') }}"}},
		{"id":"from-input","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Inputs.who }}"}},
		{"id":"teams","type":"notifications.dm","params":{"provider":"ms_teams","user_id":"Utsav Lanjewar"}},
		{"id":"print","type":"core.print","params":{"user_id":"Utsav Lanjewar"}}
	]}}`), &workflow))

	replaced := rewriteChatUsers(workflow, map[string]string{"utsav lanjewar": "U08E9FC4D96"})

	assert.Equal(t, 4, replaced)
	got := map[string]string{}
	definition := workflow["definition"].(map[string]interface{})
	walkTaskMaps(definition["tasks"].([]interface{}), func(task map[string]interface{}) {
		params, _ := task["params"].(map[string]interface{})
		if value, ok := params["user_id"].(string); ok {
			got[task["id"].(string)] = value
		}
	})
	assert.Equal(t, map[string]string{
		"dm":          "U08E9FC4D96",
		"no-provider": "U08E9FC4D96",
		"nested":      "U08E9FC4D96",
		"template":    "{{ Configs.slack_user_id | default('Utsav Lanjewar') }}",
		"from-input":  "{{ Inputs.who }}",
		"teams":       "Utsav Lanjewar",
		"print":       "Utsav Lanjewar",
	}, got)
	inputs := definition["inputs"].([]interface{})
	assert.Equal(t, "U08E9FC4D96", inputs[0].(map[string]interface{})["default"])
	assert.Equal(t, "Utsav Lanjewar", inputs[1].(map[string]interface{})["default"], "an input no DM step uses is left alone")

	assert.Zero(t, rewriteChatUsers(workflow, nil))
	assert.Equal(t, "not json", rewriteChatUsersJSON("not json", map[string]string{"a": "U1"}))
}

func TestLintChatUserIds(t *testing.T) {
	var workflow map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"definition":{
		"inputs":[{"id":"who","default":"Utsav"},{"id":"ok","default":"U08E9FC4D96"}],
		"tasks":[
		{"id":"a-name","type":"notifications.dm","params":{"provider":"slack","user_id":"Utsav Lanjewar"}},
		{"id":"b-caps-name","type":"notifications.dm","params":{"provider":"slack","user_id":"UTSAV"}},
		{"id":"c-default","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Configs.slack_user_id_utsav | default('Utsav Lanjewar') }}"}},
		{"id":"d-input","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Inputs.who }}"}},
		{"id":"id","type":"notifications.dm","params":{"provider":"slack","user_id":"U08E9FC4D96"}},
		{"id":"enterprise-id","type":"notifications.dm","params":{"provider":"slack","user_id":"W012A3CDE"}},
		{"id":"config","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Configs.slack_user_id }}"}},
		{"id":"empty-default","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Inputs.owner | default('') }}"}},
		{"id":"output","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Tasks.find.output.user }}"}},
		{"id":"good-input","type":"notifications.dm","params":{"provider":"slack","user_id":"{{ Inputs.ok }}"}},
		{"id":"teams","type":"notifications.dm","params":{"provider":"ms_teams","user_id":"Utsav"}}
	]}}`), &workflow))

	assert.Equal(t, []chatUserIssue{
		{TaskID: "a-name", Value: "Utsav Lanjewar"},
		{TaskID: "b-caps-name", Value: "UTSAV"},
		{TaskID: "c-default", Value: "Utsav Lanjewar"},
		{TaskID: "d-input", Value: "Utsav"},
	}, lintChatUserIds(workflow))
}

const dmByNameWorkflow = `{"name":"say-hi","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[
	{"id":"dm","type":"notifications.dm","params":{"provider":"slack","user_id":"Utsav Lanjewar","message":"hi"}}
]}}`

// Even with no clarifying answer, validate swaps in the id of the one member with that name.
func TestValidate_SendsTheMemberIdNotTheName(t *testing.T) {
	bodies := channelTestWorkflowServer(t, `{"ok":true}`)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.chatUserLister = func(*security.RequestContext) ([]chatUser, bool) { return workspaceUsers, true }
	require.NoError(t, json.Unmarshal([]byte(dmByNameWorkflow), &agent.state.WorkingWorkflow))

	result := agent.executeWorkflowTool(stepBudgetTestContext(), "validate", "", "")

	require.Contains(t, result, "Validation OK")
	assert.Contains(t, bodies["POST /validate"], `"user_id":"U08E9FC4D96"`)
	assert.NotContains(t, bodies["POST /validate"], "Utsav Lanjewar")
}

// A name that is not settled fails validate before the workflow server is asked.
func TestValidate_RefusesADmToAName(t *testing.T) {
	for name, lister := range map[string]func(*security.RequestContext) ([]chatUser, bool){
		"no such member":   func(*security.RequestContext) ([]chatUser, bool) { return workspaceUsers[2:], true },
		"list unavailable": func(*security.RequestContext) ([]chatUser, bool) { return nil, false },
	} {
		t.Run(name, func(t *testing.T) {
			bodies := channelTestWorkflowServer(t, `{"ok":true}`)
			agent := newWorkflowBuilderAgent("acct-1")
			agent.chatUserLister = lister
			require.NoError(t, json.Unmarshal([]byte(dmByNameWorkflow), &agent.state.WorkingWorkflow))

			result := agent.executeWorkflowTool(stepBudgetTestContext(), "validate", "", "")

			assert.Contains(t, result, "Validation FAILED")
			assert.Contains(t, result, `task "dm": user_id "Utsav Lanjewar"`)
			assert.Contains(t, result, chatUserConfigPlaceholder)
			assert.Empty(t, bodies["POST /validate"])
		})
	}
}

func TestSave_SendsTheMemberIdNotTheName(t *testing.T) {
	bodies := channelTestWorkflowServer(t, `{"id":"wf-1"}`)
	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = "create"
	agent.state.ChatUsers = map[string]string{"utsav lanjewar": "U08E9FC4D96"}

	_, err := agent.checkMissingConfigs(stepBudgetTestContext(), core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1"}, dmByNameWorkflow)

	require.NoError(t, err)
	assert.Contains(t, bodies["POST /workflows"], `"user_id":"U08E9FC4D96"`)
	assert.NotContains(t, bodies["POST /workflows"], "Utsav Lanjewar")
}

func TestLooksLikeSlackMemberId(t *testing.T) {
	for _, id := range []string{"U08E9FC4D96", "W012A3CDE", " U0123456 "} {
		assert.True(t, looksLikeSlackMemberId(id), id)
	}
	for _, name := range []string{"Utsav", "UTSAVLANJEWAR", "u08e9fc4d96", "U08E9", "Utsav Lanjewar", "C0000000002", ""} {
		assert.False(t, looksLikeSlackMemberId(name), name)
	}
}

func TestClarificationAndPlanningPrompts_KnowTheDmTask(t *testing.T) {
	prompt := getClarificationSystemPrompt("ENV", "CONFIGS", "INTENT")
	assert.Contains(t, prompt, `"kind": "chat_user"`)
	assert.Contains(t, prompt, `"recipient"`)
	assert.Regexp(t, `(?i)never propose people or slack ids`, prompt)

	planning := getWorkflowPlanningContext()
	assert.Contains(t, planning, "notifications.dm")
	assert.Regexp(t, `(?i)member ID.*never a display name`, planning)
}

func TestListSlackUsers(t *testing.T) {
	var gotPath, gotToken string
	var gotBody map[string]map[string]string
	reply := `{"data":[{"id":"U08E9FC4D96","name":"utsav.lanjewar","real_name":"Utsav Lanjewar","display_name":"Utsav L","email":"utsav@example.com"}]}`
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-ACTION-TOKEN")
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

	users, ok := listSlackUsers(stepBudgetTestContext())
	require.True(t, ok)
	assert.Equal(t, []chatUser{workspaceUsers[0]}, users)
	assert.Equal(t, "/api/users/list", gotPath)
	assert.Equal(t, "test-token", gotToken)
	assert.Equal(t, "slack", gotBody["input"]["platform"])
	assert.Equal(t, "tenant-1", gotBody["session_variables"]["tenant_id"])

	for name, failure := range map[string]struct {
		status int
		reply  string
	}{
		"error object": {http.StatusOK, `{"error":{"message":"Unable to list users"}}`},
		"server error": {http.StatusInternalServerError, `{"data":[]}`},
		"not json":     {http.StatusOK, `oops`},
	} {
		status, reply = failure.status, failure.reply
		_, ok = listSlackUsers(stepBudgetTestContext())
		assert.False(t, ok, name)
	}
}
