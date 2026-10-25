import React from 'react';
import { Box, Typography } from '@mui/material';
import FilterAltOutlinedIcon from '@mui/icons-material/FilterAltOutlined';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import LinkIcon from '@mui/icons-material/Link';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import Chart from '@ui/Chart';
import { formatStat } from '@shared/charts/legendChips';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Link } from '@ui/Link';
import { Modal } from '@ui/Modal';
import FilterDropdown from '@ui/FilterDropdown';
import { Skeleton } from '@ui/Skeleton';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import Tooltip from '@ui/Tooltip';
import { snackbar } from '@ui/Toast';
import { downloadIcon, EditIcon, RefreshIcon } from '@assets';
import { ds } from '@utils/colors';
import { downloadJsonFile, filenameSlug } from '@utils/fileDownload';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import Currency from '@shared/format/Currency';
import Memory from '@shared/format/Memory';
import NumberFormat from '@shared/format/Number';
import { formatDurationInTrace } from '@utils/common';
import type { AccountOption, Panel } from '@api1/dashboards';
import { addedColumns, columnSettings, panelColumnsOf, renderDataLinks, renderRowUrl } from './panelColumns';
// A drill-down menu opens at the clicked point — an arbitrary cursor position,
// not a trigger element — so it composes the overlay surface directly rather
// than DropdownMenu, which anchors to and opens from a trigger it clones.
import { OverlayItem, OverlaySurface } from '@ui/internal/Overlay';
import PanelAccountBreakdown, { BreakdownRows, breachedPlacement, breakdownPlacement } from './PanelAccountBreakdown';
import PanelGauge from './PanelGauge';
import {
  forOf,
  holdLabel,
  isLinePanel,
  lineBreaches,
  OP_PHRASE,
  OP_SYMBOL,
  opOf,
  panelBreaches,
  panelThresholdsOf,
  showsOneNumber,
  thresholdTone,
  type Breach,
  type LineBreaches,
  type PanelBreaches,
} from './panelThresholds';
import { thresholdChartPlugin, type ThresholdDrawing } from './panelThresholdChart';
import PanelState, { type PanelStateTone } from './PanelState';
import { usePanelData, type ColumnKind, type PanelData, type PanelErrorKind, type PanelSeries } from './usePanelData';
import { applyAccountFilter, describePanelScope, effectiveFilterAccount, panelQueryAccounts, resolvePanelAccounts } from './panelAccounts';
import { panelQueryWindow } from './dashboardRange';
import PanelColumnFilters, { PanelFilterMenu } from './PanelColumnFilters';
import { appliedViewerFilters, loadViewerFilterValues, viewerFilterColumns, type ViewerFilter } from './panelViewerFilters';
import { combineOf, consolidatedSeries, histogramBins, lastValue, metricLabel, statTotal } from './panelSeries';
import { downloadNodeAsPng, EXPORT_HIDE_ATTR, PANEL_PENDING_ATTR } from './panelImage';
import { withPanelParam } from './panelLink';
import type { VariableValues } from './templating';

/** Plot height, excluding the legend the chart renders beneath it. */
const CHART_HEIGHT = 160;

/** The same plot in the View modal, which has the room the grid cell does not. */
const VIEW_CHART_HEIGHT = 420;

/** One shared empty list, so an absent dashboard filter is a stable prop for the memoised panel. */
const NO_FILTER: string[] = [];

/** No viewer column filters — one shared list, for the same reason. */
const NO_VIEWER_FILTERS: ViewerFilter[] = [];

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

/** A series label short enough for a badge; the hover carries the whole thing. */
const BADGE_LABEL_CHARS = 28;
function shortLabel(label: string): string {
  return label.length > BADGE_LABEL_CHARS ? `${label.slice(0, BADGE_LABEL_CHARS - 1)}…` : label;
}

