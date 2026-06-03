import React, { useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Label } from '@components1/ds/Label';
import { Chip } from '@components1/ds/Chip';
import {
  CheckCircle,
  Error as ErrorIcon,
  ChevronRight,
  ExpandMore,
  Description,
  Code,
  Search,
  Build,
  HistoryEdu,
  OpenInNew,
} from '@mui/icons-material';
import MarkDowns from '@components1/common/MarkDowns';
import SimpleDiffViewer from '@components1/common/SimpleDiffViewer';
import DiffViewer from '@components1/common/DiffViewer';
import { ANNOTATIONS } from '@lib/annotationKeys';
import { ds } from '@utils/colors';

const sectionGap = ds.space.mul(0, 7);

const isNonEmptyString = (v) => typeof v === 'string' && v.trim().length > 0;
const isNonEmptyArray = (v) => Array.isArray(v) && v.length > 0;

const formatLineNumber = (n) => {
  if (typeof n === 'number' && n > 0) {
    return String(n);
  }
  if (typeof n === 'string' && n.trim().length > 0) {
    return n;
  }
  return null;
};

const shortSha = (sha) => {
  if (!isNonEmptyString(sha)) {
    return null;
  }
  return sha.slice(0, 7);
};

// Reject anything other than http(s):// to defend against javascript:, data:, etc. URIs
// emitted by the LLM that would execute on click as XSS.
const safeHttpUrl = (url) => {
  if (!isNonEmptyString(url)) {
    return null;
  }
  const trimmed = url.trim();
  if (/^https?:\/\//i.test(trimmed)) {
    return trimmed;
  }
  return null;
};

const confidenceTone = (score) => {
  const s = (score || '').toLowerCase();
  if (s === 'high') return 'success';
  if (s === 'medium' || s === 'med') return 'warning';
  if (s === 'low') return 'critical';
  return 'neutral';
};

const SectionTitle = ({ icon, label, count }) => (
  <Box display='flex' alignItems='center' gap={ds.space.mul(0, 3)} mb={ds.space.mul(0, 3)}>
    {icon}
    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
      {label}
    </Typography>
    {typeof count === 'number' && (
      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontFamily: 'monospace' }}>({count})</Typography>
    )}
  </Box>
);

SectionTitle.propTypes = {
  icon: PropTypes.node,
  label: PropTypes.string.isRequired,
  count: PropTypes.number,
};

const CollapsibleSection = ({ icon, label, count, defaultExpanded, children }) => {
  const [expanded, setExpanded] = useState(Boolean(defaultExpanded));
  return (
    <Box sx={{ border: '1px solid var(--ds-gray-200)', borderRadius: ds.radius.md, mb: sectionGap, overflow: 'hidden' }}>
      <Box
        onClick={() => setExpanded((e) => !e)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space.mul(0, 3),
          padding: `${ds.space[2]} ${ds.space[3]}`,
          backgroundColor: 'var(--ds-background-200)',
          cursor: 'pointer',
          borderBottom: expanded ? '1px solid #e5e7eb' : 'none',
        }}
      >
        {expanded ? (
          <ExpandMore sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />
        ) : (
          <ChevronRight sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />
        )}
        {icon}
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
          {label}
        </Typography>
        {typeof count === 'number' && (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontFamily: 'monospace' }}>({count})</Typography>
        )}
      </Box>
      {expanded && <Box sx={{ padding: `${ds.space.mul(0, 5)} ${ds.space[3]}` }}>{children}</Box>}
    </Box>
  );
};

CollapsibleSection.propTypes = {
  icon: PropTypes.node,
  label: PropTypes.string.isRequired,
  count: PropTypes.number,
  defaultExpanded: PropTypes.bool,
  children: PropTypes.node,
};

