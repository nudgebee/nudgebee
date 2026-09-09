import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import BugReportOutlinedIcon from '@mui/icons-material/BugReportOutlined';
import PlaceOutlinedIcon from '@mui/icons-material/PlaceOutlined';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import CustomDrawer from '@shared/CustomDrawer';
import Tabs from '@shared/navigation/Tabs';
import Datetime from '@shared/format/Datetime';
import { Label, type LabelTone } from '@ui/Label';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import { Skeleton } from '@ui/Skeleton';
import PRLink, { resolutionsDeepLink } from '@shared/links/PRLink';
import { ds } from '@utils/colors';
import recommendationApi from '@api1/recommendation';
import apiVm from '@api1/vm';
import { useLatestRequest } from '@components/vm/common';
import { type SecurityFinding, findingHeadline, findingLocation, normalizeSeverity } from './securityFinding';
import { EmptyNote, Field, FieldList, Mono, PanelActions, Section, SectionHeading } from './panelPrimitives';

const SEVERITY_TONE: Record<string, LabelTone> = {
  Critical: 'critical',
  High: 'critical',
  Medium: 'warning',
  Low: 'info',
  Info: 'neutral',
  Negligible: 'neutral',
  Unknown: 'neutral',
};

const severityTone = (s: string): LabelTone => SEVERITY_TONE[normalizeSeverity(s)] ?? 'neutral';

/** One row of an occurrence elsewhere in the fleet. */
interface AffectedRow {
  key: string;
  primary: string;
  secondary?: string;
  count?: number;
}

interface SecurityFindingPanelProps {
  open: boolean;
  onClose: () => void;
  finding: SecurityFinding | null;
  /** Display name for the finding's account, when the caller knows it. */
  accountName?: string;
  /** Accounts the "Also affects" lookup may span — the current view's scope. */
  scopeAccountId?: string | string[];
  onCreateTicket?: (finding: SecurityFinding) => void;
  onCreatePR?: (finding: SecurityFinding) => void;
  /** Disables Create Pull Request, with the reason as a tooltip. */
  createPRDisabledReason?: string;
}

/**
 * Detail panel for a single security finding — the CVE on one image or one VM.
 *
 * Replaces the per-row accordion the security tables used to expand into. Group
 * rows (app, image, CVE, VM) keep their accordion: they expand into a list of
 * findings, and each of those opens this panel.
 *
 * Tabs follow the data rather than the cost-recommendation panel's
 * Details/Evidence/History: `vulnerabilities` is deduplicated per CVE, so the
 * description and scoring describe the vulnerability wherever it appears, while
 * the version and location describe only this occurrence. "Also affects" is what
 * that split buys — the same CVE everywhere else in scope.
 */
