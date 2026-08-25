/**
 * EgressFilterDetailModal — the per-message diagnostic view for the egressfilter
 * chip. Click a secrets or PII chip in ResponseMetaRail to open.
 *
 * Answers "what fired and where" WITHOUT exposing raw values — same
 * design principle as the audit events themselves (no PII / secrets at
 * rest). Renders three attribution axes for each detector:
 *
 *   1. What triggered it (rules for secrets / categories for PII)
 *   2. Which agent(s) contributed
 *   3. Which message roles (user / system / tool response) held the hits
 *
 * Aggregation is done client-side from data the backend already emits:
 *   - FilterEvent has a per-hit array (rule_id + source per hit) already
 *   - PIIScrubEvent (post-2026-08-01 backend enrichment) carries
 *     category_counts + agent_counts alongside the flat hit_count
 *
 * Values are never shown. Audit IDs are surfaced verbatim for log
 * correlation — ops paste the "egress-abc123" prefix into their log tool.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Chip } from '@ui/Chip';

const sectionLabelSx = {
  fontSize: 'var(--ds-text-body)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  color: 'var(--ds-gray-800)',
  mb: 'var(--ds-space-1)',
};

const rowLabelSx = {
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-700)',
  fontWeight: 'var(--ds-font-weight-medium)',
  mb: 'var(--ds-space-1)',
};

const descSx = {
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-600)',
  lineHeight: 1.5,
};

const monoSx = {
  fontFamily: 'var(--ds-font-mono, monospace)',
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-700)',
  wordBreak: 'break-all',
};

// Human-readable descriptions for the built-in secret rule ids. Missing
// entries fall through to the rule id as-is — better to show a raw id
// than nothing when a new rule is added on the backend without a UI
// entry (kept intentionally forward-compat).
const RULE_HINTS = {
  'aws-access-key-id': '20-char AWS-style access key ID (AKIA/ASIA/…)',
  'anthropic-api-key': 'Anthropic API key (sk-ant-… ≥32 chars)',
  'openai-api-key': 'OpenAI API key (sk-… / sk-proj-… ≥32 chars)',
  'high-entropy-blob': 'Random-looking ≥32 char token (entropy ≥4.5). Often noisy on ops text — image digests, JWT bodies, session IDs.',
  'jwt-token': 'JSON Web Token (eyJ… . eyJ… . …)',
  'bearer-token': 'HTTP Bearer authorization header value',
  'private-key-header': 'PEM private-key block (-----BEGIN … PRIVATE KEY-----)',
  ssn: 'US Social Security Number (NNN-NN-NNNN)',
  'credit-card': '13-16 digit sequence passing Luhn checksum',
};

// Human-readable descriptions for PII categories.
const CATEGORY_HINTS = {
  EMAIL: 'Email address (regex — high precision)',
  PHONE: 'Phone number (regex — E.164, US parens, or hyphen-separated 3-3-4)',
  PERSON: 'Person name (NER — spaCy; fuzzy on ops text)',
  LOCATION: 'Location or geopolitical entity (NER — spaCy; may false-fire on infra vocab)',
};

/**
 * Aggregate FilterEvent hits[] arrays across events into per-key counts.
 * Handles legacy events with no hits[] (they still carry rule_ids at the
 * top level, so use that as a fallback).
 */
