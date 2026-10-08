package agents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// "The automation has been built and saved" used to be all a build said. A new automation is
// saved paused, so a scheduled or event-driven one then never fired and nothing told the user
// why. The reply now says what state the automation is in and what that means for its trigger.

// savedAutomation is an automation as the builder sends it, or, given a status the workflow
// server uses, as the server returns it.
func savedAutomation(status string, triggers ...string) string {
	quoted := make([]string, len(triggers))
	for i, trigger := range triggers {
		quoted[i] = `{"type":"` + trigger + `"}`
	}
	statusField := ""
	if status != "" {
		statusField = `"status":"` + status + `",`
	}
	return `{"id":"wf-1","name":"daily-report",` + statusField + `"definition":{"version":"v1","triggers":[` + strings.Join(quoted, ",") + `],"tasks":[{"id":"report","type":"core.print","params":{"message":"hello"}}]}}`
}

func TestSavedStateNote(t *testing.T) {
	cases := []struct {
		name  string
		saved string
		want  string
	}{
		{"paused, schedule", savedAutomation("PAUSED", "schedule"), "It is saved **paused**: its schedule will not fire until you activate it. You can still run it by hand."},
		{"paused, event", savedAutomation("PAUSED", "event"), "It is saved **paused**: its event trigger will not fire until you activate it."},
		{"paused, webhook", savedAutomation("PAUSED", "webhook"), "It is saved **paused**: its webhook trigger will not fire until you activate it."},
		{"paused, manual only", savedAutomation("PAUSED", "manual"), "It runs only when you start it."},
		{"paused, no trigger listed", savedAutomation("PAUSED"), "It runs only when you start it."},
		{"paused, manual and schedule: the schedule decides", savedAutomation("PAUSED", "manual", "schedule"), "It is saved **paused**: its schedule will not fire until you activate it. You can still run it by hand."},
		{"active, schedule", savedAutomation("ACTIVE", "schedule"), "It is saved **active**: its schedule is on."},
		{"active, event", savedAutomation("ACTIVE", "event"), "It is saved **active**: its event trigger is on."},
		{"active, manual", savedAutomation("ACTIVE", "manual"), "It runs only when you start it."},
		{"inactive has its own note", savedAutomation("INACTIVE", "schedule"), ""},
		{"a status the server does not use says nothing", savedAutomation("active", "schedule"), ""},
		{"no status says nothing", savedAutomation("", "schedule"), ""},
		{"not an automation", "not json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, savedStateNote(tc.saved))
		})
	}
}

func TestInsertUnderHeadline(t *testing.T) {
	assert.Equal(t, "Headline.\n\nNote.\n\nRest.\n\nMore.", insertUnderHeadline("Headline.\n\nRest.\n\nMore.", "Note."))
	assert.Equal(t, "Headline.\n\nNote.", insertUnderHeadline("Headline.", "Note."))
	assert.Equal(t, "Headline.\n\nRest.", insertUnderHeadline("Headline.\n\nRest.", ""))
}

// savedReply is the builder's reply after saving sent to a stand-in workflow server. The server
// answers the save with an id and a read of that id with onServer — the automation as it saved
// it — or with an error when onServer is empty.
func savedReply(t *testing.T, mode string, sent string, onServer string, missingConfigs []string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"wf-1"}`))
		case onServer == "":
			http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
		default:
			_, _ = w.Write([]byte(onServer))
		}
	}))
	t.Cleanup(server.Close)
	previous := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = server.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = previous })

	agent := newWorkflowBuilderAgent("acct-1")
	agent.state.Mode = mode
	response := agent.finalizeWithAutoSave(stepBudgetTestContext(),
		core.NBAgentRequest{AccountId: "acct-1", SessionId: "session-1", ConversationSource: core.ConversationSourceUserInvestigation},
		sent, missingConfigs)
	require.Len(t, response.Response, 1)
	return response.Response[0]
}

// The state comes right after the headline, where it is read, not after the task list.
func TestSavedReply_SaysTheAutomationIsPausedUnderTheHeadline(t *testing.T) {
	reply := savedReply(t, "create", savedAutomation("", "schedule"), savedAutomation("PAUSED", "schedule"), nil)

	headline, rest, _ := strings.Cut(reply, "\n\n")
	assert.Equal(t, "The automation **`daily-report`** has been built and saved.", headline)
	assert.True(t, strings.HasPrefix(rest, "It is saved **paused**: its schedule will not fire until you activate it."), rest)
	assert.Contains(t, rest, "**Trigger:** schedule")
}

// The reply states the status the server gave the automation, not the one the builder sent:
// the server saves a status it does not know as paused, and a save that lands on an automation
// this conversation built earlier keeps the status that automation has now.
func TestSavedReply_StatesTheStatusTheServerSaved(t *testing.T) {
	sentActive := savedReply(t, "create", savedAutomation("active", "schedule"), savedAutomation("PAUSED", "schedule"), nil)
	assert.Contains(t, sentActive, "It is saved **paused**")
	assert.NotContains(t, sentActive, "saved **active**")

	alreadyActive := savedReply(t, "create", savedAutomation("", "schedule"), savedAutomation("ACTIVE", "schedule"), nil)
	assert.Contains(t, alreadyActive, "It is saved **active**: its schedule is on.")
	assert.NotContains(t, alreadyActive, "saved **paused**")
}

// When the saved automation cannot be read back the reply says nothing about its state rather
// than guess; the save itself is still reported.
func TestSavedReply_LeavesTheStateOutWhenTheAutomationCannotBeReadBack(t *testing.T) {
	reply := savedReply(t, "create", savedAutomation("", "schedule"), "", nil)

	assert.Contains(t, reply, "has been built and saved.")
	assert.NotContains(t, reply, "It is saved")
	assert.NotContains(t, reply, "It runs only")
}

// An edit does not change the automation's state, and an automation saved inactive already says
// why and what to do; neither gets the sentence.
func TestSavedReply_OnlyANewRunnableAutomationGetsTheStateSentence(t *testing.T) {
	edited := savedReply(t, "fix", savedAutomation("", "schedule"), savedAutomation("PAUSED", "schedule"), nil)
	assert.NotContains(t, edited, "saved **paused**")

	inactive := savedReply(t, "create", savedAutomation("INACTIVE", "schedule"), savedAutomation("INACTIVE", "schedule"), []string{"slack_channel"})
	assert.NotContains(t, inactive, "saved **paused**")
	assert.Contains(t, inactive, "Saved as **INACTIVE**")
}
