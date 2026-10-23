import React, { useEffect, useState } from 'react';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { getAllowedNamespaces, hasAdminSurfaceAccess } from '@lib/auth';
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
// does not admit (namespace roles), so nobody sees it appear and vanish. It is hidden too where the reader
// sees only some namespaces of the alert's account: deliveries carry no namespace, so api-server leaves that
// account's out, and the section would claim nothing was sent. They are kept with the event they were
// loaded for, so when the sidebar moves to another event, the last one's never render under it.
export function useAlertDeliveries(row: AlertRow | undefined): AlertDeliveries {
  const fingerprint = row?.fingerprint;
  const eventId = row?.id;
  const key = `${fingerprint}|${eventId}`;
  const skip = row?.cloud_account_id === 'demo' || Boolean(row?.cloud_account_id && getAllowedNamespaces(row.cloud_account_id));
  const [loaded, setLoaded] = useState<{ key: string; deliveries: AlertDeliveries } | null>(null);

  useEffect(() => {
    if (!fingerprint || skip) return undefined;
    let cancelled = false;
    deliveriesApi.forAlert(fingerprint, eventId).then((res) => {
      if (cancelled) return;
      setLoaded({
        key: `${fingerprint}|${eventId}`,
        deliveries: { visible: !res.denied, rows: res.data.rows, historyStart: res.data.historyStart, error: res.denied ? '' : res.error || '' },
      });
    });
    return () => {
      cancelled = true;
    };
  }, [fingerprint, eventId, skip]);

  return loaded && fingerprint && !skip && loaded.key === key ? loaded.deliveries : HIDDEN;
}

interface Why {
  title: string;
  body?: string;
  link?: { label: string; href: string; testId: string };
}

// Why nothing was sent, from the same gates api-server applies before an alert is forwarded
// (notification/event_processor.go): SLO alerts go to the hourly summary, triage-silenced ones are held
// back, and only HIGH alerts are posted as they fire. Triage status and priority can change after an alert
// fires, so they are only read for an alert inside the history. Resolving downgrades a cloud alert to
// INFO, so INFO is not read as "not high priority".
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
  const firedAt = row.starts_at || row.created_at;
  if (historyStart && firedAt && dayjs(firedAt).isBefore(dayjs(historyStart))) {
    return {
      title: 'Not in delivery history',
      body: `Delivery history starts on ${dayjs(historyStart).format('D MMM YYYY')}, after this alert fired.`,
    };
  }
  if (['SUPPRESSED', 'DROPPED'].includes((row.nb_status || '').toUpperCase())) {
    return { title: 'Not posted', body: 'This alert is silenced in triage, so its firings aren’t sent.' };
  }
  if (['MEDIUM', 'LOW'].includes((row.priority || '').toUpperCase())) {
    return { title: 'Not posted as it happened', body: 'Only alerts that are high priority when they first fire are posted as they happen.' };
  }
  return {
    title: 'No delivery recorded',
    body: 'Deliveries are kept for 7 days.',
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

// Why this alert, or this event of it, has no deliveries. Under other firings' deliveries the reason is
// marked as this event's, so their rows are never read as its own.
const NothingSent = ({ why, thisEvent }: { why: Why; thisEvent: boolean }) => (
  <Box data-testid='sidebar-notifications-empty' sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
    {thisEvent ? (
      <Typography sx={{ fontSize: ds.text.body }}>
        This event:{' '}
        <Box component='span' sx={{ fontWeight: ds.weight.semibold }}>
          {why.title.charAt(0).toLowerCase() + why.title.slice(1)}
        </Box>
      </Typography>
    ) : (
      <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold }}>{why.title}</Typography>
    )}
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

export function AlertNotifications({ row, deliveries }: { row: AlertRow; deliveries: AlertDeliveries }) {
  const { title: brand } = useBrandingConfig();
  const [showOthers, setShowOthers] = useState(false);
  const admin = hasAdminSurfaceAccess();

  if (deliveries.error) {
    return <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Notifications didn’t load.</Typography>;
  }
  if (deliveries.rows.length === 0) {
    return <NothingSent why={emptyReason(row, deliveries.historyStart, admin)} thisEvent={false} />;
  }

  const current = deliveries.rows.filter((d) => d.event_id && d.event_id === row.id);
  const others = deliveries.rows.filter((d) => !(d.event_id && d.event_id === row.id));
  const shown = showOthers ? deliveries.rows : current;
  const counts = ['sent', 'failed', 'not_sent']
    .map((s) => ({ s, n: current.filter((d) => d.status === s).length }))
    .filter(({ n }) => n > 0)
    .map(({ s, n }) => `${n} ${s === 'not_sent' ? 'not sent' : s}`);
  // Opened on an older event, some of the other firings came after it.
  const firedAt = row.starts_at || row.created_at;
  const othersLabel = firedAt && others.some((d) => dayjs(d.created_at).isAfter(dayjs(firedAt))) ? 'other firings' : 'earlier firings';

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
      {current.length === 0 ? (
        <NothingSent why={emptyReason(row, deliveries.historyStart, admin)} thisEvent />
      ) : (
        <Typography sx={{ fontSize: ds.text.body }}>
          This event:{' '}
          <Box component='span' sx={{ fontWeight: ds.weight.semibold }}>
            {counts.join(', ')}
          </Box>
        </Typography>
      )}
      {shown.length > 0 && (
        <Box component='ul' sx={{ listStyle: 'none', m: 0, p: 0 }}>
          {shown.map((delivery) => (
            <DeliveryItem key={delivery.id} delivery={delivery} current={delivery.event_id === row.id} brand={brand} />
          ))}
        </Box>
      )}
      {others.length > 0 && (
        <Box>
          <Button
            tone='link'
            size='xs'
            onClick={() => setShowOthers((open) => !open)}
            aria-expanded={showOthers}
            data-testid='sidebar-notifications-earlier'
          >
            {showOthers ? `Hide ${othersLabel}` : `Show ${others.length} from ${othersLabel}`}
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
