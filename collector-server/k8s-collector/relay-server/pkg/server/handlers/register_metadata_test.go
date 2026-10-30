package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"nudgebee/relay-server/pkg/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// metadataFakeStore tracks how many times UpdateDatasourceMetadata was
// called and lets a test script which dsIDs "aren't found" (integrations
// row not yet created) on each call, to exercise handleDatasourceMetadata's
// retry loop deterministically.
type metadataFakeStore struct {
	fakeStore
	calls      int
	notFoundOn []map[string]bool // notFoundOn[i] = dsIDs still not found on the i-th call
	changedOn  []bool            // changedOn[i] = whether the i-th call changed any labels
}

type orderedMetadataStore struct {
	fakeStore
	mu        sync.Mutex
	started   chan struct{}
	release   chan struct{}
	attempted chan struct{}
	exists    bool
	labels    map[string]any
}

func (f *orderedMetadataStore) UpsertAgentDatasources(ctx context.Context, accountID, agentType, agentID string, datasources []db.AgentDatasource) error {
	close(f.started)
	select {
	case <-f.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	f.mu.Lock()
	f.exists = true
	f.mu.Unlock()
	return nil
}

func (f *orderedMetadataStore) UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) ([]string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case f.attempted <- struct{}{}:
	default:
	}
	if !f.exists {
		return []string{"local:vm-agent"}, false, nil
	}
	f.labels = metadata["local:vm-agent"]
	return nil, true, nil
}

func TestInventoryCompletesBeforeFollowingMetadata(t *testing.T) {
	store := &orderedMetadataStore{started: make(chan struct{}), release: make(chan struct{}), attempted: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inventory := []byte(`{"action":"datasource_inventory","datasources":[{"id":"local:vm-agent","type":"ssh","name":"vm-agent"}]}`)
	metadata := []byte(`{"action":"datasource_metadata","metadata":{"local:vm-agent":{"actions":["discovery_sweep"],"allowed_cidrs":["10.0.0.0/24"]}}}`)

	var dispatch datasourceDispatch
	dispatch.inventory(ctx, func() {
		handleDatasourceInventory(ctx, store, nil, "acct-1", "proxy", "agent-1", inventory, discardLogger())
	})
	<-store.started
	metadataDone := dispatch.metadata(ctx, func() {
		handleDatasourceMetadata(ctx, store, nil, "acct-1", "proxy", metadata, discardLogger())
	})
	select {
	case <-store.attempted:
		t.Fatal("metadata ran before inventory completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(store.release)
	select {
	case <-metadataDone:
	case <-time.After(time.Second):
		t.Fatal("metadata did not finish after inventory")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, []any{"discovery_sweep"}, store.labels["actions"])
	assert.Equal(t, []any{"10.0.0.0/24"}, store.labels["allowed_cidrs"])
}

func TestInventoryWaitStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{})
	dispatch := datasourceDispatch{inventoryDone: make(chan struct{})}
	done := dispatch.metadata(ctx, func() { close(called) })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiting handler did not stop")
	}
	select {
	case <-called:
		t.Fatal("handler ran after cancellation")
	default:
	}
}

func TestMetadataSnapshotsStayInOrder(t *testing.T) {
	var dispatch datasourceDispatch
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	dispatch.metadata(context.Background(), func() {
		close(firstStarted)
		<-releaseFirst
	})
	<-firstStarted
	secondDone := dispatch.metadata(context.Background(), func() { close(secondStarted) })
	select {
	case <-secondStarted:
		t.Fatal("second metadata ran before the first completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second metadata did not finish")
	}
}

