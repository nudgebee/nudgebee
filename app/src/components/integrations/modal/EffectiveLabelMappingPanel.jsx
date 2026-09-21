/**
 * EffectiveLabelMappingPanel — read-only answer to "which provider field does each
 * canonical log field actually resolve to for this account, and which layer decided
 * that?".
 *
 * The mapping is merged from five layers that live in five different places (a Go
 * constant, Tenant Settings, the account tile, a provider's own column fields, and
 * this form), and until now nothing rendered the result — so configuring a provider
 * meant guessing whether a filter on `pod` would reach the backend as `pod`, as
 * `kubernetes.pod_name.keyword`, or as something that matches nothing.
 *
 * The merge itself is NOT reimplemented here. The server resolves it through the same
 * function every log query goes through and returns the per-field provenance, so this
 * panel cannot report a mapping queries do not use. Rows the operator has typed but
 * not saved are sent along as `draft_mappings` and resolved server-side for the same
 * reason.
 */
import React, { useCallback, useEffect, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Label } from '@ui/Label';
import { Banner } from '@ui/Banner';
import { Skeleton } from '@ui/Skeleton';
import { ds } from '@utils/colors';
import observability from '@api1/observability';

// Tier -> how it reads to an operator. `integration` deliberately says "This
// integration" rather than naming the tier: on this form, that is what it means.
const TIER_LABEL = {
  integration: 'This integration',
  provider_config: 'Provider settings',
  account: 'Account',
  tenant: 'Tenant',
  provider_default: 'Provider default',
};

const TIER_TONE = {
  integration: 'info',
  provider_config: 'success',
  account: 'neutral',
  tenant: 'neutral',
  provider_default: 'neutral',
};

const monoSx = { fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-caption)' };

const cellSx = {
  padding: `${ds.space[2]} ${ds.space[3]}`,
  borderBottom: `1px solid ${ds.gray[100]}`,
  textAlign: 'left',
  verticalAlign: 'middle',
};

const headCellSx = {
  ...cellSx,
  fontFamily: 'var(--ds-font-display)',
  fontSize: 'var(--ds-text-caption)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  letterSpacing: '0.06em',
  textTransform: 'uppercase',
  color: ds.gray[500],
  borderBottom: `1px solid ${ds.gray[200]}`,
  whiteSpace: 'nowrap',
};

