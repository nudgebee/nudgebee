import React from 'react';
import { Box } from '@mui/material';
import { Label } from '@ui/Label';
import Tooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import { PlatformChannelBadge } from '@shared/icons/IconTextBadge';
import { ds } from '@utils/colors';
import { RULE_TYPE, RULE_TYPES, RuleTypeKey } from './catalogue';
import { Rule, RuleMapping, eventTypeOf, formatWhen, isEnded, isPaused, mappingLabel, ruleStatus, whatText, whoText } from './model';
import { Fallback, Fallbacks, effectiveFallback } from './fallback';
import { platformPreset } from './destinations';

const verb = { fontWeight: ds.weight.semibold, color: ds.gray[700] };

export const ChannelBadges: React.FC<{ mappings: RuleMapping[] }> = ({ mappings }) => (
  <>
    {mappings.map((m, i) => (
      <React.Fragment key={`${m.platform}-${i}`}>
        <Box component='span' sx={{ display: 'inline-flex', verticalAlign: 'middle' }}>
          <PlatformChannelBadge platform={platformPreset(m.platform)} channelName={mappingLabel(m)} size='small' />
        </Box>
        {i < mappings.length - 2 ? ', ' : i === mappings.length - 2 ? ' and ' : ''}
      </React.Fragment>
    ))}
  </>
);

// The rule as one sentence: what, from where, and what happens to it.
export const RuleSentence: React.FC<{ rule: Rule; accountName: (id: string) => string }> = ({ rule, accountName }) => {
  const head = `${whatText(rule)} from ${whoText(rule, accountName)} `;
  const now = Date.now();
  if (rule.action === 'send') {
    const channels = rule.mappings.length ? (
      <>
        <Box component='span' sx={verb}>
          go to{' '}
        </Box>
        <ChannelBadges mappings={rule.mappings} />
      </>
    ) : (
      <Box component='span' sx={verb}>
        go to each app’s default channel
      </Box>
    );
    if (isPaused(rule, now)) {
      return (
        <span>
          {head}
          <Box component='span' sx={verb}>
            are not sent until {formatWhen(rule.until!)}
          </Box>
          , then {channels}
          {rule.batch ? ', as one message per hour' : ''}.
        </span>
      );
    }
    return (
      <span>
        {head}
        {channels}
        {rule.batch ? ', as one message per hour' : ''}.
      </span>
    );
  }
  if (isPaused(rule, now)) {
    return (
      <span>
        {head}
        <Box component='span' sx={verb}>
          are not sent until {formatWhen(rule.until!)}
        </Box>
        , then they go out as usual.
      </span>
    );
  }
  if (isEnded(rule, now)) {
    return (
      <span>
        {head}
        <Box component='span' sx={verb}>
          were paused until {formatWhen(rule.until!)}.
        </Box>
      </span>
    );
  }
  return (
    <span>
      {head}
      <Box component='span' sx={verb}>
        are never sent
      </Box>
      , even if another rule would send them.
    </span>
  );
};

export const RuleStatusLabel: React.FC<{ rule: Rule }> = ({ rule }) => {
  const status = ruleStatus(rule);
  const label = <Label text={status.text} tone={status.tone} size='sm' textTransform='none' />;
  return status.title ? (
    <Tooltip title={status.title}>
      <span>{label}</span>
    </Tooltip>
  ) : (
    label
  );
};

export const TypeChip: React.FC<{ type: RuleTypeKey; extra?: string }> = ({ type, extra }) => (
  <Box
    component='span'
    sx={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: ds.space[1],
      padding: `${ds.space[0]} ${ds.space[2]}`,
      borderRadius: ds.radius.pill,
      border: `1px solid ${ds.gray[200]}`,
      fontSize: ds.text.small,
      color: ds.gray[700],
      whiteSpace: 'nowrap',
    }}
  >
    <SafeIcon src={RULE_TYPE[type].icon} alt='' width={12} height={12} />
    {RULE_TYPE[type].label}
    {extra ? ` · ${extra}` : ''}
  </Box>
);

export const CoversCell: React.FC<{ rule: Rule }> = ({ rule }) => {
  if (rule.types === 'all') return <Label text='All types' tone='info' size='sm' textTransform='none' />;
  const ek = eventTypeOf(rule.types);
  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
      {rule.types.map((t) => {
        const total = RULE_TYPE[t].events?.length || 0;
        const extra = ek === t && rule.events && rule.events.length < total ? `${rule.events.length} of ${total}` : undefined;
        return <TypeChip key={t} type={t} extra={extra} />;
      })}
    </Box>
  );
};

const fallbackPart = (subject: string, f: Fallback, instead: boolean, key: string) => (
  <React.Fragment key={key}>
    {subject}{' '}
    {f.mode === 'drop' ? (
      <Box component='span' sx={verb}>
        are not sent{instead ? ' instead' : ''}
      </Box>
    ) : f.mode === 'channels' ? (
      <>
        <Box component='span' sx={verb}>
          go to{' '}
        </Box>
        <ChannelBadges mappings={f.mappings} />
        {instead ? ' instead' : ''}
      </>
    ) : (
      <Box component='span' sx={verb}>
        go to each app’s default channel{instead ? ' instead' : ''}
      </Box>
    )}
    .{' '}
  </React.Fragment>
);

const sub = { color: ds.gray[600], fontSize: ds.text.small };

// What "When no rule matches" does, for every type or for one.
export const FallbackSentence: React.FC<{ fallbacks: Fallbacks; type: RuleTypeKey | null }> = ({ fallbacks, type }) => {
  if (type === 'daily_recap') {
    return <span>Without a rule, the daily email goes to your users; this setting doesn’t apply to it.</span>;
  }
  if (type === 'llm_budget') {
    return <span>Without a rule, budget alerts go to no channel; the key owner still gets the email.</span>;
  }
  if (type) {
    const f = effectiveFallback(fallbacks, type);
    const noun = RULE_TYPE[type].noun;
    return (
      <span>
        {fallbackPart(`${noun.charAt(0).toUpperCase()}${noun.slice(1)}`, f, false, type)}
        {f.inherited && (
          <Box component='span' sx={sub}>
            (same as all types){' '}
          </Box>
        )}
        {type === 'automation' && (
          <Box component='span' sx={sub}>
            Approval requests always go out.
          </Box>
        )}
      </span>
    );
  }
  return (
    <span>
      {fallbackPart('Notifications', fallbacks.all, false, 'all')}
      {RULE_TYPES.filter((t) => fallbacks[t.key] && fallbacks[t.key].mode !== 'inherit').map((t) =>
        fallbackPart(`${t.noun.charAt(0).toUpperCase()}${t.noun.slice(1)}`, fallbacks[t.key], true, t.key)
      )}
      <Box component='span' sx={sub}>
        Approval requests always go out.
      </Box>
    </span>
  );
};
