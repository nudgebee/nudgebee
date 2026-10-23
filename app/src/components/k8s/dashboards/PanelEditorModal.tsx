import React, { useEffect, useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import SafeIcon from '@shared/icons/SafeIcon';
import { DeleteIconRed } from '@assets';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import Tooltip from '@ui/Tooltip';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import {
  isCommandDatasource,
  isKubernetesOnlyDatasource,
  KUBERNETES_ACCOUNT_KIND,
  type AccountOption,
  type Panel,
  type PanelCombine,
  type PanelColumn,
  type PanelDataLink,
  type PanelDatasource,
  type PanelTarget,
  type PanelThresholdColor,
  type PanelThresholdOp,
  type PanelThresholdStep,
  type PanelType,
} from '@api1/dashboards';
import EntityQueryBuilder from './EntityQueryBuilder';
import PanelPreview, { PREVIEW_RAIL_WIDTH, usePreviewRange } from './PanelPreview';
import { buildEntityQuery, defaultDraft, draftFromQuery, findTable, tablesFor, type EntityQueryDraft } from './entityQuery';
import { grantTooltip, missingDatasourceGrant, queryableTables } from './panelAccess';
import { isCompleteColumn, isSafeLinkUrl, panelColumnsOf, referencedColumns, setHiddenColumns } from './panelColumns';
import { filterColumnsOf, savedFilterColumns } from './panelViewerFilters';
import { accountPickerOptions, accountsOfTypes, deriveAccountTypes, panelScopeFromTypes, resolvePanelAccounts } from './panelAccounts';
import { ES_PROVIDER, providerChoices, providerLabel, providerTypeOf, useEsIndexes, usePanelProviders } from './panelProviders';
import FilterDropdown from '@ui/FilterDropdown';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import PanelProviderRow from './PanelProviderRow';
import {
  appliesToOf,
  FOR_MAX_SECONDS,
  FOR_MIN_SECONDS,
  hasThresholds,
  isCompleteStep,
  isLinePanel,
  isValidFor,
  OP_PHRASE,
  OP_SYMBOL,
  opOf,
  panelThresholdStepsOf,
  showsOneNumber,
  THRESHOLD_COLORS,
  THRESHOLD_OPS,
  thresholdOverlaps,
  thresholdTone,
} from './panelThresholds';
import { combineOf } from './panelSeries';
import { referencedVariables, type VariableValues } from './templating';
import { fillBrandTokens, useBrandingConfig } from '@hooks/useTenantBranding';

interface Props {
  open: boolean;
  panel: Panel | null;
  isEdit: boolean;
  /** Every account the author may point this panel at, across providers. */
  accountOptions: AccountOption[];
  /** The host page's variables, so the preview substitutes `$namespace` as the dashboard will. */
  variables?: VariableValues;
  /** The range the preview queries; falls back to the last hour. */
  startTime?: number;
  endTime?: number;
  onClose: () => void;
  /**
   * May be async — the dashboard view writes the panel straight to the server, and
   * the modal shows its loader until the returned promise settles. What it resolves
   * WITH is ignored.
   */
  onSave: (panel: Panel) => unknown | Promise<unknown>;
}

/** How a stat or gauge folds its accounts into one number. */
const COMBINE_OPTIONS: { label: string; value: PanelCombine }[] = [
  { label: 'Sum', value: 'sum' },
  { label: 'Average', value: 'avg' },
];

const PANEL_TYPES: { label: string; value: PanelType }[] = [
  { label: 'Time series', value: 'timeseries' },
  { label: 'Stat', value: 'stat' },
  { label: 'Gauge', value: 'gauge' },
  { label: 'Table', value: 'table' },
  { label: 'Bar', value: 'bar' },
  { label: 'Text', value: 'text' },
];

const DATASOURCES: { label: string; value: PanelDatasource }[] = [
  { label: 'Metrics', value: 'metrics' },
  { label: 'Logs', value: 'logs' },
  { label: 'Traces', value: 'traces' },
  { label: 'Redis', value: 'redis' },
  { label: 'RabbitMQ', value: 'rabbitmq' },
  { label: 'PostgreSQL', value: 'postgresql' },
  { label: 'kubectl', value: 'kubectl' },
  // `{brand}` is filled at render (see fillBrandTokens) — DATASOURCES is a
  // module-level constant, so a literal here would latch the house brand.
  { label: '{brand} (events)', value: 'nudgebee' },
];

/** What each command datasource accepts. */
const COMMAND_HELP: Record<string, { placeholder: string; allowed: string; example: string }> = {
  redis: {
    placeholder: 'INFO memory',
    allowed:
      'INFO, DBSIZE, PING, LASTSAVE, TIME, ROLE, CONFIG GET, CLIENT LIST/INFO, MEMORY STATS/DOCTOR/USAGE, CLUSTER INFO/NODES, LATENCY LATEST/HISTORY, SLOWLOG GET/LEN, COMMAND COUNT/DOCS',
    example: 'INFO replication',
  },
  rabbitmq: {
    placeholder: 'list queues name messages consumers',
    allowed: 'list, show',
    example: 'list queues name messages',
  },
  postgresql: {
    placeholder: 'SELECT state, count(*) FROM pg_stat_activity GROUP BY state',
    allowed:
      'a single read-only statement starting with SELECT, WITH, SHOW, EXPLAIN, TABLE or VALUES. No writes anywhere in it (including data-modifying CTEs), no ";", no double quotes — single quotes for values are fine',
    example: "SELECT state, count(*) FROM pg_stat_activity WHERE state = 'active' GROUP BY state",
  },
  kubectl: {
    placeholder: 'get pods -n kube-system',
    allowed:
      'get, describe, logs, top, explain, api-resources, api-versions, version, cluster-info, with read flags only (-n, -A, -o, -l, --field-selector, --sort-by, -c, --tail, --since …). No secrets, no exec or port-forward, no file or template paths, and no quotes, pipes or redirects — `kubectl` itself is added by the server',
    example: 'get pods -n kube-system -o wide',
  },
};

/** Name half of a colour's label; the closed picker hides it so the column stays a swatch wide. */
const COLOR_NAME_CLASS = 'threshold-color-name';

/** What a threshold step paints the panel in: a swatch, named in the open list so colour is never the only cue. */
const THRESHOLD_COLOR_OPTIONS = THRESHOLD_COLORS.map((color) => {
  const name = `${color[0].toUpperCase()}${color.slice(1)}`;
  return {
    label: (
      <Box component='span' title={name} sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
        <Box
          component='span'
          aria-hidden
          sx={{ display: 'inline-block', width: 10, height: 10, flexShrink: 0, borderRadius: '50%', background: thresholdTone(color).border }}
        />
        <Box component='span' className={COLOR_NAME_CLASS}>
          {name}
        </Box>
      </Box>
    ),
    value: color,
  };
});

/** Spelled-out half of a comparison's label; the closed picker hides it so the column stays narrow. */
const OP_PHRASE_CLASS = 'threshold-op-phrase';

/**
 * Selects the closed picker — Select's trigger button — and not its option list,
 * which renders inline in the same wrapper (Select does not portal by default).
 */
const CLOSED_PICKER = '& [aria-haspopup="listbox"]';

/** How a threshold step compares — the symbol the badge shows, spelled out in the open list. */
const THRESHOLD_OP_OPTIONS = THRESHOLD_OPS.map((op) => ({
  label: (
    <>
      {OP_SYMBOL[op]}
      <Box component='span' className={OP_PHRASE_CLASS} sx={{ ml: ds.space[1] }}>
        {OP_PHRASE[op]}
      </Box>
    </>
  ),
  value: op,
}));

/**
 * The "Applies to" picker's value for one account. The picker is one flat list —
 * the shown number, every account, then each account — so an account's option
 * carries its id behind a prefix the two fixed choices cannot collide with.
 */
const ACCOUNT_SUBJECT = 'account:';

/**
 * The link picker's stand-in for "attach to nothing, add a column of my own".
 * A Select renders its PLACEHOLDER for an empty value, so the option cannot be
 * `''` — "New column" is a real choice, not the absence of one. Editor-local:
 * it is mapped back to an absent `column` before the panel is stored.
 */
const NEW_COLUMN = '__new_column__';

/** A threshold's number as typed. Empty reads as NaN — a step the author has not finished. */
const parseThreshold = (text: string): number => (text.trim() === '' ? Number.NaN : Number(text));

/** NaN never equals itself, and "still empty" has to count as unchanged. */
const sameThreshold = (a: number, b: number): boolean => (Number.isNaN(a) && Number.isNaN(b)) || a === b;

/**
 * The threshold's value field.
 *
 * It holds the TEXT the author typed and reports the number that text parses to,
 * rather than re-deriving the text from the number on every keystroke. That
 * round trip — `String(Number(text))` — is what made `1.05` land as `15` and
 * `-0.5` as `0.5`: it rewrites the field mid-entry, and a `type='number'` input
 * discards whatever partial value it was still holding when it is rewritten.
 * Verified in Chromium, not jsdom, which does not sanitise a number input.
 *
 * Re-seeds only when the step's value stops agreeing with the text, so removing
 * the row above this one refreshes it while typing never does.
 */
const ThresholdValue: React.FC<{ value: number; index: number; showError: boolean; onChange: (next: number) => void }> = ({
  value,
  index,
  showError,
  onChange,
}) => {
  const [text, setText] = useState(() => (Number.isFinite(value) ? String(value) : ''));

  useEffect(() => {
    setText((current) => (sameThreshold(parseThreshold(current), value) ? current : Number.isFinite(value) ? String(value) : ''));
  }, [value]);

  return (
    <Input
      type='number'
      value={text}
      onChange={(next: string) => {
        setText(next);
        onChange(parseThreshold(next));
      }}
      placeholder='e.g. 80'
      error={showError && !Number.isFinite(value) ? 'Enter a number' : undefined}
      data-testid={`threshold-value-${index}`}
    />
  );
};

/** One step read back as a sentence, so the author sees what the row they just built will do. */
const ThresholdSentence: React.FC<{ step: PanelThresholdStep; subject: string; unit: string; isLine: boolean; index: number }> = ({
  step,
  subject,
  unit,
  isLine,
  index,
}) => {
  if (!Number.isFinite(step.value)) return null;
  // A hold the field is flagging would read as "as soon as" here, which is not what the author asked for.
  if (isLine && step.for !== undefined && !isValidFor(step.for)) return null;
  const hold = isLine && step.for !== undefined && isValidFor(step.for) ? step.for / 60 : undefined;
  const figure = `${step.value.toLocaleString('en-US')}${unit ? ` ${unit}` : ''}`;
  const bold = { fontWeight: ds.weight.semibold };
  return (
    <Typography
      variant='body2'
      sx={{ mt: ds.space[2], fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[600] }}
      data-testid={`threshold-sentence-${index}`}
    >
      Turns the panel{' '}
      <Box component='span' sx={{ fontWeight: ds.weight.semibold, color: thresholdTone(step.color).text }}>
        {step.color}
      </Box>{' '}
      {hold !== undefined ? (
        <>
          when {subject} stays <Box component='span' sx={bold}>{`${OP_PHRASE[opOf(step)]} ${figure}`}</Box> for{' '}
          <Box component='span' sx={bold}>{`${hold} ${hold === 1 ? 'minute' : 'minutes'}`}</Box> in a row.
        </>
      ) : (
        <>
          {isLine ? 'as soon as' : 'when'} {subject} is <Box component='span' sx={bold}>{`${OP_PHRASE[opOf(step)]} ${figure}`}</Box>.
        </>
      )}
    </Typography>
  );
};

/** A hold as typed, in minutes. Empty is "no hold" — the step fires on the latest sample. */
const parseHold = (text: string): number | undefined => {
  if (text.trim() === '') return undefined;
  const minutes = Number(text);
  // Stored in seconds; a non-number is kept as NaN so the step reads as unfinished rather than silently instant.
  return Number.isFinite(minutes) ? Math.round(minutes * 60) : Number.NaN;
};

/**
 * The step's hold field, in minutes. Same text-first design as ThresholdValue:
 * the field holds what the author typed and reports the seconds it parses to.
 */
const ThresholdHold: React.FC<{ seconds: number | undefined; index: number; onChange: (next: number | undefined) => void }> = ({
  seconds,
  index,
  onChange,
}) => {
  const asText = (value: number | undefined) => (value !== undefined && Number.isFinite(value) ? String(value / 60) : '');
  const [text, setText] = useState(() => asText(seconds));

  useEffect(() => {
    setText((current) => {
      const parsed = parseHold(current);
      const same =
        (parsed === undefined && seconds === undefined) || (Number.isNaN(parsed as number) && Number.isNaN(seconds as number)) || parsed === seconds;
      return same ? current : asText(seconds);
    });
  }, [seconds]);

  return (
    <Input
      type='number'
      value={text}
      onChange={(next: string) => {
        setText(next);
        onChange(parseHold(next));
      }}
      placeholder='—'
      error={
        seconds === undefined || isValidFor(seconds)
          ? undefined
          : Number.isFinite(seconds) && seconds % 60 !== 0
          ? 'Whole minutes'
          : `${FOR_MIN_SECONDS / 60}–${FOR_MAX_SECONDS / 60} min`
      }
      id={`threshold-for-${index}`}
      data-testid={`threshold-for-${index}`}
    />
  );
};

/**
 * A ref id no other query on the panel holds. The metrics request is keyed by
 * ref id, so two queries sharing one would come back as a single series.
 */
const nextRefId = (targets: PanelTarget[]): string => {
  const used = new Set(targets.map((t) => t.ref_id));
  for (let code = 65; code <= 90; code++) {
    const id = String.fromCharCode(code);
    if (!used.has(id)) return id;
  }
  let n = targets.length + 1;
  while (used.has(`Q${n}`)) n++;
  return `Q${n}`;
};

/** A card's heading: what the group is, and why its fields are together. */
const GroupHeader: React.FC<{ title: string; description: string }> = ({ title, description }) => (
  <Box>
    <Typography
      sx={{
        fontFamily: 'var(--ds-font-display)',
        fontSize: 12,
        fontWeight: 700,
        letterSpacing: '0.03em',
        textTransform: 'uppercase',
        color: ds.gray[700],
      }}
    >
      {title}
    </Typography>
    <Typography variant='caption' sx={{ display: 'block', mt: ds.space[0], color: ds.gray[500], fontWeight: ds.weight.regular }}>
      {description}
    </Typography>
  </Box>
);

// The Accounts picker's section headers carry the provider's own mark, same as
// the automations listing's account groups.
const renderProviderGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const PanelEditorModal: React.FC<Props> = ({ open, panel, isEdit, accountOptions, variables, startTime, endTime, onClose, onSave }) => {
  const [draft, setDraft] = useState<Panel | null>(panel);
  /** Account types are editor-local, not panel state. */
  const [accountTypes, setAccountTypes] = useState<string[]>([]);
  const [accountIds, setAccountIds] = useState<string[]>([]);
  /** The entity builder's own state. */
  const [entityDraft, setEntityDraft] = useState<EntityQueryDraft>(defaultDraft());
  /**
   * A save can be a round trip (the dashboard view persists the panel as soon
   * as it is saved), so the modal holds the in-flight state itself rather than
   * leaving the author looking at an unchanged form wondering if it took.
   */
  const [saving, setSaving] = useState(false);
  /** A step still being filled in is not an error until the author tries to save it. */
  const [thresholdsAttempted, setThresholdsAttempted] = useState(false);
  const fallbackRange = usePreviewRange(open);

  // Re-seed when a different panel is opened.
  React.useEffect(() => {
    setDraft(panel);
    setAccountTypes(panel ? deriveAccountTypes(panel, accountOptions) : []);
    // Exactly what is stored, even when the ids happen to be every account of the providers above. Those
    // two are the same bytes on disk but not the same panel: an id list is PINNED and will not pick up an
    // account connected tomorrow, so showing it as an empty "all of these providers" field claimed a
    // behaviour the panel does not have — and re-saving it silently widened the scope to whatever had been
    // connected since.
    setAccountIds(panel?.account_ids || []);
    // With the stored time column: without it the builder showed the table's default switch, and the next
    // edit anywhere in the query wrote that default over the panel's own setting.
    setEntityDraft(draftFromQuery(panel?.targets?.[0]?.query, panel?.targets?.[0]?.time_column ?? ''));
    setThresholdsAttempted(false);
  }, [panel, accountOptions]);

  /**
   * Each datasource reaches a different backend read behind a different grant,
   * so one the viewer's role cannot reach is offered disabled rather than
   * hidden: a hidden option reads as "this product cannot do that", a disabled
   * one names the grant to ask for. Nothing is gated for a tenant admin or any
   * account user — see panelAccess.ts.
   */
  // Subscribed for the re-render, not the value: fillBrandTokens reads the
  // branding cache imperatively, and this memo is otherwise dep-free — it would
  // bake in whatever the brand was on first paint and never recompute, which is
  // the latch this placeholder work exists to remove. `title` in the deps is
  // what makes the option list repaint once /api/public/app_config lands.
  const { title: brandTitle } = useBrandingConfig();
  const datasourceOptions = useMemo(
    () =>
      DATASOURCES.map((template) => {
        const option = { ...template, label: fillBrandTokens(template.label) };
        const missing = missingDatasourceGrant(option.value);
        if (!missing) return option;
        return {
          value: option.value,
          disabled: true,
          label: (
            <Tooltip title={grantTooltip(missing)}>
              {/* A disabled MUI menu item sets `pointer-events: none`, which
                  would swallow the hover the tooltip needs — this span opts
                  back in. */}
              <Box component='span' sx={{ pointerEvents: 'auto', display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
                <LockOutlinedIcon sx={{ fontSize: 13, flexShrink: 0 }} />
                {option.label}
              </Box>
            </Tooltip>
          ),
        };
      }),
    [brandTitle]
  );

  /**
   * The accounts this panel's datasource can actually be served by.
   *
   * kubectl runs against an account's own agent, so a cloud account has nothing
   * to answer with — offering one would save a scope that can only fail at
   * render. Scoped by `kind` (what the account MANAGES) rather than
   * `cloud_provider`: a `vm` fleet reaches an agent too, and that agent has no
   * kubectl.
   */
  const draftDatasource = draft?.datasource;
  const scopedAccountOptions = useMemo(
    () =>
      draftDatasource && isKubernetesOnlyDatasource(draftDatasource)
        ? accountOptions.filter((o) => o.kind === KUBERNETES_ACCOUNT_KIND)
        : accountOptions,
    [accountOptions, draftDatasource]
  );

  const accountTypeOptions = useMemo(() => {
    const seen = new Set<string>();
    for (const o of scopedAccountOptions) {
      if (o.cloud_provider) seen.add(o.cloud_provider);
    }
    return [...seen].sort().map((t) => ({ label: t, value: t }));
  }, [scopedAccountOptions]);

  // The account picker lists only the chosen providers' accounts — an unfiltered list mixes clusters with
  // cloud accounts and is unreadable past a handful — sectioned by provider when it spans more than one.
  const accountsForTypes = useMemo(() => accountPickerOptions(accountTypes, scopedAccountOptions), [scopedAccountOptions, accountTypes]);

  // The accounts this panel will actually query, resolved exactly as the panel
  // itself resolves them at render — the provider row must not be able to
  // disagree with the requests usePanelData goes on to make.
  const providerAccounts = useMemo(
    () => resolvePanelAccounts(panelScopeFromTypes(accountTypes, accountIds, scopedAccountOptions), scopedAccountOptions),
    [accountTypes, accountIds, scopedAccountOptions]
  );
  // A text panel queries nothing, and the Source card it would sit in is hidden.
  const providerType = draft && draft.type !== 'text' ? providerTypeOf(draft.datasource) : undefined;
  const { loading: providersLoading, entries: providerEntries, total: providerTotal } = usePanelProviders(providerAccounts, providerType);
  /**
   * Every provider the panel could be written for.
   *
   * Naming none is a real state, not an unmade choice — it is how every panel
   * written before this control behaves, and it stays correct for the common
   * single-provider tenant. So it lives in the placeholder and the clear button,
   * exactly as the Accounts field already spells "all accounts of this type".
   */
  // Only ES needs an index named beside the query; every other provider carries
  // it in the expression or does not have the concept.
  const needsIndex = draft?.provider === ES_PROVIDER;
  // Listed from the panel's first account. The picker is free-solo, so an author
  // whose accounts hold different index sets can still type the pattern they want
  // — which is also the answer for a wildcard spanning several.
  const { loading: indexesLoading, indexes } = useEsIndexes(providerAccounts[0]?.value || '', needsIndex);

  const providerOptions = useMemo(
    () => providerChoices(providerEntries, draft?.provider || '').map((p) => ({ label: providerLabel(p), value: p })),
    [providerEntries, draft?.provider]
  );

  const targets = draft?.targets || [];
  const expr = targets[0]?.expr || '';
  // Every query, not just the first: a variable in query B is filled in (or
  // refused) exactly like one in query A.
  const allExprs = targets.map((t) => t.expr || '').join('\n');
  const templateVars = useMemo(() => referencedVariables(allExprs), [allExprs]);

  if (!draft) return null;

  const patch = (next: Partial<Panel>) => setDraft((prev) => (prev ? { ...prev, ...next } : prev));

  // Rewrites one query in place, preserving the fields it does not touch
  // (legend_format, hide, …) and every other query on the panel.
  const patchTarget = (next: Partial<PanelTarget>, index = 0) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const nextTargets = [...(prev.targets || [])];
      nextTargets[index] = { ...nextTargets[index], ...next, ref_id: nextTargets[index]?.ref_id || 'A' };
      return { ...prev, targets: nextTargets };
    });

  const addTarget = () =>
    setDraft((prev) => (prev ? { ...prev, targets: [...(prev.targets || []), { ref_id: nextRefId(prev.targets || []), expr: '' }] } : prev));

  const removeTarget = (index: number) =>
    setDraft((prev) => (prev ? { ...prev, targets: (prev.targets || []).filter((_, i) => i !== index) } : prev));

  /**
   * Column settings live in one list on `options`, and an empty one is dropped
   * rather than saved as `[]` — a panel that configures no column should read as
   * a panel that has never heard of the feature.
   */
  const patchColumns = (next: PanelColumn[]) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const options: Record<string, unknown> = { ...(prev.options || {}), columns: next };
      if (next.length === 0) delete options.columns;
      // The two arrays this list replaced. Dropped on the next write, the same
      // way the server's upgradeDefinition drops them on read.
      delete options.link_columns;
      delete options.hidden_columns;
      return { ...prev, options };
    });

  /**
   * The columns a viewer's "Filter by column" menu offers, under the same rule:
   * none chosen is saved as no list, which offers every column.
   */
  const patchFilterColumns = (next: string[]) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const options: Record<string, unknown> = { ...(prev.options || {}), filter_columns: next };
      if (next.length === 0) delete options.filter_columns;
      return { ...prev, options };
    });

  /** Threshold steps, under the same "an empty list is no list" rule as the columns above. */
  const patchThresholds = (next: PanelThresholdStep[]) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const options: Record<string, unknown> = { ...(prev.options || {}), thresholds: next };
      if (next.length === 0) delete options.thresholds;
      return { ...prev, options };
    });

  /** A chart's drill-down links, under the same "an empty list is no list" rule. */
  const patchDataLinks = (next: PanelDataLink[]) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const options: Record<string, unknown> = { ...(prev.options || {}), data_links: next };
      if (next.length === 0) delete options.data_links;
      return { ...prev, options };
    });

  /**
   * Thresholds are kept only across visualisations that evaluate them the same
   * way: stat and gauge judge the one shown number, a chart judges every line.
   * How a panel combines its accounts into one number belongs to stat and gauge
   * alone. Anything else is dropped on a switch: carrying it would leave the
   * panel storing config nothing renders — or, stat to chart, silently turn one
   * rule about a total into a rule across twenty pods, or sum where it would
   * average, long after whoever set it had forgotten.
   */
  const changeType = (next: PanelType) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const keepThresholds = hasThresholds(next) && isLinePanel(next) === isLinePanel(prev.type);
      const keepCombine = showsOneNumber(next);
      // Drill-down links are a timeseries-only facet; any other type strands them.
      const keepDataLinks = next === 'timeseries';
      if (
        (keepThresholds || !prev.options?.thresholds) &&
        (keepCombine || prev.options?.combine === undefined) &&
        (keepDataLinks || prev.options?.data_links === undefined)
      ) {
        return { ...prev, type: next };
      }
      const options = { ...prev.options };
      if (!keepThresholds) delete options.thresholds;
      if (!keepCombine) delete options.combine;
      if (!keepDataLinks) delete options.data_links;
      return { ...prev, type: next, options };
    });

  const changeAccountTypes = (next: string[]) => {
    setAccountTypes(next);
    // Selections from a provider that is no longer chosen would be invisible in
    // the filtered list yet still scope the panel, so drop them.
    const kept = new Set(accountsOfTypes(next, scopedAccountOptions).map((o) => o.value));
    setAccountIds((prev) => prev.filter((id) => kept.has(id)));
    // The provider was chosen from what the OLD accounts had configured, and a
    // different account type resolves an entirely different set — an AWS panel
    // holding a Kubernetes cluster's `prometheus` would name a provider none of
    // its accounts can serve. Back to the account default, which is always valid.
    patch({ provider: undefined, provider_index: undefined });
  };

  /**
   * Naming a provider does not touch the query: an author switching from
   * "Account default" to the provider those accounts already resolve to is
   * pinning what they had, not starting over. The row below names any account the
   * choice leaves behind.
   */
  const changeProvider = (next: string) =>
    // The index belongs to the provider that was named. Carrying it onto another
    // one would pin a Prometheus panel to an Elasticsearch index, which the
    // backend has nowhere to put — and ValidateDefinition rejects an index with
    // no provider at all.
    patch({ provider: next || undefined, provider_index: next === ES_PROVIDER ? draft?.provider_index : undefined });

  /** Changing the data source clears the query — all of them, back to a single empty one. */
  const changeDatasource = (next: string) => {
    const datasource = next as PanelDatasource;
    const datasourceTables = tablesFor(datasource);
    const entity = datasourceTables.length > 0;
    // Logs and entity queries are rows, like the command datasources.
    const tabular = isCommandDatasource(datasource) || entity || datasource === 'logs';
    // Reset the builder alongside the target. Start on the first table the
    // viewer may actually read — the datasource's own first table would open the
    // editor on a greyed-out selection for a custom-role holder without that
    // grant. Falls back to it when none are readable, which is the honest state.
    const entityStart = defaultDraft(entity ? (queryableTables(datasourceTables)[0] || datasourceTables[0]).value : datasource);
    if (entity) setEntityDraft(entityStart);
    // A kubectl panel can only be scoped to clusters, so a scope the new
    // datasource cannot serve is dropped here rather than saved and failed at
    // render. Where the panel was already on clusters the scope is kept; where
    // it was on AWS it falls back to the one provider clusters have.
    if (isKubernetesOnlyDatasource(datasource)) {
      const clusters = accountOptions.filter((o) => o.kind === KUBERNETES_ACCOUNT_KIND);
      const providers = [...new Set(clusters.map((o) => o.cloud_provider).filter(Boolean))];
      const kept = accountTypes.filter((t) => providers.includes(t));
      setAccountTypes((kept.length > 0 ? kept : providers).slice(0, 1));
      const keptIds = new Set(clusters.map((o) => o.value));
      setAccountIds((prev) => prev.filter((id) => keptIds.has(id)));
      patch({ provider: undefined, provider_index: undefined });
    } else if (datasource !== 'nudgebee') {
      // Only `nudgebee` reads across providers, so leaving a second one selected
      // on the way out would save a scope the single control cannot show — the
      // field would read "AWS" while the panel quietly queried AWS and GCP.
      changeAccountTypes(accountTypes.slice(0, 1));
    }
    setDraft((prev) =>
      prev
        ? {
            ...prev,
            datasource,
            type: tabular ? 'table' : prev.type,
            // Link and hidden columns name the previous table's columns, and the
            // query they came from has just been thrown away with them.
            options: undefined,
            // The provider named the query language of the expression that has
            // just been cleared, and the new datasource resolves a different set
            // of providers entirely. Keeping it would pin the panel to a provider
            // that cannot serve it — which ValidateDefinition rejects outright on
            // the entity and command datasources.
            provider: undefined,
            provider_index: undefined,
            // A nudgebee panel is authored as a structured query, so it starts
            // from a working default rather than an empty one. The time column
            // only when the range is on: a table of standing state keeps one
            // chosen with the switch off.
            targets: entity
              ? [{ ref_id: 'A', query: buildEntityQuery(entityStart) as any, time_column: entityStart.applyTimeRange ? entityStart.timeColumn : '' }]
              : [{ ...(prev.targets?.[0] || {}), ref_id: prev.targets?.[0]?.ref_id || 'A', expr: '', query: undefined, time_column: undefined }],
          }
        : prev
    );
  };

  const commandHelp = COMMAND_HELP[draft.datasource];
  // Traces read the same query engine as events, just a different pair of
  // tables — one builder, two vocabularies.
  const entityTables = tablesFor(draft.datasource);
  const isEntity = entityTables.length > 0;
  const isLogs = draft.datasource === 'logs';
  const isText = draft.type === 'text';
  /**
   * Column settings are authored for `nudgebee` panels only.
   *
   * They were offered on every panel whose rows are real records — a kubectl
   * snapshot, a log table, a trace list. But the settings name the columns the
   * QUERY returns, and only the entity builder knows those ahead of time: a
   * command or log panel's columns arrive with the first response, so both
   * controls degrade to typing a column name from memory and finding out at
   * render whether it existed. Better to offer nothing there than a form that
   * cannot check its own answers.
   *
   * Traces are an entity query too, and could carry links, but their rows open
   * a trace rather than a record elsewhere in the product — the case the link
   * column exists for. They join this when there is a destination worth linking.
   *
   * Settings a panel already stores are untouched and still rendered; only the
   * editor's card is scoped. Switching data source clears `options` anyway.
   */
  const isNudgebee = draft.datasource === 'nudgebee';
  // The editor reads the list RAW: an author typing a path has an incomplete
  // entry, and the render-time filter would delete the row out from under them.
  const columns = panelColumnsOf(draft);
  const filterColumnOptions = isEntity ? filterColumnsOf(findTable(entityDraft.table)).map((c) => ({ label: c.label, value: c.name })) : [];
  // Only names the table offers: an imported panel can carry ones it does not,
  // which the menu ignores anyway — shown here they would read as choices.
  const filterColumns = savedFilterColumns(draft).filter((name) => filterColumnOptions.some((o) => o.value === name));
  const hiddenColumns = columns.filter((c) => c.visibility === 'hidden' && c.name).map((c) => c.name as string);
  // Links are the only column setting the editor authors today. A column's other
  // settings — a title override, a format override — are honoured by the
  // renderer and set by import or by hand until this grows fields for them.
  const links = columns.filter((c) => Boolean(c.link));
  /**
   * Read RAW, exactly as the links are: a step being typed is momentarily
   * incomplete, and the render-time filter in panelThresholdsOf would delete the
   * row out from under the author mid-keystroke.
   */
  const thresholds = panelThresholdStepsOf(draft);
  /** Whether this panel's thresholds are about lines on a chart rather than the one shown number. */
  const lineThresholds = isLinePanel(draft.type);
  /** A step whose hold is outside what the renderer honours. Blocks Save with its own message. */
  const badHolds = lineThresholds ? thresholds.filter((step) => step.for !== undefined && !isValidFor(step.for)) : [];
  /**
   * Holds on a stat or gauge, which judges one sample and so cannot honour
   * them: such a step is left undrawn (panelThresholdsOf). They only arrive by
   * JSON edit or import — this editor has no hold field there — so they are
   * flagged with a way to remove them rather than blocking Save.
   */
  const ignoredHolds = lineThresholds ? [] : thresholds.filter((step) => step.for !== undefined);
  /** Drops the hold from every step `which` names, leaving the step itself. */
  const clearHolds = (which: PanelThresholdStep[]) =>
    patchThresholds(
      thresholds.map((step) => {
        if (!which.includes(step)) return step;
        const { for: _hold, ...rest } = step;
        return rest;
      })
    );
  /** A step that could not be drawn for any other reason. Blocks Save, and says so rather than greying the button out. */
  // The hold is judged above, on its own terms; a step is unfinished only for the rest.
  const unfinishedThresholds = thresholds.filter((step) => !isCompleteStep({ ...step, for: undefined }));
  /**
   * A step naming an account the panel no longer covers — the account was
   * removed from the scope, deleted, or the panel was imported from another
   * tenant. It never fires, which is safe, so it does not block Save; it is
   * flagged so the author can re-point it rather than keep a rule that is dead.
   */
  const coveredAccountIds = new Set(providerAccounts.map((a) => a.value));
  const danglingThresholds = thresholds.filter((step) => appliesToOf(step) === 'account' && !coveredAccountIds.has(step.account_id || ''));
  /**
   * On a single-account panel every subject is the same number, so the picker
   * would be a choice with no consequence — unless a step already names one,
   * which has to stay fixable.
   */
  const showAppliesTo = providerAccounts.length > 1 || thresholds.some((step) => appliesToOf(step) === 'account');
  /** One column template for the header and every row, so each field sits under its name. */
  const thresholdGridSx = {
    display: 'grid',
    gridTemplateColumns: [showAppliesTo && '110px', '64px', 'minmax(0, 1fr)', lineThresholds && '86px', '60px', '32px'].filter(Boolean).join(' '),
    gap: ds.space[2],
  };
  /** Steps that apply to the same value with an outcome worth spelling out — a hint, never a Save blocker. Lines are judged one by one, so there is nothing to overlap. */
  const thresholdConflicts = lineThresholds ? [] : thresholdOverlaps(thresholds, providerAccounts);
  // A chart has no single shown number, so "shown" and "every" are the same
  // choice there and only one is offered.
  const appliesToOptions = [
    ...(lineThresholds
      ? [{ label: 'Every line', value: 'every' }]
      : [
          { label: 'Shown number', value: 'shown' },
          { label: 'Every account', value: 'every' },
        ]),
    ...providerAccounts.map((a) => ({ label: a.label, value: `${ACCOUNT_SUBJECT}${a.value}` })),
    // One option per missing account, however many steps still name it.
    ...[...new Set(danglingThresholds.map((step) => step.account_id || ''))].map((id) => ({
      label: 'Account not available',
      value: `${ACCOUNT_SUBJECT}${id}`,
    })),
  ];
  /**
   * What a link can be attached to: an existing column, whose own values become
   * the links, or a new column of its own.
   *
   * Hidden columns are left out — a link on a column nobody can see is a link
   * nobody can click — and so is a column another link already claims, which the
   * server rejects as configured twice. Refusing to offer it beats saving it and
   * reading the refusal back as a failed round trip.
   */
  const linkTargetOptionsFor = (current: PanelColumn) => [
    { label: 'New column', value: NEW_COLUMN },
    ...entityDraft.columns
      .filter((name) => !hiddenColumns.includes(name))
      .filter((name) => name === current.name || !links.some((l) => l !== current && l.name === name))
      .map((name) => ({ label: findTable(entityDraft.table).columns.find((c) => c.name === name)?.label || name, value: name })),
  ];
  /**
   * Placeholders that name a column the query does not select. The link would
   * render as an empty cell, which looks like missing data rather than a typo.
   */
  const unknownPlaceholders = isEntity
    ? [...new Set(links.flatMap((l) => referencedColumns(l.link?.url || '')))].filter((c) => !entityDraft.columns.includes(c))
    : [];
  // Includes links that are merely unfinished — they block Save, so they have to
  // say so rather than leaving the button greyed out for no visible reason.
  const unfinishedLinks = links.filter((l) => !isCompleteColumn(l));
  /** A chart's drill-down links — a title and a safe in-app path each. Blocks Save while unfinished, like the table links. */
  const dataLinks = draft.options?.data_links ?? [];
  const isCompleteDataLink = (l: PanelDataLink) => (l.title || '').trim().length > 0 && isSafeLinkUrl(l.url || '');
  const unfinishedDataLinks = dataLinks.filter((l) => !isCompleteDataLink(l));
  /** Only a `nudgebee` panel may name several providers at once. */
  const multiType = isNudgebee;
  const hasScope = accountTypes.length > 0 || accountIds.length > 0;
  const canSave =
    draft.title.trim().length > 0 &&
    // The server refuses a panel if any one of its queries is empty, not only the first.
    (isText ||
      (hasScope && (isEntity ? Boolean(targets[0]?.query) : targets.length > 0 && targets.every((t) => (t.expr || '').trim().length > 0)))) &&
    // The server refuses these too; catching them here saves a round trip that
    // comes back as a message about a panel the author can no longer see.
    columns.every(isCompleteColumn) &&
    dataLinks.every(isCompleteDataLink);
  // Not a server rule — `options` is opaque to it — but a step with no number is a
  // row the author is still filling in, and it would save as inert config. Checked on
  // Save rather than greying the button, so the field says what is missing.
  const thresholdsIncomplete = unfinishedThresholds.length > 0 || badHolds.length > 0;

  /** The draft as it would be stored — the preview runs this and the save sends it. */
  const resolvedDraft: Panel = {
    ...draft,
    ...panelScopeFromTypes(accountTypes, accountIds, accountOptions),
    // Trimmed here, not as it is typed, so a unit like "req / s" can still be typed.
    ...(typeof draft.unit === 'string' ? { unit: draft.unit.trim() } : {}),
  };

  const handleSave = async () => {
    if (thresholdsIncomplete) {
      setThresholdsAttempted(true);
      // Save sits in the footer and the thresholds may be scrolled out of view; without this the click looks like it did nothing.
      document.getElementById('panel-thresholds')?.scrollIntoView?.({ behavior: 'smooth', block: 'center' });
      return;
    }
    setSaving(true);
    try {
      await onSave(resolvedDraft);
    } finally {
      // Also on failure: the parent keeps the modal open so the author can fix
      // whatever the server rejected, and a stuck loader would block that.
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      // Dismissing mid-save would land the result on a closed modal, and the
      // write is already on its way — there is nothing to cancel.
      handleClose={saving ? undefined : onClose}
      backdropClickClose={!saving}
      loader={saving}
      title={isEdit ? 'Edit panel' : 'Add panel'}
      width='lg'
      // Pinned height: the form column scrolls while the preview holds still.
      maxHeight='85vh'
      // Padding moves onto the columns so the divider runs full height; `display:
      // flex` gives them a real height to scroll within.
      contentStyles={{ padding: 0, overflow: 'hidden', display: 'flex' }}
      actionButtons={
        <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={onClose} disabled={saving} id='panel-cancel-btn'>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={!canSave || saving} loading={saving} id='panel-save-btn' data-testid='panel-save-btn'>
            {isEdit ? 'Update panel' : 'Save panel'}
          </Button>
        </Stack>
      }
    >
      {/* Two columns where there is room: the form scrolls, the preview stays put.
          Below `md` it stacks and the whole body scrolls together. */}
      <Box
        sx={{
          flex: 1,
          minWidth: 0,
          display: 'flex',
          flexDirection: { xs: 'column', md: 'row' },
          alignItems: 'stretch',
          overflowY: { xs: 'auto', md: 'hidden' },
        }}
      >
        <Box
          sx={{
            flex: 1,
            minWidth: 0,
            overflowY: { xs: 'visible', md: 'auto' },
            padding: 'var(--ds-space-5) var(--ds-space-6)',
          }}
        >
          <Form variant='stacked' density='default'>
            <Stack gap={2}>
              {!isText && (
                <Card
                  variant='tinted'
                  header={
                    <GroupHeader
                      title='Source'
                      description={
                        multiType
                          ? 'Where the data comes from. A findings panel is one query over every provider you pick, so several is a wider answer rather than a broken one.'
                          : 'Where the data comes from, and how much of it to chart.'
                      }
                    />
                  }
                >
                  <Form.Section>
                    <Form.Field label='Data source'>
                      <Select value={draft.datasource} options={datasourceOptions} onChange={changeDatasource} id='panel-datasource-select' />
                    </Form.Field>
                    <Form.Row ratio={[1, 1]}>
                      <Form.Field label={multiType ? 'Account types' : 'Account type'} required>
                        {multiType ? (
                          <Select
                            multiple
                            value={accountTypes}
                            options={accountTypeOptions}
                            onChange={changeAccountTypes}
                            placeholder='Select…'
                            id='panel-account-type-select'
                          />
                        ) : (
                          <Select
                            value={accountTypes[0] || ''}
                            options={accountTypeOptions}
                            onChange={(next: string) => changeAccountTypes(next ? [next] : [])}
                            placeholder='Select…'
                            id='panel-account-type-select'
                          />
                        )}
                      </Form.Field>
                      <Form.Field
                        label={
                          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                            Accounts
                            <Tooltip
                              title={multiType ? 'Leave empty for all accounts of the chosen providers' : 'Leave empty for all accounts of this type'}
                            >
                              <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}>
                                <InfoOutlinedIcon sx={{ fontSize: 13 }} />
                              </Box>
                            </Tooltip>
                          </Box>
                        }
                      >
                        <Select
                          multiple
                          grouped
                          defaultGroupsOpen
                          groupIcon={renderProviderGroupIcon}
                          value={accountIds}
                          options={accountsForTypes}
                          onChange={setAccountIds}
                          disabled={accountTypes.length === 0}
                          placeholder={accountTypes.length > 0 ? `All ${accountTypes.join(', ')} accounts` : 'Select…'}
                          id='panel-account-select'
                        />
                      </Form.Field>
                    </Form.Row>
                    {providerType && (
                      <Form.Field
                        label={
                          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                            Provider
                            <Tooltip title='The backend this panel’s query is written for. One panel speaks one query language — for a second provider, add a second panel.'>
                              <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}>
                                <InfoOutlinedIcon sx={{ fontSize: 13 }} />
                              </Box>
                            </Tooltip>
                          </Box>
                        }
                      >
                        <Stack gap={1}>
                          <Select
                            value={draft.provider || ''}
                            options={providerOptions}
                            onChange={changeProvider}
                            // Nothing to choose from until the panel has accounts: the options ARE
                            // what those accounts have configured. Disabled rather than hidden so
                            // the field keeps its place and the form does not jump as it fills in.
                            disabled={providerAccounts.length === 0}
                            placeholder={providerAccounts.length > 0 ? 'Account default' : 'Select accounts first'}
                            id='panel-provider-select'
                          />
                          <PanelProviderRow
                            providerType={providerType}
                            loading={providersLoading}
                            entries={providerEntries}
                            total={providerTotal}
                            declared={draft.provider || ''}
                          />
                        </Stack>
                      </Form.Field>
                    )}
                    {providerType && needsIndex && (
                      <Form.Field
                        label='Index'
                        helperText='Leave empty to use each account’s configured index. Wildcards are accepted — type a pattern if it is not listed.'
                      >
                        <FilterDropdown
                          id='panel-es-index-select'
                          label='Select an Index'
                          value={draft.provider_index || null}
                          options={indexes}
                          freeSolo
                          // Same affordance as the log query builder's picker: free-solo alone is
                          // invisible, so the hint is what tells the author a pattern is accepted.
                          searchPlaceholder='Search or type pattern (use * for wildcard)...'
                          isOptionsLoading={indexesLoading}
                          onSelect={(_event: any, value: any) => patch({ provider_index: value || undefined })}
                        />
                      </Form.Field>
                    )}
                  </Form.Section>
                </Card>
              )}

              <Card
                variant='tinted'
                header={
                  <GroupHeader
                    title={isText ? 'Content & display' : 'Query & display'}
                    description={isText ? 'What the panel says, and how it is rendered.' : 'What to ask, and how to render the answer.'}
                  />
                }
              >
                <Form.Section>
                  {!isText && (
                    <>
                      {isEntity ? (
                        <>
                          <EntityQueryBuilder
                            draft={entityDraft}
                            tables={entityTables}
                            onChange={(next, query, timeColumn) => {
                              setEntityDraft(next);
                              patchTarget({ query: query as any, time_column: timeColumn, expr: undefined });
                              // Settings for a column the query no longer selects would come back
                              // to life the moment the author selects it again — drop them, but
                              // keep the added columns, which name no query column at all.
                              const kept = columns.filter((c) => !c.name || next.columns.includes(c.name));
                              if (kept.length !== columns.length) patchColumns(kept);
                              // A filter column belongs to the table, not to what it selects: only a
                              // switch to another table can leave one behind.
                              const offered = new Set(filterColumnsOf(findTable(next.table)).map((c) => c.name));
                              const keptFilters = filterColumns.filter((name) => offered.has(name));
                              if (keptFilters.length !== filterColumns.length) patchFilterColumns(keptFilters);
                            }}
                          />
                          <Form.Field
                            label='Columns viewers can filter by'
                            description='What the panel’s Filter by column menu lists. Leave empty to offer every column.'
                          >
                            <Select
                              multiple
                              value={filterColumns}
                              options={filterColumnOptions}
                              onChange={(next: string[]) => patchFilterColumns(next)}
                              placeholder='Every column'
                              id='panel-filter-columns'
                            />
                          </Form.Field>
                        </>
                      ) : commandHelp ? (
                        <>
                          <Form.Field
                            label={draft.datasource === 'postgresql' ? 'Query' : 'Command'}
                            required
                            description={
                              draft.datasource === 'kubectl'
                                ? "Runs in this account's cluster through its agent. Read-only only — `kubectl` itself is added by the server."
                                : `Runs against this account's ${draft.datasource} integration. Read-only only — the credentials and connection flags are added by the server.`
                            }
                          >
                            <Input value={expr} onChange={(v) => patchTarget({ expr: v })} placeholder={commandHelp.placeholder} />
                          </Form.Field>
                          <Box sx={{ p: 1.5, border: `1px solid ${ds.gray[300]}`, background: ds.background[200], borderRadius: ds.radius.md }}>
                            <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                              Allowed: {commandHelp.allowed}. The result is a snapshot — this panel ignores the dashboard&apos;s time range. Example:{' '}
                              <Box component='code' sx={{ fontFamily: ds.font.mono }}>
                                {commandHelp.example}
                              </Box>
                            </Typography>
                          </Box>
                        </>
                      ) : isLogs ? (
                        <Form.Field
                          label='Query'
                          required
                          description="In this account's log provider syntax — LogQL for Loki, Lucene for Elasticsearch, and so on."
                        >
                          <Input value={expr} onChange={(v) => patchTarget({ expr: v })} placeholder='{namespace="$namespace"} |= "error"' />
                        </Form.Field>
                      ) : (
                        // Metrics is the one datasource that runs every query on the panel, each
                        // drawing its own series — so every one is shown here. The others read
                        // only the first. A lone query stays the plain field it always was; the
                        // legend only earns its place once there are lines to tell apart. Rows are
                        // numbered by position: a ref id is an internal key (an import's own names,
                        // or the next free letter), and the legend is what names the line.
                        <>
                          {(targets.length > 0 ? targets : [{ ref_id: 'A', expr: '' }]).map((target, i) => (
                            <Stack
                              key={i}
                              direction={{ xs: 'column', sm: 'row' }}
                              gap={1}
                              alignItems={{ xs: 'stretch', sm: 'flex-end' }}
                              data-testid='panel-query-row'
                            >
                              <Box sx={{ flex: 3, minWidth: 0 }}>
                                <Form.Field label={targets.length > 1 ? `Query ${i + 1}` : 'Query'} required id={`panel-query-${i}`}>
                                  <Input
                                    value={target.expr || ''}
                                    onChange={(v) => patchTarget({ expr: v }, i)}
                                    placeholder='sum(rate(http_requests_total{namespace="$namespace"}[5m]))'
                                  />
                                </Form.Field>
                              </Box>
                              {targets.length > 1 && (
                                <>
                                  <Box sx={{ flex: 1, minWidth: 0 }}>
                                    <Form.Field label='Legend' id={`panel-query-legend-${i}`}>
                                      <Input
                                        value={target.legend_format || ''}
                                        onChange={(v) => patchTarget({ legend_format: v || undefined }, i)}
                                        placeholder='Series name or {{label}}'
                                      />
                                    </Form.Field>
                                  </Box>
                                  <Box sx={{ alignSelf: { xs: 'flex-end', sm: 'auto' } }}>
                                    <Button
                                      tone='ghost'
                                      composition='icon-only'
                                      aria-label={`Remove query ${i + 1}`}
                                      icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
                                      onClick={() => removeTarget(i)}
                                      id={`panel-remove-query-${i}`}
                                    />
                                  </Box>
                                </>
                              )}
                            </Stack>
                          ))}
                          <Box>
                            <Button tone='secondary' size='sm' onClick={addTarget} id='panel-add-query-btn'>
                              Add query
                            </Button>
                          </Box>
                        </>
                      )}
                      {templateVars.length > 0 && (
                        <Box sx={{ p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: ds.radius.md }}>
                          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                            {commandHelp ? (
                              <>
                                This command references {templateVars.map((v) => `$${v}`).join(', ')}, and will be rejected on save. A command runs
                                through a shell on the target, where <code>$name</code> is a shell variable — so <code>$</code> is refused outright
                                rather than being substituted. Use a literal value here; variables work on metrics, logs and trace panels.
                              </>
                            ) : (
                              <>
                                This query references {templateVars.map((v) => `$${v}`).join(', ')}. Those are filled in only when the dashboard is
                                opened from a page that supplies them (a workload or pod detail page). Anywhere else the query runs against the
                                literal text.
                              </>
                            )}
                          </Typography>
                        </Box>
                      )}
                    </>
                  )}

                  {isText && (
                    <Form.Field label='Text'>
                      <Input value={draft.content || ''} onChange={(v) => patch({ content: v })} placeholder='Free text shown in the panel' />
                    </Form.Field>
                  )}

                  <Form.Field
                    label='Visualisation'
                    description={draft.type === 'gauge' ? 'The dial runs 0 to 100 — write the query to return a percentage.' : undefined}
                  >
                    <Select
                      value={draft.type}
                      options={commandHelp || isEntity || isLogs ? PANEL_TYPES.filter((t) => t.value === 'table') : PANEL_TYPES}
                      onChange={(v: string) => changeType(v as PanelType)}
                    />
                  </Form.Field>

                  {/* The unit was only reachable through a template, an import or the JSON,
                      so a number on the card could carry a suffix nobody could find to
                      change. Offered where something reads it: a metrics stat, gauge, table
                      or time series. A bar chart, a text panel — which keeps its datasource —
                      and every other datasource's table do not. */}
                  {draft.datasource === 'metrics' && draft.type !== 'bar' && !isText && (
                    <Form.Field label='Unit' description='Shown after the numbers this panel draws — e.g. %, ms, req/s, pods. Leave empty for none.'>
                      <Input value={draft.unit || ''} onChange={(v) => patch({ unit: v })} placeholder='None' id='panel-unit-input' />
                    </Form.Field>
                  )}

                  {/* Whether the one number is the accounts' sum or their mean is a fact
                      the card cannot show — 100 and 33 are both plausible readings of
                      90, 0 and 10. A gauge averages by default, since its dial reads as
                      a percentage; a stat adds up. Offered where there are accounts to
                      combine, or where a saved choice has to stay changeable. */}
                  {draft.datasource === 'metrics' &&
                    showsOneNumber(draft.type) &&
                    (providerAccounts.length > 1 || draft.options?.combine !== undefined) && (
                      <Form.Field
                        label='Across accounts'
                        description='How the accounts’ figures become the one number shown: added up, or averaged. A gauge averages unless told otherwise; a stat adds up.'
                      >
                        <Select
                          value={combineOf(draft)}
                          options={COMBINE_OPTIONS}
                          onChange={(v: string) => patch({ options: { ...(draft.options || {}), combine: v as PanelCombine } })}
                          id='panel-combine-select'
                        />
                      </Form.Field>
                    )}
                </Form.Section>
              </Card>

              {draft.type === 'timeseries' && draft.datasource === 'metrics' && (
                <Card
                  variant='tinted'
                  header={
                    <GroupHeader
                      title='Data links'
                      description='Where a point on this chart can take you — clicking it opens these links with the series’ labels and the chart’s time range filled in.'
                    />
                  }
                >
                  <Form.Section>
                    <Box>
                      <Stack direction='row' alignItems='center' gap={ds.space[2]} sx={{ mb: ds.space[2] }}>
                        <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 13, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                          Links
                        </Typography>
                        <Box sx={{ flex: 1 }} />
                        <Button
                          tone='secondary'
                          size='sm'
                          onClick={() => patchDataLinks([...dataLinks, { title: 'Open logs', url: '' }])}
                          id='add-data-link'
                        >
                          Add link
                        </Button>
                      </Stack>

                      {dataLinks.length === 0 ? (
                        <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                          No links — clicking a point does nothing.
                        </Typography>
                      ) : (
                        <Stack gap={ds.space[2]}>
                          {dataLinks.map((link, i) => {
                            const patchLink = (next: Partial<PanelDataLink>) =>
                              patchDataLinks(dataLinks.map((l, j) => (j === i ? { ...l, ...next } : l)));
                            return (
                              <Stack key={i} direction='row' gap={ds.space[2]} alignItems='center'>
                                <Box sx={{ flex: 1 }}>
                                  <Input value={link.title || ''} onChange={(v: string) => patchLink({ title: v })} placeholder='Open logs' />
                                </Box>
                                <Box sx={{ flex: 2 }}>
                                  <Input
                                    value={link.url || ''}
                                    onChange={(v: string) => patchLink({ url: v })}
                                    placeholder='/kubernetes/details/{{__account_id}}?pod={{pod}}&start_time={{__from}}&end_time={{__to}}'
                                  />
                                </Box>
                                <Button
                                  tone='ghost'
                                  composition='icon-only'
                                  aria-label='Remove link'
                                  icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
                                  onClick={() => patchDataLinks(dataLinks.filter((_, j) => j !== i))}
                                  id={`remove-data-link-${i}`}
                                />
                              </Stack>
                            );
                          })}
                        </Stack>
                      )}

                      {dataLinks.length > 0 && (
                        <Typography variant='caption' sx={{ display: 'block', mt: ds.space[2], color: ds.gray[500] }}>
                          Write an in-app path and fill it from the clicked point: a series label as{' '}
                          <Box component='code' sx={{ fontFamily: ds.font.mono }}>
                            {'{{pod}}'}
                          </Box>
                          , the account as{' '}
                          <Box component='code' sx={{ fontFamily: ds.font.mono }}>
                            {'{{__account_id}}'}
                          </Box>
                          , and the chart’s range as{' '}
                          <Box component='code' sx={{ fontFamily: ds.font.mono }}>
                            {'{{__from}}'}
                          </Box>
                          /
                          <Box component='code' sx={{ fontFamily: ds.font.mono }}>
                            {'{{__to}}'}
                          </Box>{' '}
                          (epoch ms). A link whose tokens a clicked point cannot fill is skipped rather than opened dead.
                        </Typography>
                      )}

                      {unfinishedDataLinks.length > 0 && (
                        <Box
                          sx={{
                            mt: ds.space[2],
                            p: ds.space[3],
                            border: `1px solid ${ds.amber[300]}`,
                            background: ds.amber[100],
                            borderRadius: ds.radius.md,
                          }}
                        >
                          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                            Every link needs a title and a path inside the product — starting with a single <code>/</code>, as in{' '}
                            <code>/kubernetes/details/{'{{__account_id}}'}</code>. Anyone who can see this dashboard follows these links, which is why
                            an external or scripted destination is refused rather than rendered.
                          </Typography>
                        </Box>
                      )}
                    </Box>
                  </Form.Section>
                </Card>
              )}

              {hasThresholds(draft.type) && (
                <Box id='panel-thresholds'>
                  <Card
                    variant='tinted'
                    header={
                      <GroupHeader
                        title='Thresholds'
                        description={
                          lineThresholds
                            ? 'Draw a line on the chart and colour the panel when a series crosses it.'
                            : 'Colour the panel when its number crosses a line.'
                        }
                      />
                    }
                  >
                    <Form.Section>
                      <Box>
                        {thresholds.length === 0 ? null : (
                          <Stack gap={ds.space[2]}>
                            {/* Named once above the rows, so a field still says what it is once something is typed in it. */}
                            <Box
                              sx={{
                                ...thresholdGridSx,
                                fontFamily: ds.font.display,
                                fontSize: ds.text.small,
                                fontWeight: ds.weight.medium,
                                color: ds.gray[700],
                              }}
                            >
                              {showAppliesTo && <span>Applies to</span>}
                              <span>When</span>
                              {/* Units live in the header: an affix box inside a column this narrow leaves no room to type. */}
                              <span>
                                Value{' '}
                                {draft.unit?.trim() && (
                                  <Box
                                    component='span'
                                    sx={{ color: ds.gray[500], fontWeight: ds.weight.regular, fontSize: ds.text.caption }}
                                  >{`(${draft.unit.trim()})`}</Box>
                                )}
                              </span>
                              {lineThresholds && (
                                <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                                  <span>
                                    Hold{' '}
                                    <Box component='span' sx={{ color: ds.gray[500], fontWeight: ds.weight.regular, fontSize: ds.text.caption }}>
                                      (min)
                                    </Box>
                                  </span>
                                  <Tooltip
                                    title={`Optional. Colour only once a line has stayed past the value this long — ${FOR_MIN_SECONDS / 60} to ${
                                      FOR_MAX_SECONDS / 60
                                    } minutes. Empty fires on the latest sample.`}
                                  >
                                    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}>
                                      <InfoOutlinedIcon sx={{ fontSize: 13 }} />
                                    </Box>
                                  </Tooltip>
                                </Box>
                              )}
                              <span>Colour</span>
                              <span />
                            </Box>
                            {thresholds.map((step, i) => {
                              const patchStep = (next: Partial<PanelThresholdStep>) =>
                                patchThresholds(thresholds.map((other, j) => (j === i ? { ...other, ...next } : other)));
                              return (
                                // Indexed: two steps can hold the same value while one
                                // is being typed, so neither value nor colour is a key.
                                // Read left to right as the rule: "Every account ≥ 80 → Red".
                                <Box key={i} sx={{ pb: ds.space[3], borderBottom: `1px solid ${ds.gray[200]}` }}>
                                  <Box sx={{ ...thresholdGridSx, alignItems: 'start' }}>
                                    {showAppliesTo && (
                                      <Select
                                        value={
                                          appliesToOf(step) === 'account'
                                            ? `${ACCOUNT_SUBJECT}${step.account_id || ''}`
                                            : lineThresholds
                                            ? 'every'
                                            : appliesToOf(step)
                                        }
                                        options={appliesToOptions}
                                        onChange={(v: string) =>
                                          v.startsWith(ACCOUNT_SUBJECT)
                                            ? patchStep({ applies_to: 'account', account_id: v.slice(ACCOUNT_SUBJECT.length) })
                                            : // "Shown number" is what an absent field means, so it is stored as one:
                                              // the panel JSON stays what every step before this looked like.
                                              patchStep({ applies_to: v === 'every' ? 'every' : undefined, account_id: undefined })
                                        }
                                        clearable={false}
                                        popoverWidth={240}
                                        id={`threshold-applies-${i}`}
                                      />
                                    )}
                                    {/* The closed picker shows only the symbol; the open list spells it out. */}
                                    <Box sx={{ [`${CLOSED_PICKER} .${OP_PHRASE_CLASS}`]: { display: 'none' } }}>
                                      <Select
                                        value={opOf(step)}
                                        options={THRESHOLD_OP_OPTIONS}
                                        // `gte` is what an absent field means; stored as absent for the same reason.
                                        onChange={(v: string) => patchStep({ op: v === 'gte' ? undefined : (v as PanelThresholdOp) })}
                                        clearable={false}
                                        popoverWidth={160}
                                        id={`threshold-op-${i}`}
                                      />
                                    </Box>
                                    <ThresholdValue
                                      value={step.value}
                                      index={i}
                                      showError={thresholdsAttempted}
                                      onChange={(next) => patchStep({ value: next })}
                                    />
                                    {lineThresholds && <ThresholdHold seconds={step.for} index={i} onChange={(next) => patchStep({ for: next })} />}
                                    {/* Same trick as the comparison: the closed picker is the swatch alone, the open list names it. */}
                                    <Box sx={{ [`${CLOSED_PICKER} .${COLOR_NAME_CLASS}`]: { display: 'none' } }}>
                                      <Select
                                        value={step.color}
                                        options={THRESHOLD_COLOR_OPTIONS}
                                        onChange={(v: string) => patchStep({ color: v as PanelThresholdColor })}
                                        clearable={false}
                                        popoverWidth={140}
                                        id={`threshold-color-${i}`}
                                      />
                                    </Box>
                                    <Button
                                      tone='ghost'
                                      composition='icon-only'
                                      aria-label={`Remove threshold ${i + 1}`}
                                      icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
                                      onClick={() => patchThresholds(thresholds.filter((_, j) => j !== i))}
                                      id={`remove-threshold-${i}`}
                                    />
                                  </Box>
                                  <ThresholdSentence
                                    step={step}
                                    index={i}
                                    isLine={Boolean(lineThresholds)}
                                    unit={draft.unit?.trim() || ''}
                                    subject={
                                      appliesToOf(step) === 'account'
                                        ? lineThresholds
                                          ? `any ${providerAccounts.find((a) => a.value === step.account_id)?.label || 'missing account'} line`
                                          : providerAccounts.find((a) => a.value === step.account_id)?.label || 'that account'
                                        : lineThresholds
                                        ? 'any line'
                                        : appliesToOf(step) === 'every'
                                        ? 'any account'
                                        : 'the number shown'
                                    }
                                  />
                                </Box>
                              );
                            })}
                          </Stack>
                        )}

                        <Stack direction='row' alignItems='center' justifyContent='space-between' gap={ds.space[3]} sx={{ mt: ds.space[3] }}>
                          {thresholds.length > 0 && (
                            <Tooltip
                              title={`${
                                lineThresholds
                                  ? 'Each line on the chart is judged on its own, on the samples the chart is showing. A step with a hold colours the panel only once every sample inside the hold was past the line, and the chart shades that stretch. A hold needs at least two of the chart’s samples, so a long time range may be too coarse to judge it — the panel says so.'
                                  : 'A value past a step’s line takes its colour.'
                              } Past several, the furthest wins — the highest of the ≥ and > steps, the lowest of the ≤ and < steps — and when one of each applies, the more severe colour. Past none, the panel draws plain.${
                                showAppliesTo && !lineThresholds
                                  ? ' “Shown number” compares the number on the panel: the accounts’ sum or average, or one account’s when the view is narrowed to it. “Every account” checks each account on its own, and names the one that crossed.'
                                  : ''
                              }`}
                            >
                              <Box
                                component='span'
                                data-testid='threshold-how-it-works'
                                sx={{
                                  display: 'inline-flex',
                                  alignItems: 'center',
                                  gap: ds.space[1],
                                  fontSize: ds.text.small,
                                  color: ds.gray[600],
                                  cursor: 'help',
                                }}
                              >
                                <InfoOutlinedIcon sx={{ fontSize: 13 }} />
                                How thresholds are evaluated
                              </Box>
                            </Tooltip>
                          )}
                          <Box sx={{ ml: 'auto' }}>
                            <Button
                              tone='secondary'
                              size='sm'
                              // Red first: the first threshold anyone writes is the one
                              // that means trouble, and a value of its own would be a
                              // guess at the panel's scale.
                              onClick={() => patchThresholds([...thresholds, { value: Number.NaN, color: 'red' }])}
                              id='add-threshold'
                            >
                              Add threshold
                            </Button>
                          </Box>
                        </Stack>

                        {badHolds.length > 0 && (
                          <Box data-testid='threshold-bad-hold' sx={{ mt: ds.space[2] }}>
                            <Banner
                              tone='warning'
                              surface='section'
                              message={`A hold is a whole number of minutes, from ${FOR_MIN_SECONDS / 60} to ${
                                FOR_MAX_SECONDS / 60
                              }. Change it, or clear it so the step fires on the latest sample.`}
                              actions={[
                                {
                                  id: 'threshold-clear-bad-holds',
                                  label: `Clear ${badHolds.length === 1 ? 'this hold' : 'these holds'}`,
                                  onClick: () => clearHolds(badHolds),
                                },
                              ]}
                            />
                          </Box>
                        )}

                        {ignoredHolds.length > 0 && (
                          <Box
                            data-testid='threshold-ignored-hold'
                            sx={{
                              mt: ds.space[2],
                              p: ds.space[3],
                              border: `1px solid ${ds.amber[300]}`,
                              background: ds.amber[100],
                              borderRadius: ds.radius.md,
                            }}
                          >
                            <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                              {ignoredHolds.length === 1 ? 'A threshold has' : `${ignoredHolds.length} thresholds have`} a hold, which only a
                              time-series panel can judge, so {ignoredHolds.length === 1 ? 'it is' : 'they are'} not drawn here. Remove the hold to
                              have the step fire on the number shown.
                            </Typography>
                            <Box sx={{ mt: ds.space[2] }}>
                              <Button tone='secondary' size='sm' onClick={() => clearHolds(ignoredHolds)} id='threshold-remove-ignored-holds'>
                                Remove {ignoredHolds.length === 1 ? 'hold' : 'holds'}
                              </Button>
                            </Box>
                          </Box>
                        )}

                        {/* A missing number is flagged on its own field; this covers what no field shows, such as an imported colour or comparison the editor cannot read. */}
                        {unfinishedThresholds.some((step) => Number.isFinite(step.value)) && (
                          <Box
                            sx={{
                              mt: ds.space[2],
                              p: ds.space[3],
                              border: `1px solid ${ds.amber[300]}`,
                              background: ds.amber[100],
                              borderRadius: ds.radius.md,
                            }}
                          >
                            <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                              Every threshold needs a comparison, a number and a colour. Pick them again, or remove the step.
                            </Typography>
                          </Box>
                        )}

                        {thresholdConflicts.length > 0 && (
                          <Box
                            data-testid='threshold-overlaps'
                            sx={{
                              mt: ds.space[2],
                              p: ds.space[3],
                              border: `1px solid ${ds.blue[300]}`,
                              background: ds.blue[100],
                              borderRadius: ds.radius.md,
                              display: 'grid',
                              gap: ds.space[1],
                            }}
                          >
                            {thresholdConflicts.map((overlap) => (
                              <Typography key={overlap.steps.join(':')} variant='body2' sx={{ color: ds.gray[700] }}>
                                {overlap.message}
                              </Typography>
                            ))}
                          </Box>
                        )}

                        {danglingThresholds.length > 0 && (
                          <Box
                            data-testid='threshold-dangling'
                            sx={{
                              mt: ds.space[2],
                              p: ds.space[3],
                              border: `1px solid ${ds.amber[300]}`,
                              background: ds.amber[100],
                              borderRadius: ds.radius.md,
                            }}
                          >
                            <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                              {danglingThresholds.length === 1 ? 'A threshold names' : `${danglingThresholds.length} thresholds name`} an account this
                              panel no longer covers, so it never fires. Pick one of the panel’s accounts, or remove the step.
                            </Typography>
                          </Box>
                        )}
                      </Box>
                    </Form.Section>
                  </Card>
                </Box>
              )}

              {isNudgebee && (
                <Card
                  variant='tinted'
                  header={
                    <GroupHeader
                      title='Table columns'
                      description='What the table shows, and where a row can take you — either by making a column clickable or by adding a column of links.'
                    />
                  }
                >
                  <Form.Section>
                    {isEntity && (
                      <Form.Field
                        label='Hidden columns'
                        description='Still queried, just not shown — hide the ids a link is built from so the table stays readable.'
                      >
                        <Select
                          multiple
                          value={hiddenColumns}
                          options={entityDraft.columns.map((name) => ({
                            label: findTable(entityDraft.table).columns.find((c) => c.name === name)?.label || name,
                            value: name,
                          }))}
                          onChange={(next: string[]) => patchColumns(setHiddenColumns(columns, next))}
                          placeholder='None'
                          id='panel-hidden-columns'
                        />
                      </Form.Field>
                    )}

                    <Box>
                      <Stack direction='row' alignItems='center' gap={1} sx={{ mb: 1 }}>
                        <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 13, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                          Links
                        </Typography>
                        <Box sx={{ flex: 1 }} />
                        <Button
                          tone='secondary'
                          size='sm'
                          onClick={() => patchColumns([...columns, { title: 'Investigate', link: { url: '' } }])}
                          id='add-link-column'
                        >
                          Add link
                        </Button>
                      </Stack>

                      {links.length === 0 ? (
                        <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                          No links — the table is text only.
                        </Typography>
                      ) : (
                        <Stack gap={1}>
                          {links.map((link) => {
                            const patchLink = (next: Partial<PanelColumn>) => patchColumns(columns.map((c) => (c === link ? { ...c, ...next } : c)));
                            return (
                              <Stack key={links.indexOf(link)} direction='row' gap={1} alignItems='center'>
                                {isEntity && (
                                  <Box sx={{ flex: 1.2 }}>
                                    <Select
                                      value={link.name || NEW_COLUMN}
                                      options={linkTargetOptionsFor(link)}
                                      // Switching target swaps which field names the link, so the
                                      // other one is cleared rather than left behind as dead config —
                                      // the server refuses a link that carries both. Clearing the
                                      // picker reads as New column, same as choosing it.
                                      onChange={(v: string) =>
                                        patchLink(
                                          v && v !== NEW_COLUMN
                                            ? { name: v, title: undefined }
                                            : { name: undefined, title: link.title || 'Investigate' }
                                        )
                                      }
                                    />
                                  </Box>
                                )}
                                {!link.name && (
                                  <Box sx={{ flex: 1 }}>
                                    <Input value={link.title || ''} onChange={(v: string) => patchLink({ title: v })} placeholder='Investigate' />
                                  </Box>
                                )}
                                <Box sx={{ flex: 2 }}>
                                  <Input
                                    value={link.link?.url || ''}
                                    onChange={(v: string) => patchLink({ link: { url: v } })}
                                    placeholder='/investigate?id={{id}}&accountId={{account_id}}'
                                  />
                                </Box>
                                <Button
                                  tone='ghost'
                                  composition='icon-only'
                                  aria-label='Remove link'
                                  icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
                                  // A link on an existing column keeps that column's other settings;
                                  // an added column has nothing left once its link is gone.
                                  onClick={() => patchColumns(columns.flatMap((c) => (c !== link ? [c] : c.name ? [{ ...c, link: undefined }] : [])))}
                                  id={`remove-link-${links.indexOf(link)}`}
                                />
                              </Stack>
                            );
                          })}
                        </Stack>
                      )}

                      {isEntity && links.length > 0 && (
                        <Typography variant='caption' sx={{ display: 'block', mt: 1, color: ds.gray[500] }}>
                          Attach the link to a column and its own values become the links; choose New column for a plain action column instead. Write
                          an in-app path and fill it from the row with{' '}
                          <Box component='code' sx={{ fontFamily: ds.font.mono }}>
                            {'{{column}}'}
                          </Box>
                          . Available here: {entityDraft.columns.map((c) => `{{${c}}}`).join(', ')}.
                        </Typography>
                      )}

                      {unfinishedLinks.length > 0 && (
                        <Box sx={{ mt: 1, p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: ds.radius.md }}>
                          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                            Every link needs a header (or a column to attach to) and a path inside the product — starting with a single <code>/</code>
                            , as in <code>/investigate?id={'{{id}}'}</code>. Anyone who can see this dashboard follows these links, which is why an
                            external or scripted destination is refused rather than rendered.
                          </Typography>
                        </Box>
                      )}

                      {unknownPlaceholders.length > 0 && (
                        <Box sx={{ mt: 1, p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: ds.radius.md }}>
                          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                            {unknownPlaceholders.map((c) => `{{${c}}}`).join(', ')} {unknownPlaceholders.length === 1 ? 'is' : 'are'} not selected in
                            Columns above, so {unknownPlaceholders.length === 1 ? 'it' : 'they'} cannot be filled in — those cells will be blank. Add
                            the column and hide it if you do not want it shown.
                          </Typography>
                        </Box>
                      )}
                    </Box>
                  </Form.Section>
                </Card>
              )}

              {/* Last: you can only name a panel well once you know what it shows. */}
              <Card variant='tinted' header={<GroupHeader title='Panel details' description='How this panel appears to viewers.' />}>
                <Form.Section>
                  <Form.Field label='Title' required>
                    <Input value={draft.title} onChange={(v) => patch({ title: v })} placeholder='p99 request latency' />
                  </Form.Field>
                  <Form.Field label='Description'>
                    <Input value={draft.description || ''} onChange={(v) => patch({ description: v })} placeholder='Optional — shown on hover' />
                  </Form.Field>
                </Form.Section>
              </Card>
            </Stack>
          </Form>
        </Box>

        {/* Scrolls on its own so a long table cannot push the footer around. */}
        <Box
          sx={{
            width: { xs: '100%', md: PREVIEW_RAIL_WIDTH },
            flexShrink: 0,
            overflowY: 'auto',
            padding: 'var(--ds-space-5)',
            background: ds.background[200],
            borderLeft: { xs: 'none', md: `1px solid ${ds.gray[200]}` },
            borderTop: { xs: `1px solid ${ds.gray[200]}`, md: 'none' },
          }}
        >
          <PanelPreview
            panel={resolvedDraft}
            accountOptions={accountOptions}
            variables={variables || {}}
            startTime={startTime ?? fallbackRange.start}
            endTime={endTime ?? fallbackRange.end}
          />
        </Box>
      </Box>
    </Modal>
  );
};

export default PanelEditorModal;
