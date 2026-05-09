import { Box, Stack, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import PropTypes from 'prop-types';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import CustomTable from '@components1/common/tables/CustomTable2';

const RECOMMENDATION_COMPATIBLE_HEADER1 = [
  'Chart Name',
  'Release Name',
  'Namespace',
  'Compatible',
  'Severity',
  { name: 'Min. Kube Version', width: '40%' },
];

const KubernetesHelmCompatibleRecommendation = ({ accountId, heading = '', showUpdatedEmptyData = true }) => {
  const [kubernetesHelmUpgradeRecommendation, setKubernetesHelmUpgradeRecommendation] = useState([]);
  const [kubernetesHelmUpgradeRecommendationCount, setKubernetesHelmUpgradeRecommendationCount] = useState(10);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const kubernetesHelmUpgradeTable = 'kubernetesHelmCompatibleTable';

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }

    listHelmUpgradeRecommendation();
  }, [accountId, page, rowsPerPage]);

  function extractNameAndVersion(input) {
    const regex = /(.+?)-v?(\d+\.\d+\.\d+(?:\+\S+)?)$/;
    const match = input.match(regex);
    if (match) {
      return {
        name: match[1],
        version: match[2],
      };
    }
    return null;
  }

  const listHelmUpgradeRecommendation = () => {
    setLoading(true);
    setKubernetesHelmUpgradeRecommendation([]);
    setKubernetesHelmUpgradeRecommendationCount(0);
    let recommendation = null;
    recommendationApi
      .getK8sRecommendation({
        accountId: accountId,
        ruleName: 'k8s_helm_compatibility',
        category: 'InfraUpgrade',
        status: ['Open'],
        recommendation: recommendation,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        let k8sRecommendationData = res?.data?.recommendation.map((item) => {
          let data = [];
          const chartNameAndVersion = extractNameAndVersion(item.recommendation.chart);
          data.push({
            component: ClusterNameWithRegion({
              name: chartNameAndVersion?.name || `${item.recommendation.chart}`,
              hideIcon: true,
              namespaceFont: '12px',
              region: chartNameAndVersion?.version ? (
                <Stack>
                  <Typography sx={{ fontSize: '12px' }}>Version -{chartNameAndVersion.version}</Typography>
                </Stack>
              ) : null,
            }),
          });
          data.push({ component: <Text value={item.recommendation.release} /> });
          data.push({ component: <Text value={item.recommendation.namespace} /> });
          data.push({ component: <CustomLabels text={item.recommendation.status} /> });
          data.push({ component: <CustomLabels text={item.severity} /> });
          data.push({ component: <Text value={item.recommendation.kubeVersion} /> });
          return data;
        });
        setKubernetesHelmUpgradeRecommendation(k8sRecommendationData);
        setKubernetesHelmUpgradeRecommendationCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={kubernetesHelmUpgradeRecommendationCount} />
      </Box>
      <BoxLayout2
        heading={heading}
        id='helm-compatible-recommendation'
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
      >
        <CustomTable
          id={kubernetesHelmUpgradeTable}
          headers={RECOMMENDATION_COMPATIBLE_HEADER1}
          tableData={kubernetesHelmUpgradeRecommendation}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesHelmUpgradeRecommendationCount}
          onPageChange={changePage}
          showExpandable={false}
          loading={loading}
          showUpdatedEmptyData={showUpdatedEmptyData}
          pageNumber={page + 1}
        />
      </BoxLayout2>
    </>
  );
};

KubernetesHelmCompatibleRecommendation.propTypes = {
  heading: PropTypes.string,
  accountId: PropTypes.string,
  helmCompatibilityCheck: PropTypes.bool,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesHelmCompatibleRecommendation;
