import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, Stack, Typography } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import { DeleteIconRed as deleteIcon, downloadIcon, EditIcon, K8sIcon } from '@assets';
import { ListingLayout } from '@ui/ListingLayout';
import { Button } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { snackbar } from '@ui/Toast';
import SearchInput from '@ui/SearchInput';
import AddOutlinedIcon from '@mui/icons-material/AddOutlined';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import K8sAccountModal from '@components/integrations/modal/K8sAccountModal';
import { useData } from '@context/DataContext';
import { canManage, hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';
import { downloadJsonFile, filenameSlug } from '@utils/fileDownload';
import apiDashboards, { type AccountOption, type Dashboard, type DashboardBinding } from '@api1/dashboards';
import DashboardView from './DashboardView';
import { PANEL_PARAM } from './panelLink';
import DashboardSkeleton from './DashboardSkeleton';
import ImportDashboardModal from './ImportDashboardModal';
import GenerateDashboardModal from './GenerateDashboardModal';
import TemplateGalleryModal from './TemplateGalleryModal';
import CreateDashboardOptionsModal from './CreateDashboardOptionsModal';
import { NUBI_DRAFT_PARAM, takeStashedDashboard, type StashedDashboardDraft } from './generatedDashboard';

type Mode = { name: 'list' } | { name: 'view'; id: string } | { name: 'create' };

/** The shape a dashboard that has never been saved starts from. */
const BLANK_DASHBOARD: Dashboard = { id: '', title: '', description: '', definition: { panels: [] } };

/** Account kinds a panel can actually query. Everything else in `accounts_list`
 *  (slack, …) is an integration, not an observability source. */
const QUERYABLE_ACCOUNT_TYPES = new Set(['cloud', 'kubernetes']);

/**
 * Query parameter naming the open dashboard. A parameter rather than a hash segment because this page
 * is hash-routed — `#dashboards` selects the tab, and AnchorComponent matches that fragment whole.
 */
const DASHBOARD_PARAM = 'dashboard';

/** The three things a dashboard is for, as the empty state's reassurance row. */
const EMPTY_STATE_HIGHLIGHTS = [
  {
    label: 'Metrics, logs & traces',
    icon: 'M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z',
  },
  {
    label: 'Ready-made templates',
    icon: 'M4 6a2 2 0 012-2h3a2 2 0 012 2v3a2 2 0 01-2 2H6a2 2 0 01-2-2V6zm9 0a2 2 0 012-2h3a2 2 0 012 2v3a2 2 0 01-2 2h-3a2 2 0 01-2-2V6zM4 15a2 2 0 012-2h3a2 2 0 012 2v3a2 2 0 01-2 2H6a2 2 0 01-2-2v-3zm9 0a2 2 0 012-2h3a2 2 0 012 2v3a2 2 0 01-2 2h-3a2 2 0 01-2-2v-3z',
  },
  {
    label: 'Shared across the team',
    icon: 'M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z',
  },
];

const CustomDashboards: React.FC = () => {
  const { allCluster } = useData();
  const router = useRouter();

  // A dashboard has no account of its own — each PANEL names the account it
  const accountOptions: AccountOption[] = React.useMemo(
    () =>
      (allCluster || [])
        .filter((c: any) => c.value !== 'demo')
        // `accounts_list` mixes integrations in with cloud accounts — Slack
        .filter((c: any) => !c.account_type || QUERYABLE_ACCOUNT_TYPES.has(c.account_type))
        // `kind` is what lets the template gallery pre-select a scope per panel: a PromQL widget needs a
        // kubernetes account, and only this field says which of the connected accounts is one.
        // `status` rides along so the panel editor can say a saved account has
        // been disabled — nothing downstream filters on it, so without this a
        // disabled account is indistinguishable from a live one.
        .map((c: any) => ({
          label: c.label || c.value,
          value: c.value,
          cloud_provider: c.cloud_provider || '',
          kind: c.account_type || '',
          status: c.status || '',
        })),
    [allCluster]
  );

  const [mode, setMode] = useState<Mode>({ name: 'list' });
  const [dashboards, setDashboards] = useState<Dashboard[]>([]);
  const [loading, setLoading] = useState(false);
  // Two search states, as everywhere else in the app (see KubernetesAlertManager):
  // `searchInput` is what is typed, `search` is what has been submitted. Only the
  // submitted one feeds the request, so typing costs nothing until Enter.
  const [searchInput, setSearchInput] = useState('');
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<{ dashboard: Dashboard; bindings: DashboardBinding[]; editing?: boolean } | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Dashboard | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [templatesOpen, setTemplatesOpen] = useState(false);
  const [generateOpen, setGenerateOpen] = useState(false);
  const [createOptionsOpen, setCreateOptionsOpen] = useState(false);
  /** Connecting the first cluster, from the empty state below. */
  const [showAddClusterModal, setShowAddClusterModal] = useState(false);
  /** An unsaved dashboard — blank, or built from a template — waiting to be authored. */
  const [draftDashboard, setDraftDashboard] = useState<Dashboard | null>(null);
  /**
   * The dashboard the listing's edit action asked to open in edit mode — "edit
   * this", not "look at this". An id, used up by the one open it was said about,
   * rather than a flag: the route can swap the open dashboard in place (the
   * header search), and a flag turned that into "edit whichever opens next".
   */
  const editOnOpen = useRef<string | null>(null);

  // Mirrors the backend gate: anyone who can write to at least one account may author dashboards, and the
  // panels they may point at are checked per account on save.
  const canWrite = accountOptions.some((o) => hasWriteAccess(o.value));

  // The URL is what says which dashboard is open, so a link to one survives a
  // reload and the browser's Back button walks out of it.
  const routeId = router.isReady && typeof router.query[DASHBOARD_PARAM] === 'string' ? (router.query[DASHBOARD_PARAM] as string) : '';
  // Read inside the effect below without making it re-run on every mode change,
  // which would refetch the dashboard each time it saved.
  const modeRef = useRef(mode);
  modeRef.current = mode;

  /** Writes (or clears) the open dashboard in the URL. */
  const setRouteId = useCallback(
    (id: string | null) => {
      const [pathAndQuery, hash] = (router.asPath || '').split('#');
      const [path, queryString] = (pathAndQuery || '').split('?');
      const params = new URLSearchParams(queryString);
      if (id) params.set(DASHBOARD_PARAM, id);
      else params.delete(DASHBOARD_PARAM);
      // A panel belongs to the dashboard it was linked on; the next one opens at its top.
      params.delete(PANEL_PARAM);
      const query = params.toString();
      router.push(`${path}${query ? `?${query}` : ''}${hash ? `#${hash}` : ''}`, undefined, { shallow: true });
    },
    [router]
  );

  const loadList = useCallback(
    async (signal: { cancelled: boolean }) => {
      setLoading(true);
      try {
        const res = await apiDashboards.listDashboards({ search: search || undefined });
        if (signal.cancelled) return;
        if (res.errors || !res.data) {
          snackbar.error('Could not load dashboards.');
          return;
        }
        setDashboards(res.data);
      } finally {
        // `finally` rather than a trailing call: an early return above (or an
        if (!signal.cancelled) setLoading(false);
      }
    },
    [search]
  );

  useEffect(() => {
    const signal = { cancelled: false };
    // Not while a dashboard is being opened from the URL — the listing is
    // behind it and would only be a wasted request on a deep link.
    if (mode.name === 'list' && !routeId) loadList(signal);
    return () => {
      signal.cancelled = true;
    };
  }, [mode.name, routeId, loadList]);

  // Opening is driven by the URL alone: every entry point (a click, a pasted link, Back) writes the
  // parameter, and this is the single place that turns it into a loaded dashboard.
  useEffect(() => {
    if (!router.isReady) return undefined;
    // "Edit this" was said about one dashboard. Once the route names anything else — another dashboard,
    // or the listing — the request is over, even when its own open was cut short and never used it.
    if (editOnOpen.current !== routeId) editOnOpen.current = null;
    if (!routeId) {
      if (modeRef.current.name === 'view') setMode({ name: 'list' });
      return undefined;
    }
    // Already open — an in-place save re-asserts the same id and must not refetch.
    if (modeRef.current.name === 'view' && modeRef.current.id === routeId) return undefined;

    const signal = { cancelled: false };
    apiDashboards.getDashboard(routeId).then((res) => {
      if (signal.cancelled) return;
      const editing = editOnOpen.current === routeId;
      editOnOpen.current = null;
      if (res.errors || !res.data) {
        snackbar.error('Could not open that dashboard.');
        setRouteId(null);
        return;
      }
      setSelected({ dashboard: res.data.dashboard, bindings: res.data.bindings || [], editing });
      setMode({ name: 'view', id: routeId });
    });
    return () => {
      signal.cancelled = true;
    };
  }, [router.isReady, routeId, setRouteId]);

  // "Open as dashboard" on a Nubi conversation lands here with the generated
  // draft in sessionStorage. It goes through Generate's own preview — the same
  // panel check and notes a fresh generation gets — and from there to the editor,
  // unsaved until Save. Leaving an editor with unsaved work to get here is held
  // by DashboardView's own route guard, which the push trips.
  const nubiDraftRequested = router.isReady && router.query[NUBI_DRAFT_PARAM] === 'nubi';
  const nubiDraftHandled = useRef(false);
  const [nubiDraft, setNubiDraft] = useState<StashedDashboardDraft | null>(null);
  useEffect(() => {
    // Once the param is gone the next hand-off (Nubi's popup sits over this
    // page, so it can come without a remount) must be taken too.
    if (!nubiDraftRequested) {
      nubiDraftHandled.current = false;
      return;
    }
    if (nubiDraftHandled.current) return;
    nubiDraftHandled.current = true;
    const [pathAndQuery, hash] = (router.asPath || '').split('#');
    const [path, queryString] = (pathAndQuery || '').split('?');
    const params = new URLSearchParams(queryString);
    params.delete(NUBI_DRAFT_PARAM);
    const query = params.toString();
    router.replace(`${path}${query ? `?${query}` : ''}${hash ? `#${hash}` : ''}`, undefined, { shallow: true });

    const stashed = takeStashedDashboard();
    if (!stashed) {
      snackbar.error('The generated dashboard was not found. Open it again from the conversation.');
      return;
    }
    if (!hasWriteAccess(stashed.accountId)) {
      snackbar.error('You do not have permission to create dashboards for that account.');
      return;
    }
    // The preview lives on the listing; a draft already open in the editor is
    // closed for it (the route guard has had its say by now).
    setDraftDashboard(null);
    setMode({ name: 'list' });
    setNubiDraft(stashed);
  }, [nubiDraftRequested, router]);

  /** Opens a dashboard for authoring — the same screen as viewing it, already in edit mode. */
  const openForEdit = (id: string) => {
    editOnOpen.current = id;
    setRouteId(id);
  };

  /** Downloads a dashboard as JSON. */
  const exportDashboard = (dashboard: Dashboard) => {
    const panels = dashboard.definition?.panels || [];
    // Panels name accounts by id, which is opaque in any other tenant. This
    const referenced = new Set(panels.flatMap((p) => p.account_ids || []));
    const accounts = Object.fromEntries(
      [...referenced].map((id) => {
        const account = accountOptions.find((o) => o.value === id);
        return [id, { label: account?.label || id, cloud_provider: account?.cloud_provider || '' }];
      })
    );

    downloadJsonFile(
      {
        title: dashboard.title,
        description: dashboard.description || '',
        definition: dashboard.definition || { panels: [] },
        ...(referenced.size > 0 ? { accounts } : {}),
      },
      filenameSlug(dashboard.title, 'dashboard')
    );
  };

  const confirmDelete = async () => {
    if (!pendingDelete || deleting) return;
    setDeleting(true);
    try {
      const res = await apiDashboards.deleteDashboard(pendingDelete.id);
      if (res.errors || !res.data?.deleted) {
        snackbar.error('Could not delete the dashboard.');
        setPendingDelete(null);
        return;
      }
      snackbar.success('Dashboard deleted');
      setPendingDelete(null);
      setDashboards((prev) => prev.filter((d) => d.id !== pendingDelete.id));
    } finally {
      setDeleting(false);
    }
  };

  /*
   * Nothing to point a panel at. Same treatment the Account Overview page gives its own
   * first-run state (components/overview/AccountOverview.jsx) — a card that says
   * what the page is for and carries the one action that unblocks it, rather than a line of
   * grey text on the page background.
   */
  if (accountOptions.length === 0) {
    return (
      <>
        {/* No `handleOnAccountCreate`: the modal already refreshes DataContext's
            cluster list itself (useUpdateAllClusterOption), which is the same
            `allCluster` this screen reads — so the new cluster turns the empty
            state into the listing without a second fetch. */}
        <K8sAccountModal openModal={showAddClusterModal} handleClose={() => setShowAddClusterModal(false)} />
        <Box
          data-testid='dashboards-empty-state'
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 'var(--ds-space-7) var(--ds-space-6)',
            borderRadius: 'var(--ds-radius-xl)',
            border: '1px solid var(--ds-gray-300)',
            background: 'var(--ds-background-100)',
          }}
        >
          <Box
            sx={{
              width: ds.space.mul(0, 32),
              height: ds.space.mul(0, 32),
              borderRadius: 'var(--ds-radius-xl)',
              background: `linear-gradient(135deg, var(--ds-blue-100) 0%, ${ds.blue[200]} 100%)`,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              mb: ds.space[5],
              boxShadow: '0px 1px 3px color-mix(in srgb, var(--ds-gray-700) 6%, transparent)',
              '& svg': { filter: 'none', width: 36, height: 36 },
            }}
          >
            <K8sIcon />
          </Box>

          <Typography
            sx={{
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-foreground)',
              mb: ds.space[2],
              fontFamily: 'var(--ds-font-display)',
            }}
          >
            Get started with custom dashboards
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              color: 'var(--ds-gray-600)',
              mb: ds.space[6],
              textAlign: 'center',
              maxWidth: ds.space.mul(0, 230),
              lineHeight: 1.6,
            }}
          >
            Connect a cluster to build dashboards. Each panel runs one query against the accounts you scope it to - start from a template or compose
            your own.
          </Typography>

          {/* Column below sm: each label is `nowrap`, and three of them side by side
              overflow a phone-width card. */}
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={{ xs: 2, sm: 4 }} sx={{ mb: ds.space[6] }}>
            {EMPTY_STATE_HIGHLIGHTS.map((item) => (
              <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                <Box
                  sx={{
                    width: ds.space[6],
                    height: ds.space[6],
                    borderRadius: 'var(--ds-radius-lg)',
                    background: 'var(--ds-blue-100)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    flexShrink: 0,
                  }}
                >
                  <svg
                    width='16'
                    height='16'
                    viewBox='0 0 24 24'
                    fill='none'
                    stroke='var(--ds-blue-600)'
                    strokeWidth='1.5'
                    strokeLinecap='round'
                    strokeLinejoin='round'
                  >
                    <path d={item.icon} />
                  </svg>
                </Box>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-brand-500)',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {item.label}
                </Typography>
              </Box>
            ))}
          </Stack>

          {/* `canWrite` is derived from the accounts, and there are none. Connecting one is
              `accounts_create` (module `accounts`, class Write), so the gate is CanManage's
              three ways in: tenant admin, super admin, or an `accounts:Write` custom grant.
              A bare hasWriteAccess() would deny the last two — it can only answer from the
              session's `roles` and per-account id lists, and both are empty for them. */}
          {canManage('accounts', 'Write') ? (
            <Button tone='primary' size='lg' onClick={() => setShowAddClusterModal(true)} id='dashboards-add-cluster-btn'>
              Add Cluster
            </Button>
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
              Need admin permission to connect a cluster
            </Typography>
          )}
        </Box>
      </>
    );
  }

  /*
   * A dashboard that does not exist yet. It goes to the SAME screen a saved one does, opened in edit
   * mode.
   */
  if (mode.name === 'create' && draftDashboard) {
    return (
      <DashboardView
        dashboard={draftDashboard}
        accounts={accountOptions}
        canEdit
        initialEditing
        onBack={() => {
          setDraftDashboard(null);
          setMode({ name: 'list' });
        }}
        // The create answers with the stored dashboard — the first time this
        // page learns the new id, and what it routes to.
        onChange={(saved) => {
          setDraftDashboard(null);
          setSelected({ dashboard: saved, bindings: [] });
          setMode({ name: 'view', id: saved.id });
          setRouteId(saved.id);
        }}
      />
    );
  }

  if (mode.name === 'view' && selected) {
    return (
      <DashboardView
        // Keyed on the dashboard: the route can swap the dashboard IN PLACE (the header search opens another
        // one while this is showing), and the view's account filter, time range and drafts belong to the one
        // they were set on. A remount is what drops them, rather than each piece of state resetting itself.
        key={selected.dashboard.id}
        dashboard={selected.dashboard}
        accounts={accountOptions}
        canEdit={canWrite && !selected.dashboard.is_builtin}
        initialEditing={selected.editing}
        onBack={() => setRouteId(null)}
        // Panels, title and description are edited in place; each save answers
        // with the stored dashboard, which becomes what this page holds.
        onChange={(saved) => setSelected((prev) => ({ dashboard: saved, bindings: prev?.bindings || [] }))}
      />
    );
  }

  // A dashboard named in the URL but not loaded yet. Showing the listing under
  // it for a beat would read as a failed link.
  if (routeId) {
    return <DashboardSkeleton />;
  }

  const headers = [
    { name: 'Dashboard', width: '40%' },
    { name: 'Panels', width: '8%' },
    { name: 'Created At', width: '16%' },
    { name: 'Updated At', width: '16%' },
    { name: 'Action', width: '20%' },
  ];

  // CustomTable renders `component || text` — a cell carrying only `value` renders blank, which is why Panels
  // and Updated At were empty.
  const tableData = dashboards.map((d) => [
    {
      value: d.title,
      component: (
        <Box sx={{ cursor: 'pointer', minWidth: 0 }} onClick={() => setRouteId(d.id)} data-testid={`open-dashboard-${d.id}`}>
          <Typography sx={{ fontSize: 13, fontWeight: ds.weight.semibold, color: ds.blue[500], overflowWrap: 'anywhere' }}>{d.title}</Typography>
          {d.description && (
            <Tooltip title={d.description}>
              <Typography
                variant='caption'
                sx={{
                  color: ds.gray[500],
                  display: '-webkit-box',
                  WebkitLineClamp: 2,
                  WebkitBoxOrient: 'vertical',
                  overflow: 'hidden',
                  overflowWrap: 'anywhere',
                }}
              >
                {d.description}
              </Typography>
            </Tooltip>
          )}
        </Box>
      ),
    },
    { text: String(d.definition?.panels?.length ?? 0), value: String(d.definition?.panels?.length ?? 0) },
    {
      // Relative time with an absolute tooltip, matching every other listing.
      component: <Datetime value={d.created_at} />,
      value: d.created_at || '',
    },
    {
      component: <Datetime value={d.updated_at} />,
      value: d.updated_at || '',
    },
    {
      value: '',
      component: (
        <Box sx={{ display: 'flex', gap: 0.5 }}>
          {/* Export is a read, so it is offered on every dashboard — including
              the built-ins, which are the ones worth copying as a starting
              point. Edit and Delete stay behind write access. */}
          <Tooltip title='Export JSON'>
            <Button
              tone='ghost'
              composition='icon-only'
              aria-label='Export dashboard JSON'
              icon={<SafeIcon src={downloadIcon} alt='export' width={18} height={18} />}
              onClick={() => exportDashboard(d)}
              id={`export-dashboard-${d.id}`}
              data-testid={`export-dashboard-${d.id}`}
            />
          </Tooltip>
          {canWrite && !d.is_builtin && (
            <>
              {/* Same icon assets every other listing uses (see
              components/notifications/index.tsx) — MUI's outline icons read as a
              different family next to them. */}
              <Tooltip title='Edit'>
                <Button
                  tone='ghost'
                  composition='icon-only'
                  aria-label='Edit dashboard'
                  icon={<SafeIcon src={EditIcon} alt='edit' width={18} height={18} />}
                  onClick={() => openForEdit(d.id)}
                  id={`edit-dashboard-${d.id}`}
                />
              </Tooltip>
              <Tooltip title='Delete'>
                <Button
                  tone='ghost'
                  composition='icon-only'
                  aria-label='Delete dashboard'
                  icon={<SafeIcon src={deleteIcon} alt='delete' width={18} height={18} />}
                  onClick={() => setPendingDelete(d)}
                  id={`delete-dashboard-${d.id}`}
                />
              </Tooltip>
            </>
          )}
        </Box>
      ),
    },
  ]);

  return (
    <>
      <ListingLayout id='custom-dashboards'>
        <ListingLayout.Toolbar
          actions={
            canWrite ? (
              // One button, and a chooser of the ways to start — the same shape
              // as the Automation page's "Create a New Automation".
              <Button
                icon={<AddOutlinedIcon sx={{ fontSize: 18 }} />}
                onClick={() => setCreateOptionsOpen(true)}
                id='new-dashboard-btn'
                data-testid='new-dashboard-btn'
              >
                New dashboard
              </Button>
            ) : null
          }
        >
          <SearchInput
            value={searchInput}
            // Emptying the field restores the full list without an Enter — an
            // empty box that still shows filtered rows reads as a bug. Every
            // other keystroke waits for Enter. This covers the X too: it
            // reports the cleared value through onChange before onClear, so no
            // separate onClear handler is needed here.
            onChange={(next: string) => {
              setSearchInput(next);
              if (next.trim() === '') setSearch('');
            }}
            onEnterPress={() => setSearch(searchInput.trim())}
            label='Search dashboards'
            id='dashboard-search'
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable headers={headers} tableData={tableData} loading={loading} />
        </ListingLayout.Body>
      </ListingLayout>

      <CreateDashboardOptionsModal
        open={createOptionsOpen}
        onClose={() => setCreateOptionsOpen(false)}
        onSelect={(option) => {
          setCreateOptionsOpen(false);
          if (option === 'template') setTemplatesOpen(true);
          else if (option === 'generate') setGenerateOpen(true);
          else if (option === 'import') setImportOpen(true);
          else {
            setDraftDashboard(BLANK_DASHBOARD);
            setMode({ name: 'create' });
          }
        }}
      />

      <TemplateGalleryModal
        open={templatesOpen}
        accountOptions={accountOptions}
        onClose={() => setTemplatesOpen(false)}
        // Onto the dashboard itself, in edit mode — not onto a saved dashboard
        onDraft={(draft) => {
          setTemplatesOpen(false);
          setDraftDashboard(draft);
          setMode({ name: 'create' });
        }}
      />

      <ImportDashboardModal
        open={importOpen}
        accountOptions={accountOptions}
        onClose={() => setImportOpen(false)}
        // Same draft path as a template: nothing exists until the editor's Save,
        // so backing out and discarding leaves no dashboard behind.
        onDraft={(draft) => {
          setImportOpen(false);
          setDraftDashboard(draft);
          setMode({ name: 'create' });
        }}
      />

      <GenerateDashboardModal
        open={generateOpen || Boolean(nubiDraft)}
        accountOptions={accountOptions}
        initialDraft={nubiDraft}
        onClose={() => {
          setGenerateOpen(false);
          setNubiDraft(null);
        }}
        // Same draft path again: the generated dashboard is reviewed and saved in
        // the editor, never written by the agent.
        onDraft={(draft) => {
          setGenerateOpen(false);
          setNubiDraft(null);
          setDraftDashboard(draft);
          setMode({ name: 'create' });
        }}
      />

      {/* `loader` runs the modal's progress bar and disables both footer buttons;
          the close affordances are withheld separately so the delete cannot be
          dismissed while it is already on its way to the server. */}
      <Modal
        open={Boolean(pendingDelete)}
        handleClose={deleting ? undefined : () => setPendingDelete(null)}
        title='Delete dashboard?'
        confirmText='Delete'
        confirmTone='danger'
        onConfirm={confirmDelete}
        loader={deleting}
        backdropClickClose={false}
      >
        <Typography variant='body2'>{pendingDelete?.title} and its revision history will be removed. This cannot be undone.</Typography>
      </Modal>
    </>
  );
};

export default CustomDashboards;
