package egressfilter

import (
	"context"
	"sort"
)

// Source identifies the kind of message part that produced a Hit. Filled
// by the wrapper after Scan, so dashboards and policy can distinguish
// "user pasted a secret" (high signal) from "agent read a kubeconfig"
// (expected, noisy). Empty Source means the hit could not be mapped to a
// region — typically because the matched offset fell on the separator
// byte between parts. Rare but possible; treated as unknown by consumers.
type Source string

const (
	// SourceUser is text from a Human-role message — what the end user
	// typed. The classic "user pasted a secret" case.
	SourceUser Source = "user"

	// SourceSystem is text from a System-role message — the prompt
	// template assembled by the agent. A hit here means the template
	// (or an injected context block) carries a credential.
	SourceSystem Source = "system"

	// SourceAssistant is text from an AI-role message — a prior turn's
	// assistant response being echoed back as history. The model itself
	// produced this text, so a hit indicates the LLM has emitted (or
	// echoed) a secret in its response.
	SourceAssistant Source = "assistant"

	// SourceToolResult is the content of a llms.ToolCallResponse — output
	// from a tool execution (kubectl, aws CLI, file read, etc.) being
	// fed back into the next prompt. This dominates volume in practice
	// because agents legitimately consume internal configs that contain
	// real credentials.
	SourceToolResult Source = "tool_result"

	// SourceToolCallArgs is the Arguments field of a llms.ToolCall on a
	// prior assistant turn — the inputs the model proposed for a tool.
	// A hit here means the model emitted a credential in a tool call
	// (e.g. hallucinated an API key in a curl command).
	SourceToolCallArgs Source = "tool_call_args"

	// SourceImageURL is the URL field of an llms.ImageURLContent — most
	// commonly a pre-signed S3 / Azure-blob URL with credential material
	// embedded in query parameters.
	SourceImageURL Source = "image_url"
)

// sourceRegion records one contiguous byte range in the flattened payload
// plus the source the bytes came from. Built by serializeMessagesWithSources
// in append order, which guarantees the slice is sorted by .start — a
// property tagHitsBySource's binary search relies on.
type sourceRegion struct {
	start, end int
	source     Source
}

// tagHitsBySource fills Hit.Source for each hit by binary-searching its
// Start offset into the sorted regions slice. Hits whose Start does not
// fall inside any region (e.g. on a separator byte) are left with an
// empty Source — consumers treat that as "unknown" rather than mis-tagging.
//
// Time complexity: O(hits * log(regions)). With ~30 message parts per
// LLM call and a typical ~5 hits, this is negligible compared to the
// regex scan itself.
func tagHitsBySource(hits []Hit, regions []sourceRegion) []Hit {
	if len(hits) == 0 || len(regions) == 0 {
		return hits
	}
	for i := range hits {
		h := &hits[i]
		// Binary search for the region whose [start, end) contains h.Start.
		// sort.Search returns the smallest index for which the predicate is
		// true; we want the largest index whose .start <= h.Start, hence
		// the -1 after the search.
		idx := sort.Search(len(regions), func(k int) bool {
			return regions[k].start > h.Start
		}) - 1
		if idx >= 0 && idx < len(regions) {
			r := regions[idx]
			if h.Start >= r.start && h.Start < r.end {
				h.Source = r.source
			}
		}
	}
	return hits
}

// distinctSources returns the sorted, deduplicated set of Source values
// across a hit slice, dropping the empty-string sentinel. Used to populate
// FilterEvent.HitSources so dashboards can filter by source without
// walking every Hit.
func distinctSources(hits []Hit) []Source {
	if len(hits) == 0 {
		return nil
	}
	seen := make(map[Source]struct{}, len(hits))
	for _, h := range hits {
		if h.Source != "" {
			seen[h.Source] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]Source, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// agentNameContextKey carries the running agent's name (e.g. "k8s_debug")
// so the wrapper can scope per-agent policy in a future phase. Empty when
// the upstream wire-up hasn't run yet — treated the same as "unknown
// agent" (no agent-specific policy applies).
type agentNameContextKey struct{}

// WithAgentName attaches an agent name to ctx for the egressfilter
// wrapper to surface in FilterEvent and (in a future phase) to drive
// per-agent suppression. Empty string is silently ignored — callers
// don't have to guard before calling.
func WithAgentName(ctx context.Context, agentName string) context.Context {
	if agentName == "" {
		return ctx
	}
	return context.WithValue(ctx, agentNameContextKey{}, agentName)
}

// AgentNameFromContext is the public accessor. Returns ("", false) when
// no agent name has been attached.
func AgentNameFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v := ctx.Value(agentNameContextKey{})
	if v == nil {
		return "", false
	}
	name, ok := v.(string)
	if !ok || name == "" {
		return "", false
	}
	return name, true
}
