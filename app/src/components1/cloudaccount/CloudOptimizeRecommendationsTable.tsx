import BoxLayout2 from '@components1/common/BoxLayout2';
import { useEffect, useState } from 'react';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { useRouter } from 'next/router';
import { getLast7Days } from '@lib/datetime';
import CloudAccountTable from './CloudAccountTable';
import { usePagination } from '@hooks/usePagination';
import { interpolateMitigations, buildDescriptionMarkdown } from '@api1/recommendation/data';
import apiRecommendations, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { Box, Grid, IconButton, Typography, CircularProgress } from '@mui/material';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { action } from '@utils/actionStyles';
import { useRecommendationCloudFilter } from '@hooks/useCloudFilters';
import { snakeToTitleCase, syncFilterFromQuery } from '@utils/common';
import Text from '@components1/common/format/Text';
import MarkDowns from '@components1/common/MarkDowns';
import Currency from '@components1/common/format/Currency';
import { DrilldownDetails, getTicketDescription } from './common';
import { AutoPilotGreyIcon, TicketsIcon } from '@assets';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { snackbar } from '@components1/common/snackbarService';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';
import ticketsApi from '@api1/tickets';
import useCurrencySymbol from '@hooks/useCurrencySymbol';
import AlarmCreationModal from './AlarmCreationModal';
import CustomButton from '@components1/common/NewCustomButton';
import { hasWriteAccess } from '@lib/auth';
import SafeIcon from '@components1/common/SafeIcon';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';
import SavingsPlanEvidence from '@components1/optimise-new/evidence/SavingsPlanEvidence';

export type OptimizeCategory = 'RightSizing' | 'Configuration' | 'Security' | 'InfraUpgrade';

// Helpers to detect non-meaningful resource identifiers (hex IDs, UUIDs)
const isHexOnly = (v: string) => /^[0-9a-f]+$/i.test(v);
const isUUID = (v: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v);
const isOpaqueId = (v: string) => isHexOnly(v) || isUUID(v);

interface CategoryConfig {
  tableId: string;
  ticketSubject: (ruleName: string) => string;
  showSavings: boolean;
  showAlarmModal: boolean;
  useSecurityHubTransform: boolean;
  useSummaryApi: boolean;
  showAccountFilter: boolean;
  getRecommendationText: (details: any, item: any) => string;
  getTicketSeverity?: (item: any) => string;
}

const CATEGORY_CONFIG: Record<OptimizeCategory, CategoryConfig> = {
  RightSizing: {
    tableId: 'cloudaccount-optimize-rightsizing-change',
    ticketSubject: (r) => `Cloud Optimization - ${r || 'Right Sizing Recommendation'}`,
    showSavings: true,
    showAlarmModal: false,
    useSecurityHubTransform: false,
    useSummaryApi: true,
    showAccountFilter: true,
    getRecommendationText: (details, item) => details.description || item.recommendation?.reason,
  },
  Configuration: {
    tableId: 'cloudaccount-optimize-configuration-change',
    ticketSubject: (r) => `Cloud Configuration - ${r || 'Configuration Recommendation'}`,
    showSavings: false,
    showAlarmModal: true,
    useSecurityHubTransform: false,
    useSummaryApi: false,
    showAccountFilter: false,
    getRecommendationText: (details, item) => details.recommendations?.[0] || item.recommendation?.reason,
  },
  Security: {
    tableId: 'cloudaccount-optimize-security-change',
    ticketSubject: (r) => `Cloud Security - ${r || 'Security Recommendation'}`,
    showSavings: false,
    showAlarmModal: false,
    useSecurityHubTransform: true,
    useSummaryApi: false,
    showAccountFilter: false,
    getRecommendationText: (details, item) => details.description || item.recommendation?.reason,
    getTicketSeverity: (item) => item?.severity || 'Medium',
  },
  InfraUpgrade: {
    tableId: 'cloudaccount-optimize-infra-upgrade',
    ticketSubject: (r) => `Cloud Infra Upgrade - ${r || 'Infrastructure Upgrade Recommendation'}`,
    showSavings: true,
    showAlarmModal: false,
    useSecurityHubTransform: false,
    useSummaryApi: true,
    showAccountFilter: false,
    getRecommendationText: (details, item) => details.recommendations?.[0] || item.recommendation?.reason,
  },
};

