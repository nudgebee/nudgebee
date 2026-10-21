package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// fakeSkillRepo serves one tarball per call, built from files; the commit is
// whatever the test sets.
type fakeSkillRepo struct {
	t      *testing.T
	commit string
	files  map[string]string
	// calls counts lookups; downloads counts the ones that fetched a tarball.
	calls, downloads int
	// onFetch runs during the download, while the sync is under way.
	onFetch func()
}

func (f *fakeSkillRepo) fetch(_ context.Context, _, _, _, knownCommit string) (string, io.ReadCloser, error) {
	f.calls++
	if f.onFetch != nil {
		f.onFetch()
	}
	if f.commit == knownCommit {
		return f.commit, nil, nil
	}
	f.downloads++
	return f.commit, io.NopCloser(buildRepoTarball(f.t, f.files, nil)), nil
}

func stubSkillSourceFetch(t *testing.T, repo *fakeSkillRepo) *[]string {
	t.Helper()
	oldFetch, oldIndex := FetchSkillSource, submitSkillIndexing
	var indexed []string
	FetchSkillSource = repo.fetch
	submitSkillIndexing = func(_ *security.RequestContext, _ kbOwner, kbId, _ string) error {
		indexed = append(indexed, kbId)
		return nil
	}
	t.Cleanup(func() {
		FetchSkillSource, submitSkillIndexing = oldFetch, oldIndex
	})
	return &indexed
}

func skillMD(name, body string) string {
	return "---\nname: " + name + "\ndescription: does " + name + "\n---\n" + body
}

