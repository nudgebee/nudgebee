import React from 'react';
import { Box, Typography } from '@mui/material';
import FilterAltOutlinedIcon from '@mui/icons-material/FilterAltOutlined';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Chart from '@ui/Chart';
import { Chip } from '@ui/Chip';
import { Link } from '@ui/Link';
import FilterDropdown from '@ui/FilterDropdown';
import { Skeleton } from '@ui/Skeleton';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import Tooltip from '@ui/Tooltip';
import { snackbar } from '@ui/Toast';
import { downloadIcon, RefreshIcon, writeIconLight } from '@assets';
import { ds } from '@utils/colors';
import { downloadJsonFile, filenameSlug } from '@utils/fileDownload';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import Currency from '@shared/format/Currency';
import Memory from '@shared/format/Memory';
import NumberFormat from '@shared/format/Number';
import { formatDurationInTrace } from '@utils/common';
import type { AccountOption, Panel } from '@api1/dashboards';
import { addedColumns, columnSettings, panelColumnsOf, renderRowUrl } from './panelColumns';
import PanelGauge from './PanelGauge';
import { hasThresholds, panelBreach, thresholdTone } from './panelThresholds';
import PanelState, { type PanelStateTone } from './PanelState';
import { usePanelData, type ColumnKind, type PanelData, type PanelErrorKind, type PanelSeries } from './usePanelData';
import { applyAccountFilter, describePanelScope, effectiveFilterAccount, resolvePanelAccounts } from './panelAccounts';
import { consolidatedSeries, lastValue, metricLabel, statTotal } from './panelSeries';
import { downloadNodeAsPng, EXPORT_HIDE_ATTR, PANEL_PENDING_ATTR } from './panelImage';
import type { VariableValues } from './templating';

/** Plot height, excluding the legend the chart renders beneath it. */
const CHART_HEIGHT = 160;

/** One shared empty list, so an absent dashboard filter is a stable prop for the memoised panel. */
const NO_FILTER: string[] = [];

/** One Chart.js dataset for a timeseries panel's line. */
function lineDataset(s: PanelSeries) {
  return {
    label: s.label,
    data: s.values,
    pointRadius: 0,
    borderWidth: s.consolidated ? 2.5 : 1,
    ...(s.consolidated ? { borderDash: [6, 4] } : {}),
  };
}

/**
 * How far outside the viewport a panel starts loading. Kept short: panel height
 * is `grid_pos.h * 30`px, so a row of stat panels is ~120px and a generous
 * margin pulls two or three rows of them in before they are anywhere near the
 * fold. The queue in panelQueue.ts bounds what that admits either way.
 */
const PRELOAD_MARGIN = '120px';

/** True once the element has been within `PRELOAD_MARGIN` of the viewport. */
function useSeenOnScreen<T extends HTMLElement>() {
  const ref = React.useRef<T | null>(null);
  const [seen, setSeen] = React.useState(false);

  React.useEffect(() => {
    if (seen) return;
    const node = ref.current;
    // Without an observer (jsdom, older browsers) there is no way to tell — load
    // rather than leave the panel skeletal forever.
    if (!node || typeof IntersectionObserver === 'undefined') {
      setSeen(true);
      return;
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting) return;
        setSeen(true);
        observer.disconnect();
      },
      { rootMargin: PRELOAD_MARGIN }
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [seen]);

  return [ref, seen] as const;
}

interface Props {
  panel: Panel;
  /** Every account the viewer can see; the panel's scope resolves against it. */
  accounts: AccountOption[];
  /**
   * The dashboard's account filter, applied to every panel at once. Narrows this
   * panel's scope before its own picker does; empty means no filter. A panel none
   * of these accounts belong to shows the filter message, with the action that
   * clears the dashboard filter rather than the panel's own pick.
   */
  dashboardAccountIds?: string[];
  onClearDashboardFilter?: () => void;
  variables: VariableValues;
  startTime: number;
  endTime: number;
  /** Bumped by the dashboard's Refresh; refetches every panel at once. */
  refreshToken?: number;
  /** Overrides the scroll gate. */
  forceLoad?: boolean;
  /** Edit mode. */
  editing?: boolean;
  /**
   * Draws this instead of querying anything, for the editor's preview. Setting it
   * turns the fetch off rather than racing it.
   */
  sampleData?: PanelData;
  /** Opens this panel in the editor. Omitted when the viewer cannot edit. */
  onEdit?: () => void;
  /** Rendered in the panel header — the drag and delete affordances. */
  actions?: React.ReactNode;
}

