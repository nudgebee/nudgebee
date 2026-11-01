package event

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/services/account"
	"nudgebee/services/account/adapter"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/event/lifecycle"
	eventtypes "nudgebee/services/event/types"
	"nudgebee/services/eventrule"
	"nudgebee/services/eventrule/playbooks"
	integrationcore "nudgebee/services/integrations/core"
	"nudgebee/services/internal/annotations"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/llm"
	"nudgebee/services/notification"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"nudgebee/services/triage"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"gopkg.in/yaml.v2"
)

func init() {
	integrationcore.InvestigateEventFn = InvestigateEvent
	common.CacheCreateNamespace(
		podOwnerCacheNamespace,
		common.CacheNamespaceWithExpiration(podOwnerCacheTTL),
	)
	common.CacheCreateNamespace(
		disabledAccountCacheNamespace,
		common.CacheNamespaceWithExpiration(disabledAccountCacheTTL),
	)
}

// InvestigateEvent is the single funnel every event producer reaches: the
// trigger_investigation RPC (k8s-collector and cloud-collector) and every
// incoming webhook, via integrationcore.InvestigateEventFn above. Turning a
// cloud account off has to be honoured here or it is not honoured at all —
// nothing tells a customer's agent or a third-party alert source to stop
// sending, so a disabled account keeps producing and we keep running the full
// enrichment path for it, including relay calls back to that account's own
// (often unresponsive) agent. Cached because this is the per-event hot path.
const (
	disabledAccountCacheNamespace = "cloud_account_disabled"
	disabledAccountCacheTTL       = time.Minute
	cloudAccountStatusDisabled    = "disabled"
)

// Pod → owning workload (Deployment/StatefulSet/DaemonSet) lookups happen on
// the per-event hot path when ingestion left SubjectOwner empty. The k8s state
// changes only on deploy/scale events, so caching the lookup for a few minutes
// avoids hitting the metastore on every alert firing for the same pod. Negative
// hits (pod not yet in k8s_pods, race window <30s) are cached briefly so we
// don't repeatedly query for a pod the agent hasn't synced yet — the next
// event after expiry retries.
const (
	podOwnerCacheNamespace = "pod_owner_lookup"
	podOwnerCacheTTL       = 5 * time.Minute
	podOwnerCacheSep       = "|"
)

// ownerFromLabels picks workload owner (name, kind) from common alert label
// conventions emitted by prometheus instrumentation, k8s events, and Datadog.
// Returns ("", "") if no recognised key is present.
func ownerFromLabels(labels map[string]string) (string, string) {
	for _, k := range []string{"k8s_deployment_name", "deployment", "k8s.deployment.name", "kubernetes_deployment", "kube_deployment"} {
		if v := labels[k]; v != "" {
			return v, "Deployment"
		}
	}
	for _, k := range []string{"k8s_statefulset_name", "statefulset", "k8s.statefulset.name"} {
		if v := labels[k]; v != "" {
			return v, "StatefulSet"
		}
	}
	for _, k := range []string{"k8s_daemonset_name", "daemonset", "k8s.daemonset.name"} {
		if v := labels[k]; v != "" {
			return v, "DaemonSet"
		}
	}
	return "", ""
}

// podOwnerLookupSQL resolves a pod to its top-level controller by joining
// k8s_pods with k8s_workloads. Two match modes:
//   - direct: pod's immediate owner is itself a Deployment/StatefulSet/DaemonSet
//   - indirect: pod → ReplicaSet → Deployment. K8s names ReplicaSets as
//     "<deployment>-<rs-hash>", so a prefix-match against the Deployment name
//     in k8s_workloads recovers the parent without needing a separate
//     ReplicaSet table.
//
// The lookup key $3 matches the pod's own name, its workload_name, or — for a
// subject that is itself a ReplicaSet name (e.g. an alert on
// cloud-collector-server-5cbd8ddb97 with an empty owner) — the pods that belong
// to that ReplicaSet, by name prefix: its pods are named "<rs>-<suffix>". The
// workload_name arm alone cannot cover that case because the k8s collector
// stores each pod's TOP-LEVEL controller (the Deployment) in workload_name,
// never the ReplicaSet name (verified live, #34656). $4 is $3 with LIKE
// wildcards escaped, suffixed "-%".
//
// The join doubles as validation — only resolves to workloads currently known
// to k8s state. The prefix arm carries its own guard: the subject must itself
// extend the matched workload's name ($3 LIKE kw.name || '-%'), so a subject
// that IS a plain workload name can never ride a prefix collision onto a
// longer sibling (subject "app" must not gain owner "app-prod" via app-prod's
// pods). ORDER BY prefers exact matches, then length(kw.name) DESC so a
// ReplicaSet for "payment-service" doesn't false-positive against a sibling
// "payment" deployment via prefix-LIKE; last_seen DESC is the final tiebreak
// for cluster-recreate cases.
const podOwnerLookupSQL = `
	SELECT kw.name, kw.kind
	FROM k8s_pods kp
	JOIN k8s_workloads kw
	  ON kw.tenant_id = kp.tenant_id
	 AND kw.namespace = kp.namespace
	 AND (
	        (kp.workload_name = kw.name AND kp.workload_type = kw.kind)
	     OR (kp.workload_type = 'ReplicaSet' AND kw.kind = 'Deployment' AND kp.workload_name LIKE kw.name || '-%')
	     )
	WHERE kp.tenant_id = $1 AND kp.namespace = $2
	  AND (
	        kp.name = $3 OR kp.workload_name = $3
	     OR (kp.name LIKE $4 AND $3 LIKE kw.name || '-%')
	     )
	ORDER BY (kp.name = $3 OR kp.workload_name = $3) DESC, length(kw.name) DESC, kp.last_seen DESC
	LIMIT 1
`

// workloadCloudResourceSQL resolves a k8s workload (Deployment/StatefulSet/
// DaemonSet/Job/CronJob) to the cloud_resource_id assigned during k8s state
// discovery. Kind is intentionally not matched: subject_type casing differs
// across emitters (lowercase "deployment" vs k8s_workloads "Deployment") and
// (account, namespace, name) is effectively unique within an account's
// workloads. Prefers active, most-recently-seen rows for recreate cases.
const workloadCloudResourceSQL = `
	SELECT cloud_resource_id
	FROM k8s_workloads
	WHERE tenant_id = $1 AND cloud_account_id = $2 AND namespace = $3 AND name = $4
	  AND cloud_resource_id IS NOT NULL
	ORDER BY is_active DESC, last_seen DESC
	LIMIT 1
`

// configMapConsumersSQL resolves a ConfigMap to the workloads whose pod
// template references it — mounted as a volume, or pulled in via envFrom /
// valueFrom.configMapKeyRef. The knowledge graph's k8s source walks the real
// pod spec on every build (knowledge_graph/sources/k8s/configsecret.go) and
// stores the result as Workload -USES_CONFIG-> ConfigMap, so this reads an
// already-computed relationship instead of re-deriving one from names.
//
// Deliberately no `level` filter: k8s edges are written without one, and
// filtering on it returns nothing at all rather than failing loudly.
const configMapConsumersSQL = `
	SELECT w.properties->>'name' AS name, COALESCE(w.properties->>'kind', '') AS kind
	FROM knowledge_graph_node cm
	JOIN knowledge_graph_edge e
	  ON e.destination_node_id = cm.id
	 AND e.relationship_type = 'USES_CONFIG'
	 AND e.is_active = true
	JOIN knowledge_graph_node w
	  ON w.id = e.source_node_id
	 AND w.is_active = true
	WHERE cm.tenant_id = $1 AND cm.cloud_account_id = $2
	  AND cm.node_type = 'ConfigMap' AND cm.is_active = true
	  AND cm.properties->>'namespace' = $3
	  AND cm.properties->>'name' = $4
	  AND w.properties->>'name' IS NOT NULL
	ORDER BY 1, 2
`

// ingressBackendsSQL resolves an Ingress to the workloads it routes traffic
// to, two hops through the knowledge graph:
//
//	Ingress -ROUTES_TO_SERVICE-> K8sService <-EXPOSES- Workload
//
// Note the second hop runs backwards. EXPOSES is declared as
// "K8sService → Workload" (knowledge_graph/core/types.go) but written
// Workload → K8sService, and a query built from the comment returns nothing
// at all rather than failing.
//
// An Ingress name is a particularly bad guess at a workload name: in our own
// dev cluster `nudgebee-oss/app` routes to service `notifications`, and
// `nudgebee/k8s-collector` fans out to two Deployments. Matching by name
// picks the same-named workload when one exists and silently drops the rest.
const ingressBackendsSQL = `
	SELECT DISTINCT w.properties->>'name' AS name, COALESCE(w.properties->>'kind', '') AS kind
	FROM knowledge_graph_node ing
	JOIN knowledge_graph_edge routes
	  ON routes.source_node_id = ing.id
	 AND routes.relationship_type = 'ROUTES_TO_SERVICE'
	 AND routes.is_active = true
	JOIN knowledge_graph_node svc
	  ON svc.id = routes.destination_node_id
	 AND svc.is_active = true
	JOIN knowledge_graph_edge exposes
	  ON exposes.destination_node_id = svc.id
	 AND exposes.relationship_type = 'EXPOSES'
	 AND exposes.is_active = true
	JOIN knowledge_graph_node w
	  ON w.id = exposes.source_node_id
	 AND w.is_active = true
	WHERE ing.tenant_id = $1 AND ing.cloud_account_id = $2
	  AND ing.node_type = 'Ingress' AND ing.is_active = true
	  AND ing.properties->>'namespace' = $3
	  AND ing.properties->>'name' = $4
	  AND w.properties->>'name' IS NOT NULL
	ORDER BY 1, 2
`

// podCloudResourceSQL is the fallback for pod-subject events whose owning
// workload could not be determined: link the pod's own cloud_resource_id.
const podCloudResourceSQL = `
	SELECT cloud_resource_id
	FROM k8s_pods
	WHERE tenant_id = $1 AND cloud_account_id = $2 AND namespace = $3 AND name = $4
	  AND cloud_resource_id IS NOT NULL
	ORDER BY is_active DESC, last_seen DESC
	LIMIT 1
`

// nodeCloudResourceSQL resolves a node-subject event to the node's own
// cloud_resource_id. Nodes are cluster-scoped, so the match is by name within
// the account. k8s_nodes has no last_seen; updated_at stands in for it.
const nodeCloudResourceSQL = `
	SELECT cloud_resource_id
	FROM k8s_nodes
	WHERE tenant_id = $1 AND cloud_account_id = $2 AND name = $3
	ORDER BY is_active DESC, updated_at DESC NULLS LAST
	LIMIT 1
`

// sqlLikeEscaper escapes SQL LIKE wildcards in a subject name before it is used
// as a prefix pattern ($4 of podOwnerLookupSQL). Package-level: lookupPodOwner
// runs on the per-event hot path, so the replacer must not be rebuilt per call.
var sqlLikeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// lookupPodOwner resolves a pod → owning workload via the cached k8s state
// snapshot (k8s_pods JOIN k8s_workloads). Returns ("", "") when the pod is
// unknown — caller leaves SubjectOwner empty rather than guess. Errors are
// logged but treated as a soft miss; we never block event ingestion on this
// best-effort enrichment.
func lookupPodOwner(sc *security.RequestContext, dbms *database.DatabaseManager, tenantId, namespace, podName string) (string, string) {
	if tenantId == "" || namespace == "" || podName == "" {
		return "", ""
	}

	cacheKey := tenantId + podOwnerCacheSep + namespace + podOwnerCacheSep + podName
	if cached, hit := common.CacheGet(podOwnerCacheNamespace, cacheKey); hit {
		parts := strings.SplitN(string(cached), podOwnerCacheSep, 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
		return "", "" // negative-cached
	}

	// $4: the subject as a pod-name prefix pattern ("<subject>-%"), LIKE wildcards
	// escaped — webhook subjects are not guaranteed to be clean k8s names (e.g. a
	// datname like "temporal_visibility" would otherwise wildcard-match on "_").
	likePrefix := sqlLikeEscaper.Replace(podName) + "-%"

	var ownerName, ownerKind string
	err := dbms.Db.QueryRowx(podOwnerLookupSQL, tenantId, namespace, podName, likePrefix).Scan(&ownerName, &ownerKind)
	switch {
	case err == sql.ErrNoRows:
		// Negative-cache so we don't keep querying for a pod that isn't in
		// k8s_pods yet (race window or pod not managed by k8s-collector).
		_ = common.CacheSet(podOwnerCacheNamespace, cacheKey, []byte(""))
		return "", ""
	case err != nil:
		sc.GetLogger().Warn("pod_owner_lookup: query failed", "error", err, "tenant", tenantId, "namespace", namespace, "pod", podName)
		return "", ""
	}

	_ = common.CacheSet(podOwnerCacheNamespace, cacheKey, []byte(ownerName+podOwnerCacheSep+ownerKind))
	return ownerName, ownerKind
}

// isCloudAccountDisabled reports whether the account has been turned off.
//
// Fail-open by design: an unknown account, an unexpected status value, a
// malformed id, or a query error all report false, i.e. "keep processing".
// Only an explicit 'disabled' stops an event, so a lookup problem can never
// silently halt ingestion for a live tenant. Unknown accounts are deliberately
// not cached — a row that does not exist yet is a race, not a decision.
func isCloudAccountDisabled(sc *security.RequestContext, dbms *database.DatabaseManager, accountId string) bool {
	if accountId == "" {
		return false
	}
	// Guard the ::uuid cast below: account ids reaching here come from webhook
	// payloads too, and a malformed one would otherwise error on every event.
	if _, err := uuid.Parse(accountId); err != nil {
		return false
	}

	if cached, hit := common.CacheGet(disabledAccountCacheNamespace, accountId); hit {
		return string(cached) == cloudAccountStatusDisabled
	}

	var status string
	err := dbms.Db.Get(&status, `SELECT COALESCE(status, '') FROM cloud_accounts WHERE id = $1::uuid`, accountId)
	switch {
	case err == sql.ErrNoRows:
		return false
	case err != nil:
		sc.GetLogger().Warn("cloud_account_status: lookup failed, processing event", "error", err, "account_id", accountId)
		return false
	}

	_ = common.CacheSet(disabledAccountCacheNamespace, accountId, []byte(status))
	return status == cloudAccountStatusDisabled
}

func GetEvent(context *security.RequestContext, id string) (models.Event, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return models.Event{}, err
	}
	r := databaseManager.Db.QueryRowx(`SELECT id, created_at, updated_at, finding_id, title, description, source, aggregation_key,
			failure, finding_type, category, priority, subject_type, subject_name, subject_namespace,
			subject_node, service_key, cluster, ends_at, starts_at, fingerprint, evidences, tenant,
			cloud_account_id, cloud_resource_id, status, nb_status, nb_status_changed_at,
			nb_status_changed_by, snoozed_until, principal, subject_owner, subject_owner_kind, labels,
			urgency, computed_score, computed_priority, score_factors, score_confidence
		FROM events WHERE id = $1`, id)
	if r.Err() != nil {
		return models.Event{}, r.Err()
	}
	recommendation := models.Event{}
	err = r.StructScan(&recommendation)
	return recommendation, err
}

func ListEventResolutions(context *security.RequestContext, rescommendationId string) ([]models.EventResolution, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []models.EventResolution{}, err
	}
	r, err := databaseManager.Db.Queryx(`SELECT id, created_at, updated_at, event_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at
		FROM event_resolution WHERE event_id = $1`, rescommendationId)
	defer func() {
		if r != nil {
			if cerr := r.Close(); cerr != nil {
				slog.Error("event: error closing rows", "error", cerr)
			}
		}
	}()
	if err != nil {
		return []models.EventResolution{}, err
	}
	defer func() { _ = r.Close() }()

	resolutions := []models.EventResolution{}
	for r.Next() {
		resolution := models.EventResolution{}
		err = r.StructScan(&resolution)
		if err != nil {
			return []models.EventResolution{}, err
		}
		resolutions = append(resolutions, resolution)
	}
	if err := r.Err(); err != nil {
		return []models.EventResolution{}, fmt.Errorf("error iterating event resolution rows: %w", err)
	}
	return resolutions, nil
}

func updateContainerImage(pod map[any]any, containerName string, newImage string) (map[any]any, error) {
	spec, ok := pod["spec"].(map[any]any)
	if !ok {
		return pod, fmt.Errorf("spec not found in pod")
	}

	containers, ok := spec["template"].(map[any]any)["spec"].(map[any]any)["containers"].([]any)
	if !ok {
		return pod, fmt.Errorf("containers not found in spec")
	}
	for _, c := range containers {
		container, ok := c.(map[any]any)
		if !ok {
			return pod, fmt.Errorf("invalid container structure")
		}

		if container["name"] == containerName {
			container["image"] = newImage
			return pod, nil
		}
	}
	return pod, fmt.Errorf("container %s not found", containerName)
}

