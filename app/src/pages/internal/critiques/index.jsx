import { Box } from '@mui/material';
import ErrorBoundary from '@shared/ErrorBoundary';
import { withAuth } from '@lib/auth';
import CritiqueAnalyser from '@components/llm/critique-analyser/CritiqueAnalyser';

// withAuth only checks session presence. Real authorization is server-side —
// critiques_* actions require tenant_admin+; others 403 on every fetch.
function InternalCritiques() {
  return (
    <ErrorBoundary>
      <Box sx={{ p: 'var(--ds-space-5)' }}>
        <Box sx={{ mb: 'var(--ds-space-4)' }}>
          <Box sx={{ fontSize: 'var(--ds-text-heading-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-800)' }}>
            Critique Analytics
          </Box>
          <Box sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>
            Internal — cross-tenant answer-critique quality, not visible to customers.
          </Box>
        </Box>
        <CritiqueAnalyser />
      </Box>
    </ErrorBoundary>
  );
}

export default withAuth(InternalCritiques);
