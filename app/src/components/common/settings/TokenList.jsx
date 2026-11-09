import React from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import Tooltip from '@ui/Tooltip';
import CustomTable from '@shared/tables/CustomTable';
import dayjs from 'dayjs';
import { ds } from 'src/utils/colors';
import { carriesGatewayTraffic, KIND_LABELS } from './keyKinds';
import { describeGatewayPolicy } from './GatewayRestrictions';

export const formatDate = (date) => dayjs(date).format('MMM DD, YYYY HH:mm');

// 'all' takes the warm hue: it is the one kind that opens both the app API and the gateway.
export const KIND_HUES = { platform: 'blue', gateway: 'violet', all: 'amber' };

export const limitsText = (limits) =>
  (limits || []).map((l) => `${l.value} ${l.metric}/${l.period}${l.model ? ` (${l.model})` : ''}`).join(', ') || '—';

export const usageText = (usage) => (usage ? `$${(usage.cost_usd ?? 0).toFixed(2)} · ${usage.requests ?? 0} reqs` : '—');

// Keys made before #38965 have no stored suffix.
export const suffixText = (t) => (t.token_suffix ? `…${t.token_suffix}` : '—');
export const lastUsedText = (t) => (t.accessed_at ? formatDate(t.accessed_at) : '—');

// A key with no scopes has its owner's full access; otherwise list what it may call.
const AccessCell = ({ scopes }) => {
  if (!scopes?.length) return <Typography variant='body2'>Full access</Typography>;
  return (
    <Tooltip title={scopes.join(', ')}>
      <Typography variant='body2' component='span'>
        {`${scopes.length} ${scopes.length === 1 ? 'scope' : 'scopes'}`}
      </Typography>
    </Tooltip>
  );
};

// An AI Gateway key with no policy reaches every model; otherwise summarise what it may call.
const GatewayAccessCell = ({ policy }) => {
  const summary = describeGatewayPolicy(policy);
  if (!summary) return <Typography variant='body2'>All models</Typography>;
  const detail = [
    policy.models?.length && `Models: ${policy.models.join(', ')}`,
    policy.providers?.length && `Providers: ${policy.providers.join(', ')}`,
    policy.endpoints?.length && `Endpoints: ${policy.endpoints.join(', ')}`,
  ]
    .filter(Boolean)
    .join(' · ');
  return (
    <Tooltip title={detail}>
      <Typography
        variant='body2'
        component='span'
        sx={{ display: 'block', maxWidth: ds.space.mul(0, 90), overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
      >
        {summary}
      </Typography>
    </Tooltip>
  );
};

const scopesText = (t) => (t.scopes?.length ? t.scopes.join(', ') : 'Full access');
const policyText = (t) => describeGatewayPolicy(t.gateway_policy) || 'All models';

const accessColumn = (t) => {
  if (t.kind === 'gateway') return { text: policyText(t), component: <GatewayAccessCell policy={t.gateway_policy} /> };
  if (t.kind === 'all') {
    return {
      text: `${scopesText(t)} · ${policyText(t)}`,
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
          <AccessCell scopes={t.scopes} />
          <GatewayAccessCell policy={t.gateway_policy} />
        </Box>
      ),
    };
  }
  return { text: scopesText(t), component: <AccessCell scopes={t.scopes} /> };
};

// An unscoped all key cannot take a gateway policy: it could mint an unrestricted key
// (normalizeGatewayPolicy in token_policy.go).
const canEditGatewayAccess = (t) => t.kind === 'gateway' || (t.kind === 'all' && t.scopes?.length > 0);

// Every key in one table, labelled by kind. A column that applies to some kinds only shows
// '—' on the rest: limits and spend meter gateway calls only. Access means module scopes on a
// key that reaches the app API, and model / endpoint restrictions on one that reaches the
// AI Gateway — both on an all key.
const TokenList = ({ tokens, onRevoke, onEditAccess }) => {
  if (tokens.length === 0) {
    return <EmptyState title='No API tokens yet' size='section' />;
  }

  const headers = ['Name', 'Type', 'Created', 'Last used', 'Expires', 'Access', 'Limits', 'This month', ''];

  const tableData = tokens.map((t) => {
    const label = KIND_LABELS[t.kind] ?? t.kind;
    const reachesGateway = carriesGatewayTraffic(t.kind);
    return [
      {
        text: `${t.name} ${suffixText(t)}`,
        component: (
          <Box>
            <Typography variant='body2'>{t.name}</Typography>
            <Typography variant='caption' sx={{ fontFamily: ds.font.mono, color: ds.gray[500] }}>
              {suffixText(t)}
            </Typography>
          </Box>
        ),
      },
      {
        text: label,
        component: (
          <Chip size='xs' variant='tag' hue={KIND_HUES[t.kind]}>
            {label}
          </Chip>
        ),
      },
      { text: formatDate(t.created_at) },
      { text: t.last_model ? `${lastUsedText(t)} · ${t.last_model}` : lastUsedText(t) },
      { text: t.expires_at ? formatDate(t.expires_at) : 'never' },
      accessColumn(t),
      { text: reachesGateway ? limitsText(t.limits) : '—' },
      { text: reachesGateway ? usageText(t.usage) : '—' },
      {
        component: (
          <Box sx={{ display: 'flex', gap: ds.space[2], justifyContent: 'flex-end', whiteSpace: 'nowrap' }}>
            {canEditGatewayAccess(t) && onEditAccess && (
              <Button tone='secondary' size='sm' onClick={() => onEditAccess(t)}>
                Edit access
              </Button>
            )}
            <Button tone='danger' size='sm' onClick={() => onRevoke(t.id, t.name)}>
              Revoke
            </Button>
          </Box>
        ),
      },
    ];
  });

  return <CustomTable headers={headers} tableData={tableData} />;
};

export default TokenList;
