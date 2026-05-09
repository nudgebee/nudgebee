import { Box, List, ListItem, ListItemText } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi from '@api1/recommendation';
import BoxLayout2 from '@common/BoxLayout2';
import PropTypes from 'prop-types';
import apiUser from '@api1/user';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import CustomTable from '@components1/common/tables/CustomTable2';

const RECOMMENDATION_COMPATIBLE_HEADER1 = [
  { width: '10%', name: 'Current Version' },
  { width: '10%', name: 'Min. K8s Version' },
  { width: '80%', name: 'Message' },
];

const KubernetesKubeVersionRecommendation = ({ accountId, showUpdatedEmptyData = true }) => {
  const [kubernetesKubeVersionRecommendation, setKubernetesKubeVersionRecommendation] = useState([]);
  const [kubernetesKubeVersionRecommendationCount, setKubernetesKubeVersionRecommendationCount] = useState(10);
  const [totalKubernetesKubeVersionRecommendationCount, setTotalKubernetesKubeVersionRecommendationCount] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const kubernetesKubeVersionTable = 'kubernetesKubeVersionTable';

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }

    listKubeVersionRecommendation();
  }, [accountId, page, rowsPerPage]);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    recommendationApi
      .getK8sRecommendation({
        accountId: accountId,
        ruleName: 'kube_proxy_version',
        category: 'InfraUpgrade',
        limit: 1,
        offset: 0,
      })
      .then((res) => {
        setTotalKubernetesKubeVersionRecommendationCount(res?.data?.recommendation_aggregate?.aggregate?.count ?? 0);
      })
      .catch((error) => {
        console.error('Error fetching total count:', error);
      });
  }, [accountId]);

  const listKubeVersionRecommendation = () => {
    setLoading(true);
    setKubernetesKubeVersionRecommendation([]);
    setKubernetesKubeVersionRecommendationCount(0);
    let recommendation = null;
    recommendationApi
      .getK8sRecommendation({
        accountId: accountId,
        ruleName: 'kube_proxy_version',
        category: 'InfraUpgrade',
        status: ['Open'],
        recommendation: recommendation,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        const k8sRecommendationData = res?.data?.recommendation || [];
        if (k8sRecommendationData.length > 0) {
          const tableData = k8sRecommendationData.map((f) => {
            const messages = f.recommendation?.message?.split(').') || [];
            return [
              {
                text: f.recommendation?.['kube-proxy'] || '-',
              },

              {
                text: f.recommendation?.target_k8s_version || '-',
              },
              {
                component:
                  messages.length > 0 ? (
                    <List>
                      {messages
                        .filter((g) => g != '')
                        .map((item) => (
                          <ListItem key={item.length} sx={{ display: 'list-item', pl: 2 }}>
                            <ListItemText sx={{ '&.MuiTypography-root': { fontSize: '11px' } }} primary={`• ${item.trim()}`} />
                          </ListItem>
                        ))}
                    </List>
                  ) : (
                    '-'
                  ),
              },
            ];
          });
          setKubernetesKubeVersionRecommendation(tableData);
        }
        setKubernetesKubeVersionRecommendationCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={totalKubernetesKubeVersionRecommendationCount} />
      </Box>
      <BoxLayout2
        id='kube-version'
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: kubernetesKubeVersionTable,
              };
            },
          },
          sharing: { enabled: true },
        }}
      >
        <CustomTable
          id={kubernetesKubeVersionTable}
          headers={RECOMMENDATION_COMPATIBLE_HEADER1}
          tableData={kubernetesKubeVersionRecommendation}
          rowsPerPage={rowsPerPage}
          totalRows={kubernetesKubeVersionRecommendationCount}
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

KubernetesKubeVersionRecommendation.propTypes = {
  accountId: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesKubeVersionRecommendation;