/** "10:42" — the local clock time of an epoch-ms instant, for the hover's "to 10:42". */
function clockTime(ms: number): string {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

/** "≥ 70", or "≥ 70 for 5m" for a step with a hold. */
function stepLabel(b: Breach): string {
  const hold = forOf(b.step);
  return `${OP_SYMBOL[opOf(b.step)]} ${b.step.value}${hold !== undefined ? ` for ${holdLabel(hold)}` : ''}`;
}

/**
 * The badge's text: the crossed line, and whose number crossed it when that is
 * not the one on the card — an account's figure on a stat, a series on a chart.
 */
function breachLabel(b: Breach): string {
  const line = stepLabel(b);
  if (b.line) return `${shortLabel(b.line)} ${line}`;
  return b.account ? `${b.account} ${line}` : line;
}

/**
 * The badge's hover: the same breach as a sentence, with the number that crossed.
 * A hold says which samples it rests on — the chart evaluates once, at the end
 * of the range it was opened on, so "for the 5 minutes to 10:42" is what is true.
 */
function breachSentence(b: Breach, unit?: string): string {
  const hold = forOf(b.step);
  if (b.line) {
    if (hold !== undefined && b.from !== undefined && b.to !== undefined) {
      return `${b.line}: every sample from ${clockTime(b.from)} to ${clockTime(b.to)} was ${OP_PHRASE[opOf(b.step)]} ${b.step.value} — ${holdLabel(
        hold
      )} or longer. Latest ${formatValue(b.value, unit)}.`;
    }
    return `${b.line} is at ${formatValue(b.value, unit)}, ${OP_PHRASE[opOf(b.step)]} the ${b.step.value} threshold.`;
  }
  const phrase = `${OP_PHRASE[opOf(b.step)]} the ${b.step.value} threshold.`;
  return b.account ? `${b.account} is at ${formatValue(b.value, unit)}, ${phrase}` : `${formatValue(b.value, unit)} is ${phrase}`;
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
  /*
   * The Account funnel's hover label. The list the funnel opens is portaled, and
   * React bubbles a portal's events through the component tree, not the DOM: its
   * search box taking focus, and every pointer move over its rows, reached this
   * tooltip as a hover on the trigger and re-opened it on top of the open list.
   * So the label opens only for an event that is really on the trigger, and a
   * click on it puts the label away.
   */
  const [accountTipOpen, setAccountTipOpen] = React.useState(false);
  const accountTrigger = React.useRef<HTMLSpanElement>(null);
  const scopedAccounts = React.useMemo(() => resolvePanelAccounts(panel, accounts), [panel, accounts]);
  const panelAccounts = React.useMemo(() => applyAccountFilter(scopedAccounts, dashboardAccountIds), [scopedAccounts, dashboardAccountIds]);
  const filterOptions = React.useMemo(
    () => panelAccounts.map((a) => ({ label: a.label, value: a.value, group: a.cloud_provider || 'Other' })),
    [panelAccounts]
  );
  // Nothing picked yet means the first account, not "no account": a panel that waits for a choice shows an
  // empty box, and one account costs the same single request whether it was chosen or defaulted to. A pick
  // the panel has since been re-scoped away from falls back to that same default — see the rule for why.
  // Metrics and `nudgebee` panels are the exception: they answer for every account at once — a chart merges
  // the accounts' series, a stat adds their numbers up, the query engine takes the whole list in one call — so
  // an unmade choice means ALL of them and the picker stays empty until the viewer narrows it. Unless "all of
  // them" is ONE account — the panel's scope, or what the dashboard filter left of it — in which case the
  // picker shows that account as the selection it effectively is.
  const answersEveryAccount = panel.datasource === 'metrics' || panel.datasource === 'nudgebee';
  const effectiveAccountId = effectiveFilterAccount(accountId, panelAccounts, answersEveryAccount && panelAccounts.length !== 1);
  const selectedOption = filterOptions.find((o) => o.value === effectiveAccountId) || null;
  // The hook takes a list so a panel scoped to one account still works without a
  // selection; the picker just never supplies more than one. With no pick, the
  // dashboard's filter is the list — and when that names no account of this
  // panel's, the hook reports the filter miss rather than querying nothing.
  const accountFilter = React.useMemo(
    () => (effectiveAccountId ? [effectiveAccountId] : dashboardAccountIds),
    [effectiveAccountId, dashboardAccountIds]
  );

  /*
   * Column filters, beside the Account one: what a viewer narrows a traces or
   * `nudgebee` panel to — this workload, that namespace. Panel state like the
   * Account pick, never saved; see panelViewerFilters.ts.
   */
  const filterColumns = React.useMemo(() => viewerFilterColumns(panel), [panel]);
  const [viewerFilters, setViewerFilters] = React.useState<ViewerFilter[]>(NO_VIEWER_FILTERS);
  // The column added last, so its dropdown opens as it appears.
  const [openColumn, setOpenColumn] = React.useState('');
  // A filter on a column the panel no longer has — it was edited onto another
  // table — would narrow by a column the query cannot name.
  const activeFilters = React.useMemo(
    () => (viewerFilters.length === 0 ? NO_VIEWER_FILTERS : viewerFilters.filter((f) => filterColumns.some((c) => c.name === f.column))),
    [viewerFilters, filterColumns]
  );
  const filtering = appliedViewerFilters(activeFilters).length > 0;
  const addViewerFilter = React.useCallback((column: string) => {
    // A column added and left empty does nothing; the next pick tidies it away.
    setViewerFilters((current) => [...current.filter((f) => f.values.length > 0), { column, values: [] }]);
    setOpenColumn(column);
  }, []);
  // The accounts the values are listed for are the ones the panel is querying —
  // its first account on a traces panel, all of them on a `nudgebee` one.
  const queriedAccountIds = React.useMemo(
    () => panelQueryAccounts(scopedAccounts, accountFilter, panel.datasource === 'nudgebee').accounts.map((a) => a.value),
    [scopedAccounts, accountFilter, panel.datasource]
  );
  // What this panel's queries run over: the dashboard's range, or the table's
  // minimum window where that is longer — an hour of a table written once a
  // day is empty. See panelQueryWindow.
  const queryWindow = React.useMemo(() => panelQueryWindow(panel, startTime, endTime), [panel, startTime, endTime]);
  const loadFilterValues = React.useCallback(
    (column: string) =>
      loadViewerFilterValues({
        panel,
        column,
        accountIds: queriedAccountIds,
        variables,
        startTime: queryWindow.startTime,
        endTime: queryWindow.endTime,
      }),
    [panel, queriedAccountIds, variables, queryWindow]
  );
  // A value list belongs to the accounts and the window it was read for.
  const filterScopeKey = `${queriedAccountIds.join(',')}|${queryWindow.startTime}|${queryWindow.endTime}`;

  // Composite rather than a sum: either counter moving changes the key, and no
  // pair of values can collide the way `dashboard + panel` could.
  const [panelRefresh, setPanelRefresh] = React.useState(0);
  // The View modal draws from this panel's own data rather than mounting a second panel, which would run the
  // query again.
  const [viewing, setViewing] = React.useState(false);
  const refreshKey = `${refreshToken}:${panelRefresh}`;

  // A text panel has nothing to fetch, so Refresh would be a no-op on it — but it is still editable, so the
  // menu itself is not conditional on the type.
  const menuItems = React.useMemo(() => {
    const items: { id: string; label: string; icon?: unknown; reactIcon?: React.ReactNode }[] = [];
    items.push({ id: 'view', label: 'View', reactIcon: <VisibilityOutlinedIcon sx={{ fontSize: 18 }} /> });
    if (panel.type !== 'text') items.push({ id: 'refresh', label: 'Refresh', icon: RefreshIcon });
    if (onEdit) items.push({ id: 'edit', label: 'Edit', icon: EditIcon });
    items.push({ id: 'export', label: 'Export JSON', icon: downloadIcon });
    if (panel.type !== 'text') items.push({ id: 'export-png', label: 'Export PNG', icon: downloadIcon });
    items.push({ id: 'copy-link', label: 'Copy link', reactIcon: <LinkIcon sx={{ fontSize: 18 }} /> });
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

  /** The page's own address, naming this panel — opening it scrolls straight here. */
  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(withPanelParam(window.location.href, String(panel.id)));
      snackbar.success('Link to this panel copied.');
    } catch {
      snackbar.error('Could not copy the link.');
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
    viewerFilters: activeFilters,
    variables,
    startTime: queryWindow.startTime,
    endTime: queryWindow.endTime,
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
  // A sample was never queried, so it has no window to speak of.
  const widened = sampleData ? undefined : queryWindow.widened;

  // Drill-down links: a point click opens a menu of the panel's configured links
  // with the clicked series' labels, account and the panel's range filled in.
  const dataLinks = panel.options?.data_links;
  const hasDataLinks = Array.isArray(dataLinks) && dataLinks.length > 0;
  const [pointLinks, setPointLinks] = React.useState<{ title: string; href: string }[]>([]);
  // An invisible, cursor-positioned anchor the menu hangs off, so it opens at the
  // clicked point without the shared chart component having to own the overlay.
  const pointAnchorRef = React.useRef<HTMLSpanElement | null>(null);
  const handlePointClick = React.useCallback(
    (info: { datasetIndex?: number; epochTimestamp?: number | null; clientX?: number; clientY?: number }) => {
      if (!data || !hasDataLinks) return;
      // `datasetIndex` indexes the same `drawn` list the chart was given — each
      // account's lines, then the consolidated one, which carries no account and
      // so resolves no account-dependent link.
      const drawn = [...data.series, ...consolidatedSeries(data.series)];
      const series = typeof info.datasetIndex === 'number' ? drawn[info.datasetIndex] : undefined;
      const links = renderDataLinks(dataLinks, {
        metric: series?.metric,
        accountId: series?.accountId,
        from: String(queryWindow.startTime),
        to: String(queryWindow.endTime),
        time: info.epochTimestamp != null ? String(info.epochTimestamp) : undefined,
      });
      if (!links.length) return;
      if (pointAnchorRef.current && info.clientX != null && info.clientY != null) {
        pointAnchorRef.current.style.left = `${info.clientX}px`;
        pointAnchorRef.current.style.top = `${info.clientY}px`;
      }
      setPointLinks(links);
    },
    [data, hasDataLinks, dataLinks, queryWindow]
  );

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
  const shownAccounts = React.useMemo(
    () => (effectiveAccountId ? panelAccounts.filter((a) => a.value === effectiveAccountId) : panelAccounts),
    [effectiveAccountId, panelAccounts]
  );
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
   * added up or averaged (see combineOf / statTotal). Computed here rather than inside `drawing` because the threshold
   * tint colours the FRAME — the border and the header band, both outside the
   * body `drawing` returns.
   *
   * Null for every other panel, and for a command datasource's table, which
   * comes back as rows whatever the panel type says.
   */
  const stat = React.useMemo(() => {
    if (!data || data.table || !showsOneNumber(panel.type) || data.series.length === 0) return null;
    return statTotal(
      data.series,
      (panel.targets || []).map((t) => t.ref_id || 'A'),
      data.failedAccounts || [],
      data.emptyAccounts || [],
      combineOf(panel)
    );
  }, [panel, data]);

  /**
   * Every threshold this panel has crossed — on the number it shows, or on one
   * of its accounts' — and the one the frame is drawn for. A panel with no
   * thresholds, which is every panel authored before them, resolves to nothing
   * and keeps its plain frame.
   */
  const breaches = React.useMemo<PanelBreaches | LineBreaches>(
    () =>
      isLinePanel(panel.type)
        ? lineBreaches(panel, data?.series || [], data?.timestamps, shownAccounts, { start: startTime, end: endTime })
        : panelBreaches(panel, stat, shownAccounts),
    [panel, data, stat, shownAccounts, startTime, endTime]
  );
  /** A chart's thresholds have more to say than a stat's: where the holds were met, and what could not be judged. */
  const lineVerdict = 'spans' in breaches ? breaches : undefined;
  /**
   * What the chart plugin draws. A ref, read at draw time: chart.js receives its
   * plugins once, at creation, so a plugin built per render would keep drawing
   * the first render's steps after an edit in the preview.
   */
  const thresholdDrawing = React.useRef<ThresholdDrawing>({ steps: [], spans: [] });
  thresholdDrawing.current = { steps: isLinePanel(panel.type) ? panelThresholdsOf(panel) : [], spans: lineVerdict?.spans || [] };
  const thresholdPlugin = React.useMemo(() => thresholdChartPlugin(() => thresholdDrawing.current), []);
  const breach = breaches.frame?.step;
  const tone = breach ? thresholdTone(breach.color) : undefined;

  /*
   * The drawing, rebuilt only when the data or the panel changes. The chart
   * props below are new arrays each time this runs, and react-chartjs-2
   * updates the chart whenever their identity changes — so a rebuild on an
   * unrelated render (Edit pressed, the header's account filter opening) is a
   * full chart update and legend rebuild, on every panel at once.
   */
  const renderDrawing = React.useCallback(
    (chartHeight: number, { expanded = false }: { expanded?: boolean } = {}) => {
      if (panel.type === 'text' || !data) return null;
      // A command datasource answers with a table, whatever the panel type says.
      if (data.table) {
        if (data.table.rows.length === 0) {
          return (
            <Box sx={{ height: '100%' }}>
              {filtering ? (
                // The viewer's own filters emptied it, so the way out is theirs to take.
                <PanelState
                  tone='empty'
                  icon={<FilterAltOutlinedIcon sx={{ fontSize: 18 }} />}
                  title='No rows match these filters'
                  description='Nothing in this range has every value picked above.'
                  action={{ label: 'Clear filters', onClick: () => setViewerFilters(NO_VIEWER_FILTERS) }}
                />
              ) : (
                <PanelState tone='empty' title='Nothing came back' description='The query ran and returned no rows.' />
              )}
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
          // one cluster's figure. The breakdown behind it is on the card when the
          // panel has room for every account, and on hover always — a total nobody
          // can take apart is a number nobody can check.
          //
          // Computed above the memo: the same total decides the threshold tint on
          // the frame. It is non-null on exactly the branch this is, so the guard
          // is for the type checker rather than for a case that happens.
          if (!stat) return null;
          const reporting = stat.rows.filter((r) => r.value !== undefined).length;
          // Only when the accounts are not listed: a list already says how many
          // there are and which ones reported. Without one, this is what says the
          // number is a sum — "2 of 5 reporting" when some failed or came back
          // empty — or an average, which a reader cannot tell from a sum without
          // being told — and that the hover has the parts.
          const count = stat.partial ? `${reporting} of ${stat.rows.length} reporting` : `${stat.rows.length} accounts`;
          // Nothing reported is nothing averaged: "Average of 0 of 5 reporting" names a number that is not there.
          const countCaption = stat.combine === 'avg' && reporting > 0 ? `Average of ${count}` : count;
          const format = (value: number | undefined) => formatValue(value, panel.unit);
          const flagged = breaches.rows;
          // Accounts over a threshold first, each group in account order: the one
          // in trouble is the first read, and a row only moves when it crosses a line.
          const rows =
            flagged.size === 0
              ? stat.rows
              : [...stat.rows.filter((r) => flagged.has(r.account)), ...stat.rows.filter((r) => !flagged.has(r.account))];
          const breakdown = stat.rows.length > 1 && (
            <Box sx={{ display: 'grid', py: ds.space[0] }}>
              <BreakdownRows rows={rows} format={format} flagged={flagged} />
            </Box>
          );
          // The View modal has the room the grid cell does not, so it lists every account.
          const placement = expanded ? (stat.rows.length > 1 ? 'under' : null) : breakdownPlacement(panel, stat.rows.length);
          const listed = placement !== null;
          // A card that cannot list every account still lists the ones over a
          // threshold: they are the rows the list exists for.
          const shownRows = listed ? rows : rows.filter((r) => flagged.has(r.account));
          const shownPlacement = listed ? placement : stat.rows.length > 1 ? breachedPlacement(panel, shownRows.length) : null;
          const breakdownList = shownPlacement && (
            <PanelAccountBreakdown
              rows={shownRows}
              placement={shownPlacement}
              format={format}
              flagged={flagged}
              testId={`panel-breakdown-${panel.id}`}
            />
          );
          // The dial is a stat with a bounded scale, and takes the same total.
          if (panel.type === 'gauge') {
            return (
              <Box data-testid={`panel-gauge-${panel.id}`} sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
                <Tooltip title={breakdown || ''}>
                  <Box sx={{ flex: 1, minHeight: 0 }}>
                    <PanelGauge
                      value={stat.total}
                      caption={stat.rows.length > 1 && !listed ? countCaption : stat.caption}
                      // Rows under the dial come out of the dial's own height; a
                      // single-account gauge keeps the full-size dial.
                      rowsBelow={shownPlacement && !expanded ? shownRows.length : 0}
                    />
                  </Box>
                </Tooltip>
                {breakdownList}
              </Box>
            );
          }
          return (
            <Box
              data-testid={`panel-stat-${panel.id}`}
              sx={shownPlacement === 'beside' ? { display: 'flex', alignItems: 'flex-start', gap: ds.space[4] } : undefined}
            >
              <Box sx={{ flexShrink: 0 }}>
                <Tooltip title={breakdown || ''}>
                  <Typography
                    component='div'
                    sx={{ fontSize: 28, fontWeight: 650, letterSpacing: '-0.02em', color: ds.gray[700], width: 'fit-content' }}
                  >
                    {formatValue(stat.total, panel.unit)}
                  </Typography>
                </Tooltip>
                {stat.caption && (
                  <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                    {stat.caption}
                  </Typography>
                )}
                {stat.rows.length > 1 && !listed && (
                  <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block' }}>
                    {countCaption}
                  </Typography>
                )}
              </Box>
              {breakdownList}
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
        case 'histogram': {
          // The distribution of every value the query reported over the range —
          // one bar per equal-width bucket, counting the samples that fell in it.
          // Reuses the bar renderer; the x-axis is value buckets, not time.
          const hist = histogramBins(data.series);
          if (!hist) {
            return (
              <Box sx={{ height: '100%' }}>
                <PanelState tone='empty' title='Nothing to chart' description='A histogram needs numeric values — check the query.' />
              </Box>
            );
          }
          const binLabels = hist.bins.map((b) => formatStat(b.lo, panel.unit));
          const counts = hist.bins.map((b) => b.count);
          return (
            <Chart.Bar
              dataset={[{ label: 'Count', data: counts, backgroundColor: ds.blue[500], categoryPercentage: 1, barPercentage: 1, borderRadius: 2 }]}
              labels={binLabels}
              maxHeight={chartHeight}
              options={{
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                  legend: { display: false },
                  tooltip: {
                    callbacks: {
                      // The bar sits at its bucket's lower edge; name the whole range.
                      title: (items: { dataIndex: number }[]) => {
                        const b = hist.bins[items[0].dataIndex];
                        return `${formatStat(b.lo, panel.unit)} – ${formatStat(b.hi, panel.unit)}`;
                      },
                      label: (item: { raw: number }) => `${item.raw} ${item.raw === 1 ? 'sample' : 'samples'}`,
                    },
                  },
                },
                scales: {
                  // Buckets can be many; let the axis thin its own ticks rather than
                  // stack thirty labels on top of each other.
                  x: { ticks: { autoSkip: true, maxRotation: 0 } },
                  y: { beginAtZero: true, ticks: { precision: 0 } },
                },
              }}
            />
          );
        }
        case 'timeseries':
        default: {
          // Each account's own lines, then the series that adds them up — dashed
          // and heavier, so the total reads as a different kind of line from the
          // parts it sums. Its colour is left to the chart, as every line's is:
          // naming one colour would switch off the automatic palette for the rest.
          const drawn = [...data.series, ...consolidatedSeries(data.series)];
          // The threshold lines and hold bands; a no-op on a panel without steps.
          // Spans index `data.series`, which `drawn` starts with, so they line up.
          // Chart.Line = @shared/charts/LineCharts (chart.js).
          return (
            <Chart.Line
              dataset={drawn.map(lineDataset)}
              labels={data.labels}
              timestamps={data.timestamps}
              chartLabel={drawn.map((s) => s.label)}
              minHeight={chartHeight}
              dynamicHeight={false}
              legendOptions={{ renderer: 'html', unit: panel.unit }}
              customPlugins={[thresholdPlugin]}
              onDataPointClick={hasDataLinks ? handlePointClick : undefined}
            />
          );
        }
      }
    },
    [panel, data, stat, breaches, thresholdPlugin, filtering, hasDataLinks, handlePointClick]
  );
  const drawing = React.useMemo(() => renderDrawing(CHART_HEIGHT), [renderDrawing]);
  // Built only while the modal is open, so a closed one costs no second chart.
  const viewDrawing = React.useMemo(() => (viewing ? renderDrawing(VIEW_CHART_HEIGHT, { expanded: true }) : null), [viewing, renderDrawing]);

  const body = (content: React.ReactNode = drawing, chartHeight = CHART_HEIGHT) => {
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
            description={error.detail}
            icon={error.kind === 'filter' ? <FilterAltOutlinedIcon sx={{ fontSize: 18 }} /> : undefined}
            action={errorAction(error.kind)}
          />
        </Box>
      );
    }
    if (loading || !data) {
      return <Skeleton height={chartHeight} width='100%' />;
    }
    return content;
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
          <Typography noWrap sx={{ fontSize: 13, fontWeight: 620, color: ds.gray[700], minWidth: 0 }} data-testid={`panel-title-${panel.id}`}>
            {panel.title}
          </Typography>
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
          <Tooltip title='The dial runs 0 to 100 — the value is read as a percentage, and anything outside that range pins to the ends. Over several accounts it shows their average unless the panel says otherwise.'>
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
        {breaches.frame && tone && (
          <Tooltip
            title={
              <Box sx={{ display: 'grid', gap: ds.space[0] }}>
                {breaches.all.map((b, i) => (
                  // Positional: the list is derived fresh from the data and never reordered in place.
                  <span key={i}>{breachSentence(b, panel.unit)}</span>
                ))}
                {/* A line the chart never drew is a line it never judged. */}
                {lineVerdict && data?.capped && (
                  <span>
                    Judged on the {data.capped.shown} lines shown, of {data.capped.total}.
                  </span>
                )}
              </Box>
            }
          >
            <Box component='span' data-testid={`panel-threshold-${panel.id}`} sx={{ display: 'inline-flex', cursor: 'help' }}>
              <Chip size='2xs' tone={tone.chip}>
                {breachLabel(breaches.frame)}
                {breaches.all.length > 1 ? ` +${breaches.all.length - 1}` : ''}
              </Chip>
            </Box>
          </Tooltip>
        )}
        {/* A hold the chart cannot judge at this range is said, not skipped: a
            plain frame would read as "nothing crossed" when nothing was checked. */}
        {lineVerdict && lineVerdict.tooCoarse.length > 0 && (
          <Tooltip
            title={`At this range the chart has fewer than two samples inside a ${holdLabel(
              Math.min(...lineVerdict.tooCoarse.map((step) => forOf(step) || 0))
            )} hold, so that threshold cannot be judged. Pick a shorter time range.`}
          >
            <Box component='span' data-testid={`panel-threshold-coarse-${panel.id}`} sx={{ display: 'inline-flex', cursor: 'help' }}>
              <Chip size='2xs' tone='subtle'>
                Zoom in to judge {holdLabel(Math.min(...lineVerdict.tooCoarse.map((step) => forOf(step) || 0)))} hold
              </Chip>
            </Box>
          </Tooltip>
        )}
        {lineVerdict && lineVerdict.tooCoarse.length === 0 && lineVerdict.tooLong.length > 0 && (
          <Tooltip
            title={`This time range is too short to contain a whole ${holdLabel(
              Math.max(...lineVerdict.tooLong.map((step) => forOf(step) || 0))
            )} hold, so that threshold cannot be judged. Pick a longer time range.`}
          >
            <Box component='span' data-testid={`panel-threshold-short-${panel.id}`} sx={{ display: 'inline-flex', cursor: 'help' }}>
              <Chip size='2xs' tone='subtle'>
                Widen range to judge {holdLabel(Math.max(...lineVerdict.tooLong.map((step) => forOf(step) || 0)))} hold
              </Chip>
            </Box>
          </Tooltip>
        )}
        {/* No scope chip: the header is the title's, and the account a panel
            is showing is the filter button's state. */}
        {panel.type !== 'text' && (
          <Chip size='2xs' tone='subtle'>
            {panel.datasource}
          </Chip>
        )}
        {/* A panel reading a longer window than the toolbar names has to say so:
            a week of spend under "Last 1 Hour" would be read as an hour's. */}
        {widened && (
          <Tooltip
            title={`${widened.table} is recorded once a day, so a shorter range would show little or nothing. This panel shows the ${widened.days} days up to the end of the selected range.`}
          >
            <Box component='span' data-testid={`panel-window-${panel.id}`} sx={{ display: 'inline-flex', cursor: 'help' }}>
              <Chip size='2xs' tone='subtle'>
                Showing {widened.days} days
              </Chip>
            </Box>
          </Tooltip>
        )}
        <Box sx={{ flex: 1 }} />
        {!editing && (
          <PanelFilterMenu
            panelId={panel.id}
            columns={filterColumns.filter((c) => !activeFilters.some((f) => f.column === c.name))}
            onAdd={addViewerFilter}
          />
        )}
        {/* A toolbar filter, not a form field: empty means "no filter applied"
            (DS §1.6). Hidden when the panel's own scope is one account. Shown
            even when the dashboard filter has narrowed it to one, so the
            viewer can see which account this panel is showing and why. */}
        {!editing && scopedAccounts.length > 1 && (
          // An icon, not a labelled dropdown: a 150px trigger crowded the title
          // out of a quarter-row panel. The pick shows as the button's blue
          // state, and its tooltip names the account.
          <Tooltip
            title={selectedOption ? `Showing ${selectedOption.label}` : 'Filter by account'}
            open={accountTipOpen}
            onOpen={(event: React.SyntheticEvent) => {
              if (accountTrigger.current?.contains(event.target as Node)) setAccountTipOpen(true);
            }}
            onClose={() => setAccountTipOpen(false)}
          >
            <Box
              ref={accountTrigger}
              component='span'
              {...{ [EXPORT_HIDE_ATTR]: 'true' }}
              onClickCapture={() => setAccountTipOpen(false)}
              sx={{ display: 'inline-flex', flexShrink: 0 }}
            >
              <FilterDropdown
                id={`panel-account-filter-${panel.id}`}
                label='Filter by account'
                icon={<FilterAltOutlinedIcon />}
                grouped
                // Panel headers clip their overflow, so the popover must portal.
                disablePortal={false}
                popoverAlign='right'
                value={selectedOption}
                options={filterOptions}
                searchPlaceholder='Search accounts…'
                // Single-select hands back the option object, or null when cleared.
                onSelect={(_e: any, next: any) => setAccountId(next?.value ?? next ?? '')}
              />
            </Box>
          </Tooltip>
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
                if (item?.id === 'view') setViewing(true);
                if (item?.id === 'refresh') setPanelRefresh((n) => n + 1);
                if (item?.id === 'edit') onEdit?.();
                // The panel exactly as stored — it drops straight into another
                // dashboard's `panels` array, account scope and all.
                if (item?.id === 'export') downloadJsonFile(panel, `${filenameSlug(panel.title, 'panel')}-panel`);
                if (item?.id === 'export-png') exportPng();
                if (item?.id === 'copy-link') copyLink();
              }}
            />
          </Box>
        )}
        {actions}
      </Box>
      {!editing && (
        <PanelColumnFilters
          panelId={panel.id}
          columns={filterColumns}
          filters={activeFilters}
          onChange={setViewerFilters}
          loadValues={loadFilterValues}
          scopeKey={filterScopeKey}
          openColumn={openColumn}
        />
      )}
      <Box sx={{ p: 1.25, flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', gap: 0.5 }}>
        {/* `data-panel-body` is the hook edit mode uses to make the chart inert
            while the panel is being dragged or resized — a Chart.js canvas
            otherwise swallows the mousemove the gesture needs. */}
        <Box data-panel-body sx={{ flex: 1, minHeight: 0 }}>
          {body()}
        </Box>
        {/* Partial failure: some accounts answered, some did not. Saying which
            beats silently charting an incomplete picture — at the foot of the
            panel, so the chart keeps its place at the top. */}
        {warning && (
          <Box data-testid={`panel-warning-${panel.id}`} sx={{ flexShrink: 0 }}>
            <Banner tone='warning' surface='section' message={warning} />
          </Box>
        )}
      </Box>
      {/* The whole panel with the room to show it: the grid cell's fixed height is what clips a long table
          or a busy legend. Nothing here is cut short — rows wrap rather than ellipsise, and every row is
          reachable through the table's pager. */}
      <Modal open={viewing} handleClose={() => setViewing(false)} width='xl' title={panel.title} subtitle={panel.description || undefined}>
        <Box data-testid={`panel-view-${panel.id}`} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          {panel.type !== 'text' && (
            <Box sx={{ display: 'flex', gap: ds.space[1] }}>
              <Chip size='2xs' tone='subtle'>
                {panel.datasource}
              </Chip>
              <Chip size='2xs' tone='neutral'>
                {shownLabel}
              </Chip>
              {widened && (
                <Chip size='2xs' tone='neutral'>
                  {`Showing ${widened.days} days`}
                </Chip>
              )}
              {/* The modal draws the filtered answer, so it says what it was filtered by. */}
              {appliedViewerFilters(activeFilters).map((f) => (
                <Chip key={f.column} size='2xs' tone='neutral'>
                  {`${filterColumns.find((c) => c.name === f.column)?.label || f.column}: ${f.values.join(', ')}`}
                </Chip>
              ))}
            </Box>
          )}
          {warning && (
            <Typography variant='caption' sx={{ color: ds.amber[600] }}>
              {warning}
            </Typography>
          )}
          {body(viewDrawing, VIEW_CHART_HEIGHT)}
        </Box>
      </Modal>
      {/* Drill-down menu, opened at the clicked point (the anchor is moved there).
          Links open in a new tab, like a table row's link — following in place
          would lose the dashboard you were watching, its range and its filter. */}
      <span ref={pointAnchorRef} aria-hidden style={{ position: 'fixed', width: 0, height: 0 }} />
      <OverlaySurface
        anchorEl={pointAnchorRef.current}
        open={pointLinks.length > 0}
        onClose={() => setPointLinks([])}
        role='menu'
        minWidth={180}
        disablePortal={false}
      >
        {pointLinks.map((link, i) => (
          <OverlayItem
            key={`${link.href}-${i}`}
            size='sm'
            id={`panel-${panel.id}-datalink-${i}`}
            onClick={() => {
              window.open(link.href, '_blank', 'noopener');
              setPointLinks([]);
            }}
          >
            {link.title}
          </OverlayItem>
        ))}
      </OverlaySurface>
    </Box>
  );
});

export default DashboardPanel;
