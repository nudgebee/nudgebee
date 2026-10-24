package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

const imTestUser = "7f9c2c1e-3b1a-4c5d-9e8f-0a1b2c3d4e5f"

// fakeNotificationServer answers channel lists per platform and records sends.
type fakeNotificationServer struct {
	lists   map[string]string
	answer  string
	sent    []map[string]any
	headers []http.Header
}

func (f *fakeNotificationServer) start(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		f.headers = append(f.headers, r.Header.Clone())
		switch r.URL.Path {
		case "/api/channels/list":
			platform, _ := payload["input"].(map[string]any)["platform"].(string)
			list, ok := f.lists[platform]
			if !ok {
				list = `{"data": []}`
			}
			_, _ = w.Write([]byte(list))
		case "/api/messages/send":
			f.sent = append(f.sent, payload)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(f.answer))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	previousURL, previousToken := config.Config.NotificationServerUrl, config.Config.NotificationServerToken
	config.Config.NotificationServerUrl, config.Config.NotificationServerToken = server.URL, "action-token"
	t.Cleanup(func() {
		config.Config.NotificationServerUrl, config.Config.NotificationServerToken = previousURL, previousToken
	})
}

const teamsList = `{"data": [
	{"id": "team-ops", "name": "Platform Team", "channels": [{"id": "19:general@thread.tacv2", "name": "General"}, {"id": "19:ops@thread.tacv2", "name": "ops"}]},
	{"id": "team-sre", "name": "SRE", "channels": [{"id": "19:ops-sre@thread.tacv2", "name": "ops"}]}
]}`

func imToolContext(source, userId string) core.NbToolContext {
	return core.NbToolContext{
		Ctx:       security.NewRequestContext(context.Background(), security.NewSecurityContextForTenantAccountAdmin("tenant-1", userId, []string{"acc-1"}), nil, nil, nil),
		AccountId: "acc-1",
		SessionId: "session-1",
		Source:    source,
	}
}

func callIMSend(t *testing.T, nbCtx core.NbToolContext, args map[string]any) core.NBToolResponse {
	t.Helper()
	resp, err := IMMessageSendTool{}.Call(nbCtx, core.NBToolCallRequest{Arguments: args})
	require.NoError(t, err)
	return resp
}

func TestIMMessageSend_PostsToTheNamedTeamsChannel(t *testing.T) {
	server := &fakeNotificationServer{lists: map[string]string{"ms_teams": teamsList}, answer: `[{"platform": "ms_teams", "status": "success"}]`}
	server.start(t)

	resp := callIMSend(t, imToolContext("UserInvestigation", imTestUser), map[string]any{
		"channel": "#ops", "team": "platform team", "platform": "ms_teams", "message": "## Summary\n- pod restarted",
	})

	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, resp.Data)
	assert.Contains(t, resp.Data, "Platform Team / #ops (Microsoft Teams)")
	require.Len(t, server.sent, 1)
	sent := server.sent[0]
	assert.Equal(t, "nubi", sent["source"])
	assert.Equal(t, "tenant-1", sent["tenant_id"])
	assert.Equal(t, map[string]any{"ms_teams": []any{map[string]any{"id": "19:ops@thread.tacv2", "team_id": "team-ops"}}}, sent["channels"])
	message := sent["parameters"].(map[string]any)["message"].(string)
	assert.True(t, strings.HasPrefix(message, "## Summary\n- pod restarted\n\n_"), message)
	assert.Contains(t, message, "via Nubi")
	assert.Equal(t, "action-token", server.headers[len(server.headers)-1].Get("X-ACTION-TOKEN"))
}

func TestIMMessageSend_AmbiguousChannelSendsNothing(t *testing.T) {
	server := &fakeNotificationServer{lists: map[string]string{"ms_teams": teamsList}}
	server.start(t)

	resp := callIMSend(t, imToolContext("InstantNotification", imTestUser), map[string]any{"channel": "ops", "message": "hello"})

	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "Platform Team / #ops (Microsoft Teams)")
	assert.Contains(t, resp.Data, "SRE / #ops (Microsoft Teams)")
	assert.Empty(t, server.sent)
}

