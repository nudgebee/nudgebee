import React, { useEffect, useMemo, useState } from 'react';
import { Stack } from '@mui/material';
import { fillBrandTokens } from '@hooks/useTenantBranding';
import type { DashboardVariable, Panel } from '@api1/dashboards';
import { Button } from '@ui/Button';
import { Callout } from '@ui/Callout';
import { Checkbox } from '@ui/Checkbox';
import { Input } from '@ui/Input';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { ds } from '@utils/colors';
import { isCustomVariable, isNudgebeeVariable, variableColumns, variableErrors, variableTables, variableUses } from './dashboardVariables';

interface Props {
  open: boolean;
  variables: DashboardVariable[];
  /** The panels on screen, draft edits included, to report which of them use each variable. */
  panels?: Panel[];
  onClose: () => void;
  onApply: (variables: DashboardVariable[]) => void;
}

const TYPE_OPTIONS = [
  { value: 'query', label: 'From a query' },
  // `{brand}` is filled at render: a literal here would latch the house brand.
  { value: 'nudgebee', label: 'From {brand}' },
  { value: 'custom', label: 'Fixed list' },
];

const EMPTY: DashboardVariable = { name: '', type: 'query', query: '', label_key: '' };

/** Only the fields of the chosen type, so switching type leaves nothing stale behind. */
function cleaned(v: DashboardVariable): DashboardVariable {
  const base = {
    name: v.name.trim(),
    ...(v.label?.trim() ? { label: v.label.trim() } : {}),
    ...(v.multi ? { multi: true } : {}),
    ...(v.all_value?.trim() ? { all_value: v.all_value.trim() } : {}),
  };
  if (isCustomVariable(v)) {
    const values = (v.values || []).map((o) => o.trim()).filter(Boolean);
    return { ...base, type: 'custom', values };
  }
  if (isNudgebeeVariable(v)) return { ...base, type: 'nudgebee', table: v.table, column: v.column };
  return { ...base, type: 'query', query: (v.query || '').trim(), label_key: (v.label_key || '').trim() };
}

/** Which panels a variable reaches, since a dropdown only changes the ones whose query uses it. */
const VariableUsage: React.FC<{ name: string; panels: Panel[] }> = ({ name, panels }) => {
  const uses = useMemo(() => variableUses(panels, name), [panels, name]);
  if (uses.length === 0) return null;
  const of = (kind: string) => uses.filter((u) => u.kind === kind);
  const notUsed = of('unused').length;
  const reached = of('works').length + of('partial').length;
  const flagged = [...of('partial'), ...of('blocked')];
  return (
    <div data-testid={`dashboard-variable-usage-${name}`}>
      <Callout
        tone={reached === 0 || of('partial').length > 0 ? 'warning' : 'info'}
        message={
          <Stack gap={ds.space[1]}>
            <span>
              {reached === 0
                ? `No panel uses $${name} yet, so changing it won’t change anything. Add label=~"$${name}" to a panel query.`
                : `Used by ${reached} of ${uses.length} panels.`}
            </span>
            {of('works').map((u) => (
              <span key={u.panel}>✓ {u.panel}</span>
            ))}
            {flagged.map((u) => (
              <span key={u.panel}>
                {u.kind === 'partial' ? '⚠' : '✕'} {u.panel}: {u.reason}
              </span>
            ))}
            {reached > 0 && notUsed > 0 && (
              <span>
                {notUsed} {notUsed === 1 ? 'panel doesn’t' : 'panels don’t'} use it and won’t change.
              </span>
            )}
          </Stack>
        }
      />
    </div>
  );
};

