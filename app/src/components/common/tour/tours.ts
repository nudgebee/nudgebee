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
  /**
   * Tenant feature flag (a `feature_id`) this guide needs. When set, the guide
   * is hidden unless the flag is enabled — e.g. the Knowledge Graph guide, whose
   * whole surface only exists behind its feature flag. Resolved via the cached
   * feature flags (`hasFeatureAccessCached`); see `canAccessTour`.
   */
  requiresFeature?: string;
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
 *   #create-k8s-acc          → K8sAccountModal "Next" button
 *   #learn-how-to-install-btn→ header "Learn How to Install" link (both steps)
 *   #included-components     → step-2 component checkbox grid
 *   #advanced-toggle         → step-2 "Advanced" expander (opened to reveal fields)
 *   #adv-fields-panel        → step-2 Prometheus URL + Image Registry inputs
 *   #panel-shell             → K8sAccountModal step-2 install-command panel
 *   #tab-helm                → step-2 "Helm" install-method tab (Tabs id=`tab-<value>`)
 *
 * Clicking "Next" normally creates a real account. To demo the whole flow
 * without that side-effect, K8sAccountModal detects this tour (`useTour()`) and
 * previews the install step with a sample key instead — so the tour can walk
 * the user all the way to the copy-paste command. Nothing is created.
 */
