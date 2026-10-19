import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { Checkbox } from '@ui/Checkbox';
import Tabs from '@shared/navigation/Tabs';
import Text from '@shared/format/Text';
import { ds } from 'src/utils/colors';
import { integrationGrantLabel } from '@lib/integrationProviderGrants';

// Module × {Read,Write,Execute} permission grid, shared by the custom-role editor
// (ee RolePermissions) and the API-key scope picker (CreateTokenDialog). Both
// select `<module>:<Class>` keys from the same catalog (@lib/permissionCatalog), so
// one grid keeps a box meaning the same thing in both places.

export const CLASSES = ['Read', 'Write', 'Execute'];
export const permKey = (module, cls) => `${module}:${cls}`;

// snake_case action name → Title Case, e.g. `events_list_all` → `Events List All`.
const toTitleCase = (name) =>
  name
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');

// Scope tabs for the permission matrix. Only one scope's grants are visible at a
// time, so each tab carries the number of grants selected under it — otherwise
// ticks made on the hidden tab would look lost. `count` renders as a chip;
// Tabs treats 0 as "no chip", which is what we want for an untouched scope.
export function ScopeTabs({ groups, value, onChange, counts, idPrefix, ariaLabel }) {
  const tabOptions = groups.map((g) => ({
    value: g.scope,
    text: g.tabLabel,
    count: counts?.[g.scope] ?? 0,
    id: `${idPrefix}-${g.scope}`,
  }));
  return <Tabs options={{ tabOptions }} value={value} onChange={onChange} behavior='filter' variant='secondary' smallSize ariaLabel={ariaLabel} />;
}

ScopeTabs.propTypes = {
  groups: PropTypes.arrayOf(PropTypes.shape({ scope: PropTypes.string, tabLabel: PropTypes.string })).isRequired,
  value: PropTypes.string.isRequired,
  onChange: PropTypes.func.isRequired,
  counts: PropTypes.object,
  idPrefix: PropTypes.string.isRequired,
  ariaLabel: PropTypes.string.isRequired,
};

// Reusable module × {Read,Write,Execute} checkbox grid for one scope group.
export function formatModuleName(name) {
  const providerLabel = integrationGrantLabel(name);
  if (providerLabel) return providerLabel;
  if (name.startsWith('ai_')) {
    const suffix = name.slice(3);
    const overrides = {
      kbs: 'Knowledge Bases',
      guardrails: 'Guardrails',
      rca: 'RCA',
      misc: 'Miscellaneous',
    };
    const titleCase = overrides[suffix] || suffix.charAt(0).toUpperCase() + suffix.slice(1);
    return `AI ${titleCase}`;
  }

  const overrides = {
    usergroups: 'User Groups',
    userroles: 'User Roles',
    customroles: 'Custom Roles',
    // config_* all route to WORKFLOW_SERVER_URL — these configure Automations,
    // not general tenant settings, so the bare module name reads too broad.
    // "Workflow" is the internal name for the feature the product calls
    // Automations; the label follows the product, the module key does not.
    config: 'Automation Config',
    workflows: 'Automations',
    messagingplatforms: 'Messaging Platforms',
    // llm_* are all llm_gateway_* — the AI Gateway admin surface (routing rules,
    // tiers, rate limits, data-privacy settings), not "LLM" in general.
    llm: 'AI Gateway',
    egressfilter: 'Egress Filter',
    k8s: 'Kubernetes (K8s)',
    aws: 'AWS',
    gcp: 'GCP',
    autooptimize: 'Auto Optimize',
    alertmanager: 'Alert Manager',
    upgradeplans: 'Upgrade Plans',
  };

  if (overrides[name]) return overrides[name];

  return name.charAt(0).toUpperCase() + name.slice(1);
}

