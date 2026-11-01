package egressfilter

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// RedactToolOutput replaces credential values in a tool's output with
// [REDACTED:<rule_id>] before the output is stored or shown to the model.
// Tool output often carries credentials the agent never asked for: a pod's
// environment, a log line that printed a connection string, a manifest with
// an inline token. Once a credential is in the model's context it can be
// repeated, logged again and used in later commands.
//
// It is governed by the egress filter settings, but not by its mode: output is
// redacted whenever the secrets scanner is on for the tenant (master and
// secrets switches, LLM_SERVER_EGRESSFILTER_TOOL_OUTPUT_REDACT_ENABLED, the
// tenant row's enabled flag and disabled_agents), and the allowlist and
// disabled_rules apply. detect/enforce/redact keep governing the outbound
// request only. Only the value is replaced; names, hosts and quotes stay, so
// the rest of the output stays useful. The rules are the tool-output set in
// tool_output_rules.go, not the outbound registry.
//
// agent is the running agent's name ("" when unknown), tool the tool's name.
func RedactToolOutput(rc *security.RequestContext, agent, tool, text string) string {
	if text == "" || !toolOutputRedactionEnabled() {
		return text
	}
	ctx := rc.GetContext()
	if rc != nil {
		if sc := rc.GetSecurityContext(); sc != nil {
			if id, err := uuid.Parse(sc.GetTenantId()); err == nil {
				ctx = WithTenantID(ctx, id)
			}
		}
	}
	if agent != "" {
		ctx = WithAgentName(ctx, agent)
	}
	return redactToolOutput(ctx, tool, text)
}

func toolOutputRedactionEnabled() bool {
	// Read the three fields, not a copy of the whole config: a copy reads
	// every field and races with code that sets unrelated ones.
	return config.Config.LlmServerEgressFilterEnabled && config.Config.LlmServerEgressFilterSecretsEnabled &&
		config.Config.LlmServerEgressFilterToolOutputRedactEnabled
}

// toolOutputProvider is the provider and model label on tool-output scan
// metrics, which share the outbound instruments. The tool name is not a label
// (tenant-defined tools would make it unbounded); it is in the log line.
const toolOutputProvider = "tool_output"

func redactToolOutput(ctx context.Context, tool, text string) string {
	tcfg := Resolve(ctx)
	if tcfg != nil && !tcfg.Enabled {
		return text
	}
	agent, _ := AgentNameFromContext(ctx)
	if agent != "" && tcfg.AgentExcluded(agent) {
		return text
	}

	start := time.Now()
	hits := toolOutputHits(text)
	if len(hits) == 0 {
		return text
	}
	for i := range hits {
		hits[i].Source = SourceToolResult
		hits[i].Tool = tool
	}
	hits = applyAllowlist(text, hits)
	hits = applyTenantOverrides(Result{Hits: hits}, text, tcfg).Hits
	if len(hits) == 0 {
		return text
	}

	var sb strings.Builder
	sb.Grow(len(text))
	last := 0
	for _, h := range hits {
		sb.WriteString(text[last:h.Start])
		sb.WriteString(toolOutputPlaceholder(h.RuleID))
		last = h.End
	}
	sb.WriteString(text[last:])

	result := Result{Hits: hits}
	auditID := newAuditID()
	recordScan(ctx, toolOutputProvider, toolOutputProvider, ModeRedact, "redacted", len(text), time.Since(start).Seconds(), hits)
	slog.Info("egressfilter: tool output redacted",
		"audit_id", auditID,
		"tool", tool,
		"agent", agent,
		"rule_ids", result.RuleIDs(),
		"hits", len(hits),
		"payload_bytes", len(text),
	)
	if fn := reporterFromContext(ctx); fn != nil {
		reported := annotateHits(ctx, hits, text)
		for i := range reported {
			// The point is to keep the value out of storage, so the
			// testing-only reveal flag does not apply here.
			reported[i].Value = ""
		}
		event := newFilterEvent(auditID, ModeRedact, len(text), Result{Hits: reported}, agent)
		// Built from the event's own (possibly capped) hits so entry i is the
		// placeholder of Hits[i].
		event.Redactions = make([]Redaction, len(event.Hits))
		for i, h := range event.Hits {
			event.Redactions[i] = Redaction{Placeholder: toolOutputPlaceholder(h.RuleID)}
		}
		fn(event)
	}
	return sb.String()
}

func toolOutputPlaceholder(ruleID string) string { return "[REDACTED:" + ruleID + "]" }

// toolOutputHits finds credential values in tool output and returns them in
// order, overlaps merged. Each hit covers the value only.
func toolOutputHits(text string) []Hit {
	var hits []Hit
	// A private key block spans lines, so it is matched before the text is
	// split into segments.
	if strings.Contains(text, "PRIVATE KEY") {
		for _, m := range privateKeyBlock.FindAllStringIndex(text, -1) {
			hits = append(hits, Hit{RuleID: RuleIDToolPrivateKey, Start: m[0], End: m[1]})
		}
	}
	segments := splitRedactionSegments(text)
	offset := 0
	for i, seg := range segments {
		if seg.text != "" {
			hits = appendSegmentHits(hits, seg.text, offset)
			// The Kubernetes env rule spans two lines: a `name:` segment and
			// the `value:` segment after it. Only segments split at a newline
			// pair.
			if sep := seg.sep; (sep == "\n" || sep == `\n`) && i+1 < len(segments) {
				lower := asciiLower(seg.text)
				if strings.Contains(lower, "name:") && containsAny(lower, secretNameWords) {
					hits = appendAssignmentHits(hits, k8sEnvSecretValue, seg.text+sep+segments[i+1].text, offset, RuleIDK8sEnvSecret)
				}
			}
		}
		offset += len(seg.text) + len(seg.sep)
	}
	return mergeHits(hits)
}

// mergeHits sorts hits by position and joins overlapping ones, keeping the
// first rule id, so each byte is replaced once.
func mergeHits(hits []Hit) []Hit {
	if len(hits) < 2 {
		return hits
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Start < hits[j].Start })
	merged := hits[:1]
	for _, h := range hits[1:] {
		last := &merged[len(merged)-1]
		if h.Start < last.End {
			last.End = max(last.End, h.End)
			continue
		}
		merged = append(merged, h)
	}
	return merged
}
