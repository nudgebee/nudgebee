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
import { getAssistantName, getBrandTitle } from '@hooks/useTenantBranding';

/**
 * Resolves the `{brand}` and `{assistant}` tokens in tour copy to the tenant's
 * brand and assistant names.
 *
 * Tour text carries tokens rather than product names so a white-label deploy
 * doesn't tell its users to install someone else's agent, or point them at an
 * assistant under a name their UI never shows. `{assistant}` is separate from
 * `{brand}` because the two differ per tenant — the assistant is labelled from
 * `assistantName`, not the product name. The tokens can't be interpolated
 * where the strings are declared: this
 * module is evaluated at import time, before `/api/public/app_config` has
 * resolved, so the literals would bake in the fallbacks. Every surface that
 * renders tour copy passes it through here at render time instead — see
 * `TourProvider`, `GuidesMenu`, `TourLauncher`, `FirstLoginTour`,
 * `SectionFirstVisitTour`.
 */
export const brandText = (text: string): string => text.replaceAll('{brand}', getBrandTitle()).replaceAll('{assistant}', getAssistantName());

export type TourSide = 'top' | 'right' | 'bottom' | 'left';
export type TourAlign = 'start' | 'center' | 'end';

/**
 * Access capability a guide requires. A guide walks the user through *doing*
 * something, so it should only be offered to users who can actually do it —
 * otherwise the tour dead-ends on a role-gated button that isn't rendered.
 *
 * The app has two distinct write gates and they are NOT interchangeable — pick
 * the one the guide's target button actually uses:
 *   'write'         → tenant-scoped, mirroring a bare `hasWriteAccess()` gate
 *                     (e.g. "Add New User"). Note this effectively means
 *                     tenant_admin only: with no accountId, `hasWriteAccess()`
 *                     can only return true via its tenant_admin branch.
 *   'account-write' → account-scoped, mirroring a `hasWriteAccess(accountId)`
 *                     gate (e.g. "Create Automation"). account_admins pass this
 *                     but NOT 'write', so using 'write' for an account-scoped
 *                     button hides the guide from users who can see the button.
 * Omit `requires` for guides everyone can run — the app overview, or a
 * read-only orientation tour that drives no gated action.
 * Resolved by `canAccessTour` in ./tourAccess.
 */
export type TourCapability = 'write' | 'account-write';

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
  /**
   * Role capability required to run this guide. When set, the guide is hidden
   * from the central Guides catalog (and any `TourLauncher`) for users who lack
   * it. Omit to offer the guide to everyone. See `canAccessTour`.
   */
  requires?: TourCapability;
  /**
   * Tenant feature flag (a `feature_id`) this guide needs — e.g. the "Ask AI"
   * guide, whose card only renders when WORKFLOWS is on. Resolved via the cached
   * feature flags (`hasFeatureAccessCached`); see `canAccessTour`.
   *
   * ONLY for opt-in flags (absent row = feature off). `hasFeatureAccessCached`
   * is fail-closed: it needs an explicit `status === 'enabled'` row, so an
   * opt-OUT flag (default-on, absent row = feature ON) would hide the guide from
   * exactly the tenants that have the feature. Gate those guides on nothing —
   * their surface renders for everyone anyway.
   *
   * Callers must warm the flag cache (`fetchFeatureFlagsForTenant`) before
   * evaluating, then re-render — GuidesMenu, SectionFirstVisitTour and the
   * product-updates drawer do. A bare `TourLauncher` does NOT, so don't reach
   * for `requiresFeature` on a guide launched only from one.
   */
  requiresFeature?: string;
  /**
   * Deployment-level UI toggle (a `UI_ENABLE_*` env var) this guide's surface
   * needs — e.g. the AI Gateway tab, which only renders when the pod has
   * UI_ENABLE_LLM_GATEWAY=true.
   *
   * Distinct from `requiresFeature`, and not interchangeable with it: these are
   * per-DEPLOYMENT, not per-tenant, so they aren't in `featureflags_list` and
   * `hasFeatureAccessCached` can never see them. Resolved via
   * `isUiFeatureEnabled`, which reads the session — sync, and needs no warming.
   */
  requiresUiFeature?: 'llmGateway';
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
  description: 'Add a Kubernetes cluster and install the {brand} agent.',
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
      description: 'A unique, descriptive name to identify this Kubernetes account in {brand}.',
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
    title: '🎉 Welcome to {brand} 🎉',
    description:
      "Run, troubleshoot, and optimize everything in one place. Here's a quick tour of the sidebar so you know where to find what — it takes under a minute.",
  },
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
      element: '[data-testid="nav-bcortex-btn"]',
      title: 'Nubi',
      description:
        'Nubi is the AI that runs alongside you — ask it anything from Home, or about any finding you’re looking at. b-Cortex is its memory: what it has learned about your estate and past incidents, so its answers get sharper over time.',
      side: 'right',
      align: 'start',
      // Hidden in OSS deployments (NUDGEBEE_DEPLOYMENT_MODE=oss).
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
 *   #tab-all-events          → the "Events" sub-tab (explicit `id` set on that
 *                              tabOptions entry in troubleshoot/index.jsx, since
 *                              Tabs' default id=`tab-<value>` uses a numeric,
 *                              position-dependent value there)
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
        document.querySelector<HTMLElement>('#tab-all-events')?.click();
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
 * "Explore Investigations" orientation. Investigations is the second Troubleshoot
 * view (SearchBlueIcon top tab) — the auto & manual root-cause analyses. Like
 * troubleshoot-overview it's read-only: it walks the two sub-tabs and their
 * listings. Anchors:
 *   [id="anchor-tab-Investigations"] → the Investigations top tab
 *   #tab-auto-investigated / #tab-manual-investigated → the two sub-tabs
 *                                    (Tabs applies each tabOption's `id`)
 *   #autoInvestigatedTable / #manualInvestigatedTable → the two listings
 *                                    (CustomTable id; optional — empty on a fresh
 *                                    tenant, so the step skips instead of ending
 *                                    the tour)
 * Step 1 opens the Investigations tab so the tour is robust when launched from
 * another view; steps 2/4 switch sub-tabs on the always-present tab buttons so
 * the (optional) table steps land on the right listing.
 */
const investigationsTour: TourDef = {
  id: 'investigations',
  title: 'Explore Investigations',
  module: 'Troubleshoot',
  description: 'Tour the automatic and manual root-cause analyses {brand} keeps for your incidents.',
  route: '/troubleshoot',
  steps: [
    {
      element: '[id="anchor-tab-Investigations"]',
      title: 'Root-cause investigations',
      description:
        'Every root-cause analysis lands here — the ones {brand} runs automatically when an incident fires, and the ones you start yourself. Let’s look at both.',
      side: 'bottom',
      align: 'start',
      // Make sure we're on the Investigations view before pointing at its sub-tabs.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('[id="anchor-tab-Investigations"]')?.click();
      },
    },
    {
      element: '#tab-auto-investigated',
      title: 'Auto Investigated',
      description:
        'When a significant incident fires, {brand} investigates it on its own — correlating pods, logs, metrics, and the service map into a root cause. We’ll open that list.',
      side: 'bottom',
      align: 'start',
      // Ensure the Auto listing is the one shown for the next step.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#tab-auto-investigated')?.click();
      },
    },
    {
      element: '#autoInvestigatedTable',
      title: 'Automatic root-cause analyses',
      description: 'Each row is an incident {brand} analysed for you. Open one for the full root cause, the evidence behind it, and the timeline.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#tab-manual-investigated',
      title: 'Manual Investigated',
      description: 'These are the deep-dives you kick off yourself from Ask {assistant}. We’ll open that list.',
      side: 'bottom',
      align: 'start',
      // Switch to the Manual listing for the final step.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#tab-manual-investigated')?.click();
      },
    },
    {
      element: '#manualInvestigatedTable',
      title: 'Your manual investigations',
      description: 'Every analysis you started in Ask {assistant} is saved here — reopen any session to pick up exactly where you left off.',
      side: 'top',
      align: 'center',
      optional: true,
    },
  ],
};