// Tooltip body: what ticking this (module, class) actually allows. `description`
// is the action's actions.yaml `comment:` where it has one, else the raw action
// name (see permissionCatalog.buildCatalog). Every action is listed — a long
// list (`events:Read` covers 16) scrolls inside the tooltip rather than being
// truncated, so nothing is hidden.
function GrantActionList({ actions }) {
  return (
    <Box component='ul' sx={{ m: 0, pl: 'var(--ds-space-4)', maxHeight: '260px', overflowY: 'auto' }}>
      {actions.map((a) => (
        <li key={a.name}>
          <Box component='span' sx={{ fontWeight: ds.weight.semibold }}>
            [{toTitleCase(a.name)}]
          </Box>{' '}
          {a.description}
        </li>
      ))}
    </Box>
  );
}

GrantActionList.propTypes = {
  actions: PropTypes.arrayOf(PropTypes.shape({ name: PropTypes.string, description: PropTypes.string })).isRequired,
};

// Box tooltip body for a class with a note (a grant only a run checks): the
// note, then the actions the class also covers, if any.
function GrantNote({ note, actions }) {
  return (
    <Box>
      <Box component='p' sx={{ m: 0, mb: actions.length ? 'var(--ds-space-1)' : 0 }}>
        {note}
      </Box>
      {actions.length > 0 && <GrantActionList actions={actions} />}
    </Box>
  );
}

GrantNote.propTypes = {
  note: PropTypes.string.isRequired,
  actions: PropTypes.arrayOf(PropTypes.shape({ name: PropTypes.string, description: PropTypes.string })).isRequired,
};

const DEFAULT_COLUMNS = CLASSES.map((cls) => ({ cls, label: cls }));

// Write requires Read — Write is disabled until Read is granted for the module.
// A module with no Read class has nothing to wait for, and a module whose
// classes are separate rights (integration grants: manage vs use) does not
// couple them; a prerequisite such a row needs is ticked by the editor instead.
const isCellDisabled = (entry, cls, selected) =>
  cls === 'Write' && !entry.independentClasses && entry.classes.includes('Read') && !selected.has(permKey(entry.module, 'Read'));

