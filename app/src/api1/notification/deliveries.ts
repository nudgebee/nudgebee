import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { isAccessDenied } from '@lib/accessDenied';

// One row per attempt to send a message to one destination (api-server notification_deliveries_v2). Lists
// and counts here read a send's final attempt only (is_final), so a retried send is one row that reads as
// its last attempt; its earlier attempts are fetched with listAttempts.
export interface Delivery {
  id: string;
  created_at: string;
  account_id?: string | null;
  fingerprint?: string | null;
  dispatch_id?: string | null;
  attempt?: number | null;
  kind?: string | null;
  source?: string | null;
  title?: string | null;
  event_id?: string | null;
  platform?: string | null;
  team_id?: string | null;
  destination?: string | null;
  destination_name?: string | null;
  message_id?: string | null;
  thread_id?: string | null;
  status?: string | null;
  reason?: string | null;
  error_code?: string | null;
  // JSON text: a list of {id, name} rules, or "default" / "requested".
  routed_by?: string | null;
  team_url?: string | null;
}

export interface DestinationSummary {
  platform: string;
  team_id?: string | null;
  destination: string;
  last_destination_name?: string | null;
  last_reason?: string | null;
  last_at?: string | null;
}

export interface DeliveryFilters {
  since: string;
  platforms?: string[];
  destination?: { platform: string; destination: string } | null;
  status?: string;
  kinds?: string[];
  fingerprint?: string;
}

interface Result<T> {
  data: T;
  error?: string;
  denied?: boolean;
}

const FIELDS = `id created_at account_id fingerprint dispatch_id attempt kind source title event_id platform team_id
  destination destination_name message_id thread_id status reason error_code routed_by team_url`;
const SUMMARY_COLUMNS = ['platform', 'team_id', 'destination', 'last_destination_name', 'last_reason', 'last_at'];

const finalWhere = (filters: DeliveryFilters) => {
  const where: Record<string, unknown> = { is_final: { _eq: true }, created_at: { _gte: filters.since } };
  if (filters.destination) {
    where.platform = { _eq: filters.destination.platform };
    where.destination = { _eq: filters.destination.destination };
  } else if (filters.platforms?.length) {
    where.platform = { _in: filters.platforms };
  }
  if (filters.status) where.status = { _eq: filters.status };
  if (filters.kinds?.length) where.kind = { _in: filters.kinds };
  if (filters.fingerprint) where.fingerprint = { _eq: filters.fingerprint };
  return where;
};

async function run<T>(query: string, operation: string, variables: Record<string, unknown>, empty: T, pick: (data: any) => T): Promise<Result<T>> {
  try {
    const response = await queryGraphQL(query, operation, variables);
    const errors = response?.data?.errors;
    if (errors?.length) {
      return { data: empty, error: errors[0]?.message || 'Delivery history did not load', denied: isAccessDenied(response) };
    }
    return { data: pick(response?.data?.data || {}) };
  } catch (error) {
    return { data: empty, error: 'Delivery history did not load', denied: isAccessDenied(error) };
  }
}