const HeaderStrip = ({ title, confidenceScore, requiresFix }) => {
  if (!isNonEmptyString(title) && !isNonEmptyString(confidenceScore) && requiresFix === undefined) {
    return null;
  }
  return (
    <Box sx={{ mb: sectionGap }}>
      {isNonEmptyString(title) && (
        <Typography
          sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-700)', lineHeight: 1.4 }}
        >
          {title}
        </Typography>
      )}
      <Box display='flex' gap={ds.space.mul(0, 3)} mt={ds.space.mul(0, 3)} flexWrap='wrap'>
        {isNonEmptyString(confidenceScore) && <Label text={`Confidence: ${confidenceScore}`} tone={confidenceTone(confidenceScore)} />}
        {requiresFix === true && <Label text='Requires fix' tone='critical' />}
        {requiresFix === false && <Label text='No fix needed' tone='success' />}
      </Box>
    </Box>
  );
};

HeaderStrip.propTypes = {
  title: PropTypes.string,
  confidenceScore: PropTypes.string,
  requiresFix: PropTypes.bool,
};

const VerdictCallout = ({ errorMessage, description, onLinkClick }) => {
  if (!isNonEmptyString(errorMessage) && !isNonEmptyString(description)) {
    return null;
  }
  return (
    <Box
      sx={{
        mb: sectionGap,
        padding: `${ds.space.mul(0, 5)} ${ds.space[3]}`,
        backgroundColor: 'var(--ds-yellow-100)',
        borderLeft: '3px solid var(--ds-amber-400)',
        borderRadius: ds.radius.sm,
      }}
    >
      {isNonEmptyString(errorMessage) && (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            fontFamily: 'monospace',
            color: 'var(--ds-amber-700)',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            mb: isNonEmptyString(description) ? ds.space[2] : 0,
          }}
        >
          {errorMessage}
        </Typography>
      )}
      {isNonEmptyString(description) && <MarkDowns data={description.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />}
    </Box>
  );
};

VerdictCallout.propTypes = {
  errorMessage: PropTypes.string,
  description: PropTypes.string,
  onLinkClick: PropTypes.func,
};

const RootCauseSection = ({ rootCause, onLinkClick }) => {
  if (!isNonEmptyString(rootCause)) {
    return null;
  }
  return (
    <Box sx={{ mb: sectionGap }}>
      <SectionTitle icon={<Search sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />} label='Root cause' />
      <MarkDowns data={rootCause.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />
    </Box>
  );
};

RootCauseSection.propTypes = {
  rootCause: PropTypes.string,
  onLinkClick: PropTypes.func,
};

const startLineFromValue = (n) => {
  if (typeof n === 'number' && n > 0) {
    return n;
  }
  if (typeof n === 'string') {
    const parsed = parseInt(n, 10);
    if (!Number.isNaN(parsed) && parsed > 0) {
      return parsed;
    }
  }
  return null;
};

const NumberedCodeBlock = ({ code, startLine, attachedToHeader, maxHeight = ds.space.mul(0, 160) }) => {
  if (!isNonEmptyString(code)) {
    return null;
  }
  const lines = code.replace(/\n$/, '').split('\n');
  const start = startLineFromValue(startLine);
  const gutterWidth = `${Math.max(2, String((start || 1) + lines.length - 1).length)}ch`;
  return (
    <Box
      sx={{
        margin: 0,
        backgroundColor: 'var(--ds-gray-700)',
        color: 'var(--ds-gray-300)',
        fontFamily: 'monospace',
        fontSize: 'var(--ds-text-small)',
        lineHeight: 1.5,
        overflow: 'auto',
        borderRadius: attachedToHeader ? `0 0 ${ds.radius.sm} ${ds.radius.sm}` : ds.radius.sm,
        border: '1px solid var(--ds-gray-200)',
        borderTop: attachedToHeader ? 'none' : '1px solid #e5e7eb',
        maxHeight,
      }}
    >
      <Box component='pre' sx={{ margin: 0, padding: `${ds.space.mul(0, 5)} 0`, whiteSpace: 'pre' }}>
        {lines.map((line, idx) => (
          <Box key={`l-${start ? start + idx : idx}`} sx={{ display: 'flex', alignItems: 'flex-start' }}>
            <Box
              component='span'
              sx={{
                display: 'inline-block',
                minWidth: gutterWidth,
                paddingLeft: ds.space[3],
                paddingRight: ds.space[3],
                color: 'var(--ds-gray-600)',
                textAlign: 'right',
                userSelect: 'none',
                flexShrink: 0,
              }}
            >
              {start ? start + idx : idx + 1}
            </Box>
            <Box component='code' sx={{ flex: 1, paddingRight: ds.space[3], whiteSpace: 'pre' }}>
              {line || ' '}
            </Box>
          </Box>
        ))}
      </Box>
    </Box>
  );
};

NumberedCodeBlock.propTypes = {
  code: PropTypes.string,
  startLine: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  attachedToHeader: PropTypes.bool,
  maxHeight: PropTypes.string,
};

const AffectedCodeBlock = ({ filePath, lineNumber, codeContext, originalCode }) => {
  if (!isNonEmptyString(filePath) && !isNonEmptyString(codeContext) && !isNonEmptyString(originalCode)) {
    return null;
  }
  const snippet = isNonEmptyString(codeContext) ? codeContext : originalCode;
  const lineStr = formatLineNumber(lineNumber);
  return (
    <Box sx={{ mb: sectionGap }}>
      <SectionTitle icon={<Description sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />} label='Affected code' />
      {isNonEmptyString(filePath) && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space.mul(0, 3),
            padding: `${ds.space.mul(0, 3)} ${ds.space.mul(0, 5)}`,
            backgroundColor: 'var(--ds-gray-100)',
            border: '1px solid var(--ds-gray-200)',
            borderRadius: `${ds.radius.sm} ${ds.radius.sm} 0 0`,
            fontFamily: 'monospace',
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-brand-500)',
          }}
        >
          <Code sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />
          <span style={{ wordBreak: 'break-all' }}>
            {filePath}
            {lineStr ? `:${lineStr}` : ''}
          </span>
        </Box>
      )}
      <NumberedCodeBlock code={snippet} startLine={lineNumber} attachedToHeader={isNonEmptyString(filePath)} />
    </Box>
  );
};

