import React from 'react';
import { Box, Typography } from '@mui/material';
import ErrorOutlineOutlinedIcon from '@mui/icons-material/ErrorOutlineOutlined';
import InboxOutlinedIcon from '@mui/icons-material/InboxOutlined';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import TuneOutlinedIcon from '@mui/icons-material/TuneOutlined';
import { Button } from '@ui/Button';
import { ds } from '@utils/colors';
import { EXPORT_HIDE_ATTR } from './panelImage';

/** What a panel is saying when it has nothing to draw. */
export type PanelStateTone = 'empty' | 'attention' | 'blocked' | 'error';

const TONES: Record<PanelStateTone, { icon: React.ReactNode; iconBg: string; iconBorder: string; iconColor: string; titleColor: string }> = {
  empty: {
    icon: <InboxOutlinedIcon sx={{ fontSize: 18 }} />,
    iconBg: ds.gray[100],
    iconBorder: ds.gray[200],
    iconColor: ds.gray[500],
    titleColor: ds.gray[700],
  },
  attention: {
    icon: <TuneOutlinedIcon sx={{ fontSize: 18 }} />,
    iconBg: ds.amber[100],
    iconBorder: ds.amber[300],
    iconColor: ds.amber[600],
    titleColor: ds.gray[700],
  },
  blocked: {
    icon: <LockOutlinedIcon sx={{ fontSize: 18 }} />,
    iconBg: ds.gray[100],
    iconBorder: ds.gray[200],
    iconColor: ds.gray[500],
    titleColor: ds.gray[700],
  },
  error: {
    icon: <ErrorOutlineOutlinedIcon sx={{ fontSize: 18 }} />,
    iconBg: ds.red[100],
    iconBorder: ds.red[200],
    iconColor: ds.red[500],
    titleColor: ds.red[500],
  },
};

interface Props {
  tone: PanelStateTone;
  /** One line. The panel is narrow, so this wraps rather than truncates. */
  title: string;
  /** What to do about it, when there is something. */
  description?: string;
  /** Overrides the tone's default glyph, for a state the tone alone doesn't name. */
  icon?: React.ReactNode;
  /** Omitted when there is nothing to do — an inert button implies a way out. */
  action?: { label: string; onClick: () => void };
}

/** A panel body with no data in it. */
const PanelState: React.FC<Props> = ({ tone, title, description, icon, action }) => {
  const palette = TONES[tone];
  return (
    <Box
      role='status'
      data-testid='panel-state'
      data-tone={tone}
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: ds.space[2],
        p: ds.space[3],
        textAlign: 'center',
      }}
    >
      <Box
        aria-hidden='true'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          width: 32,
          height: 32,
          borderRadius: 'var(--ds-radius-pill)',
          background: palette.iconBg,
          border: `1px solid ${palette.iconBorder}`,
          color: palette.iconColor,
          flexShrink: 0,
        }}
      >
        {icon ?? palette.icon}
      </Box>
      <Box>
        <Typography sx={{ fontSize: 13, fontWeight: 600, color: palette.titleColor, lineHeight: 1.35 }}>{title}</Typography>
        {description && (
          <Typography variant='caption' sx={{ color: ds.gray[500], display: 'block', mt: '2px' }}>
            {description}
          </Typography>
        )}
      </Box>
      {action && (
        // Excluded from an exported image, like the overflow menu.
        <Box component='span' {...{ [EXPORT_HIDE_ATTR]: 'true' }}>
          <Button tone='secondary' size='sm' onClick={action.onClick} data-testid='panel-state-action'>
            {action.label}
          </Button>
        </Box>
      )}
    </Box>
  );
};

export default PanelState;