const aggregateSecrets = (events) => {
  const rules = {};
  const sources = {};
  const agents = {};
  const auditIds = [];
  let totalHits = 0;

  events.forEach((e) => {
    if (e?.audit_id) auditIds.push(e.audit_id);
    // When we have hits[], count agents by hits.length not hit_count —
    // otherwise a truncated/capped hits[] leaves the agent total > sum-of-
    // hits-rendered, breaking the "sum by axis = total" invariant users
    // rely on to verify the breakdown. (Gemini review on #35437.)
    const hasHits = Array.isArray(e?.hits) && e.hits.length > 0;
    if (e?.agent_name) {
      agents[e.agent_name] = (agents[e.agent_name] || 0) + (hasHits ? e.hits.length : Number(e.hit_count) || 0);
    }
    if (hasHits) {
      e.hits.forEach((h) => {
        if (h?.rule_id) rules[h.rule_id] = (rules[h.rule_id] || 0) + 1;
        if (h?.source) sources[h.source] = (sources[h.source] || 0) + 1;
        totalHits += 1;
      });
    } else if (Array.isArray(e?.rule_ids)) {
      // Legacy row without hits[] — fall back to rule_ids + hit_count.
      e.rule_ids.forEach((r) => {
        if (r) rules[r] = (rules[r] || 0) + 1;
      });
      if (Array.isArray(e?.hit_sources)) {
        e.hit_sources.forEach((s) => {
          if (s) sources[s] = (sources[s] || 0) + 1;
        });
      }
      totalHits += Number(e.hit_count) || 0;
    }
  });

  return { rules, sources, agents, auditIds, totalHits };
};

/**
 * Same shape for PII, but reads the backend-provided category_counts +
 * agent_counts fields directly. Falls back to categories/hit_count for
 * legacy events written before those fields existed.
 */
const aggregatePii = (events) => {
  const categories = {};
  const agents = {};
  const auditIds = [];
  let totalHits = 0;

  events.forEach((e) => {
    if (e?.audit_id) auditIds.push(e.audit_id);
    totalHits += Number(e?.hit_count) || 0;

    if (e?.category_counts && typeof e.category_counts === 'object') {
      Object.entries(e.category_counts).forEach(([cat, count]) => {
        categories[cat] = (categories[cat] || 0) + (Number(count) || 0);
      });
    } else if (Array.isArray(e?.categories)) {
      // Legacy: no counts, only the sorted set. Best we can do is 1 per
      // category (visual placeholder — accurate total sits above).
      e.categories.forEach((c) => {
        if (c) categories[c] = (categories[c] || 0) + 1;
      });
    }

    if (e?.agent_counts && typeof e.agent_counts === 'object') {
      Object.entries(e.agent_counts).forEach(([name, count]) => {
        agents[name] = (agents[name] || 0) + (Number(count) || 0);
      });
    } else if (e?.agent_name) {
      // Legacy: comma-joined string, no counts.
      e.agent_name.split(',').forEach((name) => {
        const trimmed = name.trim();
        if (trimmed) agents[trimmed] = agents[trimmed] || 0;
      });
    }
  });

  return { categories, agents, auditIds, totalHits };
};

// Sort a {key: count} map descending by count, then alphabetically for
// stable order. Returns an array of [key, count] pairs.
const sortedEntries = (obj) => Object.entries(obj || {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));

const CountRow = ({ label, count, hint }) => (
  <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)', py: 'var(--ds-space-1)' }}>
    <Chip variant='count' tone='neutral' count={count} size='xs'>
      {label}
    </Chip>
    {hint && <Box sx={{ ...descSx, flex: 1, alignSelf: 'center' }}>{hint}</Box>}
  </Box>
);

CountRow.propTypes = {
  label: PropTypes.string.isRequired,
  count: PropTypes.number.isRequired,
  hint: PropTypes.string,
};

CountRow.defaultProps = { hint: undefined };

const Section = ({ title, entries, hintMap }) => {
  if (!entries || entries.length === 0) return null;
  return (
    <Box sx={{ mb: 'var(--ds-space-3)' }}>
      <Box sx={rowLabelSx}>{title}</Box>
      <Box sx={{ display: 'flex', flexDirection: 'column' }}>
        {entries.map(([key, count]) => (
          <CountRow key={key} label={key} count={count} hint={hintMap ? hintMap[key] : undefined} />
        ))}
      </Box>
    </Box>
  );
};

Section.propTypes = {
  title: PropTypes.string.isRequired,
  entries: PropTypes.array.isRequired,
  hintMap: PropTypes.object,
};

Section.defaultProps = { hintMap: undefined };

