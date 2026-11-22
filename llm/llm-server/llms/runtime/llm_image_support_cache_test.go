package runtime

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetImageSupportCache clears the package-level cache singleton so each
// test starts from a cold, stale state regardless of test execution order.
func resetImageSupportCache(t *testing.T) {
	t.Helper()
	imageSupport.mu.Lock()
	imageSupport.values = nil
	imageSupport.loadedAt = time.Time{}
	imageSupport.mu.Unlock()
}

func TestIsVisionCapableModel_DBVerdictFalseIsRespected(t *testing.T) {
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	store := installCatalogStore(t)

	store.queue(map[string]bool{"bedrock:some-custom-model": false}, nil)

	assert.False(t, IsVisionCapableModel("", "bedrock", "some-custom-model"))
}

func TestIsVisionCapableModel_DBVerdictTrueIsRespected(t *testing.T) {
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	store := installCatalogStore(t)

	// "some-custom-model" isn't caught by any regex pattern. Under
	// default-deny it would otherwise be treated as non-vision-capable — an
	// explicit true row is the only way to make it vision-capable.
	store.queue(map[string]bool{"bedrock:some-custom-model": true}, nil)

	assert.True(t, IsVisionCapableModel("", "bedrock", "some-custom-model"))
}

func TestIsVisionCapableModel_RegexDenyWinsOverDBTrue(t *testing.T) {
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	store := installCatalogStore(t)

	// Even if the DB explicitly says true, the regex deny-list still wins —
	// it runs first and returns before the DB lookup happens.
	store.queue(map[string]bool{"openai:gpt-3.5-turbo": true}, nil)

	assert.False(t, IsVisionCapableModel("", "openai", "gpt-3.5-turbo"))
}

func TestIsVisionCapableModel_UnknownDefaultsToNotVisionCapable(t *testing.T) {
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	store := installCatalogStore(t)

	// Catalog loads successfully but has no row for this model — still
	// unknown. Default-deny: no positive DB record means not vision-capable.
	store.queue(map[string]bool{}, nil)

	assert.False(t, IsVisionCapableModel("", "openai", "some-unlisted-model"))
}

func TestImageSupportCache_QueryErrorKeepsPreviousSnapshotAndDoesNotBackOff(t *testing.T) {
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	store := installCatalogStore(t)

	// First load succeeds and primes the cache.
	store.queue(map[string]bool{"openai:gpt-4o": true}, nil)
	supported, known := imageSupport.lookup("openai", "gpt-4o")
	require.True(t, known)
	require.True(t, supported)

	// Force staleness; the second refresh attempt fails.
	imageSupport.mu.Lock()
	imageSupport.loadedAt = time.Time{}
	imageSupport.mu.Unlock()
	store.queue(nil, errors.New("connection reset"))

	supported, known = imageSupport.lookup("openai", "gpt-4o")
	assert.True(t, known, "previous snapshot must survive a failed refresh")
	assert.True(t, supported)

	// loadedAt must NOT be stamped on failure — an outage (or DAO not yet
	// established) must not be cached as a 5-minute-long "known empty"
	// catalog. The next lookup should retry immediately rather than back off.
	imageSupport.mu.RLock()
	loadedAt := imageSupport.loadedAt
	imageSupport.mu.RUnlock()
	assert.True(t, loadedAt.IsZero(), "a failed refresh must not update loadedAt")
}

func TestImageSupportCache_NilDAODoesNotDisableCacheForTTLWindow(t *testing.T) {
	resetImageSupportCache(t)
	defer resetImageSupportCache(t)

	installStore(t, nil)

	// With no DAO established (e.g. at process startup), a lookup must not
	// cache "unknown" for the full TTL — it must retry as soon as the DAO
	// becomes available, not 5 minutes later.
	_, known := imageSupport.lookup("openai", "gpt-4o")
	assert.False(t, known)

	imageSupport.mu.RLock()
	loadedAt := imageSupport.loadedAt
	imageSupport.mu.RUnlock()
	assert.True(t, loadedAt.IsZero(), "a nil DAO must not stamp loadedAt")
}

func resetTenantImageSupport(t *testing.T) {
	t.Helper()
	tenantImageSupportMu.Lock()
	tenantImageSupport = map[string]*imageSupportCache{}
	tenantImageSupportMu.Unlock()
}

// seedImageSupport fills the built-in cache and one tenant's cache without a DB.
func seedImageSupport(t *testing.T, builtIn map[string]bool, tenants map[string]map[string]bool) {
	t.Helper()
	resetImageSupportCache(t)
	resetTenantImageSupport(t)
	t.Cleanup(func() {
		resetImageSupportCache(t)
		resetTenantImageSupport(t)
	})
	imageSupport.markRefreshed(builtIn)
	for tenant, catalog := range tenants {
		imageSupportForTenant(tenant).markRefreshed(catalog)
	}
}

// The bug this exists for: an account records that its own custom model takes
// images, and the built-in catalog knows nothing about that model.
func TestIsVisionCapable_TenantVerdictOnModelWithNoBuiltInRow(t *testing.T) {
	seedImageSupport(t,
		map[string]bool{"googleai:gemini-2.5-flash": true},
		map[string]map[string]bool{"tenant-1": {"custom:Qwen/Qwen3.6-35B-A3B-FP8": true}})

	assert.True(t, isVisionCapable("tenant-1", "custom", "Qwen/Qwen3.6-35B-A3B-FP8"))
}

