package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// Skill sources (#41513 / #41514): an admin adds a repo + folder + branch for
// the organisation; every Agent Skills folder found there becomes one
// organisation knowledge base row. Those rows are ordinary text skills
// (kb_type 'manual'), so /skill-name, load_skills, always-load and the skill
// suggestions all work on them, with three differences enforced here and in
// the update / visibility / delete paths: they are read-only, the source
// decides which accounts see them, and a sync only re-embeds a skill whose
// SKILL.md changed.

const (
	SkillSourceProviderGitHub = "github"

	SkillSourceStatusPending = "pending"
	SkillSourceStatusSyncing = "syncing"
	SkillSourceStatusActive  = "active"
	SkillSourceStatusError   = "error"
)

var (
	errSkillSourceNotFound = errors.New("skill source not found")
	errSkillSourceRepo     = errors.New("repo must be a GitHub repository: owner/name or https://github.com/owner/name")
	// ErrSyncedSkillReadOnly is returned for a write a synced skill doesn't allow.
	ErrSyncedSkillReadOnly = errors.New("this skill is synced from a repository: change it there, or change the skill source")
)

// SkillSource is one row of llm_kb_skill_sources.
type SkillSource struct {
	Id           string           `json:"id" db:"id"`
	TenantId     string           `json:"tenant_id" db:"tenant_id"`
	Provider     string           `json:"provider" db:"provider"`
	Repo         string           `json:"repo" db:"repo"`
	Folder       string           `json:"folder" db:"folder"`
	Branch       string           `json:"branch" db:"branch"`
	Visibility   string           `json:"visibility" db:"visibility"`
	AccountIds   pq.StringArray   `json:"account_ids" db:"account_ids"`
	LastCommit   *string          `json:"last_commit,omitempty" db:"last_commit"`
	LastSyncedAt *time.Time       `json:"last_synced_at,omitempty" db:"last_synced_at"`
	Status       string           `json:"status" db:"status"`
	ErrorMessage *string          `json:"error_message,omitempty" db:"error_message"`
	SyncReport   *json.RawMessage `json:"sync_report,omitempty" db:"sync_report"`
	SkillCount   int              `json:"skill_count" db:"skill_count"`
	CreatedBy    *string          `json:"-" db:"created_by"`
	CreatedAt    time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at" db:"updated_at"`
}

// SkillSyncReport is what one sync did, stored on the source for admins.
type SkillSyncReport struct {
	Commit   string         `json:"commit"`
	Found    int            `json:"found"`
	Added    int            `json:"added"`
	Updated  int            `json:"updated"`
	Archived int            `json:"archived"`
	Skipped  []SkippedSkill `json:"skipped,omitempty"`
	// Unchanged: the repository was still at the synced commit, so nothing
	// was downloaded and the stored report stands.
	Unchanged bool `json:"unchanged,omitempty"`
}

// SkillSourceFetcher resolves ref (a branch, or "" for the default branch) to
// a commit and returns the repo's gzipped tarball at it as a stream, or no
// tarball when the commit is knownCommit ("" always downloads).
type SkillSourceFetcher func(ctx context.Context, tenantId, repo, ref, knownCommit string) (commit string, tarball io.ReadCloser, err error)

// skillSyncSlots caps concurrent syncs per server. A sync holds a repository's
// skill files in memory while it reconciles them; scheduled syncs already run
// one at a time, so this bounds Sync now and newly added sources.
var skillSyncSlots = make(chan struct{}, 2)

// FetchSkillSource is set by the api package (it needs the tenant's GitHub
// integration, which lives in a package this one can't import).
var FetchSkillSource SkillSourceFetcher

var skillSourceSelect = `
	SELECT s.id, s.tenant_id, s.provider, s.repo, s.folder, s.branch, s.visibility, s.account_ids,
	       s.last_commit, s.last_synced_at, s.status, s.error_message, s.sync_report, s.created_by,
	       s.created_at, s.updated_at,
	       (SELECT count(*) FROM llm_knowledgebases kb WHERE kb.source_id = s.id AND kb.status <> 'archived') AS skill_count
	FROM llm_kb_skill_sources s`

