import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { Button as DsButton } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import { getCostReportSchedule, upsertCostReportSchedule } from '@api1/ai-cost';
import apiNotifications from '@api1/notification';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiAccount from '@api1/account';
import { safeJSONParse } from 'src/utils/common';

const DEFAULT_SEND_HOUR_UTC = 6;

// Option values stay UTC hours (the save contract); only the labels are
// converted to the viewer's local time, using today's date as the DST
// reference.
function buildHourOptions(): { value: string; label: string }[] {
  return Array.from({ length: 24 }, (_, utcHour) => {
    const reference = new Date();
    reference.setUTCHours(utcHour, 0, 0, 0);
    return {
      value: String(utcHour),
      label: reference.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      sortKey: reference.getHours() * 60 + reference.getMinutes(),
    };
  })
    .sort((a, b) => a.sortKey - b.sortKey)
    .map(({ value, label }) => ({ value, label }));
}

// "ai_cost" is a tenant-wide notification_rules source (rules.py
// TENANT_WIDE_SOURCES) — one rule per tenant, no account_id.
const AI_COST_RULE_SOURCE = 'ai_cost';
const AI_COST_RULE_NAME = 'AI Cost Daily Report';

// apiNotifications.insertNotificationRule/deleteNotificationRule never throw
// on a backend failure — they return the raw caught exception, or a 200
// response carrying an errors payload. actionKey is the mutation field name
// that additionally carries its own `error` string (only insert has one).
function isRpcFailure(res: any, actionKey?: string): boolean {
  return res instanceof Error || Boolean(res?.data?.errors?.length) || Boolean(actionKey && res?.data?.data?.[actionKey]?.error);
}

interface CostReportScheduleModalProps {
  open: boolean;
  onClose: () => void;
  onSaved?: () => void;
}

