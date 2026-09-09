import React, { useEffect, useState, useRef, useCallback } from 'react';
import k8sApi from '@api1/kubernetes';
import MarkDowns from '@shared/viewers/MarkDowns';
import DownloadButton from '@shared/buttons/DownloadButton';
import Loader from '@shared/Loader';
import { Box, CircularProgress } from '@mui/material';
import ConsoleLogOutput from '@shared/viewers/ConsoleLogOutput';
import ListingLayout from '@ui/ListingLayout';
import AutoRefreshControls from '@shared/widgets/AutoRefreshControls';
import ScrollToTopBottom from '@shared/ScrollToTopBottom';
import { ds } from '@utils/colors';

interface KubernetesWorkloadRelayLogsProps {
  accountId: string;
  namespace: string;
  workloadName: string;
  workloadType: string;
}

// Fallback for the Applications/workload Logs tab when no log provider (Loki,
// ES, ...) is configured for the account. Fetches logs straight from the
// cluster via the relay agent's kubectl_command_executor action instead of
// logs_enricher — logs_enricher only resolves a single pod name, while this
// tab is workload-scoped (Deployment/StatefulSet/DaemonSet/Job/...), so we
// run `kubectl logs <kind>/<name> --all-containers` the same way the backend's
// event-rule log action does for workload kinds (see
// api-server/services/observability/eventrule_actions_logs.go fetchLogsViaKubectl).
const KubernetesWorkloadRelayLogs: React.FC<KubernetesWorkloadRelayLogsProps> = ({ accountId, namespace, workloadName, workloadType }) => {
  const [text, setText] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [loading, setLoading] = useState(false);
  const isFirstCallRef = useRef(true);

  const fetchLogs = useCallback(
    (interval?: number) => {
      if (interval === 0 || (interval && interval > 0 && isFirstCallRef.current)) {
        return;
      }
      isFirstCallRef.current = false;
      if (!accountId || !namespace || !workloadName || !workloadType) {
        setLoading(false);
        return;
      }
      setErrorMsg('');
      setLoading(true);
      const command = `kubectl logs ${workloadType.toLowerCase()}/${workloadName} -n ${namespace} --tail=1000 --all-containers=true`;
      const requestBody = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'kubectl_command_executor',
          action_params: { command },
          origin: 'Nudgebee UI',
        },
      };
      k8sApi
        .relayForwardRequest(requestBody)
        .then((res: any) => {
          if (res?.data?.success) {
            let stdout = '';
            let stderr = '';
            const findings = res?.data?.findings || [];
            for (const element of findings) {
              for (const evi of element?.evidence || []) {
                if (!evi?.data) {
                  continue;
                }
                // kubectl_command_executor publishes its output as a JsonBlock:
                // evidence.data -> [{type:'json', data: '{"command":...,"stdout":...,"stderr":...}'}]
                try {
                  const blocks = JSON.parse(evi.data);
                  for (const block of blocks) {
                    if (block?.type === 'json' && block?.data) {
                      try {
                        const inner = JSON.parse(block.data);
                        stdout = inner?.stdout || stdout;
                        stderr = inner?.stderr || stderr;
                      } catch (err) {
                        console.error('Failed to parse kubectl block data', err);
                      }
                    }
                  }
                } catch (err) {
                  console.error('Failed to parse relay evidence data', err);
                }
              }
            }
            if (stdout) {
              setText(stdout);
            } else if (stderr) {
              setErrorMsg(stderr);
            } else {
              setText('No logs found for this workload.');
            }
          } else {
            setErrorMsg('Failed to fetch Logs');
          }
        })
        .catch(() => {
          setErrorMsg('Failed to fetch the Logs');
        })
        .finally(() => {
          setLoading(false);
        });
    },
    [accountId, namespace, workloadName, workloadType]
  );

  useEffect(() => {
    setText('');
    setErrorMsg('');
    isFirstCallRef.current = true;
    fetchLogs();
  }, [fetchLogs]);

  const renderingObject = () => {
    if (!errorMsg && !text && loading) {
      return <Loader style={{ paddingTop: 'var(--ds-space-6)', width: '100%' }} />;
    } else if (errorMsg) {
      return <MarkDowns data={errorMsg} sx={{ width: '100%', maxHeight: ds.space.mul(0, 300) }} allowExecutable={false} onLinkClick={null} />;
    }
    return (
      <>
        <ConsoleLogOutput data={text} />
        {loading && (
          <Box display='flex' justifyContent='center' alignItems='center' padding='var(--ds-space-2)'>
            <CircularProgress size={20} />
          </Box>
        )}
      </>
    );
  };

  return (
    <Box marginTop={ds.space[2]}>
      <ListingLayout id='workload-relay-logs'>
        <ListingLayout.Toolbar
          actions={
            <>
              <AutoRefreshControls callBack={fetchLogs} />
              <DownloadButton onClick={() => ({ fileName: `${namespace}_${workloadName}`, data: text })} id='workload-relay-logs' />
            </>
          }
        />
        <ListingLayout.Body>{renderingObject()}</ListingLayout.Body>
      </ListingLayout>
      <ScrollToTopBottom />
    </Box>
  );
};

export default KubernetesWorkloadRelayLogs;