func ApplyEventResolution(ctx *security.RequestContext, query EventRecommendationApplyRequest) (EventRecommendationApplyResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(query.AccountId, security.SecurityAccessTypeCreate) {
		return EventRecommendationApplyResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	queryData, ok := query.Data.(map[string]any)
	if !ok {
		return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: invalid data format, expected map")
	}
	a, err := account.GetAccount(ctx, query.AccountId)
	if err != nil {
		ctx.GetLogger().Error("error getting account", "error", err)
		return EventRecommendationApplyResponse{}, err
	}
	if a.Id == "" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: account not found - %s", query.AccountId)
	}

	if a.AccountType != "kubernetes" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: account type not supported - %s", a.AccountType)
	}

	r, err := GetEvent(ctx, query.EventId)
	if err != nil {
		ctx.GetLogger().Error("error getting events", "error", err)
		return EventRecommendationApplyResponse{}, err
	}
	if r.Id == "" {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: event not found - %s", query.EventId)
	}
	// The event is loaded by id alone, and every change below is dispatched to the
	// event's own account. The account authorized above is the one the request
	// named, so the two have to be the same account.
	if r.CloudAccountId == nil || *r.CloudAccountId != query.AccountId {
		return EventRecommendationApplyResponse{}, common.ErrorUnauthorized("error: account access not found")
	}

	var cr models.Resource
	if r.CloudResourceId != nil {
		cr, err = account.GetResource(ctx, *r.CloudResourceId)
		if err != nil {
			ctx.GetLogger().Error("error getting resource", "error", err)
			return EventRecommendationApplyResponse{}, err
		}
	} else if r.SubjectNamespace != nil && *r.SubjectNamespace != "" && r.SubjectName != nil && *r.SubjectName != "" {
		// Event was never linked to a cloud_resource_id (e.g. resolved before the
		// resource synced). Fall back to resolving the K8s workload by namespace+name
		// within the validated account so revert/replace can still find the resource.
		// SubjectType disambiguates same-named resources of different Kinds.
		subjectType := ""
		if r.SubjectType != nil {
			subjectType = *r.SubjectType
		}
		cr, err = account.GetResourceByWorkload(ctx, query.AccountId, *r.SubjectNamespace, *r.SubjectName, subjectType)
		if err != nil {
			ctx.GetLogger().Error("error resolving resource from subject", "error", err)
			return EventRecommendationApplyResponse{}, err
		}
	} else {
		cr = models.Resource{}
	}

	// Determine the provider
	switch query.Provider {
	case "git":
		// "git" means: raise a PR, auto-detect whether GitHub or GitLab from annotation
		if cr.Id == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: resource not found, cannot detect git provider")
		}
		if r.CloudAccountId == nil {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: cloud account id not found")
		}
		gitRepoURL, gitErr := adapter.GetGitRepoURLFromWorkload(ctx, cr, *r.CloudAccountId)
		if gitErr != nil {
			ctx.GetLogger().Error("error getting git repo URL from workload", "error", gitErr)
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: failed to get git repo annotation - %w", gitErr)
		}
		if gitRepoURL == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: %s annotation not found on workload", annotations.CIGitRepo)
		}
		detected := adapter.DetectGitProviderFromURL(gitRepoURL)
		if detected == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("event resolution: unable to detect git provider from URL - %s", gitRepoURL)
		}
		query.Provider = detected
		ctx.GetLogger().Info("detected git provider from annotation", "provider", detected, "repo_url", gitRepoURL)
	case "":
		query.Provider = "kubernetes"
	}

	adptr := adapter.GetAdapter(query.Provider)
	existingRecommendations, err := ListEventResolutions(ctx, r.Id)

	if err != nil {
		ctx.GetLogger().Error("failed to fetch existing recommendations", "error", err)
		return EventRecommendationApplyResponse{}, err
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return EventRecommendationApplyResponse{}, err
	}

	// insert resolution
	statusMessage := "Configuring"
	cardId, _ := queryData["card_id"].(string)
	found, recommendationResolutionId := hasInProgressRecommendation(existingRecommendations, cardId)
	resolutionCreatedAt := time.Now().UTC()
	resolution := models.EventResolution{
		Id:              recommendationResolutionId,
		CreatedAt:       &resolutionCreatedAt,
		UpdatedAt:       &resolutionCreatedAt,
		EventId:         r.Id,
		Type:            models.RecommendationResolutionTypeDeploymentChange,
		Data:            models.NewJsonObject(query),
		Status:          models.RecommendationResolutionStatusInProgress,
		TypeReferenceId: "",
		ResolverType:    models.RecommendationResolutionResolverTypeUser,
		ResolverId:      ctx.GetSecurityContext().GetUserId(),
		StatusMessage:   &statusMessage,
	}
	convertedRecommendationResolution := []models.RecommendationResolution{}
	recommendationRequest := adapter.ApplyRecommendationRequest{}

	revert, okRevert := queryData["revert"].(bool)
	raisePR, okRaisePR := queryData["raisePR"].(bool)
	// Undoing a previous attempt is not a card action — it re-applies what that attempt replaced,
	// whatever the card was. It is handled once here rather than per card, so an action becomes
	// undoable by recording a before-state, not by adding another branch below.
	undoResolutionID, _ := queryData["undo_resolution_id"].(string)
	// AggregationKey is a nullable column and is dereferenced throughout the
	// branches below; fail fast instead of panicking on a nil pointer.
	if r.AggregationKey == nil {
		return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: event has no aggregation key")
	}
	if undoResolutionID != "" {
		undoRequest, action, err := buildUndoRequest(ctx, r.Id, undoResolutionID)
		if err != nil {
			return EventRecommendationApplyResponse{}, err
		}
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "EventResolution",
				RuleName:       *r.AggregationKey,
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":    *r.CloudAccountId,
					"action_name":   action,
					"action_params": undoRequest,
				}),
				AccountObjectId: &query.EventId,
			},
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}
	} else if *r.AggregationKey == "KubePersistentVolumeFillingUp" || *r.AggregationKey == "KubernetesVolumeOutOfDiskSpace" {
		if queryData["size"] == "" || queryData["size"] == nil {
			return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: to increase persistent volume size is required")
		}
		var persistentVolumeClaimName = ""
		if r.Evidences.IsArray() {
			for _, item := range r.Evidences.Array() {
				switch v := item.(type) {
				case map[string]any:
					if evidenceType, ok := v["type"].(string); ok {
						if evidenceType == "table" {
							if dataMap, ok := v["data"].(map[string]any); ok {
								if tableName, ok := dataMap["table_name"].(string); ok && tableName == "*Alert labels*" {
									if rows, ok := dataMap["rows"].([]any); ok {
										for _, row := range rows {
											if rowSlice, ok := row.([]any); ok && len(rowSlice) == 2 {
												if label, ok := rowSlice[0].(string); ok && label == "persistentvolumeclaim" {
													if value, ok := rowSlice[1].(string); ok {
														persistentVolumeClaimName = value
													}
												}
											}
										}
									}
								}
							}
						}
					}
				default:
					slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
				}
			}
		}

		if persistentVolumeClaimName == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("recommendation: persistent volume claim not found")
		}
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "EventResolution",
				RuleName:       *r.AggregationKey,
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":  *r.CloudAccountId,
					"action_name": "rightsize_pvc",
					"action_params": map[string]any{
						"name":      persistentVolumeClaimName,
						"namespace": r.SubjectNamespace,
						"size":      queryData["size"].(string) + "Gi",
					},
				}),
				AccountObjectId: &query.EventId,
			},
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}
	} else if *r.AggregationKey == "CPUThrottlingHigh" {
		if common.IsEmptyStringPointer(r.SubjectOwnerKind) || common.IsEmptyStringPointer(r.SubjectOwner) {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: kind or owner is not correct")
		}
		if *r.SubjectOwnerKind != "Pod" {
			size, sizeOk := queryData["size"].(string)
			restart, restartOk := queryData["restart"].(bool)
			increaseReplicas, increaseReplicasOk := queryData["increase_replicas"].(string)
			if !sizeOk || !restartOk || !increaseReplicasOk {
				return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: invalid or missing data for 'size', 'restart', or 'increase_replicas'")
			}
			if size != "" {
				containerName := ""
				if r.Evidences.IsArray() {
					for _, item := range r.Evidences.Array() {
						switch v := item.(type) {
						case map[string]any:
							if evidenceType, ok := v["type"].(string); ok {
								if evidenceType == "table" {
									if dataMap, ok := v["data"].(map[string]any); ok {
										if tableName, ok := dataMap["table_name"].(string); ok && tableName == "*Alert labels*" {
											if rows, ok := dataMap["rows"].([]any); ok {
												for _, row := range rows {
													if rowSlice, ok := row.([]any); ok && len(rowSlice) == 2 {
														if label, ok := rowSlice[0].(string); ok && label == "container" {
															if value, ok := rowSlice[1].(string); ok {
																containerName = value
															}
														}
													}
												}
											}
										}
									}
								}
							}
						default:
							slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
						}
					}
				}
				if containerName == "" {
					return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: container name cannot be empty")
				}
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "CPUThrottlingHigh",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "rightsizing_resource",
							"action_params": map[string]any{
								"name":       r.SubjectOwner,
								"namespace":  r.SubjectNamespace,
								"kind":       r.SubjectOwnerKind,
								"containers": []map[string]any{{"cpu_limit": size, "container_name": containerName}},
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if increaseReplicas != "" {
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "CPUThrottlingHigh",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "replica_rightsizing",
							"action_params": map[string]any{
								"name":          r.SubjectOwner,
								"namespace":     r.SubjectNamespace,
								"kind":          r.SubjectOwnerKind,
								"replica_count": increaseReplicas,
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if restart {
				restartAction, restartParams := restartActionFor(r)
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "CPUThrottlingHigh",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":    *r.CloudAccountId,
							"action_name":   restartAction,
							"action_params": restartParams,
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			}
		} else {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: unsupported service type- %s", *r.SubjectOwnerKind)
		}
	} else if *r.AggregationKey == "PodMemoryReachingLimit" {
		if common.IsEmptyStringPointer(r.SubjectOwnerKind) || common.IsEmptyStringPointer(r.SubjectOwner) {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: kind or owner is not correct")
		}
		size, sizeOk := queryData["size"].(string)
		restart, restartOk := queryData["restart"].(bool)
		increaseReplicas, increaseReplicasOk := queryData["increase_replicas"].(string)
		if !sizeOk || !restartOk || !increaseReplicasOk {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: invalid or missing data for 'size', 'restart', or 'increase_replicas'")
		}
		if *r.SubjectOwnerKind != "Pod" {
			if size != "" {
				containerName, _ := queryData["containerName"].(string)
				if r.Evidences.IsArray() {
					for _, item := range r.Evidences.Array() {
						switch v := item.(type) {
						case map[string]any:
							if evidenceType, ok := v["type"].(string); ok {
								if evidenceType == "table" {
									if dataMap, ok := v["data"].(map[string]any); ok {
										if tableName, ok := dataMap["table_name"].(string); ok && tableName == "*Alert labels*" {
											if rows, ok := dataMap["rows"].([]any); ok {
												for _, row := range rows {
													if rowSlice, ok := row.([]any); ok && len(rowSlice) == 2 {
														if label, ok := rowSlice[0].(string); ok && label == "container" {
															if value, ok := rowSlice[1].(string); ok {
																containerName = value
															}
														}
													}
												}
											}
										}
									}
								}
							}
						default:
							slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", v))
						}
					}
				}
				if containerName == "" {
					return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: container name cannot be empty")
				}
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "PodMemoryReachingLimit",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "rightsizing_resource",
							"action_params": map[string]any{
								"name":       r.SubjectOwner,
								"namespace":  r.SubjectNamespace,
								"kind":       r.SubjectOwnerKind,
								"containers": []map[string]any{{"memory_limit": size, "container_name": containerName}},
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if increaseReplicas != "" {
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "PodMemoryReachingLimit",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":  *r.CloudAccountId,
							"action_name": "replica_rightsizing",
							"action_params": map[string]any{
								"name":          r.SubjectOwner,
								"namespace":     r.SubjectNamespace,
								"kind":          r.SubjectOwnerKind,
								"replica_count": increaseReplicas,
							},
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			} else if restart {
				restartAction, restartParams := restartActionFor(r)
				recommendationRequest = adapter.ApplyRecommendationRequest{
					Data: query.Data.(map[string]any),
					Recommendation: models.Recommendation{
						Category:       "EventResolution",
						RuleName:       "PodMemoryReachingLimit",
						Id:             r.Id,
						CloudAccountId: *r.CloudAccountId,
						TenantId:       *r.Tenant,
						Recommendation: models.NewJsonObject(map[string]any{
							"account_id":    *r.CloudAccountId,
							"action_name":   restartAction,
							"action_params": restartParams,
						}),
						AccountObjectId: &query.EventId,
					},
					Resource:       cr,
					ProviderConfig: query.ProviderConfig,
				}
			}
		} else {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: unsupported service type- %s", *r.SubjectOwnerKind)
		}
	} else if *r.AggregationKey == "image_pull_backoff_reporter" {
		imageChangeContainerName, _ := queryData["imageChangeContainerName"].(string)
		imageNameWithTag, _ := queryData["imageNameWithTag"].(string)
		if imageChangeContainerName == "" || imageNameWithTag == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: imageChangeContainerName and imageNameWithTag are required")
		}

		resp, err1 := relay.Execute(relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				ActionName: "get_resource_yaml",
				AccountID:  *r.CloudAccountId,
				ActionParams: map[string]any{
					"name":      r.SubjectOwner,
					"namespace": r.SubjectNamespace,
					"kind":      r.SubjectOwnerKind,
				},
			},
		})
		if err1 != nil {
			return EventRecommendationApplyResponse{}, err1
		}
		dataMap, ok := resp["data"].(map[string]any)
		if !ok {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: unexpected relay response shape (data is not an object)")
		}
		var result map[any]any
		if v, ok := dataMap["findings"]; ok {
			findings, isArr := v.([]any)
			if isArr && len(findings) > 0 {
				firstFinding, isMap := findings[0].(map[string]any)
				if !isMap {
					return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: first finding is not an object")
				}
				if evidenceRaw, ok := firstFinding["evidence"]; ok {
					evidence, isArr := evidenceRaw.([]any)
					if isArr && len(evidence) > 0 {
						data := []map[string]any{}
						firstEvidence, isMap := evidence[0].(map[string]any)
						if !isMap {
							return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: first evidence is not an object")
						}
						evidenceDataStr, isStr := firstEvidence["data"].(string)
						if !isStr {
							return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: evidence data missing or not a string")
						}
						err := common.UnmarshalJson([]byte(evidenceDataStr), &data)
						if err != nil {
							return EventRecommendationApplyResponse{}, err
						}
						for _, d := range data {
							if d["type"] == "yaml" {
								if encodedData, ok := d["data"].(string); ok {
									encodedData = strings.TrimPrefix(encodedData, "b'")
									encodedData = strings.TrimSuffix(encodedData, "'")
									decodedBytes, err2 := base64.StdEncoding.DecodeString(encodedData)
									if err2 != nil {
										return EventRecommendationApplyResponse{}, err2
									}
									err3 := yaml.Unmarshal(decodedBytes, &result)
									if err3 != nil {
										return EventRecommendationApplyResponse{}, err3
									}
								} else {
									return EventRecommendationApplyResponse{}, fmt.Errorf("data field is not a string")
								}
							}
						}
					} else {
						return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: evidence is missing, not an array, or empty")
					}
				}
			} else {
				return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: findings is missing, not an array, or empty")
			}
		}
		updatedResult, err4 := updateContainerImage(result, imageChangeContainerName, imageNameWithTag)
		if err4 != nil {
			return EventRecommendationApplyResponse{}, err4
		}
		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "EventResolution",
				RuleName:       *r.AggregationKey,
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":    *r.CloudAccountId,
					"action_name":   "replace_workload",
					"action_params": updatedResult,
				}),
				AccountObjectId: &query.EventId,
			},
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}
	} else if okRevert {
		if revert {
			// Undo re-applies what the change introduced. It is the same operation in the other
			// direction, so it goes through the same builder, dispatch, audit and record.
			undo, _ := queryData["undo"].(bool)
			// The operator can pick WHICH recorded change to undo. Without a selection this is
			// the alert's own attached change, which is what the single-change card sends.
			changeEventId, _ := queryData["change_event_id"].(string)
			changeSource, err := resolveRevertSource(ctx, r, changeEventId)
			if err != nil {
				return EventRecommendationApplyResponse{}, err
			}
			request, err := getRevertRecommendationRequest(cr, changeSource, undo)
			if err != nil {
				return EventRecommendationApplyResponse{}, err
			}
			if err := assertNoLaterChangeOnSamePaths(ctx, changeSource, request); err != nil {
				return EventRecommendationApplyResponse{}, err
			}
			recommendationRequest = adapter.ApplyRecommendationRequest{
				Data: query.Data.(map[string]any),
				Recommendation: models.Recommendation{
					Category:       "EventResolution",
					RuleName:       *r.AggregationKey,
					Id:             r.Id,
					CloudAccountId: *r.CloudAccountId,
					TenantId:       *r.Tenant,
					Recommendation: models.NewJsonObject(map[string]any{
						"account_id":    *r.CloudAccountId,
						"action_name":   "replace_workload",
						"action_params": request,
					}),
					AccountObjectId: &query.EventId,
				},
				Resource:       cr,
				ProviderConfig: query.ProviderConfig,
			}
		} else {
			ctx.GetLogger().Error("error applying at revert resolution", "error", "select atleast one resolution")
			return EventRecommendationApplyResponse{}, fmt.Errorf("select atleast one resolution")
		}
	} else if *r.AggregationKey == "KubeNodeNotReady" {
		cordon, _ := queryData["cordon"].(bool)
		drain, _ := queryData["drain"].(bool)
		actionName := ""
		if cordon {
			actionName = "cordon"
		} else if drain {
			actionName = "drain"
		} else {
			ctx.GetLogger().Error("error applying resolution", "error", "cordon or drain action is required")
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("cordon or drain action is required")
		}
		if cordon || drain {
			recommendationRequest = adapter.ApplyRecommendationRequest{
				Data: query.Data.(map[string]any),
				Recommendation: models.Recommendation{
					Category:       "EventResolution",
					RuleName:       *r.AggregationKey,
					Id:             r.Id,
					CloudAccountId: *r.CloudAccountId,
					TenantId:       *r.Tenant,
					Recommendation: models.NewJsonObject(map[string]any{
						"account_id":  *r.CloudAccountId,
						"action_name": actionName,
						"action_params": map[string]any{
							"name": r.SubjectName,
						},
					}),
					AccountObjectId: &query.EventId,
				},
				Resource:       cr,
				ProviderConfig: query.ProviderConfig,
			}
		}
	} else if okRaisePR && queryData["card_id"] != "MemoryAllocationCard" {
		// Guarded to exclude MemoryAllocationCard: that card raises a PR for a
		// CPU/memory rightsizing change (a mechanical values-file edit), not an
		// AI code fix. It shares the "raisePR" flag with this branch's AskAiCard
		// flow but must fall through to the generic RightSizing branch below,
		// which already builds the request shape ApplyRightsizingRecommendationUsingCodeAgent
		// expects (Category=RightSizing/pod_right_sizing) instead of the
		// event_log_analysis-derived code-diff path here.
		if !raisePR {
			return EventRecommendationApplyResponse{}, fmt.Errorf("resolution: raisePR must be true to create a pull request")
		}
		resolution.Type = models.RecommendationResolutionTypePullRequest

		// Re-derive the fix through the code agent. The log-analysis step already
		// ran agent_code_2 in propose mode (fix, no PR) and stored the drafted
		// diff under source_updates.gitDiff. Raising the PR dispatches agent_code_2
		// again (mode=fix, raise_pr=true): the stored diff and the analysis's
		// root cause are passed as intent, and the agent re-applies or adapts the
		// fix against the current code before opening the PR. The intent is to fix
		// the identified issue, not to blindly replay a possibly-stale patch.
		//
		// event_log_analysis is keyed by (cloud_account_id, event_fingerprint,
		// event_aggregation_key, analysis_type) — one row per recurring alert, not
		// per event row — matching how the RCA read path (llm-server's
		// GetEventAnalysis) looks it up. A duplicate occurrence shares the
		// fingerprint with the original event that actually completed the
		// analysis, so filtering by this event's own id here (as the query used
		// to) never matches for a duplicate and previously surfaced a raw
		// sql.ErrNoRows.
		if r.Fingerprint == nil || r.AggregationKey == nil {
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("event has no fingerprint or aggregation key; cannot look up its log analysis")
		}
		// Select the status alongside the analysis instead of filtering on
		// status = 'COMPLETED'. Filtering collapsed three distinct states —
		// still-running, failed, and genuinely-missing — into a single
		// sql.ErrNoRows, so a failed analysis surfaced a misleading "wait for it
		// Reading the status back lets us report each state
		// accurately. We fetch the mapped log analysis entry for this event ID,
		// falling back to the latest log analysis for this fingerprint if unmapped.
		// event_id is a uuid column, so bind NULL rather than "" — the join then
		// simply never matches instead of erroring on the cast.
		var dbEventId any
		if query.EventId != "" {
			dbEventId = query.EventId
		}
		// Order on COALESCE(updated_at, recorded_at): updated_at is nullable and
		// NULLs sort first on DESC, which would rank legacy rows as the newest.
		eventLogAnalysisRow := dbms.Db.QueryRowx(`
			SELECT ela.analysis, ela.status, ela.status_reason
			FROM event_log_analysis ela
			LEFT JOIN event_analysis_mapping eam ON ela.id = eam.analysis_id AND eam.event_id = $4 AND eam.analysis_type = 'log_analysis'
			WHERE ela.analysis_type = 'log_analysis' AND ela.cloud_account_id = $1 AND ela.event_fingerprint = $2 AND ela.event_aggregation_key = $3
			ORDER BY (eam.event_id IS NOT NULL) DESC, COALESCE(ela.updated_at, ela.recorded_at) DESC
			LIMIT 1
		`, query.AccountId, *r.Fingerprint, *r.AggregationKey, dbEventId)
		if eventLogAnalysisRow.Err() != nil {
			return EventRecommendationApplyResponse{}, fmt.Errorf("invalid event_log_analysis - %s", query.EventId)
		}
		eventLogAnalysisDetail := map[string]any{}
		if err = eventLogAnalysisRow.MapScan(eventLogAnalysisDetail); err != nil {
			// No log_analysis row at all: the user clicked Raise PR before the
			// analysis was created. Surface that instead of the raw sql.ErrNoRows,
			// which is meaningless in the UI and invisible in the logs.
			if errors.Is(err, sql.ErrNoRows) {
				ctx.GetLogger().Warn("raise PR requested before event log analysis started", "event_id", query.EventId, "account_id", query.AccountId)
				return EventRecommendationApplyResponse{}, common.ErrorBadRequest("log analysis for this event has not completed yet; wait for it to finish and retry")
			}
			ctx.GetLogger().Error("failed to load event log analysis", "error", err, "event_id", query.EventId, "account_id", query.AccountId)
			return EventRecommendationApplyResponse{}, err
		}
		// The row exists but may not be usable yet. Report a failed run distinctly
		// from a still-running one so the user knows whether to re-run the analysis
		// or simply wait and retry.
		if analysisStatus, _ := eventLogAnalysisDetail["status"].(string); analysisStatus != "COMPLETED" {
			if analysisStatus == "FAILED" {
				statusReason, _ := eventLogAnalysisDetail["status_reason"].(string)
				ctx.GetLogger().Warn("raise PR requested but event log analysis failed", "event_id", query.EventId, "account_id", query.AccountId, "status_reason", statusReason)
				return EventRecommendationApplyResponse{}, common.ErrorBadRequest("log analysis for this event failed; re-run the analysis before raising a PR")
			}
			ctx.GetLogger().Warn("raise PR requested before event log analysis completed", "event_id", query.EventId, "account_id", query.AccountId, "status", analysisStatus)
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("log analysis for this event has not completed yet; wait for it to finish and retry")
		}
		analysisStr, _ := eventLogAnalysisDetail["analysis"].(string)
		var result map[string]any
		if err := common.UnmarshalJson([]byte(analysisStr), &result); err != nil {
			return EventRecommendationApplyResponse{}, err
		}
		sourceUpdates, ok := result["source_updates"].(map[string]any)
		if !ok {
			return EventRecommendationApplyResponse{}, fmt.Errorf("failed to parse 'source_updates' from result")
		}
		gitDiff, _ := sourceUpdates["gitDiff"].(string)
		if strings.TrimSpace(gitDiff) == "" {
			return EventRecommendationApplyResponse{}, fmt.Errorf("no proposed code fix is stored for this event; re-run the analysis before raising a PR")
		}
		filePath, _ := sourceUpdates["file_path"].(string)
		// The repo the log-analysis already resolved. The adapter parses the
		// org/repo from it and passes it straight to the code agent, which
		// resolves its own git credentials.
		sourceDetails, _ := result["source_details"].(map[string]any)
		gitRepo, _ := sourceDetails[annotations.WorkloadGitRepo].(string)
		// Optional free-text the user added in the Raise-PR panel; threaded into
		// the agent prompt as guidance that steers how the fix is implemented.
		guidance, _ := queryData["guidance"].(string)
		// The identified root cause the agent should fix. Combine the analysis's
		// title, root-cause narrative, and fix explanation so the agent works from
		// the issue we identified rather than the (possibly stale) diff alone.
		var fixContextParts []string
		for _, v := range []any{
			result["title"],
			result["root_cause_analysis"],
			result["description"],
			sourceUpdates["explanation"],
		} {
			if s, _ := v.(string); strings.TrimSpace(s) != "" {
				fixContextParts = append(fixContextParts, strings.TrimSpace(s))
			}
		}
		fixContext := strings.Join(fixContextParts, "\n\n")

		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: queryData,
			Recommendation: models.Recommendation{
				Category:       "EventResolutionRaisePr",
				RuleName:       *r.AggregationKey,
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					"account_id":  *r.CloudAccountId,
					"git_repo":    gitRepo,
					"fileName":    filePath,
					"fix_context": fixContext,
					"gitDiff":     gitDiff,
					"guidance":    guidance,
				}),
				AccountObjectId: &query.EventId,
			},
			ResolverType:   "code fix",
			Resource:       cr,
			ProviderConfig: query.ProviderConfig,
		}
	} else {
		if raisePR {
			resolution.Type = models.RecommendationResolutionTypePullRequest
		}
		containerName := ""
		if queryData["container_name"] != nil {
			containerName = queryData["container_name"].(string)
		}

		if containerName == "" {
			ctx.GetLogger().Error("error applying recommendation", "error", "container name is required")
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("container_name not found")
		}

		containerValues := []any{}
		// Guard every assertion: cr.Meta is empty when the event's workload isn't
		// linked/synced yet, and reading it as map[string]any (or the nested
		// config/containers) with an unchecked assertion panics -> gin.Recovery
		// returns a bodyless 500 (surfaced as "Failed to apply recommendation: """).
		// Turn that into a clear, actionable 400 instead.
		resourceMeta, metaOk := cr.Meta.Object().(map[string]any)
		resourceConfigs, _ := resourceMeta["config"].(map[string]any)
		resourceContainers, _ := resourceConfigs["containers"].([]any)
		if !metaOk || len(resourceContainers) == 0 {
			ctx.GetLogger().Error("error applying recommendation", "error", "workload resource details unavailable", "container_name", containerName, "resource_id", cr.Id)
			return EventRecommendationApplyResponse{}, common.ErrorBadRequest("resource details for this workload are not available yet; trigger a sync for the account and retry")
		}
		for _, containerAny := range resourceContainers {
			container, ok := containerAny.(map[string]any)
			if !ok {
				continue
			}
			if container["name"] == containerName {
				containerResources, _ := container["resources"].(map[string]any)
				containerLimits := map[string]any{}
				containerRequests := map[string]any{}
				if lim, ok := containerResources["limits"].(map[string]any); ok {
					containerLimits = lim
				}
				if req, ok := containerResources["requests"].(map[string]any); ok {
					containerRequests = req
				}
				cpuResource := map[string]any{
					"resource":  "cpu",
					"allocated": map[string]any{},
				}
				memResource := map[string]any{
					"resource":  "memory",
					"allocated": map[string]any{},
				}
				if containerLimits["cpu"] != nil {
					cpuResource["allocated"].(map[string]any)["limits"] = containerLimits["cpu"]
				}
				if containerLimits["memory"] != nil {
					memResource["allocated"].(map[string]any)["limits"] = containerLimits["memory"]
				}
				if containerRequests["cpu"] != nil {
					cpuResource["allocated"].(map[string]any)["requests"] = containerRequests["cpu"]
				}
				if containerRequests["memory"] != nil {
					memResource["allocated"].(map[string]any)["requests"] = containerRequests["memory"]
				}
				containerValues = append(containerValues, cpuResource, memResource)
			}
		}

		if query.ProviderConfig == nil {
			query.ProviderConfig = map[string]any{}
		}
		query.ProviderConfig["recommendation_source"] = "event"

		recommendationRequest = adapter.ApplyRecommendationRequest{
			Data: query.Data.(map[string]any),
			Recommendation: models.Recommendation{
				Category:       "RightSizing",
				RuleName:       "pod_right_sizing",
				Id:             r.Id,
				CloudAccountId: *r.CloudAccountId,
				TenantId:       *r.Tenant,
				Recommendation: models.NewJsonObject(map[string]any{
					containerName: containerValues,
				}),
			},
			// The raise-PR path (github/gitlab adapter) dispatches the code agent
			// asynchronously and writes its outcome back via this flag's table
			// choice (event_resolution vs recommendation_resolution). This
			// function only ever inserts into event_resolution (below), so it
			// must stay true or a PR raised from here never surfaces here.
			IsEventResolution: true,
			Resource:          cr,
			ProviderConfig:    query.ProviderConfig,
		}

		for _, eventResolution := range existingRecommendations {
			convertedRecommendationResolution = append(convertedRecommendationResolution, models.RecommendationResolution{
				Id:               eventResolution.Id,
				CreatedAt:        eventResolution.CreatedAt,
				UpdatedAt:        eventResolution.UpdatedAt,
				RecommendationId: eventResolution.EventId,
				Type:             eventResolution.Type,
				Data:             eventResolution.Data,
				Status:           eventResolution.Status,
				TypeReferenceId:  eventResolution.TypeReferenceId,
				ResolverType:     eventResolution.ResolverType,
				ResolverId:       eventResolution.ResolverId,
				StatusMessage:    eventResolution.StatusMessage,
			})

		}
	}

	if !found {
		_, err = dbms.Db.Exec(`INSERT INTO event_resolution (id, created_at, updated_at, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			resolution.Id, resolutionCreatedAt.Format(time.RFC3339), resolutionCreatedAt.Format(time.RFC3339), resolution.EventId, resolution.Type, resolution.Data, resolution.Status, resolution.TypeReferenceId, resolution.ResolverType, resolution.ResolverId, resolution.StatusMessage)
		if err != nil {
			ctx.GetLogger().Error("error inserting recommendation resolution", "error", err, "event_id", resolution.EventId, "resolution_id", resolution.Id)
			return EventRecommendationApplyResponse{}, common.ErrorInternal(fmt.Sprintf("error inserting recommendation resolution: %s", err.Error()))
		}
	}

	resp, err := adptr.ApplyRecommendation(ctx, recommendationRequest, convertedRecommendationResolution, recommendationResolutionId)

	// Applying a resolution mutates customer cluster state through the agent, so it is audited the
	// same way the remediation panel audits its commands. This path wrote a resolution row but no
	// audit at all, so a revert left no compliance-grade record of who ran it or whether it took.
	auditEventResolutionApply(ctx, r, recommendationRequest, err)

	if err != nil {
		ctx.GetLogger().Error("error applying recommendation", "error", err)
		if _, updateErr := dbms.Db.Exec("UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1", resolution.Id, models.RecommendationResolutionStatusFailed, time.Now().UTC().Format(time.RFC3339), err.Error()); updateErr != nil {
			ctx.GetLogger().Error("error updating status of event resolution", "error", updateErr)
		}
		return EventRecommendationApplyResponse{}, err
	}

	ctx.GetLogger().Info("Recommendation applied", "response", slog.AnyValue(resp.Data))

	recommendationStatus := models.RecommendationStatusInProgress
	switch resp.Status {
	case adapter.RecommendationResolutionStatusSuccess:
		recommendationStatus = models.RecommendationStatusClosed
	case adapter.RecommendationResolutionStatusFailed:
		// Not Dismissed: that is the user's decision not to act, and reporting it
		// for a failure the platform hit says the opposite of what happened.
		recommendationStatus = models.RecommendationStatusOpen
	case adapter.RecommendationResolutionStatusInProgress:
		recommendationStatus = models.RecommendationStatusInProgress
	}

	// The event's status is left alone: see recordResolutionStatus.
	_, err = dbms.Db.Exec("UPDATE event_resolution SET type_reference_id = $2, updated_at = $3 WHERE id = $1", resolution.Id, resp.ResolutionTypeRefrenceId, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		ctx.GetLogger().Error("error updating type reference event resolution", "error", err)
	}

	// trigger notification
	err = notification.TriggerNotificationForEventResolution(ctx, r, resolution)
	if err != nil {
		ctx.GetLogger().Error("error triggering notification", "error", err)
	}
	return EventRecommendationApplyResponse{
		Data:       []any{resp.Data},
		Resolution: resolution,
		Status:     models.RecommendationStatus(string(recommendationStatus)),
	}, nil

}

// restartActionFor decides how "Restart" is carried out for an event.
//
// It dispatched delete_pod unconditionally. Deleting one pod of a Deployment restarts that replica
// and leaves the others on the old state, which is not what an operator means by restarting a
// workload — and on a controller with several replicas it does not clear the condition. The agent
// exposes rollout_restart for Deployment/StatefulSet/DaemonSet/Rollout, which cycles every replica
// through the controller the way `kubectl rollout restart` does.
//
// A bare Pod has no controller to roll, so it keeps delete_pod: recreation is the only restart
// available, and for a standalone pod that is the whole workload anyway.
func restartActionFor(r models.Event) (string, map[string]any) {
	ownerKind := ""
	if r.SubjectOwnerKind != nil {
		ownerKind = *r.SubjectOwnerKind
	}
	owner := ""
	if r.SubjectOwner != nil {
		owner = *r.SubjectOwner
	}
	// Params carry plain strings. The pointers these come from marshal to the same JSON, but a
	// map holding *string forces every reader — the audit record among them — to know which
	// fields are pointers, and one that guessed wrong silently dropped the name.
	namespace := stringValue(r.SubjectNamespace)
	if owner != "" {
		if _, ok := rolloutRestartableKinds[strings.ToLower(ownerKind)]; ok {
			return "rollout_restart", map[string]any{
				"kind":      ownerKind,
				"name":      owner,
				"namespace": namespace,
			}
		}
	}
	return "delete_pod", map[string]any{
		"name":      stringValue(r.SubjectName),
		"namespace": namespace,
		"previous":  false,
	}
}

// Kinds the agent's rollout_restart handler supports. Compared lowercased because subject_owner_kind
// is not written consistently — cloud_resourses alone carries both "Pod" and "pod".
var rolloutRestartableKinds = map[string]struct{}{
	"deployment":  {},
	"statefulset": {},
	"daemonset":   {},
	"rollout":     {},
}

// stringValue reads a param that may be either a string or a *string.
func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case *string:
		if value != nil {
			return *value
		}
	}
	return ""
}

// buildUndoRequest reconstructs the action that puts back what a previous attempt replaced.
// The attempt is looked up on the event being acted on, so an id from another event — or another
// tenant — is simply not found rather than replayed here.
//
// It re-dispatches the SAME action the attempt used, with the recorded before-state substituted for
// the values that attempt wrote. That keeps undo uniform: an action becomes undoable by recording a
// before-state, never by adding a bespoke undo branch that can drift from the apply it reverses.
//
// The coordinates (name, namespace, kind) are taken from the original task's params rather than
// recomputed from the event, so an undo targets exactly what was changed even if the event's view of
// the workload has since moved on.
func buildUndoRequest(ctx *security.RequestContext, eventID, resolutionID string) (map[string]any, string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, "", err
	}
	var dataJSON, taskID string
	if err := dbms.Db.QueryRow(
		`SELECT COALESCE(data::text, '{}'), COALESCE(type_reference_id, '') FROM event_resolution WHERE id = $1 AND event_id = $2`,
		resolutionID, eventID).Scan(&dataJSON, &taskID); err != nil {
		// Only a genuinely missing row is the caller's fault. A dropped connection or a timeout is
		// ours, and reporting it as 400 would tell the operator their undo was invalid when the
		// database was simply unreachable.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", common.ErrorBadRequest("undo: the attempt to undo could not be found")
		}
		return nil, "", err
	}
	var data map[string]any
	if err := common.UnmarshalJson([]byte(dataJSON), &data); err != nil {
		return nil, "", common.ErrorBadRequest("undo: the attempt's record could not be read")
	}
	before, _ := data["before"].(map[string]any)
	if len(before) == 0 {
		// Either the action predates before-state capture, or it is one that cannot be undone —
		// a restart has already happened, a PVC cannot shrink. Say so rather than dispatching
		// something that would not restore anything.
		return nil, "", common.ErrorBadRequest("undo: this action did not record what it replaced, so it cannot be undone")
	}
	if taskID == "" {
		return nil, "", common.ErrorBadRequest("undo: the attempt has no task to reconstruct from")
	}

	var action, payloadJSON string
	if err := dbms.Db.QueryRow(
		`SELECT action, COALESCE(payload::text, '{}') FROM agent_task WHERE id = $1`, taskID).Scan(&action, &payloadJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", common.ErrorBadRequest("undo: the original task could not be found")
		}
		return nil, "", err
	}
	var payload map[string]any
	if err := common.UnmarshalJson([]byte(payloadJSON), &payload); err != nil {
		return nil, "", common.ErrorBadRequest("undo: the original task's payload could not be read")
	}
	originalParams, _ := payload["action_params"].(map[string]any)

	params := map[string]any{}
	for _, key := range []string{"name", "namespace", "kind"} {
		if v, ok := originalParams[key]; ok {
			params[key] = v
		}
	}
	// Each recorded shape maps to the param the apply path reads. Only shapes we can actually
	// re-apply are recorded, so an unknown one means the record is from a newer writer than this
	// code — refuse rather than dispatch a half-formed action.
	switch {
	case before["containers"] != nil:
		params["containers"] = before["containers"]
	case before["replicas"] != nil:
		// replica_rightsizing reads replica_count, and accepts it as a number or a numeric string.
		params["replica_count"] = before["replicas"]
	default:
		return nil, "", common.ErrorBadRequest("undo: the recorded before-state is not one this version can re-apply")
	}
	return params, action, nil
}

// storeUndoStateFromTask copies the pre-change state the agent reported into the resolution, under
// data.before. Undo then means "apply data.before" for every action that has one, the same way a
// revert's undo means "apply the other side of the diff".
//
// Kept on the resolution rather than in a side table: it describes one attempt, and event_resolution
// .data is jsonb, so this is additive with no migration.
//
// Best-effort throughout. The action has already been applied successfully; failing to record how to
// undo it must not turn a successful apply into a failed one.
func storeUndoStateFromTask(ctx *security.RequestContext, dbms *database.DatabaseManager, resolution models.EventResolution) {
	if resolution.TypeReferenceId == "" {
		return
	}
	var response *string
	if err := dbms.Db.QueryRow("SELECT response::text FROM agent_task WHERE id = $1", resolution.TypeReferenceId).Scan(&response); err != nil {
		ctx.GetLogger().Warn("undo state: could not read agent task response", "error", err, "resolution_id", resolution.Id)
		return
	}
	if response == nil || *response == "" {
		return
	}
	var parsed map[string]any
	if err := common.UnmarshalJson([]byte(*response), &parsed); err != nil {
		return
	}
	// Only the shapes an undo can actually re-apply. An action whose agent reports nothing reusable
	// records no before-state, and the UI offers no Undo for it — which is the honest outcome.
	// An explicit null in the response is not a before-state. Storing it would leave data.before
	// non-empty, which is what the UI tests to decide whether to offer Undo — so the button would
	// appear and then fail, because a nil value re-applies nothing.
	before := map[string]any{}
	if containers, ok := parsed["previous_containers"]; ok && containers != nil {
		before["containers"] = containers
	}
	if replicas, ok := parsed["previous_replicas"]; ok && replicas != nil {
		before["replicas"] = replicas
	}
	if len(before) == 0 {
		return
	}
	beforeJSON, err := common.MarshalJson(before)
	if err != nil {
		return
	}
	if _, err := dbms.Db.Exec(
		`UPDATE event_resolution
		    SET data = jsonb_set(COALESCE(data, '{}'::jsonb), '{before}', $2::jsonb, true)
		  WHERE id = $1`, resolution.Id, string(beforeJSON)); err != nil {
		ctx.GetLogger().Warn("undo state: could not record before-state", "error", err, "resolution_id", resolution.Id)
	}
}

// auditEventResolutionApply records who applied a resolution to an event, and whether the dispatch
// succeeded. Best-effort: an audit failure must never fail the apply that already happened.
func auditEventResolutionApply(ctx *security.RequestContext, r models.Event, request adapter.ApplyRecommendationRequest, applyErr error) {
	actionName := ""
	params := map[string]any{}
	if obj, ok := request.Recommendation.Recommendation.Object().(map[string]any); ok {
		actionName, _ = obj["action_name"].(string)
		if p, ok := obj["action_params"].(map[string]any); ok {
			// Only the coordinates — the full params carry an entire manifest on the revert path.
			for _, k := range []string{"kind", "name", "namespace"} {
				if v, ok := p[k]; ok {
					params[k] = v
				}
			}
		}
	}
	status := audit.EventStatusSuccess
	if applyErr != nil {
		status = audit.EventStatusFailure
		params["error"] = applyErr.Error()
	}
	sc := ctx.GetSecurityContext()
	if sc == nil {
		return
	}
	accountId := ""
	if r.CloudAccountId != nil {
		accountId = *r.CloudAccountId
	}
	// The action params carry the name as a string on some paths and a *string on others
	// (delete_pod passes r.SubjectName straight through), so handle both rather than silently
	// dropping the target from the audit record.
	target := actionName
	if name := stringValue(params["name"]); name != "" {
		target = fmt.Sprintf("%s/%s", actionName, name)
	}
	auditEvent := audit.Audit{
		UserId:        sc.GetUserId(),
		TenantId:      sc.GetTenantId(),
		AccountId:     accountId,
		EventTime:     time.Now().UTC(),
		EventCategory: audit.EventCategoryRecommendation,
		EventType:     audit.EventTypeRecommendationApply,
		EventTarget:   target,
		EventState:    map[string]any{"event_id": r.Id, "action_name": actionName},
		EventActor:    audit.EventActorUiService,
		EventAction:   audit.EventActionExecute,
		EventStatus:   status,
		EventAttr:     params,
	}
	if err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}}); err != nil {
		ctx.GetLogger().Error("failed to create audit event for event resolution apply", "error", err)
	}
}

// resolveRevertSource picks the event whose recorded diff the revert acts on.
//
// An empty changeEventId keeps the historical behaviour: the alert carries one attached change
// and that is what gets undone. When the operator picks a change from the history list instead,
// the diff must come from THAT event — the alert's own attached change is a different change,
// and reverting it while the UI showed another is the failure this whole path exists to stop.
//
// The selected event is checked against the alert rather than trusted: same tenant, same account,
// same resource, and actually a configuration change. An id from a stale page or a hand-made
// request must not be able to reach into another tenant's history or revert an unrelated workload.
func resolveRevertSource(ctx *security.RequestContext, alert models.Event, changeEventId string) (models.Event, error) {
	if strings.TrimSpace(changeEventId) == "" {
		return alert, nil
	}
	if changeEventId == alert.Id {
		return alert, nil
	}
	change, err := GetEvent(ctx, changeEventId)
	if err != nil {
		ctx.GetLogger().Error("error getting selected change event", "error", err, "change_event_id", changeEventId)
		return models.Event{}, err
	}
	if change.Id == "" {
		return models.Event{}, common.ErrorBadRequest("event resolution: the selected change no longer exists")
	}
	if change.FindingType == nil || *change.FindingType != "configuration_change" {
		return models.Event{}, common.ErrorBadRequest("event resolution: the selected event is not a configuration change")
	}
	if !sameStringValue(change.Tenant, alert.Tenant) || !sameStringValue(change.CloudAccountId, alert.CloudAccountId) {
		return models.Event{}, common.ErrorUnauthorized("event resolution: the selected change belongs to another account")
	}
	if !sameChangeTarget(change, alert) {
		return models.Event{}, common.ErrorBadRequest("event resolution: the selected change is for a different resource than this event")
	}
	return change, nil
}

// sameChangeTarget compares what two events are about. cloud_resource_id is the reliable
// identifier and is preferred; service_key is the fallback for an event recorded before its
// resource synced, which is the same fallback the resource lookup above uses.
func sameChangeTarget(change, alert models.Event) bool {
	if change.CloudResourceId != nil && alert.CloudResourceId != nil {
		return *change.CloudResourceId == *alert.CloudResourceId
	}
	if change.ServiceKey != nil && alert.ServiceKey != nil && *change.ServiceKey != "" {
		return *change.ServiceKey == *alert.ServiceKey
	}
	return false
}

func sameStringValue(a, b *string) bool {
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// assertNoLaterChangeOnSamePaths refuses a revert that would silently undo a more recent change.
//
// The agent applies a revert by writing each recorded `old` value at its path on the LIVE object.
// That is safe for the newest change and only for the newest change: if the image went v1 -> v2 in
// the change being reverted and v2 -> v3 in a later one, writing v1 discards v3 with nothing said.
// Reverting the newest change was accidentally safe from this; letting the operator choose an
// older one removes that accident, so the check arrives with the choice.
//
// A conflict blocks rather than warns. The operator cannot see the later change from the revert
// dialog, so a warning asks them to accept a risk they have no way to evaluate.
func assertNoLaterChangeOnSamePaths(ctx *security.RequestContext, change models.Event, request map[string]any) error {
	revertPaths, _ := request["revert_paths"].([]any)
	// The conflict query filters on these, and Postgres compares NULL with `=` as UNKNOWN — a nil
	// here returns no rows, which reads as "no later change" and waves the revert through. That is
	// the one direction this guard must never fail in, so refuse instead. `tenant` is NOT NULL in
	// the schema and cannot be nil for a persisted event; `cloud_account_id` is nullable and can.
	if change.Tenant == nil || change.CloudAccountId == nil {
		return common.ErrorBadRequest("event resolution: the selected change is missing its tenant or account, so later changes cannot be checked; not reverting")
	}
	if change.StartsAt == nil {
		// Without a time for the change there is no "later" to compare against, and a revert that
		// cannot be checked must not proceed on a mutation path.
		return common.ErrorBadRequest("event resolution: the selected change has no recorded time, so a later change cannot be ruled out; not reverting")
	}
	if len(revertPaths) == 0 {
		// No paths means this request carries the pre-change manifest instead, which the Python
		// agent applies by REPLACING the whole object. Every field a later change touched is
		// discarded by that, not just an overlapping one — so the guard has to be stricter here,
		// not absent. Returning nil (as this did) left the entire un-migrated-tenant cohort with
		// no protection at all, on the more destructive of the two paths.
		if !hasManifestPayload(request) {
			return nil
		}
		later, err := laterChangesOnResource(ctx, change)
		if err != nil {
			ctx.GetLogger().Error("event resolution: could not check for later changes", "error", err, "event_id", change.Id)
			return common.ErrorBadRequest("event resolution: cannot verify whether a later change exists; not reverting")
		}
		if len(later) > 0 {
			when := ""
			if later[0].StartsAt != nil {
				when = later[0].StartsAt.UTC().Format(time.RFC3339)
			}
			return common.ErrorBadRequest(fmt.Sprintf(
				"event resolution: this change has no field-level diff, so reverting it replaces the whole object and would discard the later change at %s. Revert the later change first.", when))
		}
		return nil
	}
	targeted := make(map[string]bool, len(revertPaths))
	for _, raw := range revertPaths {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if path, _ := entry["path"].(string); path != "" {
			targeted[path] = true
		}
	}
	if len(targeted) == 0 {
		return nil
	}

	later, err := laterChangesOnResource(ctx, change)
	if err != nil {
		// Fail closed. Reverting without knowing what came after is the mutation this guards, so
		// a lookup failure must not become a silent green light.
		ctx.GetLogger().Error("event resolution: could not check for later changes", "error", err, "event_id", change.Id)
		return common.ErrorBadRequest("event resolution: cannot verify whether a later change touched the same fields; not reverting")
	}
	if reason := laterChangeConflict(change, targeted, later); reason != "" {
		return common.ErrorBadRequest("event resolution: " + reason)
	}
	return nil
}

// listIndex matches one list position in a recorded path: "env[2]" in
// "spec.template.spec.containers[0].env[2].value". listItem matches a path naming a whole list item.
var (
	listIndex = regexp.MustCompile(`\[(\d+)\]`)
	listItem  = regexp.MustCompile(`^(.*)\[(\d+)\]$`)
)

// laterChangeConflict reports why reverting `change` at the targeted paths would be unsafe given
// the changes recorded after it, or "" when it is safe.
//
// Recorded paths address list items by POSITION ("containers[1].env[2]"), and the agent writes a
// revert at that position. A position only means the same item while nothing was added to or
// removed from the list ahead of it, so three kinds of later change conflict:
//   - the same path moved to a different value (reverting would undo that later change);
//   - a parent or child of the path changed — a whole env entry replaced, or one field inside an
//     entry the revert rewrites whole;
//   - an item was added or removed at or before the position in the same list, so the position
//     now points at a different item and the revert would overwrite the wrong entry.
//
// A later record carrying the value this change itself left is the same change recorded twice
// (dev has these in quantity) and is not a conflict.
func laterChangeConflict(change models.Event, targeted map[string]bool, later []models.Event) string {
	// What the change being reverted left behind at each path. Reverting writes this change's
	// `old` back, which is safe exactly while the live value is still this change's `new`.
	appliedValues := changedValuesOf(change)
	for _, l := range later {
		when := ""
		if l.StartsAt != nil {
			when = l.StartsAt.UTC().Format(time.RFC3339)
		}
		laterNew := changedValuesOf(l)
		laterOld := changedOldValuesOf(l)
		for laterPath, laterValue := range laterNew {
			if applied, ok := appliedValues[laterPath]; ok && sameChangedValue(applied, laterValue) {
				continue
			}
			for path := range targeted {
				if laterPath == path || isPathAncestor(laterPath, path) || isPathAncestor(path, laterPath) {
					return fmt.Sprintf(
						"%q was changed again by a later change (%s) — reverting this one would undo that too. Revert the later change first.",
						path, when)
				}
				// An add or remove shows as one side of the item absent.
				if laterValue != nil && laterOld[laterPath] != nil {
					continue
				}
				if shiftsListPosition(laterPath, path) {
					return fmt.Sprintf(
						"a later change (%s) added or removed an item in the list %q belongs to, so that position now holds a different item — reverting would write to the wrong entry.",
						when, path)
				}
			}
		}
	}
	return ""
}

// isPathAncestor reports whether `ancestor` names a field or list item that contains `path`.
func isPathAncestor(ancestor, path string) bool {
	return strings.HasPrefix(path, ancestor+".") || strings.HasPrefix(path, ancestor+"[")
}

// shiftsListPosition reports whether adding or removing the list item at `itemPath` ("…env[1]")
// moves an item `path` addresses by position ("…env[2].value"): same list, at or before it.
func shiftsListPosition(itemPath, path string) bool {
	item := listItem.FindStringSubmatch(itemPath)
	if item == nil {
		return false
	}
	list := item[1]
	changedAt, _ := strconv.Atoi(item[2])
	for _, loc := range listIndex.FindAllStringSubmatchIndex(path, -1) {
		if path[:loc[0]] != list {
			continue
		}
		position, _ := strconv.Atoi(path[loc[2]:loc[3]])
		return changedAt <= position
	}
	return false
}

// changedOldValuesOf maps each path a change touched to the value it replaced — the other side of
// changedValuesOf.
func changedOldValuesOf(e models.Event) map[string]any {
	values := map[string]any{}
	for _, entry := range extractRevertPaths(e, false) {
		change, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if path, _ := change["path"].(string); path != "" {
			values[path] = change["old"]
		}
	}
	return values
}

// hasManifestPayload reports whether the request carries a pre-change manifest under its per-kind
// key ("deployment", "statefulset", ...). Those keys are whatever the resource's kind lowercases
// to, so this asks the question the other way round: is there anything here beyond the fixed
// fields the builder always sets.
func hasManifestPayload(request map[string]any) bool {
	for key := range request {
		switch key {
		case "name", "namespace", "kind", "revert_paths":
			continue
		default:
			return true
		}
	}
	return false
}

// laterChangesOnResource returns the configuration changes recorded on the same resource after
// the given change. Bounded: a revert only needs to know whether ANY later change collides, and
// the check runs on the click path.
func laterChangesOnResource(ctx *security.RequestContext, change models.Event) ([]models.Event, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	conditions := []string{
		"finding_type = 'configuration_change'",
		"tenant = $1",
		"cloud_account_id = $2",
		// >= , not > . Two config changes recorded at the same instant is not a corner case:
		// it is the reported one — a command edit and a replica edit that both land at the
		// alert's offset 0. A strict > would let the sibling through unchecked, which is
		// precisely the pair most likely to touch the same workload. id != $4 keeps the
		// change being reverted out of its own conflict set.
		"starts_at >= $3",
		"id != $4",
	}
	args := []any{change.Tenant, change.CloudAccountId, change.StartsAt, change.Id}
	if change.CloudResourceId != nil {
		conditions = append(conditions, "cloud_resource_id = $5")
		args = append(args, *change.CloudResourceId)
	} else if change.ServiceKey != nil && *change.ServiceKey != "" {
		// Non-empty, matching sameChangeTarget. An empty service_key identifies nothing, so
		// querying for it would match every event that also has none and report their changes as
		// conflicts — refusing reverts for an unrelated reason.
		conditions = append(conditions, "service_key = $5")
		args = append(args, *change.ServiceKey)
	} else {
		return nil, fmt.Errorf("event resolution: change %s identifies no resource", change.Id)
	}

	query := "SELECT id, starts_at, evidences FROM events WHERE " + strings.Join(conditions, " AND ") +
		" ORDER BY starts_at ASC LIMIT 50"
	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			ctx.GetLogger().Error("error closing rows", "error", closeErr)
		}
	}()

	var later []models.Event
	for rows.Next() {
		var e models.Event
		if scanErr := rows.Scan(&e.Id, &e.StartsAt, &e.Evidences); scanErr != nil {
			return nil, scanErr
		}
		later = append(later, e)
	}
	return later, rows.Err()
}

// changedValuesOf maps each path a config-change event touched to the value it left there. Read
// from the same diff evidence the revert itself is built from, so the two cannot disagree about
// what a change covers. `undo` direction is used because it reports the change's `new` side, which
// is what the live object should still hold if nothing has touched the field since.
func changedValuesOf(e models.Event) map[string]any {
	values := map[string]any{}
	for _, entry := range extractRevertPaths(e, true) {
		change, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if path, _ := change["path"].(string); path != "" {
			values[path] = change["old"]
		}
	}
	return values
}

// sameChangedValue compares two recorded diff values. They arrive as decoded JSON — strings,
// numbers, maps, nil — so compare their canonical encodings rather than trying to type-switch
// every shape a manifest field can take.
func sameChangedValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	aJSON, errA := common.MarshalJson(a)
	bJSON, errB := common.MarshalJson(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}

// getRevertRecommendationRequest builds the params that undo the configuration
// change recorded on the event.
//
// It adds the changed field paths (`revert_paths`), which is what the Go agent
// acts on. Sending only the evidence's pre-change manifest — all this used to do
// — never worked there: the manifest is serialized snake_case for Hikaru wire
// compatibility, so the agent's dynamic client fed it straight to the apiserver,
// which dropped every renamed key and rejected the remains ("spec.selector:
// Invalid value: {}: empty selector is invalid for deployment"). Replaying a
// stored snapshot is also wrong independently of the casing: it clobbers every
// unrelated change made since the event and trips immutable-field validation.
// Given the paths, the agent applies them to the live object instead.
//
// The manifest still goes out under its per-kind field because tenants not yet
// migrated off the Python agent are served by that agent's replace_workload,
// which consumes exactly this shape via Hikaru and ignores unknown params. The
// Go agent prefers revert_paths whenever they are present. Drop the manifest
// once the Python agent is retired.
func getRevertRecommendationRequest(cr models.Resource, r models.Event, undo bool) (map[string]any, error) {
	if cr.ResourceId == nil {
		return map[string]any{}, common.ErrorBadRequest("event resolution: cannot revert, event is not linked to a cloud resource")
	}
	resourceKeys := strings.Split(*cr.ResourceId, "/")
	if len(resourceKeys) != 3 {
		return map[string]any{}, fmt.Errorf("resolution: service key is not correct")
	}
	// An alert can carry one diff block per correlated change (its workload's deploy, a ConfigMap
	// it reads, an Ingress in front of it). Merging their paths into one request writes every
	// change's old values — a ConfigMap's `data.*` included — onto the workload. The caller must
	// name the change it means via change_event_id, whose own event holds exactly one diff.
	if countDiffEvidences(r) > 1 {
		return map[string]any{}, common.ErrorBadRequest("event resolution: this event carries several recorded changes; select the change to revert")
	}
	kind := resourceKeys[1]
	// A change to another object the workload uses — a ConfigMap it reads — is filed under the
	// workload's resource, so it reaches here looking like the workload's own change and passes the
	// same-resource check. Its paths are that object's (`data.*`); writing them onto the workload is
	// never what anyone asked for.
	if other := changedObjectKind(r); other != "" {
		return map[string]any{}, common.ErrorBadRequest(fmt.Sprintf(
			"event resolution: this change is to a %s, not to the %s itself, so it cannot be reverted onto the workload", other, kind))
	}
	request := map[string]any{
		"name":      resourceKeys[2],
		"namespace": r.SubjectNamespace,
		"kind":      kind,
	}
	revertPaths := extractRevertPaths(r, undo)
	if len(revertPaths) > 0 {
		request["revert_paths"] = revertPaths
	}
	if oldYaml := extractDiffOldManifest(r); oldYaml != "" {
		var yamlMap map[string]any
		if err := yaml.Unmarshal([]byte(oldYaml), &yamlMap); err != nil {
			return map[string]any{}, common.ErrorBadRequest("event resolution: the recorded pre-change manifest is not valid YAML")
		}
		request[strings.ToLower(kind)] = yamlMap
	}
	if len(revertPaths) == 0 && request[strings.ToLower(kind)] == nil {
		return map[string]any{}, common.ErrorBadRequest("event resolution: cannot revert, this event has no recorded configuration change to undo")
	}
	return request, nil
}

// revertableWorkloadKinds are the kinds a revert can write to, as recorded in a diff's resource_name
// ("deployment/<namespace>/<name>.yaml").
var revertableWorkloadKinds = map[string]bool{"deployment": true, "statefulset": true, "daemonset": true, "rollout": true, "replicaset": true}

// changedObjectKind names the object an event's recorded change was made to when that is not a
// workload, or returns "". Known from the diff's resource_name, or — for a record without one — from
// the paths: a change touching only `data`, `binaryData` or `stringData` is a ConfigMap or Secret.
func changedObjectKind(r models.Event) string {
	if r.Evidences == nil || !r.Evidences.IsArray() {
		return ""
	}
	for _, item := range r.Evidences.Array() {
		evidence, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if evidenceType, _ := evidence["type"].(string); evidenceType != "diff" {
			continue
		}
		dataMap, _ := evidence["data"].(map[string]any)
		if resourceName, _ := dataMap["resource_name"].(string); resourceName != "" {
			kind := strings.ToLower(strings.SplitN(resourceName, "/", 2)[0])
			if !revertableWorkloadKinds[kind] {
				return kind
			}
			return ""
		}
	}
	paths := extractRevertPaths(r, false)
	if len(paths) == 0 {
		return ""
	}
	for _, entry := range paths {
		change, _ := entry.(map[string]any)
		path, _ := change["path"].(string)
		if !strings.HasPrefix(path, "data.") && !strings.HasPrefix(path, "binaryData.") && !strings.HasPrefix(path, "stringData.") {
			return ""
		}
	}
	return "ConfigMap or Secret"
}

func countDiffEvidences(r models.Event) int {
	if r.Evidences == nil || !r.Evidences.IsArray() {
		return 0
	}
	n := 0
	for _, item := range r.Evidences.Array() {
		if evidence, ok := item.(map[string]any); ok {
			if evidenceType, _ := evidence["type"].(string); evidenceType == "diff" {
				n++
			}
		}
	}
	return n
}

// extractDiffOldManifest returns the pre-change manifest recorded on the diff
// evidence. Only the Python agent can consume it; see
// getRevertRecommendationRequest.
func extractDiffOldManifest(r models.Event) string {
	if r.Evidences == nil || !r.Evidences.IsArray() {
		return ""
	}
	for _, item := range r.Evidences.Array() {
		evidence, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if evidenceType, _ := evidence["type"].(string); evidenceType != "diff" {
			continue
		}
		dataMap, ok := evidence["data"].(map[string]any)
		if !ok {
			continue
		}
		if oldYaml, ok := dataMap["old"].(string); ok && oldYaml != "" {
			return oldYaml
		}
	}
	return ""
}

// extractRevertPaths pulls {path, old} pairs out of the event's diff evidence.
// `old` is passed through untouched, including a null — the agent reads a null
// (or absent) old value as "the change added this field", which reverts to
// removing it rather than writing a null.
// undo reverses the revert: it re-applies the values the change introduced. Undoing a revert is a
// revert with old and new swapped, so one builder serves both directions and there is no separate
// undo path that could drift from the one that is exercised daily.
func extractRevertPaths(r models.Event, undo bool) []any {
	if r.Evidences == nil || !r.Evidences.IsArray() {
		return nil
	}
	var paths []any
	for _, item := range r.Evidences.Array() {
		evidence, ok := item.(map[string]any)
		if !ok {
			slog.Warn("Unknown type for evidence item", "type", fmt.Sprintf("%T", item))
			continue
		}
		if evidenceType, _ := evidence["type"].(string); evidenceType != "diff" {
			continue
		}
		dataMap, ok := evidence["data"].(map[string]any)
		if !ok {
			continue
		}
		updatedValues, ok := dataMap["updated_values"].([]any)
		if !ok {
			slog.Warn("diff evidence has no updated_values; cannot build a revert", "event_id", r.Id)
			continue
		}
		for _, uv := range updatedValues {
			change, ok := uv.(map[string]any)
			if !ok {
				continue
			}
			path, _ := change["path"].(string)
			if path == "" {
				continue
			}
			// "old" is always the value to write; which side of the diff that is depends on direction.
			target := change["old"]
			if undo {
				target = change["new"]
			}
			paths = append(paths, map[string]any{"path": path, "old": target})
		}
	}
	return paths
}

// hasInProgressRecommendation looks for an existing InProgress resolution for
// the same card (cardId) so a retry reuses its row instead of creating a
// duplicate. Scoped by card_id — an event can have several cards each with
// their own in-flight fix (e.g. MemoryAllocationCard's rightsizing PR and
// AskAiCard's code-fix PR running concurrently); matching on status alone
// would reuse a different card's row and let this card's dispatch overwrite
// that card's PR tracking (type_reference_id, status_message) with its own.
func hasInProgressRecommendation(existingRecommendations []models.EventResolution, cardId string) (bool, string) {
	recommendationResolutionId := common.GenerateUUID()
	for _, resolution := range existingRecommendations {
		if resolution.Status != models.RecommendationResolutionStatusInProgress {
			continue
		}
		obj, _ := resolution.Data.Object().(map[string]any)
		input, _ := obj["data"].(map[string]any)
		existingCardId, _ := input["card_id"].(string)
		if existingCardId == cardId {
			return true, resolution.Id
		}
	}
	return false, recommendationResolutionId
}

func UpdateResolutionStatus(ctx *security.RequestContext) error {
	t0 := time.Now()
	defer func() {
		ctx.GetLogger().Info("UpdateResolutionStatus", "time", time.Since(t0))
	}()
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("error getting database manager", "error", err)
		return err
	}

	// only check resolutions that are in progress and are user resolutions
	rows, err := dbms.Db.Queryx(`SELECT id, created_at, updated_at, event_id, type, data, status, type_reference_id,
			resolver_type, resolver_id, status_message, pr_iteration_count, pr_lifecycle_state, last_pr_check_at
		FROM event_resolution WHERE status = $1 AND resolver_type = $2`, models.RecommendationResolutionStatusInProgress, models.RecommendationResolutionResolverTypeUser)
	if err != nil {
		ctx.GetLogger().Error("error getting event resolutions", "error", err)
		return err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}

	}()

	eventResolutionsToCheck := make([]models.EventResolution, 0)
	events := make(map[string]models.Event)
	eventIds := make([]any, 0)

	for rows.Next() {
		resolution := models.EventResolution{}
		err = rows.StructScan(&resolution)
		if err != nil {
			ctx.GetLogger().Error("error scanning event resolutions", "error", err)
			return err
		}
		eventResolutionsToCheck = append(eventResolutionsToCheck, resolution)
		eventIds = append(eventIds, resolution.EventId)
	}
	if err := rows.Err(); err != nil {
		ctx.GetLogger().Error("error iterating event resolutions", "error", err)
		return err
	}

	if len(eventResolutionsToCheck) == 0 {
		return nil
	}

	rows1, err := dbms.Query(`SELECT id, created_at, updated_at, finding_id, title, description, source, aggregation_key,
			failure, finding_type, category, priority, subject_type, subject_name, subject_namespace,
			subject_node, service_key, cluster, ends_at, starts_at, fingerprint, evidences, tenant,
			cloud_account_id, cloud_resource_id, status, nb_status, nb_status_changed_at,
			nb_status_changed_by, snoozed_until, principal, subject_owner, subject_owner_kind, labels,
			urgency, computed_score, computed_priority, score_factors, score_confidence
		FROM events WHERE id IN (?)`, eventIds)
	if err != nil {
		ctx.GetLogger().Error("error getting events", "error", err)
		return err
	}
	defer func() {
		err := rows1.Close()
		if err != nil {
			ctx.GetLogger().Error("error closing rows", "error", err)
		}
	}()

	for rows1.Next() {
		event := models.Event{}
		err = rows1.StructScan(&event)
		if err != nil {
			ctx.GetLogger().Error("error scanning events", "error", err)
			return err
		}
		events[event.Id] = event
	}
	// A truncated read here is destructive: the reconcile below permanently marks
	// every resolution whose event is missing from this map as FAILED, so a partial
	// map would fail resolutions whose events actually exist.
	if err := rows1.Err(); err != nil {
		ctx.GetLogger().Error("error iterating events", "error", err)
		return err
	}

	ctx.GetLogger().Info("Checking resolutions", "resolutions", len(eventResolutionsToCheck))

	for _, resolution := range eventResolutionsToCheck {
		event := events[resolution.EventId]
		if event.Id == "" {
			// The event is gone, so this resolution can never be evaluated. Fail it
			// instead of leaving it in progress for the cron to retry forever.
			ctx.GetLogger().Warn("event not found for resolution, marking it failed", "event_id", resolution.EventId, "resolution_id", resolution.Id)
			_, err = dbms.Db.Exec("UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4 WHERE id = $1",
				resolution.Id, models.RecommendationResolutionStatusFailed, time.Now().UTC().Format(time.RFC3339),
				fmt.Sprintf("event not found - %s", resolution.EventId))
			if err != nil {
				ctx.GetLogger().Error("error updating event resolution", "error", err)
				return err
			}
			continue
		}
		if err := settleResolution(ctx, dbms, resolution, event); err != nil {
			return err
		}
	}

	return nil
}

// settleResolution asks the resolution's adapter how the attempt went and records the answer.
func settleResolution(ctx *security.RequestContext, dbms *database.DatabaseManager, resolution models.EventResolution, event models.Event) error {
	adptr := adapter.GetAdapterFromResolutionProvider(resolution.Type)
	if adptr == nil {
		ctx.GetLogger().Error("error getting adapter", "error", fmt.Errorf("adapter not found - %s", resolution.Type))
		return nil
	}

	if event.Tenant == nil || event.CloudAccountId == nil {
		ctx.GetLogger().Error("resolution's event has no tenant or account", "event_id", event.Id, "resolution_id", resolution.Id)
		return nil
	}
	statusMessage := ""
	if resolution.StatusMessage != nil {
		statusMessage = *resolution.StatusMessage
	}
	adapterContext := security.NewRequestContext(ctx.GetContext(), security.NewSecurityContextForTenantAdmin(*event.Tenant), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	resp, err := adptr.GetRecommendationResolutionStatus(adapterContext, models.Recommendation{
		Category:       "RightSizing",
		RuleName:       "pod_right_sizing",
		Id:             event.Id,
		CloudAccountId: *event.CloudAccountId,
		TenantId:       *event.Tenant,
		Recommendation: models.NewJsonObject(map[string]any{}),
	}, resolution.TypeReferenceId, resolution.Data, statusMessage)
	var status models.RecommendationResolutionStatus
	var statusMsg string
	if err != nil {
		ctx.GetLogger().Error("error getting event resolution status", "error", err)
		statusMsg = fmt.Sprintf("error getting event resolution status - %s", err.Error())
		status = models.RecommendationResolutionStatusFailed
	} else {
		status = models.RecommendationResolutionStatus(string(resp.Status))
		statusMsg = resp.StatusMessage
	}
	// A check that could not reach the provider keeps the pull request in
	// progress and leaves last_pr_check_at alone, up to the limit — see
	// adapter.PRStatusUnverifiedLimit.
	lastCheck := lastCheckKeep // an adapter error is not a sighting either
	if err == nil && !resp.Unverified {
		lastCheck = lastCheckNow
	}
	if err == nil && resp.Unverified {
		if adapter.PRStatusCheckExpired(resolution.LastPRCheckAt, time.Now().UTC()) {
			status = models.RecommendationResolutionStatusFailed
			statusMsg = adapter.PRStatusGaveUpMessage(resp.StatusMessage)
		} else {
			lastCheck = lastCheckCoalesce
		}
	}
	return recordResolutionStatus(ctx, dbms, resolution, status, statusMsg, lastCheck)
}

// How settleResolution moves last_pr_check_at: keep it, stamp now, or stamp now only if unset.
const (
	lastCheckKeep     = "keep"
	lastCheckNow      = "now"
	lastCheckCoalesce = "coalesce"
)

// recordResolutionStatus records a remediation attempt's outcome, and only that: applied != incident
// ended, so events.status stays with the alert's source. Guarded on InProgress so a webhook that settled the row first wins.
func recordResolutionStatus(ctx *security.RequestContext, dbms *database.DatabaseManager, resolution models.EventResolution, status models.RecommendationResolutionStatus, statusMsg, lastCheck string) error {
	switch lastCheck {
	case lastCheckKeep, lastCheckNow, lastCheckCoalesce:
	default:
		return fmt.Errorf("resolution: unexpected last_pr_check_at mode %q", lastCheck)
	}
	_, err := dbms.Db.Exec(`UPDATE event_resolution SET status = $2, updated_at = $3, status_message = $4,
		last_pr_check_at = CASE $6 WHEN 'now' THEN (now() AT TIME ZONE 'UTC')
		                           WHEN 'coalesce' THEN COALESCE(last_pr_check_at, (now() AT TIME ZONE 'UTC'))
		                           ELSE last_pr_check_at END
		WHERE id = $1 AND status = $5`, resolution.Id, status, time.Now().UTC().Format(time.RFC3339), statusMsg, models.RecommendationResolutionStatusInProgress, lastCheck)
	if err != nil {
		ctx.GetLogger().Error("error updating event resolution", "error", err)
		return err
	}
	if status == models.RecommendationResolutionStatusSuccess {
		// The agent reports what it replaced; without capturing it here the applied value is
		// recorded and the value it replaced is lost, so the action cannot be undone.
		storeUndoStateFromTask(ctx, dbms, resolution)
	}
	return nil
}

// maxSubjectNameBytes is the maximum byte length for subject_name.
// PostgreSQL B-tree index entries are limited to ~2730 bytes (8KB page / 3).
// We cap at 2048 bytes to leave comfortable headroom.
const maxSubjectNameBytes = 2048

// truncateStringToMaxBytes truncates s so that its UTF-8 byte length does not
// exceed maxBytes, cutting on a valid UTF-8 character boundary.
func truncateStringToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backwards from maxBytes to find the leading byte of the last rune
	// that may have been split by the cut.
	i := maxBytes
	for i > 0 && s[i-1]&0xC0 == 0x80 {
		i--
	}
	if i == 0 {
		return ""
	}
	// s[i-1] is either ASCII or a multi-byte leading byte.
	b := s[i-1]
	if b&0x80 == 0 {
		// ASCII byte — the rune is complete, everything up to maxBytes is valid.
		return s[:maxBytes]
	}
	// Multi-byte leading byte: determine the expected rune length.
	var charLen int
	switch {
	case b&0xE0 == 0xC0:
		charLen = 2
	case b&0xF0 == 0xE0:
		charLen = 3
	case b&0xF8 == 0xF0:
		charLen = 4
	}
	// Check if the full rune fits within maxBytes.
	if (i-1)+charLen <= maxBytes {
		return s[:maxBytes]
	}
	// The rune is split — exclude it entirely.
	return s[:i-1]
}

// insertConfig holds optional InsertEvent behavior toggles.
type insertConfig struct {
	skipWorkflowRefire bool
	publishCtx         context.Context
	deferPostProcess   func(eventId string)
}

// InsertOption configures InsertEvent.
type InsertOption func(*insertConfig)

// WithPublishContext threads the caller's trace context into the post-process
// MQ publishes, so the event queue consumer — and everything it fans out to
// (triage, llm analysis, notifications) — continues the caller's distributed
// trace instead of starting a fresh root. A nil / span-less context is a no-op
// (MqPublish injects headers only when a valid span is present).
func WithPublishContext(ctx context.Context) InsertOption {
	return func(c *insertConfig) { c.publishCtx = ctx }
}

// WithoutWorkflowRefire disables the event-trigger workflow re-fire that
// InsertEvent otherwise enqueues when it UPDATES an existing FIRING event.
// Use it for callers that re-persist an event for a reason OTHER than a fresh
// occurrence — e.g. UpdateEvent's user-facing metadata edits (urgency/subject) —
// where re-running matched event-trigger workflows would be spurious. Genuine
// occurrence/ingestion paths (alert webhooks, anomaly/SLO detection) must NOT
// pass this, so repeat firings keep matching workflows (issue #25251).
func WithoutWorkflowRefire() InsertOption {
	return func(c *insertConfig) { c.skipWorkflowRefire = true }
}

// WithDeferredPostProcess redirects the post-process enqueue that InsertEvent
// performs on a genuine insert. The caller becomes responsible for getting the
// event to event_post_process eventually — used by asynchronous enrichment,
// which must land the playbook's evidence on the row first, because triage reads
// evidence content (getEvidenceBonus, parseServiceMapFromEvent) rather than a
// mere has-evidence flag.
//
// The ON CONFLICT branches are deliberately not redirected: those rows have
// already been through the pipeline once.
func WithDeferredPostProcess(fn func(eventId string)) InsertOption {
	return func(c *insertConfig) { c.deferPostProcess = fn }
}

// legacyEventSources maps event source values that were renamed in the
// event_source lookup table onto their current canonical value.
//
// events.source is a FK onto event_source.value (V175), so an insert carrying a
// retired value fails outright rather than degrading. Producers are not all
// under our rollout control — an un-rolled pod mid-deploy, or an on-prem
// runbook-server on an older tag, keeps emitting the old string long after the
// migration that retired it has run. That is exactly how V706's rename of
// 'workflow' -> 'automation' took event ingestion down (inc-cbb58afb8f): the
// migration Job deleted the lookup row while producers still sent 'workflow'.
//
// Normalizing at ingestion makes the rename survivable in both directions: old
// producers keep working against the new schema, and the retired value never
// has to be kept alive in the lookup table.
var legacyEventSources = map[string]string{
	"workflow": "automation",
}

// normalizeEventSource maps a retired event source onto its canonical value.
// Unknown values pass through untouched — this is a rename shim, not an
// allowlist; the FK remains the authority on what is valid.
func normalizeEventSource(source string) string {
	if canonical, ok := legacyEventSources[source]; ok {
		return canonical
	}
	return source
}

// normalizeSubjectNode drops a value that cannot be a node name.
//
// Alert-sourced events get subject_node from the alert's `instance` label, and
// for anything scraped from kube-state-metrics that is the KSM pod's scrape
// address — "10.64.21.224:8080". A Kubernetes node name is an object name, so
// it is a DNS-1123 subdomain and a colon is not a legal character in one: the
// test is exact, not a heuristic.
//
// Blanking it is lossless — the raw value stays in labels["instance"] — and it
// is what every consumer needs, because an empty subject_node is the documented
// "resolve it yourself" signal that the pod-inventory fallback keys off. Left
// populated, it is worse than absent: it looks authoritative, so consumers use
// it, and every node-scoped query built from it silently matches nothing.
// Measured on the test env: 104 of 124 KubePodCrashLooping events in 36h
// carried a scrape address here, and the noisy-neighbours card rendered for
// none of them.
func normalizeSubjectNode(subjectNode string) string {
	if strings.Contains(subjectNode, ":") {
		return ""
	}
	return subjectNode
}

// isPointInTime reports whether an event describes a moment that has already
// passed rather than a condition that can recover. Pure so the rule can be
// tested without a database; the feature flag is checked by the caller.
//
// Two shapes qualify:
//   - configuration_change, which has no end time of its own, and
//   - any event whose producer sent ends_at at or before starts_at, i.e. an
//     explicitly zero-length (or inverted) interval.
//
// An event with ends_at AFTER starts_at is a condition that ran and stopped.
// Its producer owns that lifecycle and sends its own resolve, so it is not
// point-in-time and is left alone here.
func isPointInTime(findingType string, startsAt, endsAt *time.Time) bool {
	if findingType == "configuration_change" {
		return true
	}
	return startsAt != nil && endsAt != nil && !endsAt.After(*startsAt)
}

func InsertEvent(event Event, id string, opts ...InsertOption) (string, error) {
	var cfg insertConfig
	for _, o := range opts {
		o(&cfg)
	}

	// Validate required UUID fields to prevent DB errors from invalid values
	if event.Tenant == "" {
		return "", fmt.Errorf("event: tenant is required")
	}
	if _, err := uuid.Parse(event.Tenant); err != nil {
		return "", fmt.Errorf("event: tenant is not a valid UUID: %s", event.Tenant)
	}
	if event.AccountId == "" {
		return "", fmt.Errorf("event: account_id is required")
	}
	if _, err := uuid.Parse(event.AccountId); err != nil {
		return "", fmt.Errorf("event: account_id is not a valid UUID: %s", event.AccountId)
	}

	event.Source = normalizeEventSource(event.Source)
	event.SubjectType = strings.ToLower(event.SubjectType)
	event.SubjectName = truncateStringToMaxBytes(event.SubjectName, maxSubjectNameBytes)
	event.SubjectNode = normalizeSubjectNode(event.SubjectNode)

	// A configuration_change event describes a moment that has already passed
	// ("this resource was changed"), not a condition that can recover — no
	// closer will ever reach it (issue #36597), so store it already closed
	// rather than leaving it to accumulate in the open-events list forever.
	// The rest of this codebase already treats configuration_change this way
	// in several places (playbook enrichment is skipped for it, it's excluded
	// from workload-recovery closes, it carries a scoring penalty); this is
	// the one place that decides it at ingestion instead of re-deriving it
	// per call site. Centralized in InsertEvent (not InvestigateEvent) so
	// every insert path gets it automatically.
	//
	// The same reasoning covers any event that ARRIVES ALREADY ENDED -- one whose
	// producer sets ends_at at or before starts_at. A cloud notification like
	// "ECS Task State Change", "GKE Resource Upgrade Started" or "Kubernetes
	// Secret Modified" describes an instant, and the webhook says so by sending a
	// zero-length interval. Storing that FIRING asserts an ongoing condition the
	// producer never claimed, and nothing will ever close it: these sources send
	// no resolve for a moment that has already passed. Measured on an anonymized production
	// estate, 18,357 `ECS Task State Change` events sat FIRING at a median age of
	// 52 days, every one of them carrying ends_at == starts_at, and they were 69%
	// of everything stuck open there.
	//
	// Only ends_at <= starts_at qualifies. A genuine interval (ends_at AFTER
	// starts_at) is a condition that ran and stopped, which the producer's own
	// resolve path owns, so it is left alone.
	isConfigChange := event.FindingType == "configuration_change"
	isPointInTimeEvent := config.Config.FeatureEventPointInTimeCloseEnabled &&
		isPointInTime(event.FindingType, event.StartsAt, event.EndsAt)
	if isPointInTimeEvent {
		event.Status = EventStatusClosed
		if isConfigChange {
			// configuration_change carries no end time of its own; stamp one so the
			// row does not read as an open-ended incident. An already-ended event
			// keeps the producer's timestamps untouched.
			event.EndsAt = event.StartsAt
		}
	}

	query := `INSERT INTO events ("id", "finding_id", "title", "description", "source", "aggregation_key", "priority", "subject_type", "subject_name", "subject_namespace", "subject_node", "service_key", "fingerprint", "evidences", "tenant", "cloud_account_id", "status", "finding_type","cluster", "starts_at", "labels", "updated_at", "ends_at", "cloud_resource_id", "category", "created_at", "urgency", "subject_owner", "subject_owner_kind", "nb_status")
			 VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30)
			 ON CONFLICT (tenant, cloud_account_id, finding_id)
			 DO UPDATE SET
			 	updated_at = EXCLUDED.updated_at
				, title = EXCLUDED.title
				, subject_type = EXCLUDED.subject_type
				, subject_node = EXCLUDED.subject_node
				, subject_namespace = EXCLUDED.subject_namespace
				, subject_name = EXCLUDED.subject_name
				, status = EXCLUDED.status
				, starts_at = EXCLUDED.starts_at
				, source = EXCLUDED.source
				, service_key = EXCLUDED.service_key
				, priority = EXCLUDED.priority
				, fingerprint = EXCLUDED.fingerprint
				, finding_type = EXCLUDED.finding_type
				, evidences = EXCLUDED.evidences
				, ends_at = EXCLUDED.ends_at
				, description = EXCLUDED.description
				, cluster = EXCLUDED.cluster
				, cloud_resource_id = EXCLUDED.cloud_resource_id
				, category = EXCLUDED.category
				, aggregation_key = EXCLUDED.aggregation_key
				, principal = EXCLUDED.principal
				, urgency = EXCLUDED.urgency
				, subject_owner = EXCLUDED.subject_owner
				, subject_owner_kind = EXCLUDED.subject_owner_kind
			 RETURNING (xmax = 0) AS is_insert`

	if id == "" {
		id = common.GenerateUUID()
	}
	// convert evidence to json string
	evidences, err := common.MarshalJson(event.Evidences)
	if err != nil {
		slog.Error("event: failed to marshal evidences", "error", err)
		return "", fmt.Errorf("event: failed to marshal evidences: %w", err)
	}
	// convert evidence to json string
	labels, err := common.MarshalJson(event.Labels)
	if err != nil {
		slog.Error("event: failed to marshal labels", "error", err)
		return "", fmt.Errorf("event: failed to marshal labels: %w", err)
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: unable to process request", "error", err)
		return "", err
	}

	nullableCloudResourceId := sql.NullString{
		String: event.CloudResourceId,
		Valid:  event.CloudResourceId != "",
	}

	if event.CreatedAt == nil {
		t0 := time.Now().UTC()
		event.CreatedAt = &t0
	}

	// Default urgency to LOW if not specified
	if event.Urgency == "" {
		event.Urgency = "LOW"
	}

	nbStatus := triage.NBStatusOpen
	if isPointInTimeEvent {
		nbStatus = triage.NBStatusResolved
	}

	var isInsert bool
	err = dbms.Db.QueryRow(query, id, event.FindingId, event.Title, event.Description, event.Source, event.AggregationKey, event.Priority, event.SubjectType, event.SubjectName, event.SubjectNamespace, event.SubjectNode, event.ServiceKey, event.Fingerprint, evidences, event.Tenant, event.AccountId, event.Status, event.FindingType, event.Cluster, event.StartsAt, labels, time.Now().UTC(), event.EndsAt, nullableCloudResourceId, event.Category, event.CreatedAt, event.Urgency, event.SubjectOwner, event.SubjectOwnerKind, nbStatus).Scan(&isInsert)
	if err != nil {
		slog.Error("event: failed to insert event", "error", err)
		return id, err
	}

	// Publish new events for post-processing (replaces RPC new_event_notification trigger).
	// Use background-retry: the row is already committed and the caller (webhook / cron /
	// anomaly path) has no useful way to react to a publish failure, so we'd rather absorb
	// a transient Rabbit blip in a background worker than silently log-and-drop here. The
	// worker logs at ERROR and increments common.MqBackgroundRetryDroppedTotal on terminal
	// failure, so drops aren't invisible.
	if isInsert {
		// The deferral hook takes over only for a genuine insert. It must be decided
		// here rather than by the caller: only this function knows whether the upsert
		// inserted or updated (RETURNING xmax = 0). A caller that guessed would, on
		// losing a race to a concurrent insert of the same finding_id, enqueue
		// enrichment for a row that has already been through the whole pipeline.
		if cfg.deferPostProcess != nil {
			cfg.deferPostProcess(id)
		} else {
			_ = common.MqPublish(
				config.Config.RabbitMqEventPostProcessExchange,
				config.Config.RabbitMqEventPostProcessQueue,
				map[string]any{"event_id": id},
				common.MqPublishWithExpiration(1*time.Hour),
				common.MqPublishWithBackgroundRetry(),
				common.MqPublishWithContext(cfg.publishCtx),
			)
		}
	} else if !cfg.skipWorkflowRefire &&
		(strings.EqualFold(string(event.Status), string(EventStatusFiring)) || isPointInTimeEvent) {
		// Re-fire of an existing event: ON CONFLICT updated an already-present row.
		// The post-process pipeline (triage/llm/notification) only runs on first
		// insert, so without this an event-trigger workflow would never see repeat
		// occurrences — and a workflow created AFTER the event first appeared would
		// never fire at all. Re-deliver to the runbook workflow exchange ONLY
		// (workflow_only), so workflows match each FIRING occurrence with no
		// duplicate notifications or investigations. Gated to FIRING so resolve/
		// close updates don't spuriously trigger, and skipped for non-occurrence
		// re-persists (WithoutWorkflowRefire, e.g. UpdateEvent metadata edits).
		// configuration_change events (issue #36597) are born CLOSED and never
		// pass through FIRING, so they need their own re-fire path here too —
		// otherwise a second occurrence of the same finding_id goes silent to
		// event-trigger workflows.
		_ = common.MqPublish(
			config.Config.RabbitMqEventPostProcessExchange,
			config.Config.RabbitMqEventPostProcessQueue,
			map[string]any{"event_id": id, "workflow_only": true},
			common.MqPublishWithExpiration(1*time.Hour),
			common.MqPublishWithBackgroundRetry(),
			common.MqPublishWithContext(cfg.publishCtx),
		)
	} else if !cfg.skipWorkflowRefire && strings.EqualFold(string(event.Status), string(EventStatusResolved)) {
		// FIRING→RESOLVED arrives as an ON CONFLICT update too. Deliver the
		// resolved notification only (notify_resolved); the upsert can't see the
		// prior status, so repeated resolve webhooks re-publish and
		// notifications-server dedupes per delivered thread.
		publishResolved(cfg.publishCtx, []string{id})
	}

	return id, nil
}

var namespaceKeys = []string{"namespace", "subject_namespace", "k8s_namespace"}
var subjectKeys = []string{"subject", "subject_name", "pod", "pod_name", "aws_event_instance", "service_name", "service.name", "dimension_QueueName", "job", "job_name"}
var clusterKeys = []string{"cluster", "k8s_cluster"}
var endTimeKeys = []string{"end", "ends_at", "ended_at"}
var startTimeKeys = []string{"start", "starts_at", "started_at"}

// getPlaybookStartEndTime returns the playbook's observability window and the
// alert start it was derived from. The window starts playbookLeadIn before the
// alert so metric and log queries cover the lead-up; detected is the alert
// start itself, for enrichers that ask what happened before the alert.
func getPlaybookStartEndTime(e Event) (start, end, detected time.Time) {
	var startTime, endTime time.Time
	if e.StartsAt != nil {
		startTime = *e.StartsAt
	} else {
		for _, s := range startTimeKeys {
			if val, ok := e.Labels[s]; ok && val != "" {
				startsAt1, err := common.ParseTimeValue(val)
				if err == nil {
					startTime = startsAt1
					break
				}
			}
		}
	}

	if e.EndsAt != nil {
		endTime = *e.EndsAt
	} else {
		for _, s := range endTimeKeys {
			if val, ok := e.Labels[s]; ok && val != "" {
				endsAt1, err := common.ParseTimeValue(val)
				if err == nil {
					endTime = endsAt1
					break
				}
			}
		}
	}

	if endTime.IsZero() && !startTime.IsZero() {
		endTime = startTime.Add(time.Hour)
	} else if endTime.IsZero() {
		endTime = time.Now()
	}

	if startTime.IsZero() {
		startTime = endTime.Add(-time.Hour)
	}

	return startTime.Add(-playbookLeadIn).UTC(), endTime.UTC(), startTime.UTC()
}

// playbookLeadIn is how far before the alert the playbook window starts.
const playbookLeadIn = 10 * time.Minute

// repeatDeliveryUpdateSQL applies a repeat delivery of an alert that is already
// stored (an Alertmanager repeat, a Datadog re-notification) to its event.
//
// A repeat is the source's payload again. It carries nothing of what enrichment
// has added to the event since the first delivery, so it must not erase that:
//   - Labels are merged, and the delivery's value wins for a label both carry.
//     A label the playbook added survives. So does a label the source has since
//     stopped sending: nothing records which labels came from the source, and a
//     stale one that lasts until the event closes costs less than losing the
//     playbook's.
//   - An empty subject field or resource link leaves the stored one alone.
//   - Unless the delivery names a different subject. The stored namespace, type,
//     owner and resource then describe the old subject, so all of them are
//     replaced as sent, empty or not. An event stored without a subject name has
//     no old subject: "<>" against its empty name is NULL, not true, so naming it
//     fills the name in and keeps the rest.
//
// Priority is always the delivery's: an escalation arrives as a repeat.
//
// A single statement for the reason enrichmentMergeSQL is one: enrichment writes
// these columns while repeats arrive, and the comparison with the stored subject
// has to be made against the row version the update locks.
const repeatDeliveryUpdateSQL = `UPDATE events
		    SET updated_at = $1,
		        labels = CASE
		                WHEN jsonb_typeof(labels) = 'object' THEN labels
		                ELSE '{}'::jsonb
		            END || $3::jsonb,
		        priority           = $4,
		        subject_name       = COALESCE(NULLIF($5, ''), subject_name),
		        subject_namespace  = CASE WHEN NULLIF($5, '') <> NULLIF(subject_name, '') THEN $6 ELSE COALESCE(NULLIF($6, ''), subject_namespace) END,
		        subject_type       = CASE WHEN NULLIF($5, '') <> NULLIF(subject_name, '') THEN $7 ELSE COALESCE(NULLIF($7, ''), subject_type) END,
		        cloud_resource_id  = CASE WHEN NULLIF($5, '') <> NULLIF(subject_name, '') THEN $8 ELSE COALESCE($8, cloud_resource_id) END,
		        subject_owner      = CASE WHEN NULLIF($5, '') <> NULLIF(subject_name, '') THEN $9 ELSE COALESCE(NULLIF($9, ''), subject_owner) END,
		        subject_owner_kind = CASE WHEN NULLIF($5, '') <> NULLIF(subject_name, '') THEN $10 ELSE COALESCE(NULLIF($10, ''), subject_owner_kind) END
		  WHERE id = $2`

func InvestigateEvent(sc *security.RequestContext, webhookEvent Event, id string) (string, error) {
	start := time.Now().UTC()
	tenantId := sc.GetSecurityContext().GetTenantId()
	accountId := webhookEvent.AccountId
	// Normalize here as well as in InsertEvent: everything below this line
	// (structured logs, processing metrics, event-rule matching) reads the
	// source, and must see the canonical value rather than a retired alias.
	webhookEvent.Source = normalizeEventSource(webhookEvent.Source)
	// Same reason: the playbook run below is handed webhookEvent.SubjectNode
	// directly, before InsertEvent ever sees it, so normalizing only there would
	// leave the enrichers reading the raw scrape address.
	webhookEvent.SubjectNode = normalizeSubjectNode(webhookEvent.SubjectNode)
	eventSource := webhookEvent.Source
	aggregationKey := webhookEvent.AggregationKey

	// Debug logging for Azure Event Grid events
	sc.GetLogger().Info("InvestigateEvent: ENTRY",
		"id", id,
		"finding_id", webhookEvent.FindingId,
		"source", eventSource,
		"title", webhookEvent.Title,
		"tenant", tenantId,
		"account_id", accountId,
		"aggregation_key", aggregationKey)

	defer func() {
		duration := time.Since(start).Seconds()
		common.MetricsEventProcessingDuration(sc.GetContext(), duration, eventSource, tenantId, accountId, aggregationKey)
	}()

	if accountId == "" {
		sc.GetLogger().Error("InvestigateEvent: account ID is empty", "source", eventSource, "finding_id", webhookEvent.FindingId)
		common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonMissingAccountID)
		return "", errors.New("account ID is required")
	}

	if tenantId == "" {
		sc.GetLogger().Error("InvestigateEvent: tenant ID is empty", "source", eventSource, "finding_id", webhookEvent.FindingId, "account_id", accountId)
		common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonMissingTenantID)
		return "", errors.New("tenant ID is required")
	}

	if webhookEvent.EndsAt == nil {
		for _, s := range endTimeKeys {
			if webhookEvent.Labels[s] != "" {
				endsAt1, err := common.ParseTimeValue(webhookEvent.Labels[s])
				if err == nil {
					webhookEvent.EndsAt = &endsAt1
				}
			}
		}
	}

	if webhookEvent.StartsAt == nil {
		for _, s := range startTimeKeys {
			if webhookEvent.Labels[s] != "" {
				startsAt1, err := common.ParseTimeValue(webhookEvent.Labels[s])
				if err == nil {
					webhookEvent.StartsAt = &startsAt1
				}
			}
		}
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		sc.GetLogger().Error("event: unable to get database manager", "error", err)
		return "", err
	}

	// Drop the event before any enrichment runs if its account is switched off.
	// Returning a nil error rather than an error is deliberate: the event was
	// handled, just not stored. An error here would make the RPC caller retry
	// and the webhook delivery record as failed, both of which would recreate
	// the load this check exists to shed.
	if isCloudAccountDisabled(sc, dbms, accountId) {
		sc.GetLogger().Info("InvestigateEvent: skipping event, cloud account is disabled",
			"account_id", accountId,
			"tenant", tenantId,
			"source", eventSource,
			"finding_id", webhookEvent.FindingId)
		common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonAccountDisabled)
		return "", nil
	}

	// Populate SubjectOwner from labels or k8s state when ingestion left it
	// empty. Some emitters (agent prometheus-rule path, raw alertmanager
	// bridges) omit SubjectOwner even when the alert clearly identifies a
	// deployment. Without this, downstream observability enrichers
	// (eventrule_actions_traces, eventrule_actions_logs,
	// knowledge_graph_service_map) fall back to SubjectName, which is a pod
	// name with a ReplicaSet hash and does not match deployment-keyed
	// observability columns — they silently return zero results.
	//
	// Resolution priority:
	//   1. Explicit deployment / statefulset / daemonset alert labels
	//   2. k8s_pods ⋈ k8s_workloads lookup (authoritative — validates the
	//      workload exists, handles all three controller kinds)
	// If both miss, SubjectOwner stays empty; consumers keep their current
	// fallback to SubjectName. We deliberately do not regex-strip the pod
	// hash — false positives on Job/CronJob/bare pods would be worse than a
	// missing owner.
	//
	// Skip logical-database subjects: their name is a datname, not a pod, and
	// the k8s_pods.workload_name arm of lookupPodOwner would otherwise re-pin a
	// database onto a same-named same-namespace workload (e.g. db "keycloak" →
	// the keycloak workload). The nb_skip_workload_match label is already
	// consumed by MatchWorkloadAndEnrich by now, so gate on subject_type here.
	if webhookEvent.SubjectOwner == "" && !isNonWorkloadDatastoreSubject(webhookEvent.SubjectType) {
		if name, kind := ownerFromLabels(webhookEvent.Labels); name != "" {
			webhookEvent.SubjectOwner = name
			if webhookEvent.SubjectOwnerKind == "" {
				webhookEvent.SubjectOwnerKind = kind
			}
		} else {
			// Fall back to k8s state. Some webhook bridges leave SubjectName /
			// SubjectNamespace empty on the struct but stuff them into Labels;
			// the existing namespaceKeys / subjectKeys slices defined in this
			// file are already the codebase's source of truth for those
			// conventions, so reuse them here rather than re-listing.
			ns := webhookEvent.SubjectNamespace
			if ns == "" {
				for _, k := range namespaceKeys {
					if v := webhookEvent.Labels[k]; v != "" {
						ns = v
						break
					}
				}
			}
			pod := webhookEvent.SubjectName
			if pod == "" {
				for _, k := range subjectKeys {
					if v := webhookEvent.Labels[k]; v != "" {
						pod = v
						break
					}
				}
			}
			if name, kind := lookupPodOwner(sc, dbms, tenantId, ns, pod); name != "" {
				webhookEvent.SubjectOwner = name
				if webhookEvent.SubjectOwnerKind == "" {
					webhookEvent.SubjectOwnerKind = kind
				}
			}
		}
	}

	// Resolve cloud_resource_id for Kubernetes-sourced events from the cached
	// k8s state snapshot. Cloud-provider events resolve theirs later from
	// playbook evidence (linkCloudResourceId); K8s emitters never carry the
	// UUID because it is assigned server-side at discovery time. Done before
	// the existing-event check so both the duplicate-update and the insert
	// branch persist the resolved id (and previously-NULL duplicates backfill
	// as the event recurs).
	linkK8sCloudResourceId(sc, dbms, tenantId, &webhookEvent)

	// A RESOLVED/CLOSED closes the event it names and never reopens one. A
	// RESOLVED that names no event of its own sender closes by fingerprint
	// instead of creating a closed row (issue #39585).
	isResolve := isCloseStatus(webhookEvent.Status)
	if webhookEvent.FindingId == "" && IsResolveByFingerprint(webhookEvent) {
		// The sender does not know which firing ended, only the alert's identity.
		return resolveByFingerprint(sc, dbms, tenantId, accountId, webhookEvent)
	}

	var existingEventId string
	var existingSubjectName, existingSource sql.NullString
	err = dbms.Db.QueryRowx("SELECT id, subject_name, source FROM events WHERE tenant = $1 AND cloud_account_id = $2 AND finding_id = $3", tenantId, accountId, webhookEvent.FindingId).Scan(&existingEventId, &existingSubjectName, &existingSource)

	if err == nil && isResolve && existingSource.String != webhookEvent.Source {
		// finding_id is sender-chosen, so this one belongs to another integration's
		// event and is not this sender's to close. Treat it as matching nothing —
		// and do not fall through to the insert, whose upsert on the same finding_id
		// would overwrite that event.
		sc.GetLogger().Warn("event: resolve names an event from another source, leaving it untouched",
			"event_id", existingEventId, "finding_id", webhookEvent.FindingId,
			"event_source", existingSource.String, "resolve_source", webhookEvent.Source)
		if resolvesUnmatchedByFingerprint(webhookEvent) {
			return resolveByFingerprint(sc, dbms, tenantId, accountId, webhookEvent)
		}
		return "", nil
	}

	switch err {
	case nil: // event exists
		sc.GetLogger().Info("event: already processed, updating event details", "event_id", existingEventId, "finding_id", webhookEvent.FindingId)
		// Resolve the subject exactly as the insert below does, so the repeat names
		// the subject the event was stored with rather than the raw one it arrived
		// with. applySubjectFallbacks also leaves Labels non-nil, which the label
		// merge in repeatDeliveryUpdateSQL needs: a nil map marshals to null.
		applySubjectFallbacks(&webhookEvent)
		applyNodeSubjectCorrection(sc, dbms, tenantId, &webhookEvent)
		// The correction can miss on a repeat where the first delivery resolved it
		// (the exporter pod was rescheduled and its row is gone). The delivery then
		// still names the exporter pod, and writing that would move a corrected
		// event back onto it. Send no subject instead, so the stored one stands.
		if existingSubjectName.String != "" && !integrationcore.IsExporterWorkloadName(existingSubjectName.String) &&
			webhookEvent.SubjectType != "node" && integrationcore.IsExporterWorkloadName(webhookEvent.SubjectName) {
			webhookEvent.SubjectName, webhookEvent.SubjectNamespace, webhookEvent.SubjectType = "", "", ""
			webhookEvent.SubjectOwner, webhookEvent.SubjectOwnerKind, webhookEvent.CloudResourceId = "", "", ""
		}
		labels, err1 := common.MarshalJson(webhookEvent.Labels)
		if err1 != nil {
			slog.Error("event: failed to marshal labels", "error", err1)
			return "", fmt.Errorf("event: failed to marshal labels: %w", err1)
		}
		nullableCloudResourceId := sql.NullString{
			String: webhookEvent.CloudResourceId,
			Valid:  webhookEvent.CloudResourceId != "",
		}
		// Do NOT overwrite evidences — playbook-collected evidences would be lost.
		// Duplicate webhooks carry the same inline evidences, so nothing new to store.
		_, err := dbms.Db.Exec(repeatDeliveryUpdateSQL, time.Now().UTC(), existingEventId, labels, webhookEvent.Priority, webhookEvent.SubjectName, webhookEvent.SubjectNamespace, webhookEvent.SubjectType, nullableCloudResourceId, webhookEvent.SubjectOwner, webhookEvent.SubjectOwnerKind)
		if err != nil {
			sc.GetLogger().Error("event: failed to update existing event", "error", err)
			return "", err
		}

		// Honour close transitions only: a FIRING on a CLOSED event leaves it
		// CLOSED, and a new firing must arrive with a new finding_id.
		if isResolve {
			closed, err := closeEventById(dbms, existingEventId, webhookEvent.Source, resolveEndsAt(webhookEvent))
			if err != nil {
				sc.GetLogger().Error("event: failed to close resolved event", "error", err, "event_id", existingEventId)
				return "", err
			}
			publishResolved(sc.GetContext(), closed)
		}

		oldSubjectEmpty := !existingSubjectName.Valid || existingSubjectName.String == ""
		newSubjectResolved := webhookEvent.SubjectName != ""
		if oldSubjectEmpty && newSubjectResolved {
			sc.GetLogger().Info("event: subject_name resolved, triggering playbook re-run",
				"event_id", existingEventId, "new_subject_name", webhookEvent.SubjectName)
			go func() {
				if err := RefreshInvestigation(sc, existingEventId); err != nil {
					sc.GetLogger().Error("event: failed to refresh investigation after subject resolution",
						"event_id", existingEventId, "error", err)
				}
			}()
		}

		return existingEventId, nil
	case sql.ErrNoRows: // event does not exist, so process it
		if resolvesUnmatchedByFingerprint(webhookEvent) {
			return resolveByFingerprint(sc, dbms, tenantId, accountId, webhookEvent)
		}
		// Resolve subject/namespace/cluster from the webhook's own labels before
		// anything else. These fallbacks only ever consult labels that arrived in
		// the payload, so they need nothing the playbook produces, and running them
		// first means the row commits with subject_name already filled — which is
		// what k8s-collector's RESOLVED lookup and the duplicate branch above read
		// while asynchronous enrichment is still in flight.
		//
		// Behaviour note: the playbook (and mergeAggregatedAlertLabels) may
		// overwrite the value of a payload label. Previously the fallback ran
		// afterwards and would have picked up that overwritten value; now it takes
		// the pre-playbook one. That only differs when the field arrived empty AND
		// the label it resolves from is both present in the payload and later
		// rewritten by an enricher.
		applySubjectFallbacks(&webhookEvent)
		applyNodeSubjectCorrection(sc, dbms, tenantId, &webhookEvent)

		if webhookEvent.CreatedAt == nil {
			webhookEvent.CreatedAt = &start
		}

		// With the flag on, the playbook runs on the event_enrich queue instead of
		// here. configuration_change never defers: the inline path already skips the
		// playbook for it below, and its point-in-time CLOSED handling has to fire at
		// insert time.
		asyncEnrichment := config.Config.FeatureEventAsyncEnrichmentEnabled &&
			webhookEvent.FindingType != "configuration_change"

		if !asyncEnrichment {
			runPlaybookEnrichment(sc, &webhookEvent, id, time.Time{})
		}

		sc.GetLogger().Info("InvestigateEvent: calling InsertEvent",
			"id", id,
			"finding_id", webhookEvent.FindingId,
			"source", eventSource,
			"tenant", tenantId,
			"account_id", accountId,
			"async_enrichment", asyncEnrichment)

		insertOpts := []InsertOption{WithPublishContext(sc.GetContext())}
		if asyncEnrichment {
			insertOpts = append(insertOpts, WithDeferredPostProcess(func(eventId string) {
				publishEventEnrichment(sc, eventId, tenantId, webhookEvent.AccountId)
			}))
		}

		id, err := InsertEvent(webhookEvent, id, insertOpts...)

		if err != nil {
			sc.GetLogger().Error("InvestigateEvent: InsertEvent failed",
				"error", err,
				"finding_id", webhookEvent.FindingId,
				"source", eventSource,
				"tenant", tenantId,
				"account_id", accountId)
			common.MetricsEventProcessingFailed(sc.GetContext(), eventSource, tenantId, accountId, common.MetricReasonEventInsertionFailed)
			return "", fmt.Errorf("event: failed to insert event: %w", err)
		}

		sc.GetLogger().Info("InvestigateEvent: successfully inserted event",
			"event_id", id,
			"finding_id", webhookEvent.FindingId,
			"source", eventSource,
			"tenant", tenantId,
			"account_id", accountId)
		common.MetricsEventProcessingTotal(sc.GetContext(), eventSource, tenantId, accountId, common.MetricStatusCompleted)
		return id, err
	default: // some other db error
		sc.GetLogger().Error("event: failed to check for existing event", "error", err, "source", eventSource, "finding_id", webhookEvent.FindingId)
		return "", err
	}
}

// publishEventEnrichment hands a freshly inserted event to the event_enrich queue.
//
// Persistent delivery on a durable queue: unlike event_post_process, losing one of
// these on a broker restart would leave the event both unenriched and never
// post-processed, since the post-process publish is the consumer's job. For the
// same reason there is deliberately no expiration — a backlog longer than an hour
// must survive rather than silently evaporate, and there is no DLX to catch it.
func publishEventEnrichment(sc *security.RequestContext, eventId, tenantId, accountId string) {
	err := common.MqPublish(
		config.Config.RabbitMqEventEnrichExchange,
		config.Config.RabbitMqEventEnrichQueue,
		eventtypes.EventEnrichmentMessage{
			EventID:   eventId,
			TenantID:  tenantId,
			AccountID: accountId,
		},
		common.MqPublishWithPersistentDelivery(),
		common.MqPublishWithBackgroundRetry(),
		common.MqPublishWithContext(sc.GetContext()),
	)
	if err != nil {
		sc.GetLogger().Error("event: failed to enqueue enrichment", "error", err, "event_id", eventId)
	}
}

// applySubjectFallbacks fills Cluster, SubjectNamespace and SubjectName from the
// event's own labels when they arrived empty. Only labels already on the event are
// consulted, which is what keeps playbook-injected labels (prometheus_enricher's
// "pod"/"job", say) from being mistaken for the real subject.
func applySubjectFallbacks(webhookEvent *Event) {
	if webhookEvent.Labels == nil {
		webhookEvent.Labels = map[string]string{}
	}

	if webhookEvent.Cluster == "" {
		for _, key := range clusterKeys {
			if cluster, ok := webhookEvent.Labels[key]; ok {
				webhookEvent.Cluster = cluster
				break
			}
		}
	}

	if webhookEvent.SubjectNamespace == "" {
		for _, key := range namespaceKeys {
			if namespace, ok := webhookEvent.Labels[key]; ok {
				webhookEvent.SubjectNamespace = namespace
				break
			}
		}
	}

	if webhookEvent.SubjectName == "" {
		for _, key := range subjectKeys {
			if subject, ok := webhookEvent.Labels[key]; ok {
				webhookEvent.SubjectName = subject
				break
			}
		}
	}
}

// applyNodeSubjectCorrection repoints a node-scoped alert at the node it is
// actually about. Prometheus stamps every series an exporter emits with that
// exporter's own pod/namespace labels, so alerts like NodeSystemSaturation and
// NodeCPUHighUsage arrive claiming to be about a node-exporter pod: the
// investigation then shows that pod's CPU chart, its logs and its events, none
// of which describe the node whose saturation fired the alert.
//
// core.ResolveNodeSubject returns "" unless the alert's rule expression is
// node-scoped AND the current subject is an exporter, so an alert genuinely
// about the exporter keeps its subject. It also returns "" when no node name can
// be resolved, in which case the event is left exactly as it arrived rather than
// half-rewritten.
//
// Runs for a repeat delivery as well as a new event. The repeat names the
// exporter pod again, and would otherwise point the stored event back at it.
//
// The resource link is part of the identity being replaced: by the time this
// runs, linkK8sCloudResourceId has linked the exporter pod, so the link is
// resolved again for the node.
func applyNodeSubjectCorrection(sc *security.RequestContext, dbms *database.DatabaseManager, tenantId string, webhookEvent *Event) {
	alertName := webhookEvent.AggregationKey
	if v := webhookEvent.Labels["nb_alert_name"]; v != "" {
		alertName = v
	} else if v := webhookEvent.Labels["alertname"]; v != "" {
		alertName = v
	}

	nodeName := integrationcore.ResolveNodeSubject(sc, webhookEvent.AccountId, integrationcore.NodeSubjectInput{
		AlertName:        alertName,
		SubjectName:      webhookEvent.SubjectName,
		SubjectType:      webhookEvent.SubjectType,
		SubjectNamespace: webhookEvent.SubjectNamespace,
		Labels:           webhookEvent.Labels,
	})
	if nodeName == "" {
		return
	}

	sc.GetLogger().Info("event: repointed node-scoped alert from exporter to node",
		"alert", alertName, "from", webhookEvent.SubjectName, "to", nodeName,
		"account_id", webhookEvent.AccountId)

	webhookEvent.SubjectType = "node"
	webhookEvent.SubjectName = nodeName
	webhookEvent.SubjectNode = nodeName
	// The namespace, owner and service key all described the exporter pod. A node
	// is cluster-scoped, so leaving them would keep correlating this event with
	// the monitoring namespace's workloads.
	webhookEvent.SubjectNamespace = ""
	webhookEvent.SubjectOwner = ""
	webhookEvent.SubjectOwnerKind = ""
	webhookEvent.ServiceKey = ""
	webhookEvent.CloudResourceId = ""
	linkK8sCloudResourceId(sc, dbms, tenantId, webhookEvent)
}

// runPlaybookEnrichment executes the event's playbook and applies every
// post-playbook mutation to webhookEvent in place: it appends evidence cards,
// merges aggregated-alert labels and links the cloud resource.
//
// deadline, when non-zero, bounds the run — ExecutePlaybook stops before starting
// an action once it passes and returns what it has, so partial evidence is real
// evidence rather than an abandoned goroutine's.
//
// Returns the evidence cards appended and the labels added or changed, which the
// asynchronous path needs in order to merge them into an already-persisted row.
// The inline path mutates webhookEvent and ignores both.
func runPlaybookEnrichment(sc *security.RequestContext, webhookEvent *Event, eventId string, deadline time.Time) (addedEvidences []any, labelDelta map[string]string) {
	eventSource := webhookEvent.Source
	tenantId := sc.GetSecurityContext().GetTenantId()
	accountId := webhookEvent.AccountId
	aggregationKey := webhookEvent.AggregationKey

	labelsBefore := make(map[string]string, len(webhookEvent.Labels))
	for k, v := range webhookEvent.Labels {
		labelsBefore[k] = v
	}
	evidenceCountBefore := len(webhookEvent.Evidences)

	var evidenceResponse []eventrule.PlaybookActionExecutionResponse

	// Skip playbook execution for configuration change events
	if webhookEvent.FindingType == "configuration_change" {
		sc.GetLogger().Info("event: skipping playbook execution for configuration change event", "event_id", eventId, "aggregation_key", webhookEvent.AggregationKey)
	} else {
		eventruleStart, eventruleEnd, eventruleDetected := getPlaybookStartEndTime(*webhookEvent)

		// Use the actual alert name for playbook lookup instead of AggregationKey (which is the PagerDuty service name).
		// nb_alert_name is set during webhook enrichment and contains the correct alert name.
		playbookName := webhookEvent.AggregationKey
		if alertName, ok := webhookEvent.Labels["nb_alert_name"]; ok && alertName != "" {
			playbookName = alertName
		} else if alertName, ok := webhookEvent.Labels["alertname"]; ok && alertName != "" {
			playbookName = alertName
		}

		existingEvidenceActions := extractEvidenceActionNames(webhookEvent.Evidences)

		var err error
		evidenceResponse, err = eventrule.ExecutePlaybook(sc, webhookEvent.AccountId, playbooks.PlaybookEvent{
			EventId:     eventId,
			Deadline:    deadline,
			Name:        playbookName,
			Labels:      webhookEvent.Labels,
			Annotations: map[string]string{},
			StartedAt:   &eventruleStart,
			EndedAt:     &eventruleEnd,
			DetectedAt:  &eventruleDetected,
			IncidentAt: resolveIncidentAt(sc, webhookEvent.AggregationKey, webhookEvent.AccountId,
				webhookEvent.SubjectName, webhookEvent.SubjectNamespace, webhookEvent.SubjectType, webhookEvent.StartsAt),
			Source:                  webhookEvent.Source,
			SubjectName:             webhookEvent.SubjectName,
			SubjectType:             webhookEvent.SubjectType,
			SubjectOwner:            webhookEvent.SubjectOwner,
			SubjectOwnerKind:        webhookEvent.SubjectOwnerKind,
			SubjectNamespace:        webhookEvent.SubjectNamespace,
			SubjectNode:             webhookEvent.SubjectNode,
			AggregationKey:          webhookEvent.AggregationKey,
			ExistingEvidenceActions: existingEvidenceActions,
		})
		if err != nil {
			sc.GetLogger().Error("event: unable to process playbook for event", "error", err, "aggregation_key", webhookEvent.AggregationKey, "account_id", webhookEvent.AccountId, "event_id", webhookEvent.Fingerprint)
		}

		for _, e := range evidenceResponse {
			if e.Error != nil {
				common.MetricsPlaybookActionFailed(sc.GetContext(), e.ActionName, eventSource, tenantId, accountId, common.MetricReasonActionExecutionError)
			} else {
				common.MetricsPlaybookActionTotal(sc.GetContext(), e.ActionName, eventSource, tenantId, accountId, common.MetricStatusCompleted)
			}
			// Record individual action duration
			common.MetricsPlaybookActionDuration(sc.GetContext(), e.DurationSeconds, e.ActionName, eventSource, tenantId, accountId)

			if e.Response == nil {
				continue
			}
			structResponse, err := common.MarshalStructToMap(e.Response)
			if err != nil {
				common.MetricsPlaybookActionFailed(sc.GetContext(), e.ActionName, eventSource, tenantId, accountId, common.MetricReasonEvidenceMarshalFailed)
				webhookEvent.Evidences = append(webhookEvent.Evidences, e.Response)
				continue
			}
			structResponse["type"] = e.Response.GetFormatName()
			additionalInfo, ok := structResponse["additional_info"].(map[string]any)
			if !ok {
				additionalInfo = map[string]any{}
				structResponse["additional_info"] = additionalInfo
			}
			if e.ActionTitle != "" {
				additionalInfo["title"] = e.ActionTitle
			}
			// Only set action_name if not already set by the action response
			if _, exists := additionalInfo["action_name"]; !exists {
				additionalInfo["action_name"] = e.ActionName
			}

			playbooks.ShapeEvidenceForFrontend(structResponse)
			webhookEvent.Evidences = append(webhookEvent.Evidences, structResponse)
		}
	}

	mergeAggregatedAlertLabels(webhookEvent.Labels, aggregationKey, evidenceResponse)
	linkCloudResourceId(webhookEvent, evidenceResponse)

	if len(webhookEvent.Evidences) > evidenceCountBefore {
		addedEvidences = webhookEvent.Evidences[evidenceCountBefore:]
	}

	// Both the playbook itself (it writes back into the same Labels map) and
	// mergeAggregatedAlertLabels can add or rewrite labels. The async path needs
	// just those, so it can merge them into the persisted row rather than
	// overwriting a labels map another writer may have touched meanwhile.
	labelDelta = map[string]string{}
	for k, v := range webhookEvent.Labels {
		if before, existed := labelsBefore[k]; !existed || before != v {
			labelDelta[k] = v
		}
	}

	return addedEvidences, labelDelta
}

// resolveIncidentAt returns when the subject container actually terminated, for
// the event classes where that differs from when the event was raised. Returns
// nil whenever the cluster cannot answer, so enrichment falls back to the
// event's own timestamps. See playbooks.PodTerminationTime.
func resolveIncidentAt(sc *security.RequestContext, aggregationKey, accountId, subjectName, subjectNamespace, subjectType string, startsAt *time.Time) *time.Time {
	if !playbooks.NeedsIncidentTimeAnchor(aggregationKey) {
		return nil
	}
	// The anchor is read off a Pod's container statuses, so a subject that is not
	// a pod can only ever miss — and missing is expensive: get_resource ignores
	// its name filter, so each futile lookup ships back every pod in the cluster.
	// Measured on dev, events whose subject was a workload name (report-worker,
	// web-app — no ReplicaSet hash) each pulled 501-543 objects to find nothing.
	switch strings.ToLower(subjectType) {
	case "", "pod":
		// Unspecified is treated as a pod, matching linkK8sCloudResourceId.
	default:
		return nil
	}
	terminatedAt, ok := playbooks.PodTerminationTime(accountId, subjectName, subjectNamespace, sc.GetLogger())
	if !ok {
		return nil
	}
	if startsAt != nil {
		if skew := startsAt.Sub(terminatedAt); skew > 2*time.Minute {
			sc.GetLogger().Info("event: anchoring evidence on container termination rather than detection time",
				"aggregation_key", aggregationKey, "pod", subjectName, "namespace", subjectNamespace,
				"detected_at", startsAt.UTC(), "terminated_at", terminatedAt, "skew_seconds", int(skew.Seconds()))
		}
	}
	return &terminatedAt
}

func extractEvidenceActionNames(evidences []any) map[string]bool {
	actions := map[string]bool{}
	for _, e := range evidences {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		ai, ok := m["additional_info"].(map[string]any)
		if !ok {
			continue
		}
		names := make([]string, 0, 2)
		if name, ok := ai["action_name"].(string); ok && name != "" {
			names = append(names, name)
		}
		if name, ok := ai["actual_action_name"].(string); ok && name != "" {
			names = append(names, name)
		}
		if len(names) == 0 {
			continue
		}

		// A log enrichment that carried nothing did not enrich. The names collected here
		// suppress the server-side equivalent action (eventrule.executeAutoActions), and
		// for logs that suppression is category-wide: any one member of the log-action set
		// having run marks the whole category done. So an agent k8s_pod_log_enricher
		// evidence whose gzip decodes to zero bytes — 62 of the 433 written in a week —
		// stopped the `logs` action from ever asking the account's configured log source.
		//
		// Restricted to log actions deliberately. The same "empty evidence still suppresses
		// its twin" pattern may well exist for other enrichers, but the measurement behind
		// this change only covers logs, and widening it would change what evidence every
		// investigation collects on no evidence at all.
		if lo.SomeBy(names, eventrule.IsLogAction) && !evidenceHasContent(m) {
			continue
		}
		for _, name := range names {
			actions[name] = true
		}
	}
	return actions
}

// evidenceHasContent reports whether an evidence carries anything worth keeping.
//
// Deliberately conservative: it answers false only for shapes it positively
// recognises as empty, so an unfamiliar enrichment still suppresses its
// server-side twin exactly as before.
func evidenceHasContent(evidence map[string]any) bool {
	switch data := evidence["data"].(type) {
	case string:
		if playbooks.IsWrappedAgentPayload(data) {
			decoded, err := playbooks.DecodeAgentPayload(data)
			if err != nil {
				// Undecodable is not the same as empty — leave the old behaviour.
				return true
			}
			if playbooks.IsLogRetrievalFailure(decoded) {
				return false
			}
			return strings.TrimSpace(decoded) != ""
		}
		if trimmed := strings.TrimSpace(data); trimmed == "" {
			return false
		} else if strings.HasPrefix(trimmed, "{") {
			var wrapper struct {
				Data []any `json:"data"`
			}
			if err := common.UnmarshalJson([]byte(trimmed), &wrapper); err == nil && wrapper.Data != nil {
				return len(wrapper.Data) > 0
			}
		}
		return true
	case map[string]any:
		if inner, ok := data["data"].([]any); ok {
			return len(inner) > 0
		}
		return true
	case []any:
		return len(data) > 0
	case nil:
		return false
	}
	return true
}

// dedupeEvidencesByContent removes exact-duplicate evidence elements (same serialized
// content), preserving order. Guards against an enricher emitting the same payload more
// than once in a single run. Elements that can't be marshaled are kept as-is.
func dedupeEvidencesByContent(evidences []any) []any {
	seen := make(map[string]bool, len(evidences))
	out := make([]any, 0, len(evidences))
	for _, ev := range evidences {
		key, err := common.MarshalJson(ev)
		if err != nil {
			out = append(out, ev)
			continue
		}
		if seen[string(key)] {
			continue
		}
		seen[string(key)] = true
		out = append(out, ev)
	}
	return out
}

// retainEvidenceOnRefresh reports whether an evidence element already on the
// event survives a playbook refresh, which regenerates enricher cards and drops
// the previous copies so they do not stack up.
func retainEvidenceOnRefresh(evidence map[string]any, regeneratedActions map[string]bool, sourceEvidenceActions map[string]bool) bool {
	additionalInfo, ok := evidence["additional_info"].(map[string]any)
	if !ok {
		return false
	}

	// Evidence an automation attached (events.add_evidence) is not this
	// playbook's output and cannot be regenerated by it, so it is kept
	// unconditionally — ahead of the regeneratedActions skip below, because
	// automation cards render under a shared action name and a run emitting that
	// same action would otherwise delete them as stale duplicates.
	//
	// Two marks say "an automation wrote this". The stamp is present whenever a
	// workflow run produced the card. A card written by the builder's Run Task,
	// which executes one task with no workflow identity to stamp, carries only
	// actual_action_name — and keying on the stamp alone quietly deleted those on
	// the next refresh, minutes after the author watched the card appear. Both
	// are written server-side, so neither can be spoofed to retain enricher output.
	if _, fromWorkflow := additionalInfo[evidenceSourceWorkflowKey].(map[string]any); fromWorkflow {
		return true
	}
	actualActionName, _ := additionalInfo["actual_action_name"].(string)
	if actualActionName == authoredEvidenceRenderer {
		return true
	}

	actionName, _ := additionalInfo["action_name"].(string)
	// Skip an action the playbook just regenerated — the fresh copy in
	// newEvidences replaces it. Retaining it duplicates the card on every refresh.
	if (actionName != "" && regeneratedActions[actionName]) || (actualActionName != "" && regeneratedActions[actualActionName]) {
		return false
	}
	if actionName == "webhook_event" || sourceEvidenceActions[actionName] {
		return true
	}
	if actionType, ok := additionalInfo["action_type"].(string); ok && actionType == "event_detail" {
		return true
	}
	return false
}

func RefreshInvestigation(sc *security.RequestContext, eventId string) error {

	event, err := GetEvent(sc, eventId)
	if err != nil {
		return fmt.Errorf("failed to get event: %w", err)
	}
	if event.Id == "" {
		return fmt.Errorf("event not found: %s", eventId)
	}

	accountId := ""
	if event.CloudAccountId != nil {
		accountId = *event.CloudAccountId
	}

	if accountId == "" {
		return errors.New("account ID is not available in the event")
	}
	if !sc.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return common.ErrorUnauthorized("access denied for account: " + accountId)
	}

	tenantId := ""
	if event.Tenant != nil {
		tenantId = *event.Tenant
	}

	if tenantId == "" {
		return errors.New("tenant ID is not available in the event")
	}

	// Skip playbook execution for configuration change events
	if event.FindingType != nil && *event.FindingType == "configuration_change" {
		sc.GetLogger().Info("event: skipping playbook refresh for configuration change event", "event_id", eventId)
		return nil
	}

	eventSc := security.NewRequestContext(sc.GetContext(), security.NewSecurityContextForTenantAdmin(tenantId), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())

	labels := map[string]string{}
	if event.Labels.IsObject() {
		for k, v := range event.Labels.Object().(map[string]any) {
			labels[k] = v.(string)
		}
	}

	eventruleStart, eventruleEnd, eventruleDetected := getPlaybookStartEndTime(Event{
		StartsAt: event.StartsAt,
		EndsAt:   event.EndsAt,
		Labels:   labels,
	})

	playbookName := *event.AggregationKey
	if alertName, ok := labels["nb_alert_name"]; ok && alertName != "" {
		playbookName = alertName
	} else if alertName, ok := labels["alertname"]; ok && alertName != "" {
		playbookName = alertName
	}

	subjectOwner := ""
	if event.SubjectOwner != nil {
		subjectOwner = *event.SubjectOwner
	}

	subjectNamespace := ""
	if event.SubjectNamespace != nil {
		subjectNamespace = *event.SubjectNamespace
	}
	subjectNode := ""
	if event.SubjectNode != nil {
		subjectNode = *event.SubjectNode
	}

	newEvidenceResponse, err := eventrule.ExecutePlaybook(eventSc, accountId, playbooks.PlaybookEvent{
		EventId:     eventId,
		Name:        playbookName,
		Labels:      labels,
		Annotations: map[string]string{},
		StartedAt:   &eventruleStart,
		EndedAt:     &eventruleEnd,
		DetectedAt:  &eventruleDetected,
		IncidentAt: resolveIncidentAt(sc, *event.AggregationKey, accountId,
			*event.SubjectName, subjectNamespace, common.StrVal(event.SubjectType), event.StartsAt),
		Source:           *event.Source,
		SubjectName:      *event.SubjectName,
		SubjectType:      *event.SubjectType,
		SubjectOwner:     subjectOwner,
		SubjectOwnerKind: common.StrVal(event.SubjectOwnerKind),
		SubjectNamespace: subjectNamespace,
		SubjectNode:      subjectNode,
		AggregationKey:   *event.AggregationKey,
	})
	if err != nil {
		sc.GetLogger().Error("event: unable to process playbook for event", "error", err, "aggregation_key", *event.AggregationKey, "account_id", accountId, "event_id", event.Id)
	}

	labelsUpdated := mergeAggregatedAlertLabels(labels, *event.AggregationKey, newEvidenceResponse)

	newEvidences := []any{}
	for _, e := range newEvidenceResponse {
		// An enricher that ran but produced neither a response nor an error has no
		// evidence to show — skip it instead of persisting an empty placeholder that
		// renders as a blank card (e.g. cloud_logs with zero matching rows shows up
		// as an empty "Cloud Logs" card on the investigation view).
		if e.Response == nil && e.Error == nil {
			continue
		}
		structResponse := map[string]any{}
		if e.Response == nil {
			if e.Error != nil {
				structResponse["error"] = e.Error.Error()
			}
		} else {
			structResponse, err = common.MarshalStructToMap(e.Response)
			if err != nil {
				newEvidences = append(newEvidences, e.Response)
				continue
			}
			structResponse["type"] = e.Response.GetFormatName()
		}
		additionalInfo, ok := structResponse["additional_info"].(map[string]any)
		if !ok {
			additionalInfo = map[string]any{}
			structResponse["additional_info"] = additionalInfo
		}
		if e.ActionTitle != "" {
			additionalInfo["title"] = e.ActionTitle
		}
		if _, hasActionName := additionalInfo["action_name"]; !hasActionName {
			additionalInfo["action_name"] = e.ActionName
		}

		playbooks.ShapeEvidenceForFrontend(structResponse)
		newEvidences = append(newEvidences, structResponse)
	}

	var existingEvidences []map[string]any
	if event.Evidences.IsArray() {
		for _, e := range event.Evidences.Array() {
			if evidence, ok := e.(map[string]any); ok {
				existingEvidences = append(existingEvidences, evidence)
			}
		}
	}

	sourceEvidenceActions := map[string]bool{
		"datadog_event":                true,
		"datadog_incident":             true,
		"datadog_event_details":        true,
		"datadog_traces":               true,
		"datadog_logs":                 true,
		"datadog_metrics":              true,
		"datadog_error_tracking_issue": true,
		"cloud_logs":                   true,
		"pagerduty_alert":              true,
		"newrelic_alert_details":       true,
		"newrelic_entity_details":      true,
	}

	// Action names regenerated by this playbook run. An existing copy of any of these
	// (from a prior refresh) must NOT be retained, or each refresh stacks another
	// duplicate — and a stale empty copy would survive even after the empty-skip above.
	regeneratedActions := map[string]bool{}
	for _, ev := range newEvidences {
		if m, ok := ev.(map[string]any); ok {
			if ai, ok := m["additional_info"].(map[string]any); ok {
				if an, ok := ai["action_name"].(string); ok && an != "" {
					regeneratedActions[an] = true
				}
				// Some enrichers render under a different action_name (e.g. cloud_metrics
				// renders as prometheus_enricher) and carry the real one in actual_action_name;
				// track both so a prior copy is replaced, not duplicated.
				if aan, ok := ai["actual_action_name"].(string); ok && aan != "" {
					regeneratedActions[aan] = true
				}
			}
		}
	}

	retainedEvidences := []any{}
	for _, evidence := range existingEvidences {
		if retainEvidenceOnRefresh(evidence, regeneratedActions, sourceEvidenceActions) {
			retainedEvidences = append(retainedEvidences, evidence)
		}
	}

	// Collapse exact-duplicate evidences (e.g. an enricher that emits the same payload
	// twice in a single run) by content so the same card isn't shown multiple times.
	finalEvidences := dedupeEvidencesByContent(append(retainedEvidences, newEvidences...))

	evidencesJson, err := common.MarshalJson(finalEvidences)
	if err != nil {
		return fmt.Errorf("failed to marshal final evidences: %w", err)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	if labelsUpdated {
		labelsJson, err := common.MarshalJson(labels)
		if err != nil {
			return fmt.Errorf("failed to marshal updated labels: %w", err)
		}
		_, err = dbms.Db.Exec("UPDATE events SET evidences = $1, labels = $2, updated_at = $3 WHERE id = $4", evidencesJson, labelsJson, time.Now().UTC(), eventId)
		if err != nil {
			return fmt.Errorf("failed to update event: %w", err)
		}
	} else {
		_, err = dbms.Db.Exec("UPDATE events SET evidences = $1, updated_at = $2 WHERE id = $3", evidencesJson, time.Now().UTC(), eventId)
		if err != nil {
			return fmt.Errorf("failed to update event evidences: %w", err)
		}
	}

	return nil
}

// aggregatedAlertLabelDefs maps aggregation keys to the labels that should be
// extracted and the metric key used to match the event's series.
var aggregatedAlertLabelDefs = map[string]struct {
	labelsToExtract []string
	matchKey        string
}{
	"ApplicationAPIFailures": {
		labelsToExtract: []string{"path", "method", "status"},
		matchKey:        "destination_workload_name",
	},
	"HighErrorCriticalLogs": {
		labelsToExtract: []string{"container_id", "sample"},
		matchKey:        "app_id",
	},
}

// mergeAggregatedAlertLabels extracts per-series labels for aggregated alerts.
// It first checks for a dedicated aggregated_alert_label_enricher result;
// if that didn't run (e.g. instant query returned no data), it falls back to
// parsing the prometheus_enricher evidence and picking the matching series.
func mergeAggregatedAlertLabels(labels map[string]string, aggregationKey string, evidenceResponse []eventrule.PlaybookActionExecutionResponse) bool {
	// Try dedicated enricher first
	updated := false
	for _, e := range evidenceResponse {
		if e.Response == nil || e.ActionName != "aggregated_alert_label_enricher" {
			continue
		}
		if labelExtractor, ok := e.Response.(playbooks.PlaybookActionResponseLabelExtractor); ok {
			for k, v := range labelExtractor.ExtractLabels() {
				if vStr, ok := v.(string); ok {
					labels[k] = vStr
					updated = true
				}
			}
		}
	}
	if updated {
		return true
	}

	// Fallback: extract from prometheus_enricher evidence
	def, ok := aggregatedAlertLabelDefs[aggregationKey]
	if !ok {
		return false
	}
	matchValue := labels[def.matchKey]
	if matchValue == "" {
		return false
	}

	for _, e := range evidenceResponse {
		if e.Response == nil || e.ActionName != "prometheus_enricher" {
			continue
		}
		promResp, ok := e.Response.(playbooks.PrometheusActionResponse)
		if !ok {
			continue
		}
		topMetric := pickTopMatchingSeries(promResp.Data, def.matchKey, matchValue)
		if topMetric == nil {
			continue
		}
		for _, key := range def.labelsToExtract {
			if v, ok := topMetric[key].(string); ok {
				labels[key] = v
				updated = true
			}
		}
	}
	return updated
}

// isNonWorkloadDatastoreSubject reports whether subject_type names a logical
// datastore (e.g. a PostgreSQL database) rather than a k8s workload. Such
// subjects must never be name-matched against k8s_workloads: doing so re-pins
// the datastore onto an unrelated same-named workload (the G2 mis-match). Both
// the owner lookup in InvestigateEvent and linkK8sCloudResourceId skip them.
func isNonWorkloadDatastoreSubject(subjectType string) bool {
	switch strings.ToLower(subjectType) {
	case "database", "cloudsql_database":
		return true
	default:
		return false
	}
}

// linkK8sCloudResourceId resolves cloud_resource_id for Kubernetes-sourced
// events (kubernetes_api_server, prometheus, anomaly, slo, ...) from the cached
// k8s state snapshot. Cloud-provider events get their resource id from playbook
// evidence (linkCloudResourceId); K8s emitters never carry the UUID because it
// is assigned server-side at discovery time, so without this lookup the event's
// cloud_resource_id stays NULL.
//
// Resolution:
//   - workload-subject events (deployment/statefulset/daemonset/job/...):
//     match the workload by (tenant, account, namespace, subject_name).
//   - pod-subject events: prefer the owning workload (SubjectOwner, enriched
//     upstream in InvestigateEvent); fall back to the pod's own resource.
//   - node-subject events: match the node by (tenant, account, subject_name).
//
// Best-effort: a miss leaves cloud_resource_id NULL and never blocks ingestion.
// k8s_workloads.cloud_resource_id, k8s_pods.cloud_resource_id and
// k8s_nodes.cloud_resource_id are valid cloud_resourses.id values, so the
// events FK is satisfied.
func linkK8sCloudResourceId(sc *security.RequestContext, dbms *database.DatabaseManager, tenantId string, webhookEvent *Event) {
	if webhookEvent.CloudResourceId != "" || webhookEvent.AccountId == "" || tenantId == "" {
		return
	}
	// Nodes are cluster-scoped, so this runs before the namespace gate below. A
	// node event has no namespace of its own: an automation leaves it empty and
	// a prometheus alert carries the namespace of the exporter that reported
	// it, which would send the node's name to the k8s_workloads match. A node
	// is not a workload, so a miss stays unlinked rather than falling through.
	if strings.EqualFold(webhookEvent.SubjectType, "node") {
		if webhookEvent.SubjectName == "" {
			return
		}
		var crid string
		err := dbms.Db.QueryRowxContext(sc.GetContext(), nodeCloudResourceSQL, tenantId, webhookEvent.AccountId, webhookEvent.SubjectName).Scan(&crid)
		switch {
		case err == nil && crid != "":
			webhookEvent.CloudResourceId = crid
		case err != nil && err != sql.ErrNoRows:
			sc.GetLogger().Warn("link_k8s_cloud_resource: node lookup failed", "error", err, "node", webhookEvent.SubjectName)
		}
		return
	}
	// SubjectNamespace / SubjectName are empty on the struct for some emitters
	// (prometheus-rule bridges, raw alertmanager) that carry them only in
	// Labels. This runs before playbook execution, so Labels holds only
	// original webhook labels — no originalLabelKeys guard needed. namespaceKeys
	// / subjectKeys are the codebase's source of truth for these conventions.
	ns := webhookEvent.SubjectNamespace
	if ns == "" {
		for _, k := range namespaceKeys {
			if v := webhookEvent.Labels[k]; v != "" {
				ns = v
				break
			}
		}
	}
	if ns == "" {
		return
	}

	subjectName := webhookEvent.SubjectName
	if subjectName == "" {
		for _, k := range subjectKeys {
			if v := webhookEvent.Labels[k]; v != "" {
				subjectName = v
				break
			}
		}
	}

	// Determine the workload name to resolve against k8s_workloads.
	// A logical-database subject (e.g. a PostgreSQL datname) is not a k8s
	// workload; name-matching it against k8s_workloads is how a "database
	// nudgebee" alert lands on an unrelated same-named workload. A host-named DB
	// subject (RDS endpoint / postgres service DNS) never matches k8s_workloads
	// by name anyway, so skipping here changes nothing for it.
	if isNonWorkloadDatastoreSubject(webhookEvent.SubjectType) {
		return
	}

	var workloadName string
	switch strings.ToLower(webhookEvent.SubjectType) {
	case "", "pod":
		// Pod (or unspecified subject) → resolve via its owning workload.
		workloadName = webhookEvent.SubjectOwner
	case "configmap":
		// A ConfigMap is not a workload, and its name collides with one
		// constantly — charts routinely name a Deployment and its config
		// the same thing. Falling through to the name match below would
		// link an edit of `grafana` the ConfigMap onto `grafana` the
		// Deployment whether or not that Deployment reads it, which is
		// worse than no attribution: it asserts a causal link that may not
		// exist. Resolve the workloads that actually consume it instead.
		linkRelatedWorkloads(sc, dbms, configMapConsumersSQL, "configmap",
			ConfigMapConsumersLabel, tenantId, ns, subjectName, webhookEvent)
		return
	case "ingress":
		// Same trap, and this one has been firing all along: Ingress
		// changes are already emitted, so the name match below has been
		// attributing them by naming coincidence. It picks the same-named
		// workload when one exists and silently drops every other backend
		// — including, in the common case of one Ingress fronting several
		// services, the one whose traffic the change actually broke.
		linkRelatedWorkloads(sc, dbms, ingressBackendsSQL, "ingress",
			IngressBackendsLabel, tenantId, ns, subjectName, webhookEvent)
		return
	default:
		// Subject is itself a workload.
		workloadName = subjectName
	}

	if workloadName != "" {
		var crid string
		err := dbms.Db.QueryRowxContext(sc.GetContext(), workloadCloudResourceSQL, tenantId, webhookEvent.AccountId, ns, workloadName).Scan(&crid)
		switch {
		case err == nil && crid != "":
			webhookEvent.CloudResourceId = crid
			return
		case err != nil && err != sql.ErrNoRows:
			sc.GetLogger().Warn("link_k8s_cloud_resource: workload lookup failed", "error", err, "namespace", ns, "workload", workloadName)
		}
	}

	// Pod fallback: link the pod's own resource when the owner is unknown.
	if strings.EqualFold(webhookEvent.SubjectType, "pod") && subjectName != "" {
		var crid string
		err := dbms.Db.QueryRowxContext(sc.GetContext(), podCloudResourceSQL, tenantId, webhookEvent.AccountId, ns, subjectName).Scan(&crid)
		switch {
		case err == nil && crid != "":
			webhookEvent.CloudResourceId = crid
		case err != nil && err != sql.ErrNoRows:
			sc.GetLogger().Warn("link_k8s_cloud_resource: pod lookup failed", "error", err, "namespace", ns, "pod", subjectName)
		}
	}
}

// ConfigMapConsumersLabel carries the workloads that consume the changed
// ConfigMap, as "Kind/name" entries joined by ",". A ConfigMap edit is
// otherwise an orphan event: nothing in it says which application just had
// its configuration changed, which is the only reason anyone cares.
const ConfigMapConsumersLabel = "nb_configmap_consumers"

// IngressBackendsLabel carries the workloads the changed Ingress routes to,
// in the same shape. An Ingress name is not a workload name — routing is the
// only thing that says whose traffic just changed.
const IngressBackendsLabel = "nb_ingress_backends"

// maxRelatedWorkloadsLabelled bounds these labels. A ConfigMap read by
// hundreds of workloads (a cluster-wide CA bundle) or an Ingress fronting a
// large namespace would otherwise write an unbounded string into every event
// row.
const maxRelatedWorkloadsLabelled = 20

// relatedWorkload is a workload reached from the changed object through the
// knowledge graph — a ConfigMap's consumer, an Ingress's backend.
type relatedWorkload struct {
	Name string `db:"name"`
	Kind string `db:"kind"`
}

func (w relatedWorkload) String() string {
	if w.Kind == "" {
		return w.Name
	}
	return w.Kind + "/" + w.Name
}

// relatedWorkloads runs one of the graph traversals above. Best-effort: an
// empty result (graph not built yet, or the relationship created since the
// last build — the graph rebuilds hourly) leaves the event unattributed,
// which is the correct outcome. Guessing from names is what this exists to
// avoid.
func relatedWorkloads(sc *security.RequestContext, dbms *database.DatabaseManager,
	query, subjectKind, tenantId, accountId, namespace, name string) []relatedWorkload {

	rows, err := dbms.Db.QueryxContext(sc.GetContext(), query, tenantId, accountId, namespace, name)
	if err != nil {
		sc.GetLogger().Warn("link_k8s_cloud_resource: related workload lookup failed",
			"error", err, "subject_kind", subjectKind, "namespace", namespace, "name", name)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var related []relatedWorkload
	for rows.Next() {
		var w relatedWorkload
		if err := rows.StructScan(&w); err != nil {
			sc.GetLogger().Warn("link_k8s_cloud_resource: related workload scan failed",
				"error", err, "subject_kind", subjectKind)
			return nil
		}
		if w.Name != "" {
			related = append(related, w)
		}
	}
	if err := rows.Err(); err != nil {
		sc.GetLogger().Warn("link_k8s_cloud_resource: related workload rows failed",
			"error", err, "subject_kind", subjectKind)
		return nil
	}
	return related
}

// linkRelatedWorkloads attributes a change on a non-workload object to the
// workloads the graph connects it to. All of them are recorded in `label`;
// the resource link is only set when there is exactly one, because with
// several any single choice would be arbitrary and the label already carries
// the full set.
func linkRelatedWorkloads(sc *security.RequestContext, dbms *database.DatabaseManager,
	query, subjectKind, label, tenantId, namespace, name string, webhookEvent *Event) {

	if name == "" {
		return
	}
	related := relatedWorkloads(sc, dbms, query, subjectKind, tenantId, webhookEvent.AccountId, namespace, name)
	if len(related) == 0 {
		return
	}

	labelled := related
	suffix := ""
	if len(labelled) > maxRelatedWorkloadsLabelled {
		suffix = fmt.Sprintf(",+%d more", len(labelled)-maxRelatedWorkloadsLabelled)
		labelled = labelled[:maxRelatedWorkloadsLabelled]
	}
	names := make([]string, 0, len(labelled))
	for _, w := range labelled {
		names = append(names, w.String())
	}
	if webhookEvent.Labels == nil {
		webhookEvent.Labels = map[string]string{}
	}
	webhookEvent.Labels[label] = strings.Join(names, ",") + suffix

	if len(related) != 1 {
		return
	}
	only := related[0]
	webhookEvent.SubjectOwner = only.Name
	webhookEvent.SubjectOwnerKind = only.Kind

	var crid string
	err := dbms.Db.QueryRowxContext(sc.GetContext(), workloadCloudResourceSQL,
		tenantId, webhookEvent.AccountId, namespace, only.Name).Scan(&crid)
	switch {
	case err == nil && crid != "":
		webhookEvent.CloudResourceId = crid
	case err != nil && err != sql.ErrNoRows:
		sc.GetLogger().Warn("link_k8s_cloud_resource: related workload resource lookup failed",
			"error", err, "subject_kind", subjectKind, "namespace", namespace, "workload", only.Name)
	}
}

// linkCloudResourceId resolves the cloud_resource_id for an event by extracting
// resource_id/resource_arn from cloud_resource evidence and looking up the UUID
// in the cloud_resourses table.
func linkCloudResourceId(webhookEvent *Event, evidenceResponse []eventrule.PlaybookActionExecutionResponse) {
	if webhookEvent.CloudResourceId != "" || webhookEvent.AccountId == "" {
		return
	}
	for _, e := range evidenceResponse {
		if e.Response == nil {
			continue
		}
		if e.ActionName != "cloud_resource" && e.ActionName != "cloud_resources" {
			continue
		}
		labelExtractor, ok := e.Response.(playbooks.PlaybookActionResponseLabelExtractor)
		if !ok {
			continue
		}
		labels := labelExtractor.ExtractLabels()
		resourceId, _ := labels["resource_id"].(string)
		resourceArn, _ := labels["resource_arn"].(string)
		if resourceId == "" && resourceArn == "" {
			continue
		}
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return
		}
		var cloudResourceUUID string
		err = dbms.Db.QueryRowx(
			// Both the bulk sync and the realtime EventBridge path store the real ARN in
			// arn, so a resource that historically got a row from each matches twice here.
			// Order so a live row always wins over an archived one before LIMIT 1 picks,
			// otherwise the event can be attached to a tombstone at random.
			"SELECT id FROM cloud_resourses WHERE account = $1 AND (resourse_id = $2 OR arn = $3) "+
				"ORDER BY is_active DESC, last_seen DESC LIMIT 1",
			webhookEvent.AccountId, resourceId, resourceArn,
		).Scan(&cloudResourceUUID)
		if err == nil && cloudResourceUUID != "" {
			webhookEvent.CloudResourceId = cloudResourceUUID
		}
		return
	}
}

// pickTopMatchingSeries finds the series whose metric[matchKey]==matchValue
// and has the highest value, returning its metric labels.
func pickTopMatchingSeries(data map[string]any, matchKey, matchValue string) map[string]any {
	var seriesList []any

	// Direct series_list_result / vector_result (extractSeriesResponse mode)
	seriesList, _ = data["series_list_result"].([]any)
	if len(seriesList) == 0 {
		seriesList, _ = data["vector_result"].([]any)
	}
	// Wrapped under key "A"
	if len(seriesList) == 0 {
		if qr, ok := data["A"]; ok {
			switch q := qr.(type) {
			case []any:
				seriesList = q
			case map[string]any:
				seriesList, _ = q["series_list_result"].([]any)
				if len(seriesList) == 0 {
					seriesList, _ = q["vector_result"].([]any)
				}
			}
		}
	}
	if len(seriesList) == 0 {
		return nil
	}

	var topMetric map[string]any
	topValue := -1.0

	for _, item := range seriesList {
		series, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric, ok := series["metric"].(map[string]any)
		if !ok {
			continue
		}
		if mv, _ := metric[matchKey].(string); mv != matchValue {
			continue
		}

		// Instant query: value is [timestamp, "value_string"]
		if value, ok := series["value"].([]any); ok && len(value) >= 2 {
			if vStr, ok := value[1].(string); ok {
				if v, err := strconv.ParseFloat(vStr, 64); err == nil && v > topValue {
					topValue = v
					topMetric = metric
				}
			}
		}
		// Range query: values can be flat strings or nested [timestamp, value] pairs
		if values, ok := series["values"].([]any); ok && len(values) > 0 {
			lastVal := values[len(values)-1]
			var vStr string
			switch lv := lastVal.(type) {
			case string:
				vStr = lv
			case []any:
				if len(lv) >= 2 {
					vStr, _ = lv[1].(string)
				}
			}
			if vStr != "" {
				if v, err := strconv.ParseFloat(vStr, 64); err == nil && v > topValue {
					topValue = v
					topMetric = metric
				}
			}
		}
	}
	return topMetric
}

func processTriage(ctx *security.RequestContext, newEvent map[string]any) error {
	eventId, ok := newEvent["id"].(string)
	if !ok || eventId == "" {
		ctx.GetLogger().Error("triage: event ID not found")
		return nil
	}

	// Fetch the full event from database
	event, err := GetEvent(ctx, eventId)
	if err != nil {
		ctx.GetLogger().Error("triage: failed to fetch event", "error", err, "event_id", eventId)
		return nil // Don't fail the entire pipeline
	}

	// Get database connection
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("triage: failed to get database manager", "error", err)
		return nil
	}

	// Process triage
	err = triage.ProcessEvent(ctx.GetContext(), dbms.Db, &event)
	if err != nil {
		ctx.GetLogger().Error("triage: processing failed", "error", err, "event_id", eventId)
		return nil // Don't fail the entire pipeline
	}

	// Propagate post-triage classification back into newEvent so downstream
	// processors (notably llm.ProcessEvent's suppressed-skip gate) AND event-trigger
	// workflows see what triage just applied. triage.ProcessEvent issues SQL UPDATEs
	// without mutating the in-memory event struct, so we re-read here. Combined with
	// PostProcessEvent running triage before other processors, this closes the
	// same-batch race where llm could publish before triage flipped nb_status to
	// SUPPRESSED/DROPPED.
	//
	// computed_score / computed_priority are carried so event-trigger workflows can
	// filter on the triage severity (e.g. {{ event.computed_priority in ['P0','P1'] }}
	// or {{ event.computed_score | int >= 80 }}). They are the ONLY triage-severity
	// signal a workflow filter can reach — the event payload published to the runbook
	// exchange is exactly this map, and these keys were previously absent, which forced
	// authors to invent non-existent labels like event.labels.nb_triage. They may be
	// null for un-scored / legacy events, so the keys are set only when present.
	var triageClassification struct {
		NbStatus         sql.NullString `db:"nb_status"`
		ComputedScore    sql.NullInt64  `db:"computed_score"`
		ComputedPriority sql.NullString `db:"computed_priority"`
	}
	if err := dbms.Db.GetContext(ctx.GetContext(), &triageClassification,
		`SELECT nb_status, computed_score, computed_priority FROM events WHERE id = $1`, eventId); err != nil {
		ctx.GetLogger().Warn("triage: failed to refresh post-triage classification", "error", err, "event_id", eventId)
	} else {
		if triageClassification.NbStatus.Valid {
			newEvent["nb_status"] = triageClassification.NbStatus.String
		}
		if triageClassification.ComputedScore.Valid {
			newEvent["computed_score"] = triageClassification.ComputedScore.Int64
		}
		if triageClassification.ComputedPriority.Valid {
			newEvent["computed_priority"] = triageClassification.ComputedPriority.String
		}
	}

	return nil
}

func processPagerDutyComment(ctx *security.RequestContext, newEvent map[string]any) error {
	tenantId, ok := newEvent["tenant"].(string)
	if !ok || tenantId == "" {
		ctx.GetLogger().Error("pagerduty_comment: tenant ID not found")
		return nil
	}

	source, ok := newEvent["source"].(string)
	if !ok || source != "pagerduty_webhook" {
		return nil
	}

	eventId, ok := newEvent["id"].(string)
	if !ok {
		ctx.GetLogger().Error("pagerduty_comment: event ID not found")
		return nil
	}

	findingId, ok := newEvent["finding_id"].(string)
	if !ok {
		ctx.GetLogger().Error("pagerduty_comment: finding ID not found")
		return nil
	}

	accountId, ok := newEvent["cloud_account_id"].(string)
	if !ok || accountId == "" {
		ctx.GetLogger().Error("pagerduty_comment: account ID not found")
		return nil
	}

	// Ensure security context has the tenant ID for downstream queries
	tenantCtx := security.NewRequestContext(
		ctx.GetContext(),
		security.NewSecurityContextForTenantAdmin(tenantId),
		ctx.GetLogger(), nil, nil,
	)

	// Check if allow_comments is enabled in PagerDuty integration config
	configs, err := integrationcore.ListIntegrationConfigs(tenantCtx, accountId, "pagerduty")
	if err != nil {
		ctx.GetLogger().Error("pagerduty_comment: failed to get integration configs", "error", err)
		return nil
	}

	allowComments := false
	for _, cfg := range configs {
		for _, v := range cfg.Configs {
			if v.Name == "allow_comments" && v.Value == "true" {
				allowComments = true
				break
			}
		}
		if allowComments {
			break
		}
	}
	if !allowComments {
		return nil
	}

	comment := fmt.Sprintf("To accelerate your investigation, Nudgebee has prepared an initial evidence report for you: %s/investigate?id=%s", config.Config.BaseUrl, eventId)

	ctx.GetLogger().Info("pagerduty_comment: adding comment to PagerDuty incident",
		"event_id", eventId,
		"incident_id", findingId,
		"account_id", accountId,
		"tenant_id", tenantId)

	if err := postIncidentComment(tenantId, accountId, "pagerduty", findingId, comment); err != nil {
		ctx.GetLogger().Error("pagerduty_comment: failed to add comment", "error", err)
		return err
	}

	ctx.GetLogger().Info("pagerduty_comment: successfully added comment to PagerDuty incident")
	return nil
}

// init registers this package's in-process lifecycle hooks. triage and llm are
// NOT hooks — they are pipeline steps PostProcessEvent runs directly, in order,
// to PRODUCE the lifecycle phases (triage sets nb_status; llm enqueues the
// investigation). See package event/lifecycle.
func init() {
	// notification fires at creation and is never gated on the LLM — the
	// primary alert must be instant and reliable (augment, don't gate).
	lifecycle.RegisterLifecycleHook(lifecycle.PhaseEventCreated, "notification", notification.ProcessEvent)

	// pagerduty_comment posts a "report ready" deep link. For an investigated
	// event that link is only meaningful once the report exists, so post it at
	// investigation.completed; for a non-investigated event there is no
	// investigation to wait for, so post at creation.
	lifecycle.RegisterLifecycleHook(lifecycle.PhaseEventCreated, "pagerduty_comment", pagerdutyCommentIfNotInvestigated)
	lifecycle.RegisterLifecycleHook(lifecycle.PhaseInvestigationCompleted, "pagerduty_comment", processPagerDutyComment)

	// rca_writeback posts the completed investigation analysis back onto the
	// source incident (ZenDuty / PagerDuty). Like pagerduty_comment it fires at
	// investigation.completed — this is the api-server-side trigger that replaced
	// llm-server's former post-RCA signal. It self-gates on source/tenant/severity.
	lifecycle.RegisterLifecycleHook(lifecycle.PhaseInvestigationCompleted, "rca_writeback", rcaWritebackHook)

	// workflow is intentionally NOT an in-process hook: lifecycle.Emit publishes
	// the event to the runbook event exchange for every workflow-eligible phase
	// (exactly what the old workflow processor did). Workflows subscribe to a
	// phase via their trigger's params.on (default event.created).
}

// pagerdutyCommentIfNotInvestigated runs processPagerDutyComment at creation
// only when no investigation was enqueued. When an investigation is in flight,
// the comment is posted later at investigation.completed (the deep-linked report
// is not ready yet). llm.ProcessEvent sets the enqueue marker, which
// PostProcessEvent runs before emitting event.created.
func pagerdutyCommentIfNotInvestigated(ctx *security.RequestContext, newEvent map[string]any) error {
	if enqueued, _ := newEvent[llm.EventInvestigationEnqueuedKey].(bool); enqueued {
		return nil
	}
	return processPagerDutyComment(ctx, newEvent)
}

// recordOnlyLabel marks an event the producer wants stored and counted but not
// acted on. The cloud collector sets it (providers/aws RecordOnlyLabel).
const recordOnlyLabel = "nb_record_only"

// recordOnlySources are the sources whose labels are written by our own cloud
// collector. Other sources carry labels the customer's tooling sets (a
// Prometheus rule, a webhook payload), and for them the label must not work as
// a way to stop notifications.
var recordOnlySources = map[string]bool{
	"AWS_CloudWatch_Alarm": true,
	"AWS_EventBridge":      true,
	"Azure_Monitor_Alert":  true,
	"GCP_Metric_Alert":     true,
}

// isRecordOnly reads recordOnlyLabel from the event's labels, in any of the
// shapes the post-process map carries them in, for an event from one of
// recordOnlySources.
func isRecordOnly(event map[string]any) bool {
	if source, _ := event["source"].(string); !recordOnlySources[source] {
		return false
	}
	switch labels := event["labels"].(type) {
	case map[string]any:
		v, _ := labels[recordOnlyLabel].(string)
		return v == "true"
	case map[string]string:
		return labels[recordOnlyLabel] == "true"
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(labels), &m) == nil {
			v, _ := m[recordOnlyLabel].(string)
			return v == "true"
		}
	}
	return false
}

// Pipeline-step / emit seams. PostProcessEvent calls these indirectly so the
// orchestration (triage → llm → event.created) is unit-testable without a live
// DB / MQ. Overridden in tests.
var (
	triageStep       = processTriage
	llmStep          = llm.ProcessEvent
	emitLifecycle    = lifecycle.Emit
	emitWorkflowOnly = lifecycle.PublishToWorkflows
)

func PostProcessEvent(ctx *security.RequestContext, newEvent map[string]any) {

	if newEvent["priority"] == nil {
		newEvent["priority"] = EventPriorityInfo
	}

	// use string as rest of the system is assuming this to be string
	if priorityE, ok := newEvent["priority"].(EventPriority); ok {
		newEvent["priority"] = string(priorityE)
	}

	// Register this event's aggregation_key in event_rules so it is selectable as
	// a workflow-trigger Event Type. Guarded + idempotent (no-op once registered).
	registerNativeEventTypeRule(ctx, newEvent)

	// triage runs first so its classification (nb_status etc.) is in newEvent
	// before any hook or workflow sees it: downstream gates such as
	// notification's priority/suppressed check and llm's suppressed-skip depend
	// on it. triage produces the classification carried by event.created.
	runStep(ctx, "triage", triageStep, newEvent)

	// A record-only event is history, not an alert: the cloud collector sends an
	// alarm firing that started and cleared between two of its polls this way.
	// Triage above has counted it (repeats, grouping, alert tuning); nothing
	// after this point acts on it — no investigation, no notification, no
	// workflow — since event.created below is the one place those fan out from.
	if isRecordOnly(newEvent) {
		ctx.GetLogger().Info("event: record-only, not investigated or notified",
			"event_id", newEvent["id"], "source", newEvent["source"], recordOnlyLabel, "true")
		return
	}

	// llm runs next: it decides eligibility and, when it enqueues an
	// investigation, sets llm.EventInvestigationEnqueuedKey. event.created
	// (below) observes that marker so pagerduty_comment defers to
	// investigation.completed for investigated events.
	runStep(ctx, "llm", llmStep, newEvent)

	// event.created: the new event is classified and ready. Fans out to the
	// created-phase hooks (notification, pagerduty-if-not-investigated) and
	// publishes to the runbook exchange for on=event.created workflows.
	emitLifecycle(ctx, lifecycle.PhaseEventCreated, newEvent, nil)
}

// ReEmitForWorkflows re-delivers an already-post-processed event to the runbook
// workflow exchange so event-trigger workflows fire on repeat occurrences. Unlike
// PostProcessEvent it deliberately skips triage/llm/notification — those ran on
// first insert and must not re-run on every re-fire — and only publishes for
// workflow matching (event.created phase). Invoked by the post-process consumer
// for workflow_only messages enqueued by InsertEvent on a FIRING re-fire.
func ReEmitForWorkflows(ctx *security.RequestContext, newEvent map[string]any) {
	emitWorkflowOnly(ctx, lifecycle.PhaseEventCreated, newEvent)
}

// runStep runs a single pipeline step (triage / llm), logging any error and
// continuing. These are not lifecycle hooks — they run directly and in order.
func runStep(ctx *security.RequestContext, name string, fn func(*security.RequestContext, map[string]any) error, newEvent map[string]any) {
	if err := fn(ctx, newEvent); err != nil {
		ctx.GetLogger().Error("Error processing event by", "processor", name, "error", err)
	}
}

func UpdateEvent(ctx *security.RequestContext, request models.UpdateEventRequest) (models.Event, error) {
	r, err := GetEvent(ctx, request.EventId)
	if err != nil {
		ctx.GetLogger().Error("services.UpdateEvent error getting events", "error", err)
		return models.Event{}, err
	}
	if r.CloudAccountId == nil || *r.CloudAccountId == "" {
		return models.Event{}, fmt.Errorf("event %s has no account association", request.EventId)
	}
	if !ctx.GetSecurityContext().HasAccountAccess(*r.CloudAccountId, security.SecurityAccessTypeUpdate) {
		return models.Event{}, common.ErrorUnauthorized("access denied for account: " + *r.CloudAccountId)
	}
	if request.Urgency != "" {
		r.Urgency = &request.Urgency
	}

	oldSubjectName := common.StrVal(r.SubjectName)
	subjectName := common.StrVal(r.SubjectName)
	subjectNamespace := common.StrVal(r.SubjectNamespace)
	subjectType := common.StrVal(r.SubjectType)

	if request.SubjectName != "" {
		subjectName = request.SubjectName
	}
	if request.SubjectNamespace != "" {
		subjectNamespace = request.SubjectNamespace
	}
	if request.SubjectType != "" {
		subjectType = request.SubjectType
	}

	now := time.Now().UTC()
	_, err1 := InsertEvent(Event{
		Urgency:          *r.Urgency,
		AccountId:        *r.CloudAccountId,
		FindingId:        *r.FindingId,
		Title:            r.Title,
		Description:      *r.Description,
		Source:           *r.Source,
		AggregationKey:   *r.AggregationKey,
		Priority:         EventPriority(*r.Priority),
		SubjectType:      subjectType,
		SubjectName:      subjectName,
		SubjectNamespace: subjectNamespace,
		SubjectNode:      common.StrVal(r.SubjectNode),
		ServiceKey:       *r.ServiceKey,
		Fingerprint:      *r.Fingerprint,
		Evidences:        r.Evidences.Array(),
		Tenant:           *r.Tenant,
		Status:           EventStatus(*r.Status),
		FindingType:      *r.FindingType,
		Cluster:          *r.Cluster,
		StartsAt:         r.StartsAt,
		EndsAt:           r.EndsAt,
		Category:         common.StrVal(r.Category),
		CreatedAt:        r.CreatedAt,
		CloudResourceId:  common.StrVal(r.CloudResourceId),
		Labels:           common.JsonToStringMap(r.Labels),
		UpdatedAt:        &now,
		SubjectOwner:     common.StrVal(r.SubjectOwner),
		SubjectOwnerKind: common.StrVal(r.SubjectOwnerKind),
		// A user metadata edit (urgency/subject) re-persists the row carrying its
		// existing FIRING status; that is NOT a fresh alert occurrence, so suppress
		// the event-trigger workflow re-fire to avoid spuriously running workflows.
	}, request.EventId, WithoutWorkflowRefire())

	if err1 != nil {
		return models.Event{}, fmt.Errorf("event: failed to insert event: %w", err1)
	}

	if oldSubjectName == "" && subjectName != "" {
		ctx.GetLogger().Info("event: subject_name resolved via update, triggering playbook re-run",
			"event_id", request.EventId, "new_subject_name", subjectName)
		go func() {
			if err := RefreshInvestigation(ctx, request.EventId); err != nil {
				ctx.GetLogger().Error("event: failed to refresh investigation after subject resolution",
					"event_id", request.EventId, "error", err)
			}
		}()
	}

	return r, nil
}

// manualStatusChangeReasons is the set of statuses the Events three-dot menu's
// Close/Open toggle accepts, and the event_history change_reason recorded for
// each so a later GetEventManualStatusChange lookup can tell a manual flip
// apart from an automated one (ingestion, webhook resolves, the inactive-
// resource sweep) touching the same status field.
var manualStatusChangeReasons = map[string]string{
	string(EventStatusClosed): "manual_close",
	string(EventStatusFiring): "manual_reopen",
}

// closeReasonCodes and reopenReasonCodes are the fixed reason vocabularies
// offered on the Close and Open dropdowns respectively — same reason_code/
// reason_text shape as event_classification, so a manual status change is
// reportable by cause rather than only readable as free text.
var closeReasonCodes = map[string]bool{
	"already_fixed_externally":   true,
	"false_alarm":                true,
	"duplicate_of_another_alert": true,
	"known_issue_wont_fix":       true,
	"other":                      true,
}

var reopenReasonCodes = map[string]bool{
	"issue_recurred":              true,
	"closed_by_mistake":           true,
	"needs_further_investigation": true,
	"other":                       true,
}

// UpdateEventStatusRequest manually opens or closes an event's alert status.
type UpdateEventStatusRequest struct {
	EventID string `json:"event_id" mapstructure:"event_id"`
	Status  string `json:"status" mapstructure:"status"`
	// ReasonCode is required: one of closeReasonCodes when Status is CLOSED, or
	// reopenReasonCodes when Status is FIRING.
	ReasonCode string `json:"reason_code" mapstructure:"reason_code"`
	// ReasonText is free-text elaboration. Required when ReasonCode is "other",
	// optional otherwise.
	ReasonText string `json:"reason_text" mapstructure:"reason_text"`
	// EvidenceUrl is always optional: it's a free-text URL, so requiring it
	// only proves the caller typed something, not that it's relevant.
	EvidenceUrl string `json:"evidence_url" mapstructure:"evidence_url"`
}

type UpdateEventStatusResponse struct {
	Success    bool   `json:"success"`
	PrevStatus string `json:"prev_status"`
	NewStatus  string `json:"new_status"`
	AccountId  string `json:"account_id"`
}

// UpdateEventStatus manually opens or closes an event (Troubleshoot > Events >
// three-dot menu > Close/Open). Unlike UpdateEvent, it writes status directly
// rather than round-tripping through InsertEvent: InsertEvent re-persists the
// full event content and is meant for metadata edits, while a status-only
// change here mirrors CloseEventsForInactiveResources's direct UPDATE +
// event_history audit row so a manual change is indistinguishable in the
// timeline from any other status change.
func UpdateEventStatus(ctx *security.RequestContext, request UpdateEventStatusRequest, userID string) (*UpdateEventStatusResponse, error) {
	changeReason, ok := manualStatusChangeReasons[request.Status]
	if !ok {
		return nil, fmt.Errorf("unsupported status: %s (only %s and %s are supported)", request.Status, EventStatusClosed, EventStatusFiring)
	}

	reasonCodes := closeReasonCodes
	if request.Status == string(EventStatusFiring) {
		reasonCodes = reopenReasonCodes
	}
	if !reasonCodes[request.ReasonCode] {
		return nil, fmt.Errorf("invalid reason_code: %s", request.ReasonCode)
	}
	if request.ReasonCode == "other" && strings.TrimSpace(request.ReasonText) == "" {
		return nil, fmt.Errorf("reason_text is required when reason_code is 'other'")
	}
	if utf8.RuneCountInString(request.ReasonText) > 500 {
		return nil, fmt.Errorf("reason_text must not exceed 500 characters")
	}
	// The frontend already enforces this, but this RPC action is a public write
	// boundary any client can call directly, bypassing that check.
	if evidenceURL := strings.TrimSpace(request.EvidenceUrl); evidenceURL != "" {
		if len(evidenceURL) > 2048 {
			return nil, fmt.Errorf("evidence_url must not exceed 2048 characters")
		}
		parsed, parseErr := url.Parse(evidenceURL)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("evidence_url must be a valid http or https URL")
		}
	}

	r, err := GetEvent(ctx, request.EventID)
	if err != nil {
		ctx.GetLogger().Error("services.UpdateEventStatus error getting event", "error", err)
		return nil, err
	}
	if r.CloudAccountId == nil || *r.CloudAccountId == "" {
		return nil, fmt.Errorf("event %s has no account association", request.EventID)
	}
	if !ctx.GetSecurityContext().HasAccountAccess(*r.CloudAccountId, security.SecurityAccessTypeUpdate) {
		return nil, common.ErrorUnauthorized("access denied for account: " + *r.CloudAccountId)
	}

	prevStatus := common.StrVal(r.Status)
	if prevStatus == request.Status {
		return &UpdateEventStatusResponse{Success: true, PrevStatus: prevStatus, NewStatus: request.Status, AccountId: *r.CloudAccountId}, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("event: failed to get db manager: %w", err)
	}

	var endsAt sql.NullTime
	if request.Status == string(EventStatusClosed) {
		endsAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	}

	_, err = dbms.Db.ExecContext(ctx.GetContext(),
		`UPDATE events SET status = $1, ends_at = $2, updated_at = now() AT TIME ZONE 'utc' WHERE id = $3 AND status <> $1`,
		request.Status, endsAt, request.EventID,
	)
	if err != nil {
		return nil, fmt.Errorf("event: failed to update event status: %w", err)
	}

	logger := ctx.GetLogger()
	tenant := common.StrVal(r.Tenant)
	accountId := *r.CloudAccountId

	// metadata carries the user-facing "why" alongside the fixed change_reason
	// marker: change_reason must stay exactly "manual_close"/"manual_reopen" for
	// GetEventManualStatusChanges' exact-match lookup below to keep working.
	metadata := map[string]any{"reason_code": request.ReasonCode}
	if reasonText := strings.TrimSpace(request.ReasonText); reasonText != "" {
		metadata["reason_text"] = reasonText
	}
	if evidenceUrl := strings.TrimSpace(request.EvidenceUrl); evidenceUrl != "" {
		metadata["evidence_url"] = evidenceUrl
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("event: failed to marshal status-change metadata: %w", err)
	}

	// Detach from the request context: this fire-and-forget write outlives the HTTP
	// request, and the request ctx is cancelled the moment the handler returns its
	// response (see the equivalent comment on triage.UpdateNBStatus).
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if histErr := insertEventStatusHistory(bgCtx, dbms.Db, request.EventID, tenant, accountId, prevStatus, request.Status, changeReason, userID, metadataJSON); histErr != nil {
			logger.Error("event: failed to record manual status-change history", "error", histErr, "event_id", request.EventID)
		}
	}()

	return &UpdateEventStatusResponse{Success: true, PrevStatus: prevStatus, NewStatus: request.Status, AccountId: accountId}, nil
}

// EventManualStatusChange is the most recent manual open/close of an event,
// surfaced as the "Closed by <user>" tooltip on the Alert Status badge.
type EventManualStatusChange struct {
	EventID       string    `json:"event_id"`
	ChangedByName string    `json:"changed_by_name"`
	Status        string    `json:"status"`
	ChangedAt     time.Time `json:"changed_at"`
	ReasonCode    string    `json:"reason_code"`
	ReasonText    string    `json:"reason_text"`
	EvidenceUrl   string    `json:"evidence_url"`
}

// GetEventManualStatusChanges batch-looks-up the most recent status-field
// entry in event_history for each of eventIDs, resolving the acting user's
// display name in the same query so the Events table can prefetch tooltip
// data for every currently-rendered "Closed" row in one round trip (instead
// of a per-row fetch-on-hover, which meant two sequential dependent requests
// — status lookup, then user lookup — before the tooltip could show anything).
// An event is included in the result only if its latest status-history row is
// a manual close (change_reason "manual_close", new_value "CLOSED"); an event
// whose current status came from ingestion, a webhook resolve, the inactive-
// resource sweep, or was never touched is simply absent from the result.
//
// Deliberately CLOSED-only, not "any manual change": callers only ever ask
// about events currently showing as CLOSED, but ingestion and webhook resolves
// write no history row on their own status flips, so the latest row can be a
// stale manual_reopen even though the event has since been closed again by
// one of those untracked paths (reopen, then an unrelated webhook resolves it
// again) — attributing that close to the old reopener would be wrong. Pinning
// the check to the "CLOSED" value, not just "some manual reason", is what
// rules that out. Taking only the LATEST row (not the latest CLOSED one) is
// what keeps this correct across repeated toggles: close, reopen, close again
// correctly attributes to the third close, not the first.
func GetEventManualStatusChanges(ctx *security.RequestContext, eventIDs []string) ([]EventManualStatusChange, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("event: failed to get db manager: %w", err)
	}

	var rows []struct {
		EventID        string         `db:"event_id"`
		CloudAccountId string         `db:"cloud_account_id"`
		ChangedBy      sql.NullString `db:"changed_by"`
		ChangedByName  sql.NullString `db:"changed_by_name"`
		NewValue       []byte         `db:"new_value"`
		ChangeReason   string         `db:"change_reason"`
		ChangedAt      time.Time      `db:"changed_at"`
		Metadata       []byte         `db:"metadata"`
	}
	err = dbms.Db.SelectContext(ctx.GetContext(), &rows,
		`SELECT DISTINCT ON (eh.event_id)
			eh.event_id, eh.cloud_account_id, eh.changed_by, eh.new_value, eh.change_reason, eh.changed_at,
			COALESCE(eh.metadata, '{}'::jsonb) AS metadata,
			COALESCE(u.display_name, u.username) AS changed_by_name
		 FROM event_history eh
		 LEFT JOIN users u ON u.id = eh.changed_by
		 WHERE eh.event_id = ANY($1) AND eh.change_type = 'status'
		 ORDER BY eh.event_id, eh.changed_at DESC`,
		pq.Array(eventIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("event: failed to look up status history for %d events: %w", len(eventIDs), err)
	}

	results := make([]EventManualStatusChange, 0, len(rows))
	for _, row := range rows {
		// Callers pass event ids from their own already-authorized events_list
		// fetch, but nothing stops a direct RPC call naming an id from an
		// account the caller can't see — event_history carries no row-level
		// scoping of its own, so this is the only check standing between an
		// arbitrary event id and another account's "who closed this" data.
		if !ctx.GetSecurityContext().HasAccountAccess(row.CloudAccountId, security.SecurityAccessTypeRead) {
			continue
		}
		status := stringFromJSON(row.NewValue)
		if !row.ChangedBy.Valid || status != string(EventStatusClosed) || row.ChangeReason != manualStatusChangeReasons[string(EventStatusClosed)] {
			continue
		}
		var meta struct {
			ReasonCode  string `json:"reason_code"`
			ReasonText  string `json:"reason_text"`
			EvidenceUrl string `json:"evidence_url"`
		}
		_ = json.Unmarshal(row.Metadata, &meta) // pre-feature rows have no metadata; leave fields empty
		results = append(results, EventManualStatusChange{
			EventID:       row.EventID,
			ChangedByName: row.ChangedByName.String,
			Status:        status,
			ChangedAt:     row.ChangedAt,
			ReasonCode:    meta.ReasonCode,
			ReasonText:    meta.ReasonText,
			EvidenceUrl:   meta.EvidenceUrl,
		})
	}
	return results, nil
}

// insertEventStatusHistory records a manual status transition. Unlike
// InsertEventHistory, it always writes a fresh row with a random id rather
// than one derived from (event, change_type, old, new): the Close/Open toggle
// legitimately repeats the same old->new pair (close, reopen, close again),
// and InsertEventHistory's fingerprint-derived id would collide on the
// second occurrence, silently dropping it via its ON CONFLICT DO NOTHING
// dedup. triage.logStatusChangeToHistory (nb_status, equally repeatable)
// uses the same random-id approach for the same reason.
func insertEventStatusHistory(ctx context.Context, db DBExecutor, eventID, tenantID, cloudAccountID, oldStatus, newStatus, changeReason, changedBy string, metadata []byte) error {
	oldJSON, err := json.Marshal(oldStatus)
	if err != nil {
		return err
	}
	newJSON, err := json.Marshal(newStatus)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason, changed_by, metadata)
		 VALUES ($1, $2, $3, $4, 'status', $5, $6, $7, $8, $9)`,
		uuid.New().String(), eventID, tenantID, cloudAccountID, oldJSON, newJSON, changeReason,
		sql.NullString{String: changedBy, Valid: changedBy != ""}, metadata,
	)
	return err
}