/** Declares the dashboard's dropdowns. The result goes to the draft; Save stores it. */
const DashboardVariablesModal: React.FC<Props> = ({ open, variables, panels = [], onClose, onApply }) => {
  const [rows, setRows] = useState<DashboardVariable[]>(variables);
  const [errors, setErrors] = useState<string[]>([]);

  useEffect(() => {
    if (!open) return;
    setRows(variables);
    setErrors([]);
  }, [open, variables]);

  const patch = (index: number, change: Partial<DashboardVariable>) =>
    setRows((prev) => prev.map((row, i) => (i === index ? { ...row, ...change } : row)));

  const apply = () => {
    const next = rows.map(cleaned);
    const problems = variableErrors(next);
    setErrors(problems);
    if (problems.length === 0) onApply(next);
  };

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title='Dashboard variables'
      subtitle='Each one becomes a dropdown. It only changes panels whose query uses it, written as label=~"$name". Command panels can’t use variables.'
      width='lg'
      backdropClickClose={false}
      actionButtons={
        <Stack direction='row' gap={ds.space[3]} justifyContent='flex-end' sx={{ width: '100%' }}>
          <Button tone='secondary' onClick={onClose} id='dashboard-variables-cancel-btn'>
            Cancel
          </Button>
          <Button tone='primary' onClick={apply} id='dashboard-variables-apply-btn' data-testid='dashboard-variables-apply-btn'>
            Apply to draft
          </Button>
        </Stack>
      }
    >
      <Stack gap={ds.space[4]}>
        {rows.map((row, i) => (
          <Stack
            key={i}
            gap={ds.space[3]}
            data-testid={`dashboard-variable-row-${i}`}
            sx={{ padding: ds.space[4], border: `1px solid ${ds.gray[200]}`, borderRadius: ds.radius.lg }}
          >
            <Stack direction='row' gap={ds.space[3]}>
              <Input
                label='Name'
                help='Use it in panel queries as $name. Case-sensitive: letters, digits and underscores.'
                value={row.name}
                onChange={(name: string) => patch(i, { name })}
                placeholder='scale_set'
                required
              />
              <Input
                label='Label'
                help='The text on the dropdown. Leave blank to show the name.'
                value={row.label || ''}
                onChange={(label: string) => patch(i, { label })}
                placeholder='Scale set'
              />
              <Select
                label='Values'
                help='Where the dropdown gets its choices.'
                options={TYPE_OPTIONS.map((o) => ({ ...o, label: fillBrandTokens(o.label) }))}
                value={row.type || 'query'}
                onChange={(type: string) => patch(i, { type: type as DashboardVariable['type'] })}
              />
            </Stack>
            {isNudgebeeVariable(row) ? (
              <Stack direction='row' gap={ds.space[3]}>
                <Select
                  label='Table'
                  help='The data to read the choices from.'
                  options={variableTables().map((t) => ({ value: t.value, label: t.label }))}
                  value={row.table || ''}
                  onChange={(table: string) => patch(i, { table, column: '' })}
                />
                <Select
                  label='Column'
                  help='Follows the dashboard’s time range, except on tables of standing rows such as open recommendations.'
                  options={variableColumns(row.table).map((c) => ({ value: c.name, label: c.label }))}
                  value={row.column || ''}
                  onChange={(column: string) => patch(i, { column })}
                />
              </Stack>
            ) : isCustomVariable(row) ? (
              <Input
                label='Values, comma separated'
                help='The choices to offer, in this order.'
                value={(row.values || []).join(',')}
                onChange={(text: string) => patch(i, { values: text.split(',') })}
                placeholder='dev,test,prod'
              />
            ) : (
              <Stack direction='row' gap={ds.space[3]}>
                <Input
                  label='Query'
                  help='A metrics query that returns one series for each choice.'
                  value={row.query || ''}
                  onChange={(query: string) => patch(i, { query })}
                  placeholder='sum by (name) (gha_max_runners)'
                />
                <Input
                  label='Label to read'
                  help='The label in that query’s results that holds the choices.'
                  value={row.label_key || ''}
                  onChange={(label_key: string) => patch(i, { label_key })}
                  placeholder='name'
                />
              </Stack>
            )}
            <Input
              label='Value when All is selected (optional)'
              help='Sent when nothing is picked. Leave blank to match everything.'
              value={row.all_value || ''}
              onChange={(all_value: string) => patch(i, { all_value })}
              placeholder='.*'
            />
            <VariableUsage name={row.name.trim()} panels={panels} />
            <Stack direction='row' justifyContent='space-between' alignItems='center'>
              <Checkbox
                checked={Boolean(row.multi)}
                onChange={(multi: boolean) => patch(i, { multi })}
                label='Allow several values'
                description='Viewers can pick more than one. Panels need =~ to match them.'
              />
              <Button
                tone='secondary'
                size='sm'
                onClick={() => setRows((prev) => prev.filter((_, n) => n !== i))}
                id={`dashboard-variable-remove-${i}`}
              >
                Remove
              </Button>
            </Stack>
          </Stack>
        ))}
        <Callout
          tone='info'
          message='The value for All is sent as written when nothing is picked, so write it for your data source. To format the picks, use ${name:csv}, or regex, pipe, raw, singlequote, doublequote, sqlstring, json or lucene.'
        />
        <div>
          <Button
            tone='secondary'
            onClick={() => setRows((prev) => [...prev, { ...EMPTY }])}
            id='dashboard-variable-add-btn'
            data-testid='dashboard-variable-add-btn'
          >
            Add variable
          </Button>
        </div>
        {errors.map((error) => (
          <span key={error} role='alert' style={{ color: ds.red[600] }}>
            {error}
          </span>
        ))}
      </Stack>
    </Modal>
  );
};

export default DashboardVariablesModal;
