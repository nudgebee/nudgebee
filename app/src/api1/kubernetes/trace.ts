import getMockData from '@api1/mock';
import { gqlStringify, queryGraphQL } from '@lib/HttpService';

interface TraceV2Params {
  accountId: string;
  namespace: string[];
  workload: string[];
  destinationNamespace: string[];
  destinationWorkload: string[];
  destinationName: string;
  limit: number;
  offset: number;
  startDate: string;
  endDate: string;
  selectedHttpStatus: string | string[];
  selectedHttpSpan: string;
  resource: string;
  duration: number | null;
  sortCol: string;
  sortOrder: string;
  header: string;
  selectedStatusCode: string;
  traceSource?: string;
  onlyCount?: boolean;
  traceId?: string | string[];
  fromWorkload?: boolean;
  cols: string[];
}

const apiTrace = {
  async traceV2(params: TraceV2Params) {
    const {
      accountId,
      namespace,
      workload,
      destinationNamespace,
      destinationWorkload,
      destinationName,
      limit,
      offset,
      startDate,
      endDate,
      selectedHttpStatus,
      selectedHttpSpan,
      resource,
      duration,
      sortCol,
      sortOrder,
      header,
      selectedStatusCode,
      traceSource,
      onlyCount,
      traceId,
      fromWorkload = false,
      cols = [
        'trace_id',
        'span_id',
        'parent_span_id',
        'workload_namespace',
        'workload_name',
        'timestamp',
        'status_code',
        'span_name',
        'resource',
        'duration_ns',
        'destination_workload_name',
        'destination_workload_namespace',
        'destination_name',
        'headers',
        'http_status_code',
        'request_payload',
        'http_response',
        'trace_source',
      ],
    } = params;

    if (accountId === 'demo') {
      const tracesMock = await getMockData('k8s-traces');
      return tracesMock.traceV2.data;
    }

    const query: any = {};
    const orderBy: any[] = [];
    if (sortCol) {
      orderBy.push({
        column: sortCol,
        order: sortOrder,
      });
    }
    const or = [];
    if (namespace?.[0] == destinationNamespace?.[0] && workload?.[0] == destinationWorkload?.[0] && fromWorkload) {
      or.push(
        {
          _binary: {
            destination_workload_name: { _in: destinationWorkload },
            destination_workload_namespace: { _in: destinationNamespace },
          },
        },
        {
          _binary: {
            workload_name: { _in: workload },
            workload_namespace: { _in: namespace },
          },
        }
      );
    } else {
      if (namespace && namespace.length > 0) {
        query['workload_namespace'] = { _in: namespace };
      }
      if (workload && workload.length > 0) {
        query['workload_name'] = { _in: workload };
      }
      if (destinationNamespace && destinationNamespace.length > 0) {
        query['destination_workload_namespace'] = { _in: destinationNamespace };
      }
      if (destinationWorkload && destinationWorkload.length > 0) {
        query['destination_workload_name'] = { _in: destinationWorkload };
      }
    }
    if (destinationName) {
      query['destination_name'] = { _eq: destinationName };
    }
    if (selectedHttpStatus && Array.isArray(selectedHttpStatus)) {
      query['http_status_code'] = { _in: selectedHttpStatus };
    } else if (selectedHttpStatus) {
      query['http_status_code'] = { _eq: selectedHttpStatus };
    }
    if (selectedHttpSpan) {
      query['span_name'] = { _eq: selectedHttpSpan };
    }
    if (resource) {
      query['resource'] = { _like: '%' + resource + '%' };
    }
    if (duration) {
      query['duration_ns'] = { _gte: duration };
    }
    if (header) {
      query['headers'] = { _like: '%' + header + '%' };
    }
    if (selectedStatusCode) {
      query['status_code'] = { _eq: selectedStatusCode };
    }
    if (traceSource?.toLocaleLowerCase()) {
      query['trace_source'] = { _eq: traceSource?.toLocaleLowerCase() };
    }
    if (traceId && Array.isArray(traceId) && traceId.length > 0) {
      query['trace_id'] = { _in: traceId };
    } else if (typeof traceId === 'string' && traceId) {
      query['trace_id'] = { _eq: traceId };
    }
    const trace_request = {
      account_id: accountId,
      query: '',
      start_time: startDate ? new Date(startDate).getTime() : 0,
      end_time: endDate ? new Date(endDate).getTime() : 0,
      query_request: {
        where: {
          _binary: query,
          ...(Array.isArray(or) && or.length > 0 ? { _or: or } : {}),
        },
        having: {},
        limit: limit,
        offset: offset,
        order_by: orderBy,
      },
    };

    const trace_count_request = {
      account_id: accountId,
      query: '',
      start_time: startDate ? new Date(startDate).getTime() : 0,
      end_time: endDate ? new Date(endDate).getTime() : 0,
      query_request: {
        where: {
          _binary: query,
          _or: or,
        },
        having: {},
      },
    };

    let TRACE_V3 = `
        query TraceV3 {
          traces_query(request: __WHERE__) {
              __COLS__
            
          }
          traces_counts(request: __WHERE1__) {
            count
          }
        }
        `;

    if (onlyCount) {
      TRACE_V3 = `
        query TraceV3 {
          traces_counts(request: __WHERE1__) {
            count
          }
        }
        `;
    }

    const response = await queryGraphQL(
      TRACE_V3.replaceAll('__WHERE1__', gqlStringify(trace_count_request))
        .replaceAll('__WHERE__', gqlStringify(trace_request))
        .replace('__COLS__', cols.join(' ')),
      'TraceV3',
      {}
    );
    return response?.data?.data;
  },
  async traceDistinctWorloadAndNamespace(accountId: string, data: any) {
    let TraceListingFiltersV3 = `
    query TraceListingFiltersV3($accountId: String!, $startTime: Float, $endTime: Float) {
      workload_name: traces_label_values(request: {account_id: $accountId, label: "workload_name", start_time: $startTime, end_time: $endTime, query_request: {where: __WHERE2__}}) {
        label
        values
      }
      workload_namespace: traces_label_values(request: {account_id: $accountId, label: "workload_namespace", start_time: $startTime, end_time: $endTime, query_request: {where: __WHERE2__}}) {
        label
        values
      }
      destination_workload_name: traces_label_values(request: {account_id: $accountId, label: "destination_workload_name", start_time: $startTime, end_time: $endTime, query_request: {where: __WHERE2__}}) {
        label
        values
      }
      destination_workload_namespace: traces_label_values(request: {account_id: $accountId, label: "destination_workload_namespace", start_time: $startTime, end_time: $endTime, query_request: {where: __WHERE2__}}) {
        label
        values
      }
    }
    `;
    if (accountId === 'demo') return null;
    const where: any = {};
    const binary: any = {};
    if (data.httpStatusCode) {
      where['http_status_code'] = { _eq: data.httpStatusCode };
    }
    if (data.statusCode) {
      where['status_code'] = { _eq: data.statusCode };
    }
    if (Object.keys(binary).length > 0) {
      where['_binary'] = binary;
    }
    TraceListingFiltersV3 = TraceListingFiltersV3.replaceAll('__WHERE2__', `${gqlStringify(where)}`);
    const response = await queryGraphQL(TraceListingFiltersV3, 'TraceListingFiltersV3', {
      accountId,
      startTime: data.startDate ? new Date(data.startDate).getTime() : 0,
      endTime: data.endDate ? new Date(data.endDate).getTime() : 0,
    });
    return response?.data?.data;
  },
  async traceDistinctFilters(accountId: string, data: any) {
    let TraceListingFiltersV3 = `
    query TraceListingFiltersV3($accountId: String!, $startTime: Float, $endTime: Float) {
      span_name: traces_label_values(request: {account_id: $accountId, label: "span_name", start_time: $startTime, end_time: $endTime, query_request: {where: __WHERE__}}) {
        label
        values
      }
      http_status_code: traces_label_values(request: {account_id: $accountId, label: "http_status_code", start_time: $startTime, end_time: $endTime, query_request: {where: __WHERE__}}) {
        label
        values
      }
      status_code: traces_label_values(request: {account_id: $accountId, label: "status_code", query_request: {where: __WHERE__}}) {
        label
        values
      }
    }
    `;
    if (accountId === 'demo') return null;
    const where: any = {};
    const binary: any = {};
    if (data.destinationNamespace) {
      binary['destination_workload_namespace'] = { _eq: data.destinationNamespace };
    }
    if (data.destinationName || data.destinationWorkload) {
      binary['destination_workload_name'] = { _eq: data.destinationName || data.destinationWorkload };
    }
    if (Object.keys(binary).length > 0) {
      where['_binary'] = binary;
    }
    TraceListingFiltersV3 = TraceListingFiltersV3.replaceAll('__WHERE__', `${gqlStringify(where)}`);
    const response = await queryGraphQL(TraceListingFiltersV3, 'TraceListingFiltersV3', {
      accountId,
      startTime: data.startDate ? new Date(data.startDate).getTime() : 0,
      endTime: data.endDate ? new Date(data.endDate).getTime() : 0,
    });
    return response?.data?.data;
  },
  async traceGroupV2(
    accountId: string,
    namespace: string | string[],
    workload: string | string[],
    destinationNamespace: string | string[],
    destinationWorkload: string | string[],
    destinationName: string,
    limit: number,
    offset: number,
    startDate: string,
    endDate: string,
    selectedHttpStatus: string,
    resource: string,
    spanType: string,
    sortCol: string,
    sortOrder: string
  ) {
    if (accountId === 'demo') {
      const tracesMock = await getMockData('k8s-traces');
      return tracesMock.traceGroupV2.data;
    }
    const binary: any = {};
    const orderBy: any[] = [];
    const and: any[] = [];
    if (sortCol) {
      orderBy.push({
        column: sortCol,
        order: sortOrder,
      });
    }
    if (Array.isArray(namespace)) {
      binary['workload_namespace'] = { _in: namespace };
    } else if (namespace) {
      binary['workload_namespace'] = { _eq: namespace };
    }
    if (Array.isArray(workload)) {
      binary['workload_name'] = { _in: workload };
    } else if (workload) {
      binary['workload_name'] = { _eq: workload };
    }
    if (Array.isArray(destinationNamespace)) {
      binary['destination_workload_namespace'] = { _in: destinationNamespace };
    } else if (destinationNamespace) {
      binary['destination_workload_namespace'] = { _eq: destinationNamespace };
    }
    if (Array.isArray(destinationWorkload)) {
      binary['destination_workload_name'] = { _in: destinationWorkload };
    } else if (destinationWorkload) {
      binary['destination_workload_name'] = { _eq: destinationWorkload };
    }
    if (destinationName) {
      binary['destination_name'] = { _eq: destinationName };
    }
    if (selectedHttpStatus) {
      and.push(
        {
          _binary: {
            http_status_code: { _eq: selectedHttpStatus },
          },
        },
        {
          _binary: {
            http_status_code: { _neq: '' },
          },
        }
      );
    }
    if (resource) {
      binary['resource'] = { _like: '%' + resource + '%' };
    }
    if (spanType == 'http') {
      binary['span_name'] = { _neq: 'query' };
    } else if (spanType == 'query') {
      binary['span_name'] = { _eq: 'query' };
    }
    const trace_group_request = {
      account_id: accountId,
      query: '',
      start_time: startDate ? new Date(startDate).getTime() : 0,
      end_time: endDate ? new Date(endDate).getTime() : 0,
      query_request: {
        where: {
          _binary: binary,
          ...(Array.isArray(and) && and.length > 0 ? { _and: and } : {}),
        },
        having: {},
        limit: limit,
        offset: offset,
        order_by: orderBy,
      },
    };

    const trace_group_count_request = {
      account_id: accountId,
      query: '',
      start_time: startDate ? new Date(startDate).getTime() : 0,
      end_time: endDate ? new Date(endDate).getTime() : 0,
      query_request: {
        where: {
          _binary: binary,
          ...(Array.isArray(and) && and.length > 0 ? { _and: and } : {}),
        },
        having: {},
      },
    };

    const TRACE_GROUPING_V3 = `
    query TraceGroupingV3 {
      traces_grouping_v3(request: __WHERE__) {
        p99_latency
        workload_name
        error_count
        count
        http_status_code
        p95_latency
        resource
        span_name
        workload_namespace
        max_latency
        duration_ns
        destination_workload_zone
        destination_workload_namespace
        destination_workload_name
      }
      traces_grouping_count_v3(request: __WHERE1__) {
        count
      }
    }
        `;
    const response = await queryGraphQL(
      TRACE_GROUPING_V3.replace('__WHERE1__', gqlStringify(trace_group_count_request)).replaceAll('__WHERE__', gqlStringify(trace_group_request)),
      'TraceGroupingV3',
      {}
    );
    return response?.data?.data || {};
  },
  async traceGroupZones({
    accountId,
    namespace,
    workload,
    destinationNamespace,
    destinationWorkload,
    destinationName,
    limit,
    offset,
    startDate,
    endDate,
  }: {
    accountId: string;
    namespace: string;
    workload: string;
    destinationNamespace: string;
    destinationWorkload: string;
    destinationName: string;
    limit: number;
    offset: number;
    startDate: string;
    endDate: string;
  }) {
    if (accountId === 'demo') {
      const tracesMock = await getMockData('k8s-traces');
      return tracesMock.traceGroupZones.data;
    }

    const query: any = {};
    query['account_id'] = { _eq: accountId };
    if (namespace) {
      query['workload_namespace'] = { _eq: namespace };
    }
    if (workload) {
      query['workload_name'] = { _eq: workload };
    }
    if (destinationNamespace) {
      query['destination_workload_namespace'] = { _eq: destinationNamespace };
    }
    if (destinationWorkload) {
      query['destination_workload_name'] = { _eq: destinationWorkload };
    }
    if (destinationName) {
      query['destination_name'] = { _eq: destinationName };
    }
    if (startDate && endDate) {
      query['timestamp'] = { _between: { _gte: startDate, _lte: endDate } };
    }

    query['_and'] = [
      {
        destination_workload_zone: {
          _is_null: false,
        },
      },
      {
        workload_zone: {
          _is_null: false,
        },
      },
      {
        workload_zone: {
          _neq: '',
        },
      },
      {
        destination_workload_zone: {
          _neq: '',
        },
      },
      {
        destination_workload_zone: {
          _neq_f: 'workload_zone',
        },
      },
    ];

    const TRACE_GROUPING_V2 = `
        query TraceGroupingV2($limit: Int!, $offset: Int!) {
          traces_groupings_v2_aggregate: traces_groupings_v2(where:__WHERE__, group_by:[], column_transformations:[{name: "count", expr: "distinct", args: ["workload_name", "workload_zone","destination_workload_name", "destination_workload_zone", "workload_namespace", "destination_workload_namespace"]}], columns:["count"]){
            rows{
              count
            }
          }
          traces_groupings: traces_groupings_v2(limit: $limit, offset: $offset, , order_by: [{column: "count", order: desc}], where: __WHERE__, group_by:["destination_workload_name", "destination_workload_zone", "workload_zone", "workload_name", "destination_workload_namespace", "workload_namespace"], columns: ["count", "workload_name", "workload_zone","destination_workload_name", "destination_workload_zone", "workload_namespace", "destination_workload_namespace"]) {
            rows {
              count
              workload_name
              workload_zone
              workload_namespace
              destination_workload_name
              destination_workload_namespace
              destination_workload_zone
            }
          }
        }    
        `;
    const response = await queryGraphQL(TRACE_GROUPING_V2.replaceAll('__WHERE__', gqlStringify(query)), 'TraceGroupingV2', {
      limit: limit,
      offset: offset,
    });
    return response?.data?.data;
  },
  async traceServiceAndOperationV2(accountId: string, traceId: string) {
    if (accountId == 'demo') {
      const tracesServiceOperationMock = await getMockData('k8s-traces-service-operation');
      return tracesServiceOperationMock.data;
    }

    const query: any = {};
    query['account_id'] = accountId;
    query['trace_id'] = traceId;
    const TRACE_SERVICE_OPERATION_V2 = `
    query TracesServiceOperations {
      traces_heat_map(request: __WHERE__) {
        resource_attributes
        span_name
        status_code
        timestamp
        duration_ns
        span_attributes
        trace_id
        span_id
        service_name
        events_attributes
        events_name
      }
    }    
        `;
    const response = await queryGraphQL(TRACE_SERVICE_OPERATION_V2.replaceAll('__WHERE__', gqlStringify(query)), 'TracesServiceOperations', {});
    return response?.data?.data;
  },
  async traceGroupQueryV2(
    accountId: string,
    namespace: string,
    workload: string,
    resource: string,
    limit: number,
    offset: number,
    startDate: string,
    endDate: string
  ) {
    const TRACE_GROUP_QUERY_V2 = `
    query TraceGroupingQueryV2($limit: Int!, $offset: Int!) {
      traces_groupings_v2_aggregate: traces_groupings_v2(where: __WHERE__) {
        rows {
          count
        }
      }
      traces_groupings: traces_groupings_v2(limit: $limit, offset: $offset, order_by: [{column: "duration_ns", order: desc}], where: __WHERE__, group_by: ["destination_workload_name", "resource"], columns: ["duration_ns", "workload_name", "span_name", "destination_workload_name", "resource", "workload_namespace", "destination_workload_namespace", "status_code"]) {
        rows {
          workload_name
          workload_namespace
          destination_workload_name
          destination_workload_namespace
          resource
          status_code
          duration_ns
        }
      }
    }
    `;
    if (accountId === 'demo') {
      return null;
    }
    const query: any = {};
    query['account_id'] = { _eq: accountId };
    query['span_name'] = { _eq: 'query' };
    if (namespace) {
      query['workload_namespace'] = { _eq: namespace };
    }
    if (workload) {
      query['workload_name'] = { _eq: workload };
    }
    if (startDate && endDate) {
      query['timestamp'] = { _between: { _gte: startDate, _lte: endDate } };
    }
    if (resource) {
      query['resource'] = { _like: '%' + resource + '%' };
    }
    const response = await queryGraphQL(TRACE_GROUP_QUERY_V2.replaceAll('__WHERE__', gqlStringify(query)), 'TraceGroupingQueryV2', {
      limit: limit,
      offset: offset,
    });
    return response?.data?.data;
  },
};

export default apiTrace;
