import dynamic from 'next/dynamic';
import { useRouter } from 'next/router';
import { useEffect } from 'react';
import { Box, CircularProgress, Typography } from '@mui/material';
import { hasWriteAccess, hasPermission } from '@lib/auth';

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
  // Same union WorkflowBuilderNotebook uses for its own `canEdit`: a pure custom-role
  // holder (workflows:Write) has no built-in account role, so hasWriteAccess alone
  // bounced them off /automation/new even though the create is authorized end to end.
  const canEdit = hasWriteAccess(accountIdStr) || hasPermission('workflows', 'Write');

  // Read-only users cannot create new workflows — bounce them back to the list.
  useEffect(() => {
    if (router.isReady && isNewWorkflow && !canEdit) {
      router.replace(`/automation?accountId=${accountIdStr ?? ''}`);
    }
  }, [router.isReady, isNewWorkflow, canEdit, accountIdStr, router]);

  if (isNewWorkflow && !canEdit) {
    return null;
  }

  return <WorkflowBuilderNoteBook mode={isNewWorkflow ? 'create' : 'edit'} />;
};

export default WorkflowPage;
