import { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import { Modal } from '@ui/Modal';
import apiTickets from '@api1/tickets';
import apiRecommendations from '@api1/recommendation';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { ds } from 'src/utils/colors';

const detectProvider = (repoUrl) => {
  if (!repoUrl) return null;
  const url = repoUrl.toLowerCase();
  if (url.includes('github')) return 'github';
  if (url.includes('gitlab')) return 'gitlab';
  return null;
};

const shortRepo = (repoUrl) => {
  if (!repoUrl) return '';
  return repoUrl
    .replace(/^https?:\/\//, '')
    .replace(/^github\.com\//, '')
    .replace(/^gitlab\.com\//, '')
    .replace(/\.git$/, '');
};

/**
 * "Raise PR" confirmation for an event's proposed code fix. Renders a compact
 * button under the diff that opens a modal with a Git integration picker and an
 * optional guidance field. Confirming calls applyRecommendation
 * (source='event' -> okRaisePR), which dispatches the code agent to fix the
 * identified issue and open the PR. The proposed diff and any guidance the user
 * adds are passed as intent — the agent re-applies or adapts the fix to the
 * current code, it does not blindly replay a possibly-stale patch.
 */
const EventRaisePrPanel = ({ data, repoUrl, filePath }) => {
  const [expanded, setExpanded] = useState(false);
  const [integrations, setIntegrations] = useState([]);
  const [loadingIntegrations, setLoadingIntegrations] = useState(false);
  const [selected, setSelected] = useState('');
  const [guidance, setGuidance] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState(null); // { ok, message, url }
  // Fetch integrations exactly once (on first expand). Keying the effect on
  // integrations.length/loadingIntegrations would re-fire — and re-fetch
  // forever — whenever the list comes back empty. A ref makes it fire-once.
  const fetchedRef = useRef(false);

  useEffect(() => {
    if (!expanded || fetchedRef.current) {
      return;
    }
    fetchedRef.current = true;
    setLoadingIntegrations(true);
    // Same source as the rightsizing PR flow: enabled tenant git integrations.
    apiTickets
      .listTicketConfigsForCreate({ status: 'enabled' })
      .then((res) => {
        const configs = res?.data || [];
        const github = configs.filter((c) => c?.tool === 'github').map((g) => ({ key: `github:${g.name}`, label: `GitHub: ${g.name}` }));
        const gitlab = configs.filter((c) => c?.tool === 'gitlab').map((g) => ({ key: `gitlab:${g.name}`, label: `GitLab: ${g.name}` }));
        setIntegrations([...github, ...gitlab]);
      })
      .catch(() => setIntegrations([]))
      .finally(() => setLoadingIntegrations(false));
  }, [expanded]);

  const provider = detectProvider(repoUrl);
  const options = useMemo(() => {
    const filtered = provider ? integrations.filter((i) => i.key.startsWith(`${provider}:`)) : integrations;
    return filtered.map((i) => ({ value: i.key, label: i.label }));
  }, [integrations, provider]);

  // Auto-select the first matching integration so a single-integration tenant is one click.
  useEffect(() => {
    if (!selected && options.length > 0) setSelected(options[0].value);
  }, [options, selected]);

  const handleRaise = () => {
    if (!selected) {
      return;
    }
    const [integrationType, ...nameParts] = selected.split(':');
    const integrationName = nameParts.join(':');
    setSubmitting(true);
    setResult(null);
    const payload = { ...data, raisePR: true };
    if (guidance.trim()) payload.guidance = guidance.trim();
    apiRecommendations
      .applyRecommendation(data.accountId, data.id, payload, integrationType, { name: integrationName }, 'event')
      .then((res) => {
        if (res?.errors && res.errors.length > 0) {
          setResult({ ok: false, message: `Failed to raise PR: ${parseHttpResponseBodyMessage(res)}` });
          return;
        }
        let url = '';
        try {
          // The API client may hand back `pr` already parsed; only JSON.parse a string.
          const prData = res?.data?.[0]?.pr;
          const parsedPr = typeof prData === 'string' ? JSON.parse(prData) : prData;
          url = parsedPr?.html_url || '';
        } catch {
          url = '';
        }
        setResult({
          ok: true,
          url,
          message: url ? 'Pull request raised.' : 'Raising pull request — it will appear at the top of the analysis shortly.',
        });
        // The PR resolution row is inserted server-side asynchronously; signal the
        // investigate page to poll for it so the "PR raised" banner surfaces.
        if (typeof window !== 'undefined') {
          window.dispatchEvent(new CustomEvent('nb:event-resolution-changed'));
        }
      })
      .catch((e) => setResult({ ok: false, message: `Failed to raise PR: ${e?.message || 'unknown error'}` }))
      .finally(() => setSubmitting(false));
  };

  const noIntegrations = expanded && !loadingIntegrations && options.length === 0;

  const closeModal = () => {
    if (submitting) {
      return;
    }
    setExpanded(false);
  };

  return (
    <Box sx={{ mt: 'var(--ds-space-3)' }}>
      <Button size='sm' onClick={() => setExpanded(true)}>
        Raise PR
      </Button>

      <Modal open={expanded} onClose={closeModal} title='Raise a pull request' width='sm'>
        <Box>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mb: 'var(--ds-space-4)' }}>
            Raises a pull request fixing this issue in <strong>{shortRepo(repoUrl)}</strong>
            {filePath ? (
              <>
                {' · '}
                <code>{filePath}</code>
              </>
            ) : null}
          </Typography>

          <Box sx={{ mb: 'var(--ds-space-4)' }}>
            <Select
              label='Git Integration'
              value={selected || ''}
              minWidth={'250px'}
              options={options}
              onChange={(v) => setSelected(v)}
              loading={loadingIntegrations}
              disabled={loadingIntegrations || options.length === 0}
            />
          </Box>

          {noIntegrations && (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700], mb: 'var(--ds-space-4)' }}>
              No Git integration is configured. <Link href='/settings/integrations'>Please configure a GitHub or GitLab integration</Link>.
            </Typography>
          )}

          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mb: 'var(--ds-space-1)' }}>
            Additional guidance (optional)
          </Typography>
          <textarea
            value={guidance}
            onChange={(e) => setGuidance(e.target.value)}
            placeholder='Steer how the fix is implemented — constraints, a preferred approach, or things to avoid.'
            rows={3}
            style={{
              width: '100%',
              boxSizing: 'border-box',
              padding: 'var(--ds-space-2) var(--ds-space-3)',
              border: `0.5px solid ${ds.gray[300]}`,
              borderRadius: 'var(--ds-radius-sm)',
              fontFamily: 'inherit',
              fontSize: 'var(--ds-text-body)',
              resize: 'vertical',
              backgroundColor: ds.background[100],
              color: ds.gray[900],
            }}
          />

          {result && (
            <Typography
              sx={{
                mt: 'var(--ds-space-3)',
                fontSize: 'var(--ds-text-body)',
                color: result.ok ? ds.green[600] : ds.red[600],
              }}
            >
              {result.message}{' '}
              {result.url ? (
                <Link href={result.url} target='_blank' rel='noreferrer'>
                  View PR
                </Link>
              ) : null}
            </Typography>
          )}

          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-2)', mt: 'var(--ds-space-5)' }}>
            <Button size='sm' tone='secondary' onClick={closeModal} disabled={submitting}>
              Cancel
            </Button>
            <Button size='sm' onClick={handleRaise} disabled={!selected || submitting || (result && result.ok)}>
              {submitting ? <CircularProgress size={16} /> : 'Raise PR'}
            </Button>
          </Box>
        </Box>
      </Modal>
    </Box>
  );
};

export default EventRaisePrPanel;