/**
 * How each failure reads. `config` is amber, not red: an unfinished panel is not
 * a fault, and red is kept for the case where something actually went wrong.
 */
const ERROR_TONE: Record<PanelErrorKind, PanelStateTone> = {
  config: 'attention',
  blocked: 'blocked',
  filter: 'empty',
  failed: 'error',
};

/** Formats a scalar for the stat panel without lying about precision. */
function formatValue(value: number | null | undefined, unit?: string): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '—';
  const abs = Math.abs(value);
  const rounded = abs >= 100 ? value.toFixed(0) : abs >= 1 ? value.toFixed(2) : value.toPrecision(3);
  return unit ? `${rounded} ${unit}` : rounded;
}

const NUMBER_CELL_SX = { textAlign: 'right', fontSize: ds.text.caption, fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-700)' };
const NUMBER_CELL_SUFFIX_SX = { color: 'var(--ds-gray-700)', fontSize: ds.text.caption };

function renderTableCell(kind: ColumnKind, value: string): React.ReactNode {
  if (!value) return value;
  switch (kind) {
    case 'time':
      return <Datetime value={value} sx={{ fontSize: ds.text.caption }} />;
    case 'currency':
      return <Currency value={Number(value)} sx={{ fontSize: ds.text.caption }} withTooltip={false} />;
    case 'memory':
      // Stored in MB, shown in GB — the same conversion the Nodes listing does.
      return <Memory value={Number(value)} sourceUnit='mb' targetUnit='gb' sx={{ fontSize: ds.text.caption }} />;
    case 'cpu':
      return <NumberFormat value={Number(value)} suffix=' cores' sx={NUMBER_CELL_SX} suffixSx={NUMBER_CELL_SUFFIX_SX} />;
    case 'duration':
      return formatDurationInTrace(Number(value));
    case 'number':
      return <NumberFormat value={Number(value)} sx={NUMBER_CELL_SX} />;
    default:
      return value;
  }
}

/**
 * Memoised: the dashboard re-renders on every keystroke in its toolbar and every
 * time the editor opens or closes, and without this each of those re-rendered
 * every panel — and every re-render handed Chart.js fresh arrays, so every chart
 * on the page updated and rebuilt its legend for a change that touched none of
 * them. Callers pass stable props (see SortablePanel) so the memo holds.
 */
