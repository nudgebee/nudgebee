import React, { useEffect, useState, useRef } from 'react';
import { Box } from '@mui/material';
import apiRecommendations from '@api1/recommendation';
import CloudAccountTable from './CloudAccountTable';
import type { ICustomTable2Row } from './ec2/Instances';
import { usePagination } from '@hooks/usePagination';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { action } from 'src/utils/actionStyles';
import Text from '@components1/common/format/Text';
import Currency from '@components1/common/format/Currency';
import { snakeToTitleCase } from 'src/utils/common';
import { AutoPilotGreyIcon, TicketsIcon } from '@assets';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { snackbar } from '@components1/common/snackbarService';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';
import BoxLayout2 from '@components1/common/BoxLayout2';
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

interface ServiceRecommendationsProps {
  accountId: string;
  serviceName: string;
  provider: string;
}

const MENU_ITEMS = [
  {
    icon: AutoPilotGreyIcon,
    disabled: true,
    label: 'Resolve',
    id: 0,
  },
  {
    icon: TicketsIcon,
    disabled: false,
    label: 'Create Ticket',
    id: 1,
  },
];

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

// Category filter options
const CATEGORY_OPTIONS = [
  { label: 'All Categories', value: '' },
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
  const [selectedCategory, setSelectedCategory] = useState<string>('');
  const fetchKeyRef = useRef<string>(''); // Prevent duplicate fetches
  const currencySymbol = useCurrencySymbol(accountId);

  const onCategoryFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedCategory(e?.target?.value);
    setPage(0);
    fetchKeyRef.current = ''; // Reset fetch key to force re-fetch
  };

  const onMenuClick = (menuItem: { id: number }, data: any) => {
    if (menuItem.id === 1) {
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

    const currentKey = `${accountId}-${serviceName}-${page}-${selectedCategory}`;
    if (currentKey === fetchKeyRef.current) {
      return;
    }

    fetchKeyRef.current = currentKey;
    setLoading(true);

    const allCategories = ['RightSizing', 'Configuration', 'Security', 'InfraUpgrade'];
    const categoryParam: any = selectedCategory || allCategories;

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
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '0px' }}>
                <SeverityIcon severityType={item.severity} />
                <Datetime value={item.updated_at} sx={{ fontSize: '11px' }} />
              </Box>
            ),
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
              <Box display={'flex'} gap={'10px'} alignItems={'center'}>
                <Currency prefix={currencySymbol} value={item.estimated_savings?.toFixed(2) ?? '0'} suffix='/yr' />
              </Box>
            ),
          });

          data.push({
            component: (
              <Box display={'flex'} justifyContent={'flex-end'} flexDirection={'row'} alignItems={'center'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
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

  const filterOptions = [
    {
      type: 'dropdown',
      enabled: true,
      options: CATEGORY_OPTIONS,
      onSelect: onCategoryFilterChange,
      minWidth: '150px',
      label: 'Category',
      value: selectedCategory,
    },
  ];

  return (
    <BoxLayout2
      heading=''
      id='service-recommendations'
      filterOptions={filterOptions}
      sharingOptions={{
        download: { enabled: true, onClick: () => ({ tableId: 'service-recommendations-table' }) },
        sharing: { enabled: false, onClick: null },
      }}
    >
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

      <CloudAccountTable
        id='service-recommendations-table'
        headers={TABLE_COLUMNS}
        data={recommendations}
        rowsPerPage={rowsPerPage}
        onPageChange={changePage}
        totalRows={recommendationsCount}
        loading={loading}
        pageNumber={page + 1}
        showUpdatedTable
      />
    </BoxLayout2>
  );
};

export default ServiceRecommendations;
