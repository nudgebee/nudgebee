package db

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mapCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *mapCache) Get(_ context.Context, k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *mapCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}

func (c *mapCache) Delete(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}

const wsEntryQuery = `FROM cloud_accounts ca`

// The tenant rides on the agent-status row, so AccountTenant and
// GetAgentStatus together cost one query, and the second call is a cache hit.
func TestAccountTenant_SharesAgentStatusRow(t *testing.T) {
	store, mock := newMockStore(t)
	store.wsCache = &mapCache{m: map[string][]byte{}}

	mock.ExpectQuery(q(wsEntryQuery)).
		WithArgs("acct-1", "k8s").
		WillReturnRows(sqlmock.NewRows([]string{"connection_status", "status", "tenant"}).
			AddRow(`{"agentWSEnabled":true}`, "CONNECTED", "tenant-1"))

	tenant, err := store.AccountTenant(context.Background(), "acct-1", "k8s")
	require.NoError(t, err)
	assert.Equal(t, "tenant-1", tenant)

	connected, wsEnabled, _, _, err := store.GetAgentStatus(context.Background(), "acct-1", "k8s")
	require.NoError(t, err)
	assert.True(t, connected)
	assert.True(t, wsEnabled)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An account with no agent row still reports its tenant (LEFT JOIN), and the
// agent reads as not connected.
func TestAccountTenant_NoAgentRow(t *testing.T) {
	store, mock := newMockStore(t)
	store.wsCache = noopCache{}

	mock.ExpectQuery(q(wsEntryQuery)).
		WithArgs("acct-1", "k8s").
		WillReturnRows(sqlmock.NewRows([]string{"connection_status", "status", "tenant"}).
			AddRow(nil, nil, "tenant-1"))

	tenant, err := store.AccountTenant(context.Background(), "acct-1", "k8s")
	require.NoError(t, err)
	assert.Equal(t, "tenant-1", tenant)
}

func TestAccountTenant_UnknownAccount(t *testing.T) {
	store, mock := newMockStore(t)
	store.wsCache = noopCache{}

	mock.ExpectQuery(q(wsEntryQuery)).WithArgs("nope", "k8s").WillReturnError(sql.ErrNoRows)

	tenant, err := store.AccountTenant(context.Background(), "nope", "k8s")
	require.NoError(t, err)
	assert.Equal(t, "", tenant)
}

// Entries cached by a relay version that did not record the tenant must not be
// trusted: they are refetched, or every account would read as tenant-less for
// up to the cache TTL after a deploy.
func TestAccountTenant_LegacyCacheEntryIsAMiss(t *testing.T) {
	store, mock := newMockStore(t)
	c := &mapCache{m: map[string][]byte{
		wsCacheKey("acct-1", "k8s"): []byte(`{"allowed":true,"fallback":"","status":"CONNECTED","connection_status":{}}`),
	}}
	store.wsCache = c

	mock.ExpectQuery(q(wsEntryQuery)).
		WithArgs("acct-1", "k8s").
		WillReturnRows(sqlmock.NewRows([]string{"connection_status", "status", "tenant"}).
			AddRow(nil, "CONNECTED", "tenant-1"))

	tenant, err := store.AccountTenant(context.Background(), "acct-1", "k8s")
	require.NoError(t, err)
	assert.Equal(t, "tenant-1", tenant)
	require.NoError(t, mock.ExpectationsWereMet())
	cached, _ := c.Get(context.Background(), wsCacheKey("acct-1", "k8s"))
	assert.Contains(t, string(cached), `"tenant_id":"tenant-1"`)
}

type noopCache struct{}

func (noopCache) Get(context.Context, string) ([]byte, bool)               { return nil, false }
func (noopCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (noopCache) Delete(context.Context, string) error                     { return nil }
