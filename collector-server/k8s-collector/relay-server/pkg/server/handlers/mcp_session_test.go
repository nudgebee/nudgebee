package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"nudgebee/relay-server/pkg/db"
)

// fakeMCPServer issues a new Mcp-Session-Id on every initialize, answers 400
// for requests without a session and 404 for an unknown session (as the MCP
// spec requires), and records DELETE terminations. With sessionless set,
// initialize issues no id and every call succeeds.
type fakeMCPServer struct {
	mu          sync.Mutex
	next        int
	inits       int
	sessionless bool
	onInit      func() // runs while an initialize is in flight
	live        map[string]bool
	deleted     []string
	deleteAuth  []string
}

func (f *fakeMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sid := r.Header.Get("Mcp-Session-Id")
	if r.Method == http.MethodDelete {
		f.deleted = append(f.deleted, sid)
		f.deleteAuth = append(f.deleteAuth, r.Header.Get("Authorization"))
		delete(f.live, sid)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var rpc struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &rpc)
	if rpc.Method == "initialize" {
		f.inits++
		if f.onInit != nil {
			f.onInit()
		}
		if !f.sessionless {
			f.next++
			sid = fmt.Sprintf("s%d", f.next)
			f.live[sid] = true
			w.Header().Set("Mcp-Session-Id", sid)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":0,"result":{}}`))
		return
	}
	if f.sessionless {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		return
	}
	if sid == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !f.live[sid] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"session":%q}}`, sid)
}

func (f *fakeMCPServer) counts() (inits int, deleted, auth []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inits, append([]string(nil), f.deleted...), append([]string(nil), f.deleteAuth...)
}

// storeKinds runs a test against the per-pod memory store and against Redis
// (miniredis). newPod returns a store as a separate relay pod would see it:
// memory stores are independent, Redis stores share one server.
var storeKinds = []struct {
	name   string
	newPod func(t *testing.T) func() mcpSessionStore
}{
	{"memory", func(t *testing.T) func() mcpSessionStore {
		return func() mcpSessionStore { return newMemMCPSessionStore() }
	}},
	{"redis", func(t *testing.T) func() mcpSessionStore {
		mr := miniredis.RunT(t)
		return func() mcpSessionStore {
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			return newRedisMCPSessionStore(client, mcpSessionTTL)
		}
	}},
}

// setupFakeMCP starts a fake server, swaps in a plain HTTP client (the real one
// refuses loopback via the SSRF guard), and installs store as this "pod"'s
// session directory with clean local state.
func setupFakeMCP(t *testing.T, store mcpSessionStore) (*fakeMCPServer, string) {
	t.Helper()
	fake := &fakeMCPServer{live: map[string]bool{}}
	srv := httptest.NewServer(fake)
	origClient, origStore, origCreds := mcpHTTPClient, mcpSessions, mcpCredentials
	mcpHTTPClient = srv.Client()
	mcpSessions = store
	mcpCredentials = nil
	resetLocalAuth()
	t.Cleanup(func() {
		srv.Close()
		mcpHTTPClient, mcpSessions, mcpCredentials = origClient, origStore, origCreds
		resetLocalAuth()
	})
	return fake, srv.URL
}

func resetLocalAuth() {
	mcpLocalAuth.Lock()
	mcpLocalAuth.m = map[mcpSessionKey]mcpAuthEntry{}
	mcpLocalAuth.Unlock()
}

const testMCPRawBody = `{"body":{"action_params":{"auth_type":"bearer","auth_credentials":{"bearer_token":"tok"},"method":"tools/call","params":{"name":"x"}}}}`

func testKey(url, session string) mcpSessionKey {
	return mcpSessionKey{Account: "acct", Session: session, URL: url}
}

func callMCPStatus(t *testing.T, key mcpSessionKey) (string, int) {
	t.Helper()
	ctx := context.Background()
	raw := []byte(testMCPRawBody)
	sid, err := ensureMCPSession(ctx, key, "int-1", raw, slog.Default())
	if err != nil {
		t.Fatalf("ensureMCPSession: %v", err)
	}
	data, status, err := doMCPRequest(ctx, key, "int-1", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`), sid, raw, slog.Default())
	if err != nil {
		t.Fatalf("doMCPRequest: %v", err)
	}
	return data, status
}

