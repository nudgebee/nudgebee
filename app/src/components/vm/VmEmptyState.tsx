import React, { useState } from 'react';
import { Box, Typography, Stack } from '@mui/material';
import { Button as DsButton } from '@ui/Button';
import AddSelfHostedAccountModal from '@pages/accounts/AddSelfHostedAccountModal';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';

/**
 * First-run state for /vm, mirroring the "Get started with Kubernetes
 * monitoring" panel in KubernetesClusterOverview — same gradient icon tile,
 * heading/description block, feature row and write-gated primary action, so
 * connecting the first fleet reads the same as connecting the first cluster.
 */
const FEATURES = [
  {
    label: 'Package inventory',
    // Layered stack — the installed-package listing.
    icon: 'M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4',
  },
  {
    label: 'Vulnerability detection',
    // Shield with a check — the CVE match.
    icon: 'M12 3l7 3v6c0 4.5-3 8.3-7 9-4-.7-7-4.5-7-9V6l7-3zm-3 9l2 2 4-4',
  },
  {
    label: 'Agent-based access',
    // Plug/link — reached through a proxy agent, not a provider API.
    icon: 'M13.828 10.172a4 4 0 010 5.656l-3 3a4 4 0 01-5.656-5.656l1.5-1.5m4.5-4.5l1.5-1.5a4 4 0 015.656 5.656l-3 3a4 4 0 01-5.656 0',
  },
];

const VmEmptyState = ({ onAccountCreated }: { onAccountCreated: () => void }) => {
  const [showAddAccountModal, setShowAddAccountModal] = useState(false);

  return (
    <>
      <AddSelfHostedAccountModal
        open={showAddAccountModal}
        onClose={(refresh: boolean) => {
          setShowAddAccountModal(false);
          if (refresh) onAccountCreated();
        }}
      />
      <Box
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
          <svg width='36' height='36' viewBox='0 0 24 24' fill='none' stroke='var(--ds-blue-600)' strokeWidth='1.5' strokeLinecap='round'>
            <rect x='3' y='4' width='18' height='6.5' rx='1.5' />
            <rect x='3' y='13.5' width='18' height='6.5' rx='1.5' />
            <path d='M6.5 7.25h.01M6.5 16.75h.01' strokeWidth='2' />
            <path d='M10 7.25h7.5M10 16.75h7.5' />
          </svg>
        </Box>

        <Typography
          sx={{
            fontSize: 'var(--ds-text-title)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-foreground)',
            mb: ds.space[2],
            fontFamily: 'Poppins',
          }}
        >
          Get started with self-hosted VMs
        </Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            color: 'var(--ds-gray-600)',
            mb: ds.space[6],
            textAlign: 'center',
            maxWidth: ds.space.mul(0, 230),
            lineHeight: 1.6,
          }}
        >
          A self-hosted account groups the machines you reach through a proxy agent rather than a cloud provider API — on-premise fleets, private
          clouds, or anywhere the machines are invisible to AWS, Azure and GCP.
        </Typography>

        <Stack direction='row' spacing={4} sx={{ mb: ds.space[6] }}>
          {FEATURES.map((item) => (
            <Box key={item.label} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <Box
                sx={{
                  width: ds.space[6],
                  height: ds.space[6],
                  borderRadius: 'var(--ds-radius-lg)',
                  background: 'var(--ds-blue-100)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  flexShrink: 0,
                }}
              >
                <svg
                  width='16'
                  height='16'
                  viewBox='0 0 24 24'
                  fill='none'
                  stroke='var(--ds-blue-600)'
                  strokeWidth='1.5'
                  strokeLinecap='round'
                  strokeLinejoin='round'
                >
                  <path d={item.icon} />
                </svg>
              </Box>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-brand-500)',
                  whiteSpace: 'nowrap',
                }}
              >
                {item.label}
              </Typography>
            </Box>
          ))}
        </Stack>

        {/* No-arg on purpose, as on the empty Kubernetes landing: there is no
            account yet to scope the check to, and creating one is tenant-level. */}
        {hasWriteAccess() ? (
          <DsButton id='add-self-hosted-account' tone='primary' size='lg' onClick={() => setShowAddAccountModal(true)}>
            Add Self-Hosted Account
          </DsButton>
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
            Need admin permission to add a self-hosted account
          </Typography>
        )}
      </Box>
    </>
  );
};

export default VmEmptyState;
