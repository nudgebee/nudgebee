import getMockData from '@api1/mock';
import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { safeJSONParse } from 'src/utils/common';

export const AUTO_PILOT_LISTING = `
query autopilotListing($limit:Int, $offset:Int) {
    auto_pilot_listing:auto_pilot_v2(where:__WHERE__,limit:$limit,offset:$offset,order_by: [{column: "status", order: asc}, {column: "creation_date", order: desc}]) {
    rows {
      name
      schedule_time
      next_schedule_time
      status
      category
      account_id
      last_executed_time
      id
      created_by
      creation_date
      notification
      start_at
      end_at
      rule
      auto_optimize_resource_maps
      username
      display_name
      updated_by_display_name
      attributes
    }
    }
    auto_pilot_aggregate:auto_pilot_groupings_v2(where:__WHERE__) {
      rows {
        count
      }
    }
  }
  `;
export const AUTO_PILOT_TASK_LISTING = `
query autopilotTaskListing($where: AutoPilotTaskWhereRequest, $whereAgg: AutoPilotTaskGroupingsWhereRequest, $limit: Int, $offset: Int) {
  auto_pilot_task: auto_pilot_task_v2(where: $where, limit: $limit, offset: $offset, order_by: [{column: "scheduled_time", order: desc}]) {
    rows {
      scheduled_time
      status
      reason
      command
      recommendation_id
      task_id
      meta
      resource_filter
      auto_pilot_category
      auto_pilot_account_id
      auto_pilot_resource_maps
      attributes
    }
  }
  auto_pilot_task_aggregate: auto_pilot_task_groupings_v2(where: $whereAgg) {
    rows {
      count
    }
  }
}`;

export const RECOMMENDATION_DATA = `
 query recommendationData {
  recommendation: recommendations_v2(where: __WHERE__) {
    rows {
      id
      resource_name
      resource_type
      resource_meta
    }
  }
} `;

export const UPDATE_AUTOPILOT_STATUS = `
mutation UpdateAutoPilotStatus( $id: uuid!,$account_id: uuid! ,$status: String!) {
  auto_optimize_update_status(arg1: {id: $id, account_id:$account_id, status: $status }){
    status
  }
}`;

export const AUTO_PILOT_AGGREGATE = `
query AutoPilotAggregate($where1: AutoPilotGroupingsWhereRequest, $where2: AutoPilotTaskGroupingsWhereRequest) {
  auto_pilot_aggregate: auto_pilot_groupings_v2(where: $where1) {
    rows {
      count
    }
  }
  auto_pilot_task_aggregate: auto_pilot_task_groupings_v2(where: $where2) {
    rows {
      count
    }
  }
}
`;

export const GET_AUTO_PILOT_APPROVALS = `
query getAutopilotApprovals($where: AutoPilotApprovalsWhereRequest) {
  auto_pilot_approvals: auto_pilot_approvals_v2(where: $where) {
    rows {
      id
      created_at
      reviewer_id
      auto_pilot_type
      reviewer_comments
      status
      updated_at
      autopilot_id
      attributes
      approval_status_description
    }
  }
}
`;

export const UPDATE_AUTOPILOT_APPOVAL_STATUS = `
mutation updateAutoPilotApprovalStatus($id: uuid!,$accountId: uuid!, $reviewComment: String,$status: String!) {
  update_status_auto_pilot_approval(arg1: {account_id: $accountId, approval_id: $id, status: $status, reviewer_comments: $reviewComment}) {
    id
  }
}
`;

export const GET_AUTO_PILOT_APPROVALS_POLICY = `
query getAutoPilotApprovalsPolicy($where: AutoPilotApprovalPolicyWhereRequest, $limit: Int, $offset: Int) {
  auto_pilot_approval_policy: auto_pilot_approval_policy_v2(where: $where, limit: $limit, offset: $offset) {
    rows {
      account_id
      created_at
      created_by
      id
      policy_attributes
      updated_at
      updated_by
      account_name
      created_by_display_name
      updated_by_display_name
    }
  }
}
`;

