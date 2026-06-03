// Create / edit dialog for a single manual dependency — Phase 2.5 (NB-30989).
//
// One component handles both modes — pass `row` to edit, or omit it to
// create. Mirrors the loose-match contract the backend enforces:
//   - source_node_type, source_name, dest_node_type, dest_name required;
//   - the other six per-side fields (namespace, cluster, arn, account_id,
//     region) are optional qualifiers that narrow ambiguous matches;
//   - relationship_type defaults to CALLS.
//
// The form is a two-column layout (Source | Destination) so the operator
// can visually parallel the two endpoints. Sits inside the parent DS Modal
// with width='md' — Coverage tab style would be too narrow given the
// number of fields.

import { useEffect, useMemo, useState } from 'react';
import { Box, Typography, Divider } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomTooltip from '@components1/common/CustomTooltip';
import apiKnowledgeGraph from '@api1/knowledge-graph';
import KgNodePicker from './KgNodePicker';
import { ds } from 'src/utils/colors';

// Endpoint kinds. The kind selector toggles which subset of NodeTypes the
// dropdown surfaces AND which qualifier fields are visible — k8s endpoints
// take namespace/cluster, cloud endpoints take arn/account/region. Mixing
// them in one form was confusing operators ("do I fill ARN for a K8s pod?").
const KIND_K8S = 'k8s';
const KIND_CLOUD = 'cloud';
// Third kind — Create-only convenience: operator picks an existing KG node
// via three cascading dropdowns instead of typing identifiers. On submit
// the picked UUID is pinned via kg_resolve_manual_dependency so the row
// resolves on the first try. Edit mode never exposes this kind.
const KIND_KG_PICK = 'kg-pick';

// NodeType allowlist surfaced in the form, partitioned by kind. The backend
// accepts the full NodeType enum from core/types.go but exposing every value
// would clutter the dropdown. Cross-stack pairs (k8s → AWS) are the
// high-leverage case, so AWS-side types are first-class on the cloud side.
const K8S_NODE_TYPE_OPTIONS = [
  { value: 'K8sService', label: 'K8sService' },
  { value: 'Workload', label: 'Workload (Deployment/StatefulSet/…)' },
  { value: 'Pod', label: 'Pod' },
  { value: 'Service', label: 'Service (generic)' },
];

// NodeTypes are provider-agnostic — `Database` matches AWS RDS, Azure
// CosmosDB / Azure SQL, and GCP Cloud SQL alike. Labels intentionally list
// cross-cloud equivalents so operators on Azure / GCP don't bounce off an
// AWS-only label set.
const CLOUD_NODE_TYPE_OPTIONS = [
  { value: 'Database', label: 'Database (RDS / CosmosDB / Cloud SQL)' },
  { value: 'ServerlessFunction', label: 'ServerlessFunction (Lambda / Azure Functions / Cloud Functions)' },
  { value: 'Queue', label: 'Queue (SQS / Service Bus / Pub/Sub)' },
  { value: 'Topic', label: 'Topic (SNS / Event Grid / Pub/Sub Topic)' },
  { value: 'MessageQueue', label: 'MessageQueue (Kinesis / Event Hubs / Dataflow)' },
  { value: 'Cache', label: 'Cache (ElastiCache / Azure Cache / Memorystore)' },
  { value: 'Storage', label: 'Storage (S3 / Blob Storage / GCS)' },
  { value: 'LoadBalancer', label: 'LoadBalancer' },
  { value: 'APIGateway', label: 'APIGateway' },
  { value: 'ExternalService', label: 'ExternalService' },
];

const K8S_NODE_TYPE_SET = new Set(K8S_NODE_TYPE_OPTIONS.map((o) => o.value));

// deriveKind looks up which side of the K8s/Cloud taxonomy a NodeType
// belongs to. Used when opening the dialog in edit mode so the kind toggle
// reflects the existing row without the operator having to re-pick it.
const deriveKind = (nodeType) => (nodeType && K8S_NODE_TYPE_SET.has(nodeType) ? KIND_K8S : KIND_CLOUD);

const defaultNodeTypeForKind = (kind) => (kind === KIND_K8S ? K8S_NODE_TYPE_OPTIONS[0].value : CLOUD_NODE_TYPE_OPTIONS[0].value);