/**
 * "Explore the Knowledge Graph" orientation. Knowledge Graph is the third
 * Troubleshoot view (feature-flagged) — a live ReactFlow service map, not a
 * table — so this tour teaches how to read and drive the graph. Anchors:
 *   [id="anchor-tab-Knowledge Graph"] → the KG top tab (opens selectedFilter=2)
 *   #kg-canvas              → the graph canvas (graphWrapperRef Box). Note it also
 *                             hosts the "Graph Too Large to Render" state, so the
 *                             step is safe even when the graph won't draw.
 *   #kg-filter-panel        → the left filter sidebar (WidgetCard)
 *   #auto-complete-kg-filter-{account,node-type,node,level}
 *                           → the four filter dropdowns. FilterDropdown re-emits
 *                             its `id` as `auto-complete-<kebab(id)>`; the explicit
 *                             `id` props were added with this guide, since these
 *                             previously fell back to `label` (`#auto-complete-account`
 *                             etc.) — ids derived from display copy, which a wording
 *                             change would silently break.
 *   #kg-filter-label-attribute → Box wrapping the Label + Attribute query builders
 *                                (LogQueryBuilderAutocomplete takes no id)
 *   [data-testid="kg-apply-filters-btn"] → Apply Filters (disabled until edited)
 *   #auto-complete-kg-node-search → node search in the top-right toolbar
 *   #relationship-types-btn → the Relationships legend button (hover shows legend)
 *   #kg-settings-btn        → the Settings button (tenant admins only)
 * Step 1 opens the KG tab so the rest can anchor its elements.
 *
 * The filter panel unmounts entirely when collapsed (`!isFilterCollapsed &&`), so
 * the canvas step re-opens it via `onBeforeNext` and every panel-internal step is
 * `optional` — if that click ever fails, the guide skips the filters and carries on
 * to search/relationships/settings instead of dead-ending. `optional`'s short wait
 * is safe here because expanding is synchronous React state, not a fetch.
 *
 * Deliberately ungated. TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH is an OPT-OUT flag
 * enforced server-side by the nightly build cron, so the KG tab is always
 * rendered (see pages/troubleshoot/index.jsx) and every user can run this guide.
 * Declaring `requiresFeature` here would invert that: `hasFeatureAccessCached`
 * needs an explicit `status === 'enabled'` row, which default-on tenants don't
 * have — hiding the guide from everyone who has the feature.
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
        'Every node is a service, workload, or cloud resource; every edge is a real dependency {brand} discovered between them. Drag to pan, scroll to zoom. Too many nodes to draw? The filters come next.',
      side: 'top',
      align: 'center',
      // Re-open the filter sidebar if the user has it collapsed, so the filter
      // steps below have something to anchor to. The button only exists while
      // collapsed, so this is a no-op when the panel is already open.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('[data-testid="kg-expand-filters-btn"]')?.click();
      },
    },
    {
      element: '#kg-filter-panel',
      title: 'Scope the map',
      description:
        'The graph only draws up to 1,500 nodes, and a real estate is far bigger — so filtering isn’t optional here, it’s how you use the map. Let’s walk the controls.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-kg-filter-account',
      title: 'Account',
      description: 'Scope the map to specific cloud accounts. Leave it empty to include every connected AWS, GCP, Azure and Kubernetes account.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-kg-filter-node-type',
      title: 'Node Type',
      description:
        'Restrict the map to certain kinds of resource — Workload, Service, EC2 Instance, VPC, and so on. Pair it with Account to isolate a slice of your infrastructure.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-kg-filter-node',
      title: 'Node',
      description:
        'Start from specific nodes. Type-ahead matches on name, namespace, region and type — the map is then built outwards from what you pick.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-kg-filter-level',
      title: 'Level — how far to expand',
      description:
        'Traversal depth: how many relationship hops to follow out from the matched nodes. Level 1 shows direct neighbours only; each level beyond that grows the graph fast. This is your main lever when it won’t render.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '#kg-filter-label-attribute',
      title: 'Label & Attribute',
      description:
        'Filter on the metadata itself — match nodes by a label (e.g. env=prod) or by an attribute of the resource. Useful for slicing one team or environment out of a shared cluster.',
      side: 'right',
      align: 'start',
      optional: true,
    },
    {
      element: '[data-testid="kg-apply-filters-btn"]',
      title: 'Apply your filters',
      description: 'Nothing changes until you apply — the button wakes up once you’ve edited a filter. Clear All resets the map back to everything.',
      side: 'right',
      align: 'end',
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
      description: 'A display name to identify this AWS account in {brand}.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#aws-access-mode',
      title: 'Access mode',
      description:
        'Standard grants the permissions {brand} needs to act (create CloudWatch alarms, track events, run remediations). Read-Only limits it to reading — those active features become unavailable.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#connect-aws-console-btn',
      title: '1-click CloudFormation (recommended)',
      description:
        'The easiest path: this launches a pre-filled CloudFormation stack in the AWS console. Create the stack and {brand} auto-detects the account when it finishes — no keys to copy. Or use the tabs above to connect manually.',
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
      description: 'The ARN of the cross-account role {brand} assumes to read your account, e.g. arn:aws:iam::123456789012:role/NudgebeeRole.',
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
      description: 'The secret half of the key pair. {brand} stores it encrypted at rest.',
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
      description: 'A display name to identify this Google Cloud account in {brand}.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#service-account-key',
      title: 'Paste your service-account key',
      description: 'Paste the full JSON key for your GCP service account — {brand} uses it to read your projects’ resources.',
      side: 'top',
      align: 'start',
    },
    {
      element: '#check-permissions-btn',
      title: 'Check permissions',
      description: 'Optional. Verify the service account has the IAM permissions {brand} needs before you continue.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#next-step1',
      title: 'On to projects',
      description: 'Click Next — we’ll open the Projects step. In a real setup {brand} discovers the projects this key can access.',
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
        'Finally, point {brand} at your BigQuery billing export — project, dataset, and table — so it can pull GCP cost data. That’s the whole flow; close this window when you’re done exploring.',
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
      description: 'A display name to identify this Azure account in {brand}.',
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
      description: 'The client ID of the service principal {brand} uses — App registrations > your app > Overview.',
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
      description: 'Click Next — we’ll open the Subscriptions step. In a real setup {brand} lists the subscriptions these credentials can read.',
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
 *
 * All four are 'account-write': the "Create Automation" button they drive is
 * gated on `hasWriteAccess(accountId)` (WorkflowListing.tsx), which account
 * admins pass — so 'write' (tenant-scoped) would hide these guides from users
 * who can see the button.
 */
