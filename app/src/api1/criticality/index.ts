import { queryGraphQL } from '@lib/HttpService';

export type Criticality = 'critical' | 'high' | 'medium' | 'low';
export type CriticalitySource = 'user' | 'fact_signal' | 'llm_inferred' | 'default';

export interface WorkloadCriticalityItem {
  cloud_resource_id: string;
  namespace: string;
  name: string;
  kind: string;
  criticality: Criticality;
  source: CriticalitySource;
  confidence: number;
  rationale: string;
  is_user_override: boolean;
}

export interface WorkloadCriticalityListResult {
  items: WorkloadCriticalityItem[];
  total: number;
}

export const WORKLOAD_LIST_CRITICALITY = `
mutation WorkloadListCriticality(
  $cloud_account_id: String!
  $namespace: String
  $tiered_only: Boolean
  $limit: Int
  $offset: Int
) {
  workload_list_criticality(
    cloud_account_id: $cloud_account_id
    namespace: $namespace
    tiered_only: $tiered_only
    limit: $limit
    offset: $offset
  ) {
    items {
      cloud_resource_id
      namespace
      name
      kind
      criticality
      source
      confidence
      rationale
      is_user_override
    }
    total
  }
}`;

export const WORKLOAD_UPSERT_CRITICALITY = `
mutation WorkloadUpsertCriticality(
  $cloud_account_id: String!
  $cloud_resource_id: String!
  $namespace: String
  $criticality: String!
) {
  workload_upsert_criticality(
    cloud_account_id: $cloud_account_id
    cloud_resource_id: $cloud_resource_id
    namespace: $namespace
    criticality: $criticality
  ) {
    success
  }
}`;

export const WORKLOAD_DELETE_CRITICALITY = `
mutation WorkloadDeleteCriticality($cloud_account_id: String!, $cloud_resource_id: String!) {
  workload_delete_criticality(cloud_account_id: $cloud_account_id, cloud_resource_id: $cloud_resource_id) {
    success
  }
}`;

export interface ListCriticalityInput {
  cloud_account_id: string;
  namespace?: string;
  tiered_only?: boolean;
  limit?: number;
  offset?: number;
}

const apiCriticality = {
  /** List workloads with their resolved criticality (Service Criticality review screen). */
  async list(input: ListCriticalityInput): Promise<WorkloadCriticalityListResult | null> {
    try {
      if (input.cloud_account_id === 'demo') return { items: [], total: 0 };
      const response = await queryGraphQL(WORKLOAD_LIST_CRITICALITY, 'WorkloadListCriticality', {
        cloud_account_id: input.cloud_account_id,
        namespace: input.namespace ?? null,
        tiered_only: input.tiered_only ?? false,
        limit: input.limit ?? 200,
        offset: input.offset ?? 0,
      });
      return response?.data?.data?.workload_list_criticality ?? null;
    } catch (error) {
      console.error('Failed to list workload criticality:', error);
      throw error;
    }
  },

  /** Set an operator override for a workload's criticality. */
  async upsert(input: { cloud_account_id: string; cloud_resource_id: string; namespace?: string; criticality: Criticality }) {
    const response = await queryGraphQL(WORKLOAD_UPSERT_CRITICALITY, 'WorkloadUpsertCriticality', {
      cloud_account_id: input.cloud_account_id,
      cloud_resource_id: input.cloud_resource_id,
      namespace: input.namespace ?? null,
      criticality: input.criticality,
    });
    return response?.data?.data?.workload_upsert_criticality;
  },

  /** Remove an operator override, reverting the workload to its derived criticality. */
  async remove(input: { cloud_account_id: string; cloud_resource_id: string }) {
    const response = await queryGraphQL(WORKLOAD_DELETE_CRITICALITY, 'WorkloadDeleteCriticality', {
      cloud_account_id: input.cloud_account_id,
      cloud_resource_id: input.cloud_resource_id,
    });
    return response?.data?.data?.workload_delete_criticality;
  },
};

export default apiCriticality;
