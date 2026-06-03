import React, { useState, useEffect } from 'react';
import { Box } from '@mui/material';
import TrendArrowPercentage from '@components1/common/widgets/TrendArrowPercentage';
import Text from '@common-new/format/Text';
import ThreeDotLoader from '@components1/common/ThreeDotLoader';
import apiKubernetes from '@api1/kubernetes';
import { ds } from '@utils/colors';

const KubernetesIssuesOverView = ({ accountId, occurence = ['last 24 hours'] }) => {
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState([
    { name: 'Pod Issues', current_count: '', old_count: '' },
    { name: 'Node Issues', current_count: '', old_count: '' },
  ]);

  const getClusterEvents = async () => {
    try {
      setLoading(true);
      const response = await apiKubernetes.listk8ClusterEventsData(accountId);
      setData([
        { name: 'Pod Issues', current_count: response.pod_issue_count, old_count: response.old_pod_issue_count },
        { name: 'Node Issues', current_count: response.node_issue_count, old_count: response.old_node_issue_count },
      ]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (accountId) {
      getClusterEvents();
    }
  }, [accountId]);

  return (
    <Box
      sx={{
        minHeight: ds.space.mul(0, 55),
        boxSizing: 'border-box',
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        borderRadius: 'var(--ds-radius-lg)',
        padding: 'var(--ds-space-4) var(--ds-space-4)',
        background: 'var(--ds-pink-100)',
        border: '1px solid var(--ds-red-200)',
        boxShadow: 'none',
        position: 'relative',
        gap: 'var(--ds-space-4)',
      }}
    >
      {data?.map((entry, index) => (
        <Box key={index}>
          <Text value={entry.name} sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
          <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap' }}>
            {loading ? (
              <Box sx={{ ml: 'var(--ds-space-6)', mt: 'var(--ds-space-2)' }}>
                <ThreeDotLoader />
              </Box>
            ) : (
              <>
                <Text value={entry.current_count || '-'} sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)' }} />
                {entry?.current_count > 0 ? (
                  <>
                    <TrendArrowPercentage
                      width='auto'
                      sign={entry?.old_count > entry?.current_count ? 1 : -1}
                      value={(Math.abs(entry?.old_count - entry?.current_count) * 100) / entry?.old_count}
                    />
                    <Text value={occurence[0]} secondaryText sx={{ color: 'var(--ds-brand-300)', width: 'max-content' }} />
                  </>
                ) : (
                  <Box sx={{ width: ds.space[3] }} />
                )}
              </>
            )}
          </Box>
        </Box>
      ))}
    </Box>
  );
};

export default KubernetesIssuesOverView;