AffectedCodeBlock.propTypes = {
  filePath: PropTypes.string,
  lineNumber: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
  codeContext: PropTypes.string,
  originalCode: PropTypes.string,
};

const ProposedFixBlock = ({ gitDiff, originalCode, fixedCode }) => {
  if (!isNonEmptyString(gitDiff) && !isNonEmptyString(fixedCode)) {
    return null;
  }
  let body = null;
  if (isNonEmptyString(gitDiff)) {
    body = <SimpleDiffViewer gitDiff={gitDiff} defaultExpanded={true} />;
  } else if (isNonEmptyString(originalCode) && isNonEmptyString(fixedCode)) {
    body = <DiffViewer originalCode={originalCode} newCode={fixedCode} />;
  } else {
    body = (
      <Box
        component='pre'
        sx={{
          margin: 0,
          padding: `${ds.space.mul(0, 5)} ${ds.space[3]}`,
          backgroundColor: 'var(--ds-gray-700)',
          color: 'var(--ds-gray-300)',
          fontFamily: 'monospace',
          fontSize: 'var(--ds-text-small)',
          lineHeight: 1.5,
          overflow: 'auto',
          borderRadius: ds.radius.sm,
          whiteSpace: 'pre',
          maxHeight: ds.space.mul(0, 160),
        }}
      >
        <code>{fixedCode}</code>
      </Box>
    );
  }
  return (
    <Box sx={{ mb: sectionGap }}>
      <SectionTitle icon={<Build sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />} label='Proposed fix' />
      {body}
    </Box>
  );
};

ProposedFixBlock.propTypes = {
  gitDiff: PropTypes.string,
  originalCode: PropTypes.string,
  fixedCode: PropTypes.string,
};

const formatCitationLineRange = (start, end) => {
  const s = formatLineNumber(start);
  const e = formatLineNumber(end);
  if (s && e && s !== e) {
    return `${s}-${e}`;
  }
  if (s) {
    return s;
  }
  if (e) {
    return e;
  }
  return null;
};