const automationFromCodeTour: TourDef = {
  id: 'automation-from-code',
  title: 'Create an automation from code',
  module: 'Automations',
  description: 'Import an automation by pasting its JSON or YAML definition.',
  route: '/automation',
  requires: 'account-write',
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
      description: 'Paste or edit the definition, switching between the JSON and YAML formats here. {brand} validates it as you type.',
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
  route: '/automation',
  requires: 'account-write',
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
  route: '/automation',
  requires: 'account-write',
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
  route: '/automation',
  requires: 'account-write',
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
      // Navigates to /automation/new — the builder's trigger picker mounts next.
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
      description: 'Kick it off from an external system via an HTTP endpoint {brand} gives you.',
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
      description: 'Run whenever {brand} surfaces a new optimization recommendation. Pick any trigger to drop it on the canvas and start building.',
      side: 'bottom',
      align: 'center',
    },
  ],
};

/**
 * "Explore Optimize" orientation. Read-only, like `troubleshoot-overview`: it
 * explains the tabs and the Recommendations toolbar rather than driving the
 * Resolve → Deploy Fix flow, which needs write access AND a row that happens to
 * be a pod-right-sizing recommendation (OptimizeNewPage.tsx) — data no guide can
 * count on. Ungated for the same reason: no step touches a role-gated control,
 * and the page itself only gates on session presence.
 *
 * `route` is '/optimise' with NO hash on purpose. useLaunchGuide compares base
 * paths and ignores the fragment, so a '/optimise#recommendations' route would
 * skip navigation for a user already on '/optimise#summary' — starting the tour
 * on Summary, where these anchors don't exist. Step 2 clicks through to
 * Recommendations instead, which also makes the guide robust when launched from
 * any tab.
 *
 * Anchors:
 *   #anchor-tab-summary|recommendations|resolutions|auto-optimize
 *                          → AnchorComponent renders id=`anchor-tab-<opt.id>`
 *   #optimize-card-savings → the "Total Savings" WidgetCard
 *   #optimize-card-all     → the "All Recommendations" card — the category
 *                            stat-cards double as tabs (the category chips and
 *                            the Top Issues band were folded into the cards +
 *                            the Rules filter in the filter-bar redesign)
 *   [data-testid="severity-summary-bar"] → the severity + safety chip bar
 *   #auto-complete-optimize-account-filter
 *                          → FilterDropdown rewrites `id` to `auto-complete-<id>`
 *   #auto-complete-optimize-rules-filter → the Rules filter (grouped by category)
 *   #optimize-search       → the search input
 *   #optimize-recommendations-table → the recommendations table (renders even
 *                                     while loading/empty, so it's a safe anchor)
 *   #optimize-download     → CSV export
 */