const SecurityFindingPanel = ({
  open,
  onClose,
  finding,
  accountName,
  scopeAccountId,
  onCreateTicket,
  onCreatePR,
  createPRDisabledReason,
}: SecurityFindingPanelProps) => {
  const [activeTab, setActiveTab] = useState(0);
  const [affected, setAffected] = useState<AffectedRow[] | null>(null);
  const [affectedLoading, setAffectedLoading] = useState(false);
  // Switching findings while a lookup is in flight must not let the older
  // response land on the newer finding — it would sit in `affected` and be
  // reused, since the tab only fetches when that state is still null.
  const beginRequest = useLatestRequest();

  // Reset to the first tab whenever a different finding is opened, so the panel
  // never starts on a tab the reader did not choose for this row.
  useEffect(() => {
    setActiveTab(0);
    setAffected(null);
  }, [finding?.id]);

  const loadAffected = useCallback(async () => {
    if (!finding?.vulnId) {
      setAffected([]);
      return;
    }
    setAffectedLoading(true);
    const isLatest = beginRequest();
    try {
      if (finding.source === 'vm') {
        const res = await apiVm.listVulnerabilityGroups({
          accountId: scopeAccountId || finding.accountId,
          grouping: 'vm',
          vulnId: finding.vulnId,
          limit: 50,
          includeTotal: false,
        });
        const rows = (res?.rows || []).map((row: any) => ({
          key: row.key,
          primary: row.label || row.key,
          secondary: 'VM',
          count: row.count,
        }));
        if (isLatest()) setAffected(rows);
      } else {
        const rows = await recommendationApi.listFindingsByVulnerability({
          accountId: scopeAccountId || finding.accountId,
          vulnerabilityId: finding.vulnId,
        });
        const mapped = (rows || []).map((row: any, i: number) => ({
          key: `${row.account_id}-${row.namespace}-${row.workload_name}-${row.image}-${i}`,
          primary: row.image || row.workload_name || '—',
          secondary: [row.namespace, row.workload_name].filter(Boolean).join(' / '),
          count: row.count,
        }));
        if (isLatest()) setAffected(mapped);
      }
    } catch (error) {
      console.error('Failed to load affected resources:', error);
      if (isLatest()) setAffected([]);
    } finally {
      if (isLatest()) setAffectedLoading(false);
    }
  }, [finding?.vulnId, finding?.source, finding?.accountId, scopeAccountId, beginRequest]);

  // Fetched only when the tab is first opened: it is the one lookup the panel
  // does not already have in hand, and most reads never leave the first tab.
  useEffect(() => {
    if (activeTab === 2 && affected === null && !affectedLoading) {
      loadAffected();
    }
  }, [activeTab, affected, affectedLoading, loadAffected]);

  const fixAvailable = Boolean(finding?.fixedVersion);

  const tabOptions = useMemo(
    () => [
      { value: 0, text: 'Vulnerability', id: 'security-finding-tab-vulnerability', icon: <BugReportOutlinedIcon sx={{ fontSize: 16 }} /> },
      { value: 1, text: 'Occurrence', id: 'security-finding-tab-occurrence', icon: <PlaceOutlinedIcon sx={{ fontSize: 16 }} /> },
      { value: 2, text: 'Also affects', id: 'security-finding-tab-affected', icon: <HubOutlinedIcon sx={{ fontSize: 16 }} /> },
    ],
    []
  );

  if (!finding) return null;

  const location = findingLocation(finding, accountName);

  return (
    <CustomDrawer open={open} onClose={onClose} bare nonModal variant='modern' width='680px' storageKey='nb.securityFindingDrawer.width'>
      <Box data-testid='security-finding-panel' sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Header */}
        <Box sx={{ p: `${ds.space[4]} ${ds.space[5]} 0 ${ds.space[5]}`, display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2], flexWrap: 'wrap' }}>
              <Label size='sm' tone={severityTone(finding.severity)}>
                {finding.severity}
              </Label>
              {finding.kev && (
                <Label size='sm' tone='critical'>
                  Known exploited
                </Label>
              )}
              {finding.status && (
                <Label size='sm' tone={finding.status === 'Open' ? 'info' : 'neutral'}>
                  {finding.status}
                </Label>
              )}
            </Box>
            <Typography
              sx={{
                fontFamily: 'var(--ds-font-mono)',
                fontSize: ds.text.title,
                fontWeight: ds.weight.semibold,
                color: ds.gray[700],
                lineHeight: 1.3,
                wordBreak: 'break-word',
              }}
            >
              {findingHeadline(finding)}
            </Typography>
            {finding.title && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], mt: ds.space[1] }}>{finding.title}</Typography>}
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[1] }}>
              {[finding.packageName, location].filter(Boolean).join(' · ')}
            </Typography>
          </Box>
          <Button
            tone='ghost'
            composition='icon-only'
            size='sm'
            icon={<CloseIcon />}
            aria-label='Close'
            onClick={onClose}
            id='security-finding-close'
          />
        </Box>

        {/* Fix strip — the single fact that decides whether there is anything to do */}
        <Box sx={{ px: ds.space[5], pt: ds.space[3] }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[3],
              flexWrap: 'wrap',
              px: ds.space[3],
              py: ds.space[2],
              borderRadius: 'var(--ds-radius-md)',
              backgroundColor: fixAvailable ? ds.green[100] : ds.amber[100],
              border: `1px solid ${fixAvailable ? ds.green[200] : ds.amber[200]}`,
            }}
          >
            <Typography
              sx={{
                fontSize: ds.text.caption,
                color: ds.gray[600],
                textTransform: 'uppercase',
                letterSpacing: '0.06em',
                fontWeight: ds.weight.medium,
              }}
            >
              Fix
            </Typography>
            {fixAvailable ? (
              <>
                <Mono>
                  <Box component='span' sx={{ color: ds.red[600] }}>
                    {finding.installedVersion || '—'}
                  </Box>
                </Mono>
                <Box component='span' sx={{ color: ds.gray[500] }}>
                  →
                </Box>
                <Mono>
                  <Box component='span' sx={{ color: ds.green[700], fontWeight: ds.weight.medium }}>
                    {finding.fixedVersion}
                  </Box>
                </Mono>
              </>
            ) : (
              <Typography sx={{ fontSize: ds.text.small, color: ds.amber[700] }}>
                {finding.fixState ? `No fix available — ${finding.fixState.replace(/_/g, ' ')}` : 'No fix available yet'}
                {finding.installedVersion ? ` · installed ${finding.installedVersion}` : ''}
              </Typography>
            )}
          </Box>
        </Box>

        {/* Tabs */}
        <Box sx={{ px: ds.space[5], pt: ds.space[3], borderBottom: `1px solid ${ds.gray[200]}` }}>
          <Tabs
            value={activeTab}
            onChange={(next: number) => setActiveTab(next)}
            behavior='filter'
            showSurface={false}
            variant='primary'
            ariaLabel='security finding detail tabs'
            options={{ tabOptions }}
          />
        </Box>

        <Box sx={{ flex: 1, overflow: 'auto', px: ds.space[5], py: ds.space[4] }}>
          {/* ── Vulnerability: true of this CVE wherever it appears ── */}
          {activeTab === 0 && (
            <Box data-testid='security-finding-vulnerability'>
              <Section title='Summary'>
                {finding.description ? (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{finding.description}</Typography>
                ) : (
                  <EmptyNote>The scanner did not supply a description for this vulnerability.</EmptyNote>
                )}
              </Section>

              <Section title='Scoring'>
                <FieldList>
                  <Field label='Severity'>{finding.severity}</Field>
                  {finding.cvssScore !== undefined && (
                    <Field label='CVSS v3'>
                      {finding.cvssScore}
                      {finding.cvssVector ? (
                        <Box sx={{ mt: ds.space[1] }}>
                          <Mono>{finding.cvssVector}</Mono>
                        </Box>
                      ) : null}
                    </Field>
                  )}
                  {finding.epss !== undefined && <Field label='EPSS'>{finding.epss}</Field>}
                  {finding.kev && <Field label='Exploited'>Listed in the known-exploited catalogue</Field>}
                  {finding.cweIds.length > 0 && <Field label='Weakness'>{finding.cweIds.join(', ')}</Field>}
                  {finding.publishedDate && (
                    <Field label='Published'>
                      <Datetime value={finding.publishedDate} />
                    </Field>
                  )}
                  {finding.dataSource && <Field label='Source'>{finding.dataSource}</Field>}
                </FieldList>
              </Section>

              <Section title='References'>
                {finding.primaryUrl || finding.references.length > 0 ? (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                    {finding.primaryUrl && (
                      <Link href={finding.primaryUrl} openInNew>
                        {finding.primaryUrl}
                      </Link>
                    )}
                    {finding.references
                      .filter((ref) => ref !== finding.primaryUrl)
                      .slice(0, 12)
                      .map((ref, index) =>
                        ref.startsWith('http') ? (
                          <Link key={`${ref}-${index}`} href={ref} openInNew>
                            {ref}
                          </Link>
                        ) : (
                          <Typography key={`${ref}-${index}`} sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>
                            <Mono>{ref}</Mono>
                          </Typography>
                        )
                      )}
                  </Box>
                ) : (
                  <EmptyNote>No advisory links were recorded for this vulnerability.</EmptyNote>
                )}
              </Section>
            </Box>
          )}

          {/* ── Occurrence: true of this copy only ── */}
          {activeTab === 1 && (
            <Box data-testid='security-finding-occurrence'>
              <Section title='Location'>
                <FieldList>
                  {accountName && <Field label={finding.source === 'vm' ? 'Account' : 'Cluster'}>{accountName}</Field>}
                  {finding.source === 'vm' ? (
                    <>
                      {finding.resourceName && <Field label='VM'>{finding.resourceName}</Field>}
                      {finding.resourceId && (
                        <Field label='Resource id'>
                          <Mono>{finding.resourceId}</Mono>
                        </Field>
                      )}
                    </>
                  ) : (
                    <>
                      {finding.namespace && <Field label='Namespace'>{finding.namespace}</Field>}
                      {finding.workloadName && <Field label='Workload'>{finding.workloadName}</Field>}
                      {finding.image && (
                        <Field label='Image'>
                          <Mono>{finding.image}</Mono>
                        </Field>
                      )}
                      {finding.layer && (
                        <Field label='Layer'>
                          <Mono>{finding.layer}</Mono>
                        </Field>
                      )}
                    </>
                  )}
                </FieldList>
              </Section>

              <Section title='Package'>
                <FieldList>
                  {finding.packageName && <Field label='Name'>{finding.packageName}</Field>}
                  {finding.packageId && (
                    <Field label='Package id'>
                      <Mono>{finding.packageId}</Mono>
                    </Field>
                  )}
                  {finding.packageType && <Field label='Type'>{finding.packageType}</Field>}
                  <Field label='Installed'>
                    <Mono>{finding.installedVersion || '—'}</Mono>
                  </Field>
                  <Field label='Fixed in'>
                    <Mono>{finding.fixedVersion || finding.fixState || 'No fix'}</Mono>
                  </Field>
                </FieldList>
              </Section>

              <Section title='Activity'>
                <FieldList>
                  {finding.createdAt && (
                    <Field label='First seen'>
                      <Datetime value={finding.createdAt} />
                    </Field>
                  )}
                  {finding.updatedAt && (
                    <Field label='Last seen'>
                      <Datetime value={finding.updatedAt} />
                    </Field>
                  )}
                  {finding.ticket?.ticket_id && (
                    <Field label='Ticket'>
                      <Link href={finding.ticket.url} openInNew>
                        {finding.ticket.ticket_id}
                      </Link>
                    </Field>
                  )}
                </FieldList>
                {finding.resolution && (
                  <Box sx={{ mt: ds.space[2] }}>
                    <PRLink
                      prURL={finding.resolution.type_reference_id}
                      statusMessage={finding.resolution.status_message}
                      status={finding.resolution.status}
                      resolutionHref={resolutionsDeepLink(finding.id)}
                    />
                  </Box>
                )}
              </Section>
            </Box>
          )}

          {/* ── Also affects: the same CVE elsewhere in scope ── */}
          {activeTab === 2 && (
            <Box data-testid='security-finding-affected'>
              {affectedLoading && (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                  <Skeleton shape='rect' height={36} />
                  <Skeleton shape='rect' height={36} />
                  <Skeleton shape='rect' height={36} />
                </Box>
              )}
              {!affectedLoading && affected !== null && affected.length === 0 && (
                <EmptyNote>
                  {finding.vulnId
                    ? 'This is the only open occurrence of this vulnerability in the accounts currently in view.'
                    : 'This finding has no CVE id, so other occurrences cannot be matched.'}
                </EmptyNote>
              )}
              {!affectedLoading && affected !== null && affected.length > 0 && (
                <>
                  <SectionHeading>{affected.length === 1 ? '1 other place in scope' : `${affected.length} other places in scope`}</SectionHeading>
                  <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                    {affected.map((row) => (
                      <Box
                        key={row.key}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          gap: ds.space[3],
                          py: ds.space[2],
                          borderBottom: `1px solid ${ds.gray[200]}`,
                        }}
                      >
                        <Box sx={{ minWidth: 0 }}>
                          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], wordBreak: 'break-all' }}>{row.primary}</Typography>
                          {row.secondary && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}>{row.secondary}</Typography>}
                        </Box>
                        {row.count !== undefined && (
                          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], whiteSpace: 'nowrap' }}>
                            {row.count} {row.count === 1 ? 'finding' : 'findings'}
                          </Typography>
                        )}
                      </Box>
                    ))}
                  </Box>
                </>
              )}
            </Box>
          )}
        </Box>

        {/* Actions — pinned to the bottom, matching the cost panel's action bar */}
        {(onCreatePR || onCreateTicket) && (
          <PanelActions testId='security-finding-actions'>
            {onCreatePR && (
              <Button
                id='security-finding-create-pr'
                tone='primary'
                size='sm'
                disabled={Boolean(createPRDisabledReason) || !fixAvailable}
                tooltip={createPRDisabledReason || (!fixAvailable ? 'No fixed version to upgrade to' : undefined)}
                onClick={() => onCreatePR(finding)}
              >
                Create pull request
              </Button>
            )}
            {onCreateTicket && (
              <Button
                id='security-finding-create-ticket'
                tone='secondary'
                size='sm'
                disabled={Boolean(finding.ticket?.ticket_id)}
                tooltip={finding.ticket?.ticket_id ? `Ticket ${finding.ticket.ticket_id} already exists` : undefined}
                onClick={() => onCreateTicket(finding)}
              >
                Create ticket
              </Button>
            )}
          </PanelActions>
        )}
      </Box>
    </CustomDrawer>
  );
};

export default SecurityFindingPanel;
