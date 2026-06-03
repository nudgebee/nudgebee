import { Box, Typography, Divider, CircularProgress, Table, TableBody, TableCell, TableContainer, TableHead, TableRow } from '@mui/material';
import { useState, useEffect } from 'react';
import { ds } from 'src/utils/colors';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import DragHandleIcon from '@mui/icons-material/DragHandle';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomPRLink from '@components1/common/CustomPRLink';
import MarkDowns from '@components1/common/MarkDowns';
import { Label } from '@components1/ds/Label';
import recommendationApi from '@api1/recommendation';
import { interpolateMitigations } from '@api1/recommendation/data';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { safeParseJSON, formatRuleName } from './utils';

interface DetailsPanelProps {
  fullRecommendation: any;
  accounts?: Record<string, { name: string; cloud_provider: string }>;
}

const DetailsPanel = ({ fullRecommendation: rec, accounts = {} }: DetailsPanelProps) => {
  const [details, setDetails] = useState<any>(null);
  const [loading, setLoading] = useState(true);

  const category = rec?.category || '';
  const ruleName = rec?.rule_name || '';
  const accountName = accounts[rec?.account_id]?.name || '';

  useEffect(() => {
    if (!category || !ruleName) {
      setLoading(false);
      return;
    }
    setLoading(true);
    const result = recommendationApi.getRecommendationDetails(category, ruleName);
    setDetails(result);
    setLoading(false);
  }, [category, ruleName]);

  if (!rec) return null;

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: '40px' }}>
        <CircularProgress size={24} />
      </Box>
    );
  }

  const mitigations = interpolateMitigations(details?.mitigations, rec);
  const recData = safeParseJSON(rec.recommendation);
  const fallback = extractFallbackContent(recData, rec);

  // Resolved values — catalog wins, fallback fills the gaps
  const title = details?.title || fallback.title || formatRuleName(ruleName);
  const serviceName = details?.serviceName || fallback.serviceName || '';
  const description = details?.description || fallback.description || '';
  const remediation = fallback.remediation || '';
  const remediationUrl = fallback.remediationUrl || '';
  const insight = getRecommendationInsight(category, ruleName, recData, rec);

  return (
    <Box sx={{ p: '16px 20px', display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
      {/* Rule Title & Service */}
      <Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: '6px', flexWrap: 'wrap' }}>
          <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>
          {serviceName && (
            <Label size='sm' tone='neutral'>
              {serviceName}
            </Label>
          )}
        </Box>
        {description && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.6 }}>{description}</Typography>}
      </Box>

      {/* Insight — why this matters */}
      {insight && (
        <Box
          sx={{
            display: 'flex',
            gap: '10px',
            p: ds.space[3],
            borderRadius: ds.radius.lg,
            backgroundColor: ds.blue[100],
            border: `1px solid ${ds.blue[200]}`,
          }}
        >
          <InfoOutlinedIcon sx={{ fontSize: '16px', color: ds.blue[700], mt: '1px', flexShrink: 0 }} />
          <Typography sx={{ fontSize: ds.text.small, color: ds.blue[700], lineHeight: 1.6 }}>{insight}</Typography>
        </Box>
      )}

      {/* Recommendation Summary — key data from JSONB */}
      <RecommendationSummary recData={recData} category={category} ruleName={ruleName} />

      {/* Recommendation Steps — from catalog */}
      {details?.recommendations?.length > 0 && (
        <>
          <Divider />
          <Box>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
              Recommendations
            </Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
              {details.recommendations.map((step: string, idx: number) => (
                <Box key={step.substring(0, 60)} sx={{ display: 'flex', gap: ds.space[2], alignItems: 'flex-start' }}>
                  <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.blue[600], minWidth: '18px', mt: '1px' }}>
                    {idx + 1}.
                  </Typography>
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], lineHeight: 1.6 }}>{step}</Typography>
                </Box>
              ))}
            </Box>
          </Box>
        </>
      )}

      {/* Remediation Steps — from catalog mitigations (interpolated) */}
      {mitigations && mitigations.length > 0 && (
        <>
          <Divider />
          <Box>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
              Remediation Steps
            </Typography>
            <Box
              sx={{
                p: '10px',
                borderRadius: ds.radius.lg,
                backgroundColor: ds.gray[100],
                border: `1px solid ${ds.gray[200]}`,
                '& pre': { fontSize: ds.text.caption, whiteSpace: 'pre-wrap', wordBreak: 'break-all' },
                '& code': { fontSize: ds.text.caption },
              }}
            >
              {mitigations.map((step: string) => (
                <MarkDowns
                  key={step.substring(0, 60)}
                  data={step}
                  sx={{ fontSize: ds.text.small, lineHeight: 1.6, color: ds.gray[500] }}
                  allowExecutable={undefined}
                  onLinkClick={undefined}
                />
              ))}
            </Box>
          </Box>
        </>
      )}

      {/* Resource-level remediation — JSONB fallback (only when no catalog mitigations) */}
      {(!mitigations || mitigations.length === 0) && remediation && (
        <>
          <Divider />
          <Box>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Remediation</Typography>
            <Box sx={{ p: '10px', borderRadius: ds.radius.lg, backgroundColor: ds.green[100], border: `1px solid ${ds.green[200]}` }}>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>{remediation}</Typography>
              {remediationUrl && (
                <Box
                  component='a'
                  href={remediationUrl}
                  target='_blank'
                  rel='noopener noreferrer'
                  sx={{
                    fontSize: ds.text.small,
                    color: ds.blue[600],
                    display: 'block',
                    mt: ds.space[2],
                    textDecoration: 'none',
                    '&:hover': { textDecoration: 'underline' },
                  }}
                >
                  View remediation guide →
                </Box>
              )}
            </Box>
          </Box>
        </>
      )}

      {/* Compliance */}
      {details?.compliances?.length > 0 && (
        <>
          <Divider />
          <Box>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Compliance</Typography>
            <Box sx={{ display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
              {details.compliances.map((c: string) => (
                <Label key={c} size='sm' tone='neutral'>
                  {c}
                </Label>
              ))}
            </Box>
          </Box>
        </>
      )}

      {/* References */}
      {details?.references?.length > 0 && (
        <>
          <Divider />
          <Box>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>References</Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
              {details.references.map((url: string) => (
                <Box
                  key={url}
                  component='a'
                  href={url}
                  target='_blank'
                  rel='noopener noreferrer'
                  sx={{
                    fontSize: ds.text.small,
                    color: ds.blue[600],
                    textDecoration: 'none',
                    wordBreak: 'break-all',
                    '&:hover': { textDecoration: 'underline' },
                  }}
                >
                  {url.replace(/https?:\/\//, '').replace(/\/$/, '')}
                </Box>
              ))}
            </Box>
          </Box>
        </>
      )}

      {/* Linked Items */}
      {(rec.ticket || rec.resolution?.type_reference_id) && (
        <>
          <Divider />
          <Box>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
              Linked Items
            </Typography>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
              {rec.ticket && (
                <Box
                  sx={{
                    p: '8px 12px',
                    borderRadius: ds.radius.lg,
                    backgroundColor: ds.gray[100],
                    border: `1px solid ${ds.gray[200]}`,
                    display: 'flex',
                    alignItems: 'center',
                    gap: ds.space[2],
                  }}
                >
                  <ConfirmationNumberOutlinedIcon sx={{ fontSize: '16px', color: ds.blue[600] }} />
                  <CustomTicketLink ticketURL={rec.ticket?.url} ticketID={rec.ticket?.ticket_id} />
                </Box>
              )}
              {rec.resolution?.type_reference_id && (
                <Box
                  sx={{
                    p: '8px 12px',
                    borderRadius: ds.radius.lg,
                    backgroundColor: ds.green[100],
                    border: `1px solid ${ds.green[200]}`,
                    display: 'flex',
                    alignItems: 'center',
                    gap: ds.space[2],
                  }}
                >
                  <CustomPRLink prURL={rec.resolution.type_reference_id} statusMessage={rec.resolution.status_message} />
                </Box>
              )}
            </Box>
          </Box>
        </>
      )}

      {/* Metadata */}
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Metadata</Typography>
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          <MetaRow label='ID' value={rec.id} mono />
          <MetaRow label='Rule' value={ruleName} />
          <MetaRow label='Account' value={accountName || rec.account_id} />
        </Box>
      </Box>
    </Box>
  );
};

// ─── MetaRow helper ───

const MetaRow = ({ label, value, mono }: { label: string; value?: string; mono?: boolean }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: ds.space[1], gap: ds.space[3] }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], whiteSpace: 'nowrap' }}>{label}</Typography>
    <Typography
      sx={{
        fontSize: mono ? ds.text.caption : ds.text.small,
        color: ds.gray[700],
        fontFamily: mono ? ds.font.mono : undefined,
        textAlign: 'right',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      }}
    >
      {value || '—'}
    </Typography>
  </Box>
);

