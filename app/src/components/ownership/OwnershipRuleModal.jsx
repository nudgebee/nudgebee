import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Switch } from '@ui/Switch';
import { Button as DsButton } from '@ui/Button';
import Text from '@shared/format/Text';
import { toast as snackbar } from '@ui/Toast';
import apiOwnership from '@api1/ownership';
import OwnerPicker from './OwnerPicker';
import AccountSelect from './AccountSelect';
import NamespaceSelect from './NamespaceSelect';
import WorkloadMultiSelect from './WorkloadMultiSelect';
import CloudRegionSelect from './CloudRegionSelect';
import CloudResourceTypeSelect from './CloudResourceTypeSelect';
import CloudResourceMultiSelect from './CloudResourceMultiSelect';
import LabelWithInfo from './LabelWithInfo';
import { K8S, CLOUD } from './accountProviders';

// Resource domain the rule targets: Kubernetes or individual cloud resources.
const DOMAIN_OPTIONS = [
  { value: 'k8s', label: 'Kubernetes' },
  { value: 'cloud', label: 'Cloud' },
];

const SCOPE_OPTIONS = [
  { value: 'label', label: 'Label (key = value)' },
  { value: 'namespace', label: 'Namespace name' },
  { value: 'workload', label: 'Specific workloads' },
];

const CLOUD_SCOPE_OPTIONS = [
  { value: 'cloud_tag', label: 'Tag (key = value)' },
  { value: 'cloud_type', label: 'Resource type' },
  { value: 'cloud_region', label: 'Region' },
  { value: 'cloud_resource', label: 'Specific resources' },
];

const firstScope = (domain) => (domain === 'cloud' ? 'cloud_tag' : 'label');

// Per-field help shown via the info icon next to each label.
const INFO = {
  name: 'A name for this rule, shown in the rules list. Does not affect matching.',
  domain: 'What this rule targets: Kubernetes workloads/namespaces, or individual cloud resources (EC2/RDS/S3/…).',
  match: 'How resources are matched to the owner. Most specific scope wins when several rules match.',
  accountOptional: 'Optional. Restrict this rule to one cloud account (cluster). Leave empty to apply across all accounts.',
  accountRequired: 'The cloud account (cluster) the workloads belong to. Required — it drives the namespace and workload lists.',
  labelKey: 'The Kubernetes label key to match, e.g. "team".',
  labelValue: 'The label value to match, e.g. "payments". A workload matches when labels[key] equals this value.',
  namespaceScope: 'The Kubernetes namespace to match. Every workload in this namespace is owned by the selected owner.',
  namespaceCascade: 'Pick the namespace whose workloads you want to select below.',
  workloads: 'Select one or more workloads in this namespace. The rule matches these workloads by name within the chosen account + namespace.',
  owner: 'The Nudgebee user or group that owns the matched resources.',
  cloudAccountOptional: 'Optional. Restrict this rule to one cloud account. Leave empty to apply across all cloud accounts.',
  cloudAccountRequired: 'The cloud account the resources belong to. Required — it drives the resource list.',
  cloudTagKey: 'The cloud resource tag key to match, e.g. "team".',
  cloudTagValue: 'The tag value to match, e.g. "payments". A resource matches when tags[key] equals this value.',
  cloudType: 'The cloud resource type (e.g. ec2_instance) to match.',
  cloudRegion: 'The cloud region (e.g. us-east-1) to match.',
  cloudResources: 'Select one or more specific resources in this account. The rule pins these resources by id.',
};

const EMPTY = { name: '', resourceDomain: 'k8s', matchScope: 'label', matchKey: '', matchValue: '', cloudAccountId: '', enabled: true };

