import { Box, Grid, List, ListItem, ListItemText, Typography } from '@mui/material';
import React, { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import homeApi from '@api1/home';
import { v4 as uuidv4 } from 'uuid';
import apiAskNudgebee from '@api1/ask-nudgebee';
import QuickLink from '@assets/home/new/quick-link.icon.svg';
import RecentErrorIcon from '@assets/home/new/recent-error.icon.svg';
import MatricsIcon from '@assets/home/new/metrics_icon.icon.svg';
import PodsIcon from '@assets/home/new/pods_icon.icon.svg';
import ServiceMapsIcon from '@assets/home/new/service_maps_icon.icon.svg';
import ArrowRightWhiteIcon from '@assets/arrow-right-white.icon.svg';
import OptimizeIconHome from '@assets/home/optimize-icon-home.icon.svg';
import K8sOpsHomeIcon from '@assets/home/k8s-ops-icon.icon.svg';
import InvestigateHomeIcon from '@assets/home/Investigate-button-icon.icon.svg';
import AttrErrorIcon from '@assets/home/new_home_icons/Error-with-BG.icon.svg';
import AttrCVEIcon from '@assets/home/new_home_icons/Images-with-BG.icon.svg';
import AttrConfigureIcon from '@assets/home/new_home_icons/Configure-with-BG.icon.svg';
import AttrServiceIcon from '@assets/home/new_home_icons/Service-with-BG.icon.svg';
import AttrRightSizingIcon from '@assets/home/new_home_icons/Right-size-with-BG.icon.svg';
import AttrCertificateExpireIcon from '@assets/home/new_home_icons/error-16-svgrepo-com2.icon.svg';
import PvcSightSizing from '@assets/kubernetes/optimize-icons/pv-right-sizing.icon.svg';
import TroubleshootIconBlue from '@assets/header/TroubleshootIconBlue.icon.svg';
import OptimizeIconBlue from '@assets/header/optimize-blue.icon.svg';
import NewExpandIcon from '@assets/expand-new-icon.icon.svg';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import DataBaseBlueIcon from '@assets/kubernetes/app-nodes-icons/database-blue.icon.svg';
import SirenBlueIcon from '@assets/home/new/siren-rounded-blue.icon.svg';
import TicketBlueIcon from '@assets/home/new/ticket-blue.icon.svg';
import RepoBlueIcon from '@assets/home/new/repo-forked-blue.icon.svg';
import SlackIcon from '@assets/slack_icon.icon.svg';
import MsTeamsIcon from '@assets/ou-management/ms_teams.icon.svg';
import GChatIcon from '@assets/gchat-icon.icon.svg';
import PagerDutyIcon from '@assets/auto-pilot/pager-duty.svg';
import ServiceNowIcon from '@assets/servicenow.icon.svg';
import JiraIcon from '@assets/jira_icon.icon.svg';
import GithubIcon from '@assets/github-icon.icon.svg';
import TroubleshootHeadingIcon from '@assets/home/troubleshoot-icon.icon.svg';
import LogsIcon from '@assets/home/logs-icon.icon.svg';
import TraceIcon from '@assets/home/traces-icon.icon.svg';
import NamespacesIcon from '@assets/kubernetes/app-nodes-icons/namespace-icon.icon.svg';
import SecurityIcon from '@assets/home/security-icon.icon.svg';
import AWSEC2Icon from '@assets/cloud-account/ec2-icon.icon.svg';
import AWSRDSIcon from '@assets/cloud-account/rds-icon.icon.svg';
import AWSS3Icon from '@assets/cloud-account/s3-icon.icon.svg';
import AWSECSIcon from '@assets/cloud-account/ecs-icon.icon.svg';
import AzureVMIcon from '@assets/cloud-account/azure-vm.icon.svg';
import AzureSqlIcon from '@assets/cloud-account/azure-sql.icon.svg';
import AzureBlobIcon from '@assets/cloud-account/azure-blob.icon.svg';
import GCPComputeEngineIcon from '@assets/cloud-account/gcp-compute-engine.icon.svg';
import GCPCloudSQLIcon from '@assets/cloud-account/gcp-cloud-sql.icon.svg';
import GCPCloudStorageIcon from '@assets/cloud-account/gcp-cloud-storage.icon.svg';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import Link from 'next/link';
import apiWorkflow from '@api1/workflow';
import { getLast24Hrs } from '@lib/datetime';
import WidgetCard from '@components1/common/WidgetCard';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { Textarea } from '@components1/k8s/common/TextArea';
import { Text } from '@components1/common';
import TextWithBorder from '@components1/common/TextWithBorder';
import K8sAccountModal from '@components1/common/K8sAccountModal';
import CustomCollapseable from '@components1/common/CustomCollapseable';
import SafeIcon from '@components1/common/SafeIcon';
import { getUserSession } from '@lib/auth';
import { FiArrowRight } from 'react-icons/fi';
import useCurrencySymbol from '@hooks/useCurrencySymbol';
import PendingFollowUps from '@components1/home/PendingFollowUps';

const replaceCurrencyInText = (text, targetCurrencySymbol) => {
  if (!text || targetCurrencySymbol === '$') return text;
  return text.replace(/\$(\d[\d,]*\.?\d*)/g, `${targetCurrencySymbol}$1`);
};

const FILTER_COLUMN_TO_PARAM = {
  status: 'status',
  eventstatus: 'eventStatus',
  category: 'category',
  severity: 'severity',
  rule_name: 'rule_name',
  source: 'source',
  aggregation_key: 'aggregation_key',
  subject_name: 'subject_name',
};

const applyFiltersToLink = (baseLink, filters) => {
  if (!baseLink || !filters || filters.length === 0) return baseLink;
  const hashIndex = baseLink.indexOf('#');
  const [pathAndQuery, hash] = hashIndex >= 0 ? [baseLink.slice(0, hashIndex), baseLink.slice(hashIndex)] : [baseLink, ''];
  let result = pathAndQuery;
  for (const filter of filters) {
    if (!filter?.value || (Array.isArray(filter?.value) && filter?.value.length === 0)) {
      continue;
    }
    const param = FILTER_COLUMN_TO_PARAM[filter.column?.toLowerCase()];
    if (!param) continue;
    const paramRegex = new RegExp(`[?&]${param}=`);
    if (paramRegex.test(result)) continue;
    const value = Array.isArray(filter.value) ? filter.value.join(',') : filter.value;
    const separator = result.includes('?') ? '&' : '?';
    result = `${result}${separator}${param}=${value}`;
  }
  return `${result}${hash}`;
};

// Extracts named placeholder values from a title using an insight_format template.
// e.g. format = "Most frequent issue: {aggregation_key} ({} FIRING events)"
//      title  = "Most frequent issue: RabbitmqUnroutableMessages (2058 FIRING events)"
//      returns { aggregation_key: "RabbitmqUnroutableMessages" }
//
// Named placeholders  {key} → regex named capture group (?<key>.+?)
// Unnamed placeholders {}   → non-capturing group        .+?   (value discarded)
const extractFromFormat = (format, title) => {
  if (!format || !title) return {};

  const placeholderRegex = /\{(\w*)\}/g;
  let match;
  const placeholders = [];
  while ((match = placeholderRegex.exec(format)) !== null) {
    placeholders.push({ name: match[1], index: match.index, length: match[0].length });
  }
  if (placeholders.length === 0) return {};

  let regexStr = '^';
  let lastIndex = 0;
  for (const ph of placeholders) {
    regexStr += format.slice(lastIndex, ph.index).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    regexStr += ph.name ? `(?<${ph.name}>.+?)` : '.+?';
    lastIndex = ph.index + ph.length;
  }
  regexStr += format.slice(lastIndex).replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$';

  try {
    return new RegExp(regexStr).exec(title)?.groups ?? {};
  } catch {
    return {};
  }
};

const specialUniqueIdsForApplyFilter = {
  129: {
    keys: ['aggregation_key'],
    defaultFilters: [
      {
        column: 'eventStatus',
        value: 'FIRING',
      },
    ],
  },
  127: {
    keys: ['subject_name'],
    defaultFilters: null,
  },
  17: {
    keys: [],
    defaultFilters: [
      {
        column: 'severity',
        value: ['Critical', 'High'],
      },
      {
        column: 'status',
        value: 'Open',
      },
    ],
  },
  114: {
    keys: [],
    defaultFilters: [
      {
        column: 'severity',
        value: ['Low'],
      },
    ],
  },
};

const Card = ({ item = {}, type = '', accountId: _accountId = '', currencySymbol = '$' }) => {
  const getInsightLink = (rule) => {
    if (rule?.redirect_url) {
      return rule.redirect_url;
    }
    return null;
  };

  const getApplicationLink = (rule, workloadFqdn) => {
    if (!rule?.redirect_url) {
      return null;
    }

    // For application-level links, append workload info to the redirect URL
    if (workloadFqdn) {
      const [workloadName, namespaceName] = workloadFqdn.split(':');
      if (workloadName && namespaceName) {
        const url = new URL(rule.redirect_url, 'http://placeholder');
        if (rule.subcategory === 'Events') {
          url.searchParams.set('eventSubjectName', workloadName);
          url.searchParams.set('eventNamespace', namespaceName);
        } else if (rule.subcategory === 'LogGroup') {
          url.searchParams.set('workloadNamespace', namespaceName);
          url.searchParams.set('workloadName', workloadName);
        } else {
          url.searchParams.set('destinationWorkload', workloadName);
          url.searchParams.set('destinationNamespace', namespaceName);
        }
        return `${url.pathname}?${url.searchParams.toString()}${url.hash}`;
      }
    }
    return rule.redirect_url;
  };

  const cardStyle = {
    row: {
      display: 'flex',
      alignItems: 'center',
      m: '6px 0',
      fontSize: '14px',
      fontWeight: 400,
      color: '#282828',
    },
  };

  const renderApplications = (applications, rule) => {
    if (applications == 'null' || !applications || applications.length === 0) {
      return null;
    }
    if (typeof applications === 'string') {
      try {
        applications = JSON.parse(applications);
      } catch {
        return null;
      }
    }

    return applications.length <= 3 ? (
      applications.map((app, index) => {
        const appLink = getApplicationLink(rule, `${app.name}:${app.namespace}`);
        return (
          <React.Fragment key={app.name}>
            {appLink ? (
              <Link
                style={{
                  marginLeft: '5px',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                  textDecoration: 'none',
                }}
                href={appLink}
                target='_blank'
              >
                <Text
                  value={app.name}
                  showAutoEllipsis
                  sx={{
                    color: colors.text.primary,
                    fontSize: '13px',
                    maxWidth: '120px',
                    wordBreak: app.name && app.name.length > 5 ? 'break-all' : '',
                  }}
                />
                <SafeIcon src={InvestigateHomeIcon} height={13} width={13} />
              </Link>
            ) : (
              <Text
                value={app.name}
                showAutoEllipsis
                sx={{
                  color: colors.text.greyDark,
                  fontSize: '13px',
                  marginLeft: '5px',
                  maxWidth: '120px',
                  wordBreak: app.name && app.name.length > 5 ? 'break-all' : '',
                }}
              />
            )}
            {index < applications.length - 1 && <Typography sx={{ color: colors.text.lastSync, fontSize: '12px', mx: '4px' }}>|</Typography>}
          </React.Fragment>
        );
      })
    ) : (
      <Box sx={{ display: 'flex', alignItems: 'center', ml: '5px' }}>
        {applications.slice(0, 4).map((app, index) => {
          const appLink = getApplicationLink(rule, `${app.name}:${app.namespace}`);
          return (
            <React.Fragment key={app.name}>
              {appLink ? (
                <Link
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '6px',
                    textDecoration: 'none',
                  }}
                  href={appLink}
                  target='_blank'
                >
                  <Text
                    value={app.name}
                    showAutoEllipsis={app.name && app.name.length > 5}
                    sx={{
                      color: colors.text.primary,
                      fontSize: '13px',
                      maxWidth: '120px',
                      wordBreak: app.name && app.name.length > 5 ? 'break-all' : '',
                    }}
                  />
                  <SafeIcon src={InvestigateHomeIcon} height={13} width={13} />
                </Link>
              ) : (
                <Text
                  value={app.name}
                  showAutoEllipsis={app.name && app.name.length > 5}
                  sx={{
                    color: colors.text.greyDark,
                    fontSize: '13px',
                    maxWidth: '120px',
                    wordBreak: app.name && app.name.length > 5 ? 'break-all' : '',
                  }}
                />
              )}
              {index < 3 && <Typography sx={{ color: colors.text.lastSync, fontSize: '12px', mx: '4px' }}>|</Typography>}
            </React.Fragment>
          );
        })}
        {applications.length > 4 &&
          (() => {
            const moreLink = getInsightLink(rule);
            return moreLink ? (
              <Link
                style={{
                  marginLeft: '4px',
                  textDecoration: 'underline',
                  textDecorationColor: colors.text.primary,
                  textDecorationThickness: '1px',
                }}
                href={moreLink}
                target='_blank'
              >
                <Text value={`${applications.length - 4} more`} sx={{ color: colors.text.primary, fontSize: '12px' }} />
              </Link>
            ) : (
              <Text value={`and ${applications.length - 4} more`} sx={{ color: colors.text.greyDark, fontSize: '12px', marginLeft: '4px' }} />
            );
          })()}
      </Box>
    );
  };

  const renderListItemText = (item, cardStyle, imageSrc, message, ruleLink, linkTitle) => (
    <ListItemText
      primary={
        <Box
          sx={{
            ...cardStyle.row,
            '& a': {
              fontSize: '13px',
              marginLeft: '5px',
              color: colors.text.primary,
              fontWeight: 400,
              textDecoration: 'underline !important',
              textDecorationColor: `${colors.text.primary} !important`,
              textDecorationThickness: '1% !important',
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
            },
          }}
        >
          <SafeIcon src={imageSrc} alt='icon' className='list-icon' width={24} height={24} />
          <Box sx={{ marginLeft: 2, display: 'flex', alignItems: 'center' }}>
            <span>{message}</span>
            {renderApplications(item.applications, item.rule)}
          </Box>
          {ruleLink && (
            <Link href={ruleLink} target='_blank'>
              {linkTitle}
            </Link>
          )}
        </Box>
      }
    />
  );

  const getIconForInsight = (item) => {
    const cat = item?.rule?.category || item?.type;
    const source = item?.rule?.source;
    if (source === 'Event') {
      return AttrErrorIcon;
    }
    switch (cat) {
      case 'Troubleshooting':
        return AttrErrorIcon;
      case 'Optimization':
      case 'Cost':
        return AttrRightSizingIcon;
      case 'Security':
        return AttrCVEIcon;
      case 'Configuration':
        return AttrConfigureIcon;
      case 'Ops':
        return AttrServiceIcon;
      case 'InfraUpgrade':
        return AttrServiceIcon;
      case 'Performance':
        return AttrErrorIcon;
      default:
        return AttrErrorIcon;
    }
  };

  const getLinkLabel = (item) => {
    const cat = item?.rule?.category || item?.type;
    switch (cat) {
      case 'Troubleshooting':
        return 'Investigate';
      case 'Security':
        return 'Secure Now';
      case 'Configuration':
        return 'Configure';
      case 'Ops':
        return 'View Details';
      case 'Optimization':
      case 'Cost':
        return 'Optimize';
      case 'InfraUpgrade':
        return 'Upgrade';
      case 'Performance':
        return 'Investigate';
      default:
        return 'View Details';
    }
  };

  const renderTroubleshootListItem = (item, type, cardStyle) => {
    let link = getInsightLink(item.rule);
    const icon = getIconForInsight(item);
    const label = getLinkLabel(item);
    const hasApplications = item.applications && item.applications.length > 0;
    // Apply currency replacement to title
    const displayTitle = item.rawTitle ? replaceCurrencyInText(item.rawTitle, currencySymbol) : item.title;
    const message = hasApplications ? `${displayTitle} detected for` : displayTitle;

    if (link && item.rule?.filters) {
      link = applyFiltersToLink(link, item.rule.filters);
    }
    if (link && specialUniqueIdsForApplyFilter[item?.rule?.unique_id]) {
      const { keys, defaultFilters } = specialUniqueIdsForApplyFilter[item.rule.unique_id];
      if (defaultFilters) {
        link = applyFiltersToLink(link, defaultFilters);
      }
      const extracted = extractFromFormat(item.rule.insight_format, item.title);
      const extractedFilters = keys.filter((key) => extracted[key]).map((key) => ({ column: key, value: extracted[key] }));
      link = applyFiltersToLink(link, extractedFilters);
    }

    return renderListItemText(item, cardStyle, icon, message, link, label);
  };

  const content = type === 'troubleshooting' || type === 'optimization' || type == 'Ops' ? renderTroubleshootListItem(item, type, cardStyle) : null;

  return (
    <>
      {content ? (
        <ListItem
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            width: '100%',
            p: 0,
            borderBottom: '0.4px dashed transparent',
            background: 'linear-gradient(to right,rgb(216, 216, 216) 30%, transparent 50%) 0 100% repeat-x',
            backgroundSize: '10px 0.8px',
            '&:last-child': {
              borderBottom: 'none',
            },
            '& .list-icon': {
              width: '24px',
              height: '24px',
              objectFit: 'contain',
            },
            '& img': {
              width: '16px',
              height: '16px',
              objectFit: 'contain',
            },
            '& .MuiListItemText-root': {
              m: 0,
            },
          }}
        >
          {content}
        </ListItem>
      ) : null}
    </>
  );
};
Card.propTypes = {
  item: PropTypes.object,
  type: PropTypes.string,
  accountId: PropTypes.string,
  currencySymbol: PropTypes.string,
};

const renderContent = (title, accountId, cloudProvider) => {
  const isK8s = cloudProvider === 'K8s';
  const detailsBase = isK8s ? '/kubernetes/details' : '/cloud-account/details';
  const getLink = (type) => {
    if (type == 'workflow') {
      return `/auto-pilot?${accountId}#workflow`;
    } else if (type == 'image-scan') {
      return `/kubernetes/details/${accountId}#security/image-scan`;
    } else if (type == 'certificate') {
      return `/kubernetes/details/${accountId}#security/ssl-certificate-issues`;
    } else if (type == 'upgrade') {
      return `/kubernetes/details/${accountId}#security/cluster-upgrade`;
    }
  };

  switch (title) {
    case 'Troubleshoot':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={2}>
            <Grid item xs={9} sx={{ display: 'flex', alignItems: 'center' }}>
              <Box>
                <Typography sx={{ mb: 1, fontSize: '16px', fontweight: '400', lineHeight: '21.09px', color: colors.text.greyDark }}>
                  Just added this account? Awesome! Give me about an hour to generate insights.
                </Typography>
                <Box
                  sx={{
                    fontSize: '14px',
                    fontweight: '400',
                    lineHeight: '28px',
                    color: colors.text.greyDark,
                  }}
                >
                  While you wait, you can explore your{' '}
                  <Link href={`${detailsBase}/${accountId}#summary`}>
                    <CustomButton
                      sx={{
                        padding: '0px 8px !important',
                        fontSize: '12px',
                        color: '#1B2D4A',
                        backgroundColor: '#FFFFFF',
                        border: '0.5px solid #1B2D4A',
                        height: '22px',
                        alignItems: 'center',
                        gap: '4px',
                        minWidth: 'fit-content',
                        '& .MuiButton-endIcon svg,img': {
                          height: '14px',
                          width: '17px',
                          filter:
                            'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                        },
                        '&:hover': {
                          backgroundColor: '#FFFFFF',
                        },
                      }}
                      text={'cluster'}
                      endIcon={
                        <Box
                          sx={{
                            backgroundColor: '#FACF39',
                            borderRadius: '2px',
                            height: '14px',
                            display: 'flex',
                            justifyContent: 'center',
                            alignItems: 'center',
                          }}
                        >
                          <FiArrowRight />
                        </Box>
                      }
                    />
                  </Link>{' '}
                  or check out the{' '}
                  <Link href={`/troubleshoot?accountId=${accountId}`}>
                    <CustomButton
                      sx={{
                        padding: '0px 8px !important',
                        fontSize: '12px',
                        color: '#1B2D4A',
                        backgroundColor: '#FFFFFF',
                        border: '0.5px solid #1B2D4A',
                        height: '22px',
                        alignItems: 'center',
                        gap: '4px',
                        minWidth: 'fit-content',
                        '& .MuiButton-endIcon svg,img': {
                          height: '14px',
                          width: '17px',
                          filter:
                            'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                        },
                        '&:hover': {
                          backgroundColor: '#FFFFFF',
                        },
                      }}
                      text={'Troubleshooting'}
                      endIcon={
                        <Box
                          sx={{
                            backgroundColor: '#FACF39',
                            borderRadius: '2px',
                            height: '14px',
                            display: 'flex',
                            justifyContent: 'center',
                            alignItems: 'center',
                          }}
                        >
                          <FiArrowRight />
                        </Box>
                      }
                    />
                  </Link>{' '}
                  for specific issues.
                </Box>
              </Box>
            </Grid>
            <Grid item xs={3}>
              <SafeIcon src={getBrandingAsset('troubleshootBee')} alt='Bee with magnifying glass' width={154} height={150} />
            </Grid>
          </Grid>
        </Box>
      );

    case 'Optimize':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={2}>
            <Grid item xs={3} sx={{ '@media (max-width: 1200px)': { pl: '0px !important', pr: '20px !important' } }}>
              <SafeIcon src={getBrandingAsset('optimizeBee')} alt='Bee with magnifying glass' width={150} height={150} />
            </Grid>
            <Grid item xs={9} sx={{ display: 'flex', alignItems: 'center' }}>
              <Box>
                <Typography sx={{ mb: 1, fontSize: '16px', fontweight: '400', lineHeight: '21.09px', color: colors.text.greyDark }}>
                  I can generate some quick optimization tips, but the best ones come from watching trends for a day or up to 7 days.
                </Typography>
                <Box sx={{ fontSize: '14px', fontweight: '400', lineHeight: '28px', color: colors.text.greyDark }}>
                  In the meantime, check your{' '}
                  <Link href={`${detailsBase}/${accountId}#optimize/summary`}>
                    <CustomButton
                      sx={{
                        padding: '0px 8px !important',
                        fontSize: '12px',
                        color: '#1B2D4A',
                        backgroundColor: '#FFFFFF',
                        border: '0.5px solid #1B2D4A',
                        height: '22px',
                        alignItems: 'center',
                        gap: '4px',
                        minWidth: 'fit-content',
                        '& .MuiButton-endIcon svg,img': {
                          height: '14px',
                          width: '17px',
                          filter:
                            'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                        },
                        '&:hover': {
                          backgroundColor: '#FFFFFF',
                        },
                      }}
                      text={'cluster'}
                      endIcon={
                        <Box
                          sx={{
                            backgroundColor: '#FACF39',
                            borderRadius: '2px',
                            height: '14px',
                            display: 'flex',
                            justifyContent: 'center',
                            alignItems: 'center',
                          }}
                        >
                          <FiArrowRight />
                        </Box>
                      }
                    />
                  </Link>{' '}
                  or check out the{' '}
                  <Link href={`/optimize?accountId=${accountId}`}>
                    <CustomButton
                      sx={{
                        padding: '0px 8px !important',
                        fontSize: '12px',
                        color: '#1B2D4A',
                        backgroundColor: '#FFFFFF',
                        border: '0.5px solid #1B2D4A',
                        height: '22px',
                        alignItems: 'center',
                        gap: '4px',
                        minWidth: 'fit-content',
                        '& .MuiButton-endIcon svg,img': {
                          height: '14px',
                          width: '17px',
                          filter:
                            'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                        },
                        '&:hover': {
                          backgroundColor: '#FFFFFF',
                        },
                      }}
                      text={'Optimize'}
                      endIcon={
                        <Box
                          sx={{
                            backgroundColor: '#FACF39',
                            borderRadius: '2px',
                            height: '14px',
                            display: 'flex',
                            justifyContent: 'center',
                            alignItems: 'center',
                          }}
                        >
                          <FiArrowRight />
                        </Box>
                      }
                    />
                  </Link>{' '}
                  section for plenty of options!
                </Box>
              </Box>
            </Grid>
          </Grid>
        </Box>
      );

    case 'K8s Ops Agent':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={2}>
            <Grid item xs={9}>
              <Typography sx={{ mb: 2, fontSize: '18px', fontweight: '400', lineHeight: '21.09px', color: colors.text.secondary }}>
                I can help with a bunch of things! Just tell me what you need
              </Typography>
              {[
                { label: 'Scan images for vulnerabilities', action: 'Start Scan', type: 'image-scan' },
                { label: 'Check certificate expire', action: 'View Status', type: 'certificate' },
                { label: 'Create automations', action: 'Create Now', type: 'workflow' },
                { label: "Upgrading K8s? Let's figure it out", action: 'Explore upgrade path', type: 'upgrade' },
              ].map((item, index) => (
                <Box key={index} sx={{ borderBottom: '0.5px dotted #D0D0D0', padding: '9px 0px 0px 0px', width: '60%' }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px', mb: 1, flexWrap: 'wrap' }}>
                    <Typography sx={{ fontSize: '14px', fontweight: '400', lineHeight: '18px', color: '#1B2D4A' }}>{item.label}</Typography>
                    <Link href={getLink(item.type)}>
                      <CustomButton
                        sx={{
                          padding: '0px 8px !important',
                          fontSize: '12px',
                          color: '#1B2D4A',
                          backgroundColor: '#FFFFFF',
                          border: '0.5px solid #1B2D4A',
                          height: '22px',
                          alignItems: 'center',
                          gap: '4px',
                          minWidth: 'fit-content',
                          '& .MuiButton-endIcon svg,img': {
                            height: '14px',
                            width: '17px',
                            filter:
                              'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                          },
                          '&:hover': {
                            backgroundColor: '#FFFFFF',
                          },
                        }}
                        text={item.action}
                        endIcon={
                          <Box
                            sx={{
                              backgroundColor: '#FACF39',
                              borderRadius: '2px',
                              height: '14px',
                              display: 'flex',
                              justifyContent: 'center',
                              alignItems: 'center',
                            }}
                          >
                            <FiArrowRight />
                          </Box>
                        }
                      />
                    </Link>
                  </Box>
                </Box>
              ))}
            </Grid>
            <Grid item xs={3}>
              <SafeIcon src={getBrandingAsset('k8sBee')} alt='Bee with magnifying glass' width={150} height={152} />
            </Grid>
          </Grid>
        </Box>
      );

    default:
      return <Box>Default content</Box>;
  }
};

const CardsBlock = ({
  icon,
  title,
  items = [],
  type = '',
  severityData = [],
  accountId = '',
  loadingInsights = false,
  hasExternalData = false,
  currencySymbol = '$',
  cloudProvider = '',
}) => {
  const generateRows = (items) => {
    return items.map((item) => (
      <Card item={item} key={item.id} type={type} severityData={severityData} accountId={accountId} currencySymbol={currencySymbol} />
    ));
  };

  const hasItems = items?.length > 0;
  const shouldShowEmptyState = !hasItems && !loadingInsights && !hasExternalData;

  const renderingItem = () => {
    if (shouldShowEmptyState) {
      return renderContent(title, accountId, cloudProvider);
    }
    return generateRows(items);
  };

  return (
    <Box
      sx={{
        position: 'relative',
        padding: '16px 0px 10px 0px',
        mr: '24px',
        '@media (max-width: 1200px)': {
          padding: '12px 0px 12px 0px',
        },
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
          background: colors.background.primaryLightest,
          padding: '8px',
          borderRadius: '4px',
          '& img': {
            width: '24px',
            height: '24px',
            objectFit: 'contain',
            borderBottom: '2px solid',
          },
          '& .dropdown-icon': {
            width: '18px',
            height: '18px',
            borderBottom: 'none',
          },
        }}
      >
        <Box display='flex' flexDirection='column' alignItems='center' gap='2px'>
          <SafeIcon src={icon || TroubleshootHeadingIcon} alt={title} width={24} height={24} />
        </Box>
        <Typography fontSize={'20px'} fontWeight={'600'} color={colors.text.secondary}>
          {title}
        </Typography>
      </Box>
      <List
        dense
        sx={{
          display: 'block',
          padding: '8px 16px 4px 16px',
          margin: 0,
          '@media (max-width: 1200px)': {
            padding: '0px',
          },
        }}
      >
        {renderingItem()}
      </List>
    </Box>
  );
};
CardsBlock.propTypes = {
  icon: PropTypes.any,
  title: PropTypes.any,
  items: PropTypes.array,
  type: PropTypes.string,
  severityData: PropTypes.array,
  accountId: PropTypes.string,
  loadingInsights: PropTypes.bool,
  hasExternalData: PropTypes.bool,
  currencySymbol: PropTypes.string,
  cloudProvider: PropTypes.string,
};

const buildUrl = (selectedCluster, id, fragment, navigate, additionalQuery = {}) => {
  let route = '';

  if (navigate === 'details') {
    const isK8s = selectedCluster?.cloud_provider === 'K8s';
    const base = isK8s ? '/kubernetes/details' : '/cloud-account/details';

    // Construct Query Params
    const params = new URLSearchParams();

    if (additionalQuery && Object.keys(additionalQuery).length > 0) {
      Object.entries(additionalQuery).forEach(([key, value]) => {
        if (value !== undefined && value !== null) {
          params.set(key, value);
        }
      });
    }

    const queryString = params.toString();
    const queryPart = queryString ? `?${queryString}` : '';
    const fragmentPart = fragment ? `#${fragment}` : '';

    // Construct Full URL: /path/id?query=params#fragment
    route = `${base}/${id}${queryPart}${fragmentPart}`;
  } else if (navigate === 'auto-pilot') {
    route = `/auto-pilot?accountId=${id}`;
  }

  return route;
};

const HomeWidgets = ({ quickLinksData, selectedCluster, cluster }) => {
  const links = quickLinksData
    .filter((d) => d.cloudProvider === selectedCluster?.cloud_provider)
    .map((data) => data.links.map((link) => ({ ...link })))
    .flat();

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mb: '8px' }}>
        <SafeIcon src={QuickLink} width='18px' height='18px' />
        <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>Quick Links</Typography>
      </Box>
      <WidgetCard sx={{ mt: '0px', padding: '12px 16px', overflow: 'hidden', boxShadow: 'none' }}>
        <Box
          my='10px'
          display={'grid'}
          gridTemplateColumns={'1fr 1fr'}
          gap={'7px'}
          sx={{
            '@media (max-width: 1250px)': {
              gridTemplateColumns: '1fr',
            },
          }}
        >
          {links.map((link) => {
            const filterColor = {};
            if (link?.base === 'white-dominant') {
              filterColor.filter = 'invert(42%) sepia(93%) saturate(1352%) hue-rotate(201deg) brightness(95%) contrast(101%)';
            } else if (link?.base === 'black-dominant') {
              filterColor.filter = 'invert(28%) sepia(78%) saturate(1804%) hue-rotate(201deg) brightness(95%) contrast(90%)';
            }
            return (
              <Link
                // REFACTORED: Passing link.fragment instead of link.tab/link.subtab
                href={buildUrl(selectedCluster, cluster, link.fragment, 'details', {})}
                key={link.name}
                style={{ textDecoration: 'none' }}
              >
                <Box
                  key={link.name}
                  display={'flex'}
                  alignItems={'center'}
                  gap='8px'
                  padding='4px 8px'
                  borderRadius='4px'
                  sx={{
                    cursor: 'pointer',
                    '&:hover': {
                      backgroundColor: colors.background.primaryLightest,
                    },
                    '& .colored-icon': {
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      transition: 'filter 0.25s ease',
                      ...filterColor,
                      '& img, & svg': {
                        maxWidth: '100%',
                        maxHeight: '100%',
                        objectFit: 'contain',
                      },
                    },
                  }}
                >
                  <Box
                    sx={{
                      width: '27px',
                      height: '27px',
                      minWidth: '27px',
                      minHeight: '27px',
                      borderRadius: '4px',
                      bgcolor: '#EFF6FF',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}
                  >
                    <SafeIcon src={link.icon} alt={link.name} className='colored-icon' width='16px' height='16px' />
                  </Box>
                  <Typography fontSize={'12px'} fontWeight={400} color={colors.text.secondary}>
                    {link.name}
                  </Typography>
                </Box>
              </Link>
            );
          })}
        </Box>
      </WidgetCard>
    </Box>
  );
};
HomeWidgets.propTypes = {
  quickLinksData: PropTypes.any,
  selectedCluster: PropTypes.any,
  cluster: PropTypes.any,
};
HomeWidgets.propTypes = {
  quickLinksData: PropTypes.any,
  selectedCluster: PropTypes.any,
  cluster: PropTypes.any,
};

const Home = () => {
  const router = useRouter();
  const [cluster, setCluster] = useState(router.query.accountId);
  const [insightData, setInsightData] = useState([]);
  const [workflowData, setWorkflowData] = useState({ totalCount: 0, configuredCount: 0, actionedCount: 0 });
  const [imageScanData, setImageScanData] = useState({});
  const [certificateData, setCertificateData] = useState({});
  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [loadingInsights, setLoadingInsights] = useState({
    troubleshooting: false,
    k8sOps: false,
  });
  const [loadingConversation, setLoadingConversation] = useState(false);
  const { selectedCluster, allCluster } = useData();
  const textareaRef = useRef(null);
  const currencySymbol = useCurrencySymbol(cluster);
  const homeStyle = {
    paragraphTypography: { fontSize: '12px', fontWeight: 400, color: colors.tertiary, width: 'max-content' },
    h4: {
      fontSize: '16px',
      fontWeight: 600,
      color: colors.text.secondary,
      margin: '0px 0px 2px 0px',
    },

    listItem: {
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'flex-start',
      width: '100%',
      gap: '12px',
      paddingBottom: '12px',
      borderBottom: '0.5px solid #D0D0D0',
      '& img': {
        width: '24px',
        height: '24px',
        objectFit: 'contain',
        mr: '10px',
      },
      '& .MuiListItemText-root': {
        m: 0,
      },
    },

    row: {
      display: 'flex',
      alignItems: 'center',
      m: '6px 0px',
      fontSize: '14px',
      fontWeight: 400,
      color: '#374151',
    },
  };

  // Map integers to fragments based on KubernetesDetails config
  const QuickLinksData = [
    {
      links: [
        {
          name: 'Query Logs',
          fragment: 'monitoring/logs', // Tab 4, Subtab 0
          icon: LogsIcon,
        },
        {
          name: 'Recent Errors',
          fragment: 'monitoring/groups', // Tab 4, Subtab 1
          icon: RecentErrorIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Query Metrics',
          // Note: In your config, both Logs and Metrics had fragment 'query'.
          // Ensure your Router config distinguishes them, or this will open Logs.
          fragment: 'monitoring/query', // Tab 4, Subtab 2
          icon: MatricsIcon,
        },
      ],
      insights: [],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'View Traces',
          fragment: 'monitoring/traces', // Tab 4, Subtab 5
          icon: TraceIcon,
        },
        {
          name: 'Service Maps',
          fragment: 'monitoring/service-map', // Tab 4, Subtab 6
          icon: ServiceMapsIcon,
        },
      ],
      insights: [],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'View Applications',
          fragment: 'kubernetes/applications', // Tab 3, Subtab 1
          icon: NamespacesIcon,
        },
        {
          name: 'View Pods',
          fragment: 'kubernetes/pods', // Tab 3, Subtab 3
          icon: PodsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Security',
          fragment: 'security/image-scan', // Tab 5, Subtab 0
          icon: SecurityIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/summary', // Tab 2, Subtab 0
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/summary', // Tab 1, Subtab 7
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    // --- Non-K8s Providers (Assumed Fragments) ---
    {
      links: [
        {
          name: 'Cloud Logs',
          fragment: 'monitoring/cloud-logs',
          icon: LogsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/right-sizing',
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Services',
          fragment: 'services',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'EC2',
          fragment: 'ec2/summary',
          icon: AWSEC2Icon,
          base: 'black-dominant',
        },
        {
          name: 'RDS',
          fragment: 'rds/summary',
          icon: AWSRDSIcon,
          base: 'black-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'S3',
          fragment: 's3/summary',
          icon: AWSS3Icon,
          base: 'black-dominant',
        },
        {
          name: 'ECS',
          fragment: 'ecs/summary',
          icon: AWSECSIcon,
          base: 'black-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Cloud Logs',
          fragment: 'monitoring/cloud-logs',
          icon: LogsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/right-sizing',
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Services',
          fragment: 'services',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'VM',
          fragment: 'vm/summary',
          icon: AzureVMIcon,
          base: 'white-dominant',
        },
        {
          name: 'SQL',
          fragment: 'sql/summary',
          icon: AzureSqlIcon,
          base: 'white-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Blob Container',
          fragment: 'blob/summary',
          icon: AzureBlobIcon,
          base: 'white-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Cloud Logs',
          fragment: 'monitoring/cloud-logs',
          icon: LogsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/right-sizing',
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Services',
          fragment: 'services',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Compute Engine',
          fragment: 'compute-engine/summary',
          icon: GCPComputeEngineIcon,
        },
        {
          name: 'Cloud SQL',
          fragment: 'cloud-sql/summary',
          icon: GCPCloudSQLIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Cloud Storage',
          fragment: 'cloud-storage/summary',
          icon: GCPCloudStorageIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    // --- CloudFoundry ---
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'CloudFoundry',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Apps',
          fragment: 'cf-apps/instances',
          icon: NamespacesIcon,
        },
        {
          name: 'Organizations',
          fragment: 'cf-organizations/instances',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'CloudFoundry',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Spaces',
          fragment: 'cf-spaces/instances',
          icon: PvcSightSizing,
        },
        {
          name: 'Routes',
          fragment: 'cf-routes/instances',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'CloudFoundry',
      accountId: cluster,
    },
  ];

  const integrationData = [
    {
      id: 'messaging',
      anchor: 'messaging',
      startIcon: DataBaseBlueIcon,
      title: 'Connect to your messaging tool',
      options: [
        { name: 'Slack', icon: SlackIcon, redirect: '/accounts/account-form?cloudProvider=SLACK' },
        { name: 'MS Teams', icon: MsTeamsIcon, redirect: '/accounts/account-form?cloudProvider=MSTEAMS' },
        { name: 'G Chat', icon: GChatIcon, redirect: '/accounts/account-form?cloudProvider=GOOGLE_CHAT' },
      ],
      actionText: 'Add Messaging',
      actionIcon: <FiArrowRight />,
    },
    {
      id: 'pagerduty',
      anchor: 'messaging',
      startIcon: SirenBlueIcon,
      title: 'Using PagerDuty? You can connect to your',
      options: [{ name: 'PagerDuty', icon: PagerDutyIcon, redirect: '/accounts/account-form?cloudProvider=PAGERDUTY' }],
      actionText: 'Add PagerDuty',
      actionIcon: <FiArrowRight />,
    },
    {
      id: 'ticketing',
      anchor: 'ticket',
      startIcon: TicketBlueIcon,
      title: 'I offer integrations with your Ticketing system',
      options: [
        { name: 'Service Now', icon: ServiceNowIcon, redirect: '/accounts/account-form?cloudProvider=SERVICENOW' },
        { name: 'Jira', icon: JiraIcon, redirect: '/accounts/account-form?cloudProvider=JIRA' },
      ],
      actionText: 'Add Ticketing',
      actionIcon: <FiArrowRight />,
    },
    {
      id: 'code',
      anchor: 'repo',
      startIcon: RepoBlueIcon,
      title: 'btw, I can also integrate with your code repo',
      options: [],
      actionText: 'Add GitHub',

      actionIcon: <FiArrowRight />,
      actionStartIcon: GithubIcon,
    },
  ];

  const footerSections = [
    {
      type: 'integrations',
      title: "We've got many other integrations available for you to explore.",
      icon: DataBaseBlueIcon,
      action: {
        label: 'Check Integrations',
        redirect: '/user-management#integrations',
      },
    },
  ];

  useEffect(() => {
    if (router.query.accountId) {
      setCluster(router.query.accountId);
      setLoadingInsights({ troubleshooting: true, k8sOps: true });
    }
  }, [router.query.accountId]);

  useEffect(() => {
    if (!cluster) {
      return;
    }
    setLoadingInsights({
      troubleshooting: true,
      k8sOps: true,
    });
    getTroubleShootData(cluster);
    getWorkflowData(cluster);
    getImageScan(cluster);
    getCertificate(cluster);
  }, [cluster]);

  const getImageScan = async (cluster) => {
    setImageScanData({});
    homeApi
      .getImageScanData(cluster)
      .then((res) => {
        const recommendationSecurityData = res?.data?.data?.recommendation_security_groupings_v2?.rows || [];
        if (recommendationSecurityData.length > 0) {
          const totalCritical = recommendationSecurityData.reduce((sum, item) => sum + item.count_severity_critical, 0);
          const imageCount = recommendationSecurityData.reduce((sum, item) => sum + item.count_image, 0);
          const appCount = recommendationSecurityData.length;
          setImageScanData({ totalCritical, imageCount, appCount });
        }
      })
      .finally(() => {
        setLoadingInsights((prevState) => ({
          ...prevState,
          k8sOps: false,
        }));
      });
  };

  const getCertificate = async (cluster) => {
    setCertificateData({});
    homeApi
      .getCertificateIssue(cluster)
      .then((res) => {
        const recommendation = res?.data?.data?.recommendation?.rows || [];
        if (recommendation.length > 0) {
          const allRecommendations = recommendation.map((r) => r.recommendation);
          const parsedArray = allRecommendations.map(JSON.parse);
          const expiringSoon = parsedArray?.filter((item) => {
            const expiry = new Date(item.expiry_date);
            return expiry.getTime() - new Date().getTime() <= 30 * 24 * 60 * 60 * 1000;
          })?.length;
          if (expiringSoon) {
            setCertificateData({ expiringSoon });
          }
        }
      })
      .finally(() => {
        setLoadingInsights((prevState) => ({
          ...prevState,
          k8sOps: false,
        }));
      });
  };

  const getTroubleShootData = async (cluster) => {
    setInsightData([]);
    homeApi
      .getInsights(cluster)
      .then((res) => {
        const insights = res?.data?.data?.insight_v2?.rows || [];
        // Store raw titles for currency processing
        const insightsWithRaw = insights.map((item) => ({
          ...item,
          rawTitle: item.title,
        }));
        setInsightData(insightsWithRaw);
      })
      .finally(() => {
        setLoadingInsights((prevState) => ({
          ...prevState,
          troubleshooting: false,
        }));
      });
  };

  const getWorkflowData = async (accountId) => {
    setWorkflowData({ totalCount: 0, configuredCount: 0, actionedCount: 0 });
    const dateRange = {
      startDate: getLast24Hrs(),
      endDate: new Date(),
    };
    try {
      const [totalResponse, configuredResponse, actionedResponse] = await Promise.all([
        apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE' }),
        apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE', triggerType: 'event' }),
        apiWorkflow.getWorkflowExecutionCount(accountId, { startDate: dateRange.startDate }),
      ]);

      setWorkflowData({
        totalCount: totalResponse?.data?.workflow_get_count?.count ?? 0,
        configuredCount: configuredResponse?.data?.workflow_get_count?.count ?? 0,
        actionedCount: actionedResponse?.data?.workflow_get_execution_count?.count ?? 0,
      });
    } catch (error) {
      console.error('Failed to fetch workflow data:', error);
    } finally {
      setLoadingInsights((prevState) => ({
        ...prevState,
        k8sOps: false,
      }));
    }
  };

  const handleGenerateInvestigation = async () => {
    setLoadingConversation(true);
    const newSessionId = uuidv4();
    apiAskNudgebee
      .aiGenerateInvestigate({
        account_id: router.query.accountId,
        query: generateQuestionText,
        session_id: newSessionId,
      })
      .then((res) => {
        const response = res?.data?.data?.ai_trigger_investigation ?? {};
        if (!response?.data?.query) {
          snackbar.error('Cant process your request right now.');
          setLoadingConversation(false);
        } else {
          setLoadingConversation(false);
          setGenerateQuestionText('');
          router.push(`/ask-nudgebee?accountId=${router.query.accountId}&session_id=${newSessionId}`);
        }
      });
  };

  const closeModal = () => {
    setShowModal(false);
  };

  return (
    <Grid container spacing={6} mt='28px' sx={{ background: 'white' }}>
      <Grid item xs={9} sx={{ pt: '0px !important' }}>
        <Grid container>
          <K8sAccountModal openModal={showModal} handleClose={closeModal} />
          <Grid item xs={12} sx={{ mr: '24px', pb: '16px' }}>
            <SummaryBlock
              hideTitle
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: '10px',
                backgroundColor: colors.background.white,
                borderRadius: '8px',
                border: `0.4px solid #9AADCC !important`,
                boxShadow: '0px 2px 7px 0px #3B82F60F,0px 4px 6px -1px #3B82F61F',
                padding: '8px 16px',
                '& textarea': {
                  width: '100%',
                  border: '0px',
                  resize: 'none',
                  boxShadow: 'none',
                  color: colors.text.secondary,
                  fontWeight: 400,
                  fontSize: '16px',
                  padding: '0px 12px 0px 0px !important',
                  '&::placeholder': { color: colors.text.secondaryDark, fontWeight: 400, fontSize: '16px' },
                  '&:focus': {
                    boxShadow: 'none',
                  },
                  '&::-webkit-scrollbar': {
                    display: 'none',
                  },
                },
                '& .MuiOutlinedInput-notchedOutline': {
                  border: '0px !important',
                },
                '& button': {
                  padding: '0px 10px !important',
                },
              }}
            >
              <Box
                sx={{
                  width: '100%',
                  position: 'relative',
                  marginTop: !generateQuestionText ? '-12px' : '6px',
                  '&:hover': {
                    cursor: 'pointer',
                  },
                  '& label': {
                    fontSize: '12px',
                    fontWeight: '400',
                    fontStyle: 'italic',
                    color: colors.text.secondaryDark,
                    position: 'absolute',
                    top: '24px',
                    left: 0,
                    width: '100%',
                    '@media (max-width: 1280px)': {
                      fontSize: '11px',
                    },
                  },
                }}
              >
                <Textarea
                  ref={textareaRef}
                  id='custom-textarea'
                  fontSize='16px'
                  fontWeight='400'
                  value={generateQuestionText}
                  maxLength={500000}
                  placeholder={'How can I assist you today?'}
                  onChange={(e) => {
                    setGenerateQuestionText(e.target.value);
                  }}
                  sx={{
                    ':disabled': {
                      opacity: 0.5,
                    },
                  }}
                  maxRows={5}
                  disabled={loadingConversation}
                />
                {!generateQuestionText && (
                  <label htmlFor='custom-textarea'>Ask me about troubleshooting, error logs, resource usage or optimizations</label>
                )}
              </Box>

              <CustomButton
                id='ask-me-btn'
                sx={{ marginTop: '2px' }}
                size='Medium'
                onClick={() => {
                  handleGenerateInvestigation();
                }}
                disabled={!generateQuestionText || loadingConversation}
                startIcon={ArrowRightWhiteIcon}
              />
            </SummaryBlock>
          </Grid>
          {getUserSession()?.user?.name && allCluster?.length == 1 ? (
            <Grid item xs={12} sx={{ mr: '24px', pb: '16px' }}>
              <CustomCollapseable
                title={
                  <Typography
                    sx={{
                      fontSize: '18px',
                      color: '#33558A',
                      fontWeight: 400,
                      letterSpacing: '0%',
                    }}
                  >
                    Hi {(getUserSession()?.user?.name || '')?.split(' ')[0]}, Nice to have you here.
                  </Typography>
                }
                sx={{
                  position: 'relative',
                  backgroundColor: '#EFF6FF',
                  boxShadow: 'none',
                  pr: '20px',
                }}
                icon={<NewExpandIcon alt='expand icon' className='expand-icon' height={24} width={24} />}
                defaultExpand={allCluster?.length == 0 || false}
              >
                <Box
                  sx={{
                    display: 'grid',
                    gridTemplateColumns: '230px 1fr',
                    gap: '20px',
                    '@media (max-width: 1230px)': {
                      gridTemplateColumns: '150px 1fr',
                    },
                  }}
                >
                  <Box
                    sx={{
                      display: 'flex',
                      justifyContent: 'center',
                      alignItems: 'center',
                    }}
                  >
                    <Box
                      sx={{
                        display: 'flex',
                        justifyContent: 'center',
                        '& img': {
                          height: '236px',
                          width: '190px',
                          '@media (max-width: 1230px)': {
                            height: '166px',
                            width: '190px',
                          },
                        },
                      }}
                    >
                      <SafeIcon src={getBrandingAsset('newUserBee')} alt='user bees icon' width={190} height={236} />
                    </Box>
                  </Box>
                  <Box>
                    <Box>
                      <SummaryBlock
                        hideTitle
                        sx={{ padding: '16px', backgroundColor: '#FFFFFF', border: '0.5px solid #93C5FD', borderRadius: '8px' }}
                      >
                        <Grid container spacing={0}>
                          <Grid item xs={8}>
                            {getUserSession()?.user?.name && (
                              <Typography
                                sx={{
                                  fontSize: '18px',
                                  color: '#33558A',
                                  fontWeight: 400,
                                  pb: '5px',
                                  letterSpacing: '0%',
                                  '@media (max-width: 1280px)': {
                                    fontSize: '15px',
                                  },
                                }}
                              >
                                Hi {getUserSession()?.user?.name?.split(' ')[0]}, Nice to have you here.
                              </Typography>
                            )}
                            <Typography
                              sx={{
                                fontSize: '14px',
                                color: '#33558A',
                                fontWeight: 400,
                                letterSpacing: '0%',
                                '@media (max-width: 1280px)': {
                                  fontSize: '10px',
                                },
                              }}
                            >
                              You are currently on our Demo Account (which only has partial data) Let&apos;s get you started with your cluster/account
                            </Typography>
                          </Grid>
                          <Grid item xs={4} justifyContent='flex-end' alignItems={'center'} display='flex'>
                            <CustomButton
                              text='Add K8s Account'
                              size='Small'
                              sx={{
                                padding: '4px 12px',
                                fontSize: '12px',
                                fontWeight: '500',
                                lineHeight: '20px',
                                color: '#fff',
                                backgroundColor: '#213F6D',
                                boxShadow: '0px 2px 4px 0px #1C2E4B7D',
                                borderRadius: '4px',
                                cursor: 'pointer',
                                '&:hover': {
                                  backgroundColor: colors.text.secondary,
                                },
                              }}
                              onClick={() => {
                                router.push(`/user-management#integrations`);
                              }}
                            />
                          </Grid>
                        </Grid>
                      </SummaryBlock>
                    </Box>
                    <Box>
                      <Typography
                        sx={{
                          fontSize: '14px',
                          color: '#33558A',
                          fontWeight: 500,
                          letterSpacing: '0%',
                          pt: '10px',
                          pb: '5px',
                        }}
                      >
                        Other things you can do to make sure you get the full intelligent automation experience.
                      </Typography>
                      {integrationData.map((item, index) => (
                        <Box
                          key={item.id}
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'space-between',
                            py: '7px',
                            borderBottom: index < integrationData.length - 1 ? '1px solid #e0e0e0' : 'none',
                            '@media (max-width: 1300px)': {
                              display: 'block',
                            },
                          }}
                        >
                          <Box sx={{ display: 'flex', rowGap: '10px', flexWrap: 'wrap' }}>
                            <Box sx={{ display: 'flex', alignItems: 'center' }}>
                              {item.startIcon && <SafeIcon src={item.startIcon} alt={item.id} width={20} height={20} />}
                              <Typography
                                sx={{
                                  fontSize: '14px',
                                  color: '#33558A',
                                  fontWeight: 400,
                                  lineHeight: '18px',
                                  letterSpacing: '0%',
                                  pl: '5px',
                                  minWidth: 'fit-content',
                                }}
                              >
                                {item.title}
                              </Typography>
                            </Box>

                            {item.options && (
                              <Box
                                sx={{
                                  display: 'flex',
                                  ml: 1,
                                  mr: '5px',
                                  gap: '6px',
                                  flexWrap: 'wrap',
                                }}
                              >
                                {item.options.map((option) => (
                                  <Link href={option.redirect} key={option.name}>
                                    <CustomButton
                                      size='xSmall'
                                      startIcon={option.icon && <SafeIcon src={option.icon} alt={option.name} width={17} height={14} />}
                                      text={option.name}
                                      variant='secondary'
                                      onClick={undefined}
                                      sx={{
                                        padding: '0px 4px !important',
                                        fontSize: '12px',
                                        fontWeight: 400,
                                        color: '#33558A',
                                        backgroundColor: '#EFF6FF',
                                        border: '0.5px solid #B9B9B9',
                                        height: '22px',
                                        alignItems: 'center',
                                        '& .MuiButton-startIcon svg,img': {
                                          height: '14px',
                                          width: '17px',
                                          filter: 'none !important',
                                        },
                                        '&:hover': {
                                          backgroundColor: '#EFF6FF',
                                          color: '#33558A',
                                          cursor: 'inherit',
                                        },
                                      }}
                                    />
                                  </Link>
                                ))}
                              </Box>
                            )}
                          </Box>
                          <CustomButton
                            sx={{
                              padding: '0px 8px !important',
                              fontSize: '12px',
                              fontWeight: 400,
                              color: colors.text.greyDark,
                              backgroundColor: '#FFFFFF',
                              border: '0.5px solid #9F9F9F',
                              height: '22px',
                              alignItems: 'center',
                              gap: '4px',
                              minWidth: 'fit-content',

                              '& .MuiButton-endIcon svg,img': {
                                height: '14px',
                                width: '17px',
                                filter:
                                  'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                              },
                              '&:hover': {
                                backgroundColor: '#FFFFFF',
                              },
                              '@media (max-width: 1300px)': {
                                mt: '10px',
                                ml: '10px',
                              },
                            }}
                            onClick={() => {
                              router.push(`/user-management#integrations`);
                              // handleClose();
                            }}
                            text={item.actionText}
                            endIcon={
                              <Box
                                sx={{
                                  backgroundColor: '#FFE776',
                                  borderRadius: '2px',
                                  height: '14px',
                                  display: 'flex',
                                  justifyContent: 'center',
                                  alignItems: 'center',
                                }}
                              >
                                {item.actionIcon}
                              </Box>
                            }
                            startIcon={item.actionStartIcon && <SafeIcon src={item.actionStartIcon} alt={item.actionText} width={17} height={14} />}
                          />
                        </Box>
                      ))}
                      <Grid container spacing={2} mt={1}>
                        {footerSections.map((section) => {
                          return (
                            <Grid item xl={6} md={12} key={section.type}>
                              <SummaryBlock
                                hideTitle
                                sx={{
                                  minHeight: '84px',
                                  backgroundColor: '#F0FDF4',
                                  borderRadius: '8px',
                                  border: `0.5px solid #93C5FD !important`,
                                  boxShadow: 'none',
                                  padding: '12px 10px',
                                  position: 'relative',
                                }}
                              >
                                <Box
                                  sx={{
                                    display: 'flex',
                                    gap: '6px',
                                    '& p': {
                                      fontSize: '14px',
                                      fontweight: 400,
                                      color: '#33558A',
                                    },
                                  }}
                                >
                                  {section.icon && <SafeIcon src={section.icon} alt={section.title} width={20} height={20} />}
                                  <Typography>{section.title}</Typography>
                                </Box>
                                <Box>
                                  <CustomButton
                                    sx={{
                                      position: 'absolute',
                                      bottom: '12px',
                                      right: '12px',
                                      padding: '0px 8px !important',
                                      fontSize: '12px',
                                      fontWeight: 400,
                                      color: colors.text.greyDark,
                                      backgroundColor: '#FFFFFF',
                                      border: '0.5px solid #9F9F9F',
                                      height: '22px',
                                      gap: '4px',
                                      minWidth: 'fit-content',
                                      '& .MuiButton-startIcon svg,img': {
                                        height: '14px',
                                        width: '17px',
                                        filter: 'none !important',
                                        borderRadius: '2px',
                                      },
                                      '& .MuiButton-endIcon svg,img': {
                                        height: '14px',
                                        width: '17px',
                                        filter:
                                          'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                                      },
                                      '&:hover': {
                                        backgroundColor: '#FFFFFF',
                                      },
                                    }}
                                    onClick={() => {
                                      router.push(`/user-management#integrations`);
                                      // handleClose();
                                    }}
                                    text={section?.action?.label}
                                    endIcon={
                                      <Box
                                        sx={{
                                          backgroundColor: '#FFE776',
                                          borderRadius: '2px',
                                          height: '14px',
                                          display: 'flex',
                                          justifyContent: 'center',
                                          alignItems: 'center',
                                        }}
                                      >
                                        <FiArrowRight />
                                      </Box>
                                    }
                                    startIcon={
                                      section?.action?.icon && (
                                        <SafeIcon src={section?.action?.icon} alt={section.actionText} height={14} width={17} />
                                      )
                                    }
                                  />
                                </Box>
                              </SummaryBlock>
                            </Grid>
                          );
                        })}
                      </Grid>
                    </Box>
                  </Box>
                </Box>
              </CustomCollapseable>
            </Grid>
          ) : null}{' '}
        </Grid>
        <CardsBlock
          title='Troubleshoot'
          icon={TroubleshootHeadingIcon}
          items={(() => {
            const troubleshootItems = insightData.filter(
              (o) =>
                o.type == 'Troubleshooting' ||
                o.rule?.category == 'Troubleshooting' ||
                o.type == 'Performance' ||
                (selectedCluster?.cloud_provider != 'K8s' && o.type == 'Ops')
            );
            return troubleshootItems;
          })()}
          type={'troubleshooting'}
          severityData={[]}
          accountId={selectedCluster.value || ''}
          loadingInsights={loadingInsights.troubleshooting}
          currencySymbol={currencySymbol || '$'}
          cloudProvider={selectedCluster?.cloud_provider || ''}
        />
        {selectedCluster?.cloud_provider !== 'CloudFoundry' && (
          <CardsBlock
            title='Optimize'
            icon={OptimizeIconHome}
            items={(() => {
              const optimizeItems = insightData.filter(
                (g) =>
                  g.type == 'Optimization' ||
                  g.rule?.category == 'Optimization' ||
                  g.type == 'Cost' ||
                  g.type == 'InfraUpgrade' ||
                  g.type == 'Security' ||
                  g.type == 'Configuration'
              );
              return optimizeItems;
            })()}
            type={'optimization'}
            severityData={[]}
            accountId={selectedCluster?.value || ''}
            loadingInsights={loadingInsights.troubleshooting}
            currencySymbol={currencySymbol || '$'}
            cloudProvider={selectedCluster?.cloud_provider || ''}
          />
        )}
        {selectedCluster?.cloud_provider === 'K8s' && (
          <CardsBlock
            title='K8s Ops Agent'
            icon={K8sOpsHomeIcon}
            items={insightData.filter((g) => g.type == 'Ops' || g.rule?.category == 'Ops')}
            type={'Ops'}
            loadingInsights={loadingInsights.k8sOps}
            accountId={selectedCluster?.value || ''}
            currencySymbol={currencySymbol || '$'}
            hasExternalData={
              Object.keys(imageScanData).length > 0 ||
              Object.keys(certificateData).length > 0 ||
              workflowData.totalCount > 0 ||
              workflowData.configuredCount > 0 ||
              workflowData.actionedCount > 0
            }
          />
        )}
        {selectedCluster?.cloud_provider === 'K8s' && (
          <Box sx={{ padding: '4px 16px', display: 'flex', alignItems: 'center', flexDirection: 'column', gap: '12px' }}>
            {imageScanData && Object.keys(imageScanData).length > 0 ? (
              <Box sx={homeStyle.listItem}>
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  <TextWithBorder
                    value='Image Scan for vulnerabilities'
                    borderColor={'#3B82F6'}
                    borderWidth='3px'
                    fontSx={{
                      fontSize: '16px',
                      fontWeight: 500,
                      color: colors.text.secondary,
                    }}
                  />
                </Box>
                <Box sx={homeStyle.row}>
                  <SafeIcon src={AttrCVEIcon} width={24} height={24} />
                  <Box sx={{ marginLeft: 2, display: 'flex', alignItems: 'center' }}>
                    <span>{`${imageScanData.appCount} apps has ${imageScanData.totalCritical} critical CVEs in ${imageScanData.imageCount} images`}</span>
                  </Box>
                  <Link href={`/kubernetes/details/${selectedCluster?.value}?status=Open&severity=Critical#security/image-scan`} target='_blank'>
                    <CustomButton
                      sx={{ maxHeight: '16px', ml: '8px', fontWeight: 400, fontSize: '11px' }}
                      variant={'tertiary'}
                      size='xSmall'
                      text={'Check'}
                    />
                  </Link>
                </Box>
              </Box>
            ) : null}
            {certificateData && Object.keys(certificateData).length > 0 ? (
              <Box sx={homeStyle.listItem}>
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  <TextWithBorder
                    value='Certificates'
                    borderColor={'#3B82F6'}
                    borderWidth='3px'
                    fontSx={{
                      fontSize: '16px',
                      fontWeight: 500,
                      color: colors.text.secondary,
                    }}
                  />
                </Box>
                <Box
                  sx={{
                    ...homeStyle.row,
                    flexDirection: 'column',
                    alignItems: 'flex-start',
                  }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center' }}>
                    <SafeIcon src={AttrCertificateExpireIcon} width={24} height={24} />
                    <Box sx={{ marginLeft: 2, display: 'flex', alignItems: 'center' }}>
                      <span>{`${certificateData.expiringSoon} certificates expiring in less than 30 days`}</span>
                    </Box>
                    <Link href={`/kubernetes/details/${selectedCluster?.value}#security/ssl-certificate-issues`} target='_blank'>
                      <CustomButton
                        sx={{ maxHeight: '16px', ml: '8px', fontWeight: 400, fontSize: '11px' }}
                        variant='tertiary'
                        size='xSmall'
                        text='Check'
                      />
                    </Link>
                  </Box>
                </Box>
              </Box>
            ) : null}
            {workflowData && (workflowData.totalCount > 0 || workflowData.configuredCount > 0 || workflowData.actionedCount > 0) ? (
              <Box sx={homeStyle.listItem}>
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  <TextWithBorder
                    value='Automations'
                    borderColor={'#3B82F6'}
                    borderWidth='3px'
                    fontSx={{
                      fontSize: '16px',
                      fontWeight: 500,
                      color: colors.text.secondary,
                    }}
                  />
                </Box>
                <Box sx={homeStyle.row}>
                  <SafeIcon src={AttrConfigureIcon} width={24} height={24} />
                  <Box sx={{ marginLeft: 2, display: 'flex', alignItems: 'center' }}>
                    <span>{`${workflowData.totalCount} automations configured. [ triggered in 24hrs: ${workflowData.actionedCount} | event automations: ${workflowData.configuredCount} ]`}</span>
                  </Box>
                  <Link href={`/auto-pilot?accountId=${selectedCluster?.value}&status=Active`} target='_blank'>
                    <CustomButton
                      sx={{ maxHeight: '16px', ml: '8px', fontWeight: 400, fontSize: '11px' }}
                      variant={'tertiary'}
                      size='xSmall'
                      text={'Check'}
                    />
                  </Link>
                </Box>
              </Box>
            ) : null}
          </Box>
        )}
      </Grid>
      <Grid item xs={3} sx={{ pl: '15px !important', pt: '0px !important', display: 'flex', flexDirection: 'column', gap: '16px' }}>
        <PendingFollowUps accountId={cluster} />
        <HomeWidgets quickLinksData={QuickLinksData} selectedCluster={selectedCluster} cluster={cluster} />
      </Grid>
    </Grid>
  );
};

export default Home;
