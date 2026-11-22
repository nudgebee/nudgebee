package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every test in the package records health into memory, never into a database.
func init() {
	mcpHealthStoreInstance = newMemMCPHealthStore()
}

type memMCPHealthStore struct {
	mu       sync.Mutex
	health   map[string]mcpHealth
	disabled map[string]bool
}

func newMemMCPHealthStore() *memMCPHealthStore {
	return &memMCPHealthStore{health: map[string]mcpHealth{}, disabled: map[string]bool{}}
}

// useMemMCPHealthStore gives the test its own empty store.
func useMemMCPHealthStore(t *testing.T) *memMCPHealthStore {
	t.Helper()
	orig := mcpHealthStoreInstance
	store := newMemMCPHealthStore()
	mcpHealthStoreInstance = store
	t.Cleanup(func() { mcpHealthStoreInstance = orig })
	return store
}

func (s *memMCPHealthStore) load(ids []string) (map[string]mcpHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]mcpHealth{}
	for _, id := range ids {
		if h, ok := s.health[id]; ok {
			c := mcpHealth{}
			for k, v := range h {
				c[k] = v
			}
			out[id] = c
		}
	}
	return out, nil
}

func (s *memMCPHealthStore) pendingFailure(id, scope string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health[id][scope].FailingSince != nil, nil
}

func (s *memMCPHealthStore) update(id, scope string, fn func(mcpHealthEntry) (mcpHealthEntry, bool)) (mcpHealthEntry, mcpHealthEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled[id] {
		return mcpHealthEntry{}, mcpHealthEntry{}, false, nil
	}
	prev := s.health[id][scope]
	next, write := fn(prev)
	if !write {
		return prev, prev, false, nil
	}
	if s.health[id] == nil {
		s.health[id] = mcpHealth{}
	}
	s.health[id][scope] = next
	return prev, next, true, nil
}

func (s *memMCPHealthStore) entry(id, scope string) mcpHealthEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health[id][scope]
}

