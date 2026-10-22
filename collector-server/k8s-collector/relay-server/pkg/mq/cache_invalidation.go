package mq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"nudgebee/relay-server/pkg/config"
)

// LLMCacheInvalidator tells every replica of every service that caches
// integration state (llm-server, services-server) to drop what it cached for
// the given accounts.
type LLMCacheInvalidator interface {
	InvalidateAccounts(accountIDs ...string)
}

// replicaBroadcastChannel is the Redis pub/sub channel every pod of
// services-server, llm-server and rag-server listens on (#40807). The relay
// only publishes to it. Design: the 2026-10 replica broadcast entries in
// docs/architecture-decisions.md.
const replicaBroadcastChannel = "nb_replica_broadcast"

// changeIntegration has the same value as services-server's and llm-server's
// ChangeIntegration; its keys are account ids. Each receiving pod only drops
// caches. No once-per-service work is requested: the knowledge-base sync only
// takes confluence and servicenow integrations, which agents do not report
// (relay registers databases, monitoring, ssh, mcp). Should an agent report
// one, its sync starts at llm-server's next periodic run.
const changeIntegration = "integration_changed"

// replicaMessage is the wire format; the one sample of it is
// docs/fixtures/replica_message.json.
type replicaMessage struct {
	Type   string   `json:"type"`
	Keys   []string `json:"keys,omitempty"`
	Sender string   `json:"sender"`
}

// replicaPublishTimeout bounds one best-effort publish. The publish runs off
// the caller's path, so this only limits how long a Redis outage can keep a
// goroutine around.
const replicaPublishTimeout = 2 * time.Second

// redisLLMCacheInvalidator publishes integration_changed on the replica
// broadcast.
//
// Without it, integrations the relay registers from a proxy agent's inventory
// (and label changes from its metadata) only reach Nubi after each cache's
// expiry, because the relay was the one writer to those rows that never said
// so.
type redisLLMCacheInvalidator struct {
	client redis.UniversalClient
	sender string
	logger *slog.Logger
}

// NewLLMCacheInvalidator publishes on the replica broadcast when the cache
// provider is redis, like every other service. Otherwise it does nothing and
// says so at startup: other services then see the relay's integration changes
// only at their caches' expiry.
func NewLLMCacheInvalidator(cfg *config.Config, logger *slog.Logger) LLMCacheInvalidator {
	if logger == nil {
		logger = slog.Default()
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Cache.Provider), "redis") {
		logger.Warn("replica broadcast: OFF (CACHE_PROVIDER is not redis); integrations the relay registers reach other services only at their cache expiry")
		return noopLLMCacheInvalidator{}
	}
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Cache.Redis.Host, cfg.Cache.Redis.Port),
		Username: cfg.Cache.Redis.Username,
		Password: cfg.Cache.Redis.Password,
	})
	return &redisLLMCacheInvalidator{client: client, sender: replicaSender(), logger: logger}
}

// replicaSender names this process: "relay-server/<pod>#<random>". The random
// part keeps it unique even when pods share a hostname.
func replicaSender() string {
	host, _ := os.Hostname()
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	return "relay-server/" + host + "#" + hex.EncodeToString(suffix)
}

// Close releases the Redis client. The router keeps one invalidator for the
// life of the process, so only tests need it.
func (i *redisLLMCacheInvalidator) Close() error {
	return i.client.Close()
}

// InvalidateAccounts publishes in the background and never blocks or fails the
// caller: a lost message only means waiting for the caches' expiry.
func (i *redisLLMCacheInvalidator) InvalidateAccounts(accountIDs ...string) {
	ids := dedupeNonEmpty(accountIDs)
	if len(ids) == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				i.logger.Error("replica broadcast: publish panicked — recovered", "type", changeIntegration, "panic", r)
			}
		}()
		payload, err := json.Marshal(replicaMessage{Type: changeIntegration, Keys: ids, Sender: i.sender})
		if err != nil {
			i.logger.Warn("replica broadcast: failed to encode message", "type", changeIntegration, "err", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), replicaPublishTimeout)
		defer cancel()
		if err := i.client.Publish(ctx, replicaBroadcastChannel, payload).Err(); err != nil {
			i.logger.Warn("replica broadcast: failed to publish (best-effort)", "type", changeIntegration, "account_ids", ids, "err", err)
			return
		}
		i.logger.Info("replica broadcast: published", "type", changeIntegration, "account_ids", ids)
	}()
}

func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

type noopLLMCacheInvalidator struct{}

func (noopLLMCacheInvalidator) InvalidateAccounts(...string) {}
