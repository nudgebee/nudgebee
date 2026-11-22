import dynamic from 'next/dynamic';
import { useRouter } from 'next/router';
import { useEffect, useState } from 'react';
import { Box, CircularProgress, Typography } from '@mui/material';
import { getUserSession, hasWriteAccess, hasPermission, missingPermissionMessage } from '@lib/auth';
import AccountPickerModal from '@components/common/AccountPickerModal';
import PermissionDeniedContent from '@components/common/navigation/PermissionDeniedContent';
import { EmptyState } from '@ui/EmptyState';
import apiWorkflow from '@api1/workflow';

const WorkflowBuilderNoteBook = dynamic(() => import('@components/workflow/WorkflowBuilderNotebook'), {
  ssr: false,
  loading: () => (
    <Box
      sx={{
        width: '100%',
        height: '100vh',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        alignItems: 'center',
        backgroundColor: 'rgb(243, 243, 243)',
        gap: 2,
      }}
    >
      <CircularProgress size={32} />
      <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body-lg)' }}>Loading automation builder...</Typography>
    </Box>
  ),
});

const WorkflowPage = () => {
  const router = useRouter();
  const { workflowId, accountId } = router.query;

  const isNewWorkflow = workflowId === 'new';
  const accountIdStr = typeof accountId === 'string' ? accountId : undefined;
  // A link without ?accountId= left the builder loading forever: look the account up,
  // and fall back to asking only when that lookup itself fails.
  const needsAccount = router.isReady && !isNewWorkflow && !accountIdStr;
  const [resolveState, setResolveState] = useState<'resolving' | 'notFound' | 'failed'>('resolving');
  // Same union WorkflowBuilderNotebook uses for its own `canEdit`: a pure custom-role
  // holder (workflows:Write) has no built-in account role, so hasWriteAccess alone
  // bounced them off /automation/new even though the create is authorized end to end.
  const canEdit = hasWriteAccess(accountIdStr) || hasPermission('workflows', 'Write');
  // Matches /automation's own gate (canReadWorkflows, automation/index.jsx) -- without
  // this, a user the sidebar and the list page both hide Automations from could still
  // open any existing workflow's full builder by its direct link.
  const session = getUserSession();
  const isAdmin = session?.roles?.includes('tenant_admin') || session?.roles?.includes('account_admin');
  const canReadWorkflows =
    isAdmin || hasPermission('workflows', 'Read') || hasPermission('workflows', 'Execute') || hasPermission('workflows', 'Write');

  // Read-only users cannot create new workflows — bounce them back to the list.
  useEffect(() => {
    if (router.isReady && isNewWorkflow && !canEdit) {
      router.replace(`/automation?accountId=${accountIdStr ?? ''}`);
    }
  }, [router.isReady, isNewWorkflow, canEdit, accountIdStr, router]);

  // Keep the rest of the link (executionId, #executions) so it lands where it pointed.
  const openInAccount = (picked: string) =>
    router.replace({ pathname: router.pathname, query: { ...router.query, accountId: picked }, hash: window.location.hash.replace(/^#/, '') });

  useEffect(() => {
    if (!needsAccount || !canReadWorkflows || typeof workflowId !== 'string') return undefined;
    let cancelled = false;
    setResolveState('resolving');
    apiWorkflow.getWorkflowAccount(workflowId).then((res: any) => {
      if (cancelled) return;
      const resolved = res?.data?.workflow_get_account?.account_id;
      if (resolved) {
        openInAccount(resolved);
      } else {
        // 404 = missing or not readable; picking an account cannot help then.
        const notFound = res?.errors?.some((e: any) => e?.extensions?.upstream?.status === 404);
        setResolveState(notFound ? 'notFound' : 'failed');
      }
    });
    return () => {
      cancelled = true;
    };
  }, [needsAccount, workflowId, canReadWorkflows]);

  if (isNewWorkflow && !canEdit) {
    return null;
  }

  if (!isNewWorkflow && !canReadWorkflows) {
    return <PermissionDeniedContent message={missingPermissionMessage('workflows:Read')} />;
  }

  if (needsAccount && resolveState === 'resolving') {
    return (
      <Box sx={{ width: '100%', height: '100vh', display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
        <CircularProgress size={32} />
      </Box>
    );
  }

  if (needsAccount && resolveState === 'notFound') {
    return (
      <EmptyState
        size='page'
        illustration='no-results'
        title='Automation not found'
        description='It may have been deleted, or it belongs to an account you cannot access.'
        action={{ label: 'Go to Automations', onClick: () => router.replace('/automation'), id: 'automation-not-found-back' }}
      />
    );
  }

  if (needsAccount) {
    return (
      <AccountPickerModal
        open
        onSelect={openInAccount}
        onCancel={() => router.replace('/automation')}
        title='Select an account to open this automation'
        description='This link does not say which account the automation belongs to. Pick its account to open it.'
        testIdPrefix='automation-account-picker'
      />
    );
  }

  return <WorkflowBuilderNoteBook mode={isNewWorkflow ? 'create' : 'edit'} />;
};

export default WorkflowPage;