const RELATIONSHIP_OPTIONS = [
  { value: 'CALLS', label: 'CALLS' },
  { value: 'PUBLISHES_TO', label: 'PUBLISHES_TO' },
  { value: 'SUBSCRIBES_TO', label: 'SUBSCRIBES_TO' },
];

// Per-field tooltip copy. Keeping the wording in one place so we can edit
// without hunting through JSX. Mirrors the resolver semantics in
// flow_sources/manual_resolver.go — when a field "narrows ambiguous
// matches" it adds an AND clause to the candidate query; when it "wins"
// (ARN / Resource ID) it short-circuits the name match.
const TOOLTIPS = {
  kind: 'Kubernetes endpoints take namespace + cluster qualifiers. Cloud endpoints (AWS, Azure, GCP) take resource ID + account + region. Pick the one that matches the resource you want to declare.',
  nodeType:
    'The KG NodeType to match against. K8sService / Workload / Pod for Kubernetes; Database / Queue / Storage etc. for cloud resources. NodeTypes are provider-agnostic — a Cloud SQL DB and an RDS instance both resolve as Database.',
  name: 'Required. The resource name as it appears in the Knowledge Graph (e.g. K8s Service name, Lambda function name, RDS instance identifier).',
  namespace: 'Optional. K8s namespace. Use it when multiple workloads share the same name across namespaces.',
  cluster: 'Optional. K8s cluster name. Use it when the same workload exists in more than one cluster.',
  resourceId:
    'Optional. Deterministic cloud identifier — AWS ARN, Azure resource ID, or GCP self-link. When set, this wins over name matching (most specific match possible).',
  cloudAccount:
    'Optional. Strict qualifier — currently AWS-only (Azure subscription_id / GCP project_id support is a deferred backend follow-up). For Azure / GCP, narrow via Region + Resource ID instead.',
  region: 'Optional. Cloud region (us-east-1, eastus, us-central1, …). Useful when the same logical resource exists in multiple regions.',
  relationship:
    'Edge type. CALLS for synchronous request/response. PUBLISHES_TO / SUBSCRIBES_TO for pub-sub (you must declare both directions; no automatic inverse).',
  notes: 'Free-text context for future readers. Surfaces in the manual dependencies table so reviewers can see why a row was declared.',
  kgPickAccount: 'Pick the cloud account whose KG nodes you want to browse. Required to populate the node-type and node dropdowns.',
  kgPickNodeType: 'Pick the KG NodeType. Only types with at least one node in the selected account are shown.',
  kgPickNode: 'Pick the exact KG node. The node’s UUID is pinned on submit so the row resolves on the first try — no resolver ambiguity.',
};

const BLANK = {
  // Kind drives which fields render; it isn't persisted to the backend
  // (the backend derives k8s-vs-cloud from node_type) but kept in form
  // state so the UI can react.
  source_kind: KIND_K8S,
  source_node_type: 'K8sService',
  source_name: '',
  source_namespace: '',
  source_cluster: '',
  source_arn: '',
  source_account_id: '',
  source_region: '',
  // KG-pick scratch state — only used when kind === KIND_KG_PICK. Not
  // sent to the backend; on submit, source_picked_node_id is passed to
  // kg_resolve_manual_dependency to pin the row to that UUID.
  source_picker_account_id: '',
  source_picker_node_type: '',
  source_picked_node_id: '',
  dest_kind: KIND_K8S,
  dest_node_type: 'K8sService',
  dest_name: '',
  dest_namespace: '',
  dest_cluster: '',
  dest_arn: '',
  dest_account_id: '',
  dest_region: '',
  dest_picker_account_id: '',
  dest_picker_node_type: '',
  dest_picked_node_id: '',
  relationship_type: 'CALLS',
  notes: '',
};

// Sentinel for the "no account selected" choice in the Account ID dropdown.
// Account ID is an optional qualifier; this option lets the operator clear a
// previously-set value back to "any account".
const ACCOUNT_ANY_VALUE = '__any__';

