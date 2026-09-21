/**
 * LogLabelMappingCards — per-account editor for a log integration's canonical →
 * provider field mapping, rendered under Advanced Settings.
 *
 * Why per-account rather than one map for the integration: one Elasticsearch cluster
 * (or Loki, or Splunk) routinely serves several cloud accounts whose shippers spell
 * the same concept differently, and this tier outranks the account-level mapping — so
 * a single integration-wide map would make those accounts unable to differ at all.
 * Same shape, and the same storage contract, as the sibling Default Log Filters and
 * Per-Account Index blobs.
 *
 * Storage: one JSON string under `log_label_mappings`, shaped
 * `[{ accountId, mappings: { canonical: providerField } }]`.
 */
import React from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import AddIcon from '@mui/icons-material/Add';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import { Button } from '@ui/Button';
import FilterDropdown from '@ui/FilterDropdown';
import { ds } from '@utils/colors';
import EffectiveLabelMappingPanel from './EffectiveLabelMappingPanel';
import { fieldOptionsKey, indexForAccount } from './useLogFieldOptions';

// The concepts the log pipeline actually builds filters from. Suggestions only —
// every input is freeSolo, because a deployment can legitimately need a concept we
// have not enumerated, and blocking that would send people back to editing DB rows.
export const CANONICAL_LOG_FIELDS = ['app', 'container', 'content', 'level', 'message', 'namespace', 'pod', 'timestamp', 'trace_id'];

const emptyRow = () => ({ canonical: '', field: '' });
const emptyCard = () => ({ accountId: '', rows: [emptyRow()] });

/**
 * parseLogLabelMappings turns the stored blob into editable cards. Anything
 * unparseable degrades to a single blank card rather than throwing — a corrupt value
 * must not make the integration uneditable.
 */
export function parseLogLabelMappings(raw) {
  let parsed = raw;
  if (typeof raw === 'string') {
    try {
      parsed = JSON.parse(raw);
    } catch {
      return [emptyCard()];
    }
  }
  if (!Array.isArray(parsed) || parsed.length === 0) return [emptyCard()];

  const cards = parsed.map((entry) => {
    const mappings = entry?.mappings && typeof entry.mappings === 'object' ? entry.mappings : {};
    const rows = Object.keys(mappings)
      .sort()
      .map((canonical) => ({ canonical, field: mappings[canonical] ?? '' }));
    return { accountId: entry?.accountId || '', rows: rows.length ? rows : [emptyRow()] };
  });
  return cards.length ? cards : [emptyCard()];
}

/**
 * serializeLogLabelMappings turns cards back into the stored shape, dropping rows the
 * operator only half-filled and cards with no account. A row with a concept but no
 * field is an abandoned edit; persisting it would either rename a column to the empty
 * string or mask a working lower-tier mapping.
 */
export function serializeLogLabelMappings(cards) {
  return (cards || [])
    .map((card) => {
      const mappings = {};
      (card.rows || []).forEach((row) => {
        const canonical = (row.canonical || '').trim();
        const field = (row.field || '').trim();
        if (canonical && field) mappings[canonical] = field;
      });
      return { accountId: (card.accountId || '').trim(), mappings };
    })
    .filter((card) => card.accountId && Object.keys(card.mappings).length > 0);
}

// stableKey serialises a mapping order-independently. A plain JSON.stringify would
// compare insertion order too, and the saved rows arrive sorted while the edited ones
// arrive in the order they were typed — every card would look unsaved.
function stableKey(mapping) {
  return Object.keys(mapping || {})
    .sort()
    .map((k) => `${k}=${mapping[k]}`)
    .join('\u0000');
}

// savedMappingsFor returns what is stored on the integration for one account, so a card
// can tell an in-progress edit from a mapping that is already live.
function savedMappingsFor(savedCards, accountId) {
  const match = (savedCards || []).find((c) => c.accountId === accountId);
  const out = {};
  (match?.rows || []).forEach((r) => {
    const canonical = (r.canonical || '').trim();
    const field = (r.field || '').trim();
    if (canonical && field) out[canonical] = field;
  });
  return out;
}

