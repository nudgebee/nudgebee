/**
 * Guided product-tour registry.
 *
 * Each tour is an ordered list of steps that anchor to *existing* DOM ids in
 * the app — the tour never reaches into component internals, it only drives the
 * UI a user would touch by hand. Add a new tour by adding a key here and a
 * launcher (`useTour().start('<id>')`) wherever it makes sense in context.
 *
 * Anchoring contract: every `element` selector below must point at a stable id
 * / data-testid that already ships in the product. If you retarget a flow,
 * update the selector here in the same change.
 */

export type TourSide = 'top' | 'right' | 'bottom' | 'left';
export type TourAlign = 'start' | 'center' | 'end';

/**
 * Access capability a guide requires. A guide walks the user through *doing*
 * something, so it should only be offered to users who can actually do it —
 * otherwise the tour dead-ends on a role-gated button that isn't rendered.
 *   'write' → the acting user needs write access (mirrors the `hasWriteAccess()`
 *             gate on the action button the guide drives, e.g. "Add New User").
 * Omit `requires` for guides everyone can run (e.g. the app overview).
 * Resolved by `canAccessTour` in ./tourAccess.
 */
export type TourCapability = 'write';

export interface TourStepDef {
  /** CSS selector for the element to spotlight. */
  element: string;
  title: string;
  description: string;
  side?: TourSide;
  align?: TourAlign;
  /**
   * Side-effect to run when the user advances *from* this step — e.g. click a
   * button that opens a modal. Awaited before the next step is shown; the
   * engine then waits for the next step's `element` to mount, so there's no
   * need to separately declare what to wait for.
   */
  onBeforeNext?: () => void | Promise<void>;
  /**
   * When true, the step is skipped if its element isn't on screen (e.g. a
   * field that only renders under certain tenant config) instead of showing a
   * detached, centered popover.
   */
  optional?: boolean;
}

export interface TourDef {
  id: string;
  /** Human label for the tour, used by launchers and the catalog. */
  title: string;
  steps: TourStepDef[];
  /**
   * Catalog metadata. When `module` is set, the guide appears in the central
   * Guides menu grouped under it. `route` is where the guide's anchors live —
   * `useLaunchGuide` navigates there before starting when launched off-page.
   */
  module?: string;
  description?: string;
  route?: string;
  /**
   * Optional intro card shown before the first step. Rendered by the launcher
   * (a centered dialog), not the engine. When present, the engine counts it as
   * step 1 in the progress text so the dialog and popovers stay in sync.
   */
  welcome?: { title: string; description: string };
  /** Show a "Restart" button in the popover footer (jumps back to step 1). */
  showRestart?: boolean;
  /**
   * Role capability required to run this guide. When set, the guide is hidden
   * from the central Guides catalog (and any `TourLauncher`) for users who lack
   * it. Omit to offer the guide to everyone. See `canAccessTour`.
   */
  requires?: TourCapability;
}

/**
 * "Add a user" walkthrough. Anchors (already present in the product):
 *   #new-user                  → AllUsers toolbar "Add New User" button
 *   #user-modal-firstname      → UserModal first-name field
 *   #user-modal-lastname       → UserModal last-name field
 *   #user-modal-email          → UserModal work-email field
 *   #user-modal-tenant-role    → UserModal tenant-role select (optional: only
 *                                renders when the tenant has roles configured)
 *   #user-modal-group          → UserModal groups select
 *   #user-modal-submit-button  → UserModal "Add user" submit button
 */