func TestMetadataBeforeInventoryCanRetry(t *testing.T) {
	orig := metadataRetryDelays
	metadataRetryDelays = []time.Duration{100 * time.Millisecond, 100 * time.Millisecond}
	t.Cleanup(func() { metadataRetryDelays = orig })
	store := &orderedMetadataStore{started: make(chan struct{}), release: make(chan struct{}), attempted: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var dispatch datasourceDispatch
	metadata := metadataMsg(t)
	metadataDone := dispatch.metadata(ctx, func() {
		handleDatasourceMetadata(ctx, store, nil, "acct-1", "proxy", metadata, discardLogger())
	})
	<-store.attempted
	dispatch.inventory(ctx, func() {
		handleDatasourceInventory(ctx, store, nil, "acct-1", "proxy", "agent-1", []byte(`{"datasources":[{"id":"local:vm-agent","type":"ssh","name":"vm-agent"}]}`), discardLogger())
	})
	<-store.started
	close(store.release)
	select {
	case <-metadataDone:
	case <-time.After(time.Second):
		t.Fatal("metadata did not complete after inventory")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, []any{"10.0.0.0/24"}, store.labels["allowed_hosts"])
}

func (f *metadataFakeStore) UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) ([]string, bool, error) {
	i := f.calls
	f.calls++
	changed := i < len(f.changedOn) && f.changedOn[i]
	if i >= len(f.notFoundOn) {
		return nil, changed, nil
	}
	var notFound []string
	for dsID := range metadata {
		if f.notFoundOn[i][dsID] {
			notFound = append(notFound, dsID)
		}
	}
	return notFound, changed, nil
}

func withFastMetadataRetries(t *testing.T) {
	t.Helper()
	orig := metadataRetryDelays
	metadataRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { metadataRetryDelays = orig })
}

// The common case from #38718: metadata for one datasource races its
// datasource_inventory INSERT, then succeeds on the next attempt once the
// integrations row exists. handleDatasourceMetadata must retry only the
// still-missing keys, not the whole batch, and must stop as soon as
// nothing is left unmatched.
func TestHandleDatasourceMetadata_RetriesRacedKeyThenSucceeds(t *testing.T) {
	withFastMetadataRetries(t)
	store := &metadataFakeStore{
		notFoundOn: []map[string]bool{
			{"local:vm-agent": true}, // 1st call: raced, not found yet
			{},                       // 2nd call: found, update lands
		},
	}
	raw, err := json.Marshal(map[string]any{
		"action":   "datasource_metadata",
		"metadata": map[string]map[string]any{"local:vm-agent": {"actions": []string{"discovery_sweep"}}},
	})
	require.NoError(t, err)

	handleDatasourceMetadata(context.Background(), store, nil, "acct-1", "k8s", raw, discardLogger())

	assert.Equal(t, 2, store.calls, "must retry exactly once after the first miss, then stop")
}

// If every retry is exhausted and the key still isn't found, the handler
// must give up (not retry forever) and not panic — it's a best-effort
// self-heal, not a guarantee (see /challenge's counterargument 3: this
// narrows the race, it does not close it).
func TestHandleDatasourceMetadata_GivesUpAfterExhaustingRetries(t *testing.T) {
	withFastMetadataRetries(t)
	alwaysNotFound := map[string]bool{"local:vm-agent": true}
	store := &metadataFakeStore{
		notFoundOn: []map[string]bool{alwaysNotFound, alwaysNotFound, alwaysNotFound, alwaysNotFound, alwaysNotFound},
	}
	raw, err := json.Marshal(map[string]any{
		"action":   "datasource_metadata",
		"metadata": map[string]map[string]any{"local:vm-agent": {"actions": []string{"discovery_sweep"}}},
	})
	require.NoError(t, err)

	handleDatasourceMetadata(context.Background(), store, nil, "acct-1", "k8s", raw, discardLogger())

	assert.Equal(t, 1+len(metadataRetryDelays), store.calls, "must stop after the bounded retry budget, not loop forever")
}

// A metadata message with nothing raced (the common, non-racing path) must
// not retry at all.
func TestHandleDatasourceMetadata_NoRetryWhenNothingRaced(t *testing.T) {
	withFastMetadataRetries(t)
	store := &metadataFakeStore{}
	raw, err := json.Marshal(map[string]any{
		"action":   "datasource_metadata",
		"metadata": map[string]map[string]any{"local:vm-agent": {"actions": []string{"discovery_sweep"}}},
	})
	require.NoError(t, err)

	handleDatasourceMetadata(context.Background(), store, nil, "acct-1", "k8s", raw, discardLogger())

	assert.Equal(t, 1, store.calls)
}