// Formats a cloud_accounts row into a Select option. Stored `value` is the
// provider-native identifier (account_number when present, falling back to
// the row UUID) since that's what the backend resolver matches against. The
// label includes name + provider + account_number for searchability.
const formatAccountOption = (acc) => {
  const identifier = acc.account_number || acc.id;
  const name = acc.account_name || identifier;
  const provider = acc.cloud_provider || '';
  const parts = [name];
  if (provider) {
    parts.push(provider);
  }
  if (acc.account_number && acc.account_number !== name) {
    parts.push(acc.account_number);
  }
  return { value: identifier, label: parts.join(' · ') };
};

// Translates between the form's `_account_id` storage (empty string = "Any
// account") and the Select's value (sentinel for "Any" so the dropdown can
// distinguish it from "no option selected"). Pulled out so source + dest
// columns stay symmetric.
const accountToSelectValue = (stored) => (stored ? stored : ACCOUNT_ANY_VALUE);
const selectValueToAccount = (next) => (next === ACCOUNT_ANY_VALUE ? '' : next);

const EditManualDependencyDialog = ({ open, row, onClose, onSaved }) => {
  const isEdit = Boolean(row?.id);
  const [form, setForm] = useState(BLANK);
  const [submitting, setSubmitting] = useState(false);
  const [cloudAccounts, setCloudAccounts] = useState([]);

  // Fetch the tenant's active cloud accounts once per dialog open so we can
  // populate the Account ID dropdown with real options instead of asking the
  // operator to remember UUIDs / AWS account numbers.
  useEffect(() => {
    if (!open) {
      return;
    }
    let cancelled = false;
    apiKnowledgeGraph
      .getCloudAccounts()
      .then((res) => {
        if (cancelled) {
          return;
        }
        const rows = res?.data?.data?.cloud_accounts?.rows ?? [];
        // Newest-first sort mirrors the Coverage tab so the lists feel
        // consistent across the two surfaces.
        const sorted = [...rows].sort((a, b) => {
          const ta = a.created_at ? Date.parse(a.created_at) : 0;
          const tb = b.created_at ? Date.parse(b.created_at) : 0;
          return tb - ta;
        });
        setCloudAccounts(sorted);
      })
      .catch((err) => {
        console.error('Failed to load cloud accounts for account selector:', err);
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  // Build the Select options. First entry is the "Any account" clear option;
  // each cloud account renders as "<name> · <provider> · <account_number>"
  // so an operator can search by any of those (Select auto-enables search
  // past 8 options per its built-in threshold).
  //
  // Stored value is the cloud account_number — the backend resolver matches
  // against query_attributes->>'aws_account_number' on the KG nodes, so the
  // provider-native identifier is what the filter actually needs.
  const accountOptions = useMemo(() => {
    return [{ value: ACCOUNT_ANY_VALUE, label: 'Any account' }, ...cloudAccounts.map(formatAccountOption)];
  }, [cloudAccounts]);

  // Sync the form with the row when opening in edit mode, or reset when
  // opening fresh. Keyed on `open` so closing-then-reopening always gets a
  // clean baseline.
  useEffect(() => {
    if (!open) {
      return;
    }
    if (row) {
      // Edit mode never exposes KIND_KG_PICK — fall back to derived kind
      // (K8S or CLOUD) so the operator sees the identifier-editing form.
      setForm({
        source_kind: deriveKind(row.source_node_type),
        source_node_type: row.source_node_type ?? 'K8sService',
        source_name: row.source_name ?? '',
        source_namespace: row.source_namespace ?? '',
        source_cluster: row.source_cluster ?? '',
        source_arn: row.source_arn ?? '',
        source_account_id: row.source_account_id ?? '',
        source_region: row.source_region ?? '',
        source_picker_account_id: '',
        source_picker_node_type: '',
        source_picked_node_id: '',
        dest_kind: deriveKind(row.dest_node_type),
        dest_node_type: row.dest_node_type ?? 'K8sService',
        dest_name: row.dest_name ?? '',
        dest_namespace: row.dest_namespace ?? '',
        dest_cluster: row.dest_cluster ?? '',
        dest_arn: row.dest_arn ?? '',
        dest_account_id: row.dest_account_id ?? '',
        dest_region: row.dest_region ?? '',
        dest_picker_account_id: '',
        dest_picker_node_type: '',
        dest_picked_node_id: '',
        relationship_type: row.relationship_type ?? 'CALLS',
        notes: row.notes ?? '',
      });
    } else {
      setForm(BLANK);
    }
  }, [open, row]);

  const updateField = (field) => (next) => setForm((prev) => ({ ...prev, [field]: next }));

  // Switching the kind toggle on one side does three things atomically:
  //   1. Updates the kind itself.
  //   2. Resets node_type to a sensible default for the new kind (since the
  //      previous value won't appear in the new dropdown).
  //   3. Clears the qualifier fields that belong to the OLD kind so stale
  //      values don't silently round-trip to the backend resolver.
  //
  // For KIND_KG_PICK: clear ALL identifier fields + picker scratch state.
  // The picker will refill them when a node is chosen. Leaving stale K8s
  // qualifiers around would confuse the projected-from-picked view.
  const updateKind = (prefix) => (nextKind) =>
    setForm((prev) => {
      if (prev[`${prefix}_kind`] === nextKind) {
        return prev;
      }
      const updated = {
        ...prev,
        [`${prefix}_kind`]: nextKind,
      };
      if (nextKind === KIND_KG_PICK) {
        updated[`${prefix}_node_type`] = '';
        updated[`${prefix}_name`] = '';
        updated[`${prefix}_namespace`] = '';
        updated[`${prefix}_cluster`] = '';
        updated[`${prefix}_arn`] = '';
        updated[`${prefix}_account_id`] = '';
        updated[`${prefix}_region`] = '';
        updated[`${prefix}_picker_account_id`] = '';
        updated[`${prefix}_picker_node_type`] = '';
        updated[`${prefix}_picked_node_id`] = '';
      } else {
        updated[`${prefix}_node_type`] = defaultNodeTypeForKind(nextKind);
        // Clear picker scratch when leaving kg-pick so a subsequent
        // re-entry starts fresh.
        updated[`${prefix}_picker_account_id`] = '';
        updated[`${prefix}_picker_node_type`] = '';
        updated[`${prefix}_picked_node_id`] = '';
        if (nextKind === KIND_K8S) {
          updated[`${prefix}_arn`] = '';
          updated[`${prefix}_account_id`] = '';
          updated[`${prefix}_region`] = '';
        } else {
          updated[`${prefix}_namespace`] = '';
          updated[`${prefix}_cluster`] = '';
        }
      }
      return updated;
    });

  // Project a picked KG node's identifiers onto the per-side form fields.
  // The picked_node_id is the ground truth — these projected identifiers
  // exist only so the row payload is meaningful (and so the operator can
  // switch Kind back to K8s/Cloud and see what was picked).
  const onPickNode = (prefix) => (node) =>
    setForm((prev) => {
      if (!node) {
        return {
          ...prev,
          [`${prefix}_picked_node_id`]: '',
          [`${prefix}_node_type`]: '',
          [`${prefix}_name`]: '',
          [`${prefix}_namespace`]: '',
          [`${prefix}_cluster`]: '',
          [`${prefix}_arn`]: '',
          [`${prefix}_region`]: '',
        };
      }
      return {
        ...prev,
        [`${prefix}_picked_node_id`]: node.id,
        [`${prefix}_node_type`]: node.node_type || '',
        [`${prefix}_name`]: node.name || '',
        [`${prefix}_namespace`]: node.namespace || '',
        [`${prefix}_cluster`]: node.cluster || '',
        [`${prefix}_arn`]: node?.properties?.arn || '',
        [`${prefix}_region`]: node?.properties?.region || '',
      };
    });

  const updatePickerField = (field) => (next) => setForm((prev) => ({ ...prev, [field]: next }));

  // Save button gating: in KG-pick mode the picked_node_id replaces the
  // "node_type + name" requirement (both are auto-projected). In K8s/Cloud
  // mode the backend's four required fields apply.
  const canSubmit = useMemo(() => {
    const sideOk = (prefix) => {
      if (form[`${prefix}_kind`] === KIND_KG_PICK) {
        return Boolean(form[`${prefix}_picked_node_id`]);
      }
      return Boolean(form[`${prefix}_node_type`] && form[`${prefix}_name`]);
    };
    return sideOk('source') && sideOk('dest');
  }, [form]);

  const handleSubmit = async () => {
    if (!canSubmit) {
      return;
    }
    setSubmitting(true);
    try {
      // Strip picker scratch fields before sending — backend only knows
      // about the resolved identifier columns. picked_node_id is forwarded
      // separately to the resolve step below.
      const payload = { ...form };
      delete payload.source_picker_account_id;
      delete payload.source_picker_node_type;
      delete payload.source_picked_node_id;
      delete payload.dest_picker_account_id;
      delete payload.dest_picker_node_type;
      delete payload.dest_picked_node_id;
      delete payload.source_kind;
      delete payload.dest_kind;

      const res = isEdit
        ? await apiKnowledgeGraph.updateManualDependency({ id: row.id, dependency: payload })
        : await apiKnowledgeGraph.createManualDependency(payload);
      const errors = res?.data?.errors;
      if (errors?.length) {
        snackbar.error((isEdit ? 'Update failed: ' : 'Create failed: ') + (errors[0]?.message ?? 'Unknown error'));
        return;
      }

      // Two-step pin for KG-pick: Create returned a row id; call
      // kg_resolve_manual_dependency to pin the picked UUID(s) over the
      // resolver's best-guess. SetResolvedNodes handles partial pin
      // (picked one side, identifiers on the other) per Round-1 fix.
      if (!isEdit) {
        const createdId =
          res?.data?.data?.kg_create_manual_dependency?.data?.row?.id ?? res?.data?.data?.kg_create_manual_dependency?.data?.id ?? null;
        const sourcePin = form.source_kind === KIND_KG_PICK ? form.source_picked_node_id : '';
        const destPin = form.dest_kind === KIND_KG_PICK ? form.dest_picked_node_id : '';
        if (createdId && (sourcePin || destPin)) {
          const pinRes = await apiKnowledgeGraph.resolveManualDependency({
            id: createdId,
            sourceNodeId: sourcePin || undefined,
            destinationNodeId: destPin || undefined,
          });
          const pinErrors = pinRes?.data?.errors;
          if (pinErrors?.length) {
            snackbar.error('Created, but pinning to picked node failed: ' + (pinErrors[0]?.message ?? 'Unknown error'));
            // Fall through to onSaved — the row exists, operator can fix
            // via the Resolve panel.
          }
        }
      }

      snackbar.success(isEdit ? 'Declaration updated.' : 'Declaration created.');
      onSaved();
    } catch (err) {
      console.error('Save manual dependency failed:', err);
      snackbar.error(isEdit ? 'Update failed.' : 'Create failed.');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal width='md' title={isEdit ? 'Edit Manual Dependency' : 'New Manual Dependency'} open={open} handleClose={onClose} onClose={onClose}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, p: 2 }}>
        <Typography sx={{ fontSize: '12px', color: ds?.text?.secondaryDark ?? '#6b7280', lineHeight: 1.5 }}>
          Declare one service-to-service dependency. Pick the <strong>kind</strong> on each side — <strong>Kubernetes</strong> or{' '}
          <strong>Cloud</strong> to type identifiers, or <strong>Pick from KG</strong> to browse existing nodes via account / type / node dropdowns
          and pin the row directly. <strong>Cloud</strong> covers AWS, Azure, and GCP; Resource ID accepts an ARN, an Azure resource ID, or a GCP
          self-link. Cross-stack pairs (k8s → cloud) work as long as the cloud resource is in the Knowledge Graph.
        </Typography>

        <Box sx={{ display: 'flex', gap: 3, alignItems: 'flex-start' }}>
          <EndpointColumn
            label='Source'
            fields={form}
            updateField={updateField}
            updateKind={updateKind}
            updatePickerField={updatePickerField}
            onPickNode={onPickNode('source')}
            prefix='source'
            accountOptions={accountOptions}
            cloudAccounts={cloudAccounts}
            isEdit={isEdit}
          />
          <Divider orientation='vertical' flexItem sx={{ alignSelf: 'stretch' }} />
          <EndpointColumn
            label='Destination'
            fields={form}
            updateField={updateField}
            updateKind={updateKind}
            updatePickerField={updatePickerField}
            onPickNode={onPickNode('dest')}
            prefix='dest'
            accountOptions={accountOptions}
            cloudAccounts={cloudAccounts}
            isEdit={isEdit}
          />
        </Box>

        <Box sx={{ display: 'flex', gap: 2 }}>
          <Box sx={{ flex: '0 0 220px' }}>
            <FieldLabel text='Relationship' tooltip={TOOLTIPS.relationship} />
            <Select
              size='sm'
              options={RELATIONSHIP_OPTIONS}
              value={form.relationship_type}
              onChange={(next) => updateField('relationship_type')(next)}
            />
          </Box>
          <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
            <FieldLabel text='Notes (optional)' tooltip={TOOLTIPS.notes} />
            <Input size='sm' placeholder='Reason for declaration / context for future readers' value={form.notes} onChange={updateField('notes')} />
          </Box>
        </Box>

        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1, pt: 0.5 }}>
          <Button tone='secondary' size='md' onClick={onClose} disabled={submitting}>
            Cancel
          </Button>
          <Button tone='primary' size='md' onClick={handleSubmit} disabled={!canSubmit || submitting} loading={submitting}>
            {isEdit ? 'Save' : 'Create'}
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

EditManualDependencyDialog.propTypes = {
  open: PropTypes.bool.isRequired,
  row: PropTypes.object,
  onClose: PropTypes.func.isRequired,
  onSaved: PropTypes.func.isRequired,
};

// Renders one endpoint (source OR dest). `prefix` selects which side's keys
// in the form state — keeps the column declarative. The Kind toggle at top
// switches between three modes:
//   - Kubernetes — type k8s identifiers (namespace, cluster).
//   - Cloud — type cloud identifiers (resource ID, account, region). AWS / Azure / GCP.
//   - Pick from KG — cascading dropdowns (account → type → node) for direct picking.
//     Create-only; edit mode hides this button. On submit the picked UUID is
//     pinned via kg_resolve_manual_dependency so the row resolves first try.
const EndpointColumn = ({ label, fields, updateField, updateKind, updatePickerField, onPickNode, prefix, accountOptions, cloudAccounts, isEdit }) => {
  const get = (suffix) => fields[`${prefix}_${suffix}`] ?? '';
  const set = (suffix) => updateField(`${prefix}_${suffix}`);
  const kind = fields[`${prefix}_kind`] ?? KIND_K8S;
  const nodeTypeOptions = kind === KIND_K8S ? K8S_NODE_TYPE_OPTIONS : CLOUD_NODE_TYPE_OPTIONS;
  return (
    <Box sx={{ flex: '1 1 0', minWidth: 0, display: 'flex', flexDirection: 'column', gap: 1.25 }}>
      <Typography sx={{ fontSize: '13px', fontWeight: 600, color: ds?.text?.secondary ?? '#374151' }}>{label}</Typography>

      <Box>
        <FieldLabel text='Kind' tooltip={TOOLTIPS.kind} />
        <Box sx={{ display: 'inline-flex', gap: 0.5, flexWrap: 'wrap' }}>
          <Button tone={kind === KIND_K8S ? 'primary' : 'secondary'} size='xs' onClick={() => updateKind(prefix)(KIND_K8S)}>
            Kubernetes
          </Button>
          <Button tone={kind === KIND_CLOUD ? 'primary' : 'secondary'} size='xs' onClick={() => updateKind(prefix)(KIND_CLOUD)}>
            Cloud
          </Button>
          {!isEdit && (
            <Button tone={kind === KIND_KG_PICK ? 'primary' : 'secondary'} size='xs' onClick={() => updateKind(prefix)(KIND_KG_PICK)}>
              Pick from KG
            </Button>
          )}
        </Box>
      </Box>

      {kind === KIND_KG_PICK ? (
        <KgNodePicker
          pickedAccountId={get('picker_account_id')}
          pickedNodeType={get('picker_node_type')}
          pickedNodeId={get('picked_node_id')}
          cloudAccounts={cloudAccounts}
          onAccountChange={(next) => {
            updatePickerField(`${prefix}_picker_account_id`)(next);
            updatePickerField(`${prefix}_picker_node_type`)('');
            updatePickerField(`${prefix}_picked_node_id`)('');
          }}
          onNodeTypeChange={(next) => {
            updatePickerField(`${prefix}_picker_node_type`)(next);
            updatePickerField(`${prefix}_picked_node_id`)('');
          }}
          onPick={onPickNode}
        />
      ) : (
        <>
          <Box>
            <FieldLabel text='Node type *' tooltip={TOOLTIPS.nodeType} />
            <Select size='sm' options={nodeTypeOptions} value={get('node_type')} onChange={(next) => set('node_type')(next)} />
          </Box>
          <Box>
            <FieldLabel text='Name *' tooltip={TOOLTIPS.name} />
            <Input size='sm' placeholder={kind === KIND_K8S ? 'e.g. payment-svc' : 'e.g. orders-db'} value={get('name')} onChange={set('name')} />
          </Box>

          {kind === KIND_K8S ? (
            <>
              <Box>
                <FieldLabel text='Namespace' tooltip={TOOLTIPS.namespace} />
                <Input size='sm' placeholder='e.g. prod' value={get('namespace')} onChange={set('namespace')} />
              </Box>
              <Box>
                <FieldLabel text='Cluster' tooltip={TOOLTIPS.cluster} />
                <Input size='sm' placeholder='e.g. us-east-1' value={get('cluster')} onChange={set('cluster')} />
              </Box>
            </>
          ) : (
            <>
              <Box>
                <FieldLabel text='Resource ID' tooltip={TOOLTIPS.resourceId} />
                <Input
                  size='sm'
                  placeholder='arn:aws:… | /subscriptions/…/providers/… | //compute.googleapis.com/projects/…'
                  value={get('arn')}
                  onChange={set('arn')}
                />
              </Box>
              <Box sx={{ display: 'flex', gap: 1 }}>
                <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
                  <FieldLabel text='Cloud account' tooltip={TOOLTIPS.cloudAccount} />
                  <Select
                    size='sm'
                    options={accountOptions}
                    value={accountToSelectValue(get('account_id'))}
                    onChange={(next) => set('account_id')(selectValueToAccount(next))}
                  />
                </Box>
                <Box sx={{ flex: '1 1 0', minWidth: 0 }}>
                  <FieldLabel text='Region' tooltip={TOOLTIPS.region} />
                  <Input size='sm' placeholder='us-east-1 | eastus | us-central1' value={get('region')} onChange={set('region')} />
                </Box>
              </Box>
            </>
          )}
        </>
      )}
    </Box>
  );
};

EndpointColumn.propTypes = {
  label: PropTypes.string.isRequired,
  fields: PropTypes.object.isRequired,
  updateField: PropTypes.func.isRequired,
  updateKind: PropTypes.func.isRequired,
  updatePickerField: PropTypes.func.isRequired,
  onPickNode: PropTypes.func.isRequired,
  prefix: PropTypes.oneOf(['source', 'dest']).isRequired,
  accountOptions: PropTypes.array.isRequired,
  cloudAccounts: PropTypes.array.isRequired,
  isEdit: PropTypes.bool.isRequired,
};

// Tiny helper for consistent field-label styling across the form. When
// `tooltip` is set, an info icon is rendered next to the label and on
// hover surfaces the description — keeps the form chrome compact while
// still self-documenting every field.
const FieldLabel = ({ text, tooltip }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, mb: 0.5 }}>
    <Typography sx={{ fontSize: '12px', fontWeight: 500, color: ds?.text?.secondaryDark ?? '#6b7280' }}>{text}</Typography>
    {tooltip && (
      <CustomTooltip title={tooltip} placement='top'>
        <InfoOutlinedIcon sx={{ fontSize: '14px', color: ds?.text?.secondaryDark ?? '#9ca3af', cursor: 'help' }} />
      </CustomTooltip>
    )}
  </Box>
);

FieldLabel.propTypes = {
  text: PropTypes.string.isRequired,
  tooltip: PropTypes.node,
};

export default EditManualDependencyDialog;
