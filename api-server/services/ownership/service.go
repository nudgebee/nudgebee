package ownership

import (
	"fmt"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/security"
	"strings"

	"github.com/jmoiron/sqlx"
)

var validResourceTypes = map[string]bool{
	ResourceTypeWorkload:      true,
	ResourceTypeNamespace:     true,
	ResourceTypeCloudAccount:  true,
	ResourceTypeService:       true,
	ResourceTypeCloudResource: true,
}

func requireTenantAdmin(ctx *security.RequestContext) (string, error) {
	if !ctx.GetSecurityContext().IsTenantAdmin() && !ctx.GetSecurityContext().IsSuperAdmin() {
		return "", common.ErrorUnauthorized("Only tenant admins can manage ownership")
	}
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return "", common.ErrorUnauthorized("Unauthorized")
	}
	return tenantId, nil
}

func requireTenant(ctx *security.RequestContext) (string, error) {
	// Ownership is tenant-scoped; require a concrete tenant even for super-admins
	// (a super-admin operates within a selected tenant). An empty tenantId would
	// otherwise reach a `$1::uuid` cast and fail — fail fast instead.
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		return "", common.ErrorUnauthorized("Unauthorized")
	}
	return tenantId, nil
}

// AssignOwner manually sets (replaces) the owner of a resource.
func AssignOwner(ctx *security.RequestContext, req AssignOwnerRequest) (StatusResponse, error) {
	if err := common.ValidateStruct(req); err != nil {
		return StatusResponse{}, err
	}
	if !validResourceTypes[req.ResourceType] {
		return StatusResponse{}, common.ErrorBadRequest("invalid resource_type")
	}
	if req.OwnerType != OwnerTypeUser && req.OwnerType != OwnerTypeGroup {
		return StatusResponse{}, common.ErrorBadRequest("owner_type must be 'user' or 'group'")
	}
	tenantId, err := requireTenantAdmin(ctx)
	if err != nil {
		return StatusResponse{}, err
	}
	d, err := db()
	if err != nil {
		return StatusResponse{}, err
	}
	if ok, oErr := ownerExistsInTenant(d, tenantId, req.OwnerType, req.OwnerId); oErr != nil {
		return StatusResponse{}, oErr
	} else if !ok {
		return StatusResponse{}, common.ErrorBadRequest("owner not found in this tenant")
	}

	cloudAccountId := deriveCloudAccountId(d, tenantId, req)
	id, err := upsertManualOwner(d, manualOwner{
		TenantId:       tenantId,
		ResourceType:   req.ResourceType,
		ResourceKey:    req.ResourceKey,
		CloudAccountId: cloudAccountId,
		OwnerType:      req.OwnerType,
		OwnerId:        req.OwnerId,
		ActorId:        ctx.GetSecurityContext().GetUserId(),
	})
	if err != nil {
		return StatusResponse{}, err
	}

	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryOwnership,
		EventType:     audit.EventTypeOwnershipAssign,
		EventAction:   audit.EventActionUpdate,
		TargetID:      id,
		TableName:     "resource_owners",
		NewData: map[string]any{
			"resource_type": req.ResourceType, "resource_key": req.ResourceKey,
			"owner_type": req.OwnerType, "owner_id": req.OwnerId,
		},
	})
	return StatusResponse{Status: "ok", Id: id}, nil
}

// RemoveOwner deletes a resource's manual owner.
func RemoveOwner(ctx *security.RequestContext, req DeleteOwnerRequest) (StatusResponse, error) {
	if err := common.ValidateStruct(req); err != nil {
		return StatusResponse{}, err
	}
	tenantId, err := requireTenantAdmin(ctx)
	if err != nil {
		return StatusResponse{}, err
	}
	d, err := db()
	if err != nil {
		return StatusResponse{}, err
	}
	n, err := deleteManualOwner(d, tenantId, req.ResourceType, req.ResourceKey)
	if err != nil {
		return StatusResponse{}, err
	}
	if n > 0 {
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryOwnership,
			EventType:     audit.EventTypeOwnershipDelete,
			EventAction:   audit.EventActionDelete,
			TargetID:      req.ResourceKey,
			TableName:     "resource_owners",
			OldData:       map[string]any{"resource_type": req.ResourceType, "resource_key": req.ResourceKey},
		})
	}
	return StatusResponse{Status: "ok", Count: int(n)}, nil
}

