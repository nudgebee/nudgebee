import React, { useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import Tooltip from '@ui/Tooltip';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import { isCommandDatasource, type AccountOption, type Panel, type PanelDatasource, type PanelType } from '@api1/dashboards';
import EntityQueryBuilder from './EntityQueryBuilder';
import PanelPreview, { PREVIEW_RAIL_WIDTH, usePreviewRange } from './PanelPreview';
import { buildEntityQuery, defaultDraft, draftFromQuery, tablesFor, type EntityQueryDraft } from './entityQuery';
import { accountsOfTypes, coversAllOfTypes, deriveAccountTypes, panelScopeFromTypes } from './panelAccounts';
import { referencedVariables, type VariableValues } from './templating';

interface Props {
  open: boolean;
  panel: Panel | null;
  /** Every account the author may point this panel at, across providers. */
  accountOptions: AccountOption[];
  /** The host page's variables, so the preview substitutes `$namespace` as the dashboard will. */
  variables?: VariableValues;
  /** The range the preview queries; falls back to the last hour. */
  startTime?: number;
  endTime?: number;
  onClose: () => void;
  /**
   * May be async — the dashboard view writes the panel straight to the server, and
   * the modal shows its loader until the returned promise settles. What it resolves
   * WITH is ignored.
   */
  onSave: (panel: Panel) => unknown | Promise<unknown>;
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

/** What each command datasource accepts. */
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

/** A card's heading: what the group is, and why its fields are together. */
const GroupHeader: React.FC<{ title: string; description: string }> = ({ title, description }) => (
  <Box>
    <Typography
      sx={{
        fontFamily: 'var(--ds-font-display)',
        fontSize: 12,
        fontWeight: 700,
        letterSpacing: '0.03em',
        textTransform: 'uppercase',
        color: ds.gray[700],
      }}
    >
      {title}
    </Typography>
    <Typography variant='caption' sx={{ display: 'block', mt: '2px', color: ds.gray[500], fontWeight: 400 }}>
      {description}
    </Typography>
  </Box>
);

const PanelEditorModal: React.FC<Props> = ({ open, panel, accountOptions, variables, startTime, endTime, onClose, onSave }) => {
  const [draft, setDraft] = useState<Panel | null>(panel);
  /** Account types are editor-local, not panel state. */
  const [accountTypes, setAccountTypes] = useState<string[]>([]);
  const [accountIds, setAccountIds] = useState<string[]>([]);
  /** The entity builder's own state. */
  const [entityDraft, setEntityDraft] = useState<EntityQueryDraft>(defaultDraft());
  /**
   * A save can be a round trip (the dashboard view persists the panel as soon
   * as it is saved), so the modal holds the in-flight state itself rather than
   * leaving the author looking at an unchanged form wondering if it took.
   */
  const [saving, setSaving] = useState(false);
  const fallbackRange = usePreviewRange(open);

  // Re-seed when a different panel is opened.
  React.useEffect(() => {
    setDraft(panel);
    setAccountTypes(panel ? deriveAccountTypes(panel, accountOptions) : []);
    // A panel naming every account of its providers IS "all of those providers", which the types control
    // already says.
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

  // The account picker lists only the chosen providers' accounts — an unfiltered list mixes clusters with
  // cloud accounts and is unreadable past a handful.
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

  /** Changing the data source clears the query. */
  const changeDatasource = (next: string) => {
    const datasource = next as PanelDatasource;
    const entity = tablesFor(datasource).length > 0;
    // Logs and entity queries are rows, like the command datasources.
    const tabular = isCommandDatasource(datasource) || entity || datasource === 'logs';
    // Reset the builder alongside the target.
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
  /** Only a `nudgebee` panel may name several providers at once. */
  const multiType = draft.datasource === 'nudgebee';
  const hasScope = accountTypes.length > 0 || accountIds.length > 0;
  const canSave = draft.title.trim().length > 0 && (isText || (hasScope && (isEntity ? Boolean(draft.targets?.[0]?.query) : expr.trim().length > 0)));
  // A panel that has been saved always carries a title (`canSave` demands one),
  // so a titled panel is one being edited rather than a blank being authored.
  const isEdit = Boolean(panel?.title);

  /** The draft as it would be stored — the preview runs this and the save sends it. */
  const resolvedDraft: Panel = { ...draft, ...panelScopeFromTypes(accountTypes, accountIds, accountOptions) };

  const handleSave = async () => {
    setSaving(true);
    try {
      await onSave(resolvedDraft);
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
      width='lg'
      // Pinned height: the form column scrolls while the preview holds still.
      maxHeight='85vh'
      // Padding moves onto the columns so the divider runs full height; `display:
      // flex` gives them a real height to scroll within.
      contentStyles={{ padding: 0, overflow: 'hidden', display: 'flex' }}
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
      {/* Two columns where there is room: the form scrolls, the preview stays put.
          Below `md` it stacks and the whole body scrolls together. */}
      <Box
        sx={{
          flex: 1,
          minWidth: 0,
          display: 'flex',
          flexDirection: { xs: 'column', md: 'row' },
          alignItems: 'stretch',
          overflowY: { xs: 'auto', md: 'hidden' },
        }}
      >
        <Box
          sx={{
            flex: 1,
            minWidth: 0,
            overflowY: { xs: 'visible', md: 'auto' },
            padding: 'var(--ds-space-5) var(--ds-space-6)',
          }}
        >
          <Form variant='stacked' density='default'>
            <Stack gap={2}>
              {!isText && (
                <Card
                  variant='tinted'
                  header={
                    <GroupHeader
                      title='Source'
                      description={
                        multiType
                          ? 'Where the data comes from. A findings panel is one query over every provider you pick, so several is a wider answer rather than a broken one.'
                          : 'Where the data comes from, and how much of it to chart.'
                      }
                    />
                  }
                >
                  <Form.Section>
                    <Form.Field label='Data source'>
                      <Select value={draft.datasource} options={DATASOURCES} onChange={changeDatasource} />
                    </Form.Field>
                    <Form.Row ratio={[1, 1]}>
                      <Form.Field label={multiType ? 'Account types' : 'Account type'} required>
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
                </Card>
              )}

              <Card
                variant='tinted'
                header={
                  <GroupHeader
                    title={isText ? 'Content & display' : 'Query & display'}
                    description={isText ? 'What the panel says, and how it is rendered.' : 'What to ask, and how to render the answer.'}
                  />
                }
              >
                <Form.Section>
                  {!isText && (
                    <>
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
                                This command references {templateVars.map((v) => `$${v}`).join(', ')}, and will be rejected on save. A command runs
                                through a shell on the target, where <code>$name</code> is a shell variable — so <code>$</code> is refused outright
                                rather than being substituted. Use a literal value here; variables work on metrics, logs and trace panels.
                              </>
                            ) : (
                              <>
                                This query references {templateVars.map((v) => `$${v}`).join(', ')}. Those are filled in only when the dashboard is
                                opened from a page that supplies them (a workload or pod detail page). Anywhere else the query runs against the
                                literal text.
                              </>
                            )}
                          </Typography>
                        </Box>
                      )}
                    </>
                  )}

                  {isText && (
                    <Form.Field label='Text'>
                      <Input value={draft.content || ''} onChange={(v) => patch({ content: v })} placeholder='Free text shown in the panel' />
                    </Form.Field>
                  )}

                  <Form.Field label='Visualisation'>
                    <Select
                      value={draft.type}
                      options={commandHelp || isEntity || isLogs ? PANEL_TYPES.filter((t) => t.value === 'table') : PANEL_TYPES}
                      onChange={(v: string) => patch({ type: v as PanelType })}
                    />
                  </Form.Field>
                </Form.Section>
              </Card>

              {/* Last: you can only name a panel well once you know what it shows. */}
              <Card variant='tinted' header={<GroupHeader title='Panel details' description='How this panel appears to viewers.' />}>
                <Form.Section>
                  <Form.Field label='Title' required>
                    <Input value={draft.title} onChange={(v) => patch({ title: v })} placeholder='p99 request latency' />
                  </Form.Field>
                  <Form.Field label='Description'>
                    <Input value={draft.description || ''} onChange={(v) => patch({ description: v })} placeholder='Optional — shown on hover' />
                  </Form.Field>
                </Form.Section>
              </Card>
            </Stack>
          </Form>
        </Box>

        {/* Scrolls on its own so a long table cannot push the footer around. */}
        <Box
          sx={{
            width: { xs: '100%', md: PREVIEW_RAIL_WIDTH },
            flexShrink: 0,
            overflowY: 'auto',
            padding: 'var(--ds-space-5)',
            background: ds.background[200],
            borderLeft: { xs: 'none', md: `1px solid ${ds.gray[200]}` },
            borderTop: { xs: `1px solid ${ds.gray[200]}`, md: 'none' },
          }}
        >
          <PanelPreview
            panel={resolvedDraft}
            accountOptions={accountOptions}
            variables={variables || {}}
            startTime={startTime ?? fallbackRange.start}
            endTime={endTime ?? fallbackRange.end}
          />
        </Box>
      </Box>
    </Modal>
  );
};

export default PanelEditorModal;
