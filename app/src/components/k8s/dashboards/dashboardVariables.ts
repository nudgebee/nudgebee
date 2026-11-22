import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import observability from '@api1/observability';
import { isCommandDatasource, type AccountOption, type DashboardVariable, type Panel } from '@api1/dashboards';
import { findTable, tablesFor } from './entityQuery';
import { metricsProviderFields } from './panelProviders';
import { filterColumnsOf, loadVariableValues } from './panelViewerFilters';
import { ALL_VALUES, referencedVariables, regexValue, renderTemplate, type VariableValues } from './templating';

export { ALL_VALUES, regexValue };

const QUERY_KEY = 'variable';
const OPTIONS_WINDOW_MS = 60 * 60 * 1000;

/** The sorted, distinct values of `labelKey` across a metrics answer. */
export function optionsFromResults(results: any[], labelKey: string): string[] {
  const found = new Set<string>();
  for (const result of results || []) {
    for (const series of result?.payload ?? []) {
      const value = series?.metric?.[labelKey];
      if (value) found.add(value);
    }
  }
  return [...found].sort((a, b) => a.localeCompare(b));
}

/** What each variable stands for: the picked values, or every value when none is picked. */
export function variableValues(
  variables: DashboardVariable[],
  selected: Record<string, string[]>,
  options: Record<string, string[]> = {}
): VariableValues {
  const out: VariableValues = {};
  for (const v of variables) {
    // Only what the dropdown can show: a variable made single keeps one pick, not a hidden list.
    const picked = (selected[v.name] || []).slice(0, v.multi ? undefined : 1);
    out[v.name] = {
      values: picked.length > 0 ? picked : options[v.name] || [],
      all: picked.length === 0,
      ...(v.all_value ? { allValue: v.all_value } : {}),
    };
  }
  return out;
}

const FIELDS: (keyof DashboardVariable)[] = ['name', 'label', 'type', 'query', 'label_key', 'values', 'multi', 'all_value', 'table', 'column'];

/** Whether two lists declare the same variables, whatever order each object's keys came back in. */
export function sameVariables(a: DashboardVariable[] = [], b: DashboardVariable[] = []): boolean {
  // `multi: false` and an absent `multi` declare the same thing, so nothing empty counts.
  const key = (list: DashboardVariable[]) => JSON.stringify(list.map((v) => FIELDS.map((f) => (f === 'type' ? v.type || 'query' : v[f] || null))));
  return key(a) === key(b);
}

export const isCustomVariable = (v: DashboardVariable) => v.type === 'custom';
export const isNudgebeeVariable = (v: DashboardVariable) => v.type === 'nudgebee';

/** The tables a variable can read, and the text columns of one: what a list of values can be. */
export const variableTables = () => tablesFor('nudgebee');
export const variableColumns = (table?: string) => {
  const known = variableTables().some((t) => t.value === table);
  return known ? filterColumnsOf(findTable(table as string)) : [];
};

const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

/** What is wrong with a list of variables, the same rules the server applies on save. */
export function variableErrors(variables: DashboardVariable[]): string[] {
  const errors: string[] = [];
  const seen = new Set<string>();
  variables.forEach((v, i) => {
    const who = v.name || `Variable ${i + 1}`;
    if (!NAME_RE.test(v.name)) errors.push(`${who}: the name must be letters, digits and underscores, and not start with a digit.`);
    if (seen.has(v.name)) errors.push(`${who}: this name is used twice.`);
    seen.add(v.name);
    if (v.type && !['query', 'custom', 'nudgebee'].includes(v.type)) {
      errors.push(`${who}: "${v.type}" is not a variable type.`);
    } else if (isCustomVariable(v)) {
      if (!(v.values || []).some((o) => o.trim())) errors.push(`${who}: add at least one value.`);
    } else if (isNudgebeeVariable(v)) {
      if (!variableColumns(v.table).some((c) => c.name === v.column)) errors.push(`${who}: choose a table and a column.`);
    } else if (!v.query?.trim() || !v.label_key?.trim()) {
      errors.push(`${who}: a query and the label to read are both needed.`);
    }
  });
  return errors;
}

