import { useEffect, useState, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { Text } from '@components1/common';
import Currency from '@components1/common/format/Currency';
import OptimizationCard from './OptimizationCard';
import { colors } from 'src/utils/colors';
import { snakeToTitleCase } from 'src/utils/common';
import recommendationApi from '@api1/recommendation';
import { ouAzure, AzureVMIcon, AzureSqlIcon, AzureBlobIcon } from '@assets';
import CustomLink from '@components1/common/CustomLink';

// --- 1. CONFIGURATION ---
const SERVICE_CONFIG = [
  {
    id: 'vm',
    title: 'Virtual Machines',
    serviceName: 'microsoft.compute/virtualmachines',
    icon: AzureVMIcon,
    categories: [
      { id: 'right-sizing', apiCategory: 'RightSizing', title: 'Right Sizing' },
      { id: 'infra-upgrade', apiCategory: 'InfraUpgrade', title: 'Infra Upgrade' },
    ],
  },
  {
    id: 'sql',
    title: 'SQL Servers',
    serviceName: 'microsoft.sql/servers',
    icon: AzureSqlIcon,
    categories: [
      { id: 'right-sizing', apiCategory: 'RightSizing', title: 'Right Sizing' },
      { id: 'infra-upgrade', apiCategory: 'InfraUpgrade', title: 'Infra Upgrade' },
    ],
  },
  {
    id: 'blob',
    title: 'Blob Storage',
    serviceName: 'microsoft.storage/storageaccounts',
    icon: AzureBlobIcon,
    categories: [
      { id: 'right-sizing', apiCategory: 'RightSizing', title: 'Right Sizing' },
      { id: 'infra-upgrade', apiCategory: 'InfraUpgrade', title: 'Infra Upgrade' },
    ],
  },
];

// --- 2. HELPER FUNCTIONS ---
const processTableRows = (items, accounts) => {
  if (!items) {
    return [];
  }

  return items.map((item) => {
    let objectName = item.objectName || '';

    if (!objectName) {
      const objectParts = item.account_object_id?.split(':') || [];
      if (objectParts.length === 7) {
        objectName = objectParts[6];
      }
    }
    const impactedValue = item.recommendation?.impacted_value;
    const isUUID = (v) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v);
    const isHexOnly = (v) => /^[0-9a-f]+$/i.test(v);
    const isOpaqueId = (v) => isHexOnly(v) || isUUID(v);
    if (objectName && isOpaqueId(objectName)) {
      objectName = '';
    }
    if (item.resource_name && isOpaqueId(item.resource_name)) {
      item.resource_name = '';
    }
    const instanceFallback =
      (impactedValue && !isUUID(impactedValue) ? impactedValue : undefined) ||
      item.recommendation?.ext_vmsize ||
      item.recommendation?.ext_sku ||
      item.recommendation?.current_resource_summary ||
      item.recommendation?.recommended_resource_summary;
    const details = recommendationApi.getRecommendationDetails(item.category, item.rule_name) || {};
    const accountName = accounts[item.account_id] || item.account_id || '-';

    return [
      {
        component: (
          <>
            <Text value={details.title || snakeToTitleCase(item.rule_name)} sx={{ color: colors.text.greyDark, fontSize: '12px' }} />
            <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '4px' }}>
              <Text value={`acc: `} secondaryText sx={{ fontSize: '11px' }} showAutoEllipsis />
              <CustomLink
                href={{
                  pathname: `/cloud-account/details/${item.account_id}`,
                }}
                target='_blank'
                secondaryText
              >
                {accountName}
              </CustomLink>
              <Text value='|' secondaryText sx={{ width: '10%', fontSize: '10px', fontWeight: 500 }} />
              <Text
                value={`re: ${objectName || item.resource_name || instanceFallback || '-'}`}
                secondaryText
                sx={{ fontSize: '11px' }}
                showAutoEllipsis
              />
            </Box>
          </>
        ),
      },
      {
        component: <Currency value={item.estimated_savings} precison={1} sx={{ color: colors.text.greyDark, fontSize: '12px' }} />,
      },
    ];
  });
};

const AzureOptimisation = ({ accounts }) => {
  const [azureTotalSavings, setAzureTotalSavings] = useState(0);
  // Stores savings map: { 'serviceName': { total: 150, 'RightSizing': 100, 'InfraUpgrade': 50 } }
  const [serviceSavingsMap, setServiceSavingsMap] = useState({});
  const [recommendationState, setRecommendationState] = useState({});

  // 1. Generic Fetch Function for Tables
  const fetchData = useCallback(async (serviceId, serviceName, categoryId, apiCategory) => {
    const stateKey = `${serviceId}-${categoryId}`;

    setRecommendationState((prev) => ({
      ...prev,
      [stateKey]: { ...prev[stateKey], loading: true },
    }));

    try {
      const res = await recommendationApi.getK8sRecommendation({
        category: apiCategory,
        serviceName: serviceName,
        limit: 4,
        offset: 0,
        fetchTicket: false,
      });

      setRecommendationState((prev) => ({
        ...prev,
        [stateKey]: {
          loading: false,
          data: res?.data?.recommendation || [],
          count: res?.data?.recommendation_aggregate?.aggregate?.count || 0,
        },
      }));
    } catch (error) {
      console.error(`Error fetching ${serviceName} - ${apiCategory}`, error);
      setRecommendationState((prev) => ({
        ...prev,
        [stateKey]: { loading: false, data: [], count: 0 },
      }));
    }
  }, []);

  // 3. Fetch Savings Summary & Process Response
  useEffect(() => {
    recommendationApi
      .getK8sRecommendationSummaryByRuleName({
        category: ['RightSizing', 'InfraUpgrade'],
        serviceName: [
          'microsoft.compute/disks',
          'microsoft.compute/virtualmachines',
          'microsoft.compute/virtualmachinescalesets',
          'microsoft.sql/servers',
          'microsoft.storage/storageaccounts',
        ],
        status: ['Open', 'InProgress'],
      })
      .then((res) => {
        if (res?.length > 0) {
          // A. Calculate Total Savings for Header
          const total = res.reduce((sum, item) => sum + item.sum_estimated_savings, 0);
          setAzureTotalSavings(Math.ceil(total));

          // B. Aggregate Savings by Service and Category
          const map = {};

          res.forEach((item) => {
            const service = item.resource_cloud_service;
            const cat = item.category;
            const savings = item.sum_estimated_savings;

            if (!map[service]) {
              map[service] = { total: 0, RightSizing: 0, InfraUpgrade: 0 };
            }

            // Add to Service Total (Card Header)
            map[service].total += Math.ceil(savings);

            // Add to Specific Category (Section Header)
            if (map[service][cat] !== undefined) {
              map[service][cat] += Math.ceil(savings);
            }
          });

          setServiceSavingsMap(map);
        }
      })
      .catch((error) => {
        console.error(error);
      });
  }, []);

  // 4. Trigger Table Data Fetches
  useEffect(() => {
    if (Object.keys(accounts).length > 0) {
      SERVICE_CONFIG.forEach((service) => {
        service.categories.forEach((cat) => {
          fetchData(service.id, service.serviceName, cat.id, cat.apiCategory);
        });
      });
    }
  }, [accounts, fetchData]);

  // 5. Render Helper
  const getSectionProps = (serviceId, serviceName, category) => {
    const stateKey = `${serviceId}-${category.id}`;
    const currentState = recommendationState[stateKey] || { loading: true, data: [], count: 0 };

    // Get savings from our map, or default to 0
    const savingsValue = serviceSavingsMap[serviceName]?.[category.apiCategory] || 0;

    return {
      id: stateKey,
      title: category.title,
      savingsValue: savingsValue,
      isLoading: currentState.loading,
      tableData: processTableRows(currentState.data, accounts),
      tableHeaders: [
        { name: 'Application Name', width: '80%' },
        { name: 'Savings', width: '20%' },
      ],
      viewAllHref: `/optimise-old/details?type=azure&subType=${category.id}&service=${serviceName}`,
      rowsPerPage: currentState.count,
      totalRows: currentState.count,
    };
  };

  return (
    <>
      {/* Header Section */}
      <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', mt: '32px', px: '8px' }}>
        <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '8px' }}>
          <SafeIcon src={ouAzure} alt='azure' width={24} height={24} />
          <Typography sx={{ color: colors.text.secondary, fontSize: '20px', fontWeight: 500 }}>Azure</Typography>
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center' }}>
          <Typography sx={{ color: colors.text.secondaryDark, fontSize: '14px', fontWeight: 400, mt: '8px' }}>savings</Typography>
          {[
            { val: azureTotalSavings, suffix: '/mo' },
            { val: azureTotalSavings * 12, suffix: '/yr' },
          ].map((item, index) => (
            <Box key={index} sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '4px', ml: index === 0 ? '8px' : '12px' }}>
              <Currency
                value={item.val}
                sx={{ color: colors.text.currency, fontSize: '24px', fontWeight: 500 }}
                suffix={item.suffix}
                withTooltip={false}
                sxSuffix={{ fontSize: '14px' }}
                sxPrefix={{ color: colors.text.currency, fontSize: '14px' }}
              />
            </Box>
          ))}
        </Box>
      </Box>

      {/* Dynamic Optimization Cards */}
      {SERVICE_CONFIG.map((service) => (
        <OptimizationCard
          key={service.id}
          cardTitle={service.title}
          cardIcon={service.icon}
          // Get total savings for this service from map
          cardSavingsValue={serviceSavingsMap[service.serviceName]?.total || 0}
          sections={service.categories.map((cat) => getSectionProps(service.id, service.serviceName, cat))}
        />
      ))}
    </>
  );
};

export default AzureOptimisation;
