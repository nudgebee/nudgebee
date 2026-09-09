/**
 * ModelAliasList — structured editor for the gateway integration's `models` field.
 *
 * The `models` config is stored/validated as one comma-joined string of entries that
 * are either a bare served id (`google/gemma-4-26b-a4b-it-maas`) or an `alias=served`
 * pair (`nb-fast=google/gemma-...`). Editing that as raw text is confusing — the alias
 * and the served model live in one string joined by an easily-missed `=`, so someone
 * changing the model can silently drop the alias. This renders each entry as an explicit
 * two-column row (client-facing name → served model) and serializes back to the exact
 * same string, so the backend contract is unchanged.
 *
 * Rendered by DynamicForm when a string field carries `widget: "model_alias_list"`.
 */
import * as React from 'react';
import { Box, Stack } from '@mui/material';
import AddIcon from '@mui/icons-material/Add';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import { Input } from '@ui/Input';
import { Button as DsButton } from '@ui/Button';
import { ds } from '@utils/colors';

interface ModelAliasListProps {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}

interface Row {
  name: string; // client-facing alias; empty means "same as served"
  served: string; // model id sent upstream
}

// parseModels turns the stored comma string into editable rows. A bare entry (no `=`)
// has an empty name (alias == served); `alias=served` splits on the FIRST `=` to match
// the backend's strings.Cut, so a served id that itself contains `=` is preserved.
export function parseModels(value: string): Row[] {
  return (value || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((entry) => {
      const eq = entry.indexOf('=');
      if (eq === -1) return { name: '', served: entry };
      return { name: entry.slice(0, eq).trim(), served: entry.slice(eq + 1).trim() };
    });
}

// serializeModels drops rows with no served model (a half-typed row isn't persisted) and
// collapses a row whose name is empty or equal to the served id back to a bare entry, so
// the output is identical to what a user would have typed by hand.
export function serializeModels(rows: Row[]): string {
  return rows
    .map((r) => ({ name: r.name.trim(), served: r.served.trim() }))
    .filter((r) => r.served)
    .map((r) => (!r.name || r.name === r.served ? r.served : `${r.name}=${r.served}`))
    .join(', ');
}

export default function ModelAliasList({ value, onChange, disabled }: ModelAliasListProps) {
  // Local rows are the source of truth while editing — re-parsing `value` on every render
  // would drop a row the moment its served field is briefly empty (mid-typing). Seed once
  // from the incoming value; every edit updates rows AND pushes the serialized string up.
  const [rows, setRows] = React.useState<Row[]>(() => {
    const parsed = parseModels(value);
    return parsed.length ? parsed : [{ name: '', served: '' }];
  });

  const apply = (next: Row[]) => {
    setRows(next);
    onChange(serializeModels(next));
  };

  const setCell = (i: number, key: keyof Row, v: string) => apply(rows.map((r, idx) => (idx === i ? { ...r, [key]: v } : r)));
  const addRow = () => apply([...rows, { name: '', served: '' }]);
  const removeRow = (i: number) => {
    const next = rows.filter((_, idx) => idx !== i);
    apply(next.length ? next : [{ name: '', served: '' }]);
  };

  return (
    <Stack spacing={ds.space[2]} sx={{ width: '100%' }}>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr 32px',
          gap: ds.space[2],
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-500)',
          fontWeight: 'var(--ds-font-weight-semibold)',
        }}
      >
        <span>Client model name</span>
        <span>Served model (sent to provider)</span>
        <span />
      </Box>

      {rows.map((row, i) => (
        <Box key={i} sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr 32px', gap: ds.space[2], alignItems: 'center' }}>
          <Input
            value={row.name}
            onChange={(v: string) => setCell(i, 'name', v)}
            size='sm'
            disabled={disabled}
            placeholder='optional, e.g. gemini-fast'
            data-testid={`model-alias-name-${i}`}
          />
          <Input
            value={row.served}
            onChange={(v: string) => setCell(i, 'served', v)}
            size='sm'
            disabled={disabled}
            placeholder='e.g. google/gemma-4-26b-a4b-it-maas'
            data-testid={`model-alias-served-${i}`}
          />
          <DsButton
            tone='ghost'
            size='sm'
            composition='icon-only'
            icon={<DeleteOutlineIcon sx={{ fontSize: 16 }} />}
            aria-label='Remove model'
            disabled={disabled || (rows.length === 1 && !row.name && !row.served)}
            onClick={() => removeRow(i)}
            data-testid={`model-alias-remove-${i}`}
          />
        </Box>
      ))}

      <Box>
        <DsButton
          tone='secondary'
          size='sm'
          icon={<AddIcon sx={{ fontSize: 16 }} />}
          disabled={disabled}
          onClick={addRow}
          data-testid='model-alias-add'
        >
          Add model
        </DsButton>
      </Box>

      <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
        Add as many mappings as this account needs. Leave the client name blank to call the served model directly. These mappings are additive and do
        not restrict other models available through the provider account.
      </Box>
    </Stack>
  );
}