const CloudOptimizeRecommendationsTable = (props: {
  accountId: string;
  category: OptimizeCategory;
  serviceName?: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
  accountAccess?: string;
  isOptimisePage?: boolean;
  provider?: string;
}) => {
  const config = CATEGORY_CONFIG[props.category];
  const router = useRouter();
  const { assistantName } = useTenantBranding();

  const [recommendations, setRecommendations] = useState([]);
  const [recommendationsCount, setRecommendationsCount] = useState(0);
  const [totalRecommendationsCount, setTotalRecommendationsCount] = useState(0);
  const [totalEstimatedSavings, setTotalEstimatedSavings] = useState(0);
  const [selectedRuleName, setSelectedRuleName] = useState<{ label: string; value: string }[]>([]);
  const [selectedServiceName, setSelectedServiceName] = useState(props?.serviceName ?? '');
  const [selectedSeverity, setSelectedSeverity] = useState<string[]>([]);
  const [selectedStatus, setSelectedStatus] = useState<{ label: string; value: string }[]>([
    { label: 'Open', value: 'Open' },
    { label: 'In Progress', value: 'InProgress' },
  ]);
  const [selectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });
  const [selectedAccountId, setSelectedAccountId] = useState(props.accountId);
  const [accounts, setAccounts] = useState([]);
  const [ticketData, setTicketData] = useState({} as any);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [isAlarmCreationModalOpen, setIsAlarmCreationModalOpen] = useState(false);
  const [selectedRecommendation, setSelectedRecommendation] = useState<any>(null);
  const [applyingRecommendationId, setApplyingRecommendationId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadingTotal, setLoadingTotal] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');
  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const { allCluster } = useData();
  const currencySymbol = useCurrencySymbol(selectedAccountId);
  const [serviceNamesFilterWithRuleName, setServiceNamesFilterWithRuleName] = useState([] as { label: string; value: string }[]);

  const { ruleNamesFilter, serviceNamesFilter, severityFilter } = useRecommendationCloudFilter(selectedAccountId, {
    category: props.category,
    serviceName: props?.serviceName ?? '',
  });

  useEffect(() => {
    let active = true;
    if (selectedRuleName.length === 0 || !!props.serviceName) {
      setServiceNamesFilterWithRuleName(serviceNamesFilter);
    } else {
      apiRecommendations
        .listRecommendationFilter(selectedAccountId, ['resource_cloud_service'], {
          category: props.category,
          ruleName: selectedRuleName.map((r) => r.value),
        })
        .then((res: any) => {
          if (!active) return;
          const filters =
            res?.data?.data?.recommendation
              ?.filter((g: any) => g.resource_cloud_service)
              .map((e: any) => ({
                label: e.resource_cloud_service,
                value: e.resource_cloud_service,
              })) || [];
          setServiceNamesFilterWithRuleName(filters);
          setSelectedServiceName((prev) => {
            if (!prev) return '';
            return filters.find((f: any) => f.value === prev)?.value || '';
          });
        });
    }
    return () => {
      active = false;
    };
  }, [selectedRuleName, selectedAccountId, props.serviceName, props.category, serviceNamesFilter]);

  // Sync filter selections from router query once filter options are loaded
  useEffect(() => {
    setSelectedRuleName(syncFilterFromQuery(ruleNamesFilter as { label: string; value: string }[], router?.query?.ruleName, (f) => f.value));
    setPage(0);
  }, [ruleNamesFilter, router?.query?.ruleName]);

  useEffect(() => {
    setSelectedSeverity(syncFilterFromQuery(severityFilter, router?.query?.severity));
    setPage(0);
  }, [severityFilter, router?.query?.severity]);

  useEffect(() => {
    if (!router?.query?.status) return;
    const statusOptions = RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }));
    setSelectedStatus(syncFilterFromQuery(statusOptions, router?.query?.status, (f) => f.value));
    setPage(0);
  }, [router?.query?.status]);

  // Load accounts for the optimise page account filter (RightSizing only)
  useEffect(() => {
    if (props.isOptimisePage && config.showAccountFilter) {
      apiHome.getCloudAccounts(props.provider).then((res) => setAccounts(res));
    }
  }, [props.isOptimisePage, allCluster, props.provider, config.showAccountFilter]);

  const getAccountName = (id: string) => {
    const match: any = accounts.find((ac: any) => ac.id == id);
    return match?.account_name || id || '-';
  };

  const onRuleNamesFilterChange = (e: any) => {
    setSelectedRuleName(e?.target?.value ?? []);
    setPage(0);
  };

  const onServiceNamesFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedServiceName(e?.target?.value);
    setPage(0);
  };

  const onSeverityFilterChange = (e: any) => {
    setSelectedSeverity(e?.target?.value ?? []);
    setPage(0);
  };

  const onStatusFilterChange = (e: any) => {
    setSelectedStatus(e?.target?.value ?? []);
    setPage(0);
  };

  const onAccountFilterChange = (e: any) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
  };

  const getMenuItems = (item: any) => {
    if (config.showAlarmModal) {
      const items: any[] = [];
      // Only show "Create Alarm" for recommendations that have alarm_config and account is not read-only
      if (item?.recommendation?.alarm_config && props.accountAccess !== 'readonly' && hasWriteAccess()) {
        items.push({ icon: AutoPilotGreyIcon, disabled: false, label: 'Create Alarm', id: 0 });
      }
      items.push({ icon: TicketsIcon, disabled: false, label: 'Create Ticket', id: 1 });
      return items;
    }
    return [
      { icon: AutoPilotGreyIcon, disabled: true, label: 'Resolve', id: 0 },
      { icon: TicketsIcon, disabled: false, label: 'Create Ticket', id: 1 },
    ];
  };

  const onMenuClick = (menuItem: { id: number }, data: any) => {
    if (menuItem.id === 0) {
      // Create Alarm
      if (config.showAlarmModal) {
        setSelectedRecommendation(data);
        setIsAlarmCreationModalOpen(true);
      } else {
        setTicketData(data);
      }
    }
    if (menuItem.id === 1) {
      // Create Ticket
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const handleTicketSuccess = () => {
    setIsTicketCreateFormOpen(false);
    setRefreshKey((prev) => prev + 1);
  };

  const handleAlarmCreationSuccess = () => {
    // Refresh recommendations list after successful alarm creation
    setPage(0);
    setRefreshKey((prev) => prev + 1);
    // The useEffect will automatically reload the data
  };

  const handleApplyAlarmRecommendation = async (row: any) => {
    if (!row?.id) return;
    setApplyingRecommendationId(row.id);
    try {
      const response = await apiRecommendations.applyRecommendation(row.account_id ?? selectedAccountId, row.id, {
        reason: 'Creating CloudWatch alarm from Nudgebee recommendation',
      });
      if (response?.errors?.length) {
        snackbar.error(response.errors[0]?.message || 'Failed to create CloudWatch alarm');
        return;
      }
      snackbar.success('CloudWatch alarm created successfully');
      handleAlarmCreationSuccess();
    } catch (err) {
      const message = (err as any)?.response?.data?.message || (err as Error)?.message || 'Failed to create CloudWatch alarm';
      snackbar.error(message);
    } finally {
      setApplyingRecommendationId(null);
    }
  };

  const handleAskNubi = (e: React.MouseEvent, item: any, recommenedationDetails: any, objectName: string, serviceName: string) => {
    e.stopPropagation();
    const prompt = buildNubiOptimizePrompt({
      ruleName: recommenedationDetails?.title || snakeToTitleCase(item.rule_name || ''),
      category: item.category || '',
      severity: item.severity || 'Info',
      resourceName: objectName || item.objectName || item.resource_name || '',
      resourceType: serviceName || item.recommendation?.service_name || '',
      accountName: getAccountName(item.account_id),
      estimatedSavings: item.estimated_savings || undefined,
      brief: recommenedationDetails?.description || item.recommendation?.reason || undefined,
    });
    setNubiQuery(prompt);
    setNubiAccountId(item.account_id || selectedAccountId);
    setNubiConversationId(`recom_${item.id}`);
    setNubiSidebarVisible(true);
  };

  const handleTicketFailure = (error: string) => {
    snackbar.error(error || 'Failed to create ticket');
  };

  const closeAlarmCreationModal = () => {
    setIsAlarmCreationModalOpen(false);
    setSelectedRecommendation(null);
  };

  const buildRecommendationRow = (item: any, ticketReferenceMap: Map<string, any>): ICustomTable2Row[] => {
    let serviceName = '';
    let objectName = '';

    const objectParts = item.account_object_id.split(':');
    if (objectParts.length === 7) {
      serviceName = objectParts[2];
      objectName = objectParts[6];
    }

    // Clear IDs that aren't meaningful resource names (hex IDs, UUIDs)
    if (objectName && isOpaqueId(objectName)) objectName = '';
    if (item.resource_name && isOpaqueId(item.resource_name)) item.resource_name = '';
    if (!item.objectName && objectName) item.objectName = objectName;
    if (!item.serviceName && serviceName) item.serviceName = serviceName;

    // Fallback instance name from recommendation JSON (e.g. Azure Advisor's impacted_value)
    const impactedValue = item.recommendation?.impacted_value;
    const instanceFallback =
      (impactedValue && !isUUID(impactedValue) ? impactedValue : undefined) ||
      item.recommendation?.ext_vmsize ||
      item.recommendation?.ext_sku ||
      item.recommendation?.current_resource_summary ||
      item.recommendation?.recommended_resource_summary;

    let recommenedationDetails: any = apiRecommendations.getRecommendationDetails(item.category, item.rule_name) || {};

    // SecurityHub recommendations require a special data transform
    if (config.useSecurityHubTransform && serviceName === 'securityhub') {
      recommenedationDetails = {
        title: item.recommendation?.Title,
        description: item.recommendation?.Description,
        serviceName: item.recommendation?.ServiceName,
        recommendations: [],
        mitigations: [`${item.recommendation?.Remediation?.Recommendation.Text} - ${item.recommendation?.Remediation?.Recommendation.Url}`],
      };
    }

    // Alarm recommendations: build details from JSONB data when catalog has no entry
    if (!recommenedationDetails?.description && item.rule_name?.endsWith('_alarm_missing')) {
      const rec = item.recommendation || {};
      const alarmConfig = rec.alarm_config || {};
      const metricName = rec.metric_name || alarmConfig.metric_name || '';
      const threshold = rec.threshold ?? alarmConfig.threshold;
      const namespace = alarmConfig.namespace || '';
      const compOp = alarmConfig.comparison_operator || '';
      const period = alarmConfig.period ? `${alarmConfig.period}s` : '';
      const evalPeriods = alarmConfig.evaluation_periods || '';
      const statistic = alarmConfig.statistic || '';

      const description = rec.reason || '';

      const mitigationParts: string[] = [];
      mitigationParts.push('Create a CloudWatch/Monitor alarm with the following configuration:\n');
      if (namespace) {
        mitigationParts.push(`- **Namespace:** \`${namespace}\``);
      }
      if (metricName) {
        mitigationParts.push(`- **Metric:** \`${metricName}\``);
      }
      if (statistic) {
        mitigationParts.push(`- **Statistic:** ${statistic}`);
      }
      if (threshold !== undefined) {
        mitigationParts.push(`- **Threshold:** ${threshold} (${compOp.replace(/([A-Z])/g, ' $1').trim()})`);
      }
      if (period) {
        mitigationParts.push(`- **Period:** ${period}`);
      }
      if (evalPeriods) {
        mitigationParts.push(`- **Evaluation Periods:** ${evalPeriods}`);
      }

      recommenedationDetails = {
        ...recommenedationDetails,
        title: recommenedationDetails?.title || snakeToTitleCase(item.rule_name),
        description,
        recommendations: [],
        mitigations: [mitigationParts.join('\n')],
      };
    }

    const ticketLink = ticketReferenceMap.has(item.id) ? (
      <CustomTicketLink ticketURL={ticketReferenceMap.get(item.id)?.url} ticketID={ticketReferenceMap.get(item.id)?.ticket_id} />
    ) : null;

    const recommendationValue = config.getRecommendationText(recommenedationDetails, item);

    const data: ICustomTable2Row[] = [];

    // Severity + Updated At
    data.push({
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '0px' }}>
          <SeverityIcon severityType={item.severity} />
          <Datetime value={item.updated_at} sx={{ fontSize: '11px' }} />
        </Box>
      ),
      data: item.severity,
    });

    // Rule Name cell
    data.push({
      component: (
        <Box>
          {ClusterNameWithRegion({
            name: recommenedationDetails.title || snakeToTitleCase(item.rule_name),
            hideIcon: true,
            showAutoEllipsis: true,
            lineClamp: 2,
            maxWidth: '100%',
            region: ticketLink ?? <></>,
          })}
          <Text value={snakeToTitleCase(item.rule_name)} secondaryText showAutoEllipsis />
          {props.isOptimisePage && config.showAccountFilter && <Text value={`acc- ${getAccountName(item.account_id)}`} secondaryText />}
        </Box>
      ),
      drilldownQuery: { recommendation: item, recommenedationDetails },
      data: item.rule_name,
    });

    // Instance (with Service name as secondary)
    const serviceNameValue = item.recommendation?.service_name || recommenedationDetails.serviceName || serviceName;
    data.push({
      component: (
        <Box>
          <Text value={objectName || item.objectName || item.resource_name || instanceFallback} showAutoEllipsis lineClamp={2} />
          {serviceNameValue && <Text value={`Svc: ${serviceNameValue}`} secondaryText showAutoEllipsis />}
        </Box>
      ),
      data: item.objectName || item.resource_name,
    });

    // Recommendation
    data.push({
      component: <Text showAutoEllipsis lineClamp={3} value={recommendationValue} />,
      data: recommendationValue,
    });

    // Savings (RightSizing and InfraUpgrade only)
    if (config.showSavings) {
      data.push({ component: <Currency value={item.estimated_savings} precison={1} prefix={currencySymbol || '$'} suffix='/yr' /> });
    }

    // Actions menu
    data.push({
      component: (
        <Box display='flex' justifyContent='flex-end' flexDirection='row' alignItems='center' gap='4px'>
          <CustomTooltip title={`Ask ${assistantName}`} placement='top'>
            <IconButton
              size='small'
              data-testid={`cloud-ask-nubi-${item.id}`}
              onClick={(e) => handleAskNubi(e, item, recommenedationDetails, objectName, serviceName)}
              sx={{ ...action.nubi }}
            >
              <SafeIcon src={getNubiIconUrl()} alt={`Ask ${assistantName}`} width={16} height={16} />
            </IconButton>
          </CustomTooltip>
          <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />
        </Box>
      ),
    });

    return data;
  };

  // Fetch paginated recommendations
  useEffect(() => {
    if (!selectedAccountId && !props.isOptimisePage) return;
    setLoading(true);
    apiRecommendations
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: props.category,
        ruleName: selectedRuleName.map((f) => f.value),
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        serviceName: selectedServiceName,
        severity: selectedSeverity,
        status: selectedStatus.length > 0 ? selectedStatus.map((s) => s.value) : undefined,
      })
      .then(async (res: any) => {
        const recommendations = res.data?.recommendation || [];

        // Fetch ticket summaries for all recommendations
        const ticketReferenceMap = new Map();
        const uniqueReferenceIds = new Set<string>();
        recommendations.forEach((item: any) => {
          if (item.id) uniqueReferenceIds.add(item.id);
        });
        const references = Array.from(uniqueReferenceIds);
        if (references.length > 0) {
          try {
            const ticketRes: any = await ticketsApi.listTicketsSummary({ reference_id: references });
            ticketRes?.data?.tickets?.forEach((element: any) => {
              ticketReferenceMap.set(element.reference_id, element);
            });
          } catch (err) {
            console.error('Error fetching ticket summaries', err);
          }
        }

        const tableData = recommendations.map((item: any) => buildRecommendationRow(item, ticketReferenceMap));

        setRecommendations(tableData);
        setRecommendationsCount(res.data?.recommendation_aggregate?.aggregate?.count ?? 0);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, [
    selectedAccountId,
    page,
    rowsPerPage,
    selectedRuleName,
    selectedServiceName,
    selectedSeverity,
    selectedStatus,
    currencySymbol,
    refreshKey,
    props.category,
    props.isOptimisePage,
  ]);

  // Fetch summary totals (count + optional savings)
  useEffect(() => {
    if (!selectedAccountId && !props.isOptimisePage) return;
    setLoadingTotal(true);
    const promise = config.useSummaryApi
      ? apiRecommendations.getK8sRecommendationSummary({
          accountId: selectedAccountId,
          category: props.category,
          status: selectedStatus.length > 0 ? selectedStatus.map((s) => s.value) : undefined,
          serviceName: props?.serviceName,
        })
      : apiRecommendations.getK8sRecommendation({
          accountId: selectedAccountId,
          category: props.category,
          serviceName: props?.serviceName || '',
          status: selectedStatus.length > 0 ? selectedStatus.map((s) => s.value) : undefined,
          limit: 1,
          offset: 0,
        });

    promise
      .then((res: any) => {
        if (config.useSummaryApi) {
          setTotalEstimatedSavings(res?.data?.recommendation_aggregate?.aggregate?.sum?.estimated_savings ?? 0);
        }
        setTotalRecommendationsCount(res?.data?.recommendation_aggregate?.aggregate?.count ?? 0);
      })
      .catch(console.error)
      .finally(() => setLoadingTotal(false));
  }, [selectedAccountId, selectedStatus, props?.serviceName, props.category]);

  const SEVERITY_TOOLTIP =
    'Indicates the urgency level of this recommendation. Critical items represent the highest impact. Timestamp shows when this recommendation was last detected.';
  const RULE_NAME_TOOLTIP =
    'The specific optimization rule that triggered this recommendation. Multiple recommendations can share the same rule but apply to different resources.';

  const TABLE_COLUMNS = config.showSavings
    ? [
        { name: 'Severity', width: '8%', info: SEVERITY_TOOLTIP },
        { name: 'Rule Name', width: '16%', info: RULE_NAME_TOOLTIP },
        { name: 'Instance', width: '24%' },
        { name: 'Recommendation', width: '35%' },
        { name: 'Savings', width: '10%' },
        { name: '', width: '5%' },
      ]
    : [
        { name: 'Severity', width: '10%', info: SEVERITY_TOOLTIP },
        { name: 'Rule Name', width: '18%', info: RULE_NAME_TOOLTIP },
        { name: 'Instance', width: '22%' },
        { name: 'Recommendation', width: '40%' },
        { name: '', width: '5%' },
      ];

  const filterOptions: any[] = [
    ...(props.isOptimisePage && config.showAccountFilter
      ? [
          {
            type: 'dropdown',
            enabled: true,
            options: accounts.map((acc: any) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value })),
            onSelect: onAccountFilterChange,
            label: 'Account',
            value: selectedAccountId,
          },
        ]
      : []),
    {
      type: 'multi-dropdown',
      enabled: true,
      options: ruleNamesFilter,
      onSelect: onRuleNamesFilterChange,
      minWidth: '150px',
      label: 'Rule Name',
      value: selectedRuleName,
    },
    {
      type: 'multi-dropdown',
      enabled: true,
      options: severityFilter,
      onSelect: onSeverityFilterChange,
      minWidth: '150px',
      label: 'Severity',
      value: selectedSeverity,
    },
    {
      type: 'multi-dropdown',
      enabled: true,
      options: RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s })),
      onSelect: onStatusFilterChange,
      minWidth: '150px',
      label: 'Status',
      value: selectedStatus,
    },
  ];

  if (!props?.serviceName) {
    filterOptions.push({
      type: 'dropdown',
      enabled: true,
      options: serviceNamesFilterWithRuleName,
      onSelect: onServiceNamesFilterChange,
      minWidth: '150px',
      label: 'Service Name',
      value: selectedServiceName,
    });
  }

  const tableId = config.tableId;

  return (
    <>
      <Box sx={{ padding: '0px 0px 16px 0px' }}>
        <Grid container spacing={2}>
          <Grid item xs={12} sm={6} md={2}>
            <SummaryWidget
              title='Total Recommendations'
              value={loadingTotal ? <CircularProgress color='inherit' size={20} /> : totalRecommendationsCount}
              variant='default'
              size='small'
            />
          </Grid>
          {config.showSavings && (
            <Grid item xs={12} sm={6} md={2}>
              <SummaryWidget
                title='Estimated Savings'
                suffix='/yr'
                size='small'
                value={
                  loadingTotal ? (
                    <CircularProgress color='inherit' size={20} />
                  ) : (
                    <Currency
                      value={totalEstimatedSavings * 12}
                      precison={0}
                      prefix={currencySymbol || '$'}
                      withTooltip={false}
                      sx={{ fontSize: '20px', lineHeight: '28px', fontWeight: 600 }}
                    />
                  )
                }
                variant='savings'
              />
            </Grid>
          )}
        </Grid>
      </Box>

      <BoxLayout2
        heading=''
        id={tableId}
        filterOptions={filterOptions}
        sharingOptions={{
          download: { enabled: true, onClick: () => ({ tableId }) },
          sharing: { enabled: false, onClick: null },
        }}
        dateTimeRange={{
          enabled: false,
          onChange: () => true,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
      >
        <CloudAccountTable
          id={tableId}
          headers={TABLE_COLUMNS}
          data={recommendations}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={recommendationsCount}
          expandable={{
            tabs: [
              { componentFn: optimizeEvidance, text: 'Evidence' },
              { componentFn: optimizeDescription, text: 'Description' },
              {
                componentFn: (_accountId: any, drilldownQuery: any) => {
                  const row = drilldownQuery?.recommendation;
                  const resolvedStatuses = ['Closed', 'Dismissed', 'Archive'];
                  const isActionableStatus = !row?.status || !resolvedStatuses.includes(row.status);
                  const canApplyAlarm =
                    config.showAlarmModal &&
                    !!row?.recommendation?.alarm_config &&
                    props.accountAccess !== 'readonly' &&
                    hasWriteAccess(row?.account_id) &&
                    isActionableStatus;
                  const isApplying = applyingRecommendationId === row?.id;
                  return optimizeMitigation(_accountId, drilldownQuery, canApplyAlarm, isApplying, () => handleApplyAlarmRecommendation(row));
                },
                text: 'Mitigation',
              },
            ],
          }}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          stickyColumnIndex={props.stickyColumnIndex}
          tableHeadingCenter={props.tableHeadingCenter}
        />
      </BoxLayout2>

      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: config.ticketSubject(ticketData?.rule_name),
          description: getTicketDescription(ticketData),
          accountId: selectedAccountId,
          ...(config.getTicketSeverity ? { severity: config.getTicketSeverity(ticketData) } : {}),
        }}
        ticketUrl={{}}
        reference={{ id: ticketData?.id, type: 'aws' }}
      />

      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={nubiAccountId}
        queryPrefix={nubiQuery}
        context={{ type: 'general', data: { conversationId: nubiConversationId } }}
        apiMode='investigate'
        categorySource='Optimize'
        position='right'
        mode='overlay'
      />

      {config.showAlarmModal && selectedRecommendation && (
        <AlarmCreationModal
          open={isAlarmCreationModalOpen}
          onClose={closeAlarmCreationModal}
          recommendation={selectedRecommendation}
          accountId={selectedAccountId}
          onSuccess={handleAlarmCreationSuccess}
          accountAccess={props.accountAccess}
        />
      )}
    </>
  );
};

