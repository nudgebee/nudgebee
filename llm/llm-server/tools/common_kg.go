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
	"sort"
	"strconv"
	"strings"
)

// KG response character cap for the `Data` field. Raw JSON goes in AdditionalDetails
// so the LLM can still reason over full payload when needed but the default context
// remains compact.
const kgDataCharCap = 2000

const (
	kgTruncatedFooter = "\n[output truncated — see additional_details for full JSON]\n"
	kgKVLineFmt       = "- %s: %s\n"
)

// doKGActionRequest sends a Hasura action request to the api-server's
// /hasura/knowledge-graph endpoint. It mirrors services_server.QueryLogs:
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
		fmt.Sprintf("%s/hasura/knowledge-graph", config.Config.ServiceEndpoint),
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

// formatKGSearchResponse formats kg_search_nodes results as a concise markdown
// table. Node IDs are preserved so the LLM can chain into kg_traverse.
//
// Expected shape (from SearchNodesResponse):
//
//	{"nodes":[{"id":..,"name":..,"node_type":..,"namespace":..,"source":..,...}],"total_count":N}
func formatKGSearchResponse(data map[string]any) string {
	nodesRaw, _ := data["nodes"].([]any)
	total := intFromAny(data["total_count"])

	if len(nodesRaw) == 0 {
		return "No nodes matched the search criteria. Try broadening the query (drop node_types, omit namespace, use a name pattern with %)."
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
			stringFromAny(node["cloud_account_id"]),
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
		return "No nodes matched. Seed search returned no nodes; verify the query/name/namespace or relax node_types."
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
	if len(edgesRaw) > 0 {
		b.WriteString("\nRelationships:\n")
		for _, e := range edgesRaw {
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
			if b.Len() > kgDataCharCap {
				b.WriteString("\n[output truncated — see additional_details for full JSON]\n")
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