// GetOwner resolves the effective owner of one resource.
func GetOwner(ctx *security.RequestContext, req GetOwnerRequest) (OwnerResult, error) {
	if err := common.ValidateStruct(req); err != nil {
		return OwnerResult{}, err
	}
	tenantId, err := requireTenant(ctx)
	if err != nil {
		return OwnerResult{}, err
	}
	d, err := db()
	if err != nil {
		return OwnerResult{}, err
	}
	res := resolveOne(singleDeps(d, tenantId), req.ResourceType, req.ResourceKey)
	one := []OwnerResult{res}
	fillOwnerNames(d, tenantId, one)
	return one[0], nil
}

// Resolve resolves a batch of resources in one round-trip.
func Resolve(ctx *security.RequestContext, req ResolveRequest) ([]OwnerResult, error) {
	tenantId, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	d, err := db()
	if err != nil {
		return nil, err
	}
	if len(req.Resources) == 0 {
		return []OwnerResult{}, nil
	}

	deps, err := buildBatchDeps(d, tenantId, req.Resources)
	if err != nil {
		return nil, err
	}
	out := make([]OwnerResult, 0, len(req.Resources))
	for _, r := range req.Resources {
		out = append(out, resolveOne(deps, r.ResourceType, r.ResourceKey))
	}
	fillOwnerNames(d, tenantId, out)
	return out, nil
}

// fillOwnerNames resolves display names for the found owners in-place. Best-effort:
// names are cosmetic, so a lookup error leaves OwnerName empty rather than failing.
// Scoped to the tenant so a cross-tenant owner id never resolves a foreign name.
func fillOwnerNames(d *sqlx.DB, tenantId string, results []OwnerResult) {
	userSet, groupSet := map[string]bool{}, map[string]bool{}
	for _, r := range results {
		if !r.Found {
			continue
		}
		switch r.OwnerType {
		case OwnerTypeUser:
			userSet[r.OwnerId] = true
		case OwnerTypeGroup:
			groupSet[r.OwnerId] = true
		}
	}
	if len(userSet) == 0 && len(groupSet) == 0 {
		return
	}
	userIds, groupIds := make([]string, 0, len(userSet)), make([]string, 0, len(groupSet))
	for id := range userSet {
		userIds = append(userIds, id)
	}
	for id := range groupSet {
		groupIds = append(groupIds, id)
	}
	names, err := loadOwnerNames(d, tenantId, userIds, groupIds)
	if err != nil {
		return
	}
	for i := range results {
		if !results[i].Found {
			continue
		}
		if n, ok := names[results[i].OwnerType+"\x00"+results[i].OwnerId]; ok {
			results[i].OwnerName = n
		}
	}
}

