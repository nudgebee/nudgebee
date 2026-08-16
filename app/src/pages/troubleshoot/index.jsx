import dynamic from 'next/dynamic';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import KubernetesEventsTable, { TROUBLESHOOT_EVENTS_FILTER_STORAGE_KEY } from '@components/events/KubernetesEvents';
import KubernetesGroupedEventsTable from '@components/k8s/details/groupedevents/KubernetesGroupedEventsTable';
import TroubleshootSummary from '@components/troubleshoot/TroubleshootSummary';
import { Box, CircularProgress } from '@mui/material';
import { useState, useEffect } from 'react';
import AutoInvestigated from '@components/troubleshoot/AutoInvestigated';
import ManualInvestigated from '@components/troubleshoot/ManualInvestigated';
import EventResolutions from '@components/troubleshoot/EventResolutions';
import Tabs from '@shared/navigation/Tabs';
import {
  AllEventsIcon,
  GroupedEventsIcon,
  PodErrorsIcon,
  ManualTriggerIconBlue,
  AutomateBlue,
  SearchBlueIcon,
  AlertManagerIcon,
  RecommendationResolutionIcon,
  ServiceMapsIcon,
} from '@assets';
import TriageRulesManager from '@components/triage/TriageRulesManager';
import ThresholdSuggestionsManager from '@components/triage/ThresholdSuggestionsManager';
import { useRouter } from 'next/router';
import { clearPersistedFilters } from '@hooks/usePersistedFilters';
import { getLast24Hrs } from '@lib/datetime';

// Knowledge Graph pulls in reactflow and only renders under the third tab, so a
// static import shipped the graph bundle in this page's chunk for every visitor
// — including the ones who never leave All Events. Load it on demand instead.
// The chunk is large, so without a fallback the tab paints blank until it lands.
const KnowledgeGraphServiceMapWrapper = dynamic(() => import('@components/knowledge-graph/KnowledgeGraph'), {
  ssr: false,
  loading: () => (
    <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 400 }}>
      <CircularProgress size={28} />
    </Box>
  ),
});

// Single source of truth for every tab/sub-tab this page renders, mirroring the
// tab/sub-tab config in [KubernetesDetails].jsx: each top-level entry carries
// `value` (-> selectedTab) and `fragment` (top-level hash segment), plus a
// nested `tabOptions` array whose own `value`/`fragment` drive selectedSubTab
// and the `parent/child` URL hash. Static (module scope, not component state)
// since — unlike KubernetesDetails' tabOptions — nothing here needs runtime
// mutation (counts, disabled flags, etc). Knowledge Graph is enabled by
// default for every tenant (opt-out via the TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH
// feature flag is enforced server-side by the nightly build cron), so its tab
// is always shown.
const filterOptions = [
  {
    name: 'All Events',
    fragment: 'all-events',
    value: 0,
    icon: AllEventsIcon,
    tabOptions: [
      { value: 0, text: 'Triage Inbox', fragment: 'fingerprint', icon: PodErrorsIcon, id: 'tab-fingerprint' },
      { value: 1, text: 'Events', fragment: 'all', icon: AllEventsIcon, id: 'tab-all-events' },
      { value: 2, text: 'Events group by type', fragment: 'event-type', icon: GroupedEventsIcon, id: 'tab-event-type' },
      { value: 3, text: 'Events group by app', fragment: 'event-app', icon: GroupedEventsIcon, id: 'tab-event-app' },
      { value: 4, text: 'Triage Rules', fragment: 'triage-rules', icon: AlertManagerIcon, id: 'tab-triage-rules' },
      { value: 5, text: 'Alert Tuning', fragment: 'threshold-suggestions', icon: AlertManagerIcon, id: 'tab-threshold-suggestions' },
      { value: 6, text: 'Event Resolutions', fragment: 'event-resolutions', icon: RecommendationResolutionIcon, id: 'tab-event-resolutions' },
    ],
  },
  {
    name: 'Investigations',
    fragment: 'investigations',
    value: 1,
    icon: SearchBlueIcon,
    tabOptions: [
      { value: 0, text: 'Auto Investigated', fragment: 'auto-investigated', icon: AutomateBlue, id: 'tab-auto-investigated' },
      { value: 1, text: 'Manual Investigated', fragment: 'manual-investigated', icon: ManualTriggerIconBlue, id: 'tab-manual-investigated' },
    ],
  },
  {
    name: 'Knowledge Graph',
    fragment: 'kg',
    value: 2,
    icon: ServiceMapsIcon,
    iconSize: 16,
  },
];

// AnchorComponent renders its own sub-tab bar whenever a filterOptions entry
// carries `tabOptions` (see AnchorComponent.jsx). This page renders its own
// sub-tab row instead — with the summary widget cards sandwiched in between —
// so strip `tabOptions` before handing the list to AnchorComponent to avoid a
// duplicate second tab row. It only needs the plain fragment/value pairs to
// highlight the correct top-level pill.
const anchorFilterOptions = filterOptions.map(({ tabOptions: _tabOptions, ...rest }) => rest);

const TroubleshootPage = () => {
  // selectedTab is the parent-tab selection (0 = All Events, 1 = Investigations,
  // 2 = Knowledge Graph); selectedSubTab indexes into that parent's own
  // tabOptions above. Investigations was promoted from a NewToggleButtons
  // sub-tab inside "All Events" to its own top-level AnchorComponent tab.
  const [selectedTab, setSelectedTab] = useState(null);
  const [selectedSubTab, setSelectedSubTab] = useState(null);
  // Bumped on each summary-widget click so the Events tab remounts and re-reads
  // the URL filters even when it is already the active tab. Kept separate from
  // the sub-tabs' own router writes so their internal filtering never forces
  // a remount.
  const [widgetNonce, setWidgetNonce] = useState(0);
  // One 24h window, frozen at page load, shared by the summary cards and the
  // Events drill-down so both query the identical interval. Derived from a
  // single `now` so the current/previous boundaries line up exactly. Frozen
  // (not live) matches the cards' existing snapshot semantics — they fetch once
  // on mount — and the user can still widen the range from the list's own date
  // picker after drilling in.
  const [summaryRange] = useState(() => {
    const endDate = new Date();
    const startDate = getLast24Hrs(endDate);
    return { startDate, endDate, previousStartDate: getLast24Hrs(startDate), previousEndDate: startDate };
  });
  const router = useRouter();

  // Drill-down from a summary widget: open the flat Events list filtered by the
  // widget's metric. The Events table (KubernetesEvents) reads eventPriority /
  // status / nbStatus / issueType from router.query on mount.
  const applyWidgetFilter = (query) => {
    // The summary cards are UNSCOPED, whole-window KPIs. The Events table, by
    // contrast, restores sticky scoping filters (account / namespace / source /
    // subject type) from localStorage + the URL — so a leftover filter from an
    // earlier visit silently narrows the drill-down and the count comes up short
    // of the card (e.g. card shows 148 High Severity, list shows 137). Clicking
    // a KPI is an explicit "show me this metric's population" action, so reset
    // the persisted Events filters first; combined with the clean URL push below
    // (only the widget's own filter), the list then matches the card exactly.
    clearPersistedFilters(TROUBLESHOOT_EVENTS_FILTER_STORAGE_KEY);

    // Pin the cards' frozen 24h window onto the drill-down. The troubleshoot
    // Events list otherwise defaults to the current week (KubernetesEvents
    // getInitialTime), so a click would count 7 days against the card's 24h.
    // start_time/end_time take precedence over that default and over persisted
    // ranges (which we just cleared), so the list window matches the card.
    const rangedQuery = {
      ...query,
      start_time: String(summaryRange.startDate.getTime()),
      end_time: String(summaryRange.endDate.getTime()),
    };

    // Write the filter into the URL BEFORE switching to the Events tab and
    // bumping the remount key. KubernetesEvents seeds its filters from
    // router.query only at mount and never re-syncs them. Setting state first
    // remounts the table synchronously while router.push is async, so the
    // first click mounted against the stale (empty) query and applied nothing
    // — only the second click, after the query had landed, "worked". Deferring
    // the state updates until the push resolves removes that race.
    router.push({ pathname: '/troubleshoot', query: rangedQuery, hash: 'all-events/all' }, undefined, { shallow: true }).then(() => {
      setSelectedTab(0);
      setSelectedSubTab(1); // 'all' — the flat Events sub-tab
      setWidgetNonce((n) => n + 1);
    });
  };

  // Re-derive tab + sub-tab from the URL hash on every navigation — mount,
  // browser back/forward, or any in-app link that changes the hash while this
  // page stays mounted. Deliberately reactive (not mount-only): AnchorComponent
  // never sees this page's tabOptions (see anchorFilterOptions above), so its
  // own onChangeFilter callback can only ever report subVal=0 and fires a
  // render late — wiring state through it clobbered the sub-tab back to 0 on
  // every top-level transition, e.g. hitting Back after drilling into a
  // sub-tab. Resolving the full parent/child hash here instead, on every
  // change, keeps this the single source of truth for both values.
  useEffect(() => {
    // Rewrite the URL to the canonical `parent/child` hash whenever we fall
    // back to a default sub-tab, so the address bar always matches what's
    // actually rendered (a refresh, or a copied/shared link, reproduces the
    // same view). Declared inside the effect so it isn't a stale-closure
    // dependency concern.
    const replaceHash = (hash) => {
      router.replace({ pathname: router.pathname, query: router.query, hash }, undefined, { shallow: true });
    };

    const hash = router.asPath.split('#')[1];
    if (!hash) {
      setSelectedTab(0);
      setSelectedSubTab(0);
      return;
    }
    const decoded = decodeURIComponent(hash);
    const [fragment, subFragment] = decoded.split('/');

    // Unknown top-level fragment (stale/typo'd link) — fall back to All Events
    // and canonicalize the URL to match.
    const parent = filterOptions.find((option) => option.fragment === fragment);
    if (!parent) {
      setSelectedTab(0);
      setSelectedSubTab(0);
      replaceHash(`${filterOptions[0].fragment}/${filterOptions[0].tabOptions[0].fragment}`);
      return;
    }
    setSelectedTab(parent.value);

    // Resolve the sub-tab from subFragment when present (a top-level-only hash,
    // e.g. a pill click, never appends one). Either way — missing or an unknown
    // fragment (stale/typo'd link) — fall back to that parent's first sub-tab
    // rather than leaving a numerically-coincidental leftover value from
    // whichever tab was active before (selectedSubTab is shared across
    // parents), and canonicalize the URL to match.
    const subTab = subFragment ? (parent.tabOptions || []).find((tab) => tab.fragment === subFragment) : null;
    if (subTab) {
      setSelectedSubTab(subTab.value);
    } else {
      setSelectedSubTab(0);
      if (!!subFragment && parent.tabOptions?.[0]) {
        replaceHash(`${parent.fragment}/${parent.tabOptions[0].fragment}`);
      }
    }
  }, [router.asPath]);

  return (
    <>
      {/* onChangeFilter deliberately omitted — AnchorComponent only owns the
          top-level pill highlight here (see anchorFilterOptions above), so
          its callback can't resolve the sub-tab; the effect above is the
          single source of truth for both selectedTab and selectedSubTab. */}
      <AnchorComponent manageRoute={true} filterOptions={anchorFilterOptions} />

      {selectedTab === 0 && (
        <div style={{ margin: '0px var(--ds-space-6)' }}>
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center', marginTop: 'var(--ds-space-4)' }}>
            <TroubleshootSummary range={summaryRange} onWidgetFilter={applyWidgetFilter} />
          </Box>
          <Box id='troubleshoot-event-tabs' sx={{ marginBottom: 'var(--ds-space-2)' }}>
            <Tabs
              value={selectedSubTab}
              onChange={setSelectedSubTab}
              options={filterOptions[0]}
              variant='secondary'
              smallSize={true}
              ariaLabel='Event grouping options'
            />
          </Box>
          <ErrorBoundary key={`${selectedSubTab}-${widgetNonce}`}>
            {selectedSubTab === 0 && <KubernetesGroupedEventsTable isTroubleshootPage={true} groupEventType='fingerprint' />}
            {selectedSubTab === 1 && <KubernetesEventsTable isTroubleshootPage={true} />}
            {selectedSubTab === 2 && <KubernetesGroupedEventsTable isTroubleshootPage={true} groupEventType='event_type' />}
            {selectedSubTab === 3 && <KubernetesGroupedEventsTable isTroubleshootPage={true} groupEventType='app' />}
            {selectedSubTab === 4 && <TriageRulesManager />}
            {selectedSubTab === 5 && <ThresholdSuggestionsManager />}
            {selectedSubTab === 6 && <EventResolutions />}
          </ErrorBoundary>
        </div>
      )}

      {selectedTab === 1 && (
        <div style={{ margin: '0px var(--ds-space-6)' }}>
          <Box sx={{ marginTop: 'var(--ds-space-4)' }}>
            <TroubleshootSummary type='investigations' tab={selectedSubTab === 1 ? 'manual' : 'auto'} range={summaryRange} />
          </Box>
          <Box sx={{ marginBottom: 'var(--ds-space-2)' }}>
            <Tabs value={selectedSubTab} smallSize={true} onChange={setSelectedSubTab} options={filterOptions[1]} />
          </Box>
          <ErrorBoundary key={selectedSubTab}>
            {selectedSubTab === 0 && <AutoInvestigated />}
            {selectedSubTab === 1 && <ManualInvestigated />}
          </ErrorBoundary>
        </div>
      )}

      {selectedTab === 2 && (
        <div style={{ margin: 'var(--ds-space-4)' }}>
          <ErrorBoundary>
            <KnowledgeGraphServiceMapWrapper />
          </ErrorBoundary>
        </div>
      )}
    </>
  );
};

export default TroubleshootPage;
