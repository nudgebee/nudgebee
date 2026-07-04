import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button as DsButton } from '@ui/Button';
import Text from '@shared/format/Text';
import { toast as snackbar } from '@ui/Toast';
import apiOwnership from '@api1/ownership';
import { isTenantAdmin } from '@lib/auth';
import OwnerPicker from './OwnerPicker';
import OwnerBadge from './OwnerBadge';

// View + set the owner of one resource. On open it resolves the EFFECTIVE owner
// (which may be inherited or rule-derived) and shows it; the picker sets a
// DIRECT owner on THIS resource, which then wins over rule/inheritance. On any
// successful write it calls onChange() so the host surface refetches (REVISE #3),
// then onClose(true).
export default function AssignOwnerModal({ open, onClose, resourceType, resourceKey, cloudAccountId, resourceLabel, onChange }) {
  const canWrite = isTenantAdmin();
  const [effective, setEffective] = useState(null);
  const [picked, setPicked] = useState(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open || !resourceKey) return undefined;
    let active = true;
    setLoading(true);
    setPicked(null);
    apiOwnership
      .getOwner({ resourceType, resourceKey })
      .then((res) => {
        if (!active) return;
        setEffective(res);
        // Pre-select the picker only when this resource has its own DIRECT owner.
        if (res && res.found && res.via === 'self' && res.source === 'manual') {
          setPicked({ ownerType: res.owner_type, ownerId: res.owner_id });
        }
      })
      .catch(() => active && setEffective(null))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, [open, resourceType, resourceKey]);

  const hasDirectOwner = effective && effective.found && effective.via === 'self' && effective.source === 'manual';

  const finish = (didChange) => {
    if (didChange && onChange) onChange();
    if (onClose) onClose(didChange);
  };

  const handleSave = async () => {
    if (!picked) return;
    setSaving(true);
    try {
      await apiOwnership.assignOwner({
        resourceType,
        resourceKey,
        ownerType: picked.ownerType,
        ownerId: picked.ownerId,
        cloudAccountId,
      });
      snackbar.success('Owner assigned');
      finish(true);
    } catch {
      snackbar.error('Failed to assign owner');
    } finally {
      setSaving(false);
    }
  };

  const handleRemove = async () => {
    setSaving(true);
    try {
      await apiOwnership.removeOwner({ resourceType, resourceKey });
      snackbar.success('Owner removed');
      finish(true);
    } catch {
      snackbar.error('Failed to remove owner');
    } finally {
      setSaving(false);
    }
  };

  const actionButtons = canWrite ? (
    <Box sx={{ display: 'flex', gap: 1, justifyContent: 'flex-end', width: '100%' }}>
      {hasDirectOwner ? (
        <DsButton tone='danger' size='md' onClick={handleRemove} loading={saving} id='owner-remove'>
          Remove owner
        </DsButton>
      ) : null}
      <DsButton tone='secondary' size='md' onClick={() => finish(false)} disabled={saving} id='owner-cancel'>
        Cancel
      </DsButton>
      <DsButton tone='primary' size='md' onClick={handleSave} disabled={!picked} loading={saving} id='owner-save'>
        Save
      </DsButton>
    </Box>
  ) : null;

  return (
    <Modal
      open={open}
      handleClose={() => (saving ? undefined : finish(false))}
      title={`Owner${resourceLabel ? ` — ${resourceLabel}` : ''}`}
      width='sm'
      loader={saving}
      actionButtons={actionButtons}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, py: 1 }}>
        <Box>
          <Text value='Effective owner' secondaryText />
          <Box sx={{ mt: 0.5 }}>{loading ? <Text value='Resolving…' secondaryText /> : <OwnerBadge owner={effective} />}</Box>
        </Box>
        {canWrite ? (
          <Box>
            <OwnerPicker value={picked} onChange={setPicked} id='assign-owner-picker' />
            <Box sx={{ mt: 0.5 }}>
              <Text value='Sets a direct owner on this resource (overrides rule / inherited ownership).' secondaryText />
            </Box>
          </Box>
        ) : null}
      </Box>
    </Modal>
  );
}

AssignOwnerModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  resourceType: PropTypes.string.isRequired,
  resourceKey: PropTypes.string.isRequired,
  cloudAccountId: PropTypes.string,
  resourceLabel: PropTypes.string,
  onChange: PropTypes.func,
};
