// Import one JSON document that declares Knowledge Graph nodes and the
// dependencies between them (kg_create_manual_declarations). Nodes land
// first; a dependency names a node of the document by its ref, or a node
// already in the graph by type, name and qualifiers.

import { useMemo, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Label } from '@ui/Label';
import { CodeEditor } from '@ui/CodeEditor';
import { CodeBlock } from '@ui/CodeBlock';
import { toast as snackbar } from '@ui/Toast';
import DownloadButton from '@shared/buttons/DownloadButton';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import ManualNodeTypeValues from './ManualNodeTypeValues';
import { buildSampleDeclarations } from './manualDeclarationsSample';
import { ds } from 'src/utils/colors';

// The server refuses a larger document; checking here saves the round trip.
const MAX_DOCUMENT_BYTES = 1024 * 1024;

const FORMAT_SKELETON = `{
  "nodes": [
    { "ref": "billing-db", "node_type": "Database", "name": "billing-db" },
    { "ref": "orders-queue", "cloud_account": "<account number or id>", "specific_type": "SQSQueue",
      "name": "orders-queue", "region": "us-east-1", "properties": { "fifo_queue": true } }
  ],
  "dependencies": [
    { "source": { "node_type": "Workload", "name": "checkout", "namespace": "shop", "cluster": "<cluster>" },
      "dest": "billing-db", "relationship_type": "CALLS" },
    { "source": "orders-queue", "dest": "billing-db" }
  ]
}`;

const Code = ({ children }) => <CodeBlock inline code={children} />;
Code.propTypes = { children: PropTypes.string.isRequired };

const sideLabel = (side) => {
  if (!side) {
    return '';
  }
  if (typeof side === 'string') {
    return side;
  }
  return `${side.node_type}: ${side.namespace ? `${side.namespace}/` : ''}${side.name}`;
};

const OUTCOME_TONE = { created: 'success', existing: 'info', rejected: 'critical' };
const OUTCOME_LABEL = { created: 'Created', existing: 'Existing', rejected: 'Rejected' };

const ResultRow = ({ outcome, text, status, detail }) => (
  <Box data-testid='manual-declarations-result-row' data-outcome={outcome} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
      <Label tone={OUTCOME_TONE[outcome]}>{OUTCOME_LABEL[outcome]}</Label>
      <Typography sx={{ fontSize: ds.text.small, wordBreak: 'break-word' }}>{text}</Typography>
      {status && <Label tone={status === 'resolved' ? 'success' : 'warning'}>{status.replace(/_/g, ' ')}</Label>}
    </Box>
    {detail && <Typography sx={{ fontSize: ds.text.caption, color: outcome === 'rejected' ? ds.red[600] : ds.gray[600] }}>{detail}</Typography>}
  </Box>
);

ResultRow.propTypes = {
  outcome: PropTypes.oneOf(['created', 'existing', 'rejected']).isRequired,
  text: PropTypes.node.isRequired,
  status: PropTypes.string,
  detail: PropTypes.string,
};

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// The outcome of an import, grouped by kind and then by outcome.
export const ResultView = ({ result, onDone }) => {
  const nodes = result?.nodes ?? {};
  const deps = result?.dependencies ?? {};
  const created = nodes.created ?? [];
  const existing = nodes.existing ?? [];
  const rejected = nodes.rejected ?? [];
  const depsCreated = deps.created ?? [];
  const depsExisting = deps.existing ?? [];
  const depsRejected = deps.rejected ?? [];
  const nodeText = (r) => `#${r.index} ${r.ref || r.declaration?.name || ''}${r.node_id ? ` → node ${r.node_id}` : ''}`;
  const depText = (r) => `#${r.index} ${sideLabel(r.declaration?.source)} → ${sideLabel(r.declaration?.dest)}`;
  const heading = (text) => <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.brand[500] }}>{text}</Typography>;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], p: ds.space[4] }}>
      <Typography data-testid='manual-declarations-result-summary' sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold }}>
        {plural(created.length, 'node', 'nodes')} created, {existing.length} existing, {rejected.length} rejected ·{' '}
        {plural(depsCreated.length, 'dependency', 'dependencies')} created, {depsExisting.length} existing, {depsRejected.length} rejected
      </Typography>

      {created.length + existing.length + rejected.length > 0 && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          {heading('Nodes')}
          <Box sx={{ maxHeight: '240px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            {created.map((r) => (
              <ResultRow key={`c${r.index}`} outcome='created' text={nodeText(r)} />
            ))}
            {existing.map((r) => (
              <ResultRow key={`e${r.index}`} outcome='existing' text={nodeText(r)} detail={r.error} />
            ))}
            {rejected.map((r) => (
              <ResultRow key={`r${r.index}`} outcome='rejected' text={nodeText(r)} detail={r.error} />
            ))}
          </Box>
        </Box>
      )}

      {depsCreated.length + depsExisting.length + depsRejected.length > 0 && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          {heading('Dependencies')}
          <Box sx={{ maxHeight: '240px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            {depsCreated.map((r) => (
              <ResultRow key={`c${r.index}`} outcome='created' text={depText(r)} status={r.status} />
            ))}
            {depsExisting.map((r) => (
              <ResultRow key={`e${r.index}`} outcome='existing' text={depText(r)} detail={r.error} />
            ))}
            {depsRejected.map((r) => (
              <ResultRow key={`r${r.index}`} outcome='rejected' text={depText(r)} detail={r.error} />
            ))}
          </Box>
        </Box>
      )}

      <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button tone='primary' size='md' onClick={onDone}>
          Done
        </Button>
      </Box>
    </Box>
  );
};

