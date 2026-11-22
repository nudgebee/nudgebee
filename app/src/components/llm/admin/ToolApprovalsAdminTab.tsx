import React from 'react';
import { ds } from '@utils/colors';
import { Box, Typography } from '@mui/material';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { EmptyState } from '@ui/EmptyState';
import { Input } from '@ui/Input';
import { Link } from '@ui/Link';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { Switch } from '@ui/Switch';
import { ToggleGroup } from '@ui/ToggleGroup';
import { toast } from '@ui/Toast';
import { getUserSession, isTenantAdmin } from '@lib/auth';
import {
  checkToolApprovalMatch,
  deleteToolApprovalRule,
  listToolApprovalAccounts,
  listToolApprovalIntegrations,
  listToolApprovalOptions,
  listToolApprovalRules,
  listToolApprovalTools,
  createToolApprovalRules,
  upsertToolApprovalRule,
  type ToolApprovalAccountOptions,
  type ToolApprovalCondition,
  type ToolApprovalIntegration,
  type ToolApprovalToolField,
  type ToolApprovalMatchKind,
  type ToolApprovalRule,
  type ToolApprovalToolOption,
} from '@api1/tool-approval';
import { snakeToTitleCase } from '@utils/common';

type Account = { id: string; account_name: string; cloud_provider: string };
// 'all' = organisation-wide; 'integrations' = organisation-wide but pinned to
// chosen integrations (one rule each); 'selected' = chosen accounts.
type RuleScope = 'selected' | 'all' | 'integrations';

// Same wording and order as organisation-level knowledge bases.
const SCOPE_OPTIONS: { value: RuleScope; label: string }[] = [
  { value: 'all', label: 'Organisation' },
  { value: 'selected', label: 'Selected accounts' },
];
// Integration tools are scoped by integration, not account.
const INTEGRATION_SCOPE_OPTIONS: { value: RuleScope; label: string }[] = [
  { value: 'all', label: 'All integrations' },
  { value: 'integrations', label: 'Selected integrations' },
];

// The value type a condition starts with for a schema field.
function valueTypeForField(field?: ToolApprovalToolField): ConditionValueType {
  switch (field?.type) {
    case 'boolean':
      return 'boolean';
    case 'number':
    case 'integer':
      return 'number';
    case 'object':
    case 'array':
      return 'json';
    default:
      return 'text';
  }
}

const EMPTY_RULE: ToolApprovalRule = {
  account_id: '',
  name: '',
  enabled: true,
  tool_name: '',
  tool_config_id: null,
  match_kind: 'exact_command',
  command_pattern: '',
  working_dir: '',
  conditions: [],
  allow_unattended: false,
};

const matchOptions = [
  { value: 'exact_command', label: 'Exact CLI command' },
  { value: 'command_regex', label: 'Regular expression (RE2, advanced)' },
  { value: 'arguments', label: 'Argument conditions' },
  { value: 'tool', label: 'Entire tool (broad)' },
];

const RE2_SYNTAX_URL = 'https://github.com/google/re2/wiki/Syntax';

// `kubectl.*rollout restart` style: a wildcard right after the first word lets
// the pattern match any command that merely contains the rest, such as
// `kubectl delete namespace x rollout restart`.
export const wildcardBeforeVerb = (pattern: string) => /^\s*\S+?\s*\.[*+]/.test(pattern);

// What each match type approves, with an example, shown under the field.
const MATCH_HELP: Record<ToolApprovalMatchKind, React.ReactNode> = {
  exact_command: (
    <>
      Approves one specific command, e.g. <code>kubectl rollout restart deployment/api -n staging</code>. Quoting may differ; any other change asks
      again. In a chain (<code>&amp;&amp;</code>, <code>;</code>, <code>|</code>) every command must be one of the rule's commands.
    </>
  ),
  command_regex: (
    <>
      Approves commands matching a regular expression (RE2 syntax, whole command), e.g. <code>kubectl get pods -n (staging|dev)</code>. Prefer Exact
      command: a pattern can match more than you expect. In a chain (<code>&amp;&amp;</code>, <code>;</code>, <code>|</code>) every command must match
      on its own.
    </>
  ),
  arguments: (
    <>
      Approves calls whose input fields match, e.g. <code>namespace</code> equals <code>staging</code>. For tools that take fields rather than a
      command.
    </>
  ),
  tool: 'Approves every call to this tool with no further checks. Use only for low-risk tools.',
};

type ConditionValueType = 'text' | 'number' | 'boolean' | 'json';
type ConditionDraft = { path: string; operator: 'equals' | 'regex'; valueType: ConditionValueType; value: string };

const emptyCondition = (): ConditionDraft => ({ path: '', operator: 'equals', valueType: 'text', value: '' });

