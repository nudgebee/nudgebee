// Declare / edit one Knowledge Graph node by hand.
//
// The form is driven by kg_get_manual_node_schema:
//   1. Where does it live — a connected cloud account, or outside any.
//   2. What is it — the types that place accepts.
//   3. The type's key fields, all of which decide the node's identity. In a
//      connected account they are exactly what the collector keys the same
//      resource by, so when it is later collected the two are one node.
//   4. Optional details from the type's schema.
//
// Editing changes only the details: the key fields decide the node id, so
// changing one would be a different node (delete and declare again).

import { useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Banner } from '@ui/Banner';
import { Callout } from '@ui/Callout';
import { CollapsableCard } from '@ui/CollapsableCard';
import { toast as snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import { humanizeType } from './ManualNodesTable';
import { ds } from 'src/utils/colors';

// The schema's place for "outside any connected account" has no account id.
export const OUTSIDE_ACCOUNTS = '__outside__';
const placeValue = (place) => place.cloud_account_id || OUTSIDE_ACCOUNTS;

const EMPTY_KEYS = { name: '', region: '', namespace: '', network: '' };

// The VPC node's name is the last segment of its unique key
// (aws:<account>:<region>:VPC::<name>), and it is what the collector keys a
// resource in that VPC by.
const networkLabel = (uniqueKey) => {
  const parts = (uniqueKey || '').split(':');
  return parts.length >= 6 ? parts.slice(5).join(':') : uniqueKey;
};

// The node id of an "already in the graph" refusal, forwarded by the gateway.
const existingNodeIdOf = (errors) => errors?.[0]?.extensions?.upstream?.body?.node_id;

// A property's value is stored in the shape its collector uses: a key/value
// map (annotations), a list (subnet ids) or a single value. The form edits a
// map as rows and a list as comma-separated text.
const toEditable = (value) => {
  if (Array.isArray(value)) {
    return value.join(', ');
  }
  if (value && typeof value === 'object') {
    return Object.entries(value).map(([k, v]) => ({ k, v: String(v ?? '') }));
  }
  return value;
};

const fromEditable = (value, shape) => {
  if (shape === 'map') {
    const rows = Array.isArray(value) ? value.filter((r) => r.k.trim()) : [];
    return rows.length ? Object.fromEntries(rows.map((r) => [r.k.trim(), r.v.trim()])) : undefined;
  }
  const text = typeof value === 'string' ? value.trim() : value;
  if (shape === 'list') {
    const items = String(text ?? '')
      .split(',')
      .map((v) => v.trim())
      .filter(Boolean);
    return items.length ? items : undefined;
  }
  return text === '' ? undefined : text;
};

const EditManualNodeDialog = ({ open, row, initial, schema, onClose, onSaved, onUseExisting }) => {
  const isEdit = Boolean(row);
  const [place, setPlace] = useState(OUTSIDE_ACCOUNTS);
  const [specificType, setSpecificType] = useState('');
  const [keys, setKeys] = useState(EMPTY_KEYS);
  const [description, setDescription] = useState('');
  const [properties, setProperties] = useState({});
  const [labels, setLabels] = useState([]);
  const [networks, setNetworks] = useState([]);
  const [loadingNetworks, setLoadingNetworks] = useState(false);
  const [existingNodeId, setExistingNodeId] = useState('');
  const [submitting, setSubmitting] = useState(false);

  // Seed the form whenever it opens: the row being edited, a prefill from an
  // unmatched dependency endpoint, or blank.
  useEffect(() => {
    if (!open) {
      return;
    }
    setExistingNodeId('');
    if (row) {
      setPlace(row.cloud_account_id || OUTSIDE_ACCOUNTS);
      setSpecificType(row.cloud_account_id ? row.specific_type : row.node_type);
      setKeys({ name: row.name, region: row.region || '', namespace: row.namespace || '', network: row.network_node_id || '' });
      setDescription(row.description || '');
      setProperties(Object.fromEntries(Object.entries(row.properties || {}).map(([k, v]) => [k, toEditable(v)])));
      setLabels(Object.entries(row.labels || {}).map(([k, v]) => ({ k, v })));
      return;
    }
    setPlace(OUTSIDE_ACCOUNTS);
    setSpecificType(initial?.node_type || '');
    setKeys({ ...EMPTY_KEYS, name: initial?.name || '', namespace: initial?.namespace || '', region: initial?.region || '' });
    setDescription('');
    setProperties({});
    setLabels([]);
  }, [open, row, initial]);

  const places = schema || [];
  const currentPlace = places.find((p) => placeValue(p) === place);
  const kinds = currentPlace?.kinds ?? [];
  const kind = kinds.find((k) => k.specific_type === specificType);
  const isOutside = place === OUTSIDE_ACCOUNTS;

  const placeOptions = useMemo(
    () =>
      places.map((p) =>
        p.cloud_account_id
          ? {
              value: p.cloud_account_id,
              label: [p.account_name || p.account_number || p.cloud_account_id, p.account_number].filter(Boolean).join(' · '),
              group: p.cloud_provider,
            }
          : { value: OUTSIDE_ACCOUNTS, label: 'Not in a connected account', group: 'Other' }
      ),
    [places]
  );

  const typeOptions = useMemo(
    () =>
      kinds
        .map((k) => ({ value: k.specific_type, label: humanizeType(k.specific_type), group: humanizeType(k.node_type) }))
        .sort((a, b) => a.group.localeCompare(b.group) || a.label.localeCompare(b.label)),
    [kinds]
  );

  const networkField = kind?.key_fields?.find((f) => f.name === 'network');

  // VPCs of the account, read from the graph — the value is the node id; the
  // server reads the name the key needs off that node.
  useEffect(() => {
    setNetworks([]);
    if (!open || isOutside || !networkField?.pick_node_type) {
      return;
    }
    let cancelled = false;
    setLoadingNetworks(true);
    apiKnowledgeGraph
      .getFilterOptions({ accountIds: [place], nodeTypes: [networkField.pick_node_type] })
      .then((res) => {
        if (cancelled) {
          return;
        }
        const d = res?.data?.data?.kg_get_filter_options?.data ?? {};
        const keysList = d.node_keys ?? [];
        const ids = d.node_ids ?? [];
        setNetworks(keysList.map((k, i) => ({ value: ids[i], label: networkLabel(k) })).filter((o) => o.value));
      })
      .catch((err) => {
        console.error('Failed to load networks:', err);
        snackbar.error('Failed to load the networks for this account.');
      })
      .finally(() => !cancelled && setLoadingNetworks(false));
    return () => {
      cancelled = true;
    };
  }, [open, place, isOutside, networkField?.pick_node_type]);

  const handlePlaceChange = (next) => {
    setPlace(next || OUTSIDE_ACCOUNTS);
    setSpecificType('');
    setKeys((prev) => ({ ...EMPTY_KEYS, name: prev.name }));
    setProperties({});
  };

  const handleTypeChange = (next) => {
    setSpecificType(next || '');
    setKeys((prev) => ({ ...prev, network: '' }));
    setProperties({});
  };

  const setKey = (name) => (next) => setKeys((prev) => ({ ...prev, [name]: next }));

  const missingRequired = (kind?.key_fields ?? []).some((f) => f.required && !String(keys[f.name] || '').trim());
  const canSubmit = isEdit || (kind && !missingRequired);

  const shapeOf = (name) => kind?.properties?.find((p) => p.name === name)?.shape;

  const cleanedProperties = () =>
    Object.fromEntries(
      Object.entries(properties)
        .map(([k, v]) => [k, fromEditable(v, shapeOf(k))])
        .filter(([, v]) => v !== undefined && v !== null)
    );

  const cleanedLabels = () => Object.fromEntries(labels.filter((l) => l.k.trim()).map((l) => [l.k.trim(), l.v.trim()]));

  const setProperty = (name) => (next) => setProperties((prev) => ({ ...prev, [name]: next }));
  const fieldProperties = (kind?.properties ?? []).filter((p) => p.shape !== 'map');
  const mapProperties = (kind?.properties ?? []).filter((p) => p.shape === 'map');

  const handleSubmit = async () => {
    setSubmitting(true);
    setExistingNodeId('');
    try {
      if (isEdit) {
        const res = await apiKnowledgeGraph.updateManualNode({
          id: row.id,
          node: { description: description.trim(), properties: cleanedProperties(), labels: cleanedLabels() },
        });
        const errors = res?.data?.errors;
        if (errors?.length) {
          snackbar.error(`Save failed: ${errors[0]?.message ?? 'Unknown error'}`);
          return;
        }
        snackbar.success('Node updated.');
        onSaved(res?.data?.data?.kg_update_manual_node?.data);
        return;
      }

      const request = {
        name: keys.name.trim(),
        description: description.trim(),
        properties: cleanedProperties(),
        labels: cleanedLabels(),
      };
      if (isOutside) {
        request.node_type = kind.node_type;
        if (keys.namespace.trim()) {
          request.namespace = keys.namespace.trim();
        }
        if (keys.region.trim()) {
          request.region = keys.region.trim();
        }
      } else {
        // Send exactly the key fields this type declares: a cloud resource has a
        // region (and maybe a VPC), a Kubernetes one a namespace.
        request.cloud_account_id = place;
        request.specific_type = kind.specific_type;
        const declares = (field) => kind.key_fields.some((f) => f.name === field);
        if (declares('region')) {
          request.region = keys.region.trim();
        }
        if (declares('namespace')) {
          request.namespace = keys.namespace.trim();
        }
        if (keys.network) {
          request.network_node_id = keys.network;
        }
      }
      const res = await apiKnowledgeGraph.createManualNode(request);
      const errors = res?.data?.errors;
      if (errors?.length) {
        const existing = existingNodeIdOf(errors);
        if (existing) {
          setExistingNodeId(existing);
          return;
        }
        snackbar.error(`Declare failed: ${errors[0]?.message ?? 'Unknown error'}`);
        return;
      }
      snackbar.success('Node declared.');
      onSaved(res?.data?.data?.kg_create_manual_node?.data);
    } catch (err) {
      console.error('Manual node save failed:', err);
      snackbar.error('Save failed.');
    } finally {
      setSubmitting(false);
    }
  };

  const identityHelp = isEdit ? 'Decides the node’s identity — to change it, delete this node and declare it again.' : undefined;

  return (
    <Modal width='md' title={isEdit ? 'Edit Manual Node' : 'Declare Node'} open={open} handleClose={onClose} onClose={onClose}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], p: ds.space[4] }}>
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.5 }}>
          Declare a resource the Knowledge Graph has not discovered, so dependencies can point at it. In a connected account the node is keyed exactly
          as the collector keys the same resource — when the resource is later collected the two become one node.
        </Typography>

        {existingNodeId && (
          <Banner
            tone='warning'
            surface='section'
            title='Already in the graph'
            message='The collector already provides this resource. Point your dependency at that node instead of declaring a second one.'
            actions={onUseExisting ? [{ label: 'Use existing node', onClick: () => onUseExisting(existingNodeId) }] : undefined}
          />
        )}

        <Form variant='stacked' density='compact'>
          <Form.Row ratio={[1, 1]}>
            <Form.Field label='Where does it live?' required helperText={identityHelp}>
              <Select
                size='sm'
                options={placeOptions}
                value={place}
                onChange={handlePlaceChange}
                grouped
                required
                disabled={isEdit}
                id='manual-node-place'
              />
            </Form.Field>
            <Form.Field label='Type' required>
              <Select
                size='sm'
                options={typeOptions}
                value={specificType}
                onChange={handleTypeChange}
                grouped
                defaultGroupsOpen
                required
                placeholder='Pick a type'
                disabled={isEdit}
                id='manual-node-type'
              />
            </Form.Field>
          </Form.Row>

          {isOutside && !isEdit && (
            <Callout
              tone='info'
              message='A node outside your connected accounts can never be matched by a collector. If the resource is in a connected account, pick that account so the two merge when it is discovered.'
            />
          )}

          {kind && (
            <Form.Section title='Identity' description='These fields decide which resource this is.'>
              <Form.Row ratio={kind.key_fields.map(() => 1)}>
                {kind.key_fields.map((f) => (
                  <Form.Field key={f.name} label={f.label} required={f.required} helperText={f.hint}>
                    {f.pick_node_type ? (
                      <Select
                        size='sm'
                        options={networks}
                        value={keys.network}
                        onChange={(next) => setKey('network')(next || '')}
                        loading={loadingNetworks}
                        placeholder={networks.length || loadingNetworks ? 'Pick a VPC' : 'No VPCs in this account’s graph'}
                        disabled={isEdit}
                        id='manual-node-network'
                      />
                    ) : (
                      <Input size='sm' value={keys[f.name] || ''} onChange={setKey(f.name)} disabled={isEdit} data-testid={`manual-node-${f.name}`} />
                    )}
                  </Form.Field>
                ))}
              </Form.Row>
            </Form.Section>
          )}

          {kind && (
            <CollapsableCard
              header={<Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold }}>More details</Typography>}
              elevation='flat'
              defaultOpen={isEdit}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
                <Form.Field label='Description'>
                  <Input size='sm' value={description} onChange={setDescription} placeholder='What it is and who owns it' />
                </Form.Field>
                {fieldProperties.length > 0 && (
                  <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: ds.space[3] }}>
                    {fieldProperties.map((p) => (
                      <Form.Field key={p.name} label={p.name} helperText={p.shape === 'list' ? 'Separate values with commas' : undefined}>
                        <Input size='sm' value={String(properties[p.name] ?? '')} onChange={setProperty(p.name)} />
                      </Form.Field>
                    ))}
                  </Box>
                )}
                {mapProperties.map((p) => (
                  <KeyValueEditor
                    key={p.name}
                    title={p.name}
                    addLabel='Add entry'
                    rows={Array.isArray(properties[p.name]) ? properties[p.name] : []}
                    onChange={setProperty(p.name)}
                  />
                ))}
                <KeyValueEditor title='Labels' addLabel='Add label' rows={labels} onChange={setLabels} />
              </Box>
            </CollapsableCard>
          )}
        </Form>

        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2], pt: ds.space[1] }}>
          <Button tone='secondary' size='md' onClick={onClose} disabled={submitting}>
            Cancel
          </Button>
          <Button
            tone='primary'
            size='md'
            onClick={handleSubmit}
            disabled={!canSubmit || submitting}
            loading={submitting}
            data-testid='manual-node-submit'
          >
            {isEdit ? 'Save' : 'Declare'}
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

EditManualNodeDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  row: PropTypes.object,
  // Prefill from an unmatched dependency endpoint: { node_type, name, namespace, region }.
  initial: PropTypes.object,
  schema: PropTypes.array,
  onClose: PropTypes.func.isRequired,
  onSaved: PropTypes.func.isRequired,
  // Offered when the resource is already in the graph (from the dependency flow).
  onUseExisting: PropTypes.func,
};

// Key/value rows: a node's labels, or a map property such as annotations.
const KeyValueEditor = ({ title, addLabel, rows, onChange }) => {
  const update = (i, field) => (next) => onChange(rows.map((l, j) => (j === i ? { ...l, [field]: next } : l)));
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
      <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[500] }}>{title}</Typography>
      {rows.map((l, i) => (
        <Box key={i} sx={{ display: 'flex', gap: ds.space[2], alignItems: 'center' }}>
          <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
            <Input size='sm' value={l.k} onChange={update(i, 'k')} placeholder='key' />
          </Box>
          <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
            <Input size='sm' value={l.v} onChange={update(i, 'v')} placeholder='value' />
          </Box>
          <Button tone='ghost' size='xs' onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            Remove
          </Button>
        </Box>
      ))}
      <Box>
        <Button tone='secondary' size='xs' onClick={() => onChange([...rows, { k: '', v: '' }])}>
          {addLabel}
        </Button>
      </Box>
    </Box>
  );
};

KeyValueEditor.propTypes = {
  title: PropTypes.string.isRequired,
  addLabel: PropTypes.string.isRequired,
  rows: PropTypes.array.isRequired,
  onChange: PropTypes.func.isRequired,
};

export default EditManualNodeDialog;
