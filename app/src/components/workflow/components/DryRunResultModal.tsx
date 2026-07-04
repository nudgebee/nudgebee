import { Box, Typography, Chip } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import JsonTreeView from '@shared/viewers/JsonTreeView';
import type { WorkflowDryRunResponse } from '@api1/workflow/types';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { getLlmSessionId, buildAskNudgebeeHref } from '../utils/llmChat';

interface DryRunResultModalProps {
  open: boolean;
  onClose: () => void;
  result: WorkflowDryRunResponse | null;
  // Account context for the Ask-Nudgebee deep link on LLM tasks.
  accountId: string;
  // The draft was run against an inline definition, not the published live
  // version. When provided, the modal shows "Dry run of draft based on vN" so
  // users know exactly what they tested.
  draftVersionNumber?: number;
}

const DryRunResultModal: React.FC<DryRunResultModalProps> = ({ open, onClose, result, accountId, draftVersionNumber }) => {
  const { assistantName, nubiIconUrl } = useTenantBranding();

  if (!result) {
    return null;
  }

  const isSuccess = result.status === 'COMPLETED';
  const isFailed = result.status === 'FAILED';

  return (
    <Modal open={open} handleClose={onClose} title='Dry Run Result' width='lg' maxHeight='80vh'>
      <Box sx={{ padding: 'var(--ds-space-4) 0' }}>
        {/* Header is always rendered so users know they're looking at a draft
            run — never the live version — even before the workflow has a
            draft_version_id (legacy rows / pre-V747 backfill quirks). The "based
            on vN" qualifier appears only when the lineage is known. */}
        <Typography variant='body2' sx={{ mb: 2, color: 'var(--ds-gray-600)', fontStyle: 'italic' }} data-testid='dry-run-result-version'>
          Dry run of draft{draftVersionNumber !== undefined ? ` based on v${draftVersionNumber}` : ''}
        </Typography>
        {/* Status Badge */}
        <Box sx={{ mb: 3, display: 'flex', alignItems: 'center', gap: 2 }}>
          <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
            Status:
          </Typography>
          <Chip
            label={result.status}
            size='small'
            sx={{
              backgroundColor: isSuccess ? 'var(--ds-green-100)' : isFailed ? 'var(--ds-red-100)' : 'var(--ds-gray-200)',
              color: isSuccess ? 'var(--ds-green-700)' : isFailed ? 'var(--ds-red-700)' : 'var(--ds-gray-600)',
              fontWeight: 'var(--ds-font-weight-semibold)',
            }}
          />
        </Box>

        {/* Error Message */}
        {result.error && (
          <Box sx={{ mb: 3 }}>
            <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 1, color: 'var(--ds-red-700)' }}>
              Error:
            </Typography>
            <Box
              sx={{
                backgroundColor: 'var(--ds-red-100)',
                border: '1px solid var(--ds-red-200)',
                borderRadius: 'var(--ds-radius-lg)',
                padding: 'var(--ds-space-3)',
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-small)',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
                color: 'var(--ds-red-700)',
                overflow: 'auto',
                maxHeight: '300px',
              }}
            >
              {result.error}
            </Box>
          </Box>
        )}

        {/* Output */}
        {result.output && (
          <Box sx={{ mb: 3 }}>
            <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 1, color: 'var(--ds-brand-500)' }}>
              Output:
            </Typography>
            <JsonTreeView data={result.output} defaultExpanded={2} maxHeight='300px' fontSize='var(--ds-text-small)' />
          </Box>
        )}

        {/* Per-Task Results (if available) */}
        {result.tasks && result.tasks.length > 0 && (
          <Box>
            <Typography variant='subtitle2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 2, color: 'var(--ds-brand-500)' }}>
              Task Results:
            </Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {result.tasks.map((task) => (
                <Box
                  key={task.id}
                  sx={{
                    backgroundColor: 'var(--ds-background-200)',
                    border: '1px solid var(--ds-brand-150)',
                    borderRadius: 'var(--ds-radius-lg)',
                    padding: 'var(--ds-space-3)',
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 1 }}>
                    <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
                      {task.id}
                    </Typography>
                    <Chip
                      label={task.status}
                      size='small'
                      sx={{
                        backgroundColor:
                          task.status === 'COMPLETED' ? 'var(--ds-green-100)' : task.status === 'FAILED' ? 'var(--ds-red-100)' : 'var(--ds-gray-200)',
                        color:
                          task.status === 'COMPLETED' ? 'var(--ds-green-700)' : task.status === 'FAILED' ? 'var(--ds-red-700)' : 'var(--ds-gray-600)',
                        fontSize: 'var(--ds-text-caption)',
                      }}
                    />
                    {(() => {
                      const sid = getLlmSessionId(task);
                      if (!sid) return null;
                      return (
                        <Button
                          size='xs'
                          tone='secondary'
                          icon={<SafeIcon alt={`Ask ${assistantName}`} src={nubiIconUrl} height={20} width={20} />}
                          data-testid='dry-run-go-to-chat-btn'
                          onClick={() => window.open(buildAskNudgebeeHref(accountId, sid), '_blank', 'noopener,noreferrer')}
                        >
                          Go to chat
                        </Button>
                      );
                    })()}
                  </Box>
                  {task.error && (
                    <Typography variant='caption' sx={{ color: 'var(--ds-red-700)', display: 'block', mb: 1 }}>
                      Error: {task.error}
                    </Typography>
                  )}
                  {task.output && <JsonTreeView data={task.output} defaultExpanded={1} maxHeight='150px' fontSize='var(--ds-text-caption)' />}
                </Box>
              ))}
            </Box>
          </Box>
        )}
      </Box>
    </Modal>
  );
};

export default DryRunResultModal;
