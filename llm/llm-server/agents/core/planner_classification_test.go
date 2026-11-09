package core

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// planWithAnswer runs the classification planner with the LLM scripted to give
// raw on every attempt, and returns the chosen option, the error and the calls.
func planWithAnswer(t *testing.T, multi bool, raw string) (string, [][]llms.MessageContent, error) {
	t.Helper()
	calls := stubGenerate(t, []string{raw, raw, raw}, nil)
	ctx := security.NewRequestContext(context.Background(), security.NewRequestContextForSuperAdmin().GetSecurityContext(), slog.Default(), nil, nil)
	options := []string{"cart-service", "checkout-api", "Payment-Gateway", "Not Found"}
	p, err := NewClassificationPlanner(ctx, NBAgentRequest{AccountId: "test-account"}, mockPlainAgent{name: "subject_test"}, "system", options, multi)
	require.NoError(t, err)
	_, finish, err := p.Plan(context.Background(), nil, "alert")
	if err != nil {
		return "", *calls, err
	}
	return finish.Data, *calls, nil
}

// TestClassificationPlanner_Answers covers the answer canonicalization for both
// selection modes. Multi-select must widen what the model may report (several
// options) without widening what it may invent (anything off the list).
func TestClassificationPlanner_Answers(t *testing.T) {
	t.Run("single-select", func(t *testing.T) {
		tests := []struct {
			name string
			raw  string
			want string // "" means the run fails
		}{
			{"exact option", "cart-service", "cart-service"},
			{"surrounding whitespace is trimmed", "  cart-service\n", "cart-service"},
			{"casing is canonicalized to the option list", "payment-gateway", "Payment-Gateway"},
			{"the Not Found sentinel is a valid choice", "Not Found", "Not Found"},
			{"invented option is rejected", "billing-service", ""},
			{"prose around the option is rejected", "The answer is cart-service", ""},
			// The whole point of leaving multi off by default: a single-select
			// caller can only consume one name, so it must not silently take the
			// first of several.
			{"multi-option answer is rejected when multi is off", "cart-service, checkout-api", ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, _, err := planWithAnswer(t, false, tt.raw)
				if tt.want == "" {
					require.Error(t, err)
					assert.Contains(t, err.Error(), "invalid option after 3 attempts")
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("multi-select", func(t *testing.T) {
		tests := []struct {
			name string
			raw  string
			want string
		}{
			{"single option still works unchanged", "cart-service", "cart-service"},
			{"two options", "cart-service, checkout-api", "cart-service, checkout-api"},
			{"separator is normalized to comma-space", "cart-service,checkout-api", "cart-service, checkout-api"},
			{"per-token casing is canonicalized", "cart-service, payment-gateway", "cart-service, Payment-Gateway"},
			{"ragged whitespace is tolerated", " cart-service ,   checkout-api ", "cart-service, checkout-api"},
			{"duplicates are collapsed", "cart-service, cart-service", "cart-service"},
			{"trailing comma is ignored", "cart-service,", "cart-service"},
			// Order is the contract: api-server makes the first name the subject.
			{"model's order is preserved, not sorted", "checkout-api, cart-service", "checkout-api, cart-service"},
			// One invented name means the model misread the alert; keeping its
			// other guesses would launder that into a confident-looking answer.
			{"one invented option rejects the whole answer", "cart-service, billing-service", ""},
			{"all-invented answer is rejected", "billing-service, shipping-service", ""},
			{"separators only is rejected", " , , ", ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, _, err := planWithAnswer(t, true, tt.raw)
				if tt.want == "" {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		}
	})
}

// TestClassificationPlanner_MultiInstruction asserts the model is told which
// mode it is in: the single-select wording ("choose exactly one option") would
// actively contradict an agent that wants several names back.
func TestClassificationPlanner_MultiInstruction(t *testing.T) {
	_, calls, err := planWithAnswer(t, false, "cart-service")
	require.NoError(t, err)
	single := calls[0][0].Parts[0].(llms.TextContent).Text
	assert.Contains(t, single, "choose exactly one option")
	assert.NotContains(t, single, "one or more")

	_, calls, err = planWithAnswer(t, true, "cart-service")
	require.NoError(t, err)
	multi := calls[0][0].Parts[0].(llms.TextContent).Text
	assert.Contains(t, multi, "choose one or more options")
	assert.Contains(t, multi, "separated by a comma and a space")
	// Both modes must keep forbidding invented options.
	assert.Contains(t, multi, "Do not make up new options")
}

type cacheScopedAgent struct{ mockPlainAgent }

func (cacheScopedAgent) GetCacheScope() CacheScope { return CacheScopeAccount }

// The planner keeps the agent's tier (stamped by the executor), its declared
// cache scope, the parent agent id and content tracking, as before.
func TestClassificationPlanner_KeepsTierAndCacheScope(t *testing.T) {
	var tier ModelTier
	var scope any
	var agentID string
	var track bool
	old := classificationGenerate
	classificationGenerate = func(ctx *security.RequestContext, _, _, _, _, agentId string, trackContent bool, _ []llms.MessageContent, _ bool, _ ...llms.CallOption) (*llms.ContentResponse, error) {
		tier = modelTierFromContext(ctx)
		scope = ctx.GetContext().Value(ContextKeyCacheScope)
		agentID, track = agentId, trackContent
		return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "Not Found"}}}, nil
	}
	t.Cleanup(func() { classificationGenerate = old })

	goCtx := context.WithValue(context.Background(), ContextKeyModelTier, ModelTierReasoning)
	ctx := security.NewRequestContext(goCtx, security.NewRequestContextForSuperAdmin().GetSecurityContext(), slog.Default(), nil, nil)
	p, err := NewClassificationPlanner(ctx, NBAgentRequest{AccountId: "test-account", AgentId: "child", ParentAgentId: "parent"}, cacheScopedAgent{mockPlainAgent{name: "subject_test"}}, "system", []string{"Not Found"}, false)
	require.NoError(t, err)
	_, finish, err := p.Plan(context.Background(), nil, "alert")
	require.NoError(t, err)
	assert.Equal(t, "Not Found", finish.Data)
	assert.Equal(t, ModelTierReasoning, tier)
	assert.Equal(t, CacheScopeAccount, scope)
	assert.Equal(t, "parent", agentID, "the parent agent id keys LLM config and cost, as before")
	assert.True(t, track)
}
