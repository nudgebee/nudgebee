import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { CodeEditor } from '@ui/CodeEditor';
import { DiffViewer } from '@ui/DiffViewer';
import { snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';
import type { AccountOption } from '@api1/dashboards';
import { dashboardJsonText, readDashboardJson, type EditableDashboard } from './dashboardJson';

/** Same cap, and for the same reason, as the importer's: the text re-parses on every change. */
const MAX_UPLOAD_MB = 2;
const MAX_UPLOAD_BYTES = MAX_UPLOAD_MB * 1024 * 1024;

/**
 * Past this many lines the side-by-side diff stops being quick: it draws every
 * line of both versions, and a 111-panel dashboard (2,745 lines) took ~540ms to
 * open in Chromium. The unified view draws only the changed sections — ~20ms on
 * the same dashboard — so a long dashboard gets that instead.
 */
const SIDE_BY_SIDE_MAX_LINES = 600;

interface Props {
  open: boolean;
  /** The edit-mode draft the JSON replaces. */
  current: EditableDashboard;
  accountOptions: AccountOption[];
  /**
   * Reopens the editor with the reason Save refused the draft — so the fix
   * happens where the JSON is, not on a grid of panels.
   */
  reopen?: { error: string } | null;
  onClose: () => void;
  onApply: (dashboard: EditableDashboard) => void;
}

const Notice: React.FC<{ tone: 'error' | 'warning'; items: string[]; testId: string; lead?: string }> = ({ tone, items, testId, lead }) => (
  <Box
    data-testid={testId}
    sx={{
      p: ds.space[3],
      border: `1px solid ${tone === 'error' ? ds.red[300] : ds.amber[300]}`,
      background: tone === 'error' ? ds.red[100] : ds.amber[100],
      borderRadius: ds.radius.md,
      display: 'grid',
      gap: ds.space[1],
    }}
  >
    {lead && (
      <Typography variant='body2' sx={{ color: ds.gray[700], fontWeight: ds.weight.semibold }}>
        {lead}
      </Typography>
    )}
    {items.map((item) => (
      <Typography key={item} variant='body2' sx={{ color: ds.gray[700] }}>
        {item}
      </Typography>
    ))}
  </Box>
);

/**
 * The dashboard as JSON, edited or replaced by an uploaded file, with the
 * difference shown before it goes into the draft.
 *
 * Apply changes only the edit-mode draft — the canvas redraws from it, and the
 * normal Save stores it as a new version (or Cancel throws it away). Nothing in
 * here writes to the server.
 */
const EditDashboardJsonModal: React.FC<Props> = ({ open, current, accountOptions, reopen, onClose, onApply }) => {
  const [text, setText] = useState('');
  const [step, setStep] = useState<'edit' | 'review'>('edit');
  const [fileName, setFileName] = useState('');
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  /** A FileReader that finishes after the modal closed or re-filled must not overwrite it. */
  const uploadSeq = useRef(0);

  const original = useMemo(() => (open ? dashboardJsonText(current) : ''), [open, current]);

  // Filled on every opening from the draft — also when Save refused it and sent the author back here.
  useEffect(() => {
    if (!open) return;
    uploadSeq.current += 1;
    setText(original);
    setStep('edit');
    setFileName('');
    // Keyed on opening alone: `original` is read at that moment, and re-running
    // on it would throw away what the author has typed since.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const result = useMemo(
    () => (open && text.trim() ? readDashboardJson(text, accountOptions, current) : null),
    [open, text, accountOptions, current]
  );
  const canReview = Boolean(result?.ok && result.changed);
  const unchanged = Boolean(result?.ok && !result.changed);
  const sideBySide = result?.ok ? Math.max(original.split('\n').length, result.text.split('\n').length) <= SIDE_BY_SIDE_MAX_LINES : true;

  const handleFileUpload = (file: File | undefined) => {
    if (!file) return;
    if (!/\.json$/i.test(file.name) && file.type !== 'application/json') {
      snackbar.error('Only a .json file can be uploaded.');
      return;
    }
    if (file.size > MAX_UPLOAD_BYTES) {
      snackbar.error(`"${file.name}" is larger than ${MAX_UPLOAD_MB} MB.`);
      return;
    }
    const seq = ++uploadSeq.current;
    const reader = new FileReader();
    reader.onload = () => {
      if (seq !== uploadSeq.current) return;
      setText(String(reader.result ?? ''));
      setFileName(file.name);
      setStep('edit');
    };
    reader.onerror = () => {
      if (seq !== uploadSeq.current) return;
      snackbar.error(`Could not read "${file.name}".`);
    };
    reader.readAsText(file);
  };

  const close = () => {
    uploadSeq.current += 1;
    onClose();
  };

  const apply = () => {
    if (!result?.ok) return;
    onApply(result.dashboard);
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title={step === 'review' ? 'Review changes' : 'Edit dashboard JSON'}
      subtitle={
        step === 'review' ? 'The current draft on the left, your JSON on the right. Nothing is saved until you Save the dashboard.' : undefined
      }
      width='xl'
      backdropClickClose={false}
      actionButtons={
        <Stack direction='row' gap={ds.space[3]} alignItems='center' sx={{ width: '100%' }}>
          {step === 'edit' && unchanged && (
            <Typography variant='caption' sx={{ color: ds.gray[500], mr: 'auto' }} data-testid='dashboard-json-unchanged'>
              No changes — this is the dashboard as it is.
            </Typography>
          )}
          <Box sx={{ flex: 1 }} />
          {step === 'review' ? (
            <>
              <Button tone='secondary' onClick={() => setStep('edit')} id='dashboard-json-back-btn'>
                Back to JSON
              </Button>
              <Button onClick={apply} id='dashboard-json-apply-btn' data-testid='dashboard-json-apply-btn'>
                Apply to draft
              </Button>
            </>
          ) : (
            <>
              <Button tone='secondary' onClick={close} id='dashboard-json-cancel-btn'>
                Cancel
              </Button>
              <Button onClick={() => setStep('review')} disabled={!canReview} id='dashboard-json-review-btn' data-testid='dashboard-json-review-btn'>
                Review changes
              </Button>
            </>
          )}
        </Stack>
      }
    >
      {step === 'edit' ? (
        <Stack gap={ds.space[3]}>
          {reopen?.error && (
            <Notice
              tone='error'
              testId='dashboard-json-save-error'
              lead='Save refused the dashboard. Fix the JSON and apply it again.'
              items={[reopen.error]}
            />
          )}
          <Stack direction='row' alignItems='center' gap={ds.space[3]} flexWrap='wrap'>
            <Typography variant='caption' sx={{ color: ds.gray[500], flex: 1, minWidth: 0 }}>
              The dashboard as Save stores it. Edit it here, or upload a file exported from the Dashboards list. Applying changes the draft only.
            </Typography>
            <Button tone='secondary' size='sm' onClick={() => setText(original)} disabled={text === original} id='dashboard-json-reset-btn'>
              Start over
            </Button>
            <Button tone='secondary' size='sm' onClick={() => fileInputRef.current?.click()} id='dashboard-json-upload-btn'>
              Upload JSON file
            </Button>
            <input
              ref={fileInputRef}
              type='file'
              accept='application/json,.json'
              hidden
              data-testid='dashboard-json-file-input'
              onChange={(e) => {
                handleFileUpload(e.target.files?.[0]);
                e.target.value = '';
              }}
            />
          </Stack>
          {fileName && (
            <Typography variant='caption' sx={{ color: ds.gray[500] }} data-testid='dashboard-json-file-name' noWrap title={fileName}>
              Loaded {fileName}
            </Typography>
          )}
          <CodeEditor value={text} onChange={setText} language='json' height={420} />
          {result && !result.ok && <Notice tone='error' testId='dashboard-json-errors' items={result.errors} />}
          {result?.ok && result.warnings.length > 0 && <Notice tone='warning' testId='dashboard-json-warnings' items={result.warnings} />}
        </Stack>
      ) : (
        result?.ok && (
          <Stack gap={ds.space[3]}>
            {!sideBySide && (
              <Typography variant='caption' sx={{ color: ds.gray[500] }} data-testid='dashboard-json-diff-unified'>
                Showing only the changed sections — this dashboard is too long to show side by side.
              </Typography>
            )}
            <DiffViewer
              originalCode={original}
              newCode={result.text}
              language='json'
              mode={sideBySide ? 'split' : 'unified'}
              leftLabel='Current draft'
              rightLabel='Your JSON'
              title='Dashboard JSON'
              fileName='dashboard.json'
              maxHeight={480}
              data-testid='dashboard-json-diff'
            />
            {result.warnings.length > 0 && <Notice tone='warning' testId='dashboard-json-warnings' items={result.warnings} />}
          </Stack>
        )
      )}
    </Modal>
  );
};

export default EditDashboardJsonModal;
