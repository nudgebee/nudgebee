/**
 * AccountsTabDisabled — placeholder rendered in the Accounts tab when the
 * tenant does not have AI_COST_REPORT enabled.
 *
 * The tab itself is now always visible to tenant-wide-role users (see
 * CostAnalyser.tsx) rather than disappearing entirely, so an admin can
 * discover the feature and learn how to turn it on instead of never knowing
 * it exists. Only ever rendered for isTenantWideRole() === true — that's
 * the same audience CostAnalyser gates the real Accounts tab behind once the
 * flag is on, so a plain member never sees this either.
 *
 * Also reports whether the tenant already has a Slack default channel mapped
 * — the digest (notifications-server's message.py) only ever posts to that
 * one channel (ai_cost_daily_report has no ms_teams/google_chat/discord
 * template, and its "ai_cost" source matches no notification rule category,
 * so it always falls through to the installation's default). Reading real
 * state here means an admin who already mapped a channel sees "you're set"
 * instead of a generic warning that doesn't match their tenant.
 */
import * as React from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import ScheduleOutlinedIcon from '@mui/icons-material/ScheduleOutlined';
import TagOutlinedIcon from '@mui/icons-material/TagOutlined';
import { EmptyState } from '@ui/EmptyState';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { ds } from '@utils/colors';
import { canEditTenantSettings } from '@lib/auth';
import apiAccount from '@api1/account';
import { safeJSONParse } from 'src/utils/common';

// justifyContent: 'center' — a flex row positions its children by flex alignment, not
// text-align, so without this the icon+label group would sit flush left instead of
// picking up the centered look the rest of the card inherits from EmptyState's
// textAlign: 'center' container.
const HEADING_ROW_SX = { display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--ds-space-2)' } as const;
const BODY_SX = { fontSize: 'var(--ds-text-body)', lineHeight: 'var(--ds-text-body-lh)', color: 'text.secondary' } as const;

function InfoRow({ icon, label, children }: { icon: React.ReactNode; label: string; children: React.ReactNode }) {
  return (
    <Box sx={{ minWidth: 0 }}>
      <Box sx={HEADING_ROW_SX}>
        <Box sx={{ color: ds.gray[400], display: 'flex' }}>{icon}</Box>
        <Typography variant='caption' sx={{ fontWeight: 600, color: 'text.primary' }}>
          {label}
        </Typography>
      </Box>
      {/* Box, not Typography: the "Where it goes" row nests its own Typography/Chip
          (WhereItGoes) for the live channel-status line, and Typography-in-Typography
          renders a <p> inside a <p> — invalid HTML and a React DOM-nesting warning. */}
      <Box sx={{ ...BODY_SX, mt: '4px' }}>{children}</Box>
    </Box>
  );
}

type ChannelStatus = { state: 'loading' } | { state: 'not_installed' } | { state: 'no_channel' } | { state: 'set'; channelName: string };

/** Mirrors MessagingIntegrationTile.jsx's own read of installationData[0].channels — the same field it writes to via "Map Channel". */
function readDefaultChannel(installationData: any[]): ChannelStatus {
  if (!installationData || installationData.length === 0) return { state: 'not_installed' };
  if (installationData.length !== 1) return { state: 'no_channel' };
  const channels = safeJSONParse(installationData[0].channels);
  const name = channels?.name;
  return name ? { state: 'set', channelName: name } : { state: 'no_channel' };
}

function useSlackDefaultChannel(): ChannelStatus {
  const [status, setStatus] = React.useState<ChannelStatus>({ state: 'loading' });
  React.useEffect(() => {
    let cancelled = false;
    apiAccount
      .getMessagingInstallations('slack')
      .then((res: any) => {
        if (!cancelled) setStatus(readDefaultChannel(res?.data ?? []));
      })
      .catch(() => {
        if (!cancelled) setStatus({ state: 'not_installed' });
      });
    return () => {
      cancelled = true;
    };
  }, []);
  return status;
}

function WhereItGoes({ status }: { status: ChannelStatus }) {
  if (status.state === 'loading') {
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--ds-space-2)' }}>
        <CircularProgress size={12} />
        <Typography variant='body2' sx={BODY_SX}>
          Checking your Slack default channel…
        </Typography>
      </Box>
    );
  }
  const intro =
    'Posts to your tenant’s single default Slack channel — this digest isn’t scoped by cost category or account yet, so every account’s numbers land in that one channel.';
  if (status.state === 'set') {
    return (
      <Typography variant='body2' sx={BODY_SX}>
        {intro} Already mapped:{' '}
        <Chip size='2xs' variant='tag' tone='success'>
          #{status.channelName}
        </Chip>{' '}
        — nothing else to configure here.
      </Typography>
    );
  }
  if (status.state === 'not_installed') {
    return (
      <Typography variant='body2' sx={BODY_SX}>
        {intro} Slack isn’t connected for this tenant yet, so the digest has nowhere to post. Connect it and map a default channel under Accounts →
        Integrations.
      </Typography>
    );
  }
  return (
    <Typography variant='body2' sx={BODY_SX}>
      {intro} No default channel is mapped yet, so the digest has nowhere to post. Set one under Accounts → Integrations → Slack → Map Channel.
    </Typography>
  );
}

export function AccountsTabDisabled() {
  const canEnable = canEditTenantSettings();
  const channelStatus = useSlackDefaultChannel();

  return (
    <EmptyState
      surface
      size='section'
      illustration='no-permissions'
      title='AI Cost Report isn’t enabled for your tenant'
      description='Turn it on to see this consolidated per-account cost report, plus a daily summary posted to Slack.'
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', maxWidth: 560, mt: 'var(--ds-space-4)' }}>
        <Card variant='outlined' size='sm' elevation='flat'>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
            <InfoRow icon={<ScheduleOutlinedIcon sx={{ fontSize: 18 }} />} label='Delivery schedule'>
              Posted daily at 6:00 AM UTC (~11:30 AM IST), once the previous day is fully complete.
            </InfoRow>
            <InfoRow icon={<TagOutlinedIcon sx={{ fontSize: 18 }} />} label='Where it goes'>
              <WhereItGoes status={channelStatus} />
            </InfoRow>
          </Box>
        </Card>

        {canEnable ? (
          <Banner
            surface='section'
            tone='info'
            title='How to enable it'
            message='Open your profile menu → Tenant Settings → Features, enable “AI/LLM cost report”, then Save. Make sure a default Slack channel is mapped under Accounts → Integrations so the daily digest has somewhere to post.'
          />
        ) : (
          <Banner
            surface='section'
            tone='info'
            title='Ask a tenant admin'
            message='Only a tenant admin can enable this — ask one to turn on “AI/LLM cost report” in Tenant Settings → Features.'
          />
        )}
      </Box>
    </EmptyState>
  );
}

export default AccountsTabDisabled;
