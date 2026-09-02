/**
 * DefaultFiltersCard — per-account "always apply" filter editor, rendered under
 * Advanced Settings on an integration form.
 *
 * One component serves both variants because they are the same editor over two
 * different config values, and the two must not drift apart:
 *
 *  - Default LOG Filters   → `default_filters`,       provider-native column names
 *  - Default TRACE Filters → `default_trace_filters`, canonical trace field names
 *
 * They are separate config values on purpose: signoz, datadog, dynatrace,
 * chronosphere and ES are each a single integration record serving both logs and
 * traces, so one shared list would apply an operator's log filters to their traces.
 *
 * The two differ only in vocabulary, which is why `fieldLabel`, `fieldOptions` and
 * the help text are props: log filters name the backend's real columns (the server
 * applies them after label mapping, verbatim), while trace filters name canonical
 * fields (the server applies them before mapping, so they survive a provider change).
 *
 * Storage for both: one JSON string shaped
 * `[{ accountId, filters: [{ key, op: '_eq', value }] }]`. Equality only.
 */
import React from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { Button } from '@ui/Button';
import FilterDropdown from '@ui/FilterDropdown';
import { ds } from '@utils/colors';
import { DeleteIconRed as NewDelete } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';

export const emptyFilterRow = () => ({ key: '', value: '' });
export const emptyFilterCard = () => ({ accountId: '', filters: [emptyFilterRow()] });

/**
 * parseDefaultFilters turns a stored blob into editable cards. An unparseable or
 * empty value degrades to a single blank card rather than throwing — a corrupt
 * config must not make the integration uneditable.
 *
 * `op` is dropped: the editor is equality-only, and the serializer writes `_eq`
 * back for every row.
 */
export function parseDefaultFilters(raw) {
  let parsed = raw;
  if (typeof raw === 'string') {
    try {
      parsed = JSON.parse(raw);
    } catch {
      return [emptyFilterCard()];
    }
  }
  if (!Array.isArray(parsed) || parsed.length === 0) return [emptyFilterCard()];

  const normalizeAcc = (a) => (typeof a === 'object' && a !== null ? a.value || '' : a || '');
  return parsed.map((e) => ({
    accountId: normalizeAcc(e?.accountId),
    filters:
      Array.isArray(e?.filters) && e.filters.length > 0
        ? e.filters.map((f) => ({ key: f?.key || '', value: String(f?.value ?? '') }))
        : [emptyFilterRow()],
  }));
}

/**
 * serializeDefaultFilters keeps a card only if it has an account and at least one
 * complete (key, value) row. Returns the array; the caller decides whether to emit
 * it — an empty array must still be written when a previously-saved config is
 * cleared, because config values are upserted per name and omitting the key would
 * leave the stale value in place.
 */
export function serializeDefaultFilters(cards) {
  return (cards || [])
    .map((c) => ({
      accountId: c?.accountId || '',
      filters: (c?.filters || [])
        .map((f) => ({ key: (f?.key || '').trim(), op: '_eq', value: (f?.value || '').trim() }))
        .filter((f) => f.key && f.value),
    }))
    .filter((c) => c.accountId && c.filters.length > 0);
}

/** True when any card has a filter typed into it but no account selected. */
export function hasCardMissingAccount(cards) {
  return (cards || []).some((c) => !c.accountId && (c.filters || []).some((f) => (f.key || '').trim() || (f.value || '').trim()));
}

