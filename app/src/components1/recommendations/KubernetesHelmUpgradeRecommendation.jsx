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
import { snackbar } from '@components1/common/snackbarService';
import CustomButton from '@components1/common/NewCustomButton';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { action } from 'src/utils/actionStyles';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { titleCase } from '@lib/formatter';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import CustomLink from '@components1/common/CustomLink';
import SummaryWidget from '@components1/optimise/SummaryWidget';

const RECOMMENDATION_HEADER = ['Chart Name', 'Release Name', 'Severity', 'Issues', 'Installed', 'Latest', 'Updated At', ''];

const KubernetesHelmUpgradeRecommendation = ({ accountId, heading = '', showUpdatedEmptyData = true }) => {
  const [kubernetesHelmUpgradeRecommendation, setKubernetesHelmUpgradeRecommendation] = useState([]);
  const [kubernetesHelmUpgradeRecommendationCount, setKubernetesHelmUpgradeRecommendationCount] = useState(10);
  const [totalKubernetesHelmUpgradeRecommendationCount, setTotalKubernetesHelmUpgradeRecommendationCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const kubernetesHelmUpgradeTable = 'kubernetesHelmUpgradeTable';

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Chart Name**: ' + data?.recommendation?.chartName + '\n';
    description += '**Release Name**: ' + data?.recommendation?.release + '\n';
    description += '**Namespace**: ' + data?.recommendation?.namespace + '\n';
    description += '**Installed Version**: ' + data?.recommendation?.Installed?.version + '\n';
    description += '**Latest Version**: ' + data?.recommendation?.Latest?.version + '\n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const checkAndShowTitleCase = (data) => {
    const trueKeys = [];
    for (const key in data) {
      if (data[key] === true) {
        trueKeys.push(titleCase(key));
      }
    }
    return trueKeys.length > 0 ? <Text value={`${trueKeys.join(', ')}`} /> : null;
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }

    listHelmUpgradeRecommendation();
  }, [accountId, page, recommendationStatus, rowsPerPage]);

  const listHelmUpgradeRecommendation = () => {
    setLoading(true);
    setKubernetesHelmUpgradeRecommendation([]);
    setKubernetesHelmUpgradeRecommendationCount(0);
    let recommendation = null;
    recommendationApi
      .getK8sRecommendation({
        accountId: accountId,
        ruleName: 'helm_chart_upgrade',
        category: 'InfraUpgrade',
        status: recommendationStatus ? [recommendationStatus] : [],
        recommendation: recommendation,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        let k8sRecommendationData = res?.data?.recommendation.map((item) => {
          let data = [];
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
              name: item.recommendation?.chartName || '-',
              hideIcon: true,
              namespace: 'ns:' + item.recommendation?.namespace || '-',
              namespaceFont: '12px',
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
                ) : null,
            }),
            drilldownQuery: item,
          });
          data.push({ component: <Text value={item.recommendation.release} /> });
          data.push({ component: <CustomLabels text={item.severity} /> });
          data.push({
            text: (
              <>
                {checkAndShowTitleCase({
                  outdated: item.recommendation?.outdated || false,
                  deprecated: item.recommendation?.deprecated || false,
                  overridden: item.recommendation?.overridden || false,
                })}
              </>
            ),
          });
          data.push({
            component: (
              <Stack>
                <Text value={item.recommendation.Installed?.version || '-'} />
                <Datetime value={item.recommendation.Installed.date} />
              </Stack>
            ),
          });
          data.push({
            component: (
              <Stack>
                <Text value={item.recommendation.Latest?.version || '-'} />
                {item.recommendation.Latest.date != '0001-01-01T00:00:00Z' && <Datetime value={item.recommendation.Latest.date} />}
              </Stack>
            ),
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
        setKubernetesHelmUpgradeRecommendation(k8sRecommendationData);
        const totalCount = res?.data?.recommendation_aggregate?.aggregate?.count || 0;
        setKubernetesHelmUpgradeRecommendationCount(totalCount);
        setTotalKubernetesHelmUpgradeRecommendationCount(totalCount);
      })
      .catch((error) => {
        console.error(error);
      });
  };

  const handleTicketSuccess = () => {
    listHelmUpgradeRecommendation();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const triggerRecommendationJob = () => {
    recommendationApi.createRecommendationJob(accountId, 'helm_chart_upgrade').then(() => {
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
          accountId: accountId,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={totalKubernetesHelmUpgradeRecommendationCount} />
      </Box>
      <BoxLayout2
        heading={heading === undefined ? 'Cluster Upgrade' : heading}
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
                tableId: kubernetesHelmUpgradeTable,
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
        onRefresh={{
          enabled: true,
          loading: loading,
          text: '',
          onClick: () => {
            listHelmUpgradeRecommendation();
          },
        }}
      >
        <KubernetesTable2
          id={kubernetesHelmUpgradeTable}
          headers={RECOMMENDATION_HEADER}
          data={kubernetesHelmUpgradeRecommendation}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesHelmUpgradeRecommendationCount}
          onPageChange={changePage}
          showExpandable={false}
          loading={loading}
          stickyColumnIndex='8'
          showUpdatedEmptyData={showUpdatedEmptyData}
          pageNumber={page + 1}
        />
        <RecommendationJobDetails jobName={'k8s_version_upgrade'} />
      </BoxLayout2>
    </>
  );
};

KubernetesHelmUpgradeRecommendation.propTypes = {
  heading: PropTypes.string,
  accountId: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesHelmUpgradeRecommendation;
