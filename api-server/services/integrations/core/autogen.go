package core

import (
	"log/slog"
	"strings"
	"sync"

	"nudgebee/services/security"
)

// AutoGenOption is one selectable suggestion returned by an autogen handler.
// Label is rendered to the user; Value is submitted in the form field.
type AutoGenOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// AutoGenResult is what a handler returns. Message is an optional user-facing
// hint shown alongside the suggestions (e.g. "Re-enter password to load
// columns") when the result is intentionally empty.
type AutoGenResult struct {
	Options []AutoGenOption `json:"options"`
	Message string          `json:"message,omitempty"`
}

// AutoGenHandler resolves form-context-dependent suggestions for an
// IntegrationSchemaProperty whose AutoGenerateFunc names the handler.
// formValues is the current state of all fields in the form (decrypted
// where the user has supplied a value; encrypted fields the user has not
// re-typed will arrive empty).
type AutoGenHandler func(ctx *security.RequestContext, formValues map[string]any) (AutoGenResult, error)

var (
	autoGenHandlers   = map[string]AutoGenHandler{}
	autoGenHandlersMu sync.RWMutex
)

// RegisterAutoGenHandler registers a handler under name (case-insensitive).
// Intended to be called from an integration package's init() — mirrors the
// pattern in registry.go.
func RegisterAutoGenHandler(name string, h AutoGenHandler) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || h == nil {
		slog.Warn("autogen: ignoring invalid registration", "name", name, "nil_handler", h == nil)
		return
	}
	autoGenHandlersMu.Lock()
	defer autoGenHandlersMu.Unlock()
	if _, ok := autoGenHandlers[key]; ok {
		slog.Warn("autogen: handler already registered, overwriting", "name", name)
	}
	autoGenHandlers[key] = h
	slog.Info("autogen: registered handler", "name", name)
}

// GetAutoGenHandler returns the handler registered under name (case-insensitive).
func GetAutoGenHandler(name string) (AutoGenHandler, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	autoGenHandlersMu.RLock()
	defer autoGenHandlersMu.RUnlock()
	h, ok := autoGenHandlers[key]
	return h, ok
}
