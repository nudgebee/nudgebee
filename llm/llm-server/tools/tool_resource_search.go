package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lithammer/fuzzysearch/fuzzy"
)

const ToolResourceSearch = "resource_search_execute"

func init() {
	core.RegisterNBToolFactory(ToolResourceSearch, func(accountId string) (core.NBTool, error) {
		return K8sResourceSearchTool{}, nil
	})
}

type K8sResourceSearchTool struct{}

type K8sResourceSearchRequest struct {
	ResourceName  string `json:"resource_name,omitempty"`
	ResourceType  string `json:"resource_type,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	LabelSelector string `json:"label_selector,omitempty"`
	SearchType    string `json:"search_type"` // "fuzzy", "suggestions", "namespace", "label"
}

type K8sResourceSearchResponse struct {
	Suggestions    []string                    `json:"suggestions,omitempty"`
	Resources      []K8sResourceInfo           `json:"resources,omitempty"`
	Commands       []K8sResourceSearchStrategy `json:"commands,omitempty"` // Fallback commands if direct search fails
	Message        string                      `json:"message,omitempty"`
	MatchQuality   string                      `json:"match_quality,omitempty"`   // exact, unique, unique_owner, fuzzy_high, fuzzy_low, multiple
	OwnerReference string                      `json:"owner_reference,omitempty"` // aggregated owner reference if applicable
}

type K8sResourceInfo struct {
	Name           string `json:"name"`
	Namespace      string `json:"namespace"`
	Type           string `json:"type"`
	Status         string `json:"status,omitempty"`
	Ready          string `json:"ready,omitempty"`
	Age            string `json:"age,omitempty"`
	OwnerReference string `json:"owner_reference,omitempty"`
}

type K8sResourceSearchStrategy struct {
	Strategy     string `json:"strategy"`
	Command      string `json:"command"`
	Description  string `json:"description"`
	ResourceType string `json:"resource_type,omitempty"`
}

// Common resource type mappings for fuzzy resolution
var k8sResourceTypeMappings = map[string][]string{
	"pod":                {"pods", "po"},
	"service":            {"services", "svc"},
	"deployment":         {"deployments", "deploy", "statefulsets", "sts", "daemonsets", "ds"}, // Generic "deployment" includes all workload types
	"statefulset":        {"statefulsets", "sts"},
	"daemonset":          {"daemonsets", "ds"},
	"job":                {"jobs", "cronjobs", "cj"}, // Generic "job" includes both job types
	"cronjob":            {"cronjobs", "cj"},
	"workload":           {"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs"}, // Generic workload term
	"app":                {"deployments", "statefulsets", "daemonsets"},                     // Generic app term
	"configmap":          {"configmaps", "cm"},
	"secret":             {"secrets"},
	"ingress":            {"ingresses", "ing"},
	"namespace":          {"namespaces", "ns"},
	"node":               {"nodes"},
	"pv":                 {"persistentvolumes"},
	"pvc":                {"persistentvolumeclaims"},
	"clusterrole":        {"clusterroles"},
	"clusterrolebinding": {"clusterrolebindings"},
	"role":               {"roles"},
	"rolebinding":        {"rolebindings"},
	"serviceaccount":     {"serviceaccounts", "sa"},
	"networkpolicy":      {"networkpolicies", "netpol"},
	"podsecuritypolicy":  {"podsecuritypolicies", "psp"},
	"storageclass":       {"storageclasses", "sc"},
	"crd":                {"customresourcedefinitions", "crds"},
}

// Pre-computed lookup tables built once at init from k8sResourceTypeMappings.
// Avoids repeated map/slice allocations on every search request.
var (
	// Reverse lookup: alias or canonical name → canonical type (O(1) instead of nested loop)
	k8sResourceTypeReverseLookup map[string]string
	// Flat list of all type names + aliases for fuzzy matching
	k8sAllResourceTypes []string
	// Cluster-wide (non-namespaced) resource types
	k8sClusterWideResources map[string]bool
)

func init() {
	k8sResourceTypeReverseLookup = make(map[string]string, len(k8sResourceTypeMappings)*3)
	k8sAllResourceTypes = make([]string, 0, len(k8sResourceTypeMappings)*3)
	for canonical, aliases := range k8sResourceTypeMappings {
		k8sResourceTypeReverseLookup[canonical] = canonical
		k8sAllResourceTypes = append(k8sAllResourceTypes, canonical)
		for _, alias := range aliases {
			k8sResourceTypeReverseLookup[alias] = canonical
			k8sAllResourceTypes = append(k8sAllResourceTypes, alias)
		}
	}

	k8sClusterWideResources = map[string]bool{
		"nodes": true, "node": true, "no": true,
		"clusterroles": true, "clusterrole": true,
		"clusterrolebindings": true, "clusterrolebinding": true,
		"persistentvolumes": true, "persistentvolume": true, "pv": true,
		"storageclasses": true, "storageclass": true, "sc": true,
		"customresourcedefinitions": true, "customresourcedefinition": true, "crd": true, "crds": true,
		"namespaces": true, "namespace": true, "ns": true,
		"podsecuritypolicies": true, "podsecuritypolicy": true, "psp": true,
	}
}

func (r K8sResourceSearchTool) Name() string {
	return ToolResourceSearch
}

func (r K8sResourceSearchTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (r K8sResourceSearchTool) Description() string {
	return `Searches for Kubernetes resources using fuzzy matching and provides suggestions for resource discovery.

**Usage:**
* **Fuzzy Resource Type Matching:** Find similar resource types for typos (e.g., "podss" → suggests "pods")
* **Comprehensive Resource Discovery:** Find all Kubernetes resources including CRDs and cluster-wide resources
* **App-centric Search:** When you find deployments/statefulsets, also returns their related pods
* **Namespace Discovery:** Find similar namespace names for typos or partial matches

**Input:** JSON with the following fields:
* resource_name (optional): Name of the resource to search for
* resource_type (optional): Type of resource (pods, services, etc.)  
* namespace (optional): Namespace to search in
* search_type: Type of search - "fuzzy", "suggestions", or "namespace"

**Examples:**
* Fuzzy resource type: {"resource_type": "podss", "search_type": "fuzzy"}
* App search: {"resource_name": "nginx", "namespace": "default", "search_type": "suggestions"} → Returns deployment + pods
* CRD discovery: {"resource_name": "my-custom-app", "namespace": "default", "search_type": "suggestions"}
* Namespace discovery: {"namespace": "nudgebe", "search_type": "namespace"}

**Output:** JSON with actual resource data (name, namespace, type, status) and fallback commands if needed.`
}

func (r K8sResourceSearchTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"resource_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of the resource to search for",
			},
			"resource_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of resource (pods, services, etc.)",
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Namespace to search in",
			},
			"label_selector": {
				Type:        core.ToolSchemaTypeString,
				Description: "Kubernetes label selector for label-based searches (e.g., 'app=nginx,tier=frontend')",
			},
			"search_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Type of search: 'fuzzy', 'suggestions', 'namespace', or 'label'",
			},
		},
		Required: []string{"search_type"},
	}
}

func (r K8sResourceSearchTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("resource-search: executing tool call", "command", input.Command)

	result, err := r.processSearchRequest(input.Command, nbRequestContext)
	if err != nil {
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	return core.NBToolResponse{
		Data:   result,
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

func (r K8sResourceSearchTool) sanitizeInput(input string, allowSpaces bool) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' || r == '/' {
			return r
		}
		if allowSpaces && r == ' ' {
			return r
		}
		return -1
	}, input)
}

func (r K8sResourceSearchTool) sanitizeLabelSelector(input string) string {
	// Allow chars commonly used in label selectors: alphanumeric, -, ., _, /, =, ,, (, )
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' || r == '/' || r == '=' || r == ',' || r == '(' || r == ')' {
			return r
		}
		return -1
	}, input)
}

func (r K8sResourceSearchTool) processSearchRequest(input string, nbRequestContext core.NbToolContext) (string, error) {
	var request K8sResourceSearchRequest
	if err := common.UnmarshalJson([]byte(input), &request); err != nil {
		return "", fmt.Errorf("invalid JSON input: %v", err)
	}

	request.ResourceName = r.sanitizeInput(request.ResourceName, true)
	request.ResourceType = r.sanitizeInput(request.ResourceType, true) // Allow spaces for multi-type
	request.Namespace = r.sanitizeInput(request.Namespace, false)
	request.LabelSelector = r.sanitizeLabelSelector(request.LabelSelector)

	// Fix for queries like "notifications pods" where the type is included in the name
	if request.ResourceName != "" {
		cleanedName, detectedType := r.extractTypeFromName(request.ResourceName)
		if detectedType != "" {
			request.ResourceName = cleanedName
			// If no type was specified, or if the detected type is more specific than what we had, use it.
			// But prioritizing the detected type from the name usually makes sense if the user explicitly typed it there.
			if request.ResourceType == "" || request.ResourceType == "resource" || request.ResourceType == "all" {
				request.ResourceType = detectedType
			}
		}
	}

	switch request.SearchType {
	case "fuzzy":
		return r.handleFuzzyResourceType(request)
	case "namespace":
		return r.handleNamespaceSearch(request, nbRequestContext)
	case "label":
		return r.handleLabelSearch(request, nbRequestContext)
	default:
		return r.handleResourceSuggestions(request, nbRequestContext)
	}
}

// handleLabelSearch finds resources using a label selector.
// extractTypeFromName checks if the resource name contains a resource type and separates them
func (r K8sResourceSearchTool) extractTypeFromName(input string) (string, string) {
	words := strings.Fields(input)
	if len(words) < 2 {
		return input, ""
	}

	var newWords []string
	var detectedType string

	for _, word := range words {
		if canonical, ok := k8sResourceTypeReverseLookup[strings.ToLower(word)]; ok {
			detectedType = canonical
		} else {
			newWords = append(newWords, word)
		}
	}

	if detectedType != "" {
		return strings.Join(newWords, " "), detectedType
	}

	return input, ""
}

func (r K8sResourceSearchTool) handleLabelSearch(request K8sResourceSearchRequest, nbRequestContext core.NbToolContext) (string, error) {
	if request.LabelSelector == "" {
		return "", fmt.Errorf("label_selector is required for label search type")
	}

	resourceType := request.ResourceType
	if resourceType == "" {
		resourceType = "pods" // Default to searching pods if no resource type is specified
	}

	namespace := request.Namespace
	if namespace == "" || namespace == "all" || namespace == "all-namespaces" {
		namespace = "--all-namespaces"
	}

	var cmd string
	if strings.HasPrefix(namespace, "-") {
		cmd = fmt.Sprintf("kubectl get %s -l '%s' %s --no-headers", resourceType, request.LabelSelector, namespace)
	} else {
		cmd = fmt.Sprintf("kubectl get %s -l '%s' -n %s --no-headers", resourceType, request.LabelSelector, namespace)
	}
	resources := r.executeKubectlAndParseResources(cmd, resourceType, namespace, nbRequestContext)

	message := fmt.Sprintf("Found %d resources matching label selector '%s'", len(resources), request.LabelSelector)

	response := K8sResourceSearchResponse{
		Resources: resources,
		Message:   message,
	}

	if len(resources) == 0 {
		response.Message = fmt.Sprintf("No resources found matching label selector '%s' in namespace '%s'", request.LabelSelector, namespace)
	}

	responseJSON, err := common.MarshalJson(response)
	if err != nil {
		return "", err
	}

	return string(responseJSON), nil
}

// handleFuzzyResourceType finds similar resource types for typos
func (r K8sResourceSearchTool) handleFuzzyResourceType(request K8sResourceSearchRequest) (string, error) {
	if request.ResourceType == "" {
		// If no resource type is provided, suggest some common ones.
		suggestions := []string{"pod", "service", "deployment", "job", "namespace", "configmap", "secret"}
		response := K8sResourceSearchResponse{
			Suggestions: suggestions,
			Message:     "No resource_type provided. Here are some common resource types you can search for.",
		}
		responseJSON, err := common.MarshalJson(response)
		if err != nil {
			return "", err
		}
		return string(responseJSON), nil
	}

	suggestions := r.findSimilarResourceTypes(request.ResourceType)

	response := K8sResourceSearchResponse{
		Suggestions: suggestions,
		Message:     fmt.Sprintf("Found %d similar resource types for '%s'", len(suggestions), request.ResourceType),
	}

	if len(suggestions) == 0 {
		response.Message = fmt.Sprintf("No similar resource types found for '%s'", request.ResourceType)
	}

	responseJSON, err := common.MarshalJson(response)
	if err != nil {
		return "", err
	}

	return string(responseJSON), nil
}

func (r K8sResourceSearchTool) searchDbForResources(resourceName, accountId string, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var resources []K8sResourceInfo
	if resourceName == "" {
		return resources
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return resources
	}

	// 1. Try to Identify Tenant
	var tenantId string
	_ = dbms.Db.Get(&tenantId, "SELECT tenant FROM cloud_accounts WHERE id = $1", accountId)

	// Prepare variations
	searchTerms := strings.Fields(strings.ToLower(resourceName))
	variations := []string{strings.ToLower(resourceName)}
	if len(searchTerms) > 1 {
		variations = append(variations, strings.Join(searchTerms, "-"))
		variations = append(variations, strings.Join(searchTerms, "_"))
		variations = append(variations, strings.Join(searchTerms, ""))
	}

	for _, v := range variations {
		var rows *sqlx.Rows
		var err error
		if tenantId != "" {
			// Tenant-wide search
			rows, err = dbms.Db.Queryx(`
				(select 'workload' as type, kw.external_id as name, kw.namespace
				FROM k8s_workloads kw
				JOIN cloud_accounts ca ON kw.cloud_account_id = ca.id
				where ca.tenant = $1 and (kw.external_id ilike $2 or kw.name ilike $2) and kw.is_active and ca.status = 'active'
				LIMIT 10)
				UNION ALL
				(select 'node' as type, kn.name, '' as namespace
				FROM k8s_nodes kn
				JOIN cloud_accounts ca ON kn.cloud_account_id = ca.id
				where ca.tenant = $1 and kn.name ilike $2 and kn.is_active and ca.status = 'active'
				LIMIT 10)`,
				tenantId, "%"+v+"%")
		} else {
			// Fallback to Account-only search
			rows, err = dbms.Db.Queryx(`
				(select 'workload' as type, external_id as name, namespace
				FROM k8s_workloads
				where cloud_account_id = $1 and (external_id ilike $2 or name ilike $2) and is_active
				LIMIT 10)
				UNION ALL
				(select 'node' as type, name, '' as namespace
				FROM k8s_nodes
				where cloud_account_id = $1 and name ilike $2 and is_active
				LIMIT 10)`,
				accountId, "%"+v+"%")
		}

		if err != nil {
			continue
		}

		for rows.Next() {
			var res K8sResourceInfo
			if err := rows.Scan(&res.Type, &res.Name, &res.Namespace); err == nil {
				resources = append(resources, res)
			}
		}
		_ = rows.Close()

		nbRequestContext.Ctx.GetLogger().Info("resource_search: db lookup result", "term", v, "count", len(resources), "tenant", tenantId)

		if len(resources) > 0 {
			break
		}
	}

	return resources
}

// handleResourceSuggestions finds actual resources using kubectl and returns resource data
func (r K8sResourceSearchTool) handleResourceSuggestions(request K8sResourceSearchRequest, nbRequestContext core.NbToolContext) (string, error) {
	namespace := request.Namespace
	if namespace == "" || namespace == "all" || namespace == "all-namespaces" {
		namespace = "--all-namespaces"
	}

	var resources []K8sResourceInfo
	searchName := request.ResourceName

	// 1. Try DB Search first (Fastest)
	dbResources := r.searchDbForResources(request.ResourceName, nbRequestContext.AccountId, nbRequestContext)
	if len(dbResources) > 0 {
		resources = dbResources
		// Use found names to do a more precise K8s search
		// But only use the DB name if the original query had spaces (meaning it needed a delimiter fix)
		// Otherwise, keep the original query name which might be more general and match multiple resources in K8s grep
		if strings.Contains(request.ResourceName, " ") {
			searchName = dbResources[0].Name
		}

		uniqueNamespaces := make(map[string]bool)
		for _, dr := range dbResources {
			if dr.Namespace != "" {
				uniqueNamespaces[dr.Namespace] = true
			}
		}

		if (request.Namespace == "" || request.Namespace == "--all-namespaces") && len(uniqueNamespaces) == 1 {
			for ns := range uniqueNamespaces {
				namespace = ns
				break
			}
		}
	}

	// 2. Try to find actual resources using multiple strategies
	k8sResources := r.findActualResources(searchName, namespace, request.ResourceType, nbRequestContext)
	// Filter out any resources whose names don't contain a meaningful term from the query.
	// This guards against grep-pipe failures on the relay returning unrelated resources.
	k8sResources = r.filterResourcesByRelevance(k8sResources, searchName)
	if len(k8sResources) > 0 {
		resources = k8sResources
	}

	// 3. Internalized discovery: If nothing found, execute fallback strategies automatically
	if len(resources) == 0 && searchName != "" {
		strategies := r.generateResourceSearchStrategies(searchName, namespace)
		// Try executing the strategies internally instead of suggesting them
		for _, strategy := range strategies {
			found := r.executeKubectlAndParseResources(strategy.Command, strategy.ResourceType, namespace, nbRequestContext)
			// Guard: filter by relevance before accepting results. The grep pipe in the
			// strategy commands may not be supported by every relay implementation, so we
			// must validate results client-side as well.
			found = r.filterResourcesByRelevance(found, searchName)
			if len(found) > 0 {
				// Filter by requested type if it's specific
				if request.ResourceType != "" && request.ResourceType != "all" && request.ResourceType != "resource" {
					var filtered []K8sResourceInfo
					for _, res := range found {
						if strings.EqualFold(res.Type, request.ResourceType) || slices.Contains(k8sResourceTypeMappings[request.ResourceType], strings.ToLower(res.Type)) {
							filtered = append(filtered, res)
						}
					}
					found = filtered
				}

				resources = append(resources, found...)
				if len(resources) > 10 {
					break // Don't over-collect
				}
			}
		}
	}

	var message string
	if len(resources) > 0 {
		resources = r.removeDuplicateResources(resources)
		message = fmt.Sprintf("Found %d resources matching your request.", len(resources))
	} else {
		message = "No resources found matching your request."
	}

	// Enrich resources with owner references (mostly for Pods)
	resources = r.enrichWithOwners(resources, nbRequestContext)

	// Calculate Match Quality
	matchQuality, ownerRef := r.calculateMatchQuality(resources, request.ResourceName)

	response := K8sResourceSearchResponse{
		Resources:      resources,
		Message:        message,
		MatchQuality:   matchQuality,
		OwnerReference: ownerRef,
	}

	responseJSON, err := common.MarshalJson(response)
	if err != nil {
		return "", err
	}

	return string(responseJSON), nil
}

// enrichWithOwners fetches owner references for found resources (especially pods)
func (r K8sResourceSearchTool) enrichWithOwners(resources []K8sResourceInfo, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	if len(resources) == 0 || len(resources) > 20 {
		// Avoid overhead for too many resources
		return resources
	}

	// Group resources by namespace to batch requests
	resourcesByNs := make(map[string][]int)
	for i, res := range resources {
		// Only fetch owners for Pods or ReplicaSets (common intermediate)
		if strings.EqualFold(res.Type, "pod") || strings.EqualFold(res.Type, "pods") {
			if res.Namespace != "" {
				resourcesByNs[res.Namespace] = append(resourcesByNs[res.Namespace], i)
			}
		}
	}

	for ns, indices := range resourcesByNs {
		names := []string{}
		for _, idx := range indices {
			names = append(names, resources[idx].Name)
		}

		// Use jsonpath to get owner info efficiently
		// We use jsonpath with [*] to avoid errors if ownerReferences is empty (index out of bounds)
		// jsonpath="{range .items[*]}{.metadata.name},{.metadata.ownerReferences[*].kind}/{.metadata.ownerReferences[*].name}{'\n'}{end}"
		// But for individual items, the root is Pod not List, so we handle both cases.
		var cmd string
		if len(names) == 1 {
			cmd = fmt.Sprintf("kubectl get pods %s -n %s -o=jsonpath='{.metadata.name},{.metadata.ownerReferences[*].kind}/{.metadata.ownerReferences[*].name}{\"\\n\"}'", names[0], ns)
		} else {
			cmd = fmt.Sprintf("kubectl get pods %s -n %s -o=jsonpath='{range .items[*]}{.metadata.name},{.metadata.ownerReferences[*].kind}/{.metadata.ownerReferences[*].name}{\"\\n\"}{end}'", strings.Join(names, " "), ns)
		}

		output := r.executeKubectlCommand(cmd, nbRequestContext)
		if output != "" {
			lines := strings.Split(output, "\n")
			ownerMap := make(map[string]string)
			for _, line := range lines {
				parts := strings.Split(line, ",")
				if len(parts) >= 2 {
					podName := parts[0]
					ownerRef := parts[1]
					// Check if owner exists (not empty, not just the separator "/")
					if ownerRef != "" && ownerRef != "/" {
						ownerMap[podName] = ownerRef
					}
				}
			}

			// Update resources
			for _, idx := range indices {
				if owner, ok := ownerMap[resources[idx].Name]; ok {
					resources[idx].OwnerReference = owner
				}
			}
		}
	}

	return resources
}

// calculateMatchQuality determines the quality of the search match
func (r K8sResourceSearchTool) calculateMatchQuality(resources []K8sResourceInfo, queryName string) (string, string) {
	if len(resources) == 0 {
		return "none", ""
	}

	// Check for exact match
	if len(resources) == 1 {
		if strings.EqualFold(resources[0].Name, queryName) {
			return "exact", resources[0].OwnerReference
		}
		return "unique", resources[0].OwnerReference
	}

	// Check for unique owner
	firstOwner := resources[0].OwnerReference
	allSameOwner := true
	for _, res := range resources {
		if res.OwnerReference != firstOwner || res.OwnerReference == "" {
			allSameOwner = false
			break
		}
	}

	if allSameOwner && firstOwner != "" {
		return "unique_owner", firstOwner
	}

	return "multiple", ""
}

// handleNamespaceSearch provides namespace discovery commands
func (r K8sResourceSearchTool) handleNamespaceSearch(request K8sResourceSearchRequest, nbRequestContext core.NbToolContext) (string, error) {
	if request.Namespace == "" {
		// If no namespace is provided, list all namespaces.
		cmd := "kubectl get ns --no-headers"
		output := r.executeKubectlCommand(cmd, nbRequestContext)
		var namespaces []string
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				namespaces = append(namespaces, fields[0])
			}
		}
		response := K8sResourceSearchResponse{
			Suggestions: namespaces,
			Message:     fmt.Sprintf("Found %d namespaces.", len(namespaces)),
		}
		responseJSON, err := common.MarshalJson(response)
		if err != nil {
			return "", err
		}
		return string(responseJSON), nil
	}

	strategies := []K8sResourceSearchStrategy{
		{
			Strategy:    "partial_match",
			Command:     fmt.Sprintf("kubectl get ns --no-headers | grep -i %s", request.Namespace),
			Description: "Find namespaces containing the partial name",
		},
		{
			Strategy:    "list_all",
			Command:     "kubectl get ns --no-headers",
			Description: "List all namespaces for manual selection",
		},
	}

	response := K8sResourceSearchResponse{
		Commands: strategies,
		Message:  fmt.Sprintf("Generated namespace search strategies for '%s'", request.Namespace),
	}

	responseJSON, err := common.MarshalJson(response)
	if err != nil {
		return "", err
	}

	return string(responseJSON), nil
}

// findSimilarResourceTypes suggests similar resource types for typos using fuzzy library
func (r K8sResourceSearchTool) findSimilarResourceTypes(input string) []string {
	ranks := fuzzy.RankFind(strings.ToLower(input), k8sAllResourceTypes)
	if len(ranks) == 0 {
		return []string{}
	}

	// Sort by distance and get top 3 matches with distance <= 2
	sort.Sort(ranks)
	var suggestions []string
	for _, rank := range ranks {
		if rank.Distance <= 2 && len(suggestions) < 3 {
			suggestions = append(suggestions, rank.Target)
		}
	}

	return suggestions
}

// generateResourceSearchStrategies creates multiple search strategies for a resource name
func (r K8sResourceSearchTool) generateResourceSearchStrategies(resourceName, namespace string) []K8sResourceSearchStrategy {
	var strategies []K8sResourceSearchStrategy
	resourceName = strings.ToLower(strings.TrimSpace(resourceName))
	searchTerms := strings.Fields(resourceName)

	grepChain := ""
	if len(searchTerms) > 0 {
		var parts []string
		for _, term := range searchTerms {
			parts = append(parts, fmt.Sprintf("grep -i '%s'", term))
		}
		grepChain = " | " + strings.Join(parts, " | ")
	}

	nsFlag := fmt.Sprintf("-n %s", namespace)
	if strings.HasPrefix(namespace, "-") {
		nsFlag = namespace
	}

	// Strategy 1: Exact label matches with multiple label keys
	labelKeys := []string{"app", "app.kubernetes.io/name", "app.kubernetes.io/instance", "k8s-app"}
	for i, labelKey := range labelKeys {
		// Label selectors don't support spaces easily, so we only use the first term or the hyphenated version
		selectorValue := resourceName
		if len(searchTerms) > 1 {
			selectorValue = strings.Join(searchTerms, "-")
		}
		strategies = append(strategies, K8sResourceSearchStrategy{
			Strategy:     fmt.Sprintf("label_match_%d", i+1),
			Command:      fmt.Sprintf("kubectl get pods -l '%s=%s' %s --no-headers", labelKey, selectorValue, nsFlag),
			Description:  fmt.Sprintf("Search using label %s", labelKey),
			ResourceType: "pods",
		})
	}

	// Strategy 2: Partial name match with wildcards using grep
	strategies = append(strategies, K8sResourceSearchStrategy{
		Strategy:     "partial_name_pods",
		Command:      fmt.Sprintf("kubectl get pods %s --no-headers%s", nsFlag, grepChain),
		Description:  "Search pods with partial name match",
		ResourceType: "pods",
	})

	// Strategy 3: Search in all resource types
	strategies = append(strategies, K8sResourceSearchStrategy{
		Strategy:     "all_resources",
		Command:      fmt.Sprintf("kubectl get all %s --no-headers%s", nsFlag, grepChain),
		Description:  "Search across all resource types",
		ResourceType: "all",
	})

	// Strategy 4: Check for common patterns
	commonPatterns := []string{"server", "api", "web", "frontend", "backend", "worker", "processor"}
	for _, pattern := range commonPatterns {
		if strings.Contains(resourceName, pattern) {
			strategies = append(strategies, K8sResourceSearchStrategy{
				Strategy:     "component_label",
				Command:      fmt.Sprintf("kubectl get pods -l 'app.kubernetes.io/component=%s' %s --no-headers", pattern, nsFlag),
				Description:  fmt.Sprintf("Search using component label for %s", pattern),
				ResourceType: "pods",
			})
			break
		}
	}

	// Strategy 5: Deployment-specific search
	strategies = append(strategies, K8sResourceSearchStrategy{
		Strategy:     "deployment_search",
		Command:      fmt.Sprintf("kubectl get deployments %s --no-headers%s", nsFlag, grepChain),
		Description:  "Search deployments specifically",
		ResourceType: "deployments",
	})

	// Strategy 6: Individual word search (Lenient)
	if len(searchTerms) > 1 {
		for _, term := range searchTerms {
			if len(term) < 3 {
				continue
			}
			strategies = append(strategies, K8sResourceSearchStrategy{
				Strategy:     fmt.Sprintf("individual_word_%s", term),
				Command:      fmt.Sprintf("kubectl get pods %s --no-headers | grep -i '%s'", nsFlag, term),
				Description:  fmt.Sprintf("Search pods by word: %s", term),
				ResourceType: "pods",
			})
		}
	}

	return strategies
}

// findActualResources executes kubectl commands to find matching resources
func (r K8sResourceSearchTool) findActualResources(resourceName, namespace, requestedResourceType string, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var allResources []K8sResourceInfo

	if requestedResourceType != "" {
		resources := r.searchResourceType(resourceName, namespace, requestedResourceType, nbRequestContext)
		allResources = append(allResources, resources...)
		allResources = r.expandWorkloadResources(allResources, namespace, nbRequestContext)
		return r.removeDuplicateResources(allResources)
	}

	type searchResult struct {
		resources []K8sResourceInfo
	}

	// Channels to collect results from parallel execution
	commonResChan := make(chan searchResult)
	clusterResChan := make(chan searchResult)
	crdResChan := make(chan searchResult)
	labelResChan := make(chan searchResult)

	// Strategy 1: Try common resource types in parallel
	commonResourceTypes := []string{"pods", "services", "deployments", "statefulsets", "daemonsets", "configmaps", "secrets", "jobs", "cronjobs", "rollouts"}

	go func() {
		var resources []K8sResourceInfo
		// We can further parallelize this inner loop if needed, but grouping by strategy is a good start
		for _, resourceType := range commonResourceTypes {
			res := r.searchResourceType(resourceName, namespace, resourceType, nbRequestContext)
			resources = append(resources, res...)
		}
		commonResChan <- searchResult{resources: resources}
	}()

	// Strategy 2: Try cluster-wide resources in parallel
	clusterResourceTypes := []string{"clusterroles", "clusterrolebindings", "nodes", "persistentvolumes", "storageclasses", "customresourcedefinitions"}
	go func() {
		var resources []K8sResourceInfo
		for _, resourceType := range clusterResourceTypes {
			res := r.searchResourceType(resourceName, namespace, resourceType, nbRequestContext)
			resources = append(resources, res...)
		}
		clusterResChan <- searchResult{resources: resources}
	}()

	// Strategy 3: CRD discovery (can be slow, run in parallel)
	go func() {
		var resources []K8sResourceInfo
		crdTypes := r.getCustomResourceTypes(nbRequestContext)
		for _, resourceType := range crdTypes {
			res := r.searchResourceType(resourceName, namespace, resourceType, nbRequestContext)
			resources = append(resources, res...)
			if len(resources) > 5 {
				break
			}
		}
		crdResChan <- searchResult{resources: resources}
	}()

	// Strategy 4: Label searches
	go func() {
		var resources []K8sResourceInfo
		labelKeys := []string{"app", "app.kubernetes.io/name", "app.kubernetes.io/instance", "k8s-app"}
		for _, labelKey := range labelKeys {
			var cmd string
			if namespace == "--all-namespaces" || namespace == "-A" {
				cmd = fmt.Sprintf("kubectl get pods -l '%s=%s' --all-namespaces --no-headers", labelKey, resourceName)
			} else {
				cmd = fmt.Sprintf("kubectl get pods -l '%s=%s' -n %s --no-headers", labelKey, resourceName, namespace)
			}
			res := r.executeKubectlAndParseResources(cmd, "pods", namespace, nbRequestContext)
			// Label selectors can return pods from a broad label match; validate by name.
			res = r.filterResourcesByRelevance(res, resourceName)
			resources = append(resources, res...)
		}
		labelResChan <- searchResult{resources: resources}
	}()

	// Collect results with a timeout to ensure we don't hang forever
	timeoutDuration := 60 * time.Second
	// Use config if available
	if cfgVal := config.Config.AsyncOperationTimeoutSeconds; cfgVal > 0 {
		timeoutDuration = time.Duration(cfgVal) * time.Second
	}
	timeout := time.After(timeoutDuration)

	// We expect 4 results
CollectResults:
	for i := 0; i < 4; i++ {
		select {
		case res := <-commonResChan:
			allResources = append(allResources, res.resources...)
		case res := <-clusterResChan:
			allResources = append(allResources, res.resources...)
		case res := <-crdResChan:
			allResources = append(allResources, res.resources...)
		case res := <-labelResChan:
			allResources = append(allResources, res.resources...)
		case <-timeout:
			nbRequestContext.Ctx.GetLogger().Warn("resource-search: timeout waiting for strategies to complete")
			// Proceed with whatever we have found so far
			break CollectResults
		}
	}

	// Strategy 5: For any deployments/statefulsets found, also find their pods
	// This depends on the results of the above searches, so it must run after
	allResources = r.expandWorkloadResources(allResources, namespace, nbRequestContext)

	return r.removeDuplicateResources(allResources)
}

// getCustomResourceTypes gets CRD resource types
func (r K8sResourceSearchTool) getCustomResourceTypes(nbRequestContext core.NbToolContext) []string {
	var crdTypes []string

	// Get CRDs first
	cmd := "kubectl get customresourcedefinitions --no-headers"
	response := r.executeKubectlCommand(cmd, nbRequestContext)
	if response == "" {
		return crdTypes
	}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 1 {
			// CRD name is the first field, we want the plural form
			crdName := fields[0]
			// Extract the plural form (before the first dot)
			if dotIndex := strings.Index(crdName, "."); dotIndex > 0 {
				plural := crdName[:dotIndex]
				crdTypes = append(crdTypes, plural)
			}
		}
	}

	// Limit to avoid performance issues
	if len(crdTypes) > 10 {
		crdTypes = crdTypes[:10]
	}

	return crdTypes
}

// expandWorkloadResources finds related pods for deployments, statefulsets, daemonsets
func (r K8sResourceSearchTool) expandWorkloadResources(resources []K8sResourceInfo, namespace string, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var expandedResources []K8sResourceInfo
	expandedResources = append(expandedResources, resources...) // Keep original resources

	for _, resource := range resources {
		if resource.Type == "deployments" || resource.Type == "statefulsets" || resource.Type == "daemonsets" || resource.Type == "jobs" {
			relatedPods := r.findRelatedPods(resource, nbRequestContext)
			expandedResources = append(expandedResources, relatedPods...)
		}
	}

	return expandedResources
}

// findRelatedPods finds pods that belong to a workload resource
func (r K8sResourceSearchTool) findRelatedPods(workload K8sResourceInfo, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var pods []K8sResourceInfo

	// Strategy 1: Use label selectors based on workload name
	labelSelectors := []string{
		fmt.Sprintf("app=\"%s\"", workload.Name),
		fmt.Sprintf("app.kubernetes.io/name=\"%s\"", workload.Name),
		fmt.Sprintf("app.kubernetes.io/instance=\"%s\"", workload.Name),
	}

	for _, selector := range labelSelectors {
		cmd := fmt.Sprintf("kubectl get pods -l %s -n %s --no-headers", selector, workload.Namespace)
		response := r.executeKubectlCommand(cmd, nbRequestContext)
		if response != "" {
			lines := strings.Split(response, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					if pod := r.parsePodLine(line, workload.Namespace, false); pod != nil {
						pods = append(pods, *pod)
					}
				}
			}
		}
	}

	// Strategy 2: For deployments, find via ReplicaSet
	if workload.Type == "deployments" {
		pods = append(pods, r.findPodsViaReplicaSet(workload, nbRequestContext)...)
	}

	// Strategy 3: Pattern matching - find pods that start with workload name
	cmd := fmt.Sprintf("kubectl get pods -n %s --no-headers", workload.Namespace)
	response := r.executeKubectlCommand(cmd, nbRequestContext)
	if response != "" {
		lines := strings.Split(response, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && strings.HasPrefix(strings.ToLower(line), strings.ToLower(workload.Name)) {
				if pod := r.parsePodLine(line, workload.Namespace, false); pod != nil {
					pods = append(pods, *pod)
				}
			}
		}
	}

	return pods
}

// findPodsViaReplicaSet finds pods through ReplicaSet for deployments
func (r K8sResourceSearchTool) findPodsViaReplicaSet(deployment K8sResourceInfo, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var pods []K8sResourceInfo

	// Find ReplicaSets owned by this deployment
	cmd := fmt.Sprintf("kubectl get replicasets -n %s --no-headers", deployment.Namespace)
	response := r.executeKubectlCommand(cmd, nbRequestContext)
	if response == "" {
		return pods
	}

	var replicaSets []string
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(strings.ToLower(line), strings.ToLower(deployment.Name)) {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				replicaSets = append(replicaSets, fields[0])
			}
		}
	}

	// Find pods owned by these ReplicaSets
	for _, rs := range replicaSets {
		cmd := fmt.Sprintf("kubectl get pods -n %s --no-headers", deployment.Namespace)
		response := r.executeKubectlCommand(cmd, nbRequestContext)
		if response != "" {
			lines := strings.Split(response, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" && strings.HasPrefix(strings.ToLower(line), strings.ToLower(rs)) {
					if pod := r.parsePodLine(line, deployment.Namespace, false); pod != nil {
						pods = append(pods, *pod)
					}
				}
			}
		}
	}

	return pods
}

// searchResourceType searches for a resource name within a specific resource type
func (r K8sResourceSearchTool) searchResourceType(resourceName, namespace, resourceType string, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var allResources []K8sResourceInfo

	types := strings.FieldsFunc(resourceType, func(r rune) bool {
		return r == ',' || r == ' '
	})

	if len(types) > 1 {
		for _, t := range types {
			if t != "" {
				allResources = append(allResources, r.searchResourceType(resourceName, namespace, t, nbRequestContext)...)
			}
		}
		return allResources
	}

	// Determine if this is a namespaced resource or cluster-wide
	var cmd string
	if r.isClusterWideResource(resourceType) {
		cmd = fmt.Sprintf("kubectl get %s --no-headers", resourceType)
	} else {
		// Handle --all-namespaces special case
		// If namespace starts with "-", treat it as a flag (e.g. -A, --all-namespaces)
		if strings.HasPrefix(namespace, "-") {
			cmd = fmt.Sprintf("kubectl get %s %s --no-headers", resourceType, namespace)
		} else {
			cmd = fmt.Sprintf("kubectl get %s -n %s --no-headers", resourceType, namespace)
		}
	}

	// Optimization: If resourceName is provided, try grepping for terms to reduce output size
	// We try terms one by one until we get some output
	searchTerms := strings.Fields(strings.ToLower(resourceName))

	// Heuristic: If resourceName contains spaces, it might be a delimited name (e.g. "llm server" -> "llm-server", "llm_server")
	// We want to prioritize finding these variations if they exist.
	var variations []string
	if len(searchTerms) > 1 {
		delimiters := []string{"-", "_", ".", ""}
		for _, d := range delimiters {
			variations = append(variations, strings.Join(searchTerms, d))
		}
	}

	var response string
	if len(searchTerms) > 0 {
		// Try terms in reverse order often works better for names like "the llm server" (server is better than the)
		// But let's just try all of them until one hits.
		for i := len(searchTerms) - 1; i >= 0; i-- {
			term := searchTerms[i]
			if (len(term) < 3 || term == "server" || term == "service" || term == "pod") && len(searchTerms) > 1 {
				continue // Skip very short or generic terms if we have others
			}
			tempCmd := fmt.Sprintf("%s | grep -i -- '%s'", cmd, term)
			response = r.executeKubectlCommand(tempCmd, nbRequestContext)
			if response != "" {
				break
			}
		}
	} else {
		response = r.executeKubectlCommand(cmd, nbRequestContext)
	}

	if response == "" {
		return allResources
	}

	lines := strings.Split(response, "\n")
	isAllNamespaces := (namespace == "--all-namespaces" || namespace == "-A")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check if the resource name matches (partial or full)
		// If resourceName is empty, we match all resources of the given type.
		matchesAll := len(searchTerms) == 0 && len(variations) == 0
		normalizedLine := strings.ToLower(line)

		// 1. Check variation matches if applicable (Prioritized)
		for _, variation := range variations {
			if strings.Contains(normalizedLine, variation) {
				matchesAll = true
				break
			}
		}

		// 2. Fallback to component match (all terms must be present)
		if !matchesAll && len(searchTerms) > 0 {
			matchesAll = true
			for _, term := range searchTerms {
				if !strings.Contains(normalizedLine, term) {
					matchesAll = false
					break
				}
			}
		}

		if matchesAll {
			if resource := r.parseGenericResourceLine(line, resourceType, namespace, isAllNamespaces); resource != nil {
				allResources = append(allResources, *resource)
			}
		}
	}

	return allResources
}

// isClusterWideResource determines if a resource type is cluster-wide (not namespaced).
// Uses pre-computed k8sClusterWideResources map to avoid allocating a new map per call.
func (r K8sResourceSearchTool) isClusterWideResource(resourceType string) bool {
	return k8sClusterWideResources[strings.ToLower(resourceType)]
}

// parseGenericResourceLine parses a generic kubectl output line
func (r K8sResourceSearchTool) parseGenericResourceLine(line, resourceType, namespace string, isAllNamespaces bool) *K8sResourceInfo {
	fields := strings.Fields(line)
	if len(fields) < 1 {
		return nil
	}

	var fieldOffset = 0
	resource := &K8sResourceInfo{
		Name: fields[0],
		Type: resourceType,
	}

	// Handle --all-namespaces output format: NAMESPACE NAME READY STATUS ...
	if isAllNamespaces && !r.isClusterWideResource(resourceType) && len(fields) >= 2 {
		resource.Namespace = fields[0]
		resource.Name = fields[1]
		fieldOffset = 1
	} else {
		resource.Namespace = namespace
	}

	// If it's a cluster-wide resource, namespace should be empty
	if r.isClusterWideResource(resourceType) {
		resource.Namespace = ""
	}

	// Try to parse common fields with proper offset
	if len(fields) >= 2+fieldOffset {
		resource.Status = fields[1+fieldOffset]
	}
	if len(fields) >= 3+fieldOffset {
		resource.Ready = fields[2+fieldOffset]
	}
	if len(fields) >= 4+fieldOffset {
		resource.Age = fields[len(fields)-1] // Age is usually the last field
	}

	return resource
}

// executeKubectlCommand runs a kubectl command and returns the output
func (r K8sResourceSearchTool) executeKubectlCommand(command string, nbRequestContext core.NbToolContext) string {
	// Ensure kubectl prefix
	if !strings.HasPrefix(command, "kubectl") {
		command = "kubectl " + command
	}

	response, err := ExecuteContainerJob(nbRequestContext, RelayJobKubectl, command, nbRequestContext.AccountId, map[string]any{}, false)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("resource-search: kubectl command failed", "error", err.Error(), "command", command)
		return ""
	}

	// ExecuteApiCall returns a JSON string with format {"stdout": "actual_output"}
	if responseStr, ok := response.(string); ok {
		var responseObj map[string]any
		if err := common.UnmarshalJson([]byte(responseStr), &responseObj); err == nil {
			if stdout, exists := responseObj["stdout"]; exists {
				if stdoutStr, ok := stdout.(string); ok {
					return stdoutStr
				}
			}
		}
		nbRequestContext.Ctx.GetLogger().Error("resource-search: failed to parse kubectl response JSON", "response", responseStr)
		return ""
	}

	nbRequestContext.Ctx.GetLogger().Error("resource-search: unexpected response type", "response_type", fmt.Sprintf("%T", response), "response", response)
	return ""
}

// executeKubectlAndParseResources executes kubectl and parses the result into resource info
func (r K8sResourceSearchTool) executeKubectlAndParseResources(command, resourceType, namespace string, nbRequestContext core.NbToolContext) []K8sResourceInfo {
	var resources []K8sResourceInfo

	output := r.executeKubectlCommand(command, nbRequestContext)
	if output == "" {
		return resources
	}

	isAllNamespaces := (namespace == "--all-namespaces" || namespace == "-A")
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch resourceType {
		case "pods", "pod":
			if resource := r.parsePodLine(line, namespace, isAllNamespaces); resource != nil {
				resources = append(resources, *resource)
			}
		case "deployments", "deployment":
			if resource := r.parseDeploymentLine(line, namespace, isAllNamespaces); resource != nil {
				resources = append(resources, *resource)
			}
		case "all":
			if resource := r.parseAllResourceLine(line, namespace, isAllNamespaces); resource != nil {
				resources = append(resources, *resource)
			}
		}
	}

	return resources
}

// parsePodLine parses a kubectl get pods output line
func (r K8sResourceSearchTool) parsePodLine(line, namespace string, isAllNamespaces bool) *K8sResourceInfo {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return nil
	}

	var fieldOffset = 0
	var actualNamespace = namespace

	// Handle --all-namespaces output format: NAMESPACE NAME READY STATUS ...
	if isAllNamespaces && len(fields) >= 4 {
		actualNamespace = fields[0]
		fieldOffset = 1
	}

	return &K8sResourceInfo{
		Name:      fields[fieldOffset],
		Namespace: actualNamespace,
		Type:      "pod",
		Ready:     fields[1+fieldOffset],
		Status:    fields[2+fieldOffset],
		Age: func() string {
			if len(fields) > 4+fieldOffset {
				return fields[4+fieldOffset]
			} else {
				return ""
			}
		}(),
	}
}

// parseDeploymentLine parses a kubectl get deployments output line
func (r K8sResourceSearchTool) parseDeploymentLine(line, namespace string, isAllNamespaces bool) *K8sResourceInfo {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return nil
	}

	var fieldOffset = 0
	var actualNamespace = namespace

	// Handle --all-namespaces output format: NAMESPACE NAME READY UP-TO-DATE AVAILABLE ...
	if isAllNamespaces && len(fields) >= 5 {
		actualNamespace = fields[0]
		fieldOffset = 1
	}

	return &K8sResourceInfo{
		Name:      fields[fieldOffset],
		Namespace: actualNamespace,
		Type:      "deployment",
		Ready:     fmt.Sprintf("%s/%s", fields[1+fieldOffset], fields[2+fieldOffset]),
		Status:    fields[3+fieldOffset],
		Age: func() string {
			if len(fields) > 4+fieldOffset {
				return fields[4+fieldOffset]
			} else {
				return ""
			}
		}(),
	}
}

// parseAllResourceLine parses a kubectl get all output line
func (r K8sResourceSearchTool) parseAllResourceLine(line, namespace string, isAllNamespaces bool) *K8sResourceInfo {
	fields := strings.Fields(line)
	if len(fields) < 1 {
		return nil
	}

	// Handle -A: NAMESPACE type/name ...
	var typeName string
	var actualNamespace = namespace
	var fieldOffset = 0

	if isAllNamespaces && len(fields) >= 2 {
		actualNamespace = fields[0]
		typeName = fields[1]
		fieldOffset = 1
	} else {
		typeName = fields[0]
	}

	parts := strings.SplitN(typeName, "/", 2)
	if len(parts) != 2 {
		return nil // skip headers or malformed lines
	}

	resType := parts[0]
	resName := parts[1]

	info := &K8sResourceInfo{
		Name:      resName,
		Namespace: actualNamespace,
		Type:      resType,
		Age:       fields[len(fields)-1],
	}

	// Simple heuristics for common types
	if strings.HasPrefix(resType, "pod") && len(fields) >= 3+fieldOffset {
		info.Ready = fields[1+fieldOffset]
		info.Status = fields[2+fieldOffset]
	} else if strings.HasPrefix(resType, "deployment") && len(fields) >= 4+fieldOffset {
		info.Ready = fmt.Sprintf("%s/%s", fields[1+fieldOffset], fields[3+fieldOffset]) // Ready/Available
		info.Status = "Active"                                                          // Deployments don't have a simple status column in get all
	}

	return info
}

// filterResourcesByRelevance removes resources whose names do not contain at least one
// meaningful term from the query. This is a defence-in-depth guard for cases where the
// kubectl grep pipe fails on the relay server and all resources are returned.
func (r K8sResourceSearchTool) filterResourcesByRelevance(resources []K8sResourceInfo, query string) []K8sResourceInfo {
	if query == "" || len(resources) == 0 {
		return resources
	}

	// Collect meaningful terms: the full query plus each hyphen/underscore/dot component
	// that is long enough and not a generic Kubernetes keyword.
	genericTerms := map[string]bool{
		"pod": true, "pods": true, "server": true, "service": true,
		"app": true, "api": true, "web": true,
	}
	seen := map[string]bool{}
	var terms []string
	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) >= 3 && !genericTerms[t] && !seen[t] {
			seen[t] = true
			terms = append(terms, t)
		}
	}

	queryLower := strings.ToLower(query)
	add(queryLower)
	for _, part := range strings.FieldsFunc(queryLower, func(c rune) bool {
		return c == '-' || c == '_' || c == '.'
	}) {
		add(part)
	}

	if len(terms) == 0 {
		return resources
	}

	var filtered []K8sResourceInfo
	for _, res := range resources {
		if ResourceNameMatchesTerms(res.Name, terms) {
			filtered = append(filtered, res)
		}
	}
	return filtered
}

// ResourceNameMatchesTerms returns true when name contains at least one of the
// provided terms (case-insensitive). Exported so agents can reuse the same logic.
func ResourceNameMatchesTerms(name string, terms []string) bool {
	lower := strings.ToLower(name)
	for _, t := range terms {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// removeDuplicateResources removes duplicate resources from the slice
func (r K8sResourceSearchTool) removeDuplicateResources(resources []K8sResourceInfo) []K8sResourceInfo {
	seen := make(map[string]bool)
	var result []K8sResourceInfo

	for _, resource := range resources {
		key := fmt.Sprintf("%s/%s/%s", resource.Namespace, resource.Type, resource.Name)
		if !seen[key] {
			seen[key] = true
			result = append(result, resource)
		}
	}

	return result
}

func (m K8sResourceSearchTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{},
		ConfigSource: core.ToolConfigSourceAccountAgent,
		Properties:   map[string]core.ToolSchemaProperty{},
	}
}

func GetCurrentK8sAccountState(accountId string, limit int) map[string][]string {
	response := map[string][]string{}
	cacheKey := fmt.Sprintf("k8s_account_state:%s:%d", accountId, limit)

	// Check cache
	if cachedData, found := common.CacheGet(core.CacheNamespaceLlmToolConfig, cacheKey); found {
		if err := common.UnmarshalJson(cachedData, &response); err == nil {
			return response
		}
		slog.Warn("tools: failed to unmarshal cached k8s account state", "error", "unmarshal error")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}

	// Fetch workloads, nodes, and external resources with per-category limits
	// Use limit+1 to detect truncation efficiently
	sqlLimit := limit + 1
	rows, err := dbms.Db.Queryx(`
		(select 'workload' as resource_type, external_id as name
		FROM k8s_workloads
		where cloud_account_id = $1 and is_active
		LIMIT $2)
		UNION ALL
		(select 'node' as resource_type, name
		FROM k8s_nodes
		where cloud_account_id = $1 and is_active
		LIMIT $2)
		UNION ALL
		(select case when service_name = 'host' then 'external-node' else 'external-service' end as resource_type, name
		from cloud_resourses cr
		where cr.account = $1 and cr.type ilike 'external' and is_active
		LIMIT $2)`,
		accountId, sqlLimit)
	if err != nil {
		slog.Error("unable to fetch workloads/nodes", "error", err)
		return response
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("tools: failed to close rows", "error", err)
		}
	}()

	counts := make(map[string]int)

	for rows.Next() {
		var resourceType, name *string
		if err := rows.Scan(&resourceType, &name); err != nil {
			continue
		}
		if resourceType == nil || name == nil {
			continue
		}

		if counts[*resourceType] >= limit {
			if counts[*resourceType] == limit {
				response[*resourceType] = append(response[*resourceType], "... (truncated)")
				counts[*resourceType]++
			}
			continue
		}

		// cleanup for ports
		val := *name
		if *resourceType == "external-node" && strings.Contains(val, ":") {
			val = strings.Split(val, ":")[0]
		}

		response[*resourceType] = append(response[*resourceType], val)
		counts[*resourceType]++
	}

	// Fetch k8s version and connection status
	rows2, err := dbms.Db.Queryx(`select k8s_version , connection_status - 'schedule_jobs' as connection_status from agent where cloud_account_id = $1`, accountId)
	if err != nil {
		slog.Error("unable to fetch k8s version and connection status", "error", err)
		return response
	}
	defer func() {
		if err := rows2.Close(); err != nil {
			slog.Error("tools: failed to close rows", "error", err)
		}
	}()

	for rows2.Next() {
		var k8sVersion, connectionStatus *string
		if err := rows2.Scan(&k8sVersion, &connectionStatus); err != nil {
			continue
		}
		if k8sVersion == nil || connectionStatus == nil {
			continue
		}
		response["k8s_version"] = append(response["k8s_version"], *k8sVersion)
		response["connection_status"] = append(response["connection_status"], *connectionStatus)
	}

	// Cache the result
	if cachedBytes, err := common.MarshalJson(response); err == nil {
		_ = common.CacheSet(core.CacheNamespaceLlmToolConfig, cacheKey, cachedBytes, common.CacheSetWithExpiration(30*time.Minute))
	}

	return response
}

func GetCurrentOtelHosts(accountId string) map[string]string {
	discoveryQuery := `sum by (host.name, host.ip) (system.memory.utilization{host.name!=""})`
	toolProm := PrometheusExecuteTool{}

	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Hour)

	response, err := toolProm.executePromQl(core.NbToolContext{
		AccountId: accountId,
	}, discoveryQuery, accountId, map[string]any{
		"end_time":   endTime.UTC().Format(time.RFC3339),
		"start_time": startTime.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return map[string]string{}
	}

	externalNodes := map[string]string{}

	for _, seriesAny := range response {
		if series, ok := seriesAny.(map[string]any); ok && series["metric"] != nil {
			if metric, ok := series["metric"].(map[string]any); ok {
				if hostName, ok := metric["host.name"].(string); ok {
					if hostIP, ok := metric["host.ip"].(string); ok {
						externalNodes[hostName] = hostIP
					} else {
						externalNodes[hostName] = ""
					}
				}
			}
		}
	}
	return externalNodes
}

func GetCurrentAwsAccountState(accountId string) map[string][]string {

	response := map[string][]string{}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}
	rows, err := dbms.Db.Queryx(`(
		select distinct 'region', region
		from cloud_resourses cr
		where cr.account  = $1
			and is_active
	)
	union
	(
		select distinct 'service', service_name
		from cloud_resourses cr
		where cr.account  = $1
			and is_active
	)
			`,
		accountId)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("tools: failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var resourceType, name *string
		err := rows.Scan(&resourceType, &name)
		if err != nil {
			slog.Error("unable to scan rows", "error", err)
			continue
		}
		if resourceType == nil || name == nil {
			continue
		}
		response[*resourceType] = append(response[*resourceType], *name)
	}
	if err := rows.Err(); err != nil {
		slog.Error("tools: error iterating rows", "error", err)
	}
	return response
}

func GetCurrentAzureAccountState(accountId string) map[string][]string {

	response := map[string][]string{}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}
	rows, err := dbms.Db.Queryx(`(
		select distinct 'region', region
		from cloud_resourses cr
		where cr.account  = $1
			and is_active
	)
	union
	(
		select distinct 'service', service_name
		from cloud_resourses cr
		where cr.account  = $1
			and is_active
	)
			`,
		accountId)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("tools: failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var resourceType, name *string
		err := rows.Scan(&resourceType, &name)
		if err != nil {
			slog.Error("unable to scan rows", "error", err)
			continue
		}
		if resourceType == nil || name == nil {
			continue
		}
		response[*resourceType] = append(response[*resourceType], *name)
	}
	if err := rows.Err(); err != nil {
		slog.Error("tools: error iterating rows", "error", err)
	}
	return response
}

func GetCurrentGcpAccountState(accountId string) map[string][]string {

	response := map[string][]string{}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}
	rows, err := dbms.Db.Queryx(`(
		select distinct 'region', region
		from cloud_resourses cr
		where cr.account  = $1
			and is_active
	)
	union
	(
		select distinct 'service', service_name
		from cloud_resourses cr
		where cr.account  = $1
			and is_active
	)
			`,
		accountId)
	if err != nil {
		slog.Error("unable to fetch dbms", "error", err)
		return response
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("tools: failed to close rows", "error", err)
		}
	}()

	for rows.Next() {
		var resourceType, name *string
		err := rows.Scan(&resourceType, &name)
		if err != nil {
			slog.Error("unable to scan rows", "error", err)
			continue
		}
		if resourceType == nil || name == nil {
			continue
		}
		response[*resourceType] = append(response[*resourceType], *name)
	}
	if err := rows.Err(); err != nil {
		slog.Error("tools: error iterating rows", "error", err)
	}
	return response
}
