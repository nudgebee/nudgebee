/** Fetches Critique Analytics data for current filters. Mirrors gateway-usage's useGatewayData. */
import * as React from 'react';
import { aggregateCritiques, listCritiques, trendCritiques, type CritiqueList, type CritiqueSummary, type CritiqueTrend } from '@api1/critiques';

export type CritiqueGranularity = 'day' | 'week' | 'month';

export interface CritiqueFilters {
  /** Inclusive day bounds, 'YYYY-MM-DD' (bridged to RFC3339 UTC for requests). */
  startDate: string;
  endDate: string;
  agents: string[];
  granularity: CritiqueGranularity;
}

export interface CritiqueData {
  loading: boolean;
  error: string | null;
  summary: CritiqueSummary | null;
  trend: CritiqueTrend | null;
  reload: () => void;
}

function toRequestWindow(f: Pick<CritiqueFilters, 'startDate' | 'endDate'>): { startDate: string; endDate: string } {
  return { startDate: `${f.startDate}T00:00:00Z`, endDate: `${f.endDate}T23:59:59Z` };
}

export function useCritiqueData(filters: CritiqueFilters): CritiqueData {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [summary, setSummary] = React.useState<CritiqueSummary | null>(null);
  const [trend, setTrend] = React.useState<CritiqueTrend | null>(null);
  const [nonce, setNonce] = React.useState(0);

  const dataKey = JSON.stringify({
    startDate: filters.startDate,
    endDate: filters.endDate,
    agents: filters.agents,
    granularity: filters.granularity,
    nonce,
  });

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setLoading(true);
      setError(null);
      const window = toRequestWindow(filters);
      try {
        const [s, t] = await Promise.all([
          aggregateCritiques({ ...window, agents: filters.agents }, controller.signal),
          trendCritiques({ ...window, agents: filters.agents, granularity: filters.granularity }, controller.signal),
        ]);
        if (cancelled) return;
        setSummary(s);
        setTrend(t);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load critique data');
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

  return { loading, error, summary, trend, reload: () => setNonce((n) => n + 1) };
}

const BROWSE_PAGE_SIZE = 50;

export interface CritiqueListState {
  loading: boolean;
  error: string | null;
  list: CritiqueList | null;
}

/** Paginated raw-row fetch for Browse — its own filter/page, independent of the aggregation above. */
export function useCritiqueList(filters: CritiqueFilters, decisions: string[], page: number, theme?: string): CritiqueListState {
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [list, setList] = React.useState<CritiqueList | null>(null);

  const dataKey = JSON.stringify({
    startDate: filters.startDate,
    endDate: filters.endDate,
    agents: filters.agents,
    decisions,
    theme,
    page,
  });

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;

    const run = async () => {
      setLoading(true);
      setError(null);
      const window = toRequestWindow(filters);
      try {
        const l = await listCritiques(
          { ...window, agents: filters.agents, decisions, theme, limit: BROWSE_PAGE_SIZE, offset: page * BROWSE_PAGE_SIZE },
          controller.signal
        );
        if (cancelled) return;
        setList(l);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load critiques');
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

  return { loading, error, list };
}

export const CRITIQUE_BROWSE_PAGE_SIZE = BROWSE_PAGE_SIZE;
