package proxy

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/maximhq/bifrost/core/providers/openai"
)

// knownChatKeys is the set of top-level JSON keys the OpenAI chat request schema
// already consumes (model, messages, and every schemas.ChatParameters field). It is
// derived from the struct tags via reflection so it tracks whatever bifrost-core
// models, rather than a hand-maintained list that silently rots on a core bump.
//
// Anything a client sends OUTSIDE this set is a provider-specific extra (e.g. vLLM's
// chat_template_kwargs, or an OpenRouter/vendor knob) that the unified schema would
// otherwise drop on the floor. applyChatExtraParams captures those so the
// passthrough-capable lanes (vLLM / custom OpenAI-compatible endpoints) forward them
// to the upstream instead.
var knownChatKeys = buildKnownChatKeys()

func buildKnownChatKeys() map[string]bool {
	keys := map[string]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported: never serialized, skip
				continue
			}
			// Lookup (not Get) so we can tell "no json tag at all" from an options-only tag
			// like `json:",omitempty"` — the two differ for anonymous fields below.
			tag, hasTag := f.Tag.Lookup("json")
			var name string
			if tag != "" {
				name = strings.Split(tag, ",")[0]
			}
			// An anonymous field (schemas.ChatParameters) serializes inline ONLY when it
			// carries NO json tag at all AND is a struct; a tag (even options-only), or a
			// non-struct named type, makes Go treat it as a named key (under its type
			// name), so recurse only for the untagged-struct case and fall through otherwise.
			if f.Anonymous && !hasTag {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			// No tag name (untagged, or options-only like `json:",omitempty"`) → Go derives
			// the JSON key from the Go field name. Mirror that so such a field is recognized
			// as known, not captured as a duplicate extra. `json:"-"` (name=="-") is skipped.
			if name == "" {
				name = f.Name
			}
			if name != "-" {
				// Lowercase: Go's json matching is case-insensitive, so a client's "Model"
				// parses into the struct AND must be recognized as known here (else it'd be
				// captured as an extra and duplicated upstream). Lookups lowercase to match.
				keys[strings.ToLower(name)] = true
			}
		}
	}
	walk(reflect.TypeFor[openai.OpenAIChatRequest]())
	// The flat reasoning_* shorthands are consumed by ChatParameters.UnmarshalJSON's
	// aux struct, not by a struct tag, so reflection misses them. Add them explicitly,
	// else they'd be treated as unknown and duplicated into ExtraParams alongside the
	// already-parsed `reasoning` object.
	keys["reasoning_effort"] = true
	keys["reasoning_max_tokens"] = true
	keys["reasoning_display"] = true
	return keys
}

// applyChatExtraParams captures any top-level request keys the OpenAI chat schema
// doesn't model and stores them on the request as ExtraParams. ToBifrostChatRequest
// carries these through ChatParameters, and passthrough-capable lanes (vLLM/custom)
// merge them into the upstream call — so provider-specific params like
// chat_template_kwargs survive the generic endpoint instead of being silently
// dropped. Known keys (in particular `model`, which routing may rewrite) are never
// included, so this can never clobber a routed target. A malformed body is left to
// the primary parse to reject; here we simply no-op.
func applyChatExtraParams(req *openai.OpenAIChatRequest, body []byte) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return
	}
	extra := map[string]any{}
	for k, raw := range all {
		if knownChatKeys[strings.ToLower(k)] { // case-insensitive: mirrors Go's json field matching
			continue
		}
		// Decode with UseNumber so a large int64 in an extra (an ID, a timestamp) keeps
		// full precision — a plain unmarshal into `any` would round it through float64.
		// json.Number re-serializes as the exact literal when merged upstream.
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			continue
		}
		extra[k] = v
	}
	if len(extra) > 0 {
		req.SetExtraParams(extra)
	}
}

// sanitizeChatMetadata coerces the OpenAI `metadata` map to string values. The OpenAI spec
// defines metadata as string→string, but the field is typed map[string]any and clients send
// non-string values (llm-server sends {"ThinkingBudget": 4000}). Lenient providers accept it,
// but strict ones — notably Vertex AI's OpenAI-compatible endpoint — reject the whole request
// with "Expected a string key-value pair for 'metadata'". Coercing keeps the data and passes
// strict validation on every provider. The map is mutated in place (it's what the outbound
// request serializes).
func sanitizeChatMetadata(req *openai.OpenAIChatRequest) {
	if req.Metadata == nil {
		return
	}
	for k, v := range *req.Metadata {
		if _, ok := v.(string); ok {
			continue // already a string — leave untouched
		}
		(*req.Metadata)[k] = metaValueToString(v)
	}
}

// metaValueToString renders a metadata value as a string: a number/bool as its literal
// (4000 → "4000", true → "true"), an object/array as compact JSON, nil as "".
func metaValueToString(v any) string {
	if v == nil {
		return ""
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}
