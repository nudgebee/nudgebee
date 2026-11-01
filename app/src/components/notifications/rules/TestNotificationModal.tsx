import React, { useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Skeleton } from '@ui/Skeleton';
import apiNotifications, { NotificationRouteCheck } from '@api1/notification';
import apiKubernetes from '@api1/kubernetes';
import { ds } from '@utils/colors';
import { APPROVAL_EVENT, RULE_TYPE, RULE_TYPES, RuleTypeKey } from './catalogue';
import { RuleAccount } from './RuleEditorModal';
import { ChannelBadges } from './RuleParts';

interface Props {
  open: boolean;
  defaultType: RuleTypeKey | null;
  accounts: RuleAccount[];
  onClose: () => void;
  onShowRule: (id: string) => void;
}

// The daily email is sent by email only; this checks chat delivery.
const TYPES = RULE_TYPES.filter((t) => t.key !== 'daily_recap');

const RuleLink: React.FC<{ rule: { id: string; name?: string }; onShowRule: (id: string) => void }> = ({ rule, onShowRule }) => (
  <Button tone='link' size='sm' id={`test-rule-${rule.id}`} onClick={() => onShowRule(rule.id)}>
    {rule.name || 'a rule'}
  </Button>
);

const joinNodes = (nodes: React.ReactNode[]) =>
  nodes.map((node, i) => (
    <React.Fragment key={i}>
      {node}
      {i < nodes.length - 2 ? ', ' : i === nodes.length - 2 ? ' and ' : ''}
    </React.Fragment>
  ));

