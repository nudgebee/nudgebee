// Client-side Knowledge Graph filter cascade.
//
// kg_get_filter_options ships a compact "v2" columnar payload (parallel arrays +
// small dictionaries) to keep the wire + server cache small. decodeFilterOptions
// expands it back into the maps the UI already uses; computeFilterOptionsClientSide
// then re-scopes the Node / Node-Type / Label / Attribute dropdowns as the user
// changes the Account / Node-Type filters — all offline, no backend round-trip.
//
// Scoping mirrors the backend buildNodeFilterSQL semantics off the same population:
//   - node_type     -> parts[3] of the canonical unique_key
//   - specific_type  -> nodeSpecificTypeMap[unique_key]
//   - account        -> nodeAccountMap[node_id] (raw cloud_account_id — the unique_key's
//                       own account segment is the account NAME after the server rewrite)
//   - label/attr keys -> per-key bucket coverage (a key survives iff one of its
//                       (account,node_type,specific_type) buckets is present in the scope)

// Parse a canonical 6-part KG unique key into its components.
// Format: {cloud_provider}:{account}:{location}:{NodeType}:{hierarchy}:{name}
// (see api-server/services/knowledge_graph/core/unique_key_builder.go). `name` is
// guaranteed colon-free server-side, so a positional split is safe. Returns null
// for non-canonical keys (e.g. legacy 3-part flow-source keys) so callers fall
// back to the raw string.
export const parseUniqueKey = (key) => {
  if (typeof key !== 'string') return null;
  const parts = key.split(':');
  if (parts.length !== 6) return null;
  const [provider, account, location, nodeType, hierarchy, name] = parts;
  return { provider, account, location, nodeType, hierarchy, name };
};

// Decode the columnar v2 payload (kg_get_filter_options.data) into the shapes the UI
// consumes. The columns are index-aligned; -1 indices mean "none". node_bucket_idx is
// shipped per node so the client never recomputes bucket ids (no sort-order coupling).
export function decodeFilterOptions(data) {
  const d = data || {};
  const accountIds = d.account_ids || [];
  const specificTypeDict = d.specific_type_dict || [];
  const clusterDict = d.cluster_dict || [];
  const nodeKeys = d.node_keys || [];
  const nodeIds = d.node_ids || [];
  const nodeAccountIdx = d.node_account_idx || [];
  const nodeSpecificTypeIdx = d.node_specific_type_idx || [];
  const nodeClusterIdx = d.node_cluster_idx || [];
  const nodeBucketIdx = d.node_bucket_idx || [];
  const nodeTypes = d.node_types || {}; // merged: node_type -> specific_types

  const nodeIdMap = {};
  const nodeClusterMap = {};
  const nodeSpecificTypeMap = {};
  const nodeAccountMap = {};
  const nodeBucketId = {};

  for (let i = 0; i < nodeKeys.length; i++) {
    const uk = nodeKeys[i];
    const id = nodeIds[i];
    nodeIdMap[uk] = id;
    if (nodeClusterIdx[i] >= 0) nodeClusterMap[uk] = clusterDict[nodeClusterIdx[i]];
    if (nodeSpecificTypeIdx[i] >= 0) nodeSpecificTypeMap[uk] = specificTypeDict[nodeSpecificTypeIdx[i]];
    if (nodeAccountIdx[i] >= 0) nodeAccountMap[id] = accountIds[nodeAccountIdx[i]];
    if (nodeBucketIdx[i] >= 0) nodeBucketId[uk] = nodeBucketIdx[i];
  }

  return {
    accountIds,
    nodeIdMap,
    nodeClusterMap,
    nodeSpecificTypeMap,
    nodeAccountMap,
    nodeTypeList: Object.keys(nodeTypes),
    specificTypesByNodeType: nodeTypes,
    labelKeys: d.label_keys || [],
    attributeKeys: d.attribute_keys || [],
    labelKeyBuckets: d.label_key_buckets || [],
    attributeKeyBuckets: d.attribute_key_buckets || [],
    nodeBucketId,
    lastSyncTime: d.last_sync_time || null,
    nodeCount: d.node_count ?? nodeKeys.length,
  };
}