func callMCP(t *testing.T, key mcpSessionKey) string {
	t.Helper()
	data, status := callMCPStatus(t, key)
	if status != http.StatusOK {
		t.Fatalf("doMCPRequest: status=%d data=%s", status, data)
	}
	return data
}

func result(sid string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":{"session":%q}}`, sid)
}

func waitForDeletes(t *testing.T, fake *fakeMCPServer, n int) ([]string, []string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, deleted, auth := fake.counts()
		if len(deleted) >= n || time.Now().After(deadline) {
			return deleted, auth
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sweepAt(offset time.Duration) {
	sweepIdleMCPSessions(context.Background(), time.Now().Add(offset), slog.Default())
}

func TestMCPSession_SeparateCallerSessionsGetSeparateUpstreamSessions(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(k.name, func(t *testing.T) {
			_, url := setupFakeMCP(t, k.newPod(t)())
			a1 := callMCP(t, testKey(url, "conv-a"))
			b := callMCP(t, testKey(url, "conv-b"))
			a2 := callMCP(t, testKey(url, "conv-a"))
			if a1 != result("s1") || a2 != result("s1") || b != result("s2") {
				t.Fatalf("want conv-a on s1 twice and conv-b on s2, got %s %s %s", a1, a2, b)
			}
		})
	}
}

func TestMCPSession_Upstream404ReinitializesAndRetries(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(k.name, func(t *testing.T) {
			fake, url := setupFakeMCP(t, k.newPod(t)())
			callMCP(t, testKey(url, "conv"))
			fake.mu.Lock()
			fake.live = map[string]bool{} // server restarted
			fake.mu.Unlock()
			if got := callMCP(t, testKey(url, "conv")); got != result("s2") {
				t.Fatalf("expected retry on fresh session s2, got %s", got)
			}
		})
	}
}

func TestMCPSession_SessionlessServerInitializedOncePerKey(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(k.name, func(t *testing.T) {
			fake, url := setupFakeMCP(t, k.newPod(t)())
			fake.sessionless = true
			for range 3 {
				callMCP(t, testKey(url, "conv"))
			}
			if inits, _, _ := fake.counts(); inits != 1 {
				t.Fatalf("sessionless server should be initialized once per key, got %d", inits)
			}
		})
	}
}

func TestMCPSession_SessionlessEntryDroppedWhenServerRejectsCall(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(k.name, func(t *testing.T) {
			_, url := setupFakeMCP(t, k.newPod(t)())
			key := testKey(url, "conv")
			ctx := context.Background()
			if _, stored, _ := mcpSessions.PutIfAbsent(ctx, key, mcpSessionRecord{}); !stored {
				t.Fatal("setup: sessionless record not stored")
			}
			// The server now requires sessions: a call without one gets 400.
			if _, status := callMCPStatus(t, key); status != http.StatusBadRequest {
				t.Fatalf("expected 400 from session-requiring server, got %d", status)
			}
			if _, ok, _ := mcpSessions.Get(ctx, key); ok {
				t.Fatal("stale sessionless entry should be dropped")
			}
			callMCP(t, key) // re-initializes and succeeds with a real session
		})
	}
}

func TestMCPSession_RealSessionReplacesConcurrentSessionlessEntry(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(k.name, func(t *testing.T) {
			fake, url := setupFakeMCP(t, k.newPod(t)())
			key := testKey(url, "conv")
			// While our initialize is in flight, a concurrent request stores
			// the key as sessionless (a server with mixed versions mid-upgrade).
			fake.onInit = func() {
				_, _, _ = mcpSessions.PutIfAbsent(context.Background(), key, mcpSessionRecord{})
			}
			sid, err := ensureMCPSession(context.Background(), key, "int-1", []byte(testMCPRawBody), slog.Default())
			if err != nil || sid != "s1" {
				t.Fatalf("expected the real session to win, got %q %v", sid, err)
			}
			if rec, _, _ := mcpSessions.Get(context.Background(), key); rec.SessionID != "s1" {
				t.Fatalf("real session should replace the sessionless entry, got %q", rec.SessionID)
			}
		})
	}
}

