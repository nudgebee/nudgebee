import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Label } from '@ui/Label';
import { Card } from '@ui/Card';
import Tabs from '@shared/navigation/Tabs';
import CustomTable from '@shared/tables/CustomTable';
import { ds } from '@utils/colors';
import { formatShape, summariseClassifier } from '@components/llm/kbDocumentClassification';
import { capabilityRows, countClassified, markVsClassifier } from '@components/llm/kbDocumentDetails';

// One labelled value in the detail view. The label is a section-style caption (display font),
// the value inherits the body font, per the typography convention.
const DetailRow = ({ label, testId, children }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: ds.space[3], py: ds.space[1] }}>
    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', fontFamily: 'var(--ds-font-display)' }}>{label}</Typography>
    <Box data-testid={testId} sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', textAlign: 'right', minWidth: 0 }}>
      {children}
    </Box>
  </Box>
);

DetailRow.propTypes = { label: PropTypes.string.isRequired, testId: PropTypes.string, children: PropTypes.node };

const Hint = ({ children }) => (
  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', mt: ds.space[2] }}>{children}</Typography>
);

Hint.propTypes = { children: PropTypes.node };

// The same kind colouring as the row chips: SOP is the one that stands out, knowledge stays neutral.
const kindTone = (kind) => (kind === 'sop' ? 'success' : 'neutral');

// What the classifier decided about one page, your mark, the knowledge base it lives in, and what the
// capability catalog says about the capabilities the page references. Layout-agnostic: the caller decides
// where it sits.
const DocumentDetailBody = ({ doc, mark, tab, onTab, knowledgeBase, documents }) => {
  const summary = summariseClassifier(doc);
  const rows = capabilityRows(doc.capabilities, knowledgeBase?.capability_details);
  const conflict = markVsClassifier(mark, doc.kind);
  const hasRollup = Boolean(knowledgeBase && ((knowledgeBase.kinds || []).length > 0 || (knowledgeBase.shapes || []).length > 0));
  const counts = countClassified(documents.filter((d) => d.document_key || d.kind));
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }} data-testid='kb-document-detail-body'>
      <Tabs
        value={tab}
        onChange={onTab}
        variant='secondary'
        behavior='filter'
        smallSize
        options={{
          tabOptions: [
            { value: 'page', text: 'This page' },
            { value: 'kb', text: 'Knowledge base' },
            { value: 'capabilities', text: 'Capabilities', count: rows.length },
          ],
        }}
      />
      {tab === 'page' && (
        <Box>
          <Card variant='tinted' size='sm'>
            <DetailRow label='Classifier' testId='kb-detail-kind'>
              {summary.classified ? (
                <Label tone={kindTone(summary.kind)}>{summary.kindLabel}</Label>
              ) : (
                <Typography component='span' sx={{ color: 'var(--ds-gray-500)' }}>
                  Not classified
                </Typography>
              )}
            </DetailRow>
            <DetailRow label='Shape' testId='kb-detail-shape'>
              {summary.classified && summary.shapeLabel ? summary.shapeLabel : '—'}
            </DetailRow>
            <DetailRow label='Your mark' testId='kb-detail-mark'>
              {mark ? <Label tone='neutral'>{mark === 'sop' ? 'SOP' : 'Fact'}</Label> : 'None'}
            </DetailRow>
          </Card>
          {!summary.classified && <Hint>No label is stored for this page yet.</Hint>}
          {conflict && <Hint>{conflict}</Hint>}
        </Box>
      )}
      {tab === 'kb' &&
        (hasRollup ? (
          <Card variant='tinted' size='sm'>
            <DetailRow label='Kinds' testId='kb-detail-kb-kinds'>
              <Box sx={{ display: 'inline-flex', gap: ds.space[1], flexWrap: 'wrap', justifyContent: 'flex-end' }}>
                {(knowledgeBase.kinds || []).map((kind) => (
                  <Label key={kind} tone={kindTone(kind)}>
                    {kind === 'sop' ? 'SOP' : 'Knowledge'}
                  </Label>
                ))}
              </Box>
            </DetailRow>
            <DetailRow label='Shapes' testId='kb-detail-kb-shapes'>
              {(knowledgeBase.shapes || []).map(formatShape).join(', ') || '—'}
            </DetailRow>
            <DetailRow label='Capabilities' testId='kb-detail-kb-capabilities'>
              {(knowledgeBase.capabilities || []).length}
            </DetailRow>
            <DetailRow label='Loaded pages' testId='kb-detail-kb-loaded'>
              {`${counts.sop} SOP · ${counts.knowledge} knowledge · ${counts.notClassified} not classified`}
            </DetailRow>
          </Card>
        ) : (
          <Hint>No roll-up is stored for this knowledge base yet.</Hint>
        ))}
      {tab === 'capabilities' &&
        (rows.length === 0 ? (
          <Hint>No capabilities recorded for this page.</Hint>
        ) : (
          <CustomTable
            headers={[{ name: 'Capability' }, { name: 'Surface' }, { name: 'Scope' }, { name: 'Description' }]}
            tableData={rows.map((row) => [
              { text: row.name },
              { text: row.surface },
              { component: <Label tone={row.tone}>{row.scope}</Label> },
              { text: row.inCatalog ? row.description || '—' : 'Not in catalog' },
            ])}
            rowsPerPage={rows.length}
            totalRows={rows.length}
          />
        ))}
    </Box>
  );
};

DocumentDetailBody.propTypes = {
  doc: PropTypes.shape({
    title: PropTypes.string,
    kind: PropTypes.string,
    shape: PropTypes.string,
    capabilities: PropTypes.arrayOf(PropTypes.string),
  }).isRequired,
  mark: PropTypes.string,
  tab: PropTypes.string.isRequired,
  onTab: PropTypes.func.isRequired,
  knowledgeBase: PropTypes.shape({
    kinds: PropTypes.arrayOf(PropTypes.string),
    shapes: PropTypes.arrayOf(PropTypes.string),
    capabilities: PropTypes.arrayOf(PropTypes.string),
    capability_details: PropTypes.arrayOf(PropTypes.object),
  }),
  documents: PropTypes.arrayOf(PropTypes.object).isRequired,
};

export default DocumentDetailBody;
