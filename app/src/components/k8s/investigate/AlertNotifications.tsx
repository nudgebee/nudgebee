import React, { useEffect, useState } from 'react';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { hasAdminSurfaceAccess } from '@lib/auth';
import deliveriesApi, { Delivery } from '@api1/notification/deliveries';
import { destinationLabel, messageLink, platformName, statusLine } from '@components/notifications/delivery-history/deliveryText';

const HISTORY_HASH = '#notification-rules/delivery-history';
const RULES_HREF = '/user-management#notification-rules';

interface AlertRow {
  id?: string;
  fingerprint?: string;
  cloud_account_id?: string;
  source?: string;
  priority?: string;
  nb_status?: string;
  starts_at?: string;
  created_at?: string;
}

export interface AlertDeliveries {
  visible: boolean;
  rows: Delivery[];
  historyStart: string | null;
  error: string;
}

const HIDDEN: AlertDeliveries = { visible: false, rows: [], historyStart: null, error: '' };

// The alert's deliveries. The section stays hidden until they load, and for readers the delivery history
// does not admit (namespace roles), so nobody sees it appear and vanish. They are kept with the fingerprint
// they were loaded for, so when the sidebar moves to another alert, the last one's never render under it.
export function useAlertDeliveries(row: AlertRow | undefined): AlertDeliveries {
  const fingerprint = row?.fingerprint;
  const demo = row?.cloud_account_id === 'demo';
  const [loaded, setLoaded] = useState<{ fingerprint: string; deliveries: AlertDeliveries } | null>(null);

  useEffect(() => {
    if (!fingerprint || demo) return undefined;
    let cancelled = false;
    deliveriesApi.forAlert(fingerprint).then((res) => {
      if (cancelled) return;
      setLoaded({
        fingerprint,
        deliveries: { visible: !res.denied, rows: res.data.rows, historyStart: res.data.historyStart, error: res.denied ? '' : res.error || '' },
      });
    });
    return () => {
      cancelled = true;
    };
  }, [fingerprint, demo]);

  return loaded && !demo && loaded.fingerprint === fingerprint ? loaded.deliveries : HIDDEN;
}

interface Why {
  title: string;
  body?: string;
  link?: { label: string; href: string; testId: string };
}

// Why nothing was sent, from the same gates api-server applies before an alert is forwarded
// (notification/event_processor.go): SLO alerts go to the hourly summary, triage-silenced ones are held
// back, and only HIGH alerts are posted as they fire. Resolving downgrades a cloud alert to INFO, so INFO
// is not read as "not high priority".
export function emptyReason(row: AlertRow, historyStart: string | null, admin: boolean): Why {
  if ((row.source || '').toLowerCase() === 'slo') {
    return {
      title: 'Posted in the hourly SLO summary',
      body: 'SLO alerts are grouped and posted once an hour.',
      link: admin
        ? { label: 'See SLO summaries', href: `/user-management?type=summary${HISTORY_HASH}`, testId: 'sidebar-notifications-slo-summaries' }
        : undefined,
    };
  }
  if (['SUPPRESSED', 'DROPPED'].includes((row.nb_status || '').toUpperCase())) {
    return { title: 'Not posted', body: 'This alert is silenced in triage, so its firings aren’t sent.' };
  }
  if (['MEDIUM', 'LOW'].includes((row.priority || '').toUpperCase())) {
    return { title: 'Not posted as it happened', body: 'Only alerts that are high priority when they first fire are posted as they happen.' };
  }
  const firedAt = row.starts_at || row.created_at;
  if (historyStart && firedAt && dayjs(firedAt).isBefore(dayjs(historyStart))) {
    return {
      title: 'Not in delivery history',
      body: `Delivery history starts on ${dayjs(historyStart).format('D MMM YYYY')}, after this alert fired.`,
    };
  }
  return {
    title: 'No delivery recorded',
    body: 'Deliveries are kept for 14 days.',
    link: admin ? { label: 'Check where it goes', href: RULES_HREF, testId: 'sidebar-notifications-check-rules' } : undefined,
  };
}