export const CREATE_AUTO_PILOT_POLICY = `
mutation createAutopilotPolicy($accountId: uuid!, $minimumApproval:Int, $reviewees:[uuid], $reviewers: [uuid] ){
    create_auto_pilot_policy(arg1: {account_id: $accountId, minimum_approval: $minimumApproval, reviewees: $reviewees, reviewers: $reviewers}) {
      id
  }
}
`;

export const UPDATE_AUTO_PILOT_POLICY = `
mutation updateAutopilotPolicy($policyId : uuid! ,$accountId: uuid!, $minimumApproval:Int, $reviewees:[uuid], $reviewers: [uuid] ){
    update_auto_pilot_policy(arg1: {id:$policyId, account_id: $accountId, minimum_approval: $minimumApproval, reviewees: $reviewees, reviewers: $reviewers}) {
      id
  }
}
`;

export const GET_AUTO_PILOT_POLICY_BY_PK = `
query getAutoPilotPolicyByPk ($id:uuid!) {
  auto_pilot_approval_policy_by_pk(id: $id) {
    account_id
    cloud_account {
      account_name
    }
    policy_attributes
    auto_pilot_reviewers {
      id
      reviwer_user {
        id
        username
      }
    }
    auto_pilot_reviewees {
      user {
        id
        username
      }
    }
  }
}
`;

export const GET_AUTO_PILOT_BY_PK = `
  query getAutoPilotByPk {
  auto_pilot_by_pk: auto_pilot_v2(where: __WHERE__, limit: 1) {
    rows {
      name
      schedule_time
      next_schedule_time
      status
      category
      account_id
      last_executed_time
      id
      created_by
      creation_date
      notification
      start_at
      end_at
      rule
      auto_optimize_resource_maps
      username
      display_name
      updated_by_display_name
      attributes
    }
  }
}
`;

export const GET_AUTO_OPTIMIZE_NAMES = `
query getAutoOptimizeNames{
  auto_pilot: auto_pilot_v2(where: __WHERE__) {
    rows {
      name
      id
    }
  }
}
`;

export const DELETE_AUTO_PILOT_POLICY = `
mutation deleteAutoPilotApprovalPolicy($id: uuid!, $accountId: uuid!) {
  auto_pilot_approval_policy_delete(arg1: {id: $id, account_id: $accountId}) {
    id
  }
}`;

export const GET_AUTOPILOT_APPROVAL_STATUS_BY_ID = `
query getAutoPilotApprovalStatusById($where: AutoPilotApprovalsWhereRequest, $whereAgg: AutoPilotApprovalsGroupingsWhereRequest, $limit: Int, $offset: Int) {
  auto_pilot_approvals: auto_pilot_approvals_v2(where: $where, limit: $limit, offset: $offset) {
    rows {
      reviewer_comments
      status
      updated_at
      created_at
      reviewer_display_name
    }
  }
  auto_pilot_approvals_aggregate: auto_pilot_approvals_groupings_v2(where: $whereAgg) {
    rows {
      count
    }
  }
}`;

