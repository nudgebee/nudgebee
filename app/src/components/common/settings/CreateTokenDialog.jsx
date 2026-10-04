import React from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Callout } from '@ui/Callout';
import { ds } from 'src/utils/colors';
import dayjs from 'dayjs';
import { getTokenScopeCatalog } from '@api1/roles';
import { carriesGatewayTraffic, KIND_LABELS } from './keyKinds';
import { permKey, PermissionMatrix, ScopeTabs } from './PermissionMatrix';
import { EMPTY_RESTRICTIONS, GATEWAY_ACCESS_OPTIONS, GatewayRestrictionPicker, hasRestrictions, toGatewayPolicy } from './GatewayRestrictions';

const EXPIRY_OPTIONS = [
  { label: '30 days', value: '30' },
  { label: '90 days', value: '90' },
  { label: '365 days', value: '365' },
  { label: 'Never', value: 'never' },
  { label: 'Custom…', value: 'custom' },
];

const CUSTOM_UNIT_OPTIONS = ['minutes', 'hours', 'days'];
const CUSTOM_UNIT_MS = { minutes: 60 * 1000, hours: 60 * 60 * 1000, days: 24 * 60 * 60 * 1000 };

// Empty, zero, negative or non-numeric all fail this — the caller must never build a
// timestamp (or enable Create) from an unusable custom value.
const isValidCustomDuration = (value) => {
  const n = Number(value);
  return value !== '' && Number.isFinite(n) && n > 0;
};

const METRICS = ['requests', 'tokens', 'cost'];
const PERIODS = ['minute', 'hour', 'day', 'month'];
const EMPTY_LIMIT = { metric: 'cost', period: 'month', value: '', model: '' };

// `model` is free text for now: which catalog supplies the names is an open question in
// the spec, so no dropdown is wired up here.
const LimitEditor = ({ limits, onChange }) => {
  const update = (i, patch) => onChange(limits.map((l, n) => (n === i ? { ...l, ...patch } : l)));
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
      {limits.map((l, i) => (
        <Box key={i} sx={{ display: 'flex', gap: ds.space[2], alignItems: 'flex-end' }}>
          <Select label='Metric' value={l.metric} onChange={(v) => update(i, { metric: v })} options={METRICS} />
          <Select label='Per' value={l.period} onChange={(v) => update(i, { period: v })} options={PERIODS} />
          <Input size='sm' label='Value' type='number' value={l.value} onChange={(v) => update(i, { value: v })} />
          <Input size='sm' label='Model (blank = any)' value={l.model} onChange={(v) => update(i, { model: v })} />
          <Button tone='secondary' size='sm' onClick={() => onChange(limits.filter((_, n) => n !== i))}>
            Remove
          </Button>
        </Box>
      ))}
      <Box>
        <Button tone='secondary' size='sm' onClick={() => onChange([...limits, { ...EMPTY_LIMIT }])}>
          Add limit
        </Button>
      </Box>
    </Box>
  );
};

const ACCESS_OPTIONS = [
  { value: 'full', label: 'Full access' },
  { value: 'restricted', label: 'Restricted' },
];

const SCOPE_GROUPS = [
  { scope: 'tenant', tabLabel: 'Tenant-level' },
  { scope: 'account', tabLabel: 'Account-level' },
];

// Which modules a platform key may call. Same grid and same Read/Write coupling as the
// custom-role editor, over the catalog minus what a scoped key can never reach.
const ScopePicker = ({ selected, onChange }) => {
  const [catalog, setCatalog] = React.useState(null);
  const [tab, setTab] = React.useState('account');

  React.useEffect(() => {
    let live = true;
    getTokenScopeCatalog().then((c) => live && setCatalog(c));
    return () => {
      live = false;
    };
  }, []);

  const toggle = (module, cls) => {
    const key = permKey(module, cls);
    const next = new Set(selected);
    if (next.has(key)) {
      next.delete(key);
      // Write requires Read — removing Read cascades to Write for the same module.
      if (cls === 'Read') next.delete(permKey(module, 'Write'));
    } else {
      next.add(key);
      if (cls === 'Write') next.add(permKey(module, 'Read'));
    }
    onChange(next);
  };

  const entries = catalog ?? [];
  const scopeByModule = new Map(entries.map((e) => [e.module, e.scope]));
  const counts = { tenant: 0, account: 0 };
  for (const key of selected) {
    const scope = scopeByModule.get(key.split(':')[0]);
    if (scope) counts[scope] += 1;
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }} data-testid='token-scope-picker'>
      <Callout message='AI and automation runs started by this key use your full access, not just these scopes.' />
      <ScopeTabs groups={SCOPE_GROUPS} value={tab} onChange={setTab} counts={counts} idPrefix='token-scope-tab' ariaLabel='Scope level' />
      {tab === 'account' && (
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
          Most account-level modules also need Accounts: Read to look up the account they act on.
        </Typography>
      )}
      <PermissionMatrix
        entries={entries.filter((e) => e.scope === tab)}
        selected={selected}
        toggle={toggle}
        emptyMessage={catalog === null ? 'Loading scopes…' : 'No modules in this group.'}
      />
    </Box>
  );
};

const KIND_OPTIONS = ['platform', 'gateway', 'all'].map((value) => ({ value, label: KIND_LABELS[value] }));

// What a key of each kind can call, shown under the type picker.
const KIND_HINTS = {
  platform: 'Calls the app API, with your full access or only the modules you pick.',
  gateway: 'Calls the AI Gateway only. Can carry its own request, token and cost limits.',
  all: 'Opens both the app API and the AI Gateway. Limits below meter AI Gateway calls only — app API calls are not metered and pass freely. To restrict models or endpoints, pick AI Gateway.',
};

