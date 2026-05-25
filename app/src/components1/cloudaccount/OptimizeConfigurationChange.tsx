import BoxLayout2 from '@components1/common/BoxLayout2';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { interpolateMitigations, buildDescriptionMarkdown } from '@api1/recommendation/data';
import { getLast7Days } from '@lib/datetime';
import CloudAccountTable from './CloudAccountTable';
import { usePagination } from '@hooks/usePagination';
import apiRecommendations from '@api1/recommendation';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { Box, Grid, Typography, CircularProgress } from '@mui/material';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { action } from 'src/utils/actionStyles';
import { useRecommendationCloudFilter } from '@hooks/useCloudFilters';
import { snakeToTitleCase } from 'src/utils/common';
import Text from '@components1/common/format/Text';
import MarkDowns from '@components1/common/MarkDowns';
import { DrilldownDetails, getTicketDescription } from './common';
import { AutoPilotGreyIcon, TicketsIcon } from '@assets';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { snackbar } from '@components1/common/snackbarService';
import ticketsApi from '@api1/tickets';
import AlarmCreationModal from './AlarmCreationModal';
import { hasWriteAccess } from '@lib/auth';
const isHexOnly = (v: string) => /^[0-9a-f]+$/i.test(v);
const isUUID = (v: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v);
const isOpaqueId = (v: string) => isHexOnly(v) || isUUID(v);

const TABLE_COLUMNS = [
  { name: 'Rule Name', width: '18%' },
  { name: 'Instance', width: '20%' },
  { name: 'Recommendation', width: '40%' },
  { name: 'Severity', width: '7%' },
  { name: 'Updated At', sortEnabled: true },
  '',
];

// Menu items are dynamic based on recommendation type
const getMenuItems = (recommendation: { recommendation?: { alarm_config?: unknown } }, accountAccess?: string) => {
  const menuItems = [];
  const isReadOnly = accountAccess === 'readonly';

  // Only show "Create Alarm" for recommendations that have alarm_config and account is not read-only
  if (recommendation?.recommendation?.alarm_config && !isReadOnly && hasWriteAccess()) {
    menuItems.push({
      icon: AutoPilotGreyIcon,
      disabled: false,
      label: 'Create Alarm',
      id: 0,
    });
  }

  // Always show "Create Ticket"
  menuItems.push({
    icon: TicketsIcon,
    disabled: false,
    label: 'Create Ticket',
    id: 1,
  });

  return menuItems;
};

