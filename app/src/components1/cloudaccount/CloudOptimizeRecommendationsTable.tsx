import { useEffect, useState } from 'react';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { useRouter } from 'next/router';
import { usePagination } from '@hooks/usePagination';
import { interpolateMitigations, buildDescriptionMarkdown } from '@api1/recommendation/data';
import apiRecommendations, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { Box, Typography } from '@mui/material';
import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import { CostCallout } from '@components1/ds/CostCallout';
import { ds } from 'src/utils/colors';
import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import DsTooltip from '@components1/ds/Tooltip';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { SeverityIcon as DsSeverityIcon } from '@components1/ds/SeverityIcon';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import Datetime from '@common-new/format/Datetime';
import { useRecommendationCloudFilter } from '@hooks/useCloudFilters';
import { snakeToTitleCase, syncFilterFromQuery } from '@utils/common';
import Text from '@common-new/format/Text';
import MarkDowns from '@components1/common/MarkDowns';
import Currency from '@common-new/format/Currency';
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
import { hasWriteAccess } from '@lib/auth';
import SafeIcon from '@components1/common/SafeIcon';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import SavingsPlanEvidence from '@components1/optimise-new/evidence/SavingsPlanEvidence';

type CanonicalSeverityLevel = 'critical' | 'high' | 'medium' | 'low' | 'info';
const toCanonicalSeverityLevel = (severity: string | undefined): CanonicalSeverityLevel => {
  const s = (severity || '').toLowerCase();
  if (s === 'critical' || s === 'highest' || s === 'firing') return 'critical';
  if (s === 'high') return 'high';
  if (s === 'medium') return 'medium';
  if (s === 'low' || s === 'lowest') return 'low';
  return 'info';
};

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
  recommendationsTooltip: string;
  savingsTooltip?: string;
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
    recommendationsTooltip: 'CPU and memory adjustments for over- or under-provisioned resources, derived from observed usage.',
    savingsTooltip: 'Total estimated annual savings if all right-sizing recommendations are applied',
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
    recommendationsTooltip: 'Misconfigurations and policy violations flagged against cloud and organizational best practices.',
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
    recommendationsTooltip:
      'Security findings and remediation guidance surfaced by cloud-native scanners (e.g. AWS Security Hub, GCP SCC, Azure Defender).',
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
    recommendationsTooltip: 'Upgrade recommendations for outdated engine versions, deprecated platforms, and end-of-life instance families.',
    savingsTooltip: 'Total estimated annual savings if all infrastructure upgrade recommendations are applied',
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
    setSelectedRuleName((prev) => {
      const next = syncFilterFromQuery(ruleNamesFilter as { label: string; value: string }[], router?.query?.ruleName, (f) => f.value);
      if (prev.length === 0 && next.length === 0) return prev;
      return next;
    });
    setPage(0);
  }, [ruleNamesFilter, router?.query?.ruleName]);

  useEffect(() => {
    setSelectedSeverity((prev) => {
      const next = syncFilterFromQuery(severityFilter, router?.query?.severity);
      if (prev.length === 0 && next.length === 0) return prev;
      return next;
    });
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
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 0 }}>
          <DsSeverityIcon level={toCanonicalSeverityLevel(item.severity)} aria-label={`Severity: ${item.severity || 'unknown'}`} />
          <Datetime value={item.updated_at} sx={{ fontSize: ds.text.caption }} />
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

    // Actions menu (Ask NuBi + per-row dropdown)
    const menuItemsConfig = getMenuItems(item);
    data.push({
      component: (
        <Box display='flex' justifyContent='flex-end' flexDirection='row' alignItems='center' gap={ds.space[1]}>
          <DsTooltip title={`Ask ${assistantName}`} placement='top'>
            <span>
              <DsButton
                tone='ghost'
                size='xs'
                composition='icon-only'
                aria-label={`Ask ${assistantName}`}
                id={`cloud-ask-nubi-${item.id}`}
                icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                onClick={(e: React.MouseEvent) => handleAskNubi(e, item, recommenedationDetails, objectName, serviceName)}
              />
            </span>
          </DsTooltip>
          <DsDropdownMenu
            align='end'
            size='sm'
            items={menuItemsConfig.map((m: any) => ({
              id: `optimize-action-${item.id}-${m.id}`,
              label: m.label,
              icon: m.icon ? <SafeIcon src={m.icon} alt='' width={14} height={14} /> : undefined,
              disabled: m.disabled,
              onSelect: () => onMenuClick({ id: m.id }, item),
            }))}
            trigger={<DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon />} />}
          />
        </Box>
      ),
    });

    return data;
  };

  // Fetch paginated recommendations
  useEffect(() => {
    if (!selectedAccountId && !props.isOptimisePage) return;
    // Guard against out-of-order responses: filter options load asynchronously, so this
    // effect can fire several times in quick succession with different filter combinations.
    // Without this flag a slower earlier request can resolve last and overwrite the correct
    // (latest) result, showing stale/wrong data.
    let active = true;
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
        if (!active) return;
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

        if (!active) return;

        const tableData = recommendations.map((item: any) => buildRecommendationRow(item, ticketReferenceMap));

        setRecommendations(tableData);
        setRecommendationsCount(res.data?.recommendation_aggregate?.aggregate?.count ?? 0);
        setLoading(false);
      })
      .catch(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
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

  const tableId = config.tableId;
  const accountOptions = accounts.map((acc: any) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value }));
  const statusOptions = RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }));

  return (
    <>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'row',
          gap: ds.space[3],
          pb: ds.space[4],
          '& > *': { maxWidth: `calc((100% - 3 * ${ds.space[3]}) / 4)` },
        }}
      >
        <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
          <Stat
            size='md'
            label='Total Recommendations'
            info={{ tooltip: config.recommendationsTooltip }}
            value={loadingTotal ? '…' : totalRecommendationsCount}
          />
        </WidgetCard>
        {config.showSavings && (
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Estimated Savings'
              info={{ tooltip: config.savingsTooltip }}
              value={
                loadingTotal ? (
                  '…'
                ) : (
                  <CostCallout
                    size='lg'
                    tone='high-savings'
                    value={totalEstimatedSavings * 12}
                    currency={currencySymbol === '₹' ? 'INR' : 'USD'}
                    period='/ yr'
                  />
                )
              }
            />
          </WidgetCard>
        )}
      </Box>

      <ListingLayout id={tableId}>
        <ListingLayout.Toolbar
          data-testid={`${tableId}-filter-toolbar`}
          actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />}
        >
          {props.isOptimisePage && config.showAccountFilter && (
            <FilterDropdown
              id={`${tableId}-filter-account`}
              label='Account'
              options={accountOptions}
              value={accountOptions.find((o) => o.value === selectedAccountId) ?? null}
              onSelect={(_e: any, item: any) => onAccountFilterChange({ target: { value: item?.value || '' } })}
            />
          )}
          <FilterDropdown
            id={`${tableId}-filter-rule-name`}
            label='Rule Name'
            multiple
            options={ruleNamesFilter}
            value={selectedRuleName}
            onSelect={(_e: any, items: any) => onRuleNamesFilterChange({ target: { value: Array.isArray(items) ? items : [] } })}
          />
          <FilterDropdown
            id={`${tableId}-filter-severity`}
            label='Severity'
            multiple
            options={(severityFilter || []).map((s: any) => ({ label: s, value: s }))}
            value={(selectedSeverity || []).map((s: any) => ({ label: s, value: s }))}
            onSelect={(_e: any, items: any) =>
              onSeverityFilterChange({ target: { value: (Array.isArray(items) ? items : []).map((it: any) => it.value) } })
            }
          />
          <FilterDropdown
            id={`${tableId}-filter-status`}
            label='Status'
            multiple
            options={statusOptions}
            value={selectedStatus}
            onSelect={(_e: any, items: any) => onStatusFilterChange({ target: { value: Array.isArray(items) ? items : [] } })}
          />
          {!props?.serviceName && (
            <FilterDropdown
              id={`${tableId}-filter-service-name`}
              label='Service Name'
              options={serviceNamesFilterWithRuleName}
              value={serviceNamesFilterWithRuleName.find((o) => o.value === selectedServiceName) ?? null}
              onSelect={(_e: any, item: any) => onServiceNamesFilterChange({ target: { value: item?.value || '' } } as any)}
            />
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable2
            id={tableId}
            headers={TABLE_COLUMNS}
            tableData={recommendations}
            rowsPerPage={rowsPerPage}
            onPageChange={changePage}
            totalRows={recommendationsCount}
            expandable={{
              tabs: [
                { componentFn: optimizeEvidance, text: 'Evidence' },
                { componentFn: optimizeDescription, text: 'Description' },
                {
                  componentFn: (_option: any, drilldownQuery: any) => {
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
                    return optimizeMitigation(_option, drilldownQuery, canApplyAlarm, isApplying, () => handleApplyAlarmRecommendation(row));
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
        </ListingLayout.Body>
      </ListingLayout>

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
        query={nubiQuery}
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
      <Box sx={{ background: ds.background[100], borderRadius: ds.radius.md }}>
        <SavingsPlanEvidence
          recommendation={drilldownQuery?.recommendation?.recommendation}
          ruleName={ruleName}
          estimatedSavings={drilldownQuery?.recommendation?.estimated_savings}
        />
      </Box>
    );
  }

  return (
    <Box sx={{ background: ds.background[100], borderRadius: ds.radius.md, p: ds.space[5] }}>
      <DrilldownDetails data={drilldownQuery?.recommendation?.recommendation} showCopyIconOnHover={true} />
    </Box>
  );
}

function optimizeDescription(_accountId: any, drilldownQuery: any) {
  const markdown = buildDescriptionMarkdown(drilldownQuery?.recommenedationDetails);
  return (
    <Box sx={{ background: ds.background[100], borderRadius: ds.radius.md, p: ds.space[2] }}>
      {markdown ? (
        <MarkDowns data={markdown} sx={{ padding: 0, '& p:last-child': { marginBottom: 0 } }} allowExecutable={false} onLinkClick={null} />
      ) : (
        <Typography color={ds.gray[500]}>No description available</Typography>
      )}
    </Box>
  );
}

function optimizeMitigation(_accountId: any, drilldownQuery: any, canApplyAlarm = false, isApplying = false, onApply?: () => void) {
  const mitigations = interpolateMitigations(drilldownQuery?.recommenedationDetails?.mitigations, drilldownQuery?.recommendation);
  const markdowns = mitigations?.length ? mitigations.join('\n\n') : null;
  return (
    <Box sx={{ background: ds.background[100], borderRadius: ds.radius.md, p: ds.space[2] }}>
      {markdowns ? (
        <MarkDowns data={markdowns} sx={{ padding: 0, '& p:last-child': { marginBottom: 0 } }} allowExecutable={false} onLinkClick={null} />
      ) : (
        <Typography color={ds.gray[500]}>No mitigation steps available</Typography>
      )}
      {canApplyAlarm && onApply && (
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[2], mt: ds.space[3] }}>
          <Typography variant='body2'>Click</Typography>
          <DsButton size='sm' tone='primary' onClick={onApply} loading={isApplying} id='mitigation-apply-create-alarm-btn'>
            {isApplying ? 'Creating Alarm...' : 'Apply'}
          </DsButton>
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