// `columns` orders and names the class columns ([{ cls, label, tip? }]); a scope
// can say "Manage" where the grant is a Write, and `tip` explains the column in
// a header tooltip. Defaults to Read / Write / Execute.
//
// `onSetColumn(cls, modules, on)` opts in to a "Select all" row under the
// header, one box per column. A box acts on the rows passed in (the caller's search-filtered list),
// skipping rows without the class and disabled cells, and hands the caller the
// modules to set so it can apply its own toggle rules in one update.
// `scope` names the header boxes' test ids (perm-col-<scope>-<Class>).
export function PermissionMatrix({
  entries,
  selected,
  toggle,
  columns = DEFAULT_COLUMNS,
  onSetColumn,
  scope = 'all',
  emptyMessage = 'No modules in this group.',
}) {
  if (!entries.length) return <Text value={emptyMessage} />;
  // Wide enough for the longest column label on one line.
  const gridTemplateColumns = `1fr repeat(${columns.length}, ${onSetColumn ? '100px' : '90px'})`;
  const columnTargets = (cls) => entries.filter((e) => e.classes.includes(cls) && !isCellDisabled(e, cls, selected)).map((e) => e.module);
  const rowSx = {
    display: 'grid',
    gridTemplateColumns,
    alignItems: 'center',
    gap: 'var(--ds-space-2)',
    padding: 'var(--ds-space-1) var(--ds-space-3)',
    borderBottom: `1px solid ${ds.border?.subtle ?? 'var(--ds-gray-100)'}`,
  };
  return (
    <Box
      sx={{
        mt: 'var(--ds-space-2)',
        // The matrix scrolls inside this box rather than growing the modal — the
        // account scope alone has ~49 modules, and the built-in-role summary sits
        // below it, so an unbounded matrix would push that off screen.
        maxHeight: '280px',
        overflow: 'auto',
        border: `1px solid ${ds.border?.default ?? 'var(--ds-gray-200)'}`,
        borderRadius: 'var(--ds-radius-md)',
      }}
    >
      {/* Header styled to match CustomTable's: the tinted teal bar with a top +
          one-step-darker bottom inset rule. Kept as a grid row rather than moving
          the matrix to CustomTable because CustomTable auto-paginates past 10
          rows, and the account scope alone has ~49 modules — paging a permission
          matrix would hide grants behind page 2. */}
      {/* Header and the select-all row stick together, so the column boxes stay
          reachable while the list scrolls. */}
      <Box sx={{ position: 'sticky', top: 0, zIndex: 1, background: ds.background?.[100] ?? ds.gray[100] }}>
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns,
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            padding: 'var(--ds-space-2) var(--ds-space-3)',
            background: ds.teal[50],
            boxShadow: `inset 0 1px 0 0 ${ds.teal[100]}, inset 0 -1px 0 0 ${ds.teal[200]}`,
            // Longhand rather than the `font` shorthand: a shorthand built from
            // var() substitutions is dropped wholesale if any part fails to
            // resolve. Same properties CustomTable sets on its own header th.
            fontWeight: 'var(--ds-font-weight-medium)',
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body)',
            lineHeight: '17px',
            color: ds.gray[700],
          }}
        >
          <Box component='span'>Module</Box>
          {columns.map(({ cls, label, tip }) => (
            <Box key={cls} sx={{ display: 'flex', justifyContent: 'center', whiteSpace: 'nowrap' }}>
              {tip ? (
                <Tooltip variant='explainer' title={label} desc={tip}>
                  <Box component='span' data-testid={`perm-col-tip-${scope}-${cls}`}>
                    {label}
                  </Box>
                </Tooltip>
              ) : (
                <Box component='span'>{label}</Box>
              )}
            </Box>
          ))}
        </Box>
        {onSetColumn && (
          // Select-all, one box per column centred under it like the cells below.
          // It acts on the visible rows that have the class and are enabled.
          <Box sx={rowSx}>
            <Text value='Select all' secondaryText />
            {columns.map(({ cls, label }) => {
              const targets = columnTargets(cls);
              const ticked = targets.filter((m) => selected.has(permKey(m, cls))).length;
              const all = targets.length > 0 && ticked === targets.length;
              return (
                <Box key={cls} sx={{ display: 'flex', justifyContent: 'center' }}>
                  <Checkbox
                    checked={all}
                    indeterminate={ticked > 0 && !all}
                    disabled={targets.length === 0}
                    onChange={() => onSetColumn(cls, targets, !all)}
                    aria-label={`Select all ${label}`}
                    data-testid={`perm-col-${scope}-${cls}`}
                  />
                </Box>
              );
            })}
          </Box>
        )}
      </Box>
      {entries.map((entry) => (
        <Box key={entry.module} sx={rowSx}>
          <Text value={formatModuleName(entry.module)} />
          {columns.map(({ cls: c, label }) => {
            const grantActions = entry.actions?.[c] ?? [];
            const note = entry.notes?.[c];
            const disabled = isCellDisabled(entry, c, selected);
            const checkbox = (
              <Checkbox
                checked={selected.has(permKey(entry.module, c))}
                onChange={() => toggle(entry.module, c)}
                disabled={disabled}
                // The module name is only in the row's first cell, so without an
                // explicit name every checkbox in the grid announces as an
                // unnamed "checkbox" to a screen reader.
                aria-label={`${formatModuleName(entry.module)} — ${label}`}
                data-testid={`perm-${entry.module}-${c}`}
              />
            );
            return (
              <Box key={c} sx={{ display: 'flex', justifyContent: 'center' }}>
                {!entry.classes.includes(c) ? (
                  <Text value='—' />
                ) : grantActions.length === 0 && !note ? (
                  checkbox
                ) : (
                  <Tooltip
                    variant='explainer'
                    title={grantActions.length ? `${label} — ${grantActions.length} ${grantActions.length === 1 ? 'action' : 'actions'}` : label}
                    desc={note ? <GrantNote note={note} actions={grantActions} /> : <GrantActionList actions={grantActions} />}
                  >
                    {/* Anchor on a span: Tooltip clones its child with a ref, which the DS Checkbox doesn't forward. */}
                    <Box component='span' sx={{ display: 'inline-flex' }} data-testid={`perm-tip-${entry.module}-${c}`}>
                      {checkbox}
                    </Box>
                  </Tooltip>
                )}
              </Box>
            );
          })}
        </Box>
      ))}
    </Box>
  );
}
