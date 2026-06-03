import dynamic from 'next/dynamic';
import { useRouter } from 'next/router';
import { useEffect } from 'react';
import { Box, CircularProgress, Typography } from '@mui/material';
import { hasWriteAccess } from '@lib/auth';

const WorkflowBuilderNoteBook = dynamic(() => import('@components1/workflow/WorkflowBuilderNotebook'), {
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
      <Typography sx={{ color: '#6b7280', fontSize: '14px' }}>Loading automation builder...</Typography>
    </Box>
  ),
});

const WorkflowPage = () => {
  const router = useRouter();
  const { workflowId, accountId } = router.query;

  const isNewWorkflow = workflowId === 'new';
  const accountIdStr = typeof accountId === 'string' ? accountId : undefined;
  const canEdit = hasWriteAccess(accountIdStr);

  // Read-only users cannot create new workflows — bounce them back to the list.
  useEffect(() => {
    if (router.isReady && isNewWorkflow && !canEdit) {
      router.replace(`/auto-pilot?accountId=${accountIdStr ?? ''}#workflow`);
    }
  }, [router.isReady, isNewWorkflow, canEdit, accountIdStr, router]);

  if (isNewWorkflow && !canEdit) {
    return null;
  }

  return <WorkflowBuilderNoteBook mode={isNewWorkflow ? 'create' : 'edit'} />;
};

export default WorkflowPage;
