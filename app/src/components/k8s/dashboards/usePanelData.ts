import { useEffect, useRef, useState } from 'react';
import observability from '@api1/observability';
import apiDashboards, { isCommandDatasource, type AccountOption, type Panel, type PanelQueryResult } from '@api1/dashboards';
import { draftFromQuery, findTable, type EntityColumnFormat, type EntityQueryDraft } from './entityQuery';
import { runTracePanel } from './traceQuery';
import { panelQueryAccounts, resolvePanelAccounts } from './panelAccounts';
import { alignSeries, toRawSeries, type RawSeries } from './panelSeries';
import { acquirePanelSlot } from './panelQueue';
import { convertNumberToTimestamp } from 'src/utils/common';
import { renderTemplate, type VariableValues } from './templating';

export interface PanelSeries {
  label: string;
  /** `null` where the series reported nothing — a gap, not a zero. */
  values: (number | null)[];
}

/** Whether an account's metrics come from CloudWatch. */
function isAwsAccount(account: AccountOption): boolean {
  return (account.cloud_provider || '').toLowerCase() === 'aws';
}

/**
 * A table cell's kind, so the renderer knows what to hand the cell to — the
 * same Datetime / Currency / Memory / Number components the product's listings
 * use. Everything not named here renders as plain text.
 */
export type ColumnKind = 'time' | 'text' | EntityColumnFormat;

export interface PanelTable extends PanelQueryResult {
  /** Per column; absent means every column is plain text. */
  column_kinds?: ColumnKind[];
  /**
   * The QUERY's column names, where `columns` holds display labels — the two
   * differ for an entity panel, whose headers are relabelled below. A panel's
   * link and hidden columns name the query's columns, so they resolve against
   * this; absent means `columns` already holds the query's own names.
   */
  column_names?: string[];
}

/** Why a panel has nothing to show. */
export type PanelErrorKind = 'config' | 'blocked' | 'filter' | 'failed';

export interface PanelError {
  kind: PanelErrorKind;
  message: string;
}

/** The common case: something downstream broke. */
function failure(message: string): PanelError {
  return { kind: 'failed', message };
}

export interface PanelData {
  labels: string[];
  /** The same axis as `labels`, in epoch milliseconds. */
  timestamps?: number[];
  series: PanelSeries[];
  /**
   * Command, entity and log datasources return a snapshot table instead of
   * series. Present only for those panels.
   */
  table?: PanelTable;
}

interface Options {
  panel: Panel;
  /** Every account the viewer can see; the panel's scope resolves against it. */
  accounts: AccountOption[];
  /** Viewer's narrowing selection. Empty = no filter applied. */
  accountFilter?: string[];
  variables: VariableValues;
  startTime: number;
  endTime: number;
  /** Changes to force a refetch with everything else identical. */
  refreshKey?: string | number;
  /** `false` holds the query back entirely — the panel renders its skeleton and fetches nothing. */
  enabled?: boolean;
  /**
   * Skips the load queue. For the panel a person is looking AT rather than one
   * that happens to be on screen — the editor's preview, and the PNG capture
   * that needs every panel drawn — neither of which should wait behind a
   * dashboard's own panels.
   */
  immediate?: boolean;
}

