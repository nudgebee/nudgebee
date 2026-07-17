import * as React from 'react';
import RefreshIcon from '@mui/icons-material/Refresh';
import Button from '@ui/Button';
import { hasWriteAccess } from '@lib/auth';
import { snackbar } from '@shared/snackbarService';
import apiCloudAccount from '@api1/cloud-account';

interface ServiceRefreshButtonProps {
  accountId: string | undefined;
  /** Provider service name token, e.g. AmazonEC2 / microsoft.compute/virtualmachines / Compute Engine */
  serviceName: string;
  /** Currently-selected region filter. When set, only that region is re-collected. */
  region?: string;
  /** Called after a successful refresh so the parent table can re-fetch its data. */
  onDone: () => void;
  /** Optional id suffix to keep the rendered button id unique within a page. */
  id?: string;
}

/**
 * Per-service refresh button for cloud account resource tables. Triggers a
 * synchronous re-collection of just the given service's resource inventory
 * (state/tags/list/metrics) via `cloud_sync_service`, then invokes `onDone`.
 *
 * Admin-gated: re-collection makes live cloud-provider API calls, so the button
 * is hidden for users without write access (matching the `accounts_sync` action).
 */
export default function ServiceRefreshButton({ accountId, serviceName, region, onDone, id }: ServiceRefreshButtonProps) {
  const [loading, setLoading] = React.useState(false);

  if (!accountId || accountId === 'demo' || !hasWriteAccess(accountId)) {
    return null;
  }

  const handleRefresh = async () => {
    if (loading || !accountId) {
      return;
    }
    setLoading(true);
    try {
      const regions = region && region !== 'all' ? [region] : undefined;
      const res = await apiCloudAccount.syncCloudService(accountId, serviceName, regions);
      if (res?.success) {
        snackbar.success('Refresh completed.');
        onDone();
      } else {
        snackbar.error(res?.message || 'Refresh failed.');
      }
    } catch (error: any) {
      snackbar.error(error?.message || 'Refresh failed.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <Button
      id={id ? `service-refresh-${id}` : 'service-refresh'}
      size='sm'
      tone='ghost'
      composition='icon-only'
      icon={<RefreshIcon fontSize='small' />}
      aria-label='Refresh'
      tooltip='Refresh data from cloud'
      loading={loading}
      disabled={loading}
      onClick={handleRefresh}
    />
  );
}