// buildBatchDeps bulk-loads owners, rules, requested workloads, and the active
// namespace set once, returning a resolver backed by in-memory maps.
func buildBatchDeps(d *sqlx.DB, tenantId string, refs []ResourceRef) (resolveDeps, error) {
	owners, err := loadAllOwners(d, tenantId)
	if err != nil {
		return resolveDeps{}, err
	}
	ownerMap := make(map[string]ResourceOwnerRow, len(owners))
	for _, o := range owners {
		ownerMap[o.ResourceType+"\x00"+o.ResourceKey] = o
	}
	var wlIds, cloudIds []string
	for _, r := range refs {
		switch r.ResourceType {
		case ResourceTypeWorkload:
			wlIds = append(wlIds, r.ResourceKey)
		case ResourceTypeCloudResource:
			cloudIds = append(cloudIds, r.ResourceKey)
		}
	}
	metas, err := loadWorkloadMetas(d, tenantId, wlIds)
	if err != nil {
		return resolveDeps{}, err
	}
	cloudMetas, err := loadCloudResourceMetas(d, tenantId, cloudIds) // no query when cloudIds is empty
	if err != nil {
		return resolveDeps{}, err
	}
	rules, err := loadEnabledRules(d, tenantId)
	if err != nil {
		return resolveDeps{}, err
	}
	nsSet, err := loadActiveNamespaceSet(d, tenantId)
	if err != nil {
		return resolveDeps{}, err
	}
	// Cloud rules are loaded lazily — only when a cloud resource is actually
	// resolved — so pure-k8s batches never query them.
	var cloudRules []OwnershipRuleRow
	cloudLoaded := false
	return resolveDeps{
		ownerOf: func(t, k string) *ResourceOwnerRow {
			if o, ok := ownerMap[t+"\x00"+k]; ok {
				return &o
			}
			return nil
		},
		workload: func(id string) *workloadMeta {
			if m, ok := metas[id]; ok {
				return &m
			}
			return nil
		},
		cloudResource: func(id string) *cloudResourceMeta {
			if m, ok := cloudMetas[id]; ok {
				return &m
			}
			return nil
		},
		namespaceActive: func(key string) bool { return nsSet[key] },
		getRules:        func() []OwnershipRuleRow { return rules },
		getCloudRules: func() []OwnershipRuleRow {
			if !cloudLoaded {
				cloudRules, _ = loadEnabledCloudRules(d, tenantId)
				cloudLoaded = true
			}
			return cloudRules
		},
	}, nil
}

// ListOwners returns stored manual owners (optionally filtered).
func ListOwners(ctx *security.RequestContext, req ListOwnersRequest) ([]OwnerDto, error) {
	tenantId, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	d, err := db()
	if err != nil {
		return nil, err
	}
	rows, err := listOwners(d, tenantId, req)
	if err != nil {
		return nil, err
	}
	out := make([]OwnerDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, toOwnerDto(r))
	}
	return out, nil
}

// CleanupOrphans purges manual rows whose resource no longer exists.
func CleanupOrphans(ctx *security.RequestContext, _ struct{}) (StatusResponse, error) {
	tenantId, err := requireTenantAdmin(ctx)
	if err != nil {
		return StatusResponse{}, err
	}
	d, err := db()
	if err != nil {
		return StatusResponse{}, err
	}
	n, err := cleanupOrphans(d, tenantId)
	if err != nil {
		return StatusResponse{}, err
	}
	return StatusResponse{Status: "ok", Count: n}, nil
}

// ---- rules ----

func validateRuleRequest(req UpsertRuleRequest) error {
	if err := common.ValidateStruct(req); err != nil {
		return err
	}
	var err error
	if req.ResourceDomain == ResourceDomainCloud {
		err = validateCloudRule(req)
	} else {
		err = validateK8sRule(req)
	}
	if err != nil {
		return err
	}
	if req.OwnerType != OwnerTypeUser && req.OwnerType != OwnerTypeGroup {
		return common.ErrorBadRequest("owner_type must be 'user' or 'group'")
	}
	return nil
}

func validateK8sRule(req UpsertRuleRequest) error {
	switch req.MatchScope {
	case MatchScopeLabel:
		if req.MatchKey == "" || req.MatchValue == "" {
			return common.ErrorBadRequest("match_key and match_value are required for label rules")
		}
	case MatchScopeNamespace:
		if req.MatchValue == "" {
			return common.ErrorBadRequest("match_value (namespace) is required for namespace rules")
		}
	case MatchScopeWorkload:
		// match_value=namespace, match_key=comma-joined workload names, account required.
		if req.CloudAccountId == "" || req.MatchValue == "" || req.MatchKey == "" {
			return common.ErrorBadRequest("cloud_account_id, namespace, and at least one workload are required for workload rules")
		}
	default:
		return common.ErrorBadRequest("match_scope must be 'label', 'namespace', or 'workload'")
	}
	return nil
}

