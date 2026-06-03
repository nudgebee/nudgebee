import React, { useEffect, useState, useRef } from 'react';
import { Box } from '@mui/material';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import apiRecommendations from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { Button as DsButton } from '@components1/ds/Button';
import { SeverityIcon as DsSeverityIcon } from '@components1/ds/SeverityIcon';
import CloudAccountTable from './CloudAccountTable';
import SafeIcon from '@components1/common/SafeIcon';
import type { ICustomTable2Row } from './ec2/Instances';
import { usePagination } from '@hooks/usePagination';
import Datetime from '@common-new/format/Datetime';
import { ds } from '@utils/colors';
import Text from '@common-new/format/Text';
import Currency from '@common-new/format/Currency';
import { snakeToTitleCase, toSeverityLevel } from '@utils/common';
import { AutoPilotGreyIcon, TicketsIcon } from '@assets';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { snackbar } from '@components1/common/snackbarService';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import { getTicketDescription } from './common';

const SEVERITY_TOOLTIP =
  'Indicates the urgency level of this recommendation. Critical items represent the highest impact. Timestamp shows when this recommendation was last detected.';
const RULE_NAME_TOOLTIP =
  'The specific optimization rule that triggered this recommendation. Multiple recommendations can share the same rule but apply to different resources.';

const TABLE_COLUMNS = [
  { name: 'Severity', width: '10%', info: SEVERITY_TOOLTIP },
  { name: 'Category', width: '10%' },
  { name: 'Rule Name', width: '15%', info: RULE_NAME_TOOLTIP },
  { name: 'Instance', width: '20%' },
  { name: 'Recommendation', width: '30%' },
  { name: 'Savings', width: '10%' },
  { name: '', width: '5%' },
];

const TABLE_ID = 'service-recommendations-table';

interface ServiceRecommendationsProps {
  accountId: string;
  serviceName: string;
  provider: string;
}

const getCategoryLabel = (category: string) => {
  switch (category) {
    case 'RightSizing':
      return 'Right Sizing';
    case 'Configuration':
      return 'Configuration';
    case 'Security':
      return 'Security';
    case 'InfraUpgrade':
      return 'Infra Upgrade';
    default:
      return category;
  }
};

// 'ALL' is a sentinel — FilterDropdown treats empty string as "no selection"
// (no chip rendered), so we use a non-empty value and map it back to
// "all 4 categories" at API-call time.
const ALL_CATEGORIES = 'ALL';
const CATEGORY_OPTIONS = [
  { label: 'All Categories', value: ALL_CATEGORIES },
  { label: 'Right Sizing', value: 'RightSizing' },
  { label: 'Configuration', value: 'Configuration' },
  { label: 'Security', value: 'Security' },
  { label: 'Infra Upgrade', value: 'InfraUpgrade' },
];

