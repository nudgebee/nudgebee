import getMockData from '@api1/mock';
import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import cache from '@lib/cache';
import { safeJSONParse } from 'src/utils/common';

function parseInsightJsonFields(rows: any[]) {
  if (!rows) return rows;
  return rows.map((row: any) => ({
    ...row,
    applications: typeof row.applications === 'string' ? safeJSONParse(row.applications) : row.applications,
    rule: typeof row.rule === 'string' ? safeJSONParse(row.rule) : row.rule,
  }));
}

export const GET_CLOUD_ACCOUNTS = `
query GetCloudAccounts {
    cloud_accounts: accounts_list(where: __WHERE__) {
      rows {
        cloud_provider
        account_type
        id
        account_name
        status
        created_at
        agents
        account_access
        cloud_account_attrs
      }
    }
}
`;

const apiDashboard = {
  getCloudAccounts: async function (cloud_provider = '', refresh = false, includeDemoAccount = false) {
    const cacheKeyWithDemo = (cloud_provider ? 'accounts-' + cloud_provider : 'all-accounts') + '-demo';
    const cacheKeyWithoutDemo = cloud_provider ? 'accounts-' + cloud_provider : 'all-accounts';
    try {
      if (!refresh) {
        const cacheKey = includeDemoAccount ? cacheKeyWithDemo : cacheKeyWithoutDemo;
        const cachedAccounts = cache.get(cacheKey);
        if (cachedAccounts?.length) {
          return cachedAccounts;
        }
      }
      const where: any = {};
      where.status = { _eq: 'active' };
      if (cloud_provider) {
        where.cloud_provider = { _eq: cloud_provider };
      }
      const response = await queryGraphQL(GET_CLOUD_ACCOUNTS.replace('__WHERE__', gqlStringify(where, [])), 'GetCloudAccounts', {});
      const rows = response?.data?.data?.cloud_accounts?.rows ?? [];
      const data = rows.map((item: any) => {
        const agents = typeof item.agents === 'string' ? safeJSONParse(item.agents) : item.agents;
        // Parse connection_status inside each agent if it's still a JSON string
        const parsedAgents = Array.isArray(agents)
          ? agents.map((agent: any) => ({
              ...agent,
              connection_status: typeof agent.connection_status === 'string' ? safeJSONParse(agent.connection_status) || {} : agent.connection_status,
            }))
          : agents;
        return {
          ...item,
          agents: parsedAgents,
          cloud_account_attrs: typeof item.cloud_account_attrs === 'string' ? safeJSONParse(item.cloud_account_attrs) : item.cloud_account_attrs,
          account_access: item.account_access,
        };
      });

      let demoAccount = null;

      try {
        const mockData = await getMockData('home');
        const mockAccount = mockData.GetCloudAccount;
        if (mockAccount && mockAccount.status === 'active') {
          if (!cloud_provider || mockAccount.cloud_provider === cloud_provider) {
            demoAccount = mockAccount;
          }
        }
      } catch (mockErr) {
        console.log('Failed to fetch demo account, skipping:', mockErr);
      }

      cache.set(cacheKeyWithoutDemo, data, 60 * 60);
      const dataWithDemo = [...data, demoAccount].filter((item) => item !== null && item !== undefined);
      cache.set(cacheKeyWithDemo, dataWithDemo, 60 * 60);
      return includeDemoAccount ? dataWithDemo : data;
    } catch (err) {
      console.log('getWidget1Query Error is', err);
      return err;
    }
  },

  getInsights: async function (accountId: string) {
    if (accountId == 'demo') {
      const mockData = await getMockData('new-home');
      return mockData.InsightSummary;
    }
    const InsightSummary = `
    query InsightSummary {
      insights_list(where: __WHERE__, order_by: [{column: "created_at", order: desc}]) {
        rows {
          account_id
          unique_id
          title
          id
          applications
          type
          severity
          rule
          created_at
        }
      }
    }
    `;
    const severityWeight: Record<string, number> = { Critical: 1, High: 2, Medium: 3, Low: 4 };

    const where: any = {};
    where.account_id = { _eq: accountId };
    where.status = { _eq: 'Open' };
    const response = await queryGraphQL(InsightSummary.replace('__WHERE__', gqlStringify(where)), 'InsightSummary', {});
    if (response?.data?.data?.insights_list?.rows) {
      response.data.data.insights_list.rows = parseInsightJsonFields(response.data.data.insights_list.rows);
    }

    // Sort by severity: Critical > High > Medium > Low
    const insights = response?.data?.data?.insights_list?.rows;
    if (Array.isArray(insights)) {
      insights.sort((a: any, b: any) => (severityWeight[a.rule?.severity] || 99) - (severityWeight[b.rule?.severity] || 99));
    }

    return response;
  },

  getImageScanData: async function (accountId: string) {
    if (accountId == 'demo') {
      const mockData = await getMockData('new-home');
      return mockData.ImageScanData.data;
    }
    const ImageScanData = `
    query ImageScanData {
      recommendation_security_groupings_v2(where: __WHERE__) {
        rows {
          count_image
          account_id
          count_severity_critical
          workload_name
          namespace
        }
      }
    }
    `;
    const where: any = {};
    where.account_id = { _eq: accountId };
    where.status = { _eq: 'Open' };
    where.severity = { _eq: 'Critical' };
    const response = await queryGraphQL(ImageScanData.replace('__WHERE__', gqlStringify(where)), 'ImageScanData', {});
    return response;
  },

  getCertificateIssue: async function (accountId: string) {
    if (accountId == 'demo') {
      const mockData = await getMockData('new-home');
      return mockData.CertificateData;
    }
    const CertificateIssue = `
    query CertificateIssue {
      recommendation: recommendations_list(where: __WHERE__) {
        rows {
          recommendation
        }
      }
    }
    `;
    const where: any = {};
    where.account_id = { _eq: accountId };
    where.category = { _eq: 'Configuration' };
    where.rule_name = { _eq: 'certificate_expiry' };
    where.status = { _in: ['Open'] };
    const response = await queryGraphQL(CertificateIssue.replace('__WHERE__', gqlStringify(where)), 'CertificateIssue', {});
    return response;
  },
};
export default apiDashboard;
