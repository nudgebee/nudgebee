import React, { useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import { Input } from '@ui/Input';
import { ListingLayout } from '@ui/ListingLayout';
import { snackbar } from '@ui/Toast';
import Tooltip from '@ui/Tooltip';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import SafeIcon from '@shared/icons/SafeIcon';
import { downloadIcon, RefreshIcon, writeIconLight } from '@assets';
import { ds } from '@utils/colors';
import apiDashboards, { EMPTY_DEFINITION, type AccountOption, type Dashboard, type Panel } from '@api1/dashboards';
import DashboardPanel from './DashboardPanel';
import { downloadNodeAsPng, waitForPanels } from './panelImage';
import PanelEditorModal from './PanelEditorModal';
import PanelLibraryModal from './PanelLibraryModal';
import { blankPanel } from './panelDefaults';
import type { VariableValues } from './templating';

interface Props {
  dashboard: Dashboard;
  /** Every account the viewer can see; each panel's scope resolves against it. */
  accounts: AccountOption[];
  /** Values injected by the host page (namespace / workload / pod on a detail page). */
  context?: VariableValues;
  onBack?: () => void;
  /** Hands back the saved dashboard after an in-place edit, so the host restates it. */
  onChange?: (saved: Dashboard) => void;
  canEdit?: boolean;
}

const HOUR_MS = 60 * 60 * 1000;

const DashboardView: React.FC<Props> = ({ dashboard, accounts, context, onBack, onChange, canEdit }) => {
  // Opens on the last hour: a dashboard is read live, and an hour keeps the
  // per-panel provider queries cheap enough to fan out across accounts.
  // `shortcutClickTime` is what makes the picker read "Last 1 Hour" rather than
  // a date range, and is carried back on every change so the label stays right.
  const [range, setRange] = useState(() => {
    const now = Date.now();
    return { startDate: now - HOUR_MS, endDate: now, shortcutClickTime: HOUR_MS };
  });

  const [refreshToken, setRefreshToken] = useState(0);

  /**
   * Exporting the whole dashboard as an image.
   *
   * `capturing` overrides every panel's scroll gate — panels below the fold
   * have deliberately not fetched, and would rasterise as skeletons. It stays
   * on after the capture: having paid for those queries, throwing the results
   * away so the panels reload on scroll would be worse than keeping them.
   */
  const gridRef = React.useRef<HTMLDivElement | null>(null);
  const [capturing, setCapturing] = useState(false);
  const [exporting, setExporting] = useState(false);

  const exportPng = async () => {
    if (!gridRef.current) return;
    setExporting(true);
    setCapturing(true);
    try {
      // One frame for the panels to re-render with their gate lifted, so the
      // pending markers exist before they are counted.
      await new Promise((resolve) => requestAnimationFrame(() => resolve(null)));
      const settled = await waitForPanels(gridRef.current);
      if (!settled) {
        // Export anyway: fourteen panels and one spinner beats no file at all,
        // as long as the viewer knows which they got.
        snackbar.warning('Some panels were still loading — they appear empty in the image.');
      }
      await downloadNodeAsPng(gridRef.current, dashboard.title, 'dashboard');
    } catch (err) {
      console.error('dashboard export failed', err);
      snackbar.error('Could not export this dashboard as an image.');
    } finally {
      setExporting(false);
    }
  };

  /**
   * Refreshes every panel.
   *
   * A relative range ("Last 1 Hour") has to be re-evaluated against now —
   * refetching the same fixed window would return the same data and read as a
   * broken button. An absolute range is left alone and only the token moves.
   */
  const refreshAll = () => {
    setRange((prev) => (prev.shortcutClickTime > 0 ? { ...prev, startDate: Date.now() - prev.shortcutClickTime, endDate: Date.now() } : prev));
    setRefreshToken((n) => n + 1);
  };

  // Substitution values come only from the host page — dashboards declare no
  // variables of their own.
  const variables = useMemo(() => context || {}, [context]);

  // Account narrowing is per PANEL, not per dashboard — each panel is scoped to
  // its own accounts, so a shared filter would offer accounts most panels never
  // query. The control lives in DashboardPanel's header.
  const panels = dashboard.definition?.panels || [];

  /*
   * Editing happens HERE rather than on a separate editor screen: a panel is
   * authored against what the dashboard is currently showing, and bouncing to a
   * form-only page loses that. Every edit is written straight through to the
   * dashboard, so there is no unsaved-changes state to lose on a reload.
   */
  const [editingPanel, setEditingPanel] = useState<Panel | null>(null);
  const [panelModalOpen, setPanelModalOpen] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [titleDraft, setTitleDraft] = useState('');

  /**
   * Writes the dashboard back.
   *
   * `bindings` is deliberately omitted rather than sent empty: the service
   * replaces bindings wholesale when the field is present, so sending [] from a
   * screen that does not edit them would silently delete any that exist. The
   * rest of `definition` is carried through for the same reason.
   */
  const persist = async (change: { title?: string; panels?: Panel[] }): Promise<boolean> => {
    setSaving(true);
    try {
      const res = await apiDashboards.saveDashboard({
        id: dashboard.id,
        title: change.title ?? dashboard.title,
        description: dashboard.description,
        definition: { ...EMPTY_DEFINITION, ...(dashboard.definition || {}), panels: change.panels ?? panels },
      });

      // The gateway reports handler failures in `errors`, not by throwing, so a
      // success toast keyed only on the absence of an exception would lie.
      if (res.errors || !res.data) {
        const message = Array.isArray(res.errors) ? (res.errors[0] as any)?.message : null;
        snackbar.error(message || 'Could not save the dashboard.');
        return false;
      }
      onChange?.(res.data);
      return true;
    } finally {
      // Guarantees the toolbar and every panel control unfreeze on both exits.
      setSaving(false);
    }
  };

  const openNewPanel = () => {
    setEditingPanel(blankPanel(panels));
    setPanelModalOpen(true);
  };

  /**
   * A library widget opens in the panel editor rather than going straight onto
   * the dashboard: it carries a query but no account, and the account is the one
   * field only the author can fill in.
   */
  const openLibraryPanel = (panel: Panel) => {
    setLibraryOpen(false);
    setEditingPanel(panel);
    setPanelModalOpen(true);
  };

  const savePanel = async (panel: Panel) => {
    const isNew = !panels.some((p) => p.id === panel.id);
    const next = isNew ? [...panels, panel] : panels.map((p) => (p.id === panel.id ? panel : p));
    if (!(await persist({ panels: next }))) return;
    snackbar.success(isNew ? 'Panel added' : 'Panel updated');
    setPanelModalOpen(false);
    setEditingPanel(null);
  };

  const startRename = () => {
    setTitleDraft(dashboard.title);
    setRenaming(true);
  };

  const commitRename = async () => {
    const title = titleDraft.trim();
    if (!title) {
      snackbar.error('Give the dashboard a title.');
      return;
    }
    // Nothing to write — closing beats a "saved" toast for a no-op.
    if (title === dashboard.title) {
      setRenaming(false);
      return;
    }
    if (!(await persist({ title }))) return;
    snackbar.success('Dashboard renamed');
    setRenaming(false);
  };

  return (
    <ListingLayout id='dashboard-view'>
      <ListingLayout.Toolbar
        actions={
          // Time range sits before Add panel: it changes what you are looking
          // at, and the action that adds to the page goes last.
          <Stack direction='row' gap={1} alignItems='center'>
            <CustomDateTimeRangePicker
              onChange={(ranges: any) =>
                setRange({
                  startDate: ranges.selection.startTime,
                  endDate: ranges.selection.endTime,
                  shortcutClickTime: ranges.selection.shortcutClickTime || 0,
                })
              }
              passedSelectedDateTime={{ startTime: range.startDate, endTime: range.endDate, shortcutClickTime: range.shortcutClickTime }}
            />
            <Tooltip title='Refresh all panels'>
              <Button
                tone='secondary'
                composition='icon-only'
                aria-label='Refresh dashboard'
                icon={<SafeIcon src={RefreshIcon} alt='refresh' width={16} height={16} />}
                onClick={refreshAll}
                id='dashboard-refresh-btn'
                data-testid='dashboard-refresh-btn'
              />
            </Tooltip>
            {/* Loads every panel first, including the ones below the fold, so
                the image is the whole dashboard rather than the part scrolled
                past. */}
            <Tooltip title='Export the whole dashboard as a PNG'>
              <Button
                tone='secondary'
                composition='icon-only'
                aria-label='Export dashboard as image'
                icon={<SafeIcon src={downloadIcon} alt='export' width={16} height={16} />}
                onClick={exportPng}
                loading={exporting}
                disabled={exporting || panels.length === 0}
                id='dashboard-export-png-btn'
                data-testid='dashboard-export-png-btn'
              />
            </Tooltip>
            {canEdit && (
              <>
                <Button
                  tone='secondary'
                  onClick={() => setLibraryOpen(true)}
                  disabled={saving}
                  id='dashboard-add-from-library-btn'
                  data-testid='dashboard-add-from-library-btn'
                >
                  Add from library
                </Button>
                <Button onClick={openNewPanel} disabled={saving} id='dashboard-add-panel-btn' data-testid='dashboard-add-panel-btn'>
                  Add panel
                </Button>
              </>
            )}
          </Stack>
        }
      >
        {onBack && (
          <Button tone='secondary' onClick={onBack} id='dashboard-back-btn'>
            Back
          </Button>
        )}
        {renaming ? (
          <Stack direction='row' alignItems='center' gap={0.5} sx={{ minWidth: 0 }}>
            <Input
              value={titleDraft}
              onChange={setTitleDraft}
              // Frozen while the rename is in flight — the request already
              // carries the name, so a later keystroke would be lost.
              disabled={saving}
              placeholder='Dashboard name'
              // Enter commits and Escape abandons, so the rename can be driven
              // without reaching for the two buttons.
              onKeyDown={(e) => {
                if (e.key === 'Enter') commitRename();
                if (e.key === 'Escape') setRenaming(false);
              }}
            />
            <Tooltip title='Save name'>
              <Button
                tone='ghost'
                composition='icon-only'
                aria-label='Save dashboard name'
                icon={<CheckIcon sx={{ fontSize: 18, color: ds.green[600] }} />}
                onClick={commitRename}
                disabled={saving}
                id='dashboard-rename-save-btn'
                data-testid='dashboard-rename-save-btn'
              />
            </Tooltip>
            <Tooltip title='Cancel'>
              <Button
                tone='ghost'
                composition='icon-only'
                aria-label='Cancel rename'
                icon={<CloseIcon sx={{ fontSize: 18, color: ds.gray[500] }} />}
                onClick={() => setRenaming(false)}
                disabled={saving}
                id='dashboard-rename-cancel-btn'
                data-testid='dashboard-rename-cancel-btn'
              />
            </Tooltip>
          </Stack>
        ) : (
          <Stack direction='row' alignItems='center' gap={0.5} sx={{ minWidth: 0 }}>
            <Box sx={{ minWidth: 0 }}>
              <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 18, fontWeight: 650, color: ds.gray[700] }}>
                {dashboard.title}
              </Typography>
              {dashboard.description && (
                <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                  {dashboard.description}
                </Typography>
              )}
            </Box>
            {canEdit && (
              <Tooltip title='Rename dashboard'>
                <Button
                  tone='ghost'
                  composition='icon-only'
                  aria-label='Rename dashboard'
                  icon={<SafeIcon src={writeIconLight} alt='edit' width={16} height={16} />}
                  onClick={startRename}
                  disabled={saving}
                  id='dashboard-rename-btn'
                  data-testid='dashboard-rename-btn'
                />
              </Tooltip>
            )}
          </Stack>
        )}
        {dashboard.is_builtin && (
          <Chip size='2xs' tone='subtle'>
            Built-in
          </Chip>
        )}
      </ListingLayout.Toolbar>

      {/* Body defaults to `0 space[5]` — no vertical padding — so the panel grid
          would sit flush against the card border. */}
      <ListingLayout.Body padding={`0 ${ds.space[5]} ${ds.space[5]}`}>
        <Box data-testid='dashboard-view'>
          {panels.length === 0 ? (
            <EmptyState
              surface
              size='section'
              illustration='first-time'
              title='No panels yet'
              description='Each panel runs one query against the accounts you scope it to.'
              action={canEdit ? { label: 'Add panel', onClick: openNewPanel } : undefined}
            />
          ) : (
            <Box ref={gridRef} sx={{ display: 'grid', gridTemplateColumns: 'repeat(12, 1fr)', gap: '10px', background: ds.background[100] }}>
              {panels.map((panel) => (
                <Box
                  key={panel.id}
                  sx={{
                    // gridPos.w is in 12ths, mirroring the Grafana panel model. The
                    // legacy renderer ignored it and stacked every panel full-width.
                    gridColumn: `span ${Math.min(Math.max(panel.grid_pos?.w || 12, 1), 12)}`,
                    minHeight: (panel.grid_pos?.h || 8) * 30,
                  }}
                >
                  <DashboardPanel
                    panel={panel}
                    accounts={accounts}
                    variables={variables}
                    startTime={range.startDate}
                    endTime={range.endDate}
                    refreshToken={refreshToken}
                    forceLoad={capturing}
                    onEdit={
                      canEdit
                        ? () => {
                            setEditingPanel(panel);
                            setPanelModalOpen(true);
                          }
                        : undefined
                    }
                  />
                </Box>
              ))}
            </Box>
          )}
        </Box>

        <PanelLibraryModal open={libraryOpen} existingPanels={panels} onClose={() => setLibraryOpen(false)} onPick={openLibraryPanel} />

        <PanelEditorModal
          open={panelModalOpen}
          panel={editingPanel}
          accountOptions={accounts}
          onClose={() => {
            setPanelModalOpen(false);
            setEditingPanel(null);
          }}
          onSave={savePanel}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default DashboardView;