var githubRepoRe = regexp.MustCompile(`^(?:https?://github\.com/)?([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// NormalizeGitHubRepo turns "owner/name" or a github.com URL into "owner/name".
func NormalizeGitHubRepo(repo string) (string, error) {
	m := githubRepoRe.FindStringSubmatch(strings.TrimSpace(repo))
	if m == nil || strings.Trim(m[1], ".") == "" || strings.Trim(m[2], ".") == "" {
		return "", errSkillSourceRepo
	}
	return m[1] + "/" + m[2], nil
}

// CreateSkillSource adds a source for the caller's organisation and starts its
// first sync in the background.
func CreateSkillSource(sc *security.RequestContext, src SkillSource) (SkillSource, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()
	if !canManageOrgKBs(sc, tenantId, security.SecurityAccessTypeCreate) {
		return SkillSource{}, errors.New(errKBOrgAdminOnly)
	}
	repo, err := NormalizeGitHubRepo(src.Repo)
	if err != nil {
		return SkillSource{}, err
	}
	folder := strings.Trim(strings.TrimSpace(src.Folder), "/")
	if folder != "" && !safeSkillPath(folder) {
		return SkillSource{}, errors.New("folder must be a path inside the repository")
	}
	visibility := src.Visibility
	if visibility == "" {
		visibility = KBVisibilityOrganisation
	}
	if visibility != KBVisibilityOrganisation && visibility != KBVisibilitySelectedAccounts {
		return SkillSource{}, errors.New("visibility must be organisation or selected_accounts")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return SkillSource{}, err
	}
	accounts, err := orgKBAccounts(dbms, tenantId, visibility, src.AccountIds)
	if err != nil {
		return SkillSource{}, err
	}
	if accounts == nil {
		accounts = []string{}
	}
	userId := sc.GetSecurityContext().GetUserId()
	var id string
	err = dbms.Db.Get(&id, `
		INSERT INTO llm_kb_skill_sources (tenant_id, provider, repo, folder, branch, visibility, account_ids, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7::uuid[], NULLIF($8, '')::uuid, NULLIF($8, '')::uuid)
		RETURNING id`,
		tenantId, SkillSourceProviderGitHub, repo, folder, strings.TrimSpace(src.Branch), visibility, pq.Array(accounts), userId)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return SkillSource{}, errors.New("this repository folder and branch is already a skill source")
		}
		return SkillSource{}, fmt.Errorf("skill source: create: %w", err)
	}
	created, err := getSkillSource(dbms, tenantId, id)
	if err != nil {
		return SkillSource{}, err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("skill source: first sync panicked", "source_id", id, "panic", r, "stack", string(debug.Stack()))
				_, _ = dbms.Db.Exec(`UPDATE llm_kb_skill_sources SET status = 'error', error_message = 'sync failed unexpectedly', updated_at = NOW() WHERE id = $1`, id)
			}
		}()
		bg := security.NewRequestContextForTenantAdminWithUser(context.Background(), tenantId, userId)
		if _, err := SyncSkillSource(bg, id); err != nil {
			slog.Warn("skill source: first sync failed", "source_id", id, "error", err)
			// A failure before the sync claimed the source leaves it pending,
			// which the UI would poll forever.
			if _, werr := dbms.Db.Exec(`UPDATE llm_kb_skill_sources SET status = 'error', error_message = $2, updated_at = NOW() WHERE id = $1 AND status = 'pending'`,
				id, err.Error()); werr != nil {
				slog.Error("skill source: status write failed", "source_id", id, "error", werr)
			}
		}
	}()
	return created, nil
}

// ListSkillSources returns the caller's organisation's sources.
func ListSkillSources(sc *security.RequestContext) ([]SkillSource, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()
	if !canManageOrgKBs(sc, tenantId, security.SecurityAccessTypeRead) {
		return nil, errors.New(errKBOrgAdminOnly)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	out := []SkillSource{}
	if err := dbms.Db.Select(&out, skillSourceSelect+` WHERE s.tenant_id = $1 ORDER BY s.created_at`, tenantId); err != nil {
		return nil, fmt.Errorf("skill source: list: %w", err)
	}
	return out, nil
}

func getSkillSource(dbms *common.DatabaseManager, tenantId, id string) (SkillSource, error) {
	var src SkillSource
	err := dbms.Db.Get(&src, skillSourceSelect+` WHERE s.id = $1 AND s.tenant_id = $2`, id, tenantId)
	if errors.Is(err, sql.ErrNoRows) {
		return SkillSource{}, errSkillSourceNotFound
	}
	return src, err
}

// UpdateSkillSourceVisibility changes which accounts see the source's skills;
// every skill of the source follows.
func UpdateSkillSourceVisibility(sc *security.RequestContext, id, visibility string, accountIds []string) (SkillSource, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()
	if !canManageOrgKBs(sc, tenantId, security.SecurityAccessTypeUpdate) {
		return SkillSource{}, errors.New(errKBOrgAdminOnly)
	}
	if visibility != KBVisibilityOrganisation && visibility != KBVisibilitySelectedAccounts {
		return SkillSource{}, errors.New("visibility must be organisation or selected_accounts")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return SkillSource{}, err
	}
	src, err := getSkillSource(dbms, tenantId, id)
	if err != nil {
		return SkillSource{}, err
	}
	accounts, err := orgKBAccounts(dbms, tenantId, visibility, accountIds)
	if err != nil {
		return SkillSource{}, err
	}
	if accounts == nil {
		accounts = []string{}
	}
	src.Visibility, src.AccountIds = visibility, accounts
	userId := sc.GetSecurityContext().GetUserId()
	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(`UPDATE llm_kb_skill_sources SET visibility = $2, account_ids = $3::uuid[], updated_by = NULLIF($4, '')::uuid, updated_at = NOW() WHERE id = $1`,
			id, visibility, pq.Array(accounts), userId); err != nil {
			return nil, err
		}
		var kbIds []string
		if err := tx.Select(&kbIds, `SELECT id FROM llm_knowledgebases WHERE source_id = $1`, id); err != nil {
			return nil, err
		}
		for _, kbId := range kbIds {
			if err := applySourceVisibility(tx, src, kbId, userId); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		return SkillSource{}, fmt.Errorf("skill source: update visibility: %w", err)
	}
	invalidateOrgKnowledgeCaches(tenantId)
	return getSkillSource(dbms, tenantId, id)
}

// applySourceVisibility makes one synced skill follow its source: the row's
// visibility, and for selected_accounts a switch-on row per selected account.
// A switch-off an admin set on one skill for one account is kept; switch-ons
// for accounts no longer selected are removed.
func applySourceVisibility(tx *sqlx.Tx, src SkillSource, kbId, userId string) error {
	if _, err := tx.Exec(`UPDATE llm_knowledgebases SET visibility = $2, updated_at = NOW() WHERE id = $1`, kbId, src.Visibility); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM llm_kb_account_overrides WHERE kb_id = $1 AND enabled AND NOT (account_id = ANY($2::uuid[]))`,
		kbId, pq.Array(selectedAccounts(src))); err != nil {
		return err
	}
	_, err := tx.Exec(`
		INSERT INTO llm_kb_account_overrides (kb_id, account_id, tenant_id, enabled, updated_by, updated_at)
		SELECT $1, unnest($2::uuid[]), $3, true, NULLIF($4, '')::uuid, NOW()
		ON CONFLICT (kb_id, account_id) DO NOTHING`, kbId, pq.Array(selectedAccounts(src)), src.TenantId, userId)
	return err
}