const ServiceRecommendations: React.FC<ServiceRecommendationsProps> = ({ accountId, serviceName, provider: _provider }) => {
  const [recommendations, setRecommendations] = useState<ICustomTable2Row[][]>([]);
  const [recommendationsCount, setRecommendationsCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const [ticketData, setTicketData] = useState({} as any);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [selectedCategory, setSelectedCategory] = useState<string>(ALL_CATEGORIES);
  const fetchKeyRef = useRef<string>(''); // Prevent duplicate fetches
  const currencySymbol = useCurrencySymbol(accountId);

  const onCategoryFilterChange = (e: { target: { value: string | null } }) => {
    // Treat "clear" (null) the same as "All Categories" — both mean no specific filter.
    setSelectedCategory(e?.target?.value ?? ALL_CATEGORIES);
    setPage(0);
    fetchKeyRef.current = ''; // Reset fetch key to force re-fetch
  };

  const onMenuAction = (id: number, data: any) => {
    if (id === 1) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const handleTicketSuccess = () => {
    setIsTicketCreateFormOpen(false);
  };

  const handleTicketFailure = (error: string) => {
    snackbar.error(error || 'Failed to create ticket');
  };

  useEffect(() => {
    if (!accountId || !serviceName) {
      return;
    }

    const currentKey = `${accountId}-${serviceName}-${page}-${rowsPerPage}-${selectedCategory}`;
    if (currentKey === fetchKeyRef.current) {
      return;
    }

    fetchKeyRef.current = currentKey;
    setLoading(true);

    const allCategories = ['RightSizing', 'Configuration', 'Security', 'InfraUpgrade'];
    const categoryParam: any = selectedCategory === ALL_CATEGORIES ? allCategories : selectedCategory;

    apiRecommendations
      .getK8sRecommendation({
        accountId: accountId,
        category: categoryParam,
        ruleName: '',
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        serviceName: serviceName,
        severity: '',
      })
      .then((res: any) => {
        const recommendations = res.data?.recommendation || [];
        const totalCount = res.data?.recommendation_aggregate?.aggregate?.count || 0;

        const formattedData = recommendations.map((item: any) => {
          let extractedServiceName = item.resource_cloud_service || '';
          let objectName = item.resource_name || '';

          if (!extractedServiceName || !objectName) {
            const objectParts = item.account_object_id?.split(':') || [];
            if (objectParts.length == 7) {
              if (!extractedServiceName) {
                extractedServiceName = objectParts[2];
              }
              if (!objectName) {
                objectName = objectParts[6];
              }
            }
          }

          const data: ICustomTable2Row[] = [];

          let recommendationDetails = apiRecommendations.getRecommendationDetails(item.category, item.rule_name);
          if (!recommendationDetails) {
            recommendationDetails = {};
          }

          data.push({
            component: (
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 0 }}>
                <DsSeverityIcon level={toSeverityLevel(item.severity)} aria-label={`Severity: ${item.severity || 'unknown'}`} />
                <Datetime value={item.updated_at} sx={{ fontSize: ds.text.caption }} />
              </Box>
            ),
            data: item.severity,
          });

          data.push({
            component: <Text value={getCategoryLabel(item.category || 'Unknown')} />,
          });

          data.push({
            component: <Text value={recommendationDetails.title || snakeToTitleCase(item.rule_name)} showAutoEllipsis lineClamp={2} />,
          });

          data.push({
            component: (
              <Box>
                <Text value={item.resource_name || objectName || '-'} showAutoEllipsis lineClamp={2} />
                <Text value={`Svc: ${item.resource_cloud_service || extractedServiceName || '-'}`} secondaryText showAutoEllipsis />
              </Box>
            ),
          });

          data.push({
            component: <Text showAutoEllipsis lineClamp={3} value={item.recommendation?.reason || item.recommendation?.description || '-'} />,
          });

          data.push({
            component: (
              <Box display={'flex'} gap={ds.space[2]} alignItems={'center'}>
                <Currency prefix={currencySymbol} value={item.estimated_savings?.toFixed(2) ?? '0'} suffix='/yr' />
              </Box>
            ),
          });

          const menuItems = [
            {
              id: `${TABLE_ID}-action-${item.id}-resolve`,
              icon: <SafeIcon src={AutoPilotGreyIcon} alt='' width={14} height={14} />,
              label: 'Resolve',
              disabled: true,
              onSelect: () => onMenuAction(0, item),
            },
            {
              id: `${TABLE_ID}-action-${item.id}-create-ticket`,
              icon: <SafeIcon src={TicketsIcon} alt='' width={14} height={14} />,
              label: 'Create Ticket',
              onSelect: () => onMenuAction(1, item),
            },
          ];

          data.push({
            component: (
              <Box display='flex' justifyContent='flex-end' flexDirection='row' alignItems='center' gap={ds.space[1]}>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={menuItems}
                  trigger={<DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon />} />}
                />
              </Box>
            ),
          });

          return data;
        });

        setRecommendations(formattedData);
        setRecommendationsCount(totalCount);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
        setRecommendations([]);
        setRecommendationsCount(0);
      });
  }, [accountId, serviceName, page, rowsPerPage, selectedCategory]);

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        ticketData={{
          subject: `Cloud Optimization - ${ticketData?.rule_name || 'Service Recommendation'}`,
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        ticketUrl={{}}
        reference={{
          id: ticketData?.id,
          type: 'aws',
        }}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
      />

      <ListingLayout id={TABLE_ID}>
        <ListingLayout.Toolbar
          data-testid={`${TABLE_ID}-filter-toolbar`}
          actions={<DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />}
        >
          <FilterDropdown
            id={`${TABLE_ID}-filter-category`}
            label='Category'
            options={CATEGORY_OPTIONS}
            value={selectedCategory}
            onSelect={onCategoryFilterChange}
          />
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CloudAccountTable
            id={TABLE_ID}
            headers={TABLE_COLUMNS}
            data={recommendations}
            rowsPerPage={rowsPerPage}
            onPageChange={changePage}
            totalRows={recommendationsCount}
            loading={loading}
            pageNumber={page + 1}
            showUpdatedTable
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default ServiceRecommendations;
