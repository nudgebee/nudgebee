package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisMCPSessionStore shares the session directory between relay pods.
//
//   - mcp:sess:{account}:{session} is a hash: field = MCP server URL, value =
//     JSON mcpSessionRecord. Its TTL (idle timeout + 3 sweep intervals) is only
//     a safety net; the sweeper ends sessions.
//   - mcp:sess:lastused is a sorted set: member = JSON mcpSessionKey, score =
//     last-used unix ms. ClaimIdle reads it.
//
// Every Lua script touches a single key, so this also works on Redis Cluster.
type redisMCPSessionStore struct {
	client *redis.Client
	ttl    time.Duration
}

const redisMCPLastUsedKey = "mcp:sess:lastused"

// The hash TTL is a safety net well past idle + several sweeps, so a slow or
// skipped sweep still finds the record (and sends the DELETE) before it expires.
func newRedisMCPSessionStore(client *redis.Client, idleTTL time.Duration) *redisMCPSessionStore {
	return &redisMCPSessionStore{client: client, ttl: idleTTL + 3*mcpSweepInterval}
}

func redisMCPHashKey(key mcpSessionKey) string {
	return "mcp:sess:" + key.Account + ":" + key.Session
}

func redisMCPMember(key mcpSessionKey) string {
	b, _ := json.Marshal(key)
	return string(b)
}

// putIfAbsentScript: KEYS[1]=hash, ARGV[1]=field (URL), ARGV[2]=record JSON,
// ARGV[3]=new session id, ARGV[4]=ttl ms. Returns the existing record when it
// wins (a real session, or a sessionless one when ours is sessionless too),
// else stores ours and returns nil.
var putIfAbsentScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur then
  local c = cjson.decode(cur)
  if c['sid'] ~= '' or ARGV[3] == '' then
    return cur
  end
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return false
`)

// deleteIfScript: KEYS[1]=hash, ARGV[1]=field, ARGV[2]=session id. Removes the
// field only if it still holds that session id.
var deleteIfScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur and cjson.decode(cur)['sid'] == ARGV[2] then
  redis.call('HDEL', KEYS[1], ARGV[1])
  return 1
end
return 0
`)

// claimScript: KEYS[1]=lastused zset, ARGV[1]=member, ARGV[2]=cutoff ms.
// Removes the member only if it is still idle, so a session used after the
// sweeper listed it is not claimed.
var claimScript = redis.NewScript(`
local s = redis.call('ZSCORE', KEYS[1], ARGV[1])
if s and tonumber(s) <= tonumber(ARGV[2]) then
  redis.call('ZREM', KEYS[1], ARGV[1])
  return 1
end
return 0
`)

func (r *redisMCPSessionStore) Get(ctx context.Context, key mcpSessionKey) (mcpSessionRecord, bool, error) {
	raw, err := r.client.HGet(ctx, redisMCPHashKey(key), key.URL).Result()
	if errors.Is(err, redis.Nil) {
		return mcpSessionRecord{}, false, nil
	}
	if err != nil {
		return mcpSessionRecord{}, false, err
	}
	var rec mcpSessionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return mcpSessionRecord{}, false, err
	}
	return rec, true, nil
}

func (r *redisMCPSessionStore) PutIfAbsent(ctx context.Context, key mcpSessionKey, rec mcpSessionRecord) (mcpSessionRecord, bool, error) {
	recJSON, _ := json.Marshal(rec)
	res, err := putIfAbsentScript.Run(ctx, r.client, []string{redisMCPHashKey(key)},
		key.URL, string(recJSON), rec.SessionID, r.ttl.Milliseconds()).Text()
	if err != nil && !errors.Is(err, redis.Nil) {
		return mcpSessionRecord{}, false, err
	}
	if err == nil {
		var cur mcpSessionRecord
		if uerr := json.Unmarshal([]byte(res), &cur); uerr != nil {
			return mcpSessionRecord{}, false, uerr
		}
		return cur, false, nil
	}
	// The record is stored; a failed touch only delays its idle-index entry until
	// the next call. Reporting it as an error would make the fallback store keep
	// a second copy whose idle sweep could later close the live session.
	_ = r.Touch(ctx, key)
	return rec, true, nil
}

func (r *redisMCPSessionStore) Touch(ctx context.Context, key mcpSessionKey) error {
	pipe := r.client.Pipeline()
	pipe.ZAdd(ctx, redisMCPLastUsedKey, redis.Z{Score: float64(time.Now().UnixMilli()), Member: redisMCPMember(key)})
	pipe.PExpire(ctx, redisMCPHashKey(key), r.ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisMCPSessionStore) DeleteIf(ctx context.Context, key mcpSessionKey, sessionID string) (bool, error) {
	n, err := deleteIfScript.Run(ctx, r.client, []string{redisMCPHashKey(key)}, key.URL, sessionID).Int()
	if err != nil {
		return false, err
	}
	if n == 1 {
		// The index entry is advisory: a stale one is skipped at claim time.
		r.client.ZRem(ctx, redisMCPLastUsedKey, redisMCPMember(key))
		return true, nil
	}
	return false, nil
}

// unclaim puts a claimed member back as idle, so a later sweep retries it
// after a Redis error interrupted this one.
func (r *redisMCPSessionStore) unclaim(ctx context.Context, member string, cutoff time.Time) {
	r.client.ZAddNX(ctx, redisMCPLastUsedKey, redis.Z{Score: float64(cutoff.UnixMilli()), Member: member})
}

func (r *redisMCPSessionStore) ClaimIdle(ctx context.Context, cutoff time.Time, limit int) ([]claimedMCPSession, error) {
	cutoffMs := strconv.FormatInt(cutoff.UnixMilli(), 10)
	// Classic ZRANGEBYSCORE (not ZRANGE ... BYSCORE) so Redis older than 6.2 works.
	members, err := r.client.Do(ctx, "ZRANGEBYSCORE", redisMCPLastUsedKey, "-inf", cutoffMs,
		"LIMIT", 0, limit).StringSlice()
	if err != nil {
		return nil, err
	}
	var out []claimedMCPSession
	for _, member := range members {
		won, err := claimScript.Run(ctx, r.client, []string{redisMCPLastUsedKey}, member, cutoffMs).Int()
		if err != nil {
			return out, err
		}
		if won != 1 {
			continue // another pod claimed it, or it was used meanwhile
		}
		var key mcpSessionKey
		if err := json.Unmarshal([]byte(member), &key); err != nil {
			continue
		}
		rec, ok, err := r.Get(ctx, key)
		if err != nil {
			r.unclaim(ctx, member, cutoff)
			return out, err
		}
		if !ok {
			continue // already gone (closed, or the hash expired)
		}
		// Remove the record before the caller ends the session upstream, so no
		// call picks up a session that is about to be closed.
		removed, err := r.DeleteIf(ctx, key, rec.SessionID)
		if err != nil {
			r.unclaim(ctx, member, cutoff)
			return out, err
		}
		if !removed {
			continue // replaced or closed meanwhile
		}
		out = append(out, claimedMCPSession{Key: key, Record: rec})
	}
	return out, nil
}
