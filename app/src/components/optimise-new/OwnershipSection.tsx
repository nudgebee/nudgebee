import { Box, Typography } from '@mui/material';
import { type ReactNode } from 'react';
import PersonOutlineOutlinedIcon from '@mui/icons-material/PersonOutlineOutlined';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import DsTooltip from '@ui/Tooltip';
import { ds } from 'src/utils/colors';
import OwnerBadge from '@components/ownership/OwnerBadge';
import useResourceOwner, { derivedText, type ChainLevel } from '@hooks/useResourceOwner';

const OWNERSHIP_HELP =
  'Who is accountable for this resource. An owner can be set directly on it, matched by an ownership rule, or inherited from the namespace or cloud account above it.';

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

  // Resolution lives in useResourceOwner, shared with the investigation sidebar.
  // buildLevels stays here because only this caller knows a recommendation's
  // namespace field is genuinely a k8s namespace.
  const {
    levels,
    effectiveIndex: effIndex,
    effective,
    loading,
    unresolvable: hidden,
  } = useResourceOwner(resourceId ? buildLevels(resourceId, accountId, namespace) : []);

  if (!resourceId || hidden) return null;

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
