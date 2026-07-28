package core

import (
	"strings"
	"sync"
	"time"
)

// discoveredToolsTTL bounds how long a search_tools discovery stays directly-callable.
// It only needs to outlive the planner loop that discovered the tool; an hour is
// generous and keeps the registry from holding stale conversations.
const discoveredToolsTTL = 1 * time.Hour

// discoveredToolsSweepThreshold triggers an opportunistic cleanup of expired
// conversations on write, so the registry can't grow unbounded across a long-lived pod.
const discoveredToolsSweepThreshold = 512

// discoveredToolsRegistry tracks, per conversation, the tool/agent names that
// search_tools surfaced during that conversation. It lets the dispatch auth gate accept
// a DIRECT call to such a tool (the "discover then call it directly" one-shot path) even
// though the tool is not in the orchestrator's preloaded GetSupportedTools — scoped to
// what was actually discovered, so the model still cannot call arbitrary tools it merely
// guessed. In-memory + TTL: this covers the common same-message planner loop (search →
// direct call in one execution); a cross-pod / cross-turn miss simply re-rejects and the
// planner falls back to delegate_agent or re-searches — graceful, no hard failure.
// discoveredToolsSweepInterval rate-limits the opportunistic cleanup so a busy pod
// doesn't iterate the whole map on every write once past the threshold.
const discoveredToolsSweepInterval = 5 * time.Minute

type discoveredToolsRegistry struct {
	mu        sync.Mutex
	byConv    map[string]*discoveredEntry
	lastSweep time.Time
}

type discoveredEntry struct {
	names   map[string]string // lowercased name -> canonical (registered) name
	expires time.Time
}

var discoveredTools = &discoveredToolsRegistry{byConv: map[string]*discoveredEntry{}}

// RecordDiscoveredTools marks toolNames as directly-callable for the conversation,
// preserving each name's canonical casing so the auth gate can resolve it against the
// case-sensitive tool/agent registries. No-op when conversationId is empty.
func RecordDiscoveredTools(conversationId string, toolNames []string) {
	if conversationId == "" || len(toolNames) == 0 {
		return
	}
	discoveredTools.mu.Lock()
	defer discoveredTools.mu.Unlock()

	// Opportunistic, rate-limited cleanup of expired conversations (bounds growth
	// without iterating the map on every write under load).
	if len(discoveredTools.byConv) >= discoveredToolsSweepThreshold && time.Since(discoveredTools.lastSweep) > discoveredToolsSweepInterval {
		nowT := time.Now()
		for id, e := range discoveredTools.byConv {
			if e.expires.Before(nowT) {
				delete(discoveredTools.byConv, id)
			}
		}
		discoveredTools.lastSweep = nowT
	}

	e := discoveredTools.byConv[conversationId]
	if e == nil || e.expires.Before(time.Now()) {
		e = &discoveredEntry{names: map[string]string{}, expires: time.Now().Add(discoveredToolsTTL)}
		discoveredTools.byConv[conversationId] = e
	}
	// Slide the expiry on every write so a conversation that keeps discovering tools
	// doesn't lose them 1h after its FIRST discovery.
	e.expires = time.Now().Add(discoveredToolsTTL)
	for _, n := range toolNames {
		if trimmed := strings.TrimSpace(n); trimmed != "" {
			e.names[strings.ToLower(trimmed)] = trimmed
		}
	}
}

// DiscoveredToolCanonicalName returns the canonical (registered) name of toolName if it
// was surfaced by search_tools in this conversation (matched case-insensitively). The
// auth gate resolves with the canonical name so a case-mismatch from the planner still
// hits the case-sensitive tool/agent registries.
func DiscoveredToolCanonicalName(conversationId, toolName string) (string, bool) {
	if conversationId == "" || toolName == "" {
		return "", false
	}
	discoveredTools.mu.Lock()
	defer discoveredTools.mu.Unlock()
	e := discoveredTools.byConv[conversationId]
	if e == nil {
		return "", false
	}
	if e.expires.Before(time.Now()) {
		// Eagerly free the expired entry instead of waiting for the next write's sweep.
		delete(discoveredTools.byConv, conversationId)
		return "", false
	}
	canonical, ok := e.names[strings.ToLower(strings.TrimSpace(toolName))]
	return canonical, ok
}

// IsDiscoveredTool reports whether toolName was surfaced by search_tools in this
// conversation (case-insensitive).
func IsDiscoveredTool(conversationId, toolName string) bool {
	_, ok := DiscoveredToolCanonicalName(conversationId, toolName)
	return ok
}