func validateCloudRule(req UpsertRuleRequest) error {
	switch req.MatchScope {
	case MatchScopeCloudTag:
		if req.MatchKey == "" || req.MatchValue == "" {
			return common.ErrorBadRequest("tag key and value are required for cloud tag rules")
		}
	case MatchScopeCloudType, MatchScopeCloudRegion:
		if req.MatchValue == "" {
			return common.ErrorBadRequest("a value is required for this cloud rule")
		}
	case MatchScopeCloudResource:
		// match_key=comma-joined cloud_resourses.id set; account required.
		if req.CloudAccountId == "" || req.MatchKey == "" {
			return common.ErrorBadRequest("cloud_account_id and at least one resource are required for specific-resource rules")
		}
	default:
		return common.ErrorBadRequest("match_scope must be 'cloud_tag', 'cloud_type', 'cloud_region', or 'cloud_resource'")
	}
	return nil
}

func UpsertRule(ctx *security.RequestContext, req UpsertRuleRequest) (StatusResponse, error) {
	if err := validateRuleRequest(req); err != nil {
		return StatusResponse{}, err
	}
	tenantId, err := requireTenantAdmin(ctx)
	if err != nil {
		return StatusResponse{}, err
	}
	d, err := db()
	if err != nil {
		return StatusResponse{}, err
	}
	if ok, oErr := ownerExistsInTenant(d, tenantId, req.OwnerType, req.OwnerId); oErr != nil {
		return StatusResponse{}, oErr
	} else if !ok {
		return StatusResponse{}, common.ErrorBadRequest("owner not found in this tenant")
	}
	// No priority: reject a rule that overlaps an existing same-scope rule, so the
	// owner of any resource is unambiguous. req.Id excludes self when editing. The
	// conflict-check + write run under a per-tenant advisory lock so two concurrent
	// upserts can't both pass the check and create colliding rules.
	var id string
	if lErr := withTenantRuleLock(d, tenantId, func() error {
		if conflict, cErr := findConflictingRule(d, tenantId, req, req.Id); cErr != nil {
			return cErr
		} else if conflict != nil {
			return common.ErrorBadRequest(fmt.Sprintf("This overlaps the existing rule %q — edit that rule instead.", conflict.Name))
		}
		newId, uErr := upsertRuleRow(d, tenantId, req, ctx.GetSecurityContext().GetUserId())
		if uErr != nil {
			return uErr
		}
		id = newId
		return nil
	}); lErr != nil {
		return StatusResponse{}, lErr
	}
	audit.LogChange(ctx, audit.ChangeInput{
		EventCategory: audit.EventCategoryOwnership,
		EventType:     audit.EventTypeOwnershipRuleUpsert,
		EventAction:   audit.EventActionUpdate,
		TargetID:      id,
		TableName:     "ownership_rules",
		NewData:       map[string]any{"name": req.Name, "match_scope": req.MatchScope, "match_value": req.MatchValue},
	})
	return StatusResponse{Status: "ok", Id: id}, nil
}

func DeleteRule(ctx *security.RequestContext, req DeleteRuleRequest) (StatusResponse, error) {
	if err := common.ValidateStruct(req); err != nil {
		return StatusResponse{}, err
	}
	tenantId, err := requireTenantAdmin(ctx)
	if err != nil {
		return StatusResponse{}, err
	}
	d, err := db()
	if err != nil {
		return StatusResponse{}, err
	}
	n, err := deleteRuleRow(d, tenantId, req.Id)
	if err != nil {
		return StatusResponse{}, err
	}
	if n > 0 {
		audit.LogChange(ctx, audit.ChangeInput{
			EventCategory: audit.EventCategoryOwnership,
			EventType:     audit.EventTypeOwnershipRuleDelete,
			EventAction:   audit.EventActionDelete,
			TargetID:      req.Id,
			TableName:     "ownership_rules",
			OldData:       map[string]any{"id": req.Id},
		})
	}
	return StatusResponse{Status: "ok", Count: int(n)}, nil
}

