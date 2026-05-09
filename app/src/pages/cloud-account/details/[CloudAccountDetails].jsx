import React, { useState, useEffect, useMemo } from 'react';
import AnchorComponent from '@components1/common/AnchorComponent';
import ErrorBoundary from '@components1/common/ErrorBoundary';
import { FormControlLabel, Radio, RadioGroup, Box, Alert } from '@mui/material';
import CloudOptimizeRecommendationsTable from '@components1/cloudaccount/CloudOptimizeRecommendationsTable';
import CloudAccountSummary from '@components1/cloudaccount/CloudAccountSummary';
import CloudAccountServices from '@components1/cloudaccount/CloudAccountServices';
import CloudAccountEvents from '@components1/cloudaccount/CloudAccountEvents';
import CloudAccountMetrices from '@components1/cloudaccount/CloudAccountMetrices';
import { CloudLogsViewer } from '@components1/cloudaccount/cloud-logs';
import { CloudMetricsViewer } from '@components1/cloudaccount/cloud-metrics';
import CloudAccountSecurity from '@components1/cloudaccount/CloudAccountSecurity';
import CloudAccountTools from '@components1/cloudaccount/CloudAccountTools';
import CloudAccountAlertManager from '@components1/cloudaccount/CloudAccountAlertManager';
import TriageRulesManager from '@components1/triage/TriageRulesManager';
import ThresholdSuggestionsManager from '@components1/triage/ThresholdSuggestionsManager';
import { Ec2Instances, Ec2Summary } from '@components1/cloudaccount/ec2';
import { CFSummaryDetails, CFInstances, CFResources } from '@components1/cloudaccount/cloudfoundry';
import { RdsInstances, RdsSummary } from '@components1/cloudaccount/rds';
import { S3Instances, S3Summary } from '@components1/cloudaccount/s3';
import Loader from '@components1/common/Loader';
import ListingRecommendationResolution from '@components1/recommendations/ListingRecommendationResolution';

import {
  OptimizeIconBlue,
  SummaryIconBlue,
  PvcSightSizing,
  AllEventsIcon,
  QueryLogIcon,
  SecuritytoolsBlue,
  ToolIconBlue,
  RightSizingIcon,
  ClusterUpgradeIcon,
  RecommendationResolutionIcon,
  ApplicationsIconblue,
  MonitoringIconBlue,
  OptimizeSummaryIcon,
  AlertManagerIcon,
  TroubleshootIconBlue,
  AWSS3Icon,
  AWSECSIcon,
  AWSEC2Icon,
  AWSRDSIcon,
  AzureVMIcon,
  AzureSqlIcon,
  AzureBlobIcon,
  GCPCloudSQLIcon,
  GCPComputeEngineIcon,
  GCPCloudStorageIcon,
  CloudFoundryIcon,
  NodesIcon,
} from '@assets';
import apiCloudAccount from '@api1/cloud-account';
import { useRouter } from 'next/router';
import { ECSInstances, ECSSummary } from '@components1/cloudaccount/ecs';
import { useData } from '@context/DataContext';

const optimizeRadioTab = [
  { value: 'right-sizing', label: 'Right Sizing' },
  { value: 'configuration', label: 'Configuration' },
  { value: 'security', label: 'Security' },
  { value: 'infra-upgrade', label: 'Infra Upgrade' },
];

const SERVICE_NAME = {
  EC2: 'AmazonEC2',
  RDS: 'AmazonRDS',
  S3: 'AmazonS3',
  ECS: 'AmazonECS',
  VM: 'microsoft.compute/virtualmachines',
  BLOB: 'microsoft.storage/storageaccounts',
  SQL: 'microsoft.sql/servers',
  SQL_MI: 'microsoft.sql/managedinstances',
  COMPUTE_ENGINE: 'Compute Engine',
  CLOUD_SQL: 'Cloud SQL',
  CLOUD_STORAGE: 'Cloud Storage',
  GKE: 'Kubernetes Engine',
  BIGQUERY: 'BigQuery',
  CLOUD_FUNCTIONS: 'cloudfunctions.googleapis.com/Function',
  CF_APPS: 'apps',
  CF_ORGS: 'organizations',
  CF_SPACES: 'spaces',
  CF_ROUTES: 'routes',
};

