package playbooks

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// maxDecodedPayloadBytes caps what one agent enrichment may expand to. Generous by
// three orders of magnitude against real payloads, and far past anything a log card
// can render, so it only ever trips on a runaway.
const maxDecodedPayloadBytes = 32 << 20 // 32 MiB

// DecodeAgentPayload turns an agent enrichment payload into text.
//
// The k8s agent gzips file-shaped enrichments (pod logs, mostly) and hands back
// the base64 of that rendered through Python's bytes repr — `b'H4sIAAAA...'`.
// Consumers that treat the string as text end up storing or displaying the
// base64 blob itself: every logs_enricher evidence written before this existed
// holds exactly one "log line" containing the blob. llm-server unwraps the same
// convention on the read side for `type:"file"` evidences (tools/tool_event.go).
//
// Anything that is not a wrapped blob is returned unchanged, so a payload that
// is already plain text still works.
func DecodeAgentPayload(payload string) (string, error) {
	encoded := strings.TrimSpace(payload)
	switch {
	case strings.HasPrefix(encoded, "b'") && strings.HasSuffix(encoded, "'"):
		encoded = strings.TrimSuffix(strings.TrimPrefix(encoded, "b'"), "'")
	case strings.HasPrefix(encoded, `b"`) && strings.HasSuffix(encoded, `"`):
		encoded = strings.TrimSuffix(strings.TrimPrefix(encoded, `b"`), `"`)
	default:
		return payload, nil
	}

	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64: %w", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = reader.Close() }()

	// Bounded read: the compressed payload arrives from the cluster agent, and what it
	// compressed is a pod's own stdout. A workload in a log loop — no attacker required —
	// produces a small blob that expands to gigabytes, and this runs inside the event
	// pipeline. Read one byte past the cap so hitting it is distinguishable from an
	// exact fit. Real payloads are three orders of magnitude under this: the largest
	// observed on dev decodes to a few KB.
	decompressed, err := io.ReadAll(io.LimitReader(reader, maxDecodedPayloadBytes+1))
	if err != nil {
		return "", fmt.Errorf("gzip read: %w", err)
	}
	if len(decompressed) > maxDecodedPayloadBytes {
		// Refuse rather than truncate. A truncated log stored as if it were the whole
		// thing is the failure this change exists to stop; no evidence and a log line
		// saying why is the honest outcome.
		return "", fmt.Errorf("decoded payload exceeds %d bytes", maxDecodedPayloadBytes)
	}
	return string(decompressed), nil
}

// IsWrappedAgentPayload reports whether payload uses the bytes-repr wrapper that
// DecodeAgentPayload unwraps.
func IsWrappedAgentPayload(payload string) bool {
	s := strings.TrimSpace(payload)
	return (strings.HasPrefix(s, "b'") && strings.HasSuffix(s, "'")) ||
		(strings.HasPrefix(s, `b"`) && strings.HasSuffix(s, `"`))
}
