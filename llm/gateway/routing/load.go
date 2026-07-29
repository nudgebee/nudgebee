package routing

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

// LoadFile reads and validates a JSON routing config file ({"rules":[...]}). Rules
// default to
// enabled unless "enabled": false is set explicitly.
func LoadFile(path string) ([]Rule, error) {
	if path == "" {
		return nil, nil // no config file → passthrough (DB rules may still apply)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("routing: read config %q: %w", path, err)
	}
	// Default enabled=true: decode into a map first to see if the key was present.
	var raw struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("routing: parse config %q: %w", path, err)
	}
	rules := make([]Rule, 0, len(raw.Rules))
	for i, rm := range raw.Rules {
		r := Rule{Enabled: true} // default on
		if err := json.Unmarshal(rm, &r); err != nil {
			return nil, fmt.Errorf("routing: parse rule #%d in %q: %w", i, path, err)
		}
		rules = append(rules, r)
	}
	if err := Validate(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// NewFromFile loads, validates, and compiles a routing config file into an Engine.
// An empty path yields an empty (passthrough-only) engine.
func NewFromFile(path string) (*Engine, error) {
	if path == "" {
		return NewEngine(nil), nil
	}
	rules, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	slog.Info("routing: loaded rules from config file", "path", path, "rules", len(rules))
	return NewEngine(rules), nil
}