func insertSkillSource(t *testing.T, db *sqlx.DB, s kbTenant, visibility string, accounts ...string) string {
	t.Helper()
	var id string
	require.NoError(t, db.Get(&id, `
		INSERT INTO llm_kb_skill_sources (tenant_id, repo, folder, visibility, account_ids, created_by)
		VALUES ($1, 'acme/skills', 'skills', $2, $3::uuid[], $4) RETURNING id`,
		s.tenant, visibility, "{"+joinIDs(accounts)+"}", s.user))
	return id
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

type syncedSkill struct {
	Id         string `db:"id"`
	Name       string `db:"name"`
	Status     string `db:"status"`
	Visibility string `db:"visibility"`
	Data       string `db:"data"`
	Path       string `db:"path"`
	Files      string `db:"files"`
}

func listSynced(t *testing.T, db *sqlx.DB, sourceId string) map[string]syncedSkill {
	t.Helper()
	var rows []syncedSkill
	require.NoError(t, db.Select(&rows, `
		SELECT id, name, status, visibility, data, skill_meta->>'source_path' AS path, COALESCE(files::text, '') AS files
		FROM llm_knowledgebases WHERE source_id = $1`, sourceId))
	out := map[string]syncedSkill{}
	for _, r := range rows {
		out[r.Path] = r
	}
	return out
}

// A sync adds every skill folder, keeps a skill's identity across a rename,
// re-indexes only changed SKILL.md text, archives skills gone from the repo,
// and skips a skill whose name another skill of the organisation uses.
func TestSkillSourceSync_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	seedKB(t, db, s.tenant, kbSeedRow{name: "taken-name", enabled: true, visibility: "organisation"})
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{
		"skills/promql/SKILL.md":         skillMD("promql", "Write PromQL."),
		"skills/promql/references/fn.md": "rate() and irate()",
		"skills/loki/SKILL.md":           skillMD("loki", "Query Loki."),
		"skills/clash/SKILL.md":          skillMD("taken-name", "x"),
		"skills/dup-a/SKILL.md":          skillMD("dup", "first"),
		"skills/dup-b/SKILL.md":          skillMD("dup", "second"),
		"skills/broken/SKILL.md":         "no header",
		"elsewhere/ignored/SKILL.md":     skillMD("ignored", "x"),
	}}
	indexed := stubSkillSourceFetch(t, repo)
	sourceId := insertSkillSource(t, db, s, KBVisibilityOrganisation)
	sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.tenant, s.user)

	report, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	assert.Equal(t, "c1", report.Commit)
	assert.Equal(t, 3, report.Added, "promql, loki, dup-a")
	reasons := map[string]string{}
	for _, sk := range report.Skipped {
		reasons[sk.SourcePath] = sk.Reason
	}
	assert.Contains(t, reasons["skills/clash"], `name "taken-name" is already used`)
	assert.Contains(t, reasons["skills/dup-b"], "already used by skills/dup-a in this source")
	assert.Contains(t, reasons["skills/broken"], "does not start with")
	assert.Len(t, *indexed, 3)

	rows := listSynced(t, db, sourceId)
	require.Len(t, rows, 3)
	promql := rows["skills/promql"]
	assert.Equal(t, "promql", promql.Name)
	assert.Equal(t, "---\nname: \"promql\"\ndescription: \"does promql\"\n---\n\n"+
		"Files in this skill (read one with load_skills skill_name \"promql/<path>\"):\n- references/fn.md\n---\n\nWrite PromQL.", promql.Data,
		"stored like the repo's SKILL.md: frontmatter, its files and how to read them, then the instructions")
	assert.Equal(t, "---\nname: \"loki\"\ndescription: \"does loki\"\n---\n\nQuery Loki.", rows["skills/loki"].Data, "no files, no list")
	assert.Equal(t, KBVisibilityOrganisation, promql.Visibility)
	assert.JSONEq(t, `{"references/fn.md": "rate() and irate()"}`, promql.Files)
	// Its description is indexed ahead of the body; the stored body is unchanged.
	assert.Equal(t, "# promql\n\ndoes promql\n\n"+promql.Data, withSkillHeading(kbOwner{TenantId: s.tenant}, promql.Id, promql.Data))

	var src SkillSource
	require.NoError(t, db.Get(&src, `SELECT status, last_commit FROM llm_kb_skill_sources WHERE id = $1`, sourceId))
	assert.Equal(t, SkillSourceStatusActive, src.Status)
	assert.Equal(t, "c1", *src.LastCommit)

	// Second sync: promql renamed (same folder), loki unchanged, dup-a removed.
	mustExec(t, db, `UPDATE llm_knowledgebases SET status = 'active' WHERE source_id = $1`, sourceId)
	repo.commit = "c2"
	repo.files = map[string]string{
		"skills/promql/SKILL.md":         skillMD("promql-guide", "Write PromQL better."),
		"skills/promql/references/fn.md": "rate()",
		"skills/loki/SKILL.md":           skillMD("loki", "Query Loki."),
	}
	*indexed = nil
	report, err = SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	assert.Equal(t, 0, report.Added)
	assert.Equal(t, 1, report.Updated, "only promql's text changed")
	assert.Equal(t, 1, report.Archived)
	rows = listSynced(t, db, sourceId)
	assert.Equal(t, promql.Id, rows["skills/promql"].Id, "a rename keeps the row")
	assert.Equal(t, "promql-guide", rows["skills/promql"].Name)
	assert.Equal(t, []string{promql.Id}, *indexed)
	assert.Equal(t, string(KBStatusActive), rows["skills/loki"].Status)
	assert.Equal(t, string(KBStatusArchived), rows["skills/dup-a"].Status)

	// Synced skills are read-only, can't change scope, and delete only once archived.
	err = UpdateKnowledgebase(sc, "", rows["skills/loki"].Id, Knowledgebase{Name: "x"})
	assert.ErrorIs(t, err, ErrSyncedSkillReadOnly)
	_, err = SetKnowledgebaseVisibility(sc, rows["skills/loki"].Id, KBVisibilityAccount, nil, s.a)
	assert.ErrorIs(t, err, ErrSyncedSkillReadOnly)
	assert.ErrorIs(t, DeleteKnowledgebase(sc, "", rows["skills/loki"].Id), ErrSyncedSkillReadOnly)
	require.NoError(t, DeleteKnowledgebase(sc, "", rows["skills/dup-a"].Id), "archived synced skill can be deleted")

	// Removing the source removes its skills.
	require.NoError(t, DeleteSkillSource(sc, sourceId))
	assert.Empty(t, listSynced(t, db, sourceId))
	var n int
	require.NoError(t, db.Get(&n, `SELECT count(*) FROM llm_kb_skill_sources WHERE id = $1`, sourceId))
	assert.Zero(t, n)
}