func selectedAccounts(src SkillSource) []string {
	if src.Visibility != KBVisibilitySelectedAccounts {
		return []string{}
	}
	return src.AccountIds
}

// DeleteSkillSource removes a source and its skills. Each skill goes through
// the normal knowledge base delete, so its search collection, agent links and
// caches are cleaned up.
func DeleteSkillSource(sc *security.RequestContext, id string) error {
	tenantId := sc.GetSecurityContext().GetTenantId()
	if !canManageOrgKBs(sc, tenantId, security.SecurityAccessTypeDelete) {
		return errors.New(errKBOrgAdminOnly)
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}
	if _, err := getSkillSource(dbms, tenantId, id); err != nil {
		return err
	}
	var kbIds []string
	if err := dbms.Db.Select(&kbIds, `SELECT id FROM llm_knowledgebases WHERE source_id = $1`, id); err != nil {
		return err
	}
	for _, kbId := range kbIds {
		if err := deleteKnowledgebase(sc, "", kbId, true); err != nil {
			return fmt.Errorf("skill source: delete skill %s: %w", kbId, err)
		}
	}
	if _, err := dbms.Db.Exec(`DELETE FROM llm_kb_skill_sources WHERE id = $1 AND tenant_id = $2`, id, tenantId); err != nil {
		return fmt.Errorf("skill source: delete: %w", err)
	}
	return nil
}

