package security

import (
	"errors"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"slices"
	"strings"

	"github.com/samber/lo"
)

type k8sPermission struct {
	Subject       string
	Permissions   []string
	Resource      string
	ResourceNames []string
	Namespace     string
	RuleType      string
	RuleName      string
}

type K8sRbacSubjectType string

const (
	K8sRbacSubjectTypeUser           K8sRbacSubjectType = "User"
	K8sRbacSubjectTypeGroup          K8sRbacSubjectType = "Group"
	K8sRbacSubjectTypeServiceAccount K8sRbacSubjectType = "ServiceAccount"
)

type K8sRbacPermissionType string

const (
	K8sRbacPermissionTypeGet    K8sRbacPermissionType = "get"
	K8sRbacPermissionTypeList   K8sRbacPermissionType = "list"
	K8sRbacPermissionTypeWatch  K8sRbacPermissionType = "watch"
	K8sRbacPermissionTypeCreate K8sRbacPermissionType = "create"
	K8sRbacPermissionTypeUpdate K8sRbacPermissionType = "update"
	K8sRbacPermissionTypePatch  K8sRbacPermissionType = "patch"
	K8sRbacPermissionTypeDelete K8sRbacPermissionType = "delete"
)

const (
	K8sObjectDeployments  = "deployments"
	K8sObjectDaemonsets   = "daemonsets"
	K8sObjectStatefulsets = "statefulsets"
	K8sObjectJobs         = "jobs"
	K8sObjectReplicaSets  = "replicasets"
	K8sObjectCronJobs     = "cronjobs"
	K8sObjectNamespaces   = "namespaces"
	K8sObjectNodes        = "nodes"
	K8sObjectPods         = "pods"
	K8sObjectConfigMaps   = "configmaps"
)

var k8sSupportedListObjects = []string{K8sObjectDeployments, K8sObjectDaemonsets, K8sObjectStatefulsets, K8sObjectJobs, K8sObjectReplicaSets, K8sObjectCronJobs, K8sObjectPods, K8sObjectConfigMaps, K8sObjectNamespaces, K8sObjectNodes}

const k8sRBACCacheNamespace = "k8s_rbac"

func init() {
	common.CacheCreateNamespace(k8sRBACCacheNamespace)
}

func getRoleAndBindings(accountId string) (map[string]any, error) {
	if data, ok := common.CacheGet(k8sRBACCacheNamespace, accountId+":rolebindings"); ok {
		decodedData := map[string]any{}
		err := common.UnmarshalJson(data, &decodedData)
		if err != nil {
			slog.Error("Error decoding rolebindings", "error", err, "accountId", accountId)
		} else {
			return decodedData, nil
		}
	}
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			ActionName: "get_resource",
			AccountID:  accountId,
			ActionParams: map[string]any{
				"group":          "rbac.authorization.k8s.io",
				"version":        "v1",
				"resource_type":  "roles,rolebindings",
				"all_namespaces": true,
			},
		},
	})
	if err != nil {
		slog.Error("Error getting role and bindings", "error", err, "accountId", accountId)
		return map[string]any{}, err
	}
	if _, ok := resp["data"]; !ok {
		return map[string]any{}, errors.New("RoleBinding not found")
	}

	if v, ok := resp["data"].(map[string]any)["findings"]; ok {
		findings := v.([]any)
		if len(findings) > 0 {
			if evidenceRaw, ok := findings[0].(map[string]any)["evidence"]; ok {
				evidence := evidenceRaw.([]any)
				if len(evidence) > 0 {
					evidenceDataRaw := evidence[0].(map[string]any)["data"]
					evidenceDataRawBytes := []byte(evidenceDataRaw.(string))
					data := []map[string]any{}
					err := common.UnmarshalJson(evidenceDataRawBytes, &data)
					if err != nil {
						return nil, err
					}
					roleBindings := map[string]any{
						"items": []any{},
					}
					if len(data) == 1 {
						data2 := data[0]["data"]
						data2Bytes := []byte(data2.(string))
						data2M := []any{}
						err := common.UnmarshalJson(data2Bytes, &data2M)
						if err != nil {
							return nil, err
						}
						roleBindings = map[string]any{
							"items": data2M,
						}
					}
					marshaledData, err := common.MarshalJson(roleBindings)
					if err == nil {
						err = common.CacheSet(k8sRBACCacheNamespace, accountId+":rolebindings", marshaledData)
						if err != nil {
							slog.Error("Error caching rolebindings", "error", err, "accountId", accountId)
						}
					} else {
						slog.Error("Error marshalling rolebindings", "error", err, "accountId", accountId)
					}
					return roleBindings, nil
				}
			}
		}
	}
	return map[string]any{}, errors.New("RoleBinding not found")
}

