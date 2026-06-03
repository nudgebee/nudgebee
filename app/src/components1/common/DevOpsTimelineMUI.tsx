import React, { useState, useEffect, useMemo } from 'react';
import { Box, Typography, Chip, Avatar, useTheme } from '@mui/material';
import { ErrorOutline, CheckCircleOutline, Commit, CloudQueue, InfoOutlined } from '@mui/icons-material';
import apiKubernetes1 from '@api1/kubernetes1';
import Loader from './Loader';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';
import WidgetCard from '@components1/common/WidgetCard';
import CopyableText from '@components1/common/CopyableText';
import { ExternalLinkIcon } from '@assets';
import SafeIcon from './SafeIcon';

interface TimelineMetadata {
  cloud_account_id?: string;
  namespace?: string;
  workload_name?: string;
}

interface TimelineItem {
  timestamp: string;
  ref_type: string;
  ref_id: string;
  action: string;
  summary: string;
  metadata?: TimelineMetadata;
}

interface TimelineResponse {
  event_id: string;
  timeline: TimelineItem[];
}

// Create formatter instance once to avoid overhead in loop
const dateTimeFormatter = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  hour: 'numeric',
  minute: 'numeric',
  hour12: true,
});

const formatDate = (isoString: string) => {
  if (!isoString) {
    return '';
  }
  const date = new Date(isoString);
  return dateTimeFormatter.format(date);
};

const getEventProps = (item: TimelineItem) => {
  const { ref_type, action } = item;
  if (ref_type === 'event' && action === 'fired') {
    return { icon: <ErrorOutline sx={{ fontSize: 16 }} />, color: 'error', label: 'Alert Fired' };
  }
  if (ref_type === 'event' && action === 'resolved') {
    return { icon: <CheckCircleOutline sx={{ fontSize: 16 }} />, color: 'success', label: 'Resolved' };
  }
  if (ref_type === 'event' && action === 'first_occurrence') {
    return { icon: <ErrorOutline sx={{ fontSize: 16 }} />, color: 'warning', label: 'First Occurrence' };
  }
  if (ref_type === 'event' && action === 'alert_fired') {
    return { icon: <ErrorOutline sx={{ fontSize: 16 }} />, color: 'error', label: 'Correlated Alert' };
  }
  if (ref_type === 'event_history') {
    return { icon: <InfoOutlined sx={{ fontSize: 16, color: colors.text.primary }} />, color: 'grey', label: 'Status Change' };
  }
  if (ref_type === 'config_change') {
    return { icon: <Commit sx={{ fontSize: 16 }} />, color: 'info', label: 'Config Change' };
  }
  if (ref_type === 'git_commit') {
    return { icon: <Commit sx={{ fontSize: 16 }} />, color: 'info', label: 'Commit' };
  }
  if (ref_type === 'workload') {
    return { icon: <CloudQueue sx={{ fontSize: 16 }} />, color: 'secondary', label: 'Workload' };
  }
  return { icon: <InfoOutlined sx={{ fontSize: 16, color: colors.text.primary }} />, color: 'grey', label: 'Event' };
};

// Get navigation URL based on item type, returns null if not navigable
const getNavigationUrl = (item: TimelineItem, currentEventId: string): string | null => {
  const { ref_type, ref_id, metadata } = item;

  // Don't navigate to self (current event)
  if (ref_id === currentEventId) {
    return null;
  }

  // Event history entries are not navigable (they're status changes on current event)
  if (ref_type === 'event_history') {
    return null;
  }

  // Workload entries should navigate to workload page
  if (ref_type === 'workload' && metadata?.cloud_account_id && metadata?.workload_name && metadata?.namespace) {
    return `/kubernetes/details/${metadata.cloud_account_id}?workloadName=${metadata.workload_name}&namespace=${metadata.namespace}#kubernetes/applications`;
  }

  // Events and config_changes navigate to investigate page
  if (ref_type === 'event' || ref_type === 'config_change') {
    return `/investigate?id=${ref_id}`;
  }

  return null;
};