const createUserTour: TourDef = {
  id: 'create-user',
  title: 'Add a user',
  module: 'Users',
  description: 'Add a teammate and set their role and group access, step by step.',
  route: '/user-management',
  // Adding a user is a write action; the "Add New User" button this guide
  // drives is gated on hasWriteAccess() (account_admins don't see it).
  requires: 'write',
  steps: [
    {
      element: '#new-user',
      title: 'Add a new user',
      description: 'Start here. We’ll open the form for you — click Next.',
      side: 'bottom',
      align: 'end',
      // Open the modal for the user; the engine then waits for the form
      // (#user-modal-firstname, the next step's element) to mount.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#new-user')?.click();
      },
    },
    {
      element: '#user-modal-firstname',
      title: 'First name',
      description: 'Enter the person’s first name.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#user-modal-lastname',
      title: 'Last name',
      description: 'And their last name.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#user-modal-email',
      title: 'Work email',
      description: 'The email they’ll sign in with. Their invitation is sent here, so double-check it.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#user-modal-tenant-role',
      title: 'Tenant role',
      description: 'Optional. Sets account-wide permissions. Leave it empty if the user only needs group-level access.',
      side: 'top',
      align: 'start',
      optional: true,
    },
    {
      element: '#user-modal-group',
      title: 'Groups',
      description: 'Optional. Groups control which clusters and dashboards this user can access.',
      side: 'top',
      align: 'start',
    },
    {
      element: '#user-modal-submit-button',
      title: 'Create the user',
      description: 'Click “Add user” to finish. The new user is created and invited by email.',
      side: 'top',
      align: 'end',
    },
  ],
};

/**
 * "Connect a cluster" walkthrough. Anchors (already present in the product):
 *   #add-k8s-account     → K8sIntegrationTile "Add K8s Account" button
 *   #k8sName             → K8sAccountModal account-name input
 *   #account-env         → K8sAccountModal environment toggle (prod/non-prod)
 *   #k8s-prerequisites   → K8sAccountModal prerequisites panel
 *   #create-k8s-acc      → K8sAccountModal "Next" button
 *
 * The tour deliberately stops at "Next" rather than clicking it: that button
 * creates the account (an async, real side-effect) and reveals the install
 * step. We point at it and let the user decide — same as create-user stopping
 * at the submit button.
 */
const connectClusterTour: TourDef = {
  id: 'connect-cluster',
  title: 'Connect a cluster',
  module: 'Clusters',
  description: 'Add a Kubernetes cluster and install the Nudgebee agent.',
  route: '/accounts/account-form?cloudProvider=K8S',
  // Same as create-user: the "Add K8s Account" button is gated on hasWriteAccess().
  requires: 'write',
  steps: [
    {
      element: '#add-k8s-account',
      title: 'Connect a cluster',
      description: 'Start here. Click Next and we’ll open the setup form for you.',
      side: 'bottom',
      align: 'end',
      // Open the modal; the engine then waits for the form (#k8sName) to mount.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#add-k8s-account')?.click();
      },
    },
    {
      element: '#k8sName',
      title: 'Name this cluster',
      description: 'A unique, descriptive name to identify this Kubernetes account in Nudgebee.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#account-env',
      title: 'Pick the environment',
      description: 'Production or Non-production. This presets notification policies — you can change them anytime later.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#k8s-prerequisites',
      title: 'Check the prerequisites',
      description: 'Make sure Helm, Kubernetes ≥ 1.27, and the required network access are in place before installing the agent.',
      side: 'top',
      align: 'start',
      optional: true,
    },
    {
      element: '#create-k8s-acc',
      title: 'Create & get the install command',
      description:
        'Click “Next” to create the account. You’ll get a copy-paste command to run against your cluster, and the agent connects shortly after.',
      side: 'top',
      align: 'end',
    },
  ],
};

/**
 * First-login "app overview" walkthrough of the left sidebar plus the header
 * cluster/cloud controls. Anchors to the sidebar nav button ids (set in
 * components/common/layout/index.jsx via `id={item.id}`) and two header ids set
 * in components/common/header/Header1.jsx:
 *   #global-cluster-filter → the active cluster / cloud account picker
 *   #cluster-detail-view   → the "detail view" button
 * The sidebar is global; the header controls render on /home (where the tour
 * runs). Conditional items (Clusters/Cloud/Tickets/Admin and the header
 * controls) are marked optional so the tour skips any that aren't rendered.
 */
