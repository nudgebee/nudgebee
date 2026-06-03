import React from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import { MergeView } from '@codemirror/merge';
import { EditorView } from '@codemirror/view';
import { EditorState } from '@codemirror/state';
import { json } from '@codemirror/lang-json';
import { Button } from '@components1/ds/Button';
import { colors } from 'src/utils/colors';

interface WorkflowJsonDiffModalProps {
  open: boolean;
  onClose: () => void;
  originalJson: string;
  proposedJson: string;
  onApply: () => void;
  onReject: () => void;
}

/**
 * WorkflowJsonDiffModal - Shows side-by-side diff of current vs. LLM-generated workflow JSON
 *
 * Uses CodeMirror's MergeView for professional diff visualization with:
 * - Syntax highlighting for JSON
 * - Line-by-line comparison
 * - Visual indicators for additions/deletions
 * - Read-only editors
 */
const WorkflowJsonDiffModal: React.FC<WorkflowJsonDiffModalProps> = ({ open, onClose, originalJson, proposedJson, onApply, onReject }) => {
  const editorRef = React.useRef<HTMLDivElement>(null);
  const mergeViewRef = React.useRef<MergeView | null>(null);

  // Initialize MergeView when modal opens
  React.useEffect(() => {
    if (!open || !editorRef.current) {
      // Cleanup previous instance
      if (mergeViewRef.current) {
        mergeViewRef.current.destroy();
        mergeViewRef.current = null;
      }
      return;
    }

    // Clear container
    editorRef.current.innerHTML = '';

    // Use empty object as fallback for originalJson when creating new workflow
    const safeOriginalJson = originalJson || '{}';
    const safeProposedJson = proposedJson || '{}';

    try {
      // Create MergeView instance
      const mergeView = new MergeView({
        a: {
          doc: safeOriginalJson,
          extensions: [
            json(),
            EditorView.editable.of(false), // Read-only
            EditorState.readOnly.of(true),
            EditorView.theme({
              '&': { height: '100%' },
              '.cm-scroller': { overflow: 'auto' },
              '.cm-content': { fontFamily: 'monospace', fontSize: 'var(--ds-text-body)' },
            }),
          ],
        },
        b: {
          doc: safeProposedJson,
          extensions: [
            json(),
            EditorView.editable.of(false), // Read-only
            EditorState.readOnly.of(true),
            EditorView.theme({
              '&': { height: '100%' },
              '.cm-scroller': { overflow: 'auto' },
              '.cm-content': { fontFamily: 'monospace', fontSize: 'var(--ds-text-body)' },
            }),
          ],
        },
        parent: editorRef.current,
      });

      mergeViewRef.current = mergeView;
    } catch (error) {
      console.error('Failed to initialize MergeView:', error);
    }

    return () => {
      if (mergeViewRef.current) {
        mergeViewRef.current.destroy();
        mergeViewRef.current = null;
      }
    };
  }, [open, originalJson, proposedJson]);

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='xl'
      title='Review LLM-Generated Automation Changes'
      subtitle='Compare your current automation (left) with LLM-generated automation (right)'
      maxHeight='80vh'
      contentStyles={{ padding: 0 }}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, p: 2 }}>
          <Button tone='ghost' size='md' onClick={onClose}>
            Cancel
          </Button>
          <Button tone='secondary' size='md' onClick={onReject}>
            Discard Changes
          </Button>
          <Button tone='primary' size='md' onClick={onApply}>
            Apply to Editor
          </Button>
        </Box>
      }
    >
      <Box sx={{ height: '70vh', minHeight: '500px', overflow: 'hidden', backgroundColor: colors.background.white }}>
        {/* Column headers */}
        <Box
          sx={{
            display: 'flex',
            borderBottom: `1px solid ${colors.border.primary}`,
            backgroundColor: colors.background.secondary,
          }}
        >
          <Box sx={{ flex: 1, px: 2, py: 1, borderRight: `1px solid ${colors.border.primary}` }}>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
              Current Automation
            </Typography>
          </Box>
          <Box sx={{ flex: 1, px: 2, py: 1 }}>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
              LLM-Generated Automation
            </Typography>
          </Box>
        </Box>
        <Box
          ref={editorRef}
          sx={{
            width: '100%',
            height: 'calc(100% - 36px)',
            '& .cm-merge': { height: '100%' },
            '& .cm-mergeView': { height: '100%' },
            '& .cm-editor': { height: '100%' },
            '& .cm-gutters': {
              backgroundColor: colors.background.secondary,
              borderRight: `1px solid ${colors.border.primary}`,
            },
          }}
        />
      </Box>
    </Modal>
  );
};

export default WorkflowJsonDiffModal;