const connectClusterTour: TourDef = {
  id: 'connect-cluster',
  title: 'Connect a K8s Cluster',
  module: 'Accounts',
  description: 'Add a Kubernetes cluster and install the Nudgebee agent.',
  route: '/accounts/account-form?cloudProvider=K8S',
  // Same as create-user: the "Add K8s Account" button is gated on hasWriteAccess().
  requires: 'write',
  steps: [
    {
      element: '#add-k8s-account',
      title: 'Connect a K8s cluster',
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
      title: 'Get your install command',
      description: 'Click Next and we’ll show you the command you’d run. This is a preview — no cluster is created.',
      side: 'top',
      align: 'end',
      // Advance the modal to the install step. K8sAccountModal sees this tour is
      // active and previews step 2 with a sample key instead of creating an account.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#create-k8s-acc')?.click();
      },
    },
    {
      element: '#learn-how-to-install-btn',
      title: 'Finish Setup',
      description: 'This is where you get everything to install the agent. Prefer to watch? “Learn How to Install” walks through it end to end.',
      side: 'bottom',
      align: 'end',
    },
    {
      element: '#included-components',
      title: 'Choose what to install',
      description:
        'Every component ships by default — tick a box to DISABLE one you don’t need (skip OpenCost, the Node Agent, and so on). The install command updates automatically.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#advanced-toggle',
      title: 'Advanced options',
      description: 'Running your own Prometheus, a private image registry, or an air-gapped cluster? Open Advanced to set those.',
      side: 'top',
      align: 'start',
      // Expand the Advanced panel so the next step's fields are on screen.
      onBeforeNext: () => {
        const adv = document.querySelector<HTMLElement>('#advanced-toggle');
        if (adv?.getAttribute('aria-expanded') !== 'true') {
          adv?.click();
        }
      },
    },
    {
      element: '#adv-fields-panel',
      title: 'Existing Prometheus & registry',
      description:
        'Point the agent at an existing Prometheus URL (required for OpenCost if you disable the built-in stack), or override the image registry for air-gapped and on-prem installs.',
      side: 'top',
      align: 'start',
    },
    {
      element: '#panel-shell',
      title: 'Run this on your cluster',
      description:
        'The Shell Script (recommended) auto-discovers existing Prometheus and Loki and installs anything missing — copy it and run it on any machine with kubectl access. The key here is a sample; your real one appears after you create the account.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#tab-helm',
      title: 'Prefer Helm?',
      description:
        'Switch to the Helm tab for the equivalent `helm upgrade --install` command with the same options — handy if you manage the agent through your own Helm workflow. Close this window when you’re done exploring.',
      side: 'bottom',
      align: 'start',
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
  // KG is behind a tenant feature flag; hide the guide when it's off (the tab it
  // drives isn't rendered, so the tour would otherwise dead-end on step 1).
  requiresFeature: 'TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH',
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

/**
 * "Connect an AWS account" walkthrough. AWS offers three methods via tabs, so
 * this tour walks the shared fields, the recommended CloudFormation path, then
 * clicks into the IAM Role ARN and Access Keys tabs to show their fields. It
 * never clicks "Connect via AWS Console" (navigates to AWS) or the final connect
 * button (real action) — those are pointed at for the user to take. Anchors
 * (all already present in the product):
 *   #add-aws-account-btn     → CloudAccountTile "Add AWS Account" button
 *   #account-name            → "Display Name" input
 *   #aws-access-mode         → Access Mode (Standard / Read-Only) radios
 *   #connect-aws-console-btn → CloudFormation "Connect via AWS Console" button
 *   #aws-tab-role-arn / #aws-tab-access-keys → the two manual method tabs
 *   #aws-role-arn / #aws-external-id          → IAM Role ARN tab fields
 *   #aws-access-key-id / #aws-secret-access-key / #aws-region → Access Keys fields
 *   #aws-connect-btn         → validate & connect (pointed at, not clicked)
 */
const connectAwsTour: TourDef = {
  id: 'connect-aws',
  title: 'Connect an AWS account',
  module: 'Accounts',
  description: 'Add an AWS account via 1-click CloudFormation, an IAM role, or access keys.',
  route: '/accounts/account-form?cloudProvider=AWS',
  requires: 'write',
  steps: [
    {
      element: '#add-aws-account-btn',
      title: 'Add an AWS account',
      description: 'Start here. Click Next and we’ll open the AWS connection form.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#add-aws-account-btn')?.click();
      },
    },
    {
      element: '#account-name',
      title: 'Name this account',
      description: 'A display name to identify this AWS account in Nudgebee.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#aws-access-mode',
      title: 'Access mode',
      description:
        'Standard grants the permissions Nudgebee needs to act (create CloudWatch alarms, track events, run remediations). Read-Only limits it to reading — those active features become unavailable.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#connect-aws-console-btn',
      title: '1-click CloudFormation (recommended)',
      description:
        'The easiest path: this launches a pre-filled CloudFormation stack in the AWS console. Create the stack and Nudgebee auto-detects the account when it finishes — no keys to copy. Or use the tabs above to connect manually.',
      side: 'top',
      align: 'start',
    },
    {
      element: '#aws-tab-role-arn',
      title: 'Method 2 — IAM Role ARN',
      description: 'Prefer a cross-account IAM role? Click Next to open this tab and we’ll show its fields.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#aws-tab-role-arn')?.click();
      },
    },
    {
      element: '#aws-role-arn',
      title: 'IAM Role ARN',
      description: 'The ARN of the cross-account role Nudgebee assumes to read your account, e.g. arn:aws:iam::123456789012:role/NudgebeeRole.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#aws-external-id',
      title: 'External ID',
      description: 'Optional. Fill this in only if the role’s trust policy requires an External ID; otherwise leave it blank.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#aws-tab-access-keys',
      title: 'Method 3 — Access Keys',
      description: 'No role available? Click Next to open the Access Keys tab and connect with an IAM user’s keys instead.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#aws-tab-access-keys')?.click();
      },
    },
    {
      element: '#aws-access-key-id',
      title: 'Access Key ID',
      description: 'The public ID of the IAM user’s key pair (starts with AKIA).',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#aws-secret-access-key',
      title: 'Secret Access Key',
      description: 'The secret half of the key pair. Nudgebee stores it encrypted at rest.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#aws-region',
      title: 'AWS Region',
      description: 'Region used to bootstrap the AWS SDK (defaults to us-east-1). Cost & Usage Report discovery always runs in us-east-1.',
      side: 'top',
      align: 'start',
    },
    {
      element: '#aws-connect-btn',
      title: 'Validate & connect',
      description:
        'Validate first to catch permission gaps (STS, Cost & Usage Report, S3 access), then click Connect yourself to create the account and start collecting data.',
      side: 'top',
      align: 'end',
    },
  ],
};

/**
 * "Connect a GCP account" walkthrough. Like connect-cluster, this previews the
 * later steps without real data: AddGcpAccountModal detects this tour and lets
 * the stepper advance (Service Account → Projects → Billing) without filling the
 * form or calling the discovery API, then closes itself when the guide ends.
 * Anchors (all already present in the product):
 *   #add-gcp-account-btn   → CloudAccountTile "Add GCP Account" button
 *   #account-name          → "Display Name" input
 *   #service-account-key   → service-account JSON textarea
 *   #check-permissions-btn → optional permission pre-check
 *   #next-step1 / #next-step2 → stepper Next buttons (clicked to preview)
 *   #discover-projects-btn → Projects step
 *   #billing-project-id    → Billing step
 */
