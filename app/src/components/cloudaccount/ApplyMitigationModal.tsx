import { useState, useCallback, useMemo } from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import { Button as DsButton } from '@ui/Button';
import { Modal } from '@ui/Modal';
import { Card } from '@ui/Card';
import { Checkbox } from '@ui/Checkbox';
import CopyButton from '@shared/buttons/CopyButton';
import CommandExecutionDetail, { CommandEntry } from './CommandExecutionDetail';
import apiCloudAccount from '@api1/cloud-account';

interface ApplyMitigationModalProps {
  markdowns: string | null;
  accountId?: string;
  recommendationId?: string;
  canExecute?: boolean;
  // Fired after a command run completes so the sibling execution-history tab
  // can refresh in real time.
  onExecuted?: () => void;
}

// A leading "[optional]" marker (case-insensitive) on a fenced command means
// the step isn't required to apply the recommendation (e.g. restarting an
// instance after a resize) — surfaced as a skippable checkbox instead of a
// mandatory step.
const OPTIONAL_MARKER = /^\[optional\]\s*/i;

// Same marker, anchored to right after a fence opens (with its own leading
// whitespace/newline), for stripping out of the raw markdown shown to the
// user — the marker is metadata for this component, not something to render.
const OPTIONAL_MARKER_IN_FENCE = /(```[\w]*[ \t]*\n?[ \t]*)\[optional\]\s*/gi;

export function stripOptionalMarkers(markdown: string | null): string | null {
  if (!markdown) return markdown;
  return markdown.replace(OPTIONAL_MARKER_IN_FENCE, '$1');
}

interface ParsedCommand {
  command: string;
  optional: boolean;
}

function extractCommandsFromMarkdown(markdown: string | null): ParsedCommand[] {
  if (!markdown) return [];
  const commands: ParsedCommand[] = [];
  const fenced = /```(?:\w+)?\n?([\s\S]*?)```/g;
  let match;
  while ((match = fenced.exec(markdown)) !== null) {
    const raw = match[1].trim();
    const optional = OPTIONAL_MARKER.test(raw);
    const cmd = raw.replace(OPTIONAL_MARKER, '').trim();
    if (cmd) commands.push({ command: cmd, optional });
  }
  return commands;
}

const ApplyMitigationModal: React.FC<ApplyMitigationModalProps> = ({ markdowns, accountId, recommendationId, canExecute = false, onExecuted }) => {
  // Memoize so the reference is stable across renders; otherwise handleConfirm's
  // useCallback would be recreated every render.
  const cliCommands = useMemo(() => extractCommandsFromMarkdown(markdowns), [markdowns]);

  const [showModal, setShowModal] = useState(false);
  const [isExecuting, setIsExecuting] = useState(false);
  const [executionResults, setExecutionResults] = useState<CommandEntry[] | null>(null);
  // Optional steps are included by default; unchecking excludes them from the run.
  const [skippedOptional, setSkippedOptional] = useState<Set<number>>(new Set());

  const selectedCommands = useMemo(() => cliCommands.filter((_, i) => !skippedOptional.has(i)).map((c) => c.command), [cliCommands, skippedOptional]);

  const handleApplyAll = useCallback(() => {
    setExecutionResults(null);
    setSkippedOptional(new Set());
    setShowModal(true);
  }, []);

  const toggleOptional = useCallback((index: number, checked: boolean) => {
    setSkippedOptional((prev) => {
      const next = new Set(prev);
      if (checked) next.delete(index);
      else next.add(index);
      return next;
    });
  }, []);

  const handleConfirm = useCallback(async () => {
    if (!accountId || selectedCommands.length === 0) return;
    setIsExecuting(true);
    const res = await apiCloudAccount.executeCommand({ account_id: accountId, commands: selectedCommands, recommendation_id: recommendationId });
    setExecutionResults(res.data?.results ?? []);
    setIsExecuting(false);
    // Only refresh history when a run actually produced results — a failed
    // execution writes no CLI_EXECUTE audit row, so a re-fetch would be wasted.
    if (res.data?.results) {
      onExecuted?.();
    }
  }, [selectedCommands, accountId, recommendationId, onExecuted]);

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
        subtitle={showConfirmation ? `${selectedCommands.length} command${selectedCommands.length !== 1 ? 's' : ''} will be executed` : undefined}
        width='md'
        sx={isExecuting ? { '& #close-modal-btn': { opacity: 0.3, cursor: 'not-allowed', pointerEvents: 'none' } } : undefined}
        confirmText={showConfirmation ? 'Execute' : undefined}
        onConfirm={showConfirmation ? handleConfirm : undefined}
        confirmDisabled={isExecuting || selectedCommands.length === 0}
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
            {cliCommands.map(({ command, optional }, i) => (
              <Box key={i}>
                {cliCommands.length > 1 && (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      mb: 'var(--ds-space-1)',
                    }}
                  >
                    <Box sx={{ fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                      Command {i + 1} of {cliCommands.length}
                    </Box>
                    {optional && (
                      <Checkbox
                        size='sm'
                        checked={!skippedOptional.has(i)}
                        onChange={(checked) => toggleOptional(i, checked)}
                        label='Include this optional step'
                      />
                    )}
                  </Box>
                )}
                <Card variant='tinted' tone='neutral' size='sm' elevation='flat'>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', opacity: skippedOptional.has(i) ? 0.5 : 1 }}>
                    <Box sx={{ flex: 1, fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-small)', wordBreak: 'break-all' }}>{command}</Box>
                    <CopyButton text={command} size='sm' />
                  </Box>
                </Card>
              </Box>
            ))}
          </Box>
        ) : (
          <CommandExecutionDetail
            commands={executionResults ?? selectedCommands.map((c) => ({ command: c }))}
            status={isExecuting ? 'RUNNING' : undefined}
          />
        )}
      </Modal>
    </>
  );
};

export default ApplyMitigationModal;
