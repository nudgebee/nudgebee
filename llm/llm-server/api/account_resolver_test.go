package api

import (
	"nudgebee/llm/agents/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveAccountFromQuery_SingleAccount(t *testing.T) {
	accounts := []CloudAccountInfo{{ID: "acc-1", AccountName: "production"}}
	result := resolveAccountFromQuery("check pods", accounts)
	assert.Equal(t, "acc-1", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_ExactSubstringMatch(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "production"},
		{ID: "acc-2", AccountName: "staging"},
	}
	result := resolveAccountFromQuery("check the production account", accounts)
	assert.Equal(t, "acc-1", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_CaseInsensitive(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "Production-US"},
		{ID: "acc-2", AccountName: "Staging-EU"},
	}
	result := resolveAccountFromQuery("check PRODUCTION-US pods", accounts)
	assert.Equal(t, "acc-1", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_NoMatch(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "production"},
		{ID: "acc-2", AccountName: "staging"},
	}
	result := resolveAccountFromQuery("check the pods", accounts)
	assert.Empty(t, result.AccountId)
	assert.NotNil(t, result.Followup)
	assert.Equal(t, core.FollowupTypeAccountSelect, result.Followup.FollowupRequest.FollowupType)
	assert.Len(t, result.Followup.FollowupRequest.FollowupOptions, 2)
	assert.Contains(t, result.Followup.FollowupRequest.FollowupOptions, "production")
	assert.Contains(t, result.Followup.FollowupRequest.FollowupOptions, "staging")
}

func TestResolveAccountFromQuery_MultipleMatches(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "production-us"},
		{ID: "acc-2", AccountName: "production-eu"},
	}
	result := resolveAccountFromQuery("check production", accounts)
	assert.Empty(t, result.AccountId)
	assert.NotNil(t, result.Followup)
	assert.Equal(t, core.FollowupTypeAccountSelect, result.Followup.FollowupRequest.FollowupType)
	assert.Len(t, result.Followup.FollowupRequest.FollowupOptions, 2)
}

func TestResolveAccountFromQuery_EmptyAccounts(t *testing.T) {
	result := resolveAccountFromQuery("check pods", nil)
	assert.Empty(t, result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_QueryConfigContainsMapping(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "production"},
		{ID: "acc-2", AccountName: "staging"},
	}
	result := resolveAccountFromQuery("what's the status?", accounts)
	assert.NotNil(t, result.Followup)
	assert.NotNil(t, result.Followup.FollowupRequest.FollowupData)
	assert.Equal(t, "acc-1", result.Followup.FollowupRequest.FollowupData["production"])
	assert.Equal(t, "acc-2", result.Followup.FollowupRequest.FollowupData["staging"])
	// Original query is preserved so client can re-submit it with selected account_id
	assert.Equal(t, "what's the status?", result.Followup.Query)
}

func TestResolveAccountFromQuery_SingleAccountIgnoresQueryContent(t *testing.T) {
	accounts := []CloudAccountInfo{{ID: "acc-1", AccountName: "production"}}
	result := resolveAccountFromQuery("look into staging account", accounts)
	assert.Equal(t, "acc-1", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_PartialMatchSingleResult(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "my-aws-prod"},
		{ID: "acc-2", AccountName: "my-gcp-dev"},
	}
	result := resolveAccountFromQuery("check logs in my-aws-prod cluster", accounts)
	assert.Equal(t, "acc-1", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_TokenMatch_QueryWordMatchesAccountToken(t *testing.T) {
	// "civo" in query matches token "civo" from account name "civo-dev"
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "azure-dev"},
		{ID: "acc-2", AccountName: "civo-dev"},
		{ID: "acc-3", AccountName: "aws-prod"},
	}
	result := resolveAccountFromQuery("list pods from nudgebee NS civo", accounts)
	assert.Equal(t, "acc-2", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestResolveAccountFromQuery_TokenMatch_MultipleTokenMatches(t *testing.T) {
	// "prod" matches both "aws-prod" and "gcp-prod" -> followup
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "aws-prod"},
		{ID: "acc-2", AccountName: "gcp-prod"},
		{ID: "acc-3", AccountName: "azure-dev"},
	}
	result := resolveAccountFromQuery("check prod logs", accounts)
	assert.Empty(t, result.AccountId)
	assert.NotNil(t, result.Followup)
}

func TestResolveAccountFromQuery_ExactMatchTakesPrecedence(t *testing.T) {
	// Full name "civo-dev" in query should exact-match even if tokens could match others
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "civo-dev"},
		{ID: "acc-2", AccountName: "civo-prod"},
	}
	result := resolveAccountFromQuery("check civo-dev cluster", accounts)
	assert.Equal(t, "acc-1", result.AccountId)
	assert.Nil(t, result.Followup)
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("list pods from nudgebee ns civo")
	assert.Contains(t, tokens, "nudgebee")
	assert.Contains(t, tokens, "civo")
	// Noise words and short words should be filtered
	assert.NotContains(t, tokens, "list")
	assert.NotContains(t, tokens, "from")
	assert.NotContains(t, tokens, "ns") // too short (< 3 chars)
}

func TestHasCommonToken(t *testing.T) {
	assert.True(t, hasCommonToken([]string{"civo", "nudgebee"}, []string{"civo", "dev"}))
	assert.False(t, hasCommonToken([]string{"azure", "nudgebee"}, []string{"civo", "dev"}))
}

// TestResolveAccountFromQuery_FollowupRoundTrip simulates the full client round-trip:
//  1. Client sends an ambiguous query (no clear account) → server returns account_select followup
//  2. Client picks an account from FollowupOptions, looks up the account_id via QueryConfig,
//     and re-submits the original query with account_id set.
//
// This mirrors the flow in chains.go where:
//   - First request:  resolveAccountFromQuery returns Followup → API responds 200 with followup
//   - Second request: client sends account_id directly → account resolution is skipped entirely
func TestResolveAccountFromQuery_FollowupRoundTrip(t *testing.T) {
	accounts := []CloudAccountInfo{
		{ID: "acc-1", AccountName: "aws-prod"},
		{ID: "acc-2", AccountName: "gcp-prod"},
		{ID: "acc-3", AccountName: "azure-dev"},
	}
	originalQuery := "why are pods crashing in prod"

	// --- Step 1: First request - ambiguous query triggers followup ---
	result := resolveAccountFromQuery(originalQuery, accounts)

	assert.Empty(t, result.AccountId, "should not auto-resolve when multiple accounts match")
	assert.NotNil(t, result.Followup, "should return followup for ambiguous query")

	followup := result.Followup

	// Verify followup type is account_select
	assert.Equal(t, core.FollowupTypeAccountSelect, followup.FollowupRequest.FollowupType)

	// Verify the original query is preserved so client can re-submit it
	assert.Equal(t, originalQuery, followup.Query)

	// Verify status is completed (this followup is returned directly, not waiting for agent)
	assert.Equal(t, core.ConversationStatusCompleted, followup.Status)

	// Verify followup options contain the matched account names
	assert.Len(t, followup.FollowupRequest.FollowupOptions, 2)
	assert.Contains(t, followup.FollowupRequest.FollowupOptions, "aws-prod")
	assert.Contains(t, followup.FollowupRequest.FollowupOptions, "gcp-prod")
	// azure-dev should NOT be in options (didn't match "prod")
	assert.NotContains(t, followup.FollowupRequest.FollowupOptions, "azure-dev")

	// Verify FollowupData maps each option name to its account ID
	assert.NotNil(t, followup.FollowupRequest.FollowupData)
	for _, option := range followup.FollowupRequest.FollowupOptions {
		accountId, exists := followup.FollowupRequest.FollowupData[option]
		assert.True(t, exists, "FollowupData must contain mapping for option %q", option)
		assert.NotEmpty(t, accountId, "account ID for option %q must not be empty", option)
	}

	// --- Step 2: Client selects "aws-prod" from the followup options ---
	selectedOption := "aws-prod"
	selectedAccountId := followup.FollowupRequest.FollowupData[selectedOption].(string)

	assert.Equal(t, "acc-1", selectedAccountId)

	// In the real flow (chains.go), the client re-submits with account_id set,
	// so resolveAccountFromQuery is never called again. The request proceeds
	// directly to agent execution with the selected account_id.
	// We verify the resolved ID is valid by confirming it exists in the original accounts.
	found := false
	for _, acc := range accounts {
		if acc.ID == selectedAccountId {
			found = true
			break
		}
	}
	assert.True(t, found, "selected account ID %q must exist in original accounts", selectedAccountId)
}
