import React from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Callout } from '@ui/Callout';
import { CodeBlock } from '@ui/CodeBlock';
import { ds } from 'src/utils/colors';
import { GATEWAY_MATCH_PROVIDER_OPTIONS, listGatewayModels } from '@api1/gateway-config';

// Mirrors gatewayEndpoints in api-server/services/user/token_policy.go and the families
// the gateway classifies in llm/gateway/proxy/policy.go.
export const ENDPOINT_OPTIONS = [
  { value: 'chat', label: 'Chat' },
  { value: 'responses', label: 'Responses' },
  { value: 'embeddings', label: 'Embeddings' },
  { value: 'conversations', label: 'Conversations' },
];

// "All" is an explicit choice in the endpoint picker, shown when no endpoint is picked, so
// an open key never looks like an empty selection.
const ALL_ENDPOINTS = 'all';
const ENDPOINT_TOGGLE_OPTIONS = [{ value: ALL_ENDPOINTS, label: 'All' }, ...ENDPOINT_OPTIONS];

// Picking an endpoint leaves "All"; picking "All" clears the endpoints. Unselecting the last
// endpoint keeps it selected: on an access control, widening the key takes a click on "All".
const nextEndpoints = (current, next) => {
  if (next.includes(ALL_ENDPOINTS) && current.length > 0) return [];
  const picked = next.filter((e) => e !== ALL_ENDPOINTS);
  return picked.length ? picked : current;
};

const PROVIDER_LABELS = Object.fromEntries(GATEWAY_MATCH_PROVIDER_OPTIONS.map((o) => [o.value, o.label]));

export const EMPTY_RESTRICTIONS = { providers: [], models: [], endpoints: [] };

// An empty selection means "any" in the picker; the API wants that as an omitted (null)
// list, since it rejects an empty one as "allow nothing".
export const toGatewayPolicy = (r) => ({
  providers: r.providers.length ? r.providers : null,
  models: r.models.length ? r.models : null,
  endpoints: r.endpoints.length ? r.endpoints : null,
});

export const fromGatewayPolicy = (p) => ({
  providers: p?.providers ?? [],
  models: p?.models ?? [],
  endpoints: p?.endpoints ?? [],
});

export const hasRestrictions = (r) => r.providers.length > 0 || r.models.length > 0 || r.endpoints.length > 0;

// One line per restricted dimension, for the token list.
export const describeGatewayPolicy = (p) => {
  const parts = [];
  if (p?.models?.length) parts.push(p.models.length === 1 ? p.models[0] : `${p.models.length} models`);
  if (p?.providers?.length) parts.push(p.providers.join(', '));
  if (p?.endpoints?.length) parts.push(p.endpoints.join(', '));
  return parts.join(' · ');
};

const isModelName = (v) => v.length > 0 && v.length <= 200 && !/\s/.test(v);

