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

// --- Incident-assembly (four-tier) rendering (#34658) ---

const TIERS = {
  core: { color: 'var(--ds-red-500)', label: 'Same problem' },
  cause: { color: 'var(--ds-amber-500)', label: 'What probably caused it' },
  impact: { color: 'var(--ds-green-500)', label: 'What it affected' },
  chronic: { color: 'var(--ds-gray-400)', label: 'Background noise' },
};

// The backend assembles from a 4-hour window ([root-2h, root+2h]). We use that to turn
// its "expected in window" figure into an everyday "times per day" number.
const WINDOW_HOURS = 4;

// dtLabel says in plain words when this alert fired, compared to the one you opened.
const dtLabel = (s) => {
  if (typeof s !== 'number') return '';
  const mins = Math.round(Math.abs(s) / 60);
  if (mins === 0) return 'same time';
  const when = s < 0 ? 'before' : 'after';
  if (mins < 60) return `${mins} min ${when}`;
  return `${Math.round(mins / 6) / 10} hr ${when}`;
};

// rarityBadge says how normal this alert is for its service, ALWAYS with the real
// number so there's no hidden threshold to guess at. Measured over the last 7 days:
//   under ~1 a day  -> "rarely happens"        (green)  — worth paying attention to
//   ~1-6 a day      -> "happens fairly often"  (amber)  — weak signal, shown honestly
//   ~6+ a day       -> "normally happens ~N a day" (grey) — background noise
const rarityBadge = (ev) => {
  if (!ev) return null;
  const expected = Number(ev.expected_in_window || 0);
  const observed = Number(ev.observed_in_window || 0);
  const perDay = expected * (24 / WINDOW_HOURS);
  // Usually noisy, but firing far above its own baseline right now. This is the guard
  // that stops a real escalation being buried as "background" — it keeps its lane.
  if (expected >= 1 && observed > 3 * expected) {
    return {
      label: `usually noisy — but ~${Math.round((observed / expected) * 10) / 10}× more than normal right now`,
      fg: 'var(--ds-red-600)',
      bg: 'var(--ds-red-100)',
    };
  }
  if (perDay <= 0) {
    return { label: 'not seen at all in the last 7 days', fg: 'var(--ds-green-600)', bg: 'var(--ds-green-100)' };
  }
  if (perDay >= 6) {
    return { label: `normally happens ~${Math.round(perDay)} times a day`, fg: 'var(--ds-gray-600)', bg: 'var(--ds-gray-200)' };
  }
  if (perDay >= 1) {
    return { label: `happens fairly often — ~${Math.round(perDay * 10) / 10} times a day`, fg: 'var(--ds-amber-600)', bg: 'var(--ds-amber-100)' };
  }
  const everyDays = Math.round(1 / perDay);
  return {
    label: `rarely happens — ${everyDays >= 7 ? 'about once a week or less' : `about once every ${everyDays} days`}`,
    fg: 'var(--ds-green-600)',
    bg: 'var(--ds-green-100)',
  };
};

// relationLabel says in plain words how this service is wired to the one you opened.
// "call each other" is shown honestly for two-way pairs, where cause-vs-impact is only
// a guess based on which alert fired first.
const relationLabel = (relation, seedName) => {
  const other = seedName || 'this service';
  if (relation === 'mutual') return `${other} and this one call each other`;
  if (relation === 'seed_calls') return `${other} calls this one`;
  if (relation === 'calls_seed') return `this one calls ${other}`;
  return '';
};

const shortName = (item) => (item.subject ? item.subject.split('|').pop() : '') || item.title || 'unknown';

const AggChip = ({ label }) => (
  <Box
    component='span'
    sx={{
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 10.5,
      color: 'var(--ds-gray-600)',
      bgcolor: 'var(--ds-background-100)',
      border: '1px solid var(--ds-gray-200)',
      px: 0.75,
      py: '1px',
      borderRadius: '5px',
    }}
  >
    {label}
  </Box>
);

