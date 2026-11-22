package aws

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func subsFor(region string) map[string][]interface{} {
	return map[string][]interface{}{
		"arn:aws:sns:" + region + ":123456789012:orders": {map[string]interface{}{"Protocol": "sqs"}},
	}
}

func TestFetchSNSSubscriptionsByRegionMergesAndReportsFailures(t *testing.T) {
	throttled := errors.New("Throttling: Rate exceeded")
	fetch := func(region string) (map[string][]interface{}, error) {
		if region == "us-east-2" {
			return nil, throttled
		}
		return subsFor(region), nil
	}

	merged, missing := fetchSNSSubscriptionsByRegion([]string{"us-east-1", "us-east-2", "eu-west-1"}, fetch, 4, time.Minute)

	assert.Len(t, merged, 2)
	assert.Contains(t, merged, "arn:aws:sns:us-east-1:123456789012:orders")
	assert.Contains(t, merged, "arn:aws:sns:eu-west-1:123456789012:orders")
	assert.Equal(t, map[string]string{"us-east-2": throttled.Error()}, missing)
}

// One slow region no longer holds up the others: they are looked up side by side.
func TestFetchSNSSubscriptionsByRegionSlowRegionDoesNotHoldUpOthers(t *testing.T) {
	var running, maxRunning atomic.Int32
	var mu sync.Mutex
	finished := map[string]time.Duration{}
	start := time.Now()
	fetch := func(region string) (map[string][]interface{}, error) {
		now := running.Add(1)
		for {
			old := maxRunning.Load()
			if now <= old || maxRunning.CompareAndSwap(old, now) {
				break
			}
		}
		defer running.Add(-1)
		if region == "slow" {
			time.Sleep(300 * time.Millisecond)
		} else {
			time.Sleep(10 * time.Millisecond)
		}
		mu.Lock()
		finished[region] = time.Since(start)
		mu.Unlock()
		return subsFor(region), nil
	}

	_, missing := fetchSNSSubscriptionsByRegion([]string{"slow", "a", "b", "c", "d", "e"}, fetch, 4, time.Minute)

	assert.Empty(t, missing)
	assert.LessOrEqual(t, maxRunning.Load(), int32(4), "never more than the concurrency limit at once")
	for _, region := range []string{"a", "b", "c", "d", "e"} {
		assert.Less(t, finished[region], 250*time.Millisecond, "%s waited for the slow region", region)
	}
}

// After the time limit no new region is started, and the regions left out are
// reported rather than silently missing.
func TestFetchSNSSubscriptionsByRegionStopsStartingAfterTheTimeLimit(t *testing.T) {
	var started atomic.Int32
	fetch := func(region string) (map[string][]interface{}, error) {
		started.Add(1)
		time.Sleep(100 * time.Millisecond)
		return subsFor(region), nil
	}

	merged, missing := fetchSNSSubscriptionsByRegion([]string{"r1", "r2", "r3", "r4"}, fetch, 1, 50*time.Millisecond)

	assert.Equal(t, int32(1), started.Load(), "only the region started before the limit runs")
	assert.Len(t, merged, 1)
	assert.Equal(t, map[string]string{
		"r2": "not started: SNS step time limit reached",
		"r3": "not started: SNS step time limit reached",
		"r4": "not started: SNS step time limit reached",
	}, missing)
}

func TestFetchSNSSubscriptionsByRegionNoRegions(t *testing.T) {
	merged, missing := fetchSNSSubscriptionsByRegion(nil, func(string) (map[string][]interface{}, error) {
		t.Fatal("no lookup expected")
		return nil, nil
	}, 4, time.Minute)
	assert.Empty(t, merged)
	assert.Empty(t, missing)
}
