import { Box, Stack } from '@mui/material';
import { useEffect, useState, useCallback } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Button as DsButton } from '@components1/ds/Button';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable from '@common-new/tables/CustomTable2';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import Datetime from '@common-new/format/Datetime';
import PropTypes from 'prop-types';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { action } from 'src/utils/actionStyles';
import Text from '@common-new/format/Text';
import apiUser from '@api1/user';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import CustomTicketLink from '@common-new/CustomTicketLink';
import { toast as snackbar } from '@components1/ds/Toast';
import { hasWriteAccess } from '@lib/auth';
import { Label } from '@components1/ds/Label';
import { Divider } from '@components1/ds/Divider';

const SEVERITY_TO_DS_LEVEL = {
  critical: 'critical',
  high: 'high',
  medium: 'medium',
  low: 'low',
  info: 'info',
};
const toDsSeverityLevel = (s) => SEVERITY_TO_DS_LEVEL[String(s || '').toLowerCase()] || 'info';

const RECOMMENDATION_HEADER = [
  { name: 'API', width: '20%' },
  { name: 'Deprecated', width: '8%' },
  { name: 'Deleted', width: '8%' },
  { name: 'Replacement', width: '18%' },
  { name: 'Impacted Objects', width: '25%' },
  { name: 'Severity', width: '12%' },
  { name: 'Updated At', width: '14%' },
  { name: 'Actions', width: '5%' },
];

