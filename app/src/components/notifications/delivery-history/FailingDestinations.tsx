import React from 'react';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Skeleton } from '@ui/Skeleton';
import { ds } from '@utils/colors';
import type { DestinationSummary } from '@api1/notification/deliveries';
import { platformName, reasonWords, summaryLabel } from './deliveryText';

interface Props {
  destinations: DestinationSummary[];
  loading: boolean;
  brand: string;
  onShow: (destination: DestinationSummary) => void;
}

// Destinations whose latest delivery in the last 24 hours failed, each with the reason and the fix.
const FailingDestinations = ({ destinations, loading, brand, onShow }: Props) => {
  if (loading) {
    return (
      <Box sx={{ border: `0.5px solid ${ds.gray[200]}`, borderRadius: ds.radius.lg, p: ds.space[4] }}>
        <Skeleton shape='text' size='text' width='40%' />
      </Box>
    );
  }
  if (destinations.length === 0) {
    return (
      <Typography id='delivery-history-no-failing' sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
        No failing destinations. Every destination&apos;s latest delivery in the last 24 hours went through.
      </Typography>
    );
  }
  return (
    <Box
      component='section'
      aria-labelledby='delivery-history-failing-title'
      sx={{ border: `0.5px solid ${ds.gray[200]}`, borderRadius: ds.radius.lg, px: ds.space[4] }}
    >
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[2], py: ds.space[3] }}>
        <Box aria-hidden sx={{ width: ds.space[2], height: ds.space[2], borderRadius: ds.radius.pill, backgroundColor: ds.red[500] }} />
        <Typography id='delivery-history-failing-title' component='h2' sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold }}>
          {destinations.length === 1 ? '1 failing destination' : `${destinations.length} failing destinations`}
        </Typography>
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Their latest delivery in the last 24 hours failed.</Typography>
      </Box>
      <Box component='ul' sx={{ listStyle: 'none', m: 0, p: 0 }}>
        {destinations.map((destination) => {
          const words = reasonWords(
            { id: destination.destination, created_at: destination.last_at || '', status: 'failed', reason: destination.last_reason, ...destination },
            brand,
            destination.last_destination_name
          );
          return (
            <Box
              component='li'
              key={`${destination.platform}:${destination.destination}`}
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'baseline',
                columnGap: ds.space[4],
                rowGap: ds.space[1],
                py: ds.space[2],
                borderTop: `0.5px solid ${ds.gray[100]}`,
              }}
            >
              <Box sx={{ flex: '0 0 210px', display: 'inline-flex', alignItems: 'baseline', gap: ds.space[2], minWidth: 0 }}>
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium }}>{summaryLabel(destination)}</Typography>
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{platformName(destination.platform)}</Typography>
              </Box>
              <Typography sx={{ flex: '1 1 320px', minWidth: 0, fontSize: ds.text.body }}>
                {[words.says, words.fix].filter(Boolean).join(' ')}
              </Typography>
              <Box sx={{ flex: '0 0 auto', display: 'inline-flex', alignItems: 'baseline', gap: ds.space[4] }}>
                {destination.last_at && (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{dayjs(destination.last_at).format('HH:mm')}</Typography>
                )}
                <Button
                  tone='link'
                  size='xs'
                  onClick={() => onShow(destination)}
                  data-testid={`delivery-history-show-${destination.platform}-${destination.destination}`}
                >
                  Show deliveries
                </Button>
              </Box>
            </Box>
          );
        })}
      </Box>
    </Box>
  );
};

export default FailingDestinations;