// TierRow renders one collapsed alert (or config change) in a tier: its subject, timing,
// occurrence count, merged sources, and rarity badge.
const TierRow = ({ item, color, goTo, showRarity, isChange, folded, seedName }) => {
  const badge = showRarity ? rarityBadge(item.evidence) : null;
  const relation = relationLabel(item.relation, seedName);
  return (
    <Box
      onClick={() => goTo(item.event_id)}
      sx={{
        borderLeft: `3px solid ${color}`,
        bgcolor: folded ? 'var(--ds-gray-100)' : 'var(--ds-background-100)',
        borderRadius: 'var(--ds-radius-sm)',
        boxShadow: folded ? 'none' : '0 1px 2px rgba(0,0,0,0.05)',
        p: 1,
        mb: 0.75,
        cursor: item.event_id ? 'pointer' : 'default',
        transition: 'background 120ms',
        '&:hover': { bgcolor: item.event_id ? 'var(--ds-brand-100)' : undefined },
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, flexWrap: 'wrap' }}>
        <Typography sx={{ fontWeight: 620, fontSize: 13.5 }}>{shortName(item)}</Typography>
        {isChange && <AggChip label='was deployed / changed' />}
        {item.occurrence_count > 1 && (
          <Typography sx={{ fontSize: 11.5, fontWeight: 700, color: 'var(--ds-gray-500)' }}>happened {item.occurrence_count} times</Typography>
        )}
      </Box>
      {item.title && !isChange && (
        <Typography sx={{ fontSize: 12, color: 'var(--ds-gray-600)', mt: 0.25, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {item.title}
        </Typography>
      )}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', mt: 0.4 }}>
        {typeof item.dt_seconds === 'number' && (
          <Typography sx={{ fontSize: 11, color: 'var(--ds-gray-500)' }}>{dtLabel(item.dt_seconds)}</Typography>
        )}
        {relation && <Typography sx={{ fontSize: 11, color: 'var(--ds-gray-500)' }}>· {relation}</Typography>}
        {(item.sources || []).length > 1 && (
          <Typography sx={{ fontSize: 11, color: 'var(--ds-gray-400)' }}>· reported by {item.sources.join(' and ')}</Typography>
        )}
        {badge && (
          <Box
            component='span'
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              height: 17,
              px: 0.75,
              fontSize: 10.5,
              fontWeight: 600,
              color: badge.fg,
              bgcolor: badge.bg,
              borderRadius: '999px',
            }}
          >
            {badge.label}
          </Box>
        )}
      </Box>
    </Box>
  );
};

// TierSection renders a labelled tier with a colored dot, a plain count, and its rows.
// `label` overrides the tier's default heading (used to name the service explicitly);
// `unit` is the singular noun for the count ("alert" -> "3 alerts").
const TierSection = ({ tierKey, label, unit = 'alert', note, items, goTo, showRarity, folded, seedName }) => {
  if (!items || items.length === 0) return null;
  const t = TIERS[tierKey];
  return (
    <Box sx={{ mt: 1.75 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, mb: 0.5 }}>
        <Box sx={{ width: 8, height: 8, borderRadius: '50%', bgcolor: t.color, flex: 'none' }} />
        <Typography sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-700)' }}>
          {label || t.label}
        </Typography>
        <Typography sx={{ ml: 'auto', fontSize: 11, color: 'var(--ds-gray-400)' }}>
          {items.length} {unit}
          {items.length === 1 ? '' : 's'}
        </Typography>
      </Box>
      {note && <Typography sx={{ fontSize: 11.5, color: 'var(--ds-gray-400)', mb: 0.75 }}>{note}</Typography>}
      {items.map((it, i) => (
        <TierRow key={it.event_id || i} item={it} color={t.color} goTo={goTo} showRarity={showRarity} folded={folded} seedName={seedName} />
      ))}
    </Box>
  );
};

// PossibleGroup is one distance band of the possible-impact list. Rendering direct callers
// and further-out dependents as separate labelled rows keeps the distinction that matters:
// a direct caller is worth checking, whereas nearly every service in a small mesh sits two
// hops from a shared datastore. Each chip opens that workload in the service map.
const PossibleGroup = ({ label, items, goToService }) => {
  if (!items || items.length === 0) return null;
  return (
    <Box sx={{ mb: 0.75 }}>
      <Typography sx={{ fontSize: 11, color: 'var(--ds-gray-500)', mb: 0.4 }}>{label}</Typography>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
        {items.map((s, i) => (
          <Chip
            key={`${s.namespace}/${s.name}/${i}`}
            size='small'
            label={s.name}
            clickable
            onClick={() => goToService(s)}
            sx={{
              height: 22,
              fontSize: 12,
              bgcolor: 'transparent',
              color: 'var(--ds-gray-600)',
              border: '1px dashed var(--ds-brand-150)',
              '&:hover': { bgcolor: 'var(--ds-brand-100)' },
            }}
          />
        ))}
      </Box>
    </Box>
  );
};

// ImpactPanel renders the topology-driven correlation for an incident: the root subject,
// the dependent services actively alerting in the window (the correlated downstream
// incidents this root caused), and the remaining dependents as potential impact.
const ImpactPanel = ({ eventId, prefetched }) => {
  const router = useRouter();
  const accountId = router?.query?.accountId || '';
  const [data, setData] = useState(prefetched || null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!eventId) {
      return undefined;
    }
    // The card already fetched this to build its summary — reuse it rather than
    // asking the server for the same assembly a second time.
    if (prefetched) {
      setData(prefetched);
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
  }, [eventId, prefetched]);

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
  if (!data) {
    return (
      <Typography sx={{ p: 2, color: 'var(--ds-gray-500)' }}>
        Impact unknown — the subject could not be located in the service topology (coverage may be incomplete).
      </Typography>
    );
  }

  const seed = data.seed || {};
  const a = data.assembly;
  const causeCfg = (a && a.cause && a.cause.config_changes) || [];
  const causeUp = (a && a.cause && a.cause.upstream) || [];
  const hasAssembly =
    a && ((a.same_incident || []).length || causeCfg.length || causeUp.length || (a.impact || []).length || (a.chronic || []).length) > 0;

  if (hasAssembly) {
    const goTo = (evId) => evId && router.push(`/investigate?id=${evId}&accountId=${accountId}`);
    // "No impact found" is only meaningful when we actually have a service map to look
    // at. Unresolved subject or none/low coverage => we cannot tell, and must say so.
    const noCoverage = !data.resolved || data.coverage_confidence === 'none' || data.coverage_confidence === 'low';
    const incidentCount = (a.same_incident || []).length + causeCfg.length + causeUp.length + (a.impact || []).length;
    // The impact tier is alert-derived, so a dependent with no alert rule can never enter
    // it. Those dependents are already in this response — showing them as possible impact
    // is the difference between "nothing alerted" and "nothing was affected".
    // Split by distance: a direct caller is the one worth checking, while almost anything
    // is two hops from a shared datastore, so listing both as equals overstates the second.
    // Infrastructure dependents belong here too. The API reports them separately so they
    // cannot inflate dependent_count (which feeds recommendation safety scoring), but for
    // "what might this have hit" they are the same question — and on a VM stack they are
    // the only answer there is.
    const possible = [
      ...(Array.isArray(data.impacted) ? data.impacted : []).filter((s) => !s.alerting),
      ...(Array.isArray(data.infrastructure_impacted) ? data.infrastructure_impacted : []),
    ];
    const directDeps = possible.filter((s) => Number(s.hops_away) <= 1);
    const indirectDeps = possible.filter((s) => Number(s.hops_away) > 1);
    // The workload, not the pod that reported the alert — nothing depends on a pod, and
    // the cause section above already names the workload. root_identity is "ns|workload".
    const rootName = (a.root_identity || '').split('|').pop() || seed.name || 'this service';
    // The service map filtered to that workload: its live traffic and dependencies, which
    // is what you'd look at to decide whether it was actually hit. There is no alert to
    // open — that is the whole point of this section.
    const goToService = (s) =>
      accountId &&
      router.push(
        `/kubernetes/details/${accountId}?namespace=${encodeURIComponent(s.namespace)}&app=${encodeURIComponent(s.name)}#monitoring/service-map`
      );
    return (
      <Box sx={{ p: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 1, mb: 0.5 }}>
          <Chip size='small' label='ROOT' sx={{ bgcolor: 'var(--ds-red-500)', color: '#fff', fontWeight: 700, height: 20, letterSpacing: 0.5 }} />
          <Typography sx={{ fontWeight: 700 }}>{seed.name}</Typography>
          <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 13 }}>
            · {seed.namespace} · {seed.type}
          </Typography>
        </Box>
        <Typography sx={{ fontSize: 12, color: 'var(--ds-gray-500)', mb: 0.25 }}>
          <b style={{ color: 'var(--ds-gray-700)' }}>
            {incidentCount} alert{incidentCount === 1 ? '' : 's'}
          </b>{' '}
          look like one problem
          {(a.chronic || []).length > 0
            ? `. ${a.chronic.length} more ${(a.chronic || []).length === 1 ? 'is' : 'are'} just normal background noise, shown at the bottom`
            : ''}
          .
        </Typography>
        {/* Re-firings of the alert you already have open. The backend keeps them out of
            the core tier so they don't read as separate alerts on the same service. */}
        {a.seed_occurrences > 1 && (
          <Typography sx={{ fontSize: 12, color: 'var(--ds-gray-500)', mb: 0.25 }}>
            This alert fired {a.seed_occurrences} times in the window.
          </Typography>
        )}

        <TierSection
          tierKey='core'
          label={`More alerts on ${seed.name || 'this service'}`}
          unit='alert'
          note='All of these are about the same service you’re looking at — one problem, reported several times.'
          items={a.same_incident}
          seedName={seed.name}
          goTo={goTo}
          showRarity
        />

        {(causeCfg.length > 0 || causeUp.length > 0) && (
          <Box sx={{ mt: 1.75 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, mb: 0.5 }}>
              <Box sx={{ width: 8, height: 8, borderRadius: '50%', bgcolor: TIERS.cause.color, flex: 'none' }} />
              <Typography sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-700)' }}>
                {TIERS.cause.label}
              </Typography>
              <Typography sx={{ ml: 'auto', fontSize: 11, color: 'var(--ds-gray-400)' }}>
                {causeCfg.length + causeUp.length} possible cause{causeCfg.length + causeUp.length === 1 ? '' : 's'}
              </Typography>
            </Box>
            <Typography sx={{ fontSize: 11.5, color: 'var(--ds-gray-400)', mb: 0.75 }}>
              Things that went wrong just before this did. Best guesses — not confirmed.
            </Typography>
            {causeCfg.map((it, i) => (
              <TierRow key={it.event_id || `c${i}`} item={it} color={TIERS.cause.color} goTo={goTo} seedName={seed.name} isChange />
            ))}
            {causeUp.map((it, i) => (
              <TierRow key={it.event_id || `u${i}`} item={it} color={TIERS.cause.color} goTo={goTo} seedName={seed.name} showRarity />
            ))}
          </Box>
        )}

        <TierSection
          tierKey='impact'
          unit='service'
          note='These broke after this one did. How often each normally alerts is shown on the right.'
          items={a.impact}
          seedName={seed.name}
          goTo={goTo}
          showRarity
        />

        {/* Dependents that exist in the service map but raised no alert. Listing them is
            what stops an empty impact tier reading as "nothing was affected" — usually it
            only means nobody wrote an alert rule for the service downstream. */}
        {possible.length > 0 && (
          <Box sx={{ mt: 1.75 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, mb: 0.5 }}>
              <Box sx={{ width: 8, height: 8, borderRadius: '50%', bgcolor: TIERS.impact.color, flex: 'none' }} />
              <Typography sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-700)' }}>
                Possible impact
              </Typography>
              <Typography sx={{ ml: 'auto', fontSize: 11, color: 'var(--ds-gray-400)' }}>
                {possible.length} depend on {rootName}
              </Typography>
            </Box>
            <Typography sx={{ fontSize: 11.5, color: 'var(--ds-gray-400)', mb: 0.75 }}>
              None of these raised an alert — usually that means no alert rule exists for them, not that they were unaffected. Open one to see its
              traffic.
            </Typography>
            <PossibleGroup label={`Calls ${rootName} directly`} items={directDeps} goToService={goToService} />
            <PossibleGroup label='Further downstream' items={indirectDeps} goToService={goToService} />
          </Box>
        )}

        {/* An empty impact list must never read as "nothing was affected" — with no
            service-map coverage we genuinely cannot tell, and saying so is the honest
            answer (a core finding of the correlation audit). Reached only when there is
            no dependent at all; otherwise the section above already answers this. */}
        {(a.impact || []).length === 0 && possible.length === 0 && (
          <Box sx={{ mt: 1.75 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.75, mb: 0.5 }}>
              <Box sx={{ width: 8, height: 8, borderRadius: '50%', bgcolor: TIERS.impact.color, flex: 'none' }} />
              <Typography sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-700)' }}>
                {TIERS.impact.label}
              </Typography>
              <Typography sx={{ ml: 'auto', fontSize: 11, color: 'var(--ds-gray-400)' }}>{noCoverage ? 'unknown' : 'none found'}</Typography>
            </Box>
            <Typography sx={{ fontSize: 12, color: noCoverage ? 'var(--ds-amber-600)' : 'var(--ds-gray-500)' }}>
              {noCoverage
                ? 'We can’t tell. We don’t have a service map for this one, so we don’t know what depends on it — this is not the same as “nothing was affected”.'
                : 'Nothing depends on this one in the service map.'}
            </Typography>
          </Box>
        )}
        <TierSection
          tierKey='chronic'
          unit='alert'
          note='These fire this often regardless of any incident, so they don’t say anything specific about this one. Shown for completeness.'
          items={a.chronic}
          seedName={seed.name}
          goTo={goTo}
          showRarity
          folded
        />

        {!data.resolved && (
          <Typography sx={{ fontSize: 11, color: 'var(--ds-amber-500)', mt: 1.5 }}>
            ⚠ We couldn’t find this service in the service map, so we can’t show what caused it or what it affected — only other alerts on the same
            service.
          </Typography>
        )}
        {a.truncated && (
          <Typography sx={{ fontSize: 11, color: 'var(--ds-gray-400)', mt: 1 }}>
            There were too many alerts around this time to check them all — we looked at the most recent 1000.
          </Typography>
        )}
      </Box>
    );
  }

  if (!data.resolved) {
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
  // Dependents that are not application-level types. They are reported separately by the
  // API so they cannot inflate dependent_count, which feeds recommendation safety scoring
  // — but they must still be shown: on a VM or serverless stack the instance calling this
  // resource IS the application, and hiding it makes a real blast radius read as "nothing
  // was affected".
  const infrastructure = Array.isArray(data.infrastructure_impacted) ? data.infrastructure_impacted : [];
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
        {potential.length > 0 ? ` · ${potential.length} potential` : ''}
        {infrastructure.length > 0 ? ` · ${infrastructure.length} infrastructure` : ''} — grouped into one incident, root cause above.
      </Typography>

      {/* Correlated (alerting) */}
      <Typography sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-600)', mb: 0.75 }}>
        Correlated incidents — {correlated.length} alerting
      </Typography>
      {correlated.length === 0 && (
        <Typography sx={{ fontSize: 13, color: 'var(--ds-gray-500)', mb: 1 }}>
          {impacted.length === 0 && infrastructure.length === 0
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

      {infrastructure.length > 0 && (
        <>
          <Typography
            sx={{ fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 0.5, color: 'var(--ds-gray-600)', mt: 2, mb: 0.75 }}
          >
            Infrastructure impacted — {infrastructure.length}
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75 }}>
            {infrastructure.map((s, i) => (
              <Chip
                key={`i${i}`}
                size='small'
                label={`${s.name} · ${s.node_type} · ${s.hops_away}-hop`}
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
