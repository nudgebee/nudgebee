import { Box } from '@mui/material';
import { Modal } from '@ui/Modal';
import { DiffViewer } from '@ui/DiffViewer';
import Text from '@shared/format/Text';
import { ds } from '@utils/colors';

// Undo changes a live workload. It used to fire straight from the row's button, so the only way to
// learn what it would do was to let it happen. This states the target and the resulting values, and
// makes the change take a deliberate second click.
//
// Everything shown is derived from the recorded state the server re-applies (see describeUndo), so
// the dialog cannot promise one change and the server perform another.

const labelSx = { fontSize: ds.text.small, color: ds.gray[600] };
const valueSx = { fontSize: ds.text.small, fontWeight: ds.weight.medium, fontFamily: 'monospace' };

const UndoConfirmDialog = ({ open, onClose, onConfirm, loading, label, actionTitle, target, preview }) => {
  if (!open) return null;

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='md'
      title={label || 'Undo'}
      subtitle={actionTitle ? `Undoes: ${actionTitle}` : undefined}
      confirmText={loading ? 'Undoing…' : label || 'Undo'}
      confirmDisabled={loading}
      onConfirm={onConfirm}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
        {target && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
            <Text value='TARGET' sx={{ ...labelSx, letterSpacing: '0.08em', fontWeight: ds.weight.semibold }} />
            <Text value={target} sx={valueSx} />
          </Box>
        )}

        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          <Text value={preview?.summary || 'This will change the workload.'} sx={{ fontSize: ds.text.small }} />

          {preview?.kind === 'values' && (
            <Box
              sx={{
                border: `1px solid ${ds.gray[200]}`,
                borderRadius: ds.radius.sm,
                display: 'flex',
                flexDirection: 'column',
              }}
            >
              {preview.groups.map((group, groupIndex) => (
                <Box
                  key={group.name}
                  sx={{
                    padding: ds.space[3],
                    borderTop: groupIndex === 0 ? 'none' : `1px solid ${ds.gray[200]}`,
                    display: 'flex',
                    flexDirection: 'column',
                    gap: ds.space[1],
                  }}
                >
                  <Text value={group.name} sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium }} />
                  {group.fields.map((field) => (
                    <Box key={field.label} sx={{ display: 'flex', gap: ds.space[2], alignItems: 'baseline' }}>
                      <Text value={field.label} sx={{ ...labelSx, minWidth: '120px' }} />
                      <Text value={field.value} sx={valueSx} />
                    </Box>
                  ))}
                </Box>
              ))}
            </Box>
          )}

          {preview?.kind === 'diff' && (
            <DiffViewer
              originalCode={preview.before}
              newCode={preview.after}
              language='yaml'
              leftLabel={preview.beforeLabel}
              rightLabel={preview.afterLabel}
            />
          )}
        </Box>

        {/* The rollout is the part people are surprised by — it is a restart of the workload, not a
            silent field edit. */}
        <Text
          value='Applying this rolls out the workload. It is recorded on this event like any other action, and can be undone again.'
          sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}
        />
      </Box>
    </Modal>
  );
};

export default UndoConfirmDialog;