// A selected-accounts source switches each skill on for exactly its accounts,
// including skills added by later syncs; an admin's switch-off on one skill
// for one account survives syncs and visibility changes.
func TestSkillSourceVisibility_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{"skills/one/SKILL.md": skillMD("one", "x")}}
	stubSkillSourceFetch(t, repo)
	sourceId := insertSkillSource(t, db, s, KBVisibilitySelectedAccounts, s.a)
	sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.tenant, s.user)

	_, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	repo.files["skills/two/SKILL.md"] = skillMD("two", "y")
	_, err = SyncSkillSource(sc, sourceId)
	require.NoError(t, err)

	switchedOn := func() map[string][]string {
		var rows []struct {
			Name    string `db:"name"`
			Account string `db:"account_id"`
			Enabled bool   `db:"enabled"`
		}
		require.NoError(t, db.Select(&rows, `
			SELECT kb.name, o.account_id::text AS account_id, o.enabled FROM llm_kb_account_overrides o
			JOIN llm_knowledgebases kb ON kb.id = o.kb_id WHERE kb.source_id = $1`, sourceId))
		out := map[string][]string{}
		for _, r := range rows {
			state := "off"
			if r.Enabled {
				state = "on"
			}
			out[r.Name] = append(out[r.Name], r.Account+":"+state)
		}
		for k := range out {
			sort.Strings(out[k])
		}
		return out
	}
	assert.Equal(t, map[string][]string{"one": {s.a + ":on"}, "two": {s.a + ":on"}}, switchedOn())

	// Admin switches "two" off for B (an organisation-wide override), then the
	// source moves to B only.
	var twoId string
	require.NoError(t, db.Get(&twoId, `SELECT id FROM llm_knowledgebases WHERE source_id = $1 AND name = 'two'`, sourceId))
	mustExec(t, db, `INSERT INTO llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled) VALUES ($1, $2, $3, false)`, twoId, s.b, s.tenant)
	_, err = UpdateSkillSourceVisibility(sc, sourceId, KBVisibilitySelectedAccounts, []string{s.b})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"one": {s.b + ":on"}, "two": {s.b + ":off"}}, switchedOn())

	_, err = UpdateSkillSourceVisibility(sc, sourceId, KBVisibilityOrganisation, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"two": {s.b + ":off"}}, switchedOn())
	var vis []string
	require.NoError(t, db.Select(&vis, `SELECT DISTINCT visibility FROM llm_knowledgebases WHERE source_id = $1`, sourceId))
	assert.Equal(t, []string{KBVisibilityOrganisation}, vis)
}

// A visibility change made while a sync runs also reaches the skills that
// sync adds (review, #41652): they used to keep the visibility read when the
// sync started, organisation-wide with no per-account rows.
func TestSkillSourceVisibilityChangedDuringSync_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{"skills/one/SKILL.md": skillMD("one", "x")}}
	stubSkillSourceFetch(t, repo)
	sourceId := insertSkillSource(t, db, s, KBVisibilityOrganisation)
	sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.tenant, s.user)
	repo.onFetch = func() {
		_, err := UpdateSkillSourceVisibility(sc, sourceId, KBVisibilitySelectedAccounts, []string{s.a})
		require.NoError(t, err)
	}

	_, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	var vis string
	require.NoError(t, db.Get(&vis, `SELECT visibility FROM llm_knowledgebases WHERE source_id = $1 AND name = 'one'`, sourceId))
	assert.Equal(t, KBVisibilitySelectedAccounts, vis)
	var on []string
	require.NoError(t, db.Select(&on, `
		SELECT o.account_id::text FROM llm_kb_account_overrides o JOIN llm_knowledgebases kb ON kb.id = o.kb_id
		WHERE kb.source_id = $1 AND o.enabled`, sourceId))
	assert.Equal(t, []string{s.a}, on)
}

