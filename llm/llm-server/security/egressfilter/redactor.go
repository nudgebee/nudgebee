package egressfilter

import (
	"log/slog"
	"sync"

	"github.com/tmc/langchaingo/llms"
)

// Redactor is the plugin surface for redact mode. When the wrapper's
// resolveAction returns ActionRedact for a call, the wrapper delegates
// the actual mutation of the outbound payload to this function. Left nil
// by default; a policy provider (typically the EE bundle at
// ee/egressfilter/) fills it in from its own init().
//
// Contract:
//   - Returned mutated slice must be a copy — the caller's messages must
//     not be mutated. Any registered implementation is expected to
//     compose against the exported helpers (CopyMessages, ExtractPartText,
//     SetPartText, FindRegionIdx) so this contract holds transparently.
//   - Returned redactions[i] must correspond to hits[i]. Zero-value
//     Redaction (empty Placeholder) means the hit was not redacted
//     (offset outside every region, unrecognised part type, malformed
//     offsets, etc.) so consumers can distinguish "redacted" from
//     "skipped by mapping failure".
//
// When Redactor is nil and the wrapper receives an ActionRedact from the
// gate, it degrades to detect mode (records + logs but does not mutate)
// and emits a one-shot WARN so operators see the misconfiguration.
//
// The mechanism itself (the algorithm that walks the messages tree and
// rewrites bytes at hit offsets) is intentionally NOT in OSS —
// activating redact mode requires an installed policy provider, so
// shipping the algorithm without an activator would be dead code.
//
// Read-only at runtime. Writes must only happen from a package init() —
// Go guarantees single-threaded init(), so no synchronisation is needed
// for the published value. Same pattern as ActionGate.
var Redactor func(messages []llms.MessageContent, hits []Hit, regions []SourceRegion) ([]llms.MessageContent, []Redaction)

// warnRedactWithoutRedactorOnce fires once per process when the wrapper
// resolves to ActionRedact but no Redactor is installed. sync.Once keeps
// the log line off the hot path after the first miss.
var warnRedactWithoutRedactorOnce sync.Once

func warnRedactWithoutRedactor() {
	warnRedactWithoutRedactorOnce.Do(func() {
		slog.Warn("egressfilter: ActionRedact returned by gate but no Redactor is registered; falling back to detect. Install a Redactor from a policy provider's init() to enable redact mode.")
	})
}
