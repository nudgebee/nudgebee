package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResolvedTargets_ValidatesBoundsAndDeduplicates(t *testing.T) {
	raw := []any{
		map[string]any{
			"domain": " kubernetes ", "kind": "Deployment", "canonical_id": "checkout",
			"scope":   map[string]any{" namespace ": " prod "},
			"members": []any{"checkout-7d9f6c8b5-x2abc", "checkout-7d9f6c8b5-x2abc"},
		},
		map[string]any{
			"domain": "kubernetes", "kind": "Deployment", "canonical_id": "checkout", "status": "candidate",
			"scope":   map[string]any{"namespace": "prod"},
			"members": []any{"checkout-7d9f6c8b5-x2abc"},
		},
	}

	targets, err := ParseResolvedTargets(raw)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "candidate", targets[0].Status)
	assert.Equal(t, map[string]string{"namespace": "prod"}, targets[0].Scope)
	assert.Equal(t, []string{"checkout-7d9f6c8b5-x2abc"}, targets[0].Members)
}

func TestParseResolvedTargets_RejectsInvalidIdentityAndStatus(t *testing.T) {
	_, err := ParseResolvedTargets([]any{map[string]any{"domain": "aws", "kind": "lambda"}})
	assert.ErrorContains(t, err, "canonical_id are required")

	_, err = ParseResolvedTargets([]any{map[string]any{
		"domain": "aws", "kind": "lambda", "canonical_id": "arn:x", "status": "trusted",
	}})
	assert.ErrorContains(t, err, "confirmed or candidate")

	_, err = ParseResolvedTargets([]any{map[string]any{
		"domain": "kubernetes", "kind": "pod", "canonical_id": "checkout-abc",
		"scope": map[string]any{" namespace ": "prod", "namespace": "dev"},
	}})
	assert.ErrorContains(t, err, `conflicting values for "namespace"`)

	tooManyScopeKeys := make(map[string]any, maxResolvedTargetScope+1)
	for i := 0; i <= maxResolvedTargetScope; i++ {
		tooManyScopeKeys[fmt.Sprintf("key-%d", i)] = "value"
	}
	_, err = ParseResolvedTargets([]any{map[string]any{
		"domain": "aws", "kind": "lambda", "canonical_id": "arn:x", "scope": tooManyScopeKeys,
	}})
	assert.ErrorContains(t, err, "scope contains 33 keys, maximum is 32")
}

func TestParseResolvedTargets_DedupKeyCannotCollideOnInputDelimiters(t *testing.T) {
	targets, err := ParseResolvedTargets([]any{
		map[string]any{"domain": "a\x00b", "kind": "c", "canonical_id": "id"},
		map[string]any{"domain": "a", "kind": "b\x00c", "canonical_id": "id"},
	})
	require.NoError(t, err)
	require.Len(t, targets, 2)
	assert.Nil(t, targets[0].Scope)
	assert.Nil(t, targets[1].Scope)
	assert.Nil(t, targets[0].Members)
	assert.Nil(t, targets[1].Members)
}

func TestParseResolvedTargets_DeduplicatesIdentityAndPrefersConfirmed(t *testing.T) {
	targets, err := ParseResolvedTargets([]any{
		map[string]any{
			"domain": "kubernetes", "kind": "Deployment", "canonical_id": "checkout", "status": "candidate",
			"members": []any{"pod-b", "pod-a"}, "source_call_id": "candidate-call",
		},
		map[string]any{
			"domain": "kubernetes", "kind": "Deployment", "canonical_id": "checkout", "status": "confirmed",
			"members": []any{"pod-a", "pod-b"}, "source_call_id": "confirmed-call",
		},
	})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "confirmed", targets[0].Status)
	assert.Equal(t, "confirmed-call", targets[0].SourceCallID)
	assert.Equal(t, []string{"pod-a", "pod-b"}, targets[0].Members)
}

func TestRenderResolvedTargetsContext_EscapesAndSortsScope(t *testing.T) {
	block, err := RenderResolvedTargetsContext([]ResolvedTarget{{
		Domain: "aws", Kind: "lambda", CanonicalID: `arn:aws:lambda:<unsafe>&`, Status: "confirmed",
		Scope: map[string]string{"region": "us-east-1", "account_id": "123"},
	}})
	require.NoError(t, err)
	assert.Contains(t, block, `<resolved_targets source="validated_tool_input">`)
	assert.Contains(t, block, `canonical_id="arn:aws:lambda:&lt;unsafe&gt;&amp;"`)
	assert.Less(t, strings.Index(block, `key="account_id"`), strings.Index(block, `key="region"`))
}