const appOverviewTour: TourDef = {
  id: 'app-overview',
  title: 'App overview',
  module: 'Getting started',
  description: 'A quick tour of the app — the sidebar and the header controls.',
  route: '/home',
  welcome: {
    title: '🎉 Welcome to Nudgebee 🎉',
    description:
      "Run, troubleshoot, and optimize everything in one place. Here's a quick tour of the sidebar so you know where to find what — it takes under a minute.",
  },
  showRestart: true,
  steps: [
    {
      element: '#home-sidenavbutton',
      title: 'Home',
      description: 'Your starting point — ask Nubi anything and see a live snapshot of every connected cluster and cloud account.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#troubleshoot-sidenavbutton',
      title: 'Troubleshoot',
      description: 'Investigate incidents end-to-end — root-cause across pods, nodes, logs, and the service map.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#auto-pilot-sidenavbutton',
      title: 'Automations',
      description: 'Build and run remediation workflows — automate fixes and routine operations so issues resolve themselves.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#optimize-sidenavbutton',
      title: 'Optimize',
      description: 'Right-size workloads and cut cloud spend with cost and efficiency recommendations.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#clusters-sidenavbutton',
      title: 'Clusters',
      description: 'Drill into your Kubernetes clusters — health, workloads, security, and configuration.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#cloud-sidenavbutton',
      title: 'Cloud',
      description: 'Your connected cloud accounts (AWS, Azure, GCP) — spend, inventory, and security posture.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#tickets-sidenavbutton',
      title: 'Tickets',
      description: 'Track issues and link them to incidents and integrations like Jira and ServiceNow.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#admin-sidenav',
      title: 'Admin',
      description: 'Manage users, groups, integrations, and account settings — and replay tours anytime.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#global-cluster-filter',
      title: 'Cluster & cloud account filter',
      description:
        'Switch the active cluster or cloud account here — most views filter to whatever you select. (Header anchors only show on pages that use them.)',
      side: 'bottom',
      align: 'end',
      optional: true,
    },
    {
      element: '#cluster-detail-view',
      title: 'Details view',
      description: 'Jump straight to the full details — health, workloads, security, and cost — of the selected cluster or cloud account.',
      side: 'bottom',
      align: 'end',
      optional: true,
    },
  ],
};

/**
 * "Explore Troubleshoot" orientation. Unlike the create-* walkthroughs this is a
 * read-only tour: it explains the three top-level views and points at the event
 * columns' built-in ⓘ tooltips rather than re-documenting each column (which are
 * user-configurable, so a per-column spotlight would be brittle). Anchors:
 *   [id="anchor-tab-All Events"] → the All Events top tab (AnchorComponent renders
 *                                  id=`anchor-tab-<name>`; the bar is absolutely
 *                                  positioned so we can't wrap it without breaking
 *                                  layout — anchor the tab itself)
 *   #troubleshoot-event-tabs → the event-grouping sub-tabs (All Events view)
 *   #tab-all                 → the "Events" sub-tab (Tabs renders id=`tab-<value>`)
 *   #all-events              → the flat Events list (KubernetesEvents ListingLayout)
 *   #auto-complete-filter-sort-by → the Sort By control in the Events toolbar
 *                                   (FilterDropdown prefixes its id; optional)
 *   #all-events thead        → the Events table header row (holds the ⓘ tooltips)
 * Step 1 re-asserts the All Events top tab so the tour is robust when launched
 * from another sub-tab; step 2 opens the flat Events list for the columns step.
 */
const troubleshootTour: TourDef = {
  id: 'troubleshoot-overview',
  title: 'Explore Troubleshoot',
  module: 'Troubleshoot',
  description: 'Tour the Troubleshoot views and see what each event column means.',
  route: '/troubleshoot',
  steps: [
    {
      element: '[id="anchor-tab-All Events"]',
      title: 'Three ways to troubleshoot',
      description:
        'All Events triages what’s happening now, Investigations holds the auto & manual root-cause analyses, and Knowledge Graph is your live service map. You’re on All Events.',
      side: 'bottom',
      align: 'start',
      // Make sure we're on the All Events view before pointing at its sub-tabs,
      // in case the tour was launched from Investigations or Knowledge Graph.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('[id="anchor-tab-All Events"]')?.click();
      },
    },
    {
      element: '#troubleshoot-event-tabs',
      title: 'Organise your events',
      description:
        'Switch how events are shown — the Triage Inbox, a flat Events list, or grouped by type or app — plus Triage Rules, Alert Tuning, and Event Resolutions. We’ll open the Events list next.',
      side: 'bottom',
      align: 'start',
      // Open the flat Events list so the column step lands on the rich table.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#tab-all')?.click();
      },
    },
    {
      element: '#all-events',
      title: 'The Events list',
      description: 'Each row is one alert or event. Click a row to open a full investigation with logs, metrics, and the service map.',
      side: 'top',
      align: 'center',
    },
    {
      // FilterDropdown re-emits its `id` as `auto-complete-<kebab(id)>`, so the
      // DOM id of the Sort By control is not the raw `filter-sort-by`.
      element: '#auto-complete-filter-sort-by',
      title: 'Filter & sort',
      description: 'Narrow the list by severity, status, source, namespace and more, and change the sort order to surface what matters first.',
      side: 'bottom',
      align: 'end',
      optional: true,
    },
    {
      element: '#all-events thead',
      title: 'What does each column mean?',
      description:
        'Not sure what a column shows? Hover the ⓘ next to any header — Severity, Triage Score, Alert Status and more each explain themselves. Use the column menu to add or remove columns.',
      side: 'bottom',
      align: 'center',
    },
  ],
};