func TestIsVisionCapable_TenantVerdictWinsOverBuiltIn(t *testing.T) {
	seedImageSupport(t,
		map[string]bool{"custom:m-up": false, "custom:m-down": true},
		map[string]map[string]bool{"tenant-1": {"custom:m-up": true, "custom:m-down": false}})

	assert.True(t, isVisionCapable("tenant-1", "custom", "m-up"))
	assert.False(t, isVisionCapable("tenant-1", "custom", "m-down"))
}

// No row of the tenant's own means the built-in answer stands — a tenant that
// merely priced a model must not lose a built-in verdict.
func TestIsVisionCapable_FallsBackToBuiltInWhenTenantHasNoVerdict(t *testing.T) {
	seedImageSupport(t,
		map[string]bool{"googleai:gemini-2.5-flash": true},
		map[string]map[string]bool{"tenant-1": {}})

	assert.True(t, isVisionCapable("tenant-1", "googleai", "gemini-2.5-flash"))
	assert.True(t, isVisionCapable("", "googleai", "gemini-2.5-flash"), "no tenant resolved: built-ins only")
	assert.False(t, isVisionCapable("tenant-1", "custom", "unrecorded"), "unknown stays default-deny")
}

func TestIsVisionCapable_OneTenantsVerdictIsNotVisibleToAnother(t *testing.T) {
	seedImageSupport(t,
		map[string]bool{},
		map[string]map[string]bool{
			"tenant-1": {"custom:private-model": true},
			"tenant-2": {},
		})

	assert.True(t, isVisionCapable("tenant-1", "custom", "private-model"))
	assert.False(t, isVisionCapable("tenant-2", "custom", "private-model"))
	assert.False(t, isVisionCapable("", "custom", "private-model"))
}

// The deny-list is an emergency override and must still beat a tenant's true.
func TestIsVisionCapable_DenyListStillWinsOverTenantVerdict(t *testing.T) {
	seedImageSupport(t,
		map[string]bool{},
		map[string]map[string]bool{"tenant-1": {"openai:gpt-3.5-turbo": true}})

	assert.False(t, isVisionCapable("tenant-1", "openai", "gpt-3.5-turbo"))
}

func TestImageSupportForTenant_EmptyTenantIsTheBuiltInCache(t *testing.T) {
	resetTenantImageSupport(t)
	t.Cleanup(func() { resetTenantImageSupport(t) })

	assert.Same(t, imageSupport, imageSupportForTenant(""))
	tenantImageSupportMu.Lock()
	defer tenantImageSupportMu.Unlock()
	assert.Empty(t, tenantImageSupport, "the built-in catalog must not get a second, per-tenant copy")
}

// The per-tenant map is bounded by active tenants: creating a new entry drops
// caches idle for over four TTLs, and ones that never loaded for that long
// after creation, but never a fresh one or one still loading.
func TestImageSupportForTenant_DropsIdleTenantsOnNewEntry(t *testing.T) {
	resetTenantImageSupport(t)
	t.Cleanup(func() { resetTenantImageSupport(t) })

	idle := imageSupportForTenant("idle")
	idle.markRefreshed(map[string]bool{})
	idle.mu.Lock()
	idle.loadedAt = time.Now().Add(-5 * imageSupportCacheTTL)
	idle.mu.Unlock()
	fresh := imageSupportForTenant("fresh")
	fresh.markRefreshed(map[string]bool{})
	loading := imageSupportForTenant("loading") // never loaded yet, but just created
	stuck := imageSupportForTenant("stuck")     // never loaded, and created long ago
	stuck.createdAt = time.Now().Add(-5 * imageSupportCacheTTL)

	imageSupportForTenant("new")

	tenantImageSupportMu.Lock()
	defer tenantImageSupportMu.Unlock()
	assert.NotContains(t, tenantImageSupport, "idle")
	assert.NotContains(t, tenantImageSupport, "stuck", "a cache that never loaded must not live forever")
	assert.Same(t, fresh, tenantImageSupport["fresh"])
	assert.Same(t, loading, tenantImageSupport["loading"])
	assert.Contains(t, tenantImageSupport, "new")
}

// catalogStore stands in for the usage store: each GetImageSupportCatalog
// call returns the next queued result, the catalog keyed "provider:model" as
// the DAO builds it.
type catalogStore struct {
	UsageStore
	results []catalogResult
}

type catalogResult struct {
	catalog map[string]bool
	err     error
}

func (s *catalogStore) queue(catalog map[string]bool, err error) {
	s.results = append(s.results, catalogResult{catalog, err})
}

func (s *catalogStore) GetImageSupportCatalog(string) (map[string]bool, error) {
	if len(s.results) == 0 {
		panic("catalogStore: unexpected GetImageSupportCatalog call")
	}
	r := s.results[0]
	s.results = s.results[1:]
	return r.catalog, r.err
}

func installCatalogStore(t *testing.T) *catalogStore {
	t.Helper()
	s := &catalogStore{}
	installStore(t, s)
	return s
}

// installStore makes store the shared usage store the image-support cache
// sees, restoring the previous one after the test.
func installStore(t *testing.T, store UsageStore) {
	t.Helper()
	prev := SetUsageStore(store)
	t.Cleanup(func() { SetUsageStore(prev) })
}
