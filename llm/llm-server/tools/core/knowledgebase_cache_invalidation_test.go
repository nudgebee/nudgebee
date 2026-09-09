package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// seedKBCaches puts one skill body and one agent menu in the caches so the
// invalidation tests can assert on what survives.
func seedKBCaches(t *testing.T, accountId, skillName, agentId string) {
	t.Helper()
	require.NoError(t, common.CacheSet(CacheNamespaceLlmSkillContent,
		fmt.Sprintf("skill:%s:%s", accountId, skillName), []byte(`{"Data":"body"}`)))
	require.NoError(t, common.CacheSet(CacheNamespaceLlmKbMapping,
		fmt.Sprintf("kb_mapping:%s:%s", accountId, agentId), []byte(`[]`)))
}

func skillCached(accountId, skillName string) bool {
	_, ok := common.CacheGet(CacheNamespaceLlmSkillContent, fmt.Sprintf("skill:%s:%s", accountId, skillName))
	return ok
}

func menuCached(accountId, agentId string) bool {
	_, ok := common.CacheGet(CacheNamespaceLlmKbMapping, fmt.Sprintf("kb_mapping:%s:%s", accountId, agentId))
	return ok
}

// TestInvalidateKBCaches_ClearsOldNameAndMappedAgentMenus is the regression
// guard for the rename bug: renaming a skill left load_skills serving the old
// body under the OLD name (the key the model keeps asking for) and left the
// agent's skill menu advertising that old name. Both caches are keyed on the
// name / agent, not the KB id, so the invalidation must cover every name the KB
// has been known by and every agent it is mapped to.
func TestInvalidateKBCaches_ClearsOldNameAndMappedAgentMenus(t *testing.T) {
	const accountId = "acct-1"
	const kbId = "kb-1"

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	seedKBCaches(t, accountId, "old_name", "aws_orchestrator")
	seedKBCaches(t, accountId, "new_name", "k8s_orchestrator")
	// Another account's entries must be untouched.
	seedKBCaches(t, "acct-2", "old_name", "aws_orchestrator")

	mock.ExpectQuery(`SELECT agent_id FROM llm_kb_agent_mappings`).
		WithArgs(kbId, accountId).
		WillReturnRows(sqlmock.NewRows([]string{"agent_id"}).
			AddRow("aws_orchestrator").
			AddRow("k8s_orchestrator"))

	invalidateKBCaches(dbms, accountId, kbId, "Old_Name", "New_Name")

	assert.False(t, skillCached(accountId, "old_name"), "old name must be invalidated")
	assert.False(t, skillCached(accountId, "new_name"), "new name must be invalidated")
	assert.False(t, menuCached(accountId, "aws_orchestrator"), "mapped agent menu must be invalidated")
	assert.False(t, menuCached(accountId, "k8s_orchestrator"), "mapped agent menu must be invalidated")
	assert.True(t, skillCached("acct-2", "old_name"), "another account must not be touched")
	assert.True(t, menuCached("acct-2", "aws_orchestrator"), "another account must not be touched")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInvalidateKBCaches_ClearsMappingNamespaceWhenLookupFails: if we cannot
// enumerate the mapped agents we must not silently skip the menus — falling
// back to a namespace clear keeps a renamed skill from staying on the menu.
func TestInvalidateKBCaches_ClearsMappingNamespaceWhenLookupFails(t *testing.T) {
	const accountId = "acct-fail"
	const kbId = "kb-2"

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}

	seedKBCaches(t, accountId, "some_skill", "aws_orchestrator")

	mock.ExpectQuery(`SELECT agent_id FROM llm_kb_agent_mappings`).
		WithArgs(kbId, accountId).
		WillReturnError(errors.New("boom"))

	invalidateKBCaches(dbms, accountId, kbId, "some_skill")

	assert.False(t, skillCached(accountId, "some_skill"))
	assert.False(t, menuCached(accountId, "aws_orchestrator"), "menu must not survive a failed mapping lookup")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInvalidateSkillContentCache_SkipsEmptyAndDeduplicates guards the
// caller contract: Update passes (oldName, newName), which are identical on a
// data-only edit, and either may arrive padded or empty.
func TestInvalidateSkillContentCache_SkipsEmptyAndDeduplicates(t *testing.T) {
	const accountId = "acct-3"
	seedKBCaches(t, accountId, "same_name", "unused_agent")

	invalidateSkillContentCache(accountId, " Same_Name ", "same_name", "")

	assert.False(t, skillCached(accountId, "same_name"))
	assert.True(t, menuCached(accountId, "unused_agent"), "skill-content invalidation must not touch menus")
}