func getClusterRoleAndBindings(accountId string) (map[string]any, error) {
	if data, ok := common.CacheGet(k8sRBACCacheNamespace, accountId+":clusterrolebindings"); ok {
		decodedData := map[string]any{}
		err := common.UnmarshalJson(data, &decodedData)
		if err != nil {
			slog.Error("Error decoding rolebindings", "error", err, "accountId", accountId)
		} else {
			return decodedData, nil
		}
	}
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			ActionName: "get_resource",
			AccountID:  accountId,
			ActionParams: map[string]any{
				"group":          "rbac.authorization.k8s.io",
				"version":        "v1",
				"resource_type":  "clusterroles,clusterrolebindings",
				"all_namespaces": true,
			},
		},
	})
	if err != nil {
		slog.Error("Error getting cluster role and bindings", "error", err, "accountId", accountId)
		return map[string]any{}, err
	}
	if _, ok := resp["data"]; !ok {
		return map[string]any{}, errors.New("ClusterRoleBinding not found")
	}

	if v, ok := resp["data"].(map[string]any)["findings"]; ok {
		findings := v.([]any)
		if len(findings) > 0 {
			if evidenceRaw, ok := findings[0].(map[string]any)["evidence"]; ok {
				evidence := evidenceRaw.([]any)
				if len(evidence) > 0 {
					evidenceDataRaw := evidence[0].(map[string]any)["data"]
					evidenceDataRawBytes := []byte(evidenceDataRaw.(string))
					data := []map[string]any{}
					err := common.UnmarshalJson(evidenceDataRawBytes, &data)
					if err != nil {
						return nil, err
					}
					roleBindings := map[string]any{
						"items": []any{},
					}
					if len(data) == 1 {
						data2 := data[0]["data"]
						data2Bytes := []byte(data2.(string))
						data2M := []any{}
						err := common.UnmarshalJson(data2Bytes, &data2M)
						if err != nil {
							return nil, err
						}
						roleBindings = map[string]any{
							"items": data2M,
						}
					}
					marshaledData, err := common.MarshalJson(roleBindings)
					if err == nil {
						err = common.CacheSet(k8sRBACCacheNamespace, accountId+":clusterrolebindings", marshaledData)
						if err != nil {
							slog.Error("Error caching rolebindings", "error", err, "accountId", accountId)
						}
					} else {
						slog.Error("Error marshalling rolebindings", "error", err, "accountId", accountId)
					}
					return roleBindings, nil
				}
			}
		}
	}
	return map[string]any{}, errors.New("ClusterRoleBinding not found")
}

func getUserGroupMappings(accountId string) (map[string][]string, error) {
	if data, ok := common.CacheGet(k8sRBACCacheNamespace, accountId+":usergroups"); ok {
		decodedData := map[string][]string{}
		err := common.UnmarshalJson(data, &decodedData)
		if err != nil {
			slog.Error("Error decoding userGroups", "error", err, "accountId", accountId)
		} else {
			return decodedData, nil
		}
	}

	//TODO populate groups from usergroups

	return nil, nil
}