const optimizeTour: TourDef = {
  id: 'optimize-overview',
  title: 'Explore Optimize',
  module: 'Optimize',
  description: 'Find savings — how recommendations are grouped, filtered, and tracked to resolution.',
  route: '/optimise',
  steps: [
    {
      element: '#anchor-tab-summary',
      title: 'Start with the Summary',
      description: 'Your savings at a glance — the biggest opportunities across every connected account and cluster, grouped by category.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#anchor-tab-recommendations',
      title: 'Recommendations',
      description: 'The full list of findings. Let’s open it.',
      side: 'bottom',
      align: 'start',
      // Switch to the Recommendations tab so the following steps can anchor it.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#anchor-tab-recommendations')?.click();
      },
    },
    {
      element: '#optimize-card-savings',
      title: 'Your total savings',
      description: 'Estimated monthly savings if every recommendation here were applied.',
      side: 'bottom',
      align: 'end',
    },
    {
      element: '#optimize-card-all',
      title: 'The cards are your category tabs',
      description:
        'Each category card filters the list to that kind of change (Right Sizing, Config, Spot…) — click one to narrow, click it again to unselect. This card brings back everything.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '[data-testid="severity-summary-bar"]',
      title: 'Filter by severity & safety',
      description:
        'Two levers — Severity for how urgent a finding is, Safety for its blast radius (Safe means no dependents in the way). Start with Critical and High — that’s where the money and the risk usually are.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-optimize-account-filter',
      title: 'Scope to an account',
      description: 'Narrow the list to a single cloud account or cluster.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-optimize-rules-filter',
      title: 'Filter by rule',
      description:
        'Every rule with open findings, grouped by category and sorted by how often it fires. Pick one to fix a whole class of problem at once — Savings and Last seen next to it narrow by value and freshness.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#optimize-search',
      title: 'Search',
      description: 'Look up a specific workload or resource by name.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#optimize-recommendations-table',
      title: 'The recommendations',
      description:
        'One row per finding, with its estimated saving. Click a row to open the details panel — that’s where you review the change, ask Nubi about it, raise a ticket, or apply it.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#optimize-download',
      title: 'Export',
      description: 'Download the current, filtered list as CSV — handy for sharing a plan with the team.',
      side: 'left',
      align: 'start',
    },
    {
      element: '#anchor-tab-resolutions',
      title: 'Track what you fixed',
      description: 'Every recommendation you’ve actioned, and what happened to it — your record of realised savings.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#anchor-tab-auto-optimize',
      title: 'Automate it',
      description: 'Let {brand} apply right-sizing continuously instead of one row at a time, with approvals if you want a human in the loop.',
      side: 'bottom',
      align: 'start',
    },
  ],
};

/**
 * Optimize deep-dives. `optimizeTour` is the module orientation and points at the
 * tabs; these three walk INTO one tab each — same split as the four Automations
 * guides. All read-only, so all ungated (tabs 0–3 carry no role gate).
 *
 * No graph steps anywhere, deliberately: Summary, Recommendations, Resolutions and
 * Auto Optimize contain no chart at all. The only charts in optimise-new live in
 * the recommendation detail drawer (evidence/RightSizingEvidence), and they pass no
 * `id` to Chart.Line — LineCharts falls back to `uuidv4()`, so the canvas id is
 * random per render and unanchorable. Their `[data-testid="cpu-trend-card"]`
 * wrappers exist but only for K8s pod-right-sizing recs behind a row click, which
 * is too data-dependent to build a step on.
 *
 * Anchors for "Optimize: Summary" (#summary-savings-card, #summary-filter-category,
 * #summary-filter-provider and #summary-view-toggle were added with this guide —
 * Card/ToggleGroup already forwarded `id`; FilterFacet gained an optional one):
 *   #anchor-tab-summary                    → the tab
 *   #summary-savings-card                  → headline savings + freshness Card
 *   #summary-filter-category / -provider   → the two chip facets
 *   #auto-complete-account-filter-select   → Account (FilterDropdown rewrites its id)
 *   #top3-open-autopilot / #sort-toggle / #ask-nubi-footer
 *                                          → all render only once findings load and
 *                                            are non-empty → optional
 *   #summary-view-toggle                   → cards/list switch
 * Note the findings table (#summary-findings-table) is NOT spotlit: viewMode
 * defaults to 'cards', so it isn't mounted on landing. The view-toggle step covers
 * it. And #category-section-<category> is a decoy — InsightSection passes that id to
 * CollapsableCard, which only uses it as a localStorage key and never renders it.
 */
const optimizeSummaryTour: TourDef = {
  id: 'optimize-summary',
  title: 'Optimize: the Summary tab',
  module: 'Optimize',
  description: 'Read the savings headline, then slice it by category, provider, and account.',
  route: '/optimise',
  steps: [
    {
      element: '#anchor-tab-summary',
      title: 'The Summary tab',
      description: 'Where Optimize opens — the whole savings picture before you drill into individual recommendations.',
      side: 'bottom',
      align: 'start',
      // Summary is the default tab, but re-assert it so the guide works when
      // launched from Recommendations or any other tab.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#anchor-tab-summary')?.click();
      },
    },
    {
      element: '#summary-savings-card',
      title: 'Your savings headline',
      description: 'Total potential monthly savings across everything connected, plus how many findings back it and how fresh the numbers are.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#summary-filter-category',
      title: 'Filter by category',
      description: 'Cost, Performance, or Security & Config — pick the kind of problem you’re here to solve. All is the default.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#summary-filter-provider',
      title: 'Filter by provider',
      description: 'Narrow to AWS, Azure, GCP, or Kubernetes when you only own one slice of the estate.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-account-filter-select',
      title: 'Filter by account',
      description: 'Scope everything below to one cloud account or cluster.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#top3-open-autopilot',
      title: 'Start with the top 3',
      description: '{brand} picks the three findings worth your time first. Open the Autopilot queue to let automations handle the repetitive ones.',
      side: 'top',
      align: 'end',
      // Only renders once findings have loaded and there are more than the top 3.
      optional: true,
    },
    {
      element: '#sort-toggle',
      title: 'Sort the findings',
      description: 'Order by savings, age, confidence, or resource — savings first is usually the right call.',
      side: 'bottom',
      align: 'end',
      // Renders only once findings have loaded and are non-empty.
      optional: true,
    },
    {
      element: '#summary-view-toggle',
      title: 'Cards or list',
      description: 'Cards read better when you’re exploring; switch to list for a dense, sortable table of every finding.',
      side: 'bottom',
      align: 'end',
    },
    {
      element: '#ask-nubi-footer',
      title: 'Ask Nubi',
      description: 'Not sure what a finding means or whether it’s safe to apply? Ask Nubi about any of it in plain language.',
      side: 'top',
      align: 'center',
      optional: true,
    },
  ],
};

