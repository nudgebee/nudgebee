/**
 * useGatewayData — fetches AI Gateway usage aggregates for the current filters.
 *
 * Mirrors the cost-analyser's `useCostData`: it fetches `llm_gateway_aggregate_usage`
 * on every change to a request field (account · date window · granularity), owns
 * loading/error/data state, and cleans up in-flight requests with an
 * AbortController so a fast filter change never lands a stale response.
 */
import * as React from 'react';
import { aggregateGatewayUsage, type GatewayGranularity, type GatewayUsageMetrics } from '@api1/gateway-usage';

export interface GatewayFilters {
  /** Inclusive day bounds, 'YYYY-MM-DD' (bridged to RFC3339 UTC for the request). */
  startDate: string;
  endDate: string;
  granularity: GatewayGranularity;
}

export interface GatewayData {
  loading: boolean;
  error: string | null;
  metrics: GatewayUsageMetrics | null;
  reload: () => void;
}

export function useGatewayData(accountId: string | undefined, filters: GatewayFilters): GatewayData {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [metrics, setMetrics] = React.useState<GatewayUsageMetrics | null>(null);
  const [nonce, setNonce] = React.useState(0);

  // Refetch key — every request-backed field plus the manual-reload nonce. Tenant
  // is server-derived (x-tenant-id header), so it isn't part of the request.
  const dataKey = JSON.stringify({
    accountId: accountId ?? '',
    startDate: filters.startDate,
    endDate: filters.endDate,
    granularity: filters.granularity,
    nonce,
  });

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setLoading(true);
      setError(null);
      try {
        const m = await aggregateGatewayUsage(
          {
            accountIds: accountId ? [accountId] : [],
            startDate: `${filters.startDate}T00:00:00Z`,
            endDate: `${filters.endDate}T23:59:59.999Z`,
            granularity: filters.granularity,
          },
          controller.signal
        );
        if (cancelled) return;
        setMetrics(m);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load gateway usage');
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

  return { loading, error, metrics, reload: () => setNonce((n) => n + 1) };
}
