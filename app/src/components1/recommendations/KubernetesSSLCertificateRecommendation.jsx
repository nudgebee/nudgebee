import { Box, Stack, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import Datetime from '@components1/common/format/Datetime';
import PropTypes from 'prop-types';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import CustomButton from '@components1/common/NewCustomButton';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { action } from 'src/utils/actionStyles';
import CustomLink from '@components1/common/CustomLink';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import { Text } from '@components1/common';
import { snackbar } from '@components1/common/snackbarService';
import apiUser from '@api1/user';

const RECOMMENDATION_HEADER = [
  { name: 'Namespace', width: '30%' },
  { name: 'Certificate Name', width: '40%' },
  { name: 'Expires In', width: '10%' },
  { name: 'Updated At', width: '10%' },
  { name: 'Actions', width: '5%' },
];

const KubernetesSSLCertificateRecommendation = (props) => {
  const [kubernetesSSLCertificateUpgradeRecommendation, setKubernetesSSLCertificateUpgradeRecommendation] = useState([]);
  const [kubernetesSSLCertificateUpgradeRecommendationCount, setKubernetesSSLCertificateUpgradeRecommendationCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const kubernetesSSLCertificateUpgradeTable = 'kubernetesSSLCertificateUpgradeTable';

  const changePage = (page, limit) => {
    setPage(page - 1);
    if (limit != rowsPerPage) {
      setRowsPerPage(limit);
    }
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Certificate Namespace**: ' + data?.recommendation?.namespace + '\n';
    description += '**Certificate Name**: ' + data?.recommendation?.name + '\n';
    description += '**Certificate Expire In**: ' + data?.recommendation?.days_until_expiry + ' days \n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const getKubernetesSSLCertificateRecommendation = () => {
    if (!props?.kubernetes?.id) {
      return;
    }
    setLoading(true);
    setKubernetesSSLCertificateUpgradeRecommendation([]);
    let recommendation = null;
    recommendationApi
      .getK8sRecommendation({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: 'certificate_expiry',
        status: recommendationStatus ? [recommendationStatus] : [],
        recommendation: recommendation,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        res?.data?.recommendation?.sort((a, b) => {
          return a?.recommendation?.days_until_expiry - b?.recommendation?.days_until_expiry;
        });

        let k8sRecommendationData = res?.data?.recommendation.map((item) => {
          let data = [];
          const expiryDate = new Date(item.recommendation.expiry_date);
          const now = new Date();
          const type = expiryDate < now ? 'previous' : 'future';
          let MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
              disabled: item.ticket !== undefined,
            },
          ];
          data.push({
            component: ClusterNameWithRegion({
              name: item.recommendation?.namespace,
              hideIcon: true,
              region:
                item.ticket !== undefined ? (
                  <Stack>
                    <Typography sx={{ fontSize: '12px' }}>
                      Ticket -{' '}
                      <CustomLink href={item.ticket?.url} style={{ fontSize: '12px' }} target='_blank' rel='noreferrer'>
                        {item.ticket?.ticket_id}
                      </CustomLink>
                    </Typography>
                  </Stack>
                ) : (
                  <> </>
                ),
            }),
            drilldownQuery: item,
          });
          data.push({
            component: (
              <Box display={'flex'} gap={2}>
                {type == 'previous' ? <div style={{ width: 2, backgroundColor: 'red', paddingRight: 4 }} /> : null}
                <Text value={item.recommendation.name} />
              </Box>
            ),
          });
          data.push({
            component: <Datetime value={item.recommendation.expiry_date} suffix=' ' />,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'space-between'} justifyContent={'center'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });
          return data;
        });
        setKubernetesSSLCertificateUpgradeRecommendation(k8sRecommendationData);
      })
      .catch((error) => {
        console.error(error);
      });
  };

  useEffect(() => {
    getKubernetesSSLCertificateRecommendation();
  }, [props?.kubernetes?.id, page, recommendationStatus, rowsPerPage]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }

    recommendationApi
      .getK8sRecommendationSummary({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: 'certificate_expiry',
        status: recommendationStatus ? [recommendationStatus] : [],
      })
      .then((res) => {
        setKubernetesSSLCertificateUpgradeRecommendationCount(res?.data?.recommendation_aggregate.aggregate.count);
      });
  }, [props?.kubernetes?.id, recommendationStatus]);

  const handleTicketSuccess = () => {
    getKubernetesSSLCertificateRecommendation();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    recommendationApi.createRecommendationJob(props?.kubernetes?.id, 'certificate_scanner').then(() => {
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
          subject: 'K8s SSL Certificate Upgrade Recommendation',
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      <BoxLayout2
        heading={props.heading === undefined ? 'Cluster Upgrade' : props.heading}
        id='cluster-upgrade-recommendation'
        filterOptions={[
          {
            type: 'dropdown',
            label: 'Status',
            options: RECOMMENDATION_STATUS,
            value: recommendationStatus,
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
                tableId: kubernetesSSLCertificateUpgradeTable,
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
        <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={1}>
          <SummaryWidget title='Total Certificates' value={kubernetesSSLCertificateUpgradeRecommendationCount} />
        </Box>
        <KubernetesTable2
          id={kubernetesSSLCertificateUpgradeTable}
          headers={RECOMMENDATION_HEADER}
          data={kubernetesSSLCertificateUpgradeRecommendation}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesSSLCertificateUpgradeRecommendationCount}
          onPageChange={changePage}
          tableHeadingCenter={['Actions']}
          stickyColumnIndex='5'
          showUpdatedEmptyData={props.showUpdatedEmptyData}
          sort={{
            name: 'Savings/mo',
            order: 'desc',
          }}
          loading={loading}
          pageNumber={page + 1}
        />
        <RecommendationJobDetails jobName={'certificate_scanner'} />
      </BoxLayout2>
    </>
  );
};

KubernetesSSLCertificateRecommendation.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesSSLCertificateRecommendation;
