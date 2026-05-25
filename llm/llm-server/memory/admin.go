package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	memprefs "nudgebee/llm/memory/stores/preferences"
	memsoul "nudgebee/llm/memory/stores/soul"
)

// ErrInvalidLayer is returned when a management request targets an unknown layer.
var ErrInvalidLayer = errors.New("memory: invalid layer")

// Get returns entries from a specific layer for the management UI.
func (m *impl) Get(_ context.Context, req GetRequest) (GetResponse, error) {
	switch req.Layer {
	case "soul":
		s, err := memsoul.Get(req.TenantID, req.UserID)
		if err != nil {
			return GetResponse{}, fmt.Errorf("memory.Get soul: %w", err)
		}
		entries := []any{}
		if s != nil {
			entries = append(entries, s)
		}
		return GetResponse{Layer: "soul", Entries: entries, Total: len(entries)}, nil

	case "preferences":
		prefs, err := memprefs.ListForUser(req.TenantID, req.UserID, "")
		if err != nil {
			return GetResponse{}, fmt.Errorf("memory.Get preferences: %w", err)
		}
		entries := make([]any, 0, len(prefs))
		for i := range prefs {
			entries = append(entries, prefs[i])
		}
		return GetResponse{Layer: "preferences", Entries: entries, Total: len(entries)}, nil

	default:
		return GetResponse{}, fmt.Errorf("%w: %q", ErrInvalidLayer, req.Layer)
	}
}

// Mutate applies a layer-specific mutation. Writes an event first (audit) then
// projects the state. Cache invalidation happens on successful projection.
func (m *impl) Mutate(ctx context.Context, req MutateRequest) (MutateResponse, error) {
	switch req.Layer {
	case "soul":
		return m.mutateSoul(ctx, req)
	case "preferences":
		return m.mutatePreferences(ctx, req)
	default:
		return MutateResponse{}, fmt.Errorf("%w: %q", ErrInvalidLayer, req.Layer)
	}
}

func (m *impl) mutateSoul(ctx context.Context, req MutateRequest) (MutateResponse, error) {
	switch req.Action {
	case "set":
		// Load-then-merge: a client that sends only `markdown` must not wipe
		// an existing `style`, and vice versa. Only fields that appear in
		// req.Value get overwritten.
		existing, err := memsoul.Get(req.TenantID, req.UserID)
		if err != nil {
			return MutateResponse{}, fmt.Errorf("mutateSoul: load existing: %w", err)
		}
		s := &memsoul.Soul{TenantID: req.TenantID, UserID: req.UserID}
		if existing != nil {
			*s = *existing
			s.TenantID = req.TenantID
			s.UserID = req.UserID
		}
		if styleMap, ok := req.Value["style"].(map[string]any); ok {
			b, err := json.Marshal(styleMap)
			if err != nil {
				return MutateResponse{}, fmt.Errorf("mutateSoul: marshal style: %w", err)
			}
			if err := json.Unmarshal(b, &s.Style); err != nil {
				return MutateResponse{}, fmt.Errorf("mutateSoul: unmarshal style: %w", err)
			}
		}
		if md, ok := req.Value["markdown"].(string); ok {
			s.Markdown = md
		}
		if err := memsoul.Upsert(s); err != nil {
			return MutateResponse{}, err
		}
		_ = m.Observe(ctx, ObserveRequest{
			TenantID: req.TenantID, UserID: req.UserID,
			EventType: "soul.updated",
			Payload:   req.Value,
			ActorKind: req.ActorKind, ActorID: req.ActorID,
		})
		invalidateSoulCache(req.TenantID, req.UserID)
		return MutateResponse{Layer: "soul", Action: "set", Success: true}, nil

	case "clear", "delete":
		if err := memsoul.Delete(req.TenantID, req.UserID); err != nil {
			return MutateResponse{}, err
		}
		_ = m.Observe(ctx, ObserveRequest{
			TenantID: req.TenantID, UserID: req.UserID,
			EventType: "soul.cleared",
			ActorKind: req.ActorKind, ActorID: req.ActorID,
		})
		invalidateSoulCache(req.TenantID, req.UserID)
		return MutateResponse{Layer: "soul", Action: req.Action, Success: true}, nil
	}
	return MutateResponse{}, fmt.Errorf("unsupported action for soul: %q", req.Action)
}

func (m *impl) mutatePreferences(ctx context.Context, req MutateRequest) (MutateResponse, error) {
	var modulePtr *string
	if mod, ok := req.Value["agent_module"].(string); ok && mod != "" {
		modulePtr = &mod
	}

	switch req.Action {
	case "set":
		value := req.Value["value"]
		p := &memprefs.Preference{
			TenantID:    req.TenantID,
			UserID:      req.UserID,
			AgentModule: modulePtr,
			Key:         req.Key,
			Value:       value,
			Source:      memprefs.SourceExplicit,
			Confidence:  1.0,
		}
		if err := memprefs.Upsert(p); err != nil {
			return MutateResponse{}, err
		}
		_ = m.Observe(ctx, ObserveRequest{
			TenantID: req.TenantID, UserID: req.UserID,
			EventType: "preference.set",
			Payload:   map[string]any{"key": req.Key, "value": value, "agent_module": modulePtr},
			ActorKind: req.ActorKind, ActorID: req.ActorID,
		})
		invalidatePrefsCache(req.TenantID, req.UserID)
		return MutateResponse{Layer: "preferences", Action: "set", Success: true}, nil

	case "clear", "delete":
		if err := memprefs.Clear(req.TenantID, req.UserID, modulePtr, req.Key); err != nil {
			return MutateResponse{}, err
		}
		_ = m.Observe(ctx, ObserveRequest{
			TenantID: req.TenantID, UserID: req.UserID,
			EventType: "preference.cleared",
			Payload:   map[string]any{"key": req.Key, "agent_module": modulePtr},
			ActorKind: req.ActorKind, ActorID: req.ActorID,
		})
		invalidatePrefsCache(req.TenantID, req.UserID)
		return MutateResponse{Layer: "preferences", Action: req.Action, Success: true}, nil
	}
	return MutateResponse{}, fmt.Errorf("unsupported action for preferences: %q", req.Action)
}

// Erase removes all user-scoped memory (GDPR).
func (m *impl) Erase(_ context.Context, req EraseRequest) error {
	if err := memsoul.Delete(req.TenantID, req.UserID); err != nil {
		return fmt.Errorf("memory.Erase soul: %w", err)
	}
	if err := memprefs.DeleteAllForUser(req.TenantID, req.UserID); err != nil {
		return fmt.Errorf("memory.Erase preferences: %w", err)
	}
	invalidateSoulCache(req.TenantID, req.UserID)
	invalidatePrefsCache(req.TenantID, req.UserID)
	return nil
}

// Export returns all user-scoped memory as a JSON bundle.
func (m *impl) Export(_ context.Context, req ExportRequest) (ExportResponse, error) {
	bundle := map[string]any{}

	if s, err := memsoul.Get(req.TenantID, req.UserID); err == nil && s != nil {
		bundle["soul"] = s
	}
	if prefs, err := memprefs.ListForUser(req.TenantID, req.UserID, ""); err == nil {
		bundle["preferences"] = prefs
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return ExportResponse{}, fmt.Errorf("memory.Export marshal: %w", err)
	}
	return ExportResponse{Format: "json", Data: data}, nil
}
