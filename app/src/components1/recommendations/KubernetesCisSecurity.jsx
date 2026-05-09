import { Box, Stack, Typography } from '@mui/material';

import { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Link from 'next/link';
import Text from '@components1/common/format/Text';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import Datetime from '@components1/common/format/Datetime';
import PropTypes from 'prop-types';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import { snackbar } from '@components1/common/snackbarService';
import CustomButton from '@components1/common/NewCustomButton';
import { action } from 'src/utils/actionStyles';
import CustomTicketLink from '@components1/common/CustomTicketLink';

const BEST_PRACTICES_HEADER = ['Test', 'Status', 'ExecutionType', 'Remediation', 'Updated At', 'Actions'];
const KubernetesCisSecurity = (props) => {
  const [kubernetesSecurity, setKubernetesSecurity] = useState([]);
  const [kubernetesSecurityCount, setKubernetesSecurityCount] = useState(0);
  const [totalKubernetesSecurityCount, setTotalKubernetesSecurityCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const rowsPerPage = 100;
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [loading, setLoading] = useState(false);
  const kubernetesSecurityTable = 'kubernetesSecurityTable';

  const changePage = (page) => {
    setPage(page - 1);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    //generate ticket description
    let description = '';
    description += '**TestId**: ' + data?.recommendation?.test_number + '\n';
    description += '**TestDesc**: ' + data?.recommendation?.test_desc + '\n';
    description += '**Status**: ' + data?.recommendation?.status + '\n';
    description += '**Type**: ' + data?.recommendation?.type + '\n';
    description += '**Remediation**: ' + data?.recommendation?.remediation + '\n';
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
      .getK8sRecommendation({
        accountId: props?.kubernetes?.id,
        category: 'Security',
        ruleName: 'CIS',
        status: recommendationStatus ? [recommendationStatus] : [],
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
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
          if (typeof item?.recommendation === 'string') {
            item.recommendation = JSON.parse(item.recommendation);
          }
          data.push({
            component: (
              <Stack direction='column' spacing={1}>
                <Link href={'https://www.cisecurity.org/benchmark/kubernetes'}>{item.recommendation?.test_number}</Link>
                <Typography sx={{ fontSize: '10px' }}>{item?.recommendation?.test_desc}</Typography>
                {item.ticket ? <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} /> : <></>}
              </Stack>
            ),
            drilldownQuery: item,
            data: item.recommendation?.VulnerabilityID,
          });
          data.push({
            component: <Text textAlign='center' value={item?.recommendation?.status} />,
          });
          data.push({
            component: <Text value={item?.recommendation?.type === '' ? 'Auto' : item?.recommendation?.type} />,
          });
          data.push({
            component: <Text value={item?.recommendation?.remediation} />,
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
        const totalCount = res?.data?.recommendation_aggregate?.aggregate?.count;
        setKubernetesSecurityCount(totalCount);
        setTotalKubernetesSecurityCount(totalCount);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listCisSecurityRecommendations();
  }, [props?.kubernetes?.id, page, recommendationStatus]);

  const handleTicketSuccess = () => {};

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    recommendationApi.createRecommendationJob(props?.kubernetes?.id, 'kube_bench_scan').then((_res) => {
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
          subject: 'Security Issue On - ' + ticketData.image,
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
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
        extraOptions={[
          <CustomButton
            variant='blueButton'
            key='triggerRecommendation'
            id='triggerRecommendation'
            text='Generate'
            onClick={triggerRecommendationJob}
          />,
        ]}
      >
        <KubernetesTable2
          id={kubernetesSecurityTable}
          showExpandable
          headers={BEST_PRACTICES_HEADER}
          data={kubernetesSecurity}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesSecurityCount}
          onPageChange={changePage}
          pageNumber={page + 1}
          stickyColumnIndex='6'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          expandable={{
            tabs: [
              {
                text: 'Test Info',
                value: 1,
                componentFn: KubernetesCisSecurityTestInfo,
              },
            ],
          }}
          loading={loading}
        />
        <RecommendationJobDetails jobName={'kube_bench_scan'} />
      </BoxLayout2>
    </>
  );
};

function KubernetesCisSecurityTestInfo(opt, drilldown, _row) {
  return (
    <>
      {drilldown?.recommendation?.test_info?.map((t) => {
        return <Typography key={t}>{t}</Typography>;
      })}

      <Typography>
        <b>Audit - </b>
        {drilldown?.recommendation?.audit}
      </Typography>
      <Typography>
        <b>Expected Result - </b>
        {drilldown?.recommendation?.expected_result}
      </Typography>
      <Typography>
        <b>Actual Result - </b>
        {drilldown?.recommendation?.actual_value}
      </Typography>
    </>
  );
}

KubernetesCisSecurity.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  disableInfographic: PropTypes.bool,
  enableFilters: PropTypes.array,
};

export default KubernetesCisSecurity;
