package providers

import (
	"sync"
	"time"
)

const metricsCacheTTL = 5 * time.Minute

type metricsCacheEntry struct {
	response  ListMetricsResponse
	expiresAt time.Time
}

var (
	metricsCache   = make(map[string]metricsCacheEntry)
	metricsCacheMu sync.RWMutex
)

// GetCachedMetrics returns cached metrics for the given key, or nil if not cached/expired.
func GetCachedMetrics(key string) *ListMetricsResponse {
	metricsCacheMu.RLock()
	defer metricsCacheMu.RUnlock()
	entry, ok := metricsCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return &entry.response
}

// SetCachedMetrics caches metrics for the given key.
func SetCachedMetrics(key string, response ListMetricsResponse) {
	metricsCacheMu.Lock()
	defer metricsCacheMu.Unlock()
	metricsCache[key] = metricsCacheEntry{
		response:  response,
		expiresAt: time.Now().Add(metricsCacheTTL),
	}
}
