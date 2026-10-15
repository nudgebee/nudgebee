package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

func TestAlwaysLoadEligible(t *testing.T) {
	atCap := strings.Repeat("é", AlwaysLoadMaxChars)
	cases := []struct {
		name       string
		kbType     string
		dataFormat string
		data       string
		want       error
	}{
		{"manual text", KBTypeManual, "text", "rule", nil},
		{"exactly at the cap (runes, not bytes)", KBTypeManual, "text", atCap, nil},
		{"over the cap", KBTypeManual, "text", atCap + "x", errAlwaysLoadTooLong},
		{"pdf", KBTypeManual, string(KBFormatPDF), "", errAlwaysLoadKind},
		{"integration source", "integration", "text", "rule", errAlwaysLoadKind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, alwaysLoadEligible(tc.kbType, tc.dataFormat, tc.data))
		})
	}
}

func TestValidateAlwaysLoad_RefusesAllAgentsLink(t *testing.T) {
	// The wildcard check runs before any database read.
	assert.Equal(t, errAlwaysLoadWildcard, validateAlwaysLoad(nil, "kb-1", KBAgentWildcard))
}

func TestSelectAlwaysLoadSkills_OrdersByNameListAndDedupes(t *testing.T) {
	all := []AlwaysLoadSkill{
		{ID: "a", Name: "A", AgentId: "elastic_search_metrics"},
		{ID: "b", Name: "B", AgentId: "metrics"},
		{ID: "a", Name: "A", AgentId: "metrics"},
		{ID: "c", Name: "C", AgentId: "logs"},
	}
	got := selectAlwaysLoadSkills(all, []string{"metrics", "elastic_search_metrics"})
	assert.Equal(t, []AlwaysLoadSkill{
		{ID: "b", Name: "B", AgentId: "metrics"},
		{ID: "a", Name: "A", AgentId: "metrics"},
	}, got, "parent names first; a skill linked twice appears once; other agents' links excluded")
	assert.Empty(t, selectAlwaysLoadSkills(all, []string{"traces"}))
}

func TestListAlwaysLoadSkills_ServedFromCacheUntilInvalidated(t *testing.T) {
	const accountId = "acct-always-load"
	cached := `[{"id":"kb-1","name":"ES setup","agent_id":"metrics","data":"metrics-*"}]`
	require.NoError(t, common.CacheSet(CacheNamespaceLlmKbMapping, alwaysLoadCacheKey(accountId), []byte(cached)))

	// A cache hit needs no database: the metastore is not set up in unit tests.
	got, err := ListAlwaysLoadSkills(security.NewRequestContextForSuperAdmin(), accountId, []string{"metrics"})
	require.NoError(t, err)
	assert.Equal(t, []AlwaysLoadSkill{{ID: "kb-1", Name: "ES setup", AgentId: "metrics", Data: "metrics-*"}}, got)

	// Any KB or link write for the account goes through invalidateKBMappingCache.
	invalidateKBMappingCache(accountId, "metrics")
	_, ok := common.CacheGet(CacheNamespaceLlmKbMapping, alwaysLoadCacheKey(accountId))
	assert.False(t, ok)
}

func TestIsAlwaysLoadRuleError(t *testing.T) {
	assert.True(t, IsAlwaysLoadRuleError(errAlwaysLoadWildcard))
	assert.True(t, IsAlwaysLoadRuleError(fmt.Errorf("this skill is always loaded on an agent: %w", errAlwaysLoadTooLong)))
	assert.False(t, IsAlwaysLoadRuleError(errors.New("db down")))
}
