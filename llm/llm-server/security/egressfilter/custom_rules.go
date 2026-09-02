package egressfilter

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// Per-tenant custom detection patterns. Admins define extra secret-shaped
// regexes (things the built-in corpus can't know about — internal token
// formats, customer-id shapes) that are scanned alongside the baseline/EE
// rules. Stored as a JSON array in the TenantConfig.CustomRules JSONB column;
// compiled once when the tenant config is resolved (see Resolve) and applied
// on the LLM call path (see scanAndDecide).
//
// Action on match follows the tenant's global Mode — a custom hit blocks in
// enforce, redacts in redact, records in detect — exactly like a built-in hit.
//
// Cardinality: every custom hit carries the single constant rule id
// CustomRuleRuleID so the hits metric stays bounded (see metrics.go). The
// specific pattern name rides on the Hit for the audit log / FilterEvent, NOT
// as a metric label.
const (
	// CustomRuleRuleID is the metric/rule id shared by every custom-pattern
	// hit. Constant by design — per-pattern ids would be unbounded-cardinality
	// tenant data on a metric label.
	CustomRuleRuleID = "custom-pattern"

	// Write-time budgets. These bound both the scan cost on the hot path and
	// the blast radius of a misconfigured tenant. Go's regexp is RE2 (linear
	// time, no catastrophic backtracking), so the cap is about scan cost and
	// UI sanity, not ReDoS.
	maxCustomRules        = 25
	maxCustomRuleRegexLen = 500
	maxCustomRuleNameLen  = 80
)

// CustomRule is one tenant-defined detection pattern. Serialized as JSON in
// the custom_rules JSONB column.
type CustomRule struct {
	ID      string `json:"id"`      // server-assigned uuid; stable across edits
	Name    string `json:"name"`    // human label, shown in the UI + audit log
	Regex   string `json:"regex"`   // the pattern; validated to compile on write
	Enabled bool   `json:"enabled"` // disabled rules persist but are not scanned
}

// compiledCustomRule pairs a rule's display name with its compiled regex for
// the scan path.
type compiledCustomRule struct {
	name  string
	regex *regexp.Regexp
}

// ParseCustomRules unmarshals the raw custom_rules JSONB into []CustomRule.
// An empty/nil payload is "no rules" (nil, nil), not an error.
func ParseCustomRules(raw []byte) ([]CustomRule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil, nil
	}
	var rules []CustomRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("egressfilter: parse custom_rules: %w", err)
	}
	return rules, nil
}

// ValidateCustomRules enforces the write-time contract so a bad config is
// rejected at the admin API rather than silently breaking the scan path:
// count/length caps, required name+regex, unique (case-insensitive) names,
// and that every regex compiles. Returns a human-readable error suitable for
// a 400 response, or nil when the set is valid.
func ValidateCustomRules(rules []CustomRule) error {
	if len(rules) > maxCustomRules {
		return fmt.Errorf("too many custom patterns (%d); max is %d", len(rules), maxCustomRules)
	}
	seen := make(map[string]struct{}, len(rules))
	for i := range rules {
		r := &rules[i]
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("custom pattern name is required")
		}
		if len(name) > maxCustomRuleNameLen {
			return fmt.Errorf("custom pattern name %q exceeds %d characters", name, maxCustomRuleNameLen)
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("duplicate custom pattern name %q", name)
		}
		seen[key] = struct{}{}

		regex := strings.TrimSpace(r.Regex)
		if regex == "" {
			return fmt.Errorf("custom pattern %q: regex is required", name)
		}
		if len(regex) > maxCustomRuleRegexLen {
			return fmt.Errorf("custom pattern %q: regex exceeds %d characters", name, maxCustomRuleRegexLen)
		}
		if _, err := regexp.Compile(regex); err != nil {
			return fmt.Errorf("custom pattern %q: invalid regex: %v", name, err)
		}
	}
	return nil
}

// compileCustomRules compiles the ENABLED rules for the scan path. A rule
// whose regex won't compile is skipped with a warn rather than failing the
// whole set — validation catches bad input on write, so this only fires on a
// corrupted row, and a single bad rule must never break the LLM call path.
// Returns nil when there is nothing to scan.
func compileCustomRules(rules []CustomRule) []compiledCustomRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]compiledCustomRule, 0, len(rules))
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		regex := strings.TrimSpace(r.Regex)
		if regex == "" {
			continue
		}
		re, err := regexp.Compile(regex)
		if err != nil {
			slog.Warn("egressfilter: skipping uncompilable custom rule", "name", r.Name, "error", err)
			continue
		}
		out = append(out, compiledCustomRule{name: r.Name, regex: re})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scanCustomRules runs a tenant's compiled custom rules over the payload and
// returns one Hit per non-overlapping match. Every Hit carries the constant
// CustomRuleRuleID (bounded metric cardinality) plus the matched pattern name
// for the audit log / FilterEvent.
func scanCustomRules(payload string, compiled []compiledCustomRule) []Hit {
	if payload == "" || len(compiled) == 0 {
		return nil
	}
	var hits []Hit
	for _, cr := range compiled {
		for _, m := range cr.regex.FindAllStringIndex(payload, -1) {
			hits = append(hits, Hit{RuleID: CustomRuleRuleID, Start: m[0], End: m[1], CustomRuleName: cr.name})
		}
	}
	return hits
}