export default function LogLabelMappingCards({
  cards,
  setCards,
  savedCards,
  accountOptions,
  accountOptionsLoading,
  grouped,
  renderAccountGroupIcon,
  provider,
  providerSource,
  connectionVerified,
  fieldOptions,
  indexRules,
  topLevelIndex,
}) {
  const updateCard = (cardIdx, patch) => setCards(cards.map((c, i) => (i === cardIdx ? { ...c, ...patch } : c)));

  const updateRow = (cardIdx, rowIdx, patch) =>
    updateCard(cardIdx, { rows: cards[cardIdx].rows.map((r, i) => (i === rowIdx ? { ...r, ...patch } : r)) });

  const addRow = (cardIdx) => updateCard(cardIdx, { rows: [...cards[cardIdx].rows, emptyRow()] });

  const removeRow = (cardIdx, rowIdx) => {
    const next = cards[cardIdx].rows.filter((_, i) => i !== rowIdx);
    updateCard(cardIdx, { rows: next.length ? next : [emptyRow()] });
  };

  const addCard = () => setCards([...cards, emptyCard()]);
  const removeCard = (cardIdx) => {
    const next = cards.filter((_, i) => i !== cardIdx);
    setCards(next.length ? next : [emptyCard()]);
  };

  return (
    <Box data-testid='log-label-mapping-section'>
      <Typography sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', mb: ds.space[1] }}>
        Log Label Mapping (Optional)
      </Typography>
      <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', mb: ds.space[4], pl: ds.space[1] }}>
        Tell Nudgebee which field in this backend holds each concept (e.g. <em>pod → kubernetes.pod_name.keyword</em>). What you set here wins over
        the account and tenant mappings. Leave a concept out and it falls through to those.
      </Typography>

      {/*
        Gated on the connection for the same reason the Per-Account Index section above
        is: without one there are no field names to choose from, so offering the editor
        invites typing them blind — and a mapping to a field that does not exist matches
        nothing and reports no error, which is the failure this editor exists to prevent.
        In the edit flow connectionVerified starts true, so an existing integration shows
        its mapping immediately and only hides it again if a connection field changes.
      */}
      {!connectionVerified ? (
        <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', pl: ds.space[1], mb: ds.space[3] }}>
          Run Test Connection to load the backend&apos;s fields and configure the mapping.
        </Typography>
      ) : (
        <>
          {cards.map((card, cardIdx) => {
            const cardNeedsAccount = !card.accountId && card.rows.some((r) => (r.canonical || '').trim() || (r.field || '').trim());
            const cardIndex = indexForAccount(indexRules, card.accountId, topLevelIndex);
            const accountFields = fieldOptions?.[fieldOptionsKey(card.accountId, cardIndex)] || { loading: false, options: [], message: '' };
            const fieldHint = accountFields.message || (accountFields.loading ? 'Loading fields…' : '');
            const draftMappings = {};
            card.rows.forEach((r) => {
              const canonical = (r.canonical || '').trim();
              const field = (r.field || '').trim();
              if (canonical && field) draftMappings[canonical] = field;
            });

            return (
              <Box
                key={cardIdx}
                sx={{
                  border: `1px solid ${ds.blue[400]}`,
                  borderRadius: 'var(--ds-radius-lg)',
                  p: 2,
                  mb: ds.space[4],
                  backgroundColor: ds.blue[100],
                }}
                data-testid={`log-label-mapping-card-${cardIdx}`}
              >
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[3] }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.blue[500] }}>
                    Account {cardIdx + 1}
                  </Typography>
                  {cards.length > 1 && (
                    <Button
                      tone='secondary'
                      size='xs'
                      composition='icon-only'
                      icon={<DeleteOutlineIcon sx={{ fontSize: 16 }} />}
                      aria-label='Remove account'
                      onClick={() => removeCard(cardIdx)}
                      data-testid={`log-label-mapping-remove-card-${cardIdx}`}
                    />
                  )}
                </Box>

                <FilterDropdown
                  label='Account'
                  grouped={grouped}
                  groupIcon={grouped ? renderAccountGroupIcon : undefined}
                  options={accountOptions}
                  value={card.accountId}
                  onSelect={(_event, value) => updateCard(cardIdx, { accountId: value?.value ?? value ?? '' })}
                  isOptionsLoading={accountOptionsLoading}
                  disabled={!accountOptions.length}
                  sx={{
                    height: ds.space.mul(0, 22),
                    mb: cardNeedsAccount ? ds.space[1] : ds.space[3],
                    ...(cardNeedsAccount ? { borderColor: 'var(--ds-red-500)', boxShadow: '0 0 0 3px var(--ds-red-100)' } : {}),
                  }}
                />
                {cardNeedsAccount && (
                  <Typography sx={{ color: 'var(--ds-red-600)', fontSize: 'var(--ds-text-caption)', mb: ds.space[3], pl: ds.space[1] }}>
                    Select an account to apply these mappings.
                  </Typography>
                )}

                {card.rows.map((row, rowIdx) => (
                  <Box
                    key={rowIdx}
                    sx={{ display: 'flex', alignItems: 'flex-end', gap: ds.space[2], mb: ds.space[2] }}
                    data-testid={`log-label-mapping-row-${cardIdx}-${rowIdx}`}
                  >
                    <Box sx={{ flex: '1 1 40%', minWidth: 140 }}>
                      <FilterDropdown
                        label={rowIdx === 0 ? 'Concept' : undefined}
                        freeSolo
                        options={CANONICAL_LOG_FIELDS}
                        value={row.canonical}
                        onSelect={(_e, v) => updateRow(cardIdx, rowIdx, { canonical: v?.value ?? v ?? '' })}
                        placeholder='e.g. pod'
                      />
                    </Box>
                    <ArrowForwardIcon sx={{ fontSize: 16, color: ds.gray[400], mb: ds.space[2] }} />
                    <Box sx={{ flex: '1 1 55%', minWidth: 180 }}>
                      <FilterDropdown
                        label={rowIdx === 0 ? `Field in ${provider || 'this backend'}` : undefined}
                        freeSolo
                        options={accountFields.options}
                        value={row.field}
                        isOptionsLoading={accountFields.loading}
                        onSelect={(_e, v) => updateRow(cardIdx, rowIdx, { field: v?.value ?? v ?? '' })}
                        placeholder='e.g. kubernetes.pod_name.keyword'
                      />
                      {rowIdx === card.rows.length - 1 && fieldHint && (
                        <Typography sx={{ color: ds.gray[500], fontSize: 'var(--ds-text-caption)', mt: ds.space[1], pl: ds.space[1] }}>
                          {fieldHint}
                        </Typography>
                      )}
                    </Box>
                    <Button
                      tone='secondary'
                      size='xs'
                      composition='icon-only'
                      icon={<DeleteOutlineIcon sx={{ fontSize: 16 }} />}
                      aria-label='Remove mapping'
                      disabled={card.rows.length === 1 && !row.canonical && !row.field}
                      onClick={() => removeRow(cardIdx, rowIdx)}
                      data-testid={`log-label-mapping-remove-row-${cardIdx}-${rowIdx}`}
                    />
                  </Box>
                ))}

                <Button
                  tone='secondary'
                  size='sm'
                  icon={<AddIcon sx={{ fontSize: 16 }} />}
                  onClick={() => addRow(cardIdx)}
                  data-testid={`log-label-mapping-add-row-${cardIdx}`}
                >
                  Add mapping
                </Button>

                <EffectiveLabelMappingPanel
                  accountId={card.accountId}
                  provider={provider}
                  providerSource={providerSource}
                  draftMappings={draftMappings}
                  unsaved={stableKey(draftMappings) !== stableKey(savedMappingsFor(savedCards, card.accountId))}
                  cardIdx={cardIdx}
                />
              </Box>
            );
          })}

          <Button tone='secondary' size='md' onClick={addCard} data-testid='log-label-mapping-add-card'>
            + Add account
          </Button>
        </>
      )}
    </Box>
  );
}

LogLabelMappingCards.propTypes = {
  cards: PropTypes.array.isRequired,
  setCards: PropTypes.func.isRequired,
  // The cards as hydrated from the saved config, for the unsaved-vs-live comparison.
  savedCards: PropTypes.array,
  accountOptions: PropTypes.array.isRequired,
  accountOptionsLoading: PropTypes.bool,
  grouped: PropTypes.bool,
  renderAccountGroupIcon: PropTypes.func,
  provider: PropTypes.string,
  providerSource: PropTypes.string,
  // Field suggestions are probed only once Test Connection has passed.
  connectionVerified: PropTypes.bool,
  // Field suggestions keyed by (account, index), from useLogFieldOptions.
  fieldOptions: PropTypes.object,
  // Per-account index cards + the top-level Log Index, for index resolution.
  indexRules: PropTypes.array,
  topLevelIndex: PropTypes.string,
};
