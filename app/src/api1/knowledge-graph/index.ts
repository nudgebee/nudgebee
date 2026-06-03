// Knowledge Graph API wrappers.
//
// Phase 2 (NB-30989) introduces this dedicated module so all kg_* actions
// live in one place. Existing kg_* wrappers that previously lived in
// `apiKubernetes1` are re-exported here verbatim — apiKubernetes1 keeps a
// thin shim so existing call sites continue to work.
//
// The new Manual Dependencies wrappers (last block) are gated on the
// Phase 1 backend (PR #31253). Three are wired up by the Phase 2 UI; the
// remaining six are scaffolded so Phase 2.5 can wire edit / delete /
// reresolve / panic without another API-layer churn.

import { gqlStringify, queryGraphQL } from '@lib/HttpService';

// -- Types ------------------------------------------------------------------

export type ManualDependencyResolutionStatus =
  | 'pending'
  | 'resolved'
  | 'source_unmatched'
  | 'dest_unmatched'
  | 'source_ambiguous'
  | 'dest_ambiguous'
  | 'source_too_many_matches'
  | 'dest_too_many_matches'
  | 'invalid_payload'
  | 'node_inactive';

export type ManualDependencyRelationshipType = 'CALLS' | 'PUBLISHES_TO' | 'SUBSCRIBES_TO';

export interface ManualMatchCandidate {
  node_id: string;
  node_type: string;
  namespace?: string;
  cluster?: string;
  arn?: string;
  display_name: string;
}

