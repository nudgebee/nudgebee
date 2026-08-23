package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"nudgebee/llm-gateway/auth"
)

// fingerprintSalt is fixed domain-separation for the prefix hash. It is NOT a secret
// (the input is one-way hashed regardless); a constant keeps the fingerprint stable
// across restarts, which is required for it to correlate a conversation's turns.
const fingerprintSalt = "nb-gw-prefix-v1"

// prefixFingerprint hashes a request's STABLE prompt prefix — the system prompt, the
// tool set, and the first user message — into a short, deterministic id. Those three
// don't change as a conversation grows, so every turn of one conversation produces the
// SAME fingerprint. It is the gateway-inferred correlation/cache-affinity key when the
// client supplies no session id: same fingerprint => same conversation => route the
// turns to the same provider (keep its prompt cache warm) and group them.
//
// It is a salted SHA-256 (128-bit, hex) of the raw field bytes — never stored content,
// fitting the structure-only capture rule. Provider-shape aware (Anthropic/OpenAI/
// Gemini). Empty when there is no prefix to hash (e.g. an admin/GET call).
//
// The caller's identity (tenant + user) is folded into the hash so two DIFFERENT users
// opening with the same prompt (e.g. "hi" + the same system/tools) get DIFFERENT
// fingerprints — otherwise they'd be grouped as one conversation. Within one user the
// prefix is still stable, so a conversation's turns still share the fingerprint.
//
// Caveat: it is COARSE — the SAME user's two conversations that open identically still
// collide. It's a stateless fallback; a precise per-conversation id needs a client-
// supplied session id or a growing-prefix + recent-fingerprint store (deferred).
func prefixFingerprint(id auth.Identity, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var r struct {
		System            json.RawMessage `json:"system"`            // Anthropic (string or blocks)
		SystemInstruction json.RawMessage `json:"systemInstruction"` // Gemini
		Tools             json.RawMessage `json:"tools"`             // Anthropic / OpenAI / Gemini
		Messages          []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"` // Anthropic / OpenAI
		Contents []struct {
			Role  string          `json:"role"`
			Parts json.RawMessage `json:"parts"`
		} `json:"contents"` // Gemini
	}
	if json.Unmarshal(body, &r) != nil {
		return ""
	}

	// System: top-level (Anthropic) or systemInstruction (Gemini), else the first
	// system/developer message (OpenAI, which carries the system role in messages).
	system := r.System
	if len(system) == 0 {
		system = r.SystemInstruction
	}
	if len(system) == 0 {
		for _, m := range r.Messages {
			if m.Role == "system" || m.Role == "developer" {
				system = m.Content
				break
			}
		}
	}

	// First user message (Anthropic messages[0], OpenAI first user, Gemini contents[0]).
	// The first user turn never changes as the conversation grows, so it stays stable.
	var firstUser json.RawMessage
	for _, m := range r.Messages {
		if m.Role == "user" {
			firstUser = m.Content
			break
		}
	}
	if len(firstUser) == 0 {
		for _, ct := range r.Contents {
			if ct.Role == "user" || ct.Role == "" {
				firstUser = ct.Parts
				break
			}
		}
	}

	if len(system) == 0 && len(r.Tools) == 0 && len(firstUser) == 0 {
		return "" // nothing stable to fingerprint
	}
	// Delimit every field with a null byte so boundary shifting can't collide
	// (system="ab"+tools="c" must not hash the same as system="a"+tools="bc"). The
	// JSON fields never contain an unescaped 0x00; identity is scoped ids, so the
	// delimiter is unambiguous. Identity (tenant ∥ user) leads the input so it
	// namespaces the prefix per user.
	h := sha256.New()
	h.Write([]byte(fingerprintSalt))
	h.Write([]byte{0})
	h.Write([]byte(id.TenantID))
	h.Write([]byte{0})
	h.Write([]byte(id.UserID))
	h.Write([]byte{0})
	h.Write(system)
	h.Write([]byte{0})
	h.Write(r.Tools)
	h.Write([]byte{0})
	h.Write(firstUser)
	return hex.EncodeToString(h.Sum(nil)[:16])
}