// ─── Recommendation Summary — renders key "what to change" data from JSONB ───

const K8sRightSizingSummary = ({ recData }: { recData: any }) => {
  const containers = extractContainerData(recData);
  if (containers.length === 0) return null;
  return (
    <>
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
          Recommended Resource Changes
        </Typography>
        <TableContainer
          sx={{
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.gray[200]}`,
            '& .MuiTableCell-root': { px: '10px', py: '7px', fontSize: ds.text.small, borderColor: ds.gray[200] },
          }}
        >
          <Table size='small'>
            <TableHead>
              <TableRow sx={{ backgroundColor: ds.blue[100] }}>
                <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: `${ds.text.caption} !important` }}>
                  Container
                </TableCell>
                <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: `${ds.text.caption} !important` }}>
                  CPU Request
                </TableCell>
                <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700], fontSize: `${ds.text.caption} !important` }}>
                  Memory Request
                </TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {containers.map(({ containerName, cpu, memory }) => (
                <TableRow key={containerName} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                  <TableCell>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontWeight: ds.weight.medium, fontStyle: 'italic' }}>
                      {containerName}
                    </Typography>
                  </TableCell>
                  <TableCell>
                    {cpu ? (
                      <ResourceChangeCell current={cpu.allocated?.request} recommended={cpu.recommended?.request} isMem={false} />
                    ) : (
                      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{'—'}</Typography>
                    )}
                  </TableCell>
                  <TableCell>
                    {memory ? (
                      <ResourceChangeCell current={memory.allocated?.request} recommended={memory.recommended?.request} isMem />
                    ) : (
                      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{'—'}</Typography>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </Box>
    </>
  );
};

const CloudRightSizingSummary = ({ recData }: { recData: any }) => {
  const currentInstance = recData.current_instance_type || recData.instance_type || '';
  const recommendedInstance = recData.recommended_instance_type || '';
  const currentPrice = recData.current_price;
  const recommendedPrice = recData.recommended_price;
  if (!currentInstance && !recommendedInstance && currentPrice == null) return null;
  return (
    <>
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
          Recommended Changes
        </Typography>
        {currentInstance && recommendedInstance && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[4],
              p: ds.space[3],
              backgroundColor: ds.blue[100],
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.blue[200]}`,
              mb: ds.space[2],
              justifyContent: 'center',
            }}
          >
            <InstanceBadge label='Current' value={currentInstance} variant='error' />
            <ArrowForwardIcon sx={{ fontSize: '18px', color: ds.blue[700] }} />
            <InstanceBadge label='Recommended' value={recommendedInstance} variant='success' />
          </Box>
        )}
        {currentPrice != null && recommendedPrice != null && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[4],
              p: '10px',
              backgroundColor: ds.green[100],
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.green[200]}`,
              justifyContent: 'center',
            }}
          >
            <Box sx={{ textAlign: 'center' }}>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Current</Typography>
              <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.red[600] }}>
                ${Number(currentPrice).toFixed(2)}/hr
              </Typography>
            </Box>
            <ArrowForwardIcon sx={{ fontSize: '16px', color: ds.green[600] }} />
            <Box sx={{ textAlign: 'center' }}>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Recommended</Typography>
              <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.green[600] }}>
                ${Number(recommendedPrice).toFixed(2)}/hr
              </Typography>
            </Box>
          </Box>
        )}
        {currentInstance && !recommendedInstance && <SummaryRow label='Instance Type' value={currentInstance} />}
      </Box>
    </>
  );
};

const InfraUpgradeSummary = ({ recData }: { recData: any }) => {
  const currentVer = recData.current_version || recData.current_api_version || recData.version || recData.chartVersion || '';
  const recommendedVer = recData.recommended_version || recData.recommended_api_version || recData.replacement_api || recData.latestVersion || '';
  if (!currentVer && !recommendedVer) return null;
  return (
    <>
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Version Change</Typography>
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[3],
            p: ds.space[3],
            backgroundColor: ds.blue[100],
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.blue[200]}`,
            justifyContent: 'center',
          }}
        >
          {currentVer && <InstanceBadge label='Current' value={currentVer} variant='error' />}
          {currentVer && recommendedVer && <ArrowForwardIcon sx={{ fontSize: '18px', color: ds.blue[700] }} />}
          {recommendedVer && <InstanceBadge label='Recommended' value={recommendedVer} variant='success' />}
        </Box>
        {recData.chartName && <SummaryRow label='Chart' value={recData.chartName} />}
        {recData.kind && <SummaryRow label='Kind' value={recData.kind} />}
        {recData.name && <SummaryRow label='Resource' value={recData.name} />}
      </Box>
    </>
  );
};