const DefaultFiltersCard = ({
  title,
  helpText,
  lockedText,
  unlocked = false,
  cards,
  onCardsChange,
  accountOptions = [],
  accountOptionsLoading = false,
  renderAccountGroupIcon,
  fieldLabel = 'Column',
  fieldPlaceholder = '',
  valuePlaceholder = '',
  fieldOptionsForCard,
  validation,
  onValidateCard,
  validateLabel = 'Validate columns',
  invalidFieldError = 'Not a known field for this account.',
  testIdPrefix = 'default-filter',
  sx,
}) => {
  const updateCard = (cardIdx, updater) => onCardsChange(cards.map((c, i) => (i === cardIdx ? updater(c) : c)));

  const handleAddCard = () => onCardsChange([...cards, emptyFilterCard()]);
  const handleRemoveCard = (cardIdx) => onCardsChange(cards.filter((_, i) => i !== cardIdx));
  const handleAccountChange = (cardIdx, accountId) => updateCard(cardIdx, (c) => ({ ...c, accountId }));
  const handleAddRow = (cardIdx) => updateCard(cardIdx, (c) => ({ ...c, filters: [...c.filters, emptyFilterRow()] }));
  const handleRemoveRow = (cardIdx, rowIdx) =>
    updateCard(cardIdx, (c) => {
      const filters = c.filters.filter((_, r) => r !== rowIdx);
      // Never drop below one row — an empty card offers nothing to type into.
      return { ...c, filters: filters.length ? filters : [emptyFilterRow()] };
    });
  const handleRowChange = (cardIdx, rowIdx, field, value) =>
    updateCard(cardIdx, (c) => ({
      ...c,
      filters: c.filters.map((f, r) => (r === rowIdx ? { ...f, [field]: value } : f)),
    }));

  return (
    <Box sx={sx}>
      <Typography sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', mb: ds.space[1] }}>
        {title}
      </Typography>
      <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', mb: ds.space[4], pl: ds.space[1] }}>{helpText}</Typography>
      {/*
        Gated on a verified connection: the field names come from the backend, so
        before one there is nothing to offer and a filter on a field that does not
        exist silently matches nothing.
      */}
      {!unlocked ? (
        <Typography sx={{ color: ds.gray[400], fontSize: 'var(--ds-text-small)', pl: ds.space[1], mb: ds.space[3] }}>{lockedText}</Typography>
      ) : (
        <>
          {cards.map((card, cardIdx) => {
            const cardNeedsAccount = !card.accountId && (card.filters || []).some((f) => (f.key || '').trim() || (f.value || '').trim());
            const cardVal = validation?.[cardIdx];
            const cardFields = fieldOptionsForCard ? fieldOptionsForCard(card) : { loading: false, options: [] };
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
                      icon={<SafeIcon src={NewDelete} alt='Remove account' style={{ width: ds.space.mul(0, 7), height: ds.space.mul(0, 7) }} />}
                      aria-label='Remove account'
                      onClick={() => handleRemoveCard(cardIdx)}
                    />
                  )}
                </Box>
                <FilterDropdown
                  label='Account'
                  grouped
                  groupIcon={renderAccountGroupIcon}
                  options={accountOptions}
                  value={card.accountId}
                  onSelect={(_event, value) => handleAccountChange(cardIdx, value?.value ?? value)}
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
                    Select an account to apply these filters.
                  </Typography>
                )}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: ds.brand[500],
                    mb: ds.space[2],
                  }}
                >
                  ALWAYS APPLY
                </Typography>
                {card.filters.map((f, rowIdx) => (
                  <Box key={rowIdx} sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-end', mb: ds.space[2] }}>
                    <Box sx={{ flex: 1 }}>
                      {/* freeSolo: the suggestions are the known field names, but a
                          deployment can legitimately filter on one we did not list. */}
                      <FilterDropdown
                        label={rowIdx === 0 ? fieldLabel : undefined}
                        freeSolo
                        options={cardFields.options}
                        isOptionsLoading={cardFields.loading}
                        placeholder={fieldPlaceholder}
                        value={f.key}
                        error={cardVal?.done && cardVal.invalid.includes((f.key || '').trim()) ? invalidFieldError : undefined}
                        onSelect={(_e, v) => handleRowChange(cardIdx, rowIdx, 'key', v?.value ?? v ?? '')}
                      />
                    </Box>
                    <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.brand[500], pb: ds.space[1] }}>=</Typography>
                    <Box sx={{ flex: 1 }}>
                      <Input
                        label={rowIdx === 0 ? 'Value' : ''}
                        placeholder={valuePlaceholder}
                        value={f.value}
                        onChange={(value) => handleRowChange(cardIdx, rowIdx, 'value', value)}
                        size='sm'
                      />
                    </Box>
                    <Box sx={{ paddingBottom: ds.space[1] }}>
                      <Button
                        tone='secondary'
                        size='xs'
                        composition='icon-only'
                        icon={<SafeIcon src={NewDelete} alt='Remove' style={{ width: ds.space.mul(0, 7), height: ds.space.mul(0, 7) }} />}
                        aria-label='Remove filter'
                        disabled={card.filters.length === 1}
                        onClick={() => handleRemoveRow(cardIdx, rowIdx)}
                      />
                    </Box>
                  </Box>
                ))}
                <Box sx={{ mt: ds.space[2], display: 'flex', alignItems: 'center', gap: ds.space[3], flexWrap: 'wrap' }}>
                  <Button tone='secondary' size='sm' onClick={() => handleAddRow(cardIdx)}>
                    + Add filter
                  </Button>
                  {/* Validation is opt-in: it only makes sense where the field names are
                      the backend's own (log columns). Canonical trace fields are
                      provider-independent by design, and one the provider cannot resolve
                      is named by the server's empty-result diagnosis at query time. */}
                  {onValidateCard && (
                    <Button tone='secondary' size='sm' onClick={() => onValidateCard(cardIdx)} disabled={!card.accountId || cardVal?.loading}>
                      {cardVal?.loading ? 'Validating…' : validateLabel}
                    </Button>
                  )}
                  {cardVal?.done &&
                    (cardVal.invalid.length === 0 ? (
                      <Typography sx={{ color: 'var(--ds-green-600)', fontSize: 'var(--ds-text-caption)' }}>
                        All {fieldLabel.toLowerCase()}s valid ✓
                      </Typography>
                    ) : (
                      <Typography sx={{ color: 'var(--ds-red-600)', fontSize: 'var(--ds-text-caption)' }}>
                        {cardVal.invalid.length} unknown {fieldLabel.toLowerCase()}
                        {cardVal.invalid.length > 1 ? 's' : ''}: {cardVal.invalid.join(', ')}
                      </Typography>
                    ))}
                </Box>
              </Box>
            );
          })}
          <Button tone='secondary' size='md' onClick={handleAddCard}>
            + Add account
          </Button>
        </>
      )}
    </Box>
  );
};

DefaultFiltersCard.propTypes = {
  title: PropTypes.string.isRequired,
  helpText: PropTypes.node.isRequired,
  lockedText: PropTypes.string.isRequired,
  unlocked: PropTypes.bool,
  cards: PropTypes.array.isRequired,
  onCardsChange: PropTypes.func.isRequired,
  accountOptions: PropTypes.array,
  accountOptionsLoading: PropTypes.bool,
  renderAccountGroupIcon: PropTypes.func,
  fieldLabel: PropTypes.string,
  fieldPlaceholder: PropTypes.string,
  valuePlaceholder: PropTypes.string,
  fieldOptionsForCard: PropTypes.func,
  validation: PropTypes.object,
  onValidateCard: PropTypes.func,
  validateLabel: PropTypes.string,
  invalidFieldError: PropTypes.string,
  testIdPrefix: PropTypes.string,
  sx: PropTypes.object,
};

export default DefaultFiltersCard;
