import { queryGraphQL } from '@lib/HttpService';

export interface QueryDatabasePerformanceRequest {
  account_id: string;
  database_identifier: string;
  region: string;
  start_time?: string;
  end_time?: string;
  granularity_seconds?: number;
  include_top_queries?: boolean;
  include_wait_events?: boolean;
  include_top_users?: boolean;
  include_top_hosts?: boolean;
  top_n?: number;
}

export interface PerformanceMetric {
  name: string;
  unit: string;
  timestamps: number[];
  values: number[];
}

export interface PerformanceQuery {
  query_id: string;
  query_text: string;
  database_load: number;
  execution_count: number;
  total_duration: number;
  avg_duration: number;
  min_duration?: number;
  max_duration?: number;
  avg_cpu_time?: number;
  avg_rows_processed?: number;
  cache_hit_ratio?: number;
}

export interface PerformanceWaitEvent {
  event_type: string;
  event_name: string;
  database_load: number;
  percentage: number;
  wait_count?: number;
  total_wait_time?: number;
  avg_wait_time?: number;
}

export interface PerformanceUser {
  user_name: string;
  database_load: number;
  percentage: number;
}

export interface PerformanceHost {
  host_name: string;
  database_load: number;
  percentage: number;
}

export interface QueryDatabasePerformanceResponse {
  database_identifier: string;
  provider: string;
  performance_enabled: boolean;
  load_metrics: PerformanceMetric[];
  resource_metrics: PerformanceMetric[];
  top_queries: PerformanceQuery[];
  wait_events: PerformanceWaitEvent[];
  top_users: PerformanceUser[];
  top_hosts: PerformanceHost[];
  metadata: Record<string, any>;
}

export const DATABASE_PERFORMANCE_MUTATION = `
mutation database_performance_insights($request: QueryDatabasePerformanceRequest!) {
  database_performance_insights(request: $request) {
    database_identifier
    provider
    performance_enabled
    load_metrics {
      name
      unit
      timestamps
      values
    }
    resource_metrics {
      name
      unit
      timestamps
      values
    }
    top_queries {
      query_id
      query_text
      database_load
      execution_count
      total_duration
      avg_duration
      min_duration
      max_duration
      avg_cpu_time
      avg_rows_processed
      cache_hit_ratio
    }
    wait_events {
      event_type
      event_name
      database_load
      percentage
      wait_count
      total_wait_time
      avg_wait_time
    }
    top_users {
      user_name
      database_load
      percentage
    }
    top_hosts {
      host_name
      database_load
      percentage
    }
    metadata
  }
}
`;

export const queryDatabasePerformance = async function (params: QueryDatabasePerformanceRequest): Promise<QueryDatabasePerformanceResponse> {
  try {
    if (params.account_id === 'demo') {
      return {
        database_identifier: params.database_identifier,
        provider: 'k8s',
        performance_enabled: false,
        load_metrics: [],
        resource_metrics: [],
        top_queries: [],
        wait_events: [],
        top_users: [],
        top_hosts: [],
        metadata: {},
      };
    }
    const response = await queryGraphQL(DATABASE_PERFORMANCE_MUTATION, 'database_performance_insights', {
      request: {
        account_id: params.account_id,
        database_identifier: params.database_identifier,
        region: params.region,
        start_time: params.start_time || new Date(Date.now() - 3600000).toISOString(),
        end_time: params.end_time || new Date().toISOString(),
        granularity_seconds: params.granularity_seconds || 60,
        include_top_queries: params.include_top_queries !== false,
        include_wait_events: params.include_wait_events !== false,
        include_top_users: params.include_top_users || false,
        include_top_hosts: params.include_top_hosts || false,
        top_n: params.top_n || 10,
      },
    });

    return response?.data?.data?.database_performance_insights || {};
  } catch (error) {
    console.error('failed to fetch database performance-', error);
    throw error;
  }
};