func TestMCPSession_IdleSessionClosedBySweep(t *testing.T) {
	for _, k := range storeKinds {
		t.Run(k.name, func(t *testing.T) {
			fake, url := setupFakeMCP(t, k.newPod(t)())
			callMCP(t, testKey(url, "conv"))

			sweepAt(mcpSessionTTL - time.Minute) // still within the idle timeout
			if _, deleted, _ := fake.counts(); len(deleted) != 0 {
				t.Fatalf("session closed before its idle timeout: %v", deleted)
			}

			sweepAt(mcpSessionTTL + time.Minute)
			deleted, auth := waitForDeletes(t, fake, 1)
			if len(deleted) != 1 || deleted[0] != "s1" || auth[0] != "Bearer tok" {
				t.Fatalf("expected DELETE of s1 with the request's auth, got %v %v", deleted, auth)
			}
			if got := callMCP(t, testKey(url, "conv")); got != result("s2") {
				t.Fatalf("a closed session must not be reused, got %s", got)
			}
		})
	}
}

// Two relay pods sharing Redis: a chat's second call, served by the other pod,
// continues the same upstream session (the multi-pod bug this store fixes).
func TestMCPSession_SecondPodContinuesSameSession(t *testing.T) {
	newPod := storeKinds[1].newPod(t)
	podA, podB := newPod(), newPod()
	fake, url := setupFakeMCP(t, podA)
	key := testKey(url, "conv")

	callMCP(t, key)
	mcpSessions = podB
	resetLocalAuth()
	if got := callMCP(t, key); got != result("s1") {
		t.Fatalf("pod B should continue pod A's session s1, got %s", got)
	}
	if inits, _, _ := fake.counts(); inits != 1 {
		t.Fatalf("pod B opened a second session (%d initializes)", inits)
	}
}

// Two pods sweeping at once: each idle session is ended exactly once.
func TestMCPSession_ConcurrentSweepsEndSessionOnce(t *testing.T) {
	newPod := storeKinds[1].newPod(t)
	podA, podB := newPod(), newPod()
	fake, url := setupFakeMCP(t, podA)
	callMCP(t, testKey(url, "conv"))

	var wg sync.WaitGroup
	for _, pod := range []mcpSessionStore{podA, podB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, _ := pod.ClaimIdle(context.Background(), time.Now().Add(time.Minute), 10)
			for _, c := range claimed {
				terminateMCPSession(c.Key, c.Record, []byte(testMCPRawBody), slog.Default())
			}
		}()
	}
	wg.Wait()
	if _, deleted, _ := fake.counts(); len(deleted) != 1 {
		t.Fatalf("expected exactly one DELETE, got %v", deleted)
	}
}

type fakeCredentialLoader struct {
	auth db.MCPIntegrationAuth
	err  error
	got  []string
}

func (f *fakeCredentialLoader) GetMCPIntegrationAuth(_ context.Context, accountID, integrationID string) (db.MCPIntegrationAuth, error) {
	f.got = append(f.got, accountID+"/"+integrationID)
	return f.auth, f.err
}

