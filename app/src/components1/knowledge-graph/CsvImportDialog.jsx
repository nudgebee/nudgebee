// CSV import dialog — Phase 2 (NB-30989).
//
// Sub-modal opened from the Manual Dependencies tab toolbar. Two input modes:
//   1) Paste — textarea with monospace font.
//   2) File  — drag-drop or browse; read via FileReader.readAsText.
// Backend route: kg_import_manual_dependencies. Response splits into
// imported / rejected lists; both rendered here so the operator sees the
// outcome before closing.

import { useCallback, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { Label } from '@components1/ds/Label';
import { toast as snackbar } from '@components1/ds/Toast';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { ds } from 'src/utils/colors';

const MODE_PASTE = 'paste';
const MODE_FILE = 'file';

const MAX_CSV_BYTES = 1024 * 1024; // 1 MB — guards against pushing absurd payloads through Hasura.

const REQUIRED_COLUMNS = ['source_node_type', 'source_name', 'dest_node_type', 'dest_name'];

// Full CSV schema — single source of truth for the "View full schema" panel
// AND the lightweight client-side header validation. Mirrors the column set
// in csvRowToDependency on the backend (manual_dependency_repository.go).
// Order matches the recommended header row.
const CSV_SCHEMA = [
  { name: 'source_node_type', required: true, purpose: 'KG NodeType of the source endpoint.', example: 'K8sService' },
  { name: 'source_name', required: true, purpose: 'Name of the source resource.', example: 'payment-svc' },
  { name: 'source_namespace', required: false, purpose: 'K8s namespace (narrows ambiguous matches).', example: 'prod' },
  { name: 'source_cluster', required: false, purpose: 'K8s cluster (narrows ambiguous matches).', example: 'us-east-1' },
  {
    name: 'source_arn',
    required: false,
    purpose: 'Deterministic cloud resource identifier (AWS ARN, Azure resource ID, or GCP self-link). Wins over name when provided.',
    example: 'arn:aws:lambda:us-east-1:123:function:checkout',
  },
  {
    name: 'source_account_id',
    required: false,
    purpose: 'AWS account number (strict qualifier — currently AWS-only; Azure/GCP narrow via region + resource ID).',
    example: '123456789012',
  },
  { name: 'source_region', required: false, purpose: 'Cloud region (narrows ambiguous matches).', example: 'us-east-1 | eastus | us-central1' },
  { name: 'dest_node_type', required: true, purpose: 'KG NodeType of the destination endpoint.', example: 'Database' },
  { name: 'dest_name', required: true, purpose: 'Name of the destination resource.', example: 'orders-db' },
  { name: 'dest_namespace', required: false, purpose: 'K8s namespace (narrows ambiguous matches).', example: 'prod' },
  { name: 'dest_cluster', required: false, purpose: 'K8s cluster (narrows ambiguous matches).', example: 'us-east-1' },
  {
    name: 'dest_arn',
    required: false,
    purpose: 'Deterministic cloud resource identifier (AWS ARN, Azure resource ID, or GCP self-link). Wins over name when provided.',
    example: 'arn:aws:rds:us-east-1:123:db:orders',
  },
  {
    name: 'dest_account_id',
    required: false,
    purpose: 'AWS account number (strict qualifier — currently AWS-only; Azure/GCP narrow via region + resource ID).',
    example: '123456789012',
  },
  { name: 'dest_region', required: false, purpose: 'Cloud region (narrows ambiguous matches).', example: 'us-east-1 | eastus | us-central1' },
  { name: 'relationship_type', required: false, purpose: 'CALLS | PUBLISHES_TO | SUBSCRIBES_TO. Blank → CALLS.', example: 'CALLS' },
  { name: 'notes', required: false, purpose: 'Free-text reason / context for future readers.', example: 'declared for FK PoC' },
];

const CSV_HEADER_ROW = CSV_SCHEMA.map((c) => c.name).join(',');

// Lightweight client-side header check — surfaces obvious header mistakes
// before round-tripping to the server. Returns the list of missing required
// columns (empty when valid).
const validateCsvHeader = (csv) => {
  const firstLine = (csv || '').split(/\r?\n/)[0] || '';
  const headers = firstLine.split(',').map((h) => h.trim());
  return REQUIRED_COLUMNS.filter((req) => !headers.includes(req));
};

const CsvImportDialog = ({ open, onClose, onImported }) => {
  const [mode, setMode] = useState(MODE_PASTE);
  const [csvBody, setCsvBody] = useState('');
  const [fileName, setFileName] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState(null);
  const [showSchema, setShowSchema] = useState(false);
  const fileInputRef = useRef(null);

  const reset = useCallback(() => {
    setMode(MODE_PASTE);
    setCsvBody('');
    setFileName('');
    setSubmitting(false);
    setResult(null);
    setShowSchema(false);
  }, []);

  const handleClose = () => {
    reset();
    onClose();
  };

  const handleFile = (file) => {
    if (!file) {
      return;
    }
    if (file.size > MAX_CSV_BYTES) {
      snackbar.error(`CSV exceeds the ${MAX_CSV_BYTES / 1024} KB limit.`);
      return;
    }
    const reader = new FileReader();
    reader.onload = (e) => {
      const body = String(e.target?.result ?? '');
      setCsvBody(body);
      setFileName(file.name);
    };
    reader.onerror = () => snackbar.error('Failed to read file.');
    reader.readAsText(file);
  };

  const handleDrop = (e) => {
    e.preventDefault();
    const file = e.dataTransfer?.files?.[0];
    handleFile(file);
  };

  const handleImport = async () => {
    if (!csvBody.trim()) {
      snackbar.error('CSV body is empty.');
      return;
    }
    // Mirror the file-upload size cap on the paste path. Without this the
    // operator can paste a 50 MB CSV via the textarea and OOM the browser
    // (or push a payload Hasura rejects past its body cap). `Blob(...).size`
    // measures the encoded byte length, matching how a File would report
    // size — keeps the two intake paths on the same scale.
    const csvBytes = new Blob([csvBody]).size;
    if (csvBytes > MAX_CSV_BYTES) {
      snackbar.error(`CSV exceeds the ${MAX_CSV_BYTES / 1024} KB limit (${Math.round(csvBytes / 1024)} KB).`);
      return;
    }
    const missing = validateCsvHeader(csvBody);
    if (missing.length) {
      snackbar.error(`CSV header missing required columns: ${missing.join(', ')}`);
      return;
    }
    setSubmitting(true);
    try {
      const res = await apiKnowledgeGraph.importManualDependencies({ csv: csvBody });
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`CSV import failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const payload = res?.data?.data?.kg_import_manual_dependencies?.data ?? { imported: [], rejected: [] };
      setResult(payload);
      const imported = payload.imported?.length ?? 0;
      const rejected = payload.rejected?.length ?? 0;
      if (imported && !rejected) {
        snackbar.success(`Imported ${imported} row${imported === 1 ? '' : 's'}.`);
      } else if (rejected) {
        snackbar.success(`Imported ${imported} row${imported === 1 ? '' : 's'}; ${rejected} rejected.`);
      }
    } catch (err) {
      console.error('CSV import failed:', err);
      snackbar.error('CSV import failed.');
    } finally {
      setSubmitting(false);
    }
  };

  const handleDone = () => {
    onImported();
    reset();
  };

  return (
    <Modal width='sm' title='Import Manual Dependencies (CSV)' open={open} handleClose={handleClose} onClose={handleClose} maxHeight='80vh'>
      {result ? (
        <ResultView result={result} onDone={handleDone} />
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, p: 2 }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 1 }}>
            <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280', lineHeight: 1.5, flex: '1 1 0' }}>
              Required columns: <strong>source_node_type</strong>, <strong>source_name</strong>, <strong>dest_node_type</strong>,{' '}
              <strong>dest_name</strong>. Optional qualifiers narrow ambiguous matches. Relationship defaults to <code>CALLS</code> when blank.
            </Typography>
            <Button tone='secondary' size='xs' onClick={() => setShowSchema((v) => !v)}>
              {showSchema ? 'Hide schema' : 'View full schema'}
            </Button>
          </Box>

          {showSchema && <CsvSchemaPanel />}

          {/* Mode toggle: Paste vs Upload file. Kept compact on its own row
              so the textarea / drop zone below has the visual weight. */}
          <Box sx={{ display: 'inline-flex', gap: 0.5 }}>
            <Button tone={mode === MODE_PASTE ? 'primary' : 'secondary'} size='xs' onClick={() => setMode(MODE_PASTE)}>
              Paste
            </Button>
            <Button tone={mode === MODE_FILE ? 'primary' : 'secondary'} size='xs' onClick={() => setMode(MODE_FILE)}>
              Upload file
            </Button>
          </Box>

          {mode === MODE_PASTE ? (
            <Box
              component='textarea'
              value={csvBody}
              onChange={(e) => setCsvBody(e.target.value)}
              placeholder='Paste CSV body here, including the header row…'
              spellCheck={false}
              sx={{
                width: '100%',
                minHeight: '180px',
                fontFamily: 'monospace',
                fontSize: '12px',
                padding: '10px 12px',
                borderRadius: '6px',
                border: `1px solid ${ds?.border?.secondary ?? '#e5e7eb'}`,
                resize: 'vertical',
                outline: 'none',
                '&:focus': { borderColor: ds?.border?.primary ?? '#3b82f6' },
              }}
            />
          ) : (
            <Box
              onDragOver={(e) => e.preventDefault()}
              onDrop={handleDrop}
              onClick={() => fileInputRef.current?.click()}
              sx={{
                border: `2px dashed ${ds?.border?.secondary ?? '#e5e7eb'}`,
                borderRadius: '8px',
                padding: '24px',
                textAlign: 'center',
                cursor: 'pointer',
                minHeight: '160px',
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                justifyContent: 'center',
                '&:hover': { borderColor: ds?.border?.primary ?? '#9ca3af' },
              }}
            >
              <input
                ref={fileInputRef}
                type='file'
                accept='.csv,text/csv'
                style={{ display: 'none' }}
                onChange={(e) => handleFile(e.target.files?.[0])}
              />
              <Typography sx={{ fontSize: '13px', color: ds?.text?.secondary ?? '#374151', mb: 0.5 }}>
                {fileName ? `Selected: ${fileName}` : 'Drop a CSV here or click to browse'}
              </Typography>
              <Typography sx={{ fontSize: '11px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>Max {MAX_CSV_BYTES / 1024} KB</Typography>
            </Box>
          )}

          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, pt: 0.5 }}>
            <Button tone='secondary' size='md' onClick={handleClose} disabled={submitting}>
              Cancel
            </Button>
            <Button tone='primary' size='md' onClick={handleImport} disabled={!csvBody || submitting} loading={submitting}>
              Import
            </Button>
          </Box>
        </Box>
      )}
    </Modal>
  );
};

CsvImportDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  onImported: PropTypes.func.isRequired,
};

// Per-row outcome viewer rendered after a successful import. Splits the
// response into Imported (with status chip) and Rejected (with error reason).
const ResultView = ({ result, onDone }) => {
  const imported = result?.imported ?? [];
  const rejected = result?.rejected ?? [];
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, p: 2 }}>
      <Typography sx={{ fontSize: '14px', fontWeight: 600 }}>
        {imported.length} imported, {rejected.length} rejected
      </Typography>

      {imported.length > 0 && (
        <Box>
          <Typography sx={{ fontSize: '12px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151', mb: 1 }}>Imported</Typography>
          <Box sx={{ maxHeight: '200px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 0.5 }}>
            {imported.map((r) => (
              <Box key={r.row_index} sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '6px 8px' }}>
                <Typography sx={{ fontSize: '12px' }}>
                  Row #{r.row_index} → id {r.id}
                </Typography>
                <Label tone={r.status === 'resolved' ? 'success' : 'warning'}>{r.status?.replace(/_/g, ' ') ?? 'imported'}</Label>
              </Box>
            ))}
          </Box>
        </Box>
      )}

      {rejected.length > 0 && (
        <Box>
          <Typography sx={{ fontSize: '12px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151', mb: 1 }}>Rejected</Typography>
          <Box sx={{ maxHeight: '200px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 0.5 }}>
            {rejected.map((r) => (
              <Box
                key={r.row_index}
                sx={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '6px 8px',
                  border: `1px solid ${ds?.border?.danger ?? '#fecaca'}`,
                  borderRadius: '4px',
                }}
              >
                <Typography sx={{ fontSize: '12px' }}>Row #{r.row_index}</Typography>
                <Typography sx={{ fontSize: '11px', color: ds?.text?.danger ?? '#b91c1c' }}>{r.error}</Typography>
              </Box>
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

// CsvSchemaPanel renders the full column reference (read-only). Operators
// flip it open when they don't remember which qualifier narrows which side.
// Sourced from CSV_SCHEMA so it can't drift from the validator.
const CsvSchemaPanel = () => (
  <Box
    sx={{
      border: `1px solid ${ds?.border?.secondary ?? '#e5e7eb'}`,
      borderRadius: '6px',
      backgroundColor: ds?.background?.secondary ?? '#f9fafb',
      padding: '10px 12px',
      display: 'flex',
      flexDirection: 'column',
      gap: 1,
    }}
  >
    <Typography sx={{ fontSize: '11px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>
      Recommended header row (paste verbatim into row 1 of your CSV):
    </Typography>
    <Box
      component='pre'
      sx={{
        fontFamily: 'monospace',
        fontSize: '11px',
        margin: 0,
        padding: '6px 8px',
        backgroundColor: ds?.background?.primary ?? '#ffffff',
        border: `1px solid ${ds?.border?.secondary ?? '#e5e7eb'}`,
        borderRadius: '4px',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-all',
      }}
    >
      {CSV_HEADER_ROW}
    </Box>

    <Box sx={{ maxHeight: '240px', overflowY: 'auto', mt: 0.5 }}>
      <Box
        component='table'
        sx={{
          width: '100%',
          borderCollapse: 'collapse',
          fontSize: '11px',
          '& th, & td': {
            textAlign: 'left',
            verticalAlign: 'top',
            padding: '6px 8px',
            borderBottom: `1px solid ${ds?.border?.secondary ?? '#e5e7eb'}`,
          },
          '& th': {
            fontWeight: 600,
            color: ds?.text?.secondary ?? '#374151',
            backgroundColor: ds?.background?.primary ?? '#ffffff',
            position: 'sticky',
            top: 0,
          },
        }}
      >
        <thead>
          <tr>
            <th>Column</th>
            <th>Required</th>
            <th>Purpose</th>
            <th>Example</th>
          </tr>
        </thead>
        <tbody>
          {CSV_SCHEMA.map((col) => (
            <tr key={col.name}>
              <td>
                <code style={{ fontSize: '11px' }}>{col.name}</code>
              </td>
              <td>
                <Label tone={col.required ? 'critical' : 'neutral'}>{col.required ? 'required' : 'optional'}</Label>
              </td>
              <td>{col.purpose}</td>
              <td>
                <code style={{ fontSize: '10px', color: ds?.text?.secondaryDark ?? '#6b7280' }}>{col.example}</code>
              </td>
            </tr>
          ))}
        </tbody>
      </Box>
    </Box>
  </Box>
);

export default CsvImportDialog;
