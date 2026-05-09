import { queryGraphQL, gqlStringify } from '@lib/HttpService';
import { getEndOfMonth, getStartOfMonth } from '@lib/datetime';

export const K8s_APPS_GROUPING = `
query AppsGrouping($where: ApplicationGroupWhereRequest, $aggregateWhere: ApplicationGroupGroupingsWhereRequest, $limit: Int, $offset: Int) {
  application_group: application_group_v2(where: $where, limit: $limit, offset: $offset, order_by: [{column: "updated_at", order: desc}]) {
    rows {
      id
      created_at
      updated_at
      updated_by
      name
      created_by_display_name
      updated_by_display_name
    }
  }
  application_group_aggregate: application_group_groupings_v2(where: $aggregateWhere) {
    rows {
      count
    }
  }
}
`;

export const K8S_APPS_GROUPING_MAPPINGS = `
query AppsGroupingMappings($where: ApplicationGroupMappingWhereRequest) {
  application_group_mappings: application_group_mapping_v2(where: $where) {
    rows {
      group_id
      account_id
      account_name
      workload_name
      workload_namespace
      workload_kind
      workload_is_active
    }
  }
}
`;

export const K8s_APPS_NAMES = `
query AppsGrouping {
  application_group: application_group_v2 {
    rows {
      name
      id
    }
  }
}
`;

export const K8S_APPS_GROUP_BY_PK = `
query ApplicationGroupByPK($where: ApplicationGroupWhereRequest, $mappingWhere: ApplicationGroupMappingWhereRequest) {
  application_group_by_pk: application_group_v2(where: $where, limit: 1) {
    rows {
      id
      description
      name
    }
  }
  application_group_mappings: application_group_mapping_v2(where: $mappingWhere) {
    rows {
      account_id
      group_id
      workload_namespace
      workload_kind
      workload_name
      cloud_resource_id
    }
  }
}
`;

export const INSERT_APP_GROUP_ONE = `
mutation InsertApplicationGroup($name: String!, $description: String, $mappings: [application_group_mapping_item!]!) {
  application_group_create(name: $name, description: $description, mappings: $mappings) {
    id
  }
}`;

export const UPDATE_APP_GROUP_ONE = `
mutation UpdateApplicationGroup($id: String!, $name: String!, $description: String, $mappings: [application_group_mapping_item!]!) {
  application_group_update(id: $id, name: $name, description: $description, mappings: $mappings) {
    id
  }
}
`;

export const GET_APPLICATIONS_BY_GROUP_ID = `
query GetApplicationsByGroup($where: ApplicationGroupMappingWhereRequest, $whereAgg: ApplicationGroupMappingGroupingsWhereRequest) {
  application_group_mapping: application_group_mapping_v2(where: $where) {
    rows {
      workload_name
      workload_namespace
      workload_kind
      account_id
      account_name
      group_id
      cloud_resource_id
    }
  }
  application_group_mapping_aggregate: application_group_mapping_groupings_v2(where: $whereAgg) {
    rows {
      count
    }
  }
}
`;

export const GET_APPLICATION_EVENTS = `
  query getAppEvents($limit: Int, $offset: Int) {

  events_aggregate: event_groupings_v2( where: {_and: [{_or:[__APPS__]}, __WHERE__ ] }) {
    rows{
      count: event_count
    }
  }

  events_v2( limit: $limit, offset: $offset, where: {_and: [{_or:[__APPS__]}, __WHERE__ ] }) {
    rows {
    id
      resource_id
      cluster
      description
      subject_name
      subject_namespace
      title
      subject_type
      priority
      status
      starts_at
      aggregation_key
      fingerprint
      evidences
      account_id
    }
  }
}

`;