/** Fetches one panel's data, across every account the panel is scoped to. */
export function usePanelData({
  panel,
  accounts,
  accountFilter,
  variables,
  startTime,
  endTime,
  refreshKey,
  enabled = true,
  immediate = false,
}: Options) {
  const [data, setData] = useState<PanelData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<PanelError | null>(null);
  const [warning, setWarning] = useState<string | null>(null);

  const scoped = resolvePanelAccounts(panel, accounts);
  // `nudgebee` panels reach the query engine, which takes every account in one
  // call — so they query all of them rather than the first. See panelQueryAccounts.
  const { accounts: resolved } = panelQueryAccounts(scoped, accountFilter, panel.datasource === 'nudgebee');
  // Distinguishes "the filter hid everything" from "this panel has no accounts",
  // which need different messages.
  const filteredOut = scoped.length > 0 && resolved.length === 0;
  // Serialised for the same reason as targetsKey: a fresh array identity per
  // render must not retrigger the fetch.
  const accountsKey = resolved.map((a) => a.value).join(',');

  // Serialised so a new object identity per render doesn't refetch forever.
  const targetsKey = JSON.stringify((panel.targets || []).map((t) => [t.ref_id, renderTemplate(t.expr || '', variables), t.legend_format, t.hide]));

  /**
   * Being on screen says the panel SHOULD load; the queue says when. Without it
   * one screenful of short stat panels is a dozen simultaneous provider queries
   * — see panelQueue.ts. Admission is per panel and permanent: a panel that has
   * loaded once refetches freely on a time-range change or Refresh, which is a
   * deliberate action rather than the open-the-dashboard stampede this bounds.
   */
  const [admitted, setAdmitted] = useState(false);
  const releaseSlot = useRef<(() => void) | null>(null);
  const wantsSlot = enabled && !immediate && panel.type !== 'text' && !admitted;

  useEffect(() => {
    if (!wantsSlot) return;
    let cancelled = false;
    acquirePanelSlot().then((release) => {
      // Scrolled away or unmounted while queued: give the slot straight back
      // rather than holding it for a panel nobody is looking at.
      if (cancelled) {
        release();
        return;
      }
      releaseSlot.current = release;
      setAdmitted(true);
    });
    return () => {
      cancelled = true;
    };
  }, [wantsSlot]);

  // The slot is held only for the FIRST load. `loading` false with something to
  // show means that load has settled — as does an early return that set an error
  // without ever fetching, which is why this keys on the outcome and not on the
  // request. The queue's own timeout covers a panel that somehow reports neither.
  useEffect(() => {
    if (!admitted) return;
    /*
     * Scrolled back out of view. A fetch abandoned this way never clears
     * `loading` — the request's `finally` skips the cancelled run — so the
     * settle path below could never fire, and the slot sat held until the
     * queue's 15s timeout took it back, starving whatever was queued behind a
     * panel nobody is looking at any more.
     *
     * Admission is given back only when a load was actually in flight. A
     * settled panel keeps it, so scrolling past one still refetches freely on
     * the next Refresh — the stampede this queue bounds is the open, not that.
     */
    if (!enabled) {
      releaseSlot.current?.();
      releaseSlot.current = null;
      if (loading) setAdmitted(false);
      return;
    }
    if (loading) return;
    if (!data && !error) return;
    releaseSlot.current?.();
    releaseSlot.current = null;
  }, [admitted, enabled, loading, data, error]);

  // Releasing on unmount as well: a panel torn down mid-flight must not take its
  // slot with it. The release is idempotent, so the settle path may have run too.
  useEffect(
    () => () => {
      releaseSlot.current?.();
      releaseSlot.current = null;
    },
    []
  );

  useEffect(() => {
    if (panel.type === 'text') return;
    // Off-screen, or still queued behind other panels: no request, and no error
    // either — an untried panel must not claim it has no accounts.
    if (!enabled || !(admitted || immediate)) return;
    const targets = (panel.targets || []).filter((t) => !t.hide);
    setWarning(null);
    // Nothing to resolve a provider from. Say so rather than rendering an empty
    // chart that reads as "these accounts have no data".
    if (resolved.length === 0) {
      if (filteredOut) {
        setError({ kind: 'filter', message: 'No accounts match the current filter.' });
      } else if (panel.account_type) {
        setError({ kind: 'blocked', message: `No ${panel.account_type} accounts are available to you.` });
      } else {
        // The "Edit it and pick one" tail is now the button underneath.
        setError({ kind: 'config', message: 'This panel has no account selected' });
      }
      return;
    }
    if (targets.length === 0) {
      setData({ labels: [], series: [] });
      return;
    }
    // Traces are read through the TRACES SERVICE, not the query engine.
    if (panel.datasource === 'traces') {
      const stored = targets[0]?.query;
      if (!stored) {
        setError({ kind: 'config', message: 'This panel has no query' });
        return;
      }
      let cancelledTraces = false;
      setLoading(true);
      setError(null);

      // One account: the traces API takes a single accountId, which is why a
      // traces panel resolves to exactly one (auto-selected, or picked).
      runTracePanel(draftFromQuery(stored), resolved[0].value, startTime, endTime)
        .then((result) => {
          if (cancelledTraces) return;
          if (result.unsupported.length > 0) {
            setWarning(`Ignored filters this trace store cannot apply: ${result.unsupported.join(', ')}.`);
          }
          setData({ labels: [], series: [], table: result });
        })
        .catch((err) => {
          if (cancelledTraces) return;
          setError(failure(err?.message || 'Could not load traces for this panel.'));
          setData(null);
        })
        .finally(() => {
          if (!cancelledTraces) setLoading(false);
        });

      return () => {
        cancelledTraces = true;
      };
    }

    // `nudgebee` panels read the internal query engine.
    if (panel.datasource === 'nudgebee') {
      const query = targets[0]?.query;
      if (!query) {
        setError({ kind: 'config', message: 'This panel has no query' });
        return;
      }
      let cancelledEntity = false;
      setLoading(true);
      setError(null);

      apiDashboards
        .executeEntityQuery({
          account_ids: resolved.map((a) => a.value),
          datasource: panel.datasource,
          query,
          time_column: targets[0]?.time_column,
          start_time: startTime,
          end_time: endTime,
        })
        .then((res) => {
          if (cancelledEntity) return;
          if (res.errors || !res.data) {
            setError(failure(gatewayMessage(res.errors) || 'Could not run this panel’s query.'));
            setData(null);
            return;
          }
          // Headers as the builder's labels, and datetime columns marked so the
          // renderer can use the same Datetime component the listings do.
          setData({ labels: [], series: [], table: labelEntityColumns(res.data, draftFromQuery(query)) });
        })
        .finally(() => {
          if (!cancelledEntity) setLoading(false);
        });

      return () => {
        cancelledEntity = true;
      };
    }

    // Logs come back as lines, not series.
    if (panel.datasource === 'logs') {
      let cancelledLogs = false;
      setLoading(true);
      setError(null);
      const query = renderTemplate(targets[0]?.expr || '', variables);
      const prefixWithAccountLabel = resolved.length > 1;

      Promise.allSettled(
        resolved.map((account) =>
          observability.fetchLogs({
            account_id: account.value,
            query,
            start_time: startTime,
            end_time: endTime,
            limit: LOG_LINE_LIMIT,
            offset: 0,
          })
        )
      )
        .then((settled) => {
          if (cancelledLogs) return;
          const failures: string[] = [];
          const rows: string[][] = [];

          settled.forEach((outcome, i) => {
            const account = resolved[i];
            const gqlErrors = outcome.status === 'fulfilled' ? (outcome.value as any)?.data?.errors : null;
            if (outcome.status === 'rejected' || gqlErrors?.length) {
              failures.push(account.label);
              return;
            }
            for (const line of (outcome.value as any)?.data?.data?.logs_list?.logs || []) {
              const cells = [formatLogTimestamp(line?.timestamp), String(line?.severity ?? ''), String(line?.message ?? '')];
              rows.push(prefixWithAccountLabel ? [account.label, ...cells] : cells);
            }
          });

          if (failures.length === resolved.length) {
            setError(failure(`Could not load logs for ${failures.join(', ')}.`));
            setData(null);
            return;
          }
          if (failures.length > 0) {
            setWarning(`No logs from ${failures.join(', ')} — showing the rest.`);
          }
          const columns = prefixWithAccountLabel ? ['Account', 'Time', 'Severity', 'Message'] : ['Time', 'Severity', 'Message'];
          const kinds: ColumnKind[] = columns.map((c) => (c === 'Time' ? 'time' : 'text'));
          setData({ labels: [], series: [], table: { columns, rows, column_kinds: kinds } });
        })
        .finally(() => {
          if (!cancelledLogs) setLoading(false);
        });

      return () => {
        cancelledLogs = true;
      };
    }

    // redis / rabbitmq run a command through the relay instead of querying a provider.
    if (isCommandDatasource(panel.datasource)) {
      let cancelledCommand = false;
      setLoading(true);
      setError(null);
      const command = renderTemplate(targets[0]?.expr || '', variables);

      Promise.allSettled(
        resolved.map((account) => apiDashboards.executePanelQuery({ account_id: account.value, datasource: panel.datasource, command }))
      )
        .then((settled) => {
          if (cancelledCommand) return;
          const failures: string[] = [];
          const answers: { account: string; result: PanelQueryResult }[] = [];

          settled.forEach((outcome, i) => {
            const account = resolved[i];
            const value = outcome.status === 'fulfilled' ? outcome.value : null;
            if (!value?.data || value.errors) {
              failures.push(account.label);
              return;
            }
            answers.push({ account: account.label, result: value.data });
          });

          if (answers.length === 0) {
            // The server's message is the useful one here — a rejected command,
            // a missing integration, an unreachable cluster all read differently.
            const first = settled.find((o) => o.status === 'fulfilled') as PromiseFulfilledResult<any> | undefined;
            setError(failure(gatewayMessage(first?.value?.errors) || `Could not run the command on ${failures.join(', ')}.`));
            setData(null);
            return;
          }
          if (failures.length > 0) {
            setWarning(`No response from ${failures.join(', ')} — showing the rest.`);
          }
          setData({ labels: [], series: [], table: mergeQueryResults(answers) });
        })
        .finally(() => {
          if (!cancelledCommand) setLoading(false);
        });

      return () => {
        cancelledCommand = true;
      };
    }

    let cancelled = false;
    setLoading(true);
    setError(null);

    const queries: Record<string, string> = {};
    const legendByKey: Record<string, string> = {};
    for (const t of targets) {
      const rendered = renderTemplate(t.expr || '', variables);
      if (!rendered.trim()) continue;
      queries[t.ref_id] = rendered;
      if (t.legend_format) legendByKey[t.ref_id] = t.legend_format;
    }

    if (Object.keys(queries).length === 0) {
      setData({ labels: [], series: [] });
      setLoading(false);
      return;
    }

    // Only prefix when there is more than one account — on a single-account
    // panel the prefix is noise on every series.
    const prefixWithAccount = resolved.length > 1;

    Promise.allSettled(
      resolved.map((account) => {
        const cloudwatch = isAwsAccount(account);
        return observability.metricsQuery({
          account_id: account.value,
          queries,
          start_time: startTime,
          end_time: endTime,
          // CloudWatch has no instant form — it always answers with a range.
          instant: cloudwatch ? false : panel.type === 'stat',
          // Named explicitly for AWS, because CloudWatch is never an account's DEFAULT metrics provider: it
          // is not an integration that can carry that flag, so without this the query goes to whatever the
          // account does default to (Datadog, typically) and comes back unparseable.
          ...(cloudwatch ? { metric_provider: 'aws_cloudwatch', metric_provider_source: 'user' } : {}),
        });
      })
    )
      .then((settled) => {
        if (cancelled) return;
        const raw: RawSeries[] = [];
        const failed: string[] = [];

        settled.forEach((outcome, i) => {
          const account = resolved[i];
          // A rejected promise is a transport failure; a resolved one can still carry GraphQL errors in the
          // body, which are just as fatal for this account.
          const gqlErrors = outcome.status === 'fulfilled' ? (outcome.value as any)?.data?.errors : null;
          if (outcome.status === 'rejected' || gqlErrors?.length) {
            failed.push(account.label);
            return;
          }
          const results = (outcome.value as any)?.data?.data?.metrics_list?.results || [];
          for (const s of toRawSeries(results, legendByKey)) {
            raw.push(prefixWithAccount ? { ...s, label: `${account.label} · ${s.label}` } : s);
          }
        });

        if (failed.length === resolved.length) {
          setError(failure(`Could not load data for ${failed.join(', ')}.`));
          setData(null);
          return;
        }
        if (failed.length > 0) {
          setWarning(`No data from ${failed.join(', ')} — showing the rest.`);
        }
        // Aligned across ALL accounts at once, so a series that only one account
        // reports still sits at the right point on the shared axis.
        setData(alignSeries(raw));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [
    accountsKey,
    filteredOut,
    panel.account_type,
    panel.type,
    panel.datasource,
    targetsKey,
    startTime,
    endTime,
    refreshKey,
    enabled,
    admitted,
    immediate,
  ]);

  return { data, loading, error, warning };
}

/** Relabels an entity result with the builder's column labels, and marks which columns are timestamps. */
export function labelEntityColumns(result: PanelQueryResult, draft: EntityQueryDraft): PanelTable {
  const table = findTable(draft.table);
  return {
    ...result,
    columns: result.columns.map((name) => table.columns.find((c) => c.name === name)?.label || name),
    column_kinds: result.columns.map((name) => {
      const column = table.columns.find((c) => c.name === name);
      // A datetime is a time whatever else it says; otherwise the registry's
      // own format decides, and a column without one is plain text.
      if (column?.type === 'datetime') return 'time';
      return column?.format || 'text';
    }),
    // Relabelling is what makes this necessary: the author configured link and
    // hidden columns against `id` and `account_id`, not "Event id".
    column_names: result.columns,
  };
}

/**
 * How many log lines one panel pulls back. A log query is unbounded by nature,
 * and a panel is a few hundred pixels tall.
 */
const LOG_LINE_LIMIT = 200;

/** Log timestamps arrive as epoch ms, epoch ns, or an ISO string. */
function formatLogTimestamp(value: unknown): string {
  if (value === null || value === undefined || value === '') return '';
  const n = Number(value);
  if (!Number.isFinite(n)) return String(value);
  // Nanoseconds (OTel) are ~19 digits; milliseconds ~13.
  const ms = n > 1e15 ? n / 1e6 : n;
  return convertNumberToTimestamp(ms);
}

/** First message out of a gateway `errors` array, if it carries one. */
function gatewayMessage(errors: unknown): string {
  const first = Array.isArray(errors) ? (errors[0] as { message?: string }) : null;
  return first?.message || '';
}

/** Stacks each account's table into one. */
function mergeQueryResults(answers: { account: string; result: PanelQueryResult }[]): PanelQueryResult {
  const first = answers[0].result;
  if (answers.length === 1) return first;

  const columns = ['Account', ...first.columns];
  const rows: string[][] = [];
  let truncated = false;
  for (const { account, result } of answers) {
    truncated = truncated || Boolean(result.truncated);
    for (const row of result.rows || []) {
      const padded = first.columns.map((_c, i) => row[i] ?? '');
      rows.push([account, ...padded]);
    }
  }
  return { columns, rows, truncated };
}
