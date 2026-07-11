package ownership

import (
	"database/sql"
	"encoding/json"
	"nudgebee/services/internal/database"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

// manualOwner is the input for an upsert (keeps the arg count sane).
type manualOwner struct {
	TenantId       string
	ResourceType   string
	ResourceKey    string
	CloudAccountId string
	OwnerType      string
	OwnerId        string
	ActorId        string
}

const ownerCols = `id, tenant_id, resource_type, resource_key, cloud_account_id, owner_type, owner_id, created_by, updated_by, created_at, updated_at`

func db() (*sqlx.DB, error) {
	manager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	return manager.Db, nil
}

// getOwnerRow returns the manual owner row for (tenant, type, key), or nil.
func getOwnerRow(d *sqlx.DB, tenantId, resourceType, resourceKey string) (*ResourceOwnerRow, error) {
	var row ResourceOwnerRow
	err := d.Get(&row, `SELECT `+ownerCols+`
		FROM resource_owners
		WHERE tenant_id = $1::uuid AND resource_type = $2 AND resource_key = $3`,
		tenantId, resourceType, resourceKey)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// upsertManualOwner inserts or replaces the single owner of a resource.
func upsertManualOwner(d *sqlx.DB, m manualOwner) (string, error) {
	var id string
	err := d.QueryRowx(`
		INSERT INTO resource_owners
			(tenant_id, resource_type, resource_key, cloud_account_id, owner_type, owner_id, created_by, updated_by)
		VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, $5, $6::uuid, NULLIF($7,'')::uuid, NULLIF($7,'')::uuid)
		ON CONFLICT ON CONSTRAINT resource_owners_tenant_type_key_uniq
		DO UPDATE SET
			owner_type       = EXCLUDED.owner_type,
			owner_id         = EXCLUDED.owner_id,
			cloud_account_id = EXCLUDED.cloud_account_id,
			updated_by       = EXCLUDED.updated_by,
			updated_at       = now()
		RETURNING id`,
		m.TenantId, m.ResourceType, m.ResourceKey, m.CloudAccountId, m.OwnerType, m.OwnerId, m.ActorId,
	).Scan(&id)
	return id, err
}

func deleteManualOwner(d *sqlx.DB, tenantId, resourceType, resourceKey string) (int64, error) {
	res, err := d.Exec(`DELETE FROM resource_owners WHERE tenant_id = $1::uuid AND resource_type = $2 AND resource_key = $3`,
		tenantId, resourceType, resourceKey)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// loadAllOwners loads every manual owner row for a tenant (used by batch resolve).
func loadAllOwners(d *sqlx.DB, tenantId string) ([]ResourceOwnerRow, error) {
	var rows []ResourceOwnerRow
	err := d.Select(&rows, `SELECT `+ownerCols+` FROM resource_owners WHERE tenant_id = $1::uuid`, tenantId)
	return rows, err
}

// listOwners returns stored manual owners, optionally filtered.
func listOwners(d *sqlx.DB, tenantId string, f ListOwnersRequest) ([]ResourceOwnerRow, error) {
	q := `SELECT ` + ownerCols + ` FROM resource_owners WHERE tenant_id = $1::uuid`
	args := []any{tenantId}
	if f.OwnerType != "" {
		args = append(args, f.OwnerType)
		q += " AND owner_type = $" + strconv.Itoa(len(args))
	}
	if f.OwnerId != "" {
		args = append(args, f.OwnerId)
		q += " AND owner_id = $" + strconv.Itoa(len(args)) + "::uuid"
	}
	if f.ResourceType != "" {
		args = append(args, f.ResourceType)
		q += " AND resource_type = $" + strconv.Itoa(len(args))
	}
	if f.CloudAccountId != "" {
		args = append(args, f.CloudAccountId)
		q += " AND cloud_account_id = $" + strconv.Itoa(len(args)) + "::uuid"
	}
	q += " ORDER BY resource_type, resource_key"
	var rows []ResourceOwnerRow
	err := d.Select(&rows, q, args...)
	return rows, err
}

// loadOwnerNames batch-resolves display names for the given user and group ids.
// Returns a map keyed by "<ownerType>\x00<ownerId>" → display name. Best-effort:
// names are cosmetic, so callers may ignore the error.
func loadOwnerNames(d *sqlx.DB, tenantId string, userIds, groupIds []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIds) > 0 {
		// users has no tenant column — scope by membership so a foreign tenant's
		// display name can never be returned for an owner id in this tenant.
		q, args, err := sqlx.In(`
			SELECT u.id::text AS id, COALESCE(NULLIF(u.display_name, ''), u.username::text) AS name
			FROM users u
			WHERE u.id::text IN (?)
			  AND EXISTS (SELECT 1 FROM tenant_users tu WHERE tu."user" = u.id AND tu.tenant::text = ?)`, userIds, tenantId)
		if err != nil {
			return out, err
		}
		if err := scanNames(d, q, args, OwnerTypeUser, out); err != nil {
			return out, err
		}
	}
	if len(groupIds) > 0 {
		q, args, err := sqlx.In(`SELECT id::text AS id, name FROM user_groups WHERE id::text IN (?) AND tenant::text = ?`, groupIds, tenantId)
		if err != nil {
			return out, err
		}
		if err := scanNames(d, q, args, OwnerTypeGroup, out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ownerExistsInTenant reports whether the given user/group owner id belongs to the
// tenant. Used to reject assigning a cross-tenant owner id (users are global and
// scoped via tenant_users; groups carry a tenant column).
func ownerExistsInTenant(d *sqlx.DB, tenantId, ownerType, ownerId string) (bool, error) {
	var q string
	switch ownerType {
	case OwnerTypeUser:
		q = `SELECT EXISTS(SELECT 1 FROM tenant_users WHERE tenant::text = $1 AND "user"::text = $2)`
	case OwnerTypeGroup:
		q = `SELECT EXISTS(SELECT 1 FROM user_groups WHERE tenant::text = $1 AND id::text = $2)`
	default:
		return false, nil
	}
	var ok bool
	if err := d.QueryRowx(q, tenantId, ownerId).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// withTenantRuleLock serializes the rule conflict-check + write per tenant, so two
// concurrent upserts can't both pass the same-scope overlap check and create
// colliding rules. The transaction-scoped advisory lock is held until fn returns and
// the tx commits/rolls back; the second caller blocks on the lock until the first
// commits, so it sees the freshly written rule during its own conflict check.
func withTenantRuleLock(d *sqlx.DB, tenantId string, fn func() error) error {
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, tenantId+":ownership_rules"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func scanNames(d *sqlx.DB, query string, args []any, ownerType string, out map[string]string) error {
	rows, err := d.Queryx(d.Rebind(query), args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		out[ownerType+"\x00"+id] = name
	}
	return nil
}

// loadEnabledRules returns the tenant's enabled rules oldest-first, so within a
// scope the first match is the deterministic winner (precedence across scopes is
// by specificity — see evalRules; there is no priority).
func loadEnabledRules(d *sqlx.DB, tenantId string) ([]OwnershipRuleRow, error) {
	var rows []OwnershipRuleRow
	err := d.Select(&rows, `
		SELECT id, tenant_id, name, resource_domain, match_scope, match_key, match_value, cloud_account_id,
		       owner_type, owner_id, priority, enabled, created_by, updated_by, created_at, updated_at
		FROM ownership_rules
		WHERE tenant_id = $1::uuid AND enabled = true AND resource_domain = 'k8s'
		ORDER BY created_at ASC, id ASC`, tenantId)
	return rows, err
}

// loadEnabledCloudRules returns the tenant's enabled cloud-domain rules, oldest-first.
func loadEnabledCloudRules(d *sqlx.DB, tenantId string) ([]OwnershipRuleRow, error) {
	var rows []OwnershipRuleRow
	err := d.Select(&rows, `
		SELECT id, tenant_id, name, resource_domain, match_scope, match_key, match_value, cloud_account_id,
		       owner_type, owner_id, priority, enabled, created_by, updated_by, created_at, updated_at
		FROM ownership_rules
		WHERE tenant_id = $1::uuid AND enabled = true AND resource_domain = 'cloud'
		ORDER BY created_at ASC, id ASC`, tenantId)
	return rows, err
}

// getCloudResourceMeta loads the active cloud resource's account/region/type/
// service/tags, or nil if gone. is_active IS NOT FALSE (NULL = active).
func getCloudResourceMeta(d *sqlx.DB, tenantId, id string) (*cloudResourceMeta, error) {
	var account, region, rtype, service string
	var tagsRaw []byte
	err := d.QueryRowx(`
		SELECT account::text, region, COALESCE(type,''), COALESCE(service_name,''), tags
		FROM cloud_resourses
		WHERE tenant = $1::uuid AND id = $2::uuid AND is_active IS NOT FALSE
		LIMIT 1`, tenantId, id).Scan(&account, &region, &rtype, &service, &tagsRaw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cloudResourceMeta{Account: account, Region: region, Type: rtype, ServiceName: service, Tags: parseLabels(tagsRaw)}, nil
}

// loadCloudResourceMetas batch-loads active cloud resources by id (batch resolve).
func loadCloudResourceMetas(d *sqlx.DB, tenantId string, ids []string) (map[string]cloudResourceMeta, error) {
	out := map[string]cloudResourceMeta{}
	if len(ids) == 0 {
		return out, nil
	}
	query, args, err := sqlx.In(`
		SELECT id::text AS id, account::text AS account, region, COALESCE(type,'') AS rtype,
		       COALESCE(service_name,'') AS service, tags
		FROM cloud_resourses
		WHERE tenant = ?::uuid AND is_active IS NOT FALSE AND id::text IN (?)`, tenantId, ids)
	if err != nil {
		return out, err
	}
	query = d.Rebind(query)
	rows, err := d.Queryx(query, args...)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, account, region, rtype, service string
		var tagsRaw []byte
		if err := rows.Scan(&id, &account, &region, &rtype, &service, &tagsRaw); err != nil {
			return out, err
		}
		out[id] = cloudResourceMeta{Account: account, Region: region, Type: rtype, ServiceName: service, Tags: parseLabels(tagsRaw)}
	}
	return out, nil
}

// loadCloudResourceTagsMatching returns the tags of active cloud resources whose
// tag key=value matches (optionally within one account). Used to detect whether
// two cloud_tag rules can both match a real resource.
func loadCloudResourceTagsMatching(d *sqlx.DB, tenantId, key, value, account string) ([]map[string]string, error) {
	q := `SELECT tags FROM cloud_resourses WHERE tenant = $1::uuid AND is_active IS NOT FALSE AND tags->>$2 = $3`
	args := []any{tenantId, key, value}
	if account != "" {
		q += ` AND account = $4::uuid`
		args = append(args, account)
	}
	rows, err := d.Queryx(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, parseLabels(raw))
	}
	return out, nil
}

// getWorkloadMeta loads the active workload's account/namespace/labels, or nil if gone.
func getWorkloadMeta(d *sqlx.DB, tenantId, cloudResourceId string) (*workloadMeta, error) {
	var account, namespace, name string
	var labelsRaw []byte
	err := d.QueryRowx(`
		SELECT cloud_account_id::text, namespace, name, labels
		FROM k8s_workloads
		WHERE tenant_id = $1::uuid AND cloud_resource_id = $2::uuid AND is_active
		LIMIT 1`, tenantId, cloudResourceId).Scan(&account, &namespace, &name, &labelsRaw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &workloadMeta{CloudAccountId: account, Namespace: namespace, Name: name, Labels: parseLabels(labelsRaw)}, nil
}

// loadWorkloadMetas batch-loads active workloads by cloud_resource_id (batch resolve).
func loadWorkloadMetas(d *sqlx.DB, tenantId string, ids []string) (map[string]workloadMeta, error) {
	out := map[string]workloadMeta{}
	if len(ids) == 0 {
		return out, nil
	}
	query, args, err := sqlx.In(`
		SELECT cloud_resource_id::text AS id, cloud_account_id::text AS account, namespace, name, labels
		FROM k8s_workloads
		WHERE tenant_id = ?::uuid AND is_active AND cloud_resource_id::text IN (?)`, tenantId, ids)
	if err != nil {
		return out, err
	}
	query = d.Rebind(query)
	rows, err := d.Queryx(query, args...)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, account, namespace, name string
		var labelsRaw []byte
		if err := rows.Scan(&id, &account, &namespace, &name, &labelsRaw); err != nil {
			return out, err
		}
		out[id] = workloadMeta{CloudAccountId: account, Namespace: namespace, Name: name, Labels: parseLabels(labelsRaw)}
	}
	return out, nil
}

// isNamespaceActive reports whether the namespace (key = "<account>/<name>") exists
// and is active.
func isNamespaceActive(d *sqlx.DB, tenantId, key string) bool {
	account, name, ok := splitNamespaceKey(key)
	if !ok {
		return false
	}
	var exists bool
	err := d.Get(&exists, `
		SELECT EXISTS (
			SELECT 1 FROM k8s_namespaces
			WHERE tenant_id = $1 AND cloud_account_id = $2 AND name = $3 AND is_active)`,
		tenantId, account, name)
	return err == nil && exists
}

// loadActiveNamespaceSet returns the set of active "<account>/<name>" keys for a
// tenant (used by batch resolve to filter orphaned namespace owners).
func loadActiveNamespaceSet(d *sqlx.DB, tenantId string) (map[string]bool, error) {
	var keys []string
	err := d.Select(&keys, `
		SELECT cloud_account_id || '/' || name FROM k8s_namespaces
		WHERE tenant_id = $1 AND is_active`, tenantId)
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set, err
}

func splitNamespaceKey(key string) (account, name string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i], key[i+1:], i > 0 && i < len(key)-1
		}
	}
	return "", "", false
}

func parseLabels(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	m := map[string]string{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]string{}
	}
	return m
}

// cleanupOrphans hard-deletes manual rows whose underlying resource no longer
// exists (inactive/absent workload or namespace). Account deletion is handled by
// the FK cascade; service rows are left as-is (provisional). Idempotent.
func cleanupOrphans(d *sqlx.DB, tenantId string) (int, error) {
	total := int64(0)
	res, err := d.Exec(`
		DELETE FROM resource_owners ro
		WHERE ro.tenant_id = $1::uuid AND ro.resource_type = 'workload'
		  AND NOT EXISTS (
		    SELECT 1 FROM k8s_workloads w
		    WHERE w.tenant_id = ro.tenant_id AND w.cloud_resource_id::text = ro.resource_key AND w.is_active)`,
		tenantId)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	total += n

	res, err = d.Exec(`
		DELETE FROM resource_owners ro
		WHERE ro.tenant_id = $1::uuid AND ro.resource_type = 'namespace'
		  AND NOT EXISTS (
		    SELECT 1 FROM k8s_namespaces ns
		    WHERE ns.tenant_id = ro.tenant_id::text
		      AND ns.cloud_account_id = split_part(ro.resource_key, '/', 1)
		      AND ns.name = split_part(ro.resource_key, '/', 2)
		      AND ns.is_active)`,
		tenantId)
	if err != nil {
		return int(total), err
	}
	n, _ = res.RowsAffected()
	total += n
	return int(total), nil
}

// ---- conflict detection (no priority: same-scope overlaps are blocked) ----

// accountOverlap reports whether two optional account scopes can apply to the
// same resource: empty means "all accounts" and overlaps any specific account.
func accountOverlap(a, b string) bool { return a == "" || b == "" || a == b }

func splitNames(csv string) []string {
	var out []string
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadWorkloadLabelsMatching returns the labels of active workloads matching the
// given label key=value (optionally within one account). Used to detect whether
// two label rules can both match a real workload.
func loadWorkloadLabelsMatching(d *sqlx.DB, tenantId, key, value, account string) ([]map[string]string, error) {
	q := `SELECT labels FROM k8s_workloads WHERE tenant_id = $1::uuid AND is_active AND labels->>$2 = $3`
	args := []any{tenantId, key, value}
	if account != "" {
		q += ` AND cloud_account_id = $4::uuid`
		args = append(args, account)
	}
	rows, err := d.Queryx(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, parseLabels(raw))
	}
	return out, nil
}

// findConflictingRule returns an existing rule of the SAME scope that overlaps the
// requested rule (could own an overlapping set of resources), or nil. excludeId
// skips the rule being edited.
func findConflictingRule(d *sqlx.DB, tenantId string, req UpsertRuleRequest, excludeId string) (*OwnershipRuleRow, error) {
	all, err := listRuleRows(d, tenantId)
	if err != nil {
		return nil, err
	}
	candidates := sameScopeCandidates(all, req.MatchScope, excludeId, req.CloudAccountId)
	switch req.MatchScope {
	case MatchScopeNamespace, MatchScopeCloudRegion, MatchScopeCloudType:
		// exact match_value equality (namespace name / region / type-or-service)
		return namespaceConflict(candidates, req), nil
	case MatchScopeWorkload:
		return workloadConflict(candidates, req), nil
	case MatchScopeCloudResource:
		return cloudResourceConflict(candidates, req), nil
	case MatchScopeLabel:
		return labelConflict(d, tenantId, candidates, req)
	case MatchScopeCloudTag:
		return cloudTagConflict(d, tenantId, candidates, req)
	}
	return nil, nil
}

// cloudResourceConflict reports an overlap when two cloud_resource rules pin a
// shared resource id (no match_value gate; account-overlap already applied).
func cloudResourceConflict(candidates []OwnershipRuleRow, req UpsertRuleRequest) *OwnershipRuleRow {
	reqIds := map[string]bool{}
	for _, n := range splitNames(req.MatchKey) {
		reqIds[n] = true
	}
	for i := range candidates {
		for _, n := range splitNames(candidates[i].MatchKey.String) {
			if reqIds[n] {
				return &candidates[i]
			}
		}
	}
	return nil
}

// cloudTagConflict reports a conflict when some active cloud resource matches both
// the requested cloud_tag rule and an existing one (mirror of labelConflict).
func cloudTagConflict(d *sqlx.DB, tenantId string, candidates []OwnershipRuleRow, req UpsertRuleRequest) (*OwnershipRuleRow, error) {
	matches, err := loadCloudResourceTagsMatching(d, tenantId, req.MatchKey, req.MatchValue, req.CloudAccountId)
	if err != nil {
		return nil, err
	}
	return labelRulesOverlap(candidates, matches), nil
}

// sameScopeCandidates returns rules of the given scope (excluding excludeId) whose
// account scope can overlap the requested one.
func sameScopeCandidates(all []OwnershipRuleRow, scope, excludeId, account string) []OwnershipRuleRow {
	var out []OwnershipRuleRow
	for i := range all {
		e := all[i]
		// Disabled rules are inert during evaluation, so they must not block a new
		// rule. (Re-enabling a disabled rule re-runs this check via upsert.)
		if e.Id == excludeId || e.MatchScope != scope || !e.Enabled {
			continue
		}
		if accountOverlap(e.CloudAccountId.String, account) {
			out = append(out, e)
		}
	}
	return out
}

func namespaceConflict(candidates []OwnershipRuleRow, req UpsertRuleRequest) *OwnershipRuleRow {
	for i := range candidates {
		if candidates[i].MatchValue == req.MatchValue {
			return &candidates[i]
		}
	}
	return nil
}

func workloadConflict(candidates []OwnershipRuleRow, req UpsertRuleRequest) *OwnershipRuleRow {
	reqNames := map[string]bool{}
	for _, n := range splitNames(req.MatchKey) {
		reqNames[n] = true
	}
	for i := range candidates {
		if candidates[i].MatchValue != req.MatchValue {
			continue
		}
		for _, n := range splitNames(candidates[i].MatchKey.String) {
			if reqNames[n] {
				return &candidates[i]
			}
		}
	}
	return nil
}

// labelRulesOverlap returns a candidate label rule that some workload (from the
// set of workloads matching the requested rule) also matches — i.e. a real
// overlap. Pure, so it's unit-testable without a DB.
func labelRulesOverlap(candidates []OwnershipRuleRow, matchedLabels []map[string]string) *OwnershipRuleRow {
	for i := range candidates {
		e := &candidates[i]
		if !e.MatchKey.Valid {
			continue
		}
		for _, lbls := range matchedLabels {
			if lbls[e.MatchKey.String] == e.MatchValue {
				return e
			}
		}
	}
	return nil
}

// labelConflict reports a conflict when some active workload matches both the
// requested label rule and an existing one.
func labelConflict(d *sqlx.DB, tenantId string, candidates []OwnershipRuleRow, req UpsertRuleRequest) (*OwnershipRuleRow, error) {
	matches, err := loadWorkloadLabelsMatching(d, tenantId, req.MatchKey, req.MatchValue, req.CloudAccountId)
	if err != nil {
		return nil, err
	}
	return labelRulesOverlap(candidates, matches), nil
}

// ---- rules CRUD ----

func upsertRuleRow(d *sqlx.DB, tenantId string, r UpsertRuleRequest, actorId string) (string, error) {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	priority := 100 // omitted ⇒ default; explicit value (incl. 0) honored
	if r.Priority != nil {
		priority = *r.Priority
	}
	domain := r.ResourceDomain
	if domain == "" {
		domain = ResourceDomainK8s
	}
	if r.Id == "" {
		var id string
		err := d.QueryRowx(`
			INSERT INTO ownership_rules
				(tenant_id, name, match_scope, match_key, match_value, cloud_account_id,
				 owner_type, owner_id, priority, enabled, created_by, updated_by, resource_domain)
			VALUES ($1::uuid, $2, $3, NULLIF($4,''), $5, NULLIF($6,'')::uuid, $7, $8::uuid, $9, $10, NULLIF($11,'')::uuid, NULLIF($11,'')::uuid, $12)
			RETURNING id`,
			tenantId, r.Name, r.MatchScope, r.MatchKey, r.MatchValue, r.CloudAccountId,
			r.OwnerType, r.OwnerId, priority, enabled, actorId, domain,
		).Scan(&id)
		return id, err
	}
	_, err := d.Exec(`
		UPDATE ownership_rules SET
			name = $3, match_scope = $4, match_key = NULLIF($5,''), match_value = $6,
			cloud_account_id = NULLIF($7,'')::uuid, owner_type = $8, owner_id = $9::uuid,
			priority = $10, enabled = $11, updated_by = NULLIF($12,'')::uuid, resource_domain = $13, updated_at = now()
		WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		r.Id, tenantId, r.Name, r.MatchScope, r.MatchKey, r.MatchValue,
		r.CloudAccountId, r.OwnerType, r.OwnerId, priority, enabled, actorId, domain)
	return r.Id, err
}

func deleteRuleRow(d *sqlx.DB, tenantId, id string) (int64, error) {
	res, err := d.Exec(`DELETE FROM ownership_rules WHERE id = $1::uuid AND tenant_id = $2::uuid`, id, tenantId)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func listRuleRows(d *sqlx.DB, tenantId string) ([]OwnershipRuleRow, error) {
	var rows []OwnershipRuleRow
	err := d.Select(&rows, `
		SELECT id, tenant_id, name, resource_domain, match_scope, match_key, match_value, cloud_account_id,
		       owner_type, owner_id, priority, enabled, created_by, updated_by, created_at, updated_at
		FROM ownership_rules WHERE tenant_id = $1::uuid
		ORDER BY created_at ASC, id ASC`, tenantId)
	return rows, err
}