const CostReportScheduleModal: React.FC<CostReportScheduleModalProps> = ({ open, onClose, onSaved }) => {
  const [sendHourUtc, setSendHourUtc] = useState(String(DEFAULT_SEND_HOUR_UTC));
  const [isLoading, setIsLoading] = useState(false);
  const hourOptions = useMemo(buildHourOptions, []);

  const [slackInstalled, setSlackInstalled] = useState(false);
  const [slackInstallationId, setSlackInstallationId] = useState('');
  const [slackChannelList, setSlackChannelList] = useState<{ value: string; label: string }[]>([]);
  const [selectedSlackChannel, setSelectedSlackChannel] = useState(''); // '' = tenant's default channel
  const [existingRuleId, setExistingRuleId] = useState('');
  const [existingChannelName, setExistingChannelName] = useState('');
  const [defaultSlackChannelName, setDefaultSlackChannelName] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setIsLoading(true);

    const loadSchedule = getCostReportSchedule()
      .then((schedule) => {
        if (!cancelled) setSendHourUtc(String(schedule?.send_hour_utc ?? DEFAULT_SEND_HOUR_UTC));
      })
      .catch(() => {
        if (!cancelled) setSendHourUtc(String(DEFAULT_SEND_HOUR_UTC));
      });

    const loadSlackDestination = apiNotifications
      .getInstalledTools()
      .then((res: any) => {
        if (cancelled) return;
        const slackPlatform = (res?.messaging_platforms || []).find((p: any) => p.platform === 'slack');
        setSlackInstalled(Boolean(slackPlatform));
        setSlackInstallationId(slackPlatform?.id || '');
        if (!slackPlatform) return;
        // Each inner fetch is caught individually — a transient failure in
        // any one of them (e.g. the rules lookup) must not make the outer
        // .catch below wrongly report Slack as not installed and hide the
        // channel picker entirely.
        return Promise.all([
          apiAutoPlaybook
            .listSlackChannels('slack')
            .then((res: any) => {
              if (cancelled) return;
              const channels = res?.data || [];
              setSlackChannelList(channels.map((c: any) => ({ value: c.id, label: c.name })));
            })
            .catch((err) => console.error('Failed to list Slack channels:', err)),
          apiNotifications
            .getNotificationRules({ isAccountNull: true, source: AI_COST_RULE_SOURCE }, 1, 0)
            .then((res: any) => {
              if (cancelled) return;
              const rule = res?.data?.notifications_list_rules?.rows?.[0];
              if (!rule) return;
              setExistingRuleId(rule.id || '');
              const mappings = safeJSONParse(rule.notification_rule_mappings) || [];
              const slackMapping = mappings.find((m: any) => m.platform === 'slack');
              setSelectedSlackChannel(slackMapping?.channels?.id || '');
              setExistingChannelName(slackMapping?.channels?.name || '');
            })
            .catch((err) => console.error('Failed to get notification rules:', err)),
          apiAccount
            .getMessagingInstallations('slack')
            .then((res: any) => {
              if (cancelled) return;
              const installationData = res?.data ?? [];
              if (installationData.length === 1) {
                const defaultChannel = safeJSONParse(installationData[0].channels);
                setDefaultSlackChannelName(defaultChannel?.name || null);
              }
            })
            .catch((err) => console.error('Failed to get messaging installations:', err)),
        ]);
      })
      .catch(() => {
        if (!cancelled) setSlackInstalled(false);
      });

    Promise.all([loadSchedule, loadSlackDestination]).finally(() => {
      if (!cancelled) setIsLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, [open]);

  const handleSave = async () => {
    setIsLoading(true);
    let scheduleSaved = false;
    try {
      const saved = await upsertCostReportSchedule({ sendHourUtc: Number(sendHourUtc) });
      if (!saved) throw new Error('schedule save failed');
      scheduleSaved = true;

      if (selectedSlackChannel) {
        const channel = slackChannelList.find((c) => c.value === selectedSlackChannel);
        // Falls back to the previously-loaded name (then a placeholder) if the
        // channel isn't in the freshly-fetched list — e.g. a private channel
        // the list API omits, or a transient load failure — so saving never
        // overwrites a good stored name with undefined.
        const channelName = channel?.label || existingChannelName || 'Unknown Channel';
        const ruleRes: any = await apiNotifications.insertNotificationRule({
          ruleName: AI_COST_RULE_NAME,
          source: AI_COST_RULE_SOURCE,
          isSuppressed: false,
          mappings: [{ channels: { name: channelName, id: selectedSlackChannel }, platform: 'slack', installation_id: slackInstallationId }],
          id: existingRuleId || undefined,
        });
        // apiNotifications' RPC wrappers answer HTTP 200 with an errors
        // payload (or the raw caught exception) instead of throwing, so a
        // failure has to be checked explicitly — an unchecked await here
        // would report success even when nothing was saved.
        if (isRpcFailure(ruleRes, 'notifications_upsert_rule')) throw new Error('rule save failed');
      } else if (existingRuleId) {
        // Cleared back to "default channel" — delete the rule rather than
        // saving an empty mapping.
        const deleteRes: any = await apiNotifications.deleteNotificationRule(existingRuleId);
        if (isRpcFailure(deleteRes)) throw new Error('rule delete failed');
        setExistingRuleId('');
      }

      snackbar.success('Cost report schedule updated');
      onSaved?.();
      onClose();
    } catch (error) {
      console.error('Error updating cost report schedule:', error);
      snackbar.error(
        scheduleSaved ? 'Send hour saved, but the Slack channel update failed — please try again.' : 'Failed to update cost report schedule'
      );
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <Modal
      width='sm'
      open={open}
      handleClose={onClose}
      title='Cost report schedule'
      loader={isLoading}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
          <DsButton tone='secondary' size='md' onClick={onClose} disabled={isLoading}>
            Cancel
          </DsButton>
          <DsButton tone='primary' size='md' onClick={handleSave} loading={isLoading}>
            Save
          </DsButton>
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', padding: 'var(--ds-space-4) var(--ds-space-5)' }}>
        {slackInstalled ? (
          <Select
            id='cost-report-slack-channel'
            label='Slack channel'
            value={selectedSlackChannel}
            options={slackChannelList}
            onChange={(next) => setSelectedSlackChannel(next)}
            clearable
            disabled={isLoading}
            placeholder={defaultSlackChannelName ? `Default: #${defaultSlackChannelName}` : "Tenant's default channel"}
          />
        ) : (
          <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
            Connect Slack under Admin → Integrations to choose a specific channel — until then, reports post to the tenant's default channel.
          </Typography>
        )}

        <Select
          id='cost-report-send-hour'
          label='Send to Slack at'
          required
          value={sendHourUtc}
          options={hourOptions}
          onChange={(next) => setSendHourUtc(next)}
          disabled={isLoading}
        />
        <Typography fontSize={ds.text.caption} color={ds.gray[600]}>
          Shown in your browser's local time; the schedule itself is stored and dispatched in UTC — there's no per-tenant timezone, so a teammate in a
          different timezone sees these same options labeled differently. Changing this after today's report has already gone out takes effect
          starting tomorrow.
        </Typography>
      </Box>
    </Modal>
  );
};

export default CostReportScheduleModal;
