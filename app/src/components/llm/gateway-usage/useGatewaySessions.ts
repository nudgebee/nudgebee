/**
 * useGatewaySessions — fetches a page of aggregated sessions (Sessions tab).
 *
 * Mirrors `useGatewayRequests`: refetches on any change to a query field (date
 * window · optional user scope · session-id search · limit · offset), owns
 * loading/error/data, and aborts in-flight requests so a fast filter change never
 * lands a stale response.
 */
import * as React from 'react';
import { listGatewaySessions, type GatewaySessionList } from '@api1/gateway-usage';

export interface GatewaySessionsData {
  loading: boolean;
  error: string | null;
  data: GatewaySessionList | null;
}

export function useGatewaySessions(
  filters: { startDate: string; endDate: string },
  opts: { userId?: string; search?: string; limit: number; offset: number }
): GatewaySessionsData {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [data, setData] = React.useState<GatewaySessionList | null>(null);

  const dataKey = JSON.stringify({
    startDate: filters.startDate,
    endDate: filters.endDate,
    userId: opts.userId ?? '',
    search: opts.search ?? '',
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
        const list = await listGatewaySessions(
          {
            startDate: `${filters.startDate}T00:00:00Z`,
            endDate: `${filters.endDate}T23:59:59.999Z`,
            userId: opts.userId,
            search: opts.search,
            limit: opts.limit,
            offset: opts.offset,
          },
          controller.signal
        );
        if (cancelled) return;
        setData(list);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load gateway sessions');
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