// passHealthTime moves every stored time of the scope back by d.
func (s *memMCPHealthStore) passHealthTime(id, scope string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.health[id][scope]
	back := func(p *time.Time) *time.Time {
		if p == nil {
			return nil
		}
		v := p.Add(-d)
		return &v
	}
	e.LastCheckedAt, e.FailingSince, e.LastProbeAt = back(e.LastCheckedAt), back(e.FailingSince), back(e.LastProbeAt)
	s.health[id][scope] = e
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyMCPOutcome(t *testing.T) {
	relayErr := func(code int, msg string) error {
		return fmt.Errorf("relay MCP request failed: %w", &relay.StatusError{Code: code, Message: msg})
	}
	transport := func(err error) error {
		return fmt.Errorf("relay MCP request failed: %w", fmt.Errorf("unable to access relay server %w", &url.Error{Op: "Post", URL: "http://relay/request", Err: err}))
	}
	dialRelay := transport(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})

	cases := []struct {
		name      string
		err       error
		discovery mcpOutcome
		call      mcpOutcome
	}{
		{"success", nil, mcpOutcome{kind: mcpOutcomeSuccess, discovery: true}, mcpOutcome{kind: mcpOutcomeSuccess}},
		{"caller went away", transport(context.Canceled), mcpOutcome{kind: mcpOutcomeIgnore}, mcpOutcome{kind: mcpOutcomeIgnore}},
		{"relay itself down", dialRelay, mcpOutcome{kind: mcpOutcomeIgnore}, mcpOutcome{kind: mcpOutcomeIgnore}},
		{"relay pod stopped mid-request (reset)", transport(&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}), mcpOutcome{kind: mcpOutcomeIgnore}, mcpOutcome{kind: mcpOutcomeIgnore}},
		{"relay pod stopped mid-request (EOF)", transport(io.EOF), mcpOutcome{kind: mcpOutcomeIgnore}, mcpOutcome{kind: mcpOutcomeIgnore}},
		{"own deadline (hung server)", transport(context.DeadlineExceeded),
			mcpFailure(mcpHealthUnreachable, "timeout", true), mcpOutcome{kind: mcpOutcomeSuspect}},
		{"client timeout", transport(timeoutErr{}),
			mcpFailure(mcpHealthUnreachable, "timeout", true), mcpOutcome{kind: mcpOutcomeSuspect}},
		{"agent offline", relayErr(503, "agent not connected"),
			mcpFailure(mcpHealthUnreachable, "agent not connected", true), mcpFailure(mcpHealthUnreachable, "agent not connected", false)},
		{"agent timeout", relayErr(504, "timeout waiting for agent"),
			mcpFailure(mcpHealthUnreachable, "timeout waiting for agent", true), mcpOutcome{kind: mcpOutcomeSuspect}},
		{"server refused (relay 502)", relayErr(502, "MCP server request failed: dial tcp: connect: connection refused"),
			mcpFailure(mcpHealthUnreachable, "MCP server request failed: dial tcp: connect: connection refused", true),
			mcpFailure(mcpHealthUnreachable, "MCP server request failed: dial tcp: connect: connection refused", false)},
		{"oauth token exchange (relay 502)", relayErr(502, "MCP server request failed: OAuth token exchange failed: 404"),
			mcpFailure(mcpHealthAuthFailed, "MCP server request failed: OAuth token exchange failed: 404", true),
			mcpFailure(mcpHealthAuthFailed, "MCP server request failed: OAuth token exchange failed: 404", false)},
		{"relay's own auth", relayErr(401, "unauthorized"), mcpOutcome{kind: mcpOutcomeIgnore}, mcpOutcome{kind: mcpOutcomeIgnore}},
		{"mcp 401", &mcpStatusError{Code: 401}, mcpFailure(mcpHealthAuthFailed, "MCP server returned 401", true), mcpFailure(mcpHealthAuthFailed, "MCP server returned 401", false)},
		{"mcp 403", &mcpStatusError{Code: 403}, mcpFailure(mcpHealthAuthFailed, "MCP server returned 403", true), mcpFailure(mcpHealthAuthFailed, "MCP server returned 403", false)},
		// A 5xx on a tool call may be one broken tool: the tools/list probe decides.
		{"mcp 503", &mcpStatusError{Code: 503}, mcpFailure(mcpHealthUnreachable, "MCP server returned 503", true), mcpOutcome{kind: mcpOutcomeSuspect}},
		{"mcp 500", &mcpStatusError{Code: 500}, mcpFailure(mcpHealthUnreachable, "MCP server returned 500", true), mcpOutcome{kind: mcpOutcomeSuspect}},
		{"mcp 404", &mcpStatusError{Code: 404}, mcpFailure(mcpHealthError, "MCP server returned 404", true), mcpOutcome{kind: mcpOutcomeIgnore}},
		{"mcp 422 on a call (bad arguments)", &mcpStatusError{Code: 422}, mcpFailure(mcpHealthError, "MCP server returned 422", true), mcpOutcome{kind: mcpOutcomeIgnore}},
		{"relay 400", relayErr(400, "invalid MCP request: missing url"), mcpFailure(mcpHealthError, "invalid MCP request: missing url", true), mcpOutcome{kind: mcpOutcomeIgnore}},
		{"anything else", errors.New("boom"), mcpFailure(mcpHealthError, "boom", true), mcpFailure(mcpHealthError, "boom", false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.discovery, classifyMCPOutcome(tc.err, true), "discovery")
			assert.Equal(t, tc.call, classifyMCPOutcome(tc.err, false), "tool call")
		})
	}
}