const connectGcpTour: TourDef = {
  id: 'connect-gcp',
  title: 'Connect a GCP account',
  module: 'Accounts',
  description: 'Add a Google Cloud account with a service-account key.',
  route: '/accounts/account-form?cloudProvider=GCP',
  requires: 'write',
  steps: [
    {
      element: '#add-gcp-account-btn',
      title: 'Add a GCP account',
      description: 'Start here. Click Next and we’ll open the GCP connection form.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#add-gcp-account-btn')?.click();
      },
    },
    {
      element: '#account-name',
      title: 'Name this account',
      description: 'A display name to identify this Google Cloud account in Nudgebee.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#service-account-key',
      title: 'Paste your service-account key',
      description: 'Paste the full JSON key for your GCP service account — Nudgebee uses it to read your projects’ resources.',
      side: 'top',
      align: 'start',
    },
    {
      element: '#check-permissions-btn',
      title: 'Check permissions',
      description: 'Optional. Verify the service account has the IAM permissions Nudgebee needs before you continue.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#next-step1',
      title: 'On to projects',
      description: 'Click Next — we’ll open the Projects step. In a real setup Nudgebee discovers the projects this key can access.',
      side: 'top',
      align: 'end',
      // Preview: the modal advances the stepper without creating anything.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#next-step1')?.click();
      },
    },
    {
      element: '#discover-projects-btn',
      title: 'Pick your projects',
      description:
        'This is where you choose which projects to monitor — auto-discover them from the service account, or switch to Manual Entry to type project IDs. Click Next to continue to Billing.',
      side: 'bottom',
      align: 'start',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#next-step2')?.click();
      },
    },
    {
      element: '#billing-project-id',
      title: 'Billing export',
      description:
        'Finally, point Nudgebee at your BigQuery billing export — project, dataset, and table — so it can pull GCP cost data. That’s the whole flow; close this window when you’re done exploring.',
      side: 'bottom',
      align: 'start',
    },
  ],
};

/**
 * "Connect an Azure account" walkthrough. Like connect-cluster, this previews the
 * later steps without real data: AddAzureAccountModal detects this tour and lets
 * the stepper advance (Credentials → Subscriptions → Review) without validating
 * credentials or listing subscriptions, then closes itself when the guide ends.
 * Anchors (all already present in the product):
 *   #add-azure-account-btn → CloudAccountTile "Add Azure Account" button
 *   #account-name          → "Display Name" input
 *   #tenant-id / #client-id / #client-secret → service-principal credentials
 *   #next-to-subscriptions / #next-to-review → stepper Next buttons (clicked to preview)
 *   #discover-subscriptions → Subscriptions step
 *   #onboard-subscriptions  → Review step (pointed at, not clicked)
 */
const connectAzureTour: TourDef = {
  id: 'connect-azure',
  title: 'Connect an Azure account',
  module: 'Accounts',
  description: 'Add an Azure account with a service-principal (tenant / client / secret).',
  route: '/accounts/account-form?cloudProvider=Azure',
  requires: 'write',
  steps: [
    {
      element: '#add-azure-account-btn',
      title: 'Add an Azure account',
      description: 'Start here. Click Next and we’ll open the Azure connection form.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#add-azure-account-btn')?.click();
      },
    },
    {
      element: '#account-name',
      title: 'Name this account',
      description: 'A display name to identify this Azure account in Nudgebee.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#tenant-id',
      title: 'Directory (tenant) ID',
      description: 'Your Azure tenant’s ID — find it in the Azure portal under Microsoft Entra ID > Overview.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#client-id',
      title: 'Application (client) ID',
      description: 'The client ID of the service principal Nudgebee uses — App registrations > your app > Overview.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#client-secret',
      title: 'Client secret',
      description: 'The secret for that app registration (Certificates & secrets). Use the eye icon to check what you paste.',
      side: 'right',
      align: 'start',
    },
    {
      element: '#next-to-subscriptions',
      title: 'On to subscriptions',
      description: 'Click Next — we’ll open the Subscriptions step. In a real setup Nudgebee lists the subscriptions these credentials can read.',
      side: 'top',
      align: 'end',
      // Preview: the modal advances the stepper without creating anything.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#next-to-subscriptions')?.click();
      },
    },
    {
      element: '#discover-subscriptions',
      title: 'Pick your subscriptions',
      description: 'Discover the subscriptions your service principal can access and tick the ones to onboard. Click Next to review.',
      side: 'bottom',
      align: 'start',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#next-to-review')?.click();
      },
    },
    {
      element: '#onboard-subscriptions',
      title: 'Review & onboard',
      description:
        'A final summary of what will be onboarded. Click Onboard yourself to create the account(s). That’s the whole flow — close this window when you’re done exploring.',
      side: 'top',
      align: 'end',
    },
  ],
};

