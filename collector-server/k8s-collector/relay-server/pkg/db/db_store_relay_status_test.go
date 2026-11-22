package db

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The connect write stamps last_connected_at = NOW(), later than any time the
// relay took beforehand. The connect call must hand that stamp back so the
// disconnect guard compares the session against itself rather than against a
// clock it always postdates.
func TestUpdateRelayConnectionStatus_ConnectReturnsStoredStamp(t *testing.T) {
	store, mock := newMockStore(t)
	store.wsCache = noopCache{}
	stamp := time.Now().Add(time.Second).UTC().Truncate(time.Microsecond)

	mock.ExpectQuery(q(`last_connected_at = NOW()`)).
		WithArgs("acct-1", true, "k8s", "CONNECTED").
		WillReturnRows(sqlmock.NewRows([]string{"old_status", "tenant", "connected_at"}).AddRow("NOT_CONNECTED", "tenant-1", stamp))

	transitioned, tenant, connectedAt, err := store.UpdateRelayConnectionStatus(context.Background(), "acct-1", "k8s", true, time.Now())
	require.NoError(t, err)
	assert.True(t, transitioned)
	assert.Equal(t, "tenant-1", tenant)
	assert.True(t, connectedAt.Equal(stamp))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRelayConnectionStatus_DisconnectGuardsOnSessionMark(t *testing.T) {
	store, mock := newMockStore(t)
	store.wsCache = noopCache{}
	mark := time.Now().UTC().Truncate(time.Microsecond)

	mock.ExpectQuery(q(`prev.last_connected_at <= $5`)).
		WithArgs("acct-1", false, "k8s", "NOT_CONNECTED", mark).
		WillReturnRows(sqlmock.NewRows([]string{"old_status", "tenant", "connected_at"}).AddRow("CONNECTED", "tenant-1", mark))

	transitioned, _, connectedAt, err := store.UpdateRelayConnectionStatus(context.Background(), "acct-1", "k8s", false, mark)
	require.NoError(t, err)
	assert.True(t, transitioned)
	assert.True(t, connectedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

type noopCache struct{}

func (noopCache) Get(context.Context, string) ([]byte, bool)               { return nil, false }
func (noopCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (noopCache) Delete(context.Context, string) error                     { return nil }
