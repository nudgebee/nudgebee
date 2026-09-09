package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// These tests guard a load-bearing and non-obvious coupling.
//
// Google AI rejects a request that sends BOTH a CachedContent handle and tool
// declarations ("CachedContent can not be used with ... tools"), so
// googleai.go skips converting opts.Tools whenever cfg.CachedContent is set —
// the cache is expected to supply the declarations that were baked in at
// CreateCachedContent time.
//
// That skip is only safe because the tools participate in the cache's content
// hash. If they ever stop doing so, an entry created WITHOUT tools could be
// served to a request that needs them: the handle suppresses the tool
// declarations, the model receives no functions, and the agent silently loses
// the ability to call any tool — with no error anywhere. These tests exist so
// that regression fails here instead of in production.

func toolFor(name string, params any) llms.Tool {
	return llms.Tool{
		Type:     "function",
		Function: &llms.FunctionDefinition{Name: name, Parameters: params},
	}
}

func msgsFor(text string) []llms.MessageContent {
	return []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: text}}},
	}
}

// THE guard: identical messages must not collide across tool-bearing and
// tool-less requests.
func TestHashContent_ToolsChangeTheHash(t *testing.T) {
	msgs := msgsFor("you are an SRE assistant")

	withoutTools := hashContent(msgs, nil)
	withTools := hashContent(msgs, []llms.Tool{toolFor("kubectl_execute", map[string]any{"type": "object"})})

	assert.NotEqual(t, withoutTools, withTools,
		"a tool-less cache entry must never be served to a tool-bearing request: "+
			"googleai.go suppresses tool declarations when CachedContent is set, so a "+
			"hash collision here silently strips every tool from the agent")
}

// Different tool sets must not share a cache slot either — otherwise an agent
// could be served a cache baked with another agent's tools.
func TestHashContent_DifferentToolSetsDiffer(t *testing.T) {
	msgs := msgsFor("same system prompt")

	a := hashContent(msgs, []llms.Tool{toolFor("kubectl_execute", map[string]any{"type": "object"})})
	b := hashContent(msgs, []llms.Tool{toolFor("prometheus_execute", map[string]any{"type": "object"})})
	assert.NotEqual(t, a, b, "different tool NAMES must produce different hashes")

	// Same name, different schema — a tool whose parameters changed must not
	// reuse a cache entry baked with the old schema.
	c := hashContent(msgs, []llms.Tool{toolFor("kubectl_execute", map[string]any{"type": "object", "required": []string{"command"}})})
	assert.NotEqual(t, a, c, "different tool SCHEMAS must produce different hashes")
}

func TestHashContent_ToolDescriptionChangesTheHash(t *testing.T) {
	msgs := msgsFor("same system prompt")
	params := map[string]any{"type": "object"}
	toolWithDescription := func(description string) llms.Tool {
		return llms.Tool{
			Type: "function",
			Function: &llms.FunctionDefinition{
				Name:        "kubectl_execute",
				Description: description,
				Parameters:  params,
			},
		}
	}

	a := hashContent(msgs, []llms.Tool{toolWithDescription("Read Kubernetes resources")})
	b := hashContent(msgs, []llms.Tool{toolWithDescription("Write Kubernetes resources")})
	assert.NotEqual(t, a, b,
		"tool descriptions are baked into Google cached content, so changed guidance must invalidate the old entry")
}

// Back-compat: entries that never had tools must keep the hash they had before
// tools joined the computation, so existing cached content is not invalidated.
// nil and empty must therefore be indistinguishable.
func TestHashContent_NilAndEmptyToolsAreEquivalent(t *testing.T) {
	msgs := msgsFor("legacy tool-less prompt")
	assert.Equal(t, hashContent(msgs, nil), hashContent(msgs, []llms.Tool{}),
		"an empty tool list must hash identically to no tool list, or every "+
			"pre-existing tool-less cache entry is invalidated on deploy")
}

func TestHashContent_StableForIdenticalInput(t *testing.T) {
	msgs := msgsFor("stable prompt")
	tools := []llms.Tool{toolFor("kubectl_execute", map[string]any{"type": "object"})}
	assert.Equal(t, hashContent(msgs, tools), hashContent(msgs, tools),
		"hashing must be deterministic or every request is a cache miss")
}

// A malformed tool (nil Function) is skipped rather than panicking — it reaches
// this path from provider-agnostic conversion code.
func TestHashContent_NilFunctionToolIsSkipped(t *testing.T) {
	msgs := msgsFor("prompt")
	assert.NotPanics(t, func() {
		_ = hashContent(msgs, []llms.Tool{{Type: "function", Function: nil}})
	})
	assert.Equal(t, hashContent(msgs, nil), hashContent(msgs, []llms.Tool{{Type: "function", Function: nil}}),
		"a tool carrying no function definition contributes nothing to the hash")
}

// Messages must still matter: tools are mixed IN, not substituted for content.
func TestHashContent_MessagesStillAffectHash(t *testing.T) {
	tools := []llms.Tool{toolFor("kubectl_execute", map[string]any{"type": "object"})}
	assert.NotEqual(t, hashContent(msgsFor("prompt A"), tools), hashContent(msgsFor("prompt B"), tools))
}

// hashContent renders tool Parameters with %v, which raises a fair question: Go
// map iteration is randomized, so does the same tool set hash differently run to
// run and quietly destroy every cache key?
//
// It does not. Since Go 1.12 fmt prints maps in key-sorted order, recursively —
// verified here rather than assumed, because the failure would be invisible
// (silent cache misses and a cost regression, no error anywhere). This test
// builds structurally identical schemas as SEPARATE map instances with keys
// declared out of order, so a regression to unsorted formatting fails here.
func TestHashContent_ToolParametersHashDeterministicallyAcrossInstances(t *testing.T) {
	mkTools := func() []llms.Tool {
		return []llms.Tool{toolFor("kubectl_execute", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"zeta":    map[string]any{"type": "string"},
				"command": map[string]any{"type": "string", "description": "the command"},
				"alpha":   map[string]any{"type": "number"},
				"mid":     map[string]any{"type": "boolean"},
			},
			"required": []string{"command"},
		})}
	}
	msgs := msgsFor("system prompt")

	first := hashContent(msgs, mkTools())
	for i := 0; i < 50; i++ {
		assert.Equal(t, first, hashContent(msgs, mkTools()),
			"identical schemas built as separate map instances must hash identically, "+
				"or every deploy silently invalidates the tool-bearing cache")
	}
}