func TestIMMessageSend_RefusesRunsWithoutAPerson(t *testing.T) {
	server := &fakeNotificationServer{lists: map[string]string{"slack": `{"data": [{"id": "C1", "name": "ops"}]}`}}
	server.start(t)
	args := map[string]any{"channel": "ops", "message": "hello"}

	for name, nbCtx := range map[string]core.NbToolContext{
		"automation":            imToolContext("Automation", imTestUser),
		"alert investigation":   imToolContext("Investigation", imTestUser),
		"unknown source":        imToolContext("", imTestUser),
		"system user":           imToolContext("UserInvestigation", security.GetSystemUserId()),
		"no user":               imToolContext("UserInvestigation", ""),
		"recommendation review": imToolContext("recommendation", imTestUser),
	} {
		t.Run(name, func(t *testing.T) {
			resp := callIMSend(t, nbCtx, args)
			assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
		})
	}
	assert.Empty(t, server.sent)
}

func TestIMMessageSend_RelaysWhyTheMessageWasNotSent(t *testing.T) {
	server := &fakeNotificationServer{
		lists:  map[string]string{"slack": `{"data": [{"id": "C1", "name": "ops"}]}`},
		answer: `[{"platform": "slack", "status": "failed", "reason": "The Nudgebee app is not in this channel"}]`,
	}
	server.start(t)

	resp := callIMSend(t, imToolContext("UserInvestigation", imTestUser), map[string]any{"channel": "C1", "message": "hello"})

	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "The Nudgebee app is not in this channel")
}

func TestIMMessageSend_NoConnectedChatApp(t *testing.T) {
	server := &fakeNotificationServer{}
	server.start(t)

	resp := callIMSend(t, imToolContext("UserInvestigation", imTestUser), map[string]any{"channel": "ops", "message": "hello"})

	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "no Slack, Microsoft Teams or Google Chat channel is available")
	assert.Empty(t, server.sent)
}

func TestIMMessageSend_RefusesAMessageItWouldHaveToCut(t *testing.T) {
	resp := callIMSend(t, imToolContext("UserInvestigation", imTestUser), map[string]any{
		"channel": "ops", "message": strings.Repeat("a", imMessageMaxBytes+1),
	})

	assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "shorten it")
}

func TestResolveChatDestination(t *testing.T) {
	destinations := []chatDestination{
		{Platform: "slack", ChannelID: "C1", ChannelName: "ops"},
		{Platform: "slack", ChannelID: "C2", ChannelName: "ops-alerts"},
		{Platform: "ms_teams", TeamID: "team-sre", TeamName: "SRE", ChannelID: "19:x@thread.tacv2", ChannelName: "incidents"},
	}

	match, _ := resolveChatDestination(destinations, "#OPS", "")
	require.NotNil(t, match)
	assert.Equal(t, "C1", match.ChannelID)

	match, _ = resolveChatDestination(destinations, "19:x@thread.tacv2", "")
	require.NotNil(t, match)
	assert.Equal(t, "team-sre", match.TeamID)

	match, candidates := resolveChatDestination(destinations, "alerts", "")
	assert.Nil(t, match)
	require.Len(t, candidates, 1)
	assert.Equal(t, "C2", candidates[0].ChannelID)

	match, candidates = resolveChatDestination(destinations, "incidents", "Platform Team")
	assert.Nil(t, match)
	assert.Empty(t, candidates)
}

func TestIMMessageSendConfirmationShowsDestinationAndMessage(t *testing.T) {
	question := IMMessageSendTool{}.ConfirmationQuestion(`{"channel": "ops", "team": "SRE", "platform": "ms_teams", "message": "Root cause: OOM"}`)

	assert.Equal(t, "Post this message to SRE / #ops on Microsoft Teams?\n\nRoot cause: OOM", question)
}

func TestMessageWithSignature(t *testing.T) {
	assert.Equal(t, "hi\n\n_Sent by Ana via Nubi_", messageWithSignature("hi", "Ana", ""))
	assert.Equal(t, "hi\n\n_[Sent via Nubi](https://app/x)_", messageWithSignature("hi", "", "https://app/x"))
	assert.Equal(t, "hi\n\n_Sent by Ana click here http://evil.example via Nubi_",
		messageWithSignature("hi", "Ana] [click here](http://evil.example)", ""))
	assert.Equal(t, "hi\n\n_Sent by Ana Lee !here via Nubi_", messageWithSignature("hi", "Ana_Lee <!here>*", ""))
}
