package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

const pickAccount = "acct-pick"

func pickPlan() storedPlan {
	return storedPlan{Version: "v-hash", Plan: RemediationPlan{RootCause: "The API pod runs out of memory. More detail follows.", Version: 3, Actions: []RemediationAction{
		{Action: "Raise the memory limit", ExecuteCommand: "kubectl set resources deploy/api --limits=memory=1Gi -n shop"},
		{Action: "Fix the leak", RequiredChange: &RequiredChange{Where: "code", Target: "cache.go", Change: "Bound the cache", Repo: "acme/api"}},
		{Action: "Ask the DBA", RequiredChange: &RequiredChange{Where: "process", Change: "Raise the pool size"}},
	}}}
}

func pickFactsFor(plan storedPlan) pickFacts {
	return pickFacts{PlanEventId: "evt-main", Session: "event-fp-main", Plan: plan, HasPlan: true}
}

// A pick is checked on the server: the fixes the person saw are still the alert's, it goes into the alert's own
// conversation, the option exists and is carried out the way picked, and nothing is under way on the alert.
func TestPickRefusal(t *testing.T) {
	waiting := time.Date(2026, 10, 24, 14, 2, 0, 0, time.UTC)
	ok := remediationPickRequest{PlanVersion: "v-hash", Option: 1, Kind: pickKindRun}
	for _, tc := range []struct {
		name    string
		pick    remediationPickRequest
		session string
		facts   func(f *pickFacts)
		code    int
		says    string
	}{
		{"run option", ok, "event-fp-main", nil, 0, ""},
		{"no session named", ok, "", nil, 0, ""},
		{"code fix as a PR", remediationPickRequest{PlanVersion: "v-hash", Option: 2, Kind: pickKindPR}, "", nil, 0, ""},
		{"no plan", ok, "", func(f *pickFacts) { f.HasPlan = false }, http.StatusConflict, "no fixes"},
		{"fixes replaced since seen", remediationPickRequest{PlanVersion: "old", Option: 1, Kind: pickKindRun}, "", nil, http.StatusConflict, "replaced"},
		{"no plan version", remediationPickRequest{Option: 1, Kind: pickKindRun}, "", nil, http.StatusConflict, "replaced"},
		{"another conversation", ok, "chat-123", nil, http.StatusBadRequest, "alert's own conversation"},
		{"no such option", remediationPickRequest{PlanVersion: "v-hash", Option: 4, Kind: pickKindRun}, "", nil, http.StatusBadRequest, "no option 4"},
		{"option 0", remediationPickRequest{PlanVersion: "v-hash", Kind: pickKindRun}, "", nil, http.StatusBadRequest, "no option 0"},
		{"a code fix is not run", remediationPickRequest{PlanVersion: "v-hash", Option: 2, Kind: pickKindRun}, "", nil, http.StatusConflict, "outside NudgeBee"},
		{"a manual change is not run", remediationPickRequest{PlanVersion: "v-hash", Option: 3, Kind: pickKindRun}, "", nil, http.StatusConflict, "outside NudgeBee"},
		{"a command is not a PR", remediationPickRequest{PlanVersion: "v-hash", Option: 1, Kind: pickKindPR}, "", nil, http.StatusConflict, "no pull request"},
		{"a manual change is not a PR", remediationPickRequest{PlanVersion: "v-hash", Option: 3, Kind: pickKindPR}, "", nil, http.StatusConflict, "no pull request"},
		{"unknown kind", remediationPickRequest{PlanVersion: "v-hash", Option: 1, Kind: "manual"}, "", nil, http.StatusBadRequest, "run or pr"},
		{"an approval waits", ok, "", func(f *pickFacts) { f.WaitingSince = &waiting }, http.StatusConflict, "already waiting for approval (asked at 14:02 UTC)"},
		{"a step runs or is checked", ok, "", func(f *pickFacts) { f.RunInFlight = true }, http.StatusConflict, "running or its check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := pickFactsFor(pickPlan())
			if tc.facts != nil {
				tc.facts(&facts)
			}
			code, says := pickRefusal(tc.pick, tc.session, facts)
			assert.Equal(t, tc.code, code)
			assert.Contains(t, says, tc.says)
		})
	}
}

// Every surface sends the same words for a pick: the tab's own (remediationLoop.js goWith / prRequestText).
func TestPickQueryText(t *testing.T) {
	plan := pickPlan().Plan
	assert.Equal(t, "Go with option 1: Raise the memory limit.", pickQueryText(remediationPickRequest{Option: 1, Kind: pickKindRun}, plan))
	assert.Equal(t, "Raise a PR for option 2 of plan v3: Bound the cache in cache.go (repo acme/api). Root cause: The API pod runs out of memory. "+prReplyAsk,
		pickQueryText(remediationPickRequest{Option: 2, Kind: pickKindPR}, plan))
	plan.Version, plan.RootCause = 0, ""
	assert.Equal(t, "Raise a PR for option 2: Bound the cache in cache.go (repo acme/api). "+prReplyAsk, pickQueryText(remediationPickRequest{Option: 2, Kind: pickKindPR}, plan))
}

