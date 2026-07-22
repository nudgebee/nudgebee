import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { Box } from '@mui/material';
import Text from '@shared/format/Text';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import { Checkbox } from '@ui/Checkbox';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import Modal from '@ui/Modal';
import apiTriage, { type ThresholdSuggestionItem, type ThresholdApplyOptions, type ThresholdApplyResult } from '@api1/triage';
import apiIntegrations from '@api1/integrations';
import { snackbar } from '@shared/snackbarService';
import { ds } from 'src/utils/colors';

interface ThresholdApplyPanelProps {
  data: ThresholdSuggestionItem;
  onApplied?: () => void;
}

type ApplyMethod = 'direct' | 'pr';

interface GitProject {
  key: string; // "owner/repo" (github) or "group/project" (gitlab)
  name: string;
}

interface GitIntegration {
  key: string; // unique dropdown key, e.g. "github:My Org"
  label: string; // display label, e.g. "GitHub: My Org"
  name: string; // integration name (credentials lookup)
  type: string; // "github" | "gitlab"
  url: string; // base URL for constructing repo links
  projects: GitProject[]; // repos accessible to the integration
}

const NON_ACTIONABLE = new Set(['none', 'review_alert', 'not_eligible']);

// Integrations management lives on the user-management page, integrations tab.
const INTEGRATIONS_SETTINGS_URL = '/user-management#integrations';

const statusTone = (status?: string): 'success' | 'warning' | 'critical' | 'neutral' => {
  switch (status) {
    case 'applied':
      return 'success';
    case 'pending':
      return 'warning';
    case 'failed':
      return 'critical';
    case 'reverted':
      return 'neutral';
    default:
      return 'neutral';
  }
};

const statusLabel = (status?: string): string => {
  switch (status) {
    case 'applied':
      return 'Applied';
    case 'pending':
      return 'PR pending';
    case 'failed':
      return 'Apply failed';
    case 'reverted':
      return 'Reverted';
    default:
      return status || '';
  }
};

/**
 * RulePreviewBlock renders the rewritten rule definition. The preview is often a
 * large provider JSON (GCP/Azure/AWS) or a PromQL expression; we pretty-print JSON
 * and keep the block collapsed by default so the dialog stays readable — the
 * threshold change itself is surfaced separately above.
 */
const RulePreviewBlock: React.FC<{ expr: string; defaultOpen?: boolean }> = ({ expr, defaultOpen = false }) => {
  const [open, setOpen] = useState(defaultOpen);
  const pretty = useMemo(() => {
    try {
      return JSON.stringify(JSON.parse(expr), null, 2);
    } catch {
      return expr;
    }
  }, [expr]);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
      <Box
        component='button'
        type='button'
        onClick={() => setOpen((v) => !v)}
        sx={{
          alignSelf: 'flex-start',
          display: 'inline-flex',
          alignItems: 'center',
          gap: ds.space[1],
          p: 0,
          border: 'none',
          background: 'none',
          cursor: 'pointer',
          color: ds.gray[600],
          fontSize: ds.text.caption,
        }}
      >
        <Text value={open ? 'Hide rule preview' : 'Show full rule preview'} sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
        <Text value={open ? '▾' : '▸'} sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
      </Box>
      {open && (
        <Box
          component='pre'
          sx={{
            m: 0,
            p: ds.space[2],
            maxHeight: 280,
            overflow: 'auto',
            fontSize: ds.text.caption,
            fontFamily: ds.font.mono,
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.sm,
            whiteSpace: 'pre',
            wordBreak: 'normal',
          }}
        >
          {pretty}
        </Box>
      )}
    </Box>
  );
};

/**
 * ThresholdApplyPanel renders the apply controls for a single threshold suggestion:
 * a method selector (Direct vs Raise PR) limited to available methods, a confirmation
 * dialog showing the rewritten rule preview + an editable threshold + risk gate, and
 * a Revert button. The PR path picks a configured Git integration rather than asking
 * for credentials inline.
 */