// --- Main Component ---
const DevOpsTimelineMUI = ({ eventId }: { eventId: string }) => {
  const theme = useTheme();

  const [timelineData, setTimelineData] = useState<TimelineResponse | null>(null);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchTimelineData = async () => {
      setIsLoading(true);
      setError(null);
      try {
        const response: any = await apiKubernetes1.getTimelineData(eventId);
        setTimelineData(response?.data?.data?.event_get_timeline || {});
      } catch (err) {
        console.error(err);
        setError('Failed to load timeline data.');
      } finally {
        setIsLoading(false);
      }
    };

    if (eventId) {
      fetchTimelineData();
    }
  }, [eventId]);

  // Memoize sorted timeline to prevent re-sorting on every render
  const sortedTimeline = useMemo(() => {
    if (!timelineData?.timeline) {
      return [];
    }
    return [...timelineData.timeline].sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
  }, [timelineData]);

  if (isLoading) {
    return (
      <Box display='flex' justifyContent='center' p={5}>
        <Loader style={{ width: '100px', height: '300px' }} />
      </Box>
    );
  }

  if (error) {
    snackbar.error(error);
  }

  if (!timelineData?.timeline || timelineData.timeline.length === 0) {
    return (
      <WidgetCard sx={{ width: '100%' }}>
        <Typography>No timeline events found.</Typography>
      </WidgetCard>
    );
  }

  return (
    <Box sx={{ margin: 'var(--ds-space-2) var(--ds-space-5) 0px var(--ds-space-5)' }}>
      {/* Header */}
      <Box sx={{ mb: 3, pb: 2, borderBottom: 1, borderColor: 'divider', display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
        <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: colors.text.secondary }}>
          Event ID:
        </Typography>
        <CopyableText
          copyableText={timelineData.event_id}
          iconPosition='end'
          sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}
        >
          {timelineData.event_id}
        </CopyableText>
      </Box>

      {/* Timeline List */}
      <Box component='ul' sx={{ p: 0, m: 0, listStyle: 'none' }}>
        {sortedTimeline.map((item, index) => {
          const styles = getEventProps(item);
          const isLast = index === sortedTimeline.length - 1;
          const navigationUrl = getNavigationUrl(item, eventId);
          const isNavigable = navigationUrl !== null;

          // Dynamic colors
          // @ts-ignore
          const colorMain = styles.color === 'grey' ? theme.palette.grey[500] : theme.palette[styles.color].main;
          // @ts-ignore

          return (
            <Box component='li' key={index} sx={{ display: 'flex', gap: 2, minHeight: 60, pb: 0 }}>
              {/* Timestamp */}
              <Box display='flex' alignItems='flex-start' justifyContent='right' color='text.secondary' minWidth={140} pt={0.5}>
                <Typography variant='caption' sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-small)' }}>
                  {formatDate(item.timestamp)}
                </Typography>
              </Box>

              {/* ICON + Line */}
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', minWidth: 24 }}>
                <Avatar
                  sx={{
                    width: 28,
                    height: 28,
                    bgcolor: item.ref_type === 'event' && item.action === 'fired' ? colorMain : 'white',
                    boxShadow: '0px 4px 4px -1px rgba(229, 229, 229, 0.4), 0px 2px 4px 0px rgb(233, 233, 233)',
                    color: item.ref_type === 'event' && item.action === 'fired' ? 'white' : colorMain,
                    border: item.ref_type === 'event' && item.action === 'fired' ? 'none' : '1px solid #EBEBEB',
                    zIndex: 1,
                  }}
                >
                  {styles.icon}
                </Avatar>

                {!isLast && <Box sx={{ width: '2px', flexGrow: 1, bgcolor: 'grey.300', my: 0.5 }} />}
              </Box>

              {/* Content */}
              <Box
                onClick={() => {
                  if (isNavigable) {
                    window.open(navigationUrl, '_blank');
                  }
                }}
                sx={{
                  flexGrow: 1,
                  mb: 'var(--ds-space-4)',
                  borderRadius: 'var(--ds-radius-lg)',
                  padding: 'var(--ds-space-3) var(--ds-space-4)',
                  border: `1px solid ${colors.border.secondaryLight}`,
                  backgroundColor: colors.background.tertiaryLightestestest,
                  cursor: isNavigable ? 'pointer' : 'default',
                  transition: 'all 0.3s ease',
                  ...(isNavigable && {
                    '&:hover': {
                      boxShadow: '0px 4px 6px -1px rgba(229, 229, 229, 0.1), 0px 2px 6px 0px rgb(233, 233, 233)',
                      transform: 'translateY(-1px)',
                      '& .timeline-text': {
                        color: colors.text.primary,
                      },
                    },
                  }),
                }}
              >
                <Box display='flex' flexDirection='column'>
                  <Box display='flex' alignItems='center' justifyContent='space-between' marginBottom='4px'>
                    <Chip
                      label={styles.label}
                      size='small'
                      sx={{
                        height: 20,
                        fontSize: 'var(--ds-text-caption)',
                        fontFamily: 'poppins',
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        color: styles.color === 'grey' ? 'text.primary' : `${styles.color}.dark`,
                        bgcolor: 'white',
                        border: '1px solid',
                        borderColor: styles.color === 'grey' ? 'grey.300' : `${styles.color}.light`,
                        width: 'fit-content',
                      }}
                    />
                    {isNavigable && <SafeIcon src={ExternalLinkIcon} alt='redirect' width={16} height={16} />}
                  </Box>
                  <Typography
                    className='timeline-text'
                    sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)', color: colors.text.secondary }}
                  >
                    {item.summary}
                  </Typography>
                </Box>
              </Box>
            </Box>
          );
        })}
      </Box>
    </Box>
  );
};

export default DevOpsTimelineMUI;