const DeliveryItem = ({ delivery, current, brand }: { delivery: Delivery; current: boolean; brand: string }) => {
  const status = statusLine(delivery, brand);
  const link = messageLink(delivery);
  const where = delivery.platform ? `${destinationLabel(delivery)} · ${platformName(delivery.platform)}` : 'No destination';
  return (
    <Box
      component='li'
      sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], py: ds.space[2], borderTop: `0.5px solid ${ds.gray[100]}` }}
    >
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[2] }}>
        <Label tone={status.tone} dot size='sm'>
          {status.label}
        </Label>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, overflowWrap: 'anywhere' }}>{where}</Typography>
        {current && (
          <Label tone='info' size='sm'>
            This event
          </Label>
        )}
      </Box>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
        {dayjs(delivery.created_at).format('DD MMM, HH:mm')}
        {delivery.kind === 'finding_resolved' ? ' · Resolved' : ''}
        {delivery.thread_id ? ' · in thread' : ''}
      </Typography>
      {status.detail && <Typography sx={{ fontSize: ds.text.small }}>{status.detail}</Typography>}
      {link && (
        <Box component='span' data-testid={`sidebar-notifications-message-${delivery.id}`}>
          <Link href={link.href} openInNew secondaryText>
            {link.label}
          </Link>
        </Box>
      )}
    </Box>
  );
};

export function AlertNotifications({ row, deliveries }: { row: AlertRow; deliveries: AlertDeliveries }) {
  const { title: brand } = useBrandingConfig();
  const [showEarlier, setShowEarlier] = useState(false);
  const admin = hasAdminSurfaceAccess();

  if (deliveries.error) {
    return <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Notifications didn’t load.</Typography>;
  }
  if (deliveries.rows.length === 0) {
    const why = emptyReason(row, deliveries.historyStart, admin);
    return (
      <Box data-testid='sidebar-notifications-empty' sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold }}>{why.title}</Typography>
        {why.body && <Typography sx={{ fontSize: ds.text.body }}>{why.body}</Typography>}
        {why.link && (
          <Box component='span' data-testid={why.link.testId}>
            <Link href={why.link.href} secondaryText>
              {why.link.label}
            </Link>
          </Box>
        )}
      </Box>
    );
  }

  const current = deliveries.rows.filter((d) => d.event_id && d.event_id === row.id);
  const earlier = deliveries.rows.filter((d) => !(d.event_id && d.event_id === row.id));
  const shown = current.length === 0 || showEarlier ? deliveries.rows : current;
  const counts = ['sent', 'failed', 'not_sent']
    .map((s) => ({ s, n: current.filter((d) => d.status === s).length }))
    .filter(({ n }) => n > 0)
    .map(({ s, n }) => `${n} ${s === 'not_sent' ? 'not sent' : s}`);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
      {counts.length > 0 && (
        <Typography sx={{ fontSize: ds.text.body }}>
          This event:{' '}
          <Box component='span' sx={{ fontWeight: ds.weight.semibold }}>
            {counts.join(', ')}
          </Box>
        </Typography>
      )}
      <Box component='ul' sx={{ listStyle: 'none', m: 0, p: 0 }}>
        {shown.map((delivery) => (
          <DeliveryItem key={delivery.id} delivery={delivery} current={delivery.event_id === row.id} brand={brand} />
        ))}
      </Box>
      {current.length > 0 && earlier.length > 0 && (
        <Box>
          <Button
            tone='link'
            size='xs'
            onClick={() => setShowEarlier((open) => !open)}
            aria-expanded={showEarlier}
            data-testid='sidebar-notifications-earlier'
          >
            {showEarlier ? 'Hide earlier firings' : `Show ${earlier.length} from earlier firings`}
          </Button>
        </Box>
      )}
      {admin && row.fingerprint && (
        <Box component='span' data-testid='sidebar-notifications-history'>
          <Link href={`/user-management?alert=${encodeURIComponent(row.fingerprint)}${HISTORY_HASH}`} secondaryText>
            See this alert in Delivery history
          </Link>
        </Box>
      )}
    </Box>
  );
}