// recordingInvalidator records llm cache invalidations synchronously.
type recordingInvalidator struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingInvalidator) InvalidateAccounts(accountIDs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), accountIDs...))
}

func (r *recordingInvalidator) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

func metadataMsg(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"action":   "datasource_metadata",
		"metadata": map[string]map[string]any{"local:vm-agent": {"allowed_hosts": []string{"10.0.0.0/24"}}},
	})
	require.NoError(t, err)
	return raw
}

// Labels carry what Nubi resolves targets from (e.g. an ssh datasource's
// allowed_hosts), so a real label change must bust llm-server's caches for
// the account — once, even when it took a retry to land.
func TestHandleDatasourceMetadata_InvalidatesLLMCacheWhenLabelsChange(t *testing.T) {
	withFastMetadataRetries(t)
	store := &metadataFakeStore{
		notFoundOn: []map[string]bool{{"local:vm-agent": true}, {}},
		changedOn:  []bool{false, true},
	}
	inv := &recordingInvalidator{}

	handleDatasourceMetadata(context.Background(), store, inv, "acct-1", "proxy", metadataMsg(t), discardLogger())

	assert.Equal(t, [][]string{{"acct-1"}}, inv.snapshot())
}

// Reconnects re-send identical metadata; that must not fan out a cache
// invalidation to every llm-server replica each time.
func TestHandleDatasourceMetadata_NoInvalidationWhenLabelsUnchanged(t *testing.T) {
	withFastMetadataRetries(t)
	store := &metadataFakeStore{changedOn: []bool{false}}
	inv := &recordingInvalidator{}

	handleDatasourceMetadata(context.Background(), store, inv, "acct-1", "proxy", metadataMsg(t), discardLogger())

	assert.Empty(t, inv.snapshot())
}

// An inventory message registers datasources under the reporting agent's id
// and invalidates llm-server's caches for the account, so a new datasource is
// usable by Nubi without waiting for the cache TTL.
func TestHandleDatasourceInventory_PassesAgentIDAndInvalidates(t *testing.T) {
	store := &fakeStore{}
	inv := &recordingInvalidator{}
	raw, err := json.Marshal(map[string]any{
		"action":      "datasource_inventory",
		"datasources": []map[string]any{{"id": "local:fleet:ssh", "type": "ssh", "proxy_type": "ssh-proxy", "name": "fleet-ssh"}},
	})
	require.NoError(t, err)

	handleDatasourceInventory(context.Background(), store, inv, "acct-1", "proxy", "agent-a", raw, discardLogger())

	assert.Equal(t, "agent-a", store.upsertAgentID)
	assert.Equal(t, [][]string{{"acct-1"}}, inv.snapshot())
}

// A partly failed upsert may still have registered or removed integrations,
// so the invalidation must not be skipped on error.
func TestHandleDatasourceInventory_InvalidatesEvenOnPartialFailure(t *testing.T) {
	store := &fakeStore{err: errors.New("one datasource failed")}
	inv := &recordingInvalidator{}
	raw, err := json.Marshal(map[string]any{
		"action":      "datasource_inventory",
		"datasources": []map[string]any{{"id": "local:pg", "type": "postgresql", "proxy_type": "db-proxy", "name": "pg"}},
	})
	require.NoError(t, err)

	handleDatasourceInventory(context.Background(), store, inv, "acct-1", "proxy", "agent-a", raw, discardLogger())

	assert.Equal(t, [][]string{{"acct-1"}}, inv.snapshot())
}

// Proxy agents consume their own queue plus the legacy shared one (rollout
// and unknown-owner fallback); every other case is the per-account queue
// alone, exactly as before.
func TestSessionQueueNames(t *testing.T) {
	assert.Equal(t,
		[]string{"relay_requests_acct-1_proxy_agent-a", "relay_requests_acct-1_proxy"},
		sessionQueueNames("acct-1", "proxy", "agent-a"))
	assert.Equal(t, []string{"relay_requests_acct-1_proxy"}, sessionQueueNames("acct-1", "proxy", ""))
	assert.Equal(t, []string{"relay_requests_acct-1"}, sessionQueueNames("acct-1", "k8s", "agent-k"))
}

var _ db.AgentStore = (*metadataFakeStore)(nil)
