import React, { useEffect, useState } from 'react';
import ClusterPotentialSaving from './ClusterPotentialSaving';
import apiKubernetes from '@api1/kubernetes';
import PropTypes from 'prop-types';

const KubernetesSavings = ({ accountId }) => {
  const [savingPotentialSummary, setSavingPotentialSummary] = useState({});
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (accountId) {
      setLoading(true);
      getClusterYearySaving(accountId);
    }
  }, [accountId]);

  const getClusterYearySaving = async (accountId) => {
    try {
      const response = await apiKubernetes.listk8ClustersYearlySaving(accountId);
      const data = response?.data ?? {};
      setSavingPotentialSummary(data);
    } finally {
      setLoading(false);
    }
  };

  return <ClusterPotentialSaving savingPotentialSummary={savingPotentialSummary} loading={loading} />;
};

KubernetesSavings.propTypes = {
  accountId: PropTypes.string,
};

export default KubernetesSavings;
