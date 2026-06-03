import React from 'react';
import { Box, ListItemText } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { Link } from '@components1/ds/Link';
import Text from '@common-new/format/Text';
import CustomAccordion from '@components1/common/CustomAccordion';
import { titleCase } from '@lib/formatter';
import { getLast24Hrs } from '@lib/datetime';
import { ds } from '@utils/colors';
import {
  attrAppErrorIcon as AttrAppErrorIcon,
  attrAppIcongreen as AttrAppIconGreen,
  attrCpuIcon,
  attrCpuNewIcon as AttrCpuNewIcon,
  attrDiscIcon as AttrDiscIcon,
  attrErrorIcon as AttrErrorIcon,
  attrFixIcon as AttrFixIcon,
  attrPodIcon as AttrPodIcon,
  attrPVIconGreen as AttrPVIconGreen,
  attrRightSizingIcon as AttrRightSizingIcon,
  attrServiceIcon as AttrServiceIcon,
  InvestigateHomeIcon,
  ExternalLinkIcon,
} from '@assets';

// --- Helper Functions ---

const createNavigationLink = (accountId, fragment) => {
  if (accountId && fragment) {
    return `/kubernetes/details/${accountId}#${fragment}`;
  }
  return '';
};

const getMoreLink = (accountId, rule) => {
  if (!rule || Object.keys(rule).length === 0) {
    return '';
  }

  const fragment = rule.subcategory === 'Events' ? 'events/all-events' : rule.subcategory === 'Trace' ? 'monitoring/traces' : null;
  if (!fragment) {
    return '';
  }

  const uiFilters = rule.ui_filters || [];
  const queryParams = new URLSearchParams();

  uiFilters.forEach(({ name, value }) => {
    const key = name === 'aggregation_key' ? 'eventAggregationKey' : titleCase(name);
    queryParams.append(key, value);
  });

  const baseUrl = `/kubernetes/details/${accountId}#${fragment}`;
  const queryString = queryParams.toString();

  return queryString ? `${baseUrl}&${queryString}` : baseUrl;
};

const getApplicationLink = (accountId, rule, workloadFqdn) => {
  if (!rule || Object.keys(rule).length === 0) {
    return '/home';
  }

  const fragment = rule.subcategory === 'Events' ? 'events/all-events' : rule.subcategory === 'Trace' ? 'monitoring/traces' : null;
  if (!fragment) {
    return '/home';
  }

  const uiFilters = rule.ui_filters || [];
  const queryParams = new URLSearchParams();

  uiFilters.forEach(({ name, value }) => {
    const key = name === 'aggregation_key' ? 'eventAggregationKey' : titleCase(name);
    queryParams.append(key, value);
  });

  if (workloadFqdn) {
    const [workloadName, namespaceName] = workloadFqdn.split(':');
    if (workloadName && namespaceName) {
      queryParams.append(rule.subcategory == 'Events' ? 'eventSubjectName' : 'destinationWorkload', workloadName);
      queryParams.append(rule.subcategory == 'Events' ? 'eventNamespace' : 'destinationNamespace', namespaceName);
    }
  }
  queryParams.append('start_time', getLast24Hrs().getTime());
  queryParams.append('end_time', new Date().getTime());
  const baseUrl = `/kubernetes/details/${accountId}#${fragment}`;
  const queryString = queryParams.toString();

  return queryString ? `${baseUrl}&${queryString}` : baseUrl;
};

// --- Sub-Components ---

const RenderApplications = React.memo(({ applications, rule, accountId }) => {
  if (!applications || applications.length === 0) {
    return null;
  }

  if (applications.length <= 3) {
    return applications.map((app, index) => (
      <React.Fragment key={`${app.name}-${app.namespace || index}`}>
        <Link
          style={{ marginLeft: ds.space[1], textDecoration: 'none' }}
          href={getApplicationLink(accountId, rule, `${app.name}:${app.namespace}`)}
          target='_blank'
        >
          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
            <Text value={app.name} maxSize={12} sx={{ color: 'var(--ds-blue-500)', fontSize: 'var(--ds-text-body)' }} />
            <SafeIcon src={InvestigateHomeIcon} width={18} height={18} />
          </Box>
        </Link>
        {index < applications.length - 1 && ', '}
      </React.Fragment>
    ));
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space.mul(0, 5), ml: ds.space.mul(0, 5) }}>
      {applications.slice(0, 3).map((app, _index) => (
        <Box key={`${app.name}-${app.namespace || _index}`} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
          <Link href={getApplicationLink(accountId, rule, `${app.name}:${app.namespace}`)} target='_blank'>
            <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
              <Text
                value={app.name}
                maxSize={40}
                sx={{ color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-body)' }}
                className='application-name'
              />
              <Text value={'Investigate'} maxSize={12} sx={{ color: 'var(--ds-blue-500)', fontSize: 'var(--ds-text-body)' }} />
            </Box>
          </Link>
        </Box>
      ))}
      <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Link
          style={{ margin: `0px ${ds.space[1]}`, textDecoration: 'none', color: 'var(--ds-gray-600)' }}
          href={getMoreLink(accountId, rule)}
          target='_blank'
        >
          <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
            <SafeIcon src={ExternalLinkIcon} alt='external link' height={14} width={14} />
            <Text
              value={`show ${applications.length - 3} more`}
              maxSize={14}
              sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)' }}
            />
          </Box>
        </Link>
      </Box>
    </Box>
  );
});