const DashboardPanel: React.FC<Props> = React.memo(function DashboardPanel({
  panel,
  accounts,
  variables,
  dashboardAccountIds = NO_FILTER,
  onClearDashboardFilter,
  startTime,
  endTime,
  refreshToken = 0,
  forceLoad = false,
  editing = false,
  sampleData,
  onEdit,
  actions,
}) {
  /**
   * The panel's own picker is single-select and narrows WITHIN the dashboard's filter: its options are the
   * accounts THIS panel is scoped to — every account of the provider when it is type-scoped, or just the ones
   * it names — less any the dashboard filter left out. So the two filters compose rather than compete: the
   * dashboard's picks every panel's accounts at once, the panel's picks one of those.
   */
  const [accountId, setAccountId] = React.useState('');
  const scopedAccounts = React.useMemo(() => resolvePanelAccounts(panel, accounts), [panel, accounts]);
  const panelAccounts = React.useMemo(() => applyAccountFilter(scopedAccounts, dashboardAccountIds), [scopedAccounts, dashboardAccountIds]);
  const filterOptions = React.useMemo(
    () => panelAccounts.map((a) => ({ label: a.label, value: a.value, group: a.cloud_provider || 'Other' })),
    [panelAccounts]
  );
  // Nothing picked yet means the first account, not "no account": a panel that waits for a choice shows an
  // empty box, and one account costs the same single request whether it was chosen or defaulted to. A pick
  // the panel has since been re-scoped away from falls back to that same default — see the rule for why.
  // Metrics is the exception: those panels answer for every account at once — a chart merges the accounts'
  // series, a stat adds their numbers up — so an unmade choice means ALL of them and the picker stays empty
  // until the viewer narrows it. Unless "all of them" is ONE account — the panel's scope, or what the dashboard
  // filter left of it — in which case the picker shows that account as the selection it effectively is.
  const effectiveAccountId = effectiveFilterAccount(accountId, panelAccounts, panel.datasource === 'metrics' && panelAccounts.length !== 1);
  const selectedOption = filterOptions.find((o) => o.value === effectiveAccountId) || null;
  // The hook takes a list so a panel scoped to one account still works without a
  // selection; the picker just never supplies more than one. With no pick, the
  // dashboard's filter is the list — and when that names no account of this
  // panel's, the hook reports the filter miss rather than querying nothing.
  const accountFilter = React.useMemo(
    () => (effectiveAccountId ? [effectiveAccountId] : dashboardAccountIds),
    [effectiveAccountId, dashboardAccountIds]
  );

  // Composite rather than a sum: either counter moving changes the key, and no
  // pair of values can collide the way `dashboard + panel` could.
  const [panelRefresh, setPanelRefresh] = React.useState(0);
  const refreshKey = `${refreshToken}:${panelRefresh}`;

  // A text panel has nothing to fetch, so Refresh would be a no-op on it — but it is still editable, so the
  // menu itself is not conditional on the type.
  const menuItems = React.useMemo(() => {
    const items: { id: string; label: string; icon: unknown }[] = [];
    if (panel.type !== 'text') items.push({ id: 'refresh', label: 'Refresh', icon: RefreshIcon });
    if (onEdit) items.push({ id: 'edit', label: 'Edit', icon: writeIconLight });
    items.push({ id: 'export', label: 'Export JSON', icon: downloadIcon });
    if (panel.type !== 'text') items.push({ id: 'export-png', label: 'Export PNG', icon: downloadIcon });
    return items;
  }, [panel.type, onEdit]);

  // Panels fetch on scroll, not on open: a 30-panel dashboard otherwise fires 30
  // concurrent provider requests before the viewer has seen the second row.
  const [panelRef, seen] = useSeenOnScreen<HTMLDivElement>();

  /** Rasterises this panel. */
  const exportPng = async () => {
    if (!panelRef.current) return;
    try {
      await downloadNodeAsPng(panelRef.current, panel.title, 'panel');
    } catch (err) {
      console.error('panel export failed', err);
      snackbar.error('Could not export this panel as an image.');
    }
  };

  const {
    data: fetched,
    loading,
    error: fetchError,
    warning: fetchWarning,
  } = usePanelData({
    panel,
    accounts,
    accountFilter,
    variables,
    startTime,
    endTime,
    refreshKey,
    enabled: (seen || forceLoad) && !sampleData,
    // `forceLoad` is the preview and the PNG capture — panels someone is waiting
    // on directly, rather than panels that merely came into view.
    immediate: forceLoad,
  });

  // A sample outranks what the last fetch left behind: `body()` reads `error`
  // before it reads the data, so a stale error would win.
  const data = sampleData ?? fetched;
  const error = sampleData ? null : fetchError;
  const warning = sampleData ? null : fetchWarning;

  /** Nothing to draw yet — no data and no error. */
  const pending = panel.type !== 'text' && !data && !error;
  /*
   * The chip says what the panel is SHOWING. Unfiltered, that is its scope —
   * "All K8S", "3 accounts" — which is what tells two otherwise identical
   * panels apart. Narrowed, by the dashboard filter or the panel's own pick,
   * it is the account name(s), so a viewer who picked prod-us in the toolbar
   * sees "prod-us" on every panel that pick applies to; past two it counts.
   * The full scope stays on hover.
   */
  const scopeLabel = describePanelScope(panel, accounts);
  const shownAccounts = effectiveAccountId ? panelAccounts.filter((a) => a.value === effectiveAccountId) : panelAccounts;
  const narrowed = shownAccounts.length > 0 && shownAccounts.length < scopedAccounts.length;
  const shownLabel = !narrowed
    ? scopeLabel
    : shownAccounts.length <= 2
    ? shownAccounts.map((a) => a.label).join(', ')
    : `${shownAccounts.length} of ${scopedAccounts.length} accounts`;

  /** The one thing worth doing about each failure. */
  const errorAction = (kind: PanelErrorKind) => {
    if (editing) return undefined;
    if (kind === 'config') return onEdit ? { label: 'Edit panel', onClick: onEdit } : undefined;
    // The miss is the panel's own pick when it has one, else the dashboard's filter.
    if (kind === 'filter') {
      if (accountId || !onClearDashboardFilter) return { label: 'Show all accounts', onClick: () => setAccountId('') };
      return { label: 'Show all accounts', onClick: onClearDashboardFilter };
    }
    // The same refetch the overflow menu's Refresh fires, one click instead of two.
    if (kind === 'failed') return { label: 'Retry', onClick: () => setPanelRefresh((n) => n + 1) };
    return undefined;
  };

  /**
   * The one number a stat or gauge panel shows: every account that answered,
   * added up. Computed here rather than inside `drawing` because the threshold
   * tint colours the FRAME — the border and the header band, both outside the
   * body `drawing` returns.
   *
   * Null for every other panel, and for a command datasource's table, which
   * comes back as rows whatever the panel type says.
   */
  const stat = React.useMemo(() => {
    if (!data || data.table || !hasThresholds(panel.type) || data.series.length === 0) return null;
    return statTotal(
      data.series,
      (panel.targets || []).map((t) => t.ref_id || 'A'),
      data.failedAccounts || []
    );
  }, [panel, data]);

  /**
   * The threshold this panel's value has crossed, and how to draw it. A panel
   * with no thresholds — which is every panel authored before this — resolves to
   * undefined and keeps its plain frame.
   */
  const breach = panelBreach(panel, stat?.total);
  const tone = breach ? thresholdTone(breach.color) : undefined;

  /*
   * The drawing, rebuilt only when the data or the panel changes. The chart
   * props below are new arrays each time this runs, and react-chartjs-2
   * updates the chart whenever their identity changes — so a rebuild on an
   * unrelated render (Edit pressed, the header's account filter opening) is a
   * full chart update and legend rebuild, on every panel at once.
   */
  const drawing = React.useMemo(() => {
    if (panel.type === 'text' || !data) return null;
    // A command datasource answers with a table, whatever the panel type says.
    if (data.table) {
      if (data.table.rows.length === 0) {
        return (
          <Box sx={{ height: '100%' }}>
            <PanelState tone='empty' title='Nothing came back' description='The query ran and returned no rows.' />
          </Box>
        );
      }
      const columns = data.table.columns;
      const kinds = data.table.column_kinds || [];
      // Headers are display labels on an entity panel ("Event id"), while the
      // author configured columns by the QUERY's names (`id`) — so settings
      // resolve against those, not against what is on screen.
      const names = data.table.column_names || columns;
      const panelColumns = panelColumnsOf(panel);
      const settings = columnSettings(panelColumns);
      // A hidden column is still QUERIED — it is what a link is built from — so
      // it is dropped here rather than from the query.
      const shown = columns
        .map((label, index) => ({ label, index, column: settings.get(names[index]) }))
        .filter((c) => c.column?.visibility !== 'hidden');
      const added = addedColumns(panelColumns);
      const headers = [...shown.map((c) => ({ name: c.column?.title || c.label })), ...added.map((c) => ({ name: c.title || '' }))];
      // Timestamps go through the same Datetime component the traces and events listings use — relative text
      // with the absolute time on hover — rather than showing the store's raw value.
      const rows = data.table.rows.map((row) => [
        ...shown.map(({ index, column }) => {
          const cell = row[index];
          // The registry decides how a value reads; a column may override it for
          // the cases the registry cannot know, like a Postgres panel's own columns.
          const content = renderTableCell(column?.format || kinds[index] || 'text', cell);
          const href = column?.link ? renderRowUrl(column.link.url, names, row) : null;
          // Linked or not, `value` stays the raw cell so sort and CSV export read
          // the data rather than the markup.
          if (href)
            return {
              // New tab, and the DS Link's arrow says so: a dashboard is something
              // you watch, and following a row's link in place loses the page you
              // were reading — along with its time range and account filter.
              component: (
                <Link href={href} openInNew>
                  {content}
                </Link>
              ),
              value: cell,
            };
          // A formatted cell is a component; plain text stays text so the table's
          // own tooltip and truncation keep working on it.
          return typeof content === 'string' ? { text: content, value: cell } : { component: content, value: cell };
        }),
        // An added column has no data of its own — every cell reads as its title.
        // Never exported: a CSV of the same word repeated is noise.
        ...added.map((column) => {
          const href = column.link ? renderRowUrl(column.link.url, names, row) : null;
          return {
            component: href ? (
              <Link href={href} openInNew>
                {column.title}
              </Link>
            ) : null,
            exportEnabled: false,
          };
        }),
      ]);
      return (
        <Box>
          <CustomTable headers={headers} tableData={rows} />
          {data.table.truncated && (
            <Typography variant='caption' sx={{ color: ds.gray[500] }}>
              Showing the first {data.table.rows.length} rows.
            </Typography>
          )}
        </Box>
      );
    }
    if (data.series.length === 0) {
      return (
        <Box sx={{ height: '100%' }}>
          <PanelState tone='empty' title='No data in this range' description='The query ran but matched nothing. Try a wider time range.' />
        </Box>
      );
    }

    switch (panel.type) {
      case 'stat':
      case 'gauge': {
        // One number, adding up every account that answered — a panel scoped to
        // four clusters used to render series[0], which reads as the total and is
        // one cluster's figure. The breakdown behind it is on hover, because a
        // total nobody can take apart is a number nobody can check.
        //
        // Computed above the memo: the same total decides the threshold tint on
        // the frame. It is non-null on exactly the branch this is, so the guard
        // is for the type checker rather than for a case that happens.
        if (!stat) return null;
        const answered = stat.rows.filter((r) => !r.failed).length;
        // "2 of 3 accounts" is the partial total's caveat, in the place the viewer
        // already reads the account count; the hover names the account that did
        // not answer. Neither a bare asterisk nor a banner above the card.
        const countCaption = stat.partial ? `${answered} of ${stat.rows.length} accounts` : `${stat.rows.length} accounts`;
        const breakdown = stat.rows.length > 1 && (
          <Box sx={{ display: 'grid', gap: 0.4, py: 0.25 }}>
            {stat.rows.map((row) => (
              <Box key={row.account} sx={{ display: 'flex', justifyContent: 'space-between', gap: 2 }}>
                <span>{row.account}</span>
                <span style={{ fontVariantNumeric: 'tabular-nums', opacity: row.failed ? 0.7 : 1 }}>
                  {row.failed ? 'no answer' : formatValue(row.value, panel.unit)}
                </span>
              </Box>
            ))}
          </Box>
        );
        // The dial is a stat with a bounded scale, and takes the same total.
        if (panel.type === 'gauge') {
          return (
            <Box data-testid={`panel-gauge-${panel.id}`} sx={{ height: '100%' }}>
              <Tooltip title={breakdown || ''}>
                <Box sx={{ height: '100%' }}>
                  <PanelGauge value={stat.total} caption={stat.partial ? countCaption : stat.caption} />
                </Box>
              </Tooltip>
            </Box>
          );
        }
        return (
          <Box data-testid={`panel-stat-${panel.id}`}>
            <Tooltip title={breakdown || ''}>
              <Typography component='div' sx={{ fontSize: 28, fontWeight: 650, letterSpacing: '-0.02em', color: ds.gray[700], width: 'fit-content' }}>
                {formatValue(stat.total, panel.unit)}
              </Typography>
            </Tooltip>
            {stat.caption && (
              <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                {stat.caption}
              </Typography>
            )}
            {stat.rows.length > 1 && (
              <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block' }}>
                {countCaption}
              </Typography>
            )}
          </Box>
        );
      }
      case 'table': {
        // Several accounts: the account is its own column rather than a prefix
        // folded into the series text, so it can be read — and scanned — as one.
        const byAccount = data.series.some((s) => s.accountLabel);
        const headers = byAccount
          ? [
              { name: 'Account', width: '25%' },
              { name: 'Series', width: '45%' },
              { name: 'Latest', width: '30%' },
            ]
          : [
              { name: 'Series', width: '60%' },
              { name: 'Latest', width: '40%' },
            ];
        const rows = data.series.map((s) => {
          const latest = formatValue(lastValue(s.values), panel.unit);
          const series = byAccount ? metricLabel(s) : s.label;
          return [
            ...(byAccount ? [{ text: s.accountLabel || '', value: s.accountLabel || '' }] : []),
            { text: series, value: series },
            { text: latest, value: latest },
          ];
        });
        return <CustomTable headers={headers} tableData={rows} />;
      }
      case 'bar':
        // Stacked, so on a multi-account panel each account is a segment and the
        // stack's height is the consolidated view — no total series needed, and
        // one would double the stack.
        return <Chart.Bar data={data.series.map((s) => s.values)} labels={data.labels} chartLabel={data.series.map((s) => s.label)} />;
      case 'timeseries':
      default: {
        // Each account's own lines, then the series that adds them up — dashed
        // and heavier, so the total reads as a different kind of line from the
        // parts it sums. Its colour is left to the chart, as every line's is:
        // naming one colour would switch off the automatic palette for the rest.
        const drawn = [...data.series, ...consolidatedSeries(data.series)];
        // Chart.Line = @shared/charts/LineCharts (chart.js).
        return (
          <Chart.Line
            dataset={drawn.map(lineDataset)}
            labels={data.labels}
            timestamps={data.timestamps}
            chartLabel={drawn.map((s) => s.label)}
            minHeight={CHART_HEIGHT}
            dynamicHeight={false}
            legendOptions={{ renderer: 'html', unit: panel.unit }}
          />
        );
      }
    }
  }, [panel, data, stat]);

  const body = () => {
    if (panel.type === 'text') {
      return (
        <Typography variant='body2' sx={{ whiteSpace: 'pre-wrap', color: ds.gray[600] }}>
          {panel.content || ''}
        </Typography>
      );
    }
    if (error) {
      return (
        <Box sx={{ height: '100%' }} data-testid={`panel-error-${panel.id}`}>
          <PanelState
            tone={ERROR_TONE[error.kind]}
            title={error.message}
            icon={error.kind === 'filter' ? <FilterAltOutlinedIcon sx={{ fontSize: 18 }} /> : undefined}
            action={errorAction(error.kind)}
          />
        </Box>
      );
    }
    if (loading || !data) {
      return <Skeleton height={CHART_HEIGHT} width='100%' />;
    }
    return drawing;
  };

  return (
    <Box
      ref={panelRef}
      data-testid={`dashboard-panel-${panel.id}`}
      data-loaded={panel.type === 'text' ? undefined : seen}
      // The crossed step's colour, for anything reading the rendered dashboard —
      // absent on a panel drawing normally, which is what "no breach" looks like.
      data-threshold={breach?.color}
      {...{ [PANEL_PENDING_ATTR]: String(pending) }}
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        border: `1px solid ${tone ? tone.border : ds.gray[300]}`,
        // A ring rather than a thicker border: panels sit in a grid, and growing
        // the border by a pixel would shift everything inside one the moment a
        // threshold trips. A shadow takes no space.
        ...(tone ? { boxShadow: `inset 0 0 0 1px ${tone.border}` } : {}),
        borderRadius: ds.radius.lg,
        background: ds.background[100],
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          px: 1.25,
          py: 1,
          borderBottom: `1px solid ${tone ? tone.border : ds.gray[200]}`,
          // The band is square-cornered; unclipped, its tint would paint over the
          // frame's rounded top corners.
          ...(tone ? { background: tone.tint, borderTopLeftRadius: ds.radius.lg, borderTopRightRadius: ds.radius.lg } : {}),
        }}
      >
        {/* The title's own tooltip is for a title clipped by a narrow panel, so
            it repeats the title rather than standing in for the description. */}
        <Tooltip title={panel.title}>
          <Typography sx={{ fontSize: 13, fontWeight: 620, color: ds.gray[700] }}>{panel.title}</Typography>
        </Tooltip>
        {/* A description hidden behind the title was undiscoverable — nothing
            distinguished a panel that has one from a panel that does not. The
            icon is the affordance; same treatment as the editor's Accounts
            field. */}
        {panel.description && (
          <Tooltip title={panel.description}>
            <Box
              component='span'
              sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}
              data-testid={`panel-description-${panel.id}`}
            >
              <InfoOutlinedIcon sx={{ fontSize: 13 }} />
            </Box>
          </Tooltip>
        )}
        {/* The dial's scale is a contract the panel cannot show on its own — the
            same affordance, in the same title-side spot, as the description icon. */}
        {panel.type === 'gauge' && (
          <Tooltip title='The dial runs 0 to 100 — the value is read as a percentage, and anything outside that range pins to the ends.'>
            <Box
              component='span'
              sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}
              data-testid={`panel-gauge-info-${panel.id}`}
            >
              <InfoOutlinedIcon sx={{ fontSize: 13 }} />
            </Box>
          </Tooltip>
        )}
        {/* Colour alone does not say WHAT was crossed, and a viewer who cannot
            tell amber from red is left with a panel that looks merely decorated.
            The badge names the step; the hover reads it back as a sentence. */}
        {breach && tone && (
          <Tooltip title={`${formatValue(stat?.total, panel.unit)} is at or above the ${breach.value} threshold.`}>
            <Box component='span' data-testid={`panel-threshold-${panel.id}`} sx={{ display: 'inline-flex', cursor: 'help' }}>
              <Chip size='2xs' tone={tone.chip}>
                {`≥ ${breach.value}`}
              </Chip>
            </Box>
          </Tooltip>
        )}
        {panel.type !== 'text' && (
          <Chip size='2xs' tone='subtle'>
            {panel.datasource}
          </Chip>
        )}
        {/* Each panel names its own accounts, so a dashboard can mix them.
            Showing the scope here is the only way to tell two otherwise
            identical panels apart. */}
        {panel.type !== 'text' && (
          <Tooltip title={narrowed ? `Scope: ${scopeLabel}` : ''}>
            <Box component='span' data-testid={`panel-scope-${panel.id}`} sx={{ display: 'inline-flex' }}>
              <Chip size='2xs' tone='neutral'>
                {shownLabel}
              </Chip>
            </Box>
          </Tooltip>
        )}
        <Box sx={{ flex: 1 }} />
        {/* A toolbar filter, not a form field: empty means "no filter applied"
            (DS §1.6). Hidden when the panel's own scope is one account. Shown
            even when the dashboard filter has narrowed it to one, so the
            viewer can see which account this panel is showing and why. */}
        {!editing && scopedAccounts.length > 1 && (
          <FilterDropdown
            id={`panel-account-filter-${panel.id}`}
            label='Account'
            size='sm'
            grouped
            // Panel headers clip their overflow, so the popover must portal.
            disablePortal={false}
            value={selectedOption}
            options={filterOptions}
            searchPlaceholder='Search accounts…'
            // Single-select hands back the option object, or null when cleared.
            onSelect={(_e: any, next: any) => setAccountId(next?.value ?? next ?? '')}
          />
        )}
        {/* ThreeDotsMenu only fires onMenuClick when `data` is set, and renders
            nothing at all for an empty item list. Excluded from an exported
            image — an affordance nobody can press reads as an artefact. */}
        {!editing && (
          <Box component='span' {...{ [EXPORT_HIDE_ATTR]: 'true' }} sx={{ display: 'inline-flex' }}>
            <ThreeDotsMenu
              id={`panel-menu-${panel.id}`}
              menuItems={menuItems}
              data={panel}
              onMenuClick={(item: any) => {
                if (item?.id === 'refresh') setPanelRefresh((n) => n + 1);
                if (item?.id === 'edit') onEdit?.();
                // The panel exactly as stored — it drops straight into another
                // dashboard's `panels` array, account scope and all.
                if (item?.id === 'export') downloadJsonFile(panel, `${filenameSlug(panel.title, 'panel')}-panel`);
                if (item?.id === 'export-png') exportPng();
              }}
            />
          </Box>
        )}
        {actions}
      </Box>
      <Box sx={{ p: 1.25, flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', gap: 0.5 }}>
        {/* Partial failure: some accounts answered, some did not. Saying which
            beats silently charting an incomplete picture. */}
        {warning && (
          <Typography variant='caption' sx={{ color: ds.amber[600] }} data-testid={`panel-warning-${panel.id}`}>
            {warning}
          </Typography>
        )}
        {/* `data-panel-body` is the hook edit mode uses to make the chart inert
            while the panel is being dragged or resized — a Chart.js canvas
            otherwise swallows the mousemove the gesture needs. */}
        <Box data-panel-body sx={{ flex: 1, minHeight: 0 }}>
          {body()}
        </Box>
      </Box>
    </Box>
  );
});

export default DashboardPanel;
