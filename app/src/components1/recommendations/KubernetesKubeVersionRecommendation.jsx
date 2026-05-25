import { Box, List, ListItem, ListItemText } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import { ds } from 'src/utils/colors';
import PropTypes from 'prop-types';
import apiUser from '@api1/user';
import CustomTable from '@common-new/tables/CustomTable2';

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
      <Box
        sx={{
          display: 'flex',
          flex: 1,
          flexDirection: 'row',
          gap: ds.space[3],
          '& > *': { maxWidth: `calc((100% - 3 * ${ds.space[3]}) / 4)` },
        }}
        mt={2}
        mb={2}
      >
        <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
          <Stat
            size='md'
            label='Total Recommendations'
            info={{ tooltip: 'Kube-proxy version mismatches with the target Kubernetes version' }}
            value={
              Number.isFinite(totalKubernetesKubeVersionRecommendationCount)
                ? totalKubernetesKubeVersionRecommendationCount.toLocaleString()
                : totalKubernetesKubeVersionRecommendationCount ?? '—'
            }
          />
        </WidgetCard>
      </Box>
      <ListingLayout id='kube-version'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: kubernetesKubeVersionTable })} />} />
        <ListingLayout.Body>
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

KubernetesKubeVersionRecommendation.propTypes = {
  accountId: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesKubeVersionRecommendation;
