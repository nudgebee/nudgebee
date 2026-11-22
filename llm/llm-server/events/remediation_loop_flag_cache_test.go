package events

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubLoopFlagLoad(t *testing.T, load func(string) (bool, error)) {
	t.Helper()
	orig := loadRemediationLoopFlag
	loadRemediationLoopFlag = load
	ResetRemediationLoopCache()
	t.Cleanup(func() {
		loadRemediationLoopFlag = orig
		ResetRemediationLoopCache()
	})
}

// One request asks for the flag several times: the database is read once per account.
func TestRemediationLoopReadIsKeptPerAccount(t *testing.T) {
	reads := map[string]int{}
	stubLoopFlagLoad(t, func(accountId string) (bool, error) {
		reads[accountId]++
		return accountId == "on", nil
	})
	for range 5 {
		assert.True(t, RemediationLoopOn("on"))
		assert.False(t, RemediationLoopOn("off"))
	}
	assert.Equal(t, map[string]int{"on": 1, "off": 1}, reads)
}

// A failed read is not kept: it still fails closed, and the next call reads again.
func TestRemediationLoopReadErrorIsNotKept(t *testing.T) {
	calls := 0
	stubLoopFlagLoad(t, func(string) (bool, error) {
		calls++
		if calls == 1 {
			return false, errors.New("db down")
		}
		return true, nil
	})
	_, err := RemediationLoopRead("acc")
	require.Error(t, err)
	assert.True(t, RemediationLoopOn("acc"), "the read after a failure goes to the database")
	assert.Equal(t, 2, calls)
}

// A kept read expires, so a flag flip takes effect within the TTL.
func TestLoopFlagCacheExpires(t *testing.T) {
	c := &loopFlagCache{entries: map[string]loopFlagEntry{}}
	now := time.Now()
	c.put("acc", true, now)
	on, ok := c.get("acc", now.Add(remediationLoopCacheTTL-time.Second))
	assert.True(t, ok)
	assert.True(t, on)
	_, ok = c.get("acc", now.Add(remediationLoopCacheTTL))
	assert.False(t, ok)
}
