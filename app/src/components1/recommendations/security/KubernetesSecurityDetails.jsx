import { Box, Grid, Stack, Typography } from '@mui/material';
import { useEffect, useState, useRef, useMemo } from 'react';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import PropTypes from 'prop-types';
import { ANNOTATIONS, CI_PREFIX, WORKLOADS_PREFIX } from '@lib/annotationKeys';
import { action } from 'src/utils/actionStyles';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import apiRecommendations from '@api1/recommendation';
import SeverityInfographics from '@components1/k8s/common/SeverityInfographic';
import InfographicList from '@components1/common/InfographicList';
import apiUser from '@api1/user';
import CustomLink from '@components1/common/CustomLink';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomTable from '@common-new/tables/CustomTable2';
import { Modal } from '@components1/ds/Modal';
import CustomDropdown from '@common-new/CustomDropdown';
import { Button } from '@components1/ds/Button';

const SEVERITY_TO_DS_LEVEL = {
  critical: 'critical',
  high: 'high',
  medium: 'medium',
  low: 'low',
  info: 'info',
};
const toDsSeverityLevel = (s) => SEVERITY_TO_DS_LEVEL[String(s || '').toLowerCase()] || 'info';
import CustomPRLink from '@components1/common/CustomPRLink';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import apiIntegrations from '@api1/integrations';
import k8sApi from '@api1/kubernetes';
import { PrOpenIcon } from '@assets';
import { hasWriteAccess } from '@lib/auth';

