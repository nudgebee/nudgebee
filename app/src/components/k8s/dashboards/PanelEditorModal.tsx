import React, { useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import { isCommandDatasource, type AccountOption, type Panel, type PanelDatasource, type PanelType } from '@api1/dashboards';
import EntityQueryBuilder from './EntityQueryBuilder';
import { buildEntityQuery, defaultDraft, draftFromQuery, tablesFor, type EntityQueryDraft } from './entityQuery';
import { accountsOfTypes, coversAllOfTypes, deriveAccountTypes, panelScopeFromTypes } from './panelAccounts';
import { referencedVariables } from './templating';

interface Props {
  open: boolean;
  panel: Panel | null;
  /** Every account the author may point this panel at, across providers. */
  accountOptions: AccountOption[];
  onClose: () => void;
  /**
   * May be async — the dashboard view writes the panel straight to the server.
   * The modal shows its loader for as long as the returned promise is pending.
   */
  onSave: (panel: Panel) => void | Promise<void>;
}

const PANEL_TYPES: { label: string; value: PanelType }[] = [
  { label: 'Time series', value: 'timeseries' },
  { label: 'Stat', value: 'stat' },
  { label: 'Table', value: 'table' },
  { label: 'Bar', value: 'bar' },
  { label: 'Text', value: 'text' },
];

const DATASOURCES: { label: string; value: PanelDatasource }[] = [
  { label: 'Metrics', value: 'metrics' },
  { label: 'Logs', value: 'logs' },
  { label: 'Traces', value: 'traces' },
  { label: 'Redis', value: 'redis' },
  { label: 'RabbitMQ', value: 'rabbitmq' },
  { label: 'PostgreSQL', value: 'postgresql' },
  { label: 'Nudgebee (events)', value: 'nudgebee' },
];

/**
 * What each command datasource accepts. The server holds the authoritative
 * allowlist — this is here so the author doesn't have to discover it by being
 * rejected.
 */
const COMMAND_HELP: Record<string, { placeholder: string; allowed: string; example: string }> = {
  redis: {
    placeholder: 'INFO memory',
    allowed:
      'INFO, DBSIZE, PING, LASTSAVE, TIME, ROLE, CONFIG GET, CLIENT LIST/INFO, MEMORY STATS/DOCTOR/USAGE, CLUSTER INFO/NODES, LATENCY LATEST/HISTORY, SLOWLOG GET/LEN, COMMAND COUNT/DOCS',
    example: 'INFO replication',
  },
  rabbitmq: {
    placeholder: 'list queues name messages consumers',
    allowed: 'list, show',
    example: 'list queues name messages',
  },
  postgresql: {
    placeholder: 'SELECT state, count(*) FROM pg_stat_activity GROUP BY state',
    allowed:
      'a single read-only statement starting with SELECT, WITH, SHOW, EXPLAIN, TABLE or VALUES. No writes anywhere in it (including data-modifying CTEs), no ";", no double quotes — single quotes for values are fine',
    example: "SELECT state, count(*) FROM pg_stat_activity WHERE state = 'active' GROUP BY state",
  },
};

const WIDTHS = [3, 4, 6, 8, 12].map((w) => ({ label: `${w} / 12`, value: String(w) }));

const PanelEditorModal: React.FC<Props> = ({ open, panel, accountOptions, onClose, onSave }) => {
  const [draft, setDraft] = useState<Panel | null>(panel);
  /**
   * Account types are editor-local, not panel state. They do double duty — they
   * filter the account picker AND, on their own, mean "every account of these
   * providers". `panelScopeFromTypes` collapses the two controls into the shape
   * the backend stores, so the panel never holds both.
   *
   * A list rather than a string because a `nudgebee` panel may legitimately span
   * providers: the query engine takes account ids and does not resolve a
   * per-provider integration the way metrics, logs and the command datasources
   * do. Every other datasource renders this same state as a single select.
   */
  const [accountTypes, setAccountTypes] = useState<string[]>([]);
  const [accountIds, setAccountIds] = useState<string[]>([]);
  /**
   * The entity builder's own state. Editor-local like the account controls: the
   * panel stores the compiled query, and reopening it reads the draft back out
   * of that rather than keeping a second copy on the panel.
   */
  const [entityDraft, setEntityDraft] = useState<EntityQueryDraft>(defaultDraft());
  /**
   * A save can be a round trip (the dashboard view persists the panel as soon
   * as it is saved), so the modal holds the in-flight state itself rather than
   * leaving the author looking at an unchanged form wondering if it took.
   */
  const [saving, setSaving] = useState(false);

  // Re-seed when a different panel is opened.
  React.useEffect(() => {
    setDraft(panel);
    setAccountTypes(panel ? deriveAccountTypes(panel, accountOptions) : []);
    // A panel naming every account of its providers IS "all of those providers",
    // which the types control already says. Echoing it in the account picker
    // would read as a hand-picked list nobody chose.
    setAccountIds(panel && coversAllOfTypes(panel, accountOptions) ? [] : panel?.account_ids || []);
    setEntityDraft(draftFromQuery(panel?.targets?.[0]?.query));
  }, [panel, accountOptions]);

  const accountTypeOptions = useMemo(() => {
    const seen = new Set<string>();
    for (const o of accountOptions) {
      if (o.cloud_provider) seen.add(o.cloud_provider);
    }
    return [...seen].sort().map((t) => ({ label: t, value: t }));
  }, [accountOptions]);

  // The account picker lists only the chosen providers' accounts — an unfiltered
  // list mixes clusters with cloud accounts and is unreadable past a handful.
  // Across several providers the label carries the provider too, since two
  // accounts called "prod" in different clouds are otherwise the same row twice.
  const accountsForTypes = useMemo(
    () =>
      accountsOfTypes(accountTypes, accountOptions).map((o) => ({
        label: accountTypes.length > 1 && o.cloud_provider ? `${o.label} (${o.cloud_provider})` : o.label,
        value: o.value,
      })),
    [accountOptions, accountTypes]
  );

  const expr = draft?.targets?.[0]?.expr || '';
  const templateVars = useMemo(() => referencedVariables(expr), [expr]);

  if (!draft) return null;

  const patch = (next: Partial<Panel>) => setDraft((prev) => (prev ? { ...prev, ...next } : prev));

  // Every edit rewrites target A in place, preserving the fields it does not
  // touch (legend_format, hide, …).
  const patchTarget = (next: Partial<NonNullable<Panel['targets']>[number]>) =>
    setDraft((prev) =>
      prev
        ? {
            ...prev,
            targets: [{ ref_id: prev.targets?.[0]?.ref_id || 'A', ...(prev.targets?.[0] || {}), ...next }],
          }
        : prev
    );

  const changeAccountTypes = (next: string[]) => {
    setAccountTypes(next);
    // Selections from a provider that is no longer chosen would be invisible in
    // the filtered list yet still scope the panel, so drop them.
    const kept = new Set(accountsOfTypes(next, accountOptions).map((o) => o.value));
    setAccountIds((prev) => prev.filter((id) => kept.has(id)));
  };

  /**
   * Changing the data source clears the query.
   *
   * Every data source speaks its own language — PromQL, Lucene, `INFO memory`,
   * `list queues` — so a query carried across is never valid in the new one. It
   * survived as far as save, where the server rejected a Redis command on a
   * RabbitMQ panel; losing the text is the cheaper failure.
   *
   * A command datasource also forces the table visualisation: it returns a
   * snapshot of text, which the chart types cannot draw and the server refuses.
   */
  const changeDatasource = (next: string) => {
    const datasource = next as PanelDatasource;
    const entity = tablesFor(datasource).length > 0;
    // Logs and entity queries are rows, like the command datasources.
    const tabular = isCommandDatasource(datasource) || entity || datasource === 'logs';
    // Reset the builder alongside the target. Without this, switching away from
    // nudgebee and back would show the filters from before the switch while the
    // stored query held the defaults.
    const entityStart = defaultDraft(datasource);
    if (entity) setEntityDraft(entityStart);
    // Only `nudgebee` reads across providers, so leaving a second one selected
    // on the way out would save a scope the single control cannot show — the
    // field would read "AWS" while the panel quietly queried AWS and GCP.
    if (datasource !== 'nudgebee') changeAccountTypes(accountTypes.slice(0, 1));
    setDraft((prev) =>
      prev
        ? {
            ...prev,
            datasource,
            type: tabular ? 'table' : prev.type,
            // A nudgebee panel is authored as a structured query, so it starts
            // from a working default rather than an empty one.
            targets: entity
              ? [{ ref_id: 'A', query: buildEntityQuery(entityStart) as any, time_column: entityStart.timeColumn }]
              : [{ ...(prev.targets?.[0] || {}), ref_id: prev.targets?.[0]?.ref_id || 'A', expr: '', query: undefined, time_column: undefined }],
          }
        : prev
    );
  };

  const commandHelp = COMMAND_HELP[draft.datasource];
  // Traces read the same query engine as events, just a different pair of
  // tables — one builder, two vocabularies.
  const entityTables = tablesFor(draft.datasource);
  const isEntity = entityTables.length > 0;
  const isLogs = draft.datasource === 'logs';
  const isText = draft.type === 'text';
  /**
   * Only a `nudgebee` panel may name several providers at once. The query engine
   * takes a list of account ids and resolves nothing per-provider; metrics, logs,
   * traces and the command datasources each resolve ONE integration from the
   * account, so a second provider there is not a wider query, it is a broken one.
   */
  const multiType = draft.datasource === 'nudgebee';
  const hasScope = accountTypes.length > 0 || accountIds.length > 0;
  const canSave = draft.title.trim().length > 0 && (isText || (hasScope && (isEntity ? Boolean(draft.targets?.[0]?.query) : expr.trim().length > 0)));
  // A panel that has been saved always carries a title (`canSave` demands one),
  // so a titled panel is one being edited rather than a blank being authored.
  const isEdit = Boolean(panel?.title);

  const handleSave = async () => {
    setSaving(true);
    try {
      await onSave({ ...draft, ...panelScopeFromTypes(accountTypes, accountIds, accountOptions) });
    } finally {
      // Also on failure: the parent keeps the modal open so the author can fix
      // whatever the server rejected, and a stuck loader would block that.
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      // Dismissing mid-save would land the result on a closed modal, and the
      // write is already on its way — there is nothing to cancel.
      handleClose={saving ? undefined : onClose}
      backdropClickClose={!saving}
      loader={saving}
      title={isEdit ? 'Edit panel' : 'Add panel'}
      width='md'
      actionButtons={
        <Stack direction='row' gap='12px' sx={{ button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={onClose} disabled={saving} id='panel-cancel-btn'>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={!canSave || saving} loading={saving} id='panel-save-btn' data-testid='panel-save-btn'>
            {isEdit ? 'Update panel' : 'Save panel'}
          </Button>
        </Stack>
      }
    >
      <Form variant='stacked' density='default'>
        <Form.Section>
          <Form.Field label='Title' required>
            <Input value={draft.title} onChange={(v) => patch({ title: v })} placeholder='p99 request latency' />
          </Form.Field>
          <Form.Field label='Description'>
            <Input value={draft.description || ''} onChange={(v) => patch({ description: v })} placeholder='Optional — shown on hover' />
          </Form.Field>
        </Form.Section>

        {!isText && (
          <>
            <Form.Section title='Query' divider>
              <Form.Field label='Data source'>
                <Select value={draft.datasource} options={DATASOURCES} onChange={changeDatasource} />
              </Form.Field>

              {isEntity ? (
                <EntityQueryBuilder
                  draft={entityDraft}
                  tables={entityTables}
                  onChange={(next, query, timeColumn) => {
                    setEntityDraft(next);
                    patchTarget({ query: query as any, time_column: timeColumn, expr: undefined });
                  }}
                />
              ) : commandHelp ? (
                <>
                  <Form.Field
                    label={draft.datasource === 'postgresql' ? 'Query' : 'Command'}
                    required
                    description={`Runs against this account's ${draft.datasource} integration. Read-only only — the credentials and connection flags are added by the server.`}
                  >
                    <Input value={expr} onChange={(v) => patchTarget({ expr: v })} placeholder={commandHelp.placeholder} />
                  </Form.Field>
                  <Box sx={{ p: 1.5, border: `1px solid ${ds.gray[300]}`, background: ds.background[200], borderRadius: '6px' }}>
                    <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                      Allowed: {commandHelp.allowed}. The result is a snapshot — this panel ignores the dashboard&apos;s time range. Example:{' '}
                      <Box component='code' sx={{ fontFamily: 'monospace' }}>
                        {commandHelp.example}
                      </Box>
                    </Typography>
                  </Box>
                </>
              ) : isLogs ? (
                <Form.Field
                  label='Query'
                  required
                  // The language depends on the account's log provider (LogQL
                  // for Loki, Lucene for Elasticsearch, …) — the server resolves
                  // that per account, so no single syntax can be promised here.
                  description="In this account's log provider syntax — LogQL for Loki, Lucene for Elasticsearch, and so on."
                >
                  <Input value={expr} onChange={(v) => patchTarget({ expr: v })} placeholder='{namespace="$namespace"} |= "error"' />
                </Form.Field>
              ) : (
                <Form.Field label='Query' required>
                  <Input
                    value={expr}
                    onChange={(v) => patchTarget({ expr: v })}
                    placeholder='sum(rate(http_requests_total{namespace="$namespace"}[5m]))'
                  />
                </Form.Field>
              )}
              {templateVars.length > 0 && (
                <Box sx={{ p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: '6px' }}>
                  <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                    {commandHelp ? (
                      <>
                        This command references {templateVars.map((v) => `$${v}`).join(', ')}, and will be rejected on save. A command runs through a
                        shell on the target, where <code>$name</code> is a shell variable — so <code>$</code> is refused outright rather than being
                        substituted. Use a literal value here; variables work on metrics, logs and trace panels.
                      </>
                    ) : (
                      <>
                        This query references {templateVars.map((v) => `$${v}`).join(', ')}. Those are filled in only when the dashboard is opened
                        from a page that supplies them (a workload or pod detail page). Anywhere else the query runs against the literal text.
                      </>
                    )}
                  </Typography>
                </Box>
              )}
            </Form.Section>

            {/* After the query, not before it: the data source decides whether
                this asks for one provider or several, so the question does not
                exist until it has been answered. It also reads in the order the
                panel is actually authored — what to fetch, then from where.

                Accounts are per panel, not per dashboard: two panels on one
                dashboard may query different accounts. The type filters the
                account list, and leaving Accounts empty charts every account of
                that provider. */}
            <Form.Section
              title='Accounts'
              description={
                multiType
                  ? 'Choose the providers to read across, or narrow to specific accounts. Findings are one query over whatever you pick, so several providers is a wider answer rather than a broken one.'
                  : 'Choose a provider to chart all of its accounts, or narrow to specific ones.'
              }
              divider
            >
              <Form.Row ratio={[1, 1]}>
                <Form.Field label={multiType ? 'Account types' : 'Account type'} required>
                  {/* Single vs multiple is the datasource's call, not the
                      author's: only the query engine reads across providers. The
                      single case wraps the same list state so nothing else has
                      to know which control is on screen. */}
                  {multiType ? (
                    <Select
                      multiple
                      value={accountTypes}
                      options={accountTypeOptions}
                      onChange={changeAccountTypes}
                      placeholder='Select…'
                      id='panel-account-type-select'
                    />
                  ) : (
                    <Select
                      value={accountTypes[0] || ''}
                      options={accountTypeOptions}
                      onChange={(next: string) => changeAccountTypes(next ? [next] : [])}
                      placeholder='Select…'
                      id='panel-account-type-select'
                    />
                  )}
                </Form.Field>
                {/* The guidance is an info icon, not a description line: a
                    description under one field of a Form.Row pushes its control
                    down and misaligns it against the other. */}
                <Form.Field
                  label={
                    <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                      Accounts
                      <Tooltip
                        title={multiType ? 'Leave empty for all accounts of the chosen providers' : 'Leave empty for all accounts of this type'}
                      >
                        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', color: ds.gray[400], cursor: 'help' }}>
                          <InfoOutlinedIcon sx={{ fontSize: 13 }} />
                        </Box>
                      </Tooltip>
                    </Box>
                  }
                >
                  <Select
                    multiple
                    value={accountIds}
                    options={accountsForTypes}
                    onChange={setAccountIds}
                    disabled={accountTypes.length === 0}
                    placeholder={accountTypes.length > 0 ? `All ${accountTypes.join(', ')} accounts` : 'Select…'}
                    id='panel-account-select'
                  />
                </Form.Field>
              </Form.Row>
            </Form.Section>
          </>
        )}

        {isText && (
          <Form.Section title='Content' divider>
            <Form.Field label='Text'>
              <Input value={draft.content || ''} onChange={(v) => patch({ content: v })} placeholder='Free text shown in the panel' />
            </Form.Field>
          </Form.Section>
        )}

        {/* Below the query, not above it: you decide how to draw a result after
            you know what the result is. Outside the !isText block so a Text
            panel can still be switched back to a chart. */}
        <Form.Section title='Display' divider>
          <Form.Row ratio={[1, 1]}>
            <Form.Field label='Visualisation'>
              <Select
                value={draft.type}
                // Command datasources and entity queries both return rows, which
                // the chart types cannot draw and the server refuses.
                options={commandHelp || isEntity || isLogs ? PANEL_TYPES.filter((t) => t.value === 'table') : PANEL_TYPES}
                onChange={(v: string) => patch({ type: v as PanelType })}
              />
            </Form.Field>
            <Form.Field label='Width'>
              <Select
                value={String(draft.grid_pos?.w || 12)}
                options={WIDTHS}
                onChange={(v: string) => patch({ grid_pos: { ...draft.grid_pos, w: Number(v) } })}
              />
            </Form.Field>
          </Form.Row>
        </Form.Section>
      </Form>
    </Modal>
  );
};

export default PanelEditorModal;
