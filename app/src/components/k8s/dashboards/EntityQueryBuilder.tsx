import React from 'react';
import { Box, Stack, Typography } from '@mui/material';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Switch } from '@ui/Switch';
import Tooltip from '@ui/Tooltip';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import { grantTooltip, missingTableGrant } from './panelAccess';
import {
  buildEntityQuery,
  defaultDraft,
  filterableColumns,
  findTable,
  operatorTakesList,
  operatorTakesValue,
  operatorsFor,
  type EntityQuery,
  type EntityQueryDraft,
  type EntityTable,
} from './entityQuery';

interface Props {
  draft: EntityQueryDraft;
  /** Tables this panel's datasource may query — events, or traces. */
  tables: EntityTable[];
  onChange: (draft: EntityQueryDraft, query: EntityQuery, timeColumn: string) => void;
}

/**
 * Builds a query-engine read without exposing the engine.
 *
 * Columns and operators come from the same registry the server validates
 * against, so the dropdowns cannot offer something the engine will reject — the
 * reason this is a guided builder rather than a JSON box.
 *
 * Two filters are deliberately absent. The ACCOUNT filter comes from the
 * panel's own scope (and is clamped by RBAC server-side regardless), and the
 * TIME filter comes from the dashboard's picker; only the column it applies to
 * is authored here.
 */