const ThresholdApplyPanel: React.FC<ThresholdApplyPanelProps> = ({ data, onApplied }) => {
  const recType = data.metric_stats?.recommendation_type || data.recommendation_type || 'tune_threshold';
  const actionable = !NON_ACTIONABLE.has(recType);

  const [options, setOptions] = useState<ThresholdApplyOptions | null>(null);
  const [loadingOptions, setLoadingOptions] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [method, setMethod] = useState<ApplyMethod>('direct');
  const [overrideRisk, setOverrideRisk] = useState(false);
  const [applying, setApplying] = useState(false);
  const [result, setResult] = useState<ThresholdApplyResult | null>(null);
  const [error, setError] = useState<string>('');
  const [applyStatus, setApplyStatus] = useState<string | undefined>(data.apply_status);

  // Editable threshold value applied to the rule (defaults to the suggested value).
  const [acceptValue, setAcceptValue] = useState<string>('');

  // PR inputs
  const [gitIntegrationKey, setGitIntegrationKey] = useState('');
  const [selectedRepoKey, setSelectedRepoKey] = useState('');
  const [gitOrg, setGitOrg] = useState(''); // manual fallback when integration exposes no repos
  const [gitRepo, setGitRepo] = useState(''); // manual fallback
  const [gitBranch, setGitBranch] = useState('main');
  const [gitFilePath, setGitFilePath] = useState('');

  // Configured Git integrations (credentials source for the PR path).
  const [gitIntegrations, setGitIntegrations] = useState<GitIntegration[]>([]);
  const [loadingIntegrations, setLoadingIntegrations] = useState(false);
  const [integrationsLoaded, setIntegrationsLoaded] = useState(false);

  const suggestedValue = options?.new_threshold ?? data.suggested_threshold;

  const fetchOptions = useCallback(async () => {
    if (!actionable || !data.alert_rule_key || !data.cloud_account_id) {
      return;
    }
    setLoadingOptions(true);
    try {
      const res = await apiTriage.getThresholdApplyOptions({
        alert_rule_key: data.alert_rule_key,
        cloud_account_id: data.cloud_account_id,
      });
      setOptions(res);
      const firstAvailable = res?.options?.find((o) => o.available);
      if (firstAvailable) {
        setMethod(firstAvailable.method as ApplyMethod);
      }
    } finally {
      setLoadingOptions(false);
    }
  }, [actionable, data.alert_rule_key, data.cloud_account_id]);

  useEffect(() => {
    fetchOptions();
  }, [fetchOptions]);

  // Load configured Git integrations once, the first time a PR confirm dialog opens.
  // NOTE: loadingIntegrations must NOT be a dependency — toggling it inside the effect
  // would re-run the effect and cancel its own in-flight request (stuck on loading).
  useEffect(() => {
    if (!confirmOpen || method !== 'pr' || integrationsLoaded) {
      return;
    }
    let cancelled = false;
    (async () => {
      setLoadingIntegrations(true);
      try {
        // listTicketConfigurationsByTool returns each integration with its accessible
        // repos already parsed into `projects` ({ key: "owner/repo", name }).
        const [ghRes, glRes]: any[] = await Promise.all([
          apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'github' }),
          apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'gitlab' }),
        ]);
        if (cancelled) {
          return;
        }
        const gh: GitIntegration[] = (ghRes?.data || []).map((g: any) => ({
          key: `github:${g.name}`,
          label: `GitHub: ${g.name}`,
          name: g.name,
          type: 'github',
          url: 'https://github.com',
          projects: Array.isArray(g.projects) ? g.projects : [],
        }));
        const gl: GitIntegration[] = (glRes?.data || []).map((g: any) => ({
          key: `gitlab:${g.name}`,
          label: `GitLab: ${g.name}`,
          name: g.name,
          type: 'gitlab',
          url: g.url || 'https://gitlab.com',
          projects: Array.isArray(g.projects) ? g.projects : [],
        }));
        const combined = [...gh, ...gl];
        setGitIntegrations(combined);
        if (combined.length > 0) {
          setGitIntegrationKey((prev) => prev || combined[0].key);
        }
      } catch (e: any) {
        if (!cancelled) {
          setError(e?.message || 'Failed to load Git integrations');
        }
      } finally {
        if (!cancelled) {
          setLoadingIntegrations(false);
          setIntegrationsLoaded(true);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [confirmOpen, method, integrationsLoaded]);

  if (!actionable) {
    return null;
  }

  const directOption = options?.options?.find((o) => o.method === 'direct');
  const prOption = options?.options?.find((o) => o.method === 'pr');
  const requiresOverride = options?.requires_override || data.metric_stats?.risk_level === 'dangerous';
  const selectedAvailable = method === 'direct' ? directOption?.available : prOption?.available;

  const selectedIntegration = gitIntegrations.find((i) => i.key === gitIntegrationKey);
  const reposFromIntegration = selectedIntegration?.projects || [];
  const selectedRepo = reposFromIntegration.find((r) => r.key === selectedRepoKey);
  const noIntegration = method === 'pr' && !loadingIntegrations && gitIntegrations.length === 0;

  // Resolve org/repo: from the selected repo key ("owner/repo") when available, else the manual inputs.
  let derivedOrg = gitOrg.trim();
  let derivedRepo = gitRepo.trim();
  if (selectedRepo?.key?.includes('/')) {
    const slash = selectedRepo.key.indexOf('/');
    derivedOrg = selectedRepo.key.slice(0, slash);
    derivedRepo = selectedRepo.key.slice(slash + 1);
  }

  const operator = options?.operator || data.operator || '';
  const currentThreshold = options?.current_threshold ?? data.current_threshold ?? '';

  // What was actually applied (from the live result, falling back to the persisted row).
  const appliedThreshold = result?.applied_threshold ?? data.applied_threshold;
  const appliedPrevious = result?.previous_threshold ?? data.previous_threshold;
  const revertedTo = result?.applied_threshold ?? data.previous_threshold;
  const acceptNum = acceptValue.trim() === '' ? NaN : Number(acceptValue);
  const validValue = !Number.isNaN(acceptNum);
  const valueChanged = validValue && suggestedValue != null && acceptNum !== suggestedValue;

  const openConfirm = (m: ApplyMethod) => {
    setMethod(m);
    setError('');
    setOverrideRisk(false);
    setAcceptValue(suggestedValue != null ? String(suggestedValue) : '');
    setConfirmOpen(true);
  };

  const doApply = async () => {
    setApplying(true);
    setError('');
    try {
      const res = await apiTriage.applyThresholdSuggestion({
        alert_rule_key: data.alert_rule_key,
        cloud_account_id: data.cloud_account_id,
        method,
        override_risk: overrideRisk,
        accept_value: validValue ? acceptNum : undefined,
        ...(method === 'pr'
          ? {
              git_provider: selectedIntegration?.type || 'github',
              git_integration: selectedIntegration?.name || '',
              git_org: derivedOrg,
              git_repo: derivedRepo,
              git_branch: gitBranch,
              git_file_path: gitFilePath,
            }
          : {}),
      });
      if (!res || res.error) {
        setError(res?.error || 'Apply failed');
        snackbar.error(res?.error || 'Failed to apply threshold');
        return;
      }
      setResult(res);
      setApplyStatus(res.status);
      setConfirmOpen(false);
      if (res.status === 'pending') {
        snackbar.success('Pull request raised to update the alert rule.');
      } else {
        const applied = res.applied_threshold ?? acceptNum;
        snackbar.success(`Threshold applied${Number.isFinite(applied) ? `: ${operator} ${applied}` : ''}.`);
      }
      onApplied?.();
    } catch (e: any) {
      setError(e?.message || 'Apply failed');
      snackbar.error(e?.message || 'Failed to apply threshold');
    } finally {
      setApplying(false);
    }
  };

  const doRevert = async () => {
    setApplying(true);
    setError('');
    try {
      const res = await apiTriage.revertThresholdSuggestion({
        alert_rule_key: data.alert_rule_key,
        cloud_account_id: data.cloud_account_id,
      });
      if (!res || res.error) {
        setError(res?.error || 'Revert failed');
        snackbar.error(res?.error || 'Failed to revert threshold');
        return;
      }
      setResult(res);
      setApplyStatus('reverted');
      snackbar.success('Threshold reverted to its previous value.');
      onApplied?.();
    } catch (e: any) {
      setError(e?.message || 'Revert failed');
      snackbar.error(e?.message || 'Failed to revert threshold');
    } finally {
      setApplying(false);
    }
  };

  const prRepoValid = reposFromIntegration.length > 0 ? !!selectedRepoKey : !!derivedOrg && !!derivedRepo;
  const prInputsValid = method !== 'pr' || (!!selectedIntegration && prRepoValid && !!gitFilePath.trim());
  const confirmDisabled = applying || !selectedAvailable || !validValue || (requiresOverride && !overrideRisk) || !prInputsValid || noIntegration;

  const alreadyApplied = applyStatus === 'applied';

  return (
    <Box
      data-testid='threshold-apply-panel'
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[2],
        p: ds.space[3],
        borderRadius: ds.radius.md,
        backgroundColor: ds.gray[100],
        border: `1px solid ${ds.gray[200]}`,
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
        <Text value='Apply this suggestion' sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold }} />
        {applyStatus && (
          <Chip variant='tag' size='xs' tone={statusTone(applyStatus)}>
            {statusLabel(applyStatus)}
          </Chip>
        )}
        {requiresOverride && (
          <Chip variant='tag' size='xs' tone='critical'>
            High risk
          </Chip>
        )}
      </Box>

      {(applyStatus === 'applied' || applyStatus === 'pending' || applyStatus === 'reverted') && (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
          {applyStatus === 'reverted'
            ? revertedTo != null && <Text value={`Reverted to ${operator} ${revertedTo}`} sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
            : appliedThreshold != null && (
                <Text
                  value={`Applied ${operator} ${appliedThreshold}${appliedPrevious != null ? ` (was ${operator} ${appliedPrevious})` : ''}`}
                  sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}
                />
              )}
          <Button tone='link' size='xs' href='/user-management#audits'>
            View in audit log
          </Button>
        </Box>
      )}

      {loadingOptions ? (
        <Text value='Checking how this can be applied…' sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
      ) : (
        <>
          {options?.preview_expr && <RulePreviewBlock expr={options.preview_expr} />}
          {options?.preview_error && <Text value={options.preview_error} sx={{ fontSize: ds.text.caption, color: ds.amber[700] }} />}

          <Box sx={{ display: 'flex', gap: ds.space[2], flexWrap: 'wrap' }}>
            <Button
              id='threshold-apply-btn'
              data-testid='threshold-apply-btn'
              size='xs'
              tone='primary'
              disabled={!directOption?.available || alreadyApplied}
              tooltip={!directOption?.available ? directOption?.reason : undefined}
              onClick={() => openConfirm('direct')}
            >
              Apply directly
            </Button>
            <Button
              id='threshold-apply-pr-btn'
              data-testid='threshold-apply-pr-btn'
              size='xs'
              tone='secondary'
              disabled={!prOption?.available || alreadyApplied}
              tooltip={!prOption?.available ? prOption?.reason : undefined}
              onClick={() => openConfirm('pr')}
            >
              Raise PR
            </Button>
            {alreadyApplied && (
              <Button id='threshold-revert-btn' data-testid='threshold-revert-btn' size='xs' tone='ghost' loading={applying} onClick={doRevert}>
                Revert
              </Button>
            )}
          </Box>

          {result?.status === 'pending' && result?.resolution_id && (
            <Text value='A pull request has been raised. Track it in your repository.' sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
          )}
          {error && <Text value={error} sx={{ fontSize: ds.text.caption, color: ds.red[700] }} />}
        </>
      )}

      <Modal
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        width='sm'
        title={method === 'pr' ? 'Raise a PR to update this alert' : 'Apply threshold change'}
        actionButtons={
          <Box sx={{ display: 'flex', gap: ds.space[2], justifyContent: 'flex-end' }}>
            <Button size='sm' tone='ghost' onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button
              id='threshold-apply-confirm'
              data-testid='threshold-apply-confirm'
              size='sm'
              tone={requiresOverride ? 'danger' : 'primary'}
              loading={applying}
              disabled={confirmDisabled}
              onClick={doApply}
            >
              {method === 'pr' ? 'Raise PR' : 'Apply'}
            </Button>
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
          {/* Editable threshold: prefilled with the suggested value, applied as accept_value. */}
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
            <Text value='New threshold' sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
              <Text value={`${operator} ${currentThreshold}`} sx={{ color: ds.gray[600] }} />
              <Text value='→' sx={{ color: ds.gray[600] }} />
              <Text value={operator} sx={{ fontWeight: ds.weight.semibold }} />
              <Box sx={{ width: 120 }}>
                <Input value={acceptValue} onChange={setAcceptValue} size='sm' type='number' inputMode='decimal' placeholder='value' />
              </Box>
            </Box>
            {!validValue && <Text value='Enter a numeric threshold.' sx={{ fontSize: ds.text.caption, color: ds.red[700] }} />}
            {valueChanged && (
              <Text
                value={`Suggested ${operator} ${suggestedValue}. The preview below reflects the suggested value; ${acceptValue} will be applied.`}
                sx={{ fontSize: ds.text.caption, color: ds.amber[700] }}
              />
            )}
          </Box>

          {options?.preview_expr && <RulePreviewBlock expr={options.preview_expr} />}

          {method === 'pr' && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
              <Text value='Repository details' sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
              {loadingIntegrations ? (
                <Text value='Loading Git integrations…' sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
              ) : noIntegration ? (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                  <Text
                    value='No GitHub or GitLab integration is configured. Configure one to raise a PR.'
                    sx={{ fontSize: ds.text.caption, color: ds.amber[700] }}
                  />
                  <Button size='sm' tone='primary' href={INTEGRATIONS_SETTINGS_URL}>
                    Configure integration
                  </Button>
                </Box>
              ) : (
                <>
                  <Select
                    label='Integration'
                    value={gitIntegrationKey || null}
                    onChange={(v) => {
                      setGitIntegrationKey((v as string) || '');
                      setSelectedRepoKey('');
                    }}
                    placeholder='Select a Git integration'
                    options={gitIntegrations.map((i) => ({ value: i.key, label: i.label }))}
                  />
                  {reposFromIntegration.length > 0 ? (
                    <Select
                      label='Repository'
                      value={selectedRepoKey || null}
                      onChange={(v) => setSelectedRepoKey((v as string) || '')}
                      placeholder='Select a repository'
                      searchable
                      options={reposFromIntegration.map((r) => ({ value: r.key, label: r.name || r.key }))}
                    />
                  ) : (
                    <>
                      <Text
                        value='No repositories found for this integration. Enter the org and repo manually.'
                        sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}
                      />
                      <Input value={gitOrg} onChange={setGitOrg} label='Org / owner' placeholder='my-org' />
                      <Input value={gitRepo} onChange={setGitRepo} label='Repository' placeholder='infra-gitops' />
                    </>
                  )}
                  <Input value={gitBranch} onChange={setGitBranch} label='Base branch' placeholder='main' />
                  <Input value={gitFilePath} onChange={setGitFilePath} label='Rule file path' placeholder='prometheus/rules/app.yaml' />
                </>
              )}
            </Box>
          )}

          {requiresOverride && (
            <Checkbox checked={overrideRisk} onChange={setOverrideRisk} label='I understand this is a high-risk change and want to proceed' />
          )}

          {error && <Text value={error} sx={{ fontSize: ds.text.caption, color: ds.red[700] }} />}
        </Box>
      </Modal>
    </Box>
  );
};

export default ThresholdApplyPanel;
