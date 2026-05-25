package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// uuidRegex detects the canonical 8-4-4-4-12 UUID form. Used to decide whether
// a value passed as account_ids is already a UUID (pass through) or a friendly
// name that needs lookup against cloud_accounts.
var uuidRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// fetchTenantAccountMap returns uuid → friendly account_name for every
// cloud_account in the caller's tenant. Tenant id is taken from the
// RequestContext first (no DB hit); only when the context is empty do we fall
// back to looking up cloud_accounts.id = accountId. Fails fast on an empty
// tenantId per the multi-tenant rules — every query downstream is scoped by it.
//
// The kg_search_nodes path calls this once and reuses the result for both
// account-name resolution (resolveAccountIdentifiers) and Account-column
// rendering, so we only hit cloud_accounts a single time per tool call.
func fetchTenantAccountMap(ctx security.RequestContext, accountId string) (map[string]string, error) {
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if tenantId == "" {
		t, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return nil, fmt.Errorf("fetchTenantAccountMap: resolving tenant from account %s: %w", accountId, err)
		}
		tenantId = t
	}
	if tenantId == "" {
		return nil, errors.New("fetchTenantAccountMap: tenantId is empty")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("fetchTenantAccountMap: db manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(
		`SELECT id::text, account_name FROM cloud_accounts WHERE tenant = $1 AND account_name IS NOT NULL`,
		tenantId,
	)
	if err != nil {
		return nil, fmt.Errorf("fetchTenantAccountMap: query cloud_accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("fetchTenantAccountMap: scan: %w", err)
		}
		out[id] = name
	}
	return out, nil
}

// resolveAccountIdentifiers maps a mixed list of UUIDs and friendly account
// names to canonical UUIDs. Pure UUIDs pass through; names are looked up in the
// supplied accountMap (uuid → name, typically produced by fetchTenantAccountMap)
// matched case-insensitively because users type "AWS-Demo", "aws-demo",
// "Aws-demo" interchangeably. Returns an error listing every name that did not
// match — the LLM sees that text and can course-correct without firing more
// tool calls. Callers MUST supply a tenant-scoped map; passing a map from a
// different tenant would let the LLM resolve cross-tenant ids.
func resolveAccountIdentifiers(identifiers []string, accountMap map[string]string) ([]string, error) {
	if len(identifiers) == 0 {
		return identifiers, nil
	}

	// Partition into "looks like a UUID" vs "treat as a name".
	resolved := make([]string, 0, len(identifiers))
	var names []string
	for _, v := range identifiers {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if uuidRegex.MatchString(v) {
			resolved = append(resolved, v)
		} else {
			names = append(names, v)
		}
	}

	if len(names) == 0 {
		return resolved, nil
	}

	// Build name→id (case-insensitive) from the caller-supplied tenant map.
	nameToId := make(map[string]string, len(accountMap))
	allNames := make([]string, 0, len(accountMap))
	for id, name := range accountMap {
		if name == "" {
			continue
		}
		nameToId[strings.ToLower(name)] = id
		allNames = append(allNames, name)
	}

	var unresolved []string
	for _, n := range names {
		if id, ok := nameToId[strings.ToLower(n)]; ok {
			resolved = append(resolved, id)
		} else {
			unresolved = append(unresolved, n)
		}
	}

	if len(unresolved) > 0 {
		// Surface the available account names so the LLM can correct itself
		// in-flight without another tool call.
		sort.Strings(allNames)
		return nil, fmt.Errorf(
			"invalid account name(s): %s. Available account names for this tenant: %s. "+
				"Use one of the listed names exactly (case-insensitive) OR pass the canonical UUID",
			strings.Join(unresolved, ", "),
			strings.Join(allNames, ", "),
		)
	}

	return resolved, nil
}

// KG response character cap for the `Data` field. Raw JSON goes in AdditionalDetails
// so the LLM can still reason over full payload when needed but the default context
// remains compact.
//
// Two separate caps:
//   - kgDataCharCap (2000): used by kg_search_nodes which renders a small markdown
//     table with one row per node — 20 nodes typically fit in <2KB.
//   - kgTraverseDataCharCap (10000): used by kg_traverse which renders one line per
//     edge, and edges easily exceed 100 chars apiece. A 21-edge response was hitting
//     the 2KB cap, and the truncation combined with the header's "Found N edges" line
//     pushed the LLM to fabricate the missing edges from training-data patterns.
//     10KB fits roughly 80+ typical CALLS edges; beyond that the truncation footer
//     fires with the explicit anti-hallucination guidance below.
const kgDataCharCap = 2000
const kgTraverseDataCharCap = 10000

