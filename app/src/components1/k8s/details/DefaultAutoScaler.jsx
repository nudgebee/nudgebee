import apiKubernetes from '@api1/kubernetes';
import PropTypes from 'prop-types';
import React, { useEffect, useState } from 'react';
import KubernetesPodYaml from './KubernetesPodYaml';
import ShimmerLoading from '@components1/common/ShimmerLoading';

const DefaultAutoScaler = ({ accountId, namespace }) => {
  const [loading, setLoading] = useState(false);
  const [workload, setWorkload] = useState({});

  useEffect(() => {
    if (!accountId || !namespace) {
      return;
    }
    setLoading(true);
    apiKubernetes
      .getK8sWorkload(
        1,
        0,
        {
          accountId: accountId,
          namespaceName: namespace,
          isActive: true,
          labels: ['{"app": "cluster-autoscaler"}', '{"app.kubernetes.io/instance": "clusterscaler"}'],
        },
        {},
        false
      )
      .then((resp) => {
        const workloads = resp?.data?.k8s_workloads || [];
        if (workloads && workloads.length > 0) {
          setWorkload(workloads[0]);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId, namespace]);

  const renderingObject = () => {
    if (loading) {
      return <ShimmerLoading />;
    } else if (workload.name && workload.namespace && workload.kind) {
      return (
        <KubernetesPodYaml
          accountId={accountId}
          query={{
            workload_name: workload.name,
            namespace_name: workload.namespace,
            kind: workload.kind,
          }}
          showEditButton={true}
        />
      );
    }
    return <div>Not found</div>;
  };

  return renderingObject();
};

DefaultAutoScaler.propTypes = {
  namespace: PropTypes.string,
  accountId: PropTypes.string,
};

export default DefaultAutoScaler;
