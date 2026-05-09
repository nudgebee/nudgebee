import { gqlStringify, queryGraphQL } from '@lib/HttpService';

export const LIST_AUDIT_EVENTS = `
query ListAuditEvents($limit: Int, $offset: Int) {
  audit_groupings_v2(where: __WHERE__) {
    rows {
      count
    }
  }
  audits_v2(where: __WHERE__, limit: $limit, offset: $offset, order_by: [{column: "event_time", order: desc}]) {
    rows {
      user_id
      account_id
      event_time
      event_category
      event_type
      event_status
      event_target
      event_action
      transaction_id
      event_prev_state
      event_state
      event_attr
    }
  }
}
`;

export const LIST_ACCOUNT_AND_USER = `
query ListUsersAndAccount {
  cloud_accounts: get_cloud_accounts_v2(order_by: [{column: "account_name", order: asc}]) {
    rows {
      id
      account_name
    }
  }
  users: admin_get_users_by_tenant_v2(order_by: [{column: "username", order: asc}]) {
    rows {
      id
      username
    }
  }
}
`;

const apiAudits = {
  listAudits: async function (
    limit = 10,
    offset = 0,
    query: {
      username?: string;
      accountId?: string;
      category?: string;
      eventType?: string;
      action?: string;
      status?: string;
      eventStart?: Date;
      eventEnd?: Date;
    } = {}
  ) {
    try {
      const gqlQuery: any = {};
      if (query.username) {
        gqlQuery['username'] = { _eq: query.username };
      }
      if (query.accountId) {
        gqlQuery['account_id'] = { _eq: query.accountId };
      }
      if (query.category) {
        gqlQuery['event_category'] = { _ilike: `${query.category}` };
      }
      if (query.eventType) {
        gqlQuery['event_type'] = { _ilike: `${query.eventType}` };
      }
      if (query.action) {
        gqlQuery['event_action'] = { _eq: query.action };
      }
      if (query.status) {
        gqlQuery['event_status'] = { _eq: query.status };
      }
      if (query.eventStart && query.eventEnd) {
        gqlQuery['event_time'] = { _between: { _gt: query.eventStart.toISOString(), _lt: query.eventEnd.toISOString() } };
      }

      const queryStr = LIST_AUDIT_EVENTS.replaceAll('__WHERE__', gqlStringify(gqlQuery, []));
      const response = await queryGraphQL(queryStr, 'ListAuditEvents', {
        limit: limit,
        offset: offset,
      });
      const data = response?.data?.data?.audits_v2?.rows || [];
      return {
        data: {
          audits: data,
          count: response.data?.data?.audit_groupings_v2?.rows[0]?.count || 0,
        },
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  lisUsersAndAccounts: async function () {
    try {
      const response = await queryGraphQL(LIST_ACCOUNT_AND_USER, 'ListUsersAndAccount', {});
      return {
        data: response?.data.data || [],
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
};
export default apiAudits;
