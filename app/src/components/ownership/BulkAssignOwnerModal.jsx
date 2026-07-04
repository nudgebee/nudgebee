import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button as DsButton } from '@ui/Button';
import Text from '@shared/format/Text';
import { toast as snackbar } from '@ui/Toast';
import apiOwnership from '@api1/ownership';
import OwnerPicker from './OwnerPicker';
import AccountSelect from './AccountSelect';
import NamespaceSelect from './NamespaceSelect';
import WorkloadMultiSelect from './WorkloadMultiSelect';
import LabelWithInfo from './LabelWithInfo';
import { K8S } from './accountProviders';

// Bulk manual assignment: pick account -> namespace -> workloads, then assign one
// owner to each selected workload by cloud_resource_id. Distinct from a workload
// RULE (matched by name at resolve time): this writes a direct manual owner per
// workload. defaultAccountId seeds the cascade from the current page.
export default function BulkAssignOwnerModal({ open, onClose, defaultAccountId, onChange }) {
  const [accountId, setAccountId] = useState(defaultAccountId || '');
  const [namespace, setNamespace] = useState('');
  const [workloads, setWorkloads] = useState([]);
  const [owner, setOwner] = useState(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (open) {
      setAccountId(defaultAccountId || '');
      setNamespace('');
      setWorkloads([]);
      setOwner(null);
    }
  }, [open, defaultAccountId]);

  const valid = accountId && namespace && workloads.length > 0 && owner;

  const handleSave = async () => {
    if (!valid) return;
    setSaving(true);
    try {
      await Promise.all(
        workloads.map((w) =>
          apiOwnership.assignOwner({
            resourceType: 'workload',
            resourceKey: w.cloud_resource_id,
            ownerType: owner.ownerType,
            ownerId: owner.ownerId,
            cloudAccountId: accountId,
          })
        )
      );
      snackbar.success(`Owner assigned to ${workloads.length} workload${workloads.length > 1 ? 's' : ''}`);
      if (onChange) onChange();
      onClose(true);
    } catch {
      snackbar.error('Failed to assign owners');
    } finally {
      setSaving(false);
    }
  };

  const actionButtons = (
    <Box sx={{ display: 'flex', gap: 1, justifyContent: 'flex-end', width: '100%' }}>
      <DsButton tone='secondary' size='md' onClick={() => onClose(false)} disabled={saving} id='bulk-assign-cancel'>
        Cancel
      </DsButton>
      <DsButton tone='primary' size='md' onClick={handleSave} disabled={!valid} loading={saving} id='bulk-assign-save'>
        Assign{workloads.length ? ` (${workloads.length})` : ''}
      </DsButton>
    </Box>
  );

  return (
    <Modal
      open={open}
      handleClose={() => (saving ? undefined : onClose(false))}
      title='Bulk assign owner'
      width='sm'
      loader={saving}
      actionButtons={actionButtons}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', py: 1 }}>
        <AccountSelect
          id='bulk-account'
          label={
            <LabelWithInfo
              text='Cloud account'
              info='The cloud account (cluster) the workloads belong to. Required — it drives the namespace and workload lists.'
            />
          }
          required
          clearable={false}
          providerFilter={K8S}
          value={accountId}
          onChange={(v) => {
            setAccountId(v);
            setNamespace('');
            setWorkloads([]);
          }}
        />
        <NamespaceSelect
          id='bulk-namespace'
          label={<LabelWithInfo text='Namespace' info='Pick the namespace whose workloads you want to assign.' />}
          accountId={accountId}
          value={namespace}
          disabled={!accountId}
          requireAccount
          onChange={(v) => {
            setNamespace(v);
            setWorkloads([]);
          }}
        />
        <WorkloadMultiSelect
          id='bulk-workloads'
          label={<LabelWithInfo text='Workloads' info='Select one or more workloads. Each gets the owner assigned directly (by resource id).' />}
          accountId={accountId}
          namespace={namespace}
          value={workloads.map((w) => w.cloud_resource_id)}
          onChange={setWorkloads}
        />
        <OwnerPicker
          value={owner}
          onChange={setOwner}
          id='bulk-owner'
          label={<LabelWithInfo text='Owner' info='The Nudgebee user or group to assign as owner of the selected workloads.' />}
        />
        <Text value='Assigns a direct owner to each selected workload (overrides rule / inherited ownership).' secondaryText />
      </Box>
    </Modal>
  );
}

BulkAssignOwnerModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  defaultAccountId: PropTypes.string,
  onChange: PropTypes.func,
};