const TEXT_FIELDS = ['name', 'label', 'type', 'query', 'label_key', 'table', 'column', 'all_value'];

function shapeProblem(raw: unknown, i: number): string | undefined {
  const who = `Variable ${i + 1}`;
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return `${who} is not a variable object.`;
  const v = raw as Record<string, unknown>;
  const wrong = TEXT_FIELDS.find((f) => v[f] !== undefined && typeof v[f] !== 'string');
  if (wrong) return `${who}: "${wrong}" must be text.`;
  if (v.multi !== undefined && typeof v.multi !== 'boolean') return `${who}: "multi" must be true or false.`;
  if (v.values !== undefined && !(Array.isArray(v.values) && v.values.every((o) => typeof o === 'string'))) {
    return `${who}: "values" must be a list of text.`;
  }
  return undefined;
}

/**
 * Reads variables from JSON an author edited or a file brought in, which can hold anything. The render
 * trusts this shape, so it is checked here rather than left to reach a dropdown and take the page down.
 */
export function readVariables(raw: unknown): { variables: DashboardVariable[]; errors: string[] } {
  if (raw === undefined || raw === null) return { variables: [], errors: [] };
  if (!Array.isArray(raw)) return { variables: [], errors: ['"variables" must be a list.'] };
  const shape = raw.map(shapeProblem).filter((p): p is string => Boolean(p));
  if (shape.length > 0) return { variables: [], errors: shape };
  const variables = raw as DashboardVariable[];
  const errors = variableErrors(variables);
  return errors.length > 0 ? { variables: [], errors } : { variables, errors };
}

export type VariableUseKind = 'works' | 'partial' | 'unused' | 'blocked';

export interface VariableUse {
  panel: string;
  kind: VariableUseKind;
  reason?: string;
}

/**
 * How each panel takes a variable, read from its query. It can say a panel does
 * not use the variable, never that it could: whether a metric carries the label
 * is only known by running it. Text panels have no query to report on.
 */
export function variableUses(panels: Panel[], name: string): VariableUse[] {
  if (!NAME_RE.test(name)) return [];
  const exactMatch = new RegExp(`=\\s*"[^"]*\\$\\{?${name}\\b(?![:\\w])`);
  const plainUse = new RegExp(`\\$\\{?${name}\\b(?![:\\w])`);
  const regexMatcher = new RegExp(`=~\\s*["'\`][^"'\`]*\\$\\{?${name}\\b(?![:\\w])`);
  return panels.flatMap((panel): VariableUse[] => {
    const targets = panel.targets || [];
    if (targets.length === 0) return [];
    const uses = { panel: panel.title };
    if (isCommandDatasource(panel.datasource)) {
      return [{ ...uses, kind: 'blocked', reason: 'Command panels refuse $, so they can’t use variables.' }];
    }
    const referenced = targets.some((t) => referencedVariables(`${t.expr || ''} ${t.query ? JSON.stringify(t.query) : ''}`).includes(name));
    if (!referenced) return [{ ...uses, kind: 'unused' }];
    if (targets.some((t) => exactMatch.test(t.expr || ''))) {
      return [{ ...uses, kind: 'partial', reason: `Uses =, so All and several values won’t match. Use =~"$${name}".` }];
    }
    // A plain $name is a regex, which only a =~ matcher reads. Every other log query takes it as text.
    if (panel.datasource === 'logs' && targets.some((t) => plainUse.test(t.expr || '') && !regexMatcher.test(t.expr || ''))) {
      return [
        {
          ...uses,
          kind: 'partial',
          reason: `Outside =~"$${name}", All and several values won’t match. Use \${${name}:lucene}, or \${${name}:json} in an Elasticsearch query.`,
        },
      ];
    }
    return [{ ...uses, kind: 'works' }];
  });
}