/*
 * Automations (auto-pilot) create guides. The "Create a New Automation" dialog
 * offers four build paths, and each is its own focused guide rather than one
 * long tour that has to reopen the dialog or navigate into the builder between
 * paths. Every guide starts on the Automations page, opens the dialog, then
 * walks one path. Anchors — the option cards + "Create Automation" button +
 * builder trigger cards (#wf-builder-trigger-option-*-card) pre-exist; the
 * sub-modal ids (#wf-code-*, #wf-ai-*, #wf-template-*) were added alongside
 * these guides. AI and templates are feature-gated, so those two guides declare
 * `requiresFeature` (WORKFLOWS / WORKFLOW_TEMPLATES) and hide where it's off.
 */
const automationFromCodeTour: TourDef = {
  id: 'automation-from-code',
  title: 'Create an automation from code',
  module: 'Automations',
  description: 'Import an automation by pasting its JSON or YAML definition.',
  route: '/auto-pilot',
  // The "Create Automation" button is write-gated (hasWriteAccess(accountId)).
  requires: 'write',
  steps: [
    {
      element: '#workflow-listing-create-btn',
      title: 'Create an automation',
      description: 'Start here — click Next to open the create dialog.',
      side: 'bottom',
      align: 'end',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#workflow-listing-create-btn')?.click();
      },
    },
    {
      element: '#wf-create-from-code-card',
      title: 'From code',
      description: 'Already have an automation as JSON or YAML? Click Next to open the code editor.',
      side: 'top',
      align: 'start',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#wf-create-from-code-card')?.click();
      },
    },
    {
      element: '#wf-code-format-tabs',
      title: 'Paste JSON or YAML',
      description: 'Paste or edit the definition, switching between the JSON and YAML formats here. Nudgebee validates it as you type.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#wf-code-create-btn',
      title: 'Create Automation',
      description: 'Click Create Automation to import it and open it in the editor.',
      side: 'top',
      align: 'end',
    },
  ],
};

const automationWithAiTour: TourDef = {
  id: 'automation-with-ai',
  title: 'Generate an automation with AI',
  module: 'Automations',
  description: 'Describe what you want in plain language and let AI draft the automation.',
  route: '/auto-pilot',
  requires: 'write',
  // The "Ask AI" card is enabled by the WORKFLOWS feature flag.
  requiresFeature: 'WORKFLOWS',
  steps: [
    {
      element: '#workflow-listing-create-btn',
      title: 'Create an automation',
      description: 'Start here — click Next to open the create dialog.',
      side: 'bottom',
      align: 'end',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#workflow-listing-create-btn')?.click();
      },
    },
    {
      element: '#wf-create-from-ai-card',
      title: 'Ask AI',
      description: 'Let AI draft the automation from a plain-language description. Click Next to open it.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#wf-create-from-ai-card')?.click();
      },
    },
    {
      element: '#wf-ai-prompt',
      title: 'Describe your automation',
      description:
        'Write what it should do — e.g. “notify the on-call channel when a pod keeps crash-looping”. The more specific, the better the draft.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#wf-ai-generate-btn',
      title: 'Generate Automation',
      description: 'Click Generate Automation and review the draft AI produces before saving.',
      side: 'top',
      align: 'end',
    },
  ],
};

