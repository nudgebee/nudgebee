import React from 'react';
import { Box, Typography } from '@mui/material';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import { ds } from '@utils/colors';

/**
 * Empty state for a surface gated behind a tenant feature flag the tenant
 * hasn't enabled (deep links can land on such a surface even when its tab
 * renders disabled). Mirrors the "Get started with …" first-run panel
 * (AccountOverview / cloud-account / VmEmptyState) — same bordered card and
 * gradient icon tile — but with a lock, and no feature row: the parent names
 * the flag in `title`, the subtitle says where to enable it.
 */
const FeatureDisabledEmptyState = ({
  title,
  subtitle = 'Enable it in Tenant Settings → Feature Flags.',
  id,
}: {
  title: string;
  subtitle?: string;
  id?: string;
}) => {
  return (
    <Box
      id={id}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        padding: 'var(--ds-space-7) var(--ds-space-6)',
        borderRadius: 'var(--ds-radius-xl)',
        border: '1px solid var(--ds-gray-300)',
        background: 'var(--ds-background-100)',
      }}
    >
      <Box
        sx={{
          width: ds.space.mul(0, 32),
          height: ds.space.mul(0, 32),
          borderRadius: 'var(--ds-radius-xl)',
          background: `linear-gradient(135deg, var(--ds-blue-100) 0%, ${ds.blue[200]} 100%)`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          mb: ds.space[5],
          boxShadow: '0px 1px 3px color-mix(in srgb, var(--ds-gray-700) 6%, transparent)',
        }}
      >
        <LockOutlinedIcon sx={{ fontSize: 36, color: 'var(--ds-blue-600)' }} />
      </Box>

      <Typography
        sx={{
          fontSize: 'var(--ds-text-title)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: 'var(--ds-foreground)',
          mb: ds.space[2],
          fontFamily: ds.font.display,
        }}
      >
        {title}
      </Typography>
      <Typography
        sx={{
          fontSize: 'var(--ds-text-body-lg)',
          color: 'var(--ds-gray-600)',
          textAlign: 'center',
          maxWidth: ds.space.mul(0, 230),
          lineHeight: 1.6,
        }}
      >
        {subtitle}
      </Typography>
    </Box>
  );
};

export default FeatureDisabledEmptyState;
