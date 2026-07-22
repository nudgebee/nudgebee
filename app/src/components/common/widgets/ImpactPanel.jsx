import { useState, useEffect } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography, Chip } from '@mui/material';
import apiKubernetes1 from '@api1/kubernetes1';
import Loader from '@shared/Loader';

// Priority -> design-system color + severity rank (lower rank = more severe).
const prioColor = (p) => {
  const s = String(p || '').toUpperCase();
  if (s === 'P0' || s === 'P1') return 'var(--ds-red-500)';
  if (s === 'P2') return 'var(--ds-amber-500)';
  return 'var(--ds-gray-400)';
};
const prioRank = (p) => {
  const s = String(p || '').toUpperCase();
  return { P0: 0, P1: 1, P2: 2, P3: 3 }[s] ?? 4;
};
const topAlert = (alerts) =>
  Array.isArray(alerts) && alerts.length ? [...alerts].sort((a, b) => prioRank(a.priority) - prioRank(b.priority))[0] : null;

const MetaChip = ({ label }) => (
  <Chip
    size='small'
    label={label}
    sx={{ height: 18, fontSize: 10, bgcolor: 'var(--ds-brand-100)', color: 'var(--ds-gray-600)', border: '1px solid var(--ds-brand-150)' }}
  />
);