const apiAutoPilot = {
  async listAutoPilot(
    limit = 10,
    offset = 0,
    query: { accountName?: string; name?: string; accountId?: string; status?: string; category?: string; id?: string } = {}
  ) {
    if (query.accountName === 'demo' || query.accountId === 'demo') {
      const demoData = await getMockData('autoPilot');
      return {
        data: {
          auto_pilot_listing: demoData.autopilotListing.data.auto_pilot_listing,
          auto_pilot_aggregate: demoData.autopilotListing?.data?.auto_pilot_aggregate,
        },
      };
    }
    query = query || {};
    const where = [];
    if (query.accountName) {
      where.push({ account_name: { _eq: query.accountName } });
    }
    if (query.accountId) {
      where.push({ account_id: { _eq: query.accountId } });
    }
    if (query.name) {
      where.push({ name: { _ilike: '%' + query.name + '%' } });
    }
    if (query.status) {
      where.push({ status: { _eq: query.status } });
    }
    if (query.category) {
      where.push({ category: { _eq: query.category } });
    }
    if (query.id) {
      where.push({ id: { _eq: query.id } });
    }
    const formattedQuery = AUTO_PILOT_LISTING.replaceAll('__WHERE__', gqlStringify({ _and: where }));

    const response = await queryGraphQL(formattedQuery, 'autopilotListing', {
      limit: limit,
      offset: offset,
    });
    const rows = response?.data?.data?.auto_pilot_listing?.rows || [];
    const data = rows.map((item: any) => ({
      ...item,
      auto_optimize_resource_maps:
        typeof item.auto_optimize_resource_maps === 'string' ? safeJSONParse(item.auto_optimize_resource_maps) : item.auto_optimize_resource_maps,
      attributes: typeof item.attributes === 'string' ? safeJSONParse(item.attributes) : item.attributes,
      user: { username: item.username, display_name: item.display_name },
      user_updated_by: { display_name: item.updated_by_display_name },
    }));
    const aggregateRows = response?.data?.data?.auto_pilot_aggregate?.rows;
    return {
      data: {
        auto_pilot_listing: data,
        auto_pilot_aggregate: { aggregate: { count: aggregateRows?.[0]?.count || 0 } },
      },
    };
  },
  async listAutoPilotTask(
    limit = 10,
    offset = 0,
    query: {
      accountId: string;
      status: string;
    }
  ) {
    query = query || {};
    const where: any[] = [];
    const whereAgg: any[] = [];
    if (query.status) {
      where.push({ status: { _eq: query.status } });
      whereAgg.push({ status: { _eq: query.status } });
    }
    if (query.accountId) {
      where.push({ auto_pilot_id: { _eq: query.accountId } });
      whereAgg.push({ auto_pilot_id: { _eq: query.accountId } });
    }
    const response = await queryGraphQL(AUTO_PILOT_TASK_LISTING, 'autopilotTaskListing', {
      where: { _and: where },
      whereAgg: { _and: whereAgg },
      limit: limit,
      offset: offset,
    });
    const rows = response?.data?.data?.auto_pilot_task?.rows || [];
    const data = rows.map((task: any) => ({
      ...task,
      meta: typeof task.meta === 'string' ? safeJSONParse(task.meta) : task.meta,
      resource_filter: typeof task.resource_filter === 'string' ? safeJSONParse(task.resource_filter) : task.resource_filter,
      attributes: typeof task.attributes === 'string' ? safeJSONParse(task.attributes) : task.attributes,
      auto_pilot: {
        cloud_account: { id: task.auto_pilot_account_id },
        auto_optimize_resource_maps:
          typeof task.auto_pilot_resource_maps === 'string' ? safeJSONParse(task.auto_pilot_resource_maps) : task.auto_pilot_resource_maps,
        category: task.auto_pilot_category,
      },
    }));
    const recommendationIds = [...new Set(data.map((task: any) => task.recommendation_id).filter((id: any) => id != null))];

    const recQuery = RECOMMENDATION_DATA.replace('__WHERE__', gqlStringify({ id: { _in: recommendationIds } }));
    const recommendationsResponse = await queryGraphQL(recQuery, 'recommendationData', {});

    const recommendationsMap = (recommendationsResponse?.data?.data?.recommendation?.rows || []).reduce((acc: any, rec: any) => {
      acc[rec.id] = { name: rec.resource_name, type: rec.resource_type, meta: rec.resource_meta };
      return acc;
    }, {});
    data.forEach((task: any) => {
      if (task.recommendation_id) {
        task.recommendation = recommendationsMap[task.recommendation_id];
      }
    });

    const aggregateRows = response?.data?.data?.auto_pilot_task_aggregate?.rows;
    return {
      data: {
        auto_pilot_task_listing: data,
        auto_pilot_task_aggregate: { aggregate: { count: aggregateRows?.[0]?.count || 0 } },
      },
    };
  },

  async updateAutoPilotStatus(id: string, accountId: string, status: string) {
    const response = await queryGraphQL(UPDATE_AUTOPILOT_STATUS, 'UpdateAutoPilotStatus', { id: id, account_id: accountId, status: status });
    const data = response?.data;
    return {
      data,
    };
  },
  async getAutoPilotAggregate(data: any) {
    if (data.accountId == 'demo') {
      const response = await getMockData('optimizeSummaryAutoPilotCounts');
      return response.data;
    }
    let time7Days = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString();
    if (data.time7Days) {
      time7Days = data.time7Days;
    }
    const categories = ['vertical_rightsize', 'horizontal_rightsize', 'pvc_rightsize', 'continuous_rightsize'];
    const response = await queryGraphQL(AUTO_PILOT_AGGREGATE, 'AutoPilotAggregate', {
      where1: {
        category: { _in: categories },
        account_id: { _eq: data.accountId },
        status: { _eq: 'Active' },
      },
      where2: {
        scheduled_time: { _gte: time7Days },
        status: { _eq: 'Complete' },
        auto_pilot_account_id: { _eq: data.accountId },
        auto_pilot_category: { _in: categories },
      },
    });
    const result = response?.data?.data;
    return {
      auto_pilot_aggregate: { aggregate: { count: result?.auto_pilot_aggregate?.rows?.[0]?.count || 0 } },
      auto_pilot_task_aggregate: { aggregate: { count: result?.auto_pilot_task_aggregate?.rows?.[0]?.count || 0 } },
    };
  },
  async getAutoPilotApprovals(accountId: string, type: string, _email: string) {
    const where: any = { account_id: { _eq: accountId } };
    if (type) {
      where.auto_pilot_type = { _eq: type };
    }
    const response = await queryGraphQL(GET_AUTO_PILOT_APPROVALS, 'getAutopilotApprovals', { where });
    const rows = response?.data?.data?.auto_pilot_approvals?.rows || [];
    const data = rows.map((item: any) => ({
      ...item,
      auto_pilot_approval_status: {
        status: item.status,
        description: item.approval_status_description,
      },
    }));
    return {
      data: { auto_pilot_approvals: data },
      errors: response?.data?.errors,
    };
  },
  async updateAutoPilotApprovalStatus(id: string, accountId: string, reviewComment: string, status: string) {
    const response = await queryGraphQL(UPDATE_AUTOPILOT_APPOVAL_STATUS, 'updateAutoPilotApprovalStatus', {
      id: id,
      accountId: accountId,
      status: status,
      reviewComment: reviewComment,
    });
    return {
      data: response?.data?.data,
      errors: response?.data?.errors,
    };
  },
  async getAutoPilotPolicies(limit: number, offset: number) {
    const response = await queryGraphQL(GET_AUTO_PILOT_APPROVALS_POLICY, 'getAutoPilotApprovalsPolicy', {
      where: {},
      limit: limit,
      offset: offset,
    });
    const rows = response?.data?.data?.auto_pilot_approval_policy?.rows || [];
    const data = rows.map((item: any) => ({
      ...item,
      policy_attributes: typeof item.policy_attributes === 'string' ? safeJSONParse(item.policy_attributes) : item.policy_attributes,
      cloud_account: { account_name: item.account_name },
      create_by_user: { display_name: item.created_by_display_name },
      update_by_user: { display_name: item.updated_by_display_name },
    }));
    return {
      data: { auto_pilot_approval_policy: data },
      errors: response?.data?.errors,
    };
  },
  async createAutoPilotPolicy(accountId: string, minimumApproval: number, reviewees: string[], reviewers: string[]) {
    const response = await queryGraphQL(CREATE_AUTO_PILOT_POLICY, 'createAutopilotPolicy', {
      accountId: accountId,
      minimumApproval: minimumApproval,
      reviewees: reviewees,
      reviewers: reviewers,
    });

    return {
      data: response?.data?.data,
      errors: response?.data?.errors,
    };
  },
  async updateAutoPilotPolicy(policyId: string, accountId: string, minimumApproval: number, reviewees: string[], reviewers: string[]) {
    const response = await queryGraphQL(UPDATE_AUTO_PILOT_POLICY, 'updateAutopilotPolicy', {
      policyId: policyId,
      accountId: accountId,
      minimumApproval: minimumApproval,
      reviewees: reviewees,
      reviewers: reviewers,
    });

    return {
      data: response?.data?.data,
      errors: response?.data?.errors,
    };
  },
  async getAutoPilotPolicyByPk(policyId: string) {
    const response = await queryGraphQL(GET_AUTO_PILOT_POLICY_BY_PK, 'getAutoPilotPolicyByPk', { id: policyId });
    return {
      data: response?.data?.data,
      errors: response?.data?.errors,
    };
  },
  async getAutoPilotByPk(id: string) {
    const formattedQuery = GET_AUTO_PILOT_BY_PK.replaceAll('__WHERE__', gqlStringify({ id: { _eq: id } }));
    const response = await queryGraphQL(formattedQuery, 'getAutoPilotByPk', {});
    const row = response?.data?.data?.auto_pilot_by_pk?.rows?.[0];
    const data = row
      ? {
          auto_pilot_by_pk: {
            ...row,
            auto_optimize_resource_maps:
              typeof row.auto_optimize_resource_maps === 'string' ? safeJSONParse(row.auto_optimize_resource_maps) : row.auto_optimize_resource_maps,
            attributes: typeof row.attributes === 'string' ? safeJSONParse(row.attributes) : row.attributes,
            user: { username: row.username, display_name: row.display_name },
            user_updated_by: { display_name: row.updated_by_display_name },
          },
        }
      : { auto_pilot_by_pk: null };
    return {
      data,
      errors: response?.data?.errors,
    };
  },
  async getAutoOptimizeNames(query: any) {
    const where: any = {};
    if (query.autoPilotId) {
      where.id = { _in: query.autoPilotId };
    }
    where.account_id = { _eq: query.accountId };

    const response = await queryGraphQL(GET_AUTO_OPTIMIZE_NAMES.replaceAll('__WHERE__', gqlStringify(where)), 'getAutoOptimizeNames');
    return {
      data: response?.data?.data?.auto_pilot?.rows,
      errors: response?.data?.errors,
    };
  },
  async deleteAutoPilotPolicy(id: string, accountId: string) {
    const response = await queryGraphQL(DELETE_AUTO_PILOT_POLICY, 'deleteAutoPilotApprovalPolicy', { id: id, accountId: accountId });
    return {
      data: response?.data?.data,
    };
  },
  async getAutoPilotApprovalStatusById(id: string, limit: number, offset: number) {
    const where = { autopilot_id: { _eq: id } };
    const whereAgg = { autopilot_id: { _eq: id } };
    const response = await queryGraphQL(GET_AUTOPILOT_APPROVAL_STATUS_BY_ID, 'getAutoPilotApprovalStatusById', {
      where: where,
      whereAgg: whereAgg,
      limit: limit,
      offset: offset,
    });
    const rows = response?.data?.data?.auto_pilot_approvals?.rows || [];
    const data = rows.map((item: any) => ({
      ...item,
      user_reviwer_id: { display_name: item.reviewer_display_name },
    }));
    const aggregateRows = response?.data?.data?.auto_pilot_approvals_aggregate?.rows;
    return {
      data: {
        auto_pilot_approvals: data,
        auto_pilot_approvals_aggregate: { aggregate: { count: aggregateRows?.[0]?.count || 0 } },
      },
      errors: response?.data?.errors,
    };
  },
};

export default apiAutoPilot;
