import React, { useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import Tooltip from '@ui/Tooltip';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import { Form } from '@shared/forms/Form';
import { ds } from '@utils/colors';
import { isCommandDatasource, type AccountOption, type Panel, type PanelColumn, type PanelDatasource, type PanelType } from '@api1/dashboards';
import EntityQueryBuilder from './EntityQueryBuilder';
import PanelPreview, { PREVIEW_RAIL_WIDTH, usePreviewRange } from './PanelPreview';
import { buildEntityQuery, defaultDraft, draftFromQuery, findTable, tablesFor, type EntityQueryDraft } from './entityQuery';
import { grantTooltip, missingDatasourceGrant, queryableTables } from './panelAccess';
import { isCompleteColumn, panelColumnsOf, referencedColumns, setHiddenColumns } from './panelColumns';
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

/**
 * The link picker's stand-in for "attach to nothing, add a column of my own".
 * A Select renders its PLACEHOLDER for an empty value, so the option cannot be
 * `''` — "New column" is a real choice, not the absence of one. Editor-local:
 * it is mapped back to an absent `column` before the panel is stored.
 */
const NEW_COLUMN = '__new_column__';

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

  /**
   * Each datasource reaches a different backend read behind a different grant,
   * so one the viewer's role cannot reach is offered disabled rather than
   * hidden: a hidden option reads as "this product cannot do that", a disabled
   * one names the grant to ask for. Nothing is gated for a tenant admin or any
   * account user — see panelAccess.ts.
   */
  const datasourceOptions = useMemo(
    () =>
      DATASOURCES.map((option) => {
        const missing = missingDatasourceGrant(option.value);
        if (!missing) return option;
        return {
          value: option.value,
          disabled: true,
          label: (
            <Tooltip title={grantTooltip(missing)}>
              {/* A disabled MUI menu item sets `pointer-events: none`, which
                  would swallow the hover the tooltip needs — this span opts
                  back in. */}
              <Box component='span' sx={{ pointerEvents: 'auto', display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
                <LockOutlinedIcon sx={{ fontSize: 13, flexShrink: 0 }} />
                {option.label}
              </Box>
            </Tooltip>
          ),
        };
      }),
    []
  );

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

  /**
   * Column settings live in one list on `options`, and an empty one is dropped
   * rather than saved as `[]` — a panel that configures no column should read as
   * a panel that has never heard of the feature.
   */
  const patchColumns = (next: PanelColumn[]) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const options: Record<string, unknown> = { ...(prev.options || {}), columns: next };
      if (next.length === 0) delete options.columns;
      // The two arrays this list replaced. Dropped on the next write, the same
      // way the server's upgradeDefinition drops them on read.
      delete options.link_columns;
      delete options.hidden_columns;
      return { ...prev, options };
    });

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
    const datasourceTables = tablesFor(datasource);
    const entity = datasourceTables.length > 0;
    // Logs and entity queries are rows, like the command datasources.
    const tabular = isCommandDatasource(datasource) || entity || datasource === 'logs';
    // Reset the builder alongside the target. Start on the first table the
    // viewer may actually read — the datasource's own first table would open the
    // editor on a greyed-out selection for a custom-role holder without that
    // grant. Falls back to it when none are readable, which is the honest state.
    const entityStart = defaultDraft(entity ? (queryableTables(datasourceTables)[0] || datasourceTables[0]).value : datasource);
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
            // Link and hidden columns name the previous table's columns, and the
            // query they came from has just been thrown away with them.
            options: undefined,
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
   * Panels whose rows are real records — the only ones a link column means
   * anything on. A metrics table is series-and-latest, which has no row to open.
   */
  const isRowTable = isEntity || Boolean(commandHelp) || isLogs;
  // The editor reads the list RAW: an author typing a path has an incomplete
  // entry, and the render-time filter would delete the row out from under them.
  const columns = panelColumnsOf(draft);
  const hiddenColumns = columns.filter((c) => c.visibility === 'hidden' && c.name).map((c) => c.name as string);
  // Links are the only column setting the editor authors today. A column's other
  // settings — a title override, a format override — are honoured by the
  // renderer and set by import or by hand until this grows fields for them.
  const links = columns.filter((c) => Boolean(c.link));
  /**
   * What a link can be attached to: an existing column, whose own values become
   * the links, or a new column of its own.
   *
   * Hidden columns are left out — a link on a column nobody can see is a link
   * nobody can click — and so is a column another link already claims, which the
   * server rejects as configured twice. Refusing to offer it beats saving it and
   * reading the refusal back as a failed round trip.
   */
  const linkTargetOptionsFor = (current: PanelColumn) => [
    { label: 'New column', value: NEW_COLUMN },
    ...entityDraft.columns
      .filter((name) => !hiddenColumns.includes(name))
      .filter((name) => name === current.name || !links.some((l) => l !== current && l.name === name))
      .map((name) => ({ label: findTable(entityDraft.table).columns.find((c) => c.name === name)?.label || name, value: name })),
  ];
  /**
   * Placeholders that name a column the query does not select. The link would
   * render as an empty cell, which looks like missing data rather than a typo.
   */
  const unknownPlaceholders = isEntity
    ? [...new Set(links.flatMap((l) => referencedColumns(l.link?.url || '')))].filter((c) => !entityDraft.columns.includes(c))
    : [];
  // Includes links that are merely unfinished — they block Save, so they have to
  // say so rather than leaving the button greyed out for no visible reason.
  const unfinishedLinks = links.filter((l) => !isCompleteColumn(l));
  /** Only a `nudgebee` panel may name several providers at once. */
  const multiType = draft.datasource === 'nudgebee';
  const hasScope = accountTypes.length > 0 || accountIds.length > 0;
  const canSave =
    draft.title.trim().length > 0 &&
    (isText || (hasScope && (isEntity ? Boolean(draft.targets?.[0]?.query) : expr.trim().length > 0))) &&
    // The server refuses these too; catching them here saves a round trip that
    // comes back as a message about a panel the author can no longer see.
    columns.every(isCompleteColumn);
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
                      <Select value={draft.datasource} options={datasourceOptions} onChange={changeDatasource} />
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
                            // Settings for a column the query no longer selects would come back
                            // to life the moment the author selects it again — drop them, but
                            // keep the added columns, which name no query column at all.
                            const kept = columns.filter((c) => !c.name || next.columns.includes(c.name));
                            if (kept.length !== columns.length) patchColumns(kept);
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

              {isRowTable && (
                <Card
                  variant='tinted'
                  header={
                    <GroupHeader
                      title='Table columns'
                      description='What the table shows, and where a row can take you — either by making a column clickable or by adding a column of links.'
                    />
                  }
                >
                  <Form.Section>
                    {isEntity && (
                      <Form.Field
                        label='Hidden columns'
                        description='Still queried, just not shown — hide the ids a link is built from so the table stays readable.'
                      >
                        <Select
                          multiple
                          value={hiddenColumns}
                          options={entityDraft.columns.map((name) => ({
                            label: findTable(entityDraft.table).columns.find((c) => c.name === name)?.label || name,
                            value: name,
                          }))}
                          onChange={(next: string[]) => patchColumns(setHiddenColumns(columns, next))}
                          placeholder='None'
                          id='panel-hidden-columns'
                        />
                      </Form.Field>
                    )}

                    <Box>
                      <Stack direction='row' alignItems='center' gap={1} sx={{ mb: 1 }}>
                        <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 13, fontWeight: 600, color: ds.gray[700] }}>
                          Links
                        </Typography>
                        <Box sx={{ flex: 1 }} />
                        <Button
                          tone='secondary'
                          size='sm'
                          onClick={() => patchColumns([...columns, { title: 'Investigate', link: { url: '' } }])}
                          id='add-link-column'
                        >
                          Add link
                        </Button>
                      </Stack>

                      {links.length === 0 ? (
                        <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                          No links — the table is text only.
                        </Typography>
                      ) : (
                        <Stack gap={1}>
                          {links.map((link) => {
                            const patchLink = (next: Partial<PanelColumn>) => patchColumns(columns.map((c) => (c === link ? { ...c, ...next } : c)));
                            return (
                              <Stack key={links.indexOf(link)} direction='row' gap={1} alignItems='center'>
                                {isEntity && (
                                  <Box sx={{ flex: 1.2 }}>
                                    <Select
                                      value={link.name || NEW_COLUMN}
                                      options={linkTargetOptionsFor(link)}
                                      // Switching target swaps which field names the link, so the
                                      // other one is cleared rather than left behind as dead config —
                                      // the server refuses a link that carries both. Clearing the
                                      // picker reads as New column, same as choosing it.
                                      onChange={(v: string) =>
                                        patchLink(
                                          v && v !== NEW_COLUMN
                                            ? { name: v, title: undefined }
                                            : { name: undefined, title: link.title || 'Investigate' }
                                        )
                                      }
                                    />
                                  </Box>
                                )}
                                {!link.name && (
                                  <Box sx={{ flex: 1 }}>
                                    <Input value={link.title || ''} onChange={(v: string) => patchLink({ title: v })} placeholder='Investigate' />
                                  </Box>
                                )}
                                <Box sx={{ flex: 2 }}>
                                  <Input
                                    value={link.link?.url || ''}
                                    onChange={(v: string) => patchLink({ link: { url: v } })}
                                    placeholder='/investigate?id={{id}}&accountId={{account_id}}'
                                  />
                                </Box>
                                <Button
                                  tone='ghost'
                                  composition='icon-only'
                                  aria-label='Remove link'
                                  icon={<DeleteOutlineIcon sx={{ fontSize: 18 }} />}
                                  // A link on an existing column keeps that column's other settings;
                                  // an added column has nothing left once its link is gone.
                                  onClick={() => patchColumns(columns.flatMap((c) => (c !== link ? [c] : c.name ? [{ ...c, link: undefined }] : [])))}
                                  id={`remove-link-${links.indexOf(link)}`}
                                />
                              </Stack>
                            );
                          })}
                        </Stack>
                      )}

                      {isEntity && links.length > 0 && (
                        <Typography variant='caption' sx={{ display: 'block', mt: 1, color: ds.gray[500] }}>
                          Attach the link to a column and its own values become the links; choose New column for a plain action column instead. Write
                          an in-app path and fill it from the row with{' '}
                          <Box component='code' sx={{ fontFamily: 'monospace' }}>
                            {'{{column}}'}
                          </Box>
                          . Available here: {entityDraft.columns.map((c) => `{{${c}}}`).join(', ')}.
                        </Typography>
                      )}

                      {unfinishedLinks.length > 0 && (
                        <Box sx={{ mt: 1, p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: '6px' }}>
                          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                            Every link needs a header (or a column to attach to) and a path inside the product — starting with a single <code>/</code>
                            , as in <code>/investigate?id={'{{id}}'}</code>. Anyone who can see this dashboard follows these links, which is why an
                            external or scripted destination is refused rather than rendered.
                          </Typography>
                        </Box>
                      )}

                      {unknownPlaceholders.length > 0 && (
                        <Box sx={{ mt: 1, p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: '6px' }}>
                          <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                            {unknownPlaceholders.map((c) => `{{${c}}}`).join(', ')} {unknownPlaceholders.length === 1 ? 'is' : 'are'} not selected in
                            Columns above, so {unknownPlaceholders.length === 1 ? 'it' : 'they'} cannot be filled in — those cells will be blank. Add
                            the column and hide it if you do not want it shown.
                          </Typography>
                        </Box>
                      )}
                    </Box>
                  </Form.Section>
                </Card>
              )}

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