const OptimizeConfigurationChange = (props: {
  accountId: string;
  serviceName?: string;
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
  accountAccess?: string;
}) => {
  const router = useRouter();
  const getValidParam = (param: any, defaultValue: string = '') => {
    if (!param || param === 'undefined' || param === 'null' || param === '') return defaultValue;
    return param;
  };
  const [recommendations, setRecommendations] = useState([]);
  const [recommendationsCount, setRecommendationsCount] = useState(0);
  const [totalRecommendationsCount, setTotalRecommendationsCount] = useState(0);
  const [selectedRuleName, setSelectedRuleName] = useState<{ label: string; value: string }[]>([]);
  const [selectedServiceName, setSelectedServiceName] = useState(props?.serviceName ?? '');
  const [selectedSeverity, setSelectedSeverity] = useState(getValidParam(router?.query?.severity));
  const [selectedDateRange, _setSelectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const [_ticketData, setTicketData] = useState({} as any);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [isAlarmCreationModalOpen, setIsAlarmCreationModalOpen] = useState(false);
  const [selectedRecommendation, setSelectedRecommendation] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [loadingTotal, setLoadingTotal] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const { page, rowsPerPage, changePage, setPage } = usePagination(10);

  const cloudOptimizeEventsTable = 'cloudaccount-optimize-configuration-change';
  const { ruleNamesFilter, serviceNamesFilter, severityFilter } = useRecommendationCloudFilter(props.accountId, {
    category: 'Configuration',
    serviceName: props?.serviceName || '',
  });

  useEffect(() => {
    if (!ruleNamesFilter?.length) return;
    const raw = router?.query?.ruleName;
    if (!raw) return;
    let rawValues: string[];
    if (Array.isArray(raw)) {
      rawValues = raw;
    } else if (raw.includes(',')) {
      rawValues = raw.split(',');
    } else {
      rawValues = [raw];
    }
    const routerValues = new Set(rawValues);
    const filtered = (ruleNamesFilter as { label: string; value: string }[]).filter((f) => routerValues.has(f.value));
    if (filtered.length > 0) {
      setSelectedRuleName(filtered);
      setPage(0);
    }
  }, [ruleNamesFilter, router?.query?.ruleName]);

  const handleChange = () => {
    return true;
  };

  const onRuleNamesFilterChange = (e: any) => {
    setSelectedRuleName(e?.target?.value || []);
    setPage(0);
  };

  const onServiceNamesFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedServiceName(e?.target?.value);
    setPage(0);
  };

  const onSeverityFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedSeverity(e?.target?.value);
    setPage(0);
  };

  const onMenuClick = (menuItem: { id: number }, data: any) => {
    if (menuItem.id === 0) {
      // Create Alarm
      setSelectedRecommendation(data);
      setIsAlarmCreationModalOpen(true);
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

  const handleTicketFailure = (error: string) => {
    snackbar.error(error || 'Failed to create ticket');
  };

  const closeAlarmCreationModal = () => {
    setIsAlarmCreationModalOpen(false);
    setSelectedRecommendation(null);
  };

  const handleAlarmCreationSuccess = () => {
    // Refresh recommendations list after successful alarm creation
    setPage(0);
    // The useEffect will automatically reload the data
  };

  useEffect(() => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiRecommendations
      .getK8sRecommendation({
        accountId: props?.accountId,
        category: 'Configuration',
        ruleName: selectedRuleName.map((f) => f.value),
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        serviceName: selectedServiceName,
        severity: selectedSeverity,
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

        const ec2ResourceData = recommendations.map((item: any) => {
          let serviceName = '';
          let objectName = '';

          const objectParts = item.account_object_id.split(':');
          if (objectParts.length == 7) {
            serviceName = objectParts[2];
            objectName = objectParts[6];
            //objectType = objectParts[5];
          }

          // Clear IDs that aren't meaningful resource names (hex IDs, UUIDs)
          if (objectName && isOpaqueId(objectName)) {
            objectName = '';
          }
          if (item.resource_name && isOpaqueId(item.resource_name)) {
            item.resource_name = '';
          }

          if (!item.objectName && objectName) {
            item.objectName = objectName;
          }
          if (!item.serviceName && serviceName) {
            item.serviceName = serviceName;
          }

          // Fallback instance name from recommendation JSON (e.g. Azure Advisor's impacted_value)
          const impactedValue = item.recommendation?.impacted_value;
          const instanceFallback =
            (impactedValue && !isUUID(impactedValue) ? impactedValue : undefined) ||
            item.recommendation?.ext_vmsize ||
            item.recommendation?.ext_sku ||
            item.recommendation?.current_resource_summary ||
            item.recommendation?.recommended_resource_summary;

          const data: ICustomTable2Row[] = [];

          let recommenedationDetails = apiRecommendations.getRecommendationDetails(item.category, item.rule_name);
          if (!recommenedationDetails) {
            recommenedationDetails = {};
          }

          data.push({
            component: ClusterNameWithRegion({
              name: recommenedationDetails.title || snakeToTitleCase(item.rule_name),
              hideIcon: true,
              showAutoEllipsis: true,
              lineClamp: 2,
              maxWidth: '100%',
              region: ticketReferenceMap.has(item.id) ? (
                <CustomTicketLink ticketURL={ticketReferenceMap.get(item.id)?.url} ticketID={ticketReferenceMap.get(item.id)?.ticket_id} />
              ) : (
                <></>
              ),
            }),
            drilldownQuery: { recommendation: item, recommenedationDetails: recommenedationDetails },
            data: item.rule_name,
          });

          const serviceNameValue = item.recommendation?.service_name || recommenedationDetails.serviceName || serviceName;
          data.push({
            component: (
              <Box>
                <Text value={objectName || item.objectName || item.resource_name || instanceFallback} showAutoEllipsis lineClamp={2} />
                {serviceNameValue && <Text value={serviceNameValue} secondaryText showAutoEllipsis />}
              </Box>
            ),
            data: item.objectName || item.resource_name,
          });

          data.push({
            component: <Text showAutoEllipsis lineClamp={3} value={recommenedationDetails.recommendations?.[0] || item.recommendation?.reason} />,
            data: item.recommendation,
          });

          data.push({ component: <SeverityIcon severityType={item.severity} />, data: item.severity });

          data.push({ component: <Datetime value={item.updated_at} />, data: item.updated_at });

          data.push({
            component: (
              <Box display={'flex'} justifyContent={'flex-end'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item, props.accountAccess)} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });

          return data;
        });
        setRecommendations(ec2ResourceData as any);
        setRecommendationsCount(res.data?.recommendation_aggregate?.aggregate?.count ?? 0);
        setLoading(false);
      })
      .catch((error) => {
        setLoading(false);
        console.log(error);
      });
  }, [props?.accountId, page, rowsPerPage, selectedRuleName, selectedServiceName, selectedSeverity, refreshKey]);

  useEffect(() => {
    if (!props?.accountId) {
      return;
    }
    setLoadingTotal(true);
    apiRecommendations
      .getK8sRecommendation({
        accountId: props?.accountId,
        category: 'Configuration',
        serviceName: props?.serviceName || '',
        limit: 1,
        offset: 0,
      })
      .then((res: any) => {
        setTotalRecommendationsCount(res.data?.recommendation_aggregate?.aggregate?.count ?? 0);
      })
      .catch((error) => {
        console.error(error);
      })
      .finally(() => {
        setLoadingTotal(false);
      });
  }, [props?.accountId, props?.serviceName]);

  const filterOptions = [
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
      type: 'dropdown',
      enabled: true,
      options: severityFilter,
      onSelect: onSeverityFilterChange,
      minWidth: '150px',
      label: 'Severity',
      value: selectedSeverity,
    },
  ];

  if (!props?.serviceName) {
    filterOptions.push({
      type: 'dropdown',
      enabled: true,
      options: serviceNamesFilter,
      onSelect: onServiceNamesFilterChange,
      minWidth: '150px',
      label: 'Service Name',
      value: selectedServiceName,
    });
  }

  return (
    <>
      <Box sx={{ padding: '20px 0px' }}>
        <Grid container spacing={2}>
          <Grid item xs={12} sm={6} md={3}>
            <SummaryWidget
              title='Total Recommendations'
              value={loadingTotal ? <CircularProgress color='inherit' size={20} /> : totalRecommendationsCount}
              variant='default'
            />
          </Grid>
        </Grid>
      </Box>
      <BoxLayout2
        heading={''}
        id={cloudOptimizeEventsTable}
        filterOptions={filterOptions}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: cloudOptimizeEventsTable,
              };
            },
          },
          sharing: { enabled: false, onClick: null },
        }}
        dateTimeRange={{
          enabled: false,
          onChange: handleChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
      >
        <CloudAccountTable
          id={cloudOptimizeEventsTable}
          headers={TABLE_COLUMNS}
          data={recommendations}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={recommendationsCount}
          expandable={{
            tabs: [
              {
                componentFn: optimizeEvidance,
                text: 'Evidence',
              },
              {
                componentFn: optimizeDescription,
                text: 'Description',
              },
              {
                componentFn: optimizeMitigation,
                text: 'Mitigation',
              },
            ],
          }}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          tableHeadingCenter={props.tableHeadingCenter}
          stickyColumnIndex={props.stickyColumnIndex}
        />
      </BoxLayout2>

      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: `Cloud Configuration - ${_ticketData?.rule_name || 'Configuration Recommendation'}`,
          description: getTicketDescription(_ticketData),
          accountId: props?.accountId,
        }}
        ticketUrl={{}}
        reference={{
          id: _ticketData?.id,
          type: 'aws',
        }}
      />

      {selectedRecommendation && (
        <AlarmCreationModal
          open={isAlarmCreationModalOpen}
          onClose={closeAlarmCreationModal}
          recommendation={selectedRecommendation}
          accountId={props?.accountId}
          onSuccess={handleAlarmCreationSuccess}
          accountAccess={props.accountAccess}
        />
      )}
    </>
  );
};