// SyncSkillSource downloads the source and reconciles its skills: adds new
// ones, updates changed ones (re-embedding only when SKILL.md changed),
// archives ones gone from the repo. A skill whose name another skill of the
// organisation already uses is skipped and listed in the report. It always
// downloads (Sync now); scheduled syncs skip a repository still at the synced
// commit.
func SyncSkillSource(sc *security.RequestContext, id string) (SkillSyncReport, error) {
	return syncSkillSourceByID(sc, id, true)
}

func syncSkillSourceByID(sc *security.RequestContext, id string, force bool) (SkillSyncReport, error) {
	tenantId := sc.GetSecurityContext().GetTenantId()
	if !canManageOrgKBs(sc, tenantId, security.SecurityAccessTypeUpdate) {
		return SkillSyncReport{}, errors.New(errKBOrgAdminOnly)
	}
	if FetchSkillSource == nil {
		return SkillSyncReport{}, errors.New("skill sources are not configured on this server")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return SkillSyncReport{}, err
	}
	src, err := getSkillSource(dbms, tenantId, id)
	if err != nil {
		return SkillSyncReport{}, err
	}
	// One sync at a time per source; a stale "syncing" (crashed run) is retaken.
	res, err := dbms.Db.Exec(`
		UPDATE llm_kb_skill_sources SET status = 'syncing', updated_at = NOW()
		WHERE id = $1 AND (status <> 'syncing' OR updated_at < NOW() - INTERVAL '30 minutes')`, id)
	if err != nil {
		return SkillSyncReport{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SkillSyncReport{}, errors.New("a sync of this source is already running")
	}

	knownCommit := ""
	if !force && src.Status == SkillSourceStatusActive && src.LastCommit != nil && !sourceHasFailedSkills(dbms, id) {
		knownCommit = *src.LastCommit
	}
	report, syncErr := syncSkillSource(sc, dbms, src, knownCommit)
	reportJSON, _ := json.Marshal(report)
	// A failed status write is logged: the source then shows "syncing" until
	// the 30-minute retake.
	logWrite := func(_ sql.Result, err error) {
		if err != nil {
			slog.Error("skill source: status write failed", "source_id", id, "error", err)
		}
	}
	if syncErr != nil {
		logWrite(dbms.Db.Exec(`UPDATE llm_kb_skill_sources SET status = 'error', error_message = $2, updated_at = NOW() WHERE id = $1`, id, syncErr.Error()))
		return report, syncErr
	}
	if report.Unchanged {
		logWrite(dbms.Db.Exec(`UPDATE llm_kb_skill_sources SET status = 'active', last_synced_at = NOW(), updated_at = NOW() WHERE id = $1`, id))
		return report, nil
	}
	logWrite(dbms.Db.Exec(`
		UPDATE llm_kb_skill_sources
		SET status = 'active', error_message = NULL, last_commit = $2, last_synced_at = NOW(), sync_report = $3, updated_at = NOW()
		WHERE id = $1`, id, report.Commit, reportJSON))
	return report, nil
}

type syncedRow struct {
	Id         string `db:"id"`
	Name       string `db:"name"`
	Status     string `db:"status"`
	SourcePath string `db:"source_path"`
	BodySHA    string `db:"body_sha"`
	ContentSHA string `db:"content_sha"`
}

func syncSkillSource(sc *security.RequestContext, dbms *common.DatabaseManager, src SkillSource, knownCommit string) (SkillSyncReport, error) {
	ctx, cancel := context.WithTimeout(sc.GetContext(), 5*time.Minute)
	defer cancel()
	select {
	case skillSyncSlots <- struct{}{}:
		defer func() { <-skillSyncSlots }()
	case <-ctx.Done():
		return SkillSyncReport{}, fmt.Errorf("waiting for another sync to finish: %w", ctx.Err())
	}
	commit, tarball, err := FetchSkillSource(ctx, src.TenantId, src.Repo, src.Branch, knownCommit)
	if err != nil {
		return SkillSyncReport{}, fmt.Errorf("download %s: %w", src.Repo, err)
	}
	if tarball == nil {
		return SkillSyncReport{Commit: commit, Unchanged: true}, nil
	}
	skills, skipped, err := DiscoverSkillsInTarball(tarball, src.Folder)
	_ = tarball.Close()
	if err != nil {
		return SkillSyncReport{}, err
	}
	report := SkillSyncReport{Commit: commit, Found: len(skills), Skipped: skipped}

	var existing []syncedRow
	if err := dbms.Db.Select(&existing, `
		SELECT id, name, status, COALESCE(skill_meta->>'source_path', '') AS source_path, COALESCE(skill_meta->>'body_sha', '') AS body_sha,
		       COALESCE(skill_meta->>'content_sha', '') AS content_sha
		FROM llm_knowledgebases WHERE source_id = $1 ORDER BY updated_at`, src.Id); err != nil {
		return report, err
	}
	byPath := map[string]syncedRow{}
	for _, r := range existing {
		byPath[r.SourcePath] = r
	}
	// A skill whose folder moved in the repo keeps its row: a row this sync
	// would archive (or already archived) still holds the skill's name, so a
	// new row would be refused as a name clash, on every sync after it too.
	inRepo := map[string]bool{}
	for _, pkg := range skills {
		inRepo[pkg.Meta.SourcePath] = true
	}
	// Only rows whose path left the repo: one still there is matched by path
	// (archived or not), and must not be taken twice. existing is ordered by
	// updated_at, so of two such rows with one name the latest is taken.
	movable := map[string]syncedRow{}
	for _, r := range existing {
		if !inRepo[r.SourcePath] {
			movable[strings.ToLower(r.Name)] = r
		}
	}
	moved := map[string]bool{}

	userId := sc.GetSecurityContext().GetUserId()
	owner := kbOwner{TenantId: src.TenantId}
	type embedJob struct{ id, body string }
	var embed []embedJob
	seenNames := map[string]string{}
	found := map[string]bool{}
	// changed is set by any add, update or archive; a sync that changed no
	// skill leaves the knowledge caches alone.
	changed := false
	// Rows written so far are indexed and the caches cleared even when the
	// sync stops on an error: they were set to processing, and nothing else
	// would ever submit them.
	defer func() {
		if changed {
			invalidateOrgKnowledgeCaches(src.TenantId)
		}
		for _, job := range embed {
			if err := submitSkillIndexing(sc, owner, job.id, job.body); err != nil {
				_ = updateKBStatusError(dbms, job.id, fmt.Sprintf("failed to submit indexing: %v", err))
			}
		}
	}()

	for _, pkg := range skills {
		if pkg.Body == "" {
			// A header-only SKILL.md: the description is all there is to read.
			pkg.Body = pkg.Description
		}
		// Stored like the repo's SKILL.md: frontmatter, then the file list,
		// then the instructions. The workspace copy is this text as is, so a
		// large-skill load and a folder copy write the same SKILL.md.
		pkg.Body = skillFrontmatter(pkg.Name, pkg.Description) + skillFilesHeader(pkg.Name, pkg.Files) + pkg.Body
		path := pkg.Meta.SourcePath
		found[path] = true
		lname := strings.ToLower(pkg.Name)
		if other, dup := seenNames[lname]; dup {
			report.Skipped = append(report.Skipped, SkippedSkill{SourcePath: path, Reason: fmt.Sprintf("name %q is already used by %s in this source", pkg.Name, other)})
			continue
		}
		seenNames[lname] = path

		bodySum := sha256.Sum256([]byte(pkg.Body))
		bodySHA := hex.EncodeToString(bodySum[:])
		filesJSON, err := json.Marshal(pkg.Files)
		if err != nil {
			return report, err
		}
		// contentSHA covers everything stored for the skill except the commit:
		// a skill no edit touched keeps its row as is, including the commit it
		// was taken at (its links still show the same content).
		unversioned, err := json.Marshal(pkg.Meta)
		if err != nil {
			return report, err
		}
		contentSum := sha256.Sum256([]byte(strings.Join([]string{pkg.Name, pkg.Description, bodySHA, string(filesJSON), string(unversioned)}, "\x00")))
		contentSHA := hex.EncodeToString(contentSum[:])
		meta := pkg.Meta
		meta.Commit = commit
		metaJSON, err := json.Marshal(struct {
			SkillMeta
			BodySHA    string `json:"body_sha"`
			ContentSHA string `json:"content_sha"`
		}{meta, bodySHA, contentSHA})
		if err != nil {
			return report, err
		}

		row, ok := byPath[path]
		if !ok {
			if r, m := movable[lname]; m {
				row, ok = r, true
				delete(movable, lname)
				moved[r.Id] = true
			}
		}
		if ok {
			if row.ContentSHA == contentSHA && strings.EqualFold(row.Name, pkg.Name) &&
				row.Status != string(KBStatusArchived) && row.Status != string(KBStatusError) {
				continue // unchanged: no write, no re-index
			}
			changed = true
			name := pkg.Name
			if !strings.EqualFold(row.Name, name) {
				if taken, err := kbNameTakenExcept(dbms, src.TenantId, name, row.Id); err != nil {
					return report, err
				} else if taken {
					report.Skipped = append(report.Skipped, SkippedSkill{SourcePath: path, Reason: fmt.Sprintf("renamed to %q, which another skill uses; kept the name %q", name, row.Name)})
					name = row.Name
				}
			}
			reembed := row.BodySHA != bodySHA || row.Status == string(KBStatusArchived) || row.Status == string(KBStatusError)
			status := row.Status
			if reembed {
				status = string(KBStatusProcessing)
			}
			if _, err := dbms.Db.Exec(`
				UPDATE llm_knowledgebases
				SET name = $2, description = $3, data = $4, data_size_bytes = $5, files = $6, skill_meta = $7,
				    status = $8, error_message = NULL, updated_by = NULLIF($9, '')::uuid, updated_at = NOW()
				WHERE id = $1`,
				row.Id, name, pkg.Description, pkg.Body, len(pkg.Body), filesJSON, metaJSON, status, userId); err != nil {
				return report, fmt.Errorf("update skill %s: %w", path, err)
			}
			if reembed {
				embed = append(embed, embedJob{row.Id, pkg.Body})
				report.Updated++
			}
			continue
		}

		if taken, err := kbNameTakenExcept(dbms, src.TenantId, pkg.Name, ""); err != nil {
			return report, err
		} else if taken {
			report.Skipped = append(report.Skipped, SkippedSkill{SourcePath: path, Reason: fmt.Sprintf("name %q is already used by another skill", pkg.Name)})
			continue
		}
		kbId := uuid.NewString()
		_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
			// The source's visibility as it is now, not when the sync started:
			// FOR SHARE waits for a visibility change in progress, and one made
			// after this commits finds the new row.
			cur := src
			if err := tx.QueryRowx(`SELECT visibility, account_ids FROM llm_kb_skill_sources WHERE id = $1 FOR SHARE`, src.Id).
				Scan(&cur.Visibility, &cur.AccountIds); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(`
				INSERT INTO llm_knowledgebases
				(id, tenant_id, account_id, name, description, data, data_format, data_filename, data_size_bytes, status,
				 kb_type, visibility, source_id, files, skill_meta, created_by, updated_by, created_at, updated_at)
				VALUES ($1, $2, NULL, $3, $4, $5, 'text', $6, $7, $8, 'manual', $9, $10, $11, $12,
				        NULLIF($13, '')::uuid, NULLIF($13, '')::uuid, NOW(), NOW())`,
				kbId, src.TenantId, pkg.Name, pkg.Description, pkg.Body, SkillMarkdown, len(pkg.Body), string(KBStatusProcessing),
				cur.Visibility, src.Id, filesJSON, metaJSON, userId); err != nil {
				return nil, err
			}
			return nil, applySourceVisibility(tx, cur, kbId, userId)
		})
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				report.Skipped = append(report.Skipped, SkippedSkill{SourcePath: path, Reason: fmt.Sprintf("name %q is already used by another skill", pkg.Name)})
				continue
			}
			return report, fmt.Errorf("add skill %s: %w", path, err)
		}
		embed = append(embed, embedJob{kbId, pkg.Body})
		report.Added++
		changed = true
	}

	for _, row := range existing {
		if found[row.SourcePath] || moved[row.Id] || row.Status == string(KBStatusArchived) {
			continue
		}
		if err := updateKBStatus(dbms, row.Id, string(KBStatusArchived)); err != nil {
			return report, err
		}
		report.Archived++
		changed = true
	}
	return report, nil
}

