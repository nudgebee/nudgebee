package playbooks

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// maxDecodedPayloadBytes caps what one agent enrichment may expand to. Generous by
// three orders of magnitude against real payloads, and far past anything a log card
// can render, so it only ever trips on a runaway.
const maxDecodedPayloadBytes = 32 << 20 // 32 MiB

// logRetrievalFailureMarkers are the kubelet's ways of saying "there are no
// logs here", returned on the same successful path as real output.
//
// They matter twice over. As evidence they read as log content — an LLM
// summarising "unable to retrieve container logs for containerd://a6a6ce…"
// is summarising a plumbing failure as if it were the workload's output. And
// because k8s_pod_log_enricher belongs to eventrule.logActions, a stored
// failure marks the whole log category collected, so the account's configured
// log source is never asked. 23 OOM/crash-loop events on dev carried one of
// these instead of logs.
var logRetrievalFailureMarkers = []string{
	"unable to retrieve container logs for",
	"is waiting to start",
	"not found in pod",
	"previous terminated container",
}

// noLogsAnswerPatterns are the sentences the cluster uses to say a container has
// no logs to give, as they arrive in a relay error or in kubectl's stderr.
//
// The first three are the kubelet's own (validateContainerLogStatus): the
// container never started, it has no earlier instance, or it exited and its logs
// went with it. The last is the agent's, for a subject it resolved to no pods.
// Each is anchored on the whole sentence rather than a phrase, because this text
// is matched inside error strings that can carry anything — "is terminated"
// alone would also match a transport error that happens to say so. "is not
// available" and a plain NotFound are deliberately absent: neither says the logs
// do not exist.
var noLogsAnswerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`container "[^"]*" in pod "[^"]*" is waiting to start`),
	regexp.MustCompile(`previous terminated container "[^"]*" in pod "[^"]*" not found`),
	regexp.MustCompile(`container "[^"]*" in pod "[^"]*" is terminated`),
	noPodsAnswerPattern,
}

// noPodsAnswerPattern is the one no-logs answer that rules out every container
// instance at once: with no pod there is no live container to try instead.
var noPodsAnswerPattern = regexp.MustCompile(`no pods found for workload`)

// ClusterHasNoLogs reports whether text — a relay error, kubectl's stderr — is
// the cluster answering that there are no logs to read, as opposed to the read
// failing.
//
// The difference decides what happens next. A failed read is worth another
// route; this answer is not, because every route ends at the same kubelet.
// Treated as a failure, each one was reported as an error and then asked again
// by the next route, which could only be told the same thing.
func ClusterHasNoLogs(text string) bool {
	for _, pattern := range noLogsAnswerPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// IsLogRetrievalFailure reports whether decoded pod-log output is one of the
// kubelet's "no logs available" responses rather than actual log lines.
// Deliberately anchored on a short prefix of the payload: a real log line
// quoting one of these phrases appears mid-stream, not as the entire body.
func IsLogRetrievalFailure(decoded string) bool {
	trimmed := strings.TrimSpace(decoded)
	if trimmed == "" || strings.Count(trimmed, "\n") > 2 {
		return false
	}
	lowered := strings.ToLower(trimmed)
	for _, marker := range logRetrievalFailureMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

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