const Answer: React.FC<{ answer: NotificationRouteCheck; type: RuleTypeKey; onShowRule: (id: string) => void }> = ({ answer, type, onShowRule }) => {
  const sent = answer.result === 'sent';
  let head: React.ReactNode;
  let why: React.ReactNode;
  if (answer.result === 'sent') {
    head = answer.channels.length ? (
      <>
        Sent to <ChannelBadges mappings={answer.channels} />
        {answer.default_channels && answer.reason === 'rule' ? ', and to the default channel of apps these rules don’t name' : ''}
      </>
    ) : (
      'Sent to each app’s default channel'
    );
    if (answer.reason === 'rule') {
      why = <>By {joinNodes(answer.rules.map((r) => <RuleLink key={r.id} rule={r} onShowRule={onShowRule} />))}.</>;
    } else if (answer.reason === 'always_sent') {
      why = 'Approval requests always go out, whatever the rules say.';
    } else {
      why = 'No rule covers it, so “When no rule matches” decides.';
    }
  } else if (answer.result === 'held') {
    head = 'Held for the hourly summary';
    why = 'A rule groups these into one Slack message per hour, in Slack’s default channel.';
  } else if (answer.reason === 'severity') {
    head = 'Not sent right away';
    why = 'Only High alerts are sent as they happen. Lower ones go to the digest, unless a rule chooses their severity.';
  } else if (answer.stopped_by) {
    head = answer.result === 'paused' ? 'Not sent for now' : 'Not sent';
    why = (
      <>
        <RuleLink rule={answer.stopped_by} onShowRule={onShowRule} /> stops it.
      </>
    );
  } else {
    head = 'Not sent';
    why = 'No rule covers it, and “When no rule matches” is Don’t send.';
  }
  return (
    <Box
      id='test-answer'
      aria-live='polite'
      sx={{
        padding: ds.space[3],
        borderRadius: ds.radius.lg,
        border: `1px solid ${sent ? ds.green[300] : ds.red[300]}`,
        backgroundColor: sent ? ds.green[100] : ds.red[100],
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[1],
      }}
    >
      <Typography sx={{ fontFamily: ds.font.display, fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
        {head}
      </Typography>
      <Box sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>{why}</Box>
      {RULE_TYPE[type].rollup && (
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
          For a message covering several accounts, this is where this account’s part goes.
        </Typography>
      )}
    </Box>
  );
};

const TestNotificationModal: React.FC<Props> = ({ open, defaultType, accounts, onClose, onShowRule }) => {
  const [type, setType] = useState<RuleTypeKey>('troubleshoot');
  const [event, setEvent] = useState<string>('');
  const [account, setAccount] = useState<string>('');
  const [namespace, setNamespace] = useState<string>('');
  const [severity, setSeverity] = useState<string>('high');
  const [namespaces, setNamespaces] = useState<{ value: string; label: string }[]>([]);
  const [answer, setAnswer] = useState<NotificationRouteCheck | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const request = useRef(0);

  useEffect(() => {
    if (!open) return;
    const start = defaultType && defaultType !== 'daily_recap' ? defaultType : 'troubleshoot';
    setType(start);
    setEvent(RULE_TYPE[start].events?.[0]?.id || '');
    setAccount((prev) => prev || accounts[0]?.id || '');
  }, [open]);

  const def = RULE_TYPE[type];

  useEffect(() => {
    if (!open || !def.workloads || !account) {
      setNamespaces([]);
      return;
    }
    let active = true;
    apiKubernetes.getAllK8sNamespaces().then((res: any) => {
      if (!active) return;
      const names = new Set<string>((res?.data || []).filter((n: any) => n.cloud_account_id === account).map((n: any) => n.namespace_name));
      setNamespaces(
        Array.from(names)
          .sort()
          .map((n) => ({ value: n, label: n }))
      );
    });
    return () => {
      active = false;
    };
  }, [open, def.workloads, account]);

  useEffect(() => {
    if (!open) {
      setAnswer(null);
      return;
    }
    const current = ++request.current;
    setLoading(true);
    setError('');
    apiNotifications
      .checkRoute({
        source: type,
        event: def.events ? event || def.events[0].id : null,
        account_id: def.accounts ? account || null : null,
        namespace: def.workloads ? namespace || null : null,
        severity: def.alerts ? severity : null,
      })
      .then((res) => {
        if (current !== request.current) return;
        setLoading(false);
        setAnswer(res.answer || null);
        setError(res.error || '');
      });
  }, [open, type, event, account, namespace, severity]);

  if (!open) return null;

  const eventOptions = def.events
    ? [
        ...def.events.map((e) => ({ value: e.id, label: e.label })),
        ...(type === 'automation' ? [{ value: APPROVAL_EVENT, label: 'Approval request' }] : []),
      ]
    : [];

  return (
    <Modal open={open} handleClose={onClose} title='Test a notification' subtitle='Where would it go with today’s rules?' width='sm'>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], padding: ds.space[1] }}>
        <Select
          id='test-type'
          clearable={false}
          label='Notification'
          size='sm'
          value={type}
          options={TYPES.map((t) => ({ value: t.key, label: t.label }))}
          onChange={(value) => {
            const next = value as RuleTypeKey;
            setType(next);
            setEvent(RULE_TYPE[next].events?.[0]?.id || '');
            setNamespace('');
          }}
        />
        {def.events && (
          <Select
            id='test-event'
            clearable={false}
            label='Which one'
            size='sm'
            value={event || def.events[0].id}
            options={eventOptions}
            onChange={setEvent}
          />
        )}
        <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: ds.space[3] }}>
          {def.accounts && (
            <Select
              id='test-account'
              clearable={false}
              label='Account'
              size='sm'
              value={account || null}
              options={accounts.map((a) => ({ value: a.id, label: a.name }))}
              onChange={(value) => {
                setAccount(value);
                setNamespace('');
              }}
            />
          )}
          {def.workloads && (
            <Select
              id='test-namespace'
              label='Namespace'
              size='sm'
              placeholder='Any'
              clearable
              value={namespace || null}
              options={namespaces}
              onChange={(value) => setNamespace(value || '')}
            />
          )}
          {def.alerts && (
            <Select
              id='test-severity'
              clearable={false}
              label='Severity'
              size='sm'
              value={severity}
              options={[
                { value: 'high', label: 'High' },
                { value: 'medium', label: 'Medium' },
                { value: 'low', label: 'Low' },
              ]}
              onChange={setSeverity}
            />
          )}
        </Box>
        {error ? (
          <Typography role='alert' sx={{ fontSize: ds.text.body, color: ds.red[600] }}>
            {error}
          </Typography>
        ) : loading ? (
          <Skeleton height={72} />
        ) : answer ? (
          <Answer answer={answer} type={type} onShowRule={onShowRule} />
        ) : null}
      </Box>
    </Modal>
  );
};

export default TestNotificationModal;
