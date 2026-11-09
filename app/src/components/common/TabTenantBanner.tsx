import { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { Banner } from '@ui/Banner';
import { subscribeTenantChange, type TenantRef } from '@lib/tabTenant';

/**
 * Shown when the session switched to another tenant in a different tab while
 * this tab still shows the old one (see @lib/tabTenant). The gateway
 * already refuses this tab's requests, so the only way forward is a reload.
 *
 * Fixed to the top of the viewport rather than in the page flow so it shows on
 * every page, including Nubi's fixed-position chat. Not dismissible: dismissing
 * would leave a tab whose every request is refused.
 */
export default function TabTenantBanner() {
  const [tenant, setTenant] = useState<TenantRef | null>(null);

  useEffect(() => subscribeTenantChange(setTenant), []);

  if (!tenant) return null;

  const target = tenant.name ? <strong>{tenant.name}</strong> : 'another tenant';
  return (
    <Box
      data-testid='tab-tenant-banner'
      sx={{
        position: 'fixed',
        top: 'var(--ds-space-3)',
        left: '50%',
        transform: 'translateX(-50%)',
        width: 'min(640px, calc(100vw - 2 * var(--ds-space-4)))',
        zIndex: (theme) => theme.zIndex.modal + 1,
      }}
    >
      <Banner
        tone='warning'
        title='Tenant changed in another tab'
        message={<>You switched to {target} in another tab. This tab still shows the previous tenant, so reload it before you change anything.</>}
        actions={[{ label: 'Reload', onClick: () => window.location.reload() }]}
        actionsPlacement='inline'
      />
    </Box>
  );
}
