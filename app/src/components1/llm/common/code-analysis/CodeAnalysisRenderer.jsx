import React, { useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Chip, Typography } from '@mui/material';
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

const sectionGap = '14px';

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

const confidenceColor = (score) => {
  const s = (score || '').toLowerCase();
  if (s === 'high') {
    return { bg: '#dcfce7', fg: '#166534' };
  }
  if (s === 'medium' || s === 'med') {
    return { bg: '#fef9c3', fg: '#854d0e' };
  }
  if (s === 'low') {
    return { bg: '#fee2e2', fg: '#991b1b' };
  }
  return { bg: '#e5e7eb', fg: '#374151' };
};

const SectionTitle = ({ icon, label, count }) => (
  <Box display='flex' alignItems='center' gap='6px' mb='6px'>
    {icon}
    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: '#374151' }}>{label}</Typography>
    {typeof count === 'number' && <Typography sx={{ fontSize: '11px', color: '#6b7280', fontFamily: 'monospace' }}>({count})</Typography>}
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
    <Box sx={{ border: '1px solid #e5e7eb', borderRadius: '6px', mb: sectionGap, overflow: 'hidden' }}>
      <Box
        onClick={() => setExpanded((e) => !e)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          padding: '8px 12px',
          backgroundColor: '#f9fafb',
          cursor: 'pointer',
          borderBottom: expanded ? '1px solid #e5e7eb' : 'none',
        }}
      >
        {expanded ? <ExpandMore sx={{ fontSize: 16, color: '#6b7280' }} /> : <ChevronRight sx={{ fontSize: 16, color: '#6b7280' }} />}
        {icon}
        <Typography sx={{ fontSize: '13px', fontWeight: 600, color: '#374151' }}>{label}</Typography>
        {typeof count === 'number' && <Typography sx={{ fontSize: '11px', color: '#6b7280', fontFamily: 'monospace' }}>({count})</Typography>}
      </Box>
      {expanded && <Box sx={{ padding: '10px 12px' }}>{children}</Box>}
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
  const conf = confidenceColor(confidenceScore);
  return (
    <Box sx={{ mb: sectionGap }}>
      {isNonEmptyString(title) && <Typography sx={{ fontSize: '16px', fontWeight: 600, color: '#111827', lineHeight: 1.4 }}>{title}</Typography>}
      <Box display='flex' gap='6px' mt='6px' flexWrap='wrap'>
        {isNonEmptyString(confidenceScore) && (
          <Chip
            label={`Confidence: ${confidenceScore}`}
            size='small'
            sx={{ backgroundColor: conf.bg, color: conf.fg, fontWeight: 500, fontSize: '11px', height: '22px' }}
          />
        )}
        {requiresFix === true && (
          <Chip
            label='Requires fix'
            size='small'
            sx={{ backgroundColor: '#fee2e2', color: '#991b1b', fontWeight: 500, fontSize: '11px', height: '22px' }}
          />
        )}
        {requiresFix === false && (
          <Chip
            label='No fix needed'
            size='small'
            sx={{ backgroundColor: '#dcfce7', color: '#166534', fontWeight: 500, fontSize: '11px', height: '22px' }}
          />
        )}
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
        padding: '10px 12px',
        backgroundColor: '#fffbeb',
        borderLeft: '3px solid #f59e0b',
        borderRadius: '4px',
      }}
    >
      {isNonEmptyString(errorMessage) && (
        <Typography
          sx={{
            fontSize: '12px',
            fontFamily: 'monospace',
            color: '#92400e',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            mb: isNonEmptyString(description) ? '8px' : 0,
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
      <SectionTitle icon={<Search sx={{ fontSize: 16, color: '#6b7280' }} />} label='Root cause' />
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

const NumberedCodeBlock = ({ code, startLine, attachedToHeader, maxHeight = '320px' }) => {
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
        backgroundColor: '#1e1e1e',
        color: '#d4d4d4',
        fontFamily: 'monospace',
        fontSize: '12px',
        lineHeight: 1.5,
        overflow: 'auto',
        borderRadius: attachedToHeader ? '0 0 4px 4px' : '4px',
        border: '1px solid #e5e7eb',
        borderTop: attachedToHeader ? 'none' : '1px solid #e5e7eb',
        maxHeight,
      }}
    >
      <Box component='pre' sx={{ margin: 0, padding: '10px 0', whiteSpace: 'pre' }}>
        {lines.map((line, idx) => (
          <Box key={`l-${start ? start + idx : idx}`} sx={{ display: 'flex', alignItems: 'flex-start' }}>
            <Box
              component='span'
              sx={{
                display: 'inline-block',
                minWidth: gutterWidth,
                paddingLeft: '12px',
                paddingRight: '12px',
                color: '#6b7280',
                textAlign: 'right',
                userSelect: 'none',
                flexShrink: 0,
              }}
            >
              {start ? start + idx : idx + 1}
            </Box>
            <Box component='code' sx={{ flex: 1, paddingRight: '12px', whiteSpace: 'pre' }}>
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
      <SectionTitle icon={<Description sx={{ fontSize: 16, color: '#6b7280' }} />} label='Affected code' />
      {isNonEmptyString(filePath) && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            padding: '6px 10px',
            backgroundColor: '#f3f4f6',
            border: '1px solid #e5e7eb',
            borderRadius: '4px 4px 0 0',
            fontFamily: 'monospace',
            fontSize: '12px',
            color: '#374151',
          }}
        >
          <Code sx={{ fontSize: 14, color: '#6b7280' }} />
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
          padding: '10px 12px',
          backgroundColor: '#1e1e1e',
          color: '#d4d4d4',
          fontFamily: 'monospace',
          fontSize: '12px',
          lineHeight: 1.5,
          overflow: 'auto',
          borderRadius: '4px',
          whiteSpace: 'pre',
          maxHeight: '320px',
        }}
      >
        <code>{fixedCode}</code>
      </Box>
    );
  }
  return (
    <Box sx={{ mb: sectionGap }}>
      <SectionTitle icon={<Build sx={{ fontSize: 16, color: '#6b7280' }} />} label='Proposed fix' />
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
    <Box sx={{ mb: '10px', border: '1px solid #e5e7eb', borderRadius: '4px', overflow: 'hidden' }}>
      <Box
        onClick={() => hasSnippet && setExpanded((e) => !e)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          padding: '6px 10px',
          backgroundColor: '#f3f4f6',
          cursor: hasSnippet ? 'pointer' : 'default',
          borderBottom: expanded && hasSnippet ? '1px solid #e5e7eb' : 'none',
        }}
      >
        {hasSnippet && (expanded ? <ExpandMore sx={{ fontSize: 14, color: '#6b7280' }} /> : <ChevronRight sx={{ fontSize: 14, color: '#6b7280' }} />)}
        <Code sx={{ fontSize: 14, color: '#6b7280' }} />
        <Typography sx={{ fontFamily: 'monospace', fontSize: '12px', color: '#374151', wordBreak: 'break-all' }}>
          {isNonEmptyString(citation.file_path) ? citation.file_path : '(unknown file)'}
          {lineRange ? `:${lineRange}` : ''}
        </Typography>
      </Box>
      {isNonEmptyString(citation.note) && (
        <Typography sx={{ padding: '6px 10px', fontSize: '12px', color: '#4b5563', backgroundColor: '#fafafa' }}>{citation.note}</Typography>
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
      <SectionTitle icon={<Description sx={{ fontSize: 16, color: '#6b7280' }} />} label='Code references' count={citations.length} />
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
      <SectionTitle icon={<Build sx={{ fontSize: 16, color: '#6b7280' }} />} label='Implementation steps' count={steps.length} />
      <Box component='ol' sx={{ margin: 0, paddingLeft: '20px', '& > li': { marginBottom: '4px' } }}>
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
      <Box key={`alt-${idx}-${alt.slice(0, 30)}`} sx={{ mb: '8px' }}>
        <MarkDowns data={alt.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />
      </Box>
    );
  }
  if (alt && typeof alt === 'object') {
    const altKey = `alt-${idx}-${alt.title || alt.description?.slice(0, 30) || ''}`;
    return (
      <Box key={altKey} sx={{ mb: '10px', padding: '8px 10px', border: '1px solid #e5e7eb', borderRadius: '4px', backgroundColor: '#fafafa' }}>
        {isNonEmptyString(alt.title) && <Typography sx={{ fontSize: '13px', fontWeight: 600, color: '#374151', mb: '4px' }}>{alt.title}</Typography>}
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
      icon={<Build sx={{ fontSize: 16, color: '#6b7280' }} />}
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
      icon={<Search sx={{ fontSize: 16, color: '#6b7280' }} />}
      label='Investigation trail'
      count={trail.length}
      defaultExpanded={false}
    >
      <Box component='ol' sx={{ margin: 0, paddingLeft: '20px', '& > li': { marginBottom: '6px', fontSize: '12px' } }}>
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
    return { bg: '#fef2f2', border: '#fecaca', fg: '#991b1b', icon: <ErrorIcon sx={{ fontSize: 18, color: '#dc2626' }} /> };
  }
  if (ok) {
    return {
      bg: '#f0fdf4',
      border: '#bbf7d0',
      fg: '#166534',
      icon: <CheckCircle sx={{ fontSize: 18, color: verificationPassed === false ? '#ca8a04' : '#16a34a' }} />,
    };
  }
  return { bg: '#f9fafb', border: '#e5e7eb', fg: '#374151', icon: <CheckCircle sx={{ fontSize: 18, color: '#6b7280' }} /> };
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
        padding: '10px 12px',
        backgroundColor: style.bg,
        border: `1px solid ${style.border}`,
        borderRadius: '6px',
      }}
    >
      <Box display='flex' alignItems='center' gap='8px' mb={isNonEmptyString(summary) || isNonEmptyArray(filesModified) ? '8px' : 0}>
        {style.icon}
        <Typography sx={{ fontSize: '13px', fontWeight: 600, color: style.fg }}>
          {isNonEmptyString(status) ? `Execution: ${status}` : 'Execution result'}
        </Typography>
        {verificationPassed === true && (
          <Chip
            label='Verification passed'
            size='small'
            sx={{ backgroundColor: '#dcfce7', color: '#166534', fontWeight: 500, fontSize: '11px', height: '20px' }}
          />
        )}
        {verificationPassed === false && (
          <Chip
            label='Verification failed'
            size='small'
            sx={{ backgroundColor: '#fee2e2', color: '#991b1b', fontWeight: 500, fontSize: '11px', height: '20px' }}
          />
        )}
      </Box>
      {isNonEmptyString(summary) && <MarkDowns data={summary.replace(/~/g, '\\~')} sx={{ width: '100%', p: 0 }} onLinkClick={onLinkClick} />}
      {isNonEmptyArray(filesModified) && (
        <Box mt='6px'>
          <Typography sx={{ fontSize: '11px', color: '#6b7280', fontWeight: 600, mb: '4px' }}>Files modified</Typography>
          <Box component='ul' sx={{ margin: 0, paddingLeft: '20px' }}>
            {filesModified.map((f, idx) => (
              <li key={`fm-${idx}-${f}`} style={{ fontFamily: 'monospace', fontSize: '12px', color: '#374151', wordBreak: 'break-all' }}>
                {f}
              </li>
            ))}
          </Box>
        </Box>
      )}
      {isNonEmptyString(verificationDetails) && (
        <Box mt='6px'>
          <Typography sx={{ fontSize: '11px', color: '#6b7280', fontWeight: 600, mb: '4px' }}>Verification details</Typography>
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
    <Box sx={{ borderBottom: '1px solid #f3f4f6', '&:last-child': { borderBottom: 'none' } }}>
      <Box
        onClick={() => hasChanges && setExpanded((e) => !e)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
          padding: '8px 0',
          cursor: hasChanges ? 'pointer' : 'default',
        }}
      >
        {hasChanges && (expanded ? <ExpandMore sx={{ fontSize: 14, color: '#6b7280' }} /> : <ChevronRight sx={{ fontSize: 14, color: '#6b7280' }} />)}
        {!hasChanges && <Box sx={{ width: '14px' }} />}
        {sha && <Typography sx={{ fontFamily: 'monospace', fontSize: '12px', color: '#2563eb', fontWeight: 600 }}>{sha}</Typography>}
        <Typography sx={{ fontSize: '12px', color: '#374151', flex: 1, wordBreak: 'break-word' }}>
          {isNonEmptyString(commit.message) ? commit.message : '(no message)'}
        </Typography>
        {isNonEmptyString(commit.author) && (
          <Typography sx={{ fontSize: '11px', color: '#6b7280', whiteSpace: 'nowrap' }}>{commit.author}</Typography>
        )}
        {isNonEmptyString(commit.date) && <Typography sx={{ fontSize: '11px', color: '#6b7280', whiteSpace: 'nowrap' }}>{commit.date}</Typography>}
      </Box>
      {expanded && hasChanges && (
        <Box sx={{ padding: '0 0 8px 22px' }}>
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
      icon={<HistoryEdu sx={{ fontSize: 16, color: '#6b7280' }} />}
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
        padding: '10px 12px',
        borderTop: '1px solid #e5e7eb',
        display: 'flex',
        flexDirection: 'column',
        gap: '8px',
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
              gap: '6px',
              padding: '6px 12px',
              backgroundColor: '#2563eb',
              color: '#fff',
              borderRadius: '4px',
              fontSize: '12px',
              fontWeight: 600,
              textDecoration: 'none',
            }}
          >
            View pull request
            <OpenInNew sx={{ fontSize: 14 }} />
          </a>
        </Box>
      )}
      {(repo || hash) && (
        <Box display='flex' alignItems='center' gap='8px' flexWrap='wrap'>
          {repo && <Typography sx={{ fontSize: '11px', color: '#6b7280', fontFamily: 'monospace', wordBreak: 'break-all' }}>{repo}</Typography>}
          {hash && (
            <Typography
              sx={{
                fontSize: '11px',
                color: '#374151',
                fontFamily: 'monospace',
                backgroundColor: '#f3f4f6',
                padding: '2px 6px',
                borderRadius: '3px',
              }}
            >
              {shortSha(hash)}
            </Typography>
          )}
        </Box>
      )}
      {components.length > 0 && (
        <Box>
          <Typography sx={{ fontSize: '11px', color: '#6b7280', fontWeight: 600, mb: '4px' }}>Affected components</Typography>
          <Box display='flex' gap='4px' flexWrap='wrap'>
            {components.map((c, idx) => (
              <Chip
                key={`comp-${idx}-${c}`}
                label={c}
                size='small'
                sx={{ backgroundColor: '#e0e7ff', color: '#3730a3', fontSize: '11px', height: '20px' }}
              />
            ))}
          </Box>
        </Box>
      )}
      {issues.length > 0 && (
        <Box>
          <Typography sx={{ fontSize: '11px', color: '#6b7280', fontWeight: 600, mb: '4px' }}>Related issues</Typography>
          <Box display='flex' gap='4px' flexWrap='wrap'>
            {issues.map((iss, idx) => (
              <Chip
                key={`iss-${idx}-${iss}`}
                label={iss}
                size='small'
                sx={{ backgroundColor: '#f3f4f6', color: '#374151', fontSize: '11px', height: '20px' }}
              />
            ))}
          </Box>
        </Box>
      )}
      {prList.length > 0 && (
        <Box>
          <Typography sx={{ fontSize: '11px', color: '#6b7280', fontWeight: 600, mb: '4px' }}>Related PRs</Typography>
          <Box display='flex' flexDirection='column' gap='4px'>
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
                  <Typography key={prKey} sx={{ fontSize: '11px', color: '#374151' }}>
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
                  style={{ fontSize: '11px', color: '#2563eb', textDecoration: 'none', wordBreak: 'break-all' }}
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