func listK8sResources(accountId string, resourceType string) ([]string, error) {
	resourceType = strings.ToLower(resourceType)
	if !slices.Contains(k8sSupportedListObjects, resourceType) {
		return []string{}, errors.New("resourceType not supported - " + resourceType)
	}

	if data, ok := common.CacheGet(k8sRBACCacheNamespace, accountId+":"+strings.ToLower(resourceType)); ok {
		decodedData := []string{}
		err := common.UnmarshalJson(data, &decodedData)
		if err != nil {
			slog.Error("Error decoding k8s resource data", "error", err, "accountId", accountId)
		} else {
			return decodedData, nil
		}
	}

	// get list of objects
	var query string
	params := []any{}
	switch resourceType {
	case "namespaces":
		query = "select distinct name from k8s_namespaces where cloud_account_id = $1 and is_active = true"
		params = append(params, accountId)
	case "nodes":
		query = "select distinct name from k8s_nodes where cloud_account_id = $1 and is_active = true"
		params = append(params, accountId)
	case "pods":
		query = "select distinct concat(namespace, '/', name) from k8s_pods where cloud_account_id = $1 and is_active = true"
		params = append(params, accountId)
	default:
		query = "select distinct concat(namespace, '/', name) from k8s_workloads where cloud_account_id = $1 and is_active = true and lower(kind) = lower($2)"
		switch resourceType {
		case "deployments":
			params = append(params, accountId, "deployment")
		case "daemonsets":
			params = append(params, accountId, "daemonset")
		case "statefulsets":
			params = append(params, accountId, "statefulset")
		case "replicasets":
			params = append(params, accountId, "replicaset")
		case "jobs":
			params = append(params, accountId, "job")
		default:
			return []string{}, errors.New("resourceType not supported - " + resourceType)
		}
	}

	// execute query
	dm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return []string{}, err
	}
	rows, err := dm.Db.Queryx(query, params...)
	if err != nil {
		return []string{}, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("Error closing rows", "error", err)
		}
	}()

	allObjects := []string{}
	for rows.Next() {
		var object string
		err = rows.Scan(&object)
		if err != nil {
			return []string{}, err
		}
		allObjects = append(allObjects, object)
	}

	allObjectData, err := common.MarshalJson(allObjects)
	if err == nil {
		err = common.CacheSet(k8sRBACCacheNamespace, accountId+":"+strings.ToLower(resourceType), allObjectData)
		if err != nil {
			slog.Error("Error caching k8s resource data", "error", err, "accountId", accountId)
		}
	} else {
		slog.Error("Error marshalling k8s resource data", "error", err, "accountId", accountId)

	}

	return allObjects, nil

}

func k8sListResourceNames(securityContext *SecurityContext, accountId string, subjectType K8sRbacSubjectType, subject string, resourceType string, permission K8sRbacPermissionType) ([]string, error) {
	if subjectType == "" || subject == "" || resourceType == "" {
		return []string{}, errors.New("subjectType/subject/resourceType is required")
	}
	if !slices.Contains(k8sSupportedListObjects, strings.ToLower(resourceType)) {
		return []string{}, errors.New("resourceType not supported - " + resourceType)
	}
	// get list of objects
	allObjects, err := listK8sResources(accountId, resourceType)
	if err != nil {
		return []string{}, err
	}

	perms, err := k8sGetPermissions(securityContext, accountId, subjectType, subject, resourceType, allObjects)
	if err != nil {
		return []string{}, err
	}

	allObjects = lo.Filter(allObjects, func(s string, i int) bool {
		if perm, ok := perms[s]; ok {
			return slices.Contains(perm, permission)
		}
		return false
	})
	return allObjects, nil
}

