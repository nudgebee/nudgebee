import KnowledgeGraphServiceMapWrapper from '@components/KnowledgeGraph';
import AnchorComponent from '@shared/navigation/AnchorComponent';
import ErrorBoundary from '@shared/ErrorBoundary';
import KubernetesEventsTable from '@components/events/KubernetesEvents';
import KubernetesGroupedEventsTable from '@components/k8s/details/groupedevents/KubernetesGroupedEventsTable';
import TroubleshootSummary from '@components/troubleshoot/TroubleshootSummary';
import { Box } from '@mui/material';
import { useState, useEffect } from 'react';
import ToggleButtons from '@components/workflow/NewToggleButtons';
import AutoInvestigated from '@components/troubleshoot/AutoInvestigated';
import ManualInvestigated from '@components/troubleshoot/ManualInvestigated';
import EventResolutions from '@components/troubleshoot/EventResolutions';
import CustomTabs from '@shared/CustomTabs';
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
import { hasFeatureAccess } from '@lib/auth';

const renderEventContent = (activeToggle) => {
  switch (activeToggle) {
    case 'event-resolutions':
      return <EventResolutions />;
    case 'threshold-suggestions':
      return <ThresholdSuggestionsManager />;
    case 'triage-rules':
      return <TriageRulesManager />;
    case 'event_type':
    case 'app':
    case 'fingerprint':
      return <KubernetesGroupedEventsTable isTroubleshootPage={true} groupEventType={activeToggle} />;
    default:
      return <KubernetesEventsTable isTroubleshootPage={true} />;
  }
};

const TroubleshootPage = () => {
  const [selectedFilter, setSelectedFilter] = useState(0);
  const [activeToggleGroupedEvents, setActiveToggleGroupedEvents] = useState('fingerprint');
  const [activeTab, setActiveTab] = useState('events');
  const [investigationTab, setInvestigationTab] = useState('auto');
  const [showKnowledgeGraphTab, setShowKnowledgeGraphTab] = useState(true);
  // Bumped on each summary-widget click so the Events tab remounts and re-reads
  // the URL filters even when it is already the active tab. Kept separate from
  // the grouped tabs' own router writes so their internal filtering never forces
  // a remount.
  const [widgetNonce, setWidgetNonce] = useState(0);
  const router = useRouter();

  // Drill-down from a summary widget: open the flat Events list filtered by the
  // widget's metric. The Events table (KubernetesEvents) reads eventPriority /
  // status / nbStatus / issueType from router.query on mount.
  const applyWidgetFilter = (query) => {
    setActiveTab('events');
    setActiveToggleGroupedEvents('all');
    setWidgetNonce((n) => n + 1);
    router.push({ pathname: '/troubleshoot', query, hash: 'all' }, undefined, { shallow: true });
  };

  // Check feature flag to conditionally show/hide Knowledge Graph tab
  useEffect(() => {
    const checkKnowledgeGraphFeatureFlag = async () => {
      try {
        const isKgCacheEnabled = await hasFeatureAccess('TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH');
        // Show Knowledge Graph tab when the feature flag is enabled
        setShowKnowledgeGraphTab(isKgCacheEnabled);
      } catch (error) {
        console.error('Error checking TRACES_SERVICE_MAP_KNOWLEDGE_GRAPH feature flag:', error);
        // Default to hiding the tab on error
        setShowKnowledgeGraphTab(false);
      }
    };
    checkKnowledgeGraphFeatureFlag();
  }, []);

  const baseFilterOptions = [
    {
      name: 'All Events',
      fragment: 'all-events',
      value: 0,
      disabled: false,
      icon: AllEventsIcon,
      options: [],
    },
  ];

  // Conditionally add Knowledge Graph tab based on feature flag
  const filterOptions = showKnowledgeGraphTab
    ? [
        ...baseFilterOptions,
        {
          name: 'Knowledge Graph',
          fragment: 'kg',
          value: 1,
          disabled: false,
          betaIcon: false,
          icon: ServiceMapsIcon,
          iconSize: 16,
        },
      ]
    : baseFilterOptions;

  const tabOptions = [
    { value: 'fingerprint', text: 'Triage Inbox', fragment: 'fingerprint', icon: PodErrorsIcon },
    { value: 'all', text: 'Events', fragment: 'all', icon: AllEventsIcon },
    { value: 'event_type', text: 'Events group by type', fragment: 'event-type', icon: GroupedEventsIcon },
    { value: 'app', text: 'Events group by app', fragment: 'event-app', icon: GroupedEventsIcon },
    { value: 'triage-rules', text: 'Triage Rules', fragment: 'triage-rules', icon: AlertManagerIcon },
    { value: 'threshold-suggestions', text: 'Alert Tuning', fragment: 'threshold-suggestions', icon: AlertManagerIcon },
    { value: 'event-resolutions', text: 'Event Resolutions', fragment: 'event-resolutions', icon: RecommendationResolutionIcon },
  ];

  const investigationTabOptions = [
    {
      value: 'auto',
      text: 'Auto Investigated',
      fragment: 'auto-investigated',
      icon: AutomateBlue,
    },
    {
      value: 'manual',
      text: 'Manual Investigated',
      fragment: 'manual-investigated',
      icon: ManualTriggerIconBlue,
    },
  ];

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) return;
    const fragment = hash;
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setSelectedFilter(filter.value);
    } else {
      const groupEvent = tabOptions.find((tab) => tab.fragment === fragment);
      if (groupEvent) {
        setActiveTab('events');
        setActiveToggleGroupedEvents(groupEvent.value);
      } else {
        const invTab = investigationTabOptions.find((tab) => tab.fragment === fragment);
        if (invTab) {
          setActiveTab('investigations');
          setInvestigationTab(invTab.value);
        }
      }
    }
  }, [router.asPath, showKnowledgeGraphTab]);
  // Need to handle toggleGroupedEvents' fragment with router.asPath

  const toggleOptions = [
    { value: 'events', label: 'Events', icon: AllEventsIcon },
    { value: 'investigations', label: 'Investigations', icon: SearchBlueIcon },
  ];

  const handleRouting = (tab) => {
    let fragment = '';
    if (tab === 'investigations') {
      fragment = investigationTabOptions.find((option) => option.value === investigationTab)?.fragment || 'auto-investigated';
    } else {
      fragment = tabOptions.find((option) => option.value === activeToggleGroupedEvents)?.fragment || 'fingerprint';
    }
    router.push(`/troubleshoot#${fragment}`, undefined, { shallow: true });
  };

  const handleActiveTabChange = (value) => {
    setActiveTab(value);
    handleRouting(value);
  };

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        filterOptions={filterOptions}
        onChangeFilter={(val) => {
          if (val === 0 || val === 1) {
            setSelectedFilter(val);
          }
        }}
      />

      {selectedFilter === 0 && (
        <div style={{ margin: '0px 40px' }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              padding: '8px 0',
              marginTop: '16px',
            }}
          >
            <ToggleButtons options={toggleOptions} activeValue={activeTab} width='500px' size='large' onChange={handleActiveTabChange} />
          </Box>

          {activeTab === 'events' && (
            <>
              <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'center' }}>
                <TroubleshootSummary onWidgetFilter={applyWidgetFilter} />
              </Box>
              <Box sx={{ marginBottom: '8px' }}>
                <CustomTabs
                  value={activeToggleGroupedEvents}
                  onChange={(newValue) => setActiveToggleGroupedEvents(newValue)}
                  options={{
                    value: 1,
                    tabOptions: tabOptions,
                  }}
                  variant='secondary'
                  smallSize={true}
                  ariaLabel='Event grouping options'
                />
              </Box>
              <ErrorBoundary key={`${activeToggleGroupedEvents}-${widgetNonce}`}>{renderEventContent(activeToggleGroupedEvents)}</ErrorBoundary>
            </>
          )}

          {activeTab === 'investigations' && (
            <>
              <TroubleshootSummary type='investigations' tab={investigationTab} />
              <Box sx={{ marginBottom: '8px' }}>
                <CustomTabs
                  value={investigationTab}
                  smallSize={true}
                  onChange={(value) => setInvestigationTab(value)}
                  options={{
                    value: 1,
                    tabOptions: investigationTabOptions,
                  }}
                />
              </Box>
              <ErrorBoundary key={investigationTab}>
                {investigationTab === 'auto' && <AutoInvestigated />}
                {investigationTab === 'manual' && <ManualInvestigated />}
              </ErrorBoundary>
            </>
          )}
        </div>
      )}

      {selectedFilter === 1 && (
        <div style={{ margin: '20px' }}>
          <ErrorBoundary>
            <KnowledgeGraphServiceMapWrapper />
          </ErrorBoundary>
        </div>
      )}
    </>
  );
};

export default TroubleshootPage;
