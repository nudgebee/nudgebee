import React, { useState, useEffect, useMemo } from 'react';
import PropTypes from 'prop-types';
import { Tabs, Tab, Box, Stack, Table, TableBody, TableCell, TableRow, IconButton, Collapse, Typography, Link as MuiLink } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import Chip from '@ui/Chip';
import CustomTablePagination from '@shared/tables/CustomTablePagination';

// Two-level classification for the Additional Contexts panel.
// Category = the top-level tab a reference belongs to.
// Subtype = the pill filter under that category (== the raw reference_type).
// Kept next to the component so the mapping is discoverable at the point of
// use rather than buried in a util file.
const CATEGORY_LABELS = {
  memory: 'Memory',
  knowledge_base: 'Knowledge Base',
  context_state: 'Context State',
  other: 'Other',
};
const SUBTYPE_LABELS = {
  memory: 'Legacy',
  memory_patterns: 'Patterns',
  memory_preferences: 'Preferences',
  memory_decisions: 'Decisions',
  memory_collective: 'Collective',
  memory_soul: 'Soul',
  memory_session: 'Session',
  knowledge_base: 'Knowledge Base',
  context_state: 'Context State',
};
const CATEGORY_ORDER = ['memory', 'knowledge_base', 'context_state', 'other'];

// Per-subtype visual mapping. `hue` is the categorical color family the
// primitive maps to. Used on BOTH the filter pill (Level 2) and the tag
// chip in the Type column so a subtype's color is the same wherever it
// appears — otherwise filter selection + row lookup fight each other for
// the user's attention.
//
// Note: the ds Chip primitive's docstring says "hue is for tag chips only",
// but the alternative here — mapping subtypes to ds `tone` slots — forces
// awkward semantic pairings (KB / context_state have no natural tone) and
// STILL produces different colors from the tag hues, defeating the purpose.
// The visual-consistency win outweighs the soft-rule miss.
const SUBTYPE_HUE = {
  memory: 'slate',
  memory_patterns: 'blue',
  memory_preferences: 'green',
  memory_decisions: 'amber',
  memory_collective: 'violet',
  memory_soul: 'pink',
  memory_session: 'teal',
  knowledge_base: 'blue',
  context_state: 'slate',
};
const categorizeRef = (type) => {
  if (typeof type !== 'string') return 'other';
  if (type === 'memory' || type.startsWith('memory_')) return 'memory';
  if (type === 'knowledge_base') return 'knowledge_base';
  if (type === 'context_state') return 'context_state';
  return 'other';
};

// deriveSubject picks the best short label for a row's Subject cell.
// Memory-v2 rows carry `metadata.subject` (populated by the bridge);
// knowledge-base rows carry `metadata.name` (the KB / runbook name); older
// rows may only have `content`. Fall back through all three so no row ever
// renders blank — KB rows previously fell straight to "(unnamed)".
const deriveSubject = (ref) => {
  const s = ref?.metadata?.subject;
  if (typeof s === 'string' && s.trim()) return s;
  const n = ref?.metadata?.name;
  if (typeof n === 'string' && n.trim()) return n;
  if (typeof ref?.content === 'string' && ref.content.trim()) {
    const line = ref.content.split('\n').find((l) => l.trim());
    if (line) return line.length > 60 ? `${line.slice(0, 60)}…` : line;
  }
  return '(unnamed)';
};

const formatScore = (score) => {
  if (score === undefined || score === null || score === '') return null;
  const n = Number(score);
  if (Number.isNaN(n) || n === 0) return null;
  // Memory scores are cosine similarity / confidence in [0..1]; format as %.
  // Anything larger falls back to raw for future non-normalised sources.
  if (n > 0 && n <= 1) return `${Math.round(n * 100)}%`;
  return String(n);
};

/**
 * ReferencesDrawerContent
 * Category-tab + subtype-pill filter over an Additional Contexts reference
 * list, with an expandable table for the filtered rows:
 *   Level 1 — category tabs (Memory / Knowledge Base / …), only for
 *             categories with rows this turn.
 *   Level 2 — subtype pills (Preferences / Patterns / Soul / …), only when
 *             the active category has more than one subtype.
 *   Table   — Type / Subject / Score columns. Click a row's chevron to
 *             expand and show the full content + metadata inline.
 * Used by both the drawer opened from MessageStream and the Additional
 * Contexts tab in LLMConversationWithTabs so the UX is identical.
 */