const CitationItem = ({ citation, defaultExpanded }) => {
  const [expanded, setExpanded] = useState(Boolean(defaultExpanded));
  const lineRange = formatCitationLineRange(citation.line_start, citation.line_end);
  const hasSnippet = isNonEmptyString(citation.snippet);
  return (
    <Box sx={{ mb: ds.space.mul(0, 5), border: '1px solid var(--ds-gray-200)', borderRadius: ds.radius.sm, overflow: 'hidden' }}>
      <Box
        onClick={() => hasSnippet && setExpanded((e) => !e)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space.mul(0, 3),
          padding: `${ds.space.mul(0, 3)} ${ds.space.mul(0, 5)}`,
          backgroundColor: 'var(--ds-gray-100)',
          cursor: hasSnippet ? 'pointer' : 'default',
          borderBottom: expanded && hasSnippet ? '1px solid #e5e7eb' : 'none',
        }}
      >
        {hasSnippet &&
          (expanded ? (
            <ExpandMore sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />
          ) : (
            <ChevronRight sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />
          ))}
        <Code sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />
        <Typography sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-small)', color: 'var(--ds-brand-500)', wordBreak: 'break-all' }}>
          {isNonEmptyString(citation.file_path) ? citation.file_path : '(unknown file)'}
          {lineRange ? `:${lineRange}` : ''}
        </Typography>
      </Box>
      {isNonEmptyString(citation.note) && (
        <Typography
          sx={{
            padding: `${ds.space.mul(0, 3)} ${ds.space.mul(0, 5)}`,
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-brand-500)',
            backgroundColor: 'var(--ds-background-200)',
          }}
        >
          {citation.note}
        </Typography>
      )}
      {expanded && hasSnippet && <NumberedCodeBlock code={citation.snippet} startLine={citation.line_start} attachedToHeader={true} />}
    </Box>
  );
};

CitationItem.propTypes = {
  citation: PropTypes.shape({
    file_path: PropTypes.string,
    line_start: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    line_end: PropTypes.oneOfType([PropTypes.number, PropTypes.string]),
    note: PropTypes.string,
    snippet: PropTypes.string,
  }).isRequired,
  defaultExpanded: PropTypes.bool,
};

const CitationsSection = ({ citations }) => {
  if (!isNonEmptyArray(citations)) {
    return null;
  }
  const expandFirst = citations.length === 1;
  return (
    <Box sx={{ mb: sectionGap }}>
      <SectionTitle icon={<Description sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />} label='Code references' count={citations.length} />
      {citations.map((c, idx) => (
        <CitationItem key={`${c.file_path || ''}-${idx}`} citation={c} defaultExpanded={expandFirst} />
      ))}
    </Box>
  );
};

CitationsSection.propTypes = {
  citations: PropTypes.array,
};

const stringifyStep = (step) => {
  if (typeof step === 'string') {
    return step;
  }
  if (step && typeof step === 'object') {
    if (isNonEmptyString(step.description)) {
      return step.description;
    }
    if (isNonEmptyString(step.instruction)) {
      return step.instruction;
    }
    if (isNonEmptyString(step.step)) {
      return step.step;
    }
    return JSON.stringify(step);
  }
  return String(step);
};

const ImplementationStepsSection = ({ steps, onLinkClick }) => {
  if (!isNonEmptyArray(steps)) {
    return null;
  }
  return (
    <Box sx={{ mb: sectionGap }}>
      <SectionTitle icon={<Build sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />} label='Implementation steps' count={steps.length} />
      <Box component='ol' sx={{ margin: 0, paddingLeft: ds.space.mul(1, 5), '& > li': { marginBottom: ds.space[1] } }}>
        {steps.map((step, idx) => {
          const text = stringifyStep(step);
          return (
            <li key={`step-${idx}-${text.slice(0, 40)}`}>
              <MarkDowns data={text.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0, '& p': { margin: 0 } }} onLinkClick={onLinkClick} />
            </li>
          );
        })}
      </Box>
    </Box>
  );
};

ImplementationStepsSection.propTypes = {
  steps: PropTypes.array,
  onLinkClick: PropTypes.func,
};

const renderAlternativeFix = (alt, idx, onLinkClick) => {
  if (typeof alt === 'string') {
    return (
      <Box key={`alt-${idx}-${alt.slice(0, 30)}`} sx={{ mb: ds.space[2] }}>
        <MarkDowns data={alt.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />
      </Box>
    );
  }
  if (alt && typeof alt === 'object') {
    const altKey = `alt-${idx}-${alt.title || alt.description?.slice(0, 30) || ''}`;
    return (
      <Box
        key={altKey}
        sx={{
          mb: ds.space.mul(0, 5),
          padding: `${ds.space[2]} ${ds.space.mul(0, 5)}`,
          border: '1px solid var(--ds-gray-200)',
          borderRadius: ds.radius.sm,
          backgroundColor: 'var(--ds-background-200)',
        }}
      >
        {isNonEmptyString(alt.title) && (
          <Typography
            sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)', mb: ds.space[1] }}
          >
            {alt.title}
          </Typography>
        )}
        {isNonEmptyString(alt.description) && (
          <MarkDowns data={alt.description.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />
        )}
        {isNonEmptyString(alt.git_diff) && <SimpleDiffViewer gitDiff={alt.git_diff} defaultExpanded={false} />}
      </Box>
    );
  }
  return null;
};

const AlternativeFixesSection = ({ alternatives, onLinkClick }) => {
  if (!isNonEmptyArray(alternatives)) {
    return null;
  }
  return (
    <CollapsibleSection
      icon={<Build sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />}
      label='Alternative fixes'
      count={alternatives.length}
      defaultExpanded={false}
    >
      {alternatives.map((alt, idx) => renderAlternativeFix(alt, idx, onLinkClick))}
    </CollapsibleSection>
  );
};

AlternativeFixesSection.propTypes = {
  alternatives: PropTypes.array,
  onLinkClick: PropTypes.func,
};

const InvestigationTrailSection = ({ trail, onLinkClick }) => {
  if (!isNonEmptyArray(trail)) {
    return null;
  }
  return (
    <CollapsibleSection
      icon={<Search sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />}
      label='Investigation trail'
      count={trail.length}
      defaultExpanded={false}
    >
      <Box
        component='ol'
        sx={{ margin: 0, paddingLeft: ds.space.mul(1, 5), '& > li': { marginBottom: ds.space.mul(0, 3), fontSize: 'var(--ds-text-small)' } }}
      >
        {trail.map((step, idx) => {
          const text = stringifyStep(step);
          return (
            <li key={`trail-${idx}-${text.slice(0, 40)}`}>
              <MarkDowns data={text.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0, '& p': { margin: 0 } }} onLinkClick={onLinkClick} />
            </li>
          );
        })}
      </Box>
    </CollapsibleSection>
  );
};

InvestigationTrailSection.propTypes = {
  trail: PropTypes.array,
  onLinkClick: PropTypes.func,
};

const executionStripStyle = (status, verificationPassed) => {
  const ok = (status || '').toLowerCase() === 'success';
  const failed = (status || '').toLowerCase() === 'failed' || (status || '').toLowerCase() === 'failure';
  if (failed) {
    return {
      bg: 'var(--ds-red-100)',
      border: 'var(--ds-red-200)',
      fg: 'var(--ds-red-700)',
      icon: <ErrorIcon sx={{ fontSize: 18, color: 'var(--ds-red-600)' }} />,
    };
  }
  if (ok) {
    return {
      bg: 'var(--ds-green-100)',
      border: 'var(--ds-green-200)',
      fg: 'var(--ds-green-700)',
      icon: <CheckCircle sx={{ fontSize: 18, color: verificationPassed === false ? '#ca8a04' : '#16a34a' }} />,
    };
  }
  return {
    bg: 'var(--ds-background-200)',
    border: 'var(--ds-gray-200)',
    fg: 'var(--ds-gray-700)',
    icon: <CheckCircle sx={{ fontSize: 18, color: 'var(--ds-gray-600)' }} />,
  };
};

