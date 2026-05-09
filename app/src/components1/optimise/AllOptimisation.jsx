import CustomTable from '@components1/common/tables/CustomTable2';
import { useEffect, useState } from 'react';
import recommendationApi from '@api1/recommendation';
import { BoxLayout2, Text } from '@components1/common';
import { Box } from '@mui/material';
import CopyableText from '@components1/common/CopyableText';
import Currency from '@components1/common/format/Currency';
import apiHome from '@api1/home';
import { snakeToTitleCase } from '@utils/common';
import Datetime from '@components1/common/format/Datetime';
import { colors } from '@utils/colors';
import CloudProviderIcon from '@components1/common/CloudIcon';
import SafeIcon from '@components1/common/SafeIcon';
import { AWSEC2Icon, AWSRDSIcon, AWSS3Icon, AppsInfraBlue, AzureBlobIcon, AzureDiskIcon, AzureSqlIcon, AzureVMIcon, K8sIcon } from '@assets';
import { usePagination } from '@hooks/usePagination';

const AllOptimisation = () => {
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState([]);
  const [ruleNames, setRuleNames] = useState([]);
  const [selectedRuleNames, setSelectedRuleNames] = useState([]);

  const { page, rowsPerPage, changePage, setPage } = usePagination(10);

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
  };

  const onRuleNameChange = (e) => {
    setSelectedRuleNames(e.target.value);
    setPage(0);
  };

  useEffect(() => {
    apiHome.getCloudAccounts().then((res) => {
      setAccounts(res);
    });
  }, []);

  const [loadingData, setLoadingData] = useState(false);
  const [allOptimisationData, setAllOptimisationData] = useState({ data: [], count: 0 });

  useEffect(() => {
    listDistinctRuleNames();
  }, []);

  useEffect(() => {
    if (Object.keys(accounts).length > 0) {
      listAllRecommendations();
    }
  }, [accounts, rowsPerPage, page, selectedAccountId, selectedRuleNames]);

  const getAccountName = (id) => {
    const filteredAcc = accounts?.find((r) => r.id == id);
    if (filteredAcc) {
      return filteredAcc?.account_name || filteredAcc?.cloud_provider;
    }
    return id;
  };

  const getAccountProvider = (id) => {
    const filteredAcc = accounts?.find((r) => r.id == id);
    if (filteredAcc) {
      return filteredAcc?.cloud_provider;
    }
    return id;
  };

  const getServiceName = (objectId, resourceType) => {
    if (resourceType == 'StatefulSet') {
      return { name: 'StatefulSet', icon: K8sIcon, accountType: 'k8s' };
    } else if (resourceType == 'Deployment') {
      return { name: 'Deployment', icon: K8sIcon, accountType: 'k8s' };
    } else if (resourceType == 'DaemonSet') {
      return { name: 'DaemonSet', icon: K8sIcon, accountType: 'k8s' };
    } else if (resourceType == 'Job') {
      return { name: 'Job', icon: K8sIcon, accountType: 'k8s' };
    } else if (resourceType == 'Node') {
      return { name: 'Node', icon: K8sIcon, accountType: 'k8s' };
    } else if (objectId.startsWith('pvc')) {
      return { name: 'Volume', icon: K8sIcon, accountType: 'k8s' };
    } else if (objectId.includes('ec2')) {
      return { name: 'EC2', icon: AWSEC2Icon, accountType: 'aws' };
    } else if (objectId.includes('s3')) {
      return { name: 'S3', icon: AWSS3Icon, accountType: 'aws' };
    } else if (objectId.includes('rds')) {
      return { name: 'RDS', icon: AWSRDSIcon, accountType: 'aws' };
    } else if (objectId.includes('microsoft.storage/storageaccounts')) {
      return { name: 'Blob', icon: AzureBlobIcon, accountType: 'azure' };
    } else if (objectId.includes('microsoft.compute/virtualmachines')) {
      return { name: 'VM', icon: AzureVMIcon, accountType: 'azure' };
    } else if (objectId.includes('microsoft.compute/disks')) {
      return { name: 'Disk', icon: AzureDiskIcon, accountType: 'azure' };
    } else if (objectId.includes('microsoft.sql/servers')) {
      return { name: 'Sql', icon: AzureSqlIcon, accountType: 'azure' };
    }
  };

  const listAllRecommendations = () => {
    setLoadingData(true);
    setAllOptimisationData({ data: [], count: 0 });

    recommendationApi
      .getK8sRecommendation({
        category: ['InfraUpgrade', 'RightSizing'],
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: false,
        accountId: selectedAccountId.map((f) => f.value),
        ruleName: selectedRuleNames.map((f) => f.value),
      })
      .then((res) => {
        let k8sRecommendationData = res?.data?.recommendation?.map((item) => {
          let data = [];
          const nameIcon = getServiceName(item.account_object_id, item.resource_type);
          data.push({
            component: (
              <>
                <CopyableText copyableText={item?.resource_name || item.recommendation?.metadata?.name}>
                  <Text value={item?.resource_name || item.recommendation?.metadata?.name} />
                </CopyableText>
                {nameIcon?.accountType == 'k8s' ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', flexDirection: 'row' }}>
                    <SafeIcon alt={nameIcon?.name} src={nameIcon?.icon || K8sIcon} style={{ width: '16px', height: '16px' }} />
                    <Text value={'EKS'} style={{ fontSize: '12px', fontWeight: 400, color: colors.text.secondaryDark }} />
                    <Text value='|' secondaryText sx={{ width: '10%', fontSize: '10px', fontWeight: 500, mx: '4px' }} />
                    <SafeIcon alt={'Apps'} src={AppsInfraBlue} style={{ width: '16px', height: '16px' }} />
                    <Text value={nameIcon.name} style={{ fontSize: '12px', fontWeight: 400, color: colors.text.secondaryDark }} />
                  </Box>
                ) : (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', flexDirection: 'row' }}>
                    <SafeIcon alt={nameIcon?.name} src={nameIcon?.icon || K8sIcon} style={{ width: '16px', height: '16px' }} />
                    <Text value={nameIcon?.name} style={{ fontSize: '12px', fontWeight: 400, color: colors.text.secondaryDark }} />
                  </Box>
                )}
              </>
            ),
          });
          data.push({
            component: (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', flexDirection: 'row' }}>
                <CloudProviderIcon cloud_provider={getAccountProvider(item.account_id)} height='20px' width='20px' sx={{ paddingRight: 1 }} />
                <Text value={getAccountName(item.account_id)} />
              </Box>
            ),
          });
          data.push({
            component: <Text value={snakeToTitleCase(item.rule_name)} />,
          });
          data.push({ component: <Datetime value={item.updated_at} /> });
          data.push({
            component: (
              <Currency
                suffix='/mo'
                value={item.estimated_savings}
                precison={1}
                sx={{ color: colors.text.currency, fontSize: '16px', fontWeight: 500 }}
              />
            ),
          });
          return data;
        });
        setAllOptimisationData({ data: k8sRecommendationData, count: res?.data?.recommendation_aggregate?.aggregate?.count || 0 });
      })
      .finally(() => {
        setLoadingData(false);
      });
  };

  const listDistinctRuleNames = () => {
    recommendationApi.getDistinctRuleName().then((res) => {
      const response = res?.data?.data?.recommendation_groupings_v2?.rows || [];
      setRuleNames(response);
    });
  };

  return (
    <div style={{ marginTop: '28px' }}>
      <BoxLayout2
        id={'all-saving'}
        filterOptions={[
          {
            type: 'multi-dropdown',
            enabled: true,
            options: accounts?.map((acc) => ({
              label: acc.label || acc.account_name,
              value: acc.id || acc.value,
            })),
            onSelect: onAccountFilterChange,
            label: 'Account',
            value: selectedAccountId,
          },
          {
            type: 'multi-dropdown',
            enabled: true,
            options: ruleNames.map((e) => ({
              label: snakeToTitleCase(e.rule_name),
              value: e.rule_name,
            })),
            onSelect: onRuleNameChange,
            label: 'By Saving Type',
            value: selectedRuleNames,
          },
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: 'all-optimise',
              };
            },
          },
          sharing: { enabled: true },
        }}
        sx={{
          boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 0.4), 0px 2px 20px 0px rgb(233, 233, 233)',
          border: '1px solid #EBEBEB',
          padding: '16px 24px',
        }}
      >
        <Box sx={{ width: '100%' }}>
          <CustomTable
            tableData={allOptimisationData.data}
            id={'all-optimise'}
            headers={[
              { name: 'Resource/Instance', width: '30%' },
              { name: 'Account Name', width: '20%' },
              { name: 'Savings Type', width: '20%' },
              { name: 'Updated at', width: '10%' },
              { name: 'Potential Savings', width: '20%' },
            ]}
            onPageChange={changePage}
            rowsPerPage={rowsPerPage}
            totalRows={allOptimisationData.count}
            pageNumber={page + 1}
            loading={loadingData}
          />
        </Box>
      </BoxLayout2>
    </div>
  );
};

export default AllOptimisation;