export const GET_K8S_CLUSTER_SUMMARY_DATA = `
query get_k8s_cluster_summary_data($accountId: String) {
  right_sizing_recommendations: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, status:{_in:["Open", "InProgress"]}, category: {_in: ["RightSizing"] }, rule_name:{_eq:"pod_right_sizing"} , __WHERE__ }
  ){
    rows{
      count
    }
  }
  unused_volume_recommendations: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, status:{_in:["Open", "InProgress"]}, category: {_in: ["RightSizing"] }, rule_name:{_eq:"unused_pvc"} , __WHERE__ }
  ){
    rows{
      count
    }
  }
  k8s_spot_recommendations: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, status:{_in:["Open", "InProgress"]}, category: {_in: ["K8sSpotRecommendation"]} , __WHERE__ }
  ){
    rows{
      count
    }
  }
}`;
const apiAppGrouping = {
  async getApplicationGroupings(data: any) {
    const where: { name?: { _ilike: string } } = {};

    if (data.search) {
      where.name = { _ilike: '%' + data.search + '%' };
    }
    const response = await queryGraphQL(K8s_APPS_GROUPING, 'AppsGrouping', {
      where,
      aggregateWhere: where,
      limit: data.limit,
      offset: data.offset,
    });
    const groups = response?.data?.data?.application_group?.rows || [];
    const aggregateRows = response?.data?.data?.application_group_aggregate?.rows;

    // Fetch mappings for all groups in one query
    const groupIds = groups.map((g: any) => g.id);
    let mappingsByGroup: Record<string, any[]> = {};
    if (groupIds.length > 0) {
      try {
        const mappingResponse = await queryGraphQL(K8S_APPS_GROUPING_MAPPINGS, 'AppsGroupingMappings', {
          where: { group_id: { _in: groupIds } },
        });
        if (mappingResponse?.data?.errors) {
          console.error('Error fetching application group mappings:', mappingResponse.data.errors);
        }
        const mappings = mappingResponse?.data?.data?.application_group_mappings?.rows || [];
        mappings.forEach((m: any) => {
          if (!mappingsByGroup[m.group_id]) mappingsByGroup[m.group_id] = [];
          mappingsByGroup[m.group_id].push({
            cloud_account: { id: m.account_id, account_name: m.account_name },
            k8s_workload: { is_active: m.workload_is_active, name: m.workload_name, namespace: m.workload_namespace },
          });
        });
      } catch (err) {
        console.error('Failed to fetch application group mappings:', err);
      }
    }

    return {
      application_group: groups.map((g: any) => ({
        ...g,
        application_group_mappings: mappingsByGroup[g.id] || [],
        created_by_user: { display_name: g.created_by_display_name },
        updated_by_user: { display_name: g.updated_by_display_name },
      })),
      application_group_aggregate: { aggregate: { count: aggregateRows?.reduce((sum: number, r: any) => sum + (r.count || 0), 0) || 0 } },
    };
  },
  async InsertAppGrouping(groupData: any, workloadData: any) {
    const mappings = workloadData.map((item: any) => ({
      namespace_name: item.namespace_name,
      workload_name: item.workload_name,
      workload_kind: item.workload_kind,
      account_id: item.account_id,
      cloud_resource_id: item.cloud_resource_id,
    }));

    const response = await queryGraphQL(INSERT_APP_GROUP_ONE, 'InsertApplicationGroup', {
      name: groupData.name,
      description: groupData.description,
      mappings,
    });
    return response;
  },
  async UpdateAppGrouping(groupData: any, workloadData: any) {
    const mappings = workloadData.map((item: any) => ({
      namespace_name: item.namespace_name,
      workload_name: item.workload_name,
      workload_kind: item.workload_kind,
      account_id: item.account_id,
      cloud_resource_id: item.cloud_resource_id,
    }));

    const response = await queryGraphQL(UPDATE_APP_GROUP_ONE, 'UpdateApplicationGroup', {
      id: groupData.id,
      name: groupData.name,
      description: groupData.description,
      mappings,
    });
    return response;
  },
  async getAppGroupByPK(id: string) {
    const response = await queryGraphQL(K8S_APPS_GROUP_BY_PK, 'ApplicationGroupByPK', {
      where: { id: { _eq: id } },
      mappingWhere: { group_id: { _eq: id } },
    });
    const row = response?.data?.data?.application_group_by_pk?.rows?.[0];
    const mappings = (response?.data?.data?.application_group_mappings?.rows || []).map((item: any) => ({
      ...item,
      namespace_name: item.workload_namespace,
    }));
    const data = row ? { application_group_by_pk: { ...row, application_group_mappings: mappings } } : { application_group_by_pk: null };
    return { data: { data }, errors: response?.data?.errors };
  },
  async listAllApplicationGroupNames() {
    const response = await queryGraphQL(K8s_APPS_NAMES, 'AppsGrouping', {});
    return (response?.data?.data?.application_group?.rows || []).map((item: any) => ({ name: item.name, id: item.id }));
  },

  async getApplicationsByGroup(id: string) {
    const where = { group_id: { _eq: id } };
    const response = await queryGraphQL(GET_APPLICATIONS_BY_GROUP_ID, 'GetApplicationsByGroup', { where, whereAgg: where });
    const rows = (response?.data?.data?.application_group_mapping?.rows || []).map((row: any) => ({
      ...row,
      namespace_name: row.workload_namespace,
      cloud_account: { id: row.account_id, account_name: row.account_name },
    }));
    const count = response?.data?.data?.application_group_mapping_aggregate?.rows?.[0]?.count || 0;
    return {
      data: {
        application_group_mapping: rows,
        application_group_mapping_aggregate: { aggregate: { count } },
      },
    };
  },

  async getApplicationsEvents(query: any, workloads: any, limit: number, offset: number) {
    const formattedWorkloads = workloads
      .map(
        (item: any) => `{
      subject_name: { _eq: "${item.workload_name}" },
      subject_namespace: { _eq: "${item.namespace_name}" },
      account_id: { _eq: "${item.account_id}" },
    }`
      )
      .join(',');

    let formattedQuery = GET_APPLICATION_EVENTS.replaceAll('__APPS__', formattedWorkloads);

    const filterParams: any = {};

    if (query?.subject_namespace) {
      if (Array.isArray(query['subject_namespace'])) {
        filterParams['subject_namespace'] = { _in: query['subject_namespace'] };
      } else {
        filterParams['subject_namespace'] = { _eq: query['subject_namespace'] };
      }
    }
    if (query?.subject_type) {
      if (Array.isArray(query['subject_type'])) {
        filterParams['subject_type'] = { _in: query['subject_type'] };
      } else {
        filterParams['subject_type'] = { _eq: query['subject_type'] };
      }
    }
    if (query?.subject_name) {
      if (Array.isArray(query['subject_name'])) {
        filterParams['subject_name'] = { _in: query['subject_name'] };
      } else {
        filterParams['subject_name'] = { _like: query['subject_name'] + '%' };
      }
    }
    if (query?.cluster) {
      if (Array.isArray(query['cluster'])) {
        filterParams['cluster'] = { _in: query['cluster'] };
      } else {
        filterParams['cluster'] = { _eq: query['cluster'] };
      }
    }
    if (query?.title) {
      if (Array.isArray(query['title'])) {
        filterParams['title'] = { _in: query['title'] };
      } else {
        filterParams['title'] = { _eq: query['title'] };
      }
    }
    if (query?.finding_type) {
      if (Array.isArray(query['finding_type'])) {
        filterParams['finding_type'] = { _in: query['finding_type'] };
      } else {
        filterParams['finding_type'] = { _eq: query['finding_type'] };
      }
    }
    if (query?.aggregation_key) {
      if (Array.isArray(query['aggregation_key'])) {
        filterParams['aggregation_key'] = { _in: query['aggregation_key'] };
      } else {
        filterParams['aggregation_key'] = { _eq: query['aggregation_key'] };
      }
    }
    if (query?.priority) {
      if (Array.isArray(query['priority'])) {
        filterParams['priority'] = { _in: query['priority'] };
      } else {
        filterParams['priority'] = { _eq: query['priority'] };
      }
    }
    if (query?.status) {
      if (Array.isArray(query['status'])) {
        filterParams['status'] = { _in: query['status'] };
      } else {
        filterParams['status'] = { _eq: query['status'] };
      }
    }
    if (query?.resource_id) {
      if (Array.isArray(query['resource_id'])) {
        filterParams['resource_id'] = { _in: query['resource_id'] };
      } else {
        filterParams['resource_id'] = { _eq: query['resource_id'] };
      }
    }
    const endDate = query.end_date || query.endDate || getEndOfMonth(new Date());
    const startDate = query.start_date || query.startDate || getStartOfMonth(new Date());

    filterParams['_and'] = [{ starts_at: { _gte: startDate.toISOString() } }, { starts_at: { _lte: endDate.toISOString() } }];

    if (Object.keys(filterParams).length === 0) {
      formattedQuery = formattedQuery.replaceAll('__WHERE__', ' ');
    } else {
      formattedQuery = formattedQuery.replaceAll('__WHERE__', gqlStringify(filterParams));
    }
    const response = await queryGraphQL(formattedQuery, 'getAppEvents', { limit: limit, offset: offset });
    return response.data;
  },
  async getK8sClusterSummaryData(accountId: string, query: any) {
    if (accountId == 'demo') {
      return {
        data: {
          total_recommendations: [],
        },
      };
    }
    let where = ``;
    if (query.resource_ids) {
      where += `,resource_id : {_in: [${query.resource_ids.map((i: string) => `"${i}"`)}]}`;
    }
    try {
      let formattedQuery = GET_K8S_CLUSTER_SUMMARY_DATA.replaceAll('__WHERE__', where);
      const response = await queryGraphQL(formattedQuery, 'get_k8s_cluster_summary_data', {
        accountId: accountId,
      });
      if (!response?.data?.errors) {
        return {
          data: {
            total_recommendations: [
              {
                category: 'Right Sizing',
                count: response?.data?.data?.right_sizing_recommendations?.rows?.[0]?.count || 0,
              },
              {
                category: 'Unused Volumes',
                count: response?.data?.data?.unused_volume_recommendations?.rows?.[0]?.count || 0,
              },
              {
                category: 'Spot Recommendations',
                count: response?.data?.data?.k8s_spot_recommendations?.rows?.[0]?.count || 0,
              },
            ],
          },
        };
      }
      return response?.data;
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
};

export default apiAppGrouping;