const ExecutionResultStrip = ({ status, summary, filesModified, verificationPassed, verificationDetails, onLinkClick }) => {
  if (!isNonEmptyString(status) && !isNonEmptyString(summary) && !isNonEmptyArray(filesModified) && verificationPassed === undefined) {
    return null;
  }
  const style = executionStripStyle(status, verificationPassed);
  return (
    <Box
      sx={{
        mb: sectionGap,
        padding: `${ds.space.mul(0, 5)} ${ds.space[3]}`,
        backgroundColor: style.bg,
        border: `1px solid ${style.border}`,
        borderRadius: ds.radius.md,
      }}
    >
      <Box display='flex' alignItems='center' gap={ds.space[2]} mb={isNonEmptyString(summary) || isNonEmptyArray(filesModified) ? ds.space[2] : 0}>
        {style.icon}
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: style.fg }}>
          {isNonEmptyString(status) ? `Execution: ${status}` : 'Execution result'}
        </Typography>
        {verificationPassed === true && <Label text='Verification passed' tone='success' />}
        {verificationPassed === false && <Label text='Verification failed' tone='critical' />}
      </Box>
      {isNonEmptyString(summary) && <MarkDowns data={summary.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />}
      {isNonEmptyArray(filesModified) && (
        <Box mt={ds.space.mul(0, 3)}>
          <Typography
            sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[1] }}
          >
            Files modified
          </Typography>
          <Box component='ul' sx={{ margin: 0, paddingLeft: ds.space.mul(1, 5) }}>
            {filesModified.map((f, idx) => (
              <li
                key={`fm-${idx}-${f}`}
                style={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-small)', color: 'var(--ds-brand-500)', wordBreak: 'break-all' }}
              >
                {f}
              </li>
            ))}
          </Box>
        </Box>
      )}
      {isNonEmptyString(verificationDetails) && (
        <Box mt={ds.space.mul(0, 3)}>
          <Typography
            sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[1] }}
          >
            Verification details
          </Typography>
          <MarkDowns data={verificationDetails.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />
        </Box>
      )}
    </Box>
  );
};

ExecutionResultStrip.propTypes = {
  status: PropTypes.string,
  summary: PropTypes.string,
  filesModified: PropTypes.array,
  verificationPassed: PropTypes.bool,
  verificationDetails: PropTypes.string,
  onLinkClick: PropTypes.func,
};

const CommitItem = ({ commit }) => {
  const [expanded, setExpanded] = useState(false);
  const sha = shortSha(commit.hash);
  const hasChanges = isNonEmptyString(commit.changes);
  return (
    <Box sx={{ borderBottom: '1px solid var(--ds-gray-100)', '&:last-child': { borderBottom: 'none' } }}>
      <Box
        onClick={() => hasChanges && setExpanded((e) => !e)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[2],
          padding: `${ds.space[2]} 0`,
          cursor: hasChanges ? 'pointer' : 'default',
        }}
      >
        {hasChanges &&
          (expanded ? (
            <ExpandMore sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />
          ) : (
            <ChevronRight sx={{ fontSize: 14, color: 'var(--ds-gray-600)' }} />
          ))}
        {!hasChanges && <Box sx={{ width: ds.space.mul(0, 7) }} />}
        {sha && (
          <Typography
            sx={{
              fontFamily: 'monospace',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-blue-500)',
              fontWeight: 'var(--ds-font-weight-semibold)',
            }}
          >
            {sha}
          </Typography>
        )}
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-brand-500)', flex: 1, wordBreak: 'break-word' }}>
          {isNonEmptyString(commit.message) ? commit.message : '(no message)'}
        </Typography>
        {isNonEmptyString(commit.author) && (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', whiteSpace: 'nowrap' }}>{commit.author}</Typography>
        )}
        {isNonEmptyString(commit.date) && (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', whiteSpace: 'nowrap' }}>{commit.date}</Typography>
        )}
      </Box>
      {expanded && hasChanges && (
        <Box sx={{ padding: `0 0 ${ds.space[2]} ${ds.space.mul(0, 11)}` }}>
          <SimpleDiffViewer gitDiff={commit.changes} defaultExpanded={true} showHeader={false} />
        </Box>
      )}
    </Box>
  );
};

CommitItem.propTypes = {
  commit: PropTypes.shape({
    hash: PropTypes.string,
    author: PropTypes.string,
    date: PropTypes.string,
    message: PropTypes.string,
    changes: PropTypes.string,
  }).isRequired,
};