/**
 * "Explore the Knowledge Graph" orientation. Knowledge Graph is the third
 * Troubleshoot view (feature-flagged) — a live ReactFlow service map, not a
 * table — so this tour teaches how to read and drive the graph. Anchors:
 *   [id="anchor-tab-Knowledge Graph"] → the KG top tab (opens selectedFilter=2)
 *   #kg-canvas              → the graph canvas (graphWrapperRef Box)
 *   #kg-filter-panel        → the left filter sidebar (WidgetCard; gone when collapsed)
 *   #auto-complete-kg-node-search → node search in the top-right toolbar
 *                                   (FilterDropdown prefixes its id — see step 4)
 *   #relationship-types-btn → the Relationships legend button (hover shows legend)
 *   #kg-settings-btn        → the Settings button (tenant admins only)
 * Step 1 opens the KG tab so the rest can anchor its elements; if the KG feature
 * flag is off that tab is absent and the tour ends there (KG isn't available).
 */
const knowledgeGraphTour: TourDef = {
  id: 'knowledge-graph',
  title: 'Explore the Knowledge Graph',
  module: 'Troubleshoot',
  description: 'Read your live service map — nodes, dependencies, filters, and search.',
  route: '/troubleshoot',
  steps: [
    {
      element: '[id="anchor-tab-Knowledge Graph"]',
      title: 'Open the Knowledge Graph',
      description: 'The Knowledge Graph is your live service map. Let’s open it.',
      side: 'bottom',
      align: 'start',
      // Switch to the KG view so the following steps can anchor its elements.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('[id="anchor-tab-Knowledge Graph"]')?.click();
      },
    },
    {
      element: '#kg-canvas',
      title: 'Your live service map',
      description:
        'Every node is a service, workload, or cloud resource; every edge is a real dependency Nudgebee discovered between them. Drag to pan, scroll to zoom.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#kg-filter-panel',
      title: 'Scope the map',
      description: 'Narrow the graph by account, node type, and label or attribute — set how many hops to expand from a node, then Apply.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      // FilterDropdown re-emits its `id` prop as `auto-complete-<kebab(id)>` on
      // the trigger button, so the DOM id is not the raw `kg-node-search`.
      element: '#auto-complete-kg-node-search',
      title: 'Find a service',
      description: 'Jump straight to any service — search by name and the graph focuses on it and its immediate neighbours.',
      side: 'bottom',
      align: 'end',
    },
    {
      element: '#relationship-types-btn',
      title: 'Read the graph',
      description:
        'Hover Relationships for the legend — what each node colour and edge type means. Click any node’s ⓘ for full details, or an edge to inspect the dependency.',
      side: 'bottom',
      align: 'end',
    },
    {
      element: '#kg-settings-btn',
      title: 'Graph settings',
      description: 'Admins can tune what the graph ingests and how it’s built here.',
      side: 'bottom',
      align: 'end',
      optional: true,
    },
  ],
};

export const TOURS: Record<string, TourDef> = {
  [createUserTour.id]: createUserTour,
  [connectClusterTour.id]: connectClusterTour,
  [appOverviewTour.id]: appOverviewTour,
  [troubleshootTour.id]: troubleshootTour,
  [knowledgeGraphTour.id]: knowledgeGraphTour,
};
