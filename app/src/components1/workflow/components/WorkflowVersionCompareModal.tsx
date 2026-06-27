import React, { useEffect, useRef, useState } from 'react';
import { Box, Typography, CircularProgress, MenuItem, Select as MuiSelect, FormControl, InputLabel } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { MergeView } from '@codemirror/merge';
import { EditorView } from '@codemirror/view';
import { EditorState } from '@codemirror/state';
import { json } from '@codemirror/lang-json';
import { colors } from 'src/utils/colors';
import apiWorkflow from '@api1/workflow';
import type { WorkflowVersionEntry } from '../WorkflowBuilderNotebook';

interface WorkflowVersionCompareModalProps {
  open: boolean;
  onClose: () => void;
  accountId: string;
  workflowId: string;
  versions: WorkflowVersionEntry[];
  preselectedBase?: WorkflowVersionEntry;
}

const WorkflowVersionCompareModal: React.FC<WorkflowVersionCompareModalProps> = ({
  open, onClose, accountId, workflowId, versions, preselectedBase,
}) => {
  const editorRef = useRef<HTMLDivElement>(null);
  const mergeViewRef = useRef<MergeView | null>(null);
  const [baseVersion, setBaseVersion] = useState<number | ''>('');
  const [compareVersion, setCompareVersion] = useState<number | ''>('');
  const [baseJson, setBaseJson] = useState<string>('');
  const [compareJson, setCompareJson] = useState<string>('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open && preselectedBase) setBaseVersion(preselectedBase.version_number);
    if (!open) { setBaseVersion(''); setCompareVersion(''); setBaseJson(''); setCompareJson(''); setError(null); }
  }, [open, preselectedBase]);

  async function handleCompare() {
    if (!baseVersion || !compareVersion) return;
    if (baseVersion === compareVersion) { setError('Please select two different versions.'); return; }
    setLoading(true);
    setError(null);
    try {
      const [baseRes, compareRes]: [any, any] = await Promise.all([
        apiWorkflow.getWorkflowVersion(accountId, workflowId, baseVersion as number),
        apiWorkflow.getWorkflowVersion(accountId, workflowId, compareVersion as number),
      ]);
      const baseDef = baseRes?.data?.workflow_get_version?.definition;
      const compareDef = compareRes?.data?.workflow_get_version?.definition;
      if (!baseDef || !compareDef) { setError('Could not load version definitions.'); return; }
      setBaseJson(JSON.stringify(baseDef, null, 2));
      setCompareJson(JSON.stringify(compareDef, null, 2));
    } catch (err) {
      console.error('Failed to compare versions:', err);
      setError('Failed to fetch versions. Please try again.');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (!baseJson || !compareJson || !editorRef.current) {
      mergeViewRef.current?.destroy();
      mergeViewRef.current = null;
      return;
    }
    editorRef.current.innerHTML = '';
    try {
      mergeViewRef.current = new MergeView({
        a: { doc: baseJson, extensions: [json(), EditorView.editable.of(false), EditorState.readOnly.of(true), EditorView.theme({ '&': { height: '100%' }, '.cm-scroller': { overflow: 'auto' } })] },
        b: { doc: compareJson, extensions: [json(), EditorView.editable.of(false), EditorState.readOnly.of(true), EditorView.theme({ '&': { height: '100%' }, '.cm-scroller': { overflow: 'auto' } })] },
        parent: editorRef.current,
      });
    } catch (err) { console.error('MergeView init failed:', err); }
    return () => { mergeViewRef.current?.destroy(); mergeViewRef.current = null; };
  }, [baseJson, compareJson]);

  const canCompare = baseVersion !== '' && compareVersion !== '' && baseVersion !== compareVersion && !loading;

  return (
    <Modal open={open} handleClose={onClose} width='xl' title='Compare Versions' subtitle='Select a base and compare version to see what changed' maxHeight='90vh' contentStyles={{ padding: 0 }}
      actionButtons={<Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, p: 2 }}><Button tone='ghost' size='md' onClick={onClose}>Close</Button></Box>}
    >
      <Box sx={{ p: 2, display: 'flex', gap: 2, alignItems: 'flex-end', borderBottom: `1px solid ${colors.border.primary}`, flexWrap: 'wrap' }}>
        <FormControl size='small' sx={{ minWidth: 220 }}>
          <InputLabel>Base Version</InputLabel>
          <MuiSelect value={baseVersion} label='Base Version' onChange={(e) => { setBaseVersion(e.target.value as number); setError(null); }}>
            {versions.map((v) => <MenuItem key={v.id} value={v.version_number}>v{v.version_number}{v.name ? ` — ${v.name}` : ''}{v.is_live ? ' (Live)' : ''}</MenuItem>)}
          </MuiSelect>
        </FormControl>
        <FormControl size='small' sx={{ minWidth: 220 }}>
          <InputLabel>Compare Version</InputLabel>
          <MuiSelect value={compareVersion} label='Compare Version' onChange={(e) => { setCompareVersion(e.target.value as number); setError(null); }}>
            {versions.map((v) => <MenuItem key={v.id} value={v.version_number}>v{v.version_number}{v.name ? ` — ${v.name}` : ''}{v.is_live ? ' (Live)' : ''}</MenuItem>)}
          </MuiSelect>
        </FormControl>
        <Button tone='primary' size='md' onClick={handleCompare} disabled={!canCompare}>{loading ? 'Loading…' : 'Compare'}</Button>
        {error && <Typography variant='caption' color='error' sx={{ width: '100%', mt: 0.5 }}>{error}</Typography>}
      </Box>
      <Box sx={{ height: '65vh', overflow: 'hidden', backgroundColor: colors.background.white }}>
        {loading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}><CircularProgress size={32} /></Box>
        ) : !baseJson ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100%' }}><Typography color='text.secondary'>Select two versions above and click Compare</Typography></Box>
        ) : (
          <>
            <Box sx={{ display: 'flex', borderBottom: `1px solid ${colors.border.primary}`, backgroundColor: colors.background.secondary }}>
              <Box sx={{ flex: 1, px: 2, py: 1, borderRight: `1px solid ${colors.border.primary}` }}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>Base — v{baseVersion}</Typography>
              </Box>
              <Box sx={{ flex: 1, px: 2, py: 1 }}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>Compare — v{compareVersion}</Typography>
              </Box>
            </Box>
            <Box ref={editorRef} sx={{ width: '100%', height: 'calc(100% - 36px)', '& .cm-merge': { height: '100%' }, '& .cm-mergeView': { height: '100%' }, '& .cm-editor': { height: '100%' }, '& .cm-gutters': { backgroundColor: colors.background.secondary, borderRight: `1px solid ${colors.border.primary}` } }} />
          </>
        )}
      </Box>
    </Modal>
  );
};

export default WorkflowVersionCompareModal;
