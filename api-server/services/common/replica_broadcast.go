package common

import (
	"context"
	"log/slog"
	"nudgebee/services/config"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// replicaBroadcastChannel carries ReplicaMessage to every pod of every service
// that listens (Redis pub/sub). Design and rules: issue #40807 and the
// 2026-10 entry in docs/architecture-decisions.md.
//
// A handler runs once per pod, so it may only drop that pod's own memory.
// Anything that must happen once per service is a RabbitMQ task instead.
const replicaBroadcastChannel = "nb_replica_broadcast"

// Change types this service sends or handles, and what Keys holds for each.
// The list across services is kept in issue #40807.
const (
	ChangeFeatureFlag = "feature_flag_changed" // keys: tenant ids
	ChangeTriageRules = "triage_rules_changed" // keys: tenant ids
	ChangeIntegration = "integration_changed"  // keys: account ids
)

// ReplicaMessage is the wire format: what changed, never the new value.
// Wire sample: docs/fixtures/replica_message.json.
type ReplicaMessage struct {
	Type   string   `json:"type"`
	Keys   []string `json:"keys,omitempty"` // empty: everything of this type
	Sender string   `json:"sender"`         // "<service>/<pod>#<process id>"
}

// ReplicaHandler drops one type's cached entries on this pod.
type ReplicaHandler struct {
	// OnKeys drops the entries for these keys.
	OnKeys func(keys []string)
	// OnAll drops everything of this type. It also runs after every
	// (re)subscribe, because pub/sub does not redeliver missed messages.
	OnAll func()
}

var (
	replicaHandlersMu sync.Mutex
	replicaHandlers   = map[string][]*ReplicaHandler{}
)

// OnReplicaBroadcast registers h for one change type; call it from init().
// Both callbacks are required.
func OnReplicaBroadcast(changeType string, h ReplicaHandler) (unregister func()) {
	if h.OnKeys == nil || h.OnAll == nil {
		panic("replica broadcast: handler for " + changeType + " needs both OnKeys and OnAll")
	}
	handler := &h
	replicaHandlersMu.Lock()
	replicaHandlers[changeType] = append(replicaHandlers[changeType], handler)
	replicaHandlersMu.Unlock()
	return func() {
		replicaHandlersMu.Lock()
		defer replicaHandlersMu.Unlock()
		kept := replicaHandlers[changeType][:0]
		for _, registered := range replicaHandlers[changeType] {
			if registered != handler {
				kept = append(kept, registered)
			}
		}
		replicaHandlers[changeType] = kept
	}
}

// ReplicaBroadcast drops these keys of changeType on this pod at once, then
// tells every other pod (one publish, inline, bounded by cachePublishTimeout).
// Call it after the write has committed. Best-effort: a failed publish is
// logged and never fails the write. Send all keys of one change in one call.
// With no keys it is ReplicaBroadcastAll.
func ReplicaBroadcast(ctx context.Context, changeType string, keys ...string) {
	publishReplicaMessage(ctx, ReplicaMessage{Type: changeType, Keys: keys})
}

// ReplicaBroadcastAll drops everything of changeType on every pod.
func ReplicaBroadcastAll(ctx context.Context, changeType string) {
	publishReplicaMessage(ctx, ReplicaMessage{Type: changeType})
}

// replicaSender names this process. The random part keeps it unique even when
// replicas share a ServerName, so skipping our own messages never skips a peer's.
var replicaSender = sync.OnceValue(func() string {
	return "services-server/" + config.Config.ServerName + "#" + uuid.NewString()[:8]
})

func publishReplicaMessage(ctx context.Context, msg ReplicaMessage) {
	if ctx == nil {
		ctx = context.Background()
	}
	msg.Sender = replicaSender()
	dispatchReplicaMessage(msg)
	if config.Config.CacheProvider != "redis" {
		// One process: there is no other pod to tell.
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		slog.WarnContext(ctx, "replica broadcast: failed to encode message", "type", msg.Type, "error", err)
		return
	}
	// Inline, bounded by cachePublishTimeout: no goroutine per publish, so a
	// burst of broadcasts queues on the caller instead of piling up goroutines
	// on the Redis pool, and one caller's messages keep their order.
	if err := CachePublish(replicaBroadcastChannel, string(payload)); err != nil {
		slog.WarnContext(ctx, "replica broadcast: failed to publish", "type", msg.Type, "error", err)
		metricsReplicaBroadcast(msg.Type, "publish_failed")
		return
	}
	metricsReplicaBroadcast(msg.Type, "published")
	logReplicaEvent(ctx, "published", msg.Type, "keys", len(msg.Keys))
}

func replicaHandlersFor(changeType string) []*ReplicaHandler {
	replicaHandlersMu.Lock()
	defer replicaHandlersMu.Unlock()
	return append([]*ReplicaHandler(nil), replicaHandlers[changeType]...)
}

// dispatchReplicaMessage runs this pod's handlers and reports whether any
// exist: keys go to OnKeys, no keys to OnAll.
func dispatchReplicaMessage(msg ReplicaMessage) bool {
	handlers := replicaHandlersFor(msg.Type)
	for _, h := range handlers {
		if len(msg.Keys) == 0 {
			runReplicaHandler(msg.Type, h.OnAll)
		} else {
			onKeys, keys := h.OnKeys, msg.Keys
			runReplicaHandler(msg.Type, func() { onKeys(keys) })
		}
	}
	return len(handlers) > 0
}

// runReplicaHandler logs a handler panic instead of letting it crash the pod
// or stop the subscriber.
func runReplicaHandler(changeType string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("replica broadcast: handler panicked", "type", changeType, "error", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
}

// replicaSlowHandler is how long handling one received message may take
// before it is reported as slow.
const replicaSlowHandler = time.Second

// replicaLogEvery caps Info lines per direction and type. Chat model changes
// broadcast on every chat message, so without a cap they would flood the logs;
// the metric still counts every message.
const replicaLogEvery = time.Minute

var replicaLogState = struct {
	sync.Mutex
	last       map[string]time.Time
	suppressed map[string]int
}{last: map[string]time.Time{}, suppressed: map[string]int{}}

// logReplicaEvent logs at Info at most once per replicaLogEvery for each
// direction and type, with how many were left out since the previous line.
func logReplicaEvent(ctx context.Context, direction, changeType string, attrs ...any) {
	key := direction + "|" + changeType
	now := time.Now()
	replicaLogState.Lock()
	if now.Sub(replicaLogState.last[key]) < replicaLogEvery {
		replicaLogState.suppressed[key]++
		replicaLogState.Unlock()
		return
	}
	suppressed := replicaLogState.suppressed[key]
	replicaLogState.last[key] = now
	replicaLogState.suppressed[key] = 0
	replicaLogState.Unlock()
	attrs = append([]any{"type", changeType, "not_logged_since_last", suppressed}, attrs...)
	slog.InfoContext(ctx, "replica broadcast: "+direction, attrs...)
}

func resyncReplicaHandlers() {
	replicaHandlersMu.Lock()
	types := make([]string, 0, len(replicaHandlers))
	for changeType := range replicaHandlers {
		types = append(types, changeType)
	}
	replicaHandlersMu.Unlock()
	for _, changeType := range types {
		dispatchReplicaMessage(ReplicaMessage{Type: changeType})
	}
}

// StartReplicaBroadcastSubscriber listens for other pods' messages; main calls
// it once. Every (re)subscribe runs every OnAll, since a message published
// while this pod was disconnected is never redelivered.
func StartReplicaBroadcastSubscriber() (stop func()) {
	if config.Config.CacheProvider != "redis" {
		slog.Warn("replica broadcast: OFF (CACHE_PROVIDER is not redis); changes apply only on the pod that made them")
		return func() {}
	}
	redisClient := cacheRedisClient()
	if redisClient == nil {
		slog.Warn("replica broadcast: cannot subscribe, redis client unavailable")
		return func() {}
	}
	self := replicaSender()
	pubsub := redisClient.Subscribe(context.Background(), replicaBroadcastChannel)
	go func() {
		for msg := range pubsub.ChannelWithSubscriptions() {
			handleReplicaPubSubMessage(msg, self)
		}
	}()
	return func() { _ = pubsub.Close() }
}

// handleReplicaPubSubMessage handles one pub/sub event. A panic is logged and
// skips only this message, so the subscriber keeps running.
func handleReplicaPubSubMessage(msg any, self string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("replica broadcast: message handling panicked", "error", r, "stack", string(debug.Stack()))
		}
	}()
	switch m := msg.(type) {
	case *redis.Subscription:
		if m.Kind == "subscribe" {
			slog.Info("replica broadcast: subscribed, resyncing caches", "channel", replicaBroadcastChannel)
			resyncReplicaHandlers()
		}
	case *redis.Message:
		var rm ReplicaMessage
		if err := json.Unmarshal([]byte(m.Payload), &rm); err != nil || rm.Type == "" {
			slog.Warn("replica broadcast: ignoring malformed message", "error", err)
			return
		}
		// This pod already ran its handlers when it published.
		if rm.Sender == self {
			return
		}
		start := time.Now()
		handled := dispatchReplicaMessage(rm)
		// The subscriber runs one message at a time, so a slow handler delays
		// every type behind it (and go-redis drops messages once its buffer is
		// full). Make that visible.
		if took := time.Since(start); took > replicaSlowHandler {
			slog.Warn("replica broadcast: slow handler", "type", rm.Type, "keys", len(rm.Keys), "took", took.String())
			metricsReplicaBroadcast(rm.Type, "handler_slow")
		}
		if handled {
			metricsReplicaBroadcast(rm.Type, "received")
			logReplicaEvent(context.Background(), "received", rm.Type, "keys", len(rm.Keys), "sender", rm.Sender)
		} else {
			// Types this pod does not handle share one label, so a stray
			// type cannot grow the metric.
			metricsReplicaBroadcast("other", "ignored")
		}
		slog.Debug("replica broadcast: message received", "type", rm.Type, "keys", rm.Keys, "sender", rm.Sender)
	}
}