func TestNextMCPHealth(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	down := mcpFailure(mcpHealthUnreachable, "refused", true)
	connected := mcpHealthEntry{Status: mcpHealthConnected, LastCheckedAt: ago(time.Hour)}

	t.Run("no verdict yet: a failure is reported at once", func(t *testing.T) {
		next, write := nextMCPHealth(mcpHealthEntry{}, down, now, false)
		assert.True(t, write)
		assert.Equal(t, mcpHealthUnreachable, next.Status)
		assert.Equal(t, now, *next.FailingSince)
	})
	t.Run("connected: held through the first two minutes", func(t *testing.T) {
		next, write := nextMCPHealth(connected, down, now, false)
		assert.True(t, write)
		assert.Equal(t, mcpHealthConnected, next.Status)
		assert.Equal(t, now, *next.FailingSince)
		assert.Equal(t, "refused", next.Reason)

		held := connected
		held.FailingSince = ago(90 * time.Second)
		next, _ = nextMCPHealth(held, down, now, false)
		assert.Equal(t, mcpHealthConnected, next.Status, "90s in")
		assert.Equal(t, *held.FailingSince, *next.FailingSince, "the run keeps its start")
	})
	t.Run("connected: reported down once failing for two minutes", func(t *testing.T) {
		held := connected
		held.FailingSince = ago(mcpHealthHold)
		next, _ := nextMCPHealth(held, down, now, false)
		assert.Equal(t, mcpHealthUnreachable, next.Status)
	})
	t.Run("Check now skips the hold", func(t *testing.T) {
		next, _ := nextMCPHealth(connected, down, now, true)
		assert.Equal(t, mcpHealthUnreachable, next.Status)
	})
	t.Run("healthy tool call writes nothing", func(t *testing.T) {
		_, write := nextMCPHealth(connected, mcpSuccess(false, 0), now, false)
		assert.False(t, write)
		_, write = nextMCPHealth(mcpHealthEntry{}, mcpSuccess(false, 0), now, false)
		assert.False(t, write, "no entry is created by a call")
	})
	t.Run("successful tool call ends a pending failure", func(t *testing.T) {
		held := connected
		held.FailingSince = ago(time.Minute)
		held.ToolCount = new(int)
		*held.ToolCount = 4
		next, write := nextMCPHealth(held, mcpSuccess(false, 0), now, false)
		assert.True(t, write)
		assert.Equal(t, mcpHealthConnected, next.Status)
		assert.Nil(t, next.FailingSince)
		assert.Equal(t, 4, *next.ToolCount, "a call does not know the tool count")
	})
	t.Run("discovery success records the tool count", func(t *testing.T) {
		failing := mcpHealthEntry{Status: mcpHealthUnreachable, FailingSince: ago(time.Hour), Reason: "refused"}
		next, write := nextMCPHealth(failing, mcpSuccess(true, 7), now, false)
		assert.True(t, write)
		assert.Equal(t, mcpHealthEntry{Status: mcpHealthConnected, ToolCount: next.ToolCount, LastCheckedAt: &now}, next)
		assert.Equal(t, 7, *next.ToolCount)
	})
	t.Run("ignore and suspect write nothing", func(t *testing.T) {
		for _, o := range []mcpOutcome{{kind: mcpOutcomeIgnore}, {kind: mcpOutcomeSuspect}} {
			_, write := nextMCPHealth(connected, o, now, false)
			assert.False(t, write)
		}
	})
}

func TestMCPHealthSkip(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }

	_, skip := mcpHealthSkip(mcpHealthEntry{Status: mcpHealthConnected, FailingSince: ago(time.Hour), LastCheckedAt: ago(0)}, now)
	assert.False(t, skip, "held as connected: discovery runs, so the hold can end")

	wait, skip := mcpHealthSkip(mcpHealthEntry{Status: mcpHealthUnreachable, FailingSince: ago(10 * time.Minute), LastCheckedAt: ago(3 * time.Minute)}, now)
	assert.True(t, skip)
	assert.InDelta(t, (7 * time.Minute).Seconds(), wait.Seconds(), 1, "down 10 min: retry 10 min after the last check")

	_, skip = mcpHealthSkip(mcpHealthEntry{Status: mcpHealthUnreachable, FailingSince: ago(10 * time.Minute), LastCheckedAt: ago(10 * time.Minute)}, now)
	assert.False(t, skip, "retry due")

	wait, skip = mcpHealthSkip(mcpHealthEntry{Status: mcpHealthAuthFailed, FailingSince: ago(5 * time.Hour), LastCheckedAt: ago(time.Minute)}, now)
	assert.True(t, skip)
	assert.InDelta(t, (defaultCacheTTL - time.Minute).Seconds(), wait.Seconds(), 1, "down for hours: every 30 min")
}