func TestFirstSentence(t *testing.T) {
	assert.Equal(t, "One.", firstSentence("One. Two."))
	assert.Equal(t, "No stop.", firstSentence("No stop"))
	assert.Equal(t, "Version 1.2 is out.", firstSentence("Version 1.2 is out. Then"))
	assert.Equal(t, "First line.", firstSentence("First line\nsecond"))
	assert.Equal(t, "", firstSentence("  "))
}

func pickWriter() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant", "user-1", []string{pickAccount})
}

func pickReadOnly(t *testing.T) *security.RequestContext {
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(`{"RoleNames":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["`+pickAccount+`"]}}`), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

// applyRemediationPick: a writer's pick is rewritten into the alert's message on the plan's event; every refusal is
// answered before anything is sent; a request without a pick goes on unchanged.
func TestApplyRemediationPick(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pick := func() *remediationPickRequest {
		return &remediationPickRequest{EventId: "evt-dup", PlanVersion: "v-hash", Option: 1, Kind: "RUN"}
	}
	for _, tc := range []struct {
		name    string
		ctx     func(*testing.T) *security.RequestContext
		request ConversationApiRequest
		loop    bool
		loopErr error
		facts   func() (pickFacts, bool, error)
		goesOn  bool
		code    int
	}{
		{name: "no pick", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, request: ConversationApiRequest{AccountId: pickAccount, Query: "hi"}, goesOn: true},
		{name: "a writer picks", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, goesOn: true,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "Use this fix", SessionId: "event-fp-main", RemediationPick: pick()}},
		{name: "the event comes from config", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, goesOn: true,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", SessionId: "event-fp-main", RemediationPick: &remediationPickRequest{PlanVersion: "v-hash", Option: 1, Kind: "run"}}},
		{name: "read-only", ctx: pickReadOnly, loop: true, code: http.StatusForbidden,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", SessionId: "event-fp-main", RemediationPick: pick()}},
		{name: "loop off", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, code: http.StatusBadRequest,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", RemediationPick: pick()}},
		{name: "loop flag not read", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loopErr: errors.New("down"), code: http.StatusServiceUnavailable,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", RemediationPick: pick()}},
		{name: "an answer is not a pick", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, code: http.StatusBadRequest,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "yes", AgentId: "agent-1", RemediationPick: pick()}},
		{name: "no event", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, code: http.StatusBadRequest,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", RemediationPick: &remediationPickRequest{PlanVersion: "v-hash", Option: 1, Kind: "run"}}},
		{name: "event not found", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, code: http.StatusNotFound,
			facts:   func() (pickFacts, bool, error) { return pickFacts{}, false, nil },
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", RemediationPick: pick()}},
		{name: "facts not read", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, code: http.StatusServiceUnavailable,
			facts:   func() (pickFacts, bool, error) { return pickFacts{}, true, errors.New("db down") },
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", RemediationPick: pick()}},
		{name: "the alert's own conversation", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, goesOn: true,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", SessionId: "event-fp-main", ConversationId: "conv-alert", RemediationPick: pick()}},
		{name: "another conversation", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, code: http.StatusBadRequest,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", SessionId: "event-fp-main", ConversationId: "conv-other", RemediationPick: pick()}},
		{name: "stale pick", ctx: func(*testing.T) *security.RequestContext { return pickWriter() }, loop: true, code: http.StatusConflict,
			request: ConversationApiRequest{AccountId: pickAccount, Query: "x", RemediationPick: &remediationPickRequest{EventId: "evt-dup", PlanVersion: "old", Option: 1, Kind: "run"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubLoopOn(t, tc.loop)
			if tc.loopErr != nil {
				events.RemediationLoopRead = func(string) (bool, error) { return false, tc.loopErr }
			}
			swap(t, &readPickFacts, func(_ *security.RequestContext, accountId, eventId string) (pickFacts, bool, error) {
				assert.Equal(t, pickAccount, accountId)
				if tc.facts != nil {
					return tc.facts()
				}
				return pickFactsFor(pickPlan()), true, nil
			})
			swap(t, &alertConversationIdOf, func(accountId, session string) (string, error) {
				assert.Equal(t, "event-fp-main", session)
				return "conv-alert", nil
			})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			request := tc.request
			request.Config.EventId = "evt-dup"
			if tc.name == "no event" {
				request.Config.EventId = ""
			}
			assert.Equal(t, tc.goesOn, applyRemediationPick(c, tc.ctx(t), &request))
			if !tc.goesOn {
				assert.Equal(t, tc.code, rec.Code)
				return
			}
			if request.RemediationPick != nil {
				assert.Equal(t, "Go with option 1: Raise the memory limit.", request.Query, "the server's words, not the client's")
				assert.Equal(t, "event-fp-main", request.SessionId)
				assert.Equal(t, "evt-main", request.Config.EventId, "the plan's event")
			} else {
				assert.Equal(t, "hi", request.Query)
			}
		})
	}
}
