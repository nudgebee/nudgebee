package core

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

type llmDefaultCaseConfig struct {
	Id              string   `json:"id"`
	Enabled         *bool    `json:"enabled"`
	Accounts        []string `json:"accounts"`
	AccountDefaults []string `json:"account_defaults"`
	TenantDefault   bool     `json:"tenant_default"`
}

type llmDefaultCase struct {
	Name     string                 `json:"name"`
	Configs  []llmDefaultCaseConfig `json:"configs"`
	Accounts map[string]struct {
		Uses   string   `json:"uses"`
		Picker []string `json:"picker"`
	} `json:"accounts"`
}

// candidatesFor builds what the SQL hands chooseAccountLLMIntegration and
// llmPickableForAccount for one account: enabled configs only, as the queries
// filter on status.
func candidatesFor(account string, configs []llmDefaultCaseConfig) []llmCandidate {
	var out []llmCandidate
	for _, c := range configs {
		if c.Enabled != nil && !*c.Enabled {
			continue
		}
		out = append(out, llmCandidate{
			Id:             c.Id,
			Linked:         slices.Contains(c.Accounts, account),
			AccountDefault: slices.Contains(c.AccountDefaults, account),
			TenantDefault:  c.TenantDefault,
			HasAccounts:    len(c.Accounts) > 0,
		})
	}
	return out
}

func TestLLMDefaultSharedCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/llm_default_cases.json")
	require.NoError(t, err)
	var file struct {
		Cases []llmDefaultCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Cases)

	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			for account, want := range tc.Accounts {
				all := candidatesFor(account, tc.Configs)

				// selectAccountLLMIntegration only loads linked configs and the tenant default.
				var loaded []llmCandidate
				for _, c := range all {
					if c.Linked || c.TenantDefault {
						loaded = append(loaded, c)
					}
				}
				require.Equal(t, want.Uses, chooseAccountLLMIntegration(account, loaded), "account %s uses", account)

				picker := []string{}
				for _, c := range all {
					if llmPickableForAccount(c) {
						picker = append(picker, c.Id)
					}
				}
				sort.Strings(picker)
				require.Equal(t, want.Picker, picker, "account %s picker", account)
			}
		})
	}
}
