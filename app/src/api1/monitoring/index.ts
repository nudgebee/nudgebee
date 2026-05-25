import { queryGraphQL, gqlStringify } from '@lib/HttpService';

export const LIST_MONITORING_WORKLOAD = `
query ListMonitoringWorkload {
  k8s_workloads_cloud_account_monitoring_v2(where: __WHERE__, order_by: __ORDER_BY__) {
    rows {
      account_id
      account_name
      name
      namespace
      workload_id
      total_pods
      ready_pods
      event_count
      creation_time
      pod_error_count
      application_error_count
      total_slo_count
      failed_slo_count
    }
  }
} 
`;

export const LIST_MONITORING_WORKLOAD_RECOMMENDATIONS_COUNT = `
query ListMonitoringWorkloadRecommendationsCount {
  k8s_workloads_cloud_account_monitoring_recommendations_v2(where: __WHERE__) {
    rows {
      account_id
      recommendation_count
      workload_name
      namespace
    }
  }
} 
`;

const apiMonitoring = {
  listMonitoringWorkload: async function (query: any) {
    try {
      if (query.accountId === 'demo') return null;
      const queryParams: any = {};
      if (query.accountId) {
        queryParams['account_id'] = { _eq: query.accountId };
      }
      if (query.namespaceName) {
        queryParams['namespace'] = { _eq: query.namespaceName };
      }
      if (query.workloadName) {
        queryParams['name'] = { _eq: query.workloadName };
      }
      const orderParams: any = [
        {
          column: 'event_count',
          order: 'desc_nulls_last',
        },
        {
          column: 'failed_slo_count',
          order: 'desc_nulls_last',
        },
      ];
      const queryStr = LIST_MONITORING_WORKLOAD.replaceAll('__WHERE__', gqlStringify(queryParams)).replaceAll(
        '__ORDER_BY__',
        gqlStringify(orderParams, ['order'])
      );
      const response = await queryGraphQL(queryStr, 'ListMonitoringWorkload');
      return response;
    } catch (err) {
      console.log('failed to list moniroting workload-', err);
      return err;
    }
  },
  listMonitoringWorkloadRecommendationCount: async function (query: any) {
    try {
      if (query.accountId === 'demo') return null;
      const queryParams: any = {};
      if (query.accountId) {
        queryParams['account_id'] = { _eq: query.accountId };
      }
      if (query.namespaceName) {
        queryParams['namespace'] = { _eq: query.namespaceName };
      }
      if (query.workloadName) {
        queryParams['name'] = { _eq: query.workloadName };
      }
      const queryStr = LIST_MONITORING_WORKLOAD_RECOMMENDATIONS_COUNT.replaceAll('__WHERE__', gqlStringify(queryParams));

      const response = await queryGraphQL(queryStr, 'ListMonitoringWorkloadRecommendationsCount');
      return response;
    } catch (err) {
      console.log('failed to list moniroting workload recommendations count-', err);
      return err;
    }
  },
};

export default apiMonitoring;
