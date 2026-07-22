//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGenerateCanonicalLogQuery_Live exercises ONLY the canonical query
// generation step of fetch_logs v2 — the LLM translating a natural-language log
// question into a provider-independent {"where": ...} JSON. It does NOT execute
// the query, so it is independent of backend/relay health (which is what makes
// the full chat path fall back to kubectl in this env).
//
// It builds the real FetchLogsAgentV2 for TEST_ACCOUNT, so the prompt is fed the
// account's actual capabilities.label_mappings + supported_operators (from
// get_default_provider) and backend labels (from get_labels). For each question
// it asserts the generated query is valid JSON, has a `where`, and uses ONLY
// canonical entity names / backend labels and supported operators — i.e. the LLM
// did not invent fields or operators.
//
// Run:
//
//	set -a; source llm/llm-server/.env; set +a
//	cd llm/llm-server && go test -tags e2e -run TestGenerateCanonicalLogQuery_Live ./agents/ -v -count=1 -timeout 300s
func TestGenerateCanonicalLogQuery_Live(t *testing.T) {
	tenant := os.Getenv("TEST_TENANT")
	user := os.Getenv("TEST_USER")
	account := os.Getenv("TEST_ACCOUNT")
	if tenant == "" || user == "" || account == "" {
		t.Skip("set TEST_TENANT / TEST_USER / TEST_ACCOUNT to run")
	}
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, nil)

	a := newFetchLogsAgentV2(account)
	a.ensureLabelsAndIndices()
	t.Logf("provider=%q\n  label_mappings=%v\n  supported_operators=%v\n  backend_labels=%v\n  default_index=%q",
		a.provider.Provider, a.provider.Capabilities.LabelMappings,
		a.provider.Capabilities.SupportedOperators, a.fields, a.provider.DefaultIndex)

	if strings.TrimSpace(a.provider.Provider) == "" {
		t.Fatalf("account %s resolves to an empty log provider — canonical path is N/A (would route to kubectl)", account)
	}

	canonical := map[string]bool{}
	for k := range a.provider.Capabilities.LabelMappings {
		canonical[k] = true
	}
	backendLabels := map[string]bool{}
	for _, f := range a.fields {
		backendLabels[f] = true
	}
	allowedOps := map[string]bool{}
	for _, op := range resolveQueryOperators(a.provider.Capabilities.SupportedOperators) {
		allowedOps[op] = true
	}

	questions := []string{
		"show me recent logs for services-server in nudgebee namespace",
		"errors in services-server in the last 1 hour",
		"logs for pod services-server-67554f95d4-4ft2b in namespace nudgebee",
		"warn or error logs for services-server",
		"all logs for services-server in namespace nudgebee last 6h limit 2000",
	}

	for i, q := range questions {
		req := core.NBAgentRequest{UserId: user, AccountId: account, Query: q, OriginalQuery: q}
		raw, err := generateCanonicalLogQuery(sc, req, a.provider, a.fields, a.indices)
		if !assert.NoErrorf(t, err, "q[%d]=%q generation failed", i, q) {
			continue
		}

		var parsed map[string]any
		if !assert.NoErrorf(t, common.ExtractAndUnmarshalJSON([]byte(raw), &parsed),
			"q[%d]=%q produced non-JSON: %s", i, q, raw) {
			continue
		}
		t.Logf("\nQ%d: %s\n --> %s", i, q, compact(parsed))

		where, ok := parsed["where"].(map[string]any)
		if !assert.Truef(t, ok, "q[%d] missing/!object `where`: %s", i, compact(parsed)) {
			continue
		}
		validateWhereFieldsAndOps(t, i, where, canonical, backendLabels, allowedOps)
	}
}

// genericCanonicalAliases are the standard canonical names the OLD prompt
// hardcoded. Emitting one of these while it is NOT a key in the account's
// label_mappings is the specific "anchoring regression" this fix removed (the
// model used generic vocab instead of the account's actual canonical_name) — so
// that is a hard failure. A field that is simply absent from the mapping AND not
// a generic alias is a mapping GAP (no canonical key exists for that concept,
// e.g. this account has no `pod` mapping) — informative, not a prompt bug.
var genericCanonicalAliases = map[string]bool{"service": true, "message": true, "pod": true, "container": true}

// validateWhereFieldsAndOps walks a where clause asserting (a) operators are in
// the supported set, and (b) the model did not anchor on a generic canonical name
// that the account's mapping does not contain. Out-of-mapping non-generic fields
// are logged as mapping gaps, not failed. Recurses through _and / _or / _not.
func validateWhereFieldsAndOps(t *testing.T, qi int, where map[string]any, canonical, backendLabels, allowedOps map[string]bool) {
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		for k, v := range node {
			switch k {
			case "_and", "_or":
				if arr, ok := v.([]any); ok {
					for _, e := range arr {
						if m, ok := e.(map[string]any); ok {
							walk(m)
						}
					}
				}
			case "_not":
				if m, ok := v.(map[string]any); ok {
					walk(m)
				}
			default:
				if !canonical[k] && !backendLabels[k] {
					if genericCanonicalAliases[k] {
						assert.Failf(t, "anchoring regression",
							"q[%d] emitted generic canonical %q which is NOT a key in this account's label_mappings — the prompt should have steered to a listed canonical_name", qi, k)
					} else {
						t.Logf("q[%d] field %q has no canonical mapping key — mapping GAP (no canonical name for this concept), not a prompt regression", qi, k)
					}
				}
				if ops, ok := v.(map[string]any); ok {
					for op := range ops {
						assert.Truef(t, allowedOps[op], "q[%d] field %q uses unsupported operator %q", qi, k, op)
					}
				}
			}
		}
	}
	walk(where)
}

func compact(v map[string]any) string {
	b, err := common.MarshalJson(v)
	if err != nil {
		return ""
	}
	return string(b)
}
