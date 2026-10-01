// The values the Manual Nodes CSV accepts in its type columns, read from
// kg_get_manual_node_schema so the list is always what the importer accepts:
// node_type outside a connected account, specific_type per connected cloud.

import { useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { csvTableSx } from './CsvImportDialog';
import { ds } from 'src/utils/colors';

const PROVIDER_LABELS = { K8s: 'Kubernetes' };

// The columns besides name a type's identity reads, e.g. "region, network".
const alsoNeeds = (kind) =>
  (kind.key_fields ?? [])
    .filter((f) => f.name !== 'name')
    .map((f) => (f.required ? f.name : `${f.name} (optional)`))
    .join(', ') || '—';

const byNodeTypeThenName = (a, b) => a.node_type.localeCompare(b.node_type) || a.specific_type.localeCompare(b.specific_type);

export const groupCsvTypeValues = (schema) => {
  const outside = (schema ?? []).find((p) => !p.cloud_account_id);
  const providers = new Map();
  for (const place of schema ?? []) {
    if (!place.cloud_account_id) {
      continue;
    }
    const kinds = providers.get(place.cloud_provider) ?? new Map();
    for (const k of place.kinds ?? []) {
      kinds.set(k.specific_type, k);
    }
    providers.set(place.cloud_provider, kinds);
  }
  return {
    outside: [...(outside?.kinds ?? [])].sort(byNodeTypeThenName),
    providers: [...providers.entries()]
      .map(([provider, kinds]) => ({ provider, kinds: [...kinds.values()].sort(byNodeTypeThenName) }))
      .sort((a, b) => a.provider.localeCompare(b.provider)),
  };
};

const Code = ({ children }) => <code style={{ fontSize: ds.text.caption }}>{children}</code>;
Code.propTypes = { children: PropTypes.node };

// One category per account type: outside any account, then each connected cloud.
export const csvTypeCategories = (schema) => {
  const { outside, providers } = groupCsvTypeValues(schema);
  const out = [];
  if (outside.length) {
    out.push({ key: 'outside', label: 'Not in an account', column: 'node_type', valueOf: (k) => k.node_type, kinds: outside });
  }
  for (const { provider, kinds } of providers) {
    out.push({ key: provider, label: PROVIDER_LABELS[provider] ?? provider, column: 'specific_type', valueOf: (k) => k.specific_type, kinds });
  }
  return out;
};

const ManualNodeCsvTypeValues = ({ schema }) => {
  const categories = useMemo(() => csvTypeCategories(schema), [schema]);
  const [activeKey, setActiveKey] = useState(null);
  if (!categories.length) {
    return null;
  }
  const active = categories.find((c) => c.key === activeKey) ?? categories[0];
  return (
    <Box
      data-testid='manual-nodes-csv-type-values'
      sx={{
        border: `1px solid ${ds.brand[200]}`,
        borderRadius: ds.radius.md,
        backgroundColor: ds.background[200],
        padding: `${ds.space.mul(0, 5)} ${ds.space[3]}`,
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[2],
      }}
    >
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], lineHeight: 1.5 }}>
        Values for the type columns, by account type. Outside a connected account set <Code>node_type</Code>; in a connected account set{' '}
        <Code>specific_type</Code> — only the clouds connected to this tenant are listed. &quot;Also needs&quot; are the other columns that decide the
        node&apos;s identity.
      </Typography>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }} role='group' aria-label='Account type'>
        {categories.map((c) => (
          <Button
            key={c.key}
            tone={c.key === active.key ? 'primary' : 'secondary'}
            size='xs'
            onClick={() => setActiveKey(c.key)}
            aria-pressed={c.key === active.key}
          >
            {`${c.label} (${c.kinds.length})`}
          </Button>
        ))}
      </Box>
      <Box sx={{ maxHeight: '280px', overflowY: 'auto' }}>
        <Box component='table' sx={csvTableSx}>
          <thead>
            <tr>
              <th>{active.column}</th>
              <th>Node type</th>
              <th>Also needs</th>
            </tr>
          </thead>
          <tbody>
            {active.kinds.map((k) => (
              <tr key={active.valueOf(k)}>
                <td>
                  <Code>{active.valueOf(k)}</Code>
                </td>
                <td>{k.node_type}</td>
                <td>{alsoNeeds(k)}</td>
              </tr>
            ))}
          </tbody>
        </Box>
      </Box>
    </Box>
  );
};

ManualNodeCsvTypeValues.propTypes = {
  // kg_get_manual_node_schema: one entry per place a node can be declared.
  schema: PropTypes.array,
};

export default ManualNodeCsvTypeValues;
