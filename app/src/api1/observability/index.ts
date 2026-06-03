import { gqlStringify, queryGraphQL } from '@lib/HttpService';

const FETCH_LOGS = `
query FetchLogs(
  $account_id: String!
  $query: String!
  $start_time: Float!
  $end_time: Float!
  $limit: Int
  $offset: Int
  $log_provider: String
  $log_provider_source: String
  $sort_fields: [SortField]
  $step_interval: Int
  $query_request: jsonb
  $request: jsonb
) {
  logs_list(request: {
    account_id: $account_id
    query: $query
    start_time: $start_time
    end_time: $end_time
    limit: $limit
    offset: $offset
    log_provider: $log_provider
    log_provider_source: $log_provider_source
    sort_fields: $sort_fields
    step_interval: $step_interval
    query_request: $query_request
    request: $request
  }) {
    timestamp
    severity
    message
    labels
  }
}
`;

const FETCH_LOG_LABELS = `
query FetchLogLabels {
  logs_list_labels(request: __WHERE__) {
    label
    attributes
  }
}
`;

const FETCH_LOG_LABEL_VALUES = `
query FetchLogLabelValues {
  logs_list_label_values(request: __WHERE__) {
    value
    attributes
  }
}
`;

const USER_HISTORY = `
mutation UserHistory {
  users_create_history(request: __WHERE__) {
    status
  }
}
`;

