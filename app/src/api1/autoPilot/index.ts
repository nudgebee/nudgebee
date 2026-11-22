import getMockData from '@api1/mock';
import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { safeJSONParse } from 'src/utils/common';

export const AUTO_PILOT_LISTING = `
query autopilotListing($limit:Int, $offset:Int) {
    auto_pilot_listing:autooptimize_list(where:__WHERE__,limit:$limit,offset:$offset,order_by: [{column: "status", order: asc}, {column: "creation_date", order: desc}]) {
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
    auto_pilot_aggregate:autooptimize_aggregate(where:__WHERE__) {
      rows {
        count
      }
    }
  }
  `;
export const AUTO_PILOT_TASK_LISTING = `
query autopilotTaskListing($where: AutoPilotTaskWhereRequest, $whereAgg: AutoPilotTaskGroupingsWhereRequest, $limit: Int, $offset: Int) {
  auto_pilot_task: autooptimize_list_tasks(where: $where, limit: $limit, offset: $offset, order_by: [{column: "scheduled_time", order: desc}]) {
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
  auto_pilot_task_aggregate: autooptimize_aggregate_tasks(where: $whereAgg) {
    rows {
      count
    }
  }
}`;

export const RECOMMENDATION_DATA = `
 query recommendationData {
  recommendation: recommendations_list(where: __WHERE__) {
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
  autooptimize_update_status(arg1: {id: $id, account_id:$account_id, status: $status }){
    status
  }
}`;

export const AUTO_PILOT_AGGREGATE = `
query AutoPilotAggregate($where1: AutoPilotGroupingsWhereRequest, $where2: AutoPilotTaskGroupingsWhereRequest) {
  auto_pilot_aggregate: autooptimize_aggregate(where: $where1) {
    rows {
      count
    }
  }
  auto_pilot_task_aggregate: autooptimize_aggregate_tasks(where: $where2) {
    rows {
      count
    }
  }
}
`;

export const GET_AUTO_PILOT_APPROVALS = `
query getAutopilotApprovals($where: AutoPilotApprovalsWhereRequest) {
  auto_pilot_approvals: autooptimize_list_approvals(where: $where, order_by: [{column: "created_at", order: desc}]) {
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
      reviewer_display_name
    }
  }
}
`;

export const DECIDE_AUTO_OPTIMIZE_APPROVAL = `
mutation decideAutoOptimizeApproval($id: uuid!, $accountId: uuid!, $status: String!, $reviewerComments: String) {
  autooptimize_update_approval(arg1: {id: $id, account_id: $accountId, status: $status, reviewer_comments: $reviewerComments}) {
    id
    status
    message
  }
}
`;

export const GET_APPROVAL_TASKS = `
query getApprovalTasks($where: AutoPilotTaskWhereRequest) {
  auto_pilot_task: autooptimize_list_tasks(where: $where) {
    rows {
      id
      name
      status
      reason
      recommendation_id
      meta
      resource_filter
      attributes
    }
  }
}
`;

export const GET_AUTO_PILOT_BY_PK = `
  query getAutoPilotByPk {
  auto_pilot_by_pk: autooptimize_list(where: __WHERE__, limit: 1) {
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
  auto_pilot: autooptimize_list(where: __WHERE__) {
    rows {
      name
      id
    }
  }
}
`;