const CloudAccounts = () => {
  const router = useRouter();
  const [accountId, setAccountId] = useState(router.query.CloudAccountDetails);
  const [loading, setLoading] = useState(false);
  const [clusterSummary, setClusterSummary] = useState({});
  const [optimizeRadioTabValue, setOptimizeRadioTabValue] = useState('right-sizing');
  const [selectedFilter, setSelectedFilter] = useState(0);
  const [selectedSubTab, setSelectedSubTab] = useState(0);

  useEffect(() => {
    const hash = router.asPath.split('#')[1];
    if (!hash || !filterOptions.length) return;
    const [fragment, subFragment] = hash.split('/');
    const filter = filterOptions.find((option) => option.fragment === fragment);
    if (filter) {
      setSelectedFilter(filter.value);
      const subTab = (filter?.tabOptions || []).find((tab) => tab.fragment === subFragment);
      if (subTab) {
        setSelectedSubTab(subTab.value);
      }
    }
  }, []);

  const { selectedCluster } = useData();

  const handleChangeOptimizationTab = (event) => {
    if (event.target.value === 'modernizing' || event.target.value === 'bin-packing') {
      return true;
    }
    setOptimizeRadioTabValue(event.target.value);
  };

  useEffect(() => {
    const newAccountId = router.query.accountId || router.query.CloudAccountDetails;
    if (accountId != newAccountId) {
      setAccountId(newAccountId);
    }
  }, [router.query.CloudAccountDetails, router.query.accountId]);

  const filterOptions = useMemo(() => {
    const baseOptions = [
      {
        name: 'Summary',
        value: 0,
        icon: SummaryIconBlue,
        fragment: 'summary',
      },
      {
        name: 'Optimize',
        fragment: 'optimize',
        value: 1,
        icon: OptimizeIconBlue,
        tabOptions: [
          { id: 'optimize-right-sizing', text: 'Right Sizing', value: 0, fragment: 'right-sizing', icon: RightSizingIcon },
          { id: 'optimize-configuration', text: 'Configuration', value: 1, fragment: 'configuration', icon: OptimizeSummaryIcon },
          { id: 'optimize-security', text: 'Security', value: 2, fragment: 'security', icon: SecuritytoolsBlue },
          { id: 'optimize-infra-upgrade', text: 'Infra Upgrade', value: 3, fragment: 'infra-upgrade', icon: ClusterUpgradeIcon },
          {
            id: 'recommendation-resolution-status',
            text: 'Recommendation Resolution',
            value: 4,
            fragment: 'recommendation-resolution',
            icon: RecommendationResolutionIcon,
          },
        ],
      },
      {
        name: 'Services',
        fragment: 'services',
        value: 2,
        icon: PvcSightSizing,
      },
      {
        name: 'Troubleshoot',
        fragment: 'events',
        value: 3,
        icon: TroubleshootIconBlue,
        tabOptions: [
          { id: 'events', text: 'Events', value: 0, fragment: 'events', icon: AllEventsIcon },
          { id: 'triage-rules', text: 'Triage Rules', value: 1, fragment: 'triage-rules', icon: AlertManagerIcon },
          { id: 'threshold-suggestions', text: 'Alert Tuning', value: 2, fragment: 'threshold-suggestions', icon: AlertManagerIcon },
        ],
      },
      {
        name: 'Monitoring',
        fragment: 'monitoring',
        value: 4,
        icon: MonitoringIconBlue,
        tabOptions: [
          { id: 'alert-manager', text: 'Alert Manager', value: 0, fragment: 'alert-manager', icon: AlertManagerIcon },
          { id: 'logs', text: 'Cloud Logs', value: 1, fragment: 'cloud-logs', icon: QueryLogIcon },
          { id: 'metrics', text: 'Cloud Metrics', value: 2, fragment: 'metrics', icon: NodesIcon },
        ],
      },
      {
        name: 'Security',
        fragment: 'security',
        value: 9,
        icon: SecuritytoolsBlue,
        disabled: true,
      },
      {
        name: 'Tools',
        fragment: 'tools',
        value: 10,
        icon: ToolIconBlue,
        disabled: true,
      },
    ];
    const awsOptions = [
      {
        name: 'EC2',
        fragment: 'ec2',
        value: 5,
        icon: AWSEC2Icon,
        doNotInvertIcon: true,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'RDS',
        fragment: 'rds',
        value: 6,
        icon: AWSRDSIcon,
        doNotInvertIcon: true,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'S3',
        fragment: 's3',
        value: 7,
        icon: AWSS3Icon,
        doNotInvertIcon: true,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'ECS',
        fragment: 'ecs',
        value: 8,
        icon: AWSECSIcon,
        doNotInvertIcon: true,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
    ];

    const azureOptions = [
      {
        name: 'VM',
        fragment: 'vm',
        value: 5,
        icon: AzureVMIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'SQL Databases',
        fragment: 'sql',
        value: 6,
        icon: AzureSqlIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'SQL Managed Instances',
        fragment: 'sql-mi',
        value: 8,
        icon: AzureSqlIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'Blob Container',
        fragment: 'blob',
        value: 7,
        icon: AzureBlobIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
    ];

    const gcpOptions = [
      {
        name: 'Compute Engine',
        fragment: 'compute-engine',
        value: 5,
        icon: GCPComputeEngineIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'Cloud SQL',
        fragment: 'cloud-sql',
        value: 6,
        icon: GCPCloudSQLIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
      {
        name: 'Cloud Storage',
        fragment: 'cloud-storage',
        value: 7,
        icon: GCPCloudStorageIcon,
        tabOptions: [
          { id: 'summary', text: 'Summary', value: 0, fragment: 'summary', icon: SummaryIconBlue },
          {
            id: 'optimize',
            text: 'Optimize',
            value: 1,
            showCustomRounded: true,
            showBottomMargin: true,
            fragment: 'optimize',
            icon: OptimizeIconBlue,
          },
          { id: 'events', text: 'Events', value: 3, fragment: 'events', icon: AllEventsIcon },
          { id: 'instances', text: 'Instances', value: 2, fragment: 'instances', icon: ApplicationsIconblue },
        ],
      },
    ];

    const cfOptions = [
      {
        name: 'Apps',
        fragment: 'cf-apps',
        value: 5,
        icon: CloudFoundryIcon,
        tabOptions: [
          { id: 'instances', text: 'Applications', value: 0, fragment: 'instances', icon: ApplicationsIconblue },
          { id: 'events', text: 'Events', value: 2, fragment: 'events', icon: AllEventsIcon },
        ],
      },
      {
        name: 'Organizations',
        fragment: 'cf-organizations',
        value: 6,
        icon: CloudFoundryIcon,
        tabOptions: [
          { id: 'instances', text: 'Organizations', value: 0, fragment: 'instances', icon: ApplicationsIconblue },
          { id: 'events', text: 'Events', value: 1, fragment: 'events', icon: AllEventsIcon },
        ],
      },
      {
        name: 'Spaces',
        fragment: 'cf-spaces',
        value: 7,
        icon: CloudFoundryIcon,
        tabOptions: [
          { id: 'instances', text: 'Spaces', value: 0, fragment: 'instances', icon: ApplicationsIconblue },
          { id: 'events', text: 'Events', value: 1, fragment: 'events', icon: AllEventsIcon },
        ],
      },
      {
        name: 'Routes',
        fragment: 'cf-routes',
        value: 8,
        icon: CloudFoundryIcon,
        tabOptions: [{ id: 'instances', text: 'Routes', value: 0, fragment: 'instances', icon: ApplicationsIconblue }],
      },
    ];

    const merged =
      selectedCluster?.cloud_provider === 'AWS'
        ? [...baseOptions, ...awsOptions]
        : selectedCluster?.cloud_provider === 'Azure'
        ? [...baseOptions, ...azureOptions]
        : selectedCluster?.cloud_provider === 'GCP'
        ? [...baseOptions, ...gcpOptions]
        : selectedCluster?.cloud_provider === 'CloudFoundry'
        ? [
            ...baseOptions.map((o) => {
              if (o.name === 'Services') return { ...o, hidden: true };
              if (o.name === 'Monitoring' || o.name === 'Optimize') return { ...o, disabled: true };
              return o;
            }),
            ...cfOptions,
          ]
        : baseOptions;
    return merged.sort((a, b) => a.value - b.value);
  }, [selectedCluster]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .cloudAccountSummary(accountId)
      .then((res) => {
        setClusterSummary(res);
        setLoading(false);
      })
      .catch((error) => {
        console.error(error);
        setLoading(false);
      });
  }, [accountId]);

  const getFilterValue = (fragment) => filterOptions.find((opt) => opt.fragment === fragment)?.value;

  const FRAGMENT_TO_SERVICE = {
    vm: SERVICE_NAME.VM,
    ec2: SERVICE_NAME.EC2,
    rds: SERVICE_NAME.RDS,
    s3: SERVICE_NAME.S3,
    ecs: SERVICE_NAME.ECS,
    blob: SERVICE_NAME.BLOB,
    sql: SERVICE_NAME.SQL,
    'sql-mi': SERVICE_NAME.SQL_MI,
    'compute-engine': SERVICE_NAME.COMPUTE_ENGINE,
    'cloud-sql': SERVICE_NAME.CLOUD_SQL,
    'cloud-storage': SERVICE_NAME.CLOUD_STORAGE,
    'cf-apps': SERVICE_NAME.CF_APPS,
    'cf-organizations': SERVICE_NAME.CF_ORGS,
    'cf-spaces': SERVICE_NAME.CF_SPACES,
    'cf-routes': SERVICE_NAME.CF_ROUTES,
  };

  const getServiceName = () => {
    const match = Object.entries(FRAGMENT_TO_SERVICE).find(([frag]) => selectedFilter === getFilterValue(frag));
    return match ? match[1] : '';
  };

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        options={filterOptions[selectedFilter]?.options || []}
        filterOptions={filterOptions}
        onChangeFilter={(val, subtab) => {
          setSelectedFilter(val);
          setSelectedSubTab(subtab);
          setOptimizeRadioTabValue('right-sizing');
        }}
      />
      {selectedCluster?.account_access === 'readonly' && (
        <Alert severity='info' sx={{ mt: 1, mb: 1 }}>
          This account is connected in read-only mode. CloudWatch alarm creation, EventBridge event tracking, and automated recommendation actions are
          unavailable.
        </Alert>
      )}
      <ErrorBoundary key={`${accountId}-${selectedFilter}-${selectedSubTab}`}>
        <Box>
          {[filterOptions[0].value].includes(selectedFilter) && (
            <>
              <CloudAccountSummary
                accountId={accountId}
                clusterSummary={clusterSummary}
                loading={loading}
                cloudProvider={selectedCluster?.cloud_provider || ''}
              />
              {selectedCluster?.cloud_provider === 'CloudFoundry' && <CFSummaryDetails accountId={accountId} />}
            </>
          )}
          {selectedFilter === getFilterValue('optimize') &&
            (!loading ? (
              <>
                {selectedSubTab == 0 && (
                  <CloudOptimizeRecommendationsTable
                    category='RightSizing'
                    accountId={accountId}
                    stickyColumnIndex='8'
                    tableHeadingCenter={['Severity']}
                  />
                )}
                {selectedSubTab == 1 && (
                  <CloudOptimizeRecommendationsTable
                    category='Configuration'
                    accountId={accountId}
                    accountAccess={selectedCluster?.account_access}
                    stickyColumnIndex='7'
                    tableHeadingCenter={['Severity']}
                  />
                )}
                {selectedSubTab == 2 && (
                  <CloudOptimizeRecommendationsTable
                    category='Security'
                    accountId={accountId}
                    stickyColumnIndex='7'
                    tableHeadingCenter={['Severity']}
                  />
                )}
                {selectedSubTab == 3 && (
                  <CloudOptimizeRecommendationsTable
                    category='InfraUpgrade'
                    accountId={accountId}
                    stickyColumnIndex='8'
                    tableHeadingCenter={['Severity']}
                  />
                )}
                {selectedSubTab == 4 && <ListingRecommendationResolution accountId={accountId} />}
              </>
            ) : (
              <Loader />
            ))}
          {selectedFilter === getFilterValue('services') &&
            (!loading ? (
              <CloudAccountServices
                accountId={accountId}
                tableHeadingCenter={['Action']}
                stickyColumnIndex='5'
                provider={selectedCluster?.cloud_provider}
              />
            ) : (
              <Loader />
            ))}
          {selectedFilter === getFilterValue('events') && (
            <>
              {selectedSubTab === 0 && <CloudAccountEvents title accountId={accountId} stickyColumnIndex='8' heading={''} />}
              {selectedSubTab === 1 && <TriageRulesManager accountId={accountId} />}
              {selectedSubTab === 2 && <ThresholdSuggestionsManager accountId={accountId} />}
            </>
          )}
          {selectedFilter === getFilterValue('monitoring') &&
            (!loading ? (
              <>
                {selectedSubTab === 0 && <CloudAccountAlertManager accountId={accountId} />}
                {selectedSubTab === 1 && <CloudLogsViewer accountId={accountId} provider={selectedCluster?.cloud_provider || 'AWS'} />}
                {selectedSubTab === 2 && <CloudMetricsViewer accountId={accountId} provider={selectedCluster?.cloud_provider || 'AWS'} />}
              </>
            ) : (
              <Loader />
            ))}
          {(selectedFilter === getFilterValue('ec2') ||
            selectedFilter === getFilterValue('vm') ||
            selectedFilter === getFilterValue('compute-engine')) && (
            <>
              {selectedSubTab === 0 && <Ec2Summary accountId={accountId} serviceName={getServiceName()} showSummary={true} />}
              {selectedSubTab === 1 && (
                <>
                  <Box sx={{ display: 'flex', bgcolor: '#FFFFFF', p: '0px 24px', mb: '12px', borderRadius: '0px 0px 8px 8px' }}>
                    <RadioGroup row value={optimizeRadioTabValue} sx={{ pb: '8px' }} onChange={handleChangeOptimizationTab}>
                      {optimizeRadioTab.map((item) => {
                        return (
                          <FormControlLabel key={item.value} value={item.value} control={<Radio />} disabled={item.disabled} label={item.label} />
                        );
                      })}
                    </RadioGroup>
                  </Box>
                  {optimizeRadioTabValue === 'right-sizing' && (
                    <CloudOptimizeRecommendationsTable category='RightSizing' accountId={accountId} serviceName={getServiceName()} />
                  )}
                  {optimizeRadioTabValue === 'configuration' && (
                    <CloudOptimizeRecommendationsTable
                      category='Configuration'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'security' && (
                    <CloudOptimizeRecommendationsTable
                      category='Security'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'infra-upgrade' && (
                    <CloudOptimizeRecommendationsTable
                      category='InfraUpgrade'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'8'}
                    />
                  )}
                </>
              )}
              {selectedSubTab === 2 && (
                <Ec2Instances accountId={accountId} serviceName={getServiceName()} tableHeadingCenter={['Severity']} stickyColumnIndex={'8'} />
              )}
              {selectedSubTab === 3 && <CloudAccountEvents accountId={accountId} serviceName={getServiceName()} />}
            </>
          )}
          {selectedFilter === getFilterValue('cf-apps') && (
            <>
              {selectedSubTab === 0 && (
                <CFInstances accountId={accountId} serviceName={getServiceName()} tableHeadingCenter={['Action']} stickyColumnIndex={'5'} />
              )}
              {selectedSubTab === 1 && (
                <CloudOptimizeRecommendationsTable category='RightSizing' accountId={accountId} serviceName={getServiceName()} />
              )}
              {selectedSubTab === 2 && <CloudAccountEvents accountId={accountId} serviceName={getServiceName()} />}
            </>
          )}
          {(selectedFilter === getFilterValue('cf-organizations') ||
            selectedFilter === getFilterValue('cf-spaces') ||
            selectedFilter === getFilterValue('cf-routes')) && (
            <>
              {selectedSubTab === 0 && <CFResources accountId={accountId} serviceName={getServiceName()} />}
              {selectedSubTab === 1 && <CloudAccountEvents accountId={accountId} serviceName={getServiceName()} />}
            </>
          )}
          {(selectedFilter === getFilterValue('rds') ||
            selectedFilter === getFilterValue('sql') ||
            selectedFilter === getFilterValue('sql-mi') ||
            selectedFilter === getFilterValue('cloud-sql')) && (
            <>
              {selectedSubTab === 0 && <RdsSummary accountId={accountId} serviceName={getServiceName()} />}
              {selectedSubTab === 1 && (
                <>
                  <Box sx={{ display: 'flex', bgcolor: '#FFFFFF', p: '0px 24px', mb: '12px', borderRadius: '0px 0px 8px 8px' }}>
                    <RadioGroup row value={optimizeRadioTabValue} sx={{ pb: '8px' }} onChange={handleChangeOptimizationTab}>
                      {optimizeRadioTab.map((item) => {
                        return (
                          <FormControlLabel key={item.value} value={item.value} control={<Radio />} disabled={item.disabled} label={item.label} />
                        );
                      })}
                    </RadioGroup>
                  </Box>
                  {optimizeRadioTabValue === 'right-sizing' && (
                    <CloudOptimizeRecommendationsTable
                      category='RightSizing'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'8'}
                    />
                  )}
                  {optimizeRadioTabValue === 'configuration' && (
                    <CloudOptimizeRecommendationsTable
                      category='Configuration'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'security' && (
                    <CloudOptimizeRecommendationsTable
                      category='Security'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'infra-upgrade' && (
                    <CloudOptimizeRecommendationsTable category='InfraUpgrade' accountId={accountId} serviceName={getServiceName()} />
                  )}
                </>
              )}
              {selectedSubTab === 2 && <RdsInstances accountId={accountId} serviceName={getServiceName()} stickyColumnIndex={'7'} />}
              {selectedSubTab === 3 && <CloudAccountEvents accountId={accountId} serviceName={getServiceName()} stickyColumnIndex={'8'} />}
            </>
          )}
          {(selectedFilter === getFilterValue('s3') ||
            selectedFilter === getFilterValue('blob') ||
            selectedFilter === getFilterValue('cloud-storage')) && (
            <>
              {selectedSubTab === 0 && <S3Summary accountId={accountId} serviceName={getServiceName()} />}
              {selectedSubTab === 1 && (
                <>
                  <Box sx={{ display: 'flex', bgcolor: '#FFFFFF', p: '0px 24px', mb: '12px', borderRadius: '0px 0px 8px 8px' }}>
                    <RadioGroup row value={optimizeRadioTabValue} sx={{ pb: '8px' }} onChange={handleChangeOptimizationTab}>
                      {optimizeRadioTab.map((item) => {
                        return (
                          <FormControlLabel key={item.value} value={item.value} control={<Radio />} disabled={item.disabled} label={item.label} />
                        );
                      })}
                    </RadioGroup>
                  </Box>
                  {optimizeRadioTabValue === 'right-sizing' && (
                    <CloudOptimizeRecommendationsTable category='RightSizing' accountId={accountId} serviceName={getServiceName()} />
                  )}
                  {optimizeRadioTabValue === 'configuration' && (
                    <CloudOptimizeRecommendationsTable
                      category='Configuration'
                      accountId={accountId}
                      accountAccess={selectedCluster?.account_access}
                      serviceName={getServiceName()}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'security' && (
                    <CloudOptimizeRecommendationsTable category='Security' accountId={accountId} serviceName={getServiceName()} />
                  )}
                  {optimizeRadioTabValue === 'infra-upgrade' && (
                    <CloudOptimizeRecommendationsTable category='InfraUpgrade' accountId={accountId} serviceName={getServiceName()} />
                  )}
                </>
              )}
              {selectedSubTab === 2 && <S3Instances accountId={accountId} serviceName={getServiceName()} stickyColumnIndex={'5'} />}
              {selectedSubTab === 3 && <CloudAccountEvents accountId={accountId} serviceName={getServiceName()} />}
              {selectedSubTab === 4 && <CloudAccountMetrices accountId={accountId} serviceName={getServiceName()} />}
            </>
          )}
          {selectedFilter === getFilterValue('ecs') && (
            <>
              {selectedSubTab === 0 && <ECSSummary accountId={accountId} serviceName={getServiceName()} showSummary={true} />}
              {selectedSubTab === 1 && (
                <>
                  <Box sx={{ display: 'flex', bgcolor: '#FFFFFF', p: '0px 24px', mb: '12px', borderRadius: '0px 0px 8px 8px' }}>
                    <RadioGroup row value={optimizeRadioTabValue} sx={{ pb: '8px' }} onChange={handleChangeOptimizationTab}>
                      {optimizeRadioTab.map((item) => {
                        return (
                          <FormControlLabel key={item.value} value={item.value} control={<Radio />} disabled={item.disabled} label={item.label} />
                        );
                      })}
                    </RadioGroup>
                  </Box>
                  {optimizeRadioTabValue === 'right-sizing' && (
                    <CloudOptimizeRecommendationsTable category='RightSizing' accountId={accountId} serviceName={getServiceName()} />
                  )}
                  {optimizeRadioTabValue === 'configuration' && (
                    <CloudOptimizeRecommendationsTable
                      category='Configuration'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'security' && (
                    <CloudOptimizeRecommendationsTable
                      category='Security'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'7'}
                    />
                  )}
                  {optimizeRadioTabValue === 'infra-upgrade' && (
                    <CloudOptimizeRecommendationsTable
                      category='InfraUpgrade'
                      accountId={accountId}
                      serviceName={getServiceName()}
                      tableHeadingCenter={['Severity']}
                      stickyColumnIndex={'8'}
                    />
                  )}
                </>
              )}
              {selectedSubTab === 2 && (
                <ECSInstances accountId={accountId} serviceName={getServiceName()} tableHeadingCenter={['Severity']} stickyColumnIndex={'8'} />
              )}
              {selectedSubTab === 3 && <CloudAccountEvents accountId={accountId} serviceName={getServiceName()} />}
            </>
          )}
          {selectedFilter === getFilterValue('security') && (!loading ? <CloudAccountSecurity accountId={accountId} /> : <Loader />)}
          {selectedFilter === getFilterValue('tools') && (!loading ? <CloudAccountTools accountId={accountId} /> : <Loader />)}
        </Box>
      </ErrorBoundary>
    </>
  );
};

export default CloudAccounts;