const deliveriesApi = {
  async list(filters: DeliveryFilters, limit: number, offset: number): Promise<Result<{ rows: Delivery[]; total: number }>> {
    const where = gqlStringify(finalWhere(filters));
    const query = `query ListDeliveries($limit: Int!, $offset: Int!) {
      deliveries: notifications_list_deliveries(where: ${where}, limit: $limit, offset: $offset, order_by: [{column: "created_at", order: desc}]) {
        rows { ${FIELDS} }
      }
      total: notifications_aggregate_deliveries(where: ${where}, columns: ["count"]) { rows { count } }
    }`;
    return run(query, 'ListDeliveries', { limit, offset }, { rows: [], total: 0 }, (data) => ({
      rows: data.deliveries?.rows || [],
      total: data.total?.rows?.[0]?.count || 0,
    }));
  },

  // Every attempt of one send to one destination, newest first.
  async listAttempts(delivery: Delivery): Promise<Result<Delivery[]>> {
    const where = gqlStringify({
      dispatch_id: { _eq: delivery.dispatch_id },
      platform: delivery.platform ? { _eq: delivery.platform } : { _is_null: true },
      destination: delivery.destination ? { _eq: delivery.destination } : { _is_null: true },
    });
    const query = `query ListDeliveryAttempts {
      attempts: notifications_list_deliveries(where: ${where}, limit: 10, order_by: [{column: "attempt", order: desc}]) {
        rows { id created_at attempt status reason error_code platform destination }
      }
    }`;
    return run(query, 'ListDeliveryAttempts', {}, [], (data) => data.attempts?.rows || []);
  },

  // Destinations whose latest delivery since `since` failed.
  async failingDestinations(since: string): Promise<Result<DestinationSummary[]>> {
    const where = gqlStringify({ is_final: { _eq: true }, created_at: { _gte: since }, destination: { _is_null: false } });
    const query = `query FailingDeliveryDestinations {
      failing: notifications_aggregate_deliveries(where: ${where}, group_by: ["platform", "team_id", "destination"],
        columns: ${JSON.stringify(SUMMARY_COLUMNS)}, having: {last_status: {_eq: "failed"}},
        order_by: [{column: "last_at", order: desc}], limit: 50) {
        rows { ${SUMMARY_COLUMNS.join(' ')} }
      }
    }`;
    return run(query, 'FailingDeliveryDestinations', {}, [], (data) => data.failing?.rows || []);
  },

  // The destinations delivered to since `since`, for the destination filter.
  async destinations(since: string): Promise<Result<DestinationSummary[]>> {
    const where = gqlStringify({ is_final: { _eq: true }, created_at: { _gte: since }, destination: { _is_null: false } });
    const query = `query DeliveryDestinations {
      destinations: notifications_aggregate_deliveries(where: ${where}, group_by: ["platform", "team_id", "destination"],
        columns: ${JSON.stringify(SUMMARY_COLUMNS)}, order_by: [{column: "last_at", order: desc}], limit: 200) {
        rows { ${SUMMARY_COLUMNS.join(' ')} }
      }
    }`;
    return run(query, 'DeliveryDestinations', {}, [], (data) => data.destinations?.rows || []);
  },

  // An alert's deliveries, newest first, and when the history the reader can see starts. With eventId, that
  // event's own come first and in full, however many other firings the alert has had since.
  async forAlert(fingerprint: string, eventId?: string): Promise<Result<{ rows: Delivery[]; historyStart: string | null }>> {
    const where = gqlStringify({ fingerprint: { _eq: fingerprint }, is_final: { _eq: true } });
    const query = `query AlertDeliveries {
      deliveries: notifications_list_deliveries(where: ${where}, limit: 50, order_by: [{column: "created_at", order: desc}]) {
        rows { ${FIELDS} }
      }
      history: notifications_aggregate_deliveries(columns: ["first_at"]) { rows { first_at } }
    }`;
    const alert = run<{ rows: Delivery[]; historyStart: string | null }>(query, 'AlertDeliveries', {}, { rows: [], historyStart: null }, (data) => ({
      rows: data.deliveries?.rows || [],
      historyStart: data.history?.rows?.[0]?.first_at || null,
    }));
    if (!eventId) return alert;
    // The fingerprint keeps this on the (tenant, fingerprint, created_at) index; event_id has none.
    const eventWhere = gqlStringify({ fingerprint: { _eq: fingerprint }, event_id: { _eq: eventId }, is_final: { _eq: true } });
    const eventQuery = `query EventDeliveries {
      deliveries: notifications_list_deliveries(where: ${eventWhere}, limit: 50, order_by: [{column: "created_at", order: desc}]) {
        rows { ${FIELDS} }
      }
    }`;
    const [all, own] = await Promise.all([alert, run<Delivery[]>(eventQuery, 'EventDeliveries', {}, [], (data) => data.deliveries?.rows || [])]);
    const ownIds = new Set(own.data.map((d) => d.id));
    return {
      data: { rows: [...own.data, ...all.data.rows.filter((d) => !ownIds.has(d.id))], historyStart: all.data.historyStart },
      error: all.error || own.error,
      denied: all.denied || own.denied,
    };
  },
};

export default deliveriesApi;