const AuditIds = ({ ids }) => {
  if (!ids || ids.length === 0) return null;
  // Dedupe — same event can appear twice on retry / duplicate emission,
  // "scrub-abc, scrub-abc" is noise. Same fix pattern as the PII chip
  // tooltip (Gemini review on #35437, mirrors the #35286 tooltip fix).
  const uniqueIds = Array.from(new Set(ids));
  return (
    <Box sx={{ mb: 'var(--ds-space-3)' }}>
      <Box sx={rowLabelSx}>Audit IDs (for log correlation)</Box>
      <Box sx={monoSx}>{uniqueIds.join(', ')}</Box>
    </Box>
  );
};

AuditIds.propTypes = { ids: PropTypes.array.isRequired };

const SecretsPanel = ({ events }) => {
  if (!events || events.length === 0) return null;
  const agg = aggregateSecrets(events);
  return (
    <Box sx={{ pb: 'var(--ds-space-3)', borderBottom: '1px solid var(--ds-gray-200)', mb: 'var(--ds-space-3)' }}>
      <Box sx={sectionLabelSx}>
        Secrets — {agg.totalHits} hit{agg.totalHits === 1 ? '' : 's'}
      </Box>
      <Box sx={{ ...descSx, mb: 'var(--ds-space-3)' }}>
        The outbound egressfilter matched these patterns in the LLM payload (raw values never persisted). The payload includes prior conversation,
        tool responses, and system prompt — not just what you typed.
      </Box>
      <Section title='Rules fired' entries={sortedEntries(agg.rules)} hintMap={RULE_HINTS} />
      <Section title='Contributing agents' entries={sortedEntries(agg.agents)} />
      <Section title='Source roles' entries={sortedEntries(agg.sources)} />
      <AuditIds ids={agg.auditIds} />
    </Box>
  );
};

SecretsPanel.propTypes = { events: PropTypes.array };
SecretsPanel.defaultProps = { events: [] };

const PiiPanel = ({ events }) => {
  if (!events || events.length === 0) return null;
  const agg = aggregatePii(events);
  return (
    <Box>
      <Box sx={sectionLabelSx}>
        PII / PHI — {agg.totalHits} distinct value{agg.totalHits === 1 ? '' : 's'}
      </Box>
      <Box sx={{ ...descSx, mb: 'var(--ds-space-3)' }}>
        Distinct-value counts across all wrapper calls this turn. A value referenced by multiple agents is counted once (attributed to the first agent
        to introduce it).
      </Box>
      <Section title='Categories' entries={sortedEntries(agg.categories)} hintMap={CATEGORY_HINTS} />
      <Section title='Contributing agents (new distinct values introduced)' entries={sortedEntries(agg.agents)} />
      <AuditIds ids={agg.auditIds} />
    </Box>
  );
};

PiiPanel.propTypes = { events: PropTypes.array };
PiiPanel.defaultProps = { events: [] };

const EgressFilterDetailModal = ({ open, onClose, secretEvents, piiEvents }) => {
  const hasSecrets = Array.isArray(secretEvents) && secretEvents.length > 0;
  const hasPii = Array.isArray(piiEvents) && piiEvents.length > 0;
  // Both empty is an edge case (a chip that opened even though the events
  // are neither secrets nor pii — likely a new detector type shipped
  // backend-first, or malformed data). Show an explicit fallback instead
  // of a blank modal. (Gemini review on #35437.)
  return (
    <Modal open={open} title='Egress filter — details' width='md' handleClose={onClose}>
      <Box sx={{ display: 'flex', flexDirection: 'column' }}>
        {!hasSecrets && !hasPii ? (
          <Box sx={descSx}>No diagnostic details available for this message.</Box>
        ) : (
          <>
            <SecretsPanel events={secretEvents} />
            <PiiPanel events={piiEvents} />
          </>
        )}
      </Box>
    </Modal>
  );
};

EgressFilterDetailModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  secretEvents: PropTypes.array,
  piiEvents: PropTypes.array,
};

EgressFilterDetailModal.defaultProps = {
  secretEvents: [],
  piiEvents: [],
};

export default EgressFilterDetailModal;

// Exported for direct unit testing of the aggregators.
export const __aggregateSecretsForTest = aggregateSecrets;
export const __aggregatePiiForTest = aggregatePii;