// Which providers, models and endpoint families an AI Gateway key may reach. Leaving a
// field empty keeps that dimension open.
export const GatewayRestrictionPicker = ({ value, onChange, subject = 'key' }) => {
  const [catalog, setCatalog] = React.useState(null);
  const [catalogFailed, setCatalogFailed] = React.useState(false);

  React.useEffect(() => {
    const ctrl = new AbortController();
    listGatewayModels(ctrl.signal)
      .then(setCatalog)
      .catch(() => {
        // An unmount (or StrictMode's dev re-run) aborts the request; that is not a failure.
        if (ctrl.signal.aborted) {
          return;
        }
        setCatalog([]);
        setCatalogFailed(true);
      });
    return () => ctrl.abort();
  }, []);

  const patch = (p) => onChange({ ...value, ...p });

  // Names already on the key stay selectable even if the catalog no longer lists them.
  const modelOptions = [...new Set([...(catalog ?? []).map((m) => m.id), ...value.models])].map((id) => ({ value: id, label: id }));
  // Every runtime provider lane is offered, so a key can be limited to one even when the
  // catalog did not load.
  const providerOptions = [
    ...new Set([...GATEWAY_MATCH_PROVIDER_OPTIONS.map((o) => o.value), ...(catalog ?? []).map((m) => m.provider), ...value.providers]),
  ]
    .filter(Boolean)
    .map((p) => ({ value: p, label: PROVIDER_LABELS[p] ?? p }));

  const endpointPicker = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>Endpoints</Typography>
      <ToggleGroup
        selection='multiple'
        size='sm'
        ariaLabel='Allowed endpoints'
        value={value.endpoints.length ? value.endpoints : [ALL_ENDPOINTS]}
        onChange={(next) => patch({ endpoints: nextEndpoints(value.endpoints, next) })}
        options={ENDPOINT_TOGGLE_OPTIONS}
        id='gateway-endpoint-toggle'
      />
    </Box>
  );

  // Grouped in a panel under the Access toggle so the fields read as what "Restricted"
  // means. Models lead: they are what most keys are narrowed by.
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[3],
        p: ds.space[3],
        border: `1px solid ${ds.gray[200]}`,
        borderRadius: ds.radius.md,
      }}
      data-testid='gateway-restriction-picker'
    >
      <Select
        multiple
        label='Models'
        placeholder='Any model'
        options={modelOptions}
        value={value.models}
        onChange={(models) => patch({ models })}
        loading={catalog === null}
        maxChips={3}
        instructionText={
          catalogFailed
            ? "Couldn't load your model catalog. Type a model name; a provider/model entry pins the model to that provider."
            : 'Pick from your catalog or type a name. A provider/model entry pins the model to that provider.'
        }
        onCreateOption={(v) => {
          if (!isModelName(v)) return false;
          patch({ models: [...value.models, v] });
        }}
      />
      {endpointPicker}
      <Select
        multiple
        label='Providers'
        placeholder='Any provider'
        options={providerOptions}
        value={value.providers}
        onChange={(providers) => patch({ providers })}
      />
      {!hasRestrictions(value) && (
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
          Pick at least one model, endpoint or provider to restrict this {subject}.
        </Typography>
      )}
      <Callout
        message={
          <>
            The gateway checks the model a request actually runs on, after aliases, substitution and failover. A tier alias such as{' '}
            <CodeBlock inline code='nb-smart' /> works only when the model it routes to is allowed. Restricted keys cannot call files, batches or
            fine-tuning endpoints.
          </>
        }
      />
    </Box>
  );
};

export const GATEWAY_ACCESS_OPTIONS = [
  { value: 'full', label: 'All models' },
  { value: 'restricted', label: 'Restricted' },
];

// Changes an existing AI Gateway key's restrictions. The gateway reads them on every
// request, so the change applies to the key's next call.
export const EditGatewayAccessDialog = ({ token, saving = false, onClose, onSave }) => {
  const initial = fromGatewayPolicy(token?.gateway_policy);
  const [access, setAccess] = React.useState(hasRestrictions(initial) ? 'restricted' : 'full');
  const [restrictions, setRestrictions] = React.useState(initial);

  const invalid = access === 'restricted' && !hasRestrictions(restrictions);
  const save = () => onSave(token, access === 'restricted' ? toGatewayPolicy(restrictions) : {});

  return (
    <Modal
      open={!!token}
      onClose={onClose}
      title={`Access for ${token?.name ?? ''}`}
      width='md'
      confirmText='Save'
      onConfirm={save}
      confirmDisabled={invalid}
      loader={saving}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
        <ToggleGroup
          selection='single'
          size='sm'
          ariaLabel='Key access'
          value={access}
          onChange={setAccess}
          options={GATEWAY_ACCESS_OPTIONS}
          id='edit-gateway-access-toggle'
        />
        {access === 'restricted' && <GatewayRestrictionPicker value={restrictions} onChange={setRestrictions} />}
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>Changes apply to the key&apos;s next request.</Typography>
      </Box>
    </Modal>
  );
};
