import { useEffect, useState } from 'react';
import observability from '@api1/observability';
import apiDashboards, { isCommandDatasource, type AccountOption, type Panel, type PanelQueryResult } from '@api1/dashboards';
import { draftFromQuery, findTable, type EntityQueryDraft } from './entityQuery';
import { runTracePanel } from './traceQuery';
import { panelQueryAccounts, resolvePanelAccounts } from './panelAccounts';
import { alignSeries, toRawSeries, type RawSeries } from './panelSeries';
import { convertNumberToTimestamp } from 'src/utils/common';
import { renderTemplate, type VariableValues } from './templating';

export interface PanelSeries {
  label: string;
  /** `null` where the series reported nothing — a gap, not a zero. */
  values: (number | null)[];
}

/**
 * Whether an account's metrics come from CloudWatch.
 *
 * `cloud_provider` is stored as the label the accounts list reports ("AWS"), so
 * the comparison is case-folded rather than pinned to one casing.
 */
function isAwsAccount(account: AccountOption): boolean {
  return (account.cloud_provider || '').toLowerCase() === 'aws';
}

/** A table cell's kind, so the renderer knows what to hand the cell to. */
export type ColumnKind = 'time' | 'text';

export interface PanelTable extends PanelQueryResult {
  /** Per column; absent means every column is plain text. */
  column_kinds?: ColumnKind[];
}

export interface PanelData {
  labels: string[];
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
  /**
   * Changes to force a refetch with everything else identical. Carries both the
   * dashboard-wide refresh and this panel's own, so either one re-runs the
   * query without the other having to know about it.
   */
  refreshKey?: string | number;
  /**
   * `false` holds the query back entirely — the panel renders its skeleton and
   * fetches nothing. The renderer flips it on once the panel scrolls into view,
   * so a long dashboard doesn't fire every panel's provider request at open.
   * Defaults to true so a caller that doesn't defer keeps loading eagerly.
   */
  enabled?: boolean;
}

/**
 * Fetches one panel's data, across every account the panel is scoped to.
 *
 * Accounts come off the PANEL, not the dashboard — `metrics_list` resolves the
 * provider integration from an account and rejects an empty one, and two panels
 * on the same dashboard may legitimately query different accounts.
 *
 * All of a panel's targets go out as a SINGLE `metrics_list` call PER ACCOUNT —
 * the action takes a `queries` map but only one account_id, so N targets across
 * M accounts cost M requests rather than N×M. Batching across panels is the next
 * step; without it a 30-panel dashboard is 30 concurrent provider requests per
 * viewer, and Datadog bills per call.
 *
 * Accounts are fetched with allSettled, not all: one account with a broken
 * integration must not blank out the panel for the accounts that do work. The
 * failures come back as `warning` while the rest still render.
 *
 * The in-flight requests are abandoned on unmount / dependency change via the
 * `cancelled` flag, so a slow response can never overwrite fresher state or set
 * state on an unmounted panel.
 */
export function usePanelData({ panel, accounts, accountFilter, variables, startTime, endTime, refreshKey, enabled = true }: Options) {
  const [data, setData] = useState<PanelData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
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

  useEffect(() => {
    if (panel.type === 'text') return;
    // Off-screen: no request, and no error either — an untried panel must not
    // claim it has no accounts. `data` stays null, which the renderer already
    // shows as a skeleton.
    if (!enabled) return;
    const targets = (panel.targets || []).filter((t) => !t.hide);
    setWarning(null);
    // Nothing to resolve a provider from. Say so rather than rendering an empty
    // chart that reads as "these accounts have no data".
    if (resolved.length === 0) {
      if (filteredOut) {
        setError('No accounts match the current filter.');
      } else {
        setError(
          panel.account_type ? `No ${panel.account_type} accounts are available to you.` : 'This panel has no account selected. Edit it and pick one.'
        );
      }
      return;
    }
    if (targets.length === 0) {
      setData({ labels: [], series: [] });
      return;
    }
    // Traces are read through the TRACES SERVICE, not the query engine. The
    // engine can reach traces_v2, but only the traces service knows how to
    // resolve a given account's span store — its Elasticsearch index, its
    // ClickHouse source — so going around it worked by luck, per account.
    if (panel.datasource === 'traces') {
      const stored = targets[0]?.query;
      if (!stored) {
        setError('This panel has no query. Edit it and build one.');
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
          setError(err?.message || 'Could not load traces for this panel.');
          setData(null);
        })
        .finally(() => {
          if (!cancelledTraces) setLoading(false);
        });

      return () => {
        cancelledTraces = true;
      };
    }

    // `nudgebee` panels read the internal query engine. Unlike every other
    // datasource this is ONE call for all accounts — the engine takes an
    // account_id list, so fanning out would be N queries against the same table.
    if (panel.datasource === 'nudgebee') {
      const query = targets[0]?.query;
      if (!query) {
        setError('This panel has no query. Edit it and build one.');
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
            setError(gatewayMessage(res.errors) || 'Could not run this panel’s query.');
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

    // Logs come back as lines, not series. `logs_list` resolves the provider
    // (Loki / Elasticsearch / …) from the account and takes one account at a
    // time, so this fans out the same way the command datasources do.
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
            setError(`Could not load logs for ${failures.join(', ')}.`);
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

    // redis / rabbitmq run a command through the relay instead of querying a
    // provider. One call per account, same allSettled contract, and the time
    // range is ignored — the command returns the state as of now.
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
            setError(gatewayMessage(first?.value?.errors) || `Could not run the command on ${failures.join(', ')}.`);
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
          // Named explicitly for AWS, because CloudWatch is never an account's
          // DEFAULT metrics provider: it is not an integration that can carry
          // that flag, so without this the query goes to whatever the account
          // does default to (Datadog, typically) and comes back unparseable.
          // BOTH fields are required — the provider alone resolves the source to
          // "agent", and only aws_cloudwatch+user is a registered metrics source.
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
          // A rejected promise is a transport failure; a resolved one can still
          // carry GraphQL errors in the body, which are just as fatal for this
          // account. Both count as failed.
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
          setError(`Could not load data for ${failed.join(', ')}.`);
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
  }, [accountsKey, filteredOut, panel.account_type, panel.type, panel.datasource, targetsKey, startTime, endTime, refreshKey, enabled]);

  return { data, loading, error, warning };
}

/**
 * Relabels an entity result with the builder's column labels, and marks which
 * columns are timestamps. The server answers with raw column NAMES — it has no
 * copy of the label metadata, and duplicating it there would be two lists to
 * keep in step.
 */
function labelEntityColumns(result: PanelQueryResult, draft: EntityQueryDraft): PanelTable {
  const table = findTable(draft.table);
  return {
    ...result,
    columns: result.columns.map((name) => table.columns.find((c) => c.name === name)?.label || name),
    column_kinds: result.columns.map((name) => (table.columns.find((c) => c.name === name)?.type === 'datetime' ? 'time' : 'text')),
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

/**
 * Stacks each account's table into one.
 *
 * The columns come from the first account that answered; a second account
 * returning a different shape (different Redis version, different rabbitmqadmin
 * flags) is padded rather than dropped, so its rows are still visible. An
 * Account column is added only when more than one account answered — on a
 * single-account panel it is the same value on every row.
 */
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
