import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Stack, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Banner } from '@ui/Banner';
import { ProgressLinear } from '@ui/ProgressLinear';
import { Form } from '@shared/forms/Form';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { useLlmAsyncPolling } from '@hooks/useLlmAsyncPolling';
import { ds } from '@utils/colors';
import apiDashboards, { KUBERNETES_ACCOUNT_KIND, type AccountOption, type Dashboard, type Panel } from '@api1/dashboards';
import { panelScope } from './panelAccounts';
import { convertNativeDashboard } from './nativeImport';
import {
  checkPanels,
  extractGeneratedDashboard,
  type GeneratedDashboardModel,
  type PanelCheckResult,
  type StashedDashboardDraft,
} from './generatedDashboard';

interface Props {
  open: boolean;
  /** Accounts the generated panels may be scoped to. */
  accountOptions: AccountOption[];
  onClose: () => void;
  /** Receives the generated dashboard unsaved (no id) — the editor's Save is what creates it. */
  onDraft: (dashboard: Dashboard) => void;
  /**
   * A dashboard already generated — handed over from its Nubi conversation. It
   * skips generation and goes through the same check and preview a fresh one
   * does, so a panel whose query fails is left out either way.
   */
  initialDraft?: StashedDashboardDraft | null;
}

type Phase = 'idle' | 'generating' | 'checking' | 'ready' | 'failed';

interface Preview {
  title: string;
  description: string;
  panels: Panel[];
  /** What the agent and the importer dropped or changed. */
  warnings: string[];
  checks: Record<number, PanelCheckResult>;
}

const EXAMPLES = [
  'Kubernetes cluster health: CPU, memory, pod restarts and node count',
  'Per-namespace CPU and memory usage against requests',
  'Redis overview: memory, connected clients and commands per second',
];

/**
 * Builds a dashboard from a description. The fourth way to start one, next to a
 * template, an import and a blank page — and it ends where they do: a draft in
 * the editor, saved only by the editor's Save.
 *
 * The agent writes PromQL against metric names it found on the chosen account.
 * Before the editor opens, every panel is tried once against that account with
 * the same request the panel renders with: a panel whose query fails is left
 * out, and one that returns nothing is kept but named, so the author decides.
 */