// sourceHasFailedSkills reports whether a source has a skill whose indexing
// failed over an hour ago. An unchanged commit would otherwise skip the
// reconcile that re-indexes it, and the skill would stay out of answers until
// the repository changed. The hour spaces out retries of a skill that keeps
// failing: a download and a re-index each time.
func sourceHasFailedSkills(dbms *common.DatabaseManager, sourceId string) bool {
	var failed bool
	if err := dbms.Db.Get(&failed, `
		SELECT EXISTS (SELECT 1 FROM llm_knowledgebases
		               WHERE source_id = $1 AND status = $2 AND updated_at < NOW() - INTERVAL '1 hour')`,
		sourceId, string(KBStatusError)); err != nil {
		return true // reconcile: costs a download, never leaves a skill broken
	}
	return failed
}

// submitSkillIndexing (re)builds a synced skill's search collection from its
// SKILL.md text. Swapped out in tests.
var submitSkillIndexing = func(sc *security.RequestContext, owner kbOwner, kbId, body string) error {
	submitCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	return kbEmbeddingWorkerPool.Submit(submitCtx, func() {
		processKBUpdateEmbeddingsAsync(sc, owner, kbId, body, "text", "skill_sync", nil)
	})
}

// skillFilesHeader lists a synced skill's stored files ahead of its SKILL.md
// text, with the load_skills form that reads each one. SKILL.md usually points
// at its files by path, but not always, and an agent only reading the text
// can't know what else is there. Every way a skill reaches an agent (search,
// /skill-name, always-load) serves this text, so the list goes with it; at the
// start, because /skill-name and excerpts cut long skills at a size limit.
func skillFilesHeader(name string, files map[string]string) string {
	if len(files) == 0 {
		return ""
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	fmt.Fprintf(&b, "%s (read one with load_skills skill_name \"%s/<path>\"):\n", skillFilesHeaderStart, name)
	hasScripts := false
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s\n", p)
		if _, ok := scriptRuntimes[strings.ToLower(path.Ext(p))]; ok {
			hasScripts = true
		}
	}
	if hasScripts {
		fmt.Fprintf(&b, "To run its scripts, first load skill_name \"%s/\" (trailing slash): it copies this folder into the workspace.\n", name)
	}
	b.WriteString(skillFilesHeaderEnd)
	return b.String()
}