const SecuritySummary = ({ recData }: { recData: any }) => {
  const vulnId = recData.vulnerability_id || '';
  const severity = recData.severity || recData.Severity?.Label || '';
  const fixVersion = recData.fix_version || '';
  const image = recData.image || '';
  const ruleId = recData.rule_id || '';
  const ruleDescription = recData.rule_description || '';
  if (!vulnId && !severity && !ruleId && !recData.Title) return null;
  return (
    <>
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Finding Summary</Typography>
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          {vulnId && <SummaryRow label='Vulnerability' value={vulnId} />}
          {ruleId && <SummaryRow label='Rule ID' value={ruleId} />}
          {ruleDescription && <SummaryRow label='Rule' value={ruleDescription} />}
          {severity && <SummaryRow label='Severity' value={severity.toUpperCase?.()} />}
          {image && <SummaryRow label='Image' value={image} />}
          {fixVersion && <SummaryRow label='Fix Version' value={fixVersion} highlight />}
          {recData.ServiceName && <SummaryRow label='Service' value={recData.ServiceName} />}
          {recData.Compliance?.Status && <SummaryRow label='Compliance' value={recData.Compliance.Status} />}
        </Box>
      </Box>
    </>
  );
};

const ConfigurationSummary = ({ recData }: { recData: any }) => {
  if (Array.isArray(recData)) {
    const categories = [...new Set(recData.map((i: any) => i.category).filter(Boolean))];
    return (
      <>
        <Divider />
        <Box>
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Issue Summary</Typography>
          <Box
            sx={{
              backgroundColor: ds.gray[100],
              borderRadius: ds.radius.lg,
              p: '10px',
              border: `1px solid ${ds.gray[200]}`,
            }}
          >
            <SummaryRow label='Total Issues' value={String(recData.length)} />
            {categories.length > 0 && <SummaryRow label='Categories' value={categories.join(', ')} />}
          </Box>
        </Box>
      </>
    );
  }
  const recommendedTags: string[] = Array.isArray(recData.recommended_tags) ? recData.recommended_tags : [];
  const hasFields = recData.service_name || recData.alarm_type || recData.instance_type || recData.load_balancer_name || recommendedTags.length > 0;
  if (!hasFields) return null;
  return (
    <>
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
          Configuration Summary
        </Typography>
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          {recData.service_name && <SummaryRow label='Service' value={recData.service_name} />}
          {recData.alarm_type && <SummaryRow label='Alarm Type' value={recData.alarm_type} />}
          {recData.instance_type && <SummaryRow label='Instance Type' value={recData.instance_type} />}
          {recData.load_balancer_name && <SummaryRow label='Load Balancer' value={recData.load_balancer_name} />}
          {recData.region && <SummaryRow label='Region' value={recData.region} />}
          {recommendedTags.length > 0 && (
            <Box sx={{ mt: '6px' }}>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mb: ds.space[1] }}>Recommended Tags</Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1] }}>
                {recommendedTags.map((tag: string) => (
                  <Label key={tag} size='sm' tone='warning'>
                    {tag}
                  </Label>
                ))}
              </Box>
            </Box>
          )}
        </Box>
      </Box>
    </>
  );
};

