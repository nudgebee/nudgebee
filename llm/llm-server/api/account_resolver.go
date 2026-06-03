package api

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"strings"
)

// CloudAccountInfo holds minimal account info for resolution.
type CloudAccountInfo struct {
	ID          string
	AccountName string
}

// ResolveAccountResult represents the outcome of account resolution.
type ResolveAccountResult struct {
	AccountId string                // resolved account ID (empty if not resolved)
	Followup  *core.NBAgentResponse // non-nil if a followup should be returned to the user
}

// AccountResolveError represents a structured error from account resolution.
type AccountResolveError struct {
	StatusCode int
	Message    string
}

// resolveAccountForRequest resolves an account from query text using the security context.
// It fetches tenant accounts, filters by user access, and applies string matching.
func resolveAccountForRequest(ctx *security.RequestContext, query string) (ResolveAccountResult, *AccountResolveError) {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return ResolveAccountResult{}, &AccountResolveError{
			StatusCode: 400,
			Message:    "api: account_id is required or tenant must be identifiable",
		}
	}

	allAccounts, err := security.GetAccountsForTenant(tenantId)
	if err != nil {
		slog.Error("account_resolver: error fetching accounts for tenant", "error", err)
		return ResolveAccountResult{}, &AccountResolveError{
			StatusCode: 500,
			Message:    "api: unable to resolve account",
		}
	}

	// Filter to user-accessible accounts
	accessibleIds := ctx.GetSecurityContext().ListAccountIds()
	accessibleSet := make(map[string]bool, len(accessibleIds))
	for _, id := range accessibleIds {
		accessibleSet[id] = true
	}
	var accessibleAccounts []CloudAccountInfo
	for _, acc := range allAccounts {
		if accessibleSet[acc.ID] {
			accessibleAccounts = append(accessibleAccounts, CloudAccountInfo{
				ID:          acc.ID,
				AccountName: acc.AccountName,
			})
		}
	}

	if len(accessibleAccounts) == 0 {
		return ResolveAccountResult{}, &AccountResolveError{
			StatusCode: 403,
			Message:    "api: no accessible accounts found",
		}
	}

	return resolveAccountFromQuery(query, accessibleAccounts), nil
}

// resolveAccountFromQuery attempts to identify an account from the query text
// using case-insensitive matching against account names.
//
// Logic:
//  1. If user has exactly 1 accessible account -> auto-select it
//  2. Exact match: query contains full account name (e.g. "check k8s-dev" matches "k8s-dev")
//  3. Token match: a query word matches a token in account name (e.g. "check k8s" matches "k8s-dev")
//  4. If zero or multiple matches at any level -> return account_select followup
func resolveAccountFromQuery(query string, accounts []CloudAccountInfo) ResolveAccountResult {

	if len(accounts) == 0 {
		return ResolveAccountResult{}
	}

	// Single account - auto-select, no ambiguity
	if len(accounts) == 1 {
		return ResolveAccountResult{AccountId: accounts[0].ID}
	}

	// Pass 1: Exact match - query contains the full account name
	queryLower := strings.ToLower(query)
	var exactMatches []CloudAccountInfo
	for _, acc := range accounts {
		if strings.Contains(queryLower, strings.ToLower(acc.AccountName)) {
			exactMatches = append(exactMatches, acc)
		}
	}

	if len(exactMatches) == 1 {
		return ResolveAccountResult{AccountId: exactMatches[0].ID}
	}

	// Pass 2: Token match - split query into words and account names into tokens,
	// check if any query word matches an account name token.
	// e.g. query "list pods k8s" -> word "k8s" matches token "k8s" in "k8s-dev"
	if len(exactMatches) == 0 {
		queryWords := tokenize(queryLower)
		var tokenMatches []CloudAccountInfo
		for _, acc := range accounts {
			accTokens := tokenize(strings.ToLower(acc.AccountName))
			if hasCommonToken(queryWords, accTokens) {
				tokenMatches = append(tokenMatches, acc)
			}
		}

		if len(tokenMatches) == 1 {
			return ResolveAccountResult{AccountId: tokenMatches[0].ID}
		}

		if len(tokenMatches) > 1 {
			return buildAccountSelectionFollowup(query, tokenMatches)
		}

	}

	slog.Info("account_resolver: returning account selection followup", "exact_matches", len(exactMatches), "total_accounts", len(accounts))
	// Zero or multiple matches - return followup with all account options
	return buildAccountSelectionFollowup(query, accounts)
}

// tokenize splits a string into meaningful tokens by common delimiters
// and filters out noise words that are too short or too generic.
func tokenize(s string) []string {
	// Split by spaces, hyphens, underscores
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.' || r == ','
	})
	// Filter out tokens that are too short (< 3 chars) or common noise words
	noiseWords := map[string]bool{
		"the": true, "from": true, "for": true, "and": true, "with": true,
		"list": true, "get": true, "show": true, "check": true, "pods": true,
		"logs": true, "events": true, "namespace": true, "cluster": true,
	}
	var tokens []string
	for _, p := range parts {
		if len(p) >= 3 && !noiseWords[p] {
			tokens = append(tokens, p)
		}
	}
	return tokens
}

// hasCommonToken checks if two token slices share any common token.
func hasCommonToken(a, b []string) bool {
	set := make(map[string]bool, len(b))
	for _, t := range b {
		set[t] = true
	}
	for _, t := range a {
		if set[t] {
			return true
		}
	}
	return false
}

// buildAccountSelectionFollowup creates an account_select followup response
// listing all available accounts for the user to choose from.
// The original query is preserved in the response so the client can re-submit
// it with the selected account_id.
func buildAccountSelectionFollowup(originalQuery string, accounts []CloudAccountInfo) ResolveAccountResult {
	options := make([]string, len(accounts))
	accountMap := make(map[string]any, len(accounts))
	for i, acc := range accounts {
		options[i] = acc.AccountName
		accountMap[acc.AccountName] = acc.ID
	}

	return ResolveAccountResult{
		Followup: &core.NBAgentResponse{
			Response: []string{"I found multiple accounts associated with your profile. Please select which account you'd like to work with:"},
			Query:    originalQuery,
			Status:   core.ConversationStatusCompleted,
			FollowupRequest: core.FollowupRequest{
				Question:        "Which account would you like to use?",
				FollowupType:    core.FollowupTypeAccountSelect,
				FollowupOptions: options,
				FollowupData:    accountMap,
			},
		},
	}
}