const EntityQueryBuilder: React.FC<Props> = ({ draft, tables, onChange }) => {
  const table = findTable(draft.table);

  const emit = (next: EntityQueryDraft) => onChange(next, buildEntityQuery(next), next.applyTimeRange ? next.timeColumn : '');

  const changeTable = (value: string) => {
    // Columns, filters and sort all name columns of the previous table, and the
    // two event tables share almost none — start clean rather than silently
    // dropping half of them at build time.
    emit(defaultDraft(value));
  };

  const patch = (next: Partial<EntityQueryDraft>) => emit({ ...draft, ...next });

  const patchFilter = (index: number, next: Partial<(typeof draft.filters)[number]>) =>
    patch({ filters: draft.filters.map((f, i) => (i === index ? { ...f, ...next } : f)) });

  // A table the viewer's role cannot read is offered disabled rather than
  // hidden: the panel would only fail at render, and naming the grant is what
  // lets the author ask for the right one. Nothing is gated for a tenant admin
  // or any account user — see panelAccess.ts.
  const tableOptions = tables.map((t) => {
    const missing = missingTableGrant(t);
    if (!missing) return { label: t.label, value: t.value };
    return {
      value: t.value,
      disabled: true,
      label: (
        <Tooltip title={grantTooltip(missing)}>
          {/* A disabled MUI menu item sets `pointer-events: none`, which would
              swallow the hover the tooltip needs — this span opts back in. */}
          <Box component='span' sx={{ pointerEvents: 'auto', display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
            <LockOutlinedIcon sx={{ fontSize: 13, flexShrink: 0 }} />
            {t.label}
          </Box>
        </Tooltip>
      ),
    };
  });

  const columnOptions = table.columns.map((c) => ({ label: c.label, value: c.name }));
  // Traces filter through named API parameters, so only some columns can carry
  // a filter — offering the rest would build panels that ignore them.
  const filterColumns = filterableColumns(table);
  const filterColumnOptions = filterColumns.map((c) => ({ label: c.label, value: c.name }));

  return (
    <>
      <Form.Row ratio={[1, 1]}>
        <Form.Field label='Table'>
          <Select value={draft.table} options={tableOptions} onChange={changeTable} />
        </Form.Field>
        <Form.Field label='Rows'>
          <Input value={String(draft.limit)} onChange={(v: string) => patch({ limit: Number(v.replace(/\D/g, '')) || 0 })} />
        </Form.Field>
      </Form.Row>

      {/* Which of the two event tables to reach for is the one thing an author
          cannot work out from the column names, so it is spelled out rather
          than hidden in a tooltip. */}
      <Box sx={{ p: 1.5, border: `1px solid ${ds.gray[300]}`, background: ds.background[200], borderRadius: '6px' }}>
        <Typography variant='body2' sx={{ color: ds.gray[700], fontWeight: 600, mb: 0.25 }}>
          {table.label} — {table.description}
        </Typography>
        <Typography variant='body2' sx={{ color: ds.gray[600] }}>
          {table.detail}
        </Typography>
      </Box>

      <Form.Field label='Columns' required>
        <Select multiple value={draft.columns} options={columnOptions} onChange={(v: string[]) => patch({ columns: v })} id='entity-columns' />
      </Form.Field>

      <Form.Row ratio={[1, 1]}>
        <Form.Field label='Sort by'>
          <Select value={draft.sortColumn} options={columnOptions} onChange={(v: string) => patch({ sortColumn: v })} />
        </Form.Field>
        <Form.Field label='Order'>
          <Select
            value={draft.sortDesc ? 'desc' : 'asc'}
            options={[
              { label: 'Newest / highest first', value: 'desc' },
              { label: 'Oldest / lowest first', value: 'asc' },
            ]}
            onChange={(v: string) => patch({ sortDesc: v === 'desc' })}
          />
        </Form.Field>
      </Form.Row>

      {/* A row query has no inherent time axis. Without this the panel would
          quietly ignore the dashboard's time picker. */}
      <Form.Row ratio={[1, 1]}>
        <Form.Field label='Time column'>
          <Select
            value={draft.timeColumn}
            options={table.timeColumns.map((name) => ({ label: table.columns.find((c) => c.name === name)?.label || name, value: name }))}
            onChange={(v: string) => patch({ timeColumn: v })}
            disabled={!draft.applyTimeRange}
          />
        </Form.Field>
        <Form.Field label="Use the dashboard's time range">
          {/* Switch reports (event, checked) — the second argument is the state. */}
          <Switch checked={draft.applyTimeRange} onChange={(_e: any, checked: boolean) => patch({ applyTimeRange: checked })} />
        </Form.Field>
      </Form.Row>

      <Box>
        <Stack direction='row' alignItems='center' gap={1} sx={{ mb: 1 }}>
          <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 13, fontWeight: 600, color: ds.gray[700] }}>Filters</Typography>
          <Box sx={{ flex: 1 }} />
          <Button
            tone='secondary'
            size='sm'
            onClick={() =>
              patch({
                filters: [
                  ...draft.filters,
                  { column: filterColumns[0].name, operator: operatorsFor(table, filterColumns[0].name)[0].value, value: '' },
                ],
              })
            }
            id='add-entity-filter'
          >
            Add filter
          </Button>
        </Stack>

        {draft.filters.length === 0 ? (
          <Typography variant='caption' sx={{ color: ds.gray[500] }}>
            No filters — every row this account can see, newest first.
          </Typography>
        ) : (
          <Stack gap={1}>
            {draft.filters.map((filter, index) => {
              const operators = operatorsFor(table, filter.column);
              const operator = operators.some((o) => o.value === filter.operator) ? filter.operator : operators[0].value;
              return (
                <Stack key={index} direction='row' gap={1} alignItems='center'>
                  <Box sx={{ flex: 1.2 }}>
                    <Select
                      value={filter.column}
                      options={filterColumnOptions}
                      onChange={(v: string) => {
                        // The operator list is per column TYPE, so a carried-over
                        // operator can be one the new column does not support.
                        const next = operatorsFor(table, v);
                        patchFilter(index, { column: v, operator: next.some((o) => o.value === filter.operator) ? filter.operator : next[0].value });
                      }}
                    />
                  </Box>
                  <Box sx={{ flex: 1 }}>
                    <Select value={operator} options={operators} onChange={(v: string) => patchFilter(index, { operator: v })} />
                  </Box>
                  <Box sx={{ flex: 1.5 }}>
                    <Input
                      value={filter.value}
                      onChange={(v: string) => patchFilter(index, { value: v })}
                      disabled={!operatorTakesValue(operator)}
                      placeholder={operatorTakesList(operator) ? 'P0, P1' : operatorTakesValue(operator) ? 'value or $namespace' : '—'}
                    />
                  </Box>
                  <Button
                    tone='ghost'
                    composition='icon-only'
                    aria-label='Remove filter'
                    icon={<DeleteOutlineIcon sx={{ fontSize: 18 }} />}
                    onClick={() => patch({ filters: draft.filters.filter((_f, i) => i !== index) })}
                    id={`remove-entity-filter-${index}`}
                  />
                </Stack>
              );
            })}
          </Stack>
        )}
      </Box>
    </>
  );
};

export default EntityQueryBuilder;
