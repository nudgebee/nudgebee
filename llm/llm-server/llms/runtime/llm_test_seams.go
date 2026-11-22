package runtime

import "time"

// SeedIntegrationConfigForTest puts cfg in the integration-config cache for
// accountId as if it had just been loaded from the database, and returns a
// func that removes it again. It is for tests outside this package, which
// cannot reach the cache; production code must not call it.
func SeedIntegrationConfigForTest(accountId string, cfg map[string]string) (remove func()) {
	llmIntegrationConfigCacheMutex.Lock()
	llmIntegrationConfigCache[accountId] = struct {
		config map[string]string
		ts     time.Time
	}{config: cfg, ts: time.Now()}
	llmIntegrationConfigCacheMutex.Unlock()
	return func() {
		llmIntegrationConfigCacheMutex.Lock()
		delete(llmIntegrationConfigCache, accountId)
		llmIntegrationConfigCacheMutex.Unlock()
	}
}