// skillFilesHeaderStart and skillFilesHeaderEnd delimit the generated file
// list, so it can be told apart from the skill's own text.
const (
	skillFilesHeaderStart = "Files in this skill"
	skillFilesHeaderEnd   = "---\n\n"
)

// skillFrontmatter is the SKILL.md header for a stored skill: its name and
// description (JSON strings are valid YAML). Scripts that read their own
// SKILL.md find it as in the repo.
func skillFrontmatter(name, description string) string {
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	return "---\nname: " + quote(name) + "\ndescription: " + quote(description) + "\n---\n\n"
}

// kbNameTakenExcept reports whether an organisation knowledge base of the
// tenant other than exceptId already uses name (case-insensitive), the same
// rule the knowledge base form applies.
func kbNameTakenExcept(dbms *common.DatabaseManager, tenantId, name, exceptId string) (bool, error) {
	return kbNameTaken(dbms, kbOwner{TenantId: tenantId}, name, exceptId)
}

const (
	// skillSyncsPerRun caps the syncs one scheduled run starts.
	skillSyncsPerRun = 20
	// anonymousSkillSyncInterval spaces out scheduled syncs for tenants without
	// the GitHub integration. GitHub allows anonymous callers 60 requests an
	// hour per IP and counts even "unchanged" answers, so every 10 minutes
	// would let only about 10 such repositories share one outbound address;
	// hourly lets about 60. Sync now is not affected.
	anonymousSkillSyncInterval = time.Hour
)