export function conditionDraft(condition: ToolApprovalCondition): ConditionDraft {
  if (condition.operator === 'regex') return { path: condition.path, operator: 'regex', valueType: 'text', value: String(condition.value ?? '') };
  if (typeof condition.value === 'string') return { path: condition.path, operator: 'equals', valueType: 'text', value: condition.value };
  if (typeof condition.value === 'number') return { path: condition.path, operator: 'equals', valueType: 'number', value: String(condition.value) };
  if (typeof condition.value === 'boolean') return { path: condition.path, operator: 'equals', valueType: 'boolean', value: String(condition.value) };
  return { path: condition.path, operator: 'equals', valueType: 'json', value: JSON.stringify(condition.value) };
}

export function parseCondition(condition: ConditionDraft): ToolApprovalCondition {
  if (!condition.path.trim()) throw new Error('Every argument condition needs a field path.');
  if (condition.operator === 'regex') return { path: condition.path.trim(), operator: 'regex', value: condition.value };
  let value: unknown = condition.value;
  if (condition.valueType === 'number') {
    value = Number(condition.value);
    if (!condition.value.trim() || !Number.isFinite(value)) throw new Error(`"${condition.path}" needs a valid number.`);
  } else if (condition.valueType === 'boolean') {
    value = condition.value === 'true';
  } else if (condition.valueType === 'json') {
    try {
      value = JSON.parse(condition.value);
    } catch {
      throw new Error(`"${condition.path}" needs valid JSON.`);
    }
  }
  return { path: condition.path.trim(), operator: 'equals', value };
}