const RenderListItemText = React.memo(({ item, imageSrc, message, ruleLink, linkTitle, accountId }) => {
  return (
    <ListItemText
      primary={
        <Box sx={{ '& a': { textDecoration: 'none', '&:hover': { '.application-name': { textDecoration: 'underline' } } } }}>
          <CustomAccordion
            title={message}
            icon={<SafeIcon src={imageSrc} className='list-icon' width={22} height={22} />}
            summaryStyle={{
              backgroundColor: `var(--ds-background-100) !important`,
              borderTop: '0px !important',
              borderRight: '0px !important',
              borderLeft: '0px !important',
              borderBottom: `0.6px dashed var(--ds-gray-300) !important`,
              padding: `${ds.space[0]} 0px !important`,
            }}
            detailsStyle={{ border: '0px !important' }}
            titleStyle={{ fontSize: 'var(--ds-text-small) !important', fontWeight: 'var(--ds-font-weight-regular) !important' }}
          >
            <RenderApplications applications={item.applications} rule={item.rule} accountId={accountId} />
            {ruleLink && (
              <Box sx={{ '& a': { color: 'var(--ds-blue-300)' } }}>
                <Link href={ruleLink} target='_blank' style={{ color: 'var(--ds-blue-500)' }}>
                  <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                    {linkTitle}
                    <SafeIcon src={AttrFixIcon} height={18} width={18} />
                  </Box>
                </Link>
              </Box>
            )}
          </CustomAccordion>
        </Box>
      }
    />
  );
});

// --- Main Component ---

const TroubleshootList = React.memo(({ data, type, accountId }) => {
  const isTroubleshooting = type === 'troubleshooting';
  const isOptimization = type === 'optimization';

  if (isTroubleshooting) {
    switch (data.unique_id) {
      case '2':
        return (
          <RenderListItemText item={data} imageSrc={AttrAppErrorIcon} message='API Error Rate increased (>50%) detected for' accountId={accountId} />
        );
      case '12':
        return <RenderListItemText item={data} imageSrc={AttrCpuNewIcon} message='High CPU Usage (>80%) detected for' accountId={accountId} />;
      case '5':
        return <RenderListItemText item={data} imageSrc={AttrDiscIcon} message='High Memory Usage (>80%) detected for' accountId={accountId} />;
      case '4':
        return <RenderListItemText item={data} imageSrc={AttrPodIcon} message='Frequent Pod Restart detected for' accountId={accountId} />;
      case '1':
        return <RenderListItemText item={data} imageSrc={AttrErrorIcon} message='API Latency (>500ms) detected for' accountId={accountId} />;
      case '15':
        return <RenderListItemText item={data} imageSrc={attrCpuIcon} message='High Latency (>500ms) detected for' accountId={accountId} />;
      default:
        return null;
    }
  } else if (isOptimization) {
    switch (data.unique_id) {
      case '16':
        return (
          <RenderListItemText
            item={data}
            imageSrc={AttrServiceIcon}
            message={data.title}
            ruleLink={createNavigationLink(accountId, 'optimize/right-sizing')}
            linkTitle='Right Size Now!'
            accountId={accountId}
          />
        );
      case '15':
        return (
          <RenderListItemText
            item={data}
            imageSrc={AttrPVIconGreen}
            message={data.title}
            ruleLink={createNavigationLink(accountId, 'optimize/unused-volume')}
            linkTitle='Check & Remove'
            accountId={accountId}
          />
        );
      case '14':
        return (
          <RenderListItemText
            item={data}
            imageSrc={AttrRightSizingIcon}
            message={data.title}
            ruleLink={createNavigationLink(accountId, 'optimize/pv-rightsizing')}
            linkTitle='Right Size Now!'
            accountId={accountId}
          />
        );
      case '13':
        return (
          <RenderListItemText
            item={data}
            imageSrc={AttrAppIconGreen}
            message={data.title}
            ruleLink={createNavigationLink(accountId, 'optimize/abandoned-resources')}
            linkTitle='Check & Remove'
            accountId={accountId}
          />
        );
      default:
        return null;
    }
  }
  return null;
});

export default TroubleshootList;