// ImpactPanel renders the topology-driven correlation for an incident: the root subject,
// the dependent services actively alerting in the window (the correlated downstream
// incidents this root caused), and the remaining dependents as potential impact.
const ImpactPanel = ({ eventId }) => {
  const router = useRouter();
  const accountId = router?.query?.accountId || '';
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!eventId) {
      return undefined;
    }
    let active = true;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const res = await apiKubernetes1.getImpactData(eventId);
        if (active) {
          setData(res?.data?.data?.event_get_impact || null);
        }
      } catch (e) {
        console.error(e);
        if (active) {
          setError('Failed to load impact data.');
        }
      } finally {
        if (active) {
          setLoading(false);
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [eventId]);

  if (loading) {
    return (
      <Box display='flex' justifyContent='center' p={2}>
        <Loader style={{ position: 'static', width: 40, height: 120 }} />
      </Box>
    );
  }
  if (error) {
    return <Typography sx={{ p: 2, color: 'var(--ds-red-500)' }}>{error}</Typography>;
  }
  if (!data || !data.resolved) {
    return (
      <Typography sx={{ p: 2, color: 'var(--ds-gray-500)' }}>
        Impact unknown — the subject could not be located in the service topology (coverage may be incomplete).
      </Typography>
    );
  }

  const impacted = Array.isArray(data.impacted) ? data.impacted : [];
  const dependsOn = Array.isArray(data.depends_on) ? data.depends_on : [];
  const correlated = impacted.filter((s) => s.alerting);
  const potential = impacted.filter((s) => !s.alerting);
  const seed = data.seed || {};
  const lowCoverage = data.coverage_confidence === 'none' || data.coverage_confidence === 'low';
  const goTo = (evId) => evId && router.push(`/investigate?id=${evId}&accountId=${accountId}`);

  return (
    <Box sx={{ p: 1 }}>
      {/* Root + one-line summary */}
      <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 1, mb: 0.5 }}>
        <Chip size='small' label='ROOT' sx={{ bgcolor: 'var(--ds-red-500)', color: '#fff', fontWeight: 700, height: 20, letterSpacing: 0.5 }} />
        <Typography sx={{ fontWeight: 700 }}>{seed.name}</Typography>
        <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 13 }}>
          · {seed.namespace} · {seed.type}
        </Typography>
      </Box>
      <Typography sx={{ fontSize: 12, color: 'var(--ds-gray-500)', mb: 1.5 }}>
        Blast radius: <b style={{ color: 'var(--ds-red-500)' }}>{correlated.length} impacted &amp; alerting</b>
        {potential.length > 0 ? ` · ${potential.length} potential` : ''} — grouped into one incident, root cause above.
      </Typography>

      {/* Correlated (alerting) */}
      <Typography sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-600)', mb: 0.75 }}>
        Correlated incidents — {correlated.length} alerting
      </Typography>
      {correlated.length === 0 && (
        <Typography sx={{ fontSize: 13, color: 'var(--ds-gray-500)', mb: 1 }}>
          {impacted.length === 0
            ? `No in-cluster services depend on ${seed.name} — it may be an edge/entry service, or lack trace coverage.`
            : 'None of its dependents are currently alerting.'}
        </Typography>
      )}
      {correlated.map((s, i) => {
        const top = topAlert(s.active_alerts);
        const accent = prioColor(top?.priority);
        return (
          <Box
            key={`c${i}`}
            onClick={() => goTo(top?.event_id)}
            sx={{
              borderLeft: `3px solid ${accent}`,
              bgcolor: 'var(--ds-background-100)',
              borderRadius: 'var(--ds-radius-sm)',
              boxShadow: '0 1px 2px var(--ds-gray-alpha-200, rgba(0,0,0,0.06))',
              p: 1,
              mb: 0.75,
              cursor: top?.event_id ? 'pointer' : 'default',
              transition: 'background 120ms',
              '&:hover': { bgcolor: top?.event_id ? 'var(--ds-brand-100)' : 'var(--ds-background-100)' },
            }}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 0.75, mb: 0.25 }}>
              <Typography sx={{ fontWeight: 600, fontSize: 13.5 }}>{s.name}</Typography>
              <MetaChip label={s.node_type} />
              <MetaChip label={`${s.hops_away}-hop`} />
              <MetaChip label={`via ${s.relationship}`} />
            </Box>
            {(s.active_alerts || []).slice(0, 3).map((a, j) => (
              <Box key={j} sx={{ display: 'flex', alignItems: 'center', gap: 0.75, mt: 0.25 }}>
                <Chip
                  size='small'
                  label={a.priority}
                  sx={{ height: 16, minWidth: 26, fontSize: 10, fontWeight: 700, bgcolor: prioColor(a.priority), color: '#fff' }}
                />
                <Typography sx={{ fontSize: 12, color: 'var(--ds-gray-600)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {a.title}
                </Typography>
              </Box>
            ))}
          </Box>
        );
      })}

      {/* Potential (topology-only) */}
      {potential.length > 0 && (
        <>
          <Typography
            sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-600)', mt: 2, mb: 0.75 }}
          >
            Potentially impacted — {potential.length} not alerting
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
            {potential.map((s, i) => (
              <Chip
                key={`p${i}`}
                size='small'
                label={`${s.name} · ${s.hops_away}-hop`}
                sx={{ height: 22, fontSize: 12, bgcolor: 'transparent', color: 'var(--ds-gray-600)', border: '1px dashed var(--ds-brand-150)' }}
              />
            ))}
          </Box>
        </>
      )}

      {dependsOn.length > 0 && (
        <>
          <Typography
            sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-600)', mt: 2, mb: 0.75 }}
          >
            Depends on — {dependsOn.length} (possible cause to check)
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
            {dependsOn.map((s, i) => (
              <Chip
                key={`d${i}`}
                size='small'
                label={`${s.name}${s.namespace ? ` · ${s.namespace}` : ''} · ${s.hops_away}-hop`}
                sx={{
                  height: 22,
                  fontSize: 12,
                  bgcolor: 'var(--ds-brand-100)',
                  color: 'var(--ds-gray-600)',
                  border: '1px solid var(--ds-brand-150)',
                }}
              />
            ))}
          </Box>
        </>
      )}

      {lowCoverage && (
        <Typography sx={{ fontSize: 11, color: 'var(--ds-amber-500)', mt: 1.5 }}>
          ⚠ Impact may be incomplete — topology coverage is {data.coverage_confidence}.
        </Typography>
      )}
    </Box>
  );
};

export default ImpactPanel;