const CommitsSection = ({ commits }) => {
  if (!isNonEmptyArray(commits)) {
    return null;
  }
  return (
    <CollapsibleSection
      icon={<HistoryEdu sx={{ fontSize: 16, color: 'var(--ds-gray-600)' }} />}
      label='Commits behind this'
      count={commits.length}
      defaultExpanded={false}
    >
      {commits.map((c, idx) => (
        <CommitItem key={`${c.hash || ''}-${idx}`} commit={c} />
      ))}
    </CollapsibleSection>
  );
};

CommitsSection.propTypes = {
  commits: PropTypes.array,
};

const extractPrUrl = (data) => {
  const candidate =
    data.pr_url || (data.pr_info && typeof data.pr_info === 'object' ? data.pr_info.url || data.pr_info.html_url || data.pr_info.web_url : null);
  return safeHttpUrl(candidate);
};

const extractRepoMeta = (sourceDetails) => {
  if (!sourceDetails || typeof sourceDetails !== 'object') {
    return { repo: null, hash: null };
  }
  const repo = sourceDetails[ANNOTATIONS.WORKLOAD_GIT_REPO] || sourceDetails.git_repo || sourceDetails.repo;
  const hash = sourceDetails[ANNOTATIONS.WORKLOAD_GIT_HASH] || sourceDetails.git_hash || sourceDetails.commit;
  return {
    repo: isNonEmptyString(repo) ? repo : null,
    hash: isNonEmptyString(hash) ? hash : null,
  };
};

const ResultFooter = ({ data }) => {
  const prUrl = extractPrUrl(data);
  const { repo, hash } = extractRepoMeta(data.source_details);
  const components = isNonEmptyArray(data.affected_components) ? data.affected_components : [];
  const issues = isNonEmptyArray(data.related_issues) ? data.related_issues : [];
  const prList = isNonEmptyArray(data.pr_list) ? data.pr_list : [];

  if (!prUrl && !repo && !hash && components.length === 0 && issues.length === 0 && prList.length === 0) {
    return null;
  }

  return (
    <Box
      sx={{
        mt: sectionGap,
        padding: `${ds.space.mul(0, 5)} ${ds.space[3]}`,
        borderTop: '1px solid var(--ds-gray-200)',
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[2],
      }}
    >
      {prUrl && (
        <Box>
          <a
            href={prUrl}
            target='_blank'
            rel='noopener noreferrer'
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: ds.space.mul(0, 3),
              padding: `${ds.space.mul(0, 3)} ${ds.space[3]}`,
              backgroundColor: 'var(--ds-blue-500)',
              color: 'var(--ds-background-100)',
              borderRadius: ds.radius.sm,
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              textDecoration: 'none',
            }}
          >
            View pull request
            <OpenInNew sx={{ fontSize: 14 }} />
          </a>
        </Box>
      )}
      {(repo || hash) && (
        <Box display='flex' alignItems='center' gap={ds.space[2]} flexWrap='wrap'>
          {repo && (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontFamily: 'monospace', wordBreak: 'break-all' }}>
              {repo}
            </Typography>
          )}
          {hash && (
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-brand-500)',
                fontFamily: 'monospace',
                backgroundColor: 'var(--ds-gray-100)',
                padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
                borderRadius: ds.radius.sm,
              }}
            >
              {shortSha(hash)}
            </Typography>
          )}
        </Box>
      )}
      {components.length > 0 && (
        <Box>
          <Typography
            sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[1] }}
          >
            Affected components
          </Typography>
          <Box display='flex' gap={ds.space[1]} flexWrap='wrap'>
            {components.map((c, idx) => (
              <Chip key={`comp-${idx}-${c}`} variant='tag' size='xs' tone='info'>
                {c}
              </Chip>
            ))}
          </Box>
        </Box>
      )}
      {issues.length > 0 && (
        <Box>
          <Typography
            sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[1] }}
          >
            Related issues
          </Typography>
          <Box display='flex' gap={ds.space[1]} flexWrap='wrap'>
            {issues.map((iss, idx) => (
              <Chip key={`iss-${idx}-${iss}`} variant='tag' size='xs' tone='neutral'>
                {iss}
              </Chip>
            ))}
          </Box>
        </Box>
      )}
      {prList.length > 0 && (
        <Box>
          <Typography
            sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[1] }}
          >
            Related PRs
          </Typography>
          <Box display='flex' flexDirection='column' gap={ds.space[1]}>
            {prList.map((pr, idx) => {
              const rawUrl = typeof pr === 'string' ? pr : pr?.url || pr?.html_url || pr?.web_url;
              const safeUrl = safeHttpUrl(rawUrl);
              const label = typeof pr === 'string' ? pr : pr?.title || pr?.name || rawUrl;
              const prKey = `pr-${idx}-${rawUrl || label || ''}`;
              if (!safeUrl && !isNonEmptyString(label)) {
                return null;
              }
              if (!safeUrl) {
                return (
                  <Typography key={prKey} sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-brand-500)' }}>
                    {label}
                  </Typography>
                );
              }
              return (
                <a
                  key={prKey}
                  href={safeUrl}
                  target='_blank'
                  rel='noopener noreferrer'
                  style={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-blue-500)', textDecoration: 'none', wordBreak: 'break-all' }}
                >
                  {label || safeUrl}
                </a>
              );
            })}
          </Box>
        </Box>
      )}
    </Box>
  );
};

