package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/tmc/langchaingo/llms"
)

// promptFingerprint is a short hash of each part of an LLM request, logged on
// every call so a prompt-cache miss can be traced to the part that changed
// (#39633). Provider prefix caching only reuses the request up to its first
// changed byte, and a change near the top (a timestamp, a map-ordered list, a
// rewritten notebook) fails silently. Comparing consecutive calls of one agent
// run (same agentId) shows the first message that differed; comparing the
// first call of runs of the same agent shows whether the system prompt or tool
// definitions differ between runs. With an explicit cache (cacheMode
// "explicit") the hashed messages are only the part sent after the cached
// prefix, so compare indexes only between calls with the same cacheMode.
type promptFingerprint struct {
	Tools    string   // hash of the tool definitions; "" when none are sent
	Messages []string // hash of each message, in order
}

func fingerprintPrompt(messages []llms.MessageContent, options []llms.CallOption) promptFingerprint {
	var opts llms.CallOptions
	for _, opt := range options {
		opt(&opts)
	}
	fp := promptFingerprint{Messages: make([]string, len(messages))}
	if len(opts.Tools) > 0 {
		fp.Tools = shortHash(opts.Tools)
	}
	for i, m := range messages {
		fp.Messages[i] = shortHash(m)
	}
	return fp
}

// shortHash returns the first 8 bytes of the SHA-256 of v's JSON encoding.
func shortHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "err"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