export default function EffectiveLabelMappingPanel({ accountId, provider, providerSource, draftMappings, unsaved, cardIdx }) {
  const [state, setState] = useState({ loading: false, data: null, error: '' });

  // Serialised draft, so the effect re-runs on a real content change rather than on
  // every parent render (the parent rebuilds this object each keystroke).
  const draftKey = JSON.stringify(draftMappings || {});

  // A slow response for an older draft must never overwrite a newer one — the user
  // types faster than the round trip.
  const seqRef = useRef(0);

  const load = useCallback(async () => {
    if (!accountId || !provider) {
      setState({ loading: false, data: null, error: '' });
      return;
    }
    const seq = ++seqRef.current;
    setState((prev) => ({ ...prev, loading: true, error: '' }));
    try {
      const data = await observability.getLabelMapping({
        account_id: accountId,
        provider,
        provider_source: providerSource || 'user',
        provider_type: 'logs',
        draft_mappings: JSON.parse(draftKey),
        // Always true: the form is the authority on this tier while it is open, and
        // "the operator deleted every row" has to be expressible as an empty map.
        draft_set: true,
      });
      if (seq !== seqRef.current) return;
      setState({ loading: false, data, error: '' });
    } catch (err) {
      if (seq !== seqRef.current) return;
      setState({ loading: false, data: null, error: err?.message || 'Could not resolve the effective mapping.' });
    }
  }, [accountId, provider, providerSource, draftKey]);

  useEffect(() => {
    // Debounced: typing a field name should not fire a request per keystroke. The
    // call is DB-only (no provider round trip), so 400ms is comfortable.
    const timer = setTimeout(load, 400);
    return () => clearTimeout(timer);
  }, [load]);

  if (!accountId) {
    return (
      <Box sx={{ mt: ds.space[3] }}>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[500] }}>Select an account to see the mapping it will use.</Typography>
      </Box>
    );
  }

  const fields = state.data?.fields || [];
  // "Unsaved" is about the rows in this card, not about whether the integration exists.
  // Tying it to integration_saved labelled an in-progress edit on an already-saved
  // integration as if it were live — the opposite of what the panel is for.
  const isDraftPreview = unsaved || !state.data?.integration_saved;

  return (
    <Box
      sx={{
        mt: ds.space[4],
        border: `1px solid ${ds.gray[200]}`,
        borderRadius: 'var(--ds-radius-lg)',
        backgroundColor: ds.background[100],
        overflow: 'hidden',
      }}
      data-testid={`effective-label-mapping-${cardIdx}`}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: ds.space[2],
          flexWrap: 'wrap',
          padding: `${ds.space[2]} ${ds.space[3]}`,
          borderBottom: `1px solid ${ds.gray[200]}`,
          backgroundColor: ds.background[200],
        }}
      >
        <Typography
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: ds.gray[700],
          }}
        >
          Mapping in effect
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>
          Resolved by the server — the same mapping log queries use
        </Typography>
      </Box>

      {state.error ? (
        <Box sx={{ padding: ds.space[3] }}>
          <Banner surface='section' tone='warning' message={state.error} />
        </Box>
      ) : null}

      {state.loading && !state.data ? (
        <Box sx={{ padding: ds.space[3], display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          <Skeleton height={18} />
          <Skeleton height={18} />
          <Skeleton height={18} />
        </Box>
      ) : null}

      {!state.error && state.data ? (
        <>
          {isDraftPreview ? (
            <Box sx={{ padding: ds.space[3], paddingBottom: 0 }}>
              <Banner
                surface='section'
                tone='info'
                message={
                  unsaved
                    ? 'Includes changes you have not saved yet. Rows marked “This integration (unsaved)” take effect once you save.'
                    : 'This integration is not saved yet. Rows marked “This integration (unsaved)” take effect once you save.'
                }
              />
            </Box>
          ) : null}
          <Box sx={{ overflowX: 'auto' }}>
            <Box component='table' sx={{ width: '100%', borderCollapse: 'collapse', fontSize: 'var(--ds-text-small)' }}>
              <thead>
                <tr>
                  <Box component='th' scope='col' sx={headCellSx}>
                    Concept
                  </Box>
                  <Box component='th' scope='col' sx={headCellSx}>
                    Resolves to
                  </Box>
                  <Box component='th' scope='col' sx={headCellSx}>
                    From
                  </Box>
                </tr>
              </thead>
              <tbody>
                {fields.map((field) => {
                  const overridden = Object.entries(field.contributions || {}).filter(([tier]) => tier !== field.winning_tier);
                  return (
                    <tr key={field.canonical} data-testid={`effective-label-row-${field.canonical}`}>
                      <Box component='td' sx={{ ...cellSx, ...monoSx, whiteSpace: 'nowrap', color: ds.gray[700] }}>
                        {field.canonical}
                      </Box>
                      <Box component='td' sx={cellSx}>
                        {field.effective ? (
                          <>
                            <Box component='span' sx={{ ...monoSx, color: ds.gray[700] }}>
                              {field.effective}
                            </Box>
                            {overridden.map(([tier, value]) => (
                              <Box
                                key={tier}
                                component='span'
                                title={`Overridden ${TIER_LABEL[tier] || tier} value`}
                                sx={{ ...monoSx, ml: ds.space[2], color: ds.gray[400], textDecoration: 'line-through' }}
                              >
                                {value}
                              </Box>
                            ))}
                          </>
                        ) : (
                          // Unmapped is the interesting case, not an omission: the
                          // canonical name reaches the backend verbatim and usually
                          // matches nothing, with no error.
                          <Box component='span' sx={{ color: ds.gray[500], fontStyle: 'italic' }}>
                            not mapped — sent as{' '}
                            <Box component='span' sx={{ ...monoSx, fontStyle: 'normal' }}>
                              {field.canonical}
                            </Box>
                          </Box>
                        )}
                      </Box>
                      <Box component='td' sx={{ ...cellSx, whiteSpace: 'nowrap' }}>
                        <Label
                          size='sm'
                          tone={TIER_TONE[field.winning_tier] || 'neutral'}
                          text={
                            field.winning_tier
                              ? `${TIER_LABEL[field.winning_tier] || field.winning_tier}${
                                  field.winning_tier === 'integration' && isDraftPreview ? ' (unsaved)' : ''
                                }`
                              : 'Provider default'
                          }
                        />
                      </Box>
                    </tr>
                  );
                })}
              </tbody>
            </Box>
          </Box>
        </>
      ) : null}
    </Box>
  );
}

EffectiveLabelMappingPanel.propTypes = {
  accountId: PropTypes.string,
  provider: PropTypes.string,
  providerSource: PropTypes.string,
  // Canonical -> provider field, as currently typed in the card (may be unsaved).
  draftMappings: PropTypes.object,
  // True when those rows differ from what is stored on the integration.
  unsaved: PropTypes.bool,
  cardIdx: PropTypes.number,
};
