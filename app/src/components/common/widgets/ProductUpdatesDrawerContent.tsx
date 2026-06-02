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
import Card from '@ui/Card';
import Chip from '@ui/Chip';
import Skeleton from '@ui/Skeleton';
import EmptyState from '@ui/EmptyState';
import MarkDowns from '@shared/viewers/MarkDowns';
import Link from '@ui/Link';
import type { ProductUpdate } from '@api1/product-updates';

export interface ProductUpdatesDrawerContentProps {
  updates: ProductUpdate[];
  loading: boolean;
  error: string | null;
  /** Last-seen high-water-mark captured when the drawer opened; flags "New" items. */
  seenAt: string | null;
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

function UpdateCard({ update, isNew }: { update: ProductUpdate; isNew: boolean }) {
  const category = update.category?.trim();

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

  const footer = update.url ? (
    <Link href={update.url} openInNew secondaryText>
      Learn more
    </Link>
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

export default function ProductUpdatesDrawerContent({ updates, loading, error, seenAt }: ProductUpdatesDrawerContentProps) {
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
        <UpdateCard key={update.id} update={update} isNew={update.highlight !== false && (!seenAt || update.published_at > seenAt)} />
      ))}
    </Box>
  );
}