const RecommendationSummary = ({ recData, category, ruleName }: { recData: any; category: string; ruleName: string }) => {
  if (!recData || typeof recData !== 'object') return null;

  if (category === 'RightSizing' && ruleName === 'pod_right_sizing') return <K8sRightSizingSummary recData={recData} />;
  if (category === 'RightSizing' && !Array.isArray(recData)) return <CloudRightSizingSummary recData={recData} />;
  if (category === 'InfraUpgrade' || category === 'K8sVersionUpgrade') return <InfraUpgradeSummary recData={recData} />;
  if (category === 'Security') return <SecuritySummary recData={recData} />;
  if (category === 'Configuration') return <ConfigurationSummary recData={recData} />;

  // Generic fallback: show key scalar fields from the JSONB
  const scalarEntries = Object.entries(recData)
    .filter(([, v]) => v != null && typeof v !== 'object')
    .filter(([k]) => !['reason', 'message', 'description', 'Description', 'Title'].includes(k))
    .slice(0, 6);
  if (scalarEntries.length === 0) return null;
  return (
    <>
      <Divider />
      <Box>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Summary</Typography>
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          {scalarEntries.map(([key, value]) => (
            <SummaryRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
          ))}
        </Box>
      </Box>
    </>
  );
};

// ─── Summary helpers ───

