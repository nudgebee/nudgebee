import dynamic from 'next/dynamic';
import { useRouter } from 'next/router';
import { Box, CircularProgress, Typography } from '@mui/material';

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
  const { workflowId } = router.query;

  const isNewWorkflow = workflowId === 'new';

  return <WorkflowBuilderNoteBook mode={isNewWorkflow ? 'create' : 'edit'} />;
};

export default WorkflowPage;
