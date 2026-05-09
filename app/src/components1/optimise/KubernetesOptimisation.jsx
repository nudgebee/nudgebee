import React, { useEffect, useState, useCallback, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { Text } from '@components1/common';
import Currency from '@components1/common/format/Currency';
import OptimizationCard from './OptimizationCard';

import { colors } from 'src/utils/colors';
import recommendationApi from '@api1/recommendation';
import { ouK8s } from '@assets';
import CustomLink from '@components1/common/CustomLink';

// --- 1. CONFIGURATION ---
const K8S_CONFIG = [
  {
    id: 'right-sizing',
    title: 'Right Sizing',
    ruleName: 'pod_right_sizing',
    subType: 'right-sizing',
    category: 'RightSizing',
    isCardSection: true,
  },
  {
    id: 'unused-volumes',
    title: 'Unused Volumes',
    ruleName: 'unused_pvc',
    subType: 'unused',
    category: 'RightSizing',
    isCardSection: true,
  },
  {
    id: 'abandoned-workloads',
    title: 'Abandoned Workloads',
    ruleName: 'abandoned_resource',
    subType: 'abandoned-apps',
    category: 'RightSizing',
    isCardSection: true,
  },
  {
    id: 'replica-right-sizing',
    title: 'Replica Right Sizing',
    ruleName: 'replica_right_sizing',
    subType: 'replica-right-sizing',
    category: 'RightSizing',
    isCardSection: true,
  },
  {
    id: 'spot-recommendation',
    title: 'Spot Instance Recommendation',
    ruleName: 'Spot instance recommendation',
    subType: 'spot-recommendation',
    category: 'K8sSpotRecommendation',
    isCardSection: true,
  },
  {
    id: 'pvc-right-sizing',
    title: 'PVC Right Sizing',
    ruleName: 'pv_rightsize',
    subType: 'pvc-right-sizing',
    category: 'RightSizing',
    // CHANGED: Set to true so it renders inside the OptimizationCard grid
    isCardSection: true,
  },
];

// --- 2. HELPER: Data Parsing Strategy ---
const getNameAndNamespace = (item, ruleName) => {
  const meta = item.cloud_resourse?.meta;
  const recMeta = item.recommendation?.metadata;
  const claimRef = item.recommendation?.spec?.claimRef;

  switch (ruleName) {
    case 'unused_pvc':
      return { name: recMeta?.name, namespace: claimRef?.namespace };
    case 'pod_right_sizing':
    case 'abandoned_resource':
      return {
        name: meta?.controller || meta?.config?.labels?.['app.kubernetes.io/name'] || item.resource_name,
        namespace: meta?.namespace,
      };
    case 'Spot instance recommendation':
      return {
        name: item.resource_name || meta?.name || meta?.controller,
        namespace: meta?.namespace,
      };
    case 'replica_right_sizing':
      return { name: recMeta?.name, namespace: recMeta?.namespace };
    case 'pv_rightsize':
      return { name: claimRef?.name, namespace: claimRef?.namespace };
    default:
      return { name: '-', namespace: '-' };
  }
};

const processK8sRows = (items, ruleName, accounts, sectionId) => {
  if (!items) {
    return [];
  }

  return items.map((item) => {
    const { name, namespace } = getNameAndNamespace(item, ruleName);
    const accountName = accounts[item.account_id] || item.account_id || '-';

    return [
      {
        component: (
          <>
            {sectionId === 'right-sizing' ? (
              <CustomLink
                href={{
                  pathname: `/kubernetes/details/${item.account_id}`,
                  query: {
                    namespace: namespace,
                    workloadName: name,
                  },
                  hash: 'kubernetes/applications',
                }}
                target='_blank'
              >
                <Text value={name} sx={{ color: colors.text.primary, fontSize: '12px' }} showAutoEllipsis />
              </CustomLink>
            ) : (
              <Text value={name} sx={{ color: colors.text.greyDark, fontSize: '12px' }} showAutoEllipsis />
            )}
            <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '4px' }}>
              <Text value={'acc: '} secondaryText sx={{ fontSize: '11px' }} showAutoEllipsis />
              <CustomLink
                href={{
                  pathname: `/kubernetes/details/${item.account_id}`,
                }}
                target='_blank'
                secondaryText
              >
                {accountName}
              </CustomLink>
              <Text value='|' secondaryText sx={{ width: '10%', fontSize: '10px', fontWeight: 500 }} />
              <Text value={`ns: ${namespace}`} secondaryText sx={{ fontSize: '11px' }} showAutoEllipsis />
            </Box>
          </>
        ),
      },
      {
        component: <Currency value={item.estimated_savings || '-'} precison={1} sx={{ color: colors.text.greyDark, fontSize: '12px' }} />,
      },
    ];
  });
};