const SummaryRow = ({ label, value, highlight }: { label: string; value: string; highlight?: boolean }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: '3px' }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{label}</Typography>
    <Typography
      sx={{
        fontSize: ds.text.small,
        color: highlight ? ds.green[600] : ds.gray[700],
        fontWeight: highlight ? ds.weight.semibold : ds.weight.regular,
      }}
    >
      {value}
    </Typography>
  </Box>
);

const InstanceBadge = ({ label, value, variant }: { label: string; value: string; variant: 'success' | 'error' }) => (
  <Box sx={{ textAlign: 'center' }}>
    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: '2px' }}>{label}</Typography>
    <Box
      component='span'
      sx={{
        display: 'inline-block',
        fontFamily: ds.font.mono,
        fontSize: ds.text.small,
        fontWeight: ds.weight.semibold,
        color: variant === 'success' ? ds.green[600] : ds.red[600],
        backgroundColor: variant === 'success' ? ds.green[100] : ds.red[100],
        border: `1px solid ${variant === 'success' ? ds.green[200] : ds.red[200]}`,
        borderRadius: ds.radius.sm,
        px: ds.space[2],
        py: '2px',
      }}
    >
      {value}
    </Box>
  </Box>
);

// ─── Right-sizing container data extraction ───

const extractContainerData = (data: any): { containerName: string; cpu: any; memory: any }[] => {
  if (!data) return [];
  if (data.notifications && Array.isArray(data.notifications)) {
    const cpu = data.notifications.find((n: any) => n.resource === 'cpu');
    const mem = data.notifications.find((n: any) => n.resource === 'memory');
    return [{ containerName: 'default', cpu, memory: mem }];
  }
  const containers: { containerName: string; cpu: any; memory: any }[] = [];
  for (const [key, value] of Object.entries(data)) {
    if (Array.isArray(value) && value.length > 0 && value[0]?.resource) {
      const cpu = value.find((v: any) => v.resource === 'cpu');
      const mem = value.find((v: any) => v.resource === 'memory');
      containers.push({ containerName: key, cpu, memory: mem });
    }
  }
  return containers;
};

