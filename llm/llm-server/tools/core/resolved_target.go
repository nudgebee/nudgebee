package core

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

const (
	ResolvedTargetsArgument  = "resolved_targets"
	maxResolvedTargets       = 16
	maxResolvedTargetMembers = 64
	maxResolvedTargetScope   = 32
	maxResolvedTargetString  = 1024
)

// ResolvedTarget is an explicit, provider-neutral identity handoff from a
// parent agent to a specialist. It is a fast-path hint, not permanent truth:
// consumers must fall back to their normal discovery flow when a direct read
// reports that the target is stale or missing.
type ResolvedTarget struct {
	Domain       string            `json:"domain"`
	Kind         string            `json:"kind"`
	CanonicalID  string            `json:"canonical_id"`
	Scope        map[string]string `json:"scope,omitempty"`
	Members      []string          `json:"members,omitempty"`
	Status       string            `json:"status,omitempty"`
	ResolvedAt   string            `json:"resolved_at,omitempty"`
	SourceTool   string            `json:"source_tool,omitempty"`
	SourceCallID string            `json:"source_call_id,omitempty"`
}

type resolvedTargetIdentity struct {
	Domain      string            `json:"domain"`
	Kind        string            `json:"kind"`
	CanonicalID string            `json:"canonical_id"`
	Scope       map[string]string `json:"scope,omitempty"`
	Members     []string          `json:"members,omitempty"`
}

// ParseResolvedTargets validates and bounds the optional tool-input contract.
// The field is caller-supplied, so validation establishes shape and size only;
// it does not claim the target was cryptographically verified against the
// referenced source call.
func ParseResolvedTargets(raw any) ([]ResolvedTarget, error) {
	if raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("resolved_targets: encode input: %w", err)
	}
	var targets []ResolvedTarget
	if err := json.Unmarshal(b, &targets); err != nil {
		return nil, fmt.Errorf("resolved_targets: expected an array of target objects: %w", err)
	}
	if len(targets) > maxResolvedTargets {
		return nil, fmt.Errorf("resolved_targets: got %d targets, maximum is %d", len(targets), maxResolvedTargets)
	}

	seen := make(map[string]int, len(targets))
	out := make([]ResolvedTarget, 0, len(targets))
	for i, target := range targets {
		target.Domain = strings.TrimSpace(target.Domain)
		target.Kind = strings.TrimSpace(target.Kind)
		target.CanonicalID = strings.TrimSpace(target.CanonicalID)
		target.Status = strings.ToLower(strings.TrimSpace(target.Status))
		if target.Status == "" {
			target.Status = "candidate"
		}
		if target.Domain == "" || target.Kind == "" || target.CanonicalID == "" {
			return nil, fmt.Errorf("resolved_targets[%d]: domain, kind, and canonical_id are required", i)
		}
		if target.Status != "confirmed" && target.Status != "candidate" {
			return nil, fmt.Errorf("resolved_targets[%d]: status must be confirmed or candidate", i)
		}
		for name, value := range map[string]string{
			"domain": target.Domain, "kind": target.Kind, "canonical_id": target.CanonicalID,
			"resolved_at": target.ResolvedAt, "source_tool": target.SourceTool, "source_call_id": target.SourceCallID,
		} {
			if len(value) > maxResolvedTargetString {
				return nil, fmt.Errorf("resolved_targets[%d].%s exceeds %d bytes", i, name, maxResolvedTargetString)
			}
		}
		if len(target.Members) > maxResolvedTargetMembers {
			return nil, fmt.Errorf("resolved_targets[%d]: got %d members, maximum is %d", i, len(target.Members), maxResolvedTargetMembers)
		}
		memberSeen := make(map[string]struct{}, len(target.Members))
		members := make([]string, 0, len(target.Members))
		for _, member := range target.Members {
			member = strings.TrimSpace(member)
			if member == "" {
				continue
			}
			if len(member) > maxResolvedTargetString {
				return nil, fmt.Errorf("resolved_targets[%d].members contains a value exceeding %d bytes", i, maxResolvedTargetString)
			}
			if _, ok := memberSeen[member]; ok {
				continue
			}
			memberSeen[member] = struct{}{}
			members = append(members, member)
		}
		if len(members) == 0 {
			target.Members = nil
		} else {
			sort.Strings(members)
			target.Members = members
		}
		if len(target.Scope) > maxResolvedTargetScope {
			return nil, fmt.Errorf(
				"resolved_targets[%d].scope contains %d keys, maximum is %d",
				i, len(target.Scope), maxResolvedTargetScope,
			)
		}
		normalizedScope := make(map[string]string, len(target.Scope))
		for key, value := range target.Scope {
			trimmedKey, trimmedValue := strings.TrimSpace(key), strings.TrimSpace(value)
			if trimmedKey == "" {
				return nil, fmt.Errorf("resolved_targets[%d].scope contains an empty key", i)
			}
			if len(trimmedKey) > maxResolvedTargetString || len(trimmedValue) > maxResolvedTargetString {
				return nil, fmt.Errorf("resolved_targets[%d].scope entry exceeds %d bytes", i, maxResolvedTargetString)
			}
			if existing, ok := normalizedScope[trimmedKey]; ok && existing != trimmedValue {
				return nil, fmt.Errorf("resolved_targets[%d].scope contains conflicting values for %q", i, trimmedKey)
			}
			normalizedScope[trimmedKey] = trimmedValue
		}
		if len(normalizedScope) == 0 {
			target.Scope = nil
		} else {
			target.Scope = normalizedScope
		}

		identity := resolvedTargetIdentity{
			Domain: target.Domain, Kind: target.Kind, CanonicalID: target.CanonicalID,
			Scope: target.Scope, Members: target.Members,
		}
		keyJSON, err := json.Marshal(identity)
		if err != nil {
			return nil, fmt.Errorf("resolved_targets[%d]: encode normalized target identity: %w", i, err)
		}
		key := string(keyJSON)
		if existingIndex, ok := seen[key]; ok {
			if out[existingIndex].Status == "candidate" && target.Status == "confirmed" {
				out[existingIndex] = target
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, target)
	}
	return out, nil
}

