import { Box, Stack, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Text from '@components1/common/format/Text';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import Datetime from '@components1/common/format/Datetime';
import { hasWriteAccess } from '@lib/auth';
import PropTypes from 'prop-types';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import CustomButton from '@components1/common/NewCustomButton';
import { action } from 'src/utils/actionStyles';
import { colors } from 'src/utils/colors';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import CustomTable from '@components1/common/tables/CustomTable2';
import Title from '@components1/common/Title';
import CustomLink from '@components1/common/CustomLink';
import SafeIcon from '@components1/common/SafeIcon';
import { RefreshIcon } from '@assets';
import { snackbar } from '@components1/common/snackbarService';

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
                <CustomLink target={'_blank'} href={'https://www.cisecurity.org/benchmark/kubernetes'} style={{ fontSize: '14px', fontWeight: 400 }}>
                  {item.rule_id}
                </CustomLink>
                <Text
                  style={{
                    display: '-webkit-box',
                    WebkitLineClamp: 2,
                    WebkitBoxOrient: 'vertical',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'normal',
                  }}
                  value={item?.rule_name}
                />
              </Stack>
            ),
            drilldownQuery: {
              data: item,
            },
            data: item.rule_id,
          });
          data.push({
            component: (
              <Text
                style={{
                  display: '-webkit-box',
                  WebkitLineClamp: 2,
                  WebkitBoxOrient: 'vertical',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'normal',
                }}
                value={item?.rule_description}
              />
            ),
          });
          data.push({
            component: <SeverityIcon severityType={item.severity || '-'} />,
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
        <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
          <SummaryWidget title='Total Recommendations' value={totalKubernetesSecurityCount} />
        </Box>
      )}
      <BoxLayout2
        heading={props.heading === undefined ? 'Security' : props.heading}
        id='best-practices'
        filterOptions={[
          {
            type: 'dropdown',
            label: 'Status',
            options: RECOMMENDATION_STATUS,
            value: recommendationStatus,
            enabled: props?.enableFilters?.includes('status') ?? true,
            onSelect: function (e, _rule) {
              setRecommendationStatus(e?.target?.value);
              setPage(0);
            },
          },
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: kubernetesSecurityTable,
              };
            },
          },
          sharing: { enabled: true },
        }}
        extraOptions={
          hasWriteAccess(props?.kubernetes?.id)
            ? [
                <CustomButton
                  showTooltip={false}
                  variant='iconButton'
                  key='triggerRecommendation'
                  id='triggerRecommendation'
                  text='Refresh'
                  startIcon={<SafeIcon src={RefreshIcon} alt='refresh' />}
                  onClick={triggerRecommendationJob}
                />,
              ]
            : []
        }
      >
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
        <RecommendationJobDetails jobName={'kube_bench_scan'} />
      </BoxLayout2>
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
                component: <Text value={targets[0]} sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }} />,
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
          setReferences(res.data?.recommendation[0]?.recommendation?.Misconfigurations[0]?.References);
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
    <BoxLayout2 sharingOptions={false} sx={{ mt: '-24px' }}>
      <Box sx={{ mb: '24px' }}>
        <Title title={'Resolution'} />
        <Typography sx={{ fontSize: '14px' }}>{resolution}</Typography>
      </Box>

      <Box sx={{ mb: '24px' }}>
        <Title title={'References'} />
        <ul>
          {references.map((item) => (
            <li key={item}>
              <CustomLink href={item} target='_blank' rel='noreferrer' style={{ fontSize: '14px', fontWeight: 400 }}>
                {item}
              </CustomLink>
            </li>
          ))}
        </ul>
      </Box>
      <Title title={'Impacted Resources'} />
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
    </BoxLayout2>
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