// Memory values from the K8s collector are always in bytes
const formatMemValue = (val: number | null | undefined): string => {
  if (val == null) return '—';
  const mi = val / (1024 * 1024);
  if (mi >= 1024) return (mi / 1024).toFixed(1) + ' Gi';
  return Math.round(mi) + ' Mi';
};

const formatCpuValue = (val: number | null | undefined): string => {
  if (val == null) return '—';
  if (val < 1) return Math.round(val * 1000) + 'm';
  return Number(val).toFixed(3);
};

const ResourceChangeCell = ({ current, recommended, isMem }: { current: number | null; recommended: number | null; isMem: boolean }) => {
  const fmt = isMem ? formatMemValue : formatCpuValue;
  const isChanged = current != null && recommended != null && current !== recommended;
  const pct = current != null && recommended != null && Math.abs(current) > 1e-10 ? Math.round(((current - recommended) / current) * 100) : null;

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'nowrap' }}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], whiteSpace: 'nowrap' }}>{fmt(current)}</Typography>
      {isChanged ? (
        <ArrowForwardIcon sx={{ fontSize: '14px', color: ds.green[600], flexShrink: 0 }} />
      ) : (
        <DragHandleIcon sx={{ fontSize: '14px', color: ds.gray[500], flexShrink: 0 }} />
      )}
      <Typography
        sx={{
          fontSize: ds.text.small,
          fontWeight: isChanged ? ds.weight.semibold : ds.weight.regular,
          color: isChanged ? ds.green[600] : ds.gray[700],
          whiteSpace: 'nowrap',
        }}
      >
        {fmt(recommended)}
      </Typography>
      {pct != null && pct !== 0 && (
        <Typography
          sx={{ fontSize: ds.text.caption, color: pct > 0 ? ds.green[600] : ds.red[600], fontWeight: ds.weight.medium, whiteSpace: 'nowrap' }}
        >
          {pct > 0 ? '-' : '+'}
          {Math.abs(pct)}%
        </Typography>
      )}
    </Box>
  );
};

// ─── Fallback content extraction from recommendation JSONB ───

interface FallbackContent {
  title: string;
  description: string;
  serviceName: string;
  remediation: string;
  remediationUrl: string;
}

function extractFallbackContent(recData: any, rec: any): FallbackContent {
  const result: FallbackContent = { title: '', description: '', serviceName: '', remediation: '', remediationUrl: '' };
  if (!recData || typeof recData !== 'object' || Array.isArray(recData)) return result;

  result.title = recData.Title || '';
  result.description = recData.Description || recData.description?.replace(/\[b\]|\[\/b\]/g, '') || recData.reason || recData.message || '';
  if (result.description.length > 800) {
    result.description = result.description.substring(0, 800) + '...';
  }
  result.serviceName = recData.service_name || recData.ServiceName || rec.cloud_resourse?.meta?.config?.serviceName || rec.service_name || '';
  result.remediation = recData.Remediation?.Recommendation?.Text || recData.remediation || '';
  result.remediationUrl = recData.Remediation?.Recommendation?.Url || '';
  return result;
}

// ─── Pod right-sizing insight — analyzes actual resource change direction ───

interface ResourceChangeCounts {
  cpuUp: number;
  cpuDown: number;
  memUp: number;
  memDown: number;
}

const DEFAULT_POD_INSIGHT =
  "Resource requests for this workload don't match observed usage. Adjusting them to reflect actual consumption improves cluster utilization and can reduce costs.";

