import React, { useEffect, useState } from 'react';
import KubernetesGroupedEventTypeTable from '@components1/k8s/details/groupedevents/KubernetesGroupedEventTypeTable';
import KubernetesGroupedApplications from '@components1/k8s/details/groupedevents/KubernetesGroupedApplications';
import { useRouter } from 'next/router';
import { Box } from '@mui/material';
import { ds } from 'src/utils/colors';
import ToggleGroup from '@components1/ds/ToggleGroup';
import KubernetesGroupedEventsTable from '@components1/k8s/details/groupedevents/KubernetesGroupedEventsTable';

type GroupedView = 'applications' | 'event_type' | 'fingerprint';

interface KubernetesGroupedEventsProps {
  accountId: string;
}

const KubernetesGroupedEvents: React.FC<KubernetesGroupedEventsProps> = ({ accountId = '' }) => {
  const [activeToggleGroupedEvents, setActiveToggleGroupedEvents] = useState<GroupedView>('applications');
  const router = useRouter();
  useEffect(() => {
    if (router.query.section) {
      if (router.query.section == '0') {
        setActiveToggleGroupedEvents('event_type');
      } else if (router.query.section == '1') {
        setActiveToggleGroupedEvents('applications');
      } else {
        setActiveToggleGroupedEvents('fingerprint');
      }
    }
  }, []);

  return (
    <>
      <Box sx={{ display: 'flex', padding: `${ds.space[1]} ${ds.space[3]} ${ds.space[3]} 0` }}>
        <ToggleGroup<GroupedView>
          id='grouped-events-view'
          selection='single'
          size='md'
          ariaLabel='Grouped events view'
          value={activeToggleGroupedEvents}
          onChange={(next) => setActiveToggleGroupedEvents(next)}
          options={[
            { value: 'applications', label: 'Applications' },
            { value: 'event_type', label: 'Event Type' },
          ]}
        />
      </Box>
      {activeToggleGroupedEvents === 'event_type' ? (
        <KubernetesGroupedEventTypeTable accountId={accountId} />
      ) : activeToggleGroupedEvents === 'applications' ? (
        <KubernetesGroupedApplications accountId={accountId} />
      ) : (
        <KubernetesGroupedEventsTable accountId={accountId} groupEventType={activeToggleGroupedEvents} isTroubleshootPage={false} />
      )}
    </>
  );
};

export default KubernetesGroupedEvents;