const (
	kgTruncatedFooter = "\n[output truncated — see additional_details for full JSON]\n"
	// kgEdgeTruncatedFooter fires specifically when the kg_traverse edge list is cut
	// mid-stream. It must be explicit about NOT fabricating the remaining items —
	// the LLM has been observed inventing plausible-looking workload names to fill
	// the gap between the visible rows and the "Found N edges" count in the header.
	kgEdgeTruncatedFooter = "\n[edge list truncated — only the first %d of %d edges are shown above. " +
		"DO NOT extrapolate, infer, or invent the remaining %d edges from training-data patterns. " +
		"In your final answer, state explicitly that only %d of %d edges were visible and either " +
		"refer the user to additional_details for the full list or run a narrower follow-up traversal.]\n"
	kgKVLineFmt = "- %s: %s\n"
)

// doKGActionRequest sends a Hasura action request to the api-server's
// /rpc/knowledge-graph endpoint. It mirrors services_server.QueryLogs:
// same envelope shape, same header set (tenant/user as HTTP headers, not
// session_variables), same error handling.
func doKGActionRequest(ctx security.RequestContext, actionName, accountId string, request map[string]any) (map[string]any, error) {
	queryPayload := map[string]any{
		"action": map[string]any{
			"name": actionName,
		},
		"input": map[string]any{
			"request": request,
		},
	}

	tenant := ctx.GetSecurityContext().GetTenantId()
	if tenant == "" {
		t, err := security.GetTenantIdFromAccountId(accountId)
		if err != nil {
			return nil, fmt.Errorf("kg: resolving tenant from account %s: %w", accountId, err)
		}
		tenant = t
	}
	if tenant == "" {
		return nil, errors.New("kg: tenant id is empty")
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s/rpc/knowledge-graph", config.Config.ServiceEndpoint),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   "application/json",
			"Accept":         "application/json",
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    tenant,
			"x-user-id":      ctx.GetSecurityContext().GetUserId(),
		}),
		common.HttpWithJsonBody(queryPayload),
	)
	if err != nil {
		return nil, fmt.Errorf("kg: %s, unable to process request: %w", actionName, err)
	}
	defer func() {
		if resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				slog.Info("kg: failed to close response body", "error", cerr)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kg: %s, reading response body: %w", actionName, err)
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("kg: %s unauthorized: %s", actionName, string(jsonBody))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("kg: %s internal server error from api-server: %s", actionName, string(jsonBody))
	}

	trimmed := bytes.TrimLeft(jsonBody, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("kg: %s unexpected response shape: %s", actionName, string(jsonBody))
	}

	var envelope map[string]any
	if err := common.UnmarshalJson(jsonBody, &envelope); err != nil {
		return nil, fmt.Errorf("kg: %s unmarshal response: %w", actionName, err)
	}

	// Hasura-action error shape: {"message":"..."}
	if msg, ok := envelope["message"].(string); ok && msg != "" {
		return nil, fmt.Errorf("kg: %s error: %s", actionName, msg)
	}

	// Success shape: {"data": <payload>}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("kg: %s missing data field in response: %s", actionName, string(jsonBody))
	}
	return data, nil
}

// accountDisplay renders a cloud_account_id as a friendly label.
// Prefers "<account_name>" when known, falls back to the raw UUID for
// unknown accounts. The UUID itself is no longer shown in the table column
// — it's the same column the LLM uses to drive `account_ids` calls and we
// want it to learn to use names there too. The full UUID list still lives
// in AdditionalDetails for any LLM that needs it.
func accountDisplay(accountId string, nameMap map[string]string) string {
	if accountId == "" {
		return ""
	}
	if name, ok := nameMap[accountId]; ok && name != "" {
		return name
	}
	return accountId
}

