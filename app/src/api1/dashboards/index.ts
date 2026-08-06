import { queryGraphQL } from '@lib/HttpService';
import type {
  Dashboard,
  DashboardBinding,
  DashboardDeleteResult,
  DashboardListRequest,
  DashboardResolveRequest,
  DashboardSaveRequest,
  DashboardVersion,
  EntityQueryRequest,
  PanelQueryRequest,
  PanelQueryResult,
} from './types';

// `definition` and `tags` are selected as leaves — the gateway's applySelection
// returns a field whole when it carries no sub-selection, so the panel document
// comes back intact without mirroring its shape in the query.
const DASHBOARD_FIELDS = `
    id
    slug
    title
    description
    definition
    tags
    status
    is_builtin
    created_by
    updated_by
    created_at
    updated_at
    version`;

export const LIST_DASHBOARDS = `
query ListDashboards($request: DashboardListRequest!) {
  dashboards_list(request: $request) {${DASHBOARD_FIELDS}
  }
}`;

export const GET_DASHBOARD = `
query GetDashboard($request: DashboardGetRequest!) {
  dashboards_get(request: $request) {
    dashboard {${DASHBOARD_FIELDS}
    }
    bindings {
      id
      dashboard_id
      scope_type
      match_kind
      match_value
      priority
    }
  }
}`;

export const LIST_CONTEXTUAL_DASHBOARDS = `
query ListContextualDashboards($request: DashboardResolveRequest!) {
  dashboards_list_contextual(request: $request) {${DASHBOARD_FIELDS}
  }
}`;

export const LIST_DASHBOARD_VERSIONS = `
query ListDashboardVersions($request: DashboardGetRequest!) {
  dashboards_list_versions(request: $request) {
    version
    message
    created_by
    created_at
  }
}`;

export const CREATE_DASHBOARD = `
mutation CreateDashboard($request: DashboardSaveRequest!) {
  dashboards_create(request: $request) {${DASHBOARD_FIELDS}
  }
}`;

export const UPDATE_DASHBOARD = `
mutation UpdateDashboard($request: DashboardSaveRequest!) {
  dashboards_update(request: $request) {${DASHBOARD_FIELDS}
  }
}`;

export const EXECUTE_PANEL_QUERY = `
mutation ExecutePanelQuery($request: DashboardExecuteQueryRequest!) {
  dashboards_execute_query(request: $request) {
    columns
    rows
    truncated
  }
}`;

export const EXECUTE_ENTITY_QUERY = `
mutation ExecuteEntityQuery($request: DashboardEntityQueryRequest!) {
  dashboards_execute_entity_query(request: $request) {
    columns
    rows
    truncated
  }
}`;

export const DELETE_DASHBOARD = `
mutation DeleteDashboard($request: DashboardDeleteRequest!) {
  dashboards_delete(request: $request) {
    id
    deleted
  }
}`;

/**
 * Errors are returned, never swallowed — every caller renders a real message
 * rather than an empty list that looks like "you have no dashboards".
 */
type Result<T> = { data: T | null; errors?: unknown };

const apiDashboards = {
  async listDashboards(request: DashboardListRequest): Promise<Result<Dashboard[]>> {
    const response = await queryGraphQL(LIST_DASHBOARDS, 'ListDashboards', { request });
    return {
      data: response?.data?.data?.dashboards_list ?? null,
      errors: response?.data?.errors,
    };
  },

  async getDashboard(id: string): Promise<Result<{ dashboard: Dashboard; bindings: DashboardBinding[] }>> {
    const response = await queryGraphQL(GET_DASHBOARD, 'GetDashboard', { request: { id } });
    return {
      data: response?.data?.data?.dashboards_get ?? null,
      errors: response?.data?.errors,
    };
  },

  /** Dashboards that auto-attach to one entity (workload / pod / node / …). */
  async listContextualDashboards(request: DashboardResolveRequest): Promise<Result<Dashboard[]>> {
    const response = await queryGraphQL(LIST_CONTEXTUAL_DASHBOARDS, 'ListContextualDashboards', { request });
    return {
      data: response?.data?.data?.dashboards_list_contextual ?? null,
      errors: response?.data?.errors,
    };
  },

  async listVersions(id: string): Promise<Result<DashboardVersion[]>> {
    const response = await queryGraphQL(LIST_DASHBOARD_VERSIONS, 'ListDashboardVersions', { request: { id } });
    return {
      data: response?.data?.data?.dashboards_list_versions ?? null,
      errors: response?.data?.errors,
    };
  },

  async saveDashboard(request: DashboardSaveRequest): Promise<Result<Dashboard>> {
    const isUpdate = Boolean(request.id);
    const query = isUpdate ? UPDATE_DASHBOARD : CREATE_DASHBOARD;
    const opName = isUpdate ? 'UpdateDashboard' : 'CreateDashboard';
    const response = await queryGraphQL(query, opName, { request });
    return {
      data: response?.data?.data?.[isUpdate ? 'dashboards_update' : 'dashboards_create'] ?? null,
      errors: response?.data?.errors,
    };
  },

  /**
   * Runs one redis / rabbitmq panel against one account. The command is
   * re-validated server-side against a read-only allowlist — the editor's
   * hints are guidance, not the guard.
   */
  async executePanelQuery(request: PanelQueryRequest): Promise<Result<PanelQueryResult>> {
    const response = await queryGraphQL(EXECUTE_PANEL_QUERY, 'ExecutePanelQuery', { request });
    return {
      data: response?.data?.data?.dashboards_execute_query ?? null,
      errors: response?.data?.errors,
    };
  },

  /**
   * Runs a `nudgebee` panel against the internal query engine. The server
   * enforces the table allowlist and appends the account + time filters; the
   * stored query carries neither.
   */
  async executeEntityQuery(request: EntityQueryRequest): Promise<Result<PanelQueryResult>> {
    const response = await queryGraphQL(EXECUTE_ENTITY_QUERY, 'ExecuteEntityQuery', { request });
    return {
      data: response?.data?.data?.dashboards_execute_entity_query ?? null,
      errors: response?.data?.errors,
    };
  },

  async deleteDashboard(id: string): Promise<Result<DashboardDeleteResult>> {
    const response = await queryGraphQL(DELETE_DASHBOARD, 'DeleteDashboard', { request: { id } });
    return {
      data: response?.data?.data?.dashboards_delete ?? null,
      errors: response?.data?.errors,
    };
  },
};

export default apiDashboards;
export * from './types';