func k8sGetPermissions(securityContext *SecurityContext, accountId string, subjectType K8sRbacSubjectType, subject string, resourceType string, resourceNames []string) (map[string][]K8sRbacPermissionType, error) {
	// subject refs
	if subjectType == "" || subject == "" || resourceType == "" || len(resourceNames) == 0 {
		return map[string][]K8sRbacPermissionType{}, errors.New("subjectType/subject/objectType/object is required")
	}
	// Use a map for O(1) subject lookups instead of O(n) slices.Contains scans.
	// In large K8s clusters with thousands of role/cluster-role bindings, each
	// binding's subjects are checked against this set, so map lookup eliminates
	// a significant amount of repeated linear scanning.
	subjectFq := map[string]struct{}{
		string(subjectType) + "::" + subject: {},
	}
	if subjectType == K8sRbacSubjectTypeUser {
		_, groups := securityContext.GetK8sUserAndGroup(accountId)
		if groups == nil {
			groupMappings, err := getUserGroupMappings(accountId)
			if err != nil {
				return map[string][]K8sRbacPermissionType{}, err
			}
			if groupMappings != nil && groupMappings[subject] != nil {
				groups = groupMappings[subject]
			}
		}
		for _, group := range groups {
			subjectFq[string(K8sRbacSubjectTypeGroup)+"::"+group] = struct{}{}
		}
	}

	// load clusterrolebinding and rolebinding
	roleMap := map[string]any{}
	roleBindingMap := map[string]any{}
	subjectRoleBindingRefs := map[string][]string{}
	roleBindingMapDataMap, err := getRoleAndBindings(accountId)
	if err != nil {
		return map[string][]K8sRbacPermissionType{}, err
	}

	for _, value := range roleBindingMapDataMap["items"].([]any) {
		valueMap := value.(map[string]any)
		if valueMap["roleRef"] != nil {
			metadata := valueMap["metadata"].(map[string]any)
			roleBindingMap[metadata["namespace"].(string)+"/"+metadata["name"].(string)] = valueMap
			if _, ok := valueMap["subjects"]; !ok || valueMap["subjects"] == nil {
				continue
			}
			valueMapSubjects := valueMap["subjects"].([]any)
			for _, subject := range valueMapSubjects {
				subjectMap := subject.(map[string]any)
				subjectRef := subjectMap["kind"].(string) + "::" + subjectMap["name"].(string)
				if subjectMap["namespace"] != nil {
					subjectRef = subjectMap["kind"].(string) + "::" + subjectMap["namespace"].(string) + "/" + subjectMap["name"].(string)
				}
				if _, found := subjectFq[subjectRef]; !found {
					continue
				}
				key := metadata["name"].(string)
				if v, ok := subjectMap["namespace"]; ok {
					key = v.(string) + "/" + key
				} else {
					key = metadata["namespace"].(string) + "/" + key
				}

				if k, ok := subjectRoleBindingRefs[subjectRef]; ok {
					subjectRoleBindingRefs[subjectRef] = append(k, key)
				} else {
					subjectRoleBindingRefs[subjectRef] = []string{key}
				}
			}
		} else if valueMap["rules"] != nil {
			metadata := valueMap["metadata"].(map[string]any)
			roleMap[metadata["namespace"].(string)+"/"+metadata["name"].(string)] = valueMap
		}
	}

	// load cluster role binding data
	clusterRoleMap := map[string]any{}
	clusterRoleBindingMap := map[string]any{}
	subjectClusterRoleBindingRefs := map[string][]string{}

	clusterRoleBindingMapDataMap, err := getClusterRoleAndBindings(accountId)
	if err != nil {
		return map[string][]K8sRbacPermissionType{}, err
	}

	for _, value := range clusterRoleBindingMapDataMap["items"].([]any) {
		valueMap := value.(map[string]any)
		if valueMap["roleRef"] != nil {
			metadata := valueMap["metadata"].(map[string]any)
			clusterRoleBindingMap[metadata["name"].(string)] = valueMap
			if _, ok := valueMap["subjects"]; !ok || valueMap["subjects"] == nil {
				continue
			}
			valueMapSubjects := valueMap["subjects"].([]any)
			for _, subject := range valueMapSubjects {
				subjectRefMap := subject.(map[string]any)
				subjectRef := subjectRefMap["kind"].(string) + "::" + subjectRefMap["name"].(string)
				if subjectRefMap["namespace"] != nil {
					subjectRef = subjectRefMap["kind"].(string) + "::" + subjectRefMap["namespace"].(string) + "/" + subjectRefMap["name"].(string)
				}
				if _, found := subjectFq[subjectRef]; !found {
					continue
				}
				key := metadata["name"].(string)
				if v, ok := subjectRefMap["namespace"]; ok {
					key = v.(string) + "/" + key
				}
				if k, ok := subjectClusterRoleBindingRefs[subjectRef]; ok {
					subjectClusterRoleBindingRefs[subjectRef] = append(k, key)
				} else {
					subjectClusterRoleBindingRefs[subjectRef] = []string{key}
				}
			}
		} else if valueMap["rules"] != nil {
			metadata := valueMap["metadata"].(map[string]any)
			clusterRoleMap[metadata["name"].(string)] = valueMap
		}
	}

	// check subjectMap for subject and get all rolebinding and clusterrolebinding refs
	roleBindingRefs := []string{}
	clusterRoleBindingRefs := []string{}
	for s := range subjectFq {
		if v, ok := subjectRoleBindingRefs[s]; ok {
			roleBindingRefs = append(roleBindingRefs, v...)
		}

		if v, ok := subjectClusterRoleBindingRefs[s]; ok {
			clusterRoleBindingRefs = append(clusterRoleBindingRefs, v...)
		}
	}

	roleBindingRefs = lo.Uniq(roleBindingRefs)
	clusterRoleBindingRefs = lo.Uniq(clusterRoleBindingRefs)

	// get all permissions
	permissions := []k8sPermission{}
	for _, roleBindingRef := range roleBindingRefs {
		roleBinding := roleBindingMap[roleBindingRef]
		if roleBinding == nil || roleBinding.(map[string]any)["roleRef"] == nil {
			continue
		}
		roleRef := roleBinding.(map[string]any)["roleRef"].(map[string]any)
		if roleBinding.(map[string]any)["metadata"] == nil || roleBinding.(map[string]any)["metadata"].(map[string]any)["namespace"] == nil {
			continue
		}
		role := roleMap[roleBinding.(map[string]any)["metadata"].(map[string]any)["namespace"].(string)+"/"+roleRef["name"].(string)]
		roleRules := role.(map[string]any)["rules"].([]any)
		for _, rule := range roleRules {
			ruleMap := rule.(map[string]any)
			if _, ok := ruleMap["resources"]; !ok || ruleMap["resources"] == nil {
				continue
			}
			for _, resource := range ruleMap["resources"].([]any) {
				if resource != "*" && resource != resourceType {
					continue
				}
				verbs := ruleMap["verbs"].([]any)
				verbsStr := []string{}
				for _, verb := range verbs {
					verbsStr = append(verbsStr, verb.(string))
				}
				var resourceNames []string
				if ruleMap["resourceNames"] != nil {
					resourceNamesAny := ruleMap["resourceNames"].([]any)
					for _, resourceName := range resourceNamesAny {
						resourceNames = append(resourceNames, resourceName.(string))
					}
				}
				permissions = append(permissions, k8sPermission{
					Subject:       subject,
					Permissions:   verbsStr,
					Resource:      resource.(string),
					ResourceNames: resourceNames,
					Namespace:     roleBinding.(map[string]any)["metadata"].(map[string]any)["namespace"].(string),
					RuleType:      "Role",
					RuleName:      roleRef["name"].(string),
				})
			}
		}
	}

	for _, clusterRoleBindingRef := range clusterRoleBindingRefs {
		clusterRoleBinding := clusterRoleBindingMap[clusterRoleBindingRef]
		if clusterRoleBinding == nil || clusterRoleBinding.(map[string]any)["roleRef"] == nil {
			continue
		}
		roleRef := clusterRoleBinding.(map[string]any)["roleRef"].(map[string]any)
		role := clusterRoleMap[roleRef["name"].(string)]
		roleRules := role.(map[string]any)["rules"].([]any)
		for _, rule := range roleRules {
			ruleMap := rule.(map[string]any)
			if _, ok := ruleMap["resources"]; !ok || ruleMap["resources"] == nil {
				continue
			}
			for _, resource := range ruleMap["resources"].([]any) {
				verbs := ruleMap["verbs"].([]any)
				verbsStr := []string{}
				for _, verb := range verbs {
					verbsStr = append(verbsStr, verb.(string))
				}
				var resourceNames []string
				if ruleMap["resourceNames"] != nil {
					resourceNamesAny := ruleMap["resourceNames"].([]any)
					for _, resourceName := range resourceNamesAny {
						resourceNames = append(resourceNames, resourceName.(string))
					}
				}
				permissions = append(permissions, k8sPermission{
					Subject:       subject,
					Permissions:   verbsStr,
					Resource:      resource.(string),
					ResourceNames: resourceNames,
					Namespace:     "",
					RuleType:      "ClusterRole",
					RuleName:      roleRef["name"].(string),
				})
			}
		}
	}

	response := map[string][]K8sRbacPermissionType{}

	for _, resourceName := range resourceNames {
		perms := []K8sRbacPermissionType{}
		resourceNamespace2 := ""
		resourceName2 := ""
		if resourceType != "namespaces" {
			resourceNamespace2 = strings.Split(resourceName, "/")[0]
			resourceName2 = strings.Split(resourceName, "/")[1]
		}
		for _, permission := range permissions {
			if permission.RuleType == "ClusterRole" {
				switch permission.Resource {
				case "*":
					if len(permission.Permissions) == 0 || slices.Contains(permission.Permissions, "*") {
						perms = append(perms, K8sRbacPermissionTypeCreate, K8sRbacPermissionTypeDelete, K8sRbacPermissionTypeGet, K8sRbacPermissionTypeList, K8sRbacPermissionTypePatch, K8sRbacPermissionTypeUpdate, K8sRbacPermissionTypeWatch)
					} else {
						for _, perm := range permission.Permissions {
							perms = append(perms, K8sRbacPermissionType(perm))
						}
					}
				case resourceType:
					if len(permission.ResourceNames) == 0 || slices.Contains(permission.ResourceNames, resourceName2) {
						if len(permission.Permissions) == 0 || slices.Contains(permission.Permissions, "*") {
							perms = append(perms, K8sRbacPermissionTypeCreate, K8sRbacPermissionTypeDelete, K8sRbacPermissionTypeGet, K8sRbacPermissionTypeList, K8sRbacPermissionTypePatch, K8sRbacPermissionTypeUpdate, K8sRbacPermissionTypeWatch)
						} else {
							for _, perm := range permission.Permissions {
								perms = append(perms, K8sRbacPermissionType(perm))
							}
						}
					}
				}
			} else {
				if permission.Namespace != resourceNamespace2 {
					continue
				}
				switch permission.Resource {
				case "*":
					if len(permission.Permissions) == 0 || slices.Contains(permission.Permissions, "*") {
						perms = append(perms, K8sRbacPermissionTypeCreate, K8sRbacPermissionTypeDelete, K8sRbacPermissionTypeGet, K8sRbacPermissionTypeList, K8sRbacPermissionTypePatch, K8sRbacPermissionTypeUpdate, K8sRbacPermissionTypeWatch)
					} else {
						for _, perm := range permission.Permissions {
							perms = append(perms, K8sRbacPermissionType(perm))
						}
					}
				case resourceType:
					if len(permission.ResourceNames) == 0 || slices.Contains(permission.ResourceNames, resourceName2) {
						if len(permission.Permissions) == 0 || slices.Contains(permission.Permissions, "*") {
							perms = append(perms, K8sRbacPermissionTypeCreate, K8sRbacPermissionTypeDelete, K8sRbacPermissionTypeGet, K8sRbacPermissionTypeList, K8sRbacPermissionTypePatch, K8sRbacPermissionTypeUpdate, K8sRbacPermissionTypeWatch)
						} else {
							for _, perm := range permission.Permissions {
								perms = append(perms, K8sRbacPermissionType(perm))
							}
						}
					}
				}

			}
		}
		response[resourceName] = lo.Uniq(perms)
	}

	return response, nil
}

func k8sVarifyPermission(securityContext *SecurityContext, accountId string, subjectType K8sRbacSubjectType, subject string, resourceType string, resourceName string, permission K8sRbacPermissionType) (bool, error) {
	perms, err := k8sGetPermissions(securityContext, accountId, subjectType, subject, resourceType, []string{resourceName})
	if err != nil {
		return false, err
	}

	if len(perms[resourceName]) == 0 {
		return false, nil
	}

	return slices.Contains(perms[resourceName], permission), nil
}
