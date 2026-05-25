import React, { useEffect, useState, useRef, useCallback } from 'react';
import k8sApi from '@api1/kubernetes';
import * as zlib from 'zlib';
import MarkDowns from '@components1/common/MarkDowns';
import DownloadButton from '@components1/common/DownloadButton';
import Loader from '@components1/common/Loader';
import { useData } from '@context/DataContext';
import { Box, Checkbox, CircularProgress, FormControlLabel } from '@mui/material';
import ConsoleLogOutput from '@components1/common/ConsoleLogOutput';
import BoxLayout2 from '@components1/common/BoxLayout2';
import AutoRefreshControls from '@components1/common/AutoRefreshControls';
import ScrollToTopBottom from '@components1/common/ScrollToTopBottom';

interface KubernetesPodLogsProps {
  podData: any;
}

const KubernetesPodLogs: React.FC<KubernetesPodLogsProps> = ({ podData }) => {
  const [text, setText] = useState('');
  const [fileName, setFileName] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [loading, setLoading] = useState(false);
  const [containerOptions, setContainerOptions] = useState<string[]>([]);
  const [selectedContainer, setSelectedContainer] = useState<string>('');
  const [getPrevious, setGetPrevious] = useState(false);

  const isFirstCallRef = useRef(true);
  const { podLog } = useData();
  const { accountId, query } = podLog;

  const fetchLogs = useCallback(
    (interval?: number) => {
      if (interval === 0 || (interval && interval > 0 && isFirstCallRef.current)) {
        return;
      }
      isFirstCallRef.current = false;
      if (accountId && Object.keys(query).length > 0) {
        setText((prevText) => prevText.replace('No newer logs at this moment', ''));
        setLoading(true);
        const requestBody = {
          no_sinks: true,
          body: {
            account_id: accountId,
            action_name: 'logs_enricher',
            action_params: {
              name: query.subject_name,
              namespace: query.subject_namespace,
              previous: getPrevious,
              since_time: isFirstCallRef.current ? undefined : interval,
              container_name: selectedContainer,
            },
            origin: 'Nudgebee UI',
          },
        };
        k8sApi
          .relayForwardRequest(requestBody)
          .then((res) => {
            if (res?.data?.success) {
              const sampleFileName = query.subject_namespace + '_' + query.subject_name + '_' + Date.now();
              const findings = res?.data.findings;
              if (findings && findings.length > 0) {
                for (const element of findings) {
                  if (element?.evidence.length > 0) {
                    for (const evi of element.evidence) {
                      if (evi?.data) {
                        const parsedData = JSON.parse(evi?.data);
                        for (const d of parsedData) {
                          if (d.type === 'gz') {
                            setFileName(d?.filename || sampleFileName);
                            const gzippedDataBuffer = Buffer.from(d.data.slice(2, -1), 'base64');
                            const decompressedData = zlib.unzipSync(gzippedDataBuffer).toString('utf8');
                            setText((prevText) => prevText + decompressedData);
                            break;
                          }
                        }
                      }
                    }
                  }
                }
              } else {
                setText((prevText) => {
                  if (!prevText.includes('No newer logs at this moment')) {
                    return prevText.concat('No newer logs at this moment');
                  }
                  return prevText;
                });
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
      } else {
        setLoading(false);
      }
    },
    [accountId, query, selectedContainer, getPrevious]
  );

  useEffect(() => {
    if (podData && Object.keys(podData).length > 0) {
      const hasContainers = podData?.meta?.config?.containers?.length > 0;
      if (hasContainers) {
        const containers = podData?.meta?.config?.containers.map((g: any) => g.name);
        setContainerOptions(containers);
      }
    }
  }, [podData]);

  useEffect(() => {
    setText('');
    setErrorMsg('');
    isFirstCallRef.current = true;
  }, [podData?.id]);

  useEffect(() => {
    fetchLogs();
  }, [podLog, selectedContainer, getPrevious, fetchLogs, podData]);

  const renderingObject = () => {
    if (!errorMsg && !text && loading) {
      return <Loader style={{ paddingTop: '40px', width: '100%' }} />;
    } else if (errorMsg) {
      return <MarkDowns data={errorMsg} sx={{ width: '100%', maxHeight: '600px' }} allowExecutable={false} onLinkClick={null} />;
    }
    return (
      <>
        <ConsoleLogOutput data={text} />
        {loading && (
          <Box display='flex' justifyContent='center' alignItems='center' padding='10px'>
            <CircularProgress size={20} />
          </Box>
        )}
      </>
    );
  };

  return (
    <>
      <BoxLayout2
        id={'pod-logs'}
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: containerOptions,
            onSelect: (e: any) => {
              setSelectedContainer(e?.target?.value);
              if (selectedContainer != e?.target?.value) {
                setText('');
                isFirstCallRef.current = true;
              }
            },
            minWidth: '150px',
            label: 'Container',
            value: selectedContainer,
          },
          {
            type: 'custom',
            enabled: true,
            component: (
              <FormControlLabel
                control={<Checkbox value={getPrevious} onChange={(event) => setGetPrevious(event.target.checked)} />}
                label='Get Previous Logs'
              />
            ),
          },
        ]}
        sharingOptions={{
          download: {
            enabled: false,
            onClick: () => {
              return {
                tableId: '',
              };
            },
          },
          sharing: {
            enabled: false,
            onClick: null,
          },
        }}
        extraOptions={[
          <AutoRefreshControls key={'autoRefresh'} callBack={fetchLogs} />,
          <DownloadButton
            onClick={() => {
              return { fileName: fileName, data: text };
            }}
            key={'1'}
            sx={{
              marginTop: '7px',
              borderRadius: '4px',
              border: '0.5px solid var(--grey-60, #B9B9B9)',
              background: '#FFF',
              userSelect: 'none',
              cursor: 'pointer',
            }}
            id={'pod-logs'}
          />,
        ]}
      >
        {renderingObject()}
      </BoxLayout2>
      <ScrollToTopBottom />
    </>
  );
};

export default KubernetesPodLogs;