const KubernetesClusterUpgradeRecommendation = (props) => {
  const [kubernetesClusterUpgradeRecommendation, setKubernetesClusterUpgradeRecommendation] = useState([]);
  const [kubernetesClusterUpgradeRecommendationCount, setKubernetesClusterUpgradeRecommendationCount] = useState(0);
  const [totalKubernetesClusterUpgradeRecommendationCount, setTotalKubernetesClusterUpgradeRecommendationCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const kubernetesClusterUpgradeTable = 'kubernetesClusterUpgradeTable';

  const changePage = (p, limit) => {
    setPage(p - 1);
    setRowsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    const parts = [];
    const push = (label, value) => {
      if (value === undefined || value === null) {
        return;
      }
      if (typeof value === 'string' && value.trim() === '') {
        return;
      }
      if (Array.isArray(value) && value.length === 0) {
        return;
      }
      parts.push(`**${label}**: ${value}`);
    };

    const rec = data?.recommendation || {};
    const replacement = rec?.replacement || {};

    const impactedDeleted =
      rec?.deleted_items?.filter((i) => i.objectname).map((i) => (i.namespace ? `${i.namespace}/${i.objectname}` : i.objectname)) || [];
    const impactedDeprecated =
      rec?.deprecated_items?.filter((i) => i.objectname).map((i) => (i.namespace ? `${i.namespace}/${i.objectname}` : i.objectname)) || [];
    const impactedAll = [...impactedDeleted, ...impactedDeprecated];

    push('Deprecated Api', rec.kind);
    push('Deprecated Api Version', rec.version);
    push('Deprecated Api Group', rec.group);
    push('Deprecated Api Description', rec.controller_type || (rec.kind ? 'Job' : undefined));
    if (impactedAll.length > 0) {
      push('Impacted Objects', impactedAll.join(', '));
    }
    push('Fixed Api Kind', replacement.kind);
    push('Fixed Api Version', replacement.version);
    push('Fixed Api Group', replacement.group);

    return parts.join('\n');
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const getSeverityOrder = (severity) => {
    const severityMap = {
      High: 1,
      Medium: 2,
      Low: 3,
      Info: 4,
    };
    return severityMap[severity] || 4; // Default to Info
  };

  const listClusterUpgradeRecommendations = useCallback(() => {
    if (!props?.kubernetes?.id) {
      return;
    }
    setLoading(true);
    setKubernetesClusterUpgradeRecommendation([]);

    const parseVersion = (v) => {
      if (!v) {
        return null;
      }
      const num = parseFloat(v.replace(/^v/, ''));
      return isNaN(num) ? null : num;
    };
    const targetVersionNum = parseVersion(props?.kubernetes?.version);

    // Helper for effective severity (prefer structural impact over provided severity)
    const effectiveSeverity = (rec) => {
      if (rec.recommendation?.deleted_items?.length > 0) {
        return 'High';
      }
      if (rec.recommendation?.deprecated_items?.length > 0) {
        return 'Medium';
      }
      return rec.severity || 'Info';
    };

    recommendationApi
      .getK8sRecommendation({
        accountId: props?.kubernetes?.id,
        category: 'InfraUpgrade',
        ruleName: 'k8s_api_deprecated',
        status: recommendationStatus ? [recommendationStatus] : [],
        recommendation: null,
        limit: 1000,
        offset: 0,
        fetchTicket: true,
      })
      .then((res) => {
        const all = res?.data?.recommendation || [];
        const relevant = all.filter((item) => {
          if (targetVersionNum == null) {
            return true;
          } // fallback: show all if target missing
          const depV = parseVersion(item.recommendation?.deprecated_version);
          const delV = parseVersion(item.recommendation?.deleted_version);
          const sevRaw = (item.severity || '').toLowerCase();
          const effSev = effectiveSeverity(item);
          const isHigh = effSev === 'High' || sevRaw === 'critical' || sevRaw === 'highest';
          if (delV && delV === targetVersionNum) {
            return true;
          }
          if (depV && depV === targetVersionNum) {
            return true;
          }
          if (isHigh && ((delV && delV < targetVersionNum) || (depV && depV < targetVersionNum))) {
            return true;
          }
          return false;
        });

        const sortedRecommendations = relevant.sort((a, b) => getSeverityOrder(effectiveSeverity(a)) - getSeverityOrder(effectiveSeverity(b)));

        const start = page * rowsPerPage;
        const pageSlice = sortedRecommendations.slice(start, start + rowsPerPage);

        const k8sRecommendationData = pageSlice.map((item) => {
          const data = [];
          const hasImpactedObjects = item.recommendation?.deleted_items?.length > 0 || item.recommendation?.deprecated_items?.length > 0;

          const MENU_ITEMS = [];
          if (hasImpactedObjects) {
            MENU_ITEMS.push({
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
              disabled: item.ticket !== undefined,
            });
          }
          data.push({
            component: ClusterNameWithRegion({
              name: item.recommendation?.kind,
              hideIcon: true,
              region:
                item.ticket !== undefined ? (
                  <Stack>
                    <Text secondaryText value={`Group - ${item.recommendation?.group}`} />
                    <Text secondaryText value={`Version - ${item.recommendation?.version}`} />
                    <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} />
                  </Stack>
                ) : (
                  <Stack>
                    <Text secondaryText value={`Group - ${item.recommendation?.group}`} />
                    <Text secondaryText value={`Version - ${item.recommendation?.version}`} />
                  </Stack>
                ),
            }),
            drilldownQuery: item,
          });
          data.push({ component: <Text value={item.recommendation?.deprecated_version ? item.recommendation?.deprecated_version : '-'} /> });
          data.push({ component: <Text value={item.recommendation?.deleted_version ? item.recommendation?.deleted_version : '-'} /> });
          data.push(add_api_details(item));
          data.push({
            component: (
              <Stack>
                <Stack spacing={1}>
                  {item.recommendation?.deleted_items?.map((o) => {
                    const displayText = o.namespace ? `${o.namespace}/${o.objectname}` : `global/${o.objectname}`;
                    return <Label key={o.namespace + o.objectname} text={displayText} displayTooltip tooltipCharLimit={40} />;
                  })}
                  {item.recommendation?.deprecated_items?.map((o) => {
                    const displayText = o.namespace ? `${o.namespace}/${o.objectname}` : `global/${o.objectname}`;
                    return <Label key={o.namespace + o.objectname} text={displayText} tone='warning' displayTooltip tooltipCharLimit={40} />;
                  })}
                </Stack>
              </Stack>
            ),
          });
          const actualSeverity = effectiveSeverity(item);
          data.push({
            component: <SeverityIcon level={toDsSeverityLevel(actualSeverity)} aria-label={actualSeverity || '-'} />,
            data: actualSeverity,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'space-between'} justifyContent={'flex-end'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });
          return data;
        });
        setKubernetesClusterUpgradeRecommendation(k8sRecommendationData);
        setKubernetesClusterUpgradeRecommendationCount(sortedRecommendations.length);
        setLoading(false);
      })
      .catch((error) => {
        console.error(error);
        setLoading(false);
      });
  }, [props?.kubernetes?.id, props?.kubernetes?.version, recommendationStatus, page, rowsPerPage]);
  useEffect(() => {
    listClusterUpgradeRecommendations();
  }, [listClusterUpgradeRecommendations]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }

    recommendationApi
      .getK8sRecommendation({
        accountId: props?.kubernetes?.id,
        category: 'InfraUpgrade',
        ruleName: 'k8s_api_deprecated',
        limit: 1000,
        offset: 0,
      })
      .then((res) => {
        const all = res?.data?.recommendation || [];
        setTotalKubernetesClusterUpgradeRecommendationCount(all.length);
      })
      .catch((error) => {
        console.error('Error fetching total count:', error);
      });
  }, [props?.kubernetes?.id]);

  const handleTicketSuccess = () => {
    listClusterUpgradeRecommendations();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    recommendationApi.createRecommendationJob(props?.kubernetes?.id, 'k8s_version_upgrade').then((_res) => {
      alert('Scan Triggered Successfully, Data will be updated in Sometime');
    });
  };

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'K8s Cluster Version Upgrade Issue',
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      {!props?.disableInfographics && (
        <Box
          sx={{
            display: 'flex',
            flex: 1,
            flexDirection: 'row',
            gap: 'var(--ds-space-3)',
            '& > *': { maxWidth: 'calc((100% - 3 * var(--ds-space-3)) / 4)' },
          }}
          mt={2}
          mb={2}
        >
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: 'var(--ds-space-3) var(--ds-space-4)' }}>
            <Stat
              size='md'
              label='Total Recommendations'
              info={{ tooltip: 'Deprecated/removed APIs impacting the target Kubernetes version' }}
              value={
                Number.isFinite(totalKubernetesClusterUpgradeRecommendationCount)
                  ? totalKubernetesClusterUpgradeRecommendationCount.toLocaleString()
                  : totalKubernetesClusterUpgradeRecommendationCount ?? '—'
              }
            />
          </WidgetCard>
        </Box>
      )}
      <ListingLayout id='cluster-upgrade-recommendation'>
        <ListingLayout.Toolbar
          actions={
            <>
              <RecommendationJobDetails jobName={'k8s_version_upgrade'} />
              <Divider orientation='vertical' color={'var(--ds-gray-200)'} sx={{ mx: 'var(--ds-space-2)', my: 1 }} />
              {!props?.disableInfographics && <DownloadButton onClick={() => ({ tableId: kubernetesClusterUpgradeTable })} />}
              <DsButton
                id='triggerRecommendation'
                tone='primary'
                size='md'
                disabled={!hasWriteAccess(props?.kubernetes?.id ?? '')}
                onClick={triggerRecommendationJob}
              >
                Generate
              </DsButton>
            </>
          }
        >
          <FilterDropdown
            id='cluster-upgrade-filter-status'
            label='Status'
            options={RECOMMENDATION_STATUS}
            value={recommendationStatus}
            onSelect={(e) => {
              setRecommendationStatus(e?.target?.value);
              setPage(0);
            }}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={kubernetesClusterUpgradeTable}
            headers={RECOMMENDATION_HEADER}
            tableData={kubernetesClusterUpgradeRecommendation}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesClusterUpgradeRecommendationCount}
            onPageChange={changePage}
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            tableHeadingCenter={[]}
            stickyColumnIndex={'8'}
            pageNumber={page + 1}
            sort={{
              name: 'Savings/mo',
              order: 'desc',
            }}
            loading={loading}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'Description',
                  value: 0,
                  componentFn: kubernetesClusterUpgradeRecommendationDescription,
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