/**
 * "Optimize: Resolutions" deep-dive. Zero new ids — every anchor already ships, and
 * all of them render unconditionally (the four FilterDropdown triggers render even
 * with no options, and CustomTable renders its <table> even when empty).
 * Anchors:
 *   #anchor-tab-resolutions → the tab; step 1 clicks it because the view is
 *                             unmounted until the tab is active
 *   #auto-complete-resolutions-filter-{account,status,recommendation,resolver}
 *                           → the four filters (FilterDropdown rewrites `id` to
 *                             `auto-complete-<kebab(id)>`)
 *   #optimise-resolutions   → the resolutions table
 *   #optimise-resolutions-download → CSV export
 */
const optimizeResolutionsTour: TourDef = {
  id: 'optimize-resolutions',
  title: 'Optimize: the Resolutions tab',
  module: 'Optimize',
  description: 'Track what you’ve actioned — and filter by status, recommendation, or who resolved it.',
  route: '/optimise',
  steps: [
    {
      element: '#anchor-tab-resolutions',
      title: 'The Resolutions tab',
      description: 'Your audit trail — every recommendation that’s been actioned, and what happened to it. Let’s open it.',
      side: 'bottom',
      align: 'start',
      // The view is unmounted until this tab is active, so the steps below need it.
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#anchor-tab-resolutions')?.click();
      },
    },
    {
      element: '#auto-complete-resolutions-filter-account',
      title: 'Filter by account',
      description: 'Scope the list to one cloud account or cluster.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-resolutions-filter-status',
      title: 'Filter by status',
      description: 'Did it land? Filter to the ones that succeeded, failed, or are still in flight.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-resolutions-filter-recommendation',
      title: 'Filter by recommendation',
      description: 'Narrow to a single kind of fix — useful when you’ve rolled out one change across many workloads.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-resolutions-filter-resolver',
      title: 'Filter by resolver',
      description: 'Who or what applied it — a teammate, or one of your automations.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#optimise-resolutions',
      title: 'The resolution history',
      description:
        'One row per action, with its status and the saving it realised. Expand a row to see exactly what ran — including the command and its output.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#optimise-resolutions-download',
      title: 'Export',
      description: 'Download the filtered history as CSV — handy for showing realised savings to whoever asked for them.',
      side: 'left',
      align: 'start',
    },
  ],
};

/**
 * "Optimize: Auto Optimize" deep-dive. Zero new ids.
 *
 * Sub-tab anchors are the raw `opt.id` — AnchorComponent renders sub-tabs through
 * Tabs' `a11yProps(value, opt.id)`, which uses `customId || \`tab-${index}\``, so the
 * declared ids pass through untouched: `#Optimizations` (capital O, as declared in
 * pages/optimise/index.jsx) and `#approvals`. They render with the tab, no hover
 * needed — the `#dropdown-<id>` variants only exist inside the hover popover, so
 * they're unusable here.
 *
 * `approvals` is ALSO the id of the approvals <table> (AutoPilotApprovalsTable) —
 * a genuine duplicate-id collision. Hence `[role="tab"]#approvals` rather than a
 * bare `#approvals`: MUI's Tab always sets role="tab", so this picks the sub-tab
 * without depending on the tab strip happening to render before the table. Note it
 * must NOT be narrowed to `button#approvals` — Tabs defaults to behavior='router'
 * and renders each tab as `component={Link}`, i.e. an <a>. The table is anchored
 * via #autopilot-approvals-listing-box to stay unambiguous.
 *
 * The listing steps anchor the wrapper Boxes (#box-layout-auto-pilot,
 * #autopilot-approvals-listing-box) rather than the <table> ids: CustomTable hands
 * its id to EmptyData when there are no rows, so `#auto-pilot` becomes
 * `#auto-pilot-no-data` on an empty tenant — exactly the tenant most likely to be
 * running this guide.
 */
const optimizeAutoOptimizeTour: TourDef = {
  id: 'optimize-auto-optimize',
  title: 'Optimize: Auto Optimize',
  module: 'Optimize',
  description: 'Let {brand} apply right-sizing for you — the automations, and the approvals queue.',
  route: '/optimise',
  steps: [
    {
      element: '#anchor-tab-auto-optimize',
      title: 'The Auto Optimize tab',
      description: 'Instead of applying recommendations one at a time, let {brand} keep workloads right-sized continuously. Let’s open it.',
      side: 'bottom',
      align: 'start',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#anchor-tab-auto-optimize')?.click();
      },
    },
    {
      element: '#Optimizations',
      title: 'Your automations',
      description: 'Every auto-optimization you’ve set up, and whether it’s currently active.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-auto-pilot-filter-status',
      title: 'Filter by status',
      description: 'Show only the automations that are enabled, or hunt down the ones that are paused.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-auto-pilot-filter-category',
      title: 'Filter by category',
      description: 'Narrow to a kind of automation — continuous, horizontal, vertical, or volume right-sizing.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-pilot-name-search',
      title: 'Search by name',
      description: 'Jump to a specific automation.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#box-layout-auto-pilot',
      title: 'The automation list',
      description: 'One row per automation, with its scope and schedule. Toggle one off here if you need to pause it.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '[role="tab"]#approvals',
      title: 'The approvals queue',
      description: 'Automations can ask before they act. Anything waiting on a human decision lands here. Let’s look.',
      side: 'bottom',
      align: 'start',
      optional: true,
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('[role="tab"]#approvals')?.click();
      },
    },
    {
      element: '#autopilot-approvals-listing-box',
      title: 'Approve or reject',
      description: 'Each pending change, what it would do, and the call you need to make — nothing here is applied until you say so.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#create-auto-optimize',
      title: 'Create an automation',
      description: 'Set up continuous, horizontal, vertical, or volume right-sizing. Pick a type and {brand} walks you through the scope.',
      side: 'left',
      align: 'end',
      // Write-gated (hasWriteAccess on the current account), so read-only users
      // never see it — the guide itself stays ungated for everyone else.
      optional: true,
    },
  ],
};

/**
 * "Optimize: LLM Analyser" deep-dive.
 *
 * Gated on `requiresFeature: 'LLM_ANALYSER'` — a per-tenant feature flag
 * (`featureflags_list`), resolved via the cached tenant flags
 * (`hasFeatureAccessCached`); see `canAccessTour`. Tenant admins toggle it from
 * the Feature Flags section of Tenant Settings.
 *
 * The tab's second gate is `hasReadAccess(selectedCluster?.value)`. That's not
 * resolvable from the global catalog (no cluster context there, same limitation as
 * 'account-write'), and it passes for anyone with read on the selected account —
 * the tenant flag is the gate that actually decides visibility.
 *
 * Anchors (all pre-existing):
 *   #anchor-tab-llm-analyser → the tab (id 'llm-analyser'; note its hash is
 *                              'cost-analyser' — fragment and id differ here)
 *   #cost-kpi-row / #cost-over-time / #cost-filter-bar / #cost-filter-reset
 *   #auto-complete-cost-filter-{account,model,user} → FilterDropdown rewrites ids
 *   #tab-conversations / #tab-models / #tab-agents → inner Tabs. These declare no
 *        `id`, so a11yProps falls back to `tab-${index}` where index is opt.value
 *        (a string) — hence #tab-models, not #tab-2. They use behavior='filter',
 *        so they're buttons that don't touch the URL: the guide must click them.
 * `isMounted` starts false, so the tab appears a tick after mount; the engine's
 * element-wait covers it.
 */
const llmAnalyserTour: TourDef = {
  id: 'llm-analyser',
  title: 'Optimize: the LLM Analyser',
  module: 'Optimize',
  description: 'See what your AI is costing you — by model, agent, conversation, and user.',
  route: '/optimise',
  requiresFeature: 'LLM_ANALYSER',
  steps: [
    {
      element: '#anchor-tab-llm-analyser',
      title: 'The LLM Analyser',
      description: 'Every LLM call {brand}’s agents make, priced and broken down. Let’s open it.',
      side: 'bottom',
      align: 'start',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#anchor-tab-llm-analyser')?.click();
      },
    },
    {
      element: '#cost-kpi-row',
      title: 'The headline numbers',
      description: 'Total spend, call volume, and token usage for the period and filters you’ve selected.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#cost-over-time',
      title: 'Spend over time',
      description: 'Where your AI spend is trending. A step change here usually means a new agent went live, or one started retrying.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#auto-complete-cost-filter-account',
      title: 'Filter by account',
      description: 'Scope the numbers to one cloud account or cluster.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#auto-complete-cost-filter-model',
      title: 'Filter by model',
      description: 'Compare what each model is costing you — often the fastest way to find an expensive default.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#cost-filter-reset',
      title: 'Reset',
      description: 'Clear every filter and go back to the whole picture.',
      side: 'bottom',
      align: 'end',
      optional: true,
    },
    {
      element: '#tab-conversations',
      title: 'Conversations',
      description: 'Drill into individual conversations — what was asked, what it cost, and how long it took.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#tab-models',
      title: 'Models',
      description: 'Per-model cost and call share over time.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#tab-agents',
      title: 'Agents',
      description: 'Which of {brand}’s agents are doing the spending, and how slow they are — the place to look when cost climbs without more users.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
  ],
};

/**
 * "Optimize: AI Gateway" deep-dive.
 *
 * Gated on `requiresUiFeature: 'llmGateway'` — a per-deployment toggle (the pod's
 * UI_ENABLE_LLM_GATEWAY env var, read in /optimise's getServerSideProps) that
 * never appears in `featureflags_list`, so the tenant-flag machinery can't see
 * it. The session carries it instead (see `uiFeatures`), which is what
 * `isUiFeatureEnabled` reads. Unlike the LLM Analyser's tab (see
 * llmAnalyserTour), this one hasn't been converted to a tenant feature flag.
 *
 * The Analyser covers {brand}'s own agent traffic; the Gateway covers your BYO-token
 * traffic forwarded through it, which is why they're separate guides.
 *
 * Anchors (all pre-existing):
 *   #anchor-tab-ai-gateway → the tab
 *   #tab-connect / #tab-overview / #tab-requests / #tab-models → inner Tabs
 *        (`tab-${opt.value}`; behavior='filter', so click — no URL change)
 *   #gateway-connect / #gateway-connect-generate-token-btn → the Connect pane
 *   #gateway-kpi-row / #gateway-usage-over-time → Overview
 *   #gateway-requests-table → the Requests log
 * Connect is the default landing view, so the guide starts there rather than
 * assuming Overview.
 */
const aiGatewayTour: TourDef = {
  id: 'ai-gateway',
  title: 'Optimize: the AI Gateway',
  module: 'Optimize',
  description: 'Route your own LLM traffic through {brand} — and see every request, model, and user.',
  route: '/optimise',
  requiresUiFeature: 'llmGateway',
  steps: [
    {
      element: '#anchor-tab-ai-gateway',
      title: 'The AI Gateway',
      description: 'Point your own apps at {brand}’s gateway and it prices, logs, and attributes every LLM call they make. Let’s open it.',
      side: 'bottom',
      align: 'start',
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#anchor-tab-ai-gateway')?.click();
      },
    },
    {
      element: '#gateway-connect',
      title: 'Connect your app',
      description: 'The base URL and a ready-made snippet for your language — point your SDK here instead of the provider and you’re done.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#gateway-connect-generate-token-btn',
      title: 'Get a token',
      description: 'Generate a gateway token to authenticate your traffic. Your own provider keys stay yours — the gateway forwards with them.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#tab-overview',
      title: 'Overview',
      description: 'Once traffic is flowing, this is the summary. Let’s look.',
      side: 'bottom',
      align: 'start',
      optional: true,
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('#tab-overview')?.click();
      },
    },
    {
      element: '#gateway-kpi-row',
      title: 'Usage at a glance',
      description: 'Spend, requests, and tokens across everything routed through the gateway.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#gateway-usage-over-time',
      title: 'Usage over time',
      description: 'The trend, so a runaway job or a retry loop shows up as a spike rather than a surprise bill.',
      side: 'top',
      align: 'center',
      optional: true,
    },
    {
      element: '#tab-requests',
      title: 'Every request',
      description: 'The full request log — filter by user, model, provider, or status to find the failing or expensive ones.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
    {
      element: '#tab-models',
      title: 'Models & users',
      description: 'Who and what is spending: per-model and per-user breakdowns, for chargeback or just for finding the outlier.',
      side: 'bottom',
      align: 'start',
      optional: true,
    },
  ],
};

/**
 * "Explore Tickets" orientation — the list and its filters.
 *
 * Deliberately UNGATED. Ticket creation is authorized for *_readonly roles
 * server-side (`tickets_create` in actions.yaml, mirrored by the read-access gate
 * on the "Create Ticket" affordances), and the Tickets page carries no role gate
 * at all — so `requires: 'write'` would hide this guide from exactly the
 * read-only users who can use the module.
 *
 * Scope note: /tickets has no create-ticket trigger of its own — every creation
 * entry point lives in another module's row menu — so a "create a ticket"
 * walkthrough can't be rooted here.
 *
 * Anchors (all pre-existing):
 *   [id="anchor-tab-All Tickets"] / [id="anchor-tab-Assigned to me"]
 *        → the tabOptions declare no `id`, so AnchorComponent falls back to
 *          `name` and the rendered ids contain a space. Hence the [id="…"] form
 *          rather than a `#` selector (same as All Events in troubleshootTour).
 *   #all-tickets           → the tickets table
 *   #auto-complete-ticket-filter-{account,priority,tool,status,assignee}
 *        → FilterDropdown rewrites `id` to `auto-complete-<id>`; the assignee
 *          filter only renders on All Tickets, so that step is optional
 *   #ticket-filter-title   → the title search input (a SearchInput, so its id is
 *                            NOT rewritten the way FilterDropdown's is)
 * The page body renders only once its tab state resolves (tab !== null), so the
 * first list anchor mounts a beat late — the engine's element-wait covers it.
 */
const ticketsTour: TourDef = {
  id: 'tickets-overview',
  title: 'Explore Tickets',
  module: 'Tickets',
  description: 'Track every ticket raised from {brand} — and find the one you need with filters.',
  route: '/tickets',
  steps: [
    {
      element: '[id="anchor-tab-All Tickets"]',
      title: 'All your tickets',
      description:
        'Every ticket raised from {brand} — from an incident, a recommendation, or by hand — kept in sync with the tool it lives in, like Jira or ServiceNow.',
      side: 'bottom',
      align: 'start',
      // Re-assert the All Tickets tab so the tour works when launched from
      // "Assigned to me" (whose toolbar hides the assignee filter).
      onBeforeNext: () => {
        document.querySelector<HTMLElement>('[id="anchor-tab-All Tickets"]')?.click();
      },
    },
    {
      element: '#all-tickets',
      title: 'The ticket list',
      description:
        'One row per ticket, with its status and priority. Click a row to expand it — and to open it in Jira, ServiceNow, or wherever it came from.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#auto-complete-ticket-filter-account',
      title: 'Scope to an account',
      description: 'Narrow the list to a single cloud account or cluster.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-ticket-filter-status',
      title: 'Filter by status',
      description: 'Hide what’s already closed and focus on what’s still open.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-ticket-filter-priority',
      title: 'Filter by priority',
      description: 'Surface the urgent ones first.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-ticket-filter-tool',
      title: 'Filter by tool',
      description: 'If you use more than one ticketing integration, narrow to just one of them.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#auto-complete-ticket-filter-assignee',
      title: 'Filter by assignee',
      description: 'See who’s carrying what.',
      side: 'bottom',
      align: 'start',
      // Only rendered on the All Tickets tab (enableAssigneeFilter).
      optional: true,
    },
    {
      element: '#ticket-filter-title',
      title: 'Search by title',
      description: 'Find a specific ticket by name.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '[id="anchor-tab-Assigned to me"]',
      title: 'Just yours',
      description: 'The same list, filtered to the tickets assigned to you — your queue.',
      side: 'bottom',
      align: 'start',
    },
  ],
};

