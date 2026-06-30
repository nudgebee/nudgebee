// UpdateDataContext.js
import { useData } from '@context/DataContext';
import homeApi from '@api1/home';

export const transformClusters = (response) => {
  return response.map((item) => ({
    label: item.account_name,
    value: item.id,
    status: item.status || '',
    last_connected_at: item.agents?.[0]?.last_connected_at || '',
    agent: item.agents?.[0] || {},
    cloud_account_attrs: item?.cloud_account_attrs || {},
    cloud_provider: item.cloud_provider,
    account_access: item.account_access || '',
    k8s_provider: item.agents?.[0]?.k8s_provider || '',
    k8s_version: item.agents?.[0]?.k8s_version || '',
    created_at: item.created_at,
  }));
};

export const useUpdateAllClusterOption = () => {
  const { setAllCluster } = useData();

  const updateClusters = (refresh = false) => {
    homeApi.getCloudAccounts('', refresh).then((res) => {
      const clusters = transformClusters(res);
      setAllCluster(clusters);
    });
  };

  return updateClusters;
};