// formatKGSearchResponse formats kg_search_nodes results as a concise markdown
// table. Node IDs are preserved so the LLM can chain into kg_traverse.
//
// Expected shape (from SearchNodesResponse):
//
//	{"nodes":[{"id":..,"name":..,"node_type":..,"namespace":..,"source":..,...}],"total_count":N}
//
// `accountNames` maps cloud_account_id → friendly account_name. When provided,
// the Account column shows names instead of UUIDs (UUIDs remain available in
// AdditionalDetails). Pass nil to fall back to UUID rendering.
func formatKGSearchResponse(data map[string]any, accountNames map[string]string) string {
	nodesRaw, _ := data["nodes"].([]any)
	total := intFromAny(data["total_count"])

	if len(nodesRaw) == 0 {
		return "No nodes matched the search criteria. This is an AUTHORITATIVE answer for the scope you queried — that combination of (query, node_types, namespace, source, account_ids) genuinely has no matching nodes in the Knowledge Graph. " +
			"DO NOT retry the same query with synonymous name variations (e.g. cycling through %db%, %mysql%, %postgres%, %pg%, %maria% in the hope of finding a database) — that loop is unproductive and inflates latency. " +
			"Allowed next step: at most ONE broader retry that drops a single filter (e.g. omit namespace, drop node_types, or widen the source). If the broader retry is also empty, STATE in your final answer that no matching nodes were found in this scope and move on; do not loop further. " +
			"Reminder: log-derived strings (from fetch_logs, etc.) are NOT KG entities — do not search the KG repeatedly for names you only saw in logs."
	}

	var b strings.Builder
	if total > len(nodesRaw) {
		fmt.Fprintf(&b, "Found %d nodes (showing %d):\n\n", total, len(nodesRaw))
	} else {
		fmt.Fprintf(&b, "Found %d nodes:\n\n", len(nodesRaw))
	}
	b.WriteString("| ID | Name | Type | Namespace | Source | Account |\n")
	b.WriteString("|----|------|------|-----------|--------|---------|\n")

	for _, n := range nodesRaw {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			stringFromAny(node["id"]),
			stringFromAny(node["name"]),
			stringFromAny(node["node_type"]),
			stringFromAny(node["namespace"]),
			stringFromAny(node["source"]),
			accountDisplay(stringFromAny(node["cloud_account_id"]), accountNames),
		)
		if b.Len() > kgDataCharCap {
			b.WriteString(kgTruncatedFooter)
			return b.String()
		}
	}
	return b.String()
}

// formatKGTraverseResponse formats kg_traverse results with a node-type summary
// and a list of key relationship chains. IDs are included inline so the LLM can
// chain into additional traversals.
//
// Expected shape (from TraverseResponse):
//
//	{"data":{"nodes":[{id,kind,name,...}],"edges":[{source_node_id,dest_node_id,relationship_type}]},
//	 "seed_node_ids":[...], "truncated":bool, "total_discovered":int}
func formatKGTraverseResponse(data map[string]any, appliedExcludeTypes []string, appliedResultLimit int) string {
	graph, _ := data["data"].(map[string]any)
	nodesRaw, _ := graph["nodes"].([]any)
	edgesRaw, _ := graph["edges"].([]any)
	seedIDs, _ := data["seed_node_ids"].([]any)
	truncated, _ := data["truncated"].(bool)
	totalDiscovered := intFromAny(data["total_discovered"])

	if len(nodesRaw) == 0 {
		return "No nodes matched. The seed search returned no nodes — the named resource(s) do not exist in this scope of the Knowledge Graph. " +
			"This is an AUTHORITATIVE answer; do NOT retry with cycled synonyms or wildcards in the hope of finding the resource. " +
			"Allowed next step: ONE broader retry — drop or widen ONE filter (node_types, namespace, account_ids), then accept the result. " +
			"If still empty, state the absence in your final answer instead of looping."
	}

	// Build id → node map for chain resolution.
	nodeByID := make(map[string]map[string]any, len(nodesRaw))
	countsByKind := map[string]int{}
	for _, n := range nodesRaw {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		id := stringFromAny(node["id"])
		if id != "" {
			nodeByID[id] = node
		}
		countsByKind[stringFromAny(node["kind"])]++
	}

	var b strings.Builder

	// Header: seed + counts
	seedNames := []string{}
	for _, s := range seedIDs {
		id := stringFromAny(s)
		if node, ok := nodeByID[id]; ok {
			seedNames = append(seedNames, fmt.Sprintf("%s [id: %s] (%s)",
				stringFromAny(node["name"]), id, stringFromAny(node["kind"])))
		} else {
			seedNames = append(seedNames, fmt.Sprintf("[id: %s]", id))
		}
	}
	if len(seedNames) > 0 {
		fmt.Fprintf(&b, "Traversal from %s\n", strings.Join(seedNames, ", "))
	} else {
		b.WriteString("Traversal:\n")
	}

	// Counts by kind — sort for deterministic output.
	kinds := make([]string, 0, len(countsByKind))
	for k := range countsByKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	countParts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		countParts = append(countParts, fmt.Sprintf("%d %s", countsByKind[k], k))
	}
	fmt.Fprintf(&b, "Found: %d nodes (%s), %d edges\n", len(nodesRaw), strings.Join(countParts, ", "), len(edgesRaw))

	// Truncation / applied filters — visible so the LLM can decide whether to refine.
	if truncated {
		fmt.Fprintf(&b, "Truncated: true (total_matches=%d, returned=%d, result_limit=%d) — refine the query before acting.\n",
			totalDiscovered, len(nodesRaw), appliedResultLimit)
	} else {
		b.WriteString("Truncated: false\n")
	}
	if len(appliedExcludeTypes) > 0 {
		fmt.Fprintf(&b, "Applied filters: exclude_node_types=[%s]\n", strings.Join(appliedExcludeTypes, ", "))
	}

	// Relationship chains — render unique (src,rel,dst) triples with inline IDs.
	// Uses the bigger kgTraverseDataCharCap so a typical 20–30 edge response fits
	// without truncation; truncation here historically led to LLM hallucination
	// (see the cap constant doc above).
	if len(edgesRaw) > 0 {
		b.WriteString("\nRelationships:\n")
		for i, e := range edgesRaw {
			edge, ok := e.(map[string]any)
			if !ok {
				continue
			}
			srcID := stringFromAny(edge["source_node_id"])
			dstID := stringFromAny(edge["dest_node_id"])
			rel := stringFromAny(edge["relationship_type"])

			srcName, srcKind := nameKindFor(nodeByID, srcID)
			dstName, dstKind := nameKindFor(nodeByID, dstID)

			fmt.Fprintf(&b, "- %s (%s) [id: %s] → %s → %s (%s) [id: %s]\n",
				srcName, srcKind, srcID,
				rel,
				dstName, dstKind, dstID,
			)
			if b.Len() > kgTraverseDataCharCap {
				shown := i + 1
				total := len(edgesRaw)
				remaining := total - shown
				fmt.Fprintf(&b, kgEdgeTruncatedFooter, shown, total, remaining, shown, total)
				return b.String()
			}
		}
	}

	return b.String()
}