ResultView.propTypes = {
  result: PropTypes.object.isRequired,
  onDone: PropTypes.func.isRequired,
};

const FormatPanel = ({ schema }) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
    <CodeBlock code={FORMAT_SKELETON} language='json' title='Document format' />
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], lineHeight: 1.5 }}>
      A node needs <Code>name</Code>. In a connected account give <Code>cloud_account</Code> and <Code>specific_type</Code>, plus <Code>region</Code>{' '}
      for a cloud resource or <Code>namespace</Code> for a Kubernetes one; outside one give <Code>node_type</Code>. <Code>properties</Code> keep their
      JSON types. A dependency side is a node&apos;s <Code>ref</Code> as a string, or an object naming a node already in the graph;{' '}
      <Code>relationship_type</Code> is CALLS, PUBLISHES_TO or SUBSCRIBES_TO (default CALLS).
    </Typography>
    <ManualNodeTypeValues schema={schema} />
  </Box>
);

FormatPanel.propTypes = { schema: PropTypes.array };

const JsonImportDialog = ({ open, onClose, onImported, schema }) => {
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState(null);
  const [showFormat, setShowFormat] = useState(false);
  const fileInputRef = useRef(null);
  // A file read that finishes after Cancel must not refill the dialog.
  const uploadSeq = useRef(0);
  const sample = useMemo(() => JSON.stringify(buildSampleDeclarations(schema), null, 2), [schema]);

  const reset = () => {
    uploadSeq.current += 1;
    setText('');
    setFileName('');
    setSubmitting(false);
    setResult(null);
    setShowFormat(false);
  };

  // Closing after an import (the X, Escape or the backdrop instead of Done)
  // still has to refresh the lists, or they keep showing the old rows.
  // An import in flight cannot be abandoned: its result would land in a
  // reset dialog and the lists would never refresh.
  const handleClose = () => {
    if (submitting) {
      return;
    }
    const imported = Boolean(result);
    reset();
    if (imported) {
      onImported();
    } else {
      onClose();
    }
  };

  const handleFile = (file) => {
    if (!file) {
      return;
    }
    // `accept` is only a hint, and a .json file can arrive with an empty type.
    if (!/\.json$/i.test(file.name) && file.type !== 'application/json') {
      snackbar.error('Only a .json file can be uploaded.');
      return;
    }
    if (file.size > MAX_DOCUMENT_BYTES) {
      snackbar.error(`The document exceeds the ${MAX_DOCUMENT_BYTES / 1024} KB limit.`);
      return;
    }
    const seq = ++uploadSeq.current;
    const reader = new FileReader();
    reader.onload = () => {
      if (seq !== uploadSeq.current) {
        return;
      }
      setText(String(reader.result ?? ''));
      setFileName(file.name);
    };
    reader.onerror = () => {
      if (seq === uploadSeq.current) {
        snackbar.error(`Could not read "${file.name}".`);
      }
    };
    reader.readAsText(file);
  };

  const handleImport = async () => {
    const body = text.trim();
    if (!body) {
      snackbar.error('Document is empty.');
      return;
    }
    if (new Blob([body]).size > MAX_DOCUMENT_BYTES) {
      snackbar.error(`The document exceeds the ${MAX_DOCUMENT_BYTES / 1024} KB limit.`);
      return;
    }
    let document;
    try {
      document = JSON.parse(body);
    } catch (err) {
      snackbar.error(`Not valid JSON: ${err.message}`);
      return;
    }
    setSubmitting(true);
    try {
      const res = await apiKnowledgeGraph.createManualDeclarations({ document });
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Import failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const payload = res?.data?.data?.kg_create_manual_declarations?.data;
      if (!payload) {
        snackbar.error('Import failed.');
        return;
      }
      setResult(payload);
      const landed = (payload.nodes?.created?.length ?? 0) + (payload.dependencies?.created?.length ?? 0);
      const existed = (payload.nodes?.existing?.length ?? 0) + (payload.dependencies?.existing?.length ?? 0);
      const refused = (payload.nodes?.rejected?.length ?? 0) + (payload.dependencies?.rejected?.length ?? 0);
      const summary = `Declared ${landed}${existed ? `, ${existed} already existed` : ''}`;
      if (refused) {
        snackbar.warning(`${summary}; ${refused} rejected.`);
      } else {
        snackbar.success(`${summary}.`);
      }
    } catch (err) {
      console.error('Manual declarations import failed:', err);
      snackbar.error('Import failed.');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDone = () => {
    reset();
    onImported();
  };

  return (
    <Modal width='md' title='Import Manual Declarations (JSON)' open={open} handleClose={handleClose} onClose={handleClose}>
      {result ? (
        <ResultView result={result} onDone={handleDone} />
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], p: ds.space[4] }}>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.5 }}>
            One document declares nodes and dependencies together. Nodes are declared first; a dependency names a node of the document by its{' '}
            <Code>ref</Code> (defaults to <Code>name</Code>) or a node already in the graph by <Code>node_type</Code> + <Code>name</Code>. A node that
            is already declared or collected is reported as existing and still links.
          </Typography>

          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              <Button tone='secondary' size='xs' onClick={() => setShowFormat((v) => !v)} id='manual-declarations-format-toggle'>
                {showFormat ? 'Hide format' : 'View format'}
              </Button>
              <DownloadButton
                id='manual-declarations-sample-download'
                size='xs'
                onClick={() => ({ data: sample, fileName: 'sample_manual_declarations.json', fileType: 'application/json' })}
              />
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Sample for your accounts</Typography>
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
              {fileName && (
                <Typography
                  data-testid='manual-declarations-file-name'
                  noWrap
                  title={fileName}
                  sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}
                >
                  Loaded {fileName}
                </Typography>
              )}
              <Button
                tone='secondary'
                size='sm'
                onClick={() => fileInputRef.current?.click()}
                id='manual-declarations-upload-btn'
                data-testid='manual-declarations-upload-btn'
              >
                Upload file
              </Button>
              {/* Hidden: a native file input takes no DS chrome; `accept` still narrows the picker. */}
              <input
                ref={fileInputRef}
                type='file'
                accept='application/json,.json'
                hidden
                data-testid='manual-declarations-file-input'
                onChange={(e) => {
                  handleFile(e.target.files?.[0]);
                  // Cleared so picking the same file again fires change.
                  e.target.value = '';
                }}
              />
            </Box>
          </Box>

          {showFormat && <FormatPanel schema={schema} />}

          <Box data-testid='manual-declarations-json-editor'>
            <CodeEditor
              value={text}
              onChange={setText}
              language='json'
              height='300px'
              placeholder='Paste the declarations document, or upload a .json file…'
            />
          </Box>
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Max {MAX_DOCUMENT_BYTES / 1024} KB</Typography>

          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2], pt: ds.space[1] }}>
            <Button tone='secondary' size='md' onClick={handleClose} disabled={submitting}>
              Cancel
            </Button>
            <Button tone='primary' size='md' onClick={handleImport} disabled={!text.trim() || submitting} loading={submitting}>
              Import
            </Button>
          </Box>
        </Box>
      )}
    </Modal>
  );
};

JsonImportDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onImported: PropTypes.func.isRequired,
  // kg_get_manual_node_schema: drives the sample and the type values.
  schema: PropTypes.array,
};

export default JsonImportDialog;
