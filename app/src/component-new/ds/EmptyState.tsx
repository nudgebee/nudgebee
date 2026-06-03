/**
 * EmptyState — DS V2 of legacy EmptyData.
 * Spec:        app/design-system/primitives/feedback/empty-state.html
 * Variants:    size = 'inline' | 'section' | 'page'
 *              illustration = 'none' | 'first-time' | 'no-results' | 'no-permissions' | 'clear-skies'
 *              tone = 'neutral' | 'success'
 *              composition = 'icon+heading' | 'icon+heading+description' | '+action' (auto from props)
 *
 * Migration:   `import EmptyData from '@common/EmptyData'`
 *           →  `import { EmptyState } from '@components1/ds/EmptyState'`
 *
 *   V1 prop      →  V2 prop
 *   img          →  illustration (preset key) OR `icon` (custom React node)
 *   heading      →  title
 *   subHeading   →  description
 *   height       →  derived from `size`
 *   children     →  passed through after the action; or use `action={{label, onClick}}` for the canonical button
 *
 * Don't (per spec):
 *   - Don't say "Empty" / "No data". State what is empty and why ("No incidents in 7 days").
 *   - Don't put two actions. One path forward.
 *   - Don't render EmptyState while data is loading — that's a Skeleton.
 */
import * as React from 'react';
import { Box, Button } from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import CheckIcon from '@mui/icons-material/Check';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import Inventory2OutlinedIcon from '@mui/icons-material/Inventory2Outlined';
import InboxOutlinedIcon from '@mui/icons-material/InboxOutlined';

export type EmptyStateSize = 'inline' | 'section' | 'page';
export type EmptyStateIllustration = 'none' | 'first-time' | 'no-results' | 'no-permissions' | 'clear-skies';
export type EmptyStateTone = 'neutral' | 'success';

export interface EmptyStateAction {
  label: string;
  onClick: () => void;
}

export interface EmptyStateProps {
  title: string;
  description?: React.ReactNode;
  size?: EmptyStateSize;
  illustration?: EmptyStateIllustration;
  /** Override the preset illustration with a custom React node */
  icon?: React.ReactNode;
  tone?: EmptyStateTone;
  action?: EmptyStateAction;
  /** Optional secondary content rendered below the action */
  children?: React.ReactNode;
  id?: string;
  sx?: React.CSSProperties;
}

const SIZE_DIMS: Record<EmptyStateSize, { padding: string; minHeight: string; iconSize: number; titleFontSize: string }> = {
  inline: {
    padding: 'var(--ds-space-4)',
    minHeight: '120px',
    iconSize: 16,
    titleFontSize: 'var(--ds-text-body)',
  },
  section: {
    padding: 'var(--ds-space-6) var(--ds-space-5)',
    minHeight: '240px',
    iconSize: 20,
    titleFontSize: 'var(--ds-text-title)',
  },
  page: {
    padding: 'var(--ds-space-7) var(--ds-space-6)',
    minHeight: '360px',
    iconSize: 28,
    titleFontSize: 'var(--ds-text-heading)',
  },
};

const ILLUSTRATION_ICON: Record<Exclude<EmptyStateIllustration, 'none'>, React.ComponentType<{ sx?: object }>> = {
  'first-time': Inventory2OutlinedIcon,
  'no-results': SearchIcon,
  'no-permissions': LockOutlinedIcon,
  'clear-skies': CheckIcon,
};

function defaultIconForIllustration(illustration: EmptyStateIllustration, sizeIndex: EmptyStateSize): React.ReactNode {
  if (illustration === 'none') {
    // Use a calm inbox glyph as the gentle default
    return <InboxOutlinedIcon sx={{ fontSize: SIZE_DIMS[sizeIndex].iconSize }} />;
  }
  const Icon = ILLUSTRATION_ICON[illustration];
  return <Icon sx={{ fontSize: SIZE_DIMS[sizeIndex].iconSize }} />;
}

const TONE_PALETTE: Record<
  EmptyStateTone,
  { iconBg: string; iconBorder: string; iconColor: string; titleColor: string; bg: string; border: string }
> = {
  neutral: {
    iconBg: 'var(--ds-gray-100)',
    iconBorder: 'var(--ds-gray-200)',
    iconColor: 'var(--ds-gray-600)',
    titleColor: 'var(--ds-gray-700)',
    bg: 'transparent',
    border: 'transparent',
  },
  success: {
    iconBg: 'var(--ds-green-100)',
    iconBorder: 'var(--ds-green-200)',
    iconColor: 'var(--ds-green-700)',
    titleColor: 'var(--ds-green-700)',
    bg: 'var(--ds-green-100)',
    border: 'var(--ds-green-200)',
  },
};

export function EmptyState({
  title,
  description,
  size = 'section',
  illustration = 'none',
  icon,
  tone = 'neutral',
  action,
  children,
  id,
  sx,
}: EmptyStateProps) {
  const dims = SIZE_DIMS[size];
  const palette = TONE_PALETTE[tone];
  const iconNode = icon ?? defaultIconForIllustration(illustration, size);
  const isInline = size === 'inline';

  return (
    <Box
      id={id}
      role='status'
      sx={{
        display: 'flex',
        flexDirection: isInline ? 'row' : 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: isInline ? 'var(--ds-space-2)' : 'var(--ds-space-2)',
        textAlign: isInline ? 'left' : 'center',
        padding: dims.padding,
        minHeight: dims.minHeight,
        borderRadius: 'var(--ds-radius-md)',
        backgroundColor: palette.bg,
        border: `1px solid ${palette.border}`,
        ...sx,
      }}
    >
      <Box
        aria-hidden='true'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: isInline ? 24 : size === 'page' ? 56 : 40,
          height: isInline ? 24 : size === 'page' ? 56 : 40,
          borderRadius: 'var(--ds-radius-pill)',
          backgroundColor: palette.iconBg,
          border: `1px solid ${palette.iconBorder}`,
          color: palette.iconColor,
          flexShrink: 0,
        }}
      >
        {iconNode}
      </Box>
      <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: isInline ? 'flex-start' : 'center', gap: 'var(--ds-space-1)' }}>
        <Box
          sx={{
            fontSize: dims.titleFontSize,
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: palette.titleColor,
            lineHeight: 1.3,
          }}
        >
          {title}
        </Box>
        {description !== undefined && (
          <Box
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-600)',
              lineHeight: 1.5,
              maxWidth: size === 'page' ? '420px' : '320px',
            }}
          >
            {description}
          </Box>
        )}
        {action && (
          <Box sx={{ mt: 'var(--ds-space-2)' }}>
            <Button
              onClick={action.onClick}
              variant='contained'
              size={size === 'page' ? 'medium' : 'small'}
              sx={{
                textTransform: 'none',
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                backgroundColor: 'var(--ds-blue-500)',
                color: 'var(--ds-background-100)',
                borderRadius: 'var(--ds-radius-sm)',
                px: 'var(--ds-space-3)',
                '&:hover': { backgroundColor: 'var(--ds-blue-600)' },
              }}
            >
              {action.label}
            </Button>
          </Box>
        )}
        {children}
      </Box>
    </Box>
  );
}

export default EmptyState;