// load_skills' "<skill>/<path>" reads a stored file of a skill the account
// can see, and nothing else.
func TestGetSkillFile_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{
		"skills/promql/SKILL.md":         skillMD("promql", "x"),
		"skills/promql/references/fn.md": "rate()",
	}}
	stubSkillSourceFetch(t, repo)
	sourceId := insertSkillSource(t, db, s, KBVisibilitySelectedAccounts, s.a)
	sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.tenant, s.user)
	_, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	mustExec(t, db, `UPDATE llm_knowledgebases SET status = 'active' WHERE source_id = $1`, sourceId)
	ClearKnowledgeBaseCaches()

	f, err := GetSkillFile(sc, s.a, "PromQL", "references/fn.md")
	require.NoError(t, err)
	require.NotNil(t, f)
	assert.Equal(t, "rate()", f.Content)
	assert.Equal(t, "promql", f.SkillName)

	_, err = GetSkillFile(sc, s.a, "promql", "references/missing.md")
	assert.True(t, errors.Is(err, ErrSkillFileNotFound))
	f, err = GetSkillFile(sc, s.b, "promql", "references/fn.md")
	require.NoError(t, err)
	assert.Nil(t, f, "B is not a selected account")
	f, err = GetSkillFile(sc, s.a, "promql", "../etc/passwd")
	require.NoError(t, err)
	assert.Nil(t, f, "unsafe path")

	var meta SkillMeta
	var raw []byte
	require.NoError(t, db.Get(&raw, `SELECT skill_meta FROM llm_knowledgebases WHERE source_id = $1`, sourceId))
	require.NoError(t, json.Unmarshal(raw, &meta))
	assert.Equal(t, "c1", meta.Commit)
	assert.Equal(t, []SkillFileInfo{{Path: "references/fn.md", Size: 6}}, meta.Files)

	// The whole folder, for copying into a workspace: same visibility as a file.
	folder, err := GetSkillFolder(sc, s.a, "PromQL")
	require.NoError(t, err)
	require.NotNil(t, folder)
	assert.Equal(t, "promql", folder.SkillName)
	assert.Equal(t, map[string]string{"references/fn.md": "rate()"}, folder.Files)
	stored := listSynced(t, db, sourceId)["skills/promql"].Data
	assert.Equal(t, stored, folder.SkillMD, "the workspace SKILL.md is the stored text as is, the same bytes a large-skill load writes")
	assert.True(t, strings.HasPrefix(folder.SkillMD, "---\nname: \"promql\"\n"), folder.SkillMD)
	assert.NotEmpty(t, folder.ContentSHA)
	folder, err = GetSkillFolder(sc, s.b, "promql")
	require.NoError(t, err)
	assert.Nil(t, folder, "B is not a selected account")
	seedKB(t, db, s.tenant, kbSeedRow{name: "hand-made", enabled: true, visibility: "organisation"})
	folder, err = GetSkillFolder(sc, s.a, "hand-made")
	require.NoError(t, err)
	assert.Nil(t, folder, "a hand-made skill has no folder")
}

// A skill with scripts says, in its file list, how to get them into the
// workspace; one with only references doesn't. The list comes off again for
// the workspace copy of SKILL.md.
func TestSkillFilesHeader(t *testing.T) {
	refs := skillFilesHeader("promql", map[string]string{"references/a.md": "x"})
	assert.NotContains(t, refs, "To run its scripts")
	withScript := skillFilesHeader("promql", map[string]string{"references/a.md": "x", "scripts/check.py": "print(1)"})
	assert.Contains(t, withScript, `- scripts/check.py`)
	assert.Contains(t, withScript, `To run its scripts, first load skill_name "promql/" (trailing slash)`)
	assert.Equal(t, "---\nname: \"promql\"\ndescription: \"say \\\"hi\\\"\"\n---\n\n", skillFrontmatter("promql", `say "hi"`))
	assert.Empty(t, skillFilesHeader("promql", nil))
}

// The scheduled sync picks up a never-synced source and one a crashed run left
// "syncing", and leaves alone a source synced or attempted within the interval.
func TestSyncDueSkillSources_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	// One skill in each source's folder: a folder missing from the repo fails
	// the sync.
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{
		"a/one/SKILL.md": skillMD("due-a", "x"), "b/one/SKILL.md": skillMD("due-b", "x"),
		"c/one/SKILL.md": skillMD("due-c", "x"), "d/one/SKILL.md": skillMD("due-d", "x"),
	}}
	stubSkillSourceFetch(t, repo)

	add := func(folder, status, updatedAgo string, syncedAgo *string) string {
		var id string
		synced := "NULL"
		if syncedAgo != nil {
			synced = "NOW() - INTERVAL '" + *syncedAgo + "'"
		}
		require.NoError(t, db.Get(&id, `
			INSERT INTO llm_kb_skill_sources (tenant_id, repo, folder, status, created_by, updated_at, last_synced_at)
			VALUES ($1, 'acme/skills', $2, $3, $4, NOW() - $5::interval, `+synced+`) RETURNING id`,
			s.tenant, folder, status, s.user, updatedAgo))
		return id
	}
	recent := "1 minute"
	neverSynced := add("a", SkillSourceStatusPending, "2 days", nil)
	crashed := add("b", SkillSourceStatusSyncing, "2 hours", nil)
	freshError := add("c", SkillSourceStatusError, "1 minute", nil)
	freshlySynced := add("d", SkillSourceStatusActive, "1 minute", &recent)

	require.NoError(t, SyncDueSkillSources())
	status := func(id string) string {
		var st string
		require.NoError(t, db.Get(&st, `SELECT status FROM llm_kb_skill_sources WHERE id = $1`, id))
		return st
	}
	assert.Equal(t, SkillSourceStatusActive, status(neverSynced))
	assert.Equal(t, SkillSourceStatusActive, status(crashed))
	assert.Equal(t, SkillSourceStatusError, status(freshError), "an error is retried after the interval, not every tick")
	assert.Equal(t, SkillSourceStatusActive, status(freshlySynced))
	var skills int
	require.NoError(t, db.Get(&skills, `SELECT count(*) FROM llm_knowledgebases WHERE source_id = $1`, freshlySynced))
	assert.Zero(t, skills, "a freshly synced source is not synced again")
}

