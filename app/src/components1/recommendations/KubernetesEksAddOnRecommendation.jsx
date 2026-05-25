import { Box } from '@mui/material';
import { useEffect, useState } from 'react';
import recommendationApi from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import PropTypes from 'prop-types';
import apiUser from '@api1/user';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import CustomTable from '@common-new/tables/CustomTable2';

const RECOMMENDATION_COMPATIBLE_HEADER1 = ['Add-on Name', 'Current Add-on Version', 'Message', 'Supported Versions', 'Min. K8s Version'];

const KubernetesEksAddOnRecommendation = ({ accountId, showUpdatedEmptyData = true }) => {
  const [kubernetesEksAddOnRecommendation, setKubernetesEksAddOnRecommendation] = useState([]);
  const [kubernetesEksAddOnRecommendationCount, setKubernetesEksAddOnRecommendationCount] = useState(10);
  const [totalKubernetesEksAddOnRecommendationCount, setTotalKubernetesEksAddOnRecommendationCount] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const kubernetesEksAddOnTable = 'kubernetesEksAddOnTable';

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }

    listEksAddOnRecommendation();
  }, [accountId, page, rowsPerPage]);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    recommendationApi
      .getK8sRecommendation({
        accountId: accountId,
        ruleName: 'eks_add_ons_version',
        category: 'InfraUpgrade',
        limit: 1,
        offset: 0,
      })
      .then((res) => {
        setTotalKubernetesEksAddOnRecommendationCount(res?.data?.recommendation_aggregate?.aggregate?.count ?? 0);
      })
      .catch((error) => {
        console.error('Error fetching total count:', error);
      });
  }, [accountId]);

  const listEksAddOnRecommendation = () => {
    setLoading(true);
    setKubernetesEksAddOnRecommendation([]);
    setKubernetesEksAddOnRecommendationCount(0);
    let recommendation = null;
    recommendationApi
      .getK8sRecommendation({
        accountId: accountId,
        ruleName: 'eks_add_ons_version',
        category: 'InfraUpgrade',
        status: ['Open'],
        recommendation: recommendation,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        const data = res?.data?.recommendation || [];
        if (data.length > 0) {
          const tableData = data.map((item) => {
            return [
              {
                text: item.recommendation?.add_on_type || '-',
              },
              {
                text: item.recommendation?.version || '-',
              },
              {
                text: item.recommendation?.message || '-',
              },
              {
                text: item.recommendation?.supported_versions.join(', ') || '-',
              },
              {
                text: item.recommendation?.target_k8s_version || '-',
              },
            ];
          });
          setKubernetesEksAddOnRecommendation(tableData);
        }
        setKubernetesEksAddOnRecommendationCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      <Box sx={{ display: 'flex', gap: '12px' }} mt={2} mb={2}>
        <SummaryWidget title='Total Recommendations' value={totalKubernetesEksAddOnRecommendationCount} />
      </Box>
      <ListingLayout id='eks-addon'>
        <ListingLayout.Toolbar
          actions={
            <>
              <DownloadButton id='eks-addon-download' onClick={() => ({ tableId: kubernetesEksAddOnTable })} />
            </>
          }
        />
        <ListingLayout.Body>
          <CustomTable
            id={kubernetesEksAddOnTable}
            headers={RECOMMENDATION_COMPATIBLE_HEADER1}
            tableData={kubernetesEksAddOnRecommendation}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesEksAddOnRecommendationCount}
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

KubernetesEksAddOnRecommendation.propTypes = {
  accountId: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
};

export default KubernetesEksAddOnRecommendation;
