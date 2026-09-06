import { useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { DropdownMenu } from '@ui/DropdownMenu';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import { Button } from '@ui/Button';
import { Banner } from '@ui/Banner';
import { ds } from '@utils/colors';
import { getKnowledgePolicy, saveKnowledgePolicy, KnowledgePolicy } from '@api1/knowledge-base/policy';

const options = [
  { value: 'auto', label: 'Auto (default)' },
  { value: 'always', label: 'Always discover' },
  { value: 'llm_only', label: 'Model-directed only' },
  { value: 'disabled', label: 'Disabled' },
];
const descriptions: Record<KnowledgePolicy, string> = {
  auto: 'Recommended for most requests. Automatically searches when knowledge may help and skips some simple requests. The model can also search when needed.',
  always:
    'Use when requests regularly depend on internal documentation. Searches on every non-empty request, which can add latency and retrieval cost. This does not guarantee every document is read.',
  llm_only: 'Let the model decide when to search. Avoids automatic retrieval, but the model may miss useful documentation.',
  disabled:
    'Use for troubleshooting or when this account should not retrieve knowledge. Turns off automatic discovery and knowledge search/load tools, so these sources will not be retrieved to inform answers.',
};

// Remount on account changes so pending reads/saves cannot update another account’s form.
export default function KnowledgePolicySettings(props: { accountId: string; canEdit: boolean }) {
  return <AccountPolicyForm key={props.accountId} {...props} />;
}

function AccountPolicyForm({ accountId, canEdit }: { accountId: string; canEdit: boolean }) {
  const [value, setValue] = useState<KnowledgePolicy>('auto');
  const [saved, setSaved] = useState<KnowledgePolicy | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);
  const [retry, setRetry] = useState(0);
  const mounted = useRef(false);

  useEffect(() => {
    mounted.current = true;
    let active = true;
    setLoading(true);
    setError('');
    getKnowledgePolicy(accountId)
      .then((policy) => {
        if (active) {
          setValue(policy);
          setSaved(policy);
        }
      })
      .catch((err) => {
        if (active) setError(err.message || 'Could not load the knowledge policy.');
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      mounted.current = false;
    };
  }, [accountId, retry]);

  const save = async () => {
    setSaving(true);
    setError('');
    setSuccess(false);
    try {
      await saveKnowledgePolicy(accountId, value);
      if (mounted.current) {
        setSaved(value);
        setSuccess(true);
      }
    } catch (err) {
      if (mounted.current) setError(err instanceof Error ? err.message : 'Could not save the knowledge policy.');
    } finally {
      if (mounted.current) setSaving(false);
    }
  };

  const summaries: Record<KnowledgePolicy, string> = {
    auto: 'Recommended · searches when knowledge may help.',
    always: 'Search every request · may add latency and cost.',
    llm_only: 'Model decides · may miss useful documentation.',
    disabled: 'No automatic or model-directed knowledge retrieval.',
  };

  return (
    <DropdownMenu
      align='end'
      disablePortal={false}
      minWidth='min(400px, calc(100vw - 32px))'
      itemsMaxHeight='min(320px, 45vh)'
      closeOnSelect={false}
      trigger={
        <Button
          data-testid='knowledge-retrieval-trigger'
          icon={<ExpandMoreIcon />}
          iconPlacement='end'
          tone='secondary'
          size='sm'
          onClick={() => {
            if (saved !== null && !saving) setValue(saved);
            setSuccess(false);
          }}
        >
          Knowledge retrieval: {loading ? 'Loading…' : saved === null ? 'Unavailable' : options.find((option) => option.value === saved)?.label}
        </Button>
      }
      loading={loading}
      items={
        saved === null
          ? []
          : options.map((option) => ({
              ...option,
              id: `knowledge-policy-${option.value}`,
              active: value === option.value,
              disabled: !canEdit || saving,
              description: summaries[option.value as KnowledgePolicy],
              onSelect: () => {
                setValue(option.value as KnowledgePolicy);
                setSuccess(false);
                setError('');
              },
            }))
      }
      footer={(close) => (
        <Box
          sx={{
            p: ds.space[3],
            borderTop: `1px solid ${ds.gray[200]}`,
            display: 'flex',
            flexDirection: 'column',
            gap: ds.space[2],
            maxWidth: 'min(400px, calc(100vw - 32px))',
          }}
        >
          {error && (
            <Banner
              tone='critical'
              message={error}
              actions={saved === null ? [{ label: 'Retry', onClick: () => setRetry((n) => n + 1) }] : undefined}
            />
          )}
          {saved !== null && (
            <>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{descriptions[value]}</Typography>
              {value === 'llm_only' && (
                <Banner
                  tone='warning'
                  message='Some custom agents require automatic discovery and cannot run in this mode. If an agent reports this limitation, select Auto or Always discover.'
                />
              )}
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                Applies to new requests in this account. Individual knowledge base enable/disable settings still apply.
              </Typography>
              {!canEdit && <Typography sx={{ fontSize: ds.text.small }}>Account write access is required to change this setting.</Typography>}
              {success && <Banner tone='success' message='Retrieval mode saved. It applies to new requests.' />}
            </>
          )}
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2] }}>
            <Button data-testid='cancel-knowledge-policy' tone='secondary' size='sm' onClick={close}>
              Cancel
            </Button>
            {canEdit && saved !== null && (
              <Button data-testid='save-knowledge-policy' size='sm' disabled={saving || value === saved || loading} onClick={save}>
                {saving ? 'Saving…' : 'Save'}
              </Button>
            )}
          </Box>
        </Box>
      )}
    />
  );
}