// A scheduled sync of a repository still at the synced commit downloads
// nothing and keeps the stored report; a new commit is downloaded, and Sync now
// always downloads.
func TestScheduledSyncSkipsUnchangedRepository_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{"skills/one/SKILL.md": skillMD("one", "x")}}
	stubSkillSourceFetch(t, repo)
	sourceId := insertSkillSource(t, db, s, KBVisibilityOrganisation)
	sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.tenant, s.user)

	_, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	require.Equal(t, 1, repo.downloads)

	makeDue := func() {
		_, err := db.Exec(`UPDATE llm_kb_skill_sources SET last_synced_at = NOW() - INTERVAL '2 days', updated_at = NOW() - INTERVAL '2 days' WHERE id = $1`, sourceId)
		require.NoError(t, err)
	}
	stored := func() (report SkillSyncReport, recent bool) {
		var row struct {
			Report []byte `db:"sync_report"`
			Recent bool   `db:"recent"`
		}
		require.NoError(t, db.Get(&row, `SELECT sync_report, last_synced_at > NOW() - INTERVAL '1 minute' AS recent FROM llm_kb_skill_sources WHERE id = $1`, sourceId))
		require.NoError(t, json.Unmarshal(row.Report, &report))
		return report, row.Recent
	}

	makeDue()
	require.NoError(t, SyncDueSkillSources())
	assert.Equal(t, 2, repo.calls)
	assert.Equal(t, 1, repo.downloads, "unchanged repository is not downloaded")
	report, recent := stored()
	assert.Equal(t, 1, report.Found, "the last full report is kept")
	assert.False(t, report.Unchanged)
	assert.True(t, recent, "last synced moves on")

	repo.commit = "c2"
	repo.files["skills/two/SKILL.md"] = skillMD("two", "y")
	makeDue()
	require.NoError(t, SyncDueSkillSources())
	assert.Equal(t, 2, repo.downloads, "a new commit is downloaded")
	assert.Len(t, listSynced(t, db, sourceId), 2)

	_, err = SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	assert.Equal(t, 3, repo.downloads, "Sync now always downloads")
}

// A new commit that touches no skill (a README edit) rewrites no skill row,
// re-indexes nothing and leaves the knowledge caches alone.
func TestSyncLeavesUntouchedSkillsAlone_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{
		"README.md":                      "v1",
		"skills/promql/SKILL.md":         skillMD("promql", "Write PromQL."),
		"skills/promql/references/fn.md": "rate()",
		"skills/loki/SKILL.md":           skillMD("loki", "Query Loki."),
	}}
	indexed := stubSkillSourceFetch(t, repo)
	orgCacheKept := seedOrgKnowledgeCache(t)
	sourceId := insertSkillSource(t, db, s, KBVisibilityOrganisation)
	sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.tenant, s.user)

	_, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	require.Len(t, *indexed, 2)
	require.False(t, orgCacheKept(), "the first sync clears the organisation caches")
	orgCacheKept = seedOrgKnowledgeCache(t)
	_, err = db.Exec(`UPDATE llm_knowledgebases SET status = 'active', updated_at = '2020-01-01' WHERE source_id = $1`, sourceId)
	require.NoError(t, err)

	repo.commit, repo.files["README.md"] = "c2", "v2"
	report, err := SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	assert.Equal(t, 2, report.Found)
	assert.Zero(t, report.Updated+report.Added+report.Archived)
	assert.Len(t, *indexed, 2, "nothing re-indexed")
	assert.True(t, orgCacheKept(), "caches untouched")
	var touched int
	require.NoError(t, db.Get(&touched, `SELECT count(*) FROM llm_knowledgebases WHERE source_id = $1 AND updated_at > '2020-01-01'`, sourceId))
	assert.Zero(t, touched, "no skill row rewritten")
	var commit string
	require.NoError(t, db.Get(&commit, `SELECT skill_meta->>'commit' FROM llm_knowledgebases WHERE source_id = $1 AND name = 'promql'`, sourceId))
	assert.Equal(t, "c1", commit, "a skill keeps the commit it was taken at")

	// Changing one reference file updates that skill only, without re-indexing
	// (its SKILL.md text is the same).
	repo.commit, repo.files["skills/promql/references/fn.md"] = "c3", "rate() and irate()"
	_, err = SyncSkillSource(sc, sourceId)
	require.NoError(t, err)
	require.NoError(t, db.Get(&touched, `SELECT count(*) FROM llm_knowledgebases WHERE source_id = $1 AND updated_at > '2020-01-01'`, sourceId))
	assert.Equal(t, 1, touched)
	assert.Len(t, *indexed, 2)
	assert.False(t, orgCacheKept(), "an updated skill clears the caches")
}

