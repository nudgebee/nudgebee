package common

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/config"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// pending_tokens stores Temporal activity TaskTokens that are waiting on
// an in-progress event analysis. When the analysis reaches a terminal
// state (COMPLETED / FAILED), the worker that ran the pipeline drains
// this list and publishes a completion envelope for each pending token.
//
// Why this exists: processTroubleshootingEventFromMq is racey across
// llm-server replicas. Two workers can end up handling messages for the
// same event_id — one starts the analysis, the other arrives later and
// sees IN_PROGRESS. Without a registry the second worker's task_token
// would be silently dropped (the workflow activity in runbook-server
// would suspend until Temporal's StartToCloseTimeout). With this
// registry the second worker registers its token, and whoever runs the
// pipeline drains the list at the end and publishes for everyone.
//
// Storage: Redis lists when CacheProvider=redis (production / dev),
// in-process sync.Map otherwise (local single-pod dev).

const (
	pendingTokensKeyPrefix = "pending_inv_tokens:"

	// pendingTokensTTL bounds how long an unconsumed token sits in the
	// list. Activity StartToCloseTimeout is the workflow-side bound;
	// this is a defence-in-depth cleanup so a worker that registers a
	// token and then dies doesn't leak forever.
	pendingTokensTTL = 1 * time.Hour
)

// inMemoryPendingTokens backs the registry when the cache provider is
// not Redis. Single-pod only — across multiple llm-server replicas the
// completion fan-out is best-effort and tokens registered on one pod
// won't reach a worker on another. Acceptable for local dev; not for
// any deployed environment.
var (
	inMemoryPendingTokens   = make(map[string][]string)
	inMemoryPendingTokensMu sync.Mutex
)

// RegisterPendingToken records token as awaiting completion for eventID.
// Safe to call concurrently for the same event from different workers.
func RegisterPendingToken(ctx context.Context, eventID, token string) error {
	if eventID == "" || token == "" {
		return fmt.Errorf("pending_tokens: eventID and token must be non-empty")
	}
	if redisClient, ok := redisClientForPendingTokens(); ok {
		key := pendingTokensKeyPrefix + eventID
		pipe := redisClient.TxPipeline()
		pipe.RPush(ctx, key, token)
		pipe.Expire(ctx, key, pendingTokensTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("pending_tokens: register failed for event %s: %w", eventID, err)
		}
		return nil
	}
	inMemoryPendingTokensMu.Lock()
	defer inMemoryPendingTokensMu.Unlock()
	inMemoryPendingTokens[eventID] = append(inMemoryPendingTokens[eventID], token)
	return nil
}

// DrainPendingTokens atomically reads and clears all tokens registered
// for eventID. Returns nil slice when none are present.
func DrainPendingTokens(ctx context.Context, eventID string) ([]string, error) {
	if eventID == "" {
		return nil, fmt.Errorf("pending_tokens: eventID must be non-empty")
	}
	if redisClient, ok := redisClientForPendingTokens(); ok {
		key := pendingTokensKeyPrefix + eventID
		pipe := redisClient.TxPipeline()
		lrangeCmd := pipe.LRange(ctx, key, 0, -1)
		pipe.Del(ctx, key)
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, fmt.Errorf("pending_tokens: drain failed for event %s: %w", eventID, err)
		}
		return lrangeCmd.Val(), nil
	}
	inMemoryPendingTokensMu.Lock()
	defer inMemoryPendingTokensMu.Unlock()
	tokens := inMemoryPendingTokens[eventID]
	delete(inMemoryPendingTokens, eventID)
	return tokens, nil
}

// RemovePendingToken removes a single token from eventID's pending list.
// Used by the race-guard path: a worker that registered a token and
// then discovered the analysis had already completed pops its own token
// (so the eventual drain by another worker doesn't double-publish) and
// publishes inline. Returns whether the token was found and removed.
func RemovePendingToken(ctx context.Context, eventID, token string) (bool, error) {
	if eventID == "" || token == "" {
		return false, fmt.Errorf("pending_tokens: eventID and token must be non-empty")
	}
	if redisClient, ok := redisClientForPendingTokens(); ok {
		key := pendingTokensKeyPrefix + eventID
		removed, err := redisClient.LRem(ctx, key, 1, token).Result()
		if err != nil {
			return false, fmt.Errorf("pending_tokens: remove failed for event %s: %w", eventID, err)
		}
		return removed > 0, nil
	}
	inMemoryPendingTokensMu.Lock()
	defer inMemoryPendingTokensMu.Unlock()
	list := inMemoryPendingTokens[eventID]
	for i, t := range list {
		if t == token {
			inMemoryPendingTokens[eventID] = append(list[:i], list[i+1:]...)
			if len(inMemoryPendingTokens[eventID]) == 0 {
				delete(inMemoryPendingTokens, eventID)
			}
			return true, nil
		}
	}
	return false, nil
}

// redisClientForPendingTokens returns the package-level Redis client when
// cache provider is "redis" and the client has been initialized. Returns
// false when callers should fall back to the in-memory map.
func redisClientForPendingTokens() (*redis.Client, bool) {
	if config.Config.CacheProvider != "redis" {
		return nil, false
	}
	if cacheClient == nil {
		return nil, false
	}
	rc, ok := cacheClient.(*redis.Client)
	if !ok {
		slog.Warn("pending_tokens: cacheClient is not *redis.Client even though CacheProvider=redis; falling back to in-memory")
		return nil, false
	}
	return rc, true
}
