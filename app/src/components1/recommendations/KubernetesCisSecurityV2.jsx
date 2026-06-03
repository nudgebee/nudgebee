import { Box, Stack, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Button as DsButton } from '@components1/ds/Button';
import DownloadButton from '@common-new/DownloadButton';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import Text from '@common-new/format/Text';
import WidgetCard from '@components1/ds/WidgetCard';
import Datetime from '@common-new/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import { Divider } from '@components1/ds/Divider';
import { Card } from '@components1/ds/Card';
import { Skeleton } from '@components1/ds/Skeleton';
import { action } from 'src/utils/actionStyles';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import CustomTable from '@common-new/tables/CustomTable2';
import { Link } from '@components1/ds/Link';
import RefreshIcon from '@mui/icons-material/Refresh';
import { toast as snackbar } from '@components1/ds/Toast';

// CIS severity values come from the API as 'High' / 'Medium' / 'Low' / 'Info'.
// ds/SeverityIcon's level enum is the lowercase 5-tier; normalize + map.
const SEVERITY_TO_DS_LEVEL = {
  critical: 'critical',
  high: 'high',
  medium: 'medium',
  low: 'low',
  info: 'info',
};
const toDsSeverityLevel = (s) => SEVERITY_TO_DS_LEVEL[String(s || '').toLowerCase()] || 'info';