/** One line on how far a dropdown reaches, for a viewer who never opens the Variables form. */
export function variableReach(panels: Panel[], name: string): string {
  const uses = variableUses(panels, name);
  const reached = uses.filter((u) => u.kind === 'works' || u.kind === 'partial').length;
  return `Applies to ${reached} of ${uses.length} ${uses.length === 1 ? 'panel' : 'panels'}`;
}

export interface VariableState {
  options: Record<string, string[]>;
  /** Why a variable's list could not load, so an empty dropdown can say so. */
  errors: Record<string, string>;
  loading: Record<string, boolean>;
  selected: Record<string, string[]>;
  select: (name: string, values: string[]) => void;
  /** Back to All on every dropdown. */
  clear: () => void;
  values: VariableValues;
}

interface VariableScope {
  variables: DashboardVariable[];
  /** Whose values to list. */
  accounts: AccountOption[];
  panels: Panel[];
  startTime: number;
  endTime: number;
  /** What the host page supplies, which a variable's own query can use as a panel's can. */
  context?: VariableValues;
}

/** What makes a pick meaningless when it changes. Relabelling a dropdown is not one of those. */
const pickKey = (v: DashboardVariable) => JSON.stringify([v.type || 'query', v.query, v.label_key, v.values, Boolean(v.multi), v.table, v.column]);

const withoutKeys = <T>(from: Record<string, T>, names: string[]) =>
  names.some((n) => n in from) ? Object.fromEntries(Object.entries(from).filter(([n]) => !names.includes(n))) : from;

/** A metrics panel using this variable that names its provider: the variable has to ask the same backend. */
function providerPanel(panels: Panel[], name: string): Panel | undefined {
  return panels.find(
    (p) => p.datasource === 'metrics' && p.provider && (p.targets || []).some((t) => referencedVariables(t.expr || '').includes(name))
  );
}

/** Why one account's answer holds no list, when it failed rather than matched nothing. */
function answerFailure(answer: PromiseSettledResult<unknown>): string | undefined {
  if (answer.status === 'rejected') return String(answer.reason?.message || 'Could not list the values.');
  const body = (answer.value as any)?.data;
  if (body?.errors?.length) return String(body.errors[0]?.message || 'Could not list the values.');
  const failed = (body?.data?.metrics_list?.results || []).find((r: any) => r?.error);
  return failed ? String(failed.error) : undefined;
}

/**
 * Loads each variable's choices from the accounts on screen and holds what the
 * viewer picked. An account that fails only loses its own choices; a picked
 * value stays selected even if it drops out, so a panel never silently widens.
 * A pick goes when its variable is redefined or removed.
 */
