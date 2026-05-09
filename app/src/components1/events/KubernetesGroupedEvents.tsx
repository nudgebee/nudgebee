import React, { useEffect, useState } from 'react';
import KubernetesGroupedEventTypeTable from '@components1/k8s/details/groupedevents/KubernetesGroupedEventTypeTable';
import KubernetesGroupedApplications from '@components1/k8s/details/groupedevents/KubernetesGroupedApplications';
import { useRouter } from 'next/router';
import { ToggleButtonGroup, ToggleButton, Box } from '@mui/material';
import KubernetesGroupedEventsTable from '@components1/k8s/details/groupedevents/KubernetesGroupedEventsTable';

interface KubernetesGroupedEventsProps {
  accountId: string;
}

const KubernetesGroupedEvents: React.FC<KubernetesGroupedEventsProps> = ({ accountId = '' }) => {
  const [activeToggleGroupedEvents, setActiveToggleGroupedEvents] = useState('applications');
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
      <Box sx={{ display: 'flex', justifyContent: 'flex-end', padding: '12px 24px' }}>
        <ToggleButtonGroup
          key='event-grouping'
          color='primary'
          aria-label='Platform'
          className='toggle-group-buttons'
          exclusive
          value={activeToggleGroupedEvents}
          onChange={(_event, newValue) => {
            if (newValue) setActiveToggleGroupedEvents(newValue);
          }}
        >
          <ToggleButton value='applications'>Applications</ToggleButton>
          <ToggleButton value='event_type'>Event Type</ToggleButton>
        </ToggleButtonGroup>
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