ResultFooter.propTypes = {
  data: PropTypes.object.isRequired,
};

export const isCodeAnalysisShape = (parsed) => {
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return false;
  }
  const hasTitle = isNonEmptyString(parsed.title);
  if (!hasTitle) {
    return false;
  }
  return (
    isNonEmptyString(parsed.description) ||
    isNonEmptyString(parsed.root_cause_analysis) ||
    isNonEmptyString(parsed.git_diff) ||
    isNonEmptyString(parsed.fixed_code) ||
    isNonEmptyString(parsed.original_code) ||
    isNonEmptyString(parsed.file_path) ||
    isNonEmptyString(parsed.execution_status) ||
    isNonEmptyArray(parsed.investigation_trail) ||
    isNonEmptyArray(parsed.implementation_instructions) ||
    isNonEmptyArray(parsed.commits) ||
    isNonEmptyArray(parsed.affected_components) ||
    isNonEmptyArray(parsed.citations)
  );
};

const CodeAnalysisRenderer = ({ data, onLinkClick }) => {
  if (!data || typeof data !== 'object') {
    return null;
  }
  return (
    <Box sx={{ width: '100%' }}>
      <HeaderStrip title={data.title} confidenceScore={data.confidence_score} requiresFix={data.requires_fix} />
      <VerdictCallout errorMessage={data.error_message} description={data.description} onLinkClick={onLinkClick} />
      <RootCauseSection rootCause={data.root_cause_analysis} onLinkClick={onLinkClick} />
      <AffectedCodeBlock filePath={data.file_path} lineNumber={data.line_number} codeContext={data.code_context} originalCode={data.original_code} />
      <CitationsSection citations={data.citations} />
      <ProposedFixBlock gitDiff={data.git_diff} originalCode={data.original_code} fixedCode={data.fixed_code} />
      <ImplementationStepsSection steps={data.implementation_instructions} onLinkClick={onLinkClick} />
      <AlternativeFixesSection alternatives={data.alternative_fixes} onLinkClick={onLinkClick} />
      <InvestigationTrailSection trail={data.investigation_trail} onLinkClick={onLinkClick} />
      <ExecutionResultStrip
        status={data.execution_status}
        summary={data.execution_summary}
        filesModified={data.files_modified}
        verificationPassed={data.verification_passed}
        verificationDetails={data.verification_details}
        onLinkClick={onLinkClick}
      />
      <CommitsSection commits={data.commits} />
      <ResultFooter data={data} />
    </Box>
  );
};

CodeAnalysisRenderer.propTypes = {
  data: PropTypes.object.isRequired,
  onLinkClick: PropTypes.func,
};

export default CodeAnalysisRenderer;
