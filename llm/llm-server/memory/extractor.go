package memory

import (
	"context"

	memcollective "nudgebee/llm/memory/stores/collective"
	memdecisions "nudgebee/llm/memory/stores/decisions"
	"nudgebee/llm/memory/stores/eventlog"
	mempatterns "nudgebee/llm/memory/stores/patterns"
	memprefs "nudgebee/llm/memory/stores/preferences"
)

// ExtractedFact is a typed output of the extractor. Classifier maps it to
// one (or more) target stores at projection time.
type ExtractedFact struct {
	TenantID       string
	UserID         string
	ConversationID string
	AgentModule    string
	Kind           string         // free-form; classifier uses it
	Subject        string         // short human-readable tag
	Body           string         // full content
	Metadata       map[string]any // arbitrary
	Target         TargetStore    // hint: where this fact belongs
	Confidence     float64
}

// ProjectFact writes a single ExtractedFact into its target store.
// Events are written by the caller (Observe); this is the projection half.
// Failures are logged but do not abort the caller — the event log is the
// authority and can be replayed.
func ProjectFact(_ context.Context, f ExtractedFact) error {
	switch f.Target {
	case TargetPreferences:
		var modulePtr *string
		if f.AgentModule != "" && f.AgentModule != "generic" {
			m := f.AgentModule
			modulePtr = &m
		}
		return memprefs.Upsert(&memprefs.Preference{
			TenantID:    f.TenantID,
			UserID:      f.UserID,
			AgentModule: modulePtr,
			Key:         f.Subject,
			Value:       f.Body,
			Source:      memprefs.SourceInferred,
			Confidence:  clampConfidence(f.Confidence, 0.6),
		})

	case TargetPatterns:
		var modulePtr *string
		if f.AgentModule != "" && f.AgentModule != "generic" {
			m := f.AgentModule
			modulePtr = &m
		}
		return mempatterns.Upsert(&mempatterns.Pattern{
			TenantID:    f.TenantID,
			UserID:      f.UserID,
			AgentModule: modulePtr,
			Kind:        f.Kind,
			Subject:     f.Subject,
			Metadata:    f.Metadata,
		})

	case TargetDecisions:
		var (
			convPtr *string
			modPtr  *string
		)
		if f.ConversationID != "" {
			v := f.ConversationID
			convPtr = &v
		}
		if f.AgentModule != "" && f.AgentModule != "generic" {
			v := f.AgentModule
			modPtr = &v
		}
		return memdecisions.Append(&memdecisions.Decision{
			TenantID:       f.TenantID,
			UserID:         f.UserID,
			ConversationID: convPtr,
			AgentModule:    modPtr,
			DecisionType:   f.Kind,
			Subject:        f.Subject,
			Context:        f.Metadata,
		})

	case TargetCollective:
		var modPtr *string
		if f.AgentModule != "" && f.AgentModule != "generic" {
			v := f.AgentModule
			modPtr = &v
		}
		return memcollective.Upsert(&memcollective.Entry{
			TenantID:    f.TenantID,
			AgentModule: modPtr,
			EntryKind:   f.Kind,
			Subject:     f.Subject,
			Body:        f.Body,
			Metadata:    f.Metadata,
			Confidence:  clampConfidence(f.Confidence, 0.7),
		})

	case TargetQuarantine:
		// Non-classified facts are surfaced via telemetry elsewhere; the
		// projection itself is a no-op so the event log stays the source of truth.
		return nil

	default:
		return nil
	}
}

// clampConfidence returns fallback if c is zero; otherwise clamps c to [0, 1].
func clampConfidence(c, fallback float64) float64 {
	if c == 0 {
		return fallback
	}
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
}

// ExtractorBridge is the interface the legacy extractor (agents/core/memory.go)
// uses to hand off facts to the Memory Module during Shadow/Dual migration
// modes. Once Phase 2 retires llm_conversation_memory, the extractor moves
// fully into this package and the bridge is removed.
//
// The legacy code calls memory.BridgeFromLegacy() for each fact it would
// have written to llm_conversation_memory; the bridge classifies and
// projects into typed stores.
type ExtractorBridge interface {
	BridgeFromLegacy(fact LegacyMemoryFact) error
}

