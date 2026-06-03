import apiKubernetes from '@api1/kubernetes';
import { useData } from '@context/DataContext';
import { useEffect, useMemo, useState } from 'react';
import KubernetesPodLogs from './KubernetesPodLogs';
import PropTypes from 'prop-types';
import CustomDropdown from '@components1/common/CustomDropdown';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import Datetime from '@components1/common/format/Datetime';
import CustomTable from '@common-new/tables/CustomTable2';
import { json } from '@codemirror/lang-json';
import { Text } from '@components1/common';
import apiUser from '@api1/user';

const KubernetesAutoScalerLogs = ({ accountId, namespace, autoscalerType }) => {
  const { setPodLogRequest } = useData();

  const [podData, setPodData] = useState({});
  const [podOptions, setPodOptions] = useState([]);
  const [selectedPod, setSelectedPod] = useState('');
  const [loading, setLoading] = useState(true);
  const [gkeAutoscalerLogData, setGkeAutoscalerLogData] = useState([]);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState(0);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const SummaryDetails = function (accountId, drilldownQuery, _row) {
    return (
      <CodeMirror
        value={JSON.stringify(drilldownQuery, null, 4)}
        height='300px'
        extensions={[json(), EditorView.lineWrapping]}
        editable={false}
        style={{
          border: '1px solid silver',
        }}
      />
    );
  };

  useEffect(() => {
    if (!accountId || !namespace) {
      return;
    }
    setLoading(true);
    if (autoscalerType == 'cluster-autoscaler' || autoscalerType == 'karpenter') {
      apiKubernetes
        .getK8sPods(
          10,
          0,
          {
            accountId: accountId,
            namespaceName: namespace,
            isActive: true,
            labels:
              autoscalerType == 'cluster-autoscaler' ? ['{"app": "cluster-autoscaler"}', '{"app.kubernetes.io/instance": "clusterscaler"}'] : '',
          },
          false
        )
        .then((res) => {
          const pods = res?.data?.k8s_pods || [];
          if (pods && pods.length > 0) {
            setPodOptions(pods.map((p) => ({ label: p.name, value: p.id })));
            setSelectedPod(pods[0].id);
            setPodLogRequest(accountId, {
              subject_name: pods[0].name,
              subject_namespace: namespace,
            });
          }
        })
        .finally(() => {
          setLoading(false);
        });
    } else if (autoscalerType == 'gke') {
      apiKubernetes
        .relayForwardRequest({
          no_sinks: true,
          body: {
            account_id: accountId,
            action_name: 'gke_logs',
            action_params: {
              project_id: namespace.split('|')[0],
              zone: namespace.split('|')[1],
              limit: 1000, // Issue From relay server not getting data according to limit so frontend side pagination is implemented
            },
          },
          cache: false,
        })
        .then((res) => {
          const logData = res?.data?.data || [];
          setCurrentPage(0);
          setGkeAutoscalerLogData(logData);
        })
        .finally(() => {
          setLoading(false);
        });
    }
  }, [accountId, namespace]);

  const pageData = useMemo(() => {
    return gkeAutoscalerLogData.slice(currentPage * recordsPerPage, currentPage * recordsPerPage + recordsPerPage).map((f) => [
      {
        component: <Datetime value={f.timestamp} />,
        drilldownQuery: f,
      },
      {
        text: <Text showAutoEllipsis value={JSON.stringify(f)} />,
      },
    ]);
  }, [recordsPerPage, currentPage, gkeAutoscalerLogData]);

  useEffect(() => {
    if (selectedPod) {
      apiKubernetes.getPodDetails(selectedPod).then((res) => {
        const pod = res.data.cloud_resourses[0];
        if (pod) {
          setPodData(pod);
          setPodLogRequest(accountId, {
            subject_name: pod.name,
            subject_namespace: namespace,
          });
        }
      });
    }
  }, [selectedPod]);

  const renderingLogs = () => {
    if (autoscalerType == 'karpenter' || autoscalerType == 'cluster-autoscaler') {
      return (
        <div>
          <CustomDropdown
            options={podOptions}
            label='Select Pod'
            value={selectedPod}
            onChange={(e) => setSelectedPod(e.target.value)}
            loading={loading}
          />
          {podData && Object.keys(podData).length > 0 ? <KubernetesPodLogs podData={podData} /> : null}
        </div>
      );
    } else if (autoscalerType == 'gke') {
      return (
        <CustomTable
          loading={loading}
          tableData={pageData}
          headers={[
            { name: 'Created At', width: '10%' },
            { name: 'Summary', width: '80%' },
          ]}
          onPageChange={onPageChange}
          rowsPerPage={recordsPerPage}
          totalRows={gkeAutoscalerLogData.length}
          pageNumber={currentPage + 1}
          expandable={{
            tabs: [
              {
                componentFn: SummaryDetails,
                text: 'Details',
              },
            ],
          }}
        />
      );
    }
  };

  return renderingLogs();
};

KubernetesAutoScalerLogs.propTypes = {
  accountId: PropTypes.string,
  namespace: PropTypes.string,
  autoscalerType: PropTypes.string,
};

export default KubernetesAutoScalerLogs;