const KubernetesSecurityDetails = (props) => {
  const prevQueryRef = useRef();

  const [kubernetesSecurity, setKubernetesSecurity] = useState([]);
  const [kubernetesSecurityCount, setKubernetesSecurityCount] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [loading, setLoading] = useState(false);
  const [severityData, setSeverityData] = useState([]);
  const [dataCounts, setDataCounts] = useState([
    { text: 'Recommendations', value: '-' },
    { text: 'CVE', value: '-' },
    { text: 'Images', value: '-' },
  ]);
  const [openCreatePR, setOpenCreatePR] = useState(false);
  const [allGitIntegrations, setAllGitIntegrations] = useState([]); // Combined GitHub + GitLab integrations
  const [selectedGitIntegration, setSelectedGitIntegration] = useState(''); // Format: "github:name" or "gitlab:name"
  const [selectedWorkloadAnnotations, setSelectedWorkloadAnnotations] = useState({});
  const [prLoading, setPRLoading] = useState(false);
  const [isGitReposLoading, setIsGitReposLoading] = useState(false);
  const [selectedItemForPR, setSelectedItemForPR] = useState(null);

  // Helper to detect git provider from repo URL
  const detectGitProvider = (repoUrl) => {
    if (!repoUrl) return null;
    const url = repoUrl.toLowerCase();
    if (url.includes('github.com')) return 'github';
    if (url.includes('gitlab')) return 'gitlab';
    return null;
  };

  // Filter integrations based on the repo URL in annotations
  const filteredGitIntegrations = useMemo(() => {
    const repoUrl = selectedWorkloadAnnotations[ANNOTATIONS.CI_GIT_REPO] || selectedWorkloadAnnotations[ANNOTATIONS.WORKLOAD_GIT_REPO];
    const detectedProvider = detectGitProvider(repoUrl);
    if (!detectedProvider) return allGitIntegrations;
    return allGitIntegrations.filter((i) => i.type === detectedProvider);
  }, [selectedWorkloadAnnotations, allGitIntegrations]);

  // Auto-select first filtered integration when available
  useEffect(() => {
    if (filteredGitIntegrations.length > 0 && !selectedGitIntegration) {
      setSelectedGitIntegration(filteredGitIntegrations[0].key);
    }
  }, [filteredGitIntegrations, selectedGitIntegration]);

  const BEST_PRACTICES_HEADER = [
    { name: 'CVE', width: '15%' },
    { name: 'Image', width: '20%' },
    { name: 'App', width: '20%' },
    { name: 'Title', width: '20%' },
    { name: 'Severity', width: '5%' },
    { name: 'Package Id', width: '5%' },
    { name: 'CWEs', width: '5%' },
  ];

  if (!props?.llmTableData?.length) {
    BEST_PRACTICES_HEADER.push({ name: 'Updated At', width: '5%' }, { name: '', width: '5%' });
  }

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    //generate ticket description
    let description = '';
    description += '**Title**: ' + data?.recommendation?.Title + '\n';
    description += '**Image**: ' + data?.image + '\n';
    description += '**Severity**: ' + data?.recommendation?.Severity + '\n';
    description += '**CVE**: ' + data?.recommendation?.VulnerabilityID + '\n';
    description += '**Package**: ' + data?.recommendation?.PkgID + '\n';
    description += '**Fixed Version**: ' + data?.recommendation?.FixedVersion + '\n';
    description += '**Installed Version**: ' + data?.recommendation?.InstalledVersion + '\n';
    description += '**Description**: ' + data?.recommendation?.Description + '\n';
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    } else if (menuItem.id === 1) {
      openPRModal(data);
    }
  };

  const listGitConfigurations = () => {
    setIsGitReposLoading(true);
    Promise.all([
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'github' }),
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'gitlab' }),
    ])
      .then(([githubRes, gitlabRes]) => {
        const githubData =
          githubRes?.data?.length > 0
            ? githubRes.data.map((g) => ({ name: g.name, type: 'github', key: `github:${g.name}`, label: `GitHub: ${g.name}` }))
            : [];
        const gitlabData =
          gitlabRes?.data?.length > 0
            ? gitlabRes.data.map((g) => ({ name: g.name, type: 'gitlab', key: `gitlab:${g.name}`, label: `GitLab: ${g.name}` }))
            : [];
        const combined = [...githubData, ...gitlabData];
        setAllGitIntegrations(combined);
      })
      .finally(() => setIsGitReposLoading(false));
  };

  const getWorkloadAnnotations = async (data) => {
    try {
      const res = await k8sApi.getK8sWorkload(1, 0, {
        accountId: props?.kubernetes?.id,
        namespaceName: data?.namespace,
        workloadName: data?.workload_name,
        exactNameMatch: true,
      });
      const workloads = res?.data?.k8s_workloads || [];
      if (workloads.length === 1) {
        const annotations = workloads[0].meta?.config?.annotations || {};
        // For security recommendations, we need workloads.nudgebee.com (source code repo) or ci.nudgebee.com or argocd
        const filteredKeys = Object.keys(annotations).filter(
          (key) => key.startsWith(WORKLOADS_PREFIX) || key.startsWith(CI_PREFIX) || key.startsWith('argocd.argoproj.io')
        );
        if (filteredKeys.length > 0) {
          const filtered = {};
          filteredKeys.forEach((key) => (filtered[key] = annotations[key]));
          setSelectedWorkloadAnnotations(filtered);
          return;
        }
        // Check cloud_resource_attributes for manual config
        if (workloads[0].cloud_resource_id) {
          const attributes = await k8sApi.getResourceAttributes(workloads[0].cloud_resource_id);
          const manualConfig = {};
          attributes.forEach((attr) => {
            // For security, prefer workloads.nudgebee.com (source repo) but also accept ci.nudgebee.com
            if (attr.name.startsWith(WORKLOADS_PREFIX) || attr.name.startsWith(CI_PREFIX)) manualConfig[attr.name] = attr.value;
          });
          if (Object.keys(manualConfig).length > 0) {
            setSelectedWorkloadAnnotations(manualConfig);
            return;
          }
        }
      }
      setSelectedWorkloadAnnotations({});
    } catch (error) {
      console.error('Error fetching workload annotations:', error);
      setSelectedWorkloadAnnotations({});
    }
  };

  const openPRModal = (data) => {
    setSelectedItemForPR(data);
    setOpenCreatePR(true);
    listGitConfigurations();
    getWorkloadAnnotations(data);
  };

  const closeCreatePRModal = () => {
    setOpenCreatePR(false);
    setSelectedGitIntegration('');
    setAllGitIntegrations([]);
    setSelectedWorkloadAnnotations({});
    setSelectedItemForPR(null);
  };

  const handleCreatePR = () => {
    if (!selectedItemForPR || !selectedGitIntegration) return;
    // Extract type and name from key (format: "github:name" or "gitlab:name")
    const [integrationType, ...nameParts] = selectedGitIntegration.split(':');
    const integrationName = nameParts.join(':'); // Handle names with colons
    setPRLoading(true);
    apiRecommendations
      .applyRecommendation(
        props?.kubernetes?.id,
        selectedItemForPR.id,
        { workload_name: selectedItemForPR?.workload_name, namespace: selectedItemForPR?.namespace },
        integrationType,
        { name: integrationName }
      )
      .then((res) => {
        if (res?.errors?.length > 0) {
          snackbar.error('Failed to create Pull Request');
        } else {
          snackbar.success('PR creation initiated! The code agent is creating the PR. Check back shortly for the PR link.', 6000);
          getSecurityDetails();
        }
        closeCreatePRModal();
      })
      .catch((error) => {
        console.error(error);
        snackbar.error('Failed to create Pull Request');
        closeCreatePRModal();
      })
      .finally(() => setPRLoading(false));
  };

  const whereClause = () => {
    const query = {};
    if (props?.query?.workload_name) {
      query.workload = props?.query?.workload_name;
    }
    if (props?.query?.namespace) {
      query.namespace = props?.query?.namespace;
    }
    if (props?.query?.severity) {
      query.severity = props?.query.severity;
    }

    if (props?.query?.vulnerabilityId) {
      query.vulnerabilityId = props?.query.vulnerabilityId;
    }
    if (props?.query?.image) {
      query.image = props.query.image;
    }
    if (props?.query?.status) {
      query.status = props.query.status;
    }
    if (props?.query?.package_id) {
      query.package_id = props?.query?.package_id;
    }
    return query;
  };

  const getMenuItems = (data) => {
    const hasFixAvailable = data?.recommendation?.FixedVersion;
    return [
      {
        icon: TicketsIcon,
        label: 'Create Ticket',
        id: 0,
        disabled: data?.ticket?.ticket_id,
      },
      {
        icon: PrOpenIcon,
        label: 'Create Pull Request',
        id: 1,
        disabled: !hasFixAvailable || !hasWriteAccess() || data?.resolution,
      },
    ];
  };

  useEffect(() => {
    const previousQuery = prevQueryRef.current;
    if (JSON.stringify(previousQuery) != JSON.stringify(props.query)) {
      setPage(0);
    }
    prevQueryRef.current = props.query;
  }, [JSON.stringify(props.query)]);

  const setTableData = (data) => {
    setLoading(false);
    let k8sRecommendationData = data?.recommendation?.map((item) => {
      let data = [];
      if (typeof item?.recommendation === 'string') {
        item.recommendation = JSON.parse(item.recommendation);
      }
      data.push({
        component: (
          <Stack direction='column' spacing={1}>
            <CustomLink target={'_blank'} href={'https://nvd.nist.gov/vuln/detail/' + item.recommendation?.VulnerabilityID}>
              {item.recommendation?.VulnerabilityID}
            </CustomLink>
            {item.ticket ? (
              <Typography sx={{ fontSize: 'var(--ds-text-small)' }}>
                Ticket -
                <CustomLink href={item.ticket?.url} style={{ fontSize: 'var(--ds-text-small)' }} target='_blank'>
                  {item.ticket?.ticket_id}
                </CustomLink>
              </Typography>
            ) : (
              <></>
            )}
            {item.resolution && <CustomPRLink prURL={item.resolution.type_reference_id} statusMessage={item.resolution.status_message} />}
          </Stack>
        ),
        drilldownQuery: item,
        data: item.recommendation?.VulnerabilityID,
      });
      data.push({
        component: <Text value={item?.image?.split('/').pop()} showAutoEllipsis />,
        data: item?.image?.split('/')[1],
      });
      data.push({
        component: <Text value={`${item.namespace} / ${item.workload_name}`} showAutoEllipsis />,
        data: item?.image?.split('/')[1],
      });
      data.push({
        component: <Text value={item?.recommendation?.Title} showAutoEllipsis />,
        data: item?.recommendation?.Title,
      });
      data.push({
        component: (
          <Box sx={{ display: 'flex', justifyContent: 'center' }}>
            <SeverityIcon level={toDsSeverityLevel(item?.severity)} aria-label={item?.severity || '-'} />
          </Box>
        ),
        data: item?.severity,
      });
      data.push({
        component: <Text showAutoEllipsis value={item?.recommendation?.PkgID} />,
        data: item?.recommendation?.PkgID,
      });
      data.push({
        component: <Text value={item?.recommendation?.CweIDs?.join(',')} />,
        data: item?.recommendation?.CweIDs?.join(','),
      });
      if (!props?.llmTableData?.length) {
        data.push({ component: <Datetime value={item.created_at} /> });

        data.push({
          component: (
            <Box display={'flex'} flexDirection={'row'} alignItems={'space-between'} justifyContent={'flex-end'}>
              <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />
            </Box>
          ),
        });
      }

      return data;
    });
    setKubernetesSecurity(k8sRecommendationData);
    setKubernetesSecurityCount(data?.recommendation_aggregate?.count ?? k8sRecommendationData?.length);
  };

  const getSecurityDetails = () => {
    setLoading(true);

    if (props?.llmTableData) {
      setTableData({ recommendation: props?.llmTableData });
      setLoading(false);
    } else {
      if (!props?.kubernetes?.id) {
        return;
      }
      setKubernetesSecurity([]);
      setKubernetesSecurityCount(0);
      const query = whereClause();
      apiRecommendations
        .getK8sSecurityRecommendation({
          accountId: props?.kubernetes?.id,
          category: 'Security',
          ruleName: 'image_scan',
          status: query.status ? [query.status] : [],
          severity: query.severity,
          image: query.image,
          resourceNamespace: query.namespace,
          resourceWorkload: query.workload,
          limit: rowsPerPage,
          offset: page * rowsPerPage,
          fetchTicket: true,
          vulnerabilityId: query.vulnerabilityId,
          package_id: query.package_id,
        })
        .then((res) => {
          setTableData(res?.data);
        })
        .finally(() => {
          setLoading(false);
        });
    }
  };

  useEffect(() => {
    getSecurityDetails();
  }, [props?.kubernetes?.id, page, rowsPerPage, JSON.stringify(props.query)]);

  const handleTicketSuccess = () => {
    getSecurityDetails();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const listSeverityInfographics = () => {
    const query = whereClause();
    query['accountId'] = props?.kubernetes?.id;
    if (props?.query.workload_name) {
      query.workload = props?.query.workload_name;
    }
    if (props?.query?.namespace) {
      query.namespace = props?.query.namespace;
    }
    if (props?.query?.status) {
      query.status = props?.query.status;
    }
    if (props?.query?.severity) {
      query.severity = props?.query.severity;
    }
    setSeverityData([
      { label: 'Critical', value: '-' },
      { label: 'High', value: '-' },
      { label: 'Medium', value: '-' },
      { label: 'Low', value: '-' },
    ]);
    setDataCounts([
      { text: 'Recommendations', value: '-' },
      { text: 'CVE', value: '-' },
      { text: 'Images', value: '-' },
    ]);
    apiRecommendations.getSecuritySeverityGrouping(query).then((res) => {
      const severityResponseData = res?.recommendation_security_groupings_v2?.rows[0];
      setSeverityData([
        { label: 'Critical', value: severityResponseData?.count_severity_critical },
        { label: 'High', value: severityResponseData?.count_severity_high },
        { label: 'Medium', value: severityResponseData?.count_severity_medium },
        { label: 'Low', value: severityResponseData?.count_severity_low },
      ]);
      setDataCounts([
        { text: 'Recommendations', value: kubernetesSecurityCount },
        { text: 'CVE', value: severityResponseData?.count_vulnerability_id },
        { text: 'Images', value: severityResponseData?.count_image },
      ]);
    });
  };

  useEffect(() => {
    if (kubernetesSecurity && kubernetesSecurity.length == 0) {
      return;
    }
    if (!props?.disableInfographic) {
      listSeverityInfographics();
    }
  }, [props?.kubernetes?.id, JSON.stringify(props.query), kubernetesSecurity]);

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Security Issue On - ' + ticketData.image,
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      <Modal width='md' open={openCreatePR} handleClose={closeCreatePRModal} title='Create Pull Request'>
        {prLoading && (
          <Box sx={{ position: 'absolute', top: 0, left: 0, right: 0, zIndex: 9999 }}>
            <LinearLoader />
          </Box>
        )}
        {isGitReposLoading || filteredGitIntegrations.length > 0 ? (
          Object.keys(selectedWorkloadAnnotations).length > 0 ? (
            <Grid container spacing={3}>
              <Grid item xs={12}>
                <CustomDropdown
                  label='Git Integration'
                  value={filteredGitIntegrations.find((i) => i.key === selectedGitIntegration)?.label || ''}
                  options={filteredGitIntegrations.map((i) => i.label)}
                  onChange={(e) => {
                    const selected = filteredGitIntegrations.find((i) => i.label === e.target.value);
                    if (selected) setSelectedGitIntegration(selected.key);
                  }}
                  showNormalField
                  isLoading={isGitReposLoading}
                />
              </Grid>
              <Grid item xs={12}>
                <Typography variant='body2' color='textSecondary'>
                  <strong>CVE:</strong> {selectedItemForPR?.recommendation?.VulnerabilityID}
                </Typography>
                <Typography variant='body2' color='textSecondary'>
                  <strong>Package:</strong> {selectedItemForPR?.recommendation?.PkgID}
                </Typography>
                <Typography variant='body2' color='textSecondary'>
                  <strong>Fixed Version:</strong> {selectedItemForPR?.recommendation?.FixedVersion}
                </Typography>
              </Grid>
              <Grid item xs={12}>
                <Typography variant='caption' color='textSecondary'>
                  CI Annotations detected for: {selectedItemForPR?.workload_name}
                </Typography>
              </Grid>
              <Grid item xs={12} sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'flex-end', marginBottom: 'var(--ds-space-3)' }}>
                <Button tone='secondary' size='md' onClick={closeCreatePRModal}>
                  Cancel
                </Button>
                <Button
                  tone='primary'
                  size='md'
                  disabled={!selectedGitIntegration || !Object.keys(selectedWorkloadAnnotations).length || prLoading}
                  onClick={handleCreatePR}
                >
                  Create PR
                </Button>
              </Grid>
            </Grid>
          ) : (
            <Box sx={{ p: 'var(--ds-space-4)' }}>
              <Typography color='warning.main'>
                No CI annotations found for this workload. Please configure CI annotations ({ANNOTATIONS.CI_GIT_REPO},{' '}
                {ANNOTATIONS.CI_HELM_VALUES_PATH}) on the workload.
              </Typography>
              <Box sx={{ mt: 'var(--ds-space-4)', display: 'flex', justifyContent: 'flex-end' }}>
                <Button tone='secondary' size='md' onClick={closeCreatePRModal}>
                  Close
                </Button>
              </Box>
            </Box>
          )
        ) : (
          <Box sx={{ p: 'var(--ds-space-4)' }}>
            <Typography color='warning.main'>No Git integrations configured. Please set up a GitHub or GitLab integration first.</Typography>
            <Box sx={{ mt: 'var(--ds-space-4)', display: 'flex', justifyContent: 'flex-end' }}>
              <Button tone='secondary' size='md' onClick={closeCreatePRModal}>
                Close
              </Button>
            </Box>
          </Box>
        )}
      </Modal>
      {props?.disableInfographic ? (
        <></>
      ) : (
        <Box
          sx={{ display: 'flex', flex: 1, flexDirection: 'row', justifyContent: 'space-between', mt: 'var(--ds-space-4)', mx: 'var(--ds-space-4)' }}
        >
          <InfographicList sequence={dataCounts} />

          {severityData.length > 0 && (
            <Box>
              <SeverityInfographics severityData={severityData} />{' '}
            </Box>
          )}
        </Box>
      )}
      <CustomTable
        id={props.tableId}
        showExpandable
        headers={BEST_PRACTICES_HEADER}
        tableData={kubernetesSecurity}
        rowsPerPage={rowsPerPage}
        totalRows={kubernetesSecurityCount}
        onPageChange={changePage}
        pageNumber={page + 1}
        tableHeadingCenter={['Severity']}
        stickyColumnIndex='9'
        showUpdatedEmptyData={kubernetesSecurity?.length == 0}
        sort={{
          name: 'Savings',
          order: 'desc',
        }}
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              componentFn: (opt, drilldown, _row) => {
                return (
                  <Grid container spacing={2}>
                    <Grid item md={3}>
                      <b>Title</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.Title}
                    </Grid>
                    <Grid item md={3}>
                      <b>Image</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.image}
                    </Grid>
                    <Grid item md={3}>
                      <b>Package</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.PkgID || '-'}
                    </Grid>
                    <Grid item md={3}>
                      <b>Fixed Version</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.FixedVersion || '-'}
                    </Grid>
                    <Grid item md={3}>
                      <b>Installed Version</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.InstalledVersion || '-'}
                    </Grid>
                    <Grid item md={3}>
                      <b>CVSS Score</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.CVSS?.nvd?.V3Score || '-'}
                    </Grid>
                    <Grid item md={3}>
                      <b>CVSS Vector</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.CVSS?.nvd?.V3Vector || '-'}
                    </Grid>
                    <Grid item md={3}>
                      <b>Layer</b>
                    </Grid>
                    <Grid item md={9}>
                      {drilldown?.recommendation?.Layer?.Digest || '-'}
                    </Grid>
                  </Grid>
                );
              },
            },
            {
              text: 'Description',
              value: 1,
              componentFn: (opt, drilldown, _row) => {
                return <>{drilldown?.recommendation?.Description}</>;
              },
            },
            {
              text: 'References',
              value: 2,
              componentFn: (opt, drilldown, _row) => {
                return (
                  <div>
                    {drilldown?.recommendation?.References?.map((r, _i) => (
                      <li key={r.id}>
                        <a href={r} target='_blank' rel='noreferrer'>
                          {r}
                        </a>
                      </li>
                    ))}
                  </div>
                );
              },
            },
          ],
        }}
        loading={loading}
      />
    </>
  );
};

KubernetesSecurityDetails.propTypes = {
  kubernetes: PropTypes.object,
  disableInfographic: PropTypes.bool,
  query: PropTypes.object,
  tableId: PropTypes.string,
  llmTableData: PropTypes.array,
};

export default KubernetesSecurityDetails;