func ListRules(ctx *security.RequestContext) ([]RuleDto, error) {
	tenantId, err := requireTenant(ctx)
	if err != nil {
		return nil, err
	}
	d, err := db()
	if err != nil {
		return nil, err
	}
	rows, err := listRuleRows(d, tenantId)
	if err != nil {
		return nil, err
	}
	out := make([]RuleDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRuleDto(r))
	}
	return out, nil
}

// ---- resolution core ----

type resolveDeps struct {
	ownerOf         func(resType, resKey string) *ResourceOwnerRow
	workload        func(cloudResourceId string) *workloadMeta // active workload only, nil if gone
	cloudResource   func(id string) *cloudResourceMeta         // active cloud resource only, nil if gone
	namespaceActive func(key string) bool
	getRules        func() []OwnershipRuleRow
	getCloudRules   func() []OwnershipRuleRow
}

// singleDeps wires the resolver to point DB lookups (one-resource resolve). Rules
// are loaded lazily — skipped entirely when a direct manual owner is found.
func singleDeps(d *sqlx.DB, tenantId string) resolveDeps {
	var rules, cloudRules []OwnershipRuleRow
	loaded, cloudLoaded := false, false
	return resolveDeps{
		ownerOf: func(t, k string) *ResourceOwnerRow {
			row, _ := getOwnerRow(d, tenantId, t, k)
			return row
		},
		workload: func(id string) *workloadMeta {
			m, _ := getWorkloadMeta(d, tenantId, id)
			return m
		},
		cloudResource: func(id string) *cloudResourceMeta {
			m, _ := getCloudResourceMeta(d, tenantId, id)
			return m
		},
		namespaceActive: func(key string) bool { return isNamespaceActive(d, tenantId, key) },
		getRules: func() []OwnershipRuleRow {
			if !loaded {
				rules, _ = loadEnabledRules(d, tenantId)
				loaded = true
			}
			return rules
		},
		getCloudRules: func() []OwnershipRuleRow {
			if !cloudLoaded {
				cloudRules, _ = loadEnabledCloudRules(d, tenantId)
				cloudLoaded = true
			}
			return cloudRules
		},
	}
}

// resolveOne is the shared resolution logic. Read-time orphan guard: an owner only
// resolves if its resource still exists and is active. Order: existence → direct
// manual → lazy rule (workloads) → inherited manual (namespace → cluster).
func resolveOne(deps resolveDeps, resType, resKey string) OwnerResult {
	res := OwnerResult{ResourceType: resType, ResourceKey: resKey}
	switch resType {
	case ResourceTypeWorkload:
		return resolveWorkload(deps, res, resKey)
	case ResourceTypeNamespace:
		return resolveNamespace(deps, res, resKey)
	case ResourceTypeCloudResource:
		return resolveCloudResource(deps, res, resKey)
	case ResourceTypeCloudAccount, ResourceTypeService:
		// cloud_account existence is guaranteed by the FK cascade; service is provisional.
		if row := deps.ownerOf(resType, resKey); row != nil {
			return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaSelf)
		}
	}
	return res
}

// resolveCloudResource resolves an individual cloud resource (EC2/RDS/S3/…).
// Order: existence → direct manual → lazy cloud rule (by specificity) → inherited
// manual owner of the cloud account.
func resolveCloudResource(deps resolveDeps, res OwnerResult, key string) OwnerResult {
	m := deps.cloudResource(key)
	if m == nil {
		return res // gone/inactive resource → no owner (orphan-safe)
	}
	if row := deps.ownerOf(ResourceTypeCloudResource, key); row != nil {
		return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaSelf)
	}
	if ot, oid, ok := evalCloudRules(deps.getCloudRules(), *m, key); ok {
		return withOwner(res, ot, oid, SourceRule, ViaSelf)
	}
	if row := deps.ownerOf(ResourceTypeCloudAccount, m.Account); row != nil {
		return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaCluster)
	}
	return res
}