// Shared drilldown panel functions used for all categories

const SAVINGS_PLAN_RULES = new Set([
  'aws_native_purchase_savings_plans',
  'aws_native_purchase_reserved_instances',
  'aws_native_ce_ri_recommendation',
  'aws_native_ce_savings_plan_recommendation',
]);

function optimizeEvidance(_accountId: any, drilldownQuery: any) {
  if (drilldownQuery?.recommenedationDetails?.drilldownInvestigation) {
    return drilldownQuery?.recommenedationDetails?.drilldownInvestigation(drilldownQuery?.recommendation);
  }

  const ruleName = drilldownQuery?.recommendation?.rule_name;
  if (ruleName && SAVINGS_PLAN_RULES.has(ruleName)) {
    return (
      <Box sx={{ background: 'white', borderRadius: '8px' }}>
        <SavingsPlanEvidence
          recommendation={drilldownQuery?.recommendation?.recommendation}
          ruleName={ruleName}
          estimatedSavings={drilldownQuery?.recommendation?.estimated_savings}
        />
      </Box>
    );
  }

  return (
    <Box sx={{ background: 'white', borderRadius: '8px', p: 3 }}>
      <DrilldownDetails data={drilldownQuery?.recommendation?.recommendation} showCopyIconOnHover={true} />
    </Box>
  );
}