/**
 * "Explore Cloud" orientation — the cloud account summary and its tabs.
 *
 * Read-only and deliberately ungated: every anchor is a read surface, and the
 * details page carries no role gate (its `withAccountGuard` is a cross-tenant
 * guard, not a role one). The connect-aws/gcp/azure guides cover the write path.
 *
 * `route` is '/cloud-account', which is NOT a list page — it's a redirector that
 * picks the first active non-K8s account and pushes to
 * '/cloud-account/details/<id>#summary'. Routing there is how this guide reaches
 * a details page whose URL contains a runtime account id it could never
 * hard-code. The redirect is async (fetch + a 100ms timer) and the guarded
 * content mounts late, but the engine waits for step 1's anchor, so the guide
 * simply starts once the page settles.
 *
 * A tenant with NO cloud accounts never redirects — it gets the "Get started"
 * empty state instead, step 1's anchor never mounts, and the engine ends the
 * tour cleanly. That's the right outcome: there's nothing to tour, and the
 * connect-* guides are what those users need.
 *
 * Anchors (all pre-existing — this guide adds no ids):
 *   #anchor-tab-Summary|Optimize|Services|Troubleshoot|Monitoring|EC2
 *        → the top-level tabOptions declare no `id`, so AnchorComponent falls
 *          back to `name` (`anchor-tab-<name>`). Unlike Tickets, these names are
 *          single words, so a plain `#` selector is valid.
 *        → `Services` is `hidden` for CloudFoundry (AnchorComponent returns null
 *          for hidden tabs) → optional. `Optimize`/`Monitoring` are only
 *          `disabled` there, and disabled tabs still render → not optional.
 *        → EC2 is AWS-only (Azure gets VM, GCP its own set) → optional.
 *   #cloud-summary-fired-alarm-count / #cloud-summary-optimize-count
 *        → Stats in the always-rendered middle column.
 *   #EVENT_TABLE_ID → the Recent Events table. Not a mistake and not a constant
 *        left un-interpolated: CloudAccountSummary declares
 *        `const EVENT_TABLE_ID = 'EVENT_TABLE_ID'`, so the value IS that string
 *        and the rendered DOM id is literally "EVENT_TABLE_ID".
 *   #cloud-summary-monthly-forecast / #cloud-summary-current-month
 *        → the Cost Summary column, which is hidden for CloudFoundry accounts
 *          (`!isCF` in CloudAccountSummary) → both optional.
 * The Resource Summary card is deliberately not spotlit: it renders only when
 * services have loaded AND are non-empty, and an `optional` step waits just
 * OPTIONAL_WAIT_MS — the Services tab step covers the same ground reliably.
 */
const cloudTour: TourDef = {
  id: 'cloud-overview',
  title: 'Explore Cloud',
  module: 'Cloud',
  description: 'Read a cloud account — spend, alarms, events, and where to go next.',
  route: '/cloud-account',
  steps: [
    {
      element: '#anchor-tab-Summary',
      title: 'Your cloud account at a glance',
      description:
        'Every connected cloud account gets this view. Switch between accounts with the picker in the header — the whole page follows your selection.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#cloud-summary-fired-alarm-count',
      title: 'What’s firing',
      description: 'Alarms raised on this account over the last 7 days, with the trend against the previous period.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#cloud-summary-optimize-count',
      title: 'Savings waiting for you',
      description: 'How many recommendations are open on this account. Click the number to jump straight to them.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#EVENT_TABLE_ID',
      title: 'Recent events',
      description: 'The latest events on this account, most severe first — your starting point when something looks wrong.',
      side: 'top',
      align: 'center',
    },
    {
      element: '#cloud-summary-monthly-forecast',
      title: 'Where spend is heading',
      description: 'Projected spend for this month, compared against last month so you can see the direction of travel.',
      side: 'left',
      align: 'start',
      // The whole cost column is hidden for CloudFoundry accounts.
      optional: true,
    },
    {
      element: '#cloud-summary-current-month',
      title: 'Spend so far',
      description: 'What this account has actually cost you this month to date.',
      side: 'left',
      align: 'start',
      optional: true,
    },
    {
      element: '#anchor-tab-Optimize',
      title: 'Cut the bill',
      description:
        'Right-sizing, configuration, security, and infra-upgrade recommendations for this account — and the status of the ones you’ve actioned.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#anchor-tab-Services',
      title: 'What’s running',
      description: 'Every service discovered in this account, with its cost and footprint.',
      side: 'bottom',
      align: 'start',
      // Hidden for CloudFoundry accounts.
      optional: true,
    },
    {
      element: '#anchor-tab-Troubleshoot',
      title: 'Dig into problems',
      description: 'The full event history for this account, plus the triage rules and alert tuning that decide what reaches you.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#anchor-tab-Monitoring',
      title: 'Alerts, logs, and metrics',
      description: 'Wire up alert manager and query this account’s cloud logs and metrics without leaving {brand}.',
      side: 'bottom',
      align: 'start',
    },
    {
      element: '#anchor-tab-EC2',
      title: 'Per-service drilldowns',
      description: 'AWS accounts also get dedicated tabs — EC2, RDS, S3, ECS — each with its own summary, recommendations, and instance list.',
      side: 'bottom',
      align: 'start',
      // AWS-only; Azure and GCP surface their own equivalents instead.
      optional: true,
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
  [investigationsTour.id]: investigationsTour,
  [knowledgeGraphTour.id]: knowledgeGraphTour,
  [automationFromCodeTour.id]: automationFromCodeTour,
  [automationWithAiTour.id]: automationWithAiTour,
  [automationFromTemplateTour.id]: automationFromTemplateTour,
  [automationFromScratchTour.id]: automationFromScratchTour,
  [optimizeTour.id]: optimizeTour,
  [optimizeSummaryTour.id]: optimizeSummaryTour,
  [optimizeResolutionsTour.id]: optimizeResolutionsTour,
  [optimizeAutoOptimizeTour.id]: optimizeAutoOptimizeTour,
  [llmAnalyserTour.id]: llmAnalyserTour,
  [aiGatewayTour.id]: aiGatewayTour,
  [ticketsTour.id]: ticketsTour,
  [cloudTour.id]: cloudTour,
};