// Create / edit one ownership rule. `rule` is a RuleDto when editing, null when
// adding. K8s scopes: label / namespace / workload. Cloud scopes: cloud_tag /
// cloud_type / cloud_region / cloud_resource (match_key holds the comma-joined
// resource id set for cloud_resource). Calls onClose(true) after a successful write.
export default function OwnershipRuleModal({ open, onClose, rule }) {
  const [form, setForm] = useState(EMPTY);
  const [owner, setOwner] = useState(null);
  const [workloads, setWorkloads] = useState([]); // [{cloud_resource_id, name}] for workload scope
  const [cloudResources, setCloudResources] = useState([]); // [id] for cloud_resource scope
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    if (rule) {
      setForm({
        name: rule.name || '',
        resourceDomain: rule.resource_domain || 'k8s',
        matchScope: rule.match_scope || 'label',
        matchKey: rule.match_key || '',
        matchValue: rule.match_value || '',
        cloudAccountId: rule.cloud_account_id || '',
        enabled: rule.enabled !== false,
      });
      setOwner(rule.owner_id ? { ownerType: rule.owner_type, ownerId: rule.owner_id } : null);
      // cloud_resource stores ids in match_key → prefill the multi-select directly.
      setCloudResources(
        rule.match_scope === 'cloud_resource' && rule.match_key
          ? rule.match_key
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean)
          : []
      );
    } else {
      setForm(EMPTY);
      setOwner(null);
      setCloudResources([]);
    }
    setWorkloads([]);
  }, [open, rule]);

  const set = (patch) => setForm((f) => ({ ...f, ...patch }));
  const isCloud = form.resourceDomain === 'cloud';
  const isLabel = form.matchScope === 'label';
  const isNamespace = form.matchScope === 'namespace';
  const isWorkload = form.matchScope === 'workload';
  const isCloudTag = form.matchScope === 'cloud_tag';
  const isCloudType = form.matchScope === 'cloud_type';
  const isCloudRegion = form.matchScope === 'cloud_region';
  const isCloudResource = form.matchScope === 'cloud_resource';
  // Names a workload rule started with (for edit prefill of the multi-select).
  const initialNames = rule && rule.match_scope === 'workload' && rule.match_key ? rule.match_key.split(',').map((s) => s.trim()) : [];

  let valid = form.name.trim() && owner;
  if (isLabel) valid = valid && form.matchKey.trim() && form.matchValue.trim();
  if (isNamespace) valid = valid && form.matchValue.trim();
  if (isWorkload) valid = valid && form.cloudAccountId && form.matchValue.trim() && workloads.length > 0;
  if (isCloudTag) valid = valid && form.matchKey.trim() && form.matchValue.trim();
  if (isCloudType || isCloudRegion) valid = valid && form.matchValue.trim();
  if (isCloudResource) valid = valid && form.cloudAccountId && cloudResources.length > 0;

  const changeDomain = (v) => {
    const domain = v || 'k8s';
    set({ resourceDomain: domain, matchScope: firstScope(domain), matchKey: '', matchValue: '', cloudAccountId: '' });
    setWorkloads([]);
    setCloudResources([]);
  };

  const changeScope = (v) => {
    set({ matchScope: v, matchKey: '', matchValue: '' });
    setWorkloads([]);
    setCloudResources([]);
  };

  const buildMatchKey = () => {
    if (isLabel || isCloudTag) return form.matchKey.trim();
    if (isWorkload) return workloads.map((w) => w.name).join(',');
    if (isCloudResource) return cloudResources.join(',');
    return '';
  };

  const handleSave = async () => {
    if (!valid) return;
    setSaving(true);
    try {
      await apiOwnership.upsertRule({
        id: rule?.id,
        name: form.name.trim(),
        resourceDomain: form.resourceDomain,
        matchScope: form.matchScope,
        matchKey: buildMatchKey(),
        matchValue: isCloudResource ? '' : form.matchValue.trim(),
        cloudAccountId: form.cloudAccountId,
        ownerType: owner.ownerType,
        ownerId: owner.ownerId,
        enabled: form.enabled,
      });
      snackbar.success(rule ? 'Rule updated' : 'Rule created');
      onClose(true);
    } catch (e) {
      snackbar.error(e?.message || 'Failed to save rule');
    } finally {
      setSaving(false);
    }
  };

  const actionButtons = (
    <Box sx={{ display: 'flex', gap: 1, justifyContent: 'flex-end', width: '100%' }}>
      <DsButton tone='secondary' size='md' onClick={() => onClose(false)} disabled={saving} id='rule-cancel'>
        Cancel
      </DsButton>
      <DsButton tone='primary' size='md' onClick={handleSave} disabled={!valid} loading={saving} id='rule-save'>
        {rule ? 'Save' : 'Create'}
      </DsButton>
    </Box>
  );

  return (
    <Modal
      open={open}
      handleClose={() => (saving ? undefined : onClose(false))}
      title={rule ? 'Edit rule' : 'Add rule'}
      width='sm'
      loader={saving}
      actionButtons={actionButtons}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', py: 1 }}>
        <Input
          id='rule-name'
          label={<LabelWithInfo text='Name' info={INFO.name} />}
          required
          placeholder='Enter rule name'
          value={form.name}
          onChange={(v) => set({ name: v })}
        />
        <Select
          id='rule-domain'
          label={<LabelWithInfo text='Domain' info={INFO.domain} />}
          options={DOMAIN_OPTIONS}
          value={form.resourceDomain}
          onChange={changeDomain}
          clearable={false}
        />
        <Select
          id='rule-scope'
          label={<LabelWithInfo text='Match' info={INFO.match} />}
          options={isCloud ? CLOUD_SCOPE_OPTIONS : SCOPE_OPTIONS}
          value={form.matchScope}
          onChange={changeScope}
          clearable={false}
        />

        {isLabel ? (
          <>
            <Input
              id='rule-match-key'
              label={<LabelWithInfo text='Label key' info={INFO.labelKey} />}
              required
              placeholder='Enter label key'
              value={form.matchKey}
              onChange={(v) => set({ matchKey: v })}
            />
            <Input
              id='rule-match-value'
              label={<LabelWithInfo text='Label value' info={INFO.labelValue} />}
              required
              placeholder='Enter label value'
              value={form.matchValue}
              onChange={(v) => set({ matchValue: v })}
            />
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account scope (optional)' info={INFO.accountOptional} />}
              placeholder='All accounts'
              providerFilter={K8S}
              value={form.cloudAccountId}
              onChange={(v) => set({ cloudAccountId: v })}
            />
          </>
        ) : null}

        {isNamespace ? (
          <>
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account scope (optional)' info={INFO.accountOptional} />}
              placeholder='All accounts'
              providerFilter={K8S}
              value={form.cloudAccountId}
              onChange={(v) => set({ cloudAccountId: v, matchValue: '' })}
            />
            <NamespaceSelect
              id='rule-namespace'
              label={<LabelWithInfo text='Namespace' info={INFO.namespaceScope} />}
              accountId={form.cloudAccountId}
              value={form.matchValue}
              onChange={(v) => set({ matchValue: v })}
            />
          </>
        ) : null}

        {isWorkload ? (
          <>
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account' info={INFO.accountRequired} />}
              required
              clearable={false}
              providerFilter={K8S}
              value={form.cloudAccountId}
              onChange={(v) => {
                set({ cloudAccountId: v, matchValue: '' });
                setWorkloads([]);
              }}
            />
            <NamespaceSelect
              id='rule-namespace'
              label={<LabelWithInfo text='Namespace' info={INFO.namespaceCascade} />}
              accountId={form.cloudAccountId}
              value={form.matchValue}
              disabled={!form.cloudAccountId}
              requireAccount
              onChange={(v) => {
                set({ matchValue: v });
                setWorkloads([]);
              }}
            />
            <WorkloadMultiSelect
              id='rule-workloads'
              label={<LabelWithInfo text='Workloads' info={INFO.workloads} />}
              accountId={form.cloudAccountId}
              namespace={form.matchValue}
              value={workloads.map((w) => w.cloud_resource_id)}
              onChange={setWorkloads}
              initialNames={initialNames}
            />
          </>
        ) : null}

        {isCloudTag ? (
          <>
            <Input
              id='rule-cloud-tag-key'
              label={<LabelWithInfo text='Tag key' info={INFO.cloudTagKey} />}
              required
              placeholder='Enter tag key'
              value={form.matchKey}
              onChange={(v) => set({ matchKey: v })}
            />
            <Input
              id='rule-cloud-tag-value'
              label={<LabelWithInfo text='Tag value' info={INFO.cloudTagValue} />}
              required
              placeholder='Enter tag value'
              value={form.matchValue}
              onChange={(v) => set({ matchValue: v })}
            />
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account scope (optional)' info={INFO.cloudAccountOptional} />}
              placeholder='All cloud accounts'
              providerFilter={CLOUD}
              value={form.cloudAccountId}
              onChange={(v) => set({ cloudAccountId: v })}
            />
          </>
        ) : null}

        {isCloudType ? (
          <>
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account scope (optional)' info={INFO.cloudAccountOptional} />}
              placeholder='All cloud accounts'
              providerFilter={CLOUD}
              value={form.cloudAccountId}
              onChange={(v) => set({ cloudAccountId: v, matchValue: '' })}
            />
            <CloudResourceTypeSelect
              id='rule-cloud-type'
              label={<LabelWithInfo text='Resource type' info={INFO.cloudType} />}
              accountId={form.cloudAccountId}
              value={form.matchValue}
              onChange={(v) => set({ matchValue: v })}
            />
          </>
        ) : null}

        {isCloudRegion ? (
          <>
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account scope (optional)' info={INFO.cloudAccountOptional} />}
              placeholder='All cloud accounts'
              providerFilter={CLOUD}
              value={form.cloudAccountId}
              onChange={(v) => set({ cloudAccountId: v, matchValue: '' })}
            />
            <CloudRegionSelect
              id='rule-cloud-region'
              label={<LabelWithInfo text='Region' info={INFO.cloudRegion} />}
              accountId={form.cloudAccountId}
              value={form.matchValue}
              onChange={(v) => set({ matchValue: v })}
            />
          </>
        ) : null}

        {isCloudResource ? (
          <>
            <AccountSelect
              id='rule-account'
              label={<LabelWithInfo text='Cloud account' info={INFO.cloudAccountRequired} />}
              required
              clearable={false}
              providerFilter={CLOUD}
              value={form.cloudAccountId}
              onChange={(v) => {
                set({ cloudAccountId: v });
                setCloudResources([]);
              }}
            />
            <CloudResourceMultiSelect
              id='rule-cloud-resources'
              label={<LabelWithInfo text='Resources' info={INFO.cloudResources} />}
              accountId={form.cloudAccountId}
              value={cloudResources}
              onChange={setCloudResources}
              disabled={!form.cloudAccountId}
            />
          </>
        ) : null}

        <OwnerPicker value={owner} onChange={setOwner} id='rule-owner' label={<LabelWithInfo text='Owner' info={INFO.owner} />} />
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Text value='Enabled' />
          <Switch checked={form.enabled} onChange={(_e, checked) => set({ enabled: checked })} aria-label='Rule enabled' />
        </Box>
        <Text
          value='Most specific rule wins (pinned resources › tag/label › type › namespace/region). Two rules can’t overlap on the same target — you’ll be asked to edit the existing rule instead.'
          secondaryText
        />
      </Box>
    </Modal>
  );
}

OwnershipRuleModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  rule: PropTypes.object,
};