export function useDashboardVariables({ variables, accounts, panels, startTime, endTime, context }: VariableScope): VariableState {
  const [options, setOptions] = useState<Record<string, string[]>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [selected, setSelected] = useState<Record<string, string[]>>({});

  const allOptions = useMemo(() => {
    const out = { ...options };
    for (const v of variables) if (isCustomVariable(v)) out[v.name] = (v.values || []).filter((o) => o.trim());
    return out;
  }, [options, variables]);
  const values = useMemo(() => variableValues(variables, selected, allOptions), [variables, selected, allOptions]);

  const picksFor = useRef<Record<string, string>>({});
  const picksKey = JSON.stringify(variables.map((v) => [v.name, pickKey(v)]));
  useEffect(() => {
    const before = picksFor.current;
    const now = Object.fromEntries(variables.map((v) => [v.name, pickKey(v)]));
    picksFor.current = now;
    setSelected((prev) =>
      withoutKeys(
        prev,
        Object.keys(prev).filter((name) => now[name] === undefined || now[name] !== before[name])
      )
    );
    // `picksKey` is the serialised form of `variables`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [picksKey]);

  const accountsKey = JSON.stringify(accounts.map((a) => [a.value, a.cloud_provider]));
  const forget = useCallback((names: string[]) => {
    setOptions((prev) => withoutKeys(prev, names));
    setErrors((prev) => withoutKeys(prev, names));
    setLoading((prev) => withoutKeys(prev, names));
  }, []);

  // Rendered first, so a variable can depend on another: picking a namespace reloads the pods under it.
  const queried = useMemo(
    () =>
      variables
        .filter((v) => !isCustomVariable(v) && !isNudgebeeVariable(v))
        .map((v) => {
          const { [v.name]: _own, ...others } = { ...values, ...(context || {}) };
          const owner = providerPanel(panels, v.name);
          return {
            name: v.name,
            label_key: v.label_key as string,
            query: renderTemplate(v.query || '', others),
            provider: owner?.provider,
            provider_index: owner?.provider_index,
          };
        }),
    [variables, values, context, panels]
  );
  const queriedKey = JSON.stringify([queried, accountsKey]);

  useEffect(() => {
    const names = queried.map((v) => v.name);
    if (names.length === 0) return undefined;
    if (accounts.length === 0) {
      forget(names);
      return undefined;
    }
    const controller = new AbortController();
    const now = Date.now();
    setLoading((prev) => ({ ...prev, ...Object.fromEntries(names.map((n) => [n, true])) }));
    queried.forEach(async (variable) => {
      const answers = await Promise.allSettled(
        accounts.map((account) => {
          const { cloudwatch, fields } = metricsProviderFields(variable, account);
          return observability.metricsQuery(
            {
              account_id: account.value,
              queries: { [QUERY_KEY]: variable.query },
              start_time: now - OPTIONS_WINDOW_MS,
              end_time: now,
              // CloudWatch has no instant form.
              instant: !cloudwatch,
              ...fields,
            },
            controller.signal
          );
        })
      );
      if (controller.signal.aborted) return;
      const merged = new Set<string>();
      for (const answer of answers) {
        if (answer.status !== 'fulfilled') continue;
        const results = (answer.value as any)?.data?.data?.metrics_list?.results;
        optionsFromResults(results, variable.label_key).forEach((o) => merged.add(o));
      }
      // Read as an empty answer, a broken query or an unreachable provider would look like "no values".
      const failure = merged.size === 0 ? answers.map(answerFailure).find(Boolean) : undefined;
      setOptions((prev) => ({ ...prev, [variable.name]: [...merged].sort((a, b) => a.localeCompare(b)) }));
      setErrors((prev) => ({ ...prev, [variable.name]: failure || '' }));
      setLoading((prev) => ({ ...prev, [variable.name]: false }));
    });
    return () => controller.abort();
    // `queriedKey` is the serialised form of the inputs, so a new identity does not refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queriedKey]);

  // Apart from the query variables: only these follow the time range, so moving it must not refetch those.
  const entityVariables = useMemo(() => variables.filter(isNudgebeeVariable), [variables]);
  const entityKey = JSON.stringify([entityVariables, accountsKey, startTime, endTime]);

  useEffect(() => {
    const names = entityVariables.map((v) => v.name);
    if (names.length === 0) return undefined;
    if (accounts.length === 0) {
      forget(names);
      return undefined;
    }
    const controller = new AbortController();
    setLoading((prev) => ({ ...prev, ...Object.fromEntries(names.map((n) => [n, true])) }));
    entityVariables.forEach(async (variable) => {
      try {
        const found = await loadVariableValues({
          table: variable.table as string,
          column: variable.column as string,
          accountIds: accounts.map((a) => a.value),
          startTime,
          endTime,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        setOptions((prev) => ({ ...prev, [variable.name]: found }));
        setErrors((prev) => ({ ...prev, [variable.name]: '' }));
      } catch (err) {
        if (controller.signal.aborted) return;
        setErrors((prev) => ({ ...prev, [variable.name]: (err as Error)?.message || 'Could not list the values.' }));
      }
      setLoading((prev) => ({ ...prev, [variable.name]: false }));
    });
    return () => controller.abort();
    // `entityKey` is the serialised form of the inputs, so a new identity does not refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entityKey]);

  const select = useCallback((name: string, picked: string[]) => setSelected((prev) => ({ ...prev, [name]: picked })), []);
  const clear = useCallback(() => setSelected({}), []);
  return { options: allOptions, errors, loading, selected, select, clear, values };
}