func resolveWorkload(deps resolveDeps, res OwnerResult, key string) OwnerResult {
	wm := deps.workload(key)
	if wm == nil {
		return res // gone/inactive workload → no owner (orphan-safe)
	}
	if row := deps.ownerOf(ResourceTypeWorkload, key); row != nil {
		return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaSelf)
	}
	// Workload-specific rules (workload + label scope) own the workload directly.
	// Namespace-scope rules are evaluated lower down (as ViaNamespace) so a manual
	// namespace owner still beats them — manual beats rule at each level.
	if ot, oid, ok := evalRules(workloadScopeRules(deps.getRules()), *wm); ok {
		return withOwner(res, ot, oid, SourceRule, ViaSelf)
	}
	// Workload is active ⇒ its namespace is active; no extra check needed.
	if row := deps.ownerOf(ResourceTypeNamespace, wm.CloudAccountId+"/"+wm.Namespace); row != nil {
		return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaNamespace)
	}
	if ot, oid, ok := evalNamespaceRules(deps.getRules(), wm.CloudAccountId, wm.Namespace); ok {
		return withOwner(res, ot, oid, SourceRule, ViaNamespace)
	}
	if row := deps.ownerOf(ResourceTypeCloudAccount, wm.CloudAccountId); row != nil {
		return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaCluster)
	}
	return res
}

func resolveNamespace(deps resolveDeps, res OwnerResult, key string) OwnerResult {
	if !deps.namespaceActive(key) {
		return res // gone/inactive namespace → no owner
	}
	// Manual namespace owner wins over a namespace rule (same precedence as workloads).
	if row := deps.ownerOf(ResourceTypeNamespace, key); row != nil {
		return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaSelf)
	}
	account := accountFromNamespaceKey(key)
	// A namespace-scoped rule owns the namespace (and thereby its workloads), so
	// surface it at the namespace level too — not just on each matched workload.
	if ns := namespaceNameFromKey(key); account != "" && ns != "" {
		if ot, oid, ok := evalNamespaceRules(deps.getRules(), account, ns); ok {
			return withOwner(res, ot, oid, SourceRule, ViaSelf)
		}
	}
	if account != "" {
		if row := deps.ownerOf(ResourceTypeCloudAccount, account); row != nil {
			return withOwner(res, row.OwnerType, row.OwnerId, SourceManual, ViaCluster)
		}
	}
	return res
}

func withOwner(r OwnerResult, ownerType, ownerId, source, via string) OwnerResult {
	r.Found = true
	r.OwnerType = ownerType
	r.OwnerId = ownerId
	r.Source = source
	r.Via = via
	return r
}

func accountFromNamespaceKey(key string) string {
	if i := strings.Index(key, "/"); i > 0 {
		return key[:i]
	}
	return ""
}

func namespaceNameFromKey(key string) string {
	if i := strings.Index(key, "/"); i > 0 && i < len(key)-1 {
		return key[i+1:]
	}
	return ""
}

// deriveCloudAccountId fills cloud_account_id from the resource when the caller
// didn't pass it, so the FK cascade can clean rows up on account deletion.
func deriveCloudAccountId(d *sqlx.DB, tenantId string, req AssignOwnerRequest) string {
	if req.CloudAccountId != "" {
		return req.CloudAccountId
	}
	switch req.ResourceType {
	case ResourceTypeWorkload:
		if m, _ := getWorkloadMeta(d, tenantId, req.ResourceKey); m != nil {
			return m.CloudAccountId
		}
	case ResourceTypeNamespace:
		return accountFromNamespaceKey(req.ResourceKey)
	case ResourceTypeCloudAccount:
		return req.ResourceKey
	}
	return ""
}
