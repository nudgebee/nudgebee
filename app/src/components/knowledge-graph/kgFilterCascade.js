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

const splitPascalCase = (type) =>
  (type || '')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .replace(/([0-9])([A-Z][a-z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2');

// Product names that are PascalCase themselves stay whole after the split
// ("DynamoDBTable" → "DynamoDB Table", not "Dynamo DB Table").
const PRODUCT_NAMES = [
  'BigQuery',
  'CloudFormation',
  'CloudFront',
  'CloudTrail',
  'CloudWatch',
  'DynamoDB',
  'ElastiCache',
  'GitHub',
  'GitLab',
  'GuardDuty',
  'OpenSearch',
  'PagerDuty',
  'SageMaker',
];
const PRODUCT_NAME_REJOINS = PRODUCT_NAMES.map((name) => [new RegExp(`\\b${splitPascalCase(name)}\\b`, 'g'), name]);

// PascalCase specific type → spaced label ("SQSQueue" → "SQS Queue",
// "EC2Instance" → "EC2 Instance"). The type picker and the Knowledge Graph's
// Node Type filter and node cards all show these labels.
export const humanizeType = (type) => PRODUCT_NAME_REJOINS.reduce((label, [re, name]) => label.replace(re, name), splitPascalCase(type));

// The selectable leaves for a given node_type: its known specific_types, or (when none are
// known yet — no ontology data, or not backfilled) a single leaf identical to the node_type
// itself, so every node type stays selectable and accounted for either way.
export function getNodeTypeLeaves(nodeType, specificTypesByNodeType) {
  const specificTypes = specificTypesByNodeType?.[nodeType];
  return specificTypes?.length ? specificTypes : [nodeType];
}

// Builds the grouped Node Type filter options: one leaf per specific_type, grouped by its
// broad node_type. A node_type whose only leaf is identical to itself (no ontology mapping,
// or the AWS/GCP/Azure specific_type extractors' own fallback-to-node_type default) has
// nothing to drill into, so it's left ungrouped rather than rendered as a group header with
// one child of the same name — FilterDropdown buckets ungrouped options under "Other".
// Labels and group headers are spaced ("RDS Instance", "Compute Instance"); `searchText` keeps
// the raw PascalCase leaf so typing "RDSInstance" still finds it, and real leaves add their broad
// (ontological) type too — e.g. searching "compute" surfaces EC2Instance/GCEInstance/etc. even
// though none of those specific-type labels themselves contain "compute".
export function buildNodeTypeOptions(nodeTypes, specificTypesByNodeType) {
  return (nodeTypes || []).flatMap((nodeType) => {
    const leaves = getNodeTypeLeaves(nodeType, specificTypesByNodeType);
    const hasRealBreakdown = !(leaves.length === 1 && leaves[0] === nodeType);
    const group = humanizeType(nodeType);
    return leaves.map((leaf) => ({
      label: humanizeType(leaf),
      value: leaf,
      ...(hasRealBreakdown ? { group, searchText: `${leaf} ${nodeType} ${group}` } : { searchText: leaf }),
    }));
  });
}

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

// Brand prefix each provider's specific_types carry (KubernetesConfigMap, AzureKeyVault,
// GCPServiceAccount, …), keyed by the unique_key's provider segment. AWS types carry no
// "AWS" prefix (EC2Instance, RDSInstance) so aws is absent.
const PROVIDER_TYPE_PREFIX = {
  k8s: 'Kubernetes',
  azure: 'Azure',
  gcp: 'GCP',
  github: 'GitHub',
  gitlab: 'GitLab',
  pagerduty: 'PagerDuty',
  selfhosted: 'SelfHosted',
};

// Type label for a node row's badge: the specific_type (or node_type) humanized from
// PascalCase, with the provider's brand prefix dropped when the row renders that
// provider's icon — the icon already says "Kubernetes", so repeating it in the badge
// only crowds out the part that identifies the resource (Config Map, Service Account).
// `hasIcon` false (unknown provider, or Nudgebee's own `external` identity nodes) keeps
// the prefix, since then nothing else on the row names the source.
export const formatTypeBadge = (type, provider, hasIcon) => {
  let label = type || '';
  const prefix = PROVIDER_TYPE_PREFIX[provider];
  // Only strip on a PascalCase word boundary, and never down to an empty label.
  if (hasIcon && prefix && label.startsWith(prefix) && /^[A-Z]/.test(label.slice(prefix.length))) {
    label = label.slice(prefix.length);
  }
  return label.replace(/([a-z])([A-Z])/g, '$1 $2');
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
  // Sparse, not index-aligned: display_name_node_idx[n] is an index into node_keys,
  // display_names[n] its label. Only identity nodes (UserAccount, keyed on the login)
  // carry one — see FilterOptions.DisplayNames in api-server.
  const displayNameNodeIdx = d.display_name_node_idx || [];
  const displayNames = d.display_names || [];
  const nodeTypes = d.node_types || {}; // merged: node_type -> specific_types

  const nodeIdMap = {};
  const nodeClusterMap = {};
  const nodeSpecificTypeMap = {};
  const nodeAccountMap = {};
  const nodeDisplayNameMap = {};
  const nodeBucketId = {};
  // account id -> { provider, name } read off the first canonical key the account owns.
  // The server rewrites the key's account segment to the account / integration name.
  const accountSources = {};

  for (let i = 0; i < nodeKeys.length; i++) {
    const uk = nodeKeys[i];
    const id = nodeIds[i];
    nodeIdMap[uk] = id;
    if (nodeClusterIdx[i] >= 0) nodeClusterMap[uk] = clusterDict[nodeClusterIdx[i]];
    if (nodeSpecificTypeIdx[i] >= 0) nodeSpecificTypeMap[uk] = specificTypeDict[nodeSpecificTypeIdx[i]];
    if (nodeAccountIdx[i] >= 0) {
      const accountId = accountIds[nodeAccountIdx[i]];
      nodeAccountMap[id] = accountId;
      if (!accountSources[accountId]) {
        const parsed = parseUniqueKey(uk);
        if (parsed?.account) accountSources[accountId] = { provider: parsed.provider, name: parsed.account };
      }
    }
    if (nodeBucketIdx[i] >= 0) nodeBucketId[uk] = nodeBucketIdx[i];
  }

  for (let n = 0; n < displayNameNodeIdx.length; n++) {
    const uk = nodeKeys[displayNameNodeIdx[n]];
    if (uk && displayNames[n]) nodeDisplayNameMap[uk] = displayNames[n];
  }

  return {
    accountIds,
    nodeIdMap,
    nodeClusterMap,
    nodeSpecificTypeMap,
    nodeAccountMap,
    nodeDisplayNameMap,
    accountSources,
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

// Account filter group for the tenant-scoped integrations, whose ids are integration ids,
// not cloud accounts (see TenantScopedSource in knowledge_graph/core/service.go).
const INTEGRATION_ACCOUNT_GROUP = { github: 'GitHub', gitlab: 'GitLab', pagerduty: 'PagerDuty' };

// Label of the entry for nodes owned by the tenant itself: users, groups and nodes
// declared outside any account. Matches the subtitle those nodes carry on the canvas.
export const TENANT_ACCOUNT_LABEL = 'Not in a connected account';

// Account filter options: every account id that owns nodes. Connected cloud and Kubernetes
// accounts come from the accounts list; the rest (GitHub / GitLab / PagerDuty integrations,
// the tenant's own id) are named from the node keys they own.
export function buildAccountOptions(accounts, accountIds, accountSources, tenantId) {
  const ids = Array.isArray(accountIds) ? accountIds : [];
  const idsSet = new Set(ids);
  const known = new Set();
  const options = (accounts || [])
    .filter((ac) => idsSet.has(ac.id))
    .map((acc) => {
      known.add(acc.id);
      return { label: acc.label || acc.account_name, value: acc.id || acc.value, group: acc.cloud_provider || 'Other' };
    });
  ids
    .filter((id) => !known.has(id))
    .forEach((id) => {
      const source = accountSources?.[id];
      if (id === tenantId || source?.provider === 'external') {
        options.push({ label: TENANT_ACCOUNT_LABEL, value: id, group: 'Other', searchText: 'users groups' });
        return;
      }
      options.push({ label: source?.name || id, value: id, group: INTEGRATION_ACCOUNT_GROUP[source?.provider] || 'Other' });
    });
  return options;
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
  const nodeDisplayNameMap = baseline?.nodeDisplayNameMap || {};
  const nodeBucketId = baseline?.nodeBucketId || {};

  const accountSet = new Set(accountIds || []);
  const broadSet = new Set(broadNodeTypes || []);
  const specificSet = new Set(specificTypes || []);
  const hasAccount = accountSet.size > 0;
  const hasType = broadSet.size > 0 || specificSet.size > 0;

  const scopedNodeIdMap = {};
  const scopedClusterMap = {};
  const scopedSpecificTypeMap = {};
  const scopedDisplayNameMap = {};
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
    if (nodeDisplayNameMap[uniqueKey]) scopedDisplayNameMap[uniqueKey] = nodeDisplayNameMap[uniqueKey];
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
    nodeDisplayNameMap: scopedDisplayNameMap,
    nodeTypeList,
    specificTypesByNodeType,
    labelMap: scopeKeys(baseline?.labelKeys || [], baseline?.labelKeyBuckets || [], presentBucketIds),
    attributeMap: scopeKeys(baseline?.attributeKeys || [], baseline?.attributeKeyBuckets || [], presentBucketIds),
  };
}