const CIS_HEADER = [
  { name: 'Rule', width: '25%' },
  { name: 'Description', width: '35%' },
  { name: 'Severity', width: '10%' },
  { name: 'Failures', width: '10%' },
  { name: 'Updated At', width: '10%' },
  { name: 'Actions', width: '5%' },
];
const KubernetesCisSecurity = (props) => {
  const kubernetesSecurityTable = 'kubernetesSecurityTable';

  const [kubernetesSecurity, setKubernetesSecurity] = useState([]);
  const [kubernetesSecurityCount, setKubernetesSecurityCount] = useState(0);
  const [totalKubernetesSecurityCount, setTotalKubernetesSecurityCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [loading, setLoading] = useState(false);

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    //generate ticket description
    let description = '';
    description += '**TestId**: ' + data?.rule_id + '\n';
    description += '**TestName**: ' + data?.rule_name + '\n';
    description += '**TestDesc**: ' + data?.rule_description + '\n';
    description += '**Severity**: ' + data?.severity + '\n';
    description += '**Breaches**: ' + data?.count + '\n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const listCisSecurityRecommendations = () => {
    if (!props?.kubernetes?.id) {
      return;
    }
    setLoading(true);
    setKubernetesSecurity([]);
    recommendationApi
      .getK8sSecurityCISRecommendationGroups({
        accountId: props?.kubernetes?.id,
        status: recommendationStatus,
      })
      .then((res) => {
        setLoading(false);
        let MENU_ITEMS = [
          {
            icon: TicketsIcon,
            label: 'Create Ticket',
            id: 0,
          },
        ];
        let k8sRecommendationData = res?.data?.recommendation.map((item) => {
          let data = [];
          data.push({
            component: (
              <Stack direction='column' spacing={0.5}>
                <Link href={'https://www.cisecurity.org/benchmark/kubernetes'} openInNew>
                  {item.rule_id}
                </Link>
                <Text showAutoEllipsis lineClamp={2} value={item?.rule_name} />
              </Stack>
            ),
            drilldownQuery: {
              data: item,
            },
            data: item.rule_id,
          });
          data.push({
            component: <Text showAutoEllipsis lineClamp={2} value={item?.rule_description} />,
          });
          data.push({
            component: <SeverityIcon level={toDsSeverityLevel(item.severity)} aria-label={item.severity || '-'} />,
            data: item.severity || '-',
          });
          data.push({
            component: <Text value={item?.count} />,
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
        setKubernetesSecurity(k8sRecommendationData);
        setKubernetesSecurityCount(k8sRecommendationData?.length ?? 0);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listCisSecurityRecommendations();
  }, [props?.kubernetes?.id, page, recommendationStatus]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }
    recommendationApi
      .getK8sSecurityCISRecommendationGroups({
        accountId: props?.kubernetes?.id,
      })
      .then((res) => {
        setTotalKubernetesSecurityCount(res?.data?.recommendation?.length ?? 0);
      })
      .catch(() => {
        console.error('Error fetching total count');
      });
  }, [props?.kubernetes?.id]);

  const handleTicketSuccess = () => {
    listCisSecurityRecommendations();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    recommendationApi.createRecommendationJob(props?.kubernetes?.id, 'trivy_cis_scan').then(() => {
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
          subject: 'CIS Compliance Issue - ' + ticketData.rule_name,
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: props?.kubernetes?.id + ':Security:k8s-cis-1.23:' + ticketData.rule_id,
          type: 'kubernetes',
        }}
      />
      {!props?.disableInfographic && (
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)' }} mt={2} mb={2}>
          <WidgetCard sx={{ mt: 0, minWidth: '160px' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-600)' }}>
              Total Recommendations
            </Typography>
            <Typography
              sx={{ fontSize: 'var(--ds-text-display)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)', lineHeight: 1.2 }}
            >
              {totalKubernetesSecurityCount}
            </Typography>
          </WidgetCard>
        </Box>
      )}
      <ListingLayout id='best-practices'>
        <ListingLayout.Toolbar
          actions={
            <>
              <RecommendationJobDetails jobName={'kube_bench_scan'} />
              <Divider orientation='vertical' color={'var(--ds-gray-200)'} sx={{ mx: 'var(--ds-space-2)', my: 1 }} />
              <DownloadButton onClick={() => ({ tableId: kubernetesSecurityTable })} />
              {hasWriteAccess(props?.kubernetes?.id) && (
                <DsButton
                  id='triggerRecommendation'
                  tone='secondary'
                  size='md'
                  composition='icon-only'
                  icon={<RefreshIcon fontSize='small' />}
                  aria-label='Refresh'
                  tooltip='Trigger Scan'
                  onClick={triggerRecommendationJob}
                />
              )}
            </>
          }
        >
          {(props?.enableFilters?.includes('status') ?? true) && (
            <FilterDropdown
              id='cis-filter-status'
              label='Status'
              options={RECOMMENDATION_STATUS}
              value={recommendationStatus}
              onSelect={(e) => {
                setRecommendationStatus(e?.target?.value);
                setPage(0);
              }}
            />
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={kubernetesSecurityTable}
            showExpandable
            headers={CIS_HEADER}
            tableData={kubernetesSecurity}
            rowsPerPage={kubernetesSecurityCount}
            totalRows={kubernetesSecurityCount}
            onPageChange={undefined}
            pageNumber={page + 1}
            stickyColumnIndex='6'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            expandable={{
              tabs: [
                {
                  text: 'Details',
                  value: 0,
                  componentFn: KubernetesCisSecurityFailureInfoFn,
                },
              ],
            }}
            loading={loading}
            tableHeadingCenter={['Actions', 'Severity']}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

function KubernetesCisSecurityFailureInfoFn(opt, drilldown, _row) {
  return <KubernetesCisSecurityFailureInfo account_id={drilldown?.data?.account_id} rule_id={drilldown?.data?.rule_id} />;
}

function KubernetesCisSecurityFailureInfo(props) {
  const [recommendations, setRecommendations] = useState([]);
  const [references, setReferences] = useState([]);
  const [resolution, setResolution] = useState('');

  const [infoRowsPerPage, setInfoRowsPerPage] = useState(5);
  const [infoPage, setInfoPage] = useState(0);
  const [totalRows, setTotalRows] = useState(0);
  const [infoLoading, setInfoLoading] = useState(false);
  useEffect(() => {
    setInfoLoading(true);
    recommendationApi
      .getK8sRecommendation({
        accountId: props.account_id,
        category: 'Security',
        ruleName: 'k8s-cis-1.23',
        recommendation: {
          Id: props.rule_id,
        },
        limit: infoRowsPerPage,
        offset: infoPage * infoRowsPerPage,
      })
      .then((res) => {
        let tableData = res.data?.recommendation?.flatMap((item) => {
          let targets = item.recommendation.Target.split('/');
          return item.recommendation.Misconfigurations.map((misconfig) => {
            return [
              {
                component: (
                  <Text
                    value={targets[0]}
                    sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-700)' }}
                  />
                ),
              },
              {
                component: <Text showAutoEllipsis value={targets[1]} />,
              },
              {
                component: <Text showAutoEllipsis value={misconfig.Message} />,
              },
            ];
          });
        });
        if (res.data?.recommendation?.length > 0) {
          setReferences(res.data?.recommendation[0]?.recommendation?.Misconfigurations[0]?.References || []);
          setResolution(res.data?.recommendation[0]?.recommendation?.Misconfigurations[0]?.Resolution);
        }
        setRecommendations(tableData);
        setTotalRows(res.data?.recommendation_aggregate.aggregate.count);
        setInfoLoading(false);
      });
  }, [props?.account_id, props?.rule_id, infoPage, infoRowsPerPage]);

  const changeInfoPage = (page, limit) => {
    setInfoPage(page - 1);
    setInfoRowsPerPage(limit);
  };

  return (
    <Box sx={{ p: 2, display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--ds-space-4)' }}>
        <Card
          size='sm'
          elevation='flat'
          header={
            infoLoading ? (
              <Skeleton shape='text' size='title' width='50%' />
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
                Resolution
              </Typography>
            )
          }
        >
          {infoLoading ? (
            <Stack spacing={1}>
              {Array.from({ length: 3 }).map((_, j) => (
                <Skeleton key={j} shape='text' size='text' width={j === 2 ? '70%' : '100%'} />
              ))}
            </Stack>
          ) : resolution ? (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-600)' }}>{resolution}</Typography>
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-400)', fontStyle: 'italic' }}>No resolution found</Typography>
          )}
        </Card>

        <Card
          size='sm'
          elevation='flat'
          header={
            infoLoading ? (
              <Skeleton shape='text' size='title' width='50%' />
            ) : (
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
                References
              </Typography>
            )
          }
        >
          {infoLoading ? (
            <Stack spacing={1}>
              {Array.from({ length: 3 }).map((_, j) => (
                <Skeleton key={j} shape='text' size='text' width={j === 2 ? '70%' : '100%'} />
              ))}
            </Stack>
          ) : references.length > 0 ? (
            <Box component='ul' sx={{ m: 0, pl: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
              {references.map((item) => (
                <li key={item}>
                  <Link href={item} openInNew>
                    {item}
                  </Link>
                </li>
              ))}
            </Box>
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-400)', fontStyle: 'italic' }}>No references found</Typography>
          )}
        </Card>
      </Box>

      <ListingLayout id='impacted-resources'>
        <ListingLayout.Toolbar title='Impacted Resources' />
        <ListingLayout.Body>
          <CustomTable
            tableData={recommendations}
            headers={[
              { name: 'ResourceType', width: '20%' },
              { name: 'Resource', width: '20%' },
              { name: 'Message', width: '50%' },
            ]}
            pageNumber={infoPage + 1}
            totalRows={totalRows}
            rowsPerPage={infoRowsPerPage}
            onPageChange={changeInfoPage}
            loading={infoLoading}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </Box>
  );
}

KubernetesCisSecurityFailureInfo.propTypes = {
  account_id: PropTypes.string,
  rule_id: PropTypes.string,
};

KubernetesCisSecurity.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  disableInfographic: PropTypes.bool,
  enableFilters: PropTypes.array,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesCisSecurity;