function optimizeDescription(_accountId: any, drilldownQuery: any) {
  const markdown = buildDescriptionMarkdown(drilldownQuery?.recommenedationDetails);
  return (
    <Box sx={{ background: 'white', borderRadius: '8px', p: 1 }}>
      {markdown ? (
        <MarkDowns data={markdown} sx={{ padding: 0, '& p:last-child': { marginBottom: 0 } }} allowExecutable={false} onLinkClick={null} />
      ) : (
        <Typography color='#999'>No description available</Typography>
      )}
    </Box>
  );
}

function optimizeMitigation(_accountId: any, drilldownQuery: any, canApplyAlarm = false, isApplying = false, onApply?: () => void) {
  const mitigations = interpolateMitigations(drilldownQuery?.recommenedationDetails?.mitigations, drilldownQuery?.recommendation);
  const markdowns = mitigations?.length ? mitigations.join('\n\n') : null;
  return (
    <Box sx={{ background: 'white', borderRadius: '8px', p: 1 }}>
      {markdowns ? (
        <MarkDowns data={markdowns} sx={{ padding: 0, '& p:last-child': { marginBottom: 0 } }} allowExecutable={false} onLinkClick={null} />
      ) : (
        <Typography color='#999'>No mitigation steps available</Typography>
      )}
      {canApplyAlarm && onApply && (
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 1, mt: 1.5 }}>
          <Typography variant='body2'>Click</Typography>
          <CustomButton
            size='Small'
            text={isApplying ? 'Creating Alarm...' : 'Apply'}
            onClick={onApply}
            loading={isApplying}
            data-testid='mitigation-apply-create-alarm-btn'
          />
          <Typography variant='body2'>
            to automatically create the alarm with the default configuration, or open the actions menu (⋮) on this row and select{' '}
            <strong>Create Alarm</strong>.
          </Typography>
        </Box>
      )}
    </Box>
  );
}

export default CloudOptimizeRecommendationsTable;
