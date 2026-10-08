import React from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { ds } from '@utils/colors';

// Renders what an LLM integration change does to each account, from the report
// of integrations_check_llm_defaults. Used by the LLM modal's save prompts and by
// the integrations list before a delete, disable or enable.

const targetLabel = (impact) => {
  if (impact.after_source === 'platform') {
    return 'the LLM provided by Nudgebee';
  }
  if (impact.after_source === 'none') {
    return 'no LLM';
  }
  return impact.after?.name || 'this config';
};

// Groups the accounts that change by where they end up.
const movesByTarget = (affected) => {
  const groups = new Map();
  (affected || []).forEach((a) => {
    const label = targetLabel(a);
    groups.set(label, [...(groups.get(label) || []), a]);
  });
  return [...groups.entries()];
};

// needsLLMDefaultsConfirm reports whether a save the user did not block needs a
// confirmation: it replaces a default, makes a config organisation-wide, or moves
// accounts to the platform LLM.
export const needsLLMDefaultsConfirm = (report) =>
  !!report &&
  (!!report.replaces_tenant_default ||
    (report.replaced_account_defaults || []).length > 0 ||
    !!report.becomes_tenant_wide ||
    (!!report.env_configured && (report.affected || []).some((a) => a.after_source === 'platform')));

const Line = ({ children, testId }) => (
  <Typography data-testid={testId} sx={{ fontSize: ds.text.body, color: ds.gray[700], lineHeight: 1.5 }}>
    {children}
  </Typography>
);

Line.propTypes = { children: PropTypes.node, testId: PropTypes.string };

const MAX_CHIPS = 10;

const AccountChips = ({ accounts }) => (
  <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
    {accounts.slice(0, MAX_CHIPS).map((a) => (
      <Chip key={a.account_id} variant='tag' size='sm' tone='neutral'>
        {a.account_name || a.account_id}
      </Chip>
    ))}
    {accounts.length > MAX_CHIPS && <Chip variant='tag' size='sm' tone='subtle'>{`+${accounts.length - MAX_CHIPS} more`}</Chip>}
  </Box>
);

AccountChips.propTypes = { accounts: PropTypes.array.isRequired };

const Section = ({ title, testId, children }) => (
  <Box data-testid={testId} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
    <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[800] }}>{title}</Typography>
    {children}
  </Box>
);

Section.propTypes = { title: PropTypes.string.isRequired, testId: PropTypes.string, children: PropTypes.node };

const LLMDefaultsSummary = ({ report, action }) => {
  if (!report) {
    return null;
  }
  const blocked = report.blocked || [];
  const replaced = report.replaced_account_defaults || [];
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
      {blocked.length > 0 && (
        <Box data-testid='llm-defaults-blocked' sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
          <Banner
            tone='critical'
            surface='section'
            title={action ? `Can't ${action} this config yet` : "This change can't be made yet"}
            message="These accounts would be left without an LLM. There's no organisation default or LLM provided by Nudgebee to fall back on."
          />
          <Section title='Accounts affected'>
            <AccountChips accounts={blocked} />
          </Section>
          <Section title='To continue, first do one of these:'>
            <Box component='ul' sx={{ m: 0, pl: ds.space[5], color: ds.gray[700], fontSize: ds.text.body, lineHeight: 1.6 }}>
              <li>Make another config the organisation default</li>
              <li>Give these accounts their own default</li>
            </Box>
          </Section>
        </Box>
      )}
      {blocked.length === 0 && report.replaces_tenant_default && (
        <Box data-testid='llm-defaults-replaces-tenant'>
          <Banner
            tone='info'
            surface='section'
            title='New organisation default'
            message={`This replaces ${report.replaces_tenant_default.name} as the organisation default.`}
          />
        </Box>
      )}
      {blocked.length === 0 && report.becomes_tenant_wide && (
        <Line testId='llm-defaults-tenant-wide'>No accounts are selected, so every account in the organisation can use this config.</Line>
      )}
      {blocked.length === 0 && replaced.length > 0 && (
        <Section title='Account defaults replaced' testId='llm-defaults-replaces-account'>
          {replaced.map((r) => (
            <Box key={r.account_id} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
              <Chip variant='tag' size='sm' tone='neutral'>
                {r.account_name || r.account_id}
              </Chip>
              <Line>{`${r.before?.name || 'current default'} → ${r.after?.name || 'this config'}`}</Line>
            </Box>
          ))}
        </Section>
      )}
      {blocked.length === 0 &&
        movesByTarget(report.affected).map(([label, accounts]) => (
          <Section key={label} title={`Will use ${label}`} testId='llm-defaults-moves'>
            <AccountChips accounts={accounts} />
          </Section>
        ))}
    </Box>
  );
};

LLMDefaultsSummary.propTypes = {
  // Verb for the blocked title: "Can't <action> this config yet".
  action: PropTypes.string,
  report: PropTypes.shape({
    env_configured: PropTypes.bool,
    tenant_default: PropTypes.object,
    replaces_tenant_default: PropTypes.object,
    becomes_tenant_wide: PropTypes.bool,
    affected: PropTypes.array,
    replaced_account_defaults: PropTypes.array,
    blocked: PropTypes.array,
  }),
};

export default LLMDefaultsSummary;