// LegacyMemoryFact is the minimal shape the legacy extractor already produces.
// Phase 2 Shadow mode: legacy code still writes llm_conversation_memory AND
// also calls BridgeFromLegacy for comparison.
type LegacyMemoryFact struct {
	TenantID       string
	UserID         string
	ConversationID string
	AgentModule    string
	MemoryType     string
	Subject        string
	Content        string
	Metadata       map[string]any
	// IdempotencyKey de-duplicates event-log writes at the (tenant_id, key)
	// boundary. Backfill sets this to the legacy row id so re-runs are safe;
	// live extraction from agent turns leaves it empty (every turn produces
	// fresh facts).
	IdempotencyKey string
}

// BridgeFromLegacy maps a legacy memory_type to a target store. Writes an
// event to the log first; projection onto the typed store happens through
// the projection worker (see observe.project). Keeping the event log as the
// single entry point preserves the audit / replay invariant for every
// memory mutation.
func BridgeFromLegacy(f LegacyMemoryFact) error {
	return Default().Observe(context.Background(), ObserveRequest{
		TenantID:    f.TenantID,
		UserID:      f.UserID,
		AgentModule: f.AgentModule,
		EventType:   eventlog.EventTypeFactExtracted,
		Payload: map[string]any{
			"conversation_id": f.ConversationID,
			"memory_type":     f.MemoryType,
			"subject":         f.Subject,
			"content":         f.Content,
			"metadata":        f.Metadata,
		},
		ActorKind:      "agent",
		ActorID:        "memory_extractor",
		IdempotencyKey: f.IdempotencyKey,
	})
}

// unmarshalEventPayload is a thin wrapper around eventlog.UnmarshalPayload so
// callers inside the memory package don't have to import eventlog directly
// just to decode an event they already received.
func unmarshalEventPayload(b []byte) (map[string]any, error) {
	return eventlog.UnmarshalPayload(b)
}

// projectFactFromEvent reconstructs an ExtractedFact from a fact.extracted
// event payload and projects it into the typed stores. Called from the
// projection worker so replays of the event log rebuild state deterministically.
func projectFactFromEvent(ctx context.Context, evt eventlog.Event) error {
	payload, err := unmarshalEventPayload(evt.Payload)
	if err != nil {
		return err
	}
	userID := ""
	if evt.UserID != nil {
		userID = *evt.UserID
	}
	agentModule := ""
	if evt.AgentModule != nil {
		agentModule = *evt.AgentModule
	}
	memoryType, _ := payload["memory_type"].(string)
	convID, _ := payload["conversation_id"].(string)
	subject, _ := payload["subject"].(string)
	content, _ := payload["content"].(string)
	metadata, _ := payload["metadata"].(map[string]any)
	fact := ExtractedFact{
		TenantID:       evt.TenantID,
		UserID:         userID,
		ConversationID: convID,
		AgentModule:    agentModule,
		Kind:           defaultKindForLegacy(memoryType),
		Subject:        subject,
		Body:           content,
		Metadata:       metadata,
		Target:         ClassifyLegacyType(memoryType),
		Confidence:     0.7,
	}
	return ProjectFact(ctx, fact)
}

// defaultKindForLegacy returns the store-specific kind value for a legacy
// memory_type so the row lands with consistent labelling.
func defaultKindForLegacy(memoryType string) string {
	switch memoryType {
	case "user_preference":
		return "preference" // kind is implicit in Preferences; key is meaningful
	case "pattern":
		return "frequent_resource_type"
	case "workflow":
		return "preferred_diagnostic_flow"
	case "investigation_result":
		return "root_cause_agreed"
	case "architectural_fact":
		return "architectural_fact"
	case "configuration_insight":
		return "configuration_insight"
	case "dependency_mapping":
		return "dependency_mapping"
	case "troubleshooting_guide", "troubleshooting":
		return "troubleshooting"
	default:
		return memoryType
	}
}
