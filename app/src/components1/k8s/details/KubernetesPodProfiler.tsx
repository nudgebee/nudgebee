import React, { useEffect, useState, useRef } from 'react';
import { Box, Dialog, DialogActions, DialogContent, DialogTitle, TextField, Tooltip, Typography } from '@mui/material';
import Loader from '@components1/common/Loader';
import BoxLayout2 from '@components1/common/BoxLayout2';
import zlib from 'zlib';
import DownloadButton from '@components1/common/DownloadButton';
import { convertStringCase, generateRandomUUID, type CustomDropDownProps } from 'src/utils/common';
import CustomButton from '@components1/common/NewCustomButton';
import CustomIconButton from '@components1/CustomIconButton';
import apiKubernetes1 from '@api1/kubernetes1';
import SvgRenderer from '@components1/common/SvgRenderer';
import { StackedLineChartOutlined } from '@mui/icons-material';
import { KubernetesPodProfilerHistory } from '@components1/k8s/common/KubernetesTable2';
import ConversationPopup from '@components1/llm/ConversationPopup';
import SafeIcon from '@components1/common/SafeIcon';
import { DEFAULT_TITLE, getNubiIconUrl } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import { inputSx } from '@data/themes/inputField';

type ProfilerState = {
  svgData: Base64Data[];
  txtData: Base64Data[];
  jfrData: Base64Data[];
  pprofData: string;
  fileExt: string;
  isLoading: boolean;
  errorMsg: string;
};

interface KubernetesPodProfilerProps {
  accountId: string;
  query: Record<string, any>;
  // New prop for external findings data
  findings?: any[];
  // New prop to disable controls
  readOnlyMode?: boolean;
}

type Base64Data = {
  fileName: string;
  data: string;
};

type OutputOptions = {
  java: CustomDropDownProps[];
  python: CustomDropDownProps[];
  node: CustomDropDownProps[];
  go: CustomDropDownProps[];
  default: CustomDropDownProps[];
};

const KubernetesPodProfiler: React.FC<KubernetesPodProfilerProps> = ({ accountId, query, findings = [], readOnlyMode = false }) => {
  // Consolidated state
  const [profilerState, setProfilerState] = useState<ProfilerState>({
    svgData: [],
    txtData: [],
    jfrData: [],
    pprofData: '',
    fileExt: '',
    isLoading: false,
    errorMsg: '',
  });
  const [selectLang, setSelectLang] = useState(query.framework || 'go');
  const [outputTypeOptions, setOutputTypeOptions] = useState<CustomDropDownProps[]>([]);
  const [selectedOutputType, setSelectedOutputType] = useState('');
  const [profileTaskId, setProfileTaskId] = useState('');
  const [profileTaskStatus, setProfileTaskStatus] = useState('');
  const [profileDuration, setProfileDuration] = useState('15');
  const [showTrendChart, setShowTrendChart] = useState(false);
  const [analysisModalOpen, setAnalysisModalOpen] = useState(false);
  const [analysisQuery, setAnalysisQuery] = useState('');
  const [analysisType, setAnalysisType] = useState('');
  const [sessionId, setSessionId] = useState('');

  const pollingRef = useRef<any>({});

  // Pre-defined output type options for each language
  const OUTPUT_OPTIONS: OutputOptions = {
    java: [
      { label: 'Java Flight Recorder', value: 'jfr' },
      { label: 'Thread Dump', value: 'threaddump' },
      { label: 'Heap Histogram', value: 'heaphistogram' },
      { label: 'Heap Dump', value: 'heapdump' },
      { label: 'Flamegraph', value: 'flamegraph' },
    ],
    python: [
      { label: 'CPU', value: 'cpu' },
      { label: 'Memory', value: 'memory' },
    ],
    node: [
      { label: 'CPU', value: 'cpu' },
      { label: 'Memory', value: 'memory' },
    ],
    go: [
      { label: 'CPU', value: 'cpu' },
      { label: 'Memory', value: 'memory' },
    ],
    default: [{ label: 'Flamegraph', value: 'flamegraph' }],
  };

  const LANGUAGE_OPTIONS = [
    { label: 'Java', value: 'java' },
    { label: 'Python', value: 'python' },
    { label: 'Go', value: 'go' },
    { label: 'Ruby', value: 'ruby' },
    { label: 'Rust', value: 'rust' },
    { label: 'C++', value: 'c++' },
    { label: 'Node', value: 'node' },
  ];

  // Initialize with external findings data if provided
  useEffect(() => {
    if (readOnlyMode && findings.length > 0) {
      processProfilerData(findings);
    }
  }, [findings, readOnlyMode]);

  // Update output options when language changes (only if not in read-only mode)
  useEffect(() => {
    if (readOnlyMode) {
      return;
    }

    if (!selectLang) {
      setOutputTypeOptions([]);
      return;
    }

    setOutputTypeOptions(OUTPUT_OPTIONS[selectLang as keyof OutputOptions] || OUTPUT_OPTIONS.default);
    setSelectedOutputType('');
  }, [selectLang, readOnlyMode]);

  const fetchApplicationProfileConvert = async () => {
    // Allow API calls in read-only mode for pprof conversion
    setProfilerState((prevState) => ({
      ...prevState,
      isLoading: true,
    }));

    try {
      const response = await apiKubernetes1.applicationProfileConvert({
        accountId: accountId,
        base64_profile: profilerState.pprofData,
      });
      const svgProfile = response?.data?.data?.application_profile_convert?.data?.svg_profile || '';
      const gzData = base64Converter(svgProfile);
      const unzippedData = await unzipData(gzData);
      setProfilerState((prevState) => ({
        ...prevState,
        svgData: [
          {
            fileName: `${query.pod_name}.svg`,
            data: unzippedData,
          },
        ],
        isLoading: false,
      }));
    } catch (error) {
      console.error('Error converting pprof to SVG:', error);
      setProfilerState((prevState) => ({
        ...prevState,
        isLoading: false,
        errorMsg: 'Failed to convert pprof data to SVG',
      }));
    }
  };

  useEffect(() => {
    // Allow pprof conversion in both read-only and regular mode
    if (profilerState.pprofData?.length > 0) {
      fetchApplicationProfileConvert();
    }
  }, [profilerState.pprofData]);

  // Helper functions
  const getProfilerType = (): string | null => (['python', 'node', 'go'].includes(selectLang) ? selectedOutputType : '');

  const getOutputType = (): string => (['java'].includes(selectLang) ? selectedOutputType : 'flamegraph');

  const findPySpyCmdReplaceWithPid = (data: string): string => {
    const regex = /py-spy[^\n]*\.svg/g;
    const matches = data.match(regex);

    if (!matches?.length) {
      return data;
    }

    let _result = data;
    for (const match of matches) {
      const pidMatch = match.match(/--pid=(\d+)/);
      if (pidMatch?.[1]) {
        _result = _result.replace(match, 'Process Id: ' + pidMatch[1]);
      }
    }
    return _result;
  };

  const base64Converter = (data: string): Buffer => {
    const cleanData = data.replace(/^b'|'$/g, '');
    return Buffer.from(cleanData, 'base64');
  };

  const unzipData = async (gzData: Buffer): Promise<string> => {
    return new Promise((resolve, reject) => {
      zlib.unzip(gzData, (err, unzippedBuffer) => {
        if (err) {
          console.error('Error unzipping the file:', err);
          reject(err);
        } else {
          resolve(unzippedBuffer.toString('utf8').replace(/\n$/, ''));
        }
      });
    });
  };

  const processProfilerData = async (findings: any[]): Promise<void> => {
    const base64Datas: Base64Data[] = [];
    const txtData = [];
    const jfrData = [];
    let pprofData: any = '';
    let fileExtension = '';

    if (!findings?.length) {
      setProfilerState((prev) => ({
        ...prev,
        isLoading: false,
        errorMsg: 'No Data Found',
      }));
      return;
    }

    for (const finding of findings) {
      const evidences = finding.evidence;
      if (!evidences?.length) {
        continue;
      }

      for (const evidence of evidences) {
        try {
          const dataObjects = JSON.parse(evidence.data);

          if (!Array.isArray(dataObjects)) {
            continue;
          }

          // Process SVG data
          const svgItems = dataObjects.filter((item) => item.type === 'svg');
          for (const item of svgItems) {
            base64Datas.push({
              fileName: item.filename,
              data: findPySpyCmdReplaceWithPid(atob(item.data.replace(/^b'|'$/g, ''))),
            });
          }

          // Process SVG.GZ data
          const svgGzItems = dataObjects.filter(
            (item) => item.type === 'gz' && (item.filename.endsWith('svg.gz') || item.filename.endsWith('pprof.svg.gz'))
          );

          if (svgGzItems.length) {
            const gzData = base64Converter(svgGzItems[0].data);
            const unzippedData = await unzipData(gzData);
            base64Datas.push({
              fileName: `${query.pod_name}.svg`,
              data: unzippedData,
            });
          }

          // Process TXT.GZ data
          const txtGzItems = dataObjects.filter((item) => item.type === 'gz' && item.filename.endsWith('txt.gz'));
          if (txtGzItems.length) {
            fileExtension = 'txt';
            for (const item of txtGzItems) {
              const gzData = base64Converter(item.data);
              const unzipped = await unzipData(gzData);
              txtData.push({
                fileName: item.filename.replace('.gz', ''),
                data: unzipped,
              });
            }
          }

          // Process JFR.GZ data
          const jfrGzItems = dataObjects.filter((item) => item.type === 'gz' && item.filename.endsWith('jfr.gz'));
          if (jfrGzItems.length) {
            fileExtension = 'jfr';
            for (const item of jfrGzItems) {
              const gzData = base64Converter(item.data);
              const unzipped = await unzipData(gzData);
              jfrData.push({
                fileName: item.filename.replace('.gz', ''),
                data: unzipped,
              });
            }
          }

          // Process PPROF.GZ data
          const pprofGzItems = dataObjects.filter((item) => item.type === 'gz' && item.filename.endsWith('pprof.gz'));

          if (pprofGzItems.length) {
            fileExtension = 'pprof.gz';
            pprofData = pprofGzItems[0].data.replace(/^b'|'$/g, '');
          }
        } catch (error) {
          console.error('Error processing evidence data:', error);
        }
      }
    }

    setProfilerState({
      svgData: base64Datas,
      txtData: txtData,
      jfrData: jfrData,
      pprofData,
      fileExt: fileExtension,
      isLoading: false,
      errorMsg: '',
    });
  };

  const processFailure = (data: any) => {
    if (readOnlyMode) {
      return;
    } // Skip processing failures in read-only mode

    setProfilerState((prev) => ({
      ...prev,
      isLoading: false,
      errorMsg: data.error_message || 'Fail to generate profiler data',
    }));
    return;
  };

  const handleSubmit = async () => {
    if (readOnlyMode) {
      return;
    } // Disable submit in read-only mode

    if (!selectLang || !selectedOutputType) {
      return;
    }

    setProfilerState((prev) => ({
      ...prev,
      svgData: [],
      txtData: [],
      jfrData: [],
      pprofData: '',
      fileExt: '',
      isLoading: true,
      errorMsg: '',
    }));

    try {
      const data = {
        account_id: accountId,
        pod_name: query?.pod_name,
        namespace: query?.namespace_name,
        application_language: selectLang,
        profile_duration: parseInt(profileDuration),
        profile_type: getProfilerType(),
        output_type: getOutputType(),
      };
      const response = await apiKubernetes1.applicationProfile(data);
      const applicationProfile = response?.data?.data?.application_profile?.data || {};
      const errors = response?.data?.errors || [];
      if (Object.keys(applicationProfile).length == 0) {
        setProfilerState((prev) => ({
          ...prev,
          isLoading: false,
          errorMsg: errors.length > 0 ? errors[0].message : 'No Data Found',
        }));
      }
      setProfileTaskId(applicationProfile.profile_task_id);
      setProfileTaskStatus(applicationProfile.status);
    } catch (error) {
      console.error('Error submitting profiler request:', error);
      setProfilerState((prev) => ({
        ...prev,
        isLoading: false,
        errorMsg: 'Failed to fetch Data',
      }));
    }
  };

  useEffect(() => {
    if (readOnlyMode) {
      return;
    } // Skip polling in read-only mode

    const pollStatus = async () => {
      try {
        const response = await apiKubernetes1.applicationProfileStatus({
          account_id: accountId,
          profile_id: profileTaskId,
        });

        const getProfileStatus = response?.data?.data?.application_get_profile?.data || {};
        const newStatus = getProfileStatus.status;
        setProfileTaskStatus(newStatus);

        if (newStatus == 'COMPLETED') {
          processProfilerData(getProfileStatus.base64_profile?.data?.findings || []);
        } else if (newStatus == 'FAILED') {
          processFailure(getProfileStatus);
        } else if (newStatus !== 'COMPLETED') {
          pollingRef.current = setTimeout(pollStatus, 5000); // poll every 5 seconds
        }
      } catch (error) {
        console.error('Polling error:', error);
        pollingRef.current = setTimeout(pollStatus, 10000); // retry slower on error
      }
    };

    if (profileTaskId && profileTaskStatus == 'TODO') {
      pollStatus();
    }

    return () => clearTimeout(pollingRef.current); // cleanup on unmount
  }, [accountId, profileTaskId, readOnlyMode]);

  const resetData = () => {
    if (readOnlyMode) {
      return;
    } // Disable reset in read-only mode

    setProfilerState({
      svgData: [],
      txtData: [],
      jfrData: [],
      pprofData: '',
      fileExt: '',
      isLoading: false,
      errorMsg: '',
    });
  };

  const handleCloseConversationPopup = () => {
    setAnalysisQuery('');
    setSessionId('');
    setAnalysisModalOpen(false);
  };

  const handleGenerateAnalysis = (type: string, data: Base64Data) => {
    let queryPrompt = '';

    // Truncate the data to a reasonable size for LLM analysis
    const maxDataLength = 100000;
    const truncatedData = data.data?.length > maxDataLength ? data.data.substring(0, maxDataLength) + '... [truncated]' : data.data;

    switch (type) {
      case 'threaddump':
        queryPrompt = `@llm Analyse this thread dump on pod ${query?.pod_name} and namespace ${query?.namespace_name}:\n\n${truncatedData}`;
        break;
      case 'heapdump':
        queryPrompt = `@llm Analyse this heap dump on pod ${query?.pod_name} and namespace ${query?.namespace_name}:\n\n${truncatedData}`;
        break;
      default:
        queryPrompt = `@llm Analyse this profiler data on pod ${query?.pod_name} and namespace ${query?.namespace_name}:\n\n${truncatedData}`;
    }

    setAnalysisQuery(queryPrompt);
    setAnalysisType(type);
    setSessionId(generateRandomUUID(`${query.pod_name}-${type}`));
    setAnalysisModalOpen(true);
  };

  const renderContent = () => {
    const { svgData, txtData, jfrData, pprofData, fileExt, isLoading, errorMsg } = profilerState;

    if (isLoading) {
      return <Loader style={{ paddingTop: '10px', width: '100%' }} />;
    }

    if (errorMsg) {
      return <Typography style={{ paddingLeft: '5px', paddingTop: '10px' }}>{errorMsg}</Typography>;
    }

    // Show message if in read-only mode but no data
    if (readOnlyMode && svgData.length === 0 && txtData.length === 0 && jfrData.length === 0 && pprofData.length == 0) {
      return <Typography style={{ paddingLeft: '5px', paddingTop: '10px' }}>No profiler findings provided</Typography>;
    }

    return (
      <>
        {/* Text Data or Pprof Data Section */}
        {txtData.map((data, index) => {
          // Determine analysis type based on filename
          let analysisType = 'threaddump'; // default
          if (data.fileName.toLowerCase().includes('heap')) {
            analysisType = 'heapdump';
          } else if (data.fileName.toLowerCase().includes('thread')) {
            analysisType = 'threaddump';
          }
          return (
            <div key={index}>
              <Box display='flex' justifyContent='flex-end' mb={1} gap={1}>
                <CustomTooltip title={`Ask ${DEFAULT_TITLE} for analysis`}>
                  <CustomIconButton
                    onClick={(e) => {
                      e.stopPropagation();
                      handleGenerateAnalysis(analysisType, data);
                    }}
                    variant={'secondary'}
                    size={'xsmall'}
                    sx={{ height: '46px', mr: '4px', width: '46px' }}
                  >
                    <SafeIcon src={getNubiIconUrl()} width={24} height={24} alt={`Ask ${DEFAULT_TITLE}`} />
                  </CustomIconButton>
                </CustomTooltip>
                <DownloadButton
                  onClick={() => ({
                    fileName: data.fileName,
                    data: data.data,
                    type: fileExt,
                  })}
                  sx={{
                    padding: '5px 6px',
                    borderRadius: '4px',
                    border: '0.5px solid var(--grey-60, #B9B9B9)',
                    background: '#FFF',
                    userSelect: 'none',
                    cursor: 'pointer',
                    marginBottom: '10px',
                  }}
                  id={`pod-profiler-txt-${index}`}
                />
              </Box>
              <Typography>{`File: ${data.fileName}`}</Typography>
              <pre style={{ overflowX: 'auto' }}>{data.data}</pre>
            </div>
          );
        })}

        {/* JFR Data Section */}
        {jfrData.map((data, index) => (
          <>
            <Box display='flex' justifyContent='flex-end' mb={1}>
              <DownloadButton
                onClick={() => ({
                  fileName: data.fileName,
                  data: data.data,
                  type: fileExt,
                })}
                sx={{
                  padding: '5px 6px',
                  borderRadius: '4px',
                  border: '0.5px solid var(--grey-60, #B9B9B9)',
                  background: '#FFF',
                  userSelect: 'none',
                  cursor: 'pointer',
                  marginLeft: 'auto',
                  marginBottom: '10px',
                }}
                id={`pod-profiler-svg-${index}`}
              />
            </Box>
            <Typography>{`Download the JFR File ${data.fileName}`}</Typography>
          </>
        ))}

        {/* SVG Data Section */}
        {svgData.map((data, index) => (
          <div key={index}>
            <Box display='flex' justifyContent='flex-end' mb={1}>
              <DownloadButton
                onClick={() => ({
                  fileName: data.fileName,
                  data: data.data,
                  type: 'text/svg+xml', // Important to specify for SVG!
                })}
                sx={{
                  padding: '5px 6px',
                  borderRadius: '4px',
                  border: '0.5px solid var(--grey-60, #B9B9B9)',
                  background: '#FFF',
                  userSelect: 'none',
                  cursor: 'pointer',
                  marginBottom: '10px',
                }}
                id={`pod-profiler-svg-${index}`}
              />
            </Box>
            <SvgRenderer
              svg={data.data}
              style={{
                width: '100%',
                border: '1px solid #ccc',
                padding: '10px',
                overflow: 'auto',
              }}
            />
          </div>
        ))}
      </>
    );
  };

  return (
    <div style={{ paddingTop: '10px', paddingLeft: '10px' }}>
      <ConversationPopup
        open={analysisModalOpen}
        query={analysisQuery}
        sessionId={sessionId}
        accountId={accountId}
        handleClose={handleCloseConversationPopup}
        title={analysisType ? `${convertStringCase(analysisType)} Analysis` : 'Analysis'}
      />
      <BoxLayout2
        id={'pod-profiler'}
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: false,
            onClick: () => ({ tableId: '' }),
          },
        }}
        filterOptions={
          readOnlyMode
            ? []
            : [
                {
                  type: 'dropdown',
                  enabled: true,
                  options: LANGUAGE_OPTIONS,
                  onSelect: (event: any) => {
                    resetData();
                    setSelectLang(event.target.value);
                  },
                  minWidth: '140px',
                  label: 'Language',
                  value: selectLang,
                  isDisabled: profilerState.isLoading,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: outputTypeOptions,
                  onSelect: (event: any) => {
                    resetData();
                    setSelectedOutputType(event.target.value);
                  },
                  minWidth: '140px',
                  label: 'Profile Type',
                  value: selectedOutputType,
                  isDisabled: profilerState.isLoading,
                },
                {
                  type: 'custom',
                  enabled: true,
                  key: 'profile-duration',
                  component: (
                    <TextField
                      label='Profile Duration (seconds, max 600)'
                      value={profileDuration}
                      onChange={(e) => {
                        const input = e.target.value;
                        if (input === '') {
                          setProfileDuration('');
                          return;
                        }
                        const numericValue = input.replace(/^0+/, '') || '0';
                        const parsedValue = parseInt(numericValue);

                        if (!isNaN(parsedValue)) {
                          const clampedValue = Math.min(parsedValue, 600);
                          setProfileDuration(clampedValue.toString());
                        }
                      }}
                      type='number'
                      inputProps={{ max: 600 }}
                      helperText={parseInt(profileDuration) > 600 ? 'Value cannot exceed 600' : ''}
                      error={parseInt(profileDuration) > 600}
                      disabled={profilerState.isLoading}
                      size='small'
                      sx={{
                        ...inputSx,
                      }}
                    />
                  ),
                },
                {
                  type: 'custom',
                  key: 'submit-button',
                  enabled: true,
                  component: (
                    <CustomButton text={'Submit'} onClick={handleSubmit} disabled={profilerState.isLoading || !selectLang || !selectedOutputType} />
                  ),
                },
              ]
        }
        extraOptions={
          readOnlyMode
            ? []
            : [
                <Box key='profiler-extra-options' display='flex' gap='10px' alignItems='center'>
                  <CustomButton
                    text={
                      <Tooltip title='Pod Profile History'>
                        <StackedLineChartOutlined />
                      </Tooltip>
                    }
                    key={'pod-profile-history'}
                    onClick={() => setShowTrendChart(true)}
                    variant='secondary'
                  />
                </Box>,
              ]
        }
      >
        <Box>
          {!readOnlyMode && (
            <Dialog
              open={showTrendChart}
              maxWidth='md'
              fullWidth
              onClose={() => {
                setShowTrendChart(false);
              }}
              aria-labelledby='alert-dialog-title'
              aria-describedby='alert-dialog-description'
            >
              <DialogTitle id='alert-dialog-title'>Pod Profile</DialogTitle>
              <DialogContent>
                <KubernetesPodProfilerHistory
                  accountId={accountId}
                  query={{
                    podName: query.pod_name,
                    namespaceName: query.namespace_name,
                  }}
                />
              </DialogContent>
              <DialogActions
                sx={{
                  mx: '28px',
                  button: {
                    minWidth: '140px',
                  },
                }}
              >
                <CustomButton
                  text={'Close'}
                  variant='secondary'
                  size='Medium'
                  onClick={() => {
                    setShowTrendChart(false);
                  }}
                />
              </DialogActions>
            </Dialog>
          )}
        </Box>
        {!readOnlyMode && !selectLang && <div style={{ color: 'red', paddingTop: '10px' }}>Please select a language first and click submit.</div>}
        <div style={{ marginTop: '20px' }}>{renderContent()}</div>
      </BoxLayout2>
    </div>
  );
};

export default KubernetesPodProfiler;
