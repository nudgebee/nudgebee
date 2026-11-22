import React from 'react';
import KubernetesGroupedEventsTable from '@components/k8s/details/groupedevents/KubernetesGroupedEventsTable';

interface VmAlertsProps {
  accountId: string;
  cloudResourceId?: string;
  /** A short, filter-less list for embedding (the Summary). */
  compact?: boolean;
}

const COMPACT_ROWS = 5;

const VmAlerts: React.FC<VmAlertsProps> = ({ accountId, cloudResourceId, compact = false }) => (
  <KubernetesGroupedEventsTable
    accountId={accountId}
    cloudResourceId={cloudResourceId}
    pageSize={compact ? COMPACT_ROWS : undefined}
    hideToolbar={compact}
    groupEventType='fingerprint'
    isTroubleshootPage={false}
    hideK8sFilters
  />
);

export default VmAlerts;
