package dashboard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

var (
	ErrNotFound  = errors.New("dashboard not found")
	ErrForbidden = errors.New("access denied for dashboard")

	slugStripRe = regexp.MustCompile(`[^a-z0-9]+`)

	// Compiled `name_regex` bindings, keyed by pattern. Resolve runs on every
	// detail-page load and evaluates each binding in turn, so without this the
	// same handful of author-written patterns are recompiled on every request.
	// Bounded by the number of distinct patterns stored in dashboard_bindings,
	// not by traffic.
	bindingRegexCache sync.Map

	validScopeTypes = map[string]bool{
		"workload": true, "pod": true, "node": true, "namespace": true,
		"cluster": true, "cloud_resource": true, "service": true,
	}
	validMatchKinds = map[string]bool{
		"app_type": true, "name_regex": true, "label_selector": true,
		"resource_type": true, "all": true,
	}
	validPanelTypes = map[string]bool{
		VizTimeseries: true, VizStat: true, VizGauge: true, VizTable: true, VizBar: true, VizText: true,
	}
	validDatasources = map[string]bool{
		DatasourceMetrics: true, DatasourceLogs: true,
		DatasourceTraces: true, DatasourceNudgebee: true,
		DatasourceRedis: true, DatasourceRabbitMQ: true,
		DatasourcePostgres: true,
	}
)

// Slugify produces a url-safe, stable identifier from a title. Collisions inside
// a tenant are resolved by the caller appending a numeric suffix — the unique
// index in V844 is the real guard.
func Slugify(title string) string {
	s := slugStripRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "dashboard"
	}
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	return s
}

// ValidateDefinition rejects panel documents the renderer cannot draw, so a bad
// definition fails at save time rather than rendering as a blank panel later.
func ValidateDefinition(def Definition) error {
	seen := map[int]bool{}
	for i, p := range def.Panels {
		if p.Title == "" {
			return fmt.Errorf("panel %d: title is required", i)
		}
		if !validPanelTypes[p.Type] {
			return fmt.Errorf("panel %q: unsupported type %q", p.Title, p.Type)
		}
		if seen[p.Id] {
			return fmt.Errorf("panel %q: duplicate panel id %d", p.Title, p.Id)
		}
		seen[p.Id] = true
		if p.GridPos.W < 1 || p.GridPos.W > 12 {
			return fmt.Errorf("panel %q: grid_pos.w must be between 1 and 12", p.Title)
		}
		// A link column renders an href every viewer of this dashboard follows,
		// so what it may point at is checked before the definition is stored.
		if err := validateTableOptions(p); err != nil {
			return err
		}
		// A text panel carries prose, not queries; everything else needs a source.
		if p.Type == VizText {
			continue
		}
		if !validDatasources[p.Datasource] {
			return fmt.Errorf("panel %q: unsupported datasource %q", p.Title, p.Datasource)
		}
		// A command datasource returns a snapshot of text, not a series, so the
		// chart visualisations have nothing to plot. An entity query returns rows,
		// which is the same story.
		if (IsCommandDatasource(p.Datasource) || IsEntityDatasource(p.Datasource) || p.Datasource == DatasourceLogs) && p.Type != VizTable {
			return fmt.Errorf("panel %q: a %s panel renders a table", p.Title, p.Datasource)
		}
		// Without accounts the provider lookup has nothing to resolve, so the
		// panel would fail at render on every load. Fail at save instead.
		hasType := strings.TrimSpace(p.AccountType) != ""
		hasIds := len(nonEmptyStrings(p.AccountIds)) > 0
		if hasType && hasIds {
			return fmt.Errorf("panel %q: choose an account type or specific accounts, not both", p.Title)
		}
		if !hasType && !hasIds {
			return fmt.Errorf("panel %q: an account type or at least one account is required", p.Title)
		}
		// A provider names the query language the panel's expression is written in,
		// which only means something where a provider is resolved per account.
		// Carrying one anywhere else would be a stored value nothing ever reads.
		if strings.TrimSpace(p.Provider) != "" && !IsProviderDatasource(p.Datasource) {
			return fmt.Errorf("panel %q: a %s panel has no provider to pin", p.Title, p.Datasource)
		}
		// An index with no provider would be forced across accounts that are each
		// on their own default backend, most of which have no index at all.
		if strings.TrimSpace(p.ProviderIndex) != "" && strings.TrimSpace(p.Provider) == "" {
			return fmt.Errorf("panel %q: an index needs a provider to belong to", p.Title)
		}
		if len(p.Targets) == 0 {
			return fmt.Errorf("panel %q: at least one target is required", p.Title)
		}
		for _, t := range p.Targets {
			if IsEntityDatasource(p.Datasource) {
				if len(t.Query) == 0 {
					return fmt.Errorf("panel %q: %s targets require a query", p.Title, p.Datasource)
				}
				// Same allowlist the executor enforces, so an unqueryable table is
				// refused at save rather than on every viewer's screen.
				if err := ValidateEntityQuery(p.Datasource, t.Query); err != nil {
					return fmt.Errorf("panel %q: %w", p.Title, err)
				}
			} else if strings.TrimSpace(t.Expr) == "" {
				return fmt.Errorf("panel %q: %s targets require an expression", p.Title, p.Datasource)
			} else if IsCommandDatasource(p.Datasource) {
				// Told at save rather than only at render. ExecuteQuery re-checks
				// this — save-time validation is the courtesy, not the guard.
				if err := ValidatePanelCommand(p.Datasource, t.Expr); err != nil {
					return fmt.Errorf("panel %q: %w", p.Title, err)
				}
			}
		}
	}
	return nil
}

