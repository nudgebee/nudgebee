import React, { useEffect, useMemo, useState } from 'react';
import { Box, Typography, Tooltip } from '@mui/material';
import KeyboardDoubleArrowLeftIcon from '@mui/icons-material/KeyboardDoubleArrowLeft';
import KeyboardDoubleArrowRightIcon from '@mui/icons-material/KeyboardDoubleArrowRight';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import SafeIcon from '@shared/icons/SafeIcon';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import NBStatusBadge from '@shared/widgets/NBStatusBadge';
import ScoreDisplay from '@shared/widgets/ScoreDisplay';
import PriorityPinControl from '@shared/widgets/PriorityPinControl';
import InvestigateDropdown from '@components/k8s/investigate/InvestigateDropdown';
import k8sApi from '@api1/kubernetes';
import { hasWriteAccess } from '@lib/auth';
import { exitCodeMapping, snakeToTitleCase } from 'src/utils/common';
import { SUBJECT_TYPE, AGGREGATION_KEY } from '@data/investigateConstants';
import TroubleShootIcon from '@assets/home/node-errors-icon.svg';
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import { BarsBlueOutlineIcon, ErrorFillIcon, FileOutlineIcon, GraphOutlineIcon, infoIcon, LastStateIcon } from '@assets';

function InvestigateSidebar({
  row,
  podDetails,
  othersData,
  matchedOptions,
  recurrenceInfo,
  alertLabels,
  alertRules,
  queryParam,
  isK8s,
  isCloud,
  onPodClick,
  onCreateTicket,
  onResetState,
  collapsed,
  onToggleCollapse,
}) {
  const router = useRouter();
  const [showAll, setShowAll] = useState(false);
  const [incidentMembers, setIncidentMembers] = useState([]);

  // Members of this event's incident group (#34655) — fetched only when the
  // event is a group leader.
  useEffect(() => {
    let cancelled = false;
    setIncidentMembers([]);
    if (row?.id && row?.cloud_account_id && row?.incident_member_count > 0) {
      k8sApi
        .getIncidentGroupMembers(row.id, row.cloud_account_id)
        .then((members) => {
          if (!cancelled) setIncidentMembers(members);
        })
        .catch(() => {});
    }
    return () => {
      cancelled = true;
    };
  }, [row?.id, row?.cloud_account_id, row?.incident_member_count]);

  const detailsPathPrefix = isCloud ? '/cloud-account/details' : '/kubernetes/details';

  const fitCustomLabelStyles = useMemo(
    () => ({
      width: 'auto',
      maxWidth: '100%',
      minWidth: 0,
      justifySelf: 'start',
      whiteSpace: 'normal',
      overflowWrap: 'anywhere',
      padding: 'var(--ds-space-1) var(--ds-space-2)',
      boxSizing: 'border-box',
    }),
    []
  );

  const investigateDescription = useMemo(() => {
    let logText = row?.description;
    if (isK8s && alertLabels?.nb_webhook_url) {
      logText = (logText || '') + `\n\n**Alert Url -** [${alertLabels.alertname || alertLabels.nb_webhook_event_id}](${alertLabels.nb_webhook_url})`;
    }
    if (logText) {
      const logSampleMatch = logText.match(/Log Sample:\s*(\{.*?\})\s*Failure Count:/s);
      const containerIdMatch = logText.match(/Container ID:\s*(\/[^\s]+)/);
      const failureCountMatch = logText.match(/Failure Count:\s*(\d+)/);
      return {
        logSample: logSampleMatch ? logSampleMatch[1] : logText,
        containerId: containerIdMatch ? containerIdMatch[1] : '',
        failureCount: failureCountMatch ? failureCountMatch[1] : '',
      };
    }
    return { logSample: '', containerId: '', failureCount: '' };
  }, [row?.description, isK8s, alertLabels]);

  const mapLabels = (label) => {
    if (!label) {
      return [];
    }
    return Object.entries(label).map(([key, value]) => (
      <Label
        textTransform='none'
        height='auto'
        margin='0'
        wordBreak={'break-all'}
        displayTooltip
        key={key}
        text={`${key}=${value}`}
        variant={'grey'}
        maxWidth={ds.space.mul(1, 65)}
        tooltipCharLimit={40}
      />
    ));
  };

  const incidentMemberIds = useMemo(() => incidentMembers.map((m) => String(m.id)), [incidentMembers]);
  const labels = useMemo(() => mapLabels(alertLabels), [alertLabels]);
  const visibleLabels = useMemo(() => (showAll ? labels : labels.slice(0, 5)), [labels, showAll]);
  const toggleShow = () => setShowAll((prev) => !prev);

  if (collapsed) {
    return (
      <Box
        sx={{
          position: 'sticky !important',
          top: `${ds.space.mul(0, 37)} !important`,
          display: 'flex',
          justifyContent: 'center',
          pt: 'var(--ds-space-2)',
        }}
      >
        <Button
          tone='secondary'
          size='md'
          composition='icon-only'
          aria-label='Expand details panel'
          tooltip='Expand details'
          tooltipPlacement='right'
          icon={<KeyboardDoubleArrowRightIcon style={{ width: 20, height: 20 }} />}
          onClick={onToggleCollapse}
          data-testid='sidebar-expand-toggle'
        />
      </Box>
    );
  }

  return (
    <Box sx={{ position: 'sticky !important', top: `${ds.space.mul(0, 37)} !important` }}>
      <Box
        sx={{
          backgroundColor: ds.background[100],
          border: `0.5px solid ${ds.gray[300]}`,
          borderRadius: 'var(--ds-radius-lg)',
          padding: '0 var(--ds-space-4) var(--ds-space-4) var(--ds-space-4)',
          maxHeight: `calc(100vh - ${ds.space.mul(0, 55)})`,
          overflowX: 'auto',
          '::-webkit-scrollbar': { width: ds.space[1] },
        }}
      >
        <Box
          sx={{
            position: 'sticky',
            top: 0,
            zIndex: 2,
            background: ds.background[100],
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            borderBottom: `1px solid ${ds.gray[200]}`,
            paddingTop: 'var(--ds-space-4)',
            paddingBottom: 'var(--ds-space-3)',
          }}
        >
          <SafeIcon alt='kube-icon' src={TroubleShootIcon} />
          <Text
            value={row?.title || ''}
            showAutoEllipsis
            placement='right'
            sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-title)', fontFamily: 'Roboto' }}
          />
          <Box sx={{ ml: 'auto', flexShrink: 0 }}>
            <Button
              tone='ghost'
              size='sm'
              composition='icon-only'
              aria-label='Collapse panel'
              tooltip='Collapse panel'
              tooltipPlacement='right'
              icon={<KeyboardDoubleArrowLeftIcon />}
              onClick={onToggleCollapse}
              data-testid='sidebar-collapse-toggle'
            />
          </Box>
        </Box>

        <>
          <Box sx={{ mt: 'var(--ds-space-3)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
            <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
              <Text value={'Where'} secondaryText />
              <Box
                role='button'
                tabIndex={0}
                onClick={onPodClick}
                onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && onPodClick(e)}
                sx={{ cursor: 'pointer' }}
                data-testid='pod-name-link'
              >
                <Text
                  value={row?.subject_name ? row?.subject_name : '-'}
                  secondaryText
                  showAutoEllipsis
                  sx={{
                    color:
                      (row?.subject_type === SUBJECT_TYPE.POD && row?.cloud_resource_id) || row?.aggregation_key === AGGREGATION_KEY.ANOMALY
                        ? ds.blue[600]
                        : ds.gray[700],
                    lineHeight: '1.4',
                  }}
                />
              </Box>
            </Box>
            <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
              <Text value={'When'} secondaryText />
              <Text
                value={
                  row?.starts_at ? (
                    <Datetime
                      value={row?.starts_at}
                      sx={{ fontSize: 'var(--ds-text-small)' }}
                      sxSuffix={{ color: ds.gray[700], fontSize: 'var(--ds-text-small)' }}
                    />
                  ) : (
                    ''
                  )
                }
              />
            </Box>
            <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
              <Text value={'Severity'} secondaryText />
              <Box>
                <Label text={row?.priority || '-'} margin='0' width={ds.space.mul(0, 21)} />
              </Box>
            </Box>

            {row?.computed_score !== null && row?.computed_score !== undefined && (
              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
                <Text value={'Triage Score'} secondaryText />
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                  <ScoreDisplay
                    score={row?.computed_score}
                    priority={row?.computed_priority}
                    scoreFactors={row?.score_factors}
                    confidence={row?.score_confidence}
                  >
                    <PriorityPinControl
                      eventId={row?.id}
                      accountId={row?.cloud_account_id || router.query.accountId}
                      currentPriority={row?.computed_priority}
                      currentScore={row?.computed_score}
                      currentScoreFactors={row?.score_factors}
                      canWrite={hasWriteAccess(router.query.accountId)}
                      onChanged={(newPriority, newScore, newFactors) => {
                        onRowChange?.((prev) => ({
                          ...prev,
                          computed_priority: newPriority,
                          computed_score: newScore,
                          score_factors: newFactors,
                        }));
                      }}
                      align='end'
                    />
                  </ScoreDisplay>
                </Box>
              </Box>
            )}

            <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
              <Text value={'Triage Status'} secondaryText />
              <Box>
                <NBStatusBadge
                  eventId={row?.id}
                  currentStatus={row?.nb_status || 'OPEN'}
                  onStatusChange={() => onPriorityChanged(row?.id)}
                  onCreateTicket={() => onCreateTicket(row)}
                />
              </Box>
            </Box>

            {row?.labels?.nb_duplicate_of && (
              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
                <Text value={'Duplicate of'} secondaryText />
                <Box sx={{ minHeight: ds.space.mul(0, 9), fontSize: 'var(--ds-text-body)' }}>
                  <Link
                    style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                    href={`/investigate?id=${row.labels.nb_duplicate_of}&accountId=${row?.cloud_account_id}`}
                    openInNew={true}
                  >
                    <Label text={'View Original Event'} margin='0' />
                  </Link>
                </Box>
              </Box>
            )}

            {row?.incident_leader_id && (
              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
                <Text value={'Same incident'} secondaryText />
                <Box sx={{ minHeight: ds.space.mul(0, 9), fontSize: 'var(--ds-text-body)' }}>
                  <Link
                    style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                    href={`/investigate?id=${row.incident_leader_id}&accountId=${row?.cloud_account_id}`}
                    openInNew={true}
                  >
                    <Label text={'View Leader Event'} margin='0' />
                  </Link>
                </Box>
              </Box>
            )}

            {recurrenceInfo?.isRecurrence && (
              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
                <Text value={'Recurrence'} secondaryText />
                <Box sx={{ minHeight: ds.space.mul(0, 9), fontSize: 'var(--ds-text-body)' }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.amber[500] }}>⚠️ Previously resolved</Typography>
                    <Link
                      style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                      href={`/investigate?id=${recurrenceInfo.previousEventId}&accountId=${row?.cloud_account_id}`}
                      openInNew={true}
                    >
                      <Label text={'View Previous'} margin='0' variant='yellow' />
                    </Link>
                  </Box>
                </Box>
              </Box>
            )}

            {row?.labels?.nb_triage_rule_id && (
              <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
                <Text value={'Triage Rule'} secondaryText />
                <Box sx={{ minHeight: ds.space.mul(0, 9), fontSize: 'var(--ds-text-body)' }}>
                  <Link
                    style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                    href={`${detailsPathPrefix}/${row?.cloud_account_id}#events/triage-rules`}
                    openInNew={true}
                  >
                    <Label text={'View Triage Rule'} margin='0' />
                  </Link>
                </Box>
              </Box>
            )}

            {/* Rule */}
            <Box sx={{ display: 'grid', flexDirection: 'column', alignItems: 'flex-start', gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr` }}>
              <Text value={'Rule'} secondaryText />
              <Box sx={{ minHeight: ds.space.mul(0, 9), fontSize: 'var(--ds-text-body)' }}>
                {row.aggregation_key && alertRules?.includes(row.aggregation_key) ? (
                  <Link
                    style={{ textDecoration: 'none', display: 'inline-flex', margin: '0' }}
                    href={`${detailsPathPrefix}/${row?.cloud_account_id}?name=${row?.aggregation_key}#monitoring/alert-manager`}
                    openInNew={true}
                  >
                    <Label
                      text={(row?.aggregation_key?.includes('_') ? snakeToTitleCase(row?.aggregation_key) : row?.aggregation_key) || '-'}
                      margin='0'
                      height='auto'
                      wordBreak='break-word'
                      customLabelStyle={fitCustomLabelStyles}
                      displayTooltip={isCloud}
                      tooltipCharLimit={isCloud ? 25 : undefined}
                    />
                  </Link>
                ) : (
                  <Label
                    text={(row?.aggregation_key?.includes('_') ? snakeToTitleCase(row?.aggregation_key) : row?.aggregation_key) || '-'}
                    margin='0'
                    height='auto'
                    wordBreak='break-word'
                    customLabelStyle={fitCustomLabelStyles}
                    displayTooltip={isCloud}
                    tooltipCharLimit={isCloud ? 25 : undefined}
                  />
                )}
              </Box>
            </Box>
          </Box>

          {/* K8s-only: Crash Details, Last State, Impact sections */}
          {isK8s && (
            <>
              {podDetails?.data?.containers?.some(
                (pd) => pd?.status?.reason || (pd?.status?.exitCode !== undefined && pd?.status?.exitCode !== null) || pd?.name || pd?.status?.state
              ) && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    background: ds.background[100],
                    gap: 'var(--ds-space-1)',
                    mb: 'var(--ds-space-4)',
                    mt: 'var(--ds-space-5)',
                    '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: ds.gray[200] },
                  }}
                >
                  <SafeIcon src={ErrorFillIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                  <Typography
                    sx={{
                      color: ds.gray[700],
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      lineHeight: 'normal',
                      minWidth: ds.space.mul(0, 51),
                    }}
                  >
                    Crash Details
                  </Typography>
                </Box>
              )}

              {podDetails?.data?.containers?.length > 0 && (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                  {podDetails?.data?.containers?.map((pd, index) => (
                    <React.Fragment key={pd?.name || index}>
                      <Box
                        sx={{
                          display: 'grid',
                          flexDirection: 'column',
                          alignItems: 'flex-start',
                          gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr`,
                          gap: 'var(--ds-space-1)',
                        }}
                      >
                        {pd?.name && (
                          <>
                            {pd?.status?.reason && (
                              <>
                                <Text value={'Reason'} secondaryText />
                                <Label
                                  variant={'red'}
                                  customLabelStyle={{ padding: 'var(--ds-space-1) var(--ds-space-1)' }}
                                  text={pd.status.reason}
                                />
                              </>
                            )}
                            <>
                              <Text value={'Container'} secondaryText />
                              <Text
                                value={pd.name}
                                copyableTooltip
                                showAutoEllipsis
                                sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700], lineHeight: '1.4' }}
                              />
                            </>
                            {pd?.status?.exitCode !== undefined && pd?.status?.exitCode !== null && (
                              <>
                                <Text value={'Exit code'} secondaryText />
                                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                                  <Text value={pd.status.exitCode} secondaryText sx={{ color: ds.gray[700] }} />
                                  <Tooltip title={exitCodeMapping[pd.status.exitCode] || 'Unknown'} arrow>
                                    <SafeIcon
                                      src={infoIcon}
                                      alt='info'
                                      style={{ width: '13px', height: '13px', cursor: 'pointer', filter: 'opacity(0.5)' }}
                                    />
                                  </Tooltip>
                                </Box>
                              </>
                            )}
                            {pd?.status?.state && (
                              <>
                                <Text value={'Status'} secondaryText />
                                <Text value={pd.status.state} secondaryText sx={{ color: ds.gray[700] }} />
                              </>
                            )}
                          </>
                        )}
                      </Box>
                    </React.Fragment>
                  ))}
                </Box>
              )}

              {podDetails?.data?.containers?.some(
                (pd) => pd?.lastStatus && (pd.lastStatus.state || pd.lastStatus.exitCode !== undefined || pd.lastStatus.reason)
              ) && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    background: ds.background[100],
                    gap: 'var(--ds-space-1)',
                    mb: 'var(--ds-space-4)',
                    mt: 'var(--ds-space-5)',
                    '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: ds.gray[200] },
                  }}
                >
                  <SafeIcon src={LastStateIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                  <Typography
                    sx={{
                      color: ds.gray[700],
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      lineHeight: 'normal',
                      minWidth: ds.space.mul(0, 35),
                    }}
                  >
                    Last State
                  </Typography>
                </Box>
              )}

              {podDetails?.data?.containers?.map((pd, index) => (
                <Box key={pd?.name || index}>
                  <Box className='container-box' sx={{ mb: ds.space[2], fontSize: 'var(--ds-text-body)' }}>
                    <Box>
                      {pd?.name && pd?.lastStatus && Object.values(pd.lastStatus).some((v) => v !== null && v !== undefined) && (
                        <Box
                          sx={{
                            display: 'grid',
                            flexDirection: 'column',
                            alignItems: 'flex-start',
                            gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr`,
                            gap: 'var(--ds-space-1)',
                          }}
                        >
                          {pd.lastStatus.state && (
                            <>
                              <Text value={'State'} secondaryText />
                              <Text value={pd.lastStatus.state} secondaryText sx={{ color: ds.gray[700] }} />
                            </>
                          )}
                          {pd.lastStatus.exitCode !== undefined && (
                            <>
                              <Text value={'Exit Code'} secondaryText />
                              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                                <Text value={pd.lastStatus.exitCode} secondaryText sx={{ color: ds.gray[700] }} />
                                <Tooltip title={exitCodeMapping[pd.lastStatus.exitCode] || 'Unknown'} arrow>
                                  <SafeIcon
                                    src={infoIcon}
                                    alt='info'
                                    style={{ width: '12px', height: '12px', cursor: 'pointer', filter: 'opacity(0.5)' }}
                                  />
                                </Tooltip>
                              </Box>
                            </>
                          )}
                          {pd.lastStatus.reason && (
                            <>
                              <Text value={'Reason'} secondaryText />
                              <Text value={pd.lastStatus.reason} secondaryText sx={{ color: ds.gray[700] }} />
                            </>
                          )}
                        </Box>
                      )}
                    </Box>
                  </Box>
                </Box>
              ))}

              {podDetails?.data?.containers?.some(
                (pd) => pd?.status?.state && ((podDetails?.data?.restarts != null && podDetails?.data?.restarts > 0) || othersData?.length > 0)
              ) && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    background: ds.background[100],
                    gap: 'var(--ds-space-1)',
                    mb: 'var(--ds-space-4)',
                    mt: 'var(--ds-space-5)',
                    '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: ds.gray[200] },
                  }}
                >
                  <SafeIcon src={GraphOutlineIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                  <Typography
                    sx={{
                      color: ds.gray[700],
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      lineHeight: 'normal',
                      minWidth: ds.space.mul(1, 15),
                    }}
                  >
                    Impact
                  </Typography>
                </Box>
              )}

              <Box
                sx={{
                  display: 'grid',
                  flexDirection: 'column',
                  alignItems: 'flex-start',
                  gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr`,
                  gap: 'var(--ds-space-1)',
                }}
              >
                {podDetails?.data?.restarts != null && podDetails?.data?.restarts > 0 && (
                  <>
                    <Text value={'Restarts'} secondaryText />
                    <Text
                      value={podDetails?.data?.restarts}
                      secondaryText
                      sx={{ color: podDetails?.data?.restarts > 1 ? ds.red[500] : ds.gray[700] }}
                    />
                  </>
                )}
                {othersData?.length > 0 && (
                  <>
                    <Text value={'Frequency'} secondaryText />
                    <Box>
                      {othersData?.map((item) => (
                        <Text key={item} value={item} secondaryText sx={{ color: ds.gray[700] }} />
                      ))}
                    </Box>
                  </>
                )}
              </Box>
            </>
          )}

          {/* Context section */}
          {(row?.cluster || row?.subject_namespace || row?.subject_node || matchedOptions.filter((o) => o.infoData?.length > 0).length > 0) && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                background: ds.background[100],
                gap: 'var(--ds-space-1)',
                mb: 'var(--ds-space-4)',
                mt: 'var(--ds-space-5)',
                '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: ds.gray[200] },
              }}
            >
              <SafeIcon src={FileOutlineIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
              <Typography
                sx={{
                  color: ds.gray[700],
                  fontSize: 'var(--ds-text-body-lg)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  lineHeight: 'normal',
                  minWidth: ds.space.mul(0, 27),
                }}
              >
                Context
              </Typography>
            </Box>
          )}

          <Box
            sx={{
              display: 'grid',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr`,
              gap: 'var(--ds-space-1)',
            }}
          >
            {isK8s && podDetails?.data?.qosClass && (
              <>
                <Text value={'Cluster'} secondaryText />
                <Text value={row?.cluster || '-'} secondaryText sx={{ color: ds.gray[700] }} />
              </>
            )}
            {row?.subject_namespace && (
              <>
                <Text value={'Namespace'} secondaryText />
                <Text value={row?.subject_namespace || '-'} secondaryText sx={{ color: ds.gray[700] }} />
              </>
            )}
            {row?.subject_node && (
              <>
                <Text value={'Node'} secondaryText />
                <Text
                  value={row?.subject_node || '-'}
                  copyableTooltip
                  showAutoEllipsis
                  sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700], lineHeight: '1.4' }}
                />
              </>
            )}
          </Box>

          {/* Others section */}
          {(row?.subject_name || row?.subject_type || podDetails?.data?.qosClass || podDetails?.data?.containers?.[0]?.imageName) && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                background: ds.background[100],
                gap: 'var(--ds-space-1)',
                mb: 'var(--ds-space-4)',
                mt: 'var(--ds-space-5)',
                '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: ds.gray[200] },
              }}
            >
              <SafeIcon src={BarsBlueOutlineIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
              <Typography
                sx={{
                  color: ds.gray[700],
                  fontSize: 'var(--ds-text-body-lg)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  lineHeight: 'normal',
                  minWidth: ds.space.mul(0, 25),
                }}
              >
                Others
              </Typography>
            </Box>
          )}
          <Box
            sx={{
              display: 'grid',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gridTemplateColumns: `${ds.space.mul(1, 25)} 1fr`,
              gap: 'var(--ds-space-1)',
            }}
          >
            {isK8s && podDetails?.data?.containers?.[0]?.imageName && (
              <>
                <Text value={'Image Name'} secondaryText />
                <Text
                  value={podDetails?.data?.containers[0].imageName || '-'}
                  showAutoEllipsis
                  sx={{ fontSize: 'var(--ds-text-small)', wordBreak: 'break-word', whiteSpace: 'normal', width: '100%', lineHeight: '1.4' }}
                />
              </>
            )}
            {row?.subject_node && (
              <>
                <Text value={'Subject Type'} secondaryText />
                <Text value={row?.subject_type || '-'} secondaryText sx={{ color: ds.gray[700] }} />
              </>
            )}
            {row?.subject_name && (
              <>
                <Text value={'Subject'} secondaryText />
                <Text
                  value={row?.subject_name || '-'}
                  copyableTooltip
                  showAutoEllipsis
                  sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700], lineHeight: '1.4' }}
                />
              </>
            )}
            {podDetails?.data?.qosClass && (
              <>
                <Text value={'QOS Class'} secondaryText />
                <Text value={podDetails?.data?.qosClass} secondaryText sx={{ color: ds.gray[700] }} />
              </>
            )}
            {row?.description && (
              <>
                {investigateDescription.containerId && (
                  <>
                    <Text value={'Container Id'} secondaryText />
                    <Text
                      value={investigateDescription.containerId || '-'}
                      copyableTooltip
                      showAutoEllipsis
                      sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[700] }}
                    />
                  </>
                )}
                {investigateDescription.failureCount && (
                  <>
                    <Text value={'Failure Count'} secondaryText />
                    <Text className='text-value' value={investigateDescription.failureCount} secondaryText sx={{ color: ds.gray[700] }} />
                  </>
                )}
                {isCloud && (
                  <>
                    <Text value={'Description'} secondaryText />
                    <Text value={investigateDescription.logSample} showAutoEllipsis sx={{ fontSize: 'var(--ds-text-small)' }} />
                  </>
                )}
              </>
            )}
          </Box>

          {/* Alert labels */}
          {Object.keys(alertLabels).length > 0 && (
            <>
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  background: ds.background[100],
                  gap: 'var(--ds-space-1)',
                  mb: 'var(--ds-space-4)',
                  mt: 'var(--ds-space-5)',
                  '&::after': { content: '""', height: '0.5px', width: '100%', backgroundColor: ds.gray[200] },
                  '& img': {
                    filter: 'brightness(0) saturate(100%) invert(44%) sepia(99%) saturate(2351%) hue-rotate(201deg) brightness(98%) contrast(97%)',
                  },
                }}
              >
                <SafeIcon src={CubeIcon} alt='issue.svg' style={{ width: '16px', height: '16px' }} />
                <Typography
                  sx={{
                    color: ds.gray[700],
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    lineHeight: 'normal',
                    minWidth: ds.space.mul(0, 43),
                  }}
                >
                  Alert labels
                </Typography>
              </Box>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-1)' }}>{visibleLabels}</Box>
              {labels.length > 5 && (
                <Box mt={ds.space[2]}>
                  <Button tone='link' size='xs' onClick={toggleShow} data-testid='toggle-labels-btn'>
                    Show {showAll ? 'Less' : `More (${labels.length - 5})`}
                  </Button>
                </Box>
              )}
            </>
          )}
          {row?.incident_member_count > 0 && incidentMembers.length > 0 && (
            <InvestigateDropdown
              query={queryParam}
              inputMaxWidth='100%'
              subjectName={row?.subject_name}
              subjectNamespace={row?.subject_namespace}
              resetStateWhenItemSelected={onResetState}
              title={`Same Incident (${row.incident_member_count})`}
              placeholder='Grouped alerts on this subject'
              optionsOverride={incidentMembers.map((m) => ({
                value: String(m.id),
                label: m.title || m.aggregation_key,
                subtext: m.starts_at ? new Date(m.starts_at).toLocaleString() : undefined,
              }))}
            />
          )}
          <InvestigateDropdown
            query={queryParam}
            inputMaxWidth='100%'
            subjectName={row?.subject_name}
            subjectNamespace={row?.subject_namespace}
            resetStateWhenItemSelected={onResetState}
            excludeIds={incidentMemberIds}
          />
        </>
      </Box>
    </Box>
  );
}

InvestigateSidebar.propTypes = {
  row: PropTypes.object,
  podDetails: PropTypes.object,
  othersData: PropTypes.array,
  matchedOptions: PropTypes.array,
  recurrenceInfo: PropTypes.object,
  alertLabels: PropTypes.object,
  alertRules: PropTypes.array,
  queryParam: PropTypes.object,
  isK8s: PropTypes.bool,
  isCloud: PropTypes.bool,
  onPodClick: PropTypes.func,
  onCreateTicket: PropTypes.func,
  onResetState: PropTypes.func,
  collapsed: PropTypes.bool,
  onToggleCollapse: PropTypes.func,
};

export default InvestigateSidebar;
