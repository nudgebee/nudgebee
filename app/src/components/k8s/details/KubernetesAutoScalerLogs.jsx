import apiKubernetes from '@api1/kubernetes';
import { useData } from '@context/DataContext';
import { useEffect, useMemo, useState } from 'react';
import KubernetesPodLogs from './KubernetesPodLogs';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Select } from '@ui/Select';
import { ListingLayout } from '@ui/ListingLayout';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import Datetime from '@shared/format/Datetime';
import CustomTable from '@shared/tables/CustomTable';
import Loader from '@shared/Loader';
import { json } from '@codemirror/lang-json';
import Text from '@shared/format/Text';
import apiUser from '@api1/user';
import { ds } from 'src/utils/colors';

const KubernetesAutoScalerLogs = ({ accountId, namespace, autoscalerType }) => {
  const { setPodLogRequest } = useData();

  const [podData, setPodData] = useState({});
  const [podOptions, setPodOptions] = useState([]);
  const [selectedPod, setSelectedPod] = useState('');
  const [loading, setLoading] = useState(true);
  const [podDetailsLoading, setPodDetailsLoading] = useState(false);
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
          border: `1px solid ${ds.gray[300]}`,
        }}
      />
    );
  };

  useEffect(() => {
    // autoscalerType resolves asynchronously from the cluster connection_status.
    // Gate on it too: without it none of the branches below match, so starting
    // the spinner here would leave an indefinite loading state.
    if (!accountId || !namespace || !autoscalerType) {
      setLoading(false);
      setPodOptions([]);
      setSelectedPod('');
      setPodData({});
      setGkeAutoscalerLogData([]);
      return;
    }
    // Clear the previous cluster/type's data before the new fetch so stale rows
    // don't flash while the request is in flight.
    setPodOptions([]);
    setSelectedPod('');
    setPodData({});
    setGkeAutoscalerLogData([]);
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
  }, [accountId, namespace, autoscalerType]);

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
    if (!selectedPod) {
      return;
    }
    // selectedPod can change again before this request resolves (fast dropdown
    // switching); isCancelled ensures only the latest request commits state, so
    // an in-flight response for a since-abandoned pod can't overwrite it.
    let isCancelled = false;
    setPodData({});
    setPodDetailsLoading(true);
    apiKubernetes
      .getPodDetails(selectedPod)
      .then((res) => {
        if (isCancelled) return;
        const pod = res?.data?.cloud_resourses?.[0];
        if (pod) {
          setPodData(pod);
          setPodLogRequest(accountId, {
            subject_name: pod.name,
            subject_namespace: namespace,
          });
        }
      })
      .finally(() => {
        if (!isCancelled) {
          setPodDetailsLoading(false);
        }
      });
    return () => {
      isCancelled = true;
    };
  }, [selectedPod, accountId, namespace]);

  const renderingLogs = () => {
    if (autoscalerType == 'karpenter' || autoscalerType == 'cluster-autoscaler') {
      return (
        <div>
          <Select options={podOptions} label='Select Pod' value={selectedPod} onChange={(v) => setSelectedPod(v)} loading={loading} />
          {podData && Object.keys(podData).length > 0 ? (
            <KubernetesPodLogs podData={podData} />
          ) : loading || podDetailsLoading ? (
            <Box sx={{ position: 'relative', minHeight: ds.space.mul(0, 100) }}>
              <Loader style={{ width: '100%' }} />
            </Box>
          ) : null}
        </div>
      );
    } else if (autoscalerType == 'gke') {
      return (
        <ListingLayout>
          <ListingLayout.Body>
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
          </ListingLayout.Body>
        </ListingLayout>
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