function optimizeEvidance(accountId: any, drilldownQuery: any) {
  return (
    <Box sx={{ background: 'white', borderRadius: '8px', p: 3 }}>
      <DrilldownDetails data={drilldownQuery?.recommendation?.recommendation} showCopyIconOnHover={true} />
    </Box>
  );
}

function optimizeDescription(accountId: any, drilldownQuery: any) {
  const markdown = buildDescriptionMarkdown(drilldownQuery?.recommenedationDetails);
  return (
    <Box sx={{ background: 'white', borderRadius: '8px', p: 1 }}>
      {markdown ? (
        <MarkDowns
          data={markdown}
          sx={{ width: '100%', padding: 1, '& p:last-child': { marginBottom: 0 } }}
          allowExecutable={false}
          onLinkClick={null}
        />
      ) : (
        <Typography color='#999'>No description available</Typography>
      )}
    </Box>
  );
}

function optimizeMitigation(accountId: any, drilldownQuery: any) {
  const mitigations = interpolateMitigations(drilldownQuery?.recommenedationDetails?.mitigations, drilldownQuery?.recommendation);
  const markdowns = mitigations?.length ? mitigations.join('\n\n') : null;

  return (
    <Box sx={{ background: 'white', borderRadius: '8px', p: 1 }}>
      {markdowns ? (
        <MarkDowns
          data={markdowns}
          sx={{
            width: '100%',
            padding: 0,
            '& p:last-child': { marginBottom: 0 },
          }}
          allowExecutable={false}
          onLinkClick={null}
        />
      ) : (
        <Typography color='#999'>No mitigation steps available</Typography>
      )}
    </Box>
  );
}

export default OptimizeConfigurationChange;
