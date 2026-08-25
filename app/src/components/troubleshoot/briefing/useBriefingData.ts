import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/router';
import apiKubernetes1 from '@api1/kubernetes1';
import apiTriage from '@api1/triage';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { getLast24Hrs } from '@lib/datetime';
import { resolveBriefing, type BriefingModel, type BriefingPayload } from './resolveBriefing';

const DAY_MS = 86400000;
const BAND_DAYS = 30;
const STUCK_AFTER_DAYS = 7;
const STUCK_LOOKBACK_DAYS = 180;

const rowsOf = (block: any): any[] => block?.rows ?? [];
const firstRow = (block: any): any => rowsOf(block)[0] ?? {};

export interface BriefingWindow {
  startMs: number;
  endMs: number;
}

export const useBriefingWindow = (): BriefingWindow => {
  const router = useRouter();
  const startParam = router.query.start_time;
  const endParam = router.query.end_time;

  return useMemo(() => {
    const start = Number(Array.isArray(startParam) ? startParam[0] : startParam);
    const end = Number(Array.isArray(endParam) ? endParam[0] : endParam);
    if (Number.isFinite(start) && Number.isFinite(end) && end > start) {
      return { startMs: start, endMs: end };
    }
    const endDate = new Date();
    return { startMs: getLast24Hrs(endDate).getTime(), endMs: endDate.getTime() };
  }, [startParam, endParam]);
};

export interface BriefingState {
  loading: boolean;
  error: boolean;
  model: BriefingModel | null;
  window: BriefingWindow;
}

export const useBriefingData = (): BriefingState => {
  const router = useRouter();
  const briefingWindow = useBriefingWindow();

  useEffect(() => {
    if (!router.isReady || router.query.start_time || router.query.end_time) return;
    router.replace(
      {
        pathname: router.pathname,
        query: { ...router.query, start_time: String(briefingWindow.startMs), end_time: String(briefingWindow.endMs) },
        hash: router.asPath.split('#')[1],
      },
      undefined,
      { shallow: true }
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.isReady, router.query.start_time, router.query.end_time]);

  const accountIdParam = router.query.accountIds;
  const accountIds = useMemo(() => (accountIdParam ? String(accountIdParam).split(',').filter(Boolean) : []), [accountIdParam]);

  const [state, setState] = useState<{ loading: boolean; error: boolean; model: BriefingModel | null }>({
    loading: true,
    error: false,
    model: null,
  });

  useEffect(() => {
    let cancelled = false;
    setState((previous) => ({ ...previous, loading: true, error: false }));

    const { startMs, endMs } = briefingWindow;
    const startDate = new Date(startMs).toISOString();
    const endDate = new Date(endMs).toISOString();
    const bandStartDate = new Date(endMs - BAND_DAYS * DAY_MS).toISOString();
    const stuckBeforeDate = new Date(endMs - STUCK_AFTER_DAYS * DAY_MS).toISOString();
    const stuckAfterDate = new Date(endMs - STUCK_LOOKBACK_DAYS * DAY_MS).toISOString();

    const suggestions = apiTriage
      .listThresholdSuggestions({ cloud_account_ids: accountIds, limit: 5, offset: 0 })
      .then((response: any) => response?.suggestions ?? [])
      .catch(() => []);

    const investigations = Promise.all([
      apiAskNudgebee.llmConversationComparsion({
        source: 'Investigation',
        startDate,
        endDate,
        previousStartDate: startDate,
        previousEndDate: startDate,
        extractEventIdsFromTitle: true,
      }),
      apiAskNudgebee.getConversationTimeAggregates({ startDate, endDate, sources: ['Investigation'], eventScoped: true }),
    ])
      .then(([comparison, aggregates]: any[]) => ({
        total: comparison?.data?.data?.current?.aggregate?.count ?? 0,
        completed: aggregates?.completed_count ?? 0,
      }))
      .catch(() => null);

    Promise.all([
      apiKubernetes1.briefingAggregates({ startDate, endDate, bandStartDate, stuckBeforeDate, stuckAfterDate, accountId: accountIds }),
      suggestions,
      investigations,
    ])
      .then(([aggregatesResponse, thresholdSuggestions, investigationCounts]: any[]) => {
        if (cancelled) return;
        const data = aggregatesResponse?.data?.data ?? {};

        const payload: BriefingPayload = {
          windowTotals: firstRow(data.window_totals),
          windowIssues: firstRow(data.window_issues)?.event_count ?? 0,
          byRank: rowsOf(data.by_rank),
          byRank30d: rowsOf(data.by_rank_30d),
          disagreement: rowsOf(data.disagreement),
          bySignalClass: rowsOf(data.by_signal_class),
          firingNow: firstRow(data.firing_now)?.event_count ?? 0,
          stuckFiring: rowsOf(data.stuck_firing),
          thresholdSuggestions,
          investigations: investigationCounts,
          windowStartMs: startMs,
          windowEndMs: endMs,
          nowMs: Date.now(),
        };

        setState({ loading: false, error: false, model: resolveBriefing(payload) });
      })
      .catch((error) => {
        if (cancelled) return;
        console.error('Failed to load Nubi briefing:', error);
        setState({ loading: false, error: true, model: null });
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [briefingWindow.startMs, briefingWindow.endMs, accountIds]);

  return { ...state, window: briefingWindow };
};
