package egressfilter

import (
	"log/slog"
	"sync"
)

// Action is what the wrapper does after a Scan returns hits. The mapping
// from Mode to Action is policy that lives behind a registration hook
// (ActionGate): callers can install a policy at init() time, and when no
// policy is registered the resolver collapses every Mode to ActionAudit.
// A future redact mode plugs in by adding ActionRedact to this enum and a
// new branch in the wrapper's switch; callers don't change.
type Action int

const (
	// ActionAudit emits metric + log + FilterEvent but does NOT block the
	// LLM call. The provider receives the original payload unchanged.
	ActionAudit Action = iota

	// ActionBlock refuses the LLM call: the wrapper returns *Error wrapped
	// in ErrSecretsBlocked and the provider is never contacted.
	ActionBlock
)

// ActionGate resolves the post-detection Action from the configured Mode
// and the Scan result. Left nil by default; a policy provider installs a
// gate from its own init() if it wants Mode == ModeEnforce to actually
// block. When nil, resolveAction returns ActionAudit regardless of Mode.
//
// Read-only at runtime. Writes must only happen from a package init() — Go
// guarantees init() runs single-threaded before main(), so no
// synchronisation is needed for the published value. There is
// intentionally no setter; the var assignment from the providing package's
// init() is the single registration site.
var ActionGate func(mode Mode, result Result) Action

// resolveAction is the wrapper-facing helper. Centralises the nil-gate
// fallback so callers don't reimplement it, and emits a one-shot warning
// when an operator has configured Mode == ModeEnforce but no gate is
// registered — without this, the operator would silently get audit
// behaviour despite their config saying otherwise.
//
// The warning fires once per process via sync.Once so a busy server
// doesn't flood logs on every blocked call.
func resolveAction(mode Mode, result Result) Action {
	if ActionGate != nil {
		return ActionGate(mode, result)
	}
	if mode == ModeEnforce {
		warnEnforceWithoutGateOnce()
	}
	return ActionAudit
}

var enforceWithoutGateOnce sync.Once

func warnEnforceWithoutGateOnce() {
	enforceWithoutGateOnce.Do(func() {
		slog.Warn("egressfilter: ModeEnforce configured but no ActionGate registered; falling back to detect. An action policy provider must be registered at init() to enable blocking.")
	})
}