func TestSanitizeMCPHealthReason(t *testing.T) {
	assert.Equal(t,
		`MCP server request failed: Post "https://mcp.example.com": dial tcp: connection refused`,
		sanitizeMCPHealthReason(`MCP server request failed: Post "https://user:pw@mcp.example.com/v1/mcp?token=s3cret": dial tcp: connection refused`))
	long := sanitizeMCPHealthReason(string(make([]byte, 500)))
	assert.LessOrEqual(t, len(long), mcpHealthReasonMax+len("..."))
}

func newTestNbToolContext(t *testing.T, accountId string) NbToolContext {
	return NbToolContext{AccountId: accountId, Ctx: security.NewRequestContext(t.Context(), nil, nil, nil, nil)}
}

// mcpRelayStub answers tools/list and tools/call per integration through a
// handler the test can switch.
type mcpRelayStub struct {
	mu          sync.Mutex
	discoveries map[string]int
	calls       map[string]int
	answer      func(id, method string) (status int, body string)
}

func newMCPRelayStub(t *testing.T) *mcpRelayStub {
	s := &mcpRelayStub{discoveries: map[string]int{}, calls: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req relay.RelayExecuteRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		id, _ := req.Body.ActionParams["integration_id"].(string)
		method, _ := req.Body.ActionParams["method"].(string)
		s.mu.Lock()
		if method == "tools/list" {
			s.discoveries[id]++
		} else {
			s.calls[id]++
		}
		answer := s.answer
		s.mu.Unlock()
		status, body := answer(id, method)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.RelayServerEndpoint = orig })
	return s
}

