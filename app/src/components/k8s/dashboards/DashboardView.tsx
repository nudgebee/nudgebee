import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, Stack, Typography } from '@mui/material';
import {
  closestCenter,
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core';
import { arrayMove, rectSortingStrategy, SortableContext, sortableKeyboardCoordinates } from '@dnd-kit/sortable';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { DropdownMenu } from '@ui/DropdownMenu';
import { EmptyState } from '@ui/EmptyState';
import { Input } from '@ui/Input';
import { Modal } from '@ui/Modal';
import { snackbar } from '@ui/Toast';
import Tooltip from '@ui/Tooltip';
import AddOutlinedIcon from '@mui/icons-material/AddOutlined';
import DragIndicatorIcon from '@mui/icons-material/DragIndicator';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import LibraryBooksOutlinedIcon from '@mui/icons-material/LibraryBooksOutlined';
import BackButton from '@shared/buttons/BackButton';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import SafeIcon from '@shared/icons/SafeIcon';
import { downloadIcon, RefreshIcon, writeIconLight } from '@assets';
import { ds } from '@utils/colors';
import apiDashboards, { EMPTY_DEFINITION, type AccountOption, type Dashboard, type Panel } from '@api1/dashboards';
import DashboardPanel from './DashboardPanel';
import SortablePanel from './SortablePanel';
import usePanelResize from './usePanelResize';
import { downloadNodeAsPng, waitForPanels } from './panelImage';
import PanelEditorModal from './PanelEditorModal';
import PanelLibraryModal from './PanelLibraryModal';
import { blankPanel, panelMinHeight, panelSpan } from './panelDefaults';
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
  /**
   * Opens straight in edit mode. Set by every path that arrives here to author
   * rather than to read — a template, New dashboard, the listing's edit action.
   */
  initialEditing?: boolean;
}

const HOUR_MS = 60 * 60 * 1000;

/** Where the toolbar parks. */
const APP_HEADER_HEIGHT = `calc(${ds.space.mul(0, 28)} + 2px)`;

/** Gap between panels, in px. */
const GRID_GAP = 10;

/**
 * Where the author was headed when the unsaved-changes prompt stopped them.
 * `edit` is the toolbar's own Discard — it leaves edit mode without leaving the
 * screen; the other two leave the screen entirely.
 */
type Exit = { to: 'edit' } | { to: 'back' } | { to: 'route'; url: string };

