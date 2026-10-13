import React, { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import Loader from '@shared/Loader';
import apiAccount from '@api1/account';
import KubernetesTracesListing from '@components/k8s/details/KubernetesTracesListing';

/**
 * Traces of a self-hosted VM account.
 *
 * A VM account has no agent, so it has no built-in trace store: traces come from
 * a trace integration the user connected (Jaeger, Tempo, Elasticsearch, ...).
 * The listing is driven by accountId alone — the cloud-account GCP tab uses it
 * the same way — so this only has to say so when nothing is connected (and why,
 * when the lookup itself failed), instead of leaving the listing to render an
 * empty table with no explanation.
 */

interface VmTracesProps {
  accountId: string;
}

const VmTraces = ({ accountId }: VmTracesProps) => {
  const router = useRouter();
  const [state, setState] = useState<'loading' | 'ready' | 'none' | 'failed'>('loading');
  const [failure, setFailure] = useState('');

  useEffect(() => {
    let cancelled = false;
    setState('loading');
    apiAccount
      .getDefaultProvider({ account_id: accountId, provider_type: 'traces' })
      .then((res: any) => {
        if (cancelled) return;
        const errors = res?.data?.errors;
        if (errors?.length || res instanceof Error) {
          setFailure(errors?.[0]?.message || res?.message || 'Failed to resolve the traces provider');
          setState('failed');
          return;
        }
        setState(res?.data?.data?.observability_get_default_provider?.provider ? 'ready' : 'none');
      })
      .catch((error: any) => {
        if (cancelled) return;
        console.error('Failed to resolve the traces provider:', error);
        setFailure(error?.message || 'Failed to resolve the traces provider');
        setState('failed');
      });
    return () => {
      cancelled = true;
    };
  }, [accountId]);

  if (state === 'loading') {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '40vh' }}>
        <Loader />
      </Box>
    );
  }

  // A failed lookup is not "nothing connected": say why, so the user does not go
  // connect a store that is already there.
  if (state === 'failed') {
    return (
      <Box sx={{ p: 'var(--ds-space-4)' }}>
        <Banner tone='critical' surface='section' title='Could not load the traces provider' message={failure} />
      </Box>
    );
  }

  if (state === 'none') {
    return (
      <Box sx={{ p: 'var(--ds-space-4)' }}>
        <EmptyState
          title='No trace store connected'
          description='Connect a trace store (Jaeger, Tempo, Elasticsearch and others) in Integrations to list the traces of this account.'
          action={{ label: 'Open Integrations', onClick: () => router.push('/user-management#integrations') }}
        />
      </Box>
    );
  }

  return (
    <Box sx={{ width: '100%', overflowX: 'auto' }}>
      <KubernetesTracesListing accountId={accountId} showNamespaceFilter={false} showWorkloadFilter={false} />
    </Box>
  );
};

export default VmTraces;