function countResourceChanges(containers: { containerName: string; cpu: any; memory: any }[]): ResourceChangeCounts {
  let cpuUp = 0,
    cpuDown = 0,
    memUp = 0,
    memDown = 0;
  for (const c of containers) {
    const cpuCur = c.cpu?.allocated?.request;
    const cpuRec = c.cpu?.recommended?.request;
    if (cpuCur != null && cpuRec != null && cpuCur !== cpuRec) {
      if (cpuRec > cpuCur) cpuUp++;
      else cpuDown++;
    }
    const memCur = c.memory?.allocated?.request;
    const memRec = c.memory?.recommended?.request;
    if (memCur != null && memRec != null && memCur !== memRec) {
      if (memRec > memCur) memUp++;
      else memDown++;
    }
  }
  return { cpuUp, cpuDown, memUp, memDown };
}

function describeResourceChanges({ cpuUp, cpuDown, memUp, memDown }: ResourceChangeCounts): string[] {
  const changes: string[] = [];
  if (cpuDown > 0 && cpuUp === 0) changes.push('CPU requests are higher than needed');
  else if (cpuUp > 0 && cpuDown === 0) changes.push('CPU requests are too low for observed usage');
  else if (cpuUp > 0 && cpuDown > 0) changes.push('some containers need more CPU while others need less');

  if (memDown > 0 && memUp === 0) changes.push('memory requests are higher than needed');
  else if (memUp > 0 && memDown === 0) changes.push('memory requests are too low for observed usage');
  else if (memUp > 0 && memDown > 0) changes.push('some containers need more memory while others need less');
  return changes;
}

function buildProvisioningExplanation(changes: string[], counts: ResourceChangeCounts): string {
  const { cpuUp, cpuDown, memUp, memDown } = counts;
  if (cpuUp === 0 && memUp === 0) {
    return `This workload is over-provisioned — ${changes.join(
      ' and '
    )}. Over-provisioned containers block cluster resources that other workloads could use, increasing infrastructure costs without benefit.`;
  }
  if (cpuDown === 0 && memDown === 0) {
    return `This workload is under-provisioned — ${changes.join(
      ' and '
    )}. Under-provisioned containers risk throttling, OOM kills, and degraded performance. Increasing resource requests ensures reliability and stability.`;
  }
  return `This workload has a resource mismatch — ${changes.join(
    '; '
  )}. Aligning resource requests with actual usage ensures both reliability and cost-efficiency.`;
}

function getPodRightSizingInsight(recData: any, savingsText: string): string {
  const containers = extractContainerData(recData);
  if (containers.length === 0) return `${DEFAULT_POD_INSIGHT}${savingsText}`;

  const counts = countResourceChanges(containers);
  const changes = describeResourceChanges(counts);
  if (changes.length === 0) return `${DEFAULT_POD_INSIGHT}${savingsText}`;

  const explanation = buildProvisioningExplanation(changes, counts);
  return `${explanation} Adjusting requests to match observed consumption improves cluster utilization.${savingsText}`;
}

// ─── Contextual insight — explains WHY this recommendation matters ───

const RIGHTSIZING_RULE_INSIGHTS: Record<string, string> = {
  replica_right_sizing:
    'The replica count for this workload can be optimized based on actual traffic and usage patterns. Running more replicas than needed during low-usage periods wastes compute resources.',
  pv_rightsize:
    'This persistent volume has significantly more storage allocated than what is being used. Over-provisioned storage incurs unnecessary costs and the excess capacity cannot be used by other workloads.',
  unused_pvc:
    'This persistent volume claim is not attached to any running workload. Unattached volumes still incur storage charges and should be cleaned up if they are no longer needed.',
  abandoned_resource:
    'This workload appears to be inactive — it has very low or no utilization over an extended period. Abandoned workloads consume cluster resources and incur costs without delivering value.',
};