const observability = {
  async fetchLogs(data: any) {
    try {
      if (data.account_id === 'demo') {
        return {
          data: {
            data: {
              logs_list: [],
            },
          },
        };
      }
      const response = await queryGraphQL(FETCH_LOGS, 'FetchLogs', {
        account_id: data.account_id,
        query: data.query,
        start_time: data.start_time,
        end_time: data.end_time,
        limit: data.limit,
        offset: data.offset,
        log_provider: data.log_provider,
        log_provider_source: data.log_provider_source,
        sort_fields: data.sort_fields,
        step_interval: data.step_interval,
        query_request: data.query_request,
        request: data.request,
      });
      return response;
    } catch (error) {
      console.log('failed to fetch logs-', error);
      throw error;
    }
  },

  async fetchLogLabels(data: any) {
    try {
      const response = await queryGraphQL(FETCH_LOG_LABELS.replaceAll('__WHERE__', gqlStringify(data)), 'FetchLogLabels');
      return response;
    } catch (error) {
      console.log('failed to fetch log labels-', error);
      throw error;
    }
  },

  async fetchLogLabelValues(data: any) {
    try {
      const response = await queryGraphQL(FETCH_LOG_LABEL_VALUES.replaceAll('__WHERE__', gqlStringify(data)), 'FetchLogLabelValues');
      return response;
    } catch (error) {
      console.log('failed to fetch log label values-', error);
      throw error;
    }
  },

  async createUserHistory(data: any) {
    if (!data.data) {
      return;
    }
    const response = await queryGraphQL(USER_HISTORY.replace('__WHERE__', gqlStringify(data)), 'UserHistory');
    return response;
  },

  async metricsList(accountId: string, options?: { metricProvider?: string; metricProviderSource?: string; serviceName?: string }) {
    if (accountId == 'demo') {
      return [];
    }
    const METRICS_LIST = `
    query MetricsList($accountId: String!, $metricProvider: String, $metricProviderSource: String, $request: jsonb) {
      metrics_list_names(request: {account_id: $accountId, metric_provider: $metricProvider, metric_provider_source: $metricProviderSource, request: $request}) {
        metric
        attributes
      }
    }
    `;
    try {
      const variables: Record<string, any> = { accountId };
      if (options?.metricProvider) {
        variables.metricProvider = options.metricProvider;
      }
      if (options?.metricProviderSource) {
        variables.metricProviderSource = options.metricProviderSource;
      }
      if (options?.serviceName) {
        variables.request = { service_name: options.serviceName };
      }
      const response = await queryGraphQL(METRICS_LIST, 'MetricsList', variables);
      return response;
    } catch (err) {
      console.error('Failed to fetch metrics list:', err);
      return err;
    }
  },

  async metricsLabelList(accountId: string, metricName: string) {
    if (accountId === 'demo') return null;
    const METRICS_LABEL_LIST = `
    query MetricsLabelList($accountId: String!, $metricName: String!) {
      metrics_list_labels(request: {account_id: $accountId, metric: $metricName}) {
        label
        attributes
      }
    }
    `;
    try {
      const response = await queryGraphQL(METRICS_LABEL_LIST, 'MetricsLabelList', {
        accountId,
        metricName: metricName,
      });
      return response;
    } catch (err) {
      console.error('Failed to fetch metrics labels:', err);
      return err;
    }
  },

  async logIndexFields(accountId: string, indexName: string) {
    if (accountId === 'demo') return null;
    try {
      const query: any = {
        account_id: accountId,
        request: {
          index: indexName,
        },
        fetch_index: true,
      };
      const response = await queryGraphQL(FETCH_LOG_LABELS.replace('__WHERE__', gqlStringify(query)), 'FetchLogLabels', {});
      return response;
    } catch (err) {
      console.error('Failed to fetch index fields:', err);
      return err;
    }
  },

  async metricsLabelValueList(accountId: string, labelName: string, request: Record<string, string>) {
    if (accountId === 'demo') return null;
    const METRICS_LABEL_VALUE_LIST = `
    query MetricsLabelValueList($accountId: String!, $labelName: String!, $request: jsonb) {
      metrics_list_label_values(request: {account_id: $accountId, label: $labelName, request: $request}) {
        value
        attributes
      }
    }
    `;
    try {
      const response = await queryGraphQL(METRICS_LABEL_VALUE_LIST, 'MetricsLabelValueList', {
        accountId,
        labelName: labelName,
        request: request,
      });
      return response;
    } catch (err) {
      console.error('Failed to fetch metrics label values:', err);
      return err;
    }
  },

  async metricsQuery(data: any) {
    const METRICS_QUERY = `
    query MetricsQuery(
      $account_id: String!
      $queries: jsonb!
      $instant: Boolean!
      $start_time: Float!
      $end_time: Float!
      $request: jsonb
      $metric_provider: String
      $metric_provider_source: String
    ) {
      metrics_list(
        request: {
          account_id: $account_id
          queries: $queries
          instant: $instant
          start_time: $start_time
          end_time: $end_time
          request: $request
          metric_provider: $metric_provider
          metric_provider_source: $metric_provider_source
        }
      ) {
        results
      }
    }
    `;
    try {
      if (data.account_id === 'demo') {
        return {
          data: {
            data: {
              metrics_list: {
                results: [],
              },
            },
          },
        };
      }
      const response = await queryGraphQL(METRICS_QUERY, 'MetricsQuery', {
        account_id: data.account_id,
        queries: data.queries,
        start_time: data.start_time,
        end_time: data.end_time,
        instant: data.instant || false,
        request: data.request,
        metric_provider: data.metric_provider,
        metric_provider_source: data.metric_provider_source,
      });
      return response;
    } catch (err) {
      console.error('Failed to fetch metrics query:', err);
      throw err;
    }
  },

  async getFormattedQuery(data: any) {
    const GET_FORMATTED_QUERY = `
    mutation GetFormattedQuery(
      $request: FetchLogQueryRequest!
    ) {
      logs_get_query(
        request: $request
      ) {
        query
      }
    }
    `;
    try {
      if (data.account_id === 'demo') {
        return {
          data: {
            data: {
              logs_get_query: {
                query: '',
              },
            },
          },
        };
      }
      const response = await queryGraphQL(GET_FORMATTED_QUERY, 'GetFormattedQuery', {
        request: {
          account_id: data.account_id,
          query_request: data.query_request,
          start_time: data.start_time,
          end_time: data.end_time,
        },
      });
      return response;
    } catch (err) {
      console.error('Failed to fetch formatted query:', err);
      throw err;
    }
  },

  async getMetricsQuery(data: any) {
    const GET_METRICS_QUERY = `
    query GetMetricsQuery($request: FetchMetricsRequest!) {
      metrics_get_query(request: $request) {
        results
      }
    }
    `;
    try {
      if (data.account_id === 'demo') {
        return {
          data: {
            data: {
              metrics_get_query: {
                results: {},
              },
            },
          },
        };
      }
      const response = await queryGraphQL(GET_METRICS_QUERY, 'GetMetricsQuery', {
        request: {
          account_id: data.account_id,
          query_items: data.query_items,
          start_time: data.start_time,
          end_time: data.end_time,
          instant: data.instant || false,
        },
      });
      return response;
    } catch (err) {
      console.error('Failed to fetch metrics get query:', err);
      throw err;
    }
  },
};

export default observability;
