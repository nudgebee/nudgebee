package core

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// The same cases llm-server and rag-server run, so the three copies of the rule
// cannot drift.
func TestLLMDefaultSharedCases(t *testing.T) {
	raw, err := os.ReadFile("../../../../llm/llm-server/agents/core/testdata/llm_default_cases.json")
	require.NoError(t, err)
	var file struct {
		Cases []struct {
			Name    string `json:"name"`
			Configs []struct {
				Id              string   `json:"id"`
				Enabled         *bool    `json:"enabled"`
				Accounts        []string `json:"accounts"`
				AccountDefaults []string `json:"account_defaults"`
				TenantDefault   bool     `json:"tenant_default"`
			} `json:"configs"`
			Accounts map[string]struct {
				Uses   string   `json:"uses"`
				Picker []string `json:"picker"`
			} `json:"accounts"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Cases)

	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var configs []llmConfigState
			for _, c := range tc.Configs {
				configs = append(configs, llmConfigState{
					Id: c.Id, Enabled: c.Enabled == nil || *c.Enabled,
					Accounts: c.Accounts, AccountDefaults: c.AccountDefaults, TenantDefault: c.TenantDefault,
				})
			}
			for account, want := range tc.Accounts {
				got, _ := chooseLLMForAccount(configs, account)
				require.Equal(t, want.Uses, got, "account %s uses", account)
				picker := []string{}
				for _, c := range configs {
					if llmPickableForAccount(c, account) {
						picker = append(picker, c.Id)
					}
				}
				sort.Strings(picker)
				require.Equal(t, want.Picker, picker, "account %s picker", account)
			}
		})
	}
}

func boolp(b bool) *bool { return &b }

func twoAccounts(configs ...llmConfigState) llmTenantState {
	return llmTenantState{Configs: configs, Accounts: []LLMRef{{Id: "A", Name: "acct-a"}, {Id: "B", Name: "acct-b"}}}
}

func TestCheckLLMChange_DeletingTheTenantDefaultBlocksWithoutEnv(t *testing.T) {
	state := twoAccounts(llmConfigState{Id: "X", Name: "x", Enabled: true, TenantDefault: true})

	report := checkLLMChange(state, LLMDefaultsChange{Op: "delete", IntegrationId: "X"}, false)

	require.Len(t, report.Blocked, 2)
	require.Equal(t, "none", report.Blocked[0].AfterSource)
}

func TestCheckLLMChange_DeletingTheTenantDefaultWarnsWithEnv(t *testing.T) {
	state := twoAccounts(llmConfigState{Id: "X", Name: "x", Enabled: true, TenantDefault: true})

	report := checkLLMChange(state, LLMDefaultsChange{Op: "disable", IntegrationId: "X"}, true)

	require.Empty(t, report.Blocked)
	require.Len(t, report.Affected, 2)
	require.Equal(t, "platform", report.Affected[0].AfterSource)
}

func TestCheckLLMChange_AnAccountWithItsOwnDefaultIsNotAffected(t *testing.T) {
	state := twoAccounts(
		llmConfigState{Id: "X", Name: "x", Enabled: true, TenantDefault: true},
		llmConfigState{Id: "Y", Name: "y", Enabled: true, Accounts: []string{"A"}, AccountDefaults: []string{"A"}},
	)

	report := checkLLMChange(state, LLMDefaultsChange{Op: "delete", IntegrationId: "X"}, false)

	require.Len(t, report.Blocked, 1)
	require.Equal(t, "B", report.Blocked[0].AccountId)
}

func TestCheckLLMChange_MakingAnotherConfigTheTenantDefaultReplacesIt(t *testing.T) {
	state := twoAccounts(
		llmConfigState{Id: "X", Name: "x", Enabled: true, TenantDefault: true},
		llmConfigState{Id: "Z", Name: "z", Enabled: true, Accounts: []string{"A"}},
	)

	report := checkLLMChange(state, LLMDefaultsChange{Op: "save", IntegrationId: "Z", AccountIds: []string{"A"}, TenantDefault: boolp(true)}, false)

	require.Equal(t, "X", report.ReplacesTenantDefault.Id)
	require.Len(t, report.Affected, 2)
	require.Empty(t, report.Blocked)
}

func TestCheckLLMChange_AddingAnUnmarkedConfigChangesNothing(t *testing.T) {
	state := twoAccounts(llmConfigState{Id: "X", Name: "x", Enabled: true, Accounts: []string{"A"}, AccountDefaults: []string{"A"}})

	report := checkLLMChange(state, LLMDefaultsChange{Op: "save", AccountIds: []string{"A"}, TenantDefault: boolp(false), AccountDefault: boolp(false)}, false)

	require.Empty(t, report.Affected)
	require.Empty(t, report.Blocked)
}

func TestCheckLLMChange_MarkingAnAccountDefaultReplacesTheOldOne(t *testing.T) {
	state := twoAccounts(llmConfigState{Id: "X", Name: "x", Enabled: true, Accounts: []string{"A"}, AccountDefaults: []string{"A"}})

	report := checkLLMChange(state, LLMDefaultsChange{Op: "save", AccountIds: []string{"A"}, AccountDefault: boolp(true)}, false)

	require.Len(t, report.ReplacedAccountDefaults, 1)
	require.Equal(t, "X", report.ReplacedAccountDefaults[0].Before.Id)
}

func TestCheckLLMChange_RemovingEveryAccountMakesTheConfigTenantWide(t *testing.T) {
	state := twoAccounts(
		llmConfigState{Id: "T", Name: "t", Enabled: true, TenantDefault: true},
		llmConfigState{Id: "Y", Name: "y", Enabled: true, Accounts: []string{"A"}, AccountDefaults: []string{"A"}},
	)

	report := checkLLMChange(state, LLMDefaultsChange{Op: "save", IntegrationId: "Y", AccountIds: nil}, false)

	require.True(t, report.BecomesTenantWide)
	require.Empty(t, report.Blocked, "A falls back to the tenant default")
	require.Equal(t, "tenant_default", report.Affected[0].AfterSource)
}

func TestCheckLLMChange_RemovingTheLastAccountWithNothingToFallBackToBlocks(t *testing.T) {
	state := twoAccounts(llmConfigState{Id: "Y", Name: "y", Enabled: true, Accounts: []string{"A"}, AccountDefaults: []string{"A"}})

	report := checkLLMChange(state, LLMDefaultsChange{Op: "save", IntegrationId: "Y", AccountIds: nil}, false)

	require.Len(t, report.Blocked, 1)
	require.Equal(t, "A", report.Blocked[0].AccountId)
}

func TestResolveLLMSaveOptions_ExplicitTicksAreRemovedBeforeStoring(t *testing.T) {
	in := WithLLMSaveOptions([]IntegrationConfigValue{
		{Name: "llm_provider", Value: "openai"},
		{Name: DefaultLLMProvider, Value: "true"},
	}, LLMSaveOptions{TenantDefault: boolp(false), AccountDefault: boolp(true)})

	opts, explicit, kept := resolveLLMSaveOptions(in)

	require.True(t, explicit)
	require.False(t, *opts.TenantDefault)
	require.True(t, *opts.AccountDefault)
	require.Equal(t, []IntegrationConfigValue{{Name: "llm_provider", Value: "openai"}}, kept,
		"the ticks and the old checkbox value they replace are never stored")
}

func TestResolveLLMSaveOptions_OldCheckboxSetsBothDefaults(t *testing.T) {
	in := []IntegrationConfigValue{{Name: "llm_provider", Value: "openai"}, {Name: DefaultLLMProvider, Value: "true"}}

	opts, explicit, kept := resolveLLMSaveOptions(in)

	require.False(t, explicit)
	require.True(t, *opts.TenantDefault)
	require.True(t, *opts.AccountDefault)
	require.Equal(t, in, kept, "the old value is still written by the save loop")
}
