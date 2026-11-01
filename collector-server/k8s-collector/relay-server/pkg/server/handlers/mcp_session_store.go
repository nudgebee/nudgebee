package handlers

import (
	"context"
	"sync"
	"time"
)

// mcpSessionKey identifies one caller session's session on one MCP server.
// Session is the caller's chat or run id ("" for tool discovery, which shares
// one session per account).
type mcpSessionKey struct {
	Account string `json:"a"`
	Session string `json:"s"`
	URL     string `json:"u"`
}

// mcpSessionRecord is what is shared between relay pods for a key. It holds no
// credentials: a pod that must end a session it did not open reloads them by
// IntegrationID (see docs/mcp-sessions.md).
type mcpSessionRecord struct {
	// SessionID is the server-issued Mcp-Session-Id; "" marks a sessionless
	// server, so calls on the key skip the initialize handshake.
	SessionID     string `json:"sid"`
	IntegrationID string `json:"iid,omitempty"`
}

type claimedMCPSession struct {
	Key    mcpSessionKey
	Record mcpSessionRecord
}

// mcpSessionStore is the directory of upstream MCP sessions. The Redis
// implementation is shared by all relay pods; the memory one is per pod.
type mcpSessionStore interface {
	// Get returns the record for key, if any.
	Get(ctx context.Context, key mcpSessionKey) (mcpSessionRecord, bool, error)
	// PutIfAbsent stores rec and marks it used, unless a record already exists.
	// A real session replaces a sessionless record, never the reverse. It
	// returns the record now in effect and whether rec was stored.
	PutIfAbsent(ctx context.Context, key mcpSessionKey, rec mcpSessionRecord) (mcpSessionRecord, bool, error)
	// Touch marks key as used now, which defers its idle close.
	Touch(ctx context.Context, key mcpSessionKey) error
	// DeleteIf removes key only if it still holds sessionID, so a session
	// another request has just stored is not discarded.
	DeleteIf(ctx context.Context, key mcpSessionKey, sessionID string) (bool, error)
	// ClaimIdle removes and returns up to limit records last used before
	// cutoff. Across pods, each record is returned to exactly one caller,
	// which then ends the session upstream.
	ClaimIdle(ctx context.Context, cutoff time.Time, limit int) ([]claimedMCPSession, error)
}

// memMCPSessionStore is the per-pod store: correct for single-pod relays and
// the fallback when Redis is not configured.
type memMCPSessionStore struct {
	mu       sync.Mutex
	sessions map[mcpSessionKey]memMCPSession
}

type memMCPSession struct {
	rec      mcpSessionRecord
	lastUsed time.Time
}

func newMemMCPSessionStore() *memMCPSessionStore {
	return &memMCPSessionStore{sessions: map[mcpSessionKey]memMCPSession{}}
}

func (m *memMCPSessionStore) Get(_ context.Context, key mcpSessionKey) (mcpSessionRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	return s.rec, ok, nil
}

func (m *memMCPSessionStore) PutIfAbsent(_ context.Context, key mcpSessionKey, rec mcpSessionRecord) (mcpSessionRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.sessions[key]; ok && (cur.rec.SessionID != "" || rec.SessionID == "") {
		return cur.rec, false, nil
	}
	m.sessions[key] = memMCPSession{rec: rec, lastUsed: time.Now()}
	return rec, true, nil
}

func (m *memMCPSessionStore) Touch(_ context.Context, key mcpSessionKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok {
		s.lastUsed = time.Now()
		m.sessions[key] = s
	}
	return nil
}

func (m *memMCPSessionStore) DeleteIf(_ context.Context, key mcpSessionKey, sessionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok && s.rec.SessionID == sessionID {
		delete(m.sessions, key)
		return true, nil
	}
	return false, nil
}

func (m *memMCPSessionStore) ClaimIdle(_ context.Context, cutoff time.Time, limit int) ([]claimedMCPSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []claimedMCPSession
	for key, s := range m.sessions {
		if len(out) >= limit {
			break
		}
		if s.lastUsed.Before(cutoff) {
			delete(m.sessions, key)
			out = append(out, claimedMCPSession{Key: key, Record: s.rec})
		}
	}
	return out, nil
}