const DashboardView: React.FC<Props> = ({ dashboard, accounts, context, onBack, onChange, canEdit, initialEditing = false }) => {
  const router = useRouter();
  // Opens on the last hour: a dashboard is read live, and an hour keeps the
  // per-panel provider queries cheap enough to fan out across accounts.
  const [range, setRange] = useState(() => {
    const now = Date.now();
    return { startDate: now - HOUR_MS, endDate: now, shortcutClickTime: HOUR_MS };
  });

  const [refreshToken, setRefreshToken] = useState(0);

  /**
   * Exporting the whole dashboard as an image. `capturing` overrides every panel's scroll gate —
   * panels below the fold have deliberately not fetched, and would rasterise as skeletons.
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
   * Refreshes every panel. A relative range ("Last 1 Hour") has to be re-evaluated against now —
   * refetching the same fixed window would return the same data and read as a broken button.
   */
  const refreshAll = () => {
    setRange((prev) => (prev.shortcutClickTime > 0 ? { ...prev, startDate: Date.now() - prev.shortcutClickTime, endDate: Date.now() } : prev));
    setRefreshToken((n) => n + 1);
  };

  // Substitution values come only from the host page — dashboards declare no
  // variables of their own.
  const variables = useMemo(() => context || {}, [context]);

  // Account narrowing is per PANEL, not per dashboard — each panel is scoped to its own accounts, so a shared
  // filter would offer accounts most panels never query.
  const savedPanels = dashboard.definition?.panels || [];

  /*
   * Editing happens HERE rather than on a separate editor screen: a panel is authored against what the
   * dashboard is currently showing, and bouncing to a form-only page loses that.
   */
  /** A dashboard with no id has never been written. */
  const isNew = !dashboard.id;

  const [draftPanels, setDraftPanels] = useState<Panel[] | null>(initialEditing ? savedPanels.slice() : null);
  // Title and description are edited in the toolbar itself while in edit mode,
  // and committed by the same Save as the layout — there is no separate rename.
  const [titleDraft, setTitleDraft] = useState(dashboard.title);
  const [descriptionDraft, setDescriptionDraft] = useState(dashboard.description || '');

  const editing = draftPanels !== null;
  const panels = draftPanels ?? savedPanels;
  // Cheap because a definition is a few dozen small objects, and it compares
  // what actually gets written rather than tracking a dirty flag per gesture.
  const dirty =
    editing &&
    (isNew ||
      titleDraft !== dashboard.title ||
      descriptionDraft !== (dashboard.description || '') ||
      JSON.stringify(draftPanels) !== JSON.stringify(savedPanels));

  /**
   * Whether leaving right now would lose work. A create counts as `dirty` from
   * the moment it opens — `isNew` forces that so Save always writes — but a
   * blank New dashboard nobody has typed into has nothing to warn about.
   */
  const unsavedWork = editing && (isNew ? Boolean(titleDraft.trim() || descriptionDraft.trim() || panels.length) : dirty);

  const [editingPanel, setEditingPanel] = useState<Panel | null>(null);
  const [panelModalOpen, setPanelModalOpen] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  /** Non-null while the leave prompt is up; holds the exit it is holding back. */
  const [pendingExit, setPendingExit] = useState<Exit | null>(null);
  /**
   * One free pass through the route guard, for a navigation this screen asked
   * for: the shallow push the host makes when a create is saved, and the leave
   * the author has just agreed to. Consumed by the first route change it sees.
   */
  const allowNavigation = useRef(false);
  /** The panel awaiting delete confirmation. Held whole so the prompt can name it. */
  const [pendingDelete, setPendingDelete] = useState<Panel | null>(null);
  /** The panel under the cursor mid-drag; drives the DragOverlay. */
  const [activeId, setActiveId] = useState<number | null>(null);

  /** Writes the dashboard back. */
  const persist = async (change: { title?: string; description?: string; panels?: Panel[] }): Promise<boolean> => {
    setSaving(true);
    // A create answers with its new id and the host writes it into the URL. That
    // push is this screen's own doing, so the guard below must not hold it.
    allowNavigation.current = true;
    try {
      const res = await apiDashboards.saveDashboard({
        // Empty on a dashboard that has never been written — `saveDashboard`
        // reads that as a create rather than an update.
        id: dashboard.id,
        title: change.title ?? dashboard.title,
        description: change.description ?? dashboard.description,
        // Falls back to the SAVED panels, never the draft: renaming mid-edit
        // must not smuggle an unsaved layout along with the new title.
        definition: { ...EMPTY_DEFINITION, ...(dashboard.definition || {}), panels: change.panels ?? savedPanels },
      });

      // The gateway reports handler failures in `errors`, not by throwing, so a
      // success toast keyed only on the absence of an exception would lie.
      if (res.errors || !res.data) {
        const message = Array.isArray(res.errors) ? (res.errors[0] as any)?.message : null;
        snackbar.error(message || 'Could not save the dashboard.');
        // Nothing was written and nothing navigates — hand the pass back.
        allowNavigation.current = false;
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

  const openLibraryPanel = (panel: Panel) => {
    setLibraryOpen(false);
    setEditingPanel(panel);
    setPanelModalOpen(true);
  };

  /** Resolves true once the panel is on the dashboard — buffered in edit mode, written otherwise. */
  const savePanel = async (panel: Panel): Promise<boolean> => {
    const isNew = !panels.some((p) => p.id === panel.id);
    const next = isNew ? [...panels, panel] : panels.map((p) => (p.id === panel.id ? panel : p));

    // In edit mode this joins the buffered layout instead of being written on
    // its own — otherwise Discard would revert where a panel sits but keep the
    // content change made in the same sitting, which is a confusing half-undo.
    if (editing) {
      setDraftPanels(next);
      setPanelModalOpen(false);
      setEditingPanel(null);
      return true;
    }

    if (!(await persist({ panels: next }))) return false;
    snackbar.success(isNew ? 'Panel added' : 'Panel updated');
    setPanelModalOpen(false);
    setEditingPanel(null);
    return true;
  };

  /* ---------------------------------------------------------------------- *
   * Edit mode
   * ---------------------------------------------------------------------- */

  const enterEdit = () => {
    setTitleDraft(dashboard.title);
    setDescriptionDraft(dashboard.description || '');
    setDraftPanels(savedPanels.slice());
  };

  /**
   * Carries out an exit, throwing the edits away. Only ever reached once the
   * author has been asked, or when there was nothing to ask about.
   */
  const discardAndLeave = (exit: Exit) => {
    setPendingExit(null);
    // A never-saved dashboard has nothing to fall back to — leaving edit mode
    // means leaving the screen.
    if (exit.to === 'route' || exit.to === 'back' || isNew) {
      allowNavigation.current = true;
      if (exit.to === 'route') router.push(exit.url);
      else onBack?.();
      return;
    }
    setTitleDraft(dashboard.title);
    setDescriptionDraft(dashboard.description || '');
    setDraftPanels(null);
  };

  const saveLayout = async (): Promise<boolean> => {
    if (!draftPanels) return false;
    const title = titleDraft.trim();
    // The server rejects an untitled dashboard, and a create started from the
    // blank New dashboard path arrives with nothing in the field.
    if (!title) {
      snackbar.error('Give the dashboard a title.');
      return false;
    }
    // Nothing changed — leaving quietly beats a request and a "saved" toast.
    if (!dirty) {
      setDraftPanels(null);
      return true;
    }
    if (!(await persist({ title, description: descriptionDraft.trim(), panels: draftPanels }))) return false;
    snackbar.success(isNew ? 'Dashboard created' : 'Dashboard saved');
    // On a create the host swaps this screen for the saved dashboard; dropping
    // the draft here is what returns it to read-only in both cases.
    setDraftPanels(null);
    return true;
  };

  /**
   * Carries out an exit, writing the edits first. A failed save keeps the author
   * where they are — `saveLayout` has already said why.
   */
  const saveAndLeave = async (exit: Exit) => {
    if (!(await saveLayout())) return;
    setPendingExit(null);
    if (exit.to === 'route' || exit.to === 'back') {
      allowNavigation.current = true;
      if (exit.to === 'route') router.push(exit.url);
      else onBack?.();
    }
    // 'edit' needs nothing further: the save already left edit mode.
  };

  /** Leaves edit mode, asking first if there is anything to lose. */
  const requestExit = () => {
    if (unsavedWork) {
      setPendingExit({ to: 'edit' });
      return;
    }
    discardAndLeave({ to: 'edit' });
  };

  /** Leaves the screen, asking first if there is anything to lose. */
  const requestBack = () => {
    if (unsavedWork) {
      setPendingExit({ to: 'back' });
      return;
    }
    onBack?.();
  };

  // A pass granted to a save that turned out not to navigate must not carry
  // into the next edit, which nobody has been asked about yet.
  useEffect(() => {
    if (unsavedWork) allowNavigation.current = false;
  }, [unsavedWork]);

  /*
   * Nothing in edit mode is written until Save, so every way off this screen has
   * to ask first — a click on the nav, the browser's own Back, a reload. The
   * toolbar's Back and Discard route through requestBack / requestExit above;
   * this covers the exits that go around them.
   */
  useEffect(() => {
    if (!unsavedWork) return undefined;

    const handleRouteChangeStart = (url: string) => {
      if (allowNavigation.current) {
        allowNavigation.current = false;
        return;
      }
      // A popstate has already rewritten the address bar. Put it back, so the
      // URL still names the dashboard the author is being held on.
      const here = router.asPath;
      if (`${window.location.pathname}${window.location.search}${window.location.hash}` !== here) {
        window.history.pushState(null, '', here);
      }
      setPendingExit({ to: 'route', url });
      // Next's own way of cancelling a transition from routeChangeStart: emit
      // the error event the router listens for, then unwind out of the handler.
      router.events.emit('routeChangeError');
      throw 'Route change aborted — the dashboard has unsaved changes. This is not an error.';
    };

    // The tab closing or reloading is the browser's to warn about; it renders
    // its own dialog and ignores any wording we pass.
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      // eslint-disable-next-line deprecation/deprecation -- returnValue is deprecated but still required for cross-browser beforeunload support
      event.returnValue = '';
      return '';
    };

    router.events.on('routeChangeStart', handleRouteChangeStart);
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => {
      router.events.off('routeChangeStart', handleRouteChangeStart);
      window.removeEventListener('beforeunload', handleBeforeUnload);
    };
  }, [unsavedWork, router]);

  /*
   * Deleting asks first, even though the change is buffered and Discard would
   * undo it: Discard is all-or-nothing, so recovering one panel deleted by
   * mistake means throwing away every other change made in the same sitting.
   */
  const confirmDeletePanel = () => {
    setDraftPanels((prev) => (prev ? prev.filter((p) => p.id !== pendingDelete?.id) : prev));
    setPendingDelete(null);
  };

  /**
   * Commits one resize step. Called only when the drag crosses into a new legal
   * width, not on every mouse move.
   */
  const handleResize = useCallback((panelId: number, width: number) => {
    setDraftPanels((prev) => (prev ? prev.map((p) => (p.id === panelId ? { ...p, grid_pos: { ...p.grid_pos, w: width } } : p)) : prev));
  }, []);

  const { resizingId, startResize } = usePanelResize({ gridRef, gap: GRID_GAP, onResize: handleResize });

  /*
   * Pointer needs a few px of travel before it counts as a drag, or clicking the handle to focus it would
   * fling the panel.
   */
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

  const handleDragStart = (event: DragStartEvent) => setActiveId(Number(event.active.id));

  const handleDragEnd = (event: DragEndEvent) => {
    setActiveId(null);
    const { active, over } = event;
    if (!over || active.id === over.id || !draftPanels) return;
    const from = draftPanels.findIndex((p) => p.id === active.id);
    const to = draftPanels.findIndex((p) => p.id === over.id);
    // A panel deleted mid-drag leaves dnd-kit holding an id that no longer
    // resolves; moving to index -1 would drop it off the front of the array.
    if (from === -1 || to === -1) return;
    // Array order IS the layout order — there are no coordinates to rewrite.
    setDraftPanels(arrayMove(draftPanels, from, to));
  };

  const activePanel = activeId === null ? null : panels.find((p) => p.id === activeId) || null;

  const openPanelEditor = (panel: Panel) => {
    setEditingPanel(panel);
    setPanelModalOpen(true);
  };

  /*
   * The panel grid, in both modes.
   */
  const grid = (
    <Box ref={gridRef} sx={{ display: 'grid', gridTemplateColumns: 'repeat(12, 1fr)', gap: `${GRID_GAP}px`, background: ds.background[300] }}>
      {panels.map((panel) =>
        editing ? (
          <SortablePanel
            key={panel.id}
            panel={panel}
            accounts={accounts}
            variables={variables}
            startTime={range.startDate}
            endTime={range.endDate}
            refreshToken={refreshToken}
            resizing={resizingId === panel.id}
            // The drag starts from the width the panel currently has, so the
            // snap points are measured from where the user grabbed it.
            onResizeStart={(event) => startResize(panel.id, panelSpan(panel), event)}
            onEdit={() => openPanelEditor(panel)}
            onDelete={() => setPendingDelete(panel)}
          />
        ) : (
          <Box
            key={panel.id}
            sx={{
              // gridPos.w is in 12ths, mirroring the Grafana panel model. The
              // legacy renderer ignored it and stacked every panel full-width.
              gridColumn: `span ${panelSpan(panel)}`,
              minHeight: panelMinHeight(panel),
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
              onEdit={canEdit ? () => openPanelEditor(panel) : undefined}
            />
          </Box>
        )
      )}
    </Box>
  );

  /**
   * One button, two ways to fill it — a blank panel and the library are the same act with a different
   * starting point, and side by side they read as competing choices.
   *
   * Rendered in the toolbar, or inside the empty state instead while the dashboard has nothing on it:
   * an empty dashboard already puts an Add panel in front of you, and a second one in the bar reads as
   * a different action.
   */
  const addPanelMenu = (options: { align: 'start' | 'end'; tone: 'primary' | 'secondary'; size: 'sm' | 'md' }) => (
    <DropdownMenu
      align={options.align}
      trigger={
        <Button
          tone={options.tone}
          size={options.size}
          icon={<KeyboardArrowDownIcon sx={{ fontSize: 18 }} />}
          iconPlacement='end'
          disabled={saving}
          id='dashboard-add-panel-btn'
          data-testid='dashboard-add-panel-btn'
        >
          Add panel
        </Button>
      }
      // Library first: picking a ready-made widget is the commoner answer
      // than authoring a query from nothing, so it leads.
      items={[
        {
          id: 'add-from-library',
          label: 'From library',
          icon: <LibraryBooksOutlinedIcon sx={{ fontSize: 17 }} />,
          onSelect: () => setLibraryOpen(true),
        },
        { id: 'add-blank-panel', label: 'Blank panel', icon: <AddOutlinedIcon sx={{ fontSize: 17 }} />, onSelect: openNewPanel },
      ]}
    />
  );

  /*
   * Time range sits before the actions: it changes what you are looking at, and what adds to the page
   * goes last. None of the three reading controls appear in edit mode.
   */
  const actions = (
    <Stack direction='row' gap={1} alignItems='center'>
      {!editing && (
        <>
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
          {/* Loads every panel first, including the ones below the fold, so the
              image is the whole dashboard rather than the part scrolled past. */}
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
        </>
      )}
      {/* Adding a panel is an edit, so both entry points live inside edit mode
          with the rest of them. Outside it the bar carries one button. */}
      {canEdit && !editing && (
        <Button
          onClick={enterEdit}
          icon={<SafeIcon src={writeIconLight} alt='edit' width={16} height={16} />}
          id='dashboard-edit-btn'
          data-testid='dashboard-edit-btn'
        >
          Edit
        </Button>
      )}
      {canEdit && editing && (
        <>
          {panels.length > 0 && addPanelMenu({ align: 'end', tone: 'secondary', size: 'md' })}
          <Button tone='secondary' onClick={requestExit} disabled={saving} id='dashboard-discard-btn' data-testid='dashboard-discard-btn'>
            Discard
          </Button>
          <Button onClick={() => void saveLayout()} loading={saving} disabled={saving} id='dashboard-save-btn' data-testid='dashboard-save-btn'>
            Save
          </Button>
        </>
      )}
    </Stack>
  );

  return (
    <Box>
      {/*
       * The dashboard's own bar, pinned under the app header.
       *
       * It carries what the whole page is scoped by — which dashboard, over
       * what window — so it has to stay reachable through a dashboard that is
       * several screens tall. Parked against the header rather than floating
       * below it so the two read as one piece of chrome.
       */}
      <Box
        id='dashboard-toolbar'
        sx={{
          position: 'sticky',
          top: APP_HEADER_HEIGHT,
          // Over the panels scrolling beneath.
          zIndex: 3,
          /*
           * Out of the page gutters and back in as padding, so the bar spans the full content column and
           * meets the header with no seam: the 24px top gutter comes from pages/dashboards, the 40px sides
           * from the page layout (components/common/layout/index.jsx).
           */
          mt: ds.space.mul(5, -1),
          mx: ds.space.mul(1, -10),
          px: ds.space.mul(1, 10),
          py: ds.space[2],
          mb: ds.space[4],
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[2],
          backgroundColor: ds.background[100],
          borderBottom: `1px solid ${ds.gray[200]}`,
        }}
      >
        {onBack && <BackButton id='dashboard-back-btn' onClick={requestBack} />}
        {editing ? (
          /* The name and description are plain fields here rather than a
             separate rename control: edit mode is already a buffered session
             with its own Save, so a second commit step inside it would be a
             save button in front of a save button. */
          <Stack direction='row' alignItems='center' gap={1} sx={{ minWidth: 0 }}>
            <Box sx={{ width: 240 }}>
              <Input
                value={titleDraft}
                onChange={setTitleDraft}
                disabled={saving}
                placeholder='Dashboard name'
                required
                id='dashboard-title-input'
                data-testid='dashboard-title-input'
              />
            </Box>
            <Box sx={{ width: 320 }}>
              <Input
                value={descriptionDraft}
                onChange={setDescriptionDraft}
                disabled={saving}
                placeholder='Description (optional)'
                id='dashboard-description-input'
                data-testid='dashboard-description-input'
              />
            </Box>
          </Stack>
        ) : (
          <Stack direction='row' alignItems='center' gap={0.5} sx={{ minWidth: 0 }}>
            <Box sx={{ minWidth: 0 }}>
              <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 18, fontWeight: 650, color: ds.gray[700] }}>
                {dashboard.title}
              </Typography>
            </Box>
            {/* The description is a tooltip, not a subtitle: it is read once
                when you arrive and then costs a line of the sticky bar on every
                screen after that. Same treatment a panel's description gets. */}
            {dashboard.description && (
              <Tooltip title={dashboard.description}>
                <Box
                  component='span'
                  sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}
                  data-testid='dashboard-description'
                >
                  <InfoOutlinedIcon sx={{ fontSize: 15 }} />
                </Box>
              </Tooltip>
            )}
          </Stack>
        )}
        {dashboard.is_builtin && (
          <Chip size='2xs' tone='subtle'>
            Built-in
          </Chip>
        )}
        <Box sx={{ flex: 1, minWidth: 0 }} />
        {actions}
      </Box>

      {/* No card around the panels: each panel is already a bordered surface,
          and a second one behind them only added a white plate between the
          panels and the page. They sit straight on the page background now. */}
      <Box data-testid='dashboard-view' sx={{ pb: ds.space[5] }}>
        {panels.length === 0 ? (
          <EmptyState
            surface
            size='section'
            illustration='first-time'
            title='No panels yet'
            description='Each panel runs one query against the accounts you scope it to.'
          >
            {/* The menu rather than EmptyState's own `action`: the first panel is the
                one most likely to come from the library, and a plain button offers
                only the blank one. Matches the action slot's own top margin. */}
            {canEdit && <Box sx={{ mt: ds.space[2] }}>{addPanelMenu({ align: 'start', tone: 'primary', size: 'sm' })}</Box>}
          </EmptyState>
        ) : editing ? (
          <DndContext
            sensors={sensors}
            // Panels differ in width, so the pointer is regularly inside more than one droppable.
            collisionDetection={closestCenter}
            onDragStart={handleDragStart}
            onDragEnd={handleDragEnd}
            onDragCancel={() => setActiveId(null)}
          >
            <SortableContext items={panels.map((p) => p.id)} strategy={rectSortingStrategy}>
              {grid}
            </SortableContext>
            {/* What follows the cursor. Deliberately NOT the real panel:
                mounting a second DashboardPanel would run its query again for
                the length of the drag. */}
            <DragOverlay>
              {activePanel && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: ds.space[2],
                    px: ds.space[3],
                    py: ds.space[2],
                    borderRadius: '8px',
                    border: `1px solid ${ds.blue[500]}`,
                    background: ds.background[100],
                    boxShadow: 'var(--ds-overlay-shadow)',
                    cursor: 'grabbing',
                  }}
                >
                  <DragIndicatorIcon sx={{ fontSize: 16, color: ds.gray[400] }} />
                  <Typography sx={{ fontSize: 13, fontWeight: 620, color: ds.gray[700] }}>{activePanel.title || 'Untitled panel'}</Typography>
                  <Chip size='2xs' tone='subtle'>{`${panelSpan(activePanel)}/12`}</Chip>
                </Box>
              )}
            </DragOverlay>
          </DndContext>
        ) : (
          grid
        )}
      </Box>

      <PanelLibraryModal
        open={libraryOpen}
        existingPanels={panels}
        accountOptions={accounts}
        // Same window and variables the dashboard is showing, so a widget
        // previews as the panel it is about to become.
        variables={variables}
        startTime={range.startDate}
        endTime={range.endDate}
        onClose={() => setLibraryOpen(false)}
        onConfigure={openLibraryPanel}
      />

      <PanelEditorModal
        open={panelModalOpen}
        panel={editingPanel}
        isEdit={editingPanel ? panels.some((p) => p.id === editingPanel.id) : false}
        accountOptions={accounts}
        // The preview runs against the same window and variables the panels beside it do, so it is not
        // answering a different question from the dashboard the author is looking at.
        variables={variables}
        startTime={range.startDate}
        endTime={range.endDate}
        onClose={() => {
          setPanelModalOpen(false);
          setEditingPanel(null);
        }}
        onSave={savePanel}
      />

      {/* Only reached with unsaved changes — leaving a clean edit session just
          leaves. Nothing here is recoverable afterwards, so it asks, and offers
          the save the author most likely meant rather than only the loss. */}
      <Modal
        open={Boolean(pendingExit)}
        handleClose={() => setPendingExit(null)}
        title='Unsaved changes'
        actionButtons={
          <Stack direction='row' gap={1} justifyContent='flex-end' sx={{ width: '100%' }}>
            <Button
              tone='ghost'
              size='md'
              onClick={() => setPendingExit(null)}
              disabled={saving}
              id='dashboard-exit-cancel-btn'
              data-testid='dashboard-exit-cancel-btn'
            >
              Cancel
            </Button>
            <Button
              tone='secondary'
              size='md'
              onClick={() => pendingExit && discardAndLeave(pendingExit)}
              disabled={saving}
              id='dashboard-exit-discard-btn'
              data-testid='dashboard-exit-discard-btn'
            >
              Discard
            </Button>
            <Button
              tone='primary'
              size='md'
              onClick={() => pendingExit && void saveAndLeave(pendingExit)}
              loading={saving}
              disabled={saving}
              id='dashboard-exit-save-btn'
              data-testid='dashboard-exit-save-btn'
            >
              Save
            </Button>
          </Stack>
        }
      >
        <Typography variant='body2'>
          {isNew ? 'This dashboard has not been saved yet.' : `Your changes to ${dashboard.title} have not been saved.`} Discard them and they are
          gone — this cannot be undone.
        </Typography>
      </Modal>

      <Modal
        open={Boolean(pendingDelete)}
        handleClose={() => setPendingDelete(null)}
        title='Delete panel?'
        confirmText='Delete'
        onConfirm={confirmDeletePanel}
      >
        <Typography variant='body2'>
          {pendingDelete?.title || 'This panel'} will be removed from the dashboard. It is not gone until you save.
        </Typography>
      </Modal>
    </Box>
  );
};

export default DashboardView;
