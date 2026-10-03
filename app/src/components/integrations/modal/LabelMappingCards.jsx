/**
 * LabelMappingCards — per-account editor for an integration's canonical → provider
 * field mapping, rendered under Advanced Settings.
 *
 * One component serves BOTH signals, the way DefaultFiltersCard serves both filter
 * lists: the log and trace mappings differ only in vocabulary, storage key and where
 * the field suggestions come from, so those are props. Defaults reproduce the log
 * mapper exactly.
 *
 * Why per-account rather than one map for the integration: one Elasticsearch cluster
 * (or Loki, or Splunk) routinely serves several cloud accounts whose shippers spell
 * the same concept differently, and this tier outranks the account-level mapping — so
 * a single integration-wide map would make those accounts unable to differ at all.
 * Same shape, and the same storage contract, as the sibling Default Log Filters and
 * Per-Account Index blobs.
 *
 * Storage: one JSON string under `log_label_mappings` / `trace_label_mappings`, shaped
 * `[{ accountId, mappings: { canonical: providerField } }]`. Two keys, not one: several
 * integrations (datadog, dynatrace, chronosphere, ES) serve both signals from a single
 * record, so sharing a key would apply a log mapping to trace queries.
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
import { getBrandTitle } from '@hooks/useTenantBranding';
import EffectiveLabelMappingPanel from './EffectiveLabelMappingPanel';
import { fieldOptionsKey, indexForAccount } from './useLogFieldOptions';

// The concepts the log pipeline actually builds filters from, with the name each one
// reads by. Mirrors knownCanonicalLogFields in observability/log_labels.go — update the
// two together.
//
// Ordered most-retuned first rather than alphabetically: pod / namespace / app are the
// three the tenant and account Settings mappers expose, so they are the ones an operator
// comes here to change. The server returns rows alphabetically; the panel reorders.
//
// Suggestions only — every input is freeSolo, because a deployment can legitimately need
// a concept we have not enumerated, and blocking that would send people back to editing
// DB rows. A provider key with no entry here (datadog logs publish `node` and `service`)
// still renders, under its raw name, after the named concepts.
export const CANONICAL_LOG_CONCEPTS = [
  { field: 'pod', label: 'Pod' },
  { field: 'namespace', label: 'Namespace' },
  { field: 'app', label: 'App' },
  { field: 'container', label: 'Container' },
  { field: 'message', label: 'Message' },
  { field: 'content', label: 'Log content' },
  { field: 'level', label: 'Level' },
  { field: 'timestamp', label: 'Timestamp' },
  { field: 'trace_id', label: 'Trace ID' },
];

// The Concept dropdown's options. Sorted, because that list is a picker and alphabetical
// is what someone scans; the PANEL uses the declared order above.
export const CANONICAL_LOG_FIELDS = CANONICAL_LOG_CONCEPTS.map(({ field }) => field).sort();

// canonical -> human name, and the panel's display order. Same shape the trace instance
// passes, so one component serves both.
export const LOG_CONCEPT_LABELS = Object.fromEntries(CANONICAL_LOG_CONCEPTS.map(({ field, label }) => [field, label]));
export const LOG_CONCEPT_ORDER = CANONICAL_LOG_CONCEPTS.map(({ field }) => field);

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

export default function LabelMappingCards({
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
  title = 'Log Label Mapping (Optional)',
  helpText,
  lockedText = "Run Test Connection to load the backend's fields and configure the mapping.",
  canonicalOptions = CANONICAL_LOG_FIELDS,
  canonicalPlaceholder = 'e.g. pod',
  fieldPlaceholder = 'e.g. kubernetes.pod_name.keyword',
  fieldOptionsForCard,
  testIdPrefix = 'log-label-mapping',
  providerType = 'logs',
  signalNoun = 'log',
  // Default to the log vocabulary: the log instance is the one that passes neither, and
  // it is the same component the trace instance overrides these on.
  conceptLabels = LOG_CONCEPT_LABELS,
  conceptOrder = LOG_CONCEPT_ORDER,
  sx,
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
    <Box data-testid={`${testIdPrefix}-section`} sx={sx}>
      <Typography sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', mb: ds.space[1] }}>
        {title}
      </Typography>
      <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', mb: ds.space[4], pl: ds.space[1] }}>
        {helpText || (
          <>
            {`Tell ${getBrandTitle()} which field in this backend holds each concept (e.g. `}
            <em>pod → kubernetes.pod_name.keyword</em>
            {`). What you set here wins over the account and tenant mappings. Leave a concept out and it falls through to those.`}
          </>
        )}
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
        <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', pl: ds.space[1], mb: ds.space[3] }}>{lockedText}</Typography>
      ) : (
        <>
          {cards.map((card, cardIdx) => {
            const cardNeedsAccount = !card.accountId && card.rows.some((r) => (r.canonical || '').trim() || (r.field || '').trim());
            // Traces have no unsaved-config field probe (there is no traces_list_labels
            // client wrapper, and that action needs a saved integration anyway), so the
            // trace instance passes a resolver returning no suggestions and the input
            // stays free text. The panel below is what confirms a typed value took.
            const cardIndex = indexForAccount(indexRules, card.accountId, topLevelIndex);
            const accountFields = (fieldOptionsForCard ? fieldOptionsForCard(card) : fieldOptions?.[fieldOptionsKey(card.accountId, cardIndex)]) || {
              loading: false,
              options: [],
              message: '',
            };
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
                data-testid={`${testIdPrefix}-card-${cardIdx}`}
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
                      data-testid={`${testIdPrefix}-remove-card-${cardIdx}`}
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
                    data-testid={`${testIdPrefix}-row-${cardIdx}-${rowIdx}`}
                  >
                    <Box sx={{ flex: '1 1 40%', minWidth: 140 }}>
                      <FilterDropdown
                        label={rowIdx === 0 ? 'Concept' : undefined}
                        freeSolo
                        options={canonicalOptions}
                        value={row.canonical}
                        onSelect={(_e, v) => updateRow(cardIdx, rowIdx, { canonical: v?.value ?? v ?? '' })}
                        placeholder={canonicalPlaceholder}
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
                        placeholder={fieldPlaceholder}
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
                      data-testid={`${testIdPrefix}-remove-row-${cardIdx}-${rowIdx}`}
                    />
                  </Box>
                ))}

                <Button
                  tone='secondary'
                  size='sm'
                  icon={<AddIcon sx={{ fontSize: 16 }} />}
                  onClick={() => addRow(cardIdx)}
                  data-testid={`${testIdPrefix}-add-row-${cardIdx}`}
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
                  providerType={providerType}
                  signalNoun={signalNoun}
                  testIdPrefix={`${testIdPrefix}-effective`}
                  conceptLabels={conceptLabels}
                  conceptOrder={conceptOrder}
                />
              </Box>
            );
          })}

          <Button tone='secondary' size='md' onClick={addCard} data-testid={`${testIdPrefix}-add-card`}>
            + Add account
          </Button>
        </>
      )}
    </Box>
  );
}

LabelMappingCards.propTypes = {
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
  // Section heading, blurb and the locked-until-Test-Connection line.
  title: PropTypes.string,
  helpText: PropTypes.node,
  lockedText: PropTypes.string,
  // Canonical-field suggestions for the left-hand input, and the two placeholders.
  canonicalOptions: PropTypes.array,
  canonicalPlaceholder: PropTypes.string,
  fieldPlaceholder: PropTypes.string,
  // Overrides the (account, index) lookup into `fieldOptions` for the right-hand input.
  // Traces pass one returning no options — see the comment at its call site.
  fieldOptionsForCard: PropTypes.func,
  // Defaults to the log testids, which app-e2e-tests already binds to. A second
  // instance on the same form must pass its own prefix or the two collide.
  testIdPrefix: PropTypes.string,
  // Which resolver the effective-mapping panel asks: 'logs' | 'traces'.
  providerType: PropTypes.string,
  signalNoun: PropTypes.string,
  // Human names and display order for the effective-mapping panel's rows.
  conceptLabels: PropTypes.object,
  conceptOrder: PropTypes.array,
  sx: PropTypes.object,
};