// upgradeDefinition migrates a stored definition to the current panel shape.
//
// This runs on every READ rather than as a data migration, which is what
// `schema_version` on the dashboards table is for: the JSON shape evolves with a
// code-side upgrader instead of a rewrite over JSONB. It is idempotent and keyed
// on what the data actually contains, so it is safe to run against a mix of old
// and new rows.
//
// v1 -> v2: a panel named one account in `account_id`. It now names either a
// cloud provider or a LIST of accounts. Without this, every dashboard authored
// before the change renders "No account" on every panel and reopens with an
// empty account picker.
//
// v2 -> v3: a table panel's column settings lived in two arrays keyed by column
// name, `link_columns` and `hidden_columns`. They are now one `columns` list
// where each entry carries everything about that column — see table_columns.go.
func upgradeDefinition(def *Definition) {
	for i := range def.Panels {
		p := &def.Panels[i]
		if p.LegacyAccountId != "" && p.AccountType == "" && len(p.AccountIds) == 0 {
			p.AccountIds = []string{p.LegacyAccountId}
		}
		// Cleared unconditionally so the field cannot survive a round-trip and
		// resurrect itself as a second source of truth.
		p.LegacyAccountId = ""
		upgradeTableOptions(p)
	}
}

// nonEmptyStrings drops blanks so a picker that emitted [""] does not read as a
// selection.
func nonEmptyStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// PanelAccountIds lists the distinct accounts a definition names EXPLICITLY, so
// the caller can authorize every one of them before the dashboard is saved.
//
// Account-type panels are deliberately absent: they name no account, and are
// resolved at render against the accounts the viewer can already see. There is
// no escalation in that — every read still goes through metrics_list / logs /
// traces, which gate on the account individually.
func PanelAccountIds(def Definition) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range def.Panels {
		for _, id := range nonEmptyStrings(p.AccountIds) {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func validateBindings(bindings []Binding) error {
	for _, b := range bindings {
		if !validScopeTypes[b.ScopeType] {
			return fmt.Errorf("binding: unsupported scope_type %q", b.ScopeType)
		}
		if !validMatchKinds[b.MatchKind] {
			return fmt.Errorf("binding: unsupported match_kind %q", b.MatchKind)
		}
		if b.MatchKind == "name_regex" {
			pattern, _ := b.MatchValue["regex"].(string)
			if pattern == "" {
				return errors.New("binding: name_regex requires match_value.regex")
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("binding: invalid regex %q: %w", pattern, err)
			}
		}
	}
	return nil
}

// scanDashboard reads one row. definition/tags arrive as JSONB bytes.
func scanDashboard(rows interface {
	Scan(dest ...any) error
}) (Dashboard, error) {
	var (
		d       Dashboard
		defRaw  []byte
		tagsRaw []byte
		desc    sql.NullString
	)
	err := rows.Scan(&d.Id, &d.TenantId, &d.Slug, &d.Title, &desc,
		&defRaw, &d.SchemaVersion, &tagsRaw, &d.Status,
		&d.IsBuiltin, &d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return d, err
	}
	d.Description = desc.String
	if len(defRaw) > 0 {
		if err := json.Unmarshal(defRaw, &d.Definition); err != nil {
			return d, fmt.Errorf("dashboard %s: corrupt definition: %w", d.Id, err)
		}
	}
	if len(tagsRaw) > 0 {
		_ = json.Unmarshal(tagsRaw, &d.Tags)
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if d.Definition.Panels == nil {
		d.Definition.Panels = []Panel{}
	}
	// Every read path goes through here, so this is the single place the shape
	// upgrade has to happen — list, get, resolve and version history all inherit
	// it rather than each remembering to call it.
	upgradeDefinition(&d.Definition)
	return d, nil
}

const dashboardColumns = `id, tenant_id, slug, title, description,
	definition, schema_version, tags, status, is_builtin, created_by, updated_by, created_at, updated_at`

// List returns the tenant's dashboards. A dashboard has no account of its own —
// its panels each carry one — so tenant scoping is the only filter applied.
func List(ctx *security.RequestContext, req ListRequest) ([]Dashboard, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	args := []any{ctx.GetSecurityContext().GetTenantId()}
	where := []string{"tenant_id = $1"}

	if req.Search != "" {
		args = append(args, "%"+req.Search+"%")
		where = append(where, fmt.Sprintf("title ILIKE $%d", len(args)))
	}
	// Archived dashboards stay out of the listing; they are reachable by id.
	args = append(args, StatusActive)
	where = append(where, fmt.Sprintf("status = $%d", len(args)))

	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args = append(args, limit, req.Offset)
	query := fmt.Sprintf(
		`SELECT %s FROM dashboards WHERE %s ORDER BY updated_at DESC LIMIT $%d OFFSET $%d`,
		dashboardColumns, strings.Join(where, " AND "), len(args)-1, len(args))

	rows, err := dbms.Db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			ctx.GetLogger().Warn("dashboard: failed to close rows", "error", closeErr)
		}
	}()

	out := []Dashboard{}
	for rows.Next() {
		d, err := scanDashboard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func Get(ctx *security.RequestContext, req GetRequest) (*Dashboard, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	row := dbms.Db.QueryRow(
		fmt.Sprintf(`SELECT %s FROM dashboards WHERE id = $1 AND tenant_id = $2`, dashboardColumns),
		req.Id, ctx.GetSecurityContext().GetTenantId())

	d, err := scanDashboard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := dbms.Db.QueryRow(
		`SELECT COALESCE(MAX(version), 0) FROM dashboard_versions WHERE dashboard_id = $1`,
		d.Id).Scan(&d.Version); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &d, nil
}

// Save creates or updates a dashboard and always appends a revision row, so the
// version history is complete by construction rather than by remembering to
// write it. Bindings are replaced wholesale when supplied.
func Save(ctx *security.RequestContext, req SaveRequest) (*Dashboard, error) {
	// Upgrade before validating: a caller replaying an old definition would
	// otherwise be rejected for a shape this package knows how to migrate.
	upgradeDefinition(&req.Definition)
	if err := ValidateDefinition(req.Definition); err != nil {
		return nil, err
	}
	if err := validateBindings(req.Bindings); err != nil {
		return nil, err
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()
	userId := ctx.GetSecurityContext().GetUserId()

	defJson, err := json.Marshal(req.Definition)
	if err != nil {
		return nil, err
	}
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJson, _ := json.Marshal(tags)

	status := req.Status
	if status == "" {
		status = StatusActive
	}

	tx, err := dbms.Db.Beginx()
	if err != nil {
		return nil, err
	}
	defer database.LogRollback(tx)

	var id string
	if req.Id == "" {
		slug, err := uniqueSlug(tx, tenantId, Slugify(req.Title))
		if err != nil {
			return nil, err
		}
		err = tx.QueryRow(
			`INSERT INTO dashboards (tenant_id, slug, title, description,
				definition, tags, status, created_by, updated_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id`,
			tenantId, slug, req.Title, req.Description,
			defJson, tagsJson, status, userId).Scan(&id)
		if err != nil {
			return nil, err
		}
	} else {
		id = req.Id
		// is_builtin dashboards ship with the product; edits would be silently
		// reverted by the next release, so refuse rather than pretend.
		var isBuiltin bool
		err = tx.QueryRow(`SELECT is_builtin FROM dashboards WHERE id = $1 AND tenant_id = $2`,
			id, tenantId).Scan(&isBuiltin)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if isBuiltin {
			return nil, errors.New("built-in dashboards cannot be edited; duplicate it first")
		}
		res, err := tx.Exec(
			`UPDATE dashboards SET title=$1, description=$2, definition=$3,
				tags=$4, status=$5, updated_by=$6
			 WHERE id=$7 AND tenant_id=$8`,
			req.Title, req.Description, defJson, tagsJson, status,
			userId, id, tenantId)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
	}

	var nextVersion int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(version), 0) + 1 FROM dashboard_versions WHERE dashboard_id = $1`,
		id).Scan(&nextVersion); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`INSERT INTO dashboard_versions (dashboard_id, version, definition, message, created_by)
		 VALUES ($1,$2,$3,$4,$5)`,
		id, nextVersion, defJson, req.Message, userId); err != nil {
		return nil, err
	}

	if req.Bindings != nil {
		if _, err := tx.Exec(`DELETE FROM dashboard_bindings WHERE dashboard_id = $1`, id); err != nil {
			return nil, err
		}
		for _, b := range req.Bindings {
			mv := b.MatchValue
			if mv == nil {
				mv = map[string]any{}
			}
			mvJson, _ := json.Marshal(mv)
			priority := b.Priority
			if priority == 0 {
				priority = 100
			}
			if _, err := tx.Exec(
				`INSERT INTO dashboard_bindings (dashboard_id, tenant_id, scope_type, match_kind, match_value, priority)
				 VALUES ($1,$2,$3,$4,$5,$6)`,
				id, tenantId, b.ScopeType, b.MatchKind, mvJson, priority); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return Get(ctx, GetRequest{Id: id})
}

// uniqueSlug appends -2, -3 … until the unique index would accept it.
func uniqueSlug(tx *sqlx.Tx, tenantId string, base string) (string, error) {
	for i := 0; i < 50; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		var count int
		err := tx.QueryRow(
			`SELECT count(*) FROM dashboards WHERE tenant_id=$1 AND slug=$2`,
			tenantId, candidate).Scan(&count)
		if err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("could not allocate a unique slug")
}

func Delete(ctx *security.RequestContext, req DeleteRequest) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	res, err := dbms.Db.Exec(
		`DELETE FROM dashboards WHERE id=$1 AND tenant_id=$2 AND is_builtin = false`,
		req.Id, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func ListVersions(ctx *security.RequestContext, dashboardId string) ([]Version, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbms.Db.Query(
		`SELECT v.version, COALESCE(v.message, ''), v.created_by, v.created_at
		 FROM dashboard_versions v
		 JOIN dashboards d ON d.id = v.dashboard_id
		 WHERE v.dashboard_id = $1 AND d.tenant_id = $2
		 ORDER BY v.version DESC LIMIT 100`,
		dashboardId, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			ctx.GetLogger().Warn("dashboard: failed to close version rows", "error", closeErr)
		}
	}()

	out := []Version{}
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.Version, &v.Message, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func ListBindings(ctx *security.RequestContext, dashboardId string) ([]Binding, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbms.Db.Query(
		`SELECT b.id, b.dashboard_id, b.scope_type, b.match_kind, b.match_value, b.priority
		 FROM dashboard_bindings b
		 JOIN dashboards d ON d.id = b.dashboard_id
		 WHERE b.dashboard_id = $1 AND d.tenant_id = $2
		 ORDER BY b.priority ASC`,
		dashboardId, ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			ctx.GetLogger().Warn("dashboard: failed to close binding rows", "error", closeErr)
		}
	}()
	return scanBindings(rows)
}

func scanBindings(rows *sql.Rows) ([]Binding, error) {
	out := []Binding{}
	for rows.Next() {
		var (
			b     Binding
			mvRaw []byte
		)
		if err := rows.Scan(&b.Id, &b.DashboardId, &b.ScopeType,
			&b.MatchKind, &mvRaw, &b.Priority); err != nil {
			return nil, err
		}
		if len(mvRaw) > 0 {
			_ = json.Unmarshal(mvRaw, &b.MatchValue)
		}
		if b.MatchValue == nil {
			b.MatchValue = map[string]any{}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Resolve returns the dashboards that auto-attach to one entity, best match
// first. Regex matching happens in Go rather than SQL so an operator-authored
// pattern can never become a Postgres regex DoS on a shared connection.
func Resolve(ctx *security.RequestContext, req ResolveRequest) ([]Dashboard, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()

	rows, err := dbms.Db.Query(
		`SELECT b.id, b.dashboard_id, b.scope_type, b.match_kind, b.match_value, b.priority
		 FROM dashboard_bindings b
		 JOIN dashboards d ON d.id = b.dashboard_id AND d.status = 'active'
		 WHERE b.tenant_id = $1 AND b.scope_type = $2
		 ORDER BY b.priority ASC`,
		tenantId, req.ScopeType)
	if err != nil {
		return nil, err
	}
	// Deferred to function scope rather than closed right after the scan, which
	// would be tighter: sqlclosecheck demands `defer`, and moving the scan into
	// a closure to get both then hides `rows` from rowserrcheck (scanBindings
	// does check rows.Err()). This holds the result set open across the second
	// query below — one idle result set, versus fighting two linters.
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			ctx.GetLogger().Warn("dashboard: failed to close binding rows", "error", closeErr)
		}
	}()
	bindings, err := scanBindings(rows)
	if err != nil {
		return nil, err
	}

	matchedIds := []string{}
	seen := map[string]bool{}
	for _, b := range bindings {
		if !bindingMatches(b, req) || seen[b.DashboardId] {
			continue
		}
		seen[b.DashboardId] = true
		matchedIds = append(matchedIds, b.DashboardId)
	}
	if len(matchedIds) == 0 {
		return []Dashboard{}, nil
	}

	query, args, err := sqlx.In(
		fmt.Sprintf(`SELECT %s FROM dashboards WHERE tenant_id = ? AND id IN (?)`, dashboardColumns),
		tenantId, matchedIds)
	if err != nil {
		return nil, err
	}
	query = dbms.Db.Rebind(query)
	drows, err := dbms.Db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := drows.Close(); closeErr != nil {
			ctx.GetLogger().Warn("dashboard: failed to close resolve rows", "error", closeErr)
		}
	}()

	byId := map[string]Dashboard{}
	for drows.Next() {
		d, err := scanDashboard(drows)
		if err != nil {
			return nil, err
		}
		byId[d.Id] = d
	}
	if err := drows.Err(); err != nil {
		return nil, err
	}

	// Preserve binding priority order rather than SQL row order.
	out := make([]Dashboard, 0, len(matchedIds))
	for _, id := range matchedIds {
		if d, ok := byId[id]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func bindingMatches(b Binding, req ResolveRequest) bool {
	switch b.MatchKind {
	case "all":
		return true
	case "app_type":
		want, _ := b.MatchValue["app_type"].(string)
		return want != "" && strings.EqualFold(want, req.AppType)
	case "name_regex":
		pattern, _ := b.MatchValue["regex"].(string)
		if pattern == "" {
			return false
		}
		re, err := cachedRegex(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(req.Name)
	case "resource_type":
		want, _ := b.MatchValue["resource_type"].(string)
		return want != "" && strings.EqualFold(want, req.ScopeType)
	case "label_selector":
		// Namespace is the only entity label carried on the resolve request
		// today; richer selectors need the label set plumbed through first.
		want, _ := b.MatchValue["namespace"].(string)
		return want != "" && want == req.Namespace
	}
	return false
}

// cachedRegex compiles a binding pattern once and reuses it.
//
// A pattern that does not compile is cached as its error too — an author can
// save an invalid one, and re-attempting the compile on every resolve would pay
// the parse cost forever to reach the same answer.
func cachedRegex(pattern string) (*regexp.Regexp, error) {
	if cached, ok := bindingRegexCache.Load(pattern); ok {
		entry := cached.(regexCacheEntry)
		return entry.re, entry.err
	}
	re, err := regexp.Compile(pattern)
	bindingRegexCache.Store(pattern, regexCacheEntry{re: re, err: err})
	return re, err
}

type regexCacheEntry struct {
	re  *regexp.Regexp
	err error
}
