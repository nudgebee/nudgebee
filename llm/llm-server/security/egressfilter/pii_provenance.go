package egressfilter

import "strings"

// kbBlockOpen / kbBlockClose delimit the KB pre-step's retrieved-knowledge
// region inside the dynamic human message (see agents/core/kb_prestep.go).
// Matching on the literal tags rather than re-plumbing the retrieval path
// keeps provenance a read-only concern of the scrub layer.
const (
	kbBlockOpen  = "<retrieved_knowledge>"
	kbBlockClose = "</retrieved_knowledge>"
	kbSourceTag  = "\nSource: "
	// kbDocSeparator delimits documents inside the block — kb_prestep writes
	// "\n" then "---\n" before every document after the first.
	//
	// Caveat: a markdown/YAML "---" inside a document body splits it for
	// attribution purposes. That degrades to an empty doc_url (the real
	// Source tag falls outside the computed bounds), never to a wrong one, so
	// the "never a wrong origin" property holds — but real-world doc_url
	// coverage is lower than the tests alone suggest.
	kbDocSeparator = "\n---\n"
)

// PIIPiece is one scrubbable unit of an outbound payload plus where it came
// from. The EE wrapper builds these while walking the message tree; keeping
// the type here lets the provenance logic and its tests live in OSS next to
// the event it populates.
type PIIPiece struct {
	Text   string
	Source Source
	Tool   string
}

// BuildPIIProvenance attributes each scrubbed value to the piece it came from,
// keyed by raw value for PIIValueAccumulator.AddWithProvenance.
//
// Attribution is by substring search over the ORIGINAL piece text rather than
// by offset bookkeeping through the scrubber. That choice is deliberate: it
// works identically for the in-process Go path and the ml-k8s-server /scrub
// HTTP path, which return the same {token: value} mapping and nothing
// positional. NER categories (PERSON, LOCATION) therefore get provenance for
// free if the backend is ever switched to http.
//
// First matching piece wins — the same value appearing in several pieces is
// one distinct value, and the accumulator dedups it that way too.
func BuildPIIProvenance(pieces []PIIPiece, mapping map[string]string) map[string]PIIValueDetail {
	if len(pieces) == 0 || len(mapping) == 0 {
		return nil
	}
	out := make(map[string]PIIValueDetail, len(mapping))
	for _, value := range mapping {
		if value == "" {
			continue
		}
		for _, p := range pieces {
			idx := strings.Index(p.Text, value)
			if idx < 0 {
				continue
			}
			d := PIIValueDetail{Source: p.Source, Tool: p.Tool}
			if origin, docURL, ok := kbOriginAt(p.Text, idx); ok {
				d.Origin = origin
				d.DocURL = docURL
			}
			out[value] = d
			break
		}
	}
	return out
}

// kbOriginAt reports whether the byte offset sits inside a
// <retrieved_knowledge> block and, if so, which retrieved document it belongs
// to.
//
// The owning document is bounded by the pre-step's "\n---\n" separators, NOT
// by the next Source line. That distinction is load-bearing: kb_prestep writes
// "\nSource: <url>" only for documents whose metadata carries a url, so a
// doc without one is followed by the NEXT document's Source tag. Searching
// forward for the first Source tag therefore misattributes to the wrong page —
// observed on a real payload where an untitled incident report was credited to
// the docs.nudgebee.com article that happened to follow it.
func kbOriginAt(text string, idx int) (origin, docURL string, ok bool) {
	open := strings.LastIndex(text[:idx], kbBlockOpen)
	if open < 0 {
		return "", "", false
	}
	// A close tag between the block start and the value means we are past
	// that block, not inside it.
	if closed := strings.Index(text[open:idx], kbBlockClose); closed >= 0 {
		return "", "", false
	}

	// Bound the search to the enclosing document.
	docStart := open + len(kbBlockOpen)
	if sep := strings.LastIndex(text[docStart:idx], kbDocSeparator); sep >= 0 {
		docStart += sep + len(kbDocSeparator)
	}
	docEnd := len(text)
	if end := strings.Index(text[idx:], kbBlockClose); end >= 0 {
		docEnd = idx + end
	}
	if sep := strings.Index(text[idx:docEnd], kbDocSeparator); sep >= 0 {
		docEnd = idx + sep
	}

	// LastIndex, not Index: kb_prestep APPENDS "\nSource: <url>" after the
	// document body, so the real metadata tag is the last one in the region.
	// Runbooks and incident reports routinely cite their own sources inline,
	// and Index would return that cited url instead of the KB document's.
	// (Gemini review on PR #35859.)
	src := strings.LastIndex(text[docStart:docEnd], kbSourceTag)
	if src < 0 {
		// Inside the block but this document carried no url — still worth
		// knowing it arrived via RAG rather than from live infrastructure.
		return OriginKnowledgeBase, "", true
	}
	// Residual limitation: a document with NO metadata url whose body happens
	// to contain a "Source:" line still yields that inline url. The text alone
	// cannot distinguish the two, and Origin is correct either way.
	// Bound by docEnd, not just the next newline: kb_prestep always writes a
	// newline after the url today, but a url ending flush against the closing
	// tag or the next document would otherwise swallow it. (Gemini review.)
	rawURL := text[docStart+src+len(kbSourceTag) : docEnd]
	if nl := strings.IndexByte(rawURL, '\n'); nl >= 0 {
		rawURL = rawURL[:nl]
	}
	return OriginKnowledgeBase, sanitizeDocURL(rawURL), true
}

// maxDocURLLen bounds a stored url. Real KB urls are Confluence/docs links
// well under this; anything longer is not a url we should be persisting.
const maxDocURLLen = 512

// sanitizeDocURL returns u only when it is an http(s) url of sane length, and
// "" otherwise.
//
// Whatever follows "\nSource: " is arbitrary document text, not a validated
// field. A runbook pasting a log excerpt — `Source: 10.0.1.5
// db_password=hunter2` — would otherwise write that string verbatim into
// llm_conversation_messages.metadata with reveal OFF, which is the exact
// at-rest exposure this package exists to prevent. The frontend's
// isSafeHttpUrl guard hides such a value from the modal but does NOT stop the
// write, so filtering has to happen here, at the point of capture.
func sanitizeDocURL(u string) string {
	u = strings.TrimSpace(u)
	if idx := strings.IndexAny(u, " \t\r\n"); idx >= 0 {
		u = u[:idx]
	}
	if len(u) > maxDocURLLen {
		return ""
	}
	// Schemes are case-insensitive (RFC 3986 §3.1) -> compare lowered.
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return ""
	}
	return u
}
