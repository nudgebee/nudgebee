/**
 * CloudRecentEvents — DS-migrated, service-agnostic "Recent Events" table for
 * the cloud-account Summary tabs (EC2, RDS, S3, ECS, CloudFoundry).
 *
 * Owns: section title "Recent Events" + a 5-row light listing of recent events
 * for the given service, with a "View all" deeplink to the full Events tab.
 *
 * The events fetch (`apiCloudAccount.listEvents`) is identical across all 5
 * services; the only service-specific bits are
 *   - `serviceName` (used as `subjectNamespace` filter)
 *   - `redirectUrl` (the "View all" deeplink — caller builds this with its
 *     own service-to-URL switch so this component stays domain-free)
 *   - `transformSubjectName` (ECS only — strips the ARN prefix off the
 *     `subject_name` so the cell shows a readable cluster/service name)
 */
import React, { useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import DSCard from '@components1/ds/Card';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { SeverityIcon as DsSeverityIcon } from '@components1/ds/SeverityIcon';
import Text from '@common-new/format/Text';
import Datetime from '@common-new/format/Datetime';
import apiCloudAccount from '@api1/cloud-account';
import { toSeverityLevel } from '@utils/common';
import { ds } from '@utils/colors';
import type { ICustomTable2Row } from './ec2/Instances';

const TABLE_ID = 'cloud-recent-events';
const HEADERS = ['Subject', 'Event', 'Severity', 'Created at'];

interface CloudRecentEventsProps {
  accountId: string;
  serviceName: string;
  /** Pre-built "View all" deeplink. Caller owns the service→URL mapping. */
  redirectUrl?: string;
  /**
   * Optional transform for `subject_name` rendering. ECS uses this to strip
   * the ARN prefix so the cell shows "my-cluster" instead of
   * "arn:aws:ecs:…/my-cluster".
   */
  transformSubjectName?: (item: any) => string;
  /** Optional secondary line below the subject (e.g. "Service: foo"). */
  secondaryRender?: (item: any) => React.ReactNode;
}

const defaultSubjectName = (item: any) => item.subject_name || '';

const defaultSecondaryRender = (item: any) => (item.subject_namespace ? <Text value={`service: ${item.subject_namespace}`} secondaryText /> : null);

export function CloudRecentEvents({
  accountId,
  serviceName,
  redirectUrl,
  transformSubjectName = defaultSubjectName,
  secondaryRender = defaultSecondaryRender,
}: CloudRecentEventsProps) {
  const [loading, setLoading] = useState(false);
  const [eventData, setEventData] = useState<ICustomTable2Row[][]>([]);

  // Keep latest callbacks in refs so the fetch effect only runs when the
  // actual data inputs (accountId / serviceName) change. Without this, callers
  // that pass inline lambdas (e.g. ECS's ARN-stripper) would re-fire the
  // effect on every parent render and create a refetch storm.
  const transformRef = useRef(transformSubjectName);
  const secondaryRef = useRef(secondaryRender);
  useEffect(() => {
    transformRef.current = transformSubjectName;
    secondaryRef.current = secondaryRender;
  });

  useEffect(() => {
    if (!accountId) return;
    // Clear stale rows before the new fetch so account-switch never shows the
    // previous account's events (even briefly), and so an error fall-through
    // doesn't leave the table populated with the old data.
    setEventData([]);
    setLoading(true);
    apiCloudAccount
      .listEvents({ accountId, subjectNamespace: serviceName }, 5, 0, { light: true })
      .then((res: any) => {
        const rows: ICustomTable2Row[][] = (res?.data?.events || []).map((item: any) => {
          const data: ICustomTable2Row[] = [];
          // Subject (primary) + optional secondary line
          data.push({
            component: (
              <Box sx={{ minWidth: '120px' }}>
                <Text showAutoEllipsis value={transformRef.current(item)} />
                {secondaryRef.current(item)}
              </Box>
            ),
          });
          data.push({ text: <Text value={item.aggregation_key} showAutoEllipsis /> });
          data.push({
            component: <DsSeverityIcon level={toSeverityLevel(item.priority)} aria-label={`Severity: ${item.priority || 'unknown'}`} />,
            data: item.priority,
          });
          data.push({ component: <Datetime value={item.starts_at} />, data: item.starts_at });
          return data;
        });
        setEventData(rows);
      })
      .catch((err) => {
        console.error('Recent events fetch error:', err);
        setEventData([]);
      })
      .finally(() => setLoading(false));
  }, [accountId, serviceName]);

  return (
    <DSCard
      size='md'
      elevation='flat'
      header={<Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.medium, color: ds.gray[700] }}>Recent Events</Typography>}
      sx={{ px: ds.space[3], pb: ds.space[2], overflow: 'hidden' }}
    >
      <CustomTable2
        tableHeadingCenter={['Severity']}
        id={TABLE_ID}
        headers={HEADERS}
        tableData={eventData}
        rowsPerPage={5}
        onPageChange={() => false}
        loading={loading}
        totalRows={eventData.length}
        showAllLink={true}
        linkToShowAll={redirectUrl || ''}
      />
    </DSCard>
  );
}

export default CloudRecentEvents;