function add_api_details(item) {
  return {
    component: ClusterNameWithRegion({
      name: item.recommendation.replacement.kind ? item.recommendation.replacement.kind : '-',
      hideIcon: true,
      region: item.recommendation.replacement.kind ? (
        <Stack>
          <Text secondaryText value={`Group - ${item.recommendation?.replacement?.group}`} />
          <Text secondaryText value={`Version - ${item.recommendation?.replacement?.version}`} />
        </Stack>
      ) : (
        <></>
      ),
    }),
  };
}

function kubernetesClusterUpgradeRecommendationDescription(opt, drilldown, _row) {
  const recommendation = drilldown?.recommendation;
  if (!recommendation) {
    return <>No description available</>;
  }

  const deletedItems = recommendation.deleted_items || [];
  const deprecatedItems = recommendation.deprecated_items || [];
  const totalImpacted = deletedItems.length + deprecatedItems.length;

  const baseDescription = recommendation.description || '';

  return (
    <div style={{ fontSize: 'var(--ds-text-body-lg)', lineHeight: '1.6' }}>
      {baseDescription && <div style={{ marginBottom: 'var(--ds-space-4)' }}>{baseDescription}</div>}

      {totalImpacted > 0 ? (
        <>
          <div style={{ marginBottom: 'var(--ds-space-3)' }}>
            <strong>Impacted Resources:</strong>
          </div>

          {deletedItems.length > 0 && (
            <div style={{ marginBottom: 'var(--ds-space-3)' }}>
              <div style={{ color: 'var(--ds-red-600)', fontWeight: 'var(--ds-font-weight-semibold)', marginBottom: 'var(--ds-space-1)' }}>
                {deletedItems.length} resource(s) will be removed in the target version:
              </div>
              {deletedItems.map((item, index) => {
                const scope = item.namespace ? item.namespace : 'cluster-wide';
                return (
                  <div key={index} style={{ marginLeft: 'var(--ds-space-4)', marginBottom: 'var(--ds-space-1)' }}>
                    • {item.objectname} ({scope})
                  </div>
                );
              })}
            </div>
          )}

          {deprecatedItems.length > 0 && (
            <div style={{ marginBottom: 'var(--ds-space-3)' }}>
              <div style={{ color: 'var(--ds-amber-400)', fontWeight: 'var(--ds-font-weight-semibold)', marginBottom: 'var(--ds-space-1)' }}>
                {deprecatedItems.length} resource(s) are using deprecated APIs:
              </div>
              {deprecatedItems.map((item, index) => {
                const scope = item.namespace ? item.namespace : 'cluster-wide';
                return (
                  <div key={index} style={{ marginLeft: 'var(--ds-space-4)', marginBottom: 'var(--ds-space-1)' }}>
                    • {item.objectname} ({scope})
                  </div>
                );
              })}
            </div>
          )}

          <div style={{ marginBottom: 'var(--ds-space-3)' }}>
            <strong>What you need to do:</strong>
            <div style={{ marginTop: 'var(--ds-space-1)' }}>
              {recommendation.replacement?.kind ? (
                <>
                  Update these resources to use <strong>{recommendation.replacement.kind}</strong> with API version{' '}
                  <strong>{recommendation.replacement.version}</strong> from group <strong>{recommendation.replacement.group}</strong>.
                </>
              ) : (
                'Review and update these resources before upgrading to avoid cluster issues.'
              )}
            </div>
          </div>

          <div style={{ color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
            Tip: Test the migration in a non-production environment first to ensure compatibility.
          </div>
        </>
      ) : (
        <div
          style={{
            backgroundColor: 'var(--ds-blue-100)',
            border: '1px solid var(--ds-blue-500)',
            borderRadius: 'var(--ds-radius-md)',
            padding: 'var(--ds-space-3) var(--ds-space-4)',
            color: 'var(--ds-blue-700)',
          }}
        >
          <div style={{ fontWeight: 'var(--ds-font-weight-semibold)', marginBottom: 'var(--ds-space-1)' }}>All good! Nothing to worry about.</div>
          <div>
            This API recommendation is just informational. Your cluster resources are using supported APIs that are compatible with the target
            Kubernetes version.
          </div>
        </div>
      )}
    </div>
  );
}

KubernetesClusterUpgradeRecommendation.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesClusterUpgradeRecommendation;
