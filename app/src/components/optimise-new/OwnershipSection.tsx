import { Box, Typography } from '@mui/material';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import PersonOutlineOutlinedIcon from '@mui/icons-material/PersonOutlineOutlined';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import DsTooltip from '@ui/Tooltip';
import { ds } from 'src/utils/colors';
import apiOwnership from '@api1/ownership';
import OwnerBadge from '@components/ownership/OwnerBadge';

const OWNERSHIP_HELP =
  'Who is accountable for this resource. An owner can be set directly on it, matched by an ownership rule, or inherited from the namespace or cloud account above it.';

// A resolved OwnerResult, as returned by ownership_resolve. Snake_case because
// these come straight off the API and are handed to OwnerBadge unchanged.
interface OwnerResult {
  resource_type?: string;
  resource_key?: string;
  found?: boolean;
  owner_type?: string;
  owner_id?: string;
  owner_name?: string;
  source?: string;
  via?: string;
}

// One rung of the ownership chain: the level label, the ref used to resolve it,
// and (after resolution) the owner set *directly* on that level.
interface ChainLevel {
  level: string;
  resourceType: string;
  resourceKey: string;
  own: OwnerResult | null;
}

// OwnershipRow — label + value pair. Deliberately the same 150px label column as
// SafetyRow in DetailsPanel, so this card's rows line up with the Blast Radius
// rows directly beneath it.
const OwnershipRow = ({ label, children }: { label: string; children: ReactNode }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium, flexShrink: 0, minWidth: '150px' }}>
      {label}
    </Typography>
    {children}
  </Box>
);

// buildLevels returns the chain to resolve, most specific first. K8s recommendations
// walk workload → namespace → cloud account; cloud ones have no namespace rung, so
// they walk cloud resource → cloud account. The `resource_id` on a recommendation is
// a cloud_resourses.id, which is also what k8s_workloads.cloud_resource_id points at,
// so the same field keys both domains.
export function buildLevels(resourceId: string, accountId: string, namespace: string): ChainLevel[] {
  const levels: ChainLevel[] = [];
  if (namespace) {
    levels.push({ level: 'Workload', resourceType: 'workload', resourceKey: resourceId, own: null });
    if (accountId) {
      levels.push({ level: 'Namespace', resourceType: 'namespace', resourceKey: `${accountId}/${namespace}`, own: null });
    }
  } else {
    levels.push({ level: 'Resource', resourceType: 'cloud_resource', resourceKey: resourceId, own: null });
  }
  if (accountId) {
    levels.push({ level: 'Cloud account', resourceType: 'cloud_account', resourceKey: accountId, own: null });
  }
  return levels;
}

// derivedText explains, in one sentence, why the effective owner is who it is.
// Wording is kept identical to the drilldown Ownership tab (OwnershipPanel) so the
// two surfaces never appear to disagree about the same resource.
export function derivedText(levels: ChainLevel[], effIndex: number): string {
  if (effIndex < 0) return 'No owner assigned yet.';
  if (effIndex === 0) {
    return levels[0].own?.source === 'rule' ? 'Matched by an ownership rule.' : 'Assigned directly to this resource.';
  }
  return levels[effIndex].level === 'Namespace' ? 'Inherited from the namespace owner.' : 'Inherited from the cloud account (cluster) owner.';
}

// OwnershipSection surfaces the effective owner of the resource a recommendation is
// about, plus the chain it was derived from — so "whose is this?" is answerable
// without leaving the drawer. Read-only: the header link routes to whichever surface
// actually decides the owner (the rules admin for a rule match, the resource listing
// otherwise). Renders nothing when the recommendation carries no resource id, or when
// the resolve call fails — ownership is supplementary and must never break the drawer.
const OwnershipSection = ({ rec }: { rec: any }) => {
  const resourceId = rec?.resource_id || rec?.cloud_resourse?.id || '';
  const accountId = rec?.account_id || '';
  const namespace = rec?.resource_k8s_namespace || '';
  const resourceName = rec?.resource_name || rec?.cloud_resourse?.name || '';

  const [levels, setLevels] = useState<ChainLevel[]>([]);
  const [loading, setLoading] = useState(true);
  // Set when this card has no valid result to show — either the resolve errored, or
  // the request was retired (drawer closed / moved to another recommendation).
  // Either way the card hides rather than sitting on a spinner or wrongly
  // claiming the resource is unowned.
  const [hidden, setHidden] = useState(false);

  // Each fetch claims a token; a response only applies if no newer fetch has started.
  // The drawer is persistent — clicking a different recommendation swaps the prop
  // rather than remounting — so without this a slow response for the previous
  // recommendation can land after the current one's and show the wrong owner.
  const requestIdRef = useRef(0);

  useEffect(() => {
    const requestId = ++requestIdRef.current;
    if (!resourceId) {
      setLoading(false);
      return;
    }
    const descs = buildLevels(resourceId, accountId, namespace);
    // Aborted when the drawer closes or moves to another recommendation, so the
    // request stops on the wire instead of running to completion unwatched.
    const controller = new AbortController();
    setLoading(true);
    setHidden(false);
    apiOwnership
      .resolveOwners(
        descs.map((d) => ({ resource_type: d.resourceType, resource_key: d.resourceKey })),
        controller.signal
      )
      .then((results: OwnerResult[]) => {
        if (requestId !== requestIdRef.current) return;
        // Match responses by (type, key) rather than by position. The resolver
        // returns results aligned to the request, but a short result set would
        // shift every level onto the wrong rung and silently show the wrong
        // owner — a wrong name here is worse than a missing one.
        const byKey = new Map((results || []).map((r) => [`${r.resource_type}\u0000${r.resource_key}`, r]));
        // A level's "own" owner is one resolved directly on it (via self), not one
        // it inherited from a level above — that's what makes the chain readable.
        setLevels(
          descs.map((d) => {
            const r = byKey.get(`${d.resourceType}\u0000${d.resourceKey}`);
            return { ...d, own: r?.found && r.via === 'self' ? r : null };
          })
        );
        setLoading(false);
      })
      .catch(() => {
        if (requestId !== requestIdRef.current) return;
        setHidden(true);
        setLoading(false);
      });
    // Retire this request on the way out: bump the token so an in-flight response
    // can't apply, abort so it stops on the wire, and hide the card. The hide
    // matters for an instance that somehow outlives its cleanup (a stale subtree
    // left behind by the drawer, or a hot reload) — it removes itself rather than
    // sitting on "Resolving…" forever.
    return () => {
      requestIdRef.current++;
      controller.abort();
      setHidden(true);
    };
  }, [resourceId, accountId, namespace]);

  if (!resourceId || hidden) return null;

  const effIndex = levels.findIndex((l) => l.own);
  // The effective owner, with `via` restated relative to the recommendation's own
  // resource so the badge shows the right inherited/rule hint.
  const effective =
    effIndex >= 0
      ? { ...(levels[effIndex].own as OwnerResult), via: effIndex === 0 ? 'self' : levels[effIndex].level === 'Namespace' ? 'namespace' : 'cluster' }
      : null;

  // Route to whatever actually decides the owner: the rules admin when a rule
  // matched, the resource's own listing otherwise. The workload drilldown's
  // Ownership tab isn't URL-addressable, so the k8s link lands on the workloads
  // list filtered to this one workload.
  const matchedByRule = effective?.source === 'rule';
  let manageHref = '/user-management#ownership';
  if (!matchedByRule && accountId) {
    manageHref = namespace
      ? `/kubernetes/details/${accountId}?namespace=${encodeURIComponent(namespace)}&workloadName=${encodeURIComponent(
          resourceName
        )}#kubernetes/applications`
      : `/cloud-account/details/${accountId}#summary`;
  }
  const manageLabel = matchedByRule ? 'Manage rules' : 'Manage ownership';

  return (
    <Card
      elevation='flat'
      size='sm'
      data-testid='recommendation-ownership'
      header={
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2] }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <PersonOutlineOutlinedIcon sx={{ fontSize: '18px', color: ds.gray[500] }} />
            <DsTooltip variant='explainer' title='Ownership' desc={OWNERSHIP_HELP}>
              <Typography
                component='span'
                sx={{
                  fontFamily: 'var(--ds-font-display)',
                  fontSize: ds.text.body,
                  fontWeight: ds.weight.semibold,
                  color: ds.gray[700],
                  cursor: 'help',
                }}
              >
                Ownership
              </Typography>
            </DsTooltip>
          </Box>
          {!loading && (
            <Link href={manageHref} openInNew secondaryText>
              {manageLabel}
            </Link>
          )}
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
        {loading ? (
          <OwnershipRow label='Owner'>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>Resolving…</Typography>
          </OwnershipRow>
        ) : effective ? (
          <>
            <OwnershipRow label='Owner'>
              <OwnerBadge owner={effective} />
            </OwnershipRow>
            <OwnershipRow label='How it was derived'>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{derivedText(levels, effIndex)}</Typography>
            </OwnershipRow>
            {levels.length > 1 && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], mt: ds.space[1] }}>
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium }}>Ownership chain</Typography>
                {levels.map((l, i) => (
                  <OwnershipRow key={l.level} label={l.level}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
                      {l.own ? <OwnerBadge owner={l.own} /> : <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>—</Typography>}
                      {i === effIndex && (
                        <Label size='sm' tone='info'>
                          effective
                        </Label>
                      )}
                    </Box>
                  </OwnershipRow>
                ))}
              </Box>
            )}
          </>
        ) : (
          <Banner
            surface='section'
            tone='info'
            title='No owner set yet'
            message='No ownership rule or assignment covers this resource yet. Add one to route findings like this to the right team.'
          />
        )}
      </Box>
    </Card>
  );
};

export default OwnershipSection;
