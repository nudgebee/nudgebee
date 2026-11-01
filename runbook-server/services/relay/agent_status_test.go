package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockQueryGetter struct {
	count int
	err   error
}

func (m *mockQueryGetter) Get(dest any, query string, args ...any) error {
	if m.err != nil {
		return m.err
	}
	if p, ok := dest.(*int); ok {
		*p = m.count
	}
	return nil
}

func TestAgentConnectedInDb(t *testing.T) {
	t.Run("connected agent exists", func(t *testing.T) {
		mockDB := &mockQueryGetter{count: 1}
		connected := agentConnectedInDb(mockDB, "acc-123")
		assert.True(t, connected)
	})

	t.Run("no connected agent exists", func(t *testing.T) {
		mockDB := &mockQueryGetter{count: 0}
		connected := agentConnectedInDb(mockDB, "acc-123")
		assert.False(t, connected)
	})

	t.Run("db error fails open", func(t *testing.T) {
		mockDB := &mockQueryGetter{err: errors.New("db connection lost")}
		connected := agentConnectedInDb(mockDB, "acc-123")
		assert.True(t, connected)
	})
}

func TestAgentConnected(t *testing.T) {
	t.Run("empty account id returns false", func(t *testing.T) {
		assert.False(t, AgentConnected(""))
	})

	t.Run("cached connected entry", func(t *testing.T) {
		accID := "acc-cached-connected"
		agentStatusCache.Lock()
		agentStatusCache.entries[accID] = agentStatusCacheEntry{
			connected: true,
			expiresAt: time.Now().Add(10 * time.Minute),
		}
		agentStatusCache.Unlock()

		assert.True(t, AgentConnected(accID))
	})

	t.Run("cached not connected entry", func(t *testing.T) {
		accID := "acc-cached-not-connected"
		agentStatusCache.Lock()
		agentStatusCache.entries[accID] = agentStatusCacheEntry{
			connected: false,
			expiresAt: time.Now().Add(10 * time.Minute),
		}
		agentStatusCache.Unlock()

		assert.False(t, AgentConnected(accID))
	})
}

func TestShouldCheckAgent(t *testing.T) {
	tests := []struct {
		name string
		body ActionExecuteBody
		want bool
	}{
		{
			name: "empty agent type checks agent",
			body: ActionExecuteBody{AgentType: ""},
			want: true,
		},
		{
			name: "k8s agent type checks agent",
			body: ActionExecuteBody{AgentType: "k8s"},
			want: true,
		},
		{
			name: "K8S agent type uppercase checks agent",
			body: ActionExecuteBody{AgentType: "K8S"},
			want: true,
		},
		{
			name: "proxy agent type skips agent check",
			body: ActionExecuteBody{AgentType: "proxy"},
			want: false,
		},
		{
			name: "direct mcp_request skips agent check",
			body: ActionExecuteBody{
				ActionName: "mcp_request",
				ActionParams: map[string]any{
					"connection_mode": "direct",
				},
			},
			want: false,
		},
		{
			name: "non-direct mcp_request with empty agent type checks agent",
			body: ActionExecuteBody{
				ActionName: "mcp_request",
				ActionParams: map[string]any{
					"connection_mode": "in_cluster",
				},
			},
			want: true,
		},
		{
			name: "regular action with empty agent type checks agent",
			body: ActionExecuteBody{
				ActionName: "kubectl_get_pods",
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldCheckAgent(tc.body))
		})
	}
}

func TestAgentNotConnectedCacheTTLUnderRelayNegativeTTL(t *testing.T) {
	const relayNotConnectedCacheTTL = 2 * time.Minute
	assert.Less(t, agentNotConnectedCacheTTL, relayNotConnectedCacheTTL,
		"agentNotConnectedCacheTTL must stay under the relay's negative cache TTL")
}