// stringFromJSON unmarshals a jsonb column known to hold a plain JSON string
// (as insertEventStatusHistory writes it), returning "" for anything else.
func stringFromJSON(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// evidenceSourceWorkflowKey is the additional_info field carrying the automation
// run that produced an evidence element. Written by AddEvidence, read by
// RefreshInvestigation and by the Investigate page.
const evidenceSourceWorkflowKey = "source_workflow"

// AddEvidence appends evidences to an event that already exists.
//
// This exists because InvestigateEvent's duplicate branch deliberately refuses
// to touch evidences ("playbook-collected evidences would be lost"), so a
// workflow that wants to attach diagnostic output to the alarm event it was
// triggered by has no way in. Storing a fresh event instead would split one
// incident across two rows.
//
// The append is done in SQL rather than read-modify-write in Go, and that is
// load-bearing rather than stylistic: the playbook enricher pipeline writes
// this same jsonb column via RefreshInvestigation, and both run within seconds
// of an event being created. A read, merge and full-column overwrite would let
// whichever transaction commits last silently discard the other's evidence.
// `evidences || $1::jsonb` is a single atomic statement, so concurrent appends
// both survive.
//
// There is no read-side duplicate suppression to rely on: dedupeEvidencesByContent
// runs only inside RefreshInvestigation, and the Investigate page reads this column
// straight through GraphQL. So a stamped append collapses its own history here —
// see the `source` handling below.
//
// `source` identifies the automation run that produced these evidences. When set,
// each element is stamped with it under `additional_info.source_workflow`. The
// stamp is injected here rather than trusted from the caller for two reasons: a
// workflow author hand-writing evidence objects cannot omit or forge it, and
// RefreshInvestigation uses it to tell evidence it must preserve from evidence it
// regenerates itself.
func AddEvidence(ctx *security.RequestContext, eventId string, evidences []any, source *models.EvidenceSourceWorkflow) error {
	if eventId == "" {
		return fmt.Errorf("event: event_id is required")
	}
	if len(evidences) == 0 {
		return fmt.Errorf("event: at least one evidence is required")
	}

	existing, err := GetEvent(ctx, eventId)
	if err != nil {
		return err
	}
	if existing.CloudAccountId == nil || *existing.CloudAccountId == "" {
		return fmt.Errorf("event %s has no account association", eventId)
	}
	if !ctx.GetSecurityContext().HasAccountAccess(*existing.CloudAccountId, security.SecurityAccessTypeUpdate) {
		return common.ErrorUnauthorized("access denied for account: " + *existing.CloudAccountId)
	}

	if source != nil && source.WorkflowID != "" {
		stampEvidencesWithSourceWorkflow(evidences, source)
	}

	evidencesJson, err := common.MarshalJson(evidences)
	if err != nil {
		return fmt.Errorf("event: failed to marshal evidences: %w", err)
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Two distinct empty states have to be normalised before concatenating.
	// A SQL NULL would make the whole expression NULL. A jsonb 'null' would
	// not — `'null'::jsonb || '[{...}]'::jsonb` yields `[null, {...}]`, which
	// keeps the evidence but leaves a junk null element in the array for every
	// consumer to skip. jsonb_typeof covers both, plus any non-array value.
	var res sql.Result
	if source != nil && source.WorkflowID != "" {
		// A re-running automation replaces its own previous output instead of
		// stacking another copy of the same cards on the event — the same
		// "regenerate replaces" rule the playbook enrichers follow. Without it an
		// alert that re-fires daily grows this row per run, and `evidences` is a
		// TOASTed column already holding 150KB-900KB blobs.
		//
		// The replacement is scoped to (workflow, task), not to the workflow alone.
		// Two tasks in one automation each attaching evidence is a real shape — the
		// pprof collector captures two heap profiles and attaches both — and keying
		// on the workflow alone made the second attach delete the first.
		//
		// A legacy element stamped before task_id existed carries none, and is
		// treated as the current task's to replace: it is a straggler from the same
		// automation, and leaving it would strand a copy nothing can ever clean up.
		//
		// Both predicates are IS DISTINCT FROM rather than <>/=, so an element with
		// no source_workflow at all (every enricher-produced card) compares as
		// "different" and is kept. A NULL-propagating form here would drop them all.
		//
		// Still one statement, so the atomicity argument above holds: the filter
		// and the append are evaluated against the same row version under a single
		// row lock. A concurrent append from another workflow cannot be lost
		// between them.
		res, err = dbms.Db.Exec(
			`UPDATE events
			    SET evidences = (
			            SELECT coalesce(jsonb_agg(elem), '[]'::jsonb)
			              FROM jsonb_array_elements(
			                     CASE WHEN jsonb_typeof(evidences) = 'array'
			                          THEN evidences ELSE '[]'::jsonb
			                     END) AS elem
			             WHERE elem->'additional_info'->'source_workflow'->>'workflow_id'
			                   IS DISTINCT FROM $4
			                OR coalesce(
			                     elem->'additional_info'->'source_workflow'->>'task_id',
			                     $5) IS DISTINCT FROM $5
			        ) || $1::jsonb,
			        updated_at = $2
			  WHERE id = $3`,
			string(evidencesJson), time.Now().UTC(), eventId, source.WorkflowID, source.TaskID)
	} else {
		res, err = dbms.Db.Exec(
			`UPDATE events
			    SET evidences = CASE
			            WHEN jsonb_typeof(evidences) = 'array' THEN evidences
			            ELSE '[]'::jsonb
			        END || $1::jsonb,
			        updated_at = $2
			  WHERE id = $3`,
			string(evidencesJson), time.Now().UTC(), eventId)
	}
	if err != nil {
		return fmt.Errorf("event: failed to append evidences: %w", err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return fmt.Errorf("event %s not found", eventId)
	}

	slog.Info("event: appended evidences", "event_id", eventId, "count", len(evidences))
	return nil
}

// stampEvidencesWithSourceWorkflow writes the producing automation onto each
// evidence element, creating the additional_info bag when the caller did not
// supply one. Elements that are not JSON objects are left alone — they cannot
// carry a stamp, and dropping them here would lose evidence.
func stampEvidencesWithSourceWorkflow(evidences []any, source *models.EvidenceSourceWorkflow) {
	for _, evidence := range evidences {
		evidenceMap, ok := evidence.(map[string]any)
		if !ok {
			continue
		}
		// The nil check is not redundant with ok: a JSON `null` fails the assertion
		// (ok=false) and takes this path anyway, but a Go caller passing a typed nil
		// map passes the assertion with a nil map, and writing to that panics.
		additionalInfo, ok := evidenceMap["additional_info"].(map[string]any)
		if !ok || additionalInfo == nil {
			additionalInfo = map[string]any{}
			evidenceMap["additional_info"] = additionalInfo
		}
		// Overwrites any caller-supplied value: the server is the authority on
		// which run this is.
		stamp := map[string]any{
			"workflow_id":   source.WorkflowID,
			"workflow_name": source.WorkflowName,
			"execution_id":  source.ExecutionID,
		}
		// Omitted rather than written empty when the caller has no task to name,
		// so the replace filter's coalesce() treats it as a legacy element.
		if source.TaskID != "" {
			stamp["task_id"] = source.TaskID
		}
		additionalInfo[evidenceSourceWorkflowKey] = stamp
	}
}