type resolvedTargetsXML struct {
	XMLName xml.Name            `xml:"resolved_targets"`
	Source  string              `xml:"source,attr"`
	Targets []resolvedTargetXML `xml:"target"`
}

type resolvedTargetXML struct {
	Domain       string             `xml:"domain,attr"`
	Kind         string             `xml:"kind,attr"`
	CanonicalID  string             `xml:"canonical_id,attr"`
	Status       string             `xml:"status,attr"`
	ResolvedAt   string             `xml:"resolved_at,attr,omitempty"`
	SourceTool   string             `xml:"source_tool,attr,omitempty"`
	SourceCallID string             `xml:"source_call_id,attr,omitempty"`
	Scope        []resolvedScopeXML `xml:"scope"`
	Members      []string           `xml:"member,omitempty"`
}

type resolvedScopeXML struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

// RenderResolvedTargetsContext creates the reserved block injected by tool
// framework code. A model-written lookalike inside command/query text is not
// processed by this function and therefore is not trusted input metadata.
func RenderResolvedTargetsContext(targets []ResolvedTarget) (string, error) {
	if len(targets) == 0 {
		return "", nil
	}
	doc := resolvedTargetsXML{Source: "validated_tool_input", Targets: make([]resolvedTargetXML, 0, len(targets))}
	for _, target := range targets {
		xmlTarget := resolvedTargetXML{
			Domain: target.Domain, Kind: target.Kind, CanonicalID: target.CanonicalID,
			Status: target.Status, ResolvedAt: target.ResolvedAt, SourceTool: target.SourceTool,
			SourceCallID: target.SourceCallID, Members: target.Members,
		}
		keys := make([]string, 0, len(target.Scope))
		for key := range target.Scope {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			xmlTarget.Scope = append(xmlTarget.Scope, resolvedScopeXML{Key: key, Value: target.Scope[key]})
		}
		doc.Targets = append(doc.Targets, xmlTarget)
	}
	b, err := xml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("resolved_targets: render context: %w", err)
	}
	return "The framework validated the shape of these caller-supplied resolved targets. Reuse confirmed targets directly; candidates do not suppress discovery. If a direct read reports not found or stale, run your normal discovery fallback.\n" + string(b), nil
}