const automationFromTemplateTour: TourDef = {
  id: 'automation-from-template',
  title: 'Use an automation template',
  module: 'Automations',
  description: 'Start from a pre-built automation for a common scenario.',
  route: '/auto-pilot',
  requires: 'write',
  // The "From a template" card is enabled by the WORKFLOW_TEMPLATES flag.
  requiresFeature: 'WORKFLOW_TEMPLATES',
  steps: [
    {
      element: '#workflow-listing-create-btn',
      title: 'Create an automation',
      description: 'Start here — click Next to open the create dialog.',
      side: 'bottom',
      align: 'end',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#workflow-listing-create-btn')?.click();
      },
    },
    {
      element: '#wf-create-from-template-card',
      title: 'From a template',
      description: 'Start from a proven automation for a common scenario. Click Next to open the template gallery.',
      side: 'bottom',
      align: 'center',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#wf-create-from-template-card')?.click();
      },
    },
    {
      element: '#wf-template-categories',
      title: 'Browse pre-built templates',
      description: 'Filter by category (Kubernetes, Monitoring, Security…) and tag (restart, scale, read-only…) to find one that fits.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#wf-template-use-first',
      title: 'Use Template',
      description: 'Each card previews what the automation does. Click Use Template to open it in the editor, pre-filled and ready to tweak.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#wf-template-scratch-btn',
      title: 'Or start blank',
      description: 'Not the right fit? Create from scratch opens an empty automation in the editor instead.',
      side: 'top',
      align: 'end',
      optional: true,
    },
  ],
};

const automationFromScratchTour: TourDef = {
  id: 'automation-from-scratch',
  title: 'Build an automation from scratch',
  module: 'Automations',
  description: 'Open the builder and pick a starting trigger for a blank automation.',
  route: '/auto-pilot',
  requires: 'write',
  steps: [
    {
      element: '#workflow-listing-create-btn',
      title: 'Create an automation',
      description: 'Start here — click Next to open the create dialog.',
      side: 'bottom',
      align: 'end',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#workflow-listing-create-btn')?.click();
      },
    },
    {
      element: '#wf-create-from-scratch-card',
      title: 'From scratch',
      description: 'Build it yourself in the visual editor. Click Next to open the builder.',
      side: 'bottom',
      align: 'start',
      // Navigates to /workflow/new — the builder's trigger picker mounts next.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#wf-create-from-scratch-card')?.click();
      },
    },
    {
      element: '#wf-builder-trigger-option-manual-card',
      title: 'Pick a starting trigger',
      description: 'Every automation begins with a trigger. Manual Trigger runs it on demand — one click, whenever you need it.',
      side: 'bottom',
      align: 'center',
    },
    {
      element: '#wf-builder-trigger-option-webhook-card',
      title: 'Webhook',
      description: 'Kick it off from an external system via an HTTP endpoint Nudgebee gives you.',
      side: 'bottom',
      align: 'center',
    },
    {
      element: '#wf-builder-trigger-option-schedule-card',
      title: 'Schedule',
      description: 'Run it on a time-based schedule — nightly, hourly, or any cron-like cadence.',
      side: 'bottom',
      align: 'center',
    },
    {
      element: '#wf-builder-trigger-option-event-card',
      title: 'Event Trigger',
      description: 'React automatically to events — a specific alert firing, a pod crash-looping, and so on.',
      side: 'bottom',
      align: 'center',
    },
    {
      element: '#wf-builder-trigger-option-optimization-card',
      title: 'Optimization',
      description: 'Run whenever Nudgebee surfaces a new optimization recommendation. Pick any trigger to drop it on the canvas and start building.',
      side: 'bottom',
      align: 'center',
    },
  ],
};

export const TOURS: Record<string, TourDef> = {
  [createUserTour.id]: createUserTour,
  [connectClusterTour.id]: connectClusterTour,
  [connectAwsTour.id]: connectAwsTour,
  [connectGcpTour.id]: connectGcpTour,
  [connectAzureTour.id]: connectAzureTour,
  [appOverviewTour.id]: appOverviewTour,
  [troubleshootTour.id]: troubleshootTour,
  [knowledgeGraphTour.id]: knowledgeGraphTour,
  [automationFromCodeTour.id]: automationFromCodeTour,
  [automationWithAiTour.id]: automationWithAiTour,
  [automationFromTemplateTour.id]: automationFromTemplateTour,
  [automationFromScratchTour.id]: automationFromScratchTour,
};