// A pod that never served the chat ends its idle session with credentials
// reloaded by integration id, only if they point at the session's URL.
func TestMCPSession_SweepReloadsCredentialsOnAnotherPod(t *testing.T) {
	cases := []struct {
		name       string
		loader     *fakeCredentialLoader
		wantDelete bool
	}{
		{"matching integration", &fakeCredentialLoader{}, true},
		{"integration URL differs", &fakeCredentialLoader{auth: db.MCPIntegrationAuth{URL: "https://elsewhere.example.com/mcp"}}, false},
		{"integration not found", &fakeCredentialLoader{err: db.ErrMCPIntegrationNotFound}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newPod := storeKinds[1].newPod(t)
			podA, podB := newPod(), newPod()
			fake, url := setupFakeMCP(t, podA)
			callMCP(t, testKey(url, "conv"))

			mcpSessions = podB
			resetLocalAuth() // pod B never saw the chat's credentials
			if c.loader.auth.URL == "" && c.loader.err == nil {
				c.loader.auth = db.MCPIntegrationAuth{URL: url, AuthType: "bearer",
					AuthCredentials: map[string]string{"bearer_token": "from-db"}}
			}
			mcpCredentials = c.loader
			sweepAt(mcpSessionTTL + time.Minute)

			if c.wantDelete {
				deleted, auth := waitForDeletes(t, fake, 1)
				if len(deleted) != 1 || auth[0] != "Bearer from-db" {
					t.Fatalf("expected DELETE with reloaded credentials, got %v %v", deleted, auth)
				}
				if len(c.loader.got) != 1 || c.loader.got[0] != "acct/int-1" {
					t.Fatalf("credentials loaded for the wrong integration: %v", c.loader.got)
				}
				return
			}
			if _, deleted, _ := fake.counts(); len(deleted) != 0 {
				t.Fatalf("credentials must not be sent when the integration does not match: %v", deleted)
			}
		})
	}
}

// failingStore fails every operation, as Redis does during an outage.
type failingStore struct{}

var errRedisDown = errors.New("redis down")

func (failingStore) Get(context.Context, mcpSessionKey) (mcpSessionRecord, bool, error) {
	return mcpSessionRecord{}, false, errRedisDown
}
func (failingStore) PutIfAbsent(context.Context, mcpSessionKey, mcpSessionRecord) (mcpSessionRecord, bool, error) {
	return mcpSessionRecord{}, false, errRedisDown
}
func (failingStore) Touch(context.Context, mcpSessionKey) error { return errRedisDown }
func (failingStore) DeleteIf(context.Context, mcpSessionKey, string) (bool, error) {
	return false, errRedisDown
}
func (failingStore) ClaimIdle(context.Context, time.Time, int) ([]claimedMCPSession, error) {
	return nil, errRedisDown
}

// During a Redis outage calls still work on the pod's memory, and sessions
// opened then are still ended by the sweep.
func TestMCPSession_RedisOutageFallsBackToMemory(t *testing.T) {
	store := &fallbackMCPSessionStore{primary: failingStore{}, fallback: newMemMCPSessionStore(), logger: slog.Default()}
	fake, url := setupFakeMCP(t, store)
	a1 := callMCP(t, testKey(url, "conv"))
	a2 := callMCP(t, testKey(url, "conv"))
	if a1 != result("s1") || a2 != result("s1") {
		t.Fatalf("one chat should keep one session on this pod during the outage, got %s %s", a1, a2)
	}
	sweepAt(mcpSessionTTL + time.Minute)
	if deleted, _ := waitForDeletes(t, fake, 1); len(deleted) != 1 {
		t.Fatalf("session opened during the outage should still be closed, got %v", deleted)
	}
}