const GenerateDashboardModal: React.FC<Props> = ({ open, accountOptions, onClose, onDraft, initialDraft }) => {
  const { assistantName } = useTenantBranding();
  const [prompt, setPrompt] = useState('');
  const [accountId, setAccountId] = useState('');
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState('');
  const [preview, setPreview] = useState<Preview | null>(null);
  /**
   * Bumped by every start and every close. Polling and the panel checks finish
   * after their own await; a result that lands once the user has cancelled or
   * started again must not overwrite what they are looking at now.
   */
  const runSeq = useRef(0);
  const { startPolling, stopPolling } = useLlmAsyncPolling({ accountId, pollInterval: 3000 });

  const activeAccounts = useMemo(() => accountOptions.filter((o) => !o.status || o.status === 'active'), [accountOptions]);
  const accountSelectOptions = useMemo(() => activeAccounts.map((o) => ({ label: o.label, value: o.value })), [activeAccounts]);

  // Defaults to the first live Kubernetes account: generation needs PromQL, and a
  // cloud account's metrics are refused. Falls back to the first live account when
  // the tenant has no cluster (its backend may still be Prometheus).
  useEffect(() => {
    if (accountId || activeAccounts.length === 0) return;
    setAccountId((activeAccounts.find((o) => o.kind === KUBERNETES_ACCOUNT_KIND) || activeAccounts[0]).value);
  }, [accountId, activeAccounts]);

  const reset = () => {
    runSeq.current += 1;
    stopPolling();
    setPrompt('');
    setPhase('idle');
    setError('');
    setPreview(null);
  };

  const close = () => {
    reset();
    onClose();
  };

  const fail = (seq: number, message: string) => {
    if (runSeq.current !== seq) return;
    setError(message);
    setPhase('failed');
  };

  const buildPreview = async (seq: number, model: GeneratedDashboardModel, forAccount: string) => {
    // The same converter Import uses, so the draft is shaped and scoped exactly
    // as an imported file would be — one account, named explicitly.
    const converted = convertNativeDashboard(model, panelScope('', [forAccount]));
    if (converted.definition.panels.length === 0) {
      fail(seq, converted.warnings[0] || 'No usable panel came back. Try describing the dashboard differently.');
      return;
    }
    setPhase('checking');
    const checks = await checkPanels(converted.definition.panels, forAccount);
    if (runSeq.current !== seq) return;
    setPreview({
      title: converted.title,
      description: converted.description,
      panels: converted.definition.panels,
      warnings: [...(model.warnings || []), ...converted.warnings],
      checks,
    });
    setPhase('ready');
  };

  const handleGenerate = async () => {
    const query = prompt.trim();
    if (!query || !accountId) return;
    runSeq.current += 1;
    const seq = runSeq.current;
    setError('');
    setPreview(null);
    setPhase('generating');

    const res = await apiDashboards.generateDashboard({ account_id: accountId, query });
    if (runSeq.current !== seq) return;
    const errors = res.errors as any[] | undefined;
    if (errors?.length) {
      fail(seq, String(errors[0]?.message || 'Could not start generating the dashboard.'));
      return;
    }
    const sessionId = res.data?.session_id;
    if (!sessionId) {
      fail(seq, 'Could not start generating the dashboard.');
      return;
    }
    startPolling(sessionId, (conversation) => {
      if (runSeq.current !== seq) return;
      const extracted = extractGeneratedDashboard(conversation);
      if ('error' in extracted) fail(seq, extracted.error);
      else void buildPreview(seq, extracted.model, accountId);
    });
  };

  // A draft handed over from Nubi: preview it for the account it was generated on.
  useEffect(() => {
    if (!open || !initialDraft) return;
    runSeq.current += 1;
    const seq = runSeq.current;
    stopPolling();
    setPrompt(initialDraft.prompt || '');
    setAccountId(initialDraft.accountId);
    setError('');
    setPreview(null);
    setPhase('checking');
    void buildPreview(seq, initialDraft.model, initialDraft.accountId);
    // Keyed on the hand-off itself; buildPreview and stopPolling are per-render closures.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialDraft]);

  const usablePanels = useMemo(() => (preview ? preview.panels.filter((p) => preview.checks[p.id]?.status !== 'error') : []), [preview]);
  const failedPanels = useMemo(() => (preview ? preview.panels.filter((p) => preview.checks[p.id]?.status === 'error') : []), [preview]);
  const emptyPanels = useMemo(() => (preview ? preview.panels.filter((p) => preview.checks[p.id]?.status === 'empty') : []), [preview]);

  const handleOpen = () => {
    if (!preview || usablePanels.length === 0) return;
    // A panel that answered keeps only the queries that ran; a failed one would
    // render as a warning on every load.
    const panels = usablePanels.map((panel) => {
      const failed = preview.checks[panel.id]?.failedRefIds;
      return failed?.length ? { ...panel, targets: (panel.targets || []).filter((t) => !failed.includes(t.ref_id)) } : panel;
    });
    // No id: a draft, exactly as Import and the template gallery hand over.
    onDraft({ id: '', title: preview.title, description: preview.description, definition: { panels } });
    reset();
  };

  const busy = phase === 'generating' || phase === 'checking';
  const partlyFailedPanels = preview ? preview.panels.filter((p) => preview.checks[p.id]?.failedRefIds?.length) : [];

  // The preview was built for one description on one account. Changing either
  // would open a draft for something else, so a change sends it back to Generate.
  const discardPreview = () => {
    if (phase === 'ready' || phase === 'failed') {
      runSeq.current += 1;
      setPreview(null);
      setError('');
      setPhase('idle');
    }
  };
  const changeAccount = (next: string) => {
    setAccountId(next);
    discardPreview();
  };
  const changePrompt = (next: string) => {
    setPrompt(next);
    discardPreview();
  };

  return (
    <Modal
      open={open}
      handleClose={close}
      title={`Generate a dashboard with ${assistantName}`}
      width='md'
      backdropClickClose={false}
      actionButtons={
        <Stack direction='row' gap={ds.space[3]} justifyContent='flex-end' sx={{ width: '100%', button: { minWidth: '140px' } }}>
          <Button tone='secondary' onClick={close} id='generate-dashboard-cancel-btn'>
            Cancel
          </Button>
          {phase === 'ready' ? (
            <Button
              onClick={handleOpen}
              disabled={usablePanels.length === 0}
              id='generate-dashboard-open-btn'
              data-testid='generate-dashboard-open-btn'
            >
              Open in editor
            </Button>
          ) : (
            <Button
              onClick={handleGenerate}
              disabled={busy || !prompt.trim() || !accountId}
              id='generate-dashboard-submit-btn'
              data-testid='generate-dashboard-submit-btn'
            >
              {phase === 'failed' ? 'Try again' : 'Generate'}
            </Button>
          )}
        </Stack>
      }
    >
      <Form variant='stacked' density='default'>
        <Form.Section title='What should it show?'>
          <Input
            type='textarea'
            minRows={3}
            maxRows={8}
            value={prompt}
            onChange={changePrompt}
            disabled={busy}
            placeholder={EXAMPLES[0]}
            id='generate-dashboard-prompt'
          />
          <Stack direction='row' gap={ds.space[2]} flexWrap='wrap'>
            {EXAMPLES.map((example) => (
              <Chip key={example} size='2xs' tone='subtle' onClick={busy ? undefined : () => changePrompt(example)}>
                {example}
              </Chip>
            ))}
          </Stack>
          <Form.Field label='Account' description='Metric names are looked up on this account, and the panels are scoped to it.'>
            <Select
              value={accountId}
              options={accountSelectOptions}
              onChange={changeAccount}
              disabled={busy}
              placeholder='Select…'
              id='generate-dashboard-account'
            />
          </Form.Field>
        </Form.Section>

        {busy && (
          <Box data-testid='generate-dashboard-progress'>
            <ProgressLinear aria-label='Generating dashboard' />
            <Typography variant='body2' sx={{ color: ds.gray[600], mt: ds.space[2] }}>
              {phase === 'generating'
                ? `${assistantName} is finding the metrics this account has and designing the panels. This usually takes under a minute.`
                : 'Trying each panel against the account…'}
            </Typography>
          </Box>
        )}

        {phase === 'failed' && error && <Banner tone='critical' surface='section' message={error} id='generate-dashboard-error' />}

        {phase === 'ready' && preview && (
          <Form.Section title='Preview' divider>
            <Stack direction='row' gap={ds.space[2]} alignItems='center' flexWrap='wrap'>
              <Typography
                sx={{ fontFamily: 'var(--ds-font-display)', fontSize: 'var(--ds-text-body)', fontWeight: ds.weight.semibold, color: ds.gray[700] }}
              >
                {preview.title}
              </Typography>
              <Chip size='2xs' tone={usablePanels.length > 0 ? 'info' : 'critical'}>
                {usablePanels.length} panel{usablePanels.length === 1 ? '' : 's'}
              </Chip>
            </Stack>
            {preview.description && (
              <Typography variant='body2' sx={{ color: ds.gray[600] }}>
                {preview.description}
              </Typography>
            )}

            <Box component='ul' sx={{ m: 0, pl: 0, listStyle: 'none' }} data-testid='generate-dashboard-panels'>
              {preview.panels.map((panel) => {
                const check = preview.checks[panel.id];
                return (
                  <Stack component='li' key={panel.id} direction='row' gap={ds.space[2]} alignItems='center' sx={{ py: ds.space[1] }}>
                    <Typography variant='body2' sx={{ color: ds.gray[700], minWidth: 0 }} noWrap title={panel.title}>
                      {panel.title}
                    </Typography>
                    <Chip size='2xs' tone='subtle'>
                      {panel.type}
                    </Chip>
                    {check?.status === 'empty' && (
                      <Chip size='2xs' tone='warning'>
                        No data right now
                      </Chip>
                    )}
                    {check?.status === 'error' && (
                      <Chip size='2xs' tone='critical'>
                        Query failed — left out
                      </Chip>
                    )}
                  </Stack>
                );
              })}
            </Box>

            {(emptyPanels.length > 0 || failedPanels.length > 0 || partlyFailedPanels.length > 0 || preview.warnings.length > 0) && (
              <Banner
                tone='warning'
                surface='section'
                title='Check before saving'
                message={
                  <Box component='ul' sx={{ m: 0, pl: 2.5 }} data-testid='generate-dashboard-warnings'>
                    {emptyPanels.length > 0 && (
                      <li>
                        {emptyPanels.map((p) => p.title).join(', ')} returned no data for this account right now. They are kept — edit or remove them
                        in the editor.
                      </li>
                    )}
                    {partlyFailedPanels.map((p) => (
                      <li key={`partial-${p.id}`}>
                        {p.title}: a query that failed was left out ({preview.checks[p.id]?.reason || 'its query failed'}).
                      </li>
                    ))}
                    {failedPanels.map((p) => (
                      <li key={p.id}>
                        {p.title} was left out: {preview.checks[p.id]?.reason || 'its query failed.'}
                      </li>
                    ))}
                    {preview.warnings.map((warning, i) => (
                      <li key={i}>{warning}</li>
                    ))}
                  </Box>
                }
                id='generate-dashboard-warnings-banner'
              />
            )}
          </Form.Section>
        )}
      </Form>
    </Modal>
  );
};

export default GenerateDashboardModal;
