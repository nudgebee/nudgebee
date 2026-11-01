package budget

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cache namespace is process-wide, so each test run needs its own entity
// id: a fixed id would hit the previous run's entry under -count>1.
func uniqueID(base string) string {
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func TestCachedEntityTokenUsage_ReusesTotalWithinTTL(t *testing.T) {
	id := uniqueID("acct-cache-hit")
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery("WITH per_call_cost").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(12.5))

	first, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
	require.NoError(t, err)
	second, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
	require.NoError(t, err)

	assert.Equal(t, 12.5, first)
	assert.Equal(t, 12.5, second)
	assert.NoError(t, mock.ExpectationsWereMet(), "the second check must not query again")
}

func TestCachedEntityTokenUsage_DoesNotCacheErrors(t *testing.T) {
	id := uniqueID("acct-cache-err")
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery("WITH per_call_cost").
		WithArgs(id).
		WillReturnError(errors.New("connection reset"))
	mock.ExpectQuery("WITH per_call_cost").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(3.0))

	_, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleInvestigation, "month")
	require.Error(t, err)
	cost, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleInvestigation, "month")
	require.NoError(t, err)

	assert.Equal(t, 3.0, cost)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCachedEntityTokenUsage_QueriesSynchronouslyPastMaxStale(t *testing.T) {
	id := uniqueID("tenant-cache-exp")
	dm, mock := newMockMetastoreDM(t)
	// Older than enforcedUsageMaxStale: too stale to serve, so the check waits for a fresh total.
	fetched := time.Now().Add(-enforcedUsageMaxStale - time.Second)
	stale, err := common.MarshalJson(cachedUsage{Cost: 1.0, ExpiresAt: fetched.Add(enforcedUsageTTL), FetchedAt: fetched})
	require.NoError(t, err)
	key := enforcedUsageKey(EntityTypeTenant, id, ModuleInvestigation, "day", time.Now())
	require.NoError(t, common.CacheSet(enforcedUsageCacheNS, key, stale))
	mock.ExpectQuery("WITH per_call_cost").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(9.0))

	cost, err := cachedEntityTokenUsage(dm, EntityTypeTenant, id, ModuleInvestigation, "day")
	require.NoError(t, err)

	assert.Equal(t, 9.0, cost)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnforcedUsageKey_ChangesWhenThePeriodRollsOver(t *testing.T) {
	lastSecond := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	nextSecond := lastSecond.Add(time.Second)

	assert.NotEqual(t,
		enforcedUsageKey(EntityTypeAccount, "a", ModuleInvestigation, "month", lastSecond),
		enforcedUsageKey(EntityTypeAccount, "a", ModuleInvestigation, "month", nextSecond),
		"a September total must not be reused on October 1")
	assert.NotEqual(t,
		enforcedUsageKey(EntityTypeAccount, "a", ModuleInvestigation, "day", lastSecond),
		enforcedUsageKey(EntityTypeAccount, "a", ModuleInvestigation, "day", nextSecond))
	assert.Equal(t,
		enforcedUsageKey(EntityTypeAccount, "a", ModuleInvestigation, "month", lastSecond),
		enforcedUsageKey(EntityTypeAccount, "a", ModuleInvestigation, "month", lastSecond.Add(-time.Hour)),
		"within a period the key is stable, or the cache would never hit")
}

// withClock pins usageNow for one test.
func withClock(t *testing.T, now *time.Time) {
	t.Helper()
	orig := usageNow
	usageNow = func() time.Time { return *now }
	t.Cleanup(func() { usageNow = orig })
}

func TestCachedEntityTokenUsage_ServesStaleAndRefreshesInBackground(t *testing.T) {
	id := uniqueID("acct-swr")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	withClock(t, &now)
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(10.0))
	first, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
	require.NoError(t, err)
	require.Equal(t, 10.0, first)

	// Past the fresh window but within max stale: the old total comes back at once...
	now = now.Add(2 * time.Minute)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(25.0))
	stale, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
	require.NoError(t, err)
	assert.Equal(t, 10.0, stale)

	// ...and the background refresh lands the new one.
	require.Eventually(t, func() bool {
		cost, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
		return err == nil && cost == 25.0
	}, 2*time.Second, 5*time.Millisecond)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCachedEntityTokenUsage_FailedBackgroundRefreshKeepsPreviousTotal(t *testing.T) {
	id := uniqueID("acct-swr-fail")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	withClock(t, &now)
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(7.0))
	_, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).WillReturnError(errors.New("connection reset"))
	cost, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleUserInvestigation, "month")
	require.NoError(t, err)
	assert.Equal(t, 7.0, cost)
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 2*time.Second, 5*time.Millisecond)

	// The failure wasn't cached: the stored entry still holds the previous total.
	data, ok := common.CacheGet(enforcedUsageCacheNS, enforcedUsageKey(EntityTypeAccount, id, ModuleUserInvestigation, "month", now))
	require.True(t, ok)
	var cached cachedUsage
	require.NoError(t, common.UnmarshalJson(data, &cached))
	assert.Equal(t, 7.0, cached.Cost)
}

func TestRefreshUsage_CollapsesConcurrentQueriesForOneKey(t *testing.T) {
	id := uniqueID("acct-sf")
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).
		WillDelayFor(50 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(4.0))
	key := enforcedUsageKey(EntityTypeAccount, id, ModuleInvestigation, "month", time.Now())

	var wg sync.WaitGroup
	results := make([]float64, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = refreshUsage(dm, key, EntityTypeAccount, id, ModuleInvestigation, "month")
		}(i)
	}
	wg.Wait()
	for _, r := range results {
		assert.Equal(t, 4.0, r)
	}
	assert.NoError(t, mock.ExpectationsWereMet(), "five concurrent refreshes must run one query")
}

func TestCachedEntityTokenUsage_BurstOfStaleReadsStartsOneRefresh(t *testing.T) {
	id := uniqueID("acct-burst")
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	withClock(t, &now)
	dm, mock := newMockMetastoreDM(t)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(1.0))
	_, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleInvestigation, "month")
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)
	mock.ExpectQuery("WITH per_call_cost").WithArgs(id).
		WillDelayFor(50 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"total_cost"}).AddRow(2.0))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cost, err := cachedEntityTokenUsage(dm, EntityTypeAccount, id, ModuleInvestigation, "month")
			assert.NoError(t, err)
			assert.Equal(t, 1.0, cost, "stale reads return the previous total without waiting")
		}()
	}
	wg.Wait()
	// Exactly one background query ran; sqlmock would fail any extra one.
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 2*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		_, running := backgroundRefreshes.Load(enforcedUsageKey(EntityTypeAccount, id, ModuleInvestigation, "month", now))
		return !running
	}, 2*time.Second, 5*time.Millisecond, "the in-flight marker is cleared once the refresh finishes")
}