// hasGitHubToken reports whether the tenant's syncs authenticate to GitHub.
// When unsure (no resolver, lookup error) it says no, the cautious schedule.
func hasGitHubToken(tenantId string) bool {
	if GitHubTokenResolver == nil {
		return false
	}
	_, token, err := GitHubTokenResolver(context.Background(), tenantId)
	return err == nil && token != ""
}

// SyncDueSkillSources syncs sources not synced (or attempted, for one in
// error) within the KB sync interval, and retakes a sync a crashed run left
// "syncing" for 30 minutes. Registered as a leader-only job.
func SyncDueSkillSources() error {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return err
	}
	interval := time.Duration(config.Config.KBSyncIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Hour
	}
	var due []struct {
		Id           string     `db:"id"`
		TenantId     string     `db:"tenant_id"`
		CreatedBy    *string    `db:"created_by"`
		LastSyncedAt *time.Time `db:"last_synced_at"`
	}
	if err := dbms.Db.Select(&due, `
		SELECT id, tenant_id, created_by::text AS created_by, last_synced_at FROM llm_kb_skill_sources
		WHERE (status <> 'syncing' OR updated_at < NOW() - INTERVAL '30 minutes')
		  AND updated_at < NOW() - $1::interval
		  AND (last_synced_at IS NULL OR last_synced_at < NOW() - $1::interval)
		ORDER BY last_synced_at NULLS FIRST LIMIT 200`, fmt.Sprintf("%d seconds", int(interval.Seconds()))); err != nil {
		return err
	}
	synced := 0
	withToken := map[string]bool{} // one lookup per tenant per run
	for _, s := range due {
		if synced >= skillSyncsPerRun {
			break
		}
		if s.LastSyncedAt != nil && time.Since(*s.LastSyncedAt) < anonymousSkillSyncInterval {
			has, seen := withToken[s.TenantId]
			if !seen {
				has = hasGitHubToken(s.TenantId)
				withToken[s.TenantId] = has
			}
			if !has {
				continue
			}
		}
		synced++
		userId := ""
		if s.CreatedBy != nil {
			userId = *s.CreatedBy
		}
		sc := security.NewRequestContextForTenantAdminWithUser(context.Background(), s.TenantId, userId)
		if _, err := syncSkillSourceByID(sc, s.Id, false); err != nil {
			slog.Warn("skill source: scheduled sync failed", "source_id", s.Id, "error", err)
		}
	}
	return nil
}
