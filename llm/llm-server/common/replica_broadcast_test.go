package common

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"nudgebee/llm/config"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type recordingHandler struct {
	keys [][]string
	all  int
}

func (r *recordingHandler) register(t *testing.T, changeType string) {
	t.Cleanup(OnReplicaBroadcast(changeType, ReplicaHandler{
		OnKeys: func(keys []string) { r.keys = append(r.keys, keys) },
		OnAll:  func() { r.all++ },
	}))
}

func TestReplicaBroadcast_RoutesKeysToOnKeysAndNoKeysToOnAll(t *testing.T) {
	var mine, other recordingHandler
	mine.register(t, "test_local_changed")
	other.register(t, "test_other_changed")

	ReplicaBroadcast(context.Background(), "test_local_changed", "t1", "t2")
	ReplicaBroadcastAll(context.Background(), "test_local_changed")

	require.Equal(t, [][]string{{"t1", "t2"}}, mine.keys)
	require.Equal(t, 1, mine.all)
	require.Empty(t, other.keys, "a handler for another type ran")
	require.Zero(t, other.all, "a handler for another type ran")
}

func TestOnReplicaBroadcast_RequiresBothCallbacks(t *testing.T) {
	require.Panics(t, func() { OnReplicaBroadcast("test_missing_changed", ReplicaHandler{OnKeys: func([]string) {}}) })
	require.Panics(t, func() { OnReplicaBroadcast("test_missing_changed", ReplicaHandler{OnAll: func() {}}) })
}

func TestOnReplicaBroadcast_Unregister(t *testing.T) {
	calls := 0
	unregister := OnReplicaBroadcast("test_unregister_changed", ReplicaHandler{
		OnKeys: func([]string) { calls++ },
		OnAll:  func() { calls++ },
	})
	unregister()

	ReplicaBroadcast(context.Background(), "test_unregister_changed", "k")
	require.Zero(t, calls)
}

func TestReplicaBroadcast_PanickingHandlerDoesNotStopOthers(t *testing.T) {
	t.Cleanup(OnReplicaBroadcast("test_panic_changed", ReplicaHandler{
		OnKeys: func([]string) { panic("boom") },
		OnAll:  func() { panic("boom") },
	}))
	var after recordingHandler
	after.register(t, "test_panic_changed")

	require.NotPanics(t, func() { ReplicaBroadcast(context.Background(), "test_panic_changed", "k") })
	require.Equal(t, [][]string{{"k"}}, after.keys, "a handler after the panicking one must still run")
}

// The wire format is shared with every other service on nb_replica_broadcast
// (#40807). docs/fixtures/replica_message.json is the one copy of it.
func TestReplicaMessage_MatchesSharedFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/fixtures/replica_message.json")
	require.NoError(t, err)

	var decoded ReplicaMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, ReplicaMessage{
		Type:   "example_changed",
		Keys:   []string{"00000000-0000-0000-0000-000000000001"},
		Sender: "services-server/pod-a",
	}, decoded)

	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	var want, got map[string]any
	require.NoError(t, json.Unmarshal(raw, &want))
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.Equal(t, want, got)
}

// Runs against a real Redis: set NB_TEST_REDIS_ADDR=host:port. Proves a message
// from another pod reaches OnKeys, this pod's own message is skipped, and a
// dropped connection runs OnAll, so a change missed while disconnected cannot
// leave a cache stale.
func TestReplicaBroadcastSubscriber_AgainstRedis(t *testing.T) {
	addr := os.Getenv("NB_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("NB_TEST_REDIS_ADDR not set")
	}
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	syncCacheManagers.Lock()
	oldProvider, oldHost, oldPort := config.Config.CacheProvider, config.Config.CacheRedisServerHost, config.Config.CacheRedisServerPort
	oldManager, oldClient := cacheManager, cacheClient
	config.Config.CacheProvider, config.Config.CacheRedisServerHost, config.Config.CacheRedisServerPort = "redis", host, port
	cacheManager, cacheClient = nil, nil
	syncCacheManagers.Unlock()
	t.Cleanup(func() {
		syncCacheManagers.Lock()
		testClient, _ := cacheClient.(*redis.Client)
		config.Config.CacheProvider, config.Config.CacheRedisServerHost, config.Config.CacheRedisServerPort = oldProvider, oldHost, oldPort
		cacheManager, cacheClient = oldManager, oldClient
		syncCacheManagers.Unlock()
		if testClient != nil {
			_ = testClient.Close()
		}
	})

	keys := make(chan []string, 8)
	all := make(chan struct{}, 8)
	t.Cleanup(OnReplicaBroadcast("test_redis_changed", ReplicaHandler{
		OnKeys: func(k []string) { keys <- k },
		OnAll:  func() { all <- struct{}{} },
	}))
	waitAll := func(what string) {
		select {
		case <-all:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	t.Cleanup(StartReplicaBroadcastSubscriber())
	waitAll("initial resync")

	// A separate client stands in for the other pods.
	other := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = other.Close() }()
	ctx := context.Background()
	require.NoError(t, other.Publish(ctx, replicaBroadcastChannel,
		`{"type":"test_redis_changed","keys":["self"],"sender":"`+replicaSender()+`"}`).Err())
	require.NoError(t, other.Publish(ctx, replicaBroadcastChannel,
		`{"type":"test_redis_changed","keys":["t9"],"sender":"services-server/pod-b"}`).Err())
	select {
	case got := <-keys:
		require.Equal(t, []string{"t9"}, got, "this pod's own message must be skipped")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the other pod's message")
	}

	killed, err := other.Do(ctx, "CLIENT", "KILL", "TYPE", "pubsub").Int()
	require.NoError(t, err)
	require.GreaterOrEqual(t, killed, 1, "no pubsub connection was killed")
	waitAll("resync after connection drop")
}

func TestLogReplicaEvent_CapsInfoLinesPerTypeAndDirection(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for i := 0; i < 5; i++ {
		logReplicaEvent(context.Background(), "published", "test_log_changed", "keys", 1)
	}
	logReplicaEvent(context.Background(), "received", "test_log_changed", "keys", 1)
	logReplicaEvent(context.Background(), "published", "test_log_other_changed", "keys", 1)

	out := buf.String()
	require.Equal(t, 1, strings.Count(out, `msg="replica broadcast: published" type=test_log_changed`), "repeats within the window must not be logged")
	require.Equal(t, 1, strings.Count(out, `msg="replica broadcast: received" type=test_log_changed`), "each direction has its own cap")
	require.Equal(t, 1, strings.Count(out, "type=test_log_other_changed"), "each type has its own cap")

	replicaLogState.Lock()
	suppressed := replicaLogState.suppressed["published|test_log_changed"]
	replicaLogState.Unlock()
	require.Equal(t, 4, suppressed, "the next line reports how many were left out")
}