func (s *mcpRelayStub) set(answer func(id, method string) (int, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answer = answer
}

func (s *mcpRelayStub) discovered(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discoveries[id]
}

func mcpToolsListBody(tool string) string {
	b, _ := json.Marshal(map[string]any{
		"status_code": 200,
		"data":        fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":%q,"inputSchema":{"type":"object"}}]}}`, tool),
	})
	return string(b)
}

const mcpRefusedBody = `{"error":"MCP server request failed: connect: connection refused"}`

func expectMCPConfigRead(mock sqlmock.Sqlmock, accountId string, ids ...string) {
	rows := sqlmock.NewRows([]string{"id", "name", "config_name", "config_value", "config_encrypted"})
	for _, id := range ids {
		rows.AddRow(id, id, nil, nil, nil)
	}
	mock.ExpectQuery(`FROM integrations i`).WithArgs(accountId).WillReturnRows(rows)
}

// #40994: the retry clock used to live in each pod's memory and restart on every
// rollout, so a server that stayed down was discovered ~249 times a day. It is
// now the integration's stored health, shared by every pod.
func TestListMCPIntegrationTools_FailingServerFollowsTheSharedClock(t *testing.T) {
	const accountId = "acct-mcp-shared-clock"
	t.Cleanup(func() { mcpIntegrationToolCacheInstance.delete(accountId) })
	store := useMemMCPHealthStore(t)
	mock := registerMockMetastore(t)
	stub := newMCPRelayStub(t)
	stub.set(func(string, string) (int, string) { return http.StatusBadGateway, mcpRefusedBody })

	// First sight of a dead server: reported at once, retried after a minute.
	expectMCPConfigRead(mock, accountId, "int-dead")
	assert.Equal(t, []string{"int_dead_unavailable"}, nbToolNames(ListMCPIntegrationTools(accountId)))
	e := store.entry("int-dead", mcpHealthAllScopes)
	assert.Equal(t, mcpHealthUnreachable, e.Status)
	assert.Contains(t, e.Reason, "connection refused")
	assert.InDelta(t, time.Minute.Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5)

	// Down for 10 minutes, last checked 2 minutes ago. A pod that just started
	// (empty cache) must not discover it again: the wait is the time it has
	// been down, measured from the shared clock.
	store.passHealthTime("int-dead", mcpHealthAllScopes, 10*time.Minute)
	now := time.Now()
	checked := now.Add(-2 * time.Minute)
	store.mu.Lock()
	h := store.health["int-dead"][mcpHealthAllScopes]
	h.LastCheckedAt = &checked
	store.health["int-dead"][mcpHealthAllScopes] = h
	store.mu.Unlock()
	mcpIntegrationToolCacheInstance.delete(accountId) // "rollout"

	expectMCPConfigRead(mock, accountId, "int-dead")
	assert.Equal(t, []string{"int_dead_unavailable"}, nbToolNames(ListMCPIntegrationTools(accountId)))
	assert.Equal(t, 1, stub.discovered("int-dead"), "skipped: retry not due")
	assert.InDelta(t, (8 * time.Minute).Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5,
		"the placeholder is kept until the shared retry is due")

	// The server is back. Its retry comes due once the wait (the time it has
	// been down, capped at 30 min) has passed since the last check: 28 more
	// minutes makes it down 38 min (wait 30) and checked 30 min ago.
	stub.set(func(string, string) (int, string) { return http.StatusOK, mcpToolsListBody("query") })
	store.passHealthTime("int-dead", mcpHealthAllScopes, 28*time.Minute)
	mcpIntegrationToolCacheInstance.delete(accountId)
	expectMCPConfigRead(mock, accountId, "int-dead")
	assert.Equal(t, []string{"int_dead_query"}, nbToolNames(ListMCPIntegrationTools(accountId)))
	e = store.entry("int-dead", mcpHealthAllScopes)
	assert.Equal(t, mcpHealthConnected, e.Status)
	assert.Nil(t, e.FailingSince)
	assert.Equal(t, 1, *e.ToolCount)
	assert.InDelta(t, defaultCacheTTL.Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A connected server whose call fails is held as connected, but the pod
// rediscovers within a minute so the hold ends even if the chat never calls the
// tool again (round-6 finding).
func TestMCPToolCall_FailureShortensTheCacheDuringTheHold(t *testing.T) {
	const accountId = "acct-mcp-hold"
	t.Cleanup(func() { mcpIntegrationToolCacheInstance.delete(accountId) })
	store := useMemMCPHealthStore(t)
	stub := newMCPRelayStub(t)
	cfg := mcpIntegrationConfig{IntegrationID: "int-flaky", IntegrationName: "flaky", ConnectionMode: "direct"}
	tool := mcpIntegrationTool{config: cfg, toolName: "flaky_run", mcpToolName: "run"}

	now := time.Now()
	store.health["int-flaky"] = mcpHealth{mcpHealthAllScopes: {Status: mcpHealthConnected, LastCheckedAt: &now}}
	mcpIntegrationToolCacheInstance.set(accountId, []NBTool{tool})
	require.InDelta(t, defaultCacheTTL.Seconds(), mcpCacheExpiresIn(accountId).Seconds(), 5)

	stub.set(func(string, string) (int, string) { return http.StatusBadGateway, mcpRefusedBody })
	_, err := tool.Call(newTestNbToolContext(t, accountId), NBToolCallRequest{Arguments: map[string]any{}})
	require.Error(t, err)

	e := store.entry("int-flaky", mcpHealthAllScopes)
	assert.Equal(t, mcpHealthConnected, e.Status, "held")
	assert.NotNil(t, e.FailingSince)
	assert.LessOrEqual(t, mcpCacheExpiresIn(accountId), time.Minute, "this pod rediscovers within a minute")

	// A later successful call ends the pending failure.
	stub.set(func(string, string) (int, string) {
		return http.StatusOK, `{"status_code":200,"data":"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}"}`
	})
	_, err = tool.Call(newTestNbToolContext(t, accountId), NBToolCallRequest{Arguments: map[string]any{}})
	require.NoError(t, err)
	assert.Nil(t, store.entry("int-flaky", mcpHealthAllScopes).FailingSince)
}

// A status change drops the integration's tools from every cached list on this
// pod and tells the other pods; lists without the integration stay.
func TestRecordMCPHealth_StatusChangeEvictsAndNotifies(t *testing.T) {
	store := useMemMCPHealthStore(t)
	cfg := mcpIntegrationConfig{IntegrationID: "int-x", IntegrationName: "x", ConnectionMode: "direct"}
	other := mcpIntegrationConfig{IntegrationID: "int-y", IntegrationName: "y", ConnectionMode: "direct"}
	t.Cleanup(func() {
		for _, a := range []string{"acc-1", "acc-2", "acc-3"} {
			mcpIntegrationToolCacheInstance.delete(a)
		}
	})
	mcpIntegrationToolCacheInstance.set("acc-1", []NBTool{mcpIntegrationTool{config: cfg, toolName: "x_a"}})
	mcpIntegrationToolCacheInstance.set("acc-2", []NBTool{mcpIntegrationTool{config: cfg, toolName: "x_a"}, mcpIntegrationTool{config: other, toolName: "y_a"}})
	mcpIntegrationToolCacheInstance.set("acc-3", []NBTool{mcpIntegrationTool{config: other, toolName: "y_a"}})

	var notified []string
	orig := MCPIntegrationHealthChanged
	MCPIntegrationHealthChanged = func(id string, accounts []string) { notified = append(notified, id+" "+fmt.Sprint(accounts)) }
	t.Cleanup(func() { MCPIntegrationHealthChanged = orig })

	held := time.Now().Add(-3 * time.Minute)
	store.health["int-x"] = mcpHealth{mcpHealthAllScopes: {Status: mcpHealthConnected, FailingSince: &held, LastCheckedAt: &held}}
	recordMCPHealth("acc-1", cfg, mcpFailure(mcpHealthUnreachable, "refused", true), false)

	for acc, want := range map[string]bool{"acc-1": false, "acc-2": false, "acc-3": true} {
		_, cached := mcpIntegrationToolCacheInstance.get(acc)
		assert.Equal(t, want, cached, acc)
	}
	assert.Equal(t, []string{"int-x []"}, notified, "direct: every account")

	// No change, no notice.
	recordMCPHealth("acc-1", cfg, mcpFailure(mcpHealthUnreachable, "refused", true), false)
	assert.Len(t, notified, 1)
}

// vm_agent health is per account: one agent offline marks only its account.
func TestRecordMCPHealth_VMAgentScopeIsTheAccount(t *testing.T) {
	store := useMemMCPHealthStore(t)
	cfg := mcpIntegrationConfig{IntegrationID: "int-vm", IntegrationName: "vm", ConnectionMode: "vm_agent"}
	recordMCPHealth("acc-a", cfg, mcpFailure(mcpHealthUnreachable, "agent not connected", true), false)
	recordMCPHealth("acc-b", cfg, mcpSuccess(true, 3), false)
	assert.Equal(t, mcpHealthUnreachable, store.entry("int-vm", "acc-a").Status)
	assert.Equal(t, mcpHealthConnected, store.entry("int-vm", "acc-b").Status)
	assert.Empty(t, store.entry("int-vm", mcpHealthAllScopes).Status)
}

// A disabled integration's health is not written (it is reset when re-enabled).
func TestRecordMCPHealth_DisabledIntegrationIsNotWritten(t *testing.T) {
	store := useMemMCPHealthStore(t)
	store.disabled["int-off"] = true
	cfg := mcpIntegrationConfig{IntegrationID: "int-off", IntegrationName: "off"}
	_, known := recordMCPHealth("acc", cfg, mcpFailure(mcpHealthUnreachable, "refused", true), false)
	assert.False(t, known)
	assert.Empty(t, store.health["int-off"])
}

// A timed-out tool call starts one probe; the probe's tools/list decides.
func TestMCPToolCall_TimeoutStartsOneProbe(t *testing.T) {
	const accountId = "acct-mcp-suspect"
	store := useMemMCPHealthStore(t)
	stub := newMCPRelayStub(t)
	cfg := mcpIntegrationConfig{IntegrationID: "int-hung", IntegrationName: "hung", ConnectionMode: "direct"}
	now := time.Now()
	store.health["int-hung"] = mcpHealth{mcpHealthAllScopes: {Status: mcpHealthConnected, LastCheckedAt: &now}}

	var listed atomic.Int32
	stub.set(func(_ string, method string) (int, string) {
		if method == "tools/list" {
			listed.Add(1)
			return http.StatusOK, mcpToolsListBody("run")
		}
		return http.StatusGatewayTimeout, `{"error":"timeout waiting for agent"}`
	})

	probesDone := func() {
		require.Eventually(t, func() bool {
			_, running := mcpSuspectProbes.Load("int-hung/*")
			return !running
		}, 5*time.Second, 10*time.Millisecond)
	}
	for range 3 {
		probeSuspectMCPIntegration(accountId, cfg)
	}
	probesDone()
	assert.Equal(t, int32(1), listed.Load(), "three timeouts, one probe")

	e := store.entry("int-hung", mcpHealthAllScopes)
	assert.Equal(t, mcpHealthConnected, e.Status, "tools/list answered: a slow tool, not a dead server")
	assert.NotNil(t, e.LastProbeAt)

	// Within the interval, another timeout (here or on another pod) probes nothing.
	probeSuspectMCPIntegration(accountId, cfg)
	probesDone()
	assert.Equal(t, int32(1), listed.Load())
}

func TestDBMCPHealthStore_Update(t *testing.T) {
	mock := registerMockMetastore(t)
	store := dbMCPHealthStore{}
	now := time.Now().UTC().Truncate(time.Second)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT status, health FROM integrations WHERE id = \$1::uuid FOR NO KEY UPDATE`).WithArgs("int-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "health"}).AddRow("enabled", []byte(`{"acc-other":{"status":"connected"}}`)))
	mock.ExpectExec(`UPDATE integrations SET health = \$2::jsonb WHERE id = \$1::uuid`).
		WithArgs("int-1", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	prev, next, written, err := store.update("int-1", mcpHealthAllScopes, func(prev mcpHealthEntry) (mcpHealthEntry, bool) {
		return nextMCPHealth(prev, mcpFailure(mcpHealthUnreachable, "refused", true), now, false)
	})
	require.NoError(t, err)
	assert.True(t, written)
	assert.Empty(t, prev.Status)
	assert.Equal(t, mcpHealthUnreachable, next.Status)

	// Disabled meanwhile: nothing is written.
	mock.ExpectBegin()
	mock.ExpectQuery(`FOR NO KEY UPDATE`).WithArgs("int-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "health"}).AddRow("disabled", nil))
	mock.ExpectCommit()
	_, _, written, err = store.update("int-1", mcpHealthAllScopes, func(prev mcpHealthEntry) (mcpHealthEntry, bool) { return prev, true })
	require.NoError(t, err)
	assert.False(t, written)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDBMCPHealthStore_LoadAndPending(t *testing.T) {
	mock := registerMockMetastore(t)
	store := dbMCPHealthStore{}

	mock.ExpectQuery(`SELECT id::text, health FROM integrations WHERE id = ANY\(\$1::uuid\[\]\) AND health IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "health"}).
			AddRow("int-1", []byte(`{"*":{"status":"unreachable","failing_since":"2026-10-13T00:00:00Z"}}`)).
			AddRow("int-2", []byte(`not json`)))
	got, err := store.load([]string{"int-1", "int-2"})
	require.NoError(t, err)
	assert.Equal(t, mcpHealthUnreachable, got["int-1"][mcpHealthAllScopes].Status)
	assert.NotContains(t, got, "int-2", "unreadable health is ignored, not fatal")

	mock.ExpectQuery(`health -> \$2 ->> 'failing_since'`).WithArgs("int-1", "*").
		WillReturnRows(sqlmock.NewRows([]string{"pending"}).AddRow(true))
	pending, err := store.pendingFailure("int-1", "*")
	require.NoError(t, err)
	assert.True(t, pending)
	require.NoError(t, mock.ExpectationsWereMet())
}
