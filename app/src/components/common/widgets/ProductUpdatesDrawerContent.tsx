/**
 * ProductUpdatesDrawerContent — body of the header "Product Updates" drawer.
 *
 * Renders the platform-wide changelog using DS primitives (Card / Chip /
 * Markdown / EmptyState / Skeleton). The drawer chrome itself is provided by
 * the shared CustomDrawer; this component only owns the content.
 */
import * as React from 'react';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import TourOutlinedIcon from '@mui/icons-material/TourOutlined';
import Card from '@ui/Card';
import Chip from '@ui/Chip';
import Skeleton from '@ui/Skeleton';
import EmptyState from '@ui/EmptyState';
import { Button } from '@ui/Button';
import MarkDowns from '@shared/viewers/MarkDowns';
import Link from '@ui/Link';
import { TOURS, canAccessTour, useLaunchGuide } from '@components/common/tour';
import { fetchFeatureFlagsForTenant } from '@lib/auth';
import type { ProductUpdate } from '@api1/product-updates';

export interface ProductUpdatesDrawerContentProps {
  updates: ProductUpdate[];
  loading: boolean;
  error: string | null;
  /** Last-seen high-water-mark captured when the drawer opened; flags "New" items. */
  seenAt: string | null;
  /**
   * Close the drawer before a tour launches — a tour spotlights the page
   * underneath and may navigate to its own route first, so the drawer must get
   * out of the way. Omitted where no tour launcher is shown.
   */
  onRequestClose?: () => void;
}

type CategoryTone = 'info' | 'success' | 'neutral' | 'agent';

const CATEGORY_TONE: Record<string, CategoryTone> = {
  feature: 'info',
  fix: 'success',
  improvement: 'agent',
  announcement: 'neutral',
};

const formatDate = (iso?: string | null): string => {
  if (!iso) {
    return '—';
  }
  const d = dayjs(iso);
  return d.isValid() ? d.format('MMM D, YYYY') : '—';
};

function UpdateCard({ update, isNew, onLaunchTour }: { update: ProductUpdate; isNew: boolean; onLaunchTour: (tourId: string) => void }) {
  const category = update.category?.trim();

  // On-demand: only updates that declare a tour that actually exists and the
  // user can run (canAccessTour — same gate as the Guides catalog, so we never
  // offer a tour that dead-ends on a role/feature-gated screen) get a launcher.
  const tour = update.tour_id ? TOURS[update.tour_id] : undefined;
  const tourAvailable = tour ? canAccessTour(tour) : false;

  const header = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
      <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-900)',
          }}
        >
          {update.title}
        </Typography>
        {isNew && (
          <Chip size='2xs' tone='info'>
            New
          </Chip>
        )}
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
        {category && (
          <Chip size='2xs' tone={CATEGORY_TONE[category.toLowerCase()] ?? 'neutral'}>
            {category}
          </Chip>
        )}
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{formatDate(update.published_at)}</Typography>
      </Box>
    </Box>
  );

  const footer =
    tourAvailable || update.url ? (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
        {tourAvailable && (
          <Button
            id={`product-update-tour-${update.id}`}
            data-testid={`product-update-tour-${update.id}`}
            tone='link'
            size='sm'
            icon={<TourOutlinedIcon fontSize='small' />}
            onClick={() => onLaunchTour(update.tour_id as string)}
          >
            Take the tour
          </Button>
        )}
        {update.url && (
          <Link href={update.url} openInNew secondaryText>
            Learn more
          </Link>
        )}
      </Box>
    ) : undefined;

  return (
    <Card variant='outlined' size='sm' header={header} footer={footer} data-testid={`product-update-${update.id}`}>
      <MarkDowns
        data={update.body}
        sx={{ fontSize: 'var(--ds-text-body)', lineHeight: 1.6, color: 'var(--ds-gray-800)' }}
        allowExecutable={undefined}
        onLinkClick={undefined}
      />
    </Card>
  );
}

function LoadingState() {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      {[0, 1, 2].map((i) => (
        <Box key={i} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
          <Skeleton shape='text' size='title' width='60%' />
          <Skeleton shape='text' size='caption' width='30%' />
          <Skeleton shape='rect' height={48} />
        </Box>
      ))}
    </Box>
  );
}

export default function ProductUpdatesDrawerContent({ updates, loading, error, seenAt, onRequestClose }: ProductUpdatesDrawerContentProps) {
  const launch = useLaunchGuide();
  // Warm the tenant feature-flag cache so feature-gated product-update tours
  // resolve correctly in canAccessTour (mirrors GuidesMenu); the state flip
  // re-runs each card's availability check once the flags land.
  const [, setFlagsReady] = React.useState(false);
  React.useEffect(() => {
    let cancelled = false;
    fetchFeatureFlagsForTenant().finally(() => {
      if (!cancelled) {
        setFlagsReady(true);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleLaunchTour = React.useCallback(
    (tourId: string) => {
      // Close the drawer first — the tour highlights the page beneath it and may
      // navigate to its own route.
      onRequestClose?.();
      launch(tourId);
    },
    [launch, onRequestClose]
  );

  if (loading) {
    return <LoadingState />;
  }

  if (error) {
    return <EmptyState size='section' illustration='no-results' title='Could not load updates' description={error} />;
  }

  if (updates.length === 0) {
    return <EmptyState size='section' illustration='clear-skies' title='You’re all caught up' description='New product updates will show up here.' />;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
      {updates.map((update) => (
        <UpdateCard
          key={update.id}
          update={update}
          isNew={update.highlight !== false && (!seenAt || update.published_at > seenAt)}
          onLaunchTour={handleLaunchTour}
        />
      ))}
    </Box>
  );
}