// Without a GitHub token a source is synced on schedule at most hourly; with
// one it follows the normal interval.
func TestScheduledSyncIsHourlyWithoutGitHubToken_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	repo := &fakeSkillRepo{t: t, commit: "c1", files: map[string]string{"skills/one/SKILL.md": skillMD("one", "x")}}
	stubSkillSourceFetch(t, repo)
	oldInterval, oldResolver := config.Config.KBSyncIntervalMinutes, GitHubTokenResolver
	t.Cleanup(func() { config.Config.KBSyncIntervalMinutes, GitHubTokenResolver = oldInterval, oldResolver })
	config.Config.KBSyncIntervalMinutes = 10
	sourceId := insertSkillSource(t, db, s, KBVisibilityOrganisation)
	_, err := db.Exec(`UPDATE llm_kb_skill_sources SET status = 'active', last_commit = 'c0',
		last_synced_at = NOW() - INTERVAL '30 minutes', updated_at = NOW() - INTERVAL '30 minutes' WHERE id = $1`, sourceId)
	require.NoError(t, err)

	GitHubTokenResolver = func(context.Context, string) (string, string, error) { return "", "", nil }
	require.NoError(t, SyncDueSkillSources())
	assert.Zero(t, repo.calls, "anonymous: 30 minutes is not yet due")

	GitHubTokenResolver = func(_ context.Context, tenant string) (string, string, error) {
		if tenant == s.tenant {
			return "", "tok", nil
		}
		return "", "", nil
	}
	require.NoError(t, SyncDueSkillSources())
	assert.Equal(t, 1, repo.calls, "with a token: due after the normal interval")
}

// A conversation asks before running code once it has asked load_skills for
// a skill folder ("<skill>/"), in any turn and whatever came of it; another
// conversation, another account or a plain skill load doesn't count.
func TestConversationHasSkillScripts_DB(t *testing.T) {
	db := kbTestDB(t)
	s := seedKBTenantCommitted(t, db)
	conv, other := uuid.NewString(), uuid.NewString()
	for _, c := range []string{conv, other} {
		mustExec(t, db, `INSERT INTO llm_conversations (id, account_id, tenant_id, status, created_at, updated_at) VALUES ($1, $2, $3, 'IN_PROGRESS', NOW(), NOW())`, c, s.a, s.tenant)
	}
	call := func(conversation, account, params, status string) {
		mustExec(t, db, `INSERT INTO llm_conversation_tool_calls (conversation_id, tool_id, tool_name, parameters, account_id, status)
			VALUES ($1, $2, 'load_skills', $3, $4, $5)`, conversation, uuid.NewString(), params, account, status)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM llm_conversation_tool_calls WHERE conversation_id IN ($1, $2)`, conv, other)
		_, _ = db.Exec(`DELETE FROM llm_conversations WHERE id IN ($1, $2)`, conv, other)
	})

	call(conv, s.a, `{"skill_name":"promql"}`, "success")
	call(conv, s.a, `{"skill_name":"promql/references/a.md"}`, "success")
	assert.False(t, ConversationHasSkillScripts(s.a, conv), "loading a skill or one of its files copies nothing")

	call(conv, s.a, `{"skill_name":"promql/"}`, "in_progress")
	assert.True(t, ConversationHasSkillScripts(s.a, conv), "recorded before the copy finishes")
	assert.False(t, ConversationHasSkillScripts(s.b, conv), "another account")
	assert.False(t, ConversationHasSkillScripts(s.a, other), "another conversation")

	call(other, s.a, `{"skills":"x/"}`, "error")
	assert.True(t, ConversationHasSkillScripts(s.a, other), "a legacy alias, and a failed copy, still count")
	assert.False(t, ConversationHasSkillScripts(s.a, "not-a-uuid"))
}
