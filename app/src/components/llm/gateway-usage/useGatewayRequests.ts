/**
 * useGatewayRequests — fetches a page of recent AI Gateway requests.
 *
 * Mirrors `useGatewayData`: it refetches on every change to a request field
 * (date window · optional user scope · limit · offset), owns loading/error/data
 * state, and cleans up in-flight requests with an AbortController so a fast
 * filter change never lands a stale response. Backs the Requests sub-tab.
 */
import * as React from 'react';
import { listGatewayRequests, type GatewayRequestList } from '@api1/gateway-usage';

export interface GatewayRequestsData {
  loading: boolean;
  error: string | null;
  data: GatewayRequestList | null;
}

export function useGatewayRequests(
  filters: { startDate: string; endDate: string },
  opts: { userId?: string; limit: number; offset: number }
): GatewayRequestsData {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [data, setData] = React.useState<GatewayRequestList | null>(null);

  // Refetch key — every request-backed field. Tenant is server-derived.
  const dataKey = JSON.stringify({
    startDate: filters.startDate,
    endDate: filters.endDate,
    userId: opts.userId ?? '',
    limit: opts.limit,
    offset: opts.offset,
  });

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setLoading(true);
      setError(null);
      try {
        const list = await listGatewayRequests(
          {
            startDate: `${filters.startDate}T00:00:00Z`,
            endDate: `${filters.endDate}T23:59:59.999Z`,
            userId: opts.userId,
            limit: opts.limit,
            offset: opts.offset,
          },
          controller.signal
        );
        if (cancelled) return;
        setData(list);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load gateway requests');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    run();
    return () => {
      cancelled = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataKey]);

  return { loading, error, data };
}