// Filter the label/attribute key list to those present in the scoped node set.
// A key survives if any of its covered buckets is present; a key with no coverage
// (e.g. present only on inferred nodes, which the coverage query excludes) is kept —
// conservative, never wrongly hidden. Returns the [{label}] shape the dropdowns use.
function scopeKeys(keys, keyBuckets, presentBucketIds) {
  const out = [];
  for (let i = 0; i < keys.length; i++) {
    const bucketList = keyBuckets[i] || [];
    if (bucketList.length === 0 || bucketList.some((t) => presentBucketIds.has(t))) {
      out.push({ label: keys[i] });
    }
  }
  return out;
}

// Derive the scoped dropdown data from the decoded baseline and the current draft
// selection. Returns plain data only — the caller formats the Node-Type options via
// buildNodeTypeOptions(nodeTypeList, specificTypesByNodeType), so this stays free of
// display concerns and unit-testable.
//
// baseline: initialKgFilterOptionsRef.current (decoded — nodeIdMap, nodeClusterMap,
//   nodeSpecificTypeMap, nodeAccountMap, nodeBucketId, labelKeys/attributeKeys + buckets).
// selection: { accountIds: string[], broadNodeTypes: string[], specificTypes: string[] }
export function computeFilterOptionsClientSide(baseline, { accountIds = [], broadNodeTypes = [], specificTypes = [] } = {}) {
  const nodeIdMap = baseline?.nodeIdMap || {};
  const nodeClusterMap = baseline?.nodeClusterMap || {};
  const nodeSpecificTypeMap = baseline?.nodeSpecificTypeMap || {};
  const nodeAccountMap = baseline?.nodeAccountMap || {};
  const nodeBucketId = baseline?.nodeBucketId || {};

  const accountSet = new Set(accountIds || []);
  const broadSet = new Set(broadNodeTypes || []);
  const specificSet = new Set(specificTypes || []);
  const hasAccount = accountSet.size > 0;
  const hasType = broadSet.size > 0 || specificSet.size > 0;

  const scopedNodeIdMap = {};
  const scopedClusterMap = {};
  const scopedSpecificTypeMap = {};
  // The Node-Type dropdown is scoped by account only (not by the type filter itself),
  // so it keeps listing every type present in the selected accounts instead of
  // collapsing to the user's current pick.
  const nodeTypeSet = new Set();
  const specificByType = {};
  // Label/attribute keys scope to the fully-filtered (account AND type) node set,
  // matching the backend's filtered key queries.
  const presentBucketIds = new Set();

  // One pass, one parseUniqueKey + one node-id lookup per node. Account match uses the
  // node_id -> cloud_account_id value (the raw UUID the backend filters on), never the
  // key's (rewritten, name-bearing) account segment.
  Object.keys(nodeIdMap).forEach((uniqueKey) => {
    const nodeId = nodeIdMap[uniqueKey];
    if (hasAccount && !accountSet.has(nodeAccountMap[nodeId])) return;

    const nt = parseUniqueKey(uniqueKey)?.nodeType;
    const st = nodeSpecificTypeMap[uniqueKey];
    if (nt) {
      nodeTypeSet.add(nt);
      if (st) {
        if (!specificByType[nt]) specificByType[nt] = new Set();
        specificByType[nt].add(st);
      }
    }

    if (hasType && !((nt && broadSet.has(nt)) || specificSet.has(st))) return;
    scopedNodeIdMap[uniqueKey] = nodeId;
    if (nodeClusterMap[uniqueKey]) scopedClusterMap[uniqueKey] = nodeClusterMap[uniqueKey];
    if (st) scopedSpecificTypeMap[uniqueKey] = st;
    const tid = nodeBucketId[uniqueKey];
    if (tid !== undefined) presentBucketIds.add(tid);
  });

  const nodeTypeList = Array.from(nodeTypeSet).sort((a, b) => a.localeCompare(b));
  const specificTypesByNodeType = {};
  Object.keys(specificByType).forEach((nt) => {
    specificTypesByNodeType[nt] = Array.from(specificByType[nt]).sort((a, b) => a.localeCompare(b));
  });

  return {
    nodeIdMap: scopedNodeIdMap,
    nodeClusterMap: scopedClusterMap,
    nodeSpecificTypeMap: scopedSpecificTypeMap,
    nodeTypeList,
    specificTypesByNodeType,
    labelMap: scopeKeys(baseline?.labelKeys || [], baseline?.labelKeyBuckets || [], presentBucketIds),
    attributeMap: scopeKeys(baseline?.attributeKeys || [], baseline?.attributeKeyBuckets || [], presentBucketIds),
  };
}