const ToolApprovalsAdminTab: React.FC = () => {
  const canWrite = isTenantAdmin() || !!getUserSession()?.isSuperAdmin;
  const [rules, setRules] = React.useState<ToolApprovalRule[]>([]);
  const [integrationNames, setIntegrationNames] = React.useState<Record<string, string>>({});
  const [accounts, setAccounts] = React.useState<Account[]>([]);
  const [featureEnabled, setFeatureEnabled] = React.useState(false);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [editing, setEditing] = React.useState<ToolApprovalRule | null>(null);
  const [selectedAccountIDs, setSelectedAccountIDs] = React.useState<string[]>([]);
  const [scope, setScope] = React.useState<RuleScope>('selected');
  const [integrations, setIntegrations] = React.useState<ToolApprovalIntegration[]>([]);
  const [selectedIntegrationIDs, setSelectedIntegrationIDs] = React.useState<string[]>([]);
  const [loadingIntegrations, setLoadingIntegrations] = React.useState(false);
  const [conditions, setConditions] = React.useState<ConditionDraft[]>([]);
  const [configIDs, setConfigIDs] = React.useState<Record<string, string>>({});
  const [configOptions, setConfigOptions] = React.useState<ToolApprovalAccountOptions[]>([]);
  const [loadingConfigOptions, setLoadingConfigOptions] = React.useState(false);
  const [toolOptions, setToolOptions] = React.useState<ToolApprovalToolOption[]>([]);
  const [loadingTools, setLoadingTools] = React.useState(false);
  const [toolOptionsError, setToolOptionsError] = React.useState<string | null>(null);
  const [sampleInput, setSampleInput] = React.useState('');
  const [preview, setPreview] = React.useState<{ matched: boolean; reason: string } | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [deleting, setDeleting] = React.useState<ToolApprovalRule | null>(null);

  const load = React.useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      const [config, accountRows] = await Promise.all([listToolApprovalRules(signal), listToolApprovalAccounts(signal)]);
      setRules(config.rules || []);
      setIntegrationNames(config.integration_names || {});
      setFeatureEnabled(config.enabled);
      setAccounts(accountRows);
    } catch (e) {
      // Axios surfaces AbortSignal cancellation as CanceledError/ERR_CANCELED,
      // not the browser's AbortError. The signal is the stable contract here
      // and also covers React development mode's effect cleanup/remount cycle.
      if (!signal?.aborted) setError(e instanceof Error ? e.message : 'Could not load tool approval rules.');
    } finally {
      // An aborted, superseded request must not mark the replacement request
      // as finished while it is still loading.
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const openEditor = (rule?: ToolApprovalRule) => {
    const next = rule ? { ...rule, conditions: rule.conditions || [] } : { ...EMPTY_RULE };
    setEditing(next);
    // New rules start organisation-wide, as organisation knowledge bases do;
    // the form warns that this includes accounts added later.
    setScope(!rule ? 'all' : rule.account_id ? 'selected' : rule.tool_config_id ? 'integrations' : 'all');
    setSelectedIntegrationIDs(rule && !rule.account_id && rule.tool_config_id ? [rule.tool_config_id] : []);
    setSelectedAccountIDs(rule?.account_id ? [rule.account_id] : []);
    setConditions(next.conditions.map(conditionDraft));
    setConfigIDs(rule?.account_id && rule.tool_config_id ? { [rule.account_id]: rule.tool_config_id } : {});
    setConfigOptions([]);
    setToolOptions([]);
    setToolOptionsError(null);
    setSampleInput(
      next.match_kind === 'exact_command' || next.match_kind === 'command_regex'
        ? JSON.stringify({ command: next.command_pattern || '', ...(next.working_dir ? { work_dir: next.working_dir } : {}) }, null, 2)
        : '{}'
    );
    setPreview(null);
  };

  const allAccounts = scope === 'all' || scope === 'integrations';
  // Targets are only chosen for selected accounts: an organisation rule pins none.
  const editorAccountIDs = !editing || allAccounts ? [] : editing.id && editing.account_id ? [editing.account_id] : selectedAccountIDs;

  // The tool list is the same for every rule, so it loads once per editor.
  const editorOpen = !!editing;
  // MCP integrations that mark no tool as needing approval: all their tools
  // run without asking, so none of them can be picked here (#40399).
  const [unmarkedMcp, setUnmarkedMcp] = React.useState<string[]>([]);
  React.useEffect(() => {
    if (!editorOpen) {
      setToolOptions([]);
      setLoadingTools(false);
      setToolOptionsError(null);
      return;
    }
    const controller = new AbortController();
    setLoadingTools(true);
    setToolOptionsError(null);
    void (async () => {
      try {
        const result = await listToolApprovalTools(controller.signal);
        if (!controller.signal.aborted) {
          setToolOptions(result.tools || []);
          setUnmarkedMcp((result.unmarked_mcp_integrations || []).map((integration) => integration.name));
        }
      } catch (e) {
        if (!controller.signal.aborted) {
          setToolOptions([]);
          setToolOptionsError(e instanceof Error ? e.message : 'Could not load tools.');
        }
      } finally {
        if (!controller.signal.aborted) setLoadingTools(false);
      }
    })();
    return () => controller.abort();
  }, [editorOpen]);

  const selectedTool = toolOptions.find((option) => option.name === editing?.tool_name);
  const integrationTool = selectedTool?.target_kind === 'integration';
  const toolFields = selectedTool?.fields || [];
  // Integration tools are scoped by integration; a legacy account rule keeps
  // its own option so it can still be shown and edited.
  const scopeOptions = (() => {
    const base = integrationTool ? INTEGRATION_SCOPE_OPTIONS : SCOPE_OPTIONS;
    const current = [...SCOPE_OPTIONS, ...INTEGRATION_SCOPE_OPTIONS].find((option) => option.value === scope);
    return current && !base.some((option) => option.value === scope) ? [...base, current] : base;
  })();

  React.useEffect(() => {
    if (!editorOpen || !integrationTool || !editing?.tool_name) {
      setIntegrations([]);
      setLoadingIntegrations(false);
      return;
    }
    const controller = new AbortController();
    setLoadingIntegrations(true);
    void (async () => {
      try {
        const result = await listToolApprovalIntegrations(editing.tool_name, controller.signal);
        if (!controller.signal.aborted) setIntegrations(result.integrations || []);
      } catch {
        if (!controller.signal.aborted) setIntegrations([]);
      } finally {
        if (!controller.signal.aborted) setLoadingIntegrations(false);
      }
    })();
    return () => controller.abort();
  }, [editorOpen, integrationTool, editing?.tool_name]);

  React.useEffect(() => {
    if (!editing || allAccounts || editorAccountIDs.length === 0 || !editing.tool_name.trim()) {
      setConfigOptions([]);
      setLoadingConfigOptions(false);
      return;
    }
    const controller = new AbortController();
    setLoadingConfigOptions(true);
    const timer = window.setTimeout(async () => {
      try {
        const result = await listToolApprovalOptions(
          editorAccountIDs,
          editing.tool_name,
          editing.match_kind,
          editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex' ? editing.command_pattern || '' : '',
          controller.signal
        );
        if (controller.signal.aborted) return;
        const rows = result.accounts || [];
        setConfigOptions(rows);
        setConfigIDs((current) => {
          const next: Record<string, string> = {};
          for (const row of rows) {
            if (!row.configuration_required) continue;
            const currentID = current[row.account_id];
            if (currentID && row.configurations.some((option) => option.id === currentID)) next[row.account_id] = currentID;
            else if (row.configurations.length === 1) next[row.account_id] = row.configurations[0].id;
          }
          return next;
        });
      } catch (e) {
        if (!controller.signal.aborted) {
          setConfigOptions(
            editorAccountIDs.map((account_id) => ({
              account_id,
              configuration_required: true,
              configuration_pending: false,
              configurations: [],
              error: e instanceof Error ? e.message : 'Could not load execution configurations.',
            }))
          );
        }
      } finally {
        if (!controller.signal.aborted) setLoadingConfigOptions(false);
      }
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [editing?.id, editing?.account_id, editing?.tool_name, editing?.match_kind, editing?.command_pattern, scope, selectedAccountIDs.join('|')]);

  const editorRule = (accountID?: string): ToolApprovalRule => {
    if (!editing) throw new Error('No rule is being edited.');
    let parsedConditions: ToolApprovalCondition[] = [];
    if (editing.match_kind === 'arguments') {
      if (conditions.length === 0) throw new Error('Add at least one argument condition.');
      parsedConditions = conditions.map(parseCondition);
    }
    const ruleAccount = allAccounts ? null : accountID || editing.account_id;
    return {
      ...editing,
      account_id: ruleAccount,
      all_accounts: allAccounts,
      tool_config_id: ruleAccount ? configIDs[ruleAccount] || null : scope === 'integrations' ? selectedIntegrationIDs[0] || null : null,
      command_pattern: editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex' ? editing.command_pattern : null,
      working_dir: editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex' ? editing.working_dir || null : null,
      conditions: parsedConditions,
    };
  };

  const save = async () => {
    try {
      setSaving(true);
      const sameName = (rule: ToolApprovalRule) => rule.name.trim().toLowerCase() === editing?.name.trim().toLowerCase();
      if (!editing?.id && allAccounts && rules.some((rule) => !rule.account_id && sameName(rule))) {
        throw new Error('An all-accounts rule with this name already exists.');
      }
      const accountIDs = allAccounts ? [] : editing?.id && editing.account_id ? [editing.account_id] : selectedAccountIDs;
      const duplicateAccounts = accountIDs.filter((accountID) => rules.some((rule) => rule.account_id === accountID && sameName(rule)));
      if (!editing?.id && duplicateAccounts.length > 0) {
        const names = duplicateAccounts.map(accountName).join(', ');
        throw new Error(`A rule with this name already exists for: ${names}.`);
      }
      const saved = editing?.id
        ? [await upsertToolApprovalRule(editorRule(editing.account_id || undefined))]
        : (
            await createToolApprovalRules(
              editorRule(accountIDs[0]),
              accountIDs.map((accountID) => ({ account_id: accountID, tool_config_id: configIDs[accountID] || null })),
              scope === 'integrations' ? selectedIntegrationIDs : []
            )
          ).rules;
      const savedIDs = new Set(saved.map((rule) => rule.id));
      setRules((current) => [...current.filter((rule) => !savedIDs.has(rule.id)), ...saved].sort((a, b) => a.name.localeCompare(b.name)));
      setEditing(null);
      toast.success(
        scope === 'integrations' && saved.length > 1
          ? `Tool approval rule added for ${saved.length} integrations.`
          : allAccounts
          ? 'Tool approval rule saved for the organisation.'
          : saved.length === 1
          ? 'Tool approval rule saved.'
          : `Tool approval rule added to ${saved.length} accounts.`
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not save the rule.');
    } finally {
      setSaving(false);
    }
  };

  const check = async () => {
    try {
      setPreview(await checkToolApprovalMatch(editorRule(editing?.id ? editing.account_id || undefined : selectedAccountIDs[0]), sampleInput));
    } catch (e) {
      setPreview({ matched: false, reason: e instanceof Error ? e.message : 'Could not check the sample.' });
    }
  };

  const remove = async () => {
    if (!deleting?.id) return;
    try {
      await deleteToolApprovalRule(deleting.id, deleting.revision || 0);
      setRules((current) => current.filter((r) => r.id !== deleting.id));
      setDeleting(null);
      toast.success('Tool approval rule deleted.');
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not delete the rule.');
    }
  };

  const toggleRule = async (rule: ToolApprovalRule, enabled: boolean) => {
    try {
      const saved = await upsertToolApprovalRule({ ...rule, enabled, all_accounts: !rule.account_id });
      setRules((current) => current.map((item) => (item.id === saved.id ? saved : item)));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not update the rule.');
    }
  };

  const accountName = (id: string | null) => (id ? accounts.find((a) => a.id === id)?.account_name || id : 'Organisation');
  // A rule's target by name: an integration (MCP, GitHub, databases…) or a
  // cluster/cloud account, whose configuration id is the account id.
  // A target that no longer exists (deleted integration or account) matches
  // nothing, so the rule waives nothing until it is re-pinned.
  const targetLabel = (id: string) => {
    if (integrationNames[id]) return `Integration: ${integrationNames[id]}`;
    const account = accounts.find((a) => a.id === id);
    return account ? `Target: ${account.account_name}` : 'Target removed, so this rule matches nothing';
  };
  const missingRequiredConfig = configOptions.some(
    (row) => !!row.error || (row.configuration_required && !row.configuration_pending && !configIDs[row.account_id])
  );
  const selectedToolAvailable = !!editing?.tool_name && toolOptions.some((option) => option.name === editing.tool_name);
  const toolUnavailable = !!editing?.tool_name && !loadingTools && !toolOptionsError && !selectedToolAvailable;
  const renderedToolOptions = [
    ...toolOptions.map((option) => ({
      value: option.name,
      label: snakeToTitleCase(option.name),
      group:
        option.source === 'mcp'
          ? 'MCP tools'
          : option.configuration_mode === 'required'
          ? 'Uses execution target'
          : option.configuration_mode === 'command'
          ? 'Target depends on command'
          : 'No execution target',
    })),
    ...(toolUnavailable ? [{ value: editing.tool_name, label: `${snakeToTitleCase(editing.tool_name)} (unavailable)`, group: 'Unavailable' }] : []),
  ];

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', p: 'var(--ds-space-4)' }}>
      {!featureEnabled && !loading && (
        <Banner
          tone='warning'
          title='Automatic approval is disabled'
          message='Rules can be prepared, but they take effect only when LLM_SERVER_TOOL_APPROVAL_POLICY_ENABLED is enabled.'
          surface='section'
        />
      )}
      {error && <Banner tone='critical' title='Could not load tool approval rules' message={error} surface='section' />}
      <Box sx={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--ds-space-3)', alignItems: 'flex-start' }}>
        <Box>
          <Typography variant='h6'>Tool approvals</Typography>
          <Typography variant='body2' color='text.secondary'>
            Skip interactive confirmation only when the selected tool, account, command, and arguments match. Access checks still apply.
          </Typography>
        </Box>
        <Button tone='primary' onClick={() => openEditor()} disabled={!canWrite}>
          Add rule
        </Button>
      </Box>
      {!loading && rules.length === 0 ? (
        <EmptyState
          surface
          title='No automatic approval rules'
          description='Write operations continue to ask for confirmation.'
          action={canWrite ? { label: 'Add rule', onClick: () => openEditor() } : undefined}
        />
      ) : (
        rules.map((rule) => (
          <Card
            key={rule.id}
            variant='outlined'
            size='sm'
            elevation='flat'
            header={
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
                <Box>
                  <Typography fontWeight={ds.weight.semibold}>{rule.name}</Typography>
                  <Typography variant='caption' color='text.secondary'>
                    {rule.tool_name} · {accountName(rule.account_id)} · revision {rule.revision}
                    {rule.tool_config_id ? ` · ${targetLabel(rule.tool_config_id)}` : ''}
                    {rule.allow_unattended ? ' · also background runs' : ''}
                  </Typography>
                </Box>
                <Switch
                  checked={rule.enabled}
                  onChange={(_event, checked) => canWrite && void toggleRule(rule, checked)}
                  disabled={!canWrite}
                  label={rule.enabled ? 'Enabled' : 'Disabled'}
                />
              </Box>
            }
            footer={
              canWrite ? (
                <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-2)' }}>
                  <Button tone='secondary' size='sm' onClick={() => openEditor(rule)}>
                    Edit
                  </Button>
                  <Button tone='danger' size='sm' onClick={() => setDeleting(rule)}>
                    Delete
                  </Button>
                </Box>
              ) : undefined
            }
          >
            <Typography variant='body2'>
              {rule.match_kind === 'tool'
                ? 'All invocations of this tool'
                : rule.match_kind === 'arguments'
                ? `${rule.conditions.length} argument condition(s)`
                : rule.command_pattern}
            </Typography>
            {rule.working_dir && (
              <Typography variant='caption' color='text.secondary'>
                Working directory: {rule.working_dir}
              </Typography>
            )}
          </Card>
        ))
      )}

      <Modal
        open={!!editing}
        handleClose={() => setEditing(null)}
        title={editing?.id ? 'Edit approval rule' : 'Add approval rule'}
        width='lg'
        confirmText='Save rule'
        onConfirm={() => void save()}
        confirmDisabled={
          !editing ||
          saving ||
          !editing.name.trim() ||
          (!allAccounts && (editing.id ? !editing.account_id : selectedAccountIDs.length === 0)) ||
          (scope === 'integrations' && (loadingIntegrations || selectedIntegrationIDs.length === 0)) ||
          !editing.tool_name.trim() ||
          ((editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex') && !editing.command_pattern?.trim()) ||
          (editing.tool_name.trim().toLowerCase() === 'shell_execute' &&
            (editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex') &&
            !editing.working_dir?.trim()) ||
          loadingConfigOptions ||
          loadingTools ||
          !!toolOptionsError ||
          toolUnavailable ||
          missingRequiredConfig ||
          (editing.match_kind === 'arguments' && conditions.length === 0)
        }
      >
        {editing && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
            <Banner
              tone='warning'
              surface='section'
              title='This rule can authorize real changes'
              message='Use exact commands where possible. In a chain (&&, ;, |) every command must match the rule on its own; a command substitution ($(…) or backticks) always asks.'
            />
            <Input label='Rule name' value={editing.name} onChange={(name) => setEditing({ ...editing, name })} required />
            <Select
              label='Tool'
              value={editing.tool_name}
              onChange={(tool_name) => {
                setConfigIDs({});
                setConfigOptions([]);
                setSelectedIntegrationIDs([]);
                if (!editing.id) setScope('all');
                // An MCP tool has no CLI command, so it starts as an entire-tool rule.
                const mcpTool = toolOptions.find((option) => option.name === tool_name)?.source === 'mcp';
                setEditing({
                  ...editing,
                  tool_name,
                  match_kind:
                    mcpTool && (editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex') ? 'tool' : editing.match_kind,
                });
              }}
              options={renderedToolOptions}
              placeholder='Select a tool…'
              instructionText={
                <>
                  Lists only tools that ask for approval, since a rule skips that ask: built-in tools that can change something (decided per call),
                  MCP tools marked as needing approval, and custom tools that are not read-only. Other MCP tools run without asking, so they are not
                  listed{unmarkedMcp.length > 0 ? ` (none marked yet in ${unmarkedMcp.join(', ')})` : ''}. Mark MCP tools in{' '}
                  <Link href='/user-management#ai-tools/tools-mcp' openInNew>
                    AI &amp; Tools → Tools &amp; MCP → MCP Servers
                  </Link>
                  .
                </>
              }
              loading={loadingTools}
              disabled={!!toolOptionsError}
              searchable
              grouped
              defaultGroupsOpen
              required
            />
            {toolOptionsError && <Banner tone='critical' surface='section' title='Could not load tools' message={toolOptionsError} />}
            {toolUnavailable && <Banner tone='warning' surface='section' title='This tool is no longer available' message='Choose another tool.' />}
            <Box data-testid='tool-approval-scope-field'>
              <Typography variant='body2' fontWeight={ds.weight.medium} sx={{ marginBottom: ds.space[2] }}>
                Applies to
              </Typography>
              <ToggleGroup
                selection='single'
                size='sm'
                ariaLabel='Where this rule applies'
                value={scope}
                onChange={(next) => {
                  if (!next) return;
                  setScope(next);
                  setConfigIDs({});
                  setConfigOptions([]);
                  // An edited rule stays one rule: switching it to one account
                  // or one integration picks that single target below.
                  if (editing.id && next !== 'selected') setEditing({ ...editing, account_id: null });
                }}
                options={scopeOptions}
              />
            </Box>
            {scope === 'all' ? (
              <Banner
                tone='warning'
                surface='section'
                title={
                  integrationTool
                    ? 'Applies to every integration of this tool, including ones added later'
                    : 'Applies across the organisation, including accounts added later'
                }
                message={
                  integrationTool
                    ? 'The rule matches calls through any of these integrations, from any account. To limit it, choose Selected integrations.'
                    : 'The rule matches calls on any account. To pin a cluster or cloud account, choose Selected accounts.'
                }
              />
            ) : scope === 'integrations' && editing.id ? (
              <Select
                label='Integration'
                instructionText='The rule matches calls through this integration, from any account.'
                value={selectedIntegrationIDs[0] || ''}
                onChange={(id) => setSelectedIntegrationIDs(id ? [id] : [])}
                options={integrations.map((integration) => ({ value: integration.id, label: integration.name }))}
                loading={loadingIntegrations}
                required
                searchable
              />
            ) : scope === 'integrations' ? (
              <Select
                multiple
                label='Integrations'
                instructionText='A rule is added for each integration and matches calls through it, from any account.'
                value={selectedIntegrationIDs}
                onChange={setSelectedIntegrationIDs}
                options={integrations.map((integration) => ({ value: integration.id, label: integration.name }))}
                loading={loadingIntegrations}
                placeholder='Select integrations…'
                required
                searchable
              />
            ) : editing.id ? (
              <Select
                label='Account'
                value={editing.account_id || ''}
                onChange={(account_id) => setEditing({ ...editing, account_id })}
                options={accounts.map((a) => ({ value: a.id, label: `${a.account_name} (${a.cloud_provider})` }))}
                required
                searchable
              />
            ) : (
              <Select
                multiple
                label='Accounts'
                instructionText='The rule is added to each selected account and matches calls that run on that account. Accounts added later are not included.'
                value={selectedAccountIDs}
                onChange={setSelectedAccountIDs}
                options={accounts.map((a) => ({ value: a.id, label: `${a.account_name} (${a.cloud_provider})`, group: a.cloud_provider }))}
                required
                searchable
                grouped
                defaultGroupsOpen
                maxChips={3}
              />
            )}
            <Box data-testid='tool-approval-unattended-field'>
              <Switch
                checked={!!editing.allow_unattended}
                onChange={(_event, checked) => setEditing({ ...editing, allow_unattended: checked })}
                label='Also apply in background runs'
              />
              <Typography variant='caption' color='text.secondary' component='div'>
                Off: the rule only skips the prompt when a signed-in person made the request. On: it also applies when nobody is signed in, such as
                system jobs, API calls without a user, or chat users whose account is not linked.
              </Typography>
            </Box>
            <Select
              label='Match type'
              value={editing.match_kind}
              onChange={(match_kind) => setEditing({ ...editing, match_kind: match_kind as ToolApprovalMatchKind })}
              instructionText={MATCH_HELP[editing.match_kind]}
              options={
                selectedTool?.source === 'mcp'
                  ? matchOptions.filter((option) => option.value === 'tool' || option.value === 'arguments')
                  : matchOptions
              }
              required
            />
            {(editing.match_kind === 'exact_command' || editing.match_kind === 'command_regex') && (
              <>
                <Input
                  type='textarea'
                  label={editing.match_kind === 'exact_command' ? 'CLI command' : 'CLI command regular expression (RE2)'}
                  help={
                    editing.match_kind === 'command_regex' ? (
                      <>
                        Must match the whole command. Put the command&apos;s verb right after the tool name, e.g.{' '}
                        <code>kubectl rollout restart .*</code>; <code>.*</code> before the verb matches any command that only contains your text.{' '}
                        <code>*</code> alone repeats the previous character. See the{' '}
                        <Link href={RE2_SYNTAX_URL} openInNew>
                          RE2 syntax
                        </Link>
                        .
                      </>
                    ) : undefined
                  }
                  value={editing.command_pattern || ''}
                  onChange={(command_pattern) => setEditing({ ...editing, command_pattern })}
                  required
                  rows={3}
                />
                {editing.match_kind === 'command_regex' && wildcardBeforeVerb(editing.command_pattern || '') && (
                  <Banner
                    tone='warning'
                    surface='section'
                    title='This pattern can match more than one kind of command'
                    message={`With a wildcard right after "${
                      (editing.command_pattern || '').trim().split(/[\s.]/)[0]
                    }", any command that contains the rest matches, e.g. one that deletes resources named after your text. Put the verb first.`}
                  />
                )}
                <Input
                  label={editing.tool_name.trim().toLowerCase() === 'shell_execute' ? 'Working directory' : 'Working directory (optional)'}
                  value={editing.working_dir || ''}
                  onChange={(working_dir) => setEditing({ ...editing, working_dir })}
                  help='When set, the invocation must use this exact directory.'
                  required={editing.tool_name.trim().toLowerCase() === 'shell_execute'}
                />
              </>
            )}
            {configOptions.some((row) => row.configuration_pending) && (
              <Banner
                tone='info'
                surface='section'
                title='Enter the command to choose its target'
                message='The execution target depends on the command, such as kubectl, AWS CLI, or a database client.'
              />
            )}
            {configOptions
              .filter((row) => !row.configuration_pending && (row.configuration_required || row.error))
              .map((row) => (
                <Box key={row.account_id}>
                  {row.error ? (
                    <Banner
                      tone='critical'
                      surface='section'
                      title={`Could not load targets for ${accountName(row.account_id)}`}
                      message={row.error}
                    />
                  ) : row.configurations.length === 0 ? (
                    <Banner
                      tone='warning'
                      surface='section'
                      title={`No available target for ${accountName(row.account_id)}`}
                      message='Configure or grant access to an execution target before creating this rule.'
                    />
                  ) : (
                    <Select
                      label={editorAccountIDs.length === 1 ? 'Execution target' : `Execution target · ${accountName(row.account_id)}`}
                      instructionText='The saved rule remains bound to this target even if its display name changes.'
                      value={configIDs[row.account_id] || ''}
                      onChange={(id) => setConfigIDs((current) => ({ ...current, [row.account_id]: id }))}
                      options={row.configurations.map((option) => ({ value: option.id, label: option.name }))}
                      placeholder='Select a target…'
                      searchable
                      required
                    />
                  )}
                </Box>
              ))}
            {editing.match_kind === 'arguments' && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                <Banner
                  tone='warning'
                  surface='section'
                  title='Only the fields you list are checked'
                  message='Every other field, including any command the tool runs, is approved with whatever value the assistant sends. To limit commands, use Exact CLI command or Regular expression, or add a condition on the CLI command field: a chain then matches only when every command in it does, and a command substitution always asks.'
                />
                <Box>
                  <Typography fontWeight={ds.weight.semibold}>Argument conditions</Typography>
                  <Typography variant='body2' color='text.secondary'>
                    All conditions must match. Pick a field from this tool&apos;s input, or type a dotted path for a nested field, for example{' '}
                    <code>metadata.namespace</code>.
                  </Typography>
                </Box>
                {conditions.map((condition, index) => (
                  <Card key={index} variant='outlined' elevation='flat' size='sm'>
                    <Box
                      sx={{
                        display: 'grid',
                        gridTemplateColumns: { xs: '1fr', md: condition.operator === 'equals' ? '2fr 1.25fr 1fr 2fr auto' : '2fr 1.25fr 3fr auto' },
                        gap: 'var(--ds-space-3)',
                        alignItems: 'end',
                      }}
                    >
                      <Select
                        label='Field'
                        value={condition.path}
                        onChange={(path) => {
                          const field = toolFields.find((candidate) => candidate.name === path);
                          const valueType = valueTypeForField(field);
                          setConditions((current) =>
                            current.map((item, i) =>
                              i === index
                                ? {
                                    ...item,
                                    path,
                                    valueType: item.operator === 'regex' ? 'text' : valueType,
                                    value: valueType === 'boolean' ? 'true' : '',
                                  }
                                : item
                            )
                          );
                        }}
                        onCreateOption={(path) => setConditions((current) => current.map((item, i) => (i === index ? { ...item, path } : item)))}
                        options={[
                          ...toolFields.map((field) => ({
                            value: field.name,
                            label: `${field.name} · ${field.command ? 'CLI command' : field.type}`,
                          })),
                          ...(condition.path && !toolFields.some((field) => field.name === condition.path)
                            ? [{ value: condition.path, label: condition.path }]
                            : []),
                        ]}
                        placeholder={editing.tool_name ? 'Choose a field…' : 'Select a tool first…'}
                        searchPlaceholder='Search or type a nested path…'
                        searchable
                        required
                      />
                      <Select
                        label='Operator'
                        value={condition.operator}
                        onChange={(operator) =>
                          setConditions((current) =>
                            current.map((item, i) => (i === index ? { ...item, operator: operator as 'equals' | 'regex', valueType: 'text' } : item))
                          )
                        }
                        options={[
                          { value: 'equals', label: 'Equals' },
                          { value: 'regex', label: 'Matches regex' },
                        ]}
                      />
                      {condition.operator === 'equals' && (
                        <Select
                          label='Value type'
                          value={condition.valueType}
                          onChange={(valueType) =>
                            setConditions((current) =>
                              current.map((item, i) =>
                                i === index
                                  ? { ...item, valueType: valueType as ConditionValueType, value: valueType === 'boolean' ? 'true' : '' }
                                  : item
                              )
                            )
                          }
                          options={[
                            { value: 'text', label: 'Text' },
                            { value: 'number', label: 'Number' },
                            { value: 'boolean', label: 'Boolean' },
                            { value: 'json', label: 'JSON' },
                          ]}
                        />
                      )}
                      {condition.operator === 'equals' && (toolFields.find((field) => field.name === condition.path)?.enum || []).length > 0 ? (
                        <Select
                          label='Value'
                          value={condition.value}
                          onChange={(value) => setConditions((current) => current.map((item, i) => (i === index ? { ...item, value } : item)))}
                          options={(toolFields.find((field) => field.name === condition.path)?.enum || []).map((option) => ({
                            value: String(option),
                            label: String(option),
                          }))}
                          required
                        />
                      ) : condition.valueType === 'boolean' && condition.operator === 'equals' ? (
                        <Select
                          label='Value'
                          value={condition.value || 'true'}
                          onChange={(value) => setConditions((current) => current.map((item, i) => (i === index ? { ...item, value } : item)))}
                          options={[
                            { value: 'true', label: 'True' },
                            { value: 'false', label: 'False' },
                          ]}
                        />
                      ) : (
                        <Input
                          label={condition.operator === 'regex' ? 'Full-value regex' : 'Value'}
                          value={condition.value}
                          onChange={(value) => setConditions((current) => current.map((item, i) => (i === index ? { ...item, value } : item)))}
                          placeholder={condition.operator === 'regex' ? 'prod-.*' : condition.valueType === 'json' ? '{"key":"value"}' : ''}
                          required
                        />
                      )}
                      <Button tone='secondary' size='sm' onClick={() => setConditions((current) => current.filter((_item, i) => i !== index))}>
                        Remove
                      </Button>
                    </Box>
                  </Card>
                ))}
                <Box>
                  <Button
                    tone='secondary'
                    size='sm'
                    disabled={conditions.length >= 16}
                    onClick={() => setConditions((current) => [...current, emptyCondition()])}
                  >
                    Add condition
                  </Button>
                </Box>
                <Typography variant='caption' color='text.secondary'>
                  Up to 16 conditions. Command, query, SQL, script, code, args, and argv fields require a command-aware rule.
                </Typography>
              </Box>
            )}
            {editing.match_kind === 'tool' && (
              <Banner
                tone='critical'
                surface='section'
                title='Broad rule'
                message='Every write invocation of this tool in the selected account will skip confirmation.'
              />
            )}
            <Card
              variant='tinted'
              tone='neutral'
              elevation='flat'
              size='sm'
              header={<Typography fontWeight={ds.weight.semibold}>Test this rule</Typography>}
            >
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                <Input type='textarea' label='Sample tool input' value={sampleInput} onChange={setSampleInput} rows={4} />
                <Box>
                  <Button tone='secondary' size='sm' onClick={() => void check()}>
                    Check match
                  </Button>
                </Box>
                {preview && (
                  <Banner
                    tone={preview.matched ? 'success' : 'info'}
                    surface='section'
                    title={preview.matched ? 'Rule matches' : 'Rule does not match'}
                    message={preview.reason}
                  />
                )}
              </Box>
            </Card>
          </Box>
        )}
      </Modal>

      <Modal
        open={!!deleting}
        handleClose={() => setDeleting(null)}
        title='Delete approval rule?'
        confirmText='Delete'
        onConfirm={() => void remove()}
      >
        Future matching invocations will require confirmation. Already-running tool calls are unaffected.
      </Modal>
    </Box>
  );
};

export default ToolApprovalsAdminTab;
