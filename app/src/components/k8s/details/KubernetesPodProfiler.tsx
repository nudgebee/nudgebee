import React, { useEffect, useState, useRef } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import Loader from '@shared/Loader';
import ListingLayout from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import { Button as DsButton } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import DownloadButton from '@shared/buttons/DownloadButton';
import { generateRandomUUID, type CustomDropDownProps } from 'src/utils/common';
import apiKubernetes1 from '@api1/kubernetes1';
import SvgRenderer from '@shared/viewers/SvgRenderer';
import { StackedLineChartOutlined } from '@mui/icons-material';
import { KubernetesPodProfilerHistory } from '@components/k8s/common/KubernetesTable';
import { useNubiGlobalChat } from '@context/NubiGlobalChatContext';
import SafeIcon from '@shared/icons/SafeIcon';
import { DEFAULT_TITLE, getNubiIconUrl } from '@hooks/useTenantBranding';
import { ds } from '@utils/colors';
import {
  AUTO_DETECT_LANGUAGE,
  artifactsFromAgentResponse,
  base64ToBuffer,
  buildProfileRequest,
  decodeProfile,
  itemGroupsFromFindings,
  languageForFramework,
  reportedLanguage,
  unzipToText,
  type ProfilerItem,
} from './podProfilerArtifacts';

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
  auto: CustomDropDownProps[];
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
  // Preselect the runtime the pod page detected; with none (or one we have no
  // profiler for) let the agent resolve it instead of guessing a language.
  const [selectLang, setSelectLang] = useState(() => languageForFramework(query.framework));
  const langPickedRef = useRef(false);
  const [detectedLang, setDetectedLang] = useState('');
  const [outputTypeOptions, setOutputTypeOptions] = useState<CustomDropDownProps[]>([]);
  const [selectedOutputType, setSelectedOutputType] = useState('');
  const [profileTaskId, setProfileTaskId] = useState('');
  const [profileTaskStatus, setProfileTaskStatus] = useState('');
  const [profileDuration, setProfileDuration] = useState('15');
  const [showTrendChart, setShowTrendChart] = useState(false);
  const { openWithContext: openNubiChat } = useNubiGlobalChat();

  const pollingRef = useRef<any>({});

  // Pre-defined output type options for each language
  const OUTPUT_OPTIONS: OutputOptions = {
    auto: [
      { label: 'CPU', value: 'cpu' },
      { label: 'Memory', value: 'memory' },
    ],
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
    { label: 'Auto-detect', value: AUTO_DETECT_LANGUAGE },
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
      processProfilerData(itemGroupsFromFindings(findings));
    }
  }, [findings, readOnlyMode]);

  // The pod page looks the runtime up after this tab can already be open, so
  // adopt it when it lands, unless a language has been picked by hand.
  useEffect(() => {
    if (!langPickedRef.current) {
      setSelectLang(languageForFramework(query.framework));
    }
  }, [query.framework]);

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
      const svgProfile = response?.data?.data?.applications_convert_profile?.data?.svg_profile || '';
      const unzippedData = await unzipToText(base64ToBuffer(svgProfile));
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

  const processProfilerData = async (groups: ProfilerItem[][]): Promise<void> => {
    if (!groups.length) {
      setProfilerState((prev) => ({
        ...prev,
        isLoading: false,
        errorMsg: 'No Data Found',
      }));
      return;
    }

    const decoded = await decodeProfile(groups, query.pod_name);
    setProfilerState({
      ...decoded,
      isLoading: false,
      errorMsg: '',
    });
  };

  const processFailure = (data: any) => {
    if (readOnlyMode) {
      return;
    } // Skip processing failures in read-only mode

    let errorMsg = data.error_message || 'Fail to generate profiler data';
    if (selectLang === AUTO_DETECT_LANGUAGE) {
      errorMsg = `${errorMsg.replace(/[.\s]+$/, '')}. If the language could not be detected, select it under Language and submit again.`;
    }
    setProfilerState((prev) => ({
      ...prev,
      isLoading: false,
      errorMsg,
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
    setDetectedLang('');
    // Keep the language this profile ran with, even if the pod page's runtime
    // lookup lands afterwards.
    langPickedRef.current = true;

    try {
      const data = buildProfileRequest({
        accountId,
        podName: query?.pod_name,
        namespace: query?.namespace_name,
        language: selectLang,
        selection: selectedOutputType,
        durationSeconds: parseInt(profileDuration),
      });
      const response = await apiKubernetes1.applicationProfile(data);
      const applicationProfile = response?.data?.data?.applications_execute_profile?.data || {};
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

        const getProfileStatus = response?.data?.data?.applications_get_profile_status?.data || {};
        const newStatus = getProfileStatus.status;
        setProfileTaskStatus(newStatus);

        if (newStatus == 'COMPLETED') {
          const artifacts = artifactsFromAgentResponse(getProfileStatus.base64_profile?.data);
          setDetectedLang(reportedLanguage(artifacts.lang));
          processProfilerData(artifacts.groups);
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

    setDetectedLang('');
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

    openNubiChat({
      accountId,
      sessionId: generateRandomUUID(`${query.pod_name}-${type}`),
      query: queryPrompt,
    });
  };

  const renderContent = () => {
    const { svgData, txtData, jfrData, pprofData, fileExt, isLoading, errorMsg } = profilerState;

    if (isLoading) {
      return <Loader style={{ paddingTop: 'var(--ds-space-2)', width: '100%' }} />;
    }

    if (errorMsg) {
      return <Typography style={{ paddingLeft: 'var(--ds-space-1)', paddingTop: 'var(--ds-space-2)' }}>{errorMsg}</Typography>;
    }

    // Show message if in read-only mode but no data
    if (readOnlyMode && svgData.length === 0 && txtData.length === 0 && jfrData.length === 0 && pprofData.length == 0) {
      return <Typography style={{ paddingLeft: 'var(--ds-space-1)', paddingTop: 'var(--ds-space-2)' }}>No profiler findings provided</Typography>;
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
              <Box display='flex' justifyContent='flex-end' mb={ds.space[2]} gap={ds.space[2]}>
                <Tooltip title={`Ask ${DEFAULT_TITLE} for analysis`}>
                  <Box sx={{ display: 'inline-flex', mr: 'var(--ds-space-1)' }}>
                    <DsButton
                      tone='secondary'
                      size='md'
                      composition='icon-only'
                      aria-label={`Ask ${DEFAULT_TITLE}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        handleGenerateAnalysis(analysisType, data);
                      }}
                      icon={<SafeIcon src={getNubiIconUrl()} width={24} height={24} alt={`Ask ${DEFAULT_TITLE}`} />}
                    />
                  </Box>
                </Tooltip>
                <DownloadButton
                  onClick={() => ({
                    fileName: data.fileName,
                    data: data.data,
                    type: fileExt,
                  })}
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
            <Box display='flex' justifyContent='flex-end' mb={ds.space[2]}>
              <DownloadButton
                onClick={() => ({
                  fileName: data.fileName,
                  data: data.data,
                  type: fileExt,
                })}
                id={`pod-profiler-svg-${index}`}
              />
            </Box>
            <Typography>{`Download the JFR File ${data.fileName}`}</Typography>
          </>
        ))}

        {/* SVG Data Section */}
        {svgData.map((data, index) => (
          <div key={index}>
            <Box display='flex' justifyContent='flex-end' mb={ds.space[2]}>
              <DownloadButton
                onClick={() => ({
                  fileName: data.fileName,
                  data: data.data,
                  type: 'text/svg+xml',
                })}
                id={`pod-profiler-svg-${index}`}
              />
            </Box>
            <SvgRenderer
              svg={data.data}
              style={{
                width: '100%',
                border: '1px solid var(--ds-gray-300)',
                padding: 'var(--ds-space-2)',
                overflow: 'auto',
              }}
            />
          </div>
        ))}
      </>
    );
  };

  return (
    <div style={{ paddingTop: 'var(--ds-space-2)', paddingLeft: 'var(--ds-space-2)' }}>
      <ListingLayout id='pod-profiler'>
        <ListingLayout.Toolbar
          actions={
            !readOnlyMode ? (
              <DsButton
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<StackedLineChartOutlined />}
                aria-label='Pod Profile History'
                tooltip='Pod Profile History'
                onClick={() => setShowTrendChart(true)}
              />
            ) : undefined
          }
        >
          {!readOnlyMode && (
            <>
              <FilterDropdown
                label='Language'
                options={LANGUAGE_OPTIONS}
                value={selectLang}
                onSelect={(event: any) => {
                  resetData();
                  langPickedRef.current = true;
                  setSelectLang(event.target.value);
                }}
                size='sm'
                disabled={profilerState.isLoading}
              />
              <FilterDropdown
                label='Profile Type'
                options={outputTypeOptions}
                value={selectedOutputType}
                onSelect={(event: any) => {
                  resetData();
                  setSelectedOutputType(event.target.value);
                }}
                size='sm'
                disabled={profilerState.isLoading}
              />
              <Input
                label='Profile Duration (seconds, max 600)'
                value={profileDuration}
                onChange={(input) => {
                  if (input === '') {
                    setProfileDuration('');
                    return;
                  }
                  const numericValue = input.replace(/^0+/, '') || '0';
                  const parsedValue = parseInt(numericValue);
                  if (!isNaN(parsedValue)) {
                    setProfileDuration(parsedValue.toString());
                  }
                }}
                type='number'
                error={parseInt(profileDuration) > 600 ? 'Value cannot exceed 600' : undefined}
                disabled={profilerState.isLoading}
                size='sm'
              />
              <DsButton tone='primary' size='sm' onClick={handleSubmit} disabled={profilerState.isLoading || !selectLang || !selectedOutputType}>
                Submit
              </DsButton>
            </>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          {!readOnlyMode && (
            <Modal
              open={showTrendChart}
              width='md'
              onClose={() => setShowTrendChart(false)}
              title='Pod Profile'
              actionButtons={
                <DsButton tone='secondary' size='md' onClick={() => setShowTrendChart(false)}>
                  Close
                </DsButton>
              }
            >
              <KubernetesPodProfilerHistory
                accountId={accountId}
                query={{
                  podName: query.pod_name,
                  namespaceName: query.namespace_name,
                }}
              />
            </Modal>
          )}
          {!readOnlyMode && !selectLang && (
            <Typography sx={{ color: 'var(--ds-red-500)', pt: 'var(--ds-space-2)' }}>Please select a language first and click submit.</Typography>
          )}
          {!readOnlyMode && selectLang === AUTO_DETECT_LANGUAGE && detectedLang && !profilerState.isLoading && (
            <Typography sx={{ pt: 'var(--ds-space-2)' }}>
              {`Detected language: ${LANGUAGE_OPTIONS.find((option) => option.value === detectedLang)?.label || detectedLang}`}
            </Typography>
          )}
          <Box sx={{ mt: 'var(--ds-space-5)' }}>{renderContent()}</Box>
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default KubernetesPodProfiler;