func TestIsMCPSessionGone(t *testing.T) {
	cases := []struct {
		name   string
		status int
		sid    string
		want   bool
	}{
		{"404 with session", 404, "s1", true},
		{"400 is never treated as gone", 400, "s1", false},
		{"404 without session", 404, "", false},
		{"200", 200, "s1", false},
	}
	for _, c := range cases {
		if got := isMCPSessionGone(c.status, c.sid); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRedisMCPMember_URLWithColonsRoundTrips(t *testing.T) {
	key := mcpSessionKey{Account: "acct", Session: "wf__a__b", URL: "https://host:8443/mcp?x=1:2"}
	var back mcpSessionKey
	if err := json.Unmarshal([]byte(redisMCPMember(key)), &back); err != nil || back != key {
		t.Fatalf("member did not round-trip: %v %+v", err, back)
	}
	if !strings.HasPrefix(redisMCPHashKey(key), "mcp:sess:acct:") {
		t.Fatalf("unexpected hash key %q", redisMCPHashKey(key))
	}
}

// The stored integration credentials win over this pod's copy, which for
// runbook OAuth2 integrations is a short-lived bearer token.
func TestMCPSession_SweepPrefersStoredCredentials(t *testing.T) {
	fake, url := setupFakeMCP(t, newMemMCPSessionStore())
	callMCP(t, testKey(url, "conv")) // this pod keeps "Bearer tok"
	mcpCredentials = &fakeCredentialLoader{auth: db.MCPIntegrationAuth{URL: url, AuthType: "bearer",
		AuthCredentials: map[string]string{"bearer_token": "from-db"}}}
	sweepAt(mcpSessionTTL + time.Minute)
	if _, auth := waitForDeletes(t, fake, 1); len(auth) != 1 || auth[0] != "Bearer from-db" {
		t.Fatalf("expected the stored credentials on DELETE, got %v", auth)
	}
}

// A session opened without integration_id (an older caller) on another pod
// is left to the server's timeout, with no DELETE and no credentials guessed.
func TestMCPSession_NoIntegrationIDAndNoLocalCopySendsNoDelete(t *testing.T) {
	fake, url := setupFakeMCP(t, newMemMCPSessionStore())
	key := testKey(url, "conv")
	if _, err := ensureMCPSession(context.Background(), key, "", []byte(testMCPRawBody), slog.Default()); err != nil {
		t.Fatal(err)
	}
	resetLocalAuth()
	sweepAt(mcpSessionTTL + time.Minute)
	if _, deleted, _ := fake.counts(); len(deleted) != 0 {
		t.Fatalf("expected no DELETE without credentials, got %v", deleted)
	}
}

type countingFailingStore struct {
	failingStore
	mu    sync.Mutex
	calls int
}

func (c *countingFailingStore) Get(ctx context.Context, k mcpSessionKey) (mcpSessionRecord, bool, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.failingStore.Get(ctx, k)
}

// After a Redis failure the store skips Redis for a while, so an outage costs
// one timeout rather than one per operation on every MCP call.
func TestMCPSession_RedisFailureSkipsRedisForAWhile(t *testing.T) {
	primary := &countingFailingStore{}
	store := &fallbackMCPSessionStore{primary: primary, fallback: newMemMCPSessionStore(), logger: slog.Default()}
	ctx := context.Background()
	for range 5 {
		_, _, _ = store.Get(ctx, testKey("https://mcp.example.com", "conv"))
	}
	if primary.calls != 1 {
		t.Fatalf("expected Redis to be tried once then skipped, got %d calls", primary.calls)
	}
}

// The request's session_id and integration_id reach the key and the stored
// record: without integration_id, other pods could never end the session.
func TestMCPSessionKeyFromRequest_CarriesSessionAndIntegration(t *testing.T) {
	raw := []byte(`{"body":{"action_params":{"session_id":"wf__a__b","integration_id":"int-9","url":"https://mcp.example.com/mcp"}}}`)
	key, iid := mcpSessionKeyFromRequest(raw, "acct", "https://mcp.example.com/mcp")
	if key != (mcpSessionKey{Account: "acct", Session: "wf__a__b", URL: "https://mcp.example.com/mcp"}) || iid != "int-9" {
		t.Fatalf("unexpected key %+v / integration %q", key, iid)
	}

	_, url := setupFakeMCP(t, newMemMCPSessionStore())
	key.URL = url
	if _, err := ensureMCPSession(context.Background(), key, iid, []byte(testMCPRawBody), slog.Default()); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := mcpSessions.Get(context.Background(), key); rec.IntegrationID != "int-9" {
		t.Fatalf("integration id not stored with the session: %+v", rec)
	}
}

func TestMCPSessionKeyFromRequest_DiscoveryHasNoSession(t *testing.T) {
	key, iid := mcpSessionKeyFromRequest([]byte(`{"body":{"action_params":{}}}`), "acct", "https://mcp.example.com/mcp")
	if key.Session != "" || iid != "" {
		t.Fatalf("expected empty session and integration for discovery, got %+v %q", key, iid)
	}
}
