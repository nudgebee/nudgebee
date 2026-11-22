// Set or remove the role a Knowledge Graph node acts as (database, cache, ...).
//
// A role a user sets overrides what the classifier detected; removing it brings
// the detected one back. The server also applies the change to the node's
// linked cloud instances (a VM host and its EC2 instance are one machine), and
// says which it could not change.

import { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Callout } from '@ui/Callout';
import { toast as snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { ds } from 'src/utils/colors';
import { detectedRoleText, nodeRoleState, useNodeRoles } from './nodeRoles';

const nodeName = (node) => node?.properties?.name || node?.name || '';

// Tells the user what else happened beyond this node.
const reportResult = (result, verb) => {
  const applied = result?.applied?.length ?? 0;
  const skipped = result?.skipped?.length ?? 0;
  const also = applied ? ` Also ${verb} on ${applied} linked instance${applied === 1 ? '' : 's'}.` : '';
  snackbar.success(`Role ${verb}.${also}`);
  if (skipped) {
    snackbar.warning(`Not changed on ${skipped} linked instance${skipped === 1 ? '' : 's'} in an account you cannot edit.`);
  }
  if (result?.build_in_progress) {
    snackbar.info('A graph build is running: it may show the old role until the next build, within the hour.');
  }
};

const NodeRoleDialog = ({ open, node, onClose, onSaved }) => {
  const { roles } = useNodeRoles();
  const current = nodeRoleState(node?.properties);
  const [role, setRole] = useState('');
  const [engine, setEngine] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      return;
    }
    const state = nodeRoleState(node?.properties);
    setRole(state.userSet ? state.role : '');
    setEngine(state.userSet ? state.engine : '');
  }, [open, node]);

  const finish = (result) => {
    if (result && onSaved) {
      onSaved(result);
    }
    onClose();
  };

  const write = async (call, verb, field) => {
    setSaving(true);
    try {
      const res = await call();
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error(`Could not change the role: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      const result = res?.data?.data?.[field]?.data;
      reportResult(result, verb);
      finish(result);
    } catch (err) {
      console.error('Node role write failed:', err);
      snackbar.error('Could not change the role.');
    } finally {
      setSaving(false);
    }
  };

  const handleSave = () =>
    write(() => apiKnowledgeGraph.setNodeRole({ node_id: node.id, role, engine: engine.trim() }), 'set', 'kg_upsert_node_role');

  const handleRemove = () => write(() => apiKnowledgeGraph.removeNodeRole({ node_id: node.id }), 'removed', 'kg_delete_node_role');

  const unchanged = current.userSet && role === current.role && engine.trim() === current.engine;
  const detected = current.detectedRole
    ? [detectedRoleText(current.detectedRole), current.userSet ? node?.properties?.detected_engine : current.engine].filter(Boolean).join(' · ')
    : '';

  const actionButtons = (
    <Box sx={{ display: 'flex', gap: ds.space[2], justifyContent: 'space-between', width: '100%' }}>
      <Box>
        {current.userSet && (
          <Button tone='danger' size='md' onClick={handleRemove} disabled={saving} data-testid='kg-node-role-remove'>
            Remove role
          </Button>
        )}
      </Box>
      <Box sx={{ display: 'flex', gap: ds.space[2] }}>
        <Button tone='secondary' size='md' onClick={() => finish(null)} disabled={saving} data-testid='kg-node-role-cancel'>
          Cancel
        </Button>
        <Button
          tone='primary'
          size='md'
          onClick={handleSave}
          disabled={!role || unchanged || saving}
          loading={saving}
          data-testid='kg-node-role-save'
        >
          Save
        </Button>
      </Box>
    </Box>
  );

  return (
    <Modal
      open={open}
      width='sm'
      title={`Role${nodeName(node) ? ` — ${nodeName(node)}` : ''}`}
      handleClose={() => (saving ? undefined : finish(null))}
      loader={saving}
      actionButtons={actionButtons}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], py: ds.space[2] }}>
        <Box data-testid='kg-node-role-detected'>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>Detected automatically</Typography>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{detected || 'Nothing detected'}</Typography>
        </Box>
        <Form variant='stacked' density='compact'>
          <Form.Field label='This node acts as' required helperText='Overrides the detected role. The graph, its filters and the assistant use it.'>
            <Select
              size='sm'
              options={roles.map((r) => ({ value: r.value, label: r.label }))}
              value={role}
              onChange={(next) => setRole(next || '')}
              placeholder='Pick a role'
              required
              id='kg-node-role-select'
            />
          </Form.Field>
          <Form.Field label='Engine' helperText='Optional: postgres, redis, kafka, … It sets the icon shown on the node.'>
            <Input size='sm' value={engine} onChange={setEngine} placeholder='postgres' data-testid='kg-node-role-engine' />
          </Form.Field>
        </Form>
        <Callout tone='info' message='Applies to this node and its linked cloud instance, if it has one. The change is recorded in the audit log.' />
      </Box>
    </Modal>
  );
};

NodeRoleDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  // The node as kg_get_node returns it.
  node: PropTypes.shape({
    id: PropTypes.string.isRequired,
    name: PropTypes.string,
    properties: PropTypes.object,
  }).isRequired,
  onClose: PropTypes.func.isRequired,
  // Called with { node, applied, skipped, build_in_progress } after a change.
  onSaved: PropTypes.func,
};

export default NodeRoleDialog;