// The type picked first decides the fields that follow: a platform key carries optional
// scopes, a gateway-reaching key carries limits. The server 400s on limits it cannot meter,
// so unusable rows are dropped here rather than sent to be rejected. `defaultKind` has a
// default of its own because the server mints an all-access key when `kind` is omitted.
const CreateTokenDialog = ({ open, defaultKind = 'platform', onClose, onCreate }) => {
  const [kind, setKind] = React.useState(defaultKind);
  const [name, setName] = React.useState('');
  const [expiryDays, setExpiryDays] = React.useState('90');
  const [customValue, setCustomValue] = React.useState('');
  const [customUnit, setCustomUnit] = React.useState('hours');
  const [limits, setLimits] = React.useState([]);
  const [access, setAccess] = React.useState('full');
  const [scopes, setScopes] = React.useState(() => new Set());
  // Held apart from the platform `access` toggle so switching type never carries a
  // restriction from one kind of key to the other.
  const [gatewayAccess, setGatewayAccess] = React.useState('full');
  const [restrictions, setRestrictions] = React.useState(EMPTY_RESTRICTIONS);
  const [submitting, setSubmitting] = React.useState(false);

  const reset = () => {
    setKind(defaultKind);
    setName('');
    setExpiryDays('90');
    setCustomValue('');
    setCustomUnit('hours');
    setLimits([]);
    setAccess('full');
    setScopes(new Set());
    setGatewayAccess('full');
    setRestrictions(EMPTY_RESTRICTIONS);
  };

  const handleClose = () => {
    reset();
    onClose();
  };

  const customValid = isValidCustomDuration(customValue);
  // Same computation submit() uses, for the "expires <date>" preview next to the inputs —
  // kept in sync by construction rather than duplicated at two different points in time.
  const customExpiresAt = customValid ? new Date(Date.now() + Number(customValue) * CUSTOM_UNIT_MS[customUnit]).toISOString() : null;

  const expiresAt = (() => {
    if (expiryDays === 'never') return null;
    if (expiryDays === 'custom') return customExpiresAt;
    return new Date(Date.now() + Number(expiryDays) * 86400000).toISOString();
  })();

  // Cleared only once the key exists: a failed create keeps the draft, type included, for a
  // retry. `submitting` holds Create disabled meanwhile so a double click cannot mint twice.
  const submit = async () => {
    // Held as a string while editing so the field can be cleared; coerced here because
    // the API takes a number.
    const usable = limits.filter((l) => Number(l.value) > 0).map((l) => ({ ...l, value: Number(l.value) }));
    // Omitted, not empty, for full access: the server stores no scopes as NULL.
    const restricted = kind === 'platform' && access === 'restricted';
    setSubmitting(true);
    const created = await onCreate({
      name: name.trim(),
      kind,
      expiresAt,
      limits: carriesGatewayTraffic(kind) ? usable : [],
      scopes: restricted ? [...scopes].sort() : null,
      gatewayPolicy: kind === 'gateway' && gatewayAccess === 'restricted' ? toGatewayPolicy(restrictions) : null,
    });
    setSubmitting(false);
    if (created) reset();
  };

  const restrictedEmpty =
    (kind === 'platform' && access === 'restricted' && scopes.size === 0) ||
    (kind === 'gateway' && gatewayAccess === 'restricted' && !hasRestrictions(restrictions));
  const createDisabled = !name.trim() || (expiryDays === 'custom' && !customValid) || restrictedEmpty;

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title='New API token'
      width='md'
      actionButtons={
        <Box sx={{ display: 'flex', gap: ds.space[3], justifyContent: 'flex-end' }}>
          <Button tone='secondary' onClick={handleClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={createDisabled} loading={submitting}>
            Create
          </Button>
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>Type</Typography>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Token type'
            value={kind}
            onChange={setKind}
            options={KIND_OPTIONS}
            id='token-kind-toggle'
          />
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{KIND_HINTS[kind]}</Typography>
        </Box>
        <Input size='sm' label='Name' value={name} placeholder='Enter a descriptive name for your token' onChange={setName} />
        <Select label='Expires' value={expiryDays} onChange={setExpiryDays} options={EXPIRY_OPTIONS} />
        {expiryDays === 'custom' && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
            <Box sx={{ display: 'flex', gap: ds.space[2] }}>
              <Input size='sm' label='Duration' type='number' value={customValue} onChange={setCustomValue} />
              <Select label='Unit' value={customUnit} onChange={setCustomUnit} options={CUSTOM_UNIT_OPTIONS} />
            </Box>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
              {customValid ? `expires ${dayjs(customExpiresAt).format('D MMM YYYY, HH:mm')}` : 'Enter a duration greater than zero'}
            </Typography>
          </Box>
        )}
        {kind === 'gateway' && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>Access</Typography>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Key access'
              value={gatewayAccess}
              onChange={setGatewayAccess}
              options={GATEWAY_ACCESS_OPTIONS}
              id='gateway-access-toggle'
            />
            {gatewayAccess === 'restricted' && <GatewayRestrictionPicker value={restrictions} onChange={setRestrictions} />}
          </Box>
        )}
        {carriesGatewayTraffic(kind) && <LimitEditor limits={limits} onChange={setLimits} />}
        {kind === 'platform' && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>Access</Typography>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Key access'
              value={access}
              onChange={setAccess}
              options={ACCESS_OPTIONS}
              id='token-access-toggle'
            />
            {access === 'restricted' && <ScopePicker selected={scopes} onChange={setScopes} />}
          </Box>
        )}
      </Box>
    </Modal>
  );
};

export default CreateTokenDialog;
