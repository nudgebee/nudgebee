import { useState, useCallback } from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import { Button as DsButton } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { Card } from '@ui/Card';
import CopyButton from '@shared/buttons/CopyButton';
import CommandExecutionDetail, { CommandEntry } from './CommandExecutionDetail';
import apiCloudAccount from '@api1/cloud-account';

interface ApplyMitigationModalProps {
  markdowns: string | null;
  accountId?: string;
  recommendationId?: string;
  canExecute?: boolean;
}

function extractCommandsFromMarkdown(markdown: string | null): string[] {
  if (!markdown) return [];
  const commands: string[] = [];
  const fenced = /```(?:\w+)?\n?([\s\S]*?)```/g;
  let match;
  while ((match = fenced.exec(markdown)) !== null) {
    const cmd = match[1].trim();
    // Skip commands with unresolved {{...}} template placeholders: the source
    // recommendation is missing the field, so executing would send literal
    // braces to the cloud CLI and fail (e.g. AWS exit 252). Better to hide the
    // command than run a guaranteed-broken one.
    if (cmd && !cmd.includes('{{')) commands.push(cmd);
  }
  return commands;
}

const ApplyMitigationModal: React.FC<ApplyMitigationModalProps> = ({ markdowns, accountId, recommendationId, canExecute = false }) => {
  const cliCommands = extractCommandsFromMarkdown(markdowns);

  const [showModal, setShowModal] = useState(false);
  const [isExecuting, setIsExecuting] = useState(false);
  const [executionResults, setExecutionResults] = useState<CommandEntry[] | null>(null);

  const handleApplyAll = useCallback(() => {
    setExecutionResults(null);
    setShowModal(true);
  }, []);

  const handleConfirm = useCallback(async () => {
    if (!accountId || cliCommands.length === 0) return;
    setIsExecuting(true);
    const res = await apiCloudAccount.executeCommand({ account_id: accountId, commands: cliCommands, recommendation_id: recommendationId });
    setExecutionResults(res.data?.results ?? []);
    setIsExecuting(false);
  }, [cliCommands, accountId, recommendationId]);

  const handleModalClose = useCallback(() => {
    if (isExecuting) return;
    setShowModal(false);
  }, [isExecuting]);

  if (!canExecute || cliCommands.length === 0) return null;

  const showConfirmation = !isExecuting && !executionResults;

  return (
    <>
      <DsButton
        size='sm'
        tone='primary'
        onClick={handleApplyAll}
        id='apply-commands-btn'
        icon={<PlayArrowIcon sx={{ fontSize: 18, color: ds.green[500] }} />}
      >
        Apply Mitigation
      </DsButton>

      <Modal
        open={showModal}
        handleClose={isExecuting ? undefined : handleModalClose}
        backdropClickClose={!isExecuting}
        title={executionResults ? 'Command Results' : 'Apply Mitigation'}
        subtitle={showConfirmation ? `${cliCommands.length} command${cliCommands.length !== 1 ? 's' : ''} will be executed` : undefined}
        width='md'
        sx={isExecuting ? { '& #close-modal-btn': { opacity: 0.3, cursor: 'not-allowed', pointerEvents: 'none' } } : undefined}
        confirmText={showConfirmation ? 'Execute' : undefined}
        onConfirm={showConfirmation ? handleConfirm : undefined}
        confirmDisabled={isExecuting}
        isCancelRequired={showConfirmation}
        isConfirmRequired={showConfirmation}
        actionButtons={
          !showConfirmation ? (
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
              <DsButton size='sm' tone='secondary' onClick={handleModalClose} disabled={isExecuting} id='command-output-close-btn'>
                Close
              </DsButton>
            </Box>
          ) : undefined
        }
      >
        {showConfirmation ? (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            {cliCommands.map((cmd, i) => (
              <Box key={i}>
                {cliCommands.length > 1 && (
                  <Box
                    sx={{
                      fontWeight: 'var(--ds-font-weight-medium)',
                      mb: 'var(--ds-space-1)',
                      fontSize: 'var(--ds-text-small)',
                      color: 'var(--ds-gray-600)',
                    }}
                  >
                    Command {i + 1} of {cliCommands.length}
                  </Box>
                )}
                <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                    <Box sx={{ flex: 1, fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-small)', wordBreak: 'break-all' }}>{cmd}</Box>
                    <CopyButton text={cmd} size='sm' />
                  </Box>
                </Card>
              </Box>
            ))}
          </Box>
        ) : (
          <CommandExecutionDetail
            commands={executionResults ?? cliCommands.map((c) => ({ command: c }))}
            status={isExecuting ? 'RUNNING' : undefined}
          />
        )}
      </Modal>
    </>
  );
};

export default ApplyMitigationModal;
