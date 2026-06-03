import React, { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import recommendationApi, { RECOMMENDATION_STATUS, RECOMMENDATION_SERVERITY } from '@api1/recommendation';
import { unique } from '@lib/collections';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import Datetime from '@common-new/format/Datetime';
import PropTypes from 'prop-types';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import apiUser from '@api1/user';
import Text from '@common-new/format/Text';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { colors, ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import { snakeToTitleCase } from 'src/utils/common';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/ds/Tooltip';
import SafeIcon from '@components1/common/SafeIcon';

import WidgetCard from '@components1/ds/WidgetCard';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Stat } from '@components1/ds/Stat';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import FilterDropdown from '@components1/ds/FilterDropdown';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { ScanRefreshButton } from './ScanRefreshButton';

const BEST_PRACTICES_HEADER = [
  { name: 'Name', width: '10%' },
  { name: 'Severity', width: '5%' },
  { name: 'Object Type', width: '10%' },
  { name: 'Namespaces', width: '5%' },
  { name: 'Object Names', width: '15%' },
  { name: 'Updated At', width: '10%' },
  { name: 'Description', width: '45%' },
  '',
];
const RULE_LABEL_MAP = {
  configmaps_misconfigurations: 'Unused ConfigMaps',
  misconfigurations: 'Misconfiguration',
  clusterroles_misconfigurations: 'ClusterRole Issues',
  namespaces_misconfigurations: 'Namespace Config Issues',
  nodes_misconfigurations: 'Node Issues',
  persistentvolumeclaims_misconfigurations: 'PVC Issues',
  persistentvolumes_misconfigurations: 'PV Issues',
  poddisruptionbudgets_misconfigurations: 'Pod Disruption Budget Issues',
  pods_misconfigurations: 'Pod Disruption Budget Issues',
  serviceaccounts_misconfigurations: 'Service Account Issues',
  services_misconfigurations: 'Service Issues',
  statefulsets_misconfigurations: 'Staefulsets Issues',
  health_check: 'Health Check',
};

const KubernetesBestPractices = ({ enabledSummary = true, enabledFilters = true, isOptimisePage = false, ...props }) => {
  const { assistantName } = useTenantBranding();
  const [kubernetesBestPractice, setKubernetesBestPractice] = useState([]);
  const [kubernetesBestPracticeCount, setKubernetesBestPracticeCount] = useState(0);
  const [totalBestPracticeCount, setTotalBestPracticeCount] = useState(0);
  const [ruleName, setRuleName] = useState('');
  const [severity, setSeverity] = useState('');
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [recommendationStatus, setRecommendationStatus] = useState('Open');
  const [namespace, setNamespace] = useState('');
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  const kubernetesBestPracticesTable = 'kubernetesBestPracticesTable';

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }
    recommendationApi
      .listRecommendationNamesapces({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: ruleName,
        status: recommendationStatus,
      })
      .then((res) => {
        setNamespaceFilter(res);
      })
      .catch(() => {
        setNamespaceFilter([]);
      });
  }, [props?.kubernetes?.id, recommendationStatus, ruleName]);

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Name**: ' + (RULE_LABEL_MAP[data.rule_name] || snakeToTitleCase(data.rule_name)) + '\n';
    description += '**Severity**: ' + data?.severity + '\n';

    if (data.rule_name === 'health_check' && data.recommendation?.workload) {
      description += '**Object Type**: ' + data.recommendation.workload.kind + '\n';
      description += '**Namespace**: ' + data.recommendation.workload.namespace + '\n';
      description += '**Object Name**: ' + data.recommendation.workload.name + '\n';
      description += '**Issues**: ' + (data.recommendation.messages?.join(', ') || '-') + '\n';
    } else if (Array.isArray(data.recommendation)) {
      description += '**Object Type**: ' + unique(data.recommendation?.map?.((r) => r?.kind))?.join(', ') + '\n';
      description += '**Namespaces**: ' + unique(data.recommendation?.map?.((r) => r?.namespace))?.join(', ') + '\n';
      description += '**Object Names**: ' + unique(data.recommendation?.map?.((r) => r?.name))?.join(', ') + '\n';
      description += '**Description**: ' + unique(data.recommendation?.map?.((r) => r?.message))?.join(', ') + '\n';
    }
    return description;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const getRecommendation = (item) => {
    if (item.rule_name === 'certificate_expiry') {
      return (
        <>
          <li style={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}>
            Date until expiry: {item.recommendation.days_until_expiry}
          </li>
          <li style={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}>
            Expiry Date: {item.recommendation.expiry_date}
          </li>
        </>
      );
    }
    if (item.rule_name === 'health_check' && item.recommendation?.messages) {
      return (
        <>
          {item.recommendation.messages.map((message, index) => (
            <li key={index} style={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}>
              {message}
            </li>
          ))}
        </>
      );
    }
    return (
      <li style={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}>
        No Data Available
      </li>
    );
  };

  const handleExportDownload = async (format) => {
    try {
      const exportFormat = format === 'xlsx' ? 'xlsx' : 'csv';
      const response = await recommendationApi.exportRecommendations({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: ruleName || undefined,
        namespace: namespace || undefined,
        status: recommendationStatus ? [recommendationStatus] : undefined,
        format: exportFormat,
      });

      if (response?.data?.data?.recommendation_export) {
        const { file_data, filename, content_type } = response.data.data.recommendation_export;

        const byteCharacters = atob(file_data);
        const byteNumbers = new Array(byteCharacters.length);
        for (let i = 0; i < byteCharacters.length; i++) {
          byteNumbers[i] = byteCharacters.charCodeAt(i);
        }
        const byteArray = new Uint8Array(byteNumbers);
        const blob = new Blob([byteArray], { type: content_type });

        const url = window.URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = filename;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
        window.URL.revokeObjectURL(url);

        snackbar.success('Export downloaded successfully');
      } else {
        snackbar.error('Export failed: No data received');
      }
    } catch (error) {
      console.error('Export error:', error);
      snackbar.error(`Export failed: ${error.message}`);
    }
  };

  const listBestPracticesRecommendations = () => {
    if (!props?.kubernetes?.id) {
      return;
    }
    setLoading(true);
    setKubernetesBestPractice([]);
    recommendationApi
      .getK8sRecommendation({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        ruleName: ruleName,
        severity: severity,
        status: recommendationStatus ? [recommendationStatus] : [],
        resourceNamespace: namespace,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res) => {
        setLoading(false);
        setKubernetesBestPracticeCount(res?.data?.recommendation_aggregate?.aggregate?.count || 0);
        const rawItems = res?.data?.recommendation || [];
        let k8sRecommendationData = rawItems.map((item) => {
          let data = [];
          let name = RULE_LABEL_MAP[item.rule_name] || snakeToTitleCase(item.rule_name);
          let nameSpace = '-';
          let objectType = '-';
          let objectNames = '-';

          if (Array.isArray(item.recommendation)) {
            nameSpace = unique(item.recommendation?.map((r) => r?.namespace))?.join(', ') ?? '-';
            objectType = unique(item.recommendation?.map?.((r) => r?.kind))?.join(', ') ?? '-';
            objectNames = unique(item.recommendation?.map?.((r) => r?.name))?.join(', ') ?? '-';
          } else if (item.rule_name === 'health_check' && item.recommendation?.workload) {
            nameSpace = item.recommendation.workload.namespace ?? '-';
            objectType = item.recommendation.workload.kind ?? '-';
            objectNames = item.recommendation.workload.name ?? '-';
          } else if (item.recommendation) {
            nameSpace = item.recommendation?.namespace ?? '-';
            objectType = item.recommendation?.kind ?? '-';
            objectNames = item.recommendation?.name ?? '-';
          }
          data.push({
            component: ClusterNameWithRegion({
              name: name,
              hideIcon: true,
              showAutoEllipsis: true,
              maxWidth: '100%',
              region:
                item.ticket !== undefined ? (
                  <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} showAutoEllipsis={true} />
                ) : (
                  ''
                ),
            }),
          });
          data.push({
            component: (() => {
              const lvl = String(item.severity || '').toLowerCase();
              const allowed = ['critical', 'high', 'medium', 'low', 'info'];
              return allowed.includes(lvl) ? <SeverityIcon level={lvl} size={16} /> : <Text value='—' secondaryText />;
            })(),
            data: item.severity,
          });
          data.push({ component: <Text value={objectType || '-'} showAutoEllipsis /> });
          data.push({ component: <Text value={nameSpace || '-'} showAutoEllipsis /> });
          data.push({ component: <Text value={objectNames || '-'} showAutoEllipsis /> });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <ul style={{ padding: '0 0 0 var(--ds-space-4)' }}>
                {item.recommendation && item.recommendation.length > 0
                  ? [...new Map(item.recommendation.map((r) => [r?.message, r])).values()].map((r) => {
                      if (r?.container) {
                        return (
                          <li
                            key={r?.message}
                            style={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}
                          >
                            {r?.message} in container <b>{r?.container}</b>
                          </li>
                        );
                      }
                      return (
                        <li
                          key={r?.message}
                          style={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}
                        >
                          {r?.message}
                        </li>
                      );
                    })
                  : getRecommendation(item)}
              </ul>
            ),
          });
          data.push({
            component: (
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}
              >
                <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
                  <span>
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      aria-label={`Ask ${assistantName}`}
                      id={`bp-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
                        const prompt = buildNubiOptimizePrompt({
                          ruleName: name,
                          category: 'Configuration',
                          severity: item.severity || 'Info',
                          resourceName: objectNames || '-',
                          resourceType: objectType || '',
                          namespace: nameSpace || '',
                          brief: Array.isArray(item.recommendation)
                            ? item.recommendation
                                .map((r) => r?.message)
                                .filter(Boolean)
                                .join('; ')
                            : item.recommendation?.messages?.join('; ') || '',
                        });
                        setNubiQuery(prompt);
                        setNubiAccountId(props?.kubernetes?.id);
                        setNubiConversationId(`recom_${item.id}`);
                        setNubiSidebarVisible(true);
                      }}
                    />
                  </span>
                </CustomTooltip>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={[
                    {
                      id: `bp-action-ticket-${item.id}`,
                      label: item.ticket?.ticket_id ? `Ticket: ${item.ticket.ticket_id}` : 'Create ticket',
                      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
                      disabled: !!item.ticket?.ticket_id,
                      onSelect: () => {
                        onMenuClick({ id: 0 }, item);
                      },
                    },
                  ]}
                  trigger={
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<MoreVertIcon />}
                      aria-label='More actions'
                      id={`bp-action-menu-${item.id}`}
                    />
                  }
                />
              </Box>
            ),
          });

          return data;
        });
        setKubernetesBestPractice(k8sRecommendationData);
      })
      .catch(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    listBestPracticesRecommendations();
  }, [props?.kubernetes?.id, page, ruleName, severity, recommendationStatus, namespace, rowsPerPage]);

  useEffect(() => {
    if (!props?.kubernetes?.id) {
      return;
    }

    recommendationApi
      .getK8sRecommendationSummary({
        accountId: props?.kubernetes?.id,
        category: 'Configuration',
        status: ['Open', 'InProgress'],
      })
      .then((res) => {
        setTotalBestPracticeCount(res?.data?.recommendation_aggregate.aggregate.count);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [props?.kubernetes?.id]);

  const handleTicketSuccess = () => {
    listBestPracticesRecommendations();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}.`);
  };

  const ruleOptions = Object.entries(RULE_LABEL_MAP).map(([k, v]) => ({ label: v, value: k }));
  const namespaceOptions = (namespaceFilter || []).map((n) => ({ label: n, value: n }));
  const severityOptions = RECOMMENDATION_SERVERITY.map((s) => ({ label: s, value: s }));
  const statusOptions = RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }));

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Remove Unused Volume - ' + RULE_LABEL_MAP[ticketData.rule_name] || ticketData.rule_name,
          description: getTicketDescription(ticketData),
          accountId: props?.kubernetes?.id,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData.id,
          type: 'kubernetes',
        }}
      />

      {enabledSummary && (
        <Box
          sx={{
            display: 'flex',
            flex: 1,
            flexDirection: 'row',
            gap: ds.space[3],
            '& > *': { maxWidth: `calc((100% - 3 * ${ds.space[3]}) / 4)` },
          }}
          mt={2}
          mb={2}
        >
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Total Recommendations'
              info={{ tooltip: 'Active best-practice recommendations across the cluster' }}
              value={Number.isFinite(totalBestPracticeCount) ? totalBestPracticeCount.toLocaleString() : totalBestPracticeCount ?? '—'}
            />
          </WidgetCard>
        </Box>
      )}

      <ListingLayout id='best-practices'>
        <ListingLayout.Toolbar
          title={props.heading === undefined ? 'Best Practices' : props.heading}
          data-testid='bp-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={props?.kubernetes?.id} jobName='popeye_scan' idPrefix='bp' />}
              <DsDropdownMenu
                align='end'
                size='sm'
                items={[
                  { id: 'export-csv', label: 'Download CSV', onSelect: () => handleExportDownload('csv') },
                  { id: 'export-xlsx', label: 'Download Excel (XLSX)', onSelect: () => handleExportDownload('xlsx') },
                ]}
                trigger={
                  <DsButton
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    icon={<FileDownloadOutlinedIcon />}
                    aria-label='Download'
                    id='bp-download'
                  />
                }
              />
            </>
          }
        >
          {enabledFilters && (
            <>
              <FilterDropdown
                id='bp-filter-rule'
                label='Rule Name'
                options={ruleOptions}
                value={ruleName ? ruleOptions.find((o) => o.value === ruleName) ?? null : null}
                onSelect={(_e, item) => {
                  setRuleName(item?.value || '');
                  setPage(0);
                }}
              />
              <FilterDropdown
                id='bp-filter-namespace'
                label='Namespace'
                options={namespaceOptions}
                value={namespace ? { label: namespace, value: namespace } : null}
                onSelect={(_e, item) => {
                  setNamespace(item?.value || '');
                  setPage(0);
                }}
              />
              <FilterDropdown
                id='bp-filter-severity'
                label='Severity'
                options={severityOptions}
                value={severity ? { label: severity, value: severity } : null}
                onSelect={(_e, item) => {
                  setSeverity(item?.value || '');
                  setPage(0);
                }}
              />
              <FilterDropdown
                id='bp-filter-status'
                label='Status'
                options={statusOptions}
                value={
                  recommendationStatus
                    ? { label: recommendationStatus === 'InProgress' ? 'In Progress' : recommendationStatus, value: recommendationStatus }
                    : null
                }
                onSelect={(_e, item) => {
                  setRecommendationStatus(item?.value || '');
                  setPage(0);
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={kubernetesBestPracticesTable}
            headers={BEST_PRACTICES_HEADER}
            tableData={kubernetesBestPractice}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesBestPracticeCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            tableHeadingCenter={['Severity']}
            stickyColumnIndex='8'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={nubiAccountId}
        queryPrefix={nubiQuery}
        context={{ type: 'cluster', data: { conversationId: nubiConversationId } }}
        apiMode='investigate'
        categorySource='Optimize'
        position='right'
        mode='overlay'
      />
    </>
  );
};

KubernetesBestPractices.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  enabledFilters: PropTypes.bool,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesBestPractices;