export interface ManualDependency {
  id: number;
  tenant_id: string;
  source_node_type: string;
  source_name: string;
  source_namespace?: string;
  source_cluster?: string;
  source_arn?: string;
  source_account_id?: string;
  source_region?: string;
  dest_node_type: string;
  dest_name: string;
  dest_namespace?: string;
  dest_cluster?: string;
  dest_arn?: string;
  dest_account_id?: string;
  dest_region?: string;
  relationship_type: ManualDependencyRelationshipType;
  notes?: string;
  declared_by_user_id?: string;
  resolved_source_node_id?: string;
  resolved_dest_node_id?: string;
  resolution_status: ManualDependencyResolutionStatus;
  resolution_error?: string;
  source_match_count?: number;
  dest_match_count?: number;
  source_match_candidates?: ManualMatchCandidate[];
  dest_match_candidates?: ManualMatchCandidate[];
  last_resolved_at?: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface CsvImportRowResult {
  row_index: number;
  id?: number;
  status?: ManualDependencyResolutionStatus;
  match_count?: number;
  error?: string;
  dependency?: ManualDependency;
}

export interface CsvImportResult {
  imported: CsvImportRowResult[];
  rejected: CsvImportRowResult[];
}

// -- GraphQL queries / mutations -------------------------------------------

const KG_GET_COMPLETE_GRAPH = `
mutation KnowledgeGraph {
  kg_get_complete_graph(request: __WHERE__) {
    data {
      edges
      generated_at
      nodes
      tenant_id
    }
  }
}`;

const KG_GET_FILTER_OPTIONS = `
query KgFilterOptions {
  kg_get_filter_options(request: __WHERE__) {
    data {
      account_ids
      attribute_keys
      label_keys
      node_types
      last_sync_time
      node_id_map
      node_count
    }
  }
}`;

const KG_GET_FILTER_VALUES = `
query KgFilterOptionLabelValues {
  kg_get_filter_values(request: __WHERE__) {
    data {
      filter_key
      filter_type
      values
    }
  }
}`;

const KG_GET_NODE = `
query KgGetNode {
  kg_get_node(request: __WHERE__) {
    data
  }
}`;

const KG_GET_EDGE = `
query KgGetEdge {
  kg_get_edge(request: __WHERE__) {
    edge
  }
}`;

const KG_GET_CLOUD_ACCOUNTS = `
query KgGetCloudAccounts {
  cloud_accounts: accounts_list(where: {status: {_eq: "active"}}) {
    rows {
      id
      account_name
      account_number
      cloud_provider
      created_at
    }
  }
}`;

const KG_GET_TENANT_FILTER = `
query KgGetTenantFilter {
  kg_get_tenant_filter {
    exists
    id
    account_ids
    flow_sources
    enabled
  }
}`;

const KG_UPSERT_TENANT_FILTER = `
mutation KgUpsertTenantFilter {
  kg_upsert_tenant_filter(request: __WHERE__) {
    id
    removed_accounts
    removed_flow_sources
    message
  }
}`;

// Manual Dependencies (Phase 2)

const KG_LIST_MANUAL_DEPENDENCIES = `
query KgListManualDependencies {
  kg_list_manual_dependencies(request: __WHERE__) {
    data
    count
  }
}`;

const KG_CREATE_MANUAL_DEPENDENCY = `
mutation KgCreateManualDependency {
  kg_create_manual_dependency(request: __WHERE__) {
    data
  }
}`;

const KG_UPDATE_MANUAL_DEPENDENCY = `
mutation KgUpdateManualDependency {
  kg_update_manual_dependency(request: __WHERE__) {
    data
  }
}`;

const KG_DELETE_MANUAL_DEPENDENCY = `
mutation KgDeleteManualDependency {
  kg_delete_manual_dependency(request: __WHERE__) {
    data
  }
}`;

const KG_IMPORT_MANUAL_DEPENDENCIES = `
mutation KgImportManualDependencies {
  kg_import_manual_dependencies(request: __WHERE__) {
    data
  }
}`;

const KG_RESOLVE_MANUAL_DEPENDENCY = `
mutation KgResolveManualDependency {
  kg_resolve_manual_dependency(request: __WHERE__) {
    data
  }
}`;

const KG_RERESOLVE_MANUAL_DEPENDENCY = `
mutation KgReresolveManualDependency {
  kg_reresolve_manual_dependency(request: __WHERE__) {
    data
  }
}`;

const KG_RERESOLVE_MANUAL_DEPENDENCIES = `
mutation KgReresolveManualDependencies {
  kg_reresolve_manual_dependencies(request: __WHERE__) {
    data
    count
  }
}`;

const KG_DELETE_ALL_MANUAL_DEPENDENCIES = `
mutation KgDeleteAllManualDependencies {
  kg_delete_all_manual_dependencies(request: __WHERE__) {
    data
  }
}`;

// -- API object ------------------------------------------------------------

const apiKnowledgeGraph = {
  // ---- Relocated kg_* wrappers (verbatim from apiKubernetes1) ------------

  getCompleteGraph: async function (
    data: {
      accountIds?: string[];
      nodeIds?: string[];
      nodeTypes?: string[];
      attributes?: Record<string, any>;
      labels?: Record<string, any>;
      levels?: number;
    },
    signal?: AbortSignal
  ) {
    const request: Record<string, any> = {};
    if (data?.accountIds?.length) {
      request.account_ids = data.accountIds;
    }
    if (data?.nodeIds?.length) {
      request.node_ids = data.nodeIds;
    }
    if (data?.nodeTypes?.length) {
      request.node_types = data.nodeTypes;
    }
    if (data?.attributes) {
      request.attributes = data.attributes;
    }
    if (data?.labels) {
      request.labels = data.labels;
    }
    if (data?.levels) {
      request.levels = data.levels;
    }
    return await queryGraphQL(KG_GET_COMPLETE_GRAPH.replace('__WHERE__', gqlStringify(request)), 'KnowledgeGraph', {}, undefined, signal);
  },

  getFilterOptions: async function (data?: { accountIds?: string[]; nodeTypes?: string[] }) {
    const request: { account_ids?: string[]; node_types?: string[] } = {};
    if (data?.accountIds?.length) {
      request.account_ids = data.accountIds;
    }
    if (data?.nodeTypes?.length) {
      request.node_types = data.nodeTypes;
    }
    return await queryGraphQL(KG_GET_FILTER_OPTIONS.replace('__WHERE__', gqlStringify(request)), 'KgFilterOptions', {});
  },

  getFilterOptionLabelValues: async function (data: { filterType: string; filterKey: string }) {
    const request = { filter_type: data.filterType, filter_key: data.filterKey };
    return await queryGraphQL(KG_GET_FILTER_VALUES.replace('__WHERE__', gqlStringify(request)), 'KgFilterOptionLabelValues', {});
  },

  getNode: async function (nodeId: string) {
    return await queryGraphQL(KG_GET_NODE.replace('__WHERE__', gqlStringify({ node_id: nodeId })), 'KgGetNode', {});
  },

  getEdge: async function (edgeId: string) {
    return await queryGraphQL(KG_GET_EDGE.replace('__WHERE__', gqlStringify({ edge_id: edgeId })), 'KgGetEdge', {});
  },

  getCloudAccounts: async function () {
    return await queryGraphQL(KG_GET_CLOUD_ACCOUNTS, 'KgGetCloudAccounts', {});
  },

  getTenantFilter: async function () {
    return await queryGraphQL(KG_GET_TENANT_FILTER, 'KgGetTenantFilter', {});
  },

  upsertTenantFilter: async function (data: { accountIds: string[]; flowSources: string[] }) {
    const request = { account_ids: data.accountIds, flow_sources: data.flowSources };
    return await queryGraphQL(KG_UPSERT_TENANT_FILTER.replace('__WHERE__', gqlStringify(request)), 'KgUpsertTenantFilter', {});
  },

  // ---- Manual Dependencies (Phase 2 MVP — wired by UI) ------------------

  listManualDependencies: async function (data?: { statusFilter?: ManualDependencyResolutionStatus[] }) {
    const request: { status_filter?: ManualDependencyResolutionStatus[] } = {};
    if (data?.statusFilter?.length) {
      request.status_filter = data.statusFilter;
    }
    return await queryGraphQL(KG_LIST_MANUAL_DEPENDENCIES.replace('__WHERE__', gqlStringify(request)), 'KgListManualDependencies', {});
  },

  importManualDependencies: async function (data: { csv: string }) {
    return await queryGraphQL(KG_IMPORT_MANUAL_DEPENDENCIES.replace('__WHERE__', gqlStringify({ csv: data.csv })), 'KgImportManualDependencies', {});
  },

  resolveManualDependency: async function (data: { id: number; sourceNodeId?: string; destinationNodeId?: string }) {
    const request: { id: number; source_node_id?: string; destination_node_id?: string } = { id: data.id };
    if (data.sourceNodeId) {
      request.source_node_id = data.sourceNodeId;
    }
    if (data.destinationNodeId) {
      request.destination_node_id = data.destinationNodeId;
    }
    return await queryGraphQL(KG_RESOLVE_MANUAL_DEPENDENCY.replace('__WHERE__', gqlStringify(request)), 'KgResolveManualDependency', {});
  },

  // ---- Manual Dependencies (Phase 2.5 — wrappers scaffolded, not yet wired) ----

  createManualDependency: async function (data: Partial<ManualDependency>) {
    return await queryGraphQL(KG_CREATE_MANUAL_DEPENDENCY.replace('__WHERE__', gqlStringify(data)), 'KgCreateManualDependency', {});
  },

  updateManualDependency: async function (data: { id: number; dependency: Partial<ManualDependency> }) {
    return await queryGraphQL(KG_UPDATE_MANUAL_DEPENDENCY.replace('__WHERE__', gqlStringify(data)), 'KgUpdateManualDependency', {});
  },

  deleteManualDependency: async function (data: { id: number }) {
    return await queryGraphQL(KG_DELETE_MANUAL_DEPENDENCY.replace('__WHERE__', gqlStringify(data)), 'KgDeleteManualDependency', {});
  },

  reresolveManualDependency: async function (data: { id: number }) {
    return await queryGraphQL(KG_RERESOLVE_MANUAL_DEPENDENCY.replace('__WHERE__', gqlStringify(data)), 'KgReresolveManualDependency', {});
  },

  reresolveManualDependencies: async function (data?: { statusFilter?: ManualDependencyResolutionStatus[]; allRows?: boolean }) {
    const request: { status_filter?: ManualDependencyResolutionStatus[]; all_rows?: boolean } = {};
    if (data?.statusFilter?.length) {
      request.status_filter = data.statusFilter;
    }
    if (data?.allRows) {
      request.all_rows = true;
    }
    return await queryGraphQL(KG_RERESOLVE_MANUAL_DEPENDENCIES.replace('__WHERE__', gqlStringify(request)), 'KgReresolveManualDependencies', {});
  },

  deleteAllManualDependencies: async function () {
    return await queryGraphQL(KG_DELETE_ALL_MANUAL_DEPENDENCIES.replace('__WHERE__', gqlStringify({})), 'KgDeleteAllManualDependencies', {});
  },
};

export default apiKnowledgeGraph;
