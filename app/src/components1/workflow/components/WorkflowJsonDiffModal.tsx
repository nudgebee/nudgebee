import React from 'react';
import { Dialog, DialogTitle, DialogContent, DialogActions, Box, Typography } from '@mui/material';
import { MergeView } from '@codemirror/merge';
import { EditorView } from '@codemirror/view';
import { EditorState } from '@codemirror/state';
import { json } from '@codemirror/lang-json';
import CustomButton from '@components1/common/NewCustomButton';
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
              '.cm-content': { fontFamily: 'monospace', fontSize: '13px' },
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
              '.cm-content': { fontFamily: 'monospace', fontSize: '13px' },
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
    <Dialog
      open={open}
      onClose={onClose}
      maxWidth='xl'
      fullWidth
      PaperProps={{
        sx: {
          height: '80vh',
          maxHeight: '900px',
          borderRadius: '12px',
        },
      }}
    >
      <DialogTitle
        sx={{
          backgroundColor: colors.background.white,
          borderBottom: `1px solid ${colors.border.primary}`,
          py: 2,
          px: 3,
        }}
      >
        <Typography variant='h6' sx={{ color: colors.text.secondary, fontWeight: 600, fontSize: '18px' }}>
          Review LLM-Generated Automation Changes
        </Typography>
        <Typography variant='body2' sx={{ color: colors.text.tertiary, mt: 0.5, fontSize: '13px' }}>
          Compare your current automation (left) with LLM-generated automation (right)
        </Typography>
      </DialogTitle>

      <DialogContent
        sx={{
          p: 0,
          height: '70vh',
          minHeight: '500px',
          overflow: 'hidden',
          backgroundColor: colors.background.white,
        }}
      >
        {/* Column headers */}
        <Box
          sx={{
            display: 'flex',
            borderBottom: `1px solid ${colors.border.primary}`,
            backgroundColor: colors.background.secondary,
          }}
        >
          <Box sx={{ flex: 1, px: 2, py: 1, borderRight: `1px solid ${colors.border.primary}` }}>
            <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.secondary }}>Current Automation</Typography>
          </Box>
          <Box sx={{ flex: 1, px: 2, py: 1 }}>
            <Typography sx={{ fontSize: '12px', fontWeight: 600, color: colors.text.secondary }}>LLM-Generated Automation</Typography>
          </Box>
        </Box>
        <Box
          ref={editorRef}
          sx={{
            width: '100%',
            height: 'calc(100% - 36px)',
            '& .cm-merge': {
              height: '100%',
            },
            '& .cm-mergeView': {
              height: '100%',
            },
            '& .cm-editor': {
              height: '100%',
            },
            '& .cm-gutters': {
              backgroundColor: colors.background.secondary,
              borderRight: `1px solid ${colors.border.primary}`,
            },
          }}
        />
      </DialogContent>

      <DialogActions
        sx={{
          backgroundColor: colors.background.white,
          borderTop: `1px solid ${colors.border.primary}`,
          p: 2,
          gap: 1,
        }}
      >
        <CustomButton text='Cancel' variant='tertiary' size='Medium' onClick={onClose} />
        <CustomButton text='Discard Changes' variant='secondary' size='Medium' onClick={onReject} />
        <CustomButton text='Apply to Editor' variant='primary' size='Medium' onClick={onApply} />
      </DialogActions>
    </Dialog>
  );
};

export default WorkflowJsonDiffModal;
