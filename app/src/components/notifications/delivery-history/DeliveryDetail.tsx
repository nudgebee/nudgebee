import React, { useEffect, useState } from 'react';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import deliveriesApi, { Delivery } from '@api1/notification/deliveries';
import { messageLink, platformName, reasonWords, statusLine } from './deliveryText';

// The expanded row of Delivery history. The table hands over the row's cells; the delivery itself rides
// on the last one (_delivery), with the account's name (_accountName).
const DeliveryDetail = ({ row, isExpanded }: { row: any[]; isExpanded?: boolean }) => {
  const { title: brand } = useBrandingConfig();
  const meta = (Array.isArray(row) && row[row.length - 1]) || {};
  const delivery: Delivery | undefined = meta._delivery;
  const [attempts, setAttempts] = useState<Delivery[]>([]);
  const retried = (delivery?.attempt || 1) > 1 && !!delivery?.dispatch_id;

  useEffect(() => {
    if (!isExpanded || !retried || !delivery) return undefined;
    let cancelled = false;
    deliveriesApi.listAttempts(delivery).then((res) => {
      if (!cancelled) setAttempts(res.data);
    });
    return () => {
      cancelled = true;
    };
  }, [isExpanded, retried, delivery]);

  if (!delivery) return null;
  const words = reasonWords(delivery, brand);
  const link = messageLink(delivery);
  const facts = [
    { label: delivery.status === 'sent' ? 'Sent' : 'Tried', value: dayjs(delivery.created_at).format('DD MMM YYYY, HH:mm:ss') },
    delivery.destination && { label: delivery.platform === 'email' ? 'Address' : 'Destination ID', value: delivery.destination, mono: true },
    delivery.error_code && { label: `${platformName(delivery.platform) || 'Platform'} code`, value: delivery.error_code, mono: true },
    { label: 'Attempts', value: retried ? `${delivery.attempt} attempts` : 'Sent on the first attempt' },
    meta._accountName && { label: 'Account', value: meta._accountName },
    delivery.dispatch_id && { label: 'Send ID', value: delivery.dispatch_id, mono: true },
  ].filter(Boolean) as { label: string; value: string; mono?: boolean }[];
  if (delivery.status !== 'sent' && !retried) {
    facts.splice(
      facts.findIndex((f) => f.label === 'Attempts'),
      1
    );
  }

  return (
    <Box sx={{ py: ds.space[3], px: ds.space[4], display: 'flex', flexDirection: 'column', gap: ds.space[3], maxWidth: 880 }}>
      {delivery.status !== 'sent' && words.says && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          <Typography sx={{ fontSize: ds.text.body }}>
            <Box component='span' sx={{ color: ds.gray[600] }}>
              {delivery.status === 'not_sent' ? 'Why it wasn’t sent: ' : 'What went wrong: '}
            </Box>
            {words.says}
          </Typography>
          {words.fix && (
            <Typography sx={{ fontSize: ds.text.body }}>
              <Box component='span' sx={{ color: ds.gray[600] }}>
                {delivery.status === 'not_sent' ? 'To change this: ' : 'How to fix: '}
              </Box>
              {words.fix}
            </Typography>
          )}
        </Box>
      )}
      {attempts.length > 1 && (
        <Box component='ul' aria-label='Attempts' sx={{ listStyle: 'none', m: 0, p: 0, display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          {attempts.map((attempt) => {
            const status = statusLine({ ...attempt, attempt: 1 }, brand);
            return (
              <Box component='li' key={attempt.id} sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[2] }}>
                <Label tone={status.tone} dot size='sm'>
                  {status.label}
                </Label>
                <Typography sx={{ fontSize: ds.text.small }}>{`Attempt ${attempt.attempt}`}</Typography>
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{dayjs(attempt.created_at).format('HH:mm:ss')}</Typography>
                {status.detail && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{status.detail}</Typography>}
              </Box>
            );
          })}
        </Box>
      )}
      <Box
        component='dl'
        sx={{ m: 0, display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', columnGap: ds.space[5], rowGap: ds.space[2] }}
      >
        {facts.map((fact) => (
          <Box key={fact.label} sx={{ minWidth: 0 }}>
            <Typography component='dt' sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
              {fact.label}
            </Typography>
            <Typography
              component='dd'
              sx={{ m: 0, fontSize: ds.text.small, overflowWrap: 'anywhere', fontFamily: fact.mono ? ds.font.mono : undefined }}
            >
              {fact.value}
            </Typography>
          </Box>
        ))}
      </Box>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[4] }}>
        {delivery.event_id && (
          <Box component='span' data-testid={`delivery-history-open-alert-${delivery.id}`}>
            <Link href={`/investigate?id=${delivery.event_id}${delivery.account_id ? `&accountId=${delivery.account_id}` : ''}`}>Open alert</Link>
          </Box>
        )}
        {link && (
          <Box component='span' data-testid={`delivery-history-detail-message-${delivery.id}`}>
            <Link href={link.href} openInNew>
              {link.label}
            </Link>
          </Box>
        )}
      </Box>
    </Box>
  );
};

export default DeliveryDetail;
