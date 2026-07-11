// Package lifecycle is the event lifecycle hook/subscription framework.
//
// An event moves through a series of lifecycle PHASES (created, triaged,
// investigation completed/failed/waiting, resolved, closed, ...). Two consumer
// surfaces subscribe to those phases:
//
//  1. In-process api-server modules register a Handler via RegisterLifecycleHook
//     and run synchronously when the phase fires (e.g. notification on created,
//     pagerduty_comment on investigation.completed).
//  2. Workflows (runbook-server) subscribe by declaring a trigger param
//     `on: <phase>`; Emit publishes the event (carrying lifecycle_phase) to the
//     runbook event exchange so the workflow EventRegistry can match it.
//
// This package is intentionally a LEAF: it imports only common/config/security
// so that both `event` (which imports `llm`) and `llm` can import it without an
// import cycle.
package lifecycle

import (
	"strings"
	"sync"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
)

// Phase is a point in an event's lifecycle.
//
// Phase string values are a FROZEN WIRE CONTRACT. They are persisted verbatim
// in workflow trigger definitions (params.on) and matched verbatim in
// runbook-server (internal/model/workflow.go holds a coupled copy of this set,
// and internal/events matches against the lifecycle_phase carried on the event).
// Only ADD new phases — never rename an existing value, or saved workflows stop
// matching.
type Phase string

const (
	PhaseEventCreated           Phase = "event.created"
	PhaseEventTriaged           Phase = "event.triaged"
	PhaseEventUpdated           Phase = "event.updated"
	PhaseInvestigationEnqueued  Phase = "investigation.enqueued"
	PhaseInvestigationWaiting   Phase = "investigation.waiting"
	PhaseInvestigationCompleted Phase = "investigation.completed"
	PhaseInvestigationFailed    Phase = "investigation.failed"
	PhaseEventResolved          Phase = "event.resolved"
	PhaseEventClosed            Phase = "event.closed"
)

// LifecyclePhaseKey is the event-map key Emit stamps with the current phase.
// runbook-server reads it (defaulting to event.created when absent) to match a
// workflow trigger's params.on.
const LifecyclePhaseKey = "lifecycle_phase"

// Handler is an in-process subscriber. It uses the same signature as the legacy
// event processors so existing functions (notification.ProcessEvent, etc.)
// register unchanged. Handlers are expected to self-gate (e.g. notification
// checks priority/nb_status) and to be best-effort — a returned error is logged,
// not propagated.
type Handler func(ctx *security.RequestContext, event map[string]any) error

type namedHandler struct {
	name string
	fn   Handler
}

var (
	mu    sync.RWMutex
	hooks = map[Phase][]namedHandler{}
)

// RegisterLifecycleHook subscribes an in-process handler to a phase. Handlers
// run in registration order (deterministic). Call from package init().
func RegisterLifecycleHook(phase Phase, name string, fn Handler) {
	mu.Lock()
	defer mu.Unlock()
	hooks[phase] = append(hooks[phase], namedHandler{name: name, fn: fn})
}

func hooksFor(phase Phase) []namedHandler {
	mu.RLock()
	defer mu.RUnlock()
	hs := hooks[phase]
	if len(hs) == 0 {
		return nil
	}
	// Return a copy: the caller (Emit) iterates without holding the lock, and
	// RegisterLifecycleHook is a public API that may append concurrently, which
	// can reallocate or mutate the shared backing array.
	cp := make([]namedHandler, len(hs))
	copy(cp, hs)
	return cp
}

// workflowEligiblePhases are the phases published to the runbook event exchange
// for workflow matching. investigation.enqueued is intentionally excluded — it
// is an internal api-server marker with no workflow-trigger use case, and
// publishing it would be pure noise.
var workflowEligiblePhases = map[Phase]bool{
	PhaseEventCreated:           true,
	PhaseEventTriaged:           true,
	PhaseEventUpdated:           true,
	PhaseInvestigationWaiting:   true,
	PhaseInvestigationCompleted: true,
	PhaseInvestigationFailed:    true,
	PhaseEventResolved:          true,
	PhaseEventClosed:            true,
}

// Emit fans a lifecycle phase out to both consumer surfaces:
//
//  1. in-process hooks registered for the phase (synchronous, ordered,
//     log-and-continue — each hook self-gates), and
//  2. for workflow-eligible phases, an evidence-stripped publish to the runbook
//     event exchange carrying lifecycle_phase, so workflows whose trigger
//     params.on == phase can match.
//
// extra is merged into the event map first (e.g. analysis_* on completion) so
// both surfaces see it. Best-effort: a hook error or publish failure is logged,
// never returned — a lifecycle emit must not break the transition that fired it.
func Emit(ctx *security.RequestContext, phase Phase, event map[string]any, extra map[string]any) {
	// Guard nil only: writing to a nil map panics. An empty (non-nil) map is a
	// valid event and is written to normally.
	if event == nil {
		ctx.GetLogger().Error("lifecycle: cannot emit on nil event map", "phase", string(phase))
		return
	}
	for k, v := range extra {
		event[k] = v
	}
	event[LifecyclePhaseKey] = string(phase)

	for _, h := range hooksFor(phase) {
		if err := h.fn(ctx, event); err != nil {
			ctx.GetLogger().Error("lifecycle: hook failed", "phase", string(phase), "hook", h.name, "error", err)
		}
	}

	if workflowEligiblePhases[phase] {
		publishToWorkflows(ctx, phase, event)
	}
}

// publishToWorkflows mirrors the legacy workflow.ProcessEvent publish: it skips
// SUPPRESSED events and strips evidences (multi-MB, not needed for matching;
// runbook-server refetches the full event from DB if a workflow needs them),
// then publishes to the runbook event exchange.
func publishToWorkflows(ctx *security.RequestContext, phase Phase, event map[string]any) {
	if nbStatus, _ := event["nb_status"].(string); strings.EqualFold(nbStatus, "SUPPRESSED") {
		return
	}
	// Guard both missing (type-assertion fail) and empty: runbook-server refetches
	// the full event by these identifiers, so publishing an empty one is a
	// guaranteed-miss / cross-tenant-scan hazard downstream.
	if cloudAccountID, _ := event["cloud_account_id"].(string); cloudAccountID == "" {
		ctx.GetLogger().Error("lifecycle: cloud_account_id missing or empty, skipping workflow publish", "phase", string(phase))
		return
	}
	if eventID, _ := event["id"].(string); eventID == "" {
		ctx.GetLogger().Error("lifecycle: event id missing or empty, skipping workflow publish", "phase", string(phase))
		return
	}

	lightweight := make(map[string]any, len(event))
	for k, v := range event {
		if k == "evidences" {
			continue
		}
		lightweight[k] = v
	}

	if err := common.MqPublish(
		config.Config.RabbitMqRunbookEventExchange,
		config.Config.RabbitMqRunbookEventRoutingKey,
		lightweight,
		common.MqPublishWithExpiration(2*time.Hour),
	); err != nil {
		ctx.GetLogger().Error("lifecycle: failed to publish event to workflow exchange", "phase", string(phase), "error", err)
	}
}