export const GET_AUTOPILOT_APPROVAL_STATUS_BY_ID = `
query getAutoPilotApprovalStatusById($where: AutoPilotApprovalsWhereRequest, $whereAgg: AutoPilotApprovalsGroupingsWhereRequest, $limit: Int, $offset: Int) {
  auto_pilot_approvals: autooptimize_list_approvals(where: $where, limit: $limit, offset: $offset) {
    rows {
      reviewer_comments
      status
      updated_at
      created_at
      reviewer_display_name
    }
  }
  auto_pilot_approvals_aggregate: autooptimize_aggregate_approvals(where: $whereAgg) {
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
    if (query.accountName === 'Demo' || query.accountId === 'demo') {
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
      rule: typeof item.rule === 'string' ? safeJSONParse(item.rule) : item.rule,
      notification: typeof item.notification === 'string' ? safeJSONParse(item.notification) : item.notification,
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
      autoPilotId: string;
      accountId?: string;
      status: string;
    }
  ) {
    if (query.accountId === 'demo') {
      return { data: { auto_pilot_task_listing: [], auto_pilot_task_aggregate: { aggregate: { count: 0 } } } };
    }
    query = query || {};
    const where: any[] = [];
    const whereAgg: any[] = [];
    if (query.status) {
      where.push({ status: { _eq: query.status } });
      whereAgg.push({ status: { _eq: query.status } });
    }
    if (query.autoPilotId) {
      where.push({ auto_pilot_id: { _eq: query.autoPilotId } });
      whereAgg.push({ auto_pilot_id: { _eq: query.autoPilotId } });
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

    let recommendationsResponse = null;
    if (recommendationIds.length > 0) {
      const recQuery = RECOMMENDATION_DATA.replace(
        '__WHERE__',
        gqlStringify({
          id: { _in: recommendationIds },
          ...(query.accountId ? { account_id: { _eq: query.accountId } } : {}),
        })
      );
      recommendationsResponse = await queryGraphQL(recQuery, 'recommendationData', {});
    }

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
    if (accountId === 'demo') return null;
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
    if (accountId === 'demo') return { data: { auto_pilot_approvals: [] }, errors: [] };
    const where: any = { account_id: { _eq: accountId } };
    if (type) {
      where.auto_pilot_type = { _eq: type };
    }
    const response = await queryGraphQL(GET_AUTO_PILOT_APPROVALS, 'getAutopilotApprovals', { where });
    const rows = response?.data?.data?.auto_pilot_approvals?.rows || [];
    const data = rows.map((item: any) => ({
      ...item,
      attributes: typeof item.attributes === 'string' ? safeJSONParse(item.attributes) : item.attributes,
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
  async decideAutoOptimizeApproval(id: string, accountId: string, status: 'APPROVED' | 'REJECTED', reviewerComments: string) {
    if (accountId === 'demo')
      return {
        data: null,
        errors: [{ message: 'Demo account does not have access.' }],
      };
    const response = await queryGraphQL(DECIDE_AUTO_OPTIMIZE_APPROVAL, 'decideAutoOptimizeApproval', {
      id,
      accountId,
      status,
      reviewerComments: reviewerComments || null,
    });
    return {
      data: response?.data?.data?.autooptimize_update_approval,
      errors: response?.data?.errors,
    };
  },
  async getApprovalTasks(taskIds: string[]) {
    if (!taskIds?.length) return [];
    const response = await queryGraphQL(GET_APPROVAL_TASKS, 'getApprovalTasks', { where: { id: { _in: taskIds } } });
    const rows = response?.data?.data?.auto_pilot_task?.rows || [];
    return rows.map((task: any) => ({
      ...task,
      meta: typeof task.meta === 'string' ? safeJSONParse(task.meta) : task.meta,
      resource_filter: typeof task.resource_filter === 'string' ? safeJSONParse(task.resource_filter) : task.resource_filter,
      attributes: typeof task.attributes === 'string' ? safeJSONParse(task.attributes) : task.attributes,
    }));
  },
  // Ids of the given automations that have a run waiting for approval right now.
  async getAutoPilotIdsWaitingForApproval(autoPilotIds: string[]) {
    const waiting = new Set<string>();
    if (!autoPilotIds?.length) return waiting;
    const response = await queryGraphQL(GET_AUTO_PILOT_APPROVALS, 'getAutopilotApprovals', {
      where: { autopilot_id: { _in: autoPilotIds }, status: { _eq: 'PENDING' }, auto_pilot_type: { _eq: 'auto_optimize' } },
    });
    const now = Date.now();
    (response?.data?.data?.auto_pilot_approvals?.rows || []).forEach((row: any) => {
      const attributes = typeof row.attributes === 'string' ? safeJSONParse(row.attributes) : row.attributes;
      if (attributes?.expires_at && new Date(attributes.expires_at).getTime() > now) {
        waiting.add(row.autopilot_id);
      }
    });
    return waiting;
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
            rule: typeof row.rule === 'string' ? safeJSONParse(row.rule) : row.rule,
            notification: typeof row.notification === 'string' ? safeJSONParse(row.notification) : row.notification,
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
    if (query.accountId === 'demo') return { data: [], errors: [] };
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