const ReferencesDrawerContent = ({ references = [] }) => {
  const [activeCategory, setActiveCategory] = useState(null);
  const [activeSubtype, setActiveSubtype] = useState('all');
  const [expandedRowId, setExpandedRowId] = useState(null);
  // CustomTablePagination is 1-indexed (matches the rest of the app's tables).
  const [page, setPage] = useState(1);
  const [rowsPerPage, setRowsPerPage] = useState(10);

  // Categorised buckets + pill counts. Memoised on `references` so we don't
  // re-bucket on every unrelated render. `usedCount` is the number of rows
  // in the bucket the LLM actually cited via <memory_used> (point 7 audit
  // signal); drives the "Used ✓" filter pill's count + visibility.
  const buckets = useMemo(() => {
    const acc = {};
    for (const ref of references) {
      const cat = categorizeRef(ref?.type);
      if (!acc[cat]) acc[cat] = { total: 0, usedCount: 0, bySubtype: {}, rows: [] };
      acc[cat].total += 1;
      if (ref.used) acc[cat].usedCount += 1;
      acc[cat].bySubtype[ref.type] = (acc[cat].bySubtype[ref.type] || 0) + 1;
      acc[cat].rows.push(ref);
    }
    return acc;
  }, [references]);

  const availableCategories = useMemo(() => CATEGORY_ORDER.filter((c) => buckets[c]?.total > 0), [buckets]);

  useEffect(() => {
    if (availableCategories.length === 0) {
      if (activeCategory !== null) setActiveCategory(null);
      return;
    }
    if (!activeCategory || !availableCategories.includes(activeCategory)) {
      setActiveCategory(availableCategories[0]);
      setActiveSubtype('all');
    }
  }, [availableCategories, activeCategory]);

  const activeBucket = activeCategory ? buckets[activeCategory] : null;
  const filteredRefs = useMemo(() => {
    if (!activeBucket) return [];
    if (activeSubtype === 'used') {
      // Cross-cutting "Used ✓" filter: shows only rows the LLM cited
      // (used=true), across all subtypes in the active category. Sorted
      // by subtype first then rank so cited items stay grouped by layer.
      return activeBucket.rows
        .filter((r) => r.used)
        .sort((a, b) => {
          if (a.type !== b.type) return a.type < b.type ? -1 : 1;
          return (a?.metadata?.rank ?? 0) - (b?.metadata?.rank ?? 0);
        });
    }
    if (activeSubtype !== 'all') {
      // Single-subtype view: scope to the picked subtype, then order by
      // rank so compose's rerank position is preserved.
      return activeBucket.rows.filter((r) => r.type === activeSubtype).sort((a, b) => (a?.metadata?.rank ?? 0) - (b?.metadata?.rank ?? 0));
    }
    // "All" view: group by subtype (same order as the pill row —
    // largest bucket first), then by rank within each subtype. Without this,
    // rows come back in created_at order — every memory row from one turn
    // shares a timestamp, so goroutine race decides the order and the list
    // looks jumbled.
    const subtypeOrder = Object.entries(activeBucket.bySubtype)
      .sort((a, b) => b[1] - a[1])
      .map(([subtype], idx) => [subtype, idx]);
    const subtypeRank = Object.fromEntries(subtypeOrder);
    return [...activeBucket.rows].sort((a, b) => {
      const sa = subtypeRank[a.type] ?? Number.MAX_SAFE_INTEGER;
      const sb = subtypeRank[b.type] ?? Number.MAX_SAFE_INTEGER;
      if (sa !== sb) return sa - sb;
      return (a?.metadata?.rank ?? 0) - (b?.metadata?.rank ?? 0);
    });
  }, [activeBucket, activeSubtype]);

  // Collapse any expanded row when the filter changes so a hidden row's
  // expanded state doesn't leak into the next view. Also reset to page 1
  // so a filter that shrinks the list can't leave the pager stranded on a
  // now-empty page N.
  useEffect(() => {
    setExpandedRowId(null);
    setPage(1);
  }, [activeCategory, activeSubtype, references]);

  const pagedRefs = useMemo(() => {
    const start = (page - 1) * rowsPerPage;
    return filteredRefs.slice(start, start + rowsPerPage);
  }, [filteredRefs, page, rowsPerPage]);

  if (!references.length) return null;

  const rowKey = (ref, idx) => ref.id || `${ref.type}-${idx}`;

  return (
    <Box
      sx={{
        // Match the drawer-header font stack so tabs, filter pills, and
        // content read as one typographic system with the "Additional
        // Contexts · N" title. Set once on the root so MUI Tab, ds Chip,
        // Typography, and the content <pre> all inherit — much less noise
        // than setting fontFamily on each nested component.
        fontFamily: 'var(--ds-font-display)',
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: 0,
      }}
    >
      {/* Level 1 — category tabs. Tab typography mirrors the drawer header
          (body-lg + semibold + brand-700 + display font, textTransform none)
          so the tab strip and "Additional Contexts · N" read as one unit
          rather than two competing type systems. */}
      <Tabs
        value={activeCategory || availableCategories[0] || false}
        onChange={(_e, v) => {
          setActiveCategory(v);
          setActiveSubtype('all');
        }}
        variant='scrollable'
        scrollButtons='auto'
        sx={{
          borderBottom: 1,
          borderColor: 'divider',
          mb: 1.5,
          flexShrink: 0,
          '& .MuiTab-root': {
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body-lg)',
            // regular (400) for unselected — lightest we can go and still
            // read; selected bumps to medium so state is legible without
            // shouting.
            fontWeight: 'var(--ds-font-weight-regular)',
            textTransform: 'none',
            color: 'var(--ds-gray-500)',
          },
          '& .MuiTab-root.Mui-selected': {
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-brand-700)',
          },
        }}
      >
        {availableCategories.map((cat) => (
          <Tab key={cat} value={cat} label={`${CATEGORY_LABELS[cat] || cat} · ${buckets[cat].total}`} />
        ))}
      </Tabs>

      {/* Level 2 — subtype pill filters. Shown when the category has more
          than one subtype OR at least one used=true row (so the "Used" cross-
          cutting pill is reachable even in a single-subtype category). */}
      {activeBucket && (Object.keys(activeBucket.bySubtype).length > 1 || activeBucket.usedCount > 0) && (
        <Stack direction='row' spacing={1} sx={{ mb: 1.5, flexWrap: 'wrap', rowGap: 1, flexShrink: 0 }}>
          <Chip variant='filter' size='xs' selected={activeSubtype === 'all'} onClick={() => setActiveSubtype('all')}>
            {`All (${activeBucket.total})`}
          </Chip>
          {/* Cross-cutting "Used ✓" pill — only rendered when the LLM
              actually cited at least one row in this category. Green hue
              matches the row-level ✓ chip so the audit signal is uniform. */}
          {activeBucket.usedCount > 0 && (
            <Chip variant='filter' size='xs' hue='green' selected={activeSubtype === 'used'} onClick={() => setActiveSubtype('used')}>
              {`Used ✓ (${activeBucket.usedCount})`}
            </Chip>
          )}
          {Object.entries(activeBucket.bySubtype)
            .sort((a, b) => b[1] - a[1])
            .map(([subtype, count]) => (
              <Chip
                key={subtype}
                variant='filter'
                size='xs'
                hue={SUBTYPE_HUE[subtype] || 'slate'}
                selected={activeSubtype === subtype}
                onClick={() => setActiveSubtype(subtype)}
              >
                {`${SUBTYPE_LABELS[subtype] || subtype} (${count})`}
              </Chip>
            ))}
        </Stack>
      )}

      {/* Expandable table. Each row is a summary; clicking the chevron (or
          anywhere on the row) toggles the full content underneath. */}
      <Box sx={{ flex: 1, overflowY: 'auto' }}>
        {/* No <TableHead> — the two columns (colored type chip, subject text)
            are self-labelling, and dropping the header saves a row of chrome
            that was mostly noise in a narrow drawer. Fixed table-layout so
            widths come from the <colgroup> below rather than from the widest
            cell content — otherwise the Type chip (which fits "Preferences"
            etc.) would balloon and starve the Subject column. */}
        <Table size='small' sx={{ tableLayout: 'fixed', width: '100%' }}>
          <colgroup>
            <col style={{ width: 40 }} />
            <col style={{ width: 110 }} />
            <col />
          </colgroup>
          <TableBody>
            {pagedRefs.map((ref, idx) => {
              // Compose the fallback key from the *global* index so rows on
              // different pages can't share a key when a ref lacks an id.
              const id = rowKey(ref, (page - 1) * rowsPerPage + idx);
              const isExpanded = expandedRowId === id;
              const subject = deriveSubject(ref);
              const scoreLabel = formatScore(ref?.metadata?.score);
              const toggleRow = () => setExpandedRowId(isExpanded ? null : id);
              return (
                <React.Fragment key={id}>
                  <TableRow hover onClick={toggleRow} sx={{ cursor: 'pointer', '& > *': { borderBottom: isExpanded ? 'unset' : undefined } }}>
                    <TableCell sx={{ py: 0.5 }}>
                      <IconButton size='small' aria-label={isExpanded ? 'collapse row' : 'expand row'}>
                        {isExpanded ? <KeyboardArrowDownIcon fontSize='small' /> : <KeyboardArrowRightIcon fontSize='small' />}
                      </IconButton>
                    </TableCell>
                    <TableCell sx={{ py: 0.5 }}>
                      <Stack direction='row' spacing={0.5} sx={{ alignItems: 'center' }}>
                        <Chip variant='tag' size='xs' hue={SUBTYPE_HUE[ref.type] || 'slate'}>
                          {SUBTYPE_LABELS[ref.type] || ref.type}
                        </Chip>
                        {ref.used && (
                          <Chip
                            variant='tag'
                            size='xs'
                            hue='green'
                            title={ref.used_by_agent ? `Cited by ${ref.used_by_agent}` : 'LLM cited this memory in at least one action'}
                          >
                            ✓
                          </Chip>
                        )}
                      </Stack>
                    </TableCell>
                    {/* Subject cell prefixes the citing-agent chip (when used).
                        Type cell is a fixed 110px — a third chip there
                        overflowed into Subject; this column is flex so the
                        agent chip lives here without fighting for space. */}
                    <TableCell
                      sx={{
                        py: 0.5,
                        overflow: 'hidden',
                        maxWidth: 0,
                      }}
                    >
                      <Stack direction='row' spacing={0.5} sx={{ alignItems: 'center', minWidth: 0 }}>
                        {ref.used && ref.used_by_agent && (
                          <Chip variant='tag' size='xs' hue='slate' title={`Cited by ${ref.used_by_agent}`} sx={{ flexShrink: 0 }}>
                            {ref.used_by_agent}
                          </Chip>
                        )}
                        <Box sx={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0, flex: 1 }}>{subject}</Box>
                      </Stack>
                    </TableCell>
                  </TableRow>
                  <TableRow>
                    <TableCell colSpan={3} sx={{ py: 0, borderBottom: isExpanded ? undefined : 0 }}>
                      <Collapse in={isExpanded} timeout='auto' unmountOnExit>
                        <Box sx={{ py: 1.5, pl: 5, pr: 2 }}>
                          <Stack direction='row' spacing={1} sx={{ mb: 1, flexWrap: 'wrap', rowGap: 0.5 }}>
                            {ref?.metadata?.source && (
                              <Chip variant='tag' size='xs'>
                                {String(ref.metadata.source)}
                              </Chip>
                            )}
                            {ref?.metadata?.rank !== undefined && (
                              <Chip variant='tag' size='xs'>
                                {`rank ${ref.metadata.rank}`}
                              </Chip>
                            )}
                            {scoreLabel && (
                              <Chip variant='tag' size='xs'>
                                {`score ${scoreLabel}`}
                              </Chip>
                            )}
                            {typeof ref?.metadata?.url === 'string' && /^https?:\/\//.test(ref.metadata.url) && (
                              // Source-document link (e.g. the Confluence
                              // runbook page a KB reference came from).
                              // stopPropagation so the click doesn't collapse
                              // the row it lives in.
                              <MuiLink
                                href={ref.metadata.url}
                                target='_blank'
                                rel='noopener noreferrer'
                                onClick={(e) => e.stopPropagation()}
                                sx={{ fontSize: 12, alignSelf: 'center', display: 'inline-flex', alignItems: 'center', gap: 0.25 }}
                              >
                                Open source page
                                <OpenInNewIcon sx={{ fontSize: 13 }} />
                              </MuiLink>
                            )}
                          </Stack>
                          <Box
                            component='pre'
                            sx={{
                              m: 0,
                              // Browser user-agent stylesheet sets
                              // `font-family: monospace` on <pre> with higher
                              // specificity than inheritance from our root
                              // container. Force inheritance with an explicit
                              // `inherit` so the content reads in the same
                              // display font as the drawer header / tabs / pills.
                              fontFamily: 'inherit',
                              fontSize: 13,
                              lineHeight: 1.5,
                              whiteSpace: 'pre-wrap',
                              wordBreak: 'break-word',
                              color: 'text.primary',
                            }}
                          >
                            {ref.content || '(no content)'}
                          </Box>
                        </Box>
                      </Collapse>
                    </TableCell>
                  </TableRow>
                </React.Fragment>
              );
            })}
            {filteredRefs.length === 0 && (
              <TableRow>
                <TableCell colSpan={3} sx={{ color: 'text.secondary', textAlign: 'center', py: 3 }}>
                  <Typography variant='body2'>No references in this filter.</Typography>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Box>

      {/* Pagination — hidden when the whole filter fits in a single page so
          we don't waste a strip of chrome on 3 rows. Uses the shared
          CustomTablePagination so the visual language matches every other
          data table in the app (Showing N-M of X · pill page picker). */}
      {filteredRefs.length > rowsPerPage && (
        <Box sx={{ flexShrink: 0, borderTop: 1, borderColor: 'divider' }}>
          <CustomTablePagination
            page={page}
            totalPages={Math.max(1, Math.ceil(filteredRefs.length / rowsPerPage))}
            totalRows={filteredRefs.length}
            rowsPerPage={rowsPerPage}
            onPageChange={(nextPage, nextRowsPerPage) => {
              setPage(nextPage);
              if (nextRowsPerPage && nextRowsPerPage !== rowsPerPage) setRowsPerPage(nextRowsPerPage);
            }}
          />
        </Box>
      )}
    </Box>
  );
};

ReferencesDrawerContent.propTypes = {
  references: PropTypes.array,
};

export default ReferencesDrawerContent;
