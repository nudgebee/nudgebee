import React, { useEffect, useMemo, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { CodeEditor } from '@ui/CodeEditor';
import { Select } from '@ui/Select';
import { ToggleGroup } from '@ui/ToggleGroup';
import { snackbar } from '@ui/Toast';
import { Form } from '@shared/forms/Form';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { ds } from '@utils/colors';
import apiDashboards, { type AccountOption, type Dashboard } from '@api1/dashboards';
import { panelScope } from './panelAccounts';
import { convertGrafanaDashboard, parseGrafanaJson, type GrafanaImportResult } from './grafanaImport';
import { collectSourceScopes, convertNativeDashboard, isNativeDashboard, parseImportJson, type ScopeMapping } from './nativeImport';

interface Props {
  open: boolean;
  /** Every account the importer may scope the imported panels to. */
  accountOptions: AccountOption[];
  onClose: () => void;
  onImported: (dashboard: Dashboard) => void;
}

/**
 * Which model the pasted JSON is in. Chosen rather than sniffed: the two
 * formats fail in different ways, and a paste that is nearly-but-not-quite one
 * of them should say "this is not a Grafana dashboard" rather than silently
 * trying the other converter and reporting whatever that one disliked.
 */
type ImportFormat = 'nudgebee' | 'grafana';

/** Mapping-row value meaning "the file's own scope, which resolves here too". */
const KEEP = 'keep';

/**
 * Imports a dashboard from pasted JSON — either a Grafana export or a file
 * exported from here.
 *
 * The conversion is previewed before anything is written. A Grafana dashboard
 * routinely contains panels this build cannot draw, and a file from another
 * tenant names accounts that do not exist here; seeing "4 of 12 panels" BEFORE
 * the import is the difference between a deliberate choice and a silently
 * truncated dashboard.
 */
const ImportDashboardModal: React.FC<Props> = ({ open, accountOptions, onClose, onImported }) => {
  const [json, setJson] = useState('');
  const [format, setFormat] = useState<ImportFormat>('nudgebee');
  const [accountType, setAccountType] = useState('');
  const [accountIds, setAccountIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  /** Source scope key → chosen target, as the encoded value of the row's Select. */
  const [mappingChoice, setMappingChoice] = useState<Record<string, string>>({});

  const accountTypes = useMemo(() => {
    const seen = new Set<string>();
    for (const o of accountOptions) {
      if (o.cloud_provider) seen.add(o.cloud_provider);
    }
    return [...seen].sort().map((t) => ({ label: t, value: t }));
  }, [accountOptions]);

  const accountsForType = useMemo(
    () => accountOptions.filter((o) => o.cloud_provider === accountType).map((o) => ({ label: o.label, value: o.value })),
    [accountOptions, accountType]
  );

  // The native format is "this product's own", so it is named by the tenant's
  // branding — a white-labelled tenant has never heard of Nudgebee. Grafana is
  // a third-party product and stays literal.
  const { baseTitle } = useTenantBranding();
  const formatOptions = useMemo(
    () => [
      { value: 'nudgebee' as ImportFormat, label: baseTitle, tooltip: 'A dashboard or panel exported from here' },
      { value: 'grafana' as ImportFormat, label: 'Grafana', tooltip: 'A Grafana dashboard from Share → Export → View JSON' },
    ],
    [baseTitle]
  );

  /**
   * The scopes the pasted file names, one row each in the mapping table.
   *
   * Only for our own format — a Grafana dashboard names no accounts, so there
   * is nothing to map and the default scope is the whole story.
   */
  const sourceScopes = useMemo(() => {
    if (format !== 'nudgebee' || !json.trim()) return [];
    try {
      const model = parseImportJson(json);
      return isNativeDashboard(model) ? collectSourceScopes(model) : [];
    } catch {
      return [];
    }
  }, [json, format]);

  /**
   * Prefills each row with "keep" when the file's own scope still resolves
   * here, which is what a same-tenant re-import wants: an export round-trips
   * without silently collapsing onto the default.
   */
  useEffect(() => {
    setMappingChoice((prev) => {
      const next: Record<string, string> = {};
      for (const scope of sourceScopes) {
        if (prev[scope.key] !== undefined) {
          next[scope.key] = prev[scope.key];
          continue;
        }
        const resolvable =
          scope.accountIds.length > 0
            ? scope.accountIds.every((id) => accountOptions.some((o) => o.value === id))
            : Boolean(scope.accountType && accountOptions.some((o) => o.cloud_provider === scope.accountType));
        next[scope.key] = resolvable ? KEEP : '';
      }
      return next;
    });
  }, [sourceScopes, accountOptions]);

  /**
   * Options for one mapping row: keep the file's scope when it resolves here,
   * every account of a provider, or one specific account. Single-select rather
   * than a multi-select of accounts — a row that maps to several accounts is
   * rare, and encoding "all AWS" alongside individual picks in one multi-select
   * reads as a contradiction. Split such a panel after import.
   */
  const mappingTargets = (scope: { accountIds: string[]; accountType?: string; label: string }) => {
    const options: { label: string; value: string }[] = [];
    const resolvable =
      scope.accountIds.length > 0
        ? scope.accountIds.every((id) => accountOptions.some((o) => o.value === id))
        : Boolean(scope.accountType && accountOptions.some((o) => o.cloud_provider === scope.accountType));
    if (resolvable) options.push({ label: `Keep — ${scope.label}`, value: KEEP });
    // Select renders a flat list, so the provider is carried in each label
    // rather than in a group heading — two accounts called "prod" in different
    // clouds are otherwise the same row twice.
    for (const type of accountTypes) options.push({ label: `All ${type.label}`, value: `type:${type.value}` });
    for (const account of accountOptions) {
      options.push({ label: account.cloud_provider ? `${account.label} (${account.cloud_provider})` : account.label, value: `id:${account.value}` });
    }
    return options;
  };

  /** Row choices decoded into the panel scopes the converter applies. */
  const mappings = useMemo((): ScopeMapping => {
    const out: ScopeMapping = {};
    for (const scope of sourceScopes) {
      const choice = mappingChoice[scope.key];
      if (!choice) continue;
      if (choice === KEEP) {
        out[scope.key] = scope.accountIds.length > 0 ? { account_ids: scope.accountIds } : { account_type: scope.accountType, account_ids: [] };
      } else if (choice.startsWith('type:')) {
        out[scope.key] = { account_type: choice.slice(5), account_ids: [] };
      } else if (choice.startsWith('id:')) {
        out[scope.key] = { account_ids: [choice.slice(3)] };
      }
    }
    return out;
  }, [sourceScopes, mappingChoice]);

  // Re-converted on every keystroke rather than on a "Preview" click: the
  // conversion is pure and the corpus converts in single-digit milliseconds.
  const { preview, parseError } = useMemo((): { preview: GrafanaImportResult | null; parseError: string } => {
    if (!json.trim()) return { preview: null, parseError: '' };
    try {
      const scope = panelScope(accountType, accountIds);
      // The Grafana path re-parses its own text so its error messages ("no
      // `panels` array — it does not look like a Grafana dashboard") stay
      // pointed at Grafana input.
      if (format === 'grafana') return { preview: convertGrafanaDashboard(parseGrafanaJson(json), scope), parseError: '' };
      // A file exported from here needs validating and re-scoping, not
      // translating.
      const model = parseImportJson(json);
      if (!isNativeDashboard(model)) {
        throw new Error(
          `This does not look like a ${baseTitle} export — a dashboard file has a \`definition.panels\`, a panel file a \`grid_pos\`. Switch to Grafana if that is what you pasted.`
        );
      }
      return { preview: convertNativeDashboard(model, scope, mappings), parseError: '' };
    } catch (err) {
      return { preview: null, parseError: (err as Error).message };
    }
  }, [json, format, accountType, accountIds, baseTitle, mappings]);

  const reset = () => {
    setJson('');
    setFormat('nudgebee');
    setAccountType('');
    setAccountIds([]);
    setMappingChoice({});
  };

  const close = () => {
    reset();
    onClose();
  };

  const panelCount = preview?.definition.panels.length ?? 0;
  /**
   * Whether every scope the file names has a target, which makes the default
   * scope unreachable — no panel would ever fall back to it. Only then is the
   * Accounts picker genuinely optional; the server still rejects a panel with
   * no scope at all, so something has to cover the unmapped ones.
   */
  const everyScopeMapped = sourceScopes.length > 0 && sourceScopes.every((scope) => mappings[scope.key]);
  const hasDefaultScope = Boolean(accountType || accountIds.length > 0);
  const canImport = Boolean(preview && panelCount > 0 && (hasDefaultScope || everyScopeMapped) && !saving);

  /**
   * Why Import is disabled, shown beside the button.
   *
   * The account controls sit below a 260px editor, so on a short window the
   * only visible signal was a greyed-out button with nothing saying which field
   * it was waiting on.
   */
  const blockedReason = (() => {
    if (!json.trim() || parseError || !preview || saving) return '';
    if (panelCount === 0) return 'No panel in this file can be imported.';
    if (!hasDefaultScope && !everyScopeMapped) return 'Choose an account type below — every panel must name one.';
    return '';
  })();

  const handleImport = async () => {
    if (!preview) return;
    setSaving(true);
    try {
      const res = await apiDashboards.saveDashboard({
        title: preview.title,
        description: preview.description,
        definition: preview.definition,
        tags: preview.tags,
      });
      // The gateway reports handler failures in `errors` rather than throwing, so
      // the server's message is the one worth showing — it names the panel that
      // failed validation.
      if (res.errors || !res.data) {
        const message = Array.isArray(res.errors) ? (res.errors[0] as any)?.message : null;
        snackbar.error(message || 'Could not import that dashboard.');
        return;
      }
      snackbar.success(`Imported ${panelCount} panel${panelCount === 1 ? '' : 's'}`);
      reset();
      onImported(res.data);
    } finally {
      // Guarantees the modal unlocks on every exit, including the early return
      // a rejected import takes.
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title='Import dashboard'
      width='md'
      backdropClickClose={false}
      actionButtons={
        // Full width so the reason can sit left of the buttons — DialogActions
        // right-aligns its single child.
        <Stack direction='row' gap='12px' alignItems='center' sx={{ width: '100%', button: { minWidth: '140px' } }}>
          <Typography variant='caption' sx={{ color: ds.amber[600], mr: 'auto' }} data-testid='import-blocked-reason'>
            {blockedReason}
          </Typography>
          <Button tone='secondary' onClick={close} disabled={saving} id='import-cancel-btn'>
            Cancel
          </Button>
          <Button onClick={handleImport} disabled={!canImport} id='import-dashboard-btn' data-testid='import-dashboard-btn'>
            {saving ? 'Importing…' : 'Import'}
          </Button>
        </Stack>
      }
    >
      <Form variant='stacked' density='default'>
        {/* Paste first, then map what the file names, then the fallback for
            anything left. The account controls used to come first, which asked
            a question about a file the importer had not pasted yet. */}
        <Form.Section title='Dashboard JSON'>
          {/* Label beside the control rather than above it: a two-option toggle
              is narrower than its own label, and Form.Field (stacked) would
              leave the row mostly empty. Typography matches Form.Field's label
              so it does not read as a different kind of label. */}
          <Stack direction='row' alignItems='center' gap={1.5} flexWrap='wrap'>
            <Typography
              component='label'
              htmlFor='import-format-toggle'
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-gray-700)',
                fontFamily: 'var(--ds-font-display)',
              }}
            >
              Format
            </Typography>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Import format'
              value={format}
              options={formatOptions}
              onChange={(next) => setFormat(next as ImportFormat)}
              id='import-format-toggle'
            />
          </Stack>
          <Typography variant='caption' sx={{ color: ds.gray[500] }}>
            {format === 'grafana'
              ? "Paste the output of Grafana's Share → Export → View JSON. Only Prometheus panels come across."
              : 'Paste a dashboard or a single panel exported from the listing or a panel’s menu.'}
          </Typography>
          <CodeEditor
            value={json}
            onChange={setJson}
            language='json'
            height={260}
            placeholder={format === 'grafana' ? '{ "title": "…", "panels": [ … ] }' : '{ "title": "…", "definition": { "panels": [ … ] } }'}
          />
        </Form.Section>

        {/* Only our own format names accounts, and only worth showing when the
            file actually names some. Each row is optional — anything left on
            "Use the default" behaves exactly as it did before mapping. */}
        {sourceScopes.length > 0 && (
          <Form.Section
            title='Accounts named in the file'
            description='Point each one at an account here. A dashboard spanning several accounts keeps that structure instead of collapsing onto one.'
            divider
          >
            <Stack gap={1}>
              {sourceScopes.map((scope) => (
                <Form.Row key={scope.key} ratio={[1, 1]}>
                  <Box sx={{ minWidth: 0 }}>
                    <Typography sx={{ fontSize: 13, fontWeight: 600, color: ds.gray[700] }} noWrap title={scope.label}>
                      {scope.label}
                    </Typography>
                    <Typography variant='caption' sx={{ color: ds.gray[500] }}>
                      {scope.panelCount} panel{scope.panelCount === 1 ? '' : 's'}
                    </Typography>
                  </Box>
                  <Select
                    value={mappingChoice[scope.key] ?? ''}
                    options={mappingTargets(scope)}
                    onChange={(next: string) => setMappingChoice((prev) => ({ ...prev, [scope.key]: next }))}
                    placeholder='Use the default'
                    id={`import-map-${scope.key}`}
                  />
                </Form.Row>
              ))}
            </Stack>
          </Form.Section>
        )}

        {/* The fallback, and the ONLY account control for a Grafana import,
            which names no accounts at all. Hidden once every scope in the file
            has a target: nothing would reach it, and a required-looking field
            with no effect is worse than no field. */}
        {!everyScopeMapped && (
          <Form.Section
            title={sourceScopes.length > 0 ? 'Default accounts' : 'Accounts'}
            description={
              sourceScopes.length > 0
                ? 'Used by any panel above still set to "Use the default", and by panels the file left unscoped.'
                : 'A Grafana dashboard names no account, so every imported panel is scoped to what you choose here.'
            }
            divider
          >
            <Form.Row ratio={[1, 1]}>
              <Form.Field label='Account type' required>
                <Select value={accountType} options={accountTypes} onChange={setAccountType} placeholder='Select…' id='import-account-type' />
              </Form.Field>
              <Form.Field label='Accounts'>
                <Select
                  multiple
                  value={accountIds}
                  options={accountsForType}
                  onChange={setAccountIds}
                  disabled={!accountType}
                  placeholder={accountType ? `All ${accountType} accounts` : 'Select…'}
                  id='import-accounts'
                />
              </Form.Field>
            </Form.Row>
          </Form.Section>
        )}

        {parseError && (
          <Box sx={{ p: 1.5, border: `1px solid ${ds.red[300]}`, background: ds.red[100], borderRadius: '6px' }}>
            <Typography variant='body2' sx={{ color: ds.red[600] }} data-testid='import-parse-error'>
              {parseError}
            </Typography>
          </Box>
        )}

        {preview && (
          <Form.Section title='Preview' divider>
            <Stack direction='row' gap={1} alignItems='center' flexWrap='wrap'>
              <Typography sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 14, fontWeight: 620, color: ds.gray[700] }}>
                {preview.title}
              </Typography>
              <Chip size='2xs' tone={panelCount > 0 ? 'info' : 'critical'}>
                {panelCount} panel{panelCount === 1 ? '' : 's'}
              </Chip>
              {preview.tags.map((tag) => (
                <Chip key={tag} size='2xs' tone='subtle'>
                  {tag}
                </Chip>
              ))}
            </Stack>

            {/* Shown before the import, not after: a dashboard that lost four
                panels is worth knowing about while it can still be cancelled. */}
            {preview.warnings.length > 0 && (
              <Box sx={{ mt: 1.5, p: 1.5, border: `1px solid ${ds.amber[300]}`, background: ds.amber[100], borderRadius: '6px' }}>
                <Typography variant='body2' sx={{ color: ds.gray[700], fontWeight: 600, mb: 0.5 }}>
                  What will not come across
                </Typography>
                <Box component='ul' sx={{ m: 0, pl: 2.5 }} data-testid='import-warnings'>
                  {preview.warnings.map((warning, i) => (
                    <Typography component='li' key={i} variant='body2' sx={{ color: ds.gray[700] }}>
                      {warning}
                    </Typography>
                  ))}
                </Box>
              </Box>
            )}
          </Form.Section>
        )}
      </Form>
    </Modal>
  );
};

export default ImportDashboardModal;