function getRightSizingInsight(ruleName: string, recData: any, savingsText: string): string {
  if (ruleName === 'pod_right_sizing') return getPodRightSizingInsight(recData, savingsText);

  const staticInsight = RIGHTSIZING_RULE_INSIGHTS[ruleName];
  if (staticInsight) return `${staticInsight}${savingsText}`;

  if (/^aws_native_(ce_ri|ce_savings_plan|purchase_reserved|purchase_savings)/.test(ruleName)) {
    return `You have consistent on-demand usage that could benefit from a commitment-based pricing model. Purchasing Reserved Instances or Savings Plans for predictable workloads typically reduces costs by 30-60% compared to on-demand pricing.${savingsText}`;
  }

  const currentType = recData?.current_instance_type || recData?.instance_type || '';
  const recommendedType = recData?.recommended_instance_type || '';
  if (currentType && recommendedType) {
    return `Based on observed CPU, memory, and network utilization, this instance is larger than what your workload requires. Downsizing from ${currentType} to ${recommendedType} will maintain performance while reducing compute costs.${savingsText}`;
  }
  return `Based on usage analysis, this resource is not fully utilized. Right-sizing to match actual demand reduces costs while maintaining performance.${savingsText}`;
}

function getInfraUpgradeInsight(recData: any): string {
  const currentVer = recData?.current_version || recData?.current_api_version || recData?.version || '';
  const recommendedVer = recData?.recommended_version || recData?.recommended_api_version || recData?.replacement_api || recData?.latestVersion || '';
  if (currentVer && recommendedVer) {
    return `You are running version ${currentVer}, but ${recommendedVer} is available with security patches, bug fixes, and improvements. Running outdated versions may expose your infrastructure to known vulnerabilities and result in loss of vendor support.`;
  }
  return 'A newer version is available with security patches, bug fixes, and feature improvements. Running outdated versions may expose your infrastructure to known vulnerabilities and result in loss of vendor support.';
}

function getSecurityInsight(ruleName: string, recData: any): string {
  if (ruleName === 'image_scan') {
    const severity = recData?.severity || '';
    const sevText = severity ? ` This is a ${severity.toLowerCase()}-severity issue.` : '';
    return `A known vulnerability has been found in a container image running in your environment. Unpatched vulnerabilities can be exploited to gain unauthorized access, escalate privileges, or disrupt services.${sevText} Updating to a patched image version is recommended.`;
  }
  if (recData?.rule_id || recData?.rule_description || ruleName?.includes('cis')) {
    return 'A CIS Benchmark check has identified a security hardening gap. CIS Benchmarks are consensus-based security configurations developed by the Center for Internet Security and are widely adopted as industry-standard guidelines for securing IT systems.';
  }
  if (recData?.Title || recData?.Compliance) {
    return 'AWS Security Hub has identified a security finding based on automated checks against security standards like AWS Foundational Security Best Practices, CIS, or PCI DSS. Remediating this finding improves your compliance posture and reduces attack surface.';
  }
  return 'A security issue has been identified that could expose your infrastructure to risk. Addressing security findings proactively reduces your attack surface and improves your overall security posture.';
}

function getRecommendationInsight(category: string, ruleName: string, recData: any, rec: any): string {
  const savings = rec.estimated_savings || 0;
  const savingsText = savings > 0 ? ` Estimated savings: ~$${savings.toFixed(0)}/month.` : '';

  if (category === 'RightSizing') return getRightSizingInsight(ruleName, recData, savingsText);
  if (category === 'Configuration')
    return 'A configuration best practice violation has been detected. Misconfigurations can lead to reliability issues, security gaps, or unexpected costs. Addressing this aligns your infrastructure with industry best practices and reduces operational risk.';
  if (category === 'InfraUpgrade' || category === 'K8sVersionUpgrade') return getInfraUpgradeInsight(recData);
  if (category === 'Security') return getSecurityInsight(ruleName, recData);
  if (category === 'K8sSpotRecommendation')
    return 'This workload is a good candidate for spot or preemptible instances based on its fault-tolerance characteristics. Spot instances offer the same compute capacity at significantly lower prices (typically 60-90% savings) and are suitable for stateless, restart-tolerant workloads.';
  return '';
}

export default DetailsPanel;