const KubernetesOptimisation = ({ accounts }) => {
  const [sectionState, setSectionState] = useState({});
  const [k8sSavings, setK8sSavings] = useState({});

  // 1. Fetch Savings Summary
  useEffect(() => {
    recommendationApi
      .getK8sRecommendationSummaryByRuleName({
        category: ['RightSizing', 'K8sSpotRecommendation'],
        ruleName: ['pod_right_sizing', 'unused_pvc', 'abandoned_resource', 'replica_right_sizing', 'pv_rightsize', 'Spot instance recommendation'],
        status: ['Open', 'InProgress'],
      })
      .then((res) => {
        if (res?.length > 0) {
          const totalEstimatedSaving = res.reduce((sum, item) => sum + item.sum_estimated_savings, 0);

          const savingsMap = res.reduce((obj, item) => {
            obj[item.rule_name] = item.sum_estimated_savings;
            return obj;
          }, {});

          savingsMap.totalEstimatedSaving = Math.ceil(totalEstimatedSaving);
          setK8sSavings(savingsMap);
        }
      })
      .catch((error) => {
        console.error(error);
      });
  }, []);

  // 3. Generic Table Data Fetcher
  const fetchData = useCallback(async (config) => {
    setSectionState((prev) => ({
      ...prev,
      [config.id]: { ...prev[config.id], loading: true },
    }));

    try {
      const res = await recommendationApi.getK8sRecommendation({
        category: config.category || 'RightSizing',
        ruleName: config.ruleName,
        limit: 4,
        offset: 0,
        fetchTicket: false,
      });

      setSectionState((prev) => ({
        ...prev,
        [config.id]: {
          loading: false,
          data: res?.data?.recommendation || [],
          count: res?.data?.recommendation_aggregate?.aggregate?.count || 0,
        },
      }));
    } catch (error) {
      console.error(`Error fetching ${config.id}`, error);
      setSectionState((prev) => ({
        ...prev,
        [config.id]: { loading: false, data: [], count: 0 },
      }));
    }
  }, []);

  useEffect(() => {
    if (Object.keys(accounts).length > 0) {
      K8S_CONFIG.forEach((config) => fetchData(config));
    }
  }, [accounts, fetchData]);

  const getSectionData = (configId) => {
    return sectionState[configId] || { loading: true, data: [], count: 0 };
  };

  // 4. Merge Configuration with Live Savings Data
  const cardSections = useMemo(() => {
    return K8S_CONFIG.filter((c) => c.isCardSection).map((config) => {
      const state = getSectionData(config.id);
      return {
        id: config.id,
        title: config.title,
        savingsValue: k8sSavings[config.ruleName] || 0,
        isLoading: state.loading,
        tableData: processK8sRows(state.data, config.ruleName, accounts, config.id),
        tableHeaders: [
          { name: 'Application Name', width: '80%' },
          { name: 'Savings', width: '20%' },
        ],
        viewAllHref: `/optimise-old/details?type=k8s&subType=${config.subType}`,
        rowsPerPage: state.count,
        totalRows: state.count,
        config,
      };
    });
  }, [sectionState, accounts, k8sSavings]);

  return (
    <>
      <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', mt: '32px', px: '8px' }}>
        <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '8px' }}>
          <SafeIcon src={ouK8s} alt='kubernetes' width={24} height={24} />
          <Typography sx={{ color: colors.text.secondary, fontSize: '20px', fontWeight: 500 }}>Kubernetes</Typography>
        </Box>

        <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center' }}>
          <Typography sx={{ color: colors.text.secondaryDark, fontSize: '14px', fontWeight: 400, mt: '8px' }}>savings</Typography>
          {[
            { val: k8sSavings.totalEstimatedSaving || 0, suffix: '/mo' },
            { val: (k8sSavings.totalEstimatedSaving || 0) * 12, suffix: '/yr' },
          ].map((item, idx) => (
            <Box key={idx} sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '4px', ml: idx === 0 ? '8px' : '12px' }}>
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

      {/* Main Optimization Card (Now includes Spot AND PVC) */}
      <OptimizationCard sections={cardSections} />
    </>
  );
};

export default KubernetesOptimisation;