func nameKindFor(nodeByID map[string]map[string]any, id string) (name, kind string) {
	if id == "" {
		return "?", "?"
	}
	if node, ok := nodeByID[id]; ok {
		return stringFromAny(node["name"]), stringFromAny(node["kind"])
	}
	return "?", "?"
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// formatKGGetNodeResponse formats a kg_get_node result (a single KgNode payload)
// as a compact markdown block with metadata, labels, and properties. Falls back
// to "Node not found." when the response is empty (the api-server's 404 path
// surfaces as an empty / id-less map after envelope unwrap).
//
// Expected shape (from KgNode):
//
//	{"id":..,"name":..,"node_type":..,"namespace":..,"cluster":..,"source":..,
//	 "cloud_account_id":..,"category":..,"level":..,"labels":{..},"properties":{..}}
func formatKGGetNodeResponse(node map[string]any) string {
	id := stringFromAny(node["id"])
	if id == "" {
		return "Node not found."
	}

	var b strings.Builder
	writeKGNodeHeader(&b, node, id)
	writeKGNodeMetadata(&b, node)
	if writeKGNodeMap(&b, "Labels", node["labels"], stringFromAny) {
		return b.String()
	}
	if writeKGNodeMap(&b, "Properties", node["properties"], formatPropertyValue) {
		return b.String()
	}
	return b.String()
}

func writeKGNodeHeader(b *strings.Builder, node map[string]any, id string) {
	name := stringFromAny(node["name"])
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(b, "**%s** (%s) — id: %s\n", name, stringFromAny(node["node_type"]), id)
}

var kgNodeMetaPairs = []struct {
	label string
	key   string
}{
	{"Namespace", "namespace"},
	{"Cluster", "cluster"},
	{"Source", "source"},
	{"Account", "cloud_account_id"},
	{"Category", "category"},
	{"Level", "level"},
}

func writeKGNodeMetadata(b *strings.Builder, node map[string]any) {
	for _, p := range kgNodeMetaPairs {
		v := stringFromAny(node[p.key])
		if v != "" {
			fmt.Fprintf(b, kgKVLineFmt, p.label, v)
		}
	}
}

// writeKGNodeMap renders a sorted key/value section ("Labels:", "Properties:")
// using the supplied value formatter. Returns true if the soft cap was tripped
// and the caller should stop appending further sections.
func writeKGNodeMap(b *strings.Builder, sectionTitle string, raw any, render func(any) string) bool {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return false
	}
	fmt.Fprintf(b, "\n%s:\n", sectionTitle)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, kgKVLineFmt, k, render(m[k]))
		if b.Len() > kgDataCharCap {
			b.WriteString(kgTruncatedFooter)
			return true
		}
	}
	return false
}

// formatPropertyValue renders a KG property value inline. Scalars become their
// natural string form; nested maps and arrays are JSON-marshalled compactly so
// the LLM can still reason over them without losing structure.
func formatPropertyValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		// JSON numbers unmarshal to float64; render integers without ".0".
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	if buf, err := common.MarshalJson(v); err == nil {
		return string(buf)
	}
	return fmt.Sprintf("%v", v)
}
