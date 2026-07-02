package egressfilter

import (
	"sort"

	"github.com/tmc/langchaingo/llms"
)

// Helpers exported for the Redactor plugin surface. Anyone implementing a
// custom redactor against a Redactor registration can compose against
// these to keep the caller-non-mutation contract without duplicating
// the langchaingo part-type switches.

// FindRegionIdx returns the index of the region containing offset, or -1
// when no region contains it. Regions are expected sorted ascending by
// Start (which is guaranteed for slices produced by
// serializeMessagesWithSources).
func FindRegionIdx(offset int, regions []SourceRegion) int {
	idx := sort.Search(len(regions), func(k int) bool {
		return regions[k].Start > offset
	}) - 1
	if idx < 0 || idx >= len(regions) {
		return -1
	}
	r := regions[idx]
	if offset >= r.Start && offset < r.End {
		return idx
	}
	return -1
}

// CopyMessages returns a shallow copy of the messages structure that is
// safe to mutate at the Parts[i] level without touching the caller's
// slice. Individual parts (including pointer parts) still need to be
// cloned via SetPartText before their fields are rewritten — otherwise
// pointer aliasing would leak mutations back to the caller.
func CopyMessages(orig []llms.MessageContent) []llms.MessageContent {
	if len(orig) == 0 {
		return orig
	}
	out := make([]llms.MessageContent, len(orig))
	for i, m := range orig {
		parts := make([]llms.ContentPart, len(m.Parts))
		copy(parts, m.Parts)
		out[i] = llms.MessageContent{Role: m.Role, Parts: parts}
	}
	return out
}

// ExtractPartText pulls the mutable text-like field out of a ContentPart
// so a Redactor can compute new content. Returns ("", false) for
// unrecognised or nil parts.
//
// Supported part types (value + pointer form each):
//   - llms.TextContent (Text)
//   - llms.ToolCall (FunctionCall.Arguments)
//   - llms.ToolCallResponse (Content)
//   - llms.ImageURLContent (URL)
func ExtractPartText(p llms.ContentPart) (string, bool) {
	switch part := p.(type) {
	case llms.TextContent:
		return part.Text, true
	case *llms.TextContent:
		if part != nil {
			return part.Text, true
		}
	case llms.ToolCall:
		if part.FunctionCall != nil {
			return part.FunctionCall.Arguments, true
		}
	case *llms.ToolCall:
		if part != nil && part.FunctionCall != nil {
			return part.FunctionCall.Arguments, true
		}
	case llms.ToolCallResponse:
		return part.Content, true
	case *llms.ToolCallResponse:
		if part != nil {
			return part.Content, true
		}
	case llms.ImageURLContent:
		return part.URL, true
	case *llms.ImageURLContent:
		if part != nil {
			return part.URL, true
		}
	}
	return "", false
}

// SetPartText returns a new ContentPart with the mutable text-like field
// set to text. Value-type parts are returned as a new value struct;
// pointer parts are shallow-cloned first so the caller's referenced
// struct is not modified. Unrecognised parts are returned unchanged —
// callers should check ExtractPartText first, so this should not be
// reached for those.
func SetPartText(p llms.ContentPart, text string) llms.ContentPart {
	switch part := p.(type) {
	case llms.TextContent:
		return llms.TextContent{Text: text}
	case *llms.TextContent:
		if part == nil {
			return p
		}
		cp := *part
		cp.Text = text
		return &cp
	case llms.ToolCall:
		if part.FunctionCall == nil {
			return p
		}
		fc := *part.FunctionCall
		fc.Arguments = text
		return llms.ToolCall{ID: part.ID, Type: part.Type, FunctionCall: &fc}
	case *llms.ToolCall:
		if part == nil || part.FunctionCall == nil {
			return p
		}
		cp := *part
		fc := *part.FunctionCall
		fc.Arguments = text
		cp.FunctionCall = &fc
		return &cp
	case llms.ToolCallResponse:
		return llms.ToolCallResponse{ToolCallID: part.ToolCallID, Name: part.Name, Content: text}
	case *llms.ToolCallResponse:
		if part == nil {
			return p
		}
		cp := *part
		cp.Content = text
		return &cp
	case llms.ImageURLContent:
		return llms.ImageURLContent{URL: text, Detail: part.Detail}
	case *llms.ImageURLContent:
		if part == nil {
			return p
		}
		cp := *part
		cp.URL = text
		return &cp
	}
	return p
}
